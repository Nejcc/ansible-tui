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
)

const (
	colInventories = iota
	colPlaybooks
)

type model struct {
	repo Repo

	view   view
	focus  int
	invIdx int
	pbIdx  int
	check  bool
	warn   string

	// asking guards a production run; confirm holds what has been typed so far.
	asking  bool
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
		report := func(title string, items []string) {
			fmt.Printf("%s (%d)\n", title, len(items))
			for _, it := range items {
				fmt.Println("  " + it)
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
		repo: Discover(root),
		msgs: make(chan any, 64),
		spin: s,
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
	case "tab", "left", "right", "h", "l":
		m.focus = 1 - m.focus
	case "up", "k":
		m.move(-1)
	case "down", "j":
		m.move(1)
	case "c":
		m.check = !m.check
	case "o":
		m.histIdx = 0
		m.renderOverview()
		m.view = viewOverview
	case "enter":
		return m.launch()
	}
	return m, nil
}

func (m *model) confirmKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
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
	if m.focus == colInventories {
		m.invIdx = clamp(m.invIdx+delta, len(m.repo.Inventories))
		return
	}
	m.pbIdx = clamp(m.pbIdx+delta, len(m.repo.Playbooks))
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

func (m *model) selectedInventory() string {
	if len(m.repo.Inventories) == 0 {
		return ""
	}
	return m.repo.Inventories[m.invIdx]
}

func (m *model) selectedPlaybook() string {
	if len(m.repo.Playbooks) == 0 {
		return ""
	}
	return m.repo.Playbooks[m.pbIdx]
}

// launch gates production behind a typed confirmation before anything starts.
func (m *model) launch() (tea.Model, tea.Cmd) {
	if m.selectedPlaybook() == "" {
		return m, nil
	}
	if needsConfirm(m.selectedInventory()) {
		m.asking, m.confirm = true, ""
		return m, nil
	}
	return m.start()
}

func (m *model) start() (tea.Model, tea.Cmd) {
	r, err := Start(m.repo.Root, m.selectedInventory(), m.selectedPlaybook(), m.check, m.msgs)
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
