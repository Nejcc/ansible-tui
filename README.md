# ansible-tui

A terminal UI for the Ansible repositories you already have. Choose an
inventory, choose a playbook, look at what that adds up to, then run it and
watch the output.

Ansible does the work. This picks the arguments, keeps a terminal on the other
end so output streams in colour, and records the outcome. It parses no YAML,
opens no SSH connections, and holds no state your repository does not already
describe.

The picker is a sequence rather than a form, because the way a production
mistake actually happens is running the right playbook against the wrong
inventory. Where comes first, what comes second, which hosts third, and the
last screen before anything runs states all of it.

```
╭──────────────────────────────────────────────────────────────────────────╮
│ ansible-tui  /srv/infra                                                  │
╰──────────────────────────────────────────────────────────────────────────╯
 1 inventory  ›  2 playbook  ›  3 review
╭──────────────────────────────────────────────────────────────────────────╮
│ INVENTORIES 3                                                            │
│ ▸ dev       two virtual machines on this desk                            │
│   stage     one shared box, same shape as production                     │
│   prod      shared box plus a dedicated instance per project             │
╰──────────────────────────────────────────────────────────────────────────╯
 ↑↓ move  ·  ⏎ choose  ·  o overview  ·  q quit
```

Then the playbooks, in groups, with everything the one under the cursor
declares about itself alongside:

```
╭──────────────────────────────────────────────────────────────────────────╮
│ ansible-tui  /srv/infra                       PRODUCTION   DRY RUN — …   │
╰──────────────────────────────────────────────────────────────────────────╯
 1 prod  ›  2 playbook  ›  3 review
╭────────────────────────────────────────────────╮╭──────────────────────╮
│ PLAYBOOKS 6                                    ││ THIS PLAYBOOK        │
│ deploy                                         ││ laravel-deploy       │
│ ▸ laravel-deploy    Deploy a PHP application   ││                      │
│   rollback          Roll back the release      ││ what it does         │
│ fleet                                          ││ Deploy a PHP         │
│   audit  read-only  Health check, every host   ││ application          │
│   site              Configure the fleet        ││                      │
│ 3 not offered here — they name other           ││ group                │
│ inventories                                    ││ deploy               │
│                                                ││                      │
│                                                ││ only on              │
│                                                ││ any inventory        │
│                                                ││                      │
│                                                ││ needs -e             │
│                                                ││ project  domain      │
│                                                ││                      │
│                                                ││ last run             │
│                                                ││ ok · dev · 2h ago    │
╰────────────────────────────────────────────────╯╰──────────────────────╯
 ↑↓ move  ·  ⏎ choose  ·  esc back  ·  q quit
```

The right-hand pane follows the cursor. On a narrow terminal the list drops its
inline descriptions and that pane carries them instead, so nothing is lost —
only moved.

Then where to run it, which you can leave alone:

```
╭──────────────────────────────────────────────────────────────────────────╮
│ ansible-tui  /srv/infra                       PRODUCTION   DRY RUN — …   │
╰──────────────────────────────────────────────────────────────────────────╯
 1 prod  ›  2 laravel-deploy  ›  3 hosts  ›  4 review
╭──────────────────────────────────────────────────────────────────────────╮
│ RUN ON                                                                   │
│ default                                                                  │
│ ▸ [x] every host       wherever the playbook sends it — nothing narrowed │
│ groups                                                                   │
│   [ ] global_prod      1 host    www-global-prod                         │
│   [ ] proj_manager     1 host    www-manager                             │
│ hosts                                                                    │
│   [ ] www-global-prod  192.168.202.114  aws       in global_prod         │
│   [ ] www-manager      192.168.202.152  aws       in proj_manager        │
│   [ ] rds01            192.168.0.79     proxmox   in local_rds           │
╰──────────────────────────────────────────────────────────────────────────╯
 space pick / unpick  ·  a every host  ·  ⏎ continue  ·  esc back
```

Each host shows the address ansible will actually connect to and, when the
inventory says so, who runs the machine — `provider: aws`, `proxmox`, whatever
you set. Both come from `_meta.hostvars`, so they are the resolved values after
group vars and host vars are merged, not a guess from the file. A host whose
inventory never names a provider leaves that column blank rather than claiming
something.

The default is the first row, ticked, and where the cursor starts — so the
answer is visible rather than implied by an empty list. It means no `--limit` at
all: the run goes wherever the playbook's own `hosts:` sends it. Ticking a group
or a host unticks the default and adds `--limit a,b`, the same thing you would
have typed; landing back on `every host` clears them again. The list is
`ansible-inventory --list`, asked once per inventory, so dynamic and plugin
inventories work without a special case. Choosing a different inventory drops
the selection rather than carrying names that mean nothing there.

And the review, which is the only screen that can start anything:

```
╭──────────────────────────────────────────────────────────────────────────╮
│ ansible-tui  /srv/infra                 PRODUCTION   DRY RUN — LOCKED   │
╰──────────────────────────────────────────────────────────────────────────╯
 1 prod  ›  2 site  ›  3 all hosts  ›  4 review
╭──────────────────────────────────────────────────────────────────────────╮
│ REVIEW                                                                   │
│ on    prod  —  shared box plus a dedicated instance per project          │
│ run   site  —  Configure the fleet                                       │
│ hosts  every host this playbook targets                                  │
│                                                                          │
│ $ ansible-playbook -i inventories/prod --check playbooks/site.yml        │
│ last   ✓ ok  ·  dev  ·  48s  ·  2h ago                                   │
│                                                                          │
│ production — dry-run only, and enter still asks before it starts         │
╰──────────────────────────────────────────────────────────────────────────╯
 ⏎ run  ·  p preview  ·  c check  ·  esc back  ·  o overview  ·  q quit
```

