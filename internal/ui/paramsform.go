package ui

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"lazycmd/internal/config"
	"lazycmd/internal/safetext"
)

// optionsCmdTimeout bounds an options_cmd run; a var so tests can shorten it.
var optionsCmdTimeout = 5 * time.Second

// optionsResult is one options_cmd run for one set of answers. done is
// closed when it finishes: huh evaluates a field's options and its
// description concurrently, and both read the same run instead of
// starting one each.
type optionsResult struct {
	done chan struct{}
	opts config.OptionList
	err  string // why it produced no options, shown under the field
}

// paramsForm wraps a huh.Form built at runtime from a command's param
// spec (one schema shared by commands.yaml, sidecars and, later,
// features). It's embedded as a sub-model via form.Update/View, never
// run with form.Run().
type paramsForm struct {
	cmd        config.ResolvedCommand
	projectDir string
	form       *huh.Form
	fields     []huh.Field // in order, for ← / → paging
	group      *huh.Group  // the form's only group, drawn by view
	scroll     int         // first content line shown by view

	strVals   map[string]*string
	boolVals  map[string]*bool
	multiVals map[string]*[]string

	notice string // shown in the modal when run-now is refused

	cacheMu sync.Mutex
	cache   map[string]*optionsResult
}

func newParamsForm(cmd config.ResolvedCommand, projectDir string) *paramsForm {
	pf := &paramsForm{
		cmd:        cmd,
		projectDir: projectDir,
		strVals:    map[string]*string{},
		boolVals:   map[string]*bool{},
		multiVals:  map[string]*[]string{},
		cache:      map[string]*optionsResult{},
	}

	fields := make([]huh.Field, 0, len(cmd.Params))
	for _, p := range cmd.Params {
		fields = append(fields, pf.buildField(p))
	}

	pf.fields = fields
	pf.group = huh.NewGroup(fields...)
	pf.form = huh.NewForm(pf.group).
		WithShowHelp(true).
		WithTheme(formTheme()).
		WithKeyMap(escCancelKeyMap())
	return pf
}

// view draws the form's fields and help in width x height. huh pads
// field lines one column wider than the form and relies on its viewport
// to cut that off, so view cuts every line to width too; otherwise the
// panel would cut them and mark each with "…". huh's own
// view scrolls so the focused field is always the top line, which hides
// the first field as soon as you move to the second. This scrolls only
// as far as it must to keep the focused field in view.
func (pf *paramsForm) view(width, height int) string {
	lines := strings.Split(pf.group.Content(), "\n")
	footer := strings.TrimSuffix(pf.group.Footer(), " ")
	avail := height
	if footer != "" {
		avail -= lipgloss.Height(footer)
	}
	avail = max(1, avail)

	if len(lines) > avail {
		top, h := pf.focusSpan()
		switch {
		case h >= avail || top < pf.scroll:
			pf.scroll = top
		case top+h > pf.scroll+avail:
			pf.scroll = top + h - avail
		}
		pf.scroll = max(0, min(pf.scroll, len(lines)-avail))
		lines = lines[pf.scroll : pf.scroll+avail]
	} else {
		pf.scroll = 0
		for len(lines) < avail { // keep the preview at the bottom
			lines = append(lines, "")
		}
	}
	for i, l := range lines {
		lines[i] = ansi.Truncate(l, width, "")
	}
	out := strings.Join(lines, "\n")
	if footer != "" {
		out += "\n\n" + footer
	}
	return out
}

// focusSpan is the focused field's first line and height in the group's
// content, measured the way huh lays the fields out.
func (pf *paramsForm) focusSpan() (top, height int) {
	focused := pf.form.GetFocusedField()
	sep := formTheme().FieldSeparator.Render()
	var b strings.Builder
	for _, f := range pf.fields {
		v := f.View()
		b.WriteString(v)
		if f == focused {
			h := lipgloss.Height(v)
			return lipgloss.Height(b.String()) - h, h
		}
		b.WriteString(sep)
	}
	return 0, 0
}

// formTheme is huh's Charm theme with the unfocused fields toned down:
// only the focused field shows the > selector and a coloured prompt and
// title, so it's clear which one the keys go to.
func formTheme() *huh.Theme {
	t := huh.ThemeCharm()
	t.Blurred.Title = t.Blurred.Title.Foreground(colorTextDim).Bold(false)
	t.Blurred.SelectSelector = lipgloss.NewStyle().SetString("  ")
	t.Blurred.MultiSelectSelector = lipgloss.NewStyle().SetString("  ")
	t.Blurred.TextInput.Prompt = t.Blurred.TextInput.Prompt.Foreground(colorTextDim)
	return t
}

