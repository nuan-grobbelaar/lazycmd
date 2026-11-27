package ui

import (
	"path/filepath"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"lazycmd/internal/config"
)

// pumpUntil drives m the way the real bubbletea runtime does: a cmd
// that resolves to a tea.BatchMsg has its sub-commands executed and
// their results fed into Update individually (matching tea.Program's
// execBatchMsg), rather than being fed into Update as one opaque
// message - which a naive "just call cmd() and Update() in a loop"
// harness gets wrong for exactly the async, multi-tick flows this
// package relies on (poll tick + prompt popup + cursor blink, all
// batched together). Stops once pred(m) is true or the deadline passes.
func pumpUntil(t *testing.T, m Model, cmd tea.Cmd, pred func(Model) bool, timeout time.Duration) Model {
	t.Helper()
	deadline := time.Now().Add(timeout)
	pending := []tea.Cmd{cmd}
	for time.Now().Before(deadline) {
		if pred(m) {
			return m
		}
		if len(pending) == 0 {
			time.Sleep(5 * time.Millisecond)
			continue
		}
		c := pending[0]
		pending = pending[1:]
		if c == nil {
			continue
		}
		msg := c()
		if msg == nil {
			continue
		}
		if batch, ok := msg.(tea.BatchMsg); ok {
			pending = append(pending, batch...)
			continue
		}
		updated, next := m.Update(msg)
		m = updated.(Model)
		if next != nil {
			pending = append(pending, next)
		}
	}
	return m
}

func TestPromptPopupOpensOnARealPauseAfterAJobFinished(t *testing.T) {
	root := testDir(t)
	writeExecutable(t, filepath.Join(root, "hi.sh"), "#!/bin/sh\necho hi\n")
	writeExecutable(t, filepath.Join(root, "confirm.sh"), "#!/bin/sh\nprintf 'Proceed? [y/N] '\nread ans\necho done\n")
	groups := []config.Group{{Title: "This project", Commands: []config.ResolvedCommand{
		{ID: "project:hi", Name: "hi", ScriptPath: filepath.Join(root, "hi.sh")},
		{ID: "project:confirm", Name: "confirm", ScriptPath: filepath.Join(root, "confirm.sh")},
	}}}

	m := ready(t, groups, testDir(t), root)

	// Run "hi" first and let its poll chain die out naturally, same as
	// a real session running one quick command before a slower one.
	m, cmd := sendKey(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	m = pumpUntil(t, m, cmd, func(m Model) bool { return !m.polling }, 2*time.Second)

	m, _ = sendKey(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	m, cmd = sendKey(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.activeJob == nil || m.activeJob.cmd.Name != "confirm" {
		t.Fatalf("expected active job to be confirm, got %+v", m.activeJob)
	}
	t.Cleanup(func() { m.activeJob.job.Kill() })

	m = pumpUntil(t, m, cmd, func(m Model) bool { return m.promptActive }, 2*time.Second)
	if !m.promptActive {
		t.Fatal("expected the prompt popup to open once the question line sat past the pause threshold")
	}
}
