package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
)

// Colours are adaptive so the interface stays legible on a light terminal as
// well as a dark one. Nothing here is chosen for decoration: the accent marks
// what has focus, and green, red and amber each mean exactly one thing —
// succeeded, failed, and "this run is not the real thing".
var (
	cAccent = lipgloss.AdaptiveColor{Light: "#1d4ed8", Dark: "#7dd3fc"}
	cOK     = lipgloss.AdaptiveColor{Light: "#15803d", Dark: "#4ade80"}
	cFail   = lipgloss.AdaptiveColor{Light: "#b91c1c", Dark: "#f87171"}
	cWarn   = lipgloss.AdaptiveColor{Light: "#b45309", Dark: "#fbbf24"}
	cMuted  = lipgloss.AdaptiveColor{Light: "#6b7280", Dark: "#8b8b96"}
	cBorder = lipgloss.AdaptiveColor{Light: "#c9ccd1", Dark: "#3f3f46"}
	cText   = lipgloss.AdaptiveColor{Light: "#111827", Dark: "#e5e7eb"}
	cOn     = lipgloss.AdaptiveColor{Light: "#ffffff", Dark: "#0b1220"}
)

var (
	appStyle     = lipgloss.NewStyle().Bold(true).Foreground(cAccent)
	titleStyle   = lipgloss.NewStyle().Bold(true).Foreground(cText)
	dimStyle     = lipgloss.NewStyle().Foreground(cMuted)
	okStyle      = lipgloss.NewStyle().Foreground(cOK)
	failStyle    = lipgloss.NewStyle().Foreground(cFail)
	warnStyle    = lipgloss.NewStyle().Foreground(cWarn)
	cmdStyle     = lipgloss.NewStyle().Foreground(cText)
	spinnerStyle = lipgloss.NewStyle().Foreground(cAccent)

	// The selected row in the focused column is filled; in the unfocused column
	// it is only tinted, so at a glance it is obvious which column the arrow
	// keys will move.
	rowActive   = lipgloss.NewStyle().Background(cAccent).Foreground(cOn).Bold(true)
	rowInactive = lipgloss.NewStyle().Foreground(cAccent)
	rowNormal   = lipgloss.NewStyle().Foreground(cText)
)

// badge renders a small filled label, for states that must be readable without
// being read: CHECK, RUNNING, EXIT 2.
func badge(text string, bg lipgloss.TerminalColor) string {
	return lipgloss.NewStyle().Background(bg).Foreground(cOn).Bold(true).Padding(0, 1).Render(text)
}

// panel draws a titled, bordered box whose border takes the accent colour when
// the panel has focus.
func panel(title, body string, width, height int, focused bool) string {
	border, heading := cBorder, dimStyle
	if focused {
		border, heading = cAccent, lipgloss.NewStyle().Bold(true).Foreground(cAccent)
	}
	inner := lipgloss.NewStyle().Width(width).Height(height).Render(body)
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(border).
		Padding(0, 1).
		Render(heading.Render(title) + "\n" + inner)
}

// keyHints renders the footer legend with the keys emphasised, so the line
// reads as a set of controls rather than a sentence.
func keyHints(pairs ...string) string {
	var parts []string
	for i := 0; i+1 < len(pairs); i += 2 {
		parts = append(parts, lipgloss.NewStyle().Foreground(cAccent).Render(pairs[i])+
			dimStyle.Render(" "+pairs[i+1]))
	}
	return strings.Join(parts, dimStyle.Render("  ·  "))
}

// bar is the full-width bordered strip used as a header on every screen.
func (m *model) bar(left, right string) string {
	gap := m.width - lipgloss.Width(left) - lipgloss.Width(right) - 6
	if gap < 1 {
		gap = 1
	}
	width := m.width - 4
	if width < 20 {
		width = 20
	}
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(cBorder).
		Padding(0, 1).
		Width(width).
		Render(left + strings.Repeat(" ", gap) + right)
}

func (m *model) View() string {
	if !m.ready {
		return "starting…"
	}
	switch m.view {
	case viewRun:
		return m.runView()
	case viewOverview:
		return m.overviewView()
	}
	return m.pickerView()
}

