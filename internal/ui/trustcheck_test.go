package ui

import (
	"testing"

	"lazycmd/internal/config"
)

func TestLaunchRefusesRepoCommandWhenTrustCheckFails(t *testing.T) {
	cmd := config.ResolvedCommand{ID: "project:build", Name: "build", Run: "true", FromRepo: true}
	m := New([]config.Group{{Title: "This project", Commands: []config.ResolvedCommand{cmd}}}, nil, testDir(t), testDir(t))
	calls := 0
	m = m.WithTrustCheck(func() *Loaded {
		calls++
		return &Loaded{Groups: []config.Group{{Title: "This project"}}, Pending: &PendingTrust{}}
	})

	next, _ := m.launch(cmd, runPanel, nil)
	got := next.(Model)
	if calls != 1 || len(got.jobs) != 0 || got.flash == "" || got.pendingTrust == nil {
		t.Errorf("calls=%d jobs=%d flash=%q pending=%v", calls, len(got.jobs), got.flash, got.pendingTrust != nil)
	}

	// A command not from the repo isn't checked.
	other := config.ResolvedCommand{ID: "global:x", Name: "x", Run: "true"}
	m.launch(other, runPanel, nil)
	if calls != 1 {
		t.Error("a global command shouldn't run the trust check")
	}
}
