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

	// Group headings sit between rows, so they are dimmed and unindented: a
	// label the eye can skip over, never something the arrow keys can land on.
	groupStyle = lipgloss.NewStyle().Foreground(cMuted).Bold(true)
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

// count suffixes a panel title with how many items it holds. The column only
// shows as many rows as it has height for, so the total is the one hint that a
// list continues past what is on screen.
func count(title string, n int) string {
	if n == 0 {
		return title
	}
	return fmt.Sprintf("%s %d", title, n)
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
	case viewPreview:
		return m.previewView()
	}
	return m.pickerView()
}

// pickerView walks one step at a time: where, then what, then both together.
// One full-width list per step, which is also the only way the descriptions fit
// beside the names.
func (m *model) pickerView() string {
	if m.asking {
		return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, m.confirmDialog())
	}

	width := m.width - 8
	if width < 20 {
		width = 20
	}
	height := m.height - 11
	if height < 3 {
		height = 3
	}

	// The panel is given the height of what it actually holds — window() has
	// already cut the list down to what fits, so measuring afterwards keeps a
	// four-entry list from being drawn as a mostly empty box.
	var body string
	switch m.step {
	case stepInventory:
		list := rows(m.repo.Inventories, m.invIdx, true, width, height, invName)
		body = panel(count("INVENTORIES", len(m.repo.Inventories)), list,
			width, lipgloss.Height(list), true)
	case stepPlaybook:
		// Split: the list on the left, everything the selected playbook says
		// about itself on the right. The meta is what decides which one you
		// want, so it belongs next to the choosing, not only on the review.
		metaWidth := width/3 - 4
		if metaWidth < 18 {
			metaWidth = 18
		}
		listWidth := width - metaWidth - 8

		pbs := m.playbooks()
		list := rows(pbs, m.pbIdx, true, listWidth, height, playbookName)
		if hidden := len(m.repo.Playbooks) - len(pbs); hidden > 0 {
			list += "\n" + dimStyle.Render(fmt.Sprintf(
				"%d not offered here — they name other inventories", hidden))
		}
		meta := m.metaBody(metaWidth)

		h := max(lipgloss.Height(list), lipgloss.Height(meta))
		body = lipgloss.JoinHorizontal(lipgloss.Top,
			panel(count("PLAYBOOKS", len(pbs)), list, listWidth, h, true),
			panel("THIS PLAYBOOK", meta, metaWidth, h, false),
		)
	case stepHosts:
		targets := m.hostRows()
		marked := make([]Item, len(targets))
		for i, it := range targets {
			marked[i] = it
			// The default row is ticked exactly when nothing else is.
			on := m.limit[it.Path]
			if it.Path == "" {
				on = len(m.limit) == 0
			}
			marked[i].Mark = "[ ] "
			if on {
				marked[i].Mark = "[x] "
			}
		}
		list := rows(marked, m.hostIdx, true, width, height, hostLabel)
		body = panel("RUN ON", list, width, lipgloss.Height(list), true)

	default:
		body = m.reviewPanel(width)
	}

	return lipgloss.JoinVertical(lipgloss.Left,
		m.bar(appStyle.Render("ansible-tui")+dimStyle.Render("  "+shorten(m.repo.Root, 40)), m.badges()),
		" "+m.breadcrumb(),
		body,
		" "+m.pickerHints(),
	)
}

// hostLabel names the default row, which has no path of its own to be named by.
func hostLabel(path string) string {
	if path == "" {
		return "every host"
	}
	return path
}

// badges are the two facts that change what a run means, kept in the top bar
// where they are visible at every step rather than only at the one that set
// them.
func (m *model) badges() string {
	var out []string
	if needsConfirm(m.selectedInventory().Path) && m.step > stepInventory {
		out = append(out, badge("PRODUCTION", cFail))
	}
	switch {
	case m.check && needsConfirm(m.selectedInventory().Path) && m.step > stepInventory:
		out = append(out, badge("DRY RUN — LOCKED", cWarn))
	case m.check:
		out = append(out, badge("DRY RUN", cWarn))
	}
	return strings.Join(out, " ")
}

