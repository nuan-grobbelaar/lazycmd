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
	"github.com/charmbracelet/huh"

	"lazycmd/internal/config"
)

const optionsCmdTimeout = 5 * time.Second

// paramsForm wraps a huh.Form built at runtime from a command's param
// spec (one schema shared by commands.yaml, sidecars and, later,
// features). It's embedded as a sub-model via form.Update/View, never
// run with form.Run().
type paramsForm struct {
	cmd        config.ResolvedCommand
	projectDir string
	form       *huh.Form

	strVals   map[string]*string
	boolVals  map[string]*bool
	multiVals map[string]*[]string

	cacheMu sync.Mutex
	cache   map[string][]string
}

func newParamsForm(cmd config.ResolvedCommand, projectDir string) *paramsForm {
	pf := &paramsForm{
		cmd:        cmd,
		projectDir: projectDir,
		strVals:    map[string]*string{},
		boolVals:   map[string]*bool{},
		multiVals:  map[string]*[]string{},
		cache:      map[string][]string{},
	}

	fields := make([]huh.Field, 0, len(cmd.Params))
	for _, p := range cmd.Params {
		fields = append(fields, pf.buildField(p))
	}

	pf.form = huh.NewForm(huh.NewGroup(fields...)).
		WithShowHelp(true).
		WithKeyMap(escCancelKeyMap())
	return pf
}

// escCancelKeyMap adds esc to the form's quit binding. By default only
// ctrl+c aborts a huh form; none of the individual field keymaps bind esc
// to anything, so without this esc would silently do nothing.
func escCancelKeyMap() *huh.KeyMap {
	km := huh.NewDefaultKeyMap()
	km.Quit = key.NewBinding(key.WithKeys("ctrl+c", "esc"))
	return km
}

func (pf *paramsForm) buildField(p config.Param) huh.Field {
	switch p.Type {
	case config.ParamBool:
		v := new(bool)
		*v, _ = strconv.ParseBool(p.Default)
		pf.boolVals[p.Name] = v
		return huh.NewConfirm().Key(p.Name).Title(p.Name).Value(v)

	case config.ParamSelect:
		v := new(string)
		*v = p.Default
		pf.strVals[p.Name] = v
		field := huh.NewSelect[string]().Key(p.Name).Title(p.Name).Value(v)
		pf.attachSelectOptions(field, p)
		return field

	case config.ParamMulti:
		v := new([]string)
		pf.multiVals[p.Name] = v
		field := huh.NewMultiSelect[string]().Key(p.Name).Title(p.Name).Value(v)
		pf.attachMultiOptions(field, p)
		return field

	case config.ParamSecret:
		v := new(string)
		*v = p.Default
		pf.strVals[p.Name] = v
		return huh.NewInput().Key(p.Name).Title(p.Name).EchoMode(huh.EchoModePassword).Value(v)

	case config.ParamText:
		v := new(string)
		*v = p.Default
		pf.strVals[p.Name] = v
		return huh.NewText().Key(p.Name).Title(p.Name).Value(v)

	default: // config.ParamString
		v := new(string)
		*v = p.Default
		pf.strVals[p.Name] = v
		field := huh.NewInput().Key(p.Name).Title(p.Name).Value(v)
		return field.Validate(stringValidator(p))
	}
}

func stringValidator(p config.Param) func(string) error {
	var pattern *regexp.Regexp
	if p.Pattern != "" {
		pattern = regexp.MustCompile(p.Pattern)
	}
	return func(s string) error {
		if p.Required && s == "" {
			return fmt.Errorf("%s is required", p.Name)
		}
		if pattern != nil && s != "" && !pattern.MatchString(s) {
			return fmt.Errorf("%s must match %s", p.Name, p.Pattern)
		}
		return nil
	}
}

func (pf *paramsForm) attachSelectOptions(field *huh.Select[string], p config.Param) {
	if p.OptionsCmd == "" {
		field.Options(toOptions(p.Options)...)
		return
	}
	deps := pf.dependencyPointers(p.DependsOn)
	field.OptionsFunc(func() []huh.Option[string] {
		return toOptions(pf.runOptionsCmd(p.OptionsCmd))
	}, deps)
}

func (pf *paramsForm) attachMultiOptions(field *huh.MultiSelect[string], p config.Param) {
	if p.OptionsCmd == "" {
		opts := toOptions(p.Options)
		// Default is a comma-separated list of options to preselect.
		if p.Default != "" {
			want := map[string]bool{}
			for _, d := range strings.Split(p.Default, ",") {
				want[strings.TrimSpace(d)] = true
			}
			for i, o := range opts {
				opts[i] = o.Selected(want[o.Value])
			}
		}
		field.Options(opts...)
		return
	}
	deps := pf.dependencyPointers(p.DependsOn)
	field.OptionsFunc(func() []huh.Option[string] {
		return toOptions(pf.runOptionsCmd(p.OptionsCmd))
	}, deps)
}

func toOptions(values []string) []huh.Option[string] {
	opts := make([]huh.Option[string], len(values))
	for i, v := range values {
		opts[i] = huh.NewOption(v, v)
	}
	return opts
}

// dependencyPointers resolves depends_on names to the bound pointers of
// those earlier fields, which is what OptionsFunc hashes to decide
// whether to re-run. Params must declare their dependencies earlier in
// the list; an unresolved name is silently dropped.
func (pf *paramsForm) dependencyPointers(names []string) []any {
	var deps []any
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

// runOptionsCmd runs cmdStr in the project root with every param value
// collected so far injected as LC_<NAME>, so a dependent select can read
// an earlier answer. Results are cached per (command, env) pair.
func (pf *paramsForm) runOptionsCmd(cmdStr string) []string {
	env := pf.currentEnv()
	key := cmdStr + "\x00" + strings.Join(env, "\x00")

	pf.cacheMu.Lock()
	if cached, ok := pf.cache[key]; ok {
		pf.cacheMu.Unlock()
		return cached
	}
	pf.cacheMu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), optionsCmdTimeout)
	defer cancel()
	c := exec.CommandContext(ctx, "sh", "-c", cmdStr)
	c.Dir = pf.projectDir
	c.Env = append(os.Environ(), env...)

	var opts []string
	if out, err := c.Output(); err == nil {
		for _, line := range strings.Split(string(out), "\n") {
			if line = strings.TrimSpace(line); line != "" {
				opts = append(opts, line)
			}
		}
	}

	pf.cacheMu.Lock()
	pf.cache[key] = opts
	pf.cacheMu.Unlock()
	return opts
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
	values := pf.values()
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

	parts := make([]string, len(pf.cmd.Params))
	for i, p := range pf.cmd.Params {
		parts[i] = envVar(p.Name, values[p.Name])
	}
	return "$ " + pf.cmd.ScriptPath + "  " + strings.Join(parts, " ")
}
