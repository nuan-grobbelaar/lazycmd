package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"lazycmd/internal/config"
	"lazycmd/internal/trust"
)

func cmd(id, name, run string) config.ResolvedCommand {
	return config.ResolvedCommand{ID: "global:" + id, Name: name, Run: run}
}

func group(cmds ...config.ResolvedCommand) []config.Group {
	return []config.Group{{Title: "Global", Commands: cmds}}
}

func currentID(m Model) string {
	if m.cursor < 0 || m.cursor >= len(m.rows) {
		return ""
	}
	return m.rows[m.cursor].cmd.ID
}

func rowNames(m Model) []string {
	var out []string
	for _, r := range m.rows {
		if !r.isHeader {
			out = append(out, r.cmd.Name)
		}
	}
	return out
}

func TestReloadKeepsSelectionAndFilter(t *testing.T) {
	m := ready(t, group(cmd("a", "alpha", "1"), cmd("b", "bravo", "1"), cmd("c", "charlie", "1")), t.TempDir(), t.TempDir())
	m = selectByName(t, m, "charlie")

	// A new command lands before the selected one: the selection follows it.
	m = m.applyLoaded(&Loaded{Groups: group(cmd("a", "alpha", "1"), cmd("aa", "alpha2", "1"), cmd("b", "bravo", "1"), cmd("c", "charlie", "1"))})
	if currentID(m) != "global:c" {
		t.Errorf("selected %q after reload, want global:c", currentID(m))
	}

	// The filter survives, and so does the selection inside it.
	m.filterQuery = "ra"
	m.applyFilter()
	m = selectByName(t, m, "bravo")
	m = m.applyLoaded(&Loaded{Groups: group(cmd("b", "bravo", "2"), cmd("c", "charlie", "1"), cmd("d", "delta", "1"))})
	if m.filterQuery != "ra" || strings.Join(rowNames(m), ",") != "bravo" || currentID(m) != "global:b" {
		t.Errorf("filter %q rows %v selected %q", m.filterQuery, rowNames(m), currentID(m))
	}

	// A removed selection falls back to the first row.
	m = m.applyLoaded(&Loaded{Groups: group(cmd("c", "charlie", "1"), cmd("d", "delta", "1"))})
	if currentID(m) != "" && m.cursor != firstSelectable(m.rows) {
		t.Errorf("cursor = %d", m.cursor)
	}
}

func TestReloadSaysWhatChanged(t *testing.T) {
	m := ready(t, group(cmd("a", "alpha", "1"), cmd("b", "bravo", "1"), cmd("c", "charlie", "1")), t.TempDir(), t.TempDir())
	m = m.applyLoaded(&Loaded{Groups: group(cmd("a", "alpha", "CHANGED"), cmd("c", "charlie", "1"), cmd("d", "delta", "1"))})

	for _, want := range []string{"Reloaded:", "1 added (delta)", "1 removed (bravo)", "1 changed (alpha)"} {
		if !strings.Contains(m.reloadNote, want) {
			t.Errorf("note %q is missing %q", m.reloadNote, want)
		}
	}
	if !strings.HasPrefix(m.flash, "reloaded: ") {
		t.Errorf("flash = %q", m.flash)
	}
	// With nothing running, the Output panel's start text carries it.
	if text := ansi.Strip(m.viewport.View()); !strings.Contains(text, "1 added (delta)") {
		t.Errorf("output panel = %q", text)
	}
}

func TestReloadIsQuietWhenTheListIsTheSame(t *testing.T) {
	groups := func() []config.Group { return group(cmd("a", "alpha", "1")) }
	m := ready(t, groups(), t.TempDir(), t.TempDir())
	m = m.applyLoaded(&Loaded{Groups: groups()})
	if m.reloadNote != "" || m.flash != "" {
		t.Errorf("note %q flash %q: editing a script's body changes no command, so there's nothing to say", m.reloadNote, m.flash)
	}
}