// breadcrumb shows the three steps with the choices already made, so the review
// step is never the first sight of which inventory is selected.
func (m *model) breadcrumb() string {
	steps := []string{"inventory", "playbook", "hosts", "review"}
	if m.step > stepInventory {
		steps[stepInventory] = invName(m.selectedInventory().Path)
	}
	if m.step > stepPlaybook {
		steps[stepPlaybook] = playbookName(m.selectedPlaybook().Path)
	}
	if m.step > stepHosts {
		if picked := m.limits(); len(picked) > 0 {
			steps[stepHosts] = trunc(strings.Join(picked, ","), 28)
		} else {
			steps[stepHosts] = "all hosts"
		}
	}

	var parts []string
	for i, s := range steps {
		style := dimStyle
		switch {
		case i == m.step:
			style = lipgloss.NewStyle().Bold(true).Foreground(cAccent)
		case i < m.step && i == stepInventory && needsConfirm(m.selectedInventory().Path):
			style = lipgloss.NewStyle().Bold(true).Foreground(cFail)
		case i < m.step:
			style = titleStyle
		}
		parts = append(parts, dimStyle.Render(fmt.Sprintf("%d ", i+1))+style.Render(s))
	}
	return strings.Join(parts, dimStyle.Render("  ›  "))
}

// metaBody is everything the selected playbook declares, as a narrow column
// beside the list. Text wraps rather than truncating: this pane exists to be
// read, and a description cut off mid-word would send the reader to the review
// step for the rest of it.
func (m *model) metaBody(width int) string {
	pb := m.selectedPlaybook()
	if pb.Path == "" {
		return dimStyle.Render("(nothing selected)")
	}
	wrap := lipgloss.NewStyle().Width(width)

	var b []string
	add := func(label, value string, style lipgloss.Style) {
		if value == "" {
			return
		}
		b = append(b, dimStyle.Render(label), wrap.Inherit(style).Render(value), "")
	}

	b = append(b, titleStyle.Render(trunc(playbookName(pb.Path), width)), "")
	if pb.Safe {
		b = append(b, okStyle.Render("read-only"), "")
	}
	add("what it does", pb.Desc, lipgloss.NewStyle().Foreground(cText))
	add("group", groupName(pb.Group), dimStyle)
	add("targets", pb.Pattern, dimStyle)
	add("file", pb.Path, dimStyle)

	if len(pb.Envs) > 0 {
		add("only on", strings.Join(pb.Envs, ", "), warnStyle)
	} else {
		add("only on", "any inventory", dimStyle)
	}
	if len(pb.Vars) > 0 {
		add("needs -e", strings.Join(pb.Vars, "  "), warnStyle)
	}
	add("usage", pb.Usage, dimStyle)

	last := "never run from here"
	if r, ok := m.hist[pb.Path]; ok {
		mark := "ok"
		if !r.OK() {
			mark = fmt.Sprintf("exit %d", r.Exit)
		}
		last = fmt.Sprintf("%s · %s · %s ago", mark, invName(r.Inventory), ago(r.When()))
	}
	add("last run", last, dimStyle)

	return strings.TrimRight(strings.Join(b, "\n"), "\n")
}