Once a production inventory is chosen it is said so in the top bar, in the
breadcrumb, and on the review — the run is pinned to `--check`, and `enter`
opens a box demanding the word be typed out. A `y/n` prompt would not qualify:
the guard has to cost more than a reflexive keypress or it guards nothing.

Colour carries meaning rather than decoration: green succeeded, red failed,
amber means the run is not the real thing. Everything adapts to light and dark
terminals.

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
| `↑` `↓` / `k` `j` | move |
| `space` | pick a group or host to limit the run to |
| `a` | back to all hosts |
| `enter` / `→` | choose, and on the review, run |
| `esc` / `←` | back one step |
| `p` | preview: hosts and tasks this would touch, on the review |
| `c` | toggle `--check` — refused on production |
| `o` | overview: host tree and recent runs |
| `ctrl+c` | interrupt a running playbook |
| `q` | quit |

## What it discovers

No configuration file. Layout is read from the directory it runs in.

**Inventories** — every entry under `inventories/`, or failing that an
`inventory*.yml` or `hosts.yml` at the root. If there are none, playbooks run
without `-i` and `ansible.cfg` supplies the default.

**Playbooks** — `playbooks/*.yml` and root-level `*.yml`. One level only, so
`playbooks/tasks/`, `playbooks/vars/`, `playbooks/templates/` and
`playbooks/archive/` are skipped, as are `requirements.yml` and inventory files.

No YAML is parsed. A file is a playbook because of where it sits and what it is
called, never because of what is inside it — a stray entry costs one keystroke,
and parsing every file to prevent that costs more.

## Groups and descriptions

Past a dozen playbooks a single column stops being a list and starts being a
haystack, and `manager3-cutover-pre` does not say what it does. Both are fixed
by what is already at the top of the file.

**A description** is the play's own `name`. Nothing to write: every playbook has
one.

```yaml
---
# tui: deploy
- name: Deploy the application and swap the release symlink
  hosts: web
```

**A group** is the `# tui:` comment. The column then reads as headed sections
rather than one flat run, ordered by group with everything untagged gathered
under `other` at the end.

**The rest is optional**, and a playbook that declares none of it behaves as it
always did:

```yaml
---
# tui: database
# tui-env: internal              only offered against inventories/internal
# tui-vars: env dump_src         cannot run without these
# tui-safe: true                 changes nothing
# tui-usage: ansible-playbook -i inventories/internal playbooks/db-sync.yml
```

`tui-env` is the useful one. A playbook that names its inventories is not
offered against the others, and the list says how many it left out — a cutover
playbook has no business being one keystroke from a dev run, and `db-sync` only
means anything on `internal`.

`tui-vars` shows on the review as `needs  env  dump_src`. It warns rather than
blocks: `host_vars` may already supply them.

`tui-safe` is a claim the file makes about itself, so it buys less than it
looks: a read-only playbook is marked in the list and asks for a keypress rather
than the typed word on production. It does not skip the guard, and it does not
unlock a real run.

Comments rather than real keys, because ansible must not have to understand
them: the header can go into a shared repository without that repository taking
on a dependency on this program.

**An inventory's description** is the first comment in its hosts file — the line
explaining what that environment is, which is usually already there:

```yaml
---
# prod = shared box, plus a dedicated instance per project
all:
```

Repeating the environment's own name at the front is common enough that it is
taken back off, since the name is in the column beside it.

Only the first 4KB of each file is read, looking for those lines. A file that
offers nothing still lists, described as having no description; a group nobody
set simply never appears.

The playbook runs with the working directory set to the repository root, so the
repository's own `ansible.cfg` applies in full — `vault_password_file`,
`roles_path`, callback plugins, privilege escalation. Nothing it already sets is
overridden.

## Guard rails

**Production is dry-run only.** `--check` is on when the program starts, and on
an inventory named `prod` or `production` it cannot be turned off — `c` says so
and refuses. Nothing this program starts can change production. That is a
deliberate ceiling, not a default: lifting it means editing `needsConfirm` and
the `c` handler, which is exactly as much friction as it should be.

**Production always asks.** A playbook that changes things will not run until
you type `prod` and press enter. One that declares itself read-only asks for a
second, deliberate enter instead. Neither is a `y/n` prompt on a key your hand
is already resting on.

**The last screen states everything.** Which inventory, what it is, which
playbook, what it does, the exact command, and how it went last time — before
`enter` means anything. `p` previews further: `--list-hosts` and `--list-tasks`,
which is ansible resolving what would happen without opening a connection.

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

`example/` is a fixture repository shaped like a real one — four inventories,
twenty-two playbooks, invented hosts, every play a no-op running against
`localhost`. Run `ansible-tui example` to try the interface without touching
anything.

Nineteen tests, no framework. Discovery is checked against synthesised repository
layouts, history against a round trip, and the exec path end to end by running a
real playbook against `localhost` — that one skips if `ansible-playbook` is not
installed. The picker is checked by walking its three steps and asserting on
`View()` output rather than driving a terminal, and the production rules — dry-run
locked, both grades of confirmation, nothing starting on one keypress — have a
test of their own.

## Licence

MIT
