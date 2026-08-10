package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
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

	view    view
	focus   int
	invIdx  int
	pbIdx   int
	check   bool
	warn    string
	confirm string // typed production confirmation, empty when not confirming
	asking  bool

	vp      viewport.Model
	body    strings.Builder
	msgs    chan any
	runner  *Runner
	started time.Time
	status  string
	live    bool

	hist    map[string]Run
	runs    []Run
	histIdx int

	width, height int
	ready         bool
}

var (
	titleStyle    = lipgloss.NewStyle().Bold(true)
	selectedStyle = lipgloss.NewStyle().Reverse(true)
	dimStyle      = lipgloss.NewStyle().Faint(true)
	okStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	failStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
	warnStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))
)

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

	m := newModel(root)
	if _, err := tea.NewProgram(m, tea.WithAltScreen()).Run(); err != nil {
		fmt.Fprintln(os.Stderr, "ansible-tui:", err)
		os.Exit(1)
	}
}

func newModel(root string) *model {
	m := &model{
		repo: Discover(root),
		msgs: make(chan any, 64),
	}
	m.reloadHistory()
	return m
}

func (m *model) reloadHistory() {
	m.runs = LoadRuns(m.repo.Root)
	m.hist = LatestPerPlaybook(m.runs)
}

func (m *model) Init() tea.Cmd { return waitFor(m.msgs) }

// waitFor turns the runner's channel into a stream of bubbletea messages.
func waitFor(ch chan any) tea.Cmd {
	return func() tea.Msg { return <-ch }
}

type tickMsg time.Time

func tick() tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg { return tickMsg(t) })
}

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		h := msg.Height - 4
		if h < 3 {
			h = 3
		}
		if !m.ready {
			m.vp = viewport.New(msg.Width, h)
			m.ready = true
		} else {
			m.vp.Width, m.vp.Height = msg.Width, h
		}
		return m, nil

	case outputMsg:
		m.body.Write(msg)
		m.vp.SetContent(m.body.String())
		m.vp.GotoBottom()
		return m, waitFor(m.msgs)

	case doneMsg:
		m.live = false
		m.runner = nil
		m.reloadHistory()
		verb := okStyle.Render("ok")
		if !msg.run.OK() {
			verb = failStyle.Render(fmt.Sprintf("failed (exit %d)", msg.run.Exit))
		}
		m.status = fmt.Sprintf("%s in %s — esc to go back", verb, dur(msg.run.DurationMS))
		if msg.err != nil {
			m.warn = "history not recorded: " + msg.err.Error()
		}
		return m, waitFor(m.msgs)

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
				m.status = "interrupting…"
				return m, nil
			}
			return m, tea.Quit
		case "esc", "q":
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
				m.status = fmt.Sprintf("%s on %s — %s", r.Playbook, invName(r.Inventory), r.TS)
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
	if n == 0 {
		return 0
	}
	if i < 0 {
		return 0
	}
	if i >= n {
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

// launch gates production behind a typed confirmation before starting.
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
	m.body.Reset()
	m.vp.SetContent("")
	m.status = ""
	m.view = viewRun
	return m, tick()
}

func (m *model) View() string {
	if !m.ready {
		return "loading…"
	}
	switch m.view {
	case viewRun, viewOverview:
		return m.header() + "\n" + m.vp.View() + "\n" + m.footer()
	}
	return m.picker()
}

func (m *model) header() string {
	if m.view == viewOverview {
		return titleStyle.Render("overview — " + m.repo.Root)
	}
	title := fmt.Sprintf("%s → %s", m.selectedPlaybook(), invName(m.selectedInventory()))
	if m.status != "" && !m.live {
		title = m.status
	} else if m.live {
		title += fmt.Sprintf("  %s", time.Since(m.started).Truncate(time.Second))
		if m.check {
			title += "  " + warnStyle.Render("[check]")
		}
	}
	return titleStyle.Render(title)
}

func (m *model) footer() string {
	if m.view == viewOverview {
		return dimStyle.Render("↑/↓ select · enter open log · esc back")
	}
	if m.live {
		return dimStyle.Render("ctrl+c interrupt · ↑/↓ scroll")
	}
	return dimStyle.Render("esc back · ↑/↓ scroll")
}