// reviewPanel is the last thing seen before a run: what, where, the exact
// command, and how it went last time — one screen, nothing to scroll past.
func (m *model) reviewPanel(width int) string {
	inv, pb := m.selectedInventory(), m.selectedPlaybook()

	body := []string{
		describes("on", invName(inv.Path), inv.Desc, width),
		describes("run", playbookName(pb.Path), pb.Desc, width),
	}

	limit := "every host this playbook targets"
	style := dimStyle
	if picked := m.limits(); len(picked) > 0 {
		limit, style = "only "+strings.Join(picked, ", "), warnStyle
	}
	body = append(body, dimStyle.Render("hosts  ")+style.Render(trunc(limit, width-9)))

	if len(pb.Vars) > 0 {
		body = append(body, dimStyle.Render("needs  ")+
			warnStyle.Render(strings.Join(pb.Vars, "  "))+
			dimStyle.Render("   — pass with -e, or set them in host_vars"))
	}
	if pb.Usage != "" {
		body = append(body, dimStyle.Render("usage  ")+dimStyle.Render(trunc(pb.Usage, width-9)))
	}

	body = append(body,
		"",
		dimStyle.Render("$ ")+cmdStyle.Render(m.commandLine()),
		m.lastRun(),
	)

	switch {
	case needsConfirm(inv.Path):
		body = append(body, "", failStyle.Bold(true).Render(
			"production — dry-run only, and enter still asks before it starts"))
	case pb.Safe:
		body = append(body, "", okStyle.Render("read-only — this playbook declares that it changes nothing"))
	case !m.check:
		body = append(body, "", warnStyle.Render("dry-run is off — this run will change things"))
	}
	if m.warn != "" {
		body = append(body, warnStyle.Render("warn   "+m.warn))
	}

	return panel("REVIEW", strings.Join(body, "\n"), width, len(body), true)
}

// lastRun is the one line of history that matters here: how this playbook went
// the last time it was run from this program.
func (m *model) lastRun() string {
	r, ok := m.hist[m.selectedPlaybook().Path]
	if !ok {
		return dimStyle.Render("last   never run from here")
	}
	mark := okStyle.Render("✓ ok")
	if !r.OK() {
		mark = failStyle.Render(fmt.Sprintf("✗ exit %d", r.Exit))
	}
	flag := ""
	if r.Check {
		flag = warnStyle.Render("  (check)")
	}
	return dimStyle.Render("last   ") + mark + dimStyle.Render(fmt.Sprintf(
		"  ·  %s  ·  %s  ·  %s ago", invName(r.Inventory), dur(r.DurationMS), ago(r.When()))) + flag
}

// describes renders one "label  name — what it is" line, saying so plainly when
// the file offers no description rather than leaving a blank the eye reads as a
// rendering fault.
func describes(label, name, desc string, width int) string {
	if desc == "" {
		desc = "(no description — add one as a comment at the top of the file)"
	}
	head := dimStyle.Render(fmt.Sprintf("%-6s", label)) + titleStyle.Render(name)
	room := width - lipgloss.Width(head) - 5
	if room < 8 {
		return head
	}
	return head + dimStyle.Render("  —  "+trunc(desc, room))
}

func (m *model) pickerHints() string {
	if len(m.repo.Playbooks) == 0 {
		return warnStyle.Render("no playbooks here") + dimStyle.Render(" — looked in playbooks/*.yml and *.yml")
	}
	if m.step == stepPlaybook && len(m.playbooks()) == 0 {
		return warnStyle.Render("no playbook names this inventory") +
			dimStyle.Render("  ·  esc back")
	}
	switch m.step {
	case stepInventory:
		return keyHints("↑↓", "move", "⏎", "choose", "o", "overview", "q", "quit")
	case stepPlaybook:
		return keyHints("↑↓", "move", "⏎", "choose", "esc", "back", "q", "quit")
	case stepHosts:
		return keyHints("space", "pick / unpick", "a", "every host", "⏎", "continue", "esc", "back")
	}
	return keyHints("⏎", "run", "p", "preview", "c", "check", "esc", "back", "o", "overview", "q", "quit")
}

// descGap is how much room a description needs before it earns a place beside
// the name. Below that it is a truncated fragment, which is worse than nothing:
// the full text is in the command panel either way.
const descGap = 14

