package jobs

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/creack/pty"
	"golang.org/x/sys/unix"

	"lazycmd/internal/config"
)

type Status int

const (
	StatusRunning Status = iota
	StatusSucceeded
	StatusFailed
)

func (s Status) String() string {
	switch s {
	case StatusRunning:
		return "running"
	case StatusSucceeded:
		return "succeeded"
	case StatusFailed:
		return "failed"
	default:
		return "unknown"
	}
}

// Job is a single PTY-backed run of a command. Output streams into
// Buffer from a reader goroutine; Status/ExitCode/Duration are read
// through Snapshot so they're safe to poll from the UI's event loop.
type Job struct {
	ID   string
	Name string

	Buffer *OutputBuffer

	mu        sync.Mutex
	status    Status
	exitCode  int
	startedAt time.Time
	endedAt   time.Time
	closed    bool // ptmx has been closed; Write/EchoEnabled must not touch it once set

	ptmx    *os.File
	cmd     *exec.Cmd
	logFile *os.File

	// LogName is the job's log file in stateDir/logs ("" if none could
	// be made). history records it, finished, for the history view.
	LogName  string
	history  historyRun
	stateDir string
	past     bool // loaded from history: no process, nothing to stop
}

// Start launches resolved as a PTY-backed job and begins streaming its
// output. Param values reach the process as LC_<NAME> env vars, never
// substituted into a command line. Output is also teed to a log file
// under stateDir/logs.
func Start(id string, resolved config.ResolvedCommand, values map[string]string, stateDir string) (*Job, error) {
	return StartSized(id, resolved, values, stateDir, 0, 0)
}

// StartSized is Start with the PTY given a size up front, so a program
// that formats to its terminal's width (or checks it has one) sees the
// output panel's real size rather than 0x0. Zero means unsized. Panel
// jobs also get LC_PANEL=1, so a script can tell it isn't in a real
// terminal and choose a simpler mode.
func StartSized(id string, resolved config.ResolvedCommand, values map[string]string, stateDir string, cols, rows int, env ...string) (*Job, error) {
	execCmd, err := BuildCmd(resolved, values)
	if err != nil {
		return nil, err
	}
	execCmd.Env = append(execCmd.Env, "LC_PANEL=1")
	execCmd.Env = append(execCmd.Env, env...)

	var ptmx *os.File
	if ws := winsize(cols, rows); ws != nil {
		ptmx, err = pty.StartWithSize(execCmd, ws)
	} else {
		ptmx, err = pty.Start(execCmd)
	}
	if err != nil {
		return nil, err
	}

	j := &Job{
		ID:        id,
		Name:      resolved.Name,
		Buffer:    &OutputBuffer{},
		status:    StatusRunning,
		startedAt: time.Now(),
		ptmx:      ptmx,
		cmd:       execCmd,
	}

	// A log holds everything the command printed, tokens included, so
	// only the user can read it. The start time keeps logs from earlier
	// sessions apart: job ids start again from 1 each time.
	logName := j.startedAt.Format("20060102-150405") + "-" + logFileName(id)
	logPath := filepath.Join(stateDir, "logs", logName)
	if err := os.MkdirAll(filepath.Dir(logPath), 0o700); err == nil {
		if f, err := os.OpenFile(logPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600); err == nil {
			j.logFile = f
			j.LogName = logName
		}
	}
	repo, _ := os.Getwd() // jobs run in lazycmd's own folder, the repo
	j.stateDir = stateDir
	j.history = historyRun{CmdID: resolved.ID, Name: resolved.Name, Repo: repo, Log: j.LogName, Started: j.startedAt}

	startedMu.Lock()
	started[j] = true
	running.Add(1)
	startedMu.Unlock()
	go j.readLoop()
	return j, nil
}

// started holds the jobs whose output is still being read, for StopAll.
var (
	startedMu sync.Mutex
	started   = map[*Job]bool{}
	running   sync.WaitGroup
)

// StopAll stops every job still running and waits up to timeout for all
// of them to finish writing their output, log and history. It reports
// whether they did. For tests: a job left running must not still be
// writing into a temp folder that is being removed.
func StopAll(timeout time.Duration) bool {
	startedMu.Lock()
	for j := range started {
		j.Kill()
	}
	startedMu.Unlock()
	done := make(chan struct{})
	go func() { running.Wait(); close(done) }()
	select {
	case <-done:
		return true
	case <-time.After(timeout):
		return false
	}
}

// logFileName turns a job id into a file name that stays inside the
// logs folder. The id starts with the command's name, which can come
// from an untrusted repo (a commands.yaml key, a package.json script)
// and so may hold "/" or "..": "../../x" must not become a path. Every
// character other than letters, digits, '.', '_' and '-' becomes '_',
// and the result is capped so an over-long name can't exceed the
// file system's limit.
func logFileName(id string) string {
	const maxLen = 120
	var b strings.Builder
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-':
			b.WriteRune(r)
		case r == '.' && b.Len() > 0:
			b.WriteRune(r) // never a leading dot: no hidden files, no ".."
		default:
			b.WriteByte('_')
		}
	}
	name := b.String()
	if len(name) > maxLen {
		// Keep the end: it carries the "-<seq>" that keeps names apart.
		name = name[len(name)-maxLen:]
	}
	return name + ".log"
}

