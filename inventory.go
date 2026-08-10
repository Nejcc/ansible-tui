package main

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// parseTargets turns `ansible-inventory --list` output into the things a run
// can be limited to: every group that directly holds hosts, then every host.
// Ansible resolves the inventory; this only reshapes what it reports, so
// dynamic sources and plugin inventories need no special case.
//
// Selecting nothing is the normal case and means every host — the same as
// omitting --limit, which is what ansible does on its own.
func parseTargets(raw []byte) []Item {
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil
	}

	// _meta.hostvars is where ansible puts the resolved variables for each host,
	// group vars and host vars already merged. The address and the provider are
	// the two that answer "which machine is this, and whose".
	var meta struct {
		HostVars map[string]struct {
			Host     string `json:"ansible_host"`
			User     string `json:"ansible_user"`
			Provider string `json:"provider"`
		} `json:"hostvars"`
	}
	json.Unmarshal(doc["_meta"], &meta)

	memberOf := map[string][]string{}
	var groups []Item

	for name, body := range doc {
		// _meta carries hostvars, and all is the implicit parent of everything.
		if name == "_meta" || name == "all" {
			continue
		}
		var group struct {
			Hosts []string `json:"hosts"`
		}
		if json.Unmarshal(body, &group) != nil || len(group.Hosts) == 0 {
			continue
		}
		sort.Strings(group.Hosts)
		for _, h := range group.Hosts {
			memberOf[h] = append(memberOf[h], name)
		}
		groups = append(groups, Item{
			Path:  name,
			Group: "groups",
			Desc: fmt.Sprintf("%-9s %s",
				plural(len(group.Hosts), "host"), strings.Join(group.Hosts, ", ")),
		})
	}

	hosts := make([]Item, 0, len(memberOf))
	for h, in := range memberOf {
		sort.Strings(in)
		v := meta.HostVars[h]

		// Padded into columns rather than joined, so a list of hosts reads down
		// the address and the provider instead of across each line. An unset
		// field leaves its column blank: an inventory that never says who runs
		// a machine should look like it never said, not like it said "unknown".
		addr := v.Host
		if addr == "" {
			addr = h // no ansible_host: the name is the address
		}
		hosts = append(hosts, Item{
			Path:  h,
			Group: "hosts",
			Desc: fmt.Sprintf("%-16s %-9s in %s",
				trunc(addr, 16), trunc(v.Provider, 9), strings.Join(in, ", ")),
		})
	}

	byPath := func(a, b Item) bool { return a.Path < b.Path }
	sort.Slice(groups, func(i, j int) bool { return byPath(groups[i], groups[j]) })
	sort.Slice(hosts, func(i, j int) bool { return byPath(hosts[i], hosts[j]) })
	return append(groups, hosts...)
}

// plural writes a count the way a sentence would.
func plural(n int, word string) string {
	if n == 1 {
		return fmt.Sprintf("1 %s", word)
	}
	return fmt.Sprintf("%d %ss", n, word)
}

// parseHosts turns `ansible-inventory --list` output into one line per group.
// Ansible resolves the inventory; this only formats what it reports, so vaulted
// vars, dynamic sources and plugin inventories all work without special cases.
func parseHosts(raw []byte) []string {
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil
	}

	var out []string
	for name, body := range doc {
		// _meta carries hostvars, and all is the implicit parent of everything.
		if name == "_meta" || name == "all" {
			continue
		}
		var group struct {
			Hosts []string `json:"hosts"`
		}
		if json.Unmarshal(body, &group) != nil || len(group.Hosts) == 0 {
			continue
		}
		sort.Strings(group.Hosts)
		out = append(out, fmt.Sprintf("%-16s %d  %s", name, len(group.Hosts), strings.Join(group.Hosts, ", ")))
	}
	sort.Strings(out)
	return out
}
