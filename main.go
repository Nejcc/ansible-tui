package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
)

type view int

const (
	viewPick view = iota
	viewRun
	viewOverview
	viewPreview
)

// The picker is a sequence, not a form: choose where, then choose what, then
// look at both together before anything runs. A production mistake is made by
// running the right playbook against the wrong inventory, and two columns and a
// tab key put that mistake one keystroke away.
const (
	stepInventory = iota
	stepPlaybook
	stepHosts
	stepReview
)

type model struct {
	repo Repo

	view   view
	step   int
	invIdx int
	pbIdx  int
	check  bool
	warn   string

	// limit is what the run is narrowed to. Empty is the normal case and means
	// every host the playbook's own `hosts:` resolves to — the same as omitting
	// --limit. targets caches one `ansible-inventory --list` per inventory,
	// because it is a subprocess and the picker redraws on every keystroke.
	hostIdx int
	limit   map[string]bool
	targets map[string][]Item

	// asking guards a production run; confirm holds what has been typed so far.
	// typing distinguishes the two grades of guard: a playbook that changes
	// things has to have the word written out, one that declares itself
	// read-only only has to be acknowledged.
	asking  bool
	typing  bool
	confirm string

	vp      viewport.Model
	spin    spinner.Model
	body    strings.Builder
	msgs    chan any
	runner  *Runner
	started time.Time
	live    bool
	done    Run

	runs    []Run
	hist    map[string]Run
	histIdx int

	width, height int
	ready         bool
}

func main() {
	root, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(os.Stderr, "ansible-tui:", err)
		os.Exit(1)
	}

	var listOnly bool
	for _, arg := range os.Args[1:] {
		switch arg {
		case "-l", "--list":
			listOnly = true
		case "-h", "--help":
			fmt.Println("usage: ansible-tui [--list] [directory]\n\n" +
				"  --list  print the discovered inventories and playbooks, then exit\n" +
				"  directory defaults to the working directory")
			return
		default:
			root = arg
		}
	}
	if abs, err := filepath.Abs(root); err == nil {
		root = abs
	}

	if listOnly {
		repo := Discover(root)
		fmt.Println(root)
		// Ungrouped items print flat; grouped ones get a heading wherever the
		// group changes, the list already being ordered by it.
		report := func(title string, items []Item) {
			fmt.Printf("%s (%d)\n", title, len(items))
			grouped := anyGrouped(items)
			indent, prev := "  ", "\x00"
			if grouped {
				indent = "    "
			}
			for _, it := range items {
				if grouped && it.Group != prev {
					prev = it.Group
					fmt.Println("  [" + groupName(prev) + "]")
				}
				line := indent + it.Path
				if it.Desc != "" {
					line += "  — " + it.Desc
				}
				fmt.Println(line)
			}
		}
		report("inventories", repo.Inventories)
		report("playbooks", repo.Playbooks)
		return
	}

	// Fail before drawing anything: a TUI reporting a missing binary is worse
	// than a one-line message.
	if _, err := exec.LookPath("ansible-playbook"); err != nil {
		fmt.Fprintln(os.Stderr, "ansible-tui: ansible-playbook not found on PATH")
		os.Exit(1)
	}
	ignoreState(root)

	if _, err := tea.NewProgram(newModel(root), tea.WithAltScreen()).Run(); err != nil {
		fmt.Fprintln(os.Stderr, "ansible-tui:", err)
		os.Exit(1)
	}
}

func newModel(root string) *model {
	s := spinner.New()
	s.Spinner = spinner.Dot
	s.Style = spinnerStyle

	m := &model{
		repo:    Discover(root),
		msgs:    make(chan any, 64),
		spin:    s,
		limit:   map[string]bool{},
		targets: map[string][]Item{},
		// Dry-run is the resting state. A real run is something you turn on,
		// deliberately, and only where it is allowed at all.
		check: true,
	}
	m.reloadHistory()
	return m
}

func (m *model) reloadHistory() {
	m.runs = LoadRuns(m.repo.Root)
	m.hist = LatestPerPlaybook(m.runs)
}

func (m *model) Init() tea.Cmd { return waitFor(m.msgs) }

// waitFor turns the runner's channel into a stream of bubbletea messages. Each
// message re-arms the wait, so output keeps flowing without polling.
func waitFor(ch chan any) tea.Cmd {
	return func() tea.Msg { return <-ch }
}

type tickMsg time.Time

// tick drives the elapsed-time display, separately from the spinner's own
// faster animation.
func tick() tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg { return tickMsg(t) })
}

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.resize(msg.Width, msg.Height)
		return m, nil

	case outputMsg:
		m.body.Write(msg)
		m.vp.SetContent(m.body.String())
		m.vp.GotoBottom()
		return m, waitFor(m.msgs)

	case doneMsg:
		m.live = false
		m.runner = nil
		m.done = msg.run
		m.reloadHistory()
		if msg.err != nil {
			m.warn = "history not recorded: " + msg.err.Error()
		}
		return m, waitFor(m.msgs)

	case spinner.TickMsg:
		if !m.live {
			return m, nil
		}
		var cmd tea.Cmd
		m.spin, cmd = m.spin.Update(msg)
		return m, cmd

	case tickMsg:
		if m.live {
			return m, tick()
		}
		return m, nil

	case tea.KeyMsg:
		return m.key(msg)
	}
	return m, nil
}

