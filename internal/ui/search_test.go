package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"lazycmd/internal/config"
	"lazycmd/internal/jobs"
)

func TestFindMatchesSmartCase(t *testing.T) {
	body := "Error: disk full\nerror again\nERROR!\nfine"
	cases := []struct {
		query string
		want  int
	}{
		{"error", 3}, // all lower case: ignores case
		{"Error", 1}, // a capital makes it exact
		{"ERROR", 1},
		{"or", 3},
		{"zzz", 0},
		{"", 0},
	}
	for _, c := range cases {
		if got := len(findMatches(body, c.query)); got != c.want {
			t.Errorf("%q: %d matches, want %d", c.query, got, c.want)
		}
	}
	// Several per line, never overlapping.
	got := findMatches("aaaa", "aa")
	if len(got) != 2 || got[1].start != 2 {
		t.Errorf("aaaa/aa = %+v", got)
	}
}

func TestSearchIgnoresColourCodesAndKeepsThem(t *testing.T) {
	line := "\x1b[31mERR\x1b[0mor: \x1b[1mdisk\x1b[0m full"
	m := findMatches(line, "error")
	if len(m) != 1 || m[0].start != 0 || m[0].end != 5 {
		t.Fatalf("a word split by a colour change should still match: %+v", m)
	}
	m = findMatches(line, "disk")
	if len(m) != 1 || m[0].start != 7 {
		t.Fatalf("disk = %+v", m)
	}

	out := highlight(line, findMatches(line, "error"), -1)
	if ansi.Strip(out) != ansi.Strip(line) {
		t.Errorf("highlighting changed the text: %q", ansi.Strip(out))
	}
	for _, code := range []string{"\x1b[31m", "\x1b[0m", "\x1b[1m"} {
		if !strings.Contains(out, code) {
			t.Errorf("the original colour %q was lost: %q", code, out)
		}
	}
	if !strings.Contains(out, hlOn+"ERR") || !strings.Contains(out, hlOff) {
		t.Errorf("match not highlighted: %q", out)
	}
	// The colour reset in the middle of the match must not end the highlight.
	if !strings.Contains(out, "\x1b[0m"+hlOn+"or") {
		t.Errorf("the highlight should resume after an escape inside the match: %q", out)
	}
	cur := highlight(line, findMatches(line, "disk"), 0)
	if !strings.Contains(cur, hlCurOn) {
		t.Errorf("the current match should be marked differently: %q", cur)
	}
}

func TestSearchHandlesUnicodeAndBadBytes(t *testing.T) {
	m := findMatches("un café très\xff bon", "é")
	if len(m) != 1 || m[0].start != 6 {
		t.Errorf("é = %+v", m)
	}
	out := highlight("un café très\xff bon", m, 0)
	if !strings.Contains(out, "\xff") || ansi.Strip(strings.ReplaceAll(strings.ReplaceAll(out, hlOn, ""), hlOff, "")) == "" {
		t.Errorf("bad byte mangled: %q", out)
	}
	// An unfinished escape at the end of a line mustn't panic.
	_ = findMatches("abc\x1b[3", "b")
	_ = highlight("abc\x1b", findMatches("abc\x1b", "c"), 0)
}

// searchModel runs a command that prints a few lines and waits for it.
func searchModel(t *testing.T) Model {
	t.Helper()
	m, _ := askModel(t, config.ResolvedCommand{ID: "global:out", Name: "out",
		Run: `printf 'alpha\nBeta line\nalpha beta\n\033[32mgreen alpha\033[0m\n'`})
	m = startNamed(t, m, "out")
	m = pollUntil(t, m, func(m Model) bool {
		s, _, _ := m.activeJob.job.Snapshot()
		return s != jobs.StatusRunning && strings.Contains(ansi.Strip(m.viewport.View()), "green alpha")
	})
	m.focus = focusOutput
	return m
}

