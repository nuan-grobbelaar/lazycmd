package jobs

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"lazycmd/internal/config"
)

func TestPrepareStateMakesItPrivateAndPrunes(t *testing.T) {
	state := filepath.Join(t.TempDir(), "lazycmd")
	logs := filepath.Join(state, "logs")
	if err := os.MkdirAll(logs, 0o755); err != nil { // as an older lazycmd made it
		t.Fatal(err)
	}
	now := time.Now()
	for i := 0; i < 5; i++ {
		p := filepath.Join(logs, fmt.Sprintf("job-%d.log", i))
		os.WriteFile(p, []byte("x"), 0o644)
		os.Chtimes(p, now, now.Add(time.Duration(i)*time.Minute)) // job-4 newest
	}
	os.WriteFile(filepath.Join(logs, "notes.txt"), []byte("x"), 0o644)

	PrepareState(state, 2)

	for _, d := range []string{state, logs} {
		if info, _ := os.Stat(d); info.Mode().Perm() != 0o700 {
			t.Errorf("%s mode %v, want 0700", d, info.Mode().Perm())
		}
	}
	entries, _ := os.ReadDir(logs)
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	want := []string{"job-3.log", "job-4.log", "notes.txt"}
	if fmt.Sprint(names) != fmt.Sprint(want) {
		t.Errorf("left %v, want %v", names, want)
	}
}

func TestLogFileIsPrivate(t *testing.T) {
	state := t.TempDir()
	j, err := Start("echo-1", config.ResolvedCommand{Name: "echo", Run: "echo hi"}, nil, state)
	if err != nil {
		t.Fatal(err)
	}
	waitForStatus(t, j, StatusSucceeded, 4*time.Second)
	info, err := os.Stat(filepath.Join(state, "logs", j.LogName))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("log mode %v, want 0600", info.Mode().Perm())
	}
	if d, _ := os.Stat(filepath.Join(state, "logs")); d.Mode().Perm() != 0o700 {
		t.Errorf("logs folder mode %v, want 0700", d.Mode().Perm())
	}
}