// resize recomputes the viewport, leaving room for the top bar, the output
// box's own border, and the footer.
func (m *model) resize(w, h int) {
	m.width, m.height = w, h

	vpWidth := w - 6
	if vpWidth < 20 {
		vpWidth = 20
	}
	vpHeight := h - 8
	if vpHeight < 3 {
		vpHeight = 3
	}

	if !m.ready {
		m.vp = viewport.New(vpWidth, vpHeight)
		m.ready = true
		return
	}
	m.vp.Width, m.vp.Height = vpWidth, vpHeight
}

func (m *model) key(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.asking {
		return m.confirmKey(msg)
	}

	switch m.view {
	case viewRun:
		switch msg.String() {
		case "ctrl+c":
			if m.live {
				m.runner.Cancel()
				return m, nil
			}
			return m, tea.Quit
		case "q":
			if !m.live {
				return m, tea.Quit
			}
			return m, nil
		case "esc":
			if m.live {
				return m, nil
			}
			m.view = viewPick
			return m, nil
		}
		var cmd tea.Cmd
		m.vp, cmd = m.vp.Update(msg)
		return m, cmd

	case viewPreview:
		switch msg.String() {
		case "ctrl+c", "q", "esc", "p":
			m.view = viewPick
			return m, nil
		}
		var cmd tea.Cmd
		m.vp, cmd = m.vp.Update(msg)
		return m, cmd

	case viewOverview:
		switch msg.String() {
		case "ctrl+c", "q", "esc":
			m.view = viewPick
			return m, nil
		case "up", "k":
			if m.histIdx > 0 {
				m.histIdx--
			}
			m.renderOverview()
			return m, nil
		case "down", "j":
			if m.histIdx < len(m.runs)-1 {
				m.histIdx++
			}
			m.renderOverview()
			return m, nil
		case "enter":
			if len(m.runs) > 0 {
				r := m.runs[len(m.runs)-1-m.histIdx]
				m.body.Reset()
				m.body.WriteString(readLog(m.repo.Root, r.Log))
				m.vp.SetContent(m.body.String())
				m.vp.GotoTop()
				m.done = r
				m.view = viewRun
			}
			return m, nil
		}
		var cmd tea.Cmd
		m.vp, cmd = m.vp.Update(msg)
		return m, cmd
	}

	switch msg.String() {
	case "ctrl+c", "q":
		return m, tea.Quit
	case "esc", "left", "h", "backspace":
		// Back one step, never out of the program: leaving by accident from
		// here would lose the selection for no good reason.
		if m.step > stepInventory {
			m.step--
		}
	case "up", "k":
		m.move(-1)
	case "down", "j":
		m.move(1)
	case " ":
		if m.step != stepHosts {
			break
		}
		rows := m.hostRows()
		if m.hostIdx >= len(rows) {
			break
		}
		// The default row is not a member of the selection, it is the absence
		// of one: choosing it clears whatever else was picked.
		switch name := rows[m.hostIdx].Path; {
		case name == "":
			m.limit = map[string]bool{}
		case m.limit[name]:
			delete(m.limit, name)
		default:
			m.limit[name] = true
		}
	case "a":
		// Back to the default in one key, from anywhere on the step.
		if m.step == stepHosts {
			m.limit = map[string]bool{}
		}
	case "c":
		// Production cannot leave dry-run. This is the one guard that does not
		// negotiate: the confirmation dialogs stop an accident from starting a
		// run, and this stops any run that does start from changing production.
		if m.check && needsConfirm(m.selectedInventory().Path) {
			m.warn = "production runs are dry-run only — --check cannot be turned off here"
			return m, nil
		}
		m.check = !m.check
		m.warn = ""
	case "o":
		m.histIdx = 0
		m.renderOverview()
		m.view = viewOverview
	case "p":
		// Only from the review: a preview of a playbook needs the inventory
		// chosen, and the earlier steps have not chosen one yet.
		if m.step == stepReview {
			m.renderPreview()
			m.view = viewPreview
		}
	case "enter", "right", "l":
		return m.advance()
	}
	return m, nil
}

