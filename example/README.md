# example

A fixture repository for exercising the TUI by hand:

```sh
go run . example
go run . --list example
```

It mirrors the shape of a real multi-environment Ansible repository — four
inventories, twenty-two playbooks, and the `playbooks/` subdirectories that
discovery must skip — so the picker, the confirmation guard on `prod`, the host
tree and the run history all get realistic input.

Nothing here touches a real host. Every inventory sets
`ansible_connection: local`, so a "host" is an alias for this machine, and every
play is a no-op that prints and pauses. The hostnames, addresses and project
names are invented.

Playbooks that exist to demonstrate specific TUI states:

| Playbook | |
| --- | --- |
| `site.yml` | several tasks over several hosts — the normal streaming case |
| `audit.yml` | long fan-out output for scrolling |
| `db-sync.yml` | slow, for testing the elapsed timer and `ctrl+c` |
| `ssl-health-check.yml` | fails, for the red badge and non-zero exit |

`.ansible-tui/` accumulates run history as you use it. That is the program
writing its own state, not fixture data — deleting it is safe.
