package ui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/huh"

	"lazycmd/internal/config"
)

// This exercises a genuinely concurrent path: huh evaluates an
// OptionsFunc asynchronously (its own goroutine, driven by its own
// message protocol), and paramsForm's options_cmd cache is what that
// goroutine touches. go test -race is what actually validates the
// cache's locking; this test is what gives -race something real to
// run through, since the form had no coverage at all before this.
func TestParamsFormOptionsCmdLoadsAsynchronously(t *testing.T) {
	root := testDir(t)
	groups := []config.Group{{Title: "This project", Commands: []config.ResolvedCommand{
		{ID: "project:deploy", Name: "deploy", Run: "echo hi", Params: []config.Param{
			{Name: "env", Type: config.ParamSelect, Options: config.Opts("dev", "staging")},
			{
				Name: "service", Type: config.ParamSelect,
				OptionsCmd: "echo svc-a; echo svc-b", DependsOn: []string{"env"},
			},
		}},
	}}}

	m := ready(t, groups, testDir(t), root)
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
	pf := newParamsForm(cmd, testDir(t))
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
	pf := newParamsForm(cmd, testDir(t))
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
		pf := newParamsForm(cmd, testDir(t))
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
	pf := newParamsForm(config.ResolvedCommand{Name: "e", ScriptPath: "/s/x.sh", Params: []config.Param{{Name: "token", Type: config.ParamSecret}}}, testDir(t))
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
	pf := newParamsForm(groups[0].Commands[0], testDir(t))
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
	pf := newParamsForm(cmd, testDir(t))
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
	root := testDir(t)
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
	m := ready(t, groups, testDir(t), testDir(t))
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
	pf := newParamsForm(cmd, testDir(t))
	pf.form.Init()
	if v := pf.values(); v["static"] != "a,c" || v["dynamic"] != "b" {
		t.Errorf("values = %v, want both multi defaults", v)
	}
}

func optionsForm(t *testing.T, optionsCmd string, extra ...config.Param) Model {
	t.Helper()
	pick := config.Param{Name: "pick", Type: config.ParamSelect, OptionsCmd: optionsCmd}
	for _, e := range extra {
		pick.DependsOn = append(pick.DependsOn, e.Name)
	}
	params := append(extra, pick)
	groups := []config.Group{{Title: "This project", Commands: []config.ResolvedCommand{
		{ID: "project:x", Name: "x", Run: "true", Params: params},
	}}}
	m := ready(t, groups, testDir(t), testDir(t))
	m, _ = sendKey(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.activeForm == nil {
		t.Fatal("expected a params form to open")
	}
	return m
}

func TestOptionsCmdFailureIsShown(t *testing.T) {
	cases := []struct{ cmd, want string }{
		{"echo 'gcloud: not logged in' >&2; exit 3", "⚠ options_cmd failed: exit 3: gcloud: not logged in"},
		{"exit 1", "⚠ options_cmd failed: exit 1"},
		{"printf 'bad\\033]0;x\\007\\n' >&2; exit 2", "exit 2: bad^[]0;x^G"},
		{"true", "no options"},
	}
	for _, c := range cases {
		m := optionsForm(t, c.cmd)
		r := m.activeForm.optionsFor(c.cmd)
		note := m.activeForm.optionsNote(c.cmd)
		if !strings.Contains(note, c.want) {
			t.Errorf("%q: note = %q (err %q), want %q", c.cmd, note, r.err, c.want)
		}
		if strings.Contains(note, "\x1b]0;") {
			t.Errorf("%q: escape from stderr reached the note", c.cmd)
		}
	}
	m := optionsForm(t, "echo a")
	if note := m.activeForm.optionsNote("echo a"); note != "" {
		t.Errorf("working options_cmd note = %q, want none", note)
	}
}

func TestOptionsCmdTimeout(t *testing.T) {
	old := optionsCmdTimeout
	optionsCmdTimeout = 200 * time.Millisecond
	defer func() { optionsCmdTimeout = old }()

	m := optionsForm(t, "sleep 5; echo late")
	start := time.Now()
	r := m.activeForm.optionsFor("sleep 5; echo late")
	if took := time.Since(start); took > 2*time.Second {
		t.Errorf("timeout took %s; a child keeping the pipe open must not block", took)
	}
	if r.err != "timed out after 200ms" || len(r.opts) != 0 {
		t.Errorf("result = %+v", r)
	}
}

func TestOptionsCmdErrorShowsInFormAndClearsWithDependency(t *testing.T) {
	cmd := `[ "$LC_ENV" = good ] && echo ok || { echo "no such env: $LC_ENV" >&2; exit 4; }`
	m := optionsForm(t, cmd, config.Param{Name: "env", Type: config.ParamString, Default: "bad"})
	m = pumpForm(t, m, func(v string) bool { return strings.Contains(v, "no such env: bad") })

	// Retype env (the focused field) and move on with enter, which is when
	// huh hands the input's text to the value. Every command huh returns is
	// kept: they carry the reload of pick's options and description.
	var cmds []tea.Cmd
	key := func(k tea.KeyMsg) {
		f, c := m.activeForm.form.Update(k)
		m.activeForm.form = f.(*huh.Form)
		cmds = append(cmds, c)
	}
	for i := 0; i < 3; i++ {
		key(tea.KeyMsg{Type: tea.KeyBackspace})
	}
	key(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("good")})
	key(tea.KeyMsg{Type: tea.KeyEnter})
	m = pumpForm(t, m, func(v string) bool { return !strings.Contains(v, "options_cmd failed") && strings.Contains(v, "ok") }, cmds...)
	if got := *m.activeForm.strVals["env"]; got != "good" {
		t.Fatalf("env = %q after typing, want good", got)
	}
}

