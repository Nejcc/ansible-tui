package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/creack/pty"
)

// outputMsg is a chunk of child output on its way to the viewport.
type outputMsg []byte

// doneMsg ends a run. Err is set only when the run could not be started or
// recorded; a playbook exiting non-zero is a normal outcome carried in Run.Exit.
type doneMsg struct {
	run Run
	err error
}

// Runner executes one ansible-playbook invocation. Ansible does the work; this
// only chooses the arguments, keeps a terminal on the other end, and watches
// the exit status.
type Runner struct {
	cmd   *exec.Cmd
	tty   *os.File
	log   *os.File
	start time.Time
	spec  Run
}

// command builds the invocation. The working directory is the repository root
// so the repository's own ansible.cfg applies: vault_password_file, roles_path,
// callbacks_enabled, privilege escalation. Nothing ansible.cfg already sets is
// repeated here.
func command(root, inventory, playbook string, check bool) *exec.Cmd {
	args := make([]string, 0, 4)
	if inventory != "" {
		args = append(args, "-i", inventory)
	}
	if check {
		args = append(args, "--check")
	}
	args = append(args, playbook)

	c := exec.Command("ansible-playbook", args...)
	c.Dir = root
	c.Env = childEnv(os.Environ())
	return c
}

// childEnv inherits the environment but pins LC_ALL unless the caller set it
// deliberately.
//
// Ansible calls setlocale(LC_ALL, "") at startup and aborts with "could not
// initialize the preferred locale: unsupported locale setting" if any LC_*
// category names a locale the host has not generated. Desktop sessions set
// per-category values such as LC_TIME to a regional locale that is often
// missing from a minimal install, so checking LANG alone is not enough — the
// broken value hides in a category nobody thinks to look at.
//
// LC_ALL overrides every category, and C.UTF-8 exists wherever glibc does, so
// setting it makes the run independent of how the session was configured. An
// explicit LC_ALL is left alone: that is someone choosing on purpose.
func childEnv(env []string) []string {
	const key = "LC_ALL="
	for _, e := range env {
		if strings.HasPrefix(e, key) && len(e) > len(key) {
			return env
		}
	}
	return append(env, key+"C.UTF-8")
}

// Start launches the playbook under a pseudo-terminal and streams its output to
// out. The PTY is not decoration: without one ansible sees a pipe, disables
// colour and buffers, so the pane would stay blank until the run finished.
func Start(root, inventory, playbook string, check bool, out chan<- any) (*Runner, error) {
	now := time.Now()
	rel := logPath(playbook, now)

	r := &Runner{
		cmd:   command(root, inventory, playbook, check),
		start: now,
		spec: Run{
			TS:        now.UTC().Format(time.RFC3339),
			Playbook:  playbook,
			Inventory: inventory,
			Check:     check,
			Log:       rel,
		},
	}

	// A run that cannot be logged still has to run, so log failures downgrade to
	// no log file rather than aborting.
	if err := os.MkdirAll(filepath.Join(root, filepath.Dir(rel)), 0o755); err == nil {
		r.log, _ = os.Create(filepath.Join(root, rel))
	}
	if r.log == nil {
		r.spec.Log = ""
	}

	tty, err := pty.Start(r.cmd)
	if err != nil {
		if r.log != nil {
			r.log.Close()
		}
		return nil, err
	}
	r.tty = tty

	go r.pump(root, out)
	return r, nil
}

// pump forwards child output until the PTY closes, then waits for the exit
// status and records the run.
func (r *Runner) pump(root string, out chan<- any) {
	buf := make([]byte, 4096)
	for {
		n, err := r.tty.Read(buf)
		if n > 0 {
			chunk := make([]byte, n)
			copy(chunk, buf[:n])
			if r.log != nil {
				r.log.Write(chunk)
			}
			out <- outputMsg(chunk)
		}
		if err != nil {
			// On Linux the master side returns EIO rather than EOF once the
			// child's last file descriptor closes. Both mean the same thing.
			break
		}
	}

	err := r.cmd.Wait()
	if r.log != nil {
		r.log.Close()
	}
	r.tty.Close()

	run := r.spec
	run.DurationMS = time.Since(r.start).Milliseconds()
	run.Exit = exitCode(err)

	out <- doneMsg{run: run, err: AppendRun(root, run)}
}

// exitCode maps Wait's result to a status. A signalled child (the Cancel path)
// reports 130, the shell convention for an interrupt.
func exitCode(err error) int {
	if err == nil {
		return 0
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		if status, ok := ee.Sys().(syscall.WaitStatus); ok && status.Signaled() {
			return 130
		}
		return ee.ExitCode()
	}
	return 1
}

// Cancel interrupts the playbook without touching this program. pty.Start puts
// the child in its own session, so signalling the negated pid reaches ansible
// and every task it forked.
func (r *Runner) Cancel() {
	if r == nil || r.cmd.Process == nil {
		return
	}
	if err := syscall.Kill(-r.cmd.Process.Pid, syscall.SIGINT); err != nil {
		r.cmd.Process.Kill()
	}
}

// readLog returns a past run's captured output for redisplay.
func readLog(root, rel string) string {
	if rel == "" {
		return "(no log recorded for this run)"
	}
	b, err := os.ReadFile(filepath.Join(root, rel))
	if err != nil {
		return "(log unavailable: " + err.Error() + ")"
	}
	if len(b) == 0 {
		return "(log is empty)"
	}
	return string(b)
}
