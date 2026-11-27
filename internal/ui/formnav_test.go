package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/x/ansi"

	"lazycmd/internal/ask"
	"lazycmd/internal/config"
)

func navForm(t *testing.T) *paramsForm {
	t.Helper()
	pf := newParamsForm(config.ResolvedCommand{Name: "nav", Params: []config.Param{
		{Name: "env", Type: config.ParamSelect, Options: config.Opts("dev", "staging", "prod")},
		{Name: "tag", Type: config.ParamString},
		{Name: "services", Type: config.ParamMulti, Options: config.Opts("api", "web")},
		{Name: "verbose", Type: config.ParamBool},
	}}, testDir(t))
	pf.form.Init()
	return pf
}

// press sends k the way the model does: the edit mode first, then the
// arrow paging, then huh.
func press(pf *paramsForm, k tea.KeyMsg) {
	pf.syncFocus()
	if pf.textKey(k) {
		return
	}
	if _, ok := pf.arrowNav(k); ok {
		return
	}
	f, _ := pf.form.Update(k)
	pf.form = f.(*huh.Form)
	pf.syncFocus()
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

var keySpace = tea.KeyMsg{Type: tea.KeySpace, Runes: []rune{' '}}

func typeInto(pf *paramsForm, s string) {
	press(pf, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)})
}

func TestTextFieldNeedsSpaceBeforeItTakesKeys(t *testing.T) {
	pf := navForm(t)
	press(pf, keyRight) // to tag, a text field

	// Not being edited: typing does nothing but hint, and the arrows page.
	typeInto(pf, "v1")
	if got := pf.form.GetFocusedField().GetValue(); got != "" {
		t.Fatalf("typed %q into a field that isn't being edited", got)
	}
	if !strings.Contains(pf.notice, "press space to edit") {
		t.Errorf("notice = %q", pf.notice)
	}
	press(pf, keyRight)
	press(pf, keyLeft)
	if focusedIndex(pf) != 1 {
		t.Fatalf("focus = %d: the arrows should page over an empty text field", focusedIndex(pf))
	}

	// Space starts editing, and isn't typed itself.
	press(pf, keySpace)
	if !pf.editing || pf.form.GetFocusedField().GetValue() != "" {
		t.Fatalf("editing %v, value %q", pf.editing, pf.form.GetFocusedField().GetValue())
	}
	typeInto(pf, "v1")
	press(pf, keyLeft) // the cursor, not the field
	if focusedIndex(pf) != 1 {
		t.Fatalf("← while editing moved focus to %d", focusedIndex(pf))
	}
	typeInto(pf, "x")
	if got := pf.form.GetFocusedField().GetValue(); got != "vx1" {
		t.Fatalf("value = %q, want the cursor moved left (vx1)", got)
	}

	// esc stops editing without leaving the form; the arrows page again,
	// even though the field now has text in it.
	press(pf, tea.KeyMsg{Type: tea.KeyEsc})
	if pf.editing || pf.form.State != huh.StateNormal {
		t.Fatalf("esc: editing %v, state %v", pf.editing, pf.form.State)
	}
	press(pf, keyRight)
	if focusedIndex(pf) != 2 {
		t.Errorf("focus = %d: the arrows should page over a text field with text", focusedIndex(pf))
	}
	if got := pf.fields[1].GetValue(); got != "vx1" {
		t.Errorf("the text was lost: %q", got)
	}
}

func TestEditingEndsWhenFocusMoves(t *testing.T) {
	pf := navForm(t)
	press(pf, keyRight)
	press(pf, keySpace)
	if !pf.editing {
		t.Fatal("setup")
	}
	// What tab does (huh moves the focus through a follow-up message).
	pf.form.NextField()
	pf.syncFocus()
	if pf.editing {
		t.Fatal("editing must end when the focus moves on")
	}
	pf.form.PrevField()
	pf.syncFocus()
	if pf.editing || focusedIndex(pf) != 1 {
		t.Errorf("editing %v focus %d: coming back to a text field starts in navigation", pf.editing, focusedIndex(pf))
	}
	typeInto(pf, "z")
	if pf.form.GetFocusedField().GetValue() != "" {
		t.Error("typing should be ignored until space")
	}
}

func TestPasteStartsEditing(t *testing.T) {
	pf := navForm(t)
	press(pf, keyRight)
	press(pf, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("pasted"), Paste: true})
	if !pf.editing || pf.form.GetFocusedField().GetValue() != "pasted" {
		t.Errorf("editing %v value %q", pf.editing, pf.form.GetFocusedField().GetValue())
	}
}

func TestOtherFieldsAreNotAffectedByEditMode(t *testing.T) {
	pf := navForm(t)
	// env is a select: letters filter/move it, space isn't swallowed, and
	// there's no edit mode to enter.
	if pf.onTextField() || pf.textKey(keySpace) {
		t.Error("a select shouldn't have an edit mode")
	}
}

