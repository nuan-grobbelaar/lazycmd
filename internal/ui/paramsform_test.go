package ui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"lazycast/internal/config"
)

// This exercises a genuinely concurrent path: huh evaluates an
// OptionsFunc asynchronously (its own goroutine, driven by its own
// message protocol), and paramsForm's options_cmd cache is what that
// goroutine touches. go test -race is what actually validates the
// cache's locking; this test is what gives -race something real to
// run through, since the form had no coverage at all before this.
func TestParamsFormOptionsCmdLoadsAsynchronously(t *testing.T) {
	root := t.TempDir()
	groups := []config.Group{{Title: "This project", Commands: []config.ResolvedCommand{
		{ID: "project:deploy", Name: "deploy", Run: "echo hi", Params: []config.Param{
			{Name: "env", Type: config.ParamSelect, Options: []string{"dev", "staging"}},
			{
				Name: "service", Type: config.ParamSelect,
				OptionsCmd: "echo svc-a; echo svc-b", DependsOn: []string{"env"},
			},
		}},
	}}}

	m := ready(t, groups, t.TempDir(), root)
	m, cmd := sendKey(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.activeForm == nil {
		t.Fatal("expected a params form to open")
	}

	m = pumpUntil(t, m, cmd, func(m Model) bool {
		return strings.Contains(m.activeForm.form.View(), "svc-a")
	}, 2*time.Second)

	view := m.activeForm.form.View()
	if !strings.Contains(view, "svc-a") || !strings.Contains(view, "svc-b") {
		t.Fatalf("expected the options_cmd-backed select to have loaded its options, got view:\n%s", view)
	}
}