// arrowNav makes ← and → move to the previous and next field. It reports
// false, leaving the key to huh, where the arrows mean something else: a
// text field with text in it (they move the cursor) and a select that is
// being filtered (they move in the filter). It never leaves the form:
// → on the last field and ← on the first do nothing, so an arrow can't
// submit it.
func (pf *paramsForm) arrowNav(k tea.KeyMsg) (tea.Cmd, bool) {
	if k.Type != tea.KeyLeft && k.Type != tea.KeyRight || k.Alt {
		return nil, false
	}
	focused := pf.form.GetFocusedField()
	switch f := focused.(type) {
	case *huh.Input:
		if s, _ := f.GetValue().(string); s != "" {
			return nil, false
		}
	case *huh.Text:
		if s, _ := f.GetValue().(string); s != "" {
			return nil, false
		}
	}
	if filtering(focused) {
		return nil, false
	}
	i := -1
	for j, f := range pf.fields {
		if f == focused {
			i = j
		}
	}
	switch {
	case i < 0:
		return nil, false
	case k.Type == tea.KeyRight && i < len(pf.fields)-1:
		return pf.form.NextField(), true
	case k.Type == tea.KeyLeft && i > 0:
		return pf.form.PrevField(), true
	}
	return nil, true
}

// filtering reports whether a select or multi select is taking typed
// filter text. huh doesn't expose it, but enables its "set filter" key
// only then.
func filtering(f huh.Field) bool {
	for _, b := range f.KeyBinds() {
		if b.Enabled() && b.Help().Desc == "set filter" {
			return true
		}
	}
	return false
}

// escCancelKeyMap adds esc to the form's quit binding. By default only
// ctrl+c aborts a huh form; none of the individual field keymaps bind esc
// to anything, so without this esc would silently do nothing.
func escCancelKeyMap() *huh.KeyMap {
	km := huh.NewDefaultKeyMap()
	km.Quit = key.NewBinding(key.WithKeys("ctrl+c", "esc"))
	// ← / → move between fields in the params form (arrowNav), so a bool
	// toggles with space, h/l or y/n.
	km.Confirm.Toggle = key.NewBinding(key.WithKeys(" ", "h", "l", "left", "right"), key.WithHelp("space", "toggle"))
	return km
}

func (pf *paramsForm) buildField(p config.Param) huh.Field {
	switch p.Type {
	case config.ParamBool:
		v := new(bool)
		*v, _ = strconv.ParseBool(p.Default)
		pf.boolVals[p.Name] = v
		return huh.NewConfirm().Key(p.Name).Title(safetext.Line(p.Name)).Value(v)

	case config.ParamSelect:
		v := new(string)
		*v = p.Default
		pf.strVals[p.Name] = v
		field := huh.NewSelect[string]().Key(p.Name).Title(safetext.Line(p.Name)).Value(v)
		pf.attachSelectOptions(field, p)
		return field

	case config.ParamMulti:
		// Seed the value with the default rather than marking options
		// selected: huh only writes a multi's value back when the field
		// is touched, but it ticks options from the value it's given,
		// including options that arrive later from options_cmd.
		v := new([]string)
		for _, d := range strings.Split(p.Default, ",") {
			if d = strings.TrimSpace(d); d != "" {
				*v = append(*v, d)
			}
		}
		pf.multiVals[p.Name] = v
		field := huh.NewMultiSelect[string]().Key(p.Name).Title(safetext.Line(p.Name)).Value(v)
		pf.attachMultiOptions(field, p)
		return field

	case config.ParamSecret:
		v := new(string)
		*v = p.Default
		pf.strVals[p.Name] = v
		return huh.NewInput().Key(p.Name).Title(safetext.Line(p.Name)).EchoMode(huh.EchoModePassword).Value(v)

	case config.ParamText:
		v := new(string)
		*v = p.Default
		pf.strVals[p.Name] = v
		return huh.NewText().Key(p.Name).Title(safetext.Line(p.Name)).Value(v)

	default: // config.ParamString
		v := new(string)
		*v = p.Default
		pf.strVals[p.Name] = v
		field := huh.NewInput().Key(p.Name).Title(requiredTitle(p, v)).Value(v)
		if p.Required {
			// huh writes the text back on every keystroke, so the * goes
			// as soon as something is typed. The bindings include a
			// constant: huh only re-runs the func when their hash changes.
			field.TitleFunc(func() string { return requiredTitle(p, v) }, []any{"title", v})
		}
		return field.Validate(stringValidator(p))
	}
}