func TestEditModeThroughTheModel(t *testing.T) {
	m := ready(t, group(config.ResolvedCommand{ID: "global:t", Name: "t", Run: "true",
		Params: []config.Param{{Name: "a", Type: config.ParamString}, {Name: "b", Type: config.ParamString}}}), testDir(t), testDir(t))
	m, _ = sendKey(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.activeForm == nil {
		t.Fatal("the form should be open")
	}
	if m.keyBarText() != "" {
		t.Errorf("the bottom bar should say nothing while a form is open: %q", m.keyBarText())
	}
	if hints := ansi.Strip(m.activeForm.hintLine(100)); !strings.Contains(hints, "space edit") || strings.Contains(hints, "stop editing") {
		t.Errorf("form hints = %q", hints)
	}

	m, _ = sendKey(t, m, keySpace)
	if hints := ansi.Strip(m.activeForm.hintLine(100)); !strings.Contains(hints, "esc stop editing") || strings.Contains(hints, "esc cancel") {
		t.Errorf("form hints while editing = %q", hints)
	}
	m, _ = sendKey(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.activeForm == nil {
		t.Fatal("esc while editing should stop editing, not cancel the form")
	}
	m, _ = sendKey(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.activeForm != nil {
		t.Error("esc when not editing still cancels the form")
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
	}}, testDir(t))
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

func TestFormHintLineFollowsTheFocusedField(t *testing.T) {
	pf := newParamsForm(config.ResolvedCommand{Name: "h", Params: []config.Param{
		{Name: "env", Type: config.ParamSelect, Options: config.Opts("dev", "prod")},
		{Name: "services", Type: config.ParamMulti, Options: config.Opts("api", "web")},
		{Name: "tag", Type: config.ParamString, Required: true},
		{Name: "dry", Type: config.ParamBool},
	}}, testDir(t))
	pf.form.Init()
	hints := func() string { return ansi.Strip(pf.hintLine(200)) }
	next := func() { press(pf, keyRight) }

	// A select: its own keys, the form's, and how to leave.
	for _, want := range []string{"filter", "←/→ fields", "* 1 required field empty", "esc cancel"} {
		if !strings.Contains(hints(), want) {
			t.Errorf("select: %q missing from %q", want, hints())
		}
	}
	next()
	if !strings.Contains(hints(), "toggle") || !strings.Contains(hints(), "←/→ fields") {
		t.Errorf("multi: %q", hints())
	}
	next() // the required text field
	if h := hints(); !strings.Contains(h, "space edit") || !strings.Contains(h, "* 1 required field empty") {
		t.Errorf("text: %q", h)
	}
	press(pf, keySpace)
	if h := hints(); !strings.Contains(h, "esc stop editing") || strings.Contains(h, "esc cancel") || !strings.Contains(h, "* 1 required") {
		t.Errorf("editing: %q (esc stops editing; the required count still shows)", h)
	}
	typeInto(pf, "v1")
	press(pf, tea.KeyMsg{Type: tea.KeyEsc})
	next()
	if h := hints(); !strings.Contains(h, "space toggle") || !strings.Contains(h, "alt+enter run now") || strings.Contains(h, "required") {
		t.Errorf("bool, ready to run: %q", h)
	}

	// Never wider than the form. When one line won't hold it all, the
	// field's keys and the form's keys get a line each, so "esc cancel"
	// is never the part that's cut.
	for _, w := range []int{20, 40, 60} {
		for _, l := range strings.Split(pf.hintLine(w), "\n") {
			if ansi.StringWidth(l) > w {
				t.Errorf("width %d: a line is %d wide: %q", w, ansi.StringWidth(l), ansi.Strip(l))
			}
		}
	}
	two := strings.Split(ansi.Strip(pf.hintLine(30)), "\n")
	if len(two) != 2 || !strings.HasSuffix(two[0], "…") || !strings.Contains(two[1], "esc cancel") {
		t.Errorf("a narrow form should put the form's keys on their own line: %q", two)
	}
	if one := pf.hintLine(200); strings.Contains(one, "\n") {
		t.Errorf("everything fits at width 200, so one line: %q", one)
	}
}

func TestAskPopupCarriesItsOwnHints(t *testing.T) {
	m, srv := askModel(t, config.ResolvedCommand{ID: "global:q", Name: "q", Run: "sleep 10"})
	m = startNamed(t, m, "q")
	defer m.jobs[0].job.Kill()
	sendAsk(srv, m.jobs[0].askToken, ask.Request{Kind: ask.KindPick, Prompt: "Which one?", Options: []ask.Option{{Value: "a"}, {Value: "b"}}})
	m = pollUntil(t, m, func(m Model) bool { return m.askPopup != nil })

	if m.keyBarText() != "" {
		t.Errorf("the bottom bar should say nothing while a popup is open: %q", m.keyBarText())
	}
	if view := ansi.Strip(m.View()); !strings.Contains(view, "esc cancel (the script sees exit 1)") {
		t.Errorf("the cancel hint should be in the popup:\n%s", view)
	}
}
