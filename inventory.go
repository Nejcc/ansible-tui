package main

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

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