func (m *model) picker() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("ansible-tui") + dimStyle.Render("  "+m.repo.Root) + "\n\n")

	left := column("inventories", m.repo.Inventories, m.invIdx, m.focus == colInventories, invName)
	right := column("playbooks", m.repo.Playbooks, m.pbIdx, m.focus == colPlaybooks, func(s string) string {
		return strings.TrimSuffix(filepath.Base(s), filepath.Ext(s))
	})
	b.WriteString(lipgloss.JoinHorizontal(lipgloss.Top, left, "   ", right))
	b.WriteString("\n\n")

	if len(m.repo.Playbooks) == 0 {
		b.WriteString(warnStyle.Render("no playbooks found in "+m.repo.Root) +
			dimStyle.Render("\nlooked in playbooks/*.yml and *.yml — is this an ansible repository?\n"))
	} else {
		b.WriteString(dimStyle.Render("$ ") + m.commandLine() + "\n")
	}

	if last, ok := m.hist[m.selectedPlaybook()]; ok {
		mark := okStyle.Render("ok")
		if !last.OK() {
			mark = failStyle.Render(fmt.Sprintf("exit %d", last.Exit))
		}
		b.WriteString(dimStyle.Render("last: ") + fmt.Sprintf("%s on %s, %s, %s ago\n",
			mark, invName(last.Inventory), dur(last.DurationMS), ago(last.When())))
	} else {
		b.WriteString(dimStyle.Render("last: never run from here\n"))
	}

	if m.warn != "" {
		b.WriteString(warnStyle.Render(m.warn) + "\n")
	}

	if m.asking {
		b.WriteString("\n" + failStyle.Render("PRODUCTION — type prod to confirm: ") + m.confirm + "▏\n" +
			dimStyle.Render("esc to cancel\n"))
		return b.String()
	}

	check := dimStyle.Render("check-mode off")
	if m.check {
		check = warnStyle.Render("CHECK MODE")
	}
	b.WriteString("\n" + dimStyle.Render("tab switch · ↑/↓ move · enter run · c toggle · o overview · q quit") +
		"   " + check)
	return b.String()
}

func (m *model) commandLine() string {
	parts := []string{"ansible-playbook"}
	if inv := m.selectedInventory(); inv != "" {
		parts = append(parts, "-i", inv)
	}
	if m.check {
		parts = append(parts, "--check")
	}
	return strings.Join(append(parts, m.selectedPlaybook()), " ")
}

func column(title string, items []string, idx int, focused bool, label func(string) string) string {
	head := dimStyle.Render("  " + title)
	if focused {
		head = titleStyle.Render("▸ " + title)
	}
	rows := []string{head}
	if len(items) == 0 {
		rows = append(rows, dimStyle.Render("  (none)"))
	}
	for i, it := range items {
		line := "  " + label(it)
		if i == idx && focused {
			line = selectedStyle.Render(line)
		} else if i == idx {
			line = "▸ " + label(it)
		}
		rows = append(rows, line)
	}
	return lipgloss.NewStyle().Width(34).Render(strings.Join(rows, "\n"))
}

// renderOverview joins the inventory's host tree with the recorded history.
func (m *model) renderOverview() {
	var b strings.Builder

	inv := m.selectedInventory()
	b.WriteString(titleStyle.Render("hosts — "+invName(inv)) + "\n")
	b.WriteString(hostTree(m.repo.Root, inv))

	b.WriteString("\n" + titleStyle.Render("recent runs") + "\n")
	if len(m.runs) == 0 {
		b.WriteString(dimStyle.Render("  nothing recorded yet — runs started outside this program are not tracked\n"))
	}
	for i := len(m.runs) - 1; i >= 0; i-- {
		r := m.runs[i]
		mark := okStyle.Render("ok  ")
		if !r.OK() {
			mark = failStyle.Render(fmt.Sprintf("e%-3d", r.Exit))
		}
		flags := ""
		if r.Check {
			flags = warnStyle.Render(" [check]")
		}
		line := fmt.Sprintf("  %s %-34s %-10s %8s  %s%s",
			mark, r.Playbook, invName(r.Inventory), dur(r.DurationMS), ago(r.When()), flags)
		if len(m.runs)-1-i == m.histIdx {
			line = selectedStyle.Render(line)
		}
		b.WriteString(line + "\n")
	}

	m.body.Reset()
	m.body.WriteString(b.String())
	m.vp.SetContent(m.body.String())
	m.vp.GotoTop()
}

// hostTree asks ansible for the inventory rather than parsing YAML here.
// Failure is common (vault-locked or malformed inventories), so the error text
// is shown in place of the tree instead of being swallowed.
func hostTree(root, inventory string) string {
	args := []string{"--list"}
	if inventory != "" {
		args = append([]string{"-i", inventory}, args...)
	}
	c := exec.Command("ansible-inventory", args...)
	c.Dir = root
	c.Env = childEnv(os.Environ())

	out, err := c.CombinedOutput()
	if err != nil {
		return warnStyle.Render("  ansible-inventory failed:\n") + indent(string(out))
	}
	hosts := parseHosts(out)
	if len(hosts) == 0 {
		return dimStyle.Render("  (no hosts)\n")
	}
	var b strings.Builder
	for _, g := range hosts {
		b.WriteString("  " + g + "\n")
	}
	return b.String()
}

func indent(s string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	for i, l := range lines {
		lines[i] = "  " + l
	}
	return strings.Join(lines, "\n") + "\n"
}

func invName(inv string) string {
	if inv == "" {
		return "default"
	}
	return filepath.Base(inv)
}

func dur(ms int64) string {
	if ms <= 0 {
		return "-"
	}
	return (time.Duration(ms) * time.Millisecond).Truncate(time.Second).String()
}

func ago(t time.Time) string {
	if t.IsZero() {
		return "?"
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}
