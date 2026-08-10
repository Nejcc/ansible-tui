package main

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Item is one selectable entry — a playbook or an inventory — together with
// whatever the file already says about itself.
type Item struct {
	Path  string // relative to Repo.Root
	Group string // playbooks only; "" means ungrouped
	Desc  string // "" when the file says nothing

	// Declared by a playbook's header. All optional: a playbook that says
	// nothing is offered everywhere, assumed to change things, and reviewed
	// with no extra notes — which is what every playbook did before any of
	// this existed.
	Mark  string   // a selection marker rendered before the name, when one applies
	Envs  []string // inventories it may run against; empty means any
	Vars  []string // extra vars it cannot run without
	Safe  bool     // declares itself read-only
	Usage string   // an example invocation worth having on screen

	// Pattern is the first play's own `hosts:` line — what the playbook aims at
	// before any --limit narrows it further.
	Pattern string
}

// RunsOn reports whether this playbook may run against an inventory. A playbook
// that declares nothing runs anywhere, and an empty inventory (a repository
// with none, where -i is omitted) filters nothing: there is no name to match
// against, and hiding everything would be worse than offering too much.
func (it Item) RunsOn(inventory string) bool {
	if len(it.Envs) == 0 || inventory == "" {
		return true
	}
	base := strings.ToLower(filepath.Base(inventory))
	for _, env := range it.Envs {
		if env == base {
			return true
		}
	}
	return false
}

// Repo is what a directory looks like to this program: a set of inventories and
// a set of playbooks.
type Repo struct {
	Root        string
	Inventories []Item
	Playbooks   []Item
}

// Paths returns just the paths, in order.
func Paths(items []Item) []string {
	out := make([]string, len(items))
	for i, it := range items {
		out[i] = it.Path
	}
	return out
}

// Discover inspects root without parsing any YAML. Deciding whether a file is
// really a playbook by parsing it costs more than the mistake it prevents: a
// stray entry in the list wastes one keystroke.
func Discover(root string) Repo {
	r := Repo{Root: root}

	for _, inv := range findInventories(root) {
		r.Inventories = append(r.Inventories, Item{
			Path: inv,
			Desc: describeInventory(root, inv),
		})
	}

	skip := make(map[string]bool, len(r.Inventories))
	for _, inv := range r.Inventories {
		skip[inv.Path] = true
	}

	for _, pb := range findPlaybooks(root, skip) {
		it := describePlaybook(filepath.Join(root, pb))
		it.Path = pb
		r.Playbooks = append(r.Playbooks, it)
	}
	sortByGroup(r.Playbooks)
	return r
}

// A playbook declares itself in comments at the top of the file:
//
//	# tui: database              the group it belongs in
//	# tui-env: internal stage    the only inventories it may run against
//	# tui-vars: project domain   extra vars it cannot run without
//	# tui-safe: true             it changes nothing
//	# tui-usage: ansible-playbook -i inventories/internal playbooks/x.yml
//
// Comments rather than real keys, so ansible never sees them and a shared
// repository can carry them without taking on a dependency on this program.
// Every one is optional; a playbook that declares nothing behaves as it always
// did.

// head returns the first lines of a file, trimmed. Reading the head is the only
// thing done with a candidate file's contents: what is at the top is a header,
// and anything further down is the playbook's business, not this program's.
// Unreadable files come back empty — a description is never worth an error.
func head(path string) []string {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()

	buf := make([]byte, 4096)
	n, _ := f.Read(buf)

	lines := strings.Split(string(buf[:n]), "\n")
	for i := range lines {
		lines[i] = strings.TrimSpace(lines[i])
	}
	return lines
}

// comment reports whether a line is a comment, and what it says.
func comment(line string) (string, bool) {
	if !strings.HasPrefix(line, "#") {
		return "", false
	}
	// Tolerate any amount of hash and space in front: someone writing #tui: or
	// ##  tui: meant the same thing, and a header that silently fails to match
	// is worse than a lenient one.
	return strings.TrimSpace(strings.TrimLeft(line, "#")), true
}

// describePlaybook reads what a playbook declares about itself. The description
// is the first play's own name — every playbook already has one, so asking
// anybody to write a second one would be asking twice. Comments that are not
// one of the known keys are ordinary prose and ignored.
func describePlaybook(path string) Item {
	var it Item
	for _, line := range head(path) {
		text, isComment := comment(line)
		if !isComment {
			if rest, ok := strings.CutPrefix(line, "- name:"); ok && it.Desc == "" {
				it.Desc = unquote(strings.TrimSpace(rest))
			}
			// Both spellings: `- hosts:` when the play has no name, `  hosts:`
			// when it follows one. A multi-line expression yields its first
			// line, which is enough to say what is aimed at.
			for _, prefix := range []string{"- hosts:", "hosts:"} {
				if rest, ok := strings.CutPrefix(line, prefix); ok && it.Pattern == "" {
					it.Pattern = unquote(strings.TrimSpace(rest))
				}
			}
			continue
		}

		key, value, found := strings.Cut(text, ":")
		if !found {
			continue
		}
		value = strings.TrimSpace(value)
		switch strings.ToLower(strings.TrimSpace(key)) {
		case "tui":
			if it.Group == "" {
				it.Group = strings.ToLower(value)
			}
		case "tui-env":
			it.Envs = strings.Fields(strings.ToLower(value))
		case "tui-vars":
			it.Vars = strings.Fields(value)
		case "tui-safe":
			// Anything but an explicit yes leaves the playbook treated as
			// making changes. This flag lowers the cost of the production
			// guard, never removes it, so a wrong value is not a way past it.
			v := strings.ToLower(value)
			it.Safe = v == "true" || v == "yes"
		case "tui-usage":
			it.Usage = value
		}
	}
	return it
}

