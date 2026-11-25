package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"lazycmd/internal/config"
	"lazycmd/internal/jobs"
)

func testGroups() []config.Group {
	return []config.Group{
		{Title: "Global", Commands: []config.ResolvedCommand{
			{ID: "global:alpha", Name: "alpha"},
			{ID: "global:bravo", Name: "bravo"},
		}},
		{Title: "This project", Commands: []config.ResolvedCommand{
			{ID: "project:charlie", Name: "charlie"},
		}},
	}
}

// ready returns a Model that's already been through its initial
// WindowSizeMsg, same as a real program does on startup, so viewport
// and layout-dependent state are initialized.
func ready(t *testing.T, groups []config.Group, stateDir, projectRoot string) Model {
	t.Helper()
	m := New(groups, nil, stateDir, projectRoot)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	return updated.(Model)
}

func sendKey(t *testing.T, m Model, msg tea.KeyMsg) (Model, tea.Cmd) {
	t.Helper()
	updated, cmd := m.Update(msg)
	return updated.(Model), cmd
}

func rowLabels(m Model) []string {
	labels := make([]string, len(m.rows))
	for i, r := range m.rows {
		labels[i] = r.label
	}
	return labels
}

func writeExecutable(t *testing.T, path, script string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestViewShowsAllThreePanelTitles(t *testing.T) {
	m := ready(t, testGroups(), t.TempDir(), "/repo")
	view := m.View()
	for _, title := range []string{"[1] Commands", "[2] Jobs", "[3] Output"} {
		if !strings.Contains(view, title) {
			t.Errorf("View() missing panel title %q", title)
		}
	}
}

func TestViewShowsFormAsAnOverlay(t *testing.T) {
	groups := []config.Group{{Title: "This project", Commands: []config.ResolvedCommand{
		{ID: "project:greet", Name: "greet", Run: "echo hi", Params: []config.Param{
			{Name: "who", Type: config.ParamString},
		}},
	}}}
	m := ready(t, groups, t.TempDir(), "/repo")
	m, _ = sendKey(t, m, tea.KeyMsg{Type: tea.KeyEnter})

	view := m.View()
	if !strings.Contains(view, "greet") {
		t.Errorf("View() missing the form modal title; got:\n%s", view)
	}
	// The base view behind the modal should still be present - that's
	// the whole point of an overlay instead of replacing panel content.
	if !strings.Contains(view, "[1] Commands") {
		t.Errorf("View() lost the base panels behind the modal")
	}
}

func TestViewDoesNotPanicAtDegenerateSizes(t *testing.T) {
	for _, size := range []struct{ w, h int }{{0, 0}, {1, 1}, {5, 3}, {-1, -1}} {
		m := New(testGroups(), nil, t.TempDir(), "/repo")
		updated, _ := m.Update(tea.WindowSizeMsg{Width: size.w, Height: size.h})
		m = updated.(Model)
		m.View() // must not panic
	}
}

func TestEmptyStateMessageDistinguishesNoCommandsFromNoMatches(t *testing.T) {
	m := ready(t, nil, t.TempDir(), "/repo")
	if !strings.Contains(m.renderList(40), "no commands found") {
		t.Errorf("expected the no-commands-at-all message when nothing is configured")
	}

	m = ready(t, testGroups(), t.TempDir(), "/repo")
	m, _ = sendKey(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	m, _ = sendKey(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("nonexistent")})
	if !strings.Contains(m.renderList(40), "no matches") {
		t.Errorf("expected the no-matches message when a filter excludes everything")
	}
	if strings.Contains(m.renderList(40), "no commands found") {
		t.Errorf("should not show the no-commands message when commands exist but are filtered out")
	}
}

func TestNewFlattensRowsWithHeaders(t *testing.T) {
	m := ready(t, testGroups(), t.TempDir(), "/repo")
	got := rowLabels(m)
	want := []string{"Global", "alpha", "bravo", "This project", "charlie"}
	if len(got) != len(want) {
		t.Fatalf("rows = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("rows[%d] = %q, want %q", i, got[i], want[i])
		}
	}
	if m.cursor != 1 { // first non-header row
		t.Errorf("cursor = %d, want 1 (alpha)", m.cursor)
	}
}

func TestCursorMovementSkipsHeaders(t *testing.T) {
	m := ready(t, testGroups(), t.TempDir(), "/repo")
	// alpha(1) -> bravo(2) -> skip "This project"(3) -> charlie(4)
	m, _ = sendKey(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	if m.rows[m.cursor].label != "bravo" {
		t.Fatalf("after one down, cursor on %q, want bravo", m.rows[m.cursor].label)
	}
	m, _ = sendKey(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	if m.rows[m.cursor].label != "charlie" {
		t.Fatalf("after two downs, cursor on %q, want charlie (header skipped)", m.rows[m.cursor].label)
	}
	// one more down: already at the last row, should not move
	m, _ = sendKey(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	if m.rows[m.cursor].label != "charlie" {
		t.Errorf("cursor moved past the last row to %q", m.rows[m.cursor].label)
	}
}

func TestFilterNarrowsRowsAndKeepsOnlyMatchingHeaders(t *testing.T) {
	m := ready(t, testGroups(), t.TempDir(), "/repo")
	m, _ = sendKey(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	if !m.filtering {
		t.Fatal("expected filtering to be active after /")
	}
	for _, r := range "charlie" {
		m, _ = sendKey(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	got := rowLabels(m)
	want := []string{"This project", "charlie"}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("filtered rows = %v, want %v (Global header dropped, no match left under it)", got, want)
	}
}

func TestFilterBackspaceAndEsc(t *testing.T) {
	m := ready(t, testGroups(), t.TempDir(), "/repo")
	m, _ = sendKey(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	m, _ = sendKey(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})
	m, _ = sendKey(t, m, tea.KeyMsg{Type: tea.KeyBackspace})
	if m.filterQuery != "" {
		t.Errorf("filterQuery = %q after backspace, want empty", m.filterQuery)
	}
	if len(m.rows) != len(m.allRows) {
		t.Errorf("rows should be back to all %d after clearing query, got %d", len(m.allRows), len(m.rows))
	}

	m, _ = sendKey(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("bravo")})
	m, _ = sendKey(t, m, tea.KeyMsg{Type: tea.KeyEnter}) // confirm: stop editing, keep filter
	if m.filtering {
		t.Error("enter should stop editing the filter")
	}
	if m.filterQuery != "bravo" {
		t.Errorf("filterQuery = %q after enter, want kept as bravo", m.filterQuery)
	}

	m, _ = sendKey(t, m, tea.KeyMsg{Type: tea.KeyEsc}) // now from normal mode: clears
	if m.filterQuery != "" {
		t.Errorf("filterQuery = %q after esc from normal mode, want cleared", m.filterQuery)
	}
}

func TestHelpOverlayTogglesAndAnyKeyDismisses(t *testing.T) {
	m := ready(t, testGroups(), t.TempDir(), "/repo")
	m, _ = sendKey(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("?")})
	if !m.helpActive {
		t.Fatal("expected helpActive after ?")
	}
	m, _ = sendKey(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("z")}) // any key closes it
	if m.helpActive {
		t.Error("expected helpActive to clear on any key")
	}
}

func TestQuitKeysReturnTeaQuit(t *testing.T) {
	m := ready(t, testGroups(), t.TempDir(), "/repo")
	for _, key := range []string{"q", "ctrl+c"} {
		var msg tea.KeyMsg
		if key == "ctrl+c" {
			msg = tea.KeyMsg{Type: tea.KeyCtrlC}
		} else {
			msg = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)}
		}
		_, cmd := sendKey(t, m, msg)
		if cmd == nil {
			t.Fatalf("%s: expected a cmd, got nil", key)
		}
		if _, ok := cmd().(tea.QuitMsg); !ok {
			t.Errorf("%s: cmd produced %T, want tea.QuitMsg", key, cmd())
		}
	}
}

func TestSelectRowWithNoParamsStartsARealJob(t *testing.T) {
	root := t.TempDir()
	writeExecutable(t, filepath.Join(root, "hi.sh"), "#!/bin/sh\necho hi\n")
	groups := []config.Group{{Title: "This project", Commands: []config.ResolvedCommand{
		{ID: "project:hi", Name: "hi", ScriptPath: filepath.Join(root, "hi.sh")},
	}}}

	m := ready(t, groups, t.TempDir(), root)
	m, cmd := sendKey(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.activeJob == nil {
		t.Fatal("expected activeJob to be set")
	}
	t.Cleanup(func() { m.activeJob.job.Kill() })
	if cmd == nil {
		t.Fatal("expected a poll tick cmd to be returned")
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if status, _, _ := m.activeJob.job.Snapshot(); status != jobs.StatusRunning {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	content, _ := m.activeJob.job.Buffer.Snapshot()
	if content != "hi\n" {
		t.Errorf("job output = %q, want %q", content, "hi\n")
	}
}

func TestSelectRowWithParamsOpensFormInsteadOfRunning(t *testing.T) {
	groups := []config.Group{{Title: "This project", Commands: []config.ResolvedCommand{
		{ID: "project:greet", Name: "greet", Run: "echo hi", Params: []config.Param{
			{Name: "who", Type: config.ParamString},
		}},
	}}}
	m := ready(t, groups, t.TempDir(), "/repo")
	m, _ = sendKey(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.activeForm == nil {
		t.Fatal("expected a params form to open")
	}
	if m.activeJob != nil {
		t.Error("expected no job to have started yet")
	}
}

func TestEscAbortsOpenForm(t *testing.T) {
	groups := []config.Group{{Title: "This project", Commands: []config.ResolvedCommand{
		{ID: "project:greet", Name: "greet", Run: "echo hi", Params: []config.Param{
			{Name: "who", Type: config.ParamString},
		}},
	}}}
	m := ready(t, groups, t.TempDir(), "/repo")
	m, _ = sendKey(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.activeForm == nil {
		t.Fatal("expected a params form to open")
	}
	m, _ = sendKey(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.activeForm != nil {
		t.Error("expected esc to abort the form")
	}
	if m.activeJob != nil {
		t.Error("expected no job to have started after abort")
	}
}

func TestPanelJumpKeys(t *testing.T) {
	m := ready(t, testGroups(), t.TempDir(), "/repo")
	press := func(m Model, msg tea.KeyMsg) Model { m, _ = sendKey(t, m, msg); return m }
	right, left := tea.KeyMsg{Type: tea.KeyRight}, tea.KeyMsg{Type: tea.KeyLeft}

	for _, want := range []focusZone{focusJobs, focusOutput, focusOutput} {
		m = press(m, right)
		if m.focus != want {
			t.Fatalf("right: focus = %v, want %v", m.focus, want)
		}
	}
	for _, want := range []focusZone{focusJobs, focusList, focusList} {
		m = press(m, left)
		if m.focus != want {
			t.Fatalf("left: focus = %v, want %v", m.focus, want)
		}
	}

	m = press(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("™")})
	if m.focus != focusJobs {
		t.Fatalf("option+2: focus = %v, want jobs", m.focus)
	}
	m = press(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("1"), Alt: true})
	if m.focus != focusList {
		t.Fatalf("alt+1: focus = %v, want list", m.focus)
	}
}

func TestAdhocCommandRunsTypedTextAsNamedJob(t *testing.T) {
	groups := append([]config.Group{config.AdhocGroup()}, testGroups()...)
	m := ready(t, groups, t.TempDir(), "/repo")
	if m.rows[0].label != "Ad-hoc" {
		t.Fatalf("first row = %q, want the Ad-hoc group above everything else", m.rows[0].label)
	}

	m, _ = sendKey(t, m, tea.KeyMsg{Type: tea.KeyEnter}) // open the form
	if m.activeForm == nil {
		t.Fatal("enter on the ad-hoc entry should open its form")
	}
	m, _ = sendKey(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("echo hi && echo there")})
	m, cmd := sendKey(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	// huh reports completion through a follow-up message.
	for i := 0; cmd != nil && m.activeJob == nil && i < 5; i++ {
		msg := cmd()
		m2, c2 := m.Update(msg)
		m, cmd = m2.(Model), c2
	}
	if m.activeJob == nil {
		t.Fatalf("no job started; form still open = %v", m.activeForm != nil)
	}
	t.Cleanup(m.Shutdown)
	if got, want := m.activeJob.job.Name, "echo hi && echo there"; got != want {
		t.Errorf("job name = %q, want the typed command %q", got, want)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if s, _, _ := m.activeJob.job.Snapshot(); s != jobs.StatusRunning {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	out, _ := m.activeJob.job.Buffer.Snapshot()
	if !strings.Contains(out, "hi\n") || !strings.Contains(out, "there\n") {
		t.Errorf("output = %q, want both echo lines (eval should honour &&)", out)
	}
}

func TestHeaderShowsProfileName(t *testing.T) {
	m := ready(t, testGroups(), t.TempDir(), "/repo")
	if strings.Contains(m.renderHeader(), "profile") {
		t.Error("header shows a profile when none matched")
	}
	m = m.WithProfile("vandag")
	h := m.renderHeader()
	if !strings.Contains(h, "profile") || !strings.Contains(h, "vandag") {
		t.Errorf("header = %q, want it to show the profile name", h)
	}
}

func TestRunNowKeyNeedsRequiredFieldsThenRuns(t *testing.T) {
	groups := []config.Group{{Title: "Global", Commands: []config.ResolvedCommand{{
		ID: "g:greet", Name: "greet", Run: `echo "hi $LC_WHO from $LC_ENV"`,
		Params: []config.Param{
			{Name: "who", Type: config.ParamString, Required: true},
			{Name: "env", Type: config.ParamSelect, Options: config.Opts("dev", "prod")},
			{Name: "tag", Type: config.ParamString, Pattern: `^[a-z]+$`},
		},
	}}}}
	m := ready(t, groups, t.TempDir(), "/repo")
	m, _ = sendKey(t, m, tea.KeyMsg{Type: tea.KeyEnter}) // open the form
	if m.activeForm == nil {
		t.Fatal("form did not open")
	}
	if strings.Contains(m.keyBarText(), "run now") {
		t.Error("run-now hint shown while a required field is empty")
	}

	// Refused while 'who' is empty: form stays open with a notice.
	m, _ = sendKey(t, m, tea.KeyMsg{Type: tea.KeyCtrlJ})
	if m.activeForm == nil || m.activeJob != nil {
		t.Fatal("ctrl+j must not run with a required field empty")
	}
	if !strings.Contains(m.activeForm.notice, "who is required") {
		t.Errorf("notice = %q", m.activeForm.notice)
	}
	if !strings.Contains(m.View(), "who is required") {
		t.Error("the refusal reason should be visible in the modal")
	}

	// Fill only the first field, never tabbing through the rest.
	m, _ = sendKey(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("sam")})
	if !strings.Contains(m.keyBarText(), "alt+enter") || !strings.Contains(m.keyBarText(), "run now") {
		t.Errorf("hint should read alt+enter run now once required fields are set; got %q", m.keyBarText())
	}
	m, _ = sendKey(t, m, tea.KeyMsg{Type: tea.KeyCtrlJ})
	if m.activeForm != nil || m.activeJob == nil {
		t.Fatal("ctrl+j should have run the command")
	}
	t.Cleanup(m.Shutdown)

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if s, _, _ := m.activeJob.job.Snapshot(); s != jobs.StatusRunning {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	out, _ := m.activeJob.job.Buffer.Snapshot()
	if !strings.Contains(out, "hi sam from dev") {
		t.Errorf("output = %q, want the typed value and the select's default option", out)
	}
}

func TestRunNowRefusesPatternMismatch(t *testing.T) {
	groups := []config.Group{{Title: "Global", Commands: []config.ResolvedCommand{{
		ID: "g:x", Name: "x", Run: "true",
		Params: []config.Param{{Name: "tag", Type: config.ParamString, Default: "BAD", Pattern: `^[a-z]+$`}},
	}}}}
	m := ready(t, groups, t.TempDir(), "/repo")
	m, _ = sendKey(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	m, _ = sendKey(t, m, tea.KeyMsg{Type: tea.KeyCtrlJ})
	if m.activeForm == nil || m.activeJob != nil {
		t.Fatal("a value that fails its pattern must not run")
	}
}

func TestRunNowAlsoBoundToAltEnter(t *testing.T) {
	if !isRunNowKey(tea.KeyMsg{Type: tea.KeyEnter, Alt: true}) {
		t.Error("alt+enter should be a run-now key")
	}
	if isRunNowKey(tea.KeyMsg{Type: tea.KeyEnter}) {
		t.Error("plain enter must keep its next-field meaning")
	}
}
