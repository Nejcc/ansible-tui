package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// stateDir holds run history and per-run logs inside the target repository.
const stateDir = ".ansible-tui"

// Run is one line of runs.jsonl. TS is RFC3339 in UTC so that lexical order and
// chronological order are the same thing, which is what lets LatestPerPlaybook
// compare timestamps as plain strings.
type Run struct {
	TS         string `json:"ts"`
	Playbook   string `json:"playbook"`
	Inventory  string `json:"inventory"`
	Check      bool   `json:"check"`
	Exit       int    `json:"exit"`
	DurationMS int64  `json:"duration_ms"`
	Log        string `json:"log"`
}

func (r Run) When() time.Time {
	t, _ := time.Parse(time.RFC3339, r.TS)
	return t
}

func (r Run) OK() bool { return r.Exit == 0 }

func historyPath(root string) string {
	return filepath.Join(root, stateDir, "runs.jsonl")
}

// logPath names a log file for a run starting at now. Collisions within the
// same second for the same playbook are possible and harmless: the second run
// appends to the first one's log.
func logPath(playbook string, now time.Time) string {
	base := filepath.Base(playbook)
	base = strings.TrimSuffix(base, filepath.Ext(base))
	return filepath.Join(stateDir, "logs", now.UTC().Format("20060102-150405")+"-"+base+".log")
}

// AppendRun records one run. Append-only JSONL, not a database: the reader is a
// single process scanning a file that grows by one short line per deploy.
func AppendRun(root string, r Run) error {
	path := historyPath(root)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	return json.NewEncoder(f).Encode(r)
}

// LoadRuns returns every recorded run, oldest first. A missing file means no
// runs yet, which is not an error. Unparseable lines are skipped rather than
// failing the read: a truncated final line must not hide the whole history.
func LoadRuns(root string) []Run {
	f, err := os.Open(historyPath(root))
	if err != nil {
		return nil
	}
	defer f.Close()

	var out []Run
	s := bufio.NewScanner(f)
	s.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for s.Scan() {
		line := bytes.TrimSpace(s.Bytes())
		if len(line) == 0 {
			continue
		}
		var r Run
		if json.Unmarshal(line, &r) == nil {
			out = append(out, r)
		}
	}
	return out
}

// LatestPerPlaybook keeps only the most recent run of each playbook, which is
// what the overview shows.
func LatestPerPlaybook(runs []Run) map[string]Run {
	out := make(map[string]Run, len(runs))
	for _, r := range runs {
		if prev, ok := out[r.Playbook]; !ok || r.TS >= prev.TS {
			out[r.Playbook] = r
		}
	}
	return out
}

// ignoreState adds stateDir to the repository's existing .gitignore, so the run
// history this program writes does not turn up as untracked noise in someone
// else's git status. It never creates a .gitignore: this program writes into
// repositories it does not own, and inventing tracked files there is a bigger
// intrusion than the untracked directory it would hide.
func ignoreState(root string) {
	path := filepath.Join(root, ".gitignore")
	body, err := os.ReadFile(path)
	if err != nil {
		return
	}
	entry := stateDir + "/"
	for _, line := range strings.Split(string(body), "\n") {
		if strings.TrimSpace(line) == entry || strings.TrimSpace(line) == stateDir {
			return
		}
	}
	if len(body) > 0 && !bytes.HasSuffix(body, []byte("\n")) {
		body = append(body, '\n')
	}
	os.WriteFile(path, append(body, []byte(entry+"\n")...), 0o644)
}