func (m *model) pickerView() string {
	// Two columns splitting the usable width, minus each panel's border and
	// padding.
	colWidth := (m.width-4)/2 - 4
	if colWidth < 12 {
		colWidth = 12
	}
	colHeight := m.height - 15
	if colHeight < 3 {
		colHeight = 3
	}

	check := ""
	if m.check {
		check = badge("CHECK MODE", cWarn)
	}

	screen := lipgloss.JoinVertical(lipgloss.Left,
		m.bar(appStyle.Render("ansible-tui")+dimStyle.Render("  "+shorten(m.repo.Root, 56)), check),
		lipgloss.JoinHorizontal(lipgloss.Top,
			panel("INVENTORIES",
				rows(m.repo.Inventories, m.invIdx, m.focus == colInventories, colWidth, invName),
				colWidth, colHeight, m.focus == colInventories),
			panel("PLAYBOOKS",
				rows(m.repo.Playbooks, m.pbIdx, m.focus == colPlaybooks, colWidth, playbookName),
				colWidth, colHeight, m.focus == colPlaybooks),
		),
		m.commandPanel(),
		" "+m.pickerHints(),
	)

	if m.asking {
		return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, m.confirmDialog())
	}
	return screen
}

// commandPanel shows the exact command enter will run, and how that playbook
// went last time. Both matter more than anything else on screen, so they get a
// box rather than a status line.
func (m *model) commandPanel() string {
	width := m.width - 8
	if width < 20 {
		width = 20
	}

	body := dimStyle.Render("$ ") + cmdStyle.Render(m.commandLine())

	last := dimStyle.Render("last   never run from here")
	if r, ok := m.hist[m.selectedPlaybook()]; ok {
		mark := okStyle.Render("✓ ok")
		if !r.OK() {
			mark = failStyle.Render(fmt.Sprintf("✗ exit %d", r.Exit))
		}
		flag := ""
		if r.Check {
			flag = warnStyle.Render("  (check)")
		}
		last = dimStyle.Render("last   ") + mark + dimStyle.Render(fmt.Sprintf(
			"  ·  %s  ·  %s  ·  %s ago", invName(r.Inventory), dur(r.DurationMS), ago(r.When()))) + flag
	}

	if m.warn != "" {
		last += "\n" + warnStyle.Render("warn   "+m.warn)
	}

	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(cBorder).
		Padding(0, 1).
		Width(width).
		Render(body + "\n" + last)
}

func (m *model) pickerHints() string {
	if len(m.repo.Playbooks) == 0 {
		return warnStyle.Render("no playbooks here") + dimStyle.Render(" — looked in playbooks/*.yml and *.yml")
	}
	return keyHints(
		"tab", "switch",
		"↑↓", "move",
		"⏎", "run",
		"c", "check",
		"o", "overview",
		"q", "quit",
	)
}

// rows renders one column, marking the selection differently depending on
// whether that column has focus.
func rows(items []string, idx int, focused bool, width int, label func(string) string) string {
	if len(items) == 0 {
		return dimStyle.Render("(none)")
	}
	out := make([]string, 0, len(items))
	for i, it := range items {
		text := label(it)
		if width > 6 && len(text) > width-3 {
			text = text[:width-4] + "…"
		}
		switch {
		case i == idx && focused:
			out = append(out, rowActive.Width(width).Render("▸ "+text))
		case i == idx:
			out = append(out, rowInactive.Render("▸ "+text))
		default:
			out = append(out, rowNormal.Render("  "+text))
		}
	}
	return strings.Join(out, "\n")
}

func (m *model) runView() string {
	head := titleStyle.Render(playbookName(m.selectedPlaybook())) +
		dimStyle.Render("  →  ") + titleStyle.Render(invName(m.selectedInventory()))

	var status string
	switch {
	case m.live:
		head = m.spin.View() + " " + head
		status = badge("RUNNING", cAccent) + dimStyle.Render("  "+
			time.Since(m.started).Truncate(time.Second).String())
		if m.check {
			status = badge("CHECK", cWarn) + " " + status
		}
	case m.done.TS == "":
		status = dimStyle.Render("log")
	case m.done.OK():
		status = badge("OK", cOK) + dimStyle.Render("  "+dur(m.done.DurationMS))
	default:
		status = badge(fmt.Sprintf("EXIT %d", m.done.Exit), cFail) +
			dimStyle.Render("  "+dur(m.done.DurationMS))
	}

	hints := keyHints("esc", "back", "↑↓", "scroll", "q", "quit")
	if m.live {
		hints = keyHints("ctrl+c", "interrupt", "↑↓", "scroll")
	}

	return lipgloss.JoinVertical(lipgloss.Left, m.bar(head, status), m.outputBox(), " "+hints)
}