// requiredMark follows the title of a required field while it's empty.
var requiredMark = lipgloss.NewStyle().Foreground(colorAmber).Bold(true).Render(" *")

func requiredTitle(p config.Param, v *string) string {
	if p.Required && *v == "" {
		return safetext.Line(p.Name) + requiredMark
	}
	return safetext.Line(p.Name)
}

// requiredLeft counts the required fields that are still empty.
func (pf *paramsForm) requiredLeft() int {
	n := 0
	for _, p := range pf.cmd.Params {
		if v := pf.strVals[p.Name]; p.Required && v != nil && *v == "" {
			n++
		}
	}
	return n
}

func stringValidator(p config.Param) func(string) error {
	var pattern *regexp.Regexp
	if p.Pattern != "" {
		// Patterns are checked when the command loads; a bad one that
		// slips through is ignored here rather than crashing the UI.
		pattern, _ = regexp.Compile(p.Pattern)
	}
	return func(s string) error {
		if p.Required && s == "" {
			return fmt.Errorf("%s is required", safetext.Line(p.Name))
		}
		if pattern != nil && s != "" && !pattern.MatchString(s) {
			return fmt.Errorf("%s must match %s", safetext.Line(p.Name), safetext.Line(p.Pattern))
		}
		return nil
	}
}

func (pf *paramsForm) attachSelectOptions(field *huh.Select[string], p config.Param) {
	if p.OptionsCmd == "" {
		field.Options(pf.toOptions(p, p.Options)...)
		return
	}
	deps := pf.dependencyPointers(p.DependsOn)
	field.OptionsFunc(func() []huh.Option[string] {
		return pf.toOptions(p, pf.runOptionsCmd(p.OptionsCmd))
	}, deps)
	// No validator needed for enter: huh won't move past a select with no
	// options. The run-now key skips huh, so problems() checks it.
	field.DescriptionFunc(func() string { return pf.optionsNote(p.OptionsCmd) }, deps)
}

func (pf *paramsForm) attachMultiOptions(field *huh.MultiSelect[string], p config.Param) {
	if p.OptionsCmd == "" {
		field.Options(pf.toOptions(p, p.Options)...)
		return
	}
	deps := pf.dependencyPointers(p.DependsOn)
	field.OptionsFunc(func() []huh.Option[string] {
		return pf.toOptions(p, pf.runOptionsCmd(p.OptionsCmd))
	}, deps)
	field.DescriptionFunc(func() string { return pf.optionsNote(p.OptionsCmd) }, deps)
}

// optionsNote is the line under an options_cmd field: why it failed, or
// that it ran fine but listed nothing. Empty when there are options.
func (pf *paramsForm) optionsNote(cmdStr string) string {
	r := pf.optionsFor(cmdStr)
	switch {
	case r.err != "":
		return lipgloss.NewStyle().Foreground(colorRed).Render("⚠ options_cmd failed: " + r.err)
	case len(r.opts) == 0:
		return lipgloss.NewStyle().Foreground(colorTextDim).Render("no options")
	}
	return ""
}

// toOptions turns a param's options into huh options: labels from the
// options or the param's label mode, values as given.
func (pf *paramsForm) toOptions(p config.Param, opts config.OptionList) []huh.Option[string] {
	opts = config.WithLabels(opts, p.Label, pf.projectDir)
	out := make([]huh.Option[string], len(opts))
	for i, o := range opts {
		// The label is drawn, so it is cleaned; the value is what the
		// command receives, so it stays exactly as given.
		out[i] = huh.NewOption(safetext.Line(o.Display()), o.Value)
	}
	return out
}

