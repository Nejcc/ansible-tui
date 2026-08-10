package main

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Repo is what a directory looks like to this program: a set of inventories and
// a set of playbooks, both as paths relative to Root.
type Repo struct {
	Root        string
	Inventories []string
	Playbooks   []string
}

// Discover inspects root without reading any YAML. Deciding whether a file is
// really a playbook by parsing it costs more than the mistake it prevents: a
// stray entry in the list wastes one keystroke.
func Discover(root string) Repo {
	r := Repo{Root: root}
	r.Inventories = findInventories(root)

	skip := make(map[string]bool, len(r.Inventories))
	for _, inv := range r.Inventories {
		skip[inv] = true
	}
	r.Playbooks = findPlaybooks(root, skip)
	return r
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
