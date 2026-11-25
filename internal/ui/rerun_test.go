package ui

import (
	"strconv"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"lazycmd/internal/config"
	"lazycmd/internal/last"
)

func rerunGroups() []config.Group {
	return []config.Group{{Title: "Global", Commands: []config.ResolvedCommand{
		{ID: "global:deploy", Name: "deploy", Run: "true", Params: []config.Param{
			{Name: "env", Type: config.ParamSelect, Options: config.Opts("dev", "prod"), Default: "dev"},
			{Name: "tag", Type: config.ParamString, Default: "latest"},
		}},
		{ID: "global:login", Name: "login", Run: "true", Params: []config.Param{
			{Name: "user", Type: config.ParamString},
			{Name: "token", Type: config.ParamSecret},
		}},
		{ID: "global:plain", Name: "plain", Run: "true"},
		{ID: "global:pick", Name: "pick", Run: "true", Terminal: true},
	}}}
}

// selectByName moves the cursor to the command called name.
func selectByName(t *testing.T, m Model, name string) Model {
	t.Helper()
	for i, r := range m.rows {
		if !r.isHeader && r.cmd.Name == name {
			m.cursor = i
			return m
		}
	}
	t.Fatalf("no row %q", name)
	return m
}

func seed(t *testing.T, stateDir, repo, id string, values map[string]string) {
	t.Helper()
	s := last.Load(stateDir)
	s.Record(repo, id, values, nil, time.Now().Add(-12*time.Minute))
	if err := s.Save(stateDir); err != nil {
		t.Fatal(err)
	}
}

func runes(s string) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)} }