// describeInventory reads an inventory's description from the first comment in
// its hosts file — the line that is already there explaining what this
// environment is and where it runs.
//
// The convention of opening that comment with the environment's own name
// ("# prod = shared box plus dedicated instances") is common enough to be worth
// undoing, since the name is already in the column beside it.
func describeInventory(root, inv string) string {
	path := filepath.Join(root, inv)
	if fi, err := os.Stat(path); err == nil && fi.IsDir() {
		path = hostsFile(path)
	}

	for _, line := range head(path) {
		if line == "" || line == "---" {
			continue
		}
		text, ok := comment(line)
		if !ok {
			// Content has started; anything below is not a header.
			return ""
		}
		for _, sep := range []string{"=", ":", "—", "-"} {
			name, rest, found := strings.Cut(text, sep)
			if found && strings.EqualFold(strings.TrimSpace(name), filepath.Base(inv)) {
				return strings.TrimSpace(rest)
			}
		}
		return text
	}
	return ""
}

// hostsFile picks the file inside an inventory directory whose header describes
// the environment: hosts.yml by convention, otherwise whichever YAML sorts
// first, so a directory named differently still gets a description.
func hostsFile(dir string) string {
	for _, name := range []string{"hosts.yml", "hosts.yaml"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err == nil {
			return filepath.Join(dir, name)
		}
	}
	matches, _ := filepath.Glob(filepath.Join(dir, "*.yml"))
	if len(matches) == 0 {
		matches, _ = filepath.Glob(filepath.Join(dir, "*.yaml"))
	}
	if len(matches) > 0 {
		sort.Strings(matches)
		return matches[0]
	}
	return dir
}

// unquote strips the quoting a YAML scalar may carry. Not a parser: a play name
// is a plain string in every repository, and one that is not simply displays as
// written.
func unquote(s string) string {
	if len(s) >= 2 && (s[0] == '"' || s[0] == '\'') && s[len(s)-1] == s[0] {
		return s[1 : len(s)-1]
	}
	return s
}

// sortByGroup rearranges playbooks so those sharing a group are adjacent,
// keeping the existing order within each group. Ungrouped playbooks sort last
// under a heading of their own rather than floating above the named groups.
func sortByGroup(playbooks []Item) {
	sort.SliceStable(playbooks, func(a, b int) bool {
		return groupKey(playbooks[a].Group) < groupKey(playbooks[b].Group)
	})
}

// anyGrouped reports whether a column should show headings at all. A repository
// that has adopted no groups gets the plain list it had before.
func anyGrouped(items []Item) bool {
	for _, it := range items {
		if it.Group != "" {
			return true
		}
	}
	return false
}

func groupKey(g string) string {
	if g == "" {
		return "￿"
	}
	return g
}

// findInventories prefers an inventories/ directory holding one entry per
// environment, and falls back to a single inventory file at the root. An empty
// result is valid: the run then omits -i entirely and lets ansible.cfg supply
// the default.
func findInventories(root string) []string {
	if entries, err := os.ReadDir(filepath.Join(root, "inventories")); err == nil {
		var out []string
		for _, e := range entries {
			if strings.HasPrefix(e.Name(), ".") {
				continue
			}
			out = append(out, filepath.Join("inventories", e.Name()))
		}
		if len(out) > 0 {
			sort.Strings(out)
			return out
		}
	}

	var out []string
	seen := map[string]bool{}
	for _, pattern := range []string{"inventory*.yml", "inventory*.yaml", "hosts.yml", "hosts.yaml"} {
		matches, _ := filepath.Glob(filepath.Join(root, pattern))
		for _, m := range matches {
			name := filepath.Base(m)
			if !seen[name] {
				seen[name] = true
				out = append(out, name)
			}
		}
	}
	sort.Strings(out)
	return out
}

// notPlaybook lists root-level YAML that is configuration rather than plays.
var notPlaybook = map[string]bool{
	"requirements.yml":  true,
	"requirements.yaml": true,
	"hosts.yml":         true,
	"hosts.yaml":        true,
}

// findPlaybooks globs one level only, so playbooks/tasks/, playbooks/vars/,
// playbooks/templates/ and playbooks/archive/ are excluded for free.
func findPlaybooks(root string, skip map[string]bool) []string {
	var nested, top []string
	seen := map[string]bool{}

	collect := func(pattern string, into *[]string) {
		matches, _ := filepath.Glob(filepath.Join(root, pattern))
		for _, m := range matches {
			rel, err := filepath.Rel(root, m)
			if err != nil || skip[rel] || seen[rel] {
				continue
			}
			base := filepath.Base(rel)
			if notPlaybook[base] || strings.HasPrefix(base, "inventory") || strings.HasPrefix(base, ".") {
				continue
			}
			if fi, err := os.Stat(m); err != nil || fi.IsDir() {
				continue
			}
			seen[rel] = true
			*into = append(*into, rel)
		}
	}

	collect("playbooks/*.yml", &nested)
	collect("playbooks/*.yaml", &nested)
	collect("*.yml", &top)
	collect("*.yaml", &top)

	sort.Strings(nested)
	sort.Strings(top)
	return append(nested, top...)
}

// needsConfirm reports whether running against this inventory should demand a
// typed confirmation. A y/n prompt would not qualify: the guard has to cost
// more than a reflexive keypress or it guards nothing.
func needsConfirm(inventory string) bool {
	base := strings.ToLower(filepath.Base(inventory))
	return base == "prod" || base == "production"
}
