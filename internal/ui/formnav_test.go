package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/x/ansi"

	"lazycmd/internal/config"
)

func navForm(t *testing.T) *paramsForm {
	t.Helper()
	pf := newParamsForm(config.ResolvedCommand{Name: "nav", Params: []config.Param{
		{Name: "env", Type: config.ParamSelect, Options: config.Opts("dev", "staging", "prod")},
		{Name: "tag", Type: config.ParamString},
		{Name: "services", Type: config.ParamMulti, Options: config.Opts("api", "web")},
		{Name: "verbose", Type: config.ParamBool},
	}}, t.TempDir())
	pf.form.Init()
	return pf
}

// press sends k through arrowNav first, as the model does, then to huh.
func press(pf *paramsForm, k tea.KeyMsg) {
	if _, ok := pf.arrowNav(k); ok {
		return
	}
	f, _ := pf.form.Update(k)
	pf.form = f.(*huh.Form)
}

func focusedIndex(pf *paramsForm) int {
	for i, f := range pf.fields {
		if f == pf.form.GetFocusedField() {
			return i
		}
	}
	return -1
}

var (
	keyLeft  = tea.KeyMsg{Type: tea.KeyLeft}
	keyRight = tea.KeyMsg{Type: tea.KeyRight}
)

func TestArrowsPageThroughFields(t *testing.T) {
	pf := navForm(t)
	press(pf, keyLeft) // first field: nothing
	if i := focusedIndex(pf); i != 0 {
		t.Fatalf("← on the first field moved to %d", i)
	}
	for want := 1; want <= 3; want++ {
		press(pf, keyRight)
		if i := focusedIndex(pf); i != want {
			t.Fatalf("after → focus = %d, want %d", i, want)
		}
	}
	press(pf, keyRight) // last field: must not submit
	if pf.form.State != huh.StateNormal || focusedIndex(pf) != 3 {
		t.Fatalf("→ on the last field: state %v, focus %d", pf.form.State, focusedIndex(pf))
	}
	press(pf, keyLeft)
	if i := focusedIndex(pf); i != 2 {
		t.Fatalf("after ← focus = %d, want 2", i)
	}
}

func TestArrowsMoveCursorInTextWithText(t *testing.T) {
	pf := navForm(t)
	press(pf, keyRight) // to tag
	press(pf, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("v1")})
	press(pf, keyLeft)
	if i := focusedIndex(pf); i != 1 {
		t.Fatalf("← in a text field with text moved focus to %d", i)
	}
	press(pf, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})
	if got := pf.form.GetFocusedField().GetValue(); got != "vx1" {
		t.Fatalf("value = %q, want the cursor moved left (vx1)", got)
	}
}

func TestArrowsStayInSelectFilter(t *testing.T) {
	pf := navForm(t)
	press(pf, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	press(pf, keyRight)
	if i := focusedIndex(pf); i != 0 {
		t.Fatalf("→ while filtering moved focus to %d", i)
	}
}

func TestOnlyFocusedFieldShowsSelector(t *testing.T) {
	pf := navForm(t)
	for focusedIndex(pf) != 3 { // the bool, which has no selector
		press(pf, keyRight)
	}
	// Only the empty text field's own prompt (drawn dim) is left.
	if v := ansi.Strip(pf.form.View()); strings.Count(v, ">") != 1 {
		t.Fatalf("an unfocused select shows >:\n%s", v)
	}
}

func TestFormScrollsOnlyWhenFocusLeavesView(t *testing.T) {
	pf := navForm(t)
	pf.form = pf.form.WithWidth(60).WithHeight(12)
	const h = 12
	first := func() bool { return strings.Contains(ansi.Strip(pf.view(60, h)), "env") }

	pf.view(60, h)
	press(pf, keyRight) // to tag, which fits below env
	if !first() {
		t.Fatalf("moving to the second field hid the first:\n%s", ansi.Strip(pf.view(60, h)))
	}
	for focusedIndex(pf) != 3 {
		press(pf, keyRight)
	}
	v := ansi.Strip(pf.view(60, h))
	if !strings.Contains(v, "verbose") || strings.Contains(v, "env") {
		t.Fatalf("the last field should be in view and the first scrolled off:\n%s", v)
	}
	for _, l := range strings.Split(pf.view(60, h), "\n") {
		if w := ansi.StringWidth(l); w > 60 {
			t.Fatalf("line %d wide, form is 60: %q", w, ansi.Strip(l))
		}
	}
	if n := strings.Count(v, "\n") + 1; n != h+1 { // like huh: height plus the line above the help
		t.Fatalf("view is %d lines, want %d", n, h+1)
	}
	press(pf, keyLeft) // back up one: services is still in view, no jump
	if v2 := ansi.Strip(pf.view(60, h)); strings.Contains(v2, "env") || !strings.Contains(v2, "verbose") {
		t.Fatalf("moving up to a field already in view scrolled:\n%s", v2)
	}
}

func TestRequiredMarkGoesWhenFilled(t *testing.T) {
	pf := newParamsForm(config.ResolvedCommand{Name: "req", Params: []config.Param{
		{Name: "tag", Type: config.ParamString, Required: true},
		{Name: "note", Type: config.ParamString},
	}}, t.TempDir())
	cmd := pf.form.Init()
	run := func(c tea.Cmd) { // deliver huh's title update, as the model would
		for _, msg := range drain(c) {
			f, _ := pf.form.Update(msg)
			pf.form = f.(*huh.Form)
		}
	}
	run(cmd)
	view := func() string { return ansi.Strip(pf.view(60, 20)) }
	if !strings.Contains(view(), "tag *") || strings.Contains(view(), "note *") {
		t.Fatalf("want * on tag only:\n%s", view())
	}
	if n := pf.requiredLeft(); n != 1 {
		t.Fatalf("requiredLeft = %d, want 1", n)
	}
	f, c := pf.form.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("v1")})
	pf.form = f.(*huh.Form)
	run(c)
	if strings.Contains(view(), "tag *") || pf.requiredLeft() != 0 {
		t.Fatalf("* still shown after typing:\n%s", view())
	}
}

// drain runs a command and any batch it returns, collecting the messages.
func drain(c tea.Cmd) []tea.Msg {
	if c == nil {
		return nil
	}
	msg := c()
	if b, ok := msg.(tea.BatchMsg); ok {
		var out []tea.Msg
		for _, c := range b {
			out = append(out, drain(c)...)
		}
		return out
	}
	if msg == nil {
		return nil
	}
	return []tea.Msg{msg}
}
