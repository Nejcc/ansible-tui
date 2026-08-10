package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// write creates a file and every directory leading to it.
func write(t *testing.T, root, rel string) {
	t.Helper()
	path := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestDiscoverNestedLayout covers the common shape: an inventories/ directory
// with one entry per environment, and a playbooks/ tree whose subdirectories
// hold includes and vars that must not be mistaken for playbooks.
func TestDiscoverNestedLayout(t *testing.T) {
	root := t.TempDir()
	for _, env := range []string{"dev", "stage", "prod", "internal"} {
		if err := os.MkdirAll(filepath.Join(root, "inventories", env), 0o755); err != nil {
			t.Fatal(err)
		}
		write(t, root, filepath.Join("inventories", env, "hosts.yml"))
	}
	write(t, root, "playbooks/site.yml")
	write(t, root, "playbooks/audit.yml")
	write(t, root, "playbooks/tasks/common.yml")
	write(t, root, "playbooks/vars/main.yml")
	write(t, root, "playbooks/archive/old.yml")
	write(t, root, "requirements.yml")

	repo := Discover(root)

	wantInv := []string{
		filepath.Join("inventories", "dev"),
		filepath.Join("inventories", "internal"),
		filepath.Join("inventories", "prod"),
		filepath.Join("inventories", "stage"),
	}
	if !reflect.DeepEqual(Paths(repo.Inventories), wantInv) {
		t.Errorf("inventories = %v, want %v", Paths(repo.Inventories), wantInv)
	}

	wantPb := []string{
		filepath.Join("playbooks", "audit.yml"),
		filepath.Join("playbooks", "site.yml"),
	}
	if !reflect.DeepEqual(Paths(repo.Playbooks), wantPb) {
		t.Errorf("playbooks = %v, want %v", Paths(repo.Playbooks), wantPb)
	}
}

// TestDiscoverFlatLayout covers a small repository with no inventories/ and no
// playbooks/ directory, everything sitting at the root.
func TestDiscoverFlatLayout(t *testing.T) {
	root := t.TempDir()
	write(t, root, "playbook.yml")
	write(t, root, "inventory.yml")

	repo := Discover(root)

	if !reflect.DeepEqual(Paths(repo.Inventories), []string{"inventory.yml"}) {
		t.Errorf("inventories = %v, want [inventory.yml]", Paths(repo.Inventories))
	}
	if !reflect.DeepEqual(Paths(repo.Playbooks), []string{"playbook.yml"}) {
		t.Errorf("playbooks = %v, want [playbook.yml]", Paths(repo.Playbooks))
	}
}

// writeBody creates a file with the given contents, for the cases where the
// header matters.
func writeBody(t *testing.T, root, rel, body string) {
	t.Helper()
	write(t, root, rel)
	if err := os.WriteFile(filepath.Join(root, rel), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestDiscoverGroups: playbooks carrying a `# tui:` header are ordered by group,
// keeping their existing order within one, and ungrouped ones come last.
func TestDiscoverGroups(t *testing.T) {
	root := t.TempDir()
	writeBody(t, root, "playbooks/site.yml", "---\n# tui: Fleet\n- name: site\n")
	writeBody(t, root, "playbooks/rollback.yml", "---\n# tui: deploy\n")
	writeBody(t, root, "playbooks/laravel.yml", "---\n#   tui:   deploy   \n- name: \"Deploy the application\"\n")
	writeBody(t, root, "playbooks/notify.yml", "---\n- name: notify\n")
	// Past the end of the header read, so this one is ungrouped: a marker
	// thousands of bytes down is not a header by any reading.
	writeBody(t, root, "playbooks/audit.yml", "---\n"+strings.Repeat("# padding\n", 600)+"# tui: fleet\n")

	repo := Discover(root)

	wantPb := []string{
		filepath.Join("playbooks", "laravel.yml"),
		filepath.Join("playbooks", "rollback.yml"),
		filepath.Join("playbooks", "site.yml"),
		filepath.Join("playbooks", "audit.yml"),
		filepath.Join("playbooks", "notify.yml"),
	}
	wantGr := []string{"deploy", "deploy", "fleet", "", ""}

	if !reflect.DeepEqual(Paths(repo.Playbooks), wantPb) {
		t.Errorf("playbooks = %v, want %v", Paths(repo.Playbooks), wantPb)
	}
	var gotGr []string
	for _, it := range repo.Playbooks {
		gotGr = append(gotGr, it.Group)
	}
	if !reflect.DeepEqual(gotGr, wantGr) {
		t.Errorf("groups = %v, want %v", gotGr, wantGr)
	}

	// Descriptions come from each play's own name, quoting and all.
	if got := repo.Playbooks[2].Desc; got != "site" {
		t.Errorf("site.yml description = %q, want %q", got, "site")
	}
	if got := repo.Playbooks[0].Desc; got != "Deploy the application" {
		t.Errorf("laravel.yml description = %q, want it unquoted", got)
	}

	// The selection still indexes playbooks, not rendered rows.
	m := newModel(root)
	m.step = stepPlaybook
	m.move(2)
	if got := m.selectedPlaybook().Path; got != filepath.Join("playbooks", "site.yml") {
		t.Errorf("third playbook = %q, want playbooks/site.yml", got)
	}
}

// TestDescribeInventory: the description is the comment the hosts file already
// carries, with the environment's own name taken off the front when the comment
// opens by repeating it.
func TestDescribeInventory(t *testing.T) {
	root := t.TempDir()
	cases := map[string]struct{ body, want string }{
		"dev":      {"---\n# dev = local hypervisor VMs\nall:\n", "local hypervisor VMs"},
		"stage":    {"---\n# staging on one shared box\nall:\n", "staging on one shared box"},
		"prod":     {"---\n# prod — dedicated instance per project\nall:\n", "dedicated instance per project"},
		"internal": {"all:\n# not a header: content started above\n", ""},
		"bare":     {"---\nall:\n", ""},
	}
	for env, c := range cases {
		writeBody(t, root, filepath.Join("inventories", env, "hosts.yml"), c.body)
	}

	for _, it := range Discover(root).Inventories {
		env := filepath.Base(it.Path)
		if want := cases[env].want; it.Desc != want {
			t.Errorf("%s description = %q, want %q", env, it.Desc, want)
		}
	}

	// An inventory that is a single file at the root, not a directory.
	flat := t.TempDir()
	writeBody(t, flat, "inventory.yml", "---\n# the one host this repository has\nall:\n")
	inv := Discover(flat).Inventories
	if len(inv) != 1 || inv[0].Desc != "the one host this repository has" {
		t.Errorf("flat inventory description wrong: %+v", inv)
	}
}

// TestProductionIsDryRunOnly pins the two rules that matter more than anything
// else here: a production run cannot leave dry-run, and it cannot start without
// being confirmed — read-only or not.
func TestProductionIsDryRunOnly(t *testing.T) {
	root := t.TempDir()
	for _, env := range []string{"dev", "prod"} {
		os.MkdirAll(filepath.Join(root, "inventories", env), 0o755)
	}
	writeBody(t, root, "playbooks/audit.yml", "---\n# tui-safe: true\n- name: Audit\n")
	writeBody(t, root, "playbooks/site.yml", "---\n- name: Configure\n")

	for _, tc := range []struct {
		playbook   string
		wantTyping bool
	}{
		{"audit", false}, // read-only: acknowledged, not typed
		{"site", true},   // changes things: the word
	} {
		m := newModel(root)
		m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})

		m.move(1) // prod
		m.advance()
		for range m.playbooks() {
			if playbookName(m.selectedPlaybook().Path) == tc.playbook {
				break
			}
			m.move(1)
		}
		m.advance() // hosts
		m.advance() // review

		// c must not unlock production, no matter how insistently.
		for range 3 {
			m.key(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("c")})
		}
		if !m.check || !strings.Contains(m.commandLine(), "--check") {
			t.Errorf("%s: production left dry-run — command was %q", tc.playbook, m.commandLine())
		}

		// And nothing starts on a single keypress.
		m.advance()
		if !m.asking {
			t.Fatalf("%s: a production run started without confirmation", tc.playbook)
		}
		if m.typing != tc.wantTyping {
			t.Errorf("%s: typing = %v, want %v", tc.playbook, m.typing, tc.wantTyping)
		}
		if m.live {
			t.Fatalf("%s: a run was started by the confirmation itself", tc.playbook)
		}

		// esc backs out of both grades without running.
		m.confirmKey(tea.KeyMsg{Type: tea.KeyEsc})
		if m.asking || m.live {
			t.Errorf("%s: esc did not cancel", tc.playbook)
		}
	}
}