// BuildCmd builds the *exec.Cmd for resolved with values injected as
// LC_<NAME> env vars. Exported so a caller that needs the real terminal
// directly (tea.ExecProcess, for a full-screen program like vim or
// less) can build the same command without going through a PTY job.
func BuildCmd(resolved config.ResolvedCommand, values map[string]string) (*exec.Cmd, error) {
	var cmd *exec.Cmd
	switch {
	case resolved.ScriptPath != "":
		cmd = exec.Command(resolved.ScriptPath, config.ExpandArgs(resolved.Args, values)...)
	case resolved.Run != "":
		cmd = exec.Command("sh", "-c", resolved.Run)
	default:
		return nil, fmt.Errorf("command %q has neither a script nor a run body", resolved.Name)
	}
	cmd.Env = append(os.Environ(), paramEnv(keepEnvSecrets(resolved, values))...)
	return cmd, nil
}

// keepEnvSecrets drops each secret left empty whose LC_<NAME> is already
// set in lazycmd's own environment, so the command gets that value
// rather than an empty one. It's how a secret reaches a command without
// being typed: `LC_TOKEN=... lc deploy.sh` (in CI, say), since a secret
// can't be given as an argument.
func keepEnvSecrets(resolved config.ResolvedCommand, values map[string]string) map[string]string {
	var out map[string]string
	for _, p := range resolved.Params {
		if p.Type != config.ParamSecret || values[p.Name] != "" || os.Getenv("LC_"+strings.ToUpper(p.Name)) == "" {
			continue
		}
		if out == nil {
			out = make(map[string]string, len(values))
			for k, v := range values {
				out[k] = v
			}
		}
		delete(out, p.Name)
	}
	if out == nil {
		return values
	}
	return out
}

func paramEnv(values map[string]string) []string {
	env := make([]string, 0, len(values))
	for name, value := range values {
		env = append(env, "LC_"+strings.ToUpper(name)+"="+value)
	}
	return env
}

func (j *Job) readLoop() {
	defer func() {
		startedMu.Lock()
		delete(started, j)
		startedMu.Unlock()
		running.Done()
	}()
	buf := make([]byte, 4096)
	var queries queryScanner
	for {
		// Unsynchronized against Write/EchoEnabled is fine here: Read and
		// Write pull in independent directions on the same fd, and this
		// loop only ever races the one thing that actually matters -
		// Close, below, which does take the lock.
		n, err := j.ptmx.Read(buf)
		if n > 0 {
			chunk := buf[:n]
			if reply := queries.scan(chunk); len(reply) > 0 {
				j.Write(reply) // see queries.go
			}
			j.Buffer.Write(chunk)
			if j.logFile != nil {
				j.logFile.Write(chunk)
			}
		}
		if err != nil {
			break
		}
	}

	j.mu.Lock()
	j.closed = true
	j.ptmx.Close()
	j.mu.Unlock()

	if j.logFile != nil {
		j.logFile.Close()
	}

	waitErr := j.cmd.Wait()

	ended := time.Now()
	status, exitCode := StatusSucceeded, 0
	if waitErr != nil {
		status, exitCode = StatusFailed, -1
		if exitErr, ok := waitErr.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		}
	}
	// Recorded before the job shows as finished, so whoever sees it
	// finished also finds it in the history.
	if j.history.Log != "" {
		run := j.history
		run.Status, run.ExitCode, run.DurationMS = status.String(), exitCode, ended.Sub(j.startedAt).Milliseconds()
		appendHistory(j.stateDir, run) // best effort
	}

	j.mu.Lock()
	j.endedAt, j.status, j.exitCode = ended, status, exitCode
	j.mu.Unlock()

	j.Buffer.Touch()
}

// Snapshot returns the job's current status, exit code and run duration.
func (j *Job) Snapshot() (status Status, exitCode int, duration time.Duration) {
	j.mu.Lock()
	defer j.mu.Unlock()
	end := j.endedAt
	if end.IsZero() {
		end = time.Now()
	}
	return j.status, j.exitCode, end.Sub(j.startedAt)
}

// Kill terminates the job's process. Used when the app is shutting down
// with jobs still running in the background.
func (j *Job) Kill() {
	if j.cmd != nil && j.cmd.Process != nil {
		_ = j.cmd.Process.Kill()
	}
}

// Resize tells the job's PTY its new size, which also sends the program
// SIGWINCH so it can redraw. A no-op once the job has finished.
func (j *Job) Resize(cols, rows int) {
	ws := winsize(cols, rows)
	if ws == nil {
		return
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	if !j.closed {
		_ = pty.Setsize(j.ptmx, ws)
	}
}

func winsize(cols, rows int) *pty.Winsize {
	if cols <= 0 || rows <= 0 || cols > 0xffff || rows > 0xffff {
		return nil
	}
	return &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows)}
}

// Write sends keystrokes to the job's PTY, i.e. to the running
// program's stdin, for focus mode and for answering a detected prompt.
// A no-op once the job has finished and closed its PTY - racing that
// close is exactly what the lock here prevents.
func (j *Job) Write(p []byte) (int, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.closed {
		return 0, os.ErrClosed
	}
	return j.ptmx.Write(p)
}

// EchoEnabled reports whether the PTY currently has local echo on. A
// program turns it off right before a password prompt, which is what
// lets the UI mask the popup for that prompt without the program ever
// telling us explicitly that it's a password.
func (j *Job) EchoEnabled() bool {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.closed {
		return true
	}
	term, err := unix.IoctlGetTermios(int(j.ptmx.Fd()), ioctlGetTermios)
	if err != nil {
		return true
	}
	return term.Lflag&unix.ECHO != 0
}
