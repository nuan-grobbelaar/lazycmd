package jobs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lazycmd/internal/config"
)

func TestFinishedJobsAreRecordedAndListed(t *testing.T) {
	state := t.TempDir()
	repo, _ := os.Getwd()

	ok, err := Start("ok-1", config.ResolvedCommand{ID: "global:ok", Name: "ok", Run: "echo first"}, nil, state)
	if err != nil {
		t.Fatal(err)
	}
	waitForStatus(t, ok, StatusSucceeded, 4*time.Second)
	time.Sleep(1100 * time.Millisecond) // log names carry the second they started
	bad, _ := Start("bad-2", config.ResolvedCommand{ID: "global:bad", Name: "bad", Run: "echo second; exit 4"}, nil, state)
	waitForStatus(t, bad, StatusFailed, 4*time.Second)

	past := History(state, repo, 10, nil)
	if len(past) != 2 || past[0].Name != "bad" || past[1].Name != "ok" {
		t.Fatalf("want bad then ok, newest first; got %d runs", len(past))
	}
	status, code, _ := past[0].Snapshot()
	if status != StatusFailed || code != 4 || past[0].CmdID != "global:bad" || !past[0].Past() {
		t.Errorf("bad: status %v code %d id %q", status, code, past[0].CmdID)
	}
	past[0].Load()
	if out, _ := past[0].Buffer.Snapshot(); !strings.Contains(out, "second") {
		t.Errorf("bad's output = %q", out)
	}
	past[0].Kill() // no process: must not panic

	if got := History(state, "/some/other/repo", 10, nil); len(got) != 0 {
		t.Errorf("another repo's runs listed: %d", len(got))
	}
	if got := History(state, repo, 10, map[string]bool{bad.LogName: true}); len(got) != 1 || got[0].Name != "ok" {
		t.Errorf("skip should leave out this session's own job")
	}
	if got := History(state, repo, 1, nil); len(got) != 1 {
		t.Errorf("limit: got %d", len(got))
	}
	if info, _ := os.Stat(filepath.Join(state, historyFile)); info.Mode().Perm() != 0o600 {
		t.Errorf("history file mode %v", info.Mode().Perm())
	}

	// Once a log is pruned, its run is left out, and compacting drops it.
	os.Remove(filepath.Join(state, "logs", ok.LogName))
	if got := History(state, repo, 10, nil); len(got) != 1 {
		t.Errorf("a run without its log should be left out, got %d", len(got))
	}
	compactHistory(state)
	data, _ := os.ReadFile(filepath.Join(state, historyFile))
	if strings.Count(string(data), "\n") != 1 {
		t.Errorf("compacted history = %q", data)
	}
}

func TestHistoryIgnoresBadLines(t *testing.T) {
	state := t.TempDir()
	os.MkdirAll(filepath.Join(state, "logs"), 0o700)
	os.WriteFile(filepath.Join(state, "logs", "x.log"), []byte("hi"), 0o600)
	lines := "not json\n" +
		`{"name":"evil","repo":"/r","log":"../../etc/passwd","started":"2026-01-01T00:00:00Z"}` + "\n" +
		`{"name":"good","repo":"/r","log":"x.log","started":"2026-01-01T00:00:00Z","status":"succeeded"}` + "\n"
	os.WriteFile(filepath.Join(state, historyFile), []byte(lines), 0o600)
	got := History(state, "/r", 10, nil)
	if len(got) != 1 || got[0].Name != "good" {
		t.Errorf("got %d runs", len(got))
	}
}