// rows renders one column, marking the selection differently depending on
// whether that column has focus. A heading is emitted wherever the group
// changes, and each name carries as much of its description as fits.
func rows(items []Item, idx int, focused bool, width, height int, label func(string) string) string {
	if len(items) == 0 {
		return dimStyle.Render("(none)")
	}
	out := make([]string, 0, len(items))
	cursor := 0
	grouped := anyGrouped(items)
	// Not "": that is a real group name here, meaning ungrouped.
	prev := "\x00"

	// One description column for the whole list, so the names stay in a block
	// rather than each description starting wherever its own name ended.
	names := make([]string, len(items))
	widest := 0
	for i, it := range items {
		names[i] = label(it.Path)
		if n := lipgloss.Width(it.Mark + names[i]); n > widest {
			widest = n
		}
	}
	descAt, room := widest+2, width-widest-4

	for i, it := range items {
		if grouped && it.Group != prev {
			out = append(out, groupStyle.Render(groupName(it.Group)))
			prev = it.Group
		}

		text := it.Desc
		if it.Safe {
			text = "read-only  " + text
		}
		name, desc := it.Mark+names[i], ""
		if text != "" && room >= descGap {
			name += strings.Repeat(" ", descAt-lipgloss.Width(name))
			desc = trunc(text, room)
		} else {
			name = trunc(name, width-2)
		}

		if i == idx {
			cursor = len(out)
		}
		switch {
		// The selected row is a filled block, so its description is not dimmed
		// separately: a second colour inside the fill breaks the background.
		case i == idx && focused:
			out = append(out, rowActive.Width(width).Render("▸ "+name+desc))
		case i == idx:
			out = append(out, rowInactive.Render("▸ "+name)+dimStyle.Render(desc))
		default:
			out = append(out, rowNormal.Render("  "+name)+dimStyle.Render(desc))
		}
	}
	return strings.Join(window(out, cursor, height), "\n")
}

// trunc shortens text to max columns, marking that something was cut. Measured
// in runes, so an em dash in a description does not push the column out.
func trunc(text string, max int) string {
	r := []rune(text)
	if max < 4 || len(r) <= max {
		return text
	}
	return string(r[:max-1]) + "…"
}

func groupName(g string) string {
	if g == "" {
		return "other"
	}
	return g
}

// window scrolls a list that is taller than the panel, keeping the cursor near
// the middle. The panel only ever grows to fit, so without this a long list
// would push the command panel and the hints off the bottom of the screen.
func window(lines []string, cursor, height int) []string {
	if height <= 0 || len(lines) <= height {
		return lines
	}
	start := cursor - height/2
	if start > len(lines)-height {
		start = len(lines) - height
	}
	if start < 0 {
		start = 0
	}
	return lines[start : start+height]
}

