package ui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"lazycmd/internal/config"
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

func TestMultiParamDefaultPreselects(t *testing.T) {
	cmd := config.ResolvedCommand{Name: "x", Run: "true", Params: []config.Param{
		{Name: "features", Type: config.ParamMulti, Options: []string{"logging", "metrics", "tracing"}, Default: "logging,tracing"},
	}}
	pf := newParamsForm(cmd, t.TempDir())
	pf.form.Init()
	if got := pf.values()["features"]; got != "logging,tracing" {
		t.Errorf("features = %q, want the default preselected", got)
	}
}

func TestPreviewShowsScriptArgsQuoted(t *testing.T) {
	cmd := config.ResolvedCommand{
		Name: "deploy", ScriptPath: "/s/deploy.sh",
		Params: []config.Param{{Name: "svc", Type: config.ParamString, Default: "my api"}},
		Args:   []string{"--svc", "{svc}"},
	}
	pf := newParamsForm(cmd, t.TempDir())
	if got, want := pf.preview(), "$ /s/deploy.sh --svc 'my api'"; got != want {
		t.Errorf("preview = %q, want %q", got, want)
	}
}

// A secret is typed with masked echo; the preview under the field must
// not print it back in plain text. The command still gets the real value.
func TestPreviewMasksSecrets(t *testing.T) {
	params := []config.Param{
		{Name: "user", Type: config.ParamString, Default: "nuan"},
		{Name: "token", Type: config.ParamSecret, Default: "hunter2"},
	}
	cases := []config.ResolvedCommand{
		{Name: "run", Run: `curl -u "$LC_USER:${LC_TOKEN}" x`, Params: params},
		{Name: "script", ScriptPath: "/s/login.sh", Params: params},
	}
	for _, cmd := range cases {
		pf := newParamsForm(cmd, t.TempDir())
		got := pf.preview()
		if strings.Contains(got, "hunter2") {
			t.Errorf("%s: preview shows the secret: %q", cmd.Name, got)
		}
		if !strings.Contains(got, secretMask) || !strings.Contains(got, "nuan") {
			t.Errorf("%s: preview = %q, want the mask and the plain user", cmd.Name, got)
		}
		if v := pf.values()["token"]; v != "hunter2" {
			t.Errorf("%s: command value = %q, want the real secret", cmd.Name, v)
		}
	}

	// An empty secret shows as empty, so you can see it still needs filling in.
	pf := newParamsForm(config.ResolvedCommand{Name: "e", ScriptPath: "/s/x.sh", Params: []config.Param{{Name: "token", Type: config.ParamSecret}}}, t.TempDir())
	if got := pf.preview(); strings.Contains(got, secretMask) {
		t.Errorf("empty secret should not show the mask: %q", got)
	}
}

func TestProfileOverriddenMultiIsPreselected(t *testing.T) {
	groups := []config.Group{{Title: "Global", Commands: []config.ResolvedCommand{{
		ID: "global:echo-params", Name: "echo-params", Run: "true",
		Params: []config.Param{{Name: "features", Type: config.ParamMulti, OptionsCmd: "echo nope"}},
	}}}}
	if w := config.ApplyParamOverrides(groups, map[string]map[string]config.ParamOverride{
		"echo-params": {"features": {Options: []string{"logging", "tracing", "audit"}, Default: []any{"logging", "audit"}}},
	}); len(w) != 0 {
		t.Fatal(w)
	}
	pf := newParamsForm(groups[0].Commands[0], t.TempDir())
	pf.form.Init()
	if got := pf.values()["features"]; got != "logging,audit" {
		t.Errorf("features = %q, want logging,audit preselected", got)
	}
}