// dependencyPointers is the bindings huh watches to decide when to re-run
// an OptionsFunc. huh runs it whenever the bindings' hash differs from
// the last one, starting from 0, and nil bindings hash to 0 too, so a
// param with no depends_on would never load its options. The leading
// constant makes the hash non-zero, so they load once when the form opens.
func (pf *paramsForm) dependencyPointers(names []string) []any {
	deps := []any{"options"}
	for _, n := range names {
		switch {
		case pf.strVals[n] != nil:
			deps = append(deps, pf.strVals[n])
		case pf.boolVals[n] != nil:
			deps = append(deps, pf.boolVals[n])
		case pf.multiVals[n] != nil:
			deps = append(deps, pf.multiVals[n])
		}
	}
	return deps
}

// runOptionsCmd returns cmdStr's options for the answers so far.
func (pf *paramsForm) runOptionsCmd(cmdStr string) config.OptionList {
	return pf.optionsFor(cmdStr).opts
}

// optionsFor runs cmdStr in the project root with every param value
// collected so far injected as LC_<NAME>, so a dependent select can read
// an earlier answer. Results, failures included, are cached per (command,
// answers) pair; a caller that arrives while a run is in flight waits for
// it rather than starting another.
func (pf *paramsForm) optionsFor(cmdStr string) *optionsResult {
	env := pf.currentEnv()
	key := cmdStr + "\x00" + strings.Join(env, "\x00")

	pf.cacheMu.Lock()
	if r, ok := pf.cache[key]; ok {
		pf.cacheMu.Unlock()
		<-r.done
		return r
	}
	r := &optionsResult{done: make(chan struct{})}
	pf.cache[key] = r
	pf.cacheMu.Unlock()

	r.opts, r.err = runOptions(cmdStr, pf.projectDir, env)
	close(r.done)
	return r
}

// finishedOptions returns the run for the current answers if it has
// already finished, without starting or waiting for one. For use from
// the UI goroutine, which must never block on a command.
func (pf *paramsForm) finishedOptions(cmdStr string) (*optionsResult, bool) {
	key := cmdStr + "\x00" + strings.Join(pf.currentEnv(), "\x00")
	pf.cacheMu.Lock()
	r, ok := pf.cache[key]
	pf.cacheMu.Unlock()
	if !ok {
		return nil, false
	}
	select {
	case <-r.done:
		return r, true
	default:
		return nil, false
	}
}

// runOptions runs one options_cmd and returns its options, or why there
// are none: a timeout, or the exit code with the first line of stderr.
func runOptions(cmdStr, dir string, env []string) (config.OptionList, string) {
	ctx, cancel := context.WithTimeout(context.Background(), optionsCmdTimeout)
	defer cancel()
	c := exec.CommandContext(ctx, "sh", "-c", cmdStr)
	c.Dir = dir
	c.Env = append(os.Environ(), env...)
	// A child the shell started (sleep, a hung CLI) can keep the output
	// pipe open after sh is killed; don't wait on it past the timeout.
	c.WaitDelay = 500 * time.Millisecond
	var stderr strings.Builder
	c.Stderr = &stderr

	out, err := c.Output()
	switch {
	case ctx.Err() == context.DeadlineExceeded:
		return nil, fmt.Sprintf("timed out after %s", optionsCmdTimeout)
	case err != nil:
		msg := err.Error()
		if ee, ok := err.(*exec.ExitError); ok {
			msg = fmt.Sprintf("exit %d", ee.ExitCode())
		}
		for _, line := range strings.Split(stderr.String(), "\n") {
			if line = strings.TrimSpace(line); line != "" {
				msg += ": " + line
				break
			}
		}
		return nil, shortNote(msg)
	}
	return config.ParseOptionLines(string(out)), "" // "value<TAB>label" or just the value
}

// shortNote cleans msg for display and caps it, since it can come from
// any command's stderr.
func shortNote(msg string) string {
	const max = 200
	msg = safetext.Line(msg)
	if r := []rune(msg); len(r) > max {
		msg = string(r[:max-1]) + "…"
	}
	return msg
}

func (pf *paramsForm) currentEnv() []string {
	env := make([]string, 0, len(pf.strVals)+len(pf.boolVals)+len(pf.multiVals))
	for name, v := range pf.strVals {
		env = append(env, envVar(name, *v))
	}
	for name, v := range pf.boolVals {
		env = append(env, envVar(name, strconv.FormatBool(*v)))
	}
	for name, v := range pf.multiVals {
		env = append(env, envVar(name, strings.Join(*v, ",")))
	}
	sort.Strings(env)
	return env
}

