package usage

import (
	"testing"
	"time"

	"lazycmd/internal/config"
)

func TestRecordAndScore(t *testing.T) {
	l := Load(t.TempDir())
	now := time.Now()
	l.Record("/repo", "global:deploy", now)
	if got := l.Score("/repo", "global:deploy", now); got != 1 {
		t.Errorf("score = %v, want 1", got)
	}
	if got := l.Score("/repo", "global:unused", now); got != 0 {
		t.Errorf("score of unused = %v, want 0", got)
	}
	if got := l.Score("/other-repo", "global:deploy", now); got != 0 {
		t.Errorf("score in a different repo = %v, want 0 (usage is per-repo)", got)
	}
}

func TestScoreDecaysByHalfLife(t *testing.T) {
	l := Load(t.TempDir())
	t0 := time.Now()
	l.Record("/repo", "global:deploy", t0)

	got := l.Score("/repo", "global:deploy", t0.Add(halfLife))
	if got < 0.49 || got > 0.51 {
		t.Errorf("score after one half-life = %v, want ~0.5", got)
	}

	got = l.Score("/repo", "global:deploy", t0.Add(2*halfLife))
	if got < 0.24 || got > 0.26 {
		t.Errorf("score after two half-lives = %v, want ~0.25", got)
	}
}

func TestRecordReanchorsDecayedCount(t *testing.T) {
	l := Load(t.TempDir())
	t0 := time.Now()
	l.Record("/repo", "global:deploy", t0)

	// A second run a full half-life later should add 1 to the decayed
	// value (~0.5), not to the original raw count of 1.
	t1 := t0.Add(halfLife)
	l.Record("/repo", "global:deploy", t1)

	got := l.Score("/repo", "global:deploy", t1)
	if got < 1.49 || got > 1.51 {
		t.Errorf("score right after second run = %v, want ~1.5", got)
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()

	l := Load(dir)
	l.Record("/repo", "global:deploy", now)
	if err := l.Save(dir); err != nil {
		t.Fatal(err)
	}

	l2 := Load(dir)
	if got := l2.Score("/repo", "global:deploy", now); got != 1 {
		t.Errorf("score after reload = %v, want 1", got)
	}
}

func TestLoadMissingFileStartsEmpty(t *testing.T) {
	l := Load(t.TempDir())
	if got := l.Score("/repo", "global:deploy", time.Now()); got != 0 {
		t.Errorf("score from fresh log = %v, want 0", got)
	}
}

func TestSortByUsagePutsMostUsedFirstAndKeepsTieOrder(t *testing.T) {
	cmds := []config.ResolvedCommand{
		{ID: "global:a", Name: "a"},
		{ID: "global:b", Name: "b"},
		{ID: "global:c", Name: "c"},
		{ID: "global:d", Name: "d"},
	}
	l := Load(t.TempDir())
	now := time.Now()
	l.Record("/repo", "global:c", now)
	l.Record("/repo", "global:c", now)
	l.Record("/repo", "global:a", now)

	l.SortByUsage(cmds, "/repo", now)

	names := make([]string, len(cmds))
	for i, c := range cmds {
		names[i] = c.Name
	}
	want := []string{"c", "a", "b", "d"} // c (score 2), a (score 1), then b/d tied at 0 in original order
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("order = %v, want %v", names, want)
		}
	}
}