func TestSelectWithNoOptionsCantBeSubmitted(t *testing.T) {
	m := optionsForm(t, "exit 1")
	m = pumpForm(t, m, func(v string) bool { return strings.Contains(v, "options_cmd failed") })
	// enter: huh won't leave a select with no options.
	m, _ = sendKey(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.activeForm == nil {
		t.Fatal("form closed: an empty select was submitted with enter")
	}
	// run-now skips huh's checks, so lazycmd's own must catch it.
	m, _ = sendKey(t, m, tea.KeyMsg{Type: tea.KeyCtrlJ})
	if m.activeForm == nil {
		t.Fatal("form closed: an empty select was submitted with run-now")
	}
	if !strings.Contains(m.activeForm.notice, "pick: nothing to pick (options_cmd failed)") {
		t.Errorf("notice = %q", m.activeForm.notice)
	}
}

// pumpForm drives the open form until cond holds for its view: it runs
// the commands huh returns (option and description loads) and, when none
// are pending, sends a no-op message, since huh re-checks its dynamic
// fields on every message.
func pumpForm(t *testing.T, m Model, cond func(view string) bool, cmds ...tea.Cmd) Model {
	t.Helper()
	type nudge struct{}
	pending := append([]tea.Cmd{m.activeForm.form.Init()}, cmds...)
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond(m.activeForm.form.View()) {
			return m
		}
		var msg tea.Msg = nudge{}
		if len(pending) > 0 {
			c := pending[0]
			pending = pending[1:]
			if c == nil {
				continue
			}
			if msg = c(); msg == nil {
				continue
			}
			if batch, ok := msg.(tea.BatchMsg); ok {
				pending = append(pending, batch...)
				continue
			}
		} else {
			time.Sleep(10 * time.Millisecond)
		}
		f, c := m.activeForm.form.Update(msg)
		m.activeForm.form = f.(*huh.Form)
		pending = append(pending, c)
	}
	t.Fatalf("condition never held; view:\n%s", m.activeForm.form.View())
	return m
}