func typeText(t *testing.T, m Model, s string) Model {
	t.Helper()
	for _, r := range s {
		m, _ = sendKey(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	return m
}

func TestSearchFlow(t *testing.T) {
	m := searchModel(t)

	m, _ = sendKey(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	if !m.search.editing {
		t.Fatal("/ should open the search line")
	}
	m = typeText(t, m, "alpha")
	if m.searchStatus() != "1/3" {
		t.Errorf("status = %q while typing, want 1/3 (the colour-coded one counts)", m.searchStatus())
	}
	if m.followTail {
		t.Error("searching stops following the output")
	}
	m, _ = sendKey(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.search.editing || m.search.query != "alpha" {
		t.Fatalf("enter keeps the search: %+v", m.search)
	}

	next := func(k string) {
		m, _ = sendKey(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)})
	}
	next("n")
	if m.searchStatus() != "2/3" {
		t.Errorf("after n: %q", m.searchStatus())
	}
	next("n")
	next("n")
	if m.searchStatus() != "1/3" {
		t.Errorf("n wraps round: %q", m.searchStatus())
	}
	next("N")
	if m.searchStatus() != "3/3" {
		t.Errorf("N goes back, wrapping: %q", m.searchStatus())
	}
	if !strings.Contains(m.viewport.View(), hlCurOn) || !strings.Contains(m.viewport.View(), hlOn) {
		t.Error("matches aren't highlighted in the panel")
	}
	if title := ansi.Strip(m.View()); !strings.Contains(title, "· 3/3") {
		t.Errorf("the count should be in the panel title:\n%s", title)
	}

	// esc clears it, and the output is back to plain.
	m, _ = sendKey(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.search.query != "" || strings.Contains(m.viewport.View(), hlOn) || m.searchStatus() != "" {
		t.Errorf("esc should clear the search: %+v", m.search)
	}
}

func TestSearchSmartCaseInThePanel(t *testing.T) {
	m := searchModel(t)
	m, _ = sendKey(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	m = typeText(t, m, "beta")
	if m.searchStatus() != "1/2" { // Beta and beta
		t.Errorf("beta: %q", m.searchStatus())
	}
	m = typeText(t, m, "x")
	if m.searchStatus() != "no matches" {
		t.Errorf("betax: %q", m.searchStatus())
	}
	for range "betax" {
		m, _ = sendKey(t, m, tea.KeyMsg{Type: tea.KeyBackspace})
	}
	m = typeText(t, m, "B")
	if m.searchStatus() != "1/1" { // a capital: exact
		t.Errorf("B: %q", m.searchStatus())
	}
}

func TestSearchEscWhileTypingClearsAndQDoesNotQuit(t *testing.T) {
	m := searchModel(t)
	m, _ = sendKey(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	m = typeText(t, m, "q") // typed into the query, not "quit"
	if m.search.query != "q" {
		t.Fatalf("query = %q", m.search.query)
	}
	m, _ = sendKey(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.search.editing || m.search.query != "" {
		t.Errorf("esc while typing clears it: %+v", m.search)
	}
}

func TestSearchKeepsMatchingNewOutput(t *testing.T) {
	m, _ := askModel(t, config.ResolvedCommand{ID: "global:slow", Name: "slow",
		Run: `echo hit one; sleep 0.4; echo hit two; sleep 5`})
	m = startNamed(t, m, "slow")
	defer m.jobs[0].job.Kill()
	m = pollUntil(t, m, func(m Model) bool { return strings.Contains(ansi.Strip(m.viewport.View()), "hit one") })
	m.focus = focusOutput
	m, _ = sendKey(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	m = typeText(t, m, "hit")
	m, _ = sendKey(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if !strings.HasSuffix(m.searchStatus(), "/1") && !strings.HasSuffix(m.searchStatus(), "/2") {
		t.Fatalf("status = %q", m.searchStatus())
	}
	m = pollUntil(t, m, func(m Model) bool { return strings.HasSuffix(m.searchStatus(), "/2") })
	if m.followTail {
		t.Error("new output must not pull the view away from a search")
	}
}

func TestSlashInOutputWithNoJobDoesNotFilter(t *testing.T) {
	m := ready(t, group(cmd("a", "alpha", "1")), testDir(t), testDir(t))
	m.focus = focusOutput
	m, _ = sendKey(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	if m.filtering || m.search.editing || m.flash == "" {
		t.Errorf("filtering %v, editing %v, flash %q", m.filtering, m.search.editing, m.flash)
	}
	// In the Commands panel, / still filters.
	m.focus = focusList
	m, _ = sendKey(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	if !m.filtering {
		t.Error("/ in the Commands panel should filter")
	}
}

func TestSpacesCanBeTypedInFilterAndSearch(t *testing.T) {
	// A space arrives as KeySpace (with the space in Runes), not KeyRunes.
	space := tea.KeyMsg{Type: tea.KeySpace, Runes: []rune{' '}}

	m := ready(t, group(cmd("a", "git: pull", "1"), cmd("b", "git: push", "1"), cmd("c", "gitpull", "1")), testDir(t), testDir(t))
	m, _ = sendKey(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	m = typeText(t, m, "git:")
	m, _ = sendKey(t, m, space)
	m = typeText(t, m, "pu")
	if m.filterQuery != "git: pu" || strings.Join(rowNames(m), ",") != "git: pull,git: push" {
		t.Errorf("filter %q matches %v", m.filterQuery, rowNames(m))
	}

	m = searchModel(t)
	m, _ = sendKey(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	m = typeText(t, m, "beta")
	m, _ = sendKey(t, m, space)
	m = typeText(t, m, "line")
	if m.search.query != "beta line" || m.searchStatus() != "1/1" {
		t.Errorf("search %q status %q: one space typed, not two", m.search.query, m.searchStatus())
	}
}