// advance moves to the next step, and from the last one starts the run.
func (m *model) advance() (tea.Model, tea.Cmd) {
	switch m.step {
	case stepInventory:
		// A repository with no inventories is not stuck here: the run omits -i
		// and ansible.cfg supplies the default. Changing inventory can shorten
		// the playbook list, so the cursor is brought back into it.
		m.pbIdx = clamp(m.pbIdx, len(m.playbooks()))
		m.hostIdx, m.limit = 0, map[string]bool{}
		m.step = stepPlaybook
	case stepPlaybook:
		if m.selectedPlaybook().Path != "" {
			m.step = stepHosts
		}
	case stepHosts:
		m.step = stepReview
	default:
		return m.launch()
	}
	return m, nil
}

func (m *model) confirmKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Without typing, enter is the whole guard: nothing has been reached yet by
	// arrow keys alone, so one more deliberate press is the acknowledgement.
	if !m.typing {
		switch msg.String() {
		case "enter":
			m.asking = false
			return m.start()
		case "esc", "ctrl+c", "q":
			m.asking = false
		}
		return m, nil
	}

	switch msg.String() {
	case "esc", "ctrl+c":
		m.asking, m.confirm = false, ""
	case "backspace":
		if n := len(m.confirm); n > 0 {
			m.confirm = m.confirm[:n-1]
		}
	case "enter":
		if strings.EqualFold(m.confirm, "prod") {
			m.asking, m.confirm = false, ""
			return m.start()
		}
		m.confirm = ""
	default:
		if len(msg.String()) == 1 {
			m.confirm += msg.String()
		}
	}
	return m, nil
}

func (m *model) move(delta int) {
	switch m.step {
	case stepInventory:
		m.invIdx = clamp(m.invIdx+delta, len(m.repo.Inventories))
	case stepPlaybook:
		m.pbIdx = clamp(m.pbIdx+delta, len(m.playbooks()))
	case stepHosts:
		m.hostIdx = clamp(m.hostIdx+delta, len(m.hostRows()))
	}
}

// hostTargets are the groups and hosts this inventory resolves to, asked of
// ansible once per inventory and kept.
func (m *model) hostTargets() []Item {
	inv := m.selectedInventory().Path
	if got, ok := m.targets[inv]; ok {
		return got
	}
	out := listTargets(m.repo.Root, inv)
	m.targets[inv] = out
	return out
}

// hostRows is the list as shown: the default first, then what the inventory
// resolves to. The default is a row rather than a sentence because a list where
// nothing is ticked reads as a question not yet answered — this way the answer
// is visible, selected, and the thing the cursor starts on.
func (m *model) hostRows() []Item {
	return append([]Item{{
		Path:  "",
		Group: "default",
		Desc:  "wherever the playbook sends it — nothing narrowed",
	}}, m.hostTargets()...)
}

// limits returns what was picked, in the order it is shown, for --limit.
func (m *model) limits() []string {
	var out []string
	for _, it := range m.hostTargets() {
		if m.limit[it.Path] {
			out = append(out, it.Path)
		}
	}
	return out
}

func clamp(i, n int) int {
	switch {
	case n == 0, i < 0:
		return 0
	case i >= n:
		return n - 1
	}
	return i
}

func (m *model) selectedInventory() Item {
	if len(m.repo.Inventories) == 0 {
		return Item{}
	}
	return m.repo.Inventories[m.invIdx]
}

// playbooks are those that may run against the inventory now selected. A
// playbook that names its inventories is not offered against the others: the
// list is what enter can actually do, and an entry that would only ever fail
// does not belong in it.
func (m *model) playbooks() []Item {
	inv := m.selectedInventory().Path
	out := make([]Item, 0, len(m.repo.Playbooks))
	for _, it := range m.repo.Playbooks {
		if it.RunsOn(inv) {
			out = append(out, it)
		}
	}
	return out
}

func (m *model) selectedPlaybook() Item {
	pbs := m.playbooks()
	if len(pbs) == 0 || m.pbIdx >= len(pbs) {
		return Item{}
	}
	return pbs[m.pbIdx]
}

// launch gates production behind a typed confirmation before anything starts.
func (m *model) launch() (tea.Model, tea.Cmd) {
	if m.selectedPlaybook().Path == "" {
		return m, nil
	}
	// Production always asks. A read-only playbook asks for less — one
	// deliberate keypress rather than the word — because the cost of being
	// wrong is smaller, not zero: `tui-safe` is a claim the file makes about
	// itself, and a file can be wrong.
	if needsConfirm(m.selectedInventory().Path) {
		m.asking, m.confirm = true, ""
		m.typing = !m.selectedPlaybook().Safe
		return m, nil
	}
	return m.start()
}

func (m *model) start() (tea.Model, tea.Cmd) {
	r, err := Start(m.repo.Root, m.selectedInventory().Path, m.selectedPlaybook().Path, m.check, m.msgs)
	if err != nil {
		m.warn = "could not start: " + err.Error()
		return m, nil
	}

	m.runner, m.live = r, true
	m.started = time.Now()
	m.done = Run{}
	m.warn = ""
	m.body.Reset()
	m.vp.SetContent("")
	m.view = viewRun
	return m, tea.Batch(tick(), m.spin.Tick)
}
