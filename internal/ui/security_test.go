package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"lazycmd/internal/config"
	"lazycmd/internal/usage"
)

// A cloned repo controls .git/HEAD, command names, descriptions and
// warning text. None of it may put a raw escape sequence on the
// terminal: OSC 52 would write the clipboard, OSC 0 the window title.
func TestRepoTextCannotInjectEscapeSequences(t *testing.T) {
	root := testDir(t)
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	head := "ref: refs/heads/main\x1b]52;c;cHduZWQ=\x07\n"
	if err := os.WriteFile(filepath.Join(root, ".git", "HEAD"), []byte(head), 0o644); err != nil {
		t.Fatal(err)
	}
	groups := []config.Group{{Title: "This project", Commands: []config.ResolvedCommand{{
		ID: "project:x", Name: "ok\x1b]0;TITLE\x07\x1b[8mhidden", Run: "true",
		Desc:   "desc\x1b]52;c;eA==\x07",
		Params: []config.Param{{Name: "p\x1b]0;P\x07", Default: "d\x1b]0;D\x07", Options: config.Opts("o\x1b]0;O\x07"), Type: config.ParamSelect}},
	}}}}
	m := New(groups, []string{"warn \x1b]0;W\x07"}, testDir(t), root)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 140, Height: 40})
	m = updated.(Model)

	views := []string{m.View()}
	m, _ = sendKey(t, m, tea.KeyMsg{Type: tea.KeyEnter}) // open the form
	if m.activeForm == nil {
		t.Fatal("expected the params form to open")
	}
	views = append(views, m.View())

	for _, v := range views {
		for _, bad := range []string{"\x1b]", "\x1b[8m", "\x07"} {
			if strings.Contains(v, bad) {
				t.Errorf("view contains raw %q:\n%q", bad, v)
			}
		}
	}
	if !strings.Contains(views[0], "main^[]52") {
		t.Errorf("branch should be shown with the escape made visible; view:\n%s", views[0])
	}
}

func shadowGroups(root string) []config.Group {
	return []config.Group{{Title: "This project", Commands: []config.ResolvedCommand{{
		ID: "project:deploy", Name: "deploy", Run: "echo project",
		Shadows: filepath.Join(root, "global", "deploy.sh"),
	}}}}
}

func TestShadowingCommandAsksBeforeFirstRun(t *testing.T) {
	root := testDir(t)
	stateDir := testDir(t)
	m := ready(t, shadowGroups(root), stateDir, root)

	m, _ = sendKey(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.shadowConfirm == nil || m.activeJob != nil {
		t.Fatalf("first run should ask first; confirm=%v job=%v", m.shadowConfirm, m.activeJob)
	}
	if v := m.View(); !strings.Contains(v, "replaces one of your global scripts") || !strings.Contains(v, "echo project") {
		t.Errorf("warning should name what it hides and what runs; view:\n%s", v)
	}

	// enter is not yes: a stray enter must not run it.
	m, _ = sendKey(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.shadowConfirm != nil || m.activeJob != nil {
		t.Fatal("enter should cancel without running")
	}

	m, _ = sendKey(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	m, _ = sendKey(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
	if m.shadowConfirm != nil || m.activeJob == nil {
		t.Fatal("y should run the command")
	}
	t.Cleanup(func() { m.activeJob.job.Kill() })
	waitDone(t, m)

	// Same session: no second warning.
	m, _ = sendKey(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.shadowConfirm != nil {
		t.Fatal("should not ask again after it has run")
	}
	t.Cleanup(func() { m.activeJob.job.Kill() })

	// New session, same repo: the usage log says it has run, so no warning.
	m2 := ready(t, shadowGroups(root), stateDir, root)
	m2, _ = sendKey(t, m2, tea.KeyMsg{Type: tea.KeyEnter})
	if m2.shadowConfirm != nil {
		t.Fatal("should not ask in a later session once it has run in this repo")
	}
	t.Cleanup(func() { m2.activeJob.job.Kill() })
}

func TestShadowWarningIsPerRepo(t *testing.T) {
	stateDir := testDir(t)
	l := usage.Load(stateDir)
	l.Record("/some/other/repo", "project:deploy", time.Now())
	if err := l.Save(stateDir); err != nil {
		t.Fatal(err)
	}
	root := testDir(t)
	m := ready(t, shadowGroups(root), stateDir, root)
	m, _ = sendKey(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.shadowConfirm == nil {
		t.Fatal("a run in another repo must not skip the warning here")
	}
}

func TestShadowingCommandIsYellow(t *testing.T) {
	root := testDir(t)
	groups := append(shadowGroups(root), config.Group{Title: "Global", Commands: []config.ResolvedCommand{{ID: "global:other", Name: "other", Run: "true"}}})
	prev := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(prev) })

	m := ready(t, groups, testDir(t), root)
	list := m.renderList(40)
	// The SGR parameters for the yellow foreground (on the selected row
	// lipgloss merges them with the background into one sequence).
	sample := lipgloss.NewStyle().Foreground(colorYellow).Render("X")
	yellow := strings.TrimSuffix(strings.TrimPrefix(sample[:strings.Index(sample, "X")], "\x1b["), "m")
	var deployLine, otherLine string
	for _, l := range strings.Split(list, "\n") {
		switch {
		case strings.Contains(l, "deploy"):
			deployLine = l
		case strings.Contains(l, "other"):
			otherLine = l
		}
	}
	if !strings.Contains(deployLine, yellow) {
		t.Errorf("shadowing row should be yellow when selected: %q", deployLine)
	}
	m.moveCursor(1) // select "other", so deploy renders unselected
	for _, l := range strings.Split(m.renderList(40), "\n") {
		if strings.Contains(l, "deploy") && !strings.Contains(l, yellow) {
			t.Errorf("shadowing row should be yellow when not selected: %q", l)
		}
	}
	if strings.Contains(otherLine, yellow) {
		t.Errorf("ordinary row should not be yellow: %q", otherLine)
	}
}

func waitDone(t *testing.T, m Model) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if st, _, _ := m.activeJob.job.Snapshot(); st.String() != "running" {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
}