func TestReloadListsOnlyNewWarnings(t *testing.T) {
	m := ready(t, group(cmd("a", "alpha", "1")), t.TempDir(), t.TempDir())
	m = m.applyLoaded(&Loaded{Groups: group(cmd("a", "alpha", "1")), Warnings: []string{"old problem"}})
	if !strings.Contains(m.reloadNote, "old problem") {
		t.Fatalf("note = %q", m.reloadNote)
	}
	m = m.applyLoaded(&Loaded{Groups: group(cmd("a", "alpha", "1"), cmd("b", "bravo", "1")), Warnings: []string{"old problem", "new problem"}})
	if strings.Contains(m.reloadNote, "old problem") || !strings.Contains(m.reloadNote, "new problem") {
		t.Errorf("note = %q: only the new warning should be listed", m.reloadNote)
	}
	if len(m.warnings) != 2 {
		t.Errorf("warnings = %v, want the full current list", m.warnings)
	}
	m = m.applyLoaded(&Loaded{Groups: group(cmd("a", "alpha", "1"))})
	if len(m.warnings) != 0 {
		t.Errorf("a warning that went away should be gone: %v", m.warnings)
	}
}

func TestReloadKeepsWarningsThatDontComeFromConfig(t *testing.T) {
	m := ready(t, group(cmd("a", "alpha", "1")), t.TempDir(), t.TempDir()).WithWarning("popups are unavailable")
	m = m.applyLoaded(&Loaded{Groups: group(cmd("a", "alpha", "1")), Warnings: []string{"from config"}})
	if strings.Join(m.warnings, "|") != "from config|popups are unavailable" {
		t.Errorf("warnings = %v", m.warnings)
	}
}

func TestReloadLeavesJobsAndOpenFormsAlone(t *testing.T) {
	m, _ := askModel(t, cmd("job", "job", "sleep 10"))
	m = startNamed(t, m, "job")
	defer m.jobs[0].job.Kill()
	jobs := len(m.jobs)

	withForm := config.ResolvedCommand{ID: "global:f", Name: "f", Run: "true", Params: []config.Param{{Name: "x", Type: config.ParamString}}}
	m.activeForm = newParamsForm(withForm, t.TempDir())
	form := m.activeForm

	changed := withForm
	changed.Params = []config.Param{{Name: "y", Type: config.ParamString}}
	m = m.applyLoaded(&Loaded{Groups: group(cmd("job", "job", "sleep 10"), changed)})

	if len(m.jobs) != jobs || m.activeForm != form || m.activeForm.cmd.Params[0].Name != "x" {
		t.Errorf("a reload touched a running job or an open form: jobs %d, form %v", len(m.jobs), m.activeForm)
	}
	if got := strings.Join(rowNames(m), ","); got != "job,f" {
		t.Errorf("rows = %s: the new list should be in", got)
	}
}

func TestReloadHidesChangedRepoCommandsAgain(t *testing.T) {
	m := ready(t, group(cmd("a", "alpha", "1"), config.ResolvedCommand{ID: "project:own", Name: "own", Run: "1"}), t.TempDir(), t.TempDir())
	own := []config.ResolvedCommand{{ID: "project:own", Name: "own", Run: "2"}}
	full := group(cmd("a", "alpha", "1"), own[0])

	m = m.applyLoaded(&Loaded{
		Groups:  group(cmd("a", "alpha", "1")),
		Pending: &PendingTrust{State: trust.Changed, Commands: own, Fingerprint: "f2", FullGroups: full},
	})
	if m.pendingTrust == nil || m.pendingTrust.Fingerprint != "f2" {
		t.Fatal("the repo's commands should be held back again")
	}
	if m.trustPromptOpen {
		t.Error("the prompt must not open on its own mid-session")
	}
	var review bool
	for _, r := range m.allRows {
		review = review || r.cmd.ID == trustReviewID
	}
	if !review || !strings.Contains(m.reloadNote, "1 of this repo's commands need review") {
		t.Errorf("review row %v, note %q", review, m.reloadNote)
	}
	if _, ok := m.commandByID("project:own"); ok {
		t.Error("a hidden command must not be runnable")
	}

	// The same pending state again doesn't repeat the announcement.
	m.reloadNote = ""
	m = m.applyLoaded(&Loaded{
		Groups:  group(cmd("a", "alpha", "1")),
		Pending: &PendingTrust{State: trust.Changed, Commands: own, Fingerprint: "f2", FullGroups: full},
	})
	if m.reloadNote != "" {
		t.Errorf("note = %q", m.reloadNote)
	}

	// Allowed again: the row goes and the commands are back.
	m = m.applyLoaded(&Loaded{Groups: full})
	if m.pendingTrust != nil || m.trustPromptOpen {
		t.Error("nothing should be pending once the repo is allowed")
	}
	if _, ok := m.commandByID("project:own"); !ok {
		t.Error("the repo's command should be listed again")
	}
}