func (m *model) runView() string {
	head := titleStyle.Render(playbookName(m.selectedPlaybook().Path)) +
		dimStyle.Render("  →  ") + titleStyle.Render(invName(m.selectedInventory().Path))

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
		m.bar(titleStyle.Render("overview")+dimStyle.Render("  "+invName(m.selectedInventory().Path)),
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
// confirmDialog is the last gate. Both grades of it look the same and say the
// same thing — production, this playbook, this inventory — and differ only in
// what they cost to get past.
func (m *model) confirmDialog() string {
	lines := []string{
		badge(" PRODUCTION ", cFail),
		"",
		titleStyle.Render(playbookName(m.selectedPlaybook().Path)) +
			dimStyle.Render("  →  ") + failStyle.Render(invName(m.selectedInventory().Path)),
		"",
	}

	if m.typing {
		lines = append(lines,
			dimStyle.Render("type ")+titleStyle.Render("prod")+dimStyle.Render(" and press enter"),
			"",
			lipgloss.NewStyle().Border(lipgloss.NormalBorder()).BorderForeground(cFail).
				Padding(0, 2).Render(m.confirm+lipgloss.NewStyle().Foreground(cAccent).Render("▏")),
		)
	} else {
		lines = append(lines,
			okStyle.Render("this playbook declares itself read-only"),
			"",
			dimStyle.Render("press ")+titleStyle.Render("enter")+dimStyle.Render(" again to run it"),
		)
	}

	body := lipgloss.JoinVertical(lipgloss.Center, append(lines,
		"",
		dimStyle.Render("esc to cancel"),
	)...)

	return lipgloss.NewStyle().
		Border(lipgloss.DoubleBorder()).
		BorderForeground(cFail).
		Padding(1, 4).
		Render(body)
}

// renderPreview fills the viewport with what the run would consist of: the
// hosts the inventory resolves to for this playbook, and the tasks in the order
// they would run. Both come from ansible itself, and neither opens a connection
// or changes anything — they are the answer to "what is this going to do?"
// asked before rather than after.
func (m *model) renderPreview() {
	inv, pb := m.selectedInventory().Path, m.selectedPlaybook().Path

	var b strings.Builder
	b.WriteString(titleStyle.Render("HOSTS IT WOULD TOUCH") + "\n")
	b.WriteString(playbookPreview(m.repo.Root, inv, pb, "--list-hosts"))
	b.WriteString("\n" + titleStyle.Render("TASKS IT WOULD RUN") + "\n")
	b.WriteString(playbookPreview(m.repo.Root, inv, pb, "--list-tasks"))

	if vars := m.selectedPlaybook().Vars; len(vars) > 0 {
		b.WriteString("\n" + warnStyle.Render("this playbook declares it needs -e "+strings.Join(vars, " ")+
			" — a preview without them may fail or resolve differently") + "\n")
	}

	m.vp.SetContent(b.String())
	m.vp.GotoTop()
}

// playbookPreview asks ansible to list rather than run. A failure here is
// information, not an error: a playbook whose hosts expression needs an extra
// var says so in its own words, which is more useful than a summary of it.
func playbookPreview(root, inventory, playbook, mode string) string {
	var args []string
	if inventory != "" {
		args = append(args, "-i", inventory)
	}
	args = append(args, playbook, mode)

	c := exec.Command("ansible-playbook", args...)
	c.Dir = root
	c.Env = childEnv(os.Environ())

	out, err := c.CombinedOutput()
	if err != nil {
		return warnStyle.Render("  "+mode+" failed:") + "\n" + dimStyle.Render(indent(string(out)))
	}
	if len(strings.TrimSpace(string(out))) == 0 {
		return dimStyle.Render("  (nothing)") + "\n"
	}
	return indent(string(out))
}

func (m *model) previewView() string {
	return lipgloss.JoinVertical(lipgloss.Left,
		m.bar(titleStyle.Render("preview")+dimStyle.Render("  "+
			playbookName(m.selectedPlaybook().Path)+"  →  "+invName(m.selectedInventory().Path)),
			dimStyle.Render("nothing has run")),
		m.outputBox(),
		" "+keyHints("↑↓", "scroll", "esc", "back"),
	)
}

func (m *model) commandLine() string {
	parts := []string{"ansible-playbook"}
	if inv := m.selectedInventory().Path; inv != "" {
		parts = append(parts, "-i", inv)
	}
	if m.check {
		parts = append(parts, "--check")
	}
	if picked := m.limits(); len(picked) > 0 {
		parts = append(parts, "--limit", strings.Join(picked, ","))
	}
	return strings.Join(append(parts, m.selectedPlaybook().Path), " ")
}

// renderOverview fills the viewport with the host tree and the run history.
func (m *model) renderOverview() {
	var b strings.Builder

	b.WriteString(titleStyle.Render("HOSTS") + dimStyle.Render("  "+invName(m.selectedInventory().Path)) + "\n")
	b.WriteString(hostTree(m.repo.Root, m.selectedInventory().Path))

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
// listTargets asks ansible what this inventory resolves to. A failure comes
// back empty rather than as an error: the picker still works with nothing
// selected, which is every host, which is what a run does anyway.
func listTargets(root, inventory string) []Item {
	args := []string{"--list"}
	if inventory != "" {
		args = append([]string{"-i", inventory}, args...)
	}
	c := exec.Command("ansible-inventory", args...)
	c.Dir = root
	c.Env = childEnv(os.Environ())

	out, err := c.CombinedOutput()
	if err != nil {
		return nil
	}
	return parseTargets(out)
}

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
