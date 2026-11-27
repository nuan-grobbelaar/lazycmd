package ui

import (
	"os"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"lazycmd/internal/config"
	"lazycmd/internal/jobs"
)

// runPast leaves a finished run of cmd in stateDir's history, as an
// earlier lazycmd would have.
func runPast(t *testing.T, stateDir string, cmd config.ResolvedCommand) {
	t.Helper()
	j, err := jobs.Start(cmd.Name+"-1", cmd, nil, stateDir)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(4 * time.Second)
	for {
		if status, _, _ := j.Snapshot(); status != jobs.StatusRunning {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("job never finished")
		}
		time.Sleep(10 * time.Millisecond)
	}
	time.Sleep(50 * time.Millisecond) // history is written right after the status changes
}

func TestHistoryShowViewRerunHide(t *testing.T) {
	state := t.TempDir()
	repo, _ := os.Getwd() // jobs record the folder they ran in
	build := config.ResolvedCommand{ID: "global:build", Name: "build", Run: "echo built-it"}
	runPast(t, state, build)

	m := New([]config.Group{{Title: "Global", Commands: []config.ResolvedCommand{build}}}, nil, state, repo)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m = updated.(Model)
	m.focus = focusJobs

	if !strings.Contains(m.renderJobsList(30, 6), "H earlier runs") {
		t.Error("an empty Jobs panel should mention H")
	}
	m, _ = sendKey(t, m, runes("H"))
	rows := m.jobRows()
	if !m.showHistory || len(rows) != 1 || rows[0].past == nil || m.jobsCursor != 0 {
		t.Fatalf("after H: show=%v rows=%d cursor=%d", m.showHistory, len(rows), m.jobsCursor)
	}
	if list := m.renderJobsList(40, 6); !strings.Contains(list, "build") || !strings.Contains(list, "ago") && !strings.Contains(list, "just now") {
		t.Errorf("history row = %q", list)
	}

	m, _ = sendKey(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.activeJob == nil || !strings.Contains(m.activeJobBody(), "built-it") || !strings.Contains(m.activeJobBody(), "earlier run") {
		t.Errorf("viewing a past run: %q", m.activeJobBody())
	}

	// r runs the command again, now as a job of this session.
	m, _ = sendKey(t, m, runes("r"))
	if len(m.jobs) != 1 || m.jobsCursor != 1 {
		t.Fatalf("rerun: jobs=%d cursor=%d", len(m.jobs), m.jobsCursor)
	}

	m, _ = sendKey(t, m, runes("H"))
	if m.showHistory || len(m.jobRows()) != 1 || m.jobsCursor != 0 {
		t.Errorf("after hiding: show=%v rows=%d cursor=%d", m.showHistory, len(m.jobRows()), m.jobsCursor)
	}
}

func TestHistoryRerunOfGoneCommand(t *testing.T) {
	state := t.TempDir()
	repo, _ := os.Getwd()
	runPast(t, state, config.ResolvedCommand{ID: "global:gone", Name: "gone", Run: "true"})

	m := New(nil, nil, state, repo)
	m.focus = focusJobs
	m, _ = sendKey(t, m, runes("H"))
	m, _ = sendKey(t, m, runes("r"))
	if len(m.jobs) != 0 || !strings.Contains(m.flash, "isn't available") {
		t.Errorf("jobs=%d flash=%q", len(m.jobs), m.flash)
	}
}

func TestJobsListScrollsToCursor(t *testing.T) {
	m := New(nil, nil, t.TempDir(), t.TempDir())
	m.focus = focusJobs
	for i := 0; i < 8; i++ {
		j, _ := jobs.Start("j", config.ResolvedCommand{Name: string(rune('a' + i)), Run: "true"}, nil, t.TempDir())
		m.jobs = append(m.jobs, &trackedJob{job: j, cmd: config.ResolvedCommand{Name: "job" + string(rune('a'+i))}})
	}
	m.jobsCursor = 7
	list := m.renderJobsList(30, 3)
	if strings.Count(list, "\n") != 2 || !strings.Contains(list, "jobh") || strings.Contains(list, "jobe") {
		t.Errorf("cursor at the end: %q", list)
	}
	m.jobsCursor = 0
	if list := m.renderJobsList(30, 3); !strings.Contains(list, "joba") || strings.Contains(list, "jobd") {
		t.Errorf("cursor at the top: %q", list)
	}
}