func TestReloadKeepsAnOpenTrustPromptOpen(t *testing.T) {
	m := ready(t, group(cmd("a", "alpha", "1")), t.TempDir(), t.TempDir())
	p := PendingTrust{State: trust.Unknown, Commands: []config.ResolvedCommand{{ID: "project:own", Name: "own"}}, Fingerprint: "f1"}
	m = m.WithPendingTrust(p)
	if !m.trustPromptOpen {
		t.Fatal("setup: the prompt opens at startup")
	}
	p.Fingerprint = "f2"
	m = m.applyLoaded(&Loaded{Groups: group(cmd("a", "alpha", "1")), Pending: &p})
	if !m.trustPromptOpen || m.pendingTrust.Fingerprint != "f2" {
		t.Errorf("open %v, fingerprint %v: a prompt being read stays open, with the new state", m.trustPromptOpen, m.pendingTrust)
	}
}

// fakeReloader answers Poll from a script of results.
type fakeReloader struct{ results []*Loaded }

func (f *fakeReloader) Poll() (*Loaded, bool) {
	if len(f.results) == 0 {
		return nil, false
	}
	l := f.results[0]
	f.results = f.results[1:]
	return l, l != nil
}

func TestReloadTimerLoop(t *testing.T) {
	fr := &fakeReloader{results: []*Loaded{nil, {Groups: group(cmd("n", "new", "1"))}}}
	m := ready(t, group(cmd("a", "alpha", "1")), t.TempDir(), t.TempDir()).WithReload(fr)
	if m.Init() == nil {
		t.Fatal("a model with a reloader should start its timer")
	}
	if (Model{}).Init() != nil {
		t.Fatal("without one, there's no timer")
	}

	step := func() (Model, tea.Cmd) {
		_, cmd := m.Update(reloadTickMsg{})
		msg := cmd() // runs Poll
		updated, next := m.Update(msg)
		return updated.(Model), next
	}
	var next tea.Cmd
	m, next = step() // nothing changed
	if next == nil || strings.Join(rowNames(m), ",") != "alpha" {
		t.Fatalf("rows %v, next %v", rowNames(m), next)
	}
	m, next = step() // a change
	if next == nil || strings.Join(rowNames(m), ",") != "new" {
		t.Errorf("rows %v after a change", rowNames(m))
	}
}

func TestReloadTimerRunsWhileAFormIsOpen(t *testing.T) {
	fr := &fakeReloader{results: []*Loaded{{Groups: group(cmd("n", "new", "1"))}}}
	m := ready(t, group(cmd("a", "alpha", "1")), t.TempDir(), t.TempDir()).WithReload(fr)
	m.activeForm = newParamsForm(config.ResolvedCommand{ID: "x", Name: "x", Params: []config.Param{{Name: "p", Type: config.ParamString}}}, t.TempDir())

	_, c := m.Update(reloadTickMsg{})
	updated, _ := m.Update(c())
	m = updated.(Model)
	if strings.Join(rowNames(m), ",") != "new" || m.activeForm == nil {
		t.Errorf("rows %v, form open %v", rowNames(m), m.activeForm != nil)
	}
}