func TestRerunSelectedUsesLastAnswers(t *testing.T) {
	state, repo := t.TempDir(), t.TempDir()
	seed(t, state, repo, "global:deploy", map[string]string{"env": "prod", "tag": "v2"})
	m := selectByName(t, ready(t, rerunGroups(), state, repo), "deploy")

	m, _ = sendKey(t, m, runes("R"))
	if m.activeForm != nil {
		t.Fatal("R opened a form; the stored answers still fit")
	}
	if len(m.jobs) != 1 || m.jobs[0].values["env"] != "prod" || m.jobs[0].values["tag"] != "v2" {
		t.Fatalf("jobs = %+v", m.jobs)
	}

	// enter still starts from the defaults.
	m, _ = sendKey(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.activeForm == nil || m.activeForm.values()["env"] != "dev" || m.activeForm.values()["tag"] != "latest" {
		t.Errorf("enter form values = %v, want the defaults", m.activeForm.values())
	}
}

func TestRunLastHere(t *testing.T) {
	state, repo := t.TempDir(), t.TempDir()
	m := ready(t, rerunGroups(), state, repo)
	m, _ = sendKey(t, m, runes("."))
	if m.flash != "nothing run in this repo yet" || len(m.jobs) != 0 {
		t.Fatalf("flash = %q, jobs = %d", m.flash, len(m.jobs))
	}

	// Running something records it; "." then runs it again.
	m = selectByName(t, m, "plain")
	m, _ = sendKey(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	m = selectByName(t, m, "deploy") // the cursor doesn't matter for "."
	m, _ = sendKey(t, m, runes("."))
	if len(m.jobs) != 2 || m.jobs[1].cmd.ID != "global:plain" {
		t.Fatalf("jobs = %d, last = %+v", len(m.jobs), m.jobs[len(m.jobs)-1].cmd.ID)
	}
	if bar := m.keyBarText(); !strings.Contains(bar, "plain (just now)") {
		t.Errorf("key bar = %q, want the last command shown", bar)
	}

	// A different repo has its own history.
	m2 := ready(t, rerunGroups(), state, t.TempDir())
	m2, _ = sendKey(t, m2, runes("."))
	if len(m2.jobs) != 0 {
		t.Error("another repo's last command ran")
	}
}

func TestRerunWithSecretOpensFilledForm(t *testing.T) {
	state, repo := t.TempDir(), t.TempDir()
	m := selectByName(t, ready(t, rerunGroups(), state, repo), "login")

	// Run it once through the form, with a secret.
	m, _ = sendKey(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	*m.activeForm.strVals["user"] = "nuan"
	*m.activeForm.strVals["token"] = "s3cret"
	m, _ = sendKey(t, m, tea.KeyMsg{Type: tea.KeyCtrlJ})
	if len(m.jobs) != 1 {
		t.Fatalf("run-now didn't start the job (notice %q)", m.activeForm.notice)
	}
	if e, _ := last.Load(state).Get(repo, "global:login"); e.Values["user"] != "nuan" || e.Values["token"] != "" {
		t.Fatalf("stored = %+v, want user kept and no token", e.Values)
	}

	m, _ = sendKey(t, m, runes("R"))
	if m.activeForm == nil {
		t.Fatal("R ran a command with a secret without asking for it")
	}
	if v := m.activeForm.values(); v["user"] != "nuan" || v["token"] != "" {
		t.Errorf("form values = %v, want user filled in and token empty", v)
	}
	if !strings.Contains(m.activeForm.notice, "secrets aren't saved, so enter token") {
		t.Errorf("notice = %q", m.activeForm.notice)
	}
}

func TestRerunWithStaleAnswersOpensForm(t *testing.T) {
	state, repo := t.TempDir(), t.TempDir()
	cases := []struct {
		values map[string]string
		why    string
	}{
		{map[string]string{"env": "staging", "tag": "v1"}, `"staging" isn't an option for env any more`},
		{map[string]string{"env": "prod"}, "tag is new since last time"},
	}
	for _, c := range cases {
		seed(t, state, repo, "global:deploy", c.values)
		m := selectByName(t, ready(t, rerunGroups(), state, repo), "deploy")
		m, _ = sendKey(t, m, runes("R"))
		if m.activeForm == nil || len(m.jobs) != 0 {
			t.Fatalf("%v: ran instead of opening the form", c.values)
		}
		if !strings.Contains(m.activeForm.notice, c.why) {
			t.Errorf("%v: notice = %q, want %q", c.values, m.activeForm.notice, c.why)
		}
	}
}

func TestRerunWithoutHistoryStartsFromDefaults(t *testing.T) {
	m := selectByName(t, ready(t, rerunGroups(), t.TempDir(), t.TempDir()), "deploy")
	m, _ = sendKey(t, m, runes("R"))
	if m.activeForm == nil || m.activeForm.values()["env"] != "dev" {
		t.Fatal("R with no history should open the form with defaults")
	}
	if !strings.Contains(m.flash, "no earlier answers") {
		t.Errorf("flash = %q", m.flash)
	}
}

func TestHiddenCommandCantBeRerun(t *testing.T) {
	state, repo := t.TempDir(), t.TempDir()
	seed(t, state, repo, "project:evil", map[string]string{})
	m := ready(t, rerunGroups(), state, repo) // project:evil isn't listed (e.g. not allowed yet)
	m, _ = sendKey(t, m, runes("."))
	if len(m.jobs) != 0 || !strings.Contains(m.flash, "isn't available here") {
		t.Errorf("jobs = %d, flash = %q", len(m.jobs), m.flash)
	}
}

func TestTerminalCommandRunsInTerminal(t *testing.T) {
	m := selectByName(t, ready(t, rerunGroups(), t.TempDir(), t.TempDir()), "pick")
	if !strings.Contains(m.renderList(60), "⧉") {
		t.Error("terminal command has no ⧉ marker")
	}
	if bar := m.keyBarText(); !strings.Contains(bar, "run in terminal") {
		t.Errorf("key bar = %q", bar)
	}
	m, cmd := sendKey(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if len(m.jobs) != 0 || cmd == nil {
		t.Errorf("enter started a panel job (%d) instead of handing over the terminal", len(m.jobs))
	}
}

func TestPanelJobGetsViewportSize(t *testing.T) {
	m := selectByName(t, ready(t, []config.Group{{Title: "Global", Commands: []config.ResolvedCommand{
		{ID: "global:size", Name: "size", Run: "stty size"},
	}}}, t.TempDir(), t.TempDir()), "size")
	m, _ = sendKey(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	want := []string{itoa(m.viewport.Height), itoa(m.viewport.Width)}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if out, _ := m.jobs[0].job.Buffer.Snapshot(); strings.Contains(out, strings.Join(want, " ")) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	out, _ := m.jobs[0].job.Buffer.Snapshot()
	t.Errorf("stty size = %q, want %q", out, strings.Join(want, " "))
}

func itoa(n int) string { return strconv.Itoa(n) }

func TestRerunStillAsksAboutShadowing(t *testing.T) {
	state, repo := t.TempDir(), t.TempDir()
	seed(t, state, repo, "project:deploy", map[string]string{"env": "prod"})
	groups := []config.Group{{Title: "This project", Commands: []config.ResolvedCommand{
		{ID: "project:deploy", Name: "deploy", Run: "true", Shadows: "/home/me/.config/lazycmd/scripts/deploy.sh",
			Params: []config.Param{{Name: "env", Type: config.ParamSelect, Options: config.Opts("dev", "prod")}}},
	}}}
	m := selectByName(t, ready(t, groups, state, repo), "deploy")
	m, _ = sendKey(t, m, runes("R"))
	if m.shadowConfirm == nil || len(m.jobs) != 0 {
		t.Fatal("R skipped the shadow warning")
	}
	m, _ = sendKey(t, m, runes("y"))
	if len(m.jobs) != 1 || m.jobs[0].values["env"] != "prod" {
		t.Fatalf("after y: jobs = %+v, want it run with the stored answers", m.jobs)
	}
}
