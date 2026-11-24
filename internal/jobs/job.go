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

	"lazycast/internal/config"
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
	Mode config.Mode

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
}

// Start launches resolved as a PTY-backed job and begins streaming its
// output. Param values reach the process as LC_<NAME> env vars, never
// substituted into a command line. Output is also teed to a log file
// under stateDir/logs.
func Start(id string, resolved config.ResolvedCommand, values map[string]string, stateDir string) (*Job, error) {
	execCmd, err := BuildCmd(resolved, values)
	if err != nil {
		return nil, err
	}

	ptmx, err := pty.Start(execCmd)
	if err != nil {
		return nil, err
	}

	j := &Job{
		ID:        id,
		Name:      resolved.Name,
		Mode:      resolved.Mode,
		Buffer:    &OutputBuffer{},
		status:    StatusRunning,
		startedAt: time.Now(),
		ptmx:      ptmx,
		cmd:       execCmd,
	}

	logPath := filepath.Join(stateDir, "logs", id+".log")
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err == nil {
		if f, err := os.Create(logPath); err == nil {
			j.logFile = f
		}
	}

	go j.readLoop()
	return j, nil
}

// BuildCmd builds the *exec.Cmd for resolved with values injected as
// LC_<NAME> env vars. Exported so a caller that needs the real terminal
// directly (tea.ExecProcess, for a full-screen program like vim or
// less) can build the same command without going through a PTY job.
func BuildCmd(resolved config.ResolvedCommand, values map[string]string) (*exec.Cmd, error) {
	var cmd *exec.Cmd
	switch {
	case resolved.ScriptPath != "":
		cmd = exec.Command(resolved.ScriptPath)
	case resolved.Run != "":
		cmd = exec.Command("sh", "-c", resolved.Run)
	default:
		return nil, fmt.Errorf("command %q has neither a script nor a run body", resolved.Name)
	}
	cmd.Env = append(os.Environ(), paramEnv(values)...)
	return cmd, nil
}

func paramEnv(values map[string]string) []string {
	env := make([]string, 0, len(values))
	for name, value := range values {
		env = append(env, "LC_"+strings.ToUpper(name)+"="+value)
	}
	return env
}

func (j *Job) readLoop() {
	buf := make([]byte, 4096)
	for {
		// Unsynchronized against Write/EchoEnabled is fine here: Read and
		// Write pull in independent directions on the same fd, and this
		// loop only ever races the one thing that actually matters -
		// Close, below, which does take the lock.
		n, err := j.ptmx.Read(buf)
		if n > 0 {
			chunk := buf[:n]
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

	j.mu.Lock()
	j.endedAt = time.Now()
	if waitErr != nil {
		j.status = StatusFailed
		if exitErr, ok := waitErr.(*exec.ExitError); ok {
			j.exitCode = exitErr.ExitCode()
		} else {
			j.exitCode = -1
		}
	} else {
		j.status = StatusSucceeded
	}
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
	if j.cmd.Process != nil {
		_ = j.cmd.Process.Kill()
	}
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