func envVar(name, value string) string {
	return "LC_" + strings.ToUpper(name) + "=" + value
}

// values collects the final answers once the form is complete, in the
// LC_<NAME> shape jobs.Start expects.
func (pf *paramsForm) values() map[string]string {
	values := make(map[string]string, len(pf.cmd.Params))
	for _, p := range pf.cmd.Params {
		switch p.Type {
		case config.ParamBool:
			values[p.Name] = strconv.FormatBool(*pf.boolVals[p.Name])
		case config.ParamMulti:
			values[p.Name] = strings.Join(*pf.multiVals[p.Name], ",")
		default:
			values[p.Name] = *pf.strVals[p.Name]
		}
	}
	return values
}

// preview renders, as a single line, what the command will actually do
// with the current answers: the run body with LC_<NAME> substituted for
// display, or the script path plus the env it'll receive. Recomputed
// fresh on every render, which is what makes it "live" without wiring
// into huh's own binding system. One line because the panel it sits in
// has a fixed height budget and huh pads its form to fill whatever
// height it's given, so anything after the form gets clipped; keeping
// this above the form and single-line keeps its height predictable.
func (pf *paramsForm) preview() string {
	values := pf.displayValues()
	if pf.cmd.Preview != nil {
		return "$ " + pf.cmd.Preview(values)
	}
	if pf.cmd.Run != "" {
		body := pf.cmd.Run
		for name, val := range values {
			body = strings.ReplaceAll(body, "$LC_"+strings.ToUpper(name), val)
			body = strings.ReplaceAll(body, "${LC_"+strings.ToUpper(name)+"}", val)
		}
		return "$ " + strings.Join(strings.Fields(body), " ")
	}

	if len(pf.cmd.Args) > 0 {
		argv := append([]string{pf.cmd.ScriptPath}, config.ExpandArgs(pf.cmd.Args, values)...)
		for i, a := range argv {
			argv[i] = shellQuote(a)
		}
		return "$ " + strings.Join(argv, " ")
	}

	parts := make([]string, len(pf.cmd.Params))
	for i, p := range pf.cmd.Params {
		parts[i] = envVar(p.Name, values[p.Name])
	}
	return "$ " + pf.cmd.ScriptPath + "  " + strings.Join(parts, " ")
}

// secretMask stands in for a secret's value wherever the form shows it.
const secretMask = "••••••"

// displayValues is values with every non-empty secret replaced by
// secretMask, for the preview: the value is typed with masked echo, so
// printing it in plain text right below the field would undo that for
// anyone looking at the screen (or a screen share). The real values
// still go to the command.
func (pf *paramsForm) displayValues() map[string]string {
	values := pf.values()
	for _, p := range pf.cmd.Params {
		if p.Type == config.ParamSecret && values[p.Name] != "" {
			values[p.Name] = secretMask
		}
	}
	return values
}

// shellQuote quotes a for display in the preview line only (jobs never
// go through a shell for script args), so an argument with spaces reads
// as one argument.
func shellQuote(a string) string {
	if a != "" && !strings.ContainsAny(a, " \t\n'\"$&;|<>()*?[]{}\\`!#~") {
		return a
	}
	return "'" + strings.ReplaceAll(a, "'", `'\''`) + "'"
}

// problems lists what stops the form from running right now: a required
// string param that is empty, or a value that doesn't match its pattern.
// It is the same check each field applies when you press enter on it, so
// "run now" never accepts anything the field-by-field path would reject.
func (pf *paramsForm) problems() []string {
	var out []string
	for _, p := range pf.cmd.Params {
		if p.Type == config.ParamSelect && p.OptionsCmd != "" && *pf.strVals[p.Name] == "" {
			name := safetext.Line(p.Name)
			switch r, done := pf.finishedOptions(p.OptionsCmd); {
			case !done:
				out = append(out, name+": options are still loading")
			case r.err != "":
				out = append(out, name+": nothing to pick (options_cmd failed)")
			case len(r.opts) == 0:
				out = append(out, name+": nothing to pick (no options)")
			}
			continue
		}
		if p.Type != config.ParamString && p.Type != "" {
			continue
		}
		v := pf.strVals[p.Name]
		if v == nil {
			continue
		}
		if err := stringValidator(p)(*v); err != nil {
			out = append(out, err.Error())
		}
	}
	return out
}
