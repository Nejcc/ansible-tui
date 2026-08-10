# ansible-tui

A terminal UI for the Ansible repositories you already have. Pick an inventory
and a playbook, run it, watch the output, and see what ran last.

Ansible does the work. This picks the arguments, keeps a terminal on the other
end so output streams in colour, and records the outcome. It parses no YAML,
opens no SSH connections, and holds no state your repository does not already
describe.

```
╭──────────────────────────────────────────────────────────────────────────╮
│ ansible-tui  /srv/infra                                     CHECK MODE   │
╰──────────────────────────────────────────────────────────────────────────╯
╭──────────────────────────────────╮╭──────────────────────────────────────╮
│ INVENTORIES                      ││ PLAYBOOKS                            │
│ ▸ dev                            ││   audit                              │
│   prod                           ││ ▸ site                               │
╰──────────────────────────────────╯╰──────────────────────────────────────╯
╭──────────────────────────────────────────────────────────────────────────╮
│ $ ansible-playbook -i inventories/dev --check playbooks/site.yml         │
│ last   ✓ ok  ·  dev  ·  48s  ·  2h ago                                   │
╰──────────────────────────────────────────────────────────────────────────╯
 tab switch  ·  ↑↓ move  ·  ⏎ run  ·  c check  ·  o overview  ·  q quit
```

The focused column takes the accent colour, so it is always obvious which one
the arrow keys will move. Colour carries meaning rather than decoration: green
succeeded, red failed, amber means the run is not the real thing. Everything
adapts to light and dark terminals.

## Install

```sh
go install github.com/Nejcc/ansible-tui@latest
```

Requires Go 1.24+ and `ansible-playbook` on `PATH`.

## Use

Run it inside an Ansible repository, or give it a path:

```sh
cd /srv/infra && ansible-tui
ansible-tui /srv/infra
ansible-tui --list /srv/infra    # print what was discovered, then exit
```

### Keys

| Key | |
| --- | --- |
| `tab` | switch between the inventory and playbook columns |
| `↑` `↓` / `k` `j` | move |
| `enter` | run the selected playbook |
| `c` | toggle `--check` |
| `o` | overview: host tree and recent runs |
| `ctrl+c` | interrupt a running playbook |
| `esc` | back |
| `q` | quit |

## What it discovers

No configuration file. Layout is read from the directory it runs in.

**Inventories** — every entry under `inventories/`, or failing that an
`inventory*.yml` or `hosts.yml` at the root. If there are none, playbooks run
without `-i` and `ansible.cfg` supplies the default.

**Playbooks** — `playbooks/*.yml` and root-level `*.yml`. One level only, so
`playbooks/tasks/`, `playbooks/vars/`, `playbooks/templates/` and
`playbooks/archive/` are skipped, as are `requirements.yml` and inventory files.

Files are not opened to confirm they contain plays. A stray entry costs one
keystroke; reading every YAML file to prevent it costs more than that.

The playbook runs with the working directory set to the repository root, so the
repository's own `ansible.cfg` applies in full — `vault_password_file`,
`roles_path`, callback plugins, privilege escalation. Nothing it already sets is
overridden.

## Guard rails

**Production needs typing.** An inventory named `prod` or `production` will not
run until you type `prod` and press enter. Not a `y/n` prompt — a guard that
costs less than a reflex is not a guard.

**Check mode is always visible.** `c` toggles `--check`, and its state shows in
the footer and in the printed command. The command about to run is displayed
verbatim, so there is never a question about what `enter` does.

**Interrupt stops the playbook, not the UI.** The child runs in its own session;
`ctrl+c` signals it and every task it forked, then records the run as interrupted.

## Run history

Ansible keeps no history of its own unless you configure `log_path`, so this
writes its own, into `.ansible-tui/` in the repository it drives:

```
.ansible-tui/runs.jsonl                       one JSON object per run
.ansible-tui/logs/20260810-120953-site.log    full output of that run
```

`runs.jsonl` is append-only, one short line per deploy:

```json
{"ts":"2026-08-10T12:09:53Z","playbook":"playbooks/site.yml","inventory":"inventories/prod","check":false,"exit":0,"duration_ms":48210,"log":".ansible-tui/logs/20260810-120953-site.log"}
```

The overview joins that against `ansible-inventory --list` and lets you reopen
any past run's output. `.ansible-tui/` is added to an existing `.gitignore`; no
`.gitignore` is created where none exists.

**Only runs started from here are recorded.** Cron jobs, wrapper scripts and
bare `ansible-playbook` invocations stay invisible. Closing that gap means a
small callback plugin appending to the same file — the format is stable, so
that can be added later without migrating anything.

## Deliberately not included

No polling loop, no daemon, no database, no scheduler, no web interface, no
user accounts. The overview is a read of what happened, not a live feed; if you
want continuous monitoring, that is a different program and good ones exist.

No vault interface either — `ansible-vault edit` already works.

## Note on locales

Ansible calls `setlocale(LC_ALL, "")` at startup and aborts with *"could not
initialize the preferred locale: unsupported locale setting"* if any `LC_*`
variable names a locale the host has not generated. Desktop sessions commonly
set `LC_TIME` or `LC_MONETARY` to a regional locale that a minimal install
lacks.

So `LC_ALL=C.UTF-8` is set for the child unless you set `LC_ALL` yourself. If
you would rather fix the cause, generate the missing locale (on glibc systems,
uncomment it in `/etc/locale.gen` and run `locale-gen`).

## Development

```sh
go test ./...
```

Twelve tests, no framework. Discovery is checked against synthesised repository
layouts, history against a round trip, and the exec path end to end by running a
real playbook against `localhost` — that one skips if `ansible-playbook` is not
installed. The bubbletea rendering is checked by asserting on `View()` output
rather than driving a terminal.

## Licence

MIT
