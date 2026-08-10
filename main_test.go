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
	if !reflect.DeepEqual(repo.Inventories, wantInv) {
		t.Errorf("inventories = %v, want %v", repo.Inventories, wantInv)
	}

	wantPb := []string{
		filepath.Join("playbooks", "audit.yml"),
		filepath.Join("playbooks", "site.yml"),
	}
	if !reflect.DeepEqual(repo.Playbooks, wantPb) {
		t.Errorf("playbooks = %v, want %v", repo.Playbooks, wantPb)
	}
}

// TestDiscoverFlatLayout covers a small repository with no inventories/ and no
// playbooks/ directory, everything sitting at the root.
func TestDiscoverFlatLayout(t *testing.T) {
	root := t.TempDir()
	write(t, root, "playbook.yml")
	write(t, root, "inventory.yml")

	repo := Discover(root)

	if !reflect.DeepEqual(repo.Inventories, []string{"inventory.yml"}) {
		t.Errorf("inventories = %v, want [inventory.yml]", repo.Inventories)
	}
	if !reflect.DeepEqual(repo.Playbooks, []string{"playbook.yml"}) {
		t.Errorf("playbooks = %v, want [playbook.yml]", repo.Playbooks)
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

// TestPickerView renders the first screen without a terminal. It guards the
// two things a reader of the picker must never be wrong about: the exact
// command that will run, and whether check mode is on.
func TestPickerView(t *testing.T) {
	root := t.TempDir()
	for _, env := range []string{"dev", "prod"} {
		os.MkdirAll(filepath.Join(root, "inventories", env), 0o755)
	}
	write(t, root, "playbooks/site.yml")
	AppendRun(root, Run{
		TS:       time.Now().UTC().Add(-2 * time.Hour).Format(time.RFC3339),
		Playbook: filepath.Join("playbooks", "site.yml"), Inventory: "inventories/dev",
		Exit: 0, DurationMS: 48210,
	})

	m := newModel(root)
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})

	out := m.View()
	t.Logf("\n%s", out)

	for _, want := range []string{"INVENTORIES", "PLAYBOOKS", "dev", "prod", "site", "last", "2h"} {
		if !strings.Contains(out, want) {
			t.Errorf("picker is missing %q:\n%s", want, out)
		}
	}
	if !strings.Contains(out, "ansible-playbook -i inventories/dev playbooks/site.yml") {
		t.Errorf("the command about to run is not shown verbatim:\n%s", out)
	}

	m.check = true
	if out := m.View(); !strings.Contains(out, "--check") || !strings.Contains(out, "CHECK MODE") {
		t.Errorf("check mode not visible in the command or the status:\n%s", out)
	}

	// Selecting production must ask before anything starts.
	m.focus = colInventories
	m.move(1)
	if m.selectedInventory() != filepath.Join("inventories", "prod") {
		t.Fatalf("expected prod selected, got %q", m.selectedInventory())
	}
	m.launch()
	if !m.asking {
		t.Fatal("production run started without asking for confirmation")
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
