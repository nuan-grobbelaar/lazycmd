package jobs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lazycmd/internal/config"
)

func writeExecutable(t *testing.T, path, script string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}

func waitForStatus(t *testing.T, j *Job, want Status, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if status, _, _ := j.Snapshot(); status == want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	status, exitCode, _ := j.Snapshot()
	t.Fatalf("timed out waiting for status %v, got %v (exit %d)", want, status, exitCode)
}

func TestBuildCmdRequiresRunOrScript(t *testing.T) {
	_, err := BuildCmd(config.ResolvedCommand{Name: "nothing"}, nil)
	if err == nil {
		t.Fatal("expected an error for a command with neither run nor script")
	}
}

func TestBuildCmdInjectsParamsAsEnvVars(t *testing.T) {
	cmd, err := BuildCmd(config.ResolvedCommand{Name: "x", Run: "echo hi"}, map[string]string{"service": "api", "dry_run": "true"})
	if err != nil {
		t.Fatal(err)
	}
	var sawService, sawDryRun bool
	for _, e := range cmd.Env {
		if e == "LC_SERVICE=api" {
			sawService = true
		}
		if e == "LC_DRY_RUN=true" {
			sawDryRun = true
		}
	}
	if !sawService || !sawDryRun {
		t.Errorf("env = %v, missing LC_SERVICE/LC_DRY_RUN", cmd.Env)
	}
}

func TestStartRunsToSuccess(t *testing.T) {
	root := t.TempDir()
	writeExecutable(t, filepath.Join(root, "ok.sh"), "#!/bin/sh\necho hello\n")

	j, err := Start("test-ok", config.ResolvedCommand{Name: "ok", ScriptPath: filepath.Join(root, "ok.sh")}, nil, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	waitForStatus(t, j, StatusSucceeded, 2*time.Second)

	content, _ := j.Buffer.Snapshot()
	if content != "hello\n" {
		t.Errorf("output = %q, want %q", content, "hello\n")
	}
	_, exitCode, duration := j.Snapshot()
	if exitCode != 0 {
		t.Errorf("exitCode = %d, want 0", exitCode)
	}
	if duration <= 0 {
		t.Errorf("duration = %v, want > 0", duration)
	}
}

func TestStartRunsToFailureWithExitCode(t *testing.T) {
	root := t.TempDir()
	writeExecutable(t, filepath.Join(root, "bad.sh"), "#!/bin/sh\nexit 7\n")

	j, err := Start("test-bad", config.ResolvedCommand{Name: "bad", ScriptPath: filepath.Join(root, "bad.sh")}, nil, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	waitForStatus(t, j, StatusFailed, 2*time.Second)

	_, exitCode, _ := j.Snapshot()
	if exitCode != 7 {
		t.Errorf("exitCode = %d, want 7", exitCode)
	}
}

func TestStartTeesOutputToLogFile(t *testing.T) {
	root := t.TempDir()
	writeExecutable(t, filepath.Join(root, "ok.sh"), "#!/bin/sh\necho logged\n")
	stateDir := t.TempDir()

	j, err := Start("tee-test", config.ResolvedCommand{Name: "ok", ScriptPath: filepath.Join(root, "ok.sh")}, nil, stateDir)
	if err != nil {
		t.Fatal(err)
	}
	waitForStatus(t, j, StatusSucceeded, 2*time.Second)

	data, err := os.ReadFile(filepath.Join(stateDir, "logs", "tee-test.log"))
	if err != nil {
		t.Fatalf("log file: %v", err)
	}
	// The log is a raw tee straight from the PTY, before OutputBuffer's
	// CRLF normalization - a PTY in cooked mode turns the script's "\n"
	// into "\r\n", so that's what should land on disk too.
	if string(data) != "logged\r\n" {
		t.Errorf("log content = %q, want %q", data, "logged\r\n")
	}
}

func TestWriteSendsInputToTheProcess(t *testing.T) {
	root := t.TempDir()
	writeExecutable(t, filepath.Join(root, "echo.sh"), "#!/bin/sh\nread line\necho \"got: $line\"\n")

	j, err := Start("write-test", config.ResolvedCommand{Name: "echo", ScriptPath: filepath.Join(root, "echo.sh")}, nil, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	time.Sleep(100 * time.Millisecond) // let the script reach its `read` before we answer it
	if _, err := j.Write([]byte("hello\r")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	waitForStatus(t, j, StatusSucceeded, 2*time.Second)

	content, _ := j.Buffer.Snapshot()
	if !strings.Contains(content, "got: hello") {
		t.Errorf("output = %q, want it to contain %q", content, "got: hello")
	}
}

func TestEchoEnabledReflectsSttyEcho(t *testing.T) {
	root := t.TempDir()
	writeExecutable(t, filepath.Join(root, "secret.sh"), "#!/bin/sh\nstty -echo\nread pw\nstty echo\n")

	j, err := Start("echo-test", config.ResolvedCommand{Name: "secret", ScriptPath: filepath.Join(root, "secret.sh")}, nil, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { j.Kill() })

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if !j.EchoEnabled() {
			return // observed echo off, as expected
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("expected EchoEnabled to report false once the script disabled it")
}

func TestKillTerminatesARunningJob(t *testing.T) {
	root := t.TempDir()
	writeExecutable(t, filepath.Join(root, "slow.sh"), "#!/bin/sh\nsleep 30\n")

	j, err := Start("kill-test", config.ResolvedCommand{Name: "slow", ScriptPath: filepath.Join(root, "slow.sh")}, nil, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	j.Kill()
	waitForStatus(t, j, StatusFailed, 2*time.Second)
}

func TestWriteAfterCloseIsSafeNotAPanic(t *testing.T) {
	root := t.TempDir()
	writeExecutable(t, filepath.Join(root, "ok.sh"), "#!/bin/sh\necho hi\n")

	j, err := Start("closed-write-test", config.ResolvedCommand{Name: "ok", ScriptPath: filepath.Join(root, "ok.sh")}, nil, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	waitForStatus(t, j, StatusSucceeded, 2*time.Second)

	// The PTY is closed by now; Write/EchoEnabled must no-op, not panic
	// or race with readLoop's Close (this is what go test -race checks).
	if _, err := j.Write([]byte("x")); err == nil {
		t.Error("expected an error writing to a closed job, got nil")
	}
	if !j.EchoEnabled() {
		t.Error("expected EchoEnabled to default to true once closed")
	}
}

func TestStatusString(t *testing.T) {
	cases := map[Status]string{
		StatusRunning:   "running",
		StatusSucceeded: "succeeded",
		StatusFailed:    "failed",
		Status(99):      "unknown",
	}
	for status, want := range cases {
		if got := status.String(); got != want {
			t.Errorf("Status(%d).String() = %q, want %q", status, got, want)
		}
	}
}

func TestJobOutputNeverContainsScreenControl(t *testing.T) {
	cmd := config.ResolvedCommand{
		Name: "noisy",
		Run:  `printf '\033[2J\033[3J\033[H\033[1;32mvite\033[0m ready\tin 5ms\n\033[1A\033[2K\033]0;title\007\033[?25l done\n'`,
	}
	j, err := Start("noisy", cmd, nil, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if status, _, _ := j.Snapshot(); status != StatusRunning {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	got, _ := j.Buffer.Snapshot()
	want := "\x1b[1;32mvite\x1b[0m ready      in 5ms\n done\n"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}
