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
			{Name: "env", Type: config.ParamSelect, Options: config.Opts("dev", "staging")},
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
		{Name: "features", Type: config.ParamMulti, Options: config.Opts("logging", "metrics", "tracing"), Default: "logging,tracing"},
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
		"echo-params": {"features": {Options: config.Opts("logging", "tracing", "audit"), Default: []any{"logging", "audit"}}},
	}); len(w) != 0 {
		t.Fatal(w)
	}
	pf := newParamsForm(groups[0].Commands[0], t.TempDir())
	pf.form.Init()
	if got := pf.values()["features"]; got != "logging,audit" {
		t.Errorf("features = %q, want logging,audit preselected", got)
	}
}

func TestFormShowsLabelsAndSendsValues(t *testing.T) {
	cmd := config.ResolvedCommand{Name: "pick", Run: "true", Params: []config.Param{
		{Name: "repo", Type: config.ParamSelect, Default: "/r/cast", Options: config.OptionList{
			{Label: "vandag", Value: "/r/vandag"}, {Label: "cast", Value: "/r/cast"},
		}},
		{Name: "many", Type: config.ParamMulti, Default: "/b", Options: config.OptionList{
			{Label: "Ay\x1b]0;x\x07", Value: "/a"}, {Label: "Bee", Value: "/b"},
		}},
	}}
	pf := newParamsForm(cmd, t.TempDir())
	pf.form.Init()
	view := pf.form.View()
	if !strings.Contains(view, "cast") || !strings.Contains(view, "Bee") || strings.Contains(view, "/r/") {
		t.Errorf("view should show labels, not values:\n%s", view)
	}
	if v := pf.values(); v["repo"] != "/r/cast" || v["many"] != "/b" {
		t.Errorf("values = %v, want the values, not the labels", v)
	}

	opts := pf.toOptions(cmd.Params[1], cmd.Params[1].Options)
	if opts[0].Key != "Ay^[]0;x^G" || opts[0].Value != "/a" {
		t.Errorf("option = %+v, want a cleaned label and the raw value", opts[0])
	}
}

func TestToOptionsLabelModes(t *testing.T) {
	root := t.TempDir()
	pf := newParamsForm(config.ResolvedCommand{Name: "x", Run: "true", Params: []config.Param{{Name: "s", Type: config.ParamString}}}, root)
	p := config.Param{Name: "pom", Type: config.ParamSelect, Label: config.LabelRelative}
	opts := pf.toOptions(p, config.Opts(root+"/svc/api/pom.xml", "./web/pom.xml"))
	if opts[0].Key != "svc/api/pom.xml" || opts[0].Value != root+"/svc/api/pom.xml" || opts[1].Key != "web/pom.xml" {
		t.Errorf("relative = %+v", opts)
	}
	p.Label = config.LabelBasename
	opts = pf.toOptions(p, config.Opts("/a/one.txt", "/b/two.txt"))
	if opts[0].Key != "one.txt" || opts[1].Key != "two.txt" {
		t.Errorf("basename = %+v", opts)
	}
}

// Regression: an options_cmd with no depends_on never loaded, because huh
// only re-runs an OptionsFunc when its bindings' hash changes from 0, and
// nil bindings hash to 0. Also covers tab-separated labels.
func TestOptionsCmdWithoutDependsOnLoads(t *testing.T) {
	groups := []config.Group{{Title: "This project", Commands: []config.ResolvedCommand{
		{ID: "project:pick", Name: "pick", Run: "true", Params: []config.Param{
			{Name: "repo", Type: config.ParamSelect, OptionsCmd: `printf '/r/vandag\tVandag\n/r/cast\n'`},
		}},
	}}}
	m := ready(t, groups, t.TempDir(), t.TempDir())
	m, cmd := sendKey(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.activeForm == nil {
		t.Fatal("expected a params form to open")
	}
	m = pumpUntil(t, m, cmd, func(m Model) bool { return strings.Contains(m.activeForm.form.View(), "Vandag") }, 2*time.Second)
	view := m.activeForm.form.View()
	if !strings.Contains(view, "Vandag") || !strings.Contains(view, "/r/cast") || strings.Contains(view, "/r/vandag") {
		t.Errorf("options_cmd options didn't load with labels:\n%s", view)
	}
}

// Regression: a multi's default only reached the command when the multi
// was the first field (huh writes a multi's value back only once the
// field is touched), and never for options_cmd options.
func TestMultiDefaultReachesCommandAnywhere(t *testing.T) {
	cmd := config.ResolvedCommand{Name: "x", Run: "true", Params: []config.Param{
		{Name: "first", Type: config.ParamString},
		{Name: "static", Type: config.ParamMulti, Options: config.Opts("a", "b", "c"), Default: "a,c"},
		{Name: "dynamic", Type: config.ParamMulti, OptionsCmd: "echo a; echo b", Default: "b"},
	}}
	pf := newParamsForm(cmd, t.TempDir())
	pf.form.Init()
	if v := pf.values(); v["static"] != "a,c" || v["dynamic"] != "b" {
		t.Errorf("values = %v, want both multi defaults", v)
	}
}