func (m *model) overviewView() string {
	return lipgloss.JoinVertical(lipgloss.Left,
		m.bar(titleStyle.Render("overview")+dimStyle.Render("  "+invName(m.selectedInventory())),
			dimStyle.Render(fmt.Sprintf("%d runs recorded", len(m.runs)))),
		m.outputBox(),
		" "+keyHints("↑↓", "select", "⏎", "open log", "esc", "back"),
	)
}

func (m *model) outputBox() string {
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(cBorder).
		Padding(0, 1).
		Render(m.vp.View())
}

// confirmDialog is deliberately loud. It is the last thing between a keystroke
// and a production run.
func (m *model) confirmDialog() string {
	body := lipgloss.JoinVertical(lipgloss.Center,
		badge(" PRODUCTION ", cFail),
		"",
		titleStyle.Render(playbookName(m.selectedPlaybook()))+
			dimStyle.Render("  →  ")+failStyle.Render(invName(m.selectedInventory())),
		"",
		dimStyle.Render("type ")+titleStyle.Render("prod")+dimStyle.Render(" and press enter"),
		"",
		lipgloss.NewStyle().Border(lipgloss.NormalBorder()).BorderForeground(cFail).
			Padding(0, 2).Render(m.confirm+lipgloss.NewStyle().Foreground(cAccent).Render("▏")),
		"",
		dimStyle.Render("esc to cancel"),
	)

	return lipgloss.NewStyle().
		Border(lipgloss.DoubleBorder()).
		BorderForeground(cFail).
		Padding(1, 4).
		Render(body)
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

// renderOverview fills the viewport with the host tree and the run history.
func (m *model) renderOverview() {
	var b strings.Builder

	b.WriteString(titleStyle.Render("HOSTS") + dimStyle.Render("  "+invName(m.selectedInventory())) + "\n")
	b.WriteString(hostTree(m.repo.Root, m.selectedInventory()))

	b.WriteString("\n" + titleStyle.Render("RECENT RUNS") + "\n")
	if len(m.runs) == 0 {
		b.WriteString(dimStyle.Render("  nothing recorded yet — runs started outside this program are not tracked") + "\n")
	}
	for i := len(m.runs) - 1; i >= 0; i-- {
		r := m.runs[i]
		mark := okStyle.Render("✓")
		if !r.OK() {
			mark = failStyle.Render("✗")
		}
		flags := ""
		if r.Check {
			flags = warnStyle.Render(" check")
		}
		line := fmt.Sprintf("  %s  %-26s %-12s %8s  %8s ago%s",
			mark, playbookName(r.Playbook), invName(r.Inventory), dur(r.DurationMS), ago(r.When()), flags)
		if len(m.runs)-1-i == m.histIdx {
			line = rowActive.Render(line)
		}
		b.WriteString(line + "\n")
	}

	m.body.Reset()
	m.body.WriteString(b.String())
	m.vp.SetContent(m.body.String())
	m.vp.GotoTop()
}

// hostTree asks ansible for the inventory rather than parsing YAML here.
// Failure is common with vault-locked or malformed inventories, so the error is
// shown in place of the tree instead of being swallowed.
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
		return warnStyle.Render("  ansible-inventory failed:") + "\n" + dimStyle.Render(indent(string(out)))
	}
	groups := parseHosts(out)
	if len(groups) == 0 {
		return dimStyle.Render("  (no hosts)") + "\n"
	}
	var b strings.Builder
	for _, g := range groups {
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

func playbookName(p string) string {
	if p == "" {
		return "(none)"
	}
	return strings.TrimSuffix(filepath.Base(p), filepath.Ext(p))
}

func invName(inv string) string {
	if inv == "" {
		return "default"
	}
	return filepath.Base(inv)
}

// shorten keeps a long path readable by dropping the front rather than the
// end: the last segments identify the repository, the first ones rarely do.
func shorten(path string, max int) string {
	if len(path) <= max {
		return path
	}
	return "…" + path[len(path)-max+1:]
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