// TestParseTargets: what a run can be limited to, read out of ansible's own
// inventory dump — every group that directly holds hosts, then every host with
// the groups it belongs to.
func TestParseTargets(t *testing.T) {
	raw := []byte(`{
	  "_meta": {"hostvars": {
	    "www-1": {"ansible_host": "10.0.0.1", "provider": "aws"},
	    "www-2": {}
	  }},
	  "all": {"children": ["prod"]},
	  "prod": {"children": ["web", "db"]},
	  "web": {"hosts": ["www-2", "www-1"]},
	  "db":  {"hosts": ["www-1"]},
	  "empty": {"hosts": []}
	}`)

	got := parseTargets(raw)
	want := []Item{
		{Path: "db", Group: "groups", Desc: "1 host    www-1"},
		{Path: "web", Group: "groups", Desc: "2 hosts   www-1, www-2"},
		// address, provider, then the groups it belongs to
		{Path: "www-1", Group: "hosts", Desc: "10.0.0.1         aws       in db, web"},
		// no ansible_host and no provider: the name stands in, provider blank
		{Path: "www-2", Group: "hosts", Desc: "www-2                      in web"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("parseTargets:\n got %+v\nwant %+v", got, want)
	}

	// A group holding nothing, _meta and all are not things to limit to.
	for _, it := range got {
		if it.Path == "empty" || it.Path == "all" || it.Path == "_meta" {
			t.Errorf("%q should not be offered as a limit", it.Path)
		}
	}
	if parseTargets([]byte("not json")) != nil {
		t.Error("unparseable output should yield nothing, not a panic")
	}
}

// TestLimit: picking nothing runs everything, picking something narrows it, and
// changing inventory drops a selection that no longer refers to anything.
func TestLimit(t *testing.T) {
	root := t.TempDir()
	for _, env := range []string{"dev", "prod"} {
		os.MkdirAll(filepath.Join(root, "inventories", env), 0o755)
	}
	writeBody(t, root, "playbooks/site.yml", "---\n- name: Configure\n  hosts: web\n")

	m := newModel(root)
	// Stand in for ansible: this test is about the picking, not the dumping.
	m.targets[filepath.Join("inventories", "dev")] = []Item{
		{Path: "web", Group: "groups"}, {Path: "www-1", Group: "hosts"},
	}

	m.advance() // dev → playbooks
	m.advance() // → hosts

	// The cursor starts on the default, which is ticked and adds no --limit.
	if m.hostIdx != 0 || m.hostRows()[0].Path != "" {
		t.Fatalf("the default is not the first row: %+v", m.hostRows()[0])
	}
	if strings.Contains(m.commandLine(), "--limit") {
		t.Errorf("the default should mean no --limit: %q", m.commandLine())
	}
	// Choosing it again is a no-op, not a selection of something called "".
	m.key(tea.KeyMsg{Type: tea.KeySpace})
	if strings.Contains(m.commandLine(), "--limit") {
		t.Errorf("picking the default limited the run: %q", m.commandLine())
	}

	m.move(1) // web
	m.key(tea.KeyMsg{Type: tea.KeySpace})
	m.move(1) // www-1
	m.key(tea.KeyMsg{Type: tea.KeySpace})
	if !strings.Contains(m.commandLine(), "--limit web,www-1") {
		t.Errorf("both picks should be in the command: %q", m.commandLine())
	}

	// Landing back on the default clears them, rather than adding a third.
	m.move(-2)
	m.key(tea.KeyMsg{Type: tea.KeySpace})
	if strings.Contains(m.commandLine(), "--limit") {
		t.Errorf("the default did not clear the selection: %q", m.commandLine())
	}
	m.move(1)
	m.key(tea.KeyMsg{Type: tea.KeySpace})

	// a returns to the default without walking back up the list.
	m.key(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")})
	if len(m.limits()) != 0 {
		t.Errorf("a should clear the selection, got %v", m.limits())
	}

	// The playbook's own pattern is read from the file, and is not a limit.
	if got := m.selectedPlaybook().Pattern; got != "web" {
		t.Errorf("pattern = %q, want %q", got, "web")
	}

	// A selection made against one inventory must not survive into another.
	m.key(tea.KeyMsg{Type: tea.KeySpace})
	m.step = stepInventory
	m.move(1)
	m.advance()
	if len(m.limit) != 0 {
		t.Errorf("changing inventory kept a stale limit: %v", m.limit)
	}
}

// TestWindow: a list taller than the panel scrolls to keep the cursor visible,
// and never returns more lines than the panel can hold.
func TestWindow(t *testing.T) {
	lines := make([]string, 30)
	for i := range lines {
		lines[i] = string(rune('a' + i%26))
	}

	if got := window(lines[:4], 0, 10); len(got) != 4 {
		t.Errorf("a short list should be returned whole, got %d lines", len(got))
	}
	for _, cursor := range []int{0, 15, 29} {
		got := window(lines, cursor, 10)
		if len(got) != 10 {
			t.Fatalf("cursor %d: got %d lines, want 10", cursor, len(got))
		}
		found := false
		for _, l := range got {
			if l == lines[cursor] {
				found = true
			}
		}
		if !found {
			t.Errorf("cursor %d scrolled out of view", cursor)
		}
	}
}

// TestDiscoverEmpty: launching outside a repository is not an error.
func TestDiscoverEmpty(t *testing.T) {
	repo := Discover(t.TempDir())
	if len(repo.Inventories) != 0 || len(repo.Playbooks) != 0 {
		t.Errorf("expected nothing found, got %+v", repo)
	}
}

func TestNeedsConfirm(t *testing.T) {
	cases := map[string]bool{
		"inventories/prod":       true,
		"inventories/production": true,
		"prod":                   true,
		"PROD":                   true,
		"inventories/dev":        false,
		"inventories/stage":      false,
		"inventories/internal":   false,
		"":                       false,
	}
	for inv, want := range cases {
		if got := needsConfirm(inv); got != want {
			t.Errorf("needsConfirm(%q) = %v, want %v", inv, got, want)
		}
	}
}

// TestHistoryRoundTrip checks the append-and-read path and the most-recent-per-
// playbook selection the overview depends on.
func TestHistoryRoundTrip(t *testing.T) {
	root := t.TempDir()

	if got := LoadRuns(root); got != nil {
		t.Errorf("empty history should read as nil, got %v", got)
	}

	runs := []Run{
		{TS: "2026-08-10T09:00:00Z", Playbook: "playbooks/site.yml", Inventory: "inventories/dev", Exit: 1, DurationMS: 1000},
		{TS: "2026-08-10T10:00:00Z", Playbook: "playbooks/audit.yml", Inventory: "inventories/dev", Exit: 0, DurationMS: 2000},
		{TS: "2026-08-10T11:00:00Z", Playbook: "playbooks/site.yml", Inventory: "inventories/prod", Exit: 0, DurationMS: 3000, Check: true},
	}
	for _, r := range runs {
		if err := AppendRun(root, r); err != nil {
			t.Fatal(err)
		}
	}

	got := LoadRuns(root)
	if !reflect.DeepEqual(got, runs) {
		t.Fatalf("round trip mismatch:\n got %+v\nwant %+v", got, runs)
	}

	latest := LatestPerPlaybook(got)
	if len(latest) != 2 {
		t.Fatalf("expected 2 playbooks, got %d", len(latest))
	}
	site := latest["playbooks/site.yml"]
	if site.TS != "2026-08-10T11:00:00Z" || !site.OK() || !site.Check {
		t.Errorf("latest site.yml wrong: %+v", site)
	}
}

// TestLoadRunsSkipsGarbage: a truncated final line must not hide the history
// written before it.
func TestLoadRunsSkipsGarbage(t *testing.T) {
	root := t.TempDir()
	if err := AppendRun(root, Run{TS: "2026-08-10T09:00:00Z", Playbook: "a.yml"}); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(historyPath(root), os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString("{\"ts\": truncated\n\n")
	f.Close()

	if got := LoadRuns(root); len(got) != 1 || got[0].Playbook != "a.yml" {
		t.Errorf("expected the one good record, got %+v", got)
	}
}

func TestLogPath(t *testing.T) {
	at := time.Date(2026, 8, 10, 12, 9, 53, 0, time.UTC)
	want := filepath.Join(stateDir, "logs", "20260810-120953-site.log")
	if got := logPath("playbooks/site.yml", at); got != want {
		t.Errorf("logPath = %q, want %q", got, want)
	}
}

// TestChildEnvLocale guards the locale workaround: ansible aborts at startup if
// any LC_* category names a locale the host has not generated.
func TestChildEnvLocale(t *testing.T) {
	got := childEnv([]string{"PATH=/usr/bin"})
	if len(got) != 2 || got[1] != "LC_ALL=C.UTF-8" {
		t.Errorf("expected a locale to be added, got %v", got)
	}

	// The failure this exists for: LANG is fine, but a per-category value names
	// a locale that was never generated. LC_ALL must still be pinned.
	got = childEnv([]string{"LANG=en_US.UTF-8", "LC_TIME=sl_SI.UTF-8"})
	if len(got) != 3 || got[2] != "LC_ALL=C.UTF-8" {
		t.Errorf("a broken LC_* category must still be overridden, got %v", got)
	}

	// An explicit LC_ALL is a deliberate choice and is left alone.
	explicit := []string{"PATH=/usr/bin", "LC_ALL=en_US.UTF-8"}
	if got := childEnv(explicit); !reflect.DeepEqual(got, explicit) {
		t.Errorf("explicit LC_ALL should be respected, got %v", got)
	}

	// An empty value is not a usable locale, so it is treated as unset.
	if got := childEnv([]string{"LC_ALL="}); len(got) != 2 {
		t.Errorf("empty LC_ALL should be treated as unset, got %v", got)
	}
}

func TestCommand(t *testing.T) {
	c := command("/repo", "inventories/prod", "playbooks/site.yml", true)
	want := []string{"ansible-playbook", "-i", "inventories/prod", "--check", "playbooks/site.yml"}
	if !reflect.DeepEqual(c.Args, want) {
		t.Errorf("args = %v, want %v", c.Args, want)
	}
	if c.Dir != "/repo" {
		t.Errorf("dir = %q, want /repo", c.Dir)
	}

	// No inventory means no -i, so ansible.cfg's default applies.
	c = command("/repo", "", "playbook.yml", false)
	if !reflect.DeepEqual(c.Args, []string{"ansible-playbook", "playbook.yml"}) {
		t.Errorf("args = %v, want no -i and no --check", c.Args)
	}
}

func TestParseHosts(t *testing.T) {
	raw := []byte(`{
		"_meta": {"hostvars": {"web1": {"ansible_host": "10.0.0.1"}}},
		"all": {"children": ["ungrouped", "web"]},
		"ungrouped": {},
		"web": {"hosts": ["web2", "web1"]},
		"db": {"hosts": ["db1"]}
	}`)

	got := parseHosts(raw)
	if len(got) != 2 {
		t.Fatalf("expected 2 groups with hosts, got %v", got)
	}
	if !strings.HasPrefix(got[0], "db") || !strings.Contains(got[0], "db1") {
		t.Errorf("first group = %q, want db", got[0])
	}
	// Hosts are sorted so the display is stable between refreshes.
	if !strings.Contains(got[1], "web1, web2") {
		t.Errorf("web hosts not sorted: %q", got[1])
	}

	if parseHosts([]byte("not json")) != nil {
		t.Error("invalid json should yield nil, not a panic")
	}
}

// TestStartEndToEnd drives the real exec/PTY/history path against a playbook
// that runs on localhost, so nothing is contacted over the network. This is the
// only check that proves output actually streams and a run gets recorded.
func TestStartEndToEnd(t *testing.T) {
	if _, err := exec.LookPath("ansible-playbook"); err != nil {
		t.Skip("ansible-playbook not installed")
	}

	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "playbook.yml"), []byte(`---
- hosts: localhost
  connection: local
  gather_facts: false
  tasks:
    - name: say something
      debug:
        msg: hello from the test
`), 0o644)
	os.WriteFile(filepath.Join(root, "inventory.yml"), []byte("---\nall:\n  hosts:\n    localhost:\n"), 0o644)
	// An empty ansible.cfg keeps any user-level config from reaching this run.
	os.WriteFile(filepath.Join(root, "ansible.cfg"), []byte("[defaults]\n"), 0o644)

	ch := make(chan any, 256)
	if _, err := Start(root, "inventory.yml", "playbook.yml", false, ch); err != nil {
		t.Fatal(err)
	}

	var out strings.Builder
	var done doneMsg
	deadline := time.After(90 * time.Second)
	for {
		select {
		case msg := <-ch:
			if chunk, ok := msg.(outputMsg); ok {
				out.Write(chunk)
				continue
			}
			done = msg.(doneMsg)
		case <-deadline:
			t.Fatalf("timed out; output so far:\n%s", out.String())
		}
		break
	}

	if done.err != nil {
		t.Errorf("history not recorded: %v", done.err)
	}
	if done.run.Exit != 0 {
		t.Errorf("exit = %d, want 0; output:\n%s", done.run.Exit, out.String())
	}
	if !strings.Contains(out.String(), "hello from the test") {
		t.Errorf("task output did not stream through; got:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "PLAY RECAP") {
		t.Errorf("run did not reach the recap; got:\n%s", out.String())
	}
	if done.run.DurationMS <= 0 {
		t.Errorf("duration not measured: %d", done.run.DurationMS)
	}

	recorded := LoadRuns(root)
	if len(recorded) != 1 || recorded[0].Playbook != "playbook.yml" {
		t.Fatalf("expected one recorded run, got %+v", recorded)
	}
	if body := readLog(root, recorded[0].Log); !strings.Contains(body, "PLAY RECAP") {
		t.Errorf("log file does not hold the output: %q", body)
	}
}

// TestPickerView walks the picker without a terminal: inventory, then playbook,
// then review. It guards the things a reader must never be wrong about — what is
// selected at each step, the exact command that will run, whether check mode is
// on, and that production cannot start without being typed out.
func TestPickerView(t *testing.T) {
	root := t.TempDir()
	for _, env := range []string{"dev", "prod"} {
		os.MkdirAll(filepath.Join(root, "inventories", env), 0o755)
	}
	writeBody(t, root, filepath.Join("inventories", "dev", "hosts.yml"),
		"---\n# dev = two virtual machines on this desk\nall:\n")
	writeBody(t, root, "playbooks/site.yml", "---\n# tui: fleet\n- name: Configure the fleet\n")
	AppendRun(root, Run{
		TS:       time.Now().UTC().Add(-2 * time.Hour).Format(time.RFC3339),
		Playbook: filepath.Join("playbooks", "site.yml"), Inventory: "inventories/dev",
		Exit: 0, DurationMS: 48210,
	})

	m := newModel(root)
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})

	// Step one lists inventories and what each one is, and nothing else.
	out := m.View()
	t.Logf("\n%s", out)
	for _, want := range []string{"INVENTORIES", "dev", "prod", "two virtual machines on this desk"} {
		if !strings.Contains(out, want) {
			t.Errorf("inventory step is missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "PLAYBOOKS") {
		t.Errorf("the playbook list should not be on the inventory step:\n%s", out)
	}

	// Step two lists playbooks, with their group and their own play name.
	m.advance()
	out = m.View()
	t.Logf("\n%s", out)
	for _, want := range []string{"PLAYBOOKS", "site", "fleet", "Configure the fleet"} {
		if !strings.Contains(out, want) {
			t.Errorf("playbook step is missing %q:\n%s", want, out)
		}
	}
	// The meta pane sits beside the list, describing whatever the cursor is on.
	for _, want := range []string{"THIS PLAYBOOK", "what it does", "group", "only on", "last run"} {
		if !strings.Contains(out, want) {
			t.Errorf("meta pane is missing %q:\n%s", want, out)
		}
	}

	// Step three narrows the run, and defaults to narrowing nothing.
	m.advance()
	out = m.View()
	t.Logf("\n%s", out)
	if !strings.Contains(out, "[x] every host") {
		t.Errorf("the default is not shown as the chosen row:\n%s", out)
	}
	if strings.Contains(m.commandLine(), "--limit") {
		t.Errorf("an unpicked hosts step still limited the run: %q", m.commandLine())
	}

	// Step four states every choice and the command they add up to.
	m.advance()
	out = m.View()
	t.Logf("\n%s", out)
	for _, want := range []string{"REVIEW", "two virtual machines on this desk", "Configure the fleet", "last", "2h"} {
		if !strings.Contains(out, want) {
			t.Errorf("review is missing %q:\n%s", want, out)
		}
	}
	// Dry-run is the resting state, and it is in the command and on the bar.
	if !m.check {
		t.Error("a fresh model should start in dry-run")
	}
	if !strings.Contains(out, "ansible-playbook -i inventories/dev --check playbooks/site.yml") {
		t.Errorf("the command about to run is not shown verbatim:\n%s", out)
	}
	if !strings.Contains(out, "DRY RUN") {
		t.Errorf("dry-run is not visible on the bar:\n%s", out)
	}

	// Off it comes on a non-production inventory, and the command follows.
	m.key(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("c")})
	if out := m.View(); strings.Contains(out, "--check") || strings.Contains(out, "DRY RUN") {
		t.Errorf("dry-run should be off on dev after c:\n%s", out)
	}
	m.key(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("c")})

	// esc walks back rather than out, and the earlier choice survives.
	m.key(tea.KeyMsg{Type: tea.KeyEsc})
	m.key(tea.KeyMsg{Type: tea.KeyEsc})
	m.key(tea.KeyMsg{Type: tea.KeyEsc})
	if m.step != stepInventory {
		t.Fatalf("esc did not return to the first step, got step %d", m.step)
	}
	if out := m.View(); !strings.Contains(out, "INVENTORIES") {
		t.Errorf("expected the inventory step after escaping back:\n%s", out)
	}

	// Selecting production is announced from the step it is chosen on, and must
	// be typed out before anything starts.
	m.move(1)
	if m.selectedInventory().Path != filepath.Join("inventories", "prod") {
		t.Fatalf("expected prod selected, got %q", m.selectedInventory().Path)
	}
	m.advance()
	if out := m.View(); !strings.Contains(out, "PRODUCTION") {
		t.Errorf("production is not announced on the playbook step:\n%s", out)
	}
	m.advance()

	// Production cannot leave dry-run, however many times it is asked.
	m.key(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("c")})
	m.key(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("c")})
	if !m.check {
		t.Fatal("dry-run was turned off on a production inventory")
	}
	if out := m.View(); !strings.Contains(out, "LOCKED") {
		t.Errorf("the production dry-run lock is not stated:\n%s", out)
	}
	if !strings.Contains(m.commandLine(), "--check") {
		t.Errorf("a production command without --check: %q", m.commandLine())
	}

	m.advance() // review
	m.advance() // run
	if !m.asking {
		t.Fatal("production run started without asking for confirmation")
	}
	if !m.typing {
		t.Error("a playbook that changes things must have the word typed out")
	}
	if out := m.View(); !strings.Contains(out, "PRODUCTION") || !strings.Contains(out, "esc to cancel") {
		t.Errorf("confirmation prompt not rendered:\n%s", out)
	}

	// A wrong answer clears the field instead of running.
	m.confirmKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})
	m.confirmKey(tea.KeyMsg{Type: tea.KeyEnter})
	if m.confirm != "" || !m.asking || m.view != viewPick {
		t.Error("a wrong confirmation should clear the field and stay put")
	}
}
func TestIgnoreState(t *testing.T) {
	root := t.TempDir()

	// No .gitignore: nothing to do, and no file invented.
	ignoreState(root)
	if _, err := os.Stat(filepath.Join(root, ".gitignore")); !os.IsNotExist(err) {
		t.Error("should not create .gitignore where none exists")
	}

	path := filepath.Join(root, ".gitignore")
	os.WriteFile(path, []byte("*.log"), 0o644)
	ignoreState(root)
	body, _ := os.ReadFile(path)
	if !strings.Contains(string(body), stateDir+"/") {
		t.Errorf("state dir not ignored: %q", body)
	}
	if !strings.HasPrefix(string(body), "*.log\n") {
		t.Errorf("existing content mangled: %q", body)
	}

	// Running twice must not add the entry twice.
	ignoreState(root)
	again, _ := os.ReadFile(path)
	if strings.Count(string(again), stateDir) != 1 {
		t.Errorf("entry duplicated: %q", again)
	}
}
