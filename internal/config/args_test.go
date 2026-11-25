package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestExpandArgs(t *testing.T) {
	vals := map[string]string{"env": "staging", "svc": "my api; rm -rf ~", "empty": "", "tags": "a,b", "dry": "true"}
	cases := []struct {
		name string
		args []string
		want []string
	}{
		{"plain and placeholder", []string{"--env", "{env}"}, []string{"--env", "staging"}},
		{"value with spaces and ; stays one arg", []string{"{svc}"}, []string{"my api; rm -rf ~"}},
		{"joined in one entry", []string{"--env={env}", "--tags={tags}"}, []string{"--env=staging", "--tags=a,b"}},
		{"all-empty entry dropped", []string{"--x={empty}", "keep"}, []string{"keep"}},
		{"partly empty entry kept", []string{"{empty}-{env}"}, []string{"-staging"}},
		{"unknown param counts as empty", []string{"--y={nope}"}, []string{}},
		{"bool as text", []string{"--dry={dry}"}, []string{"--dry=true"}},
		{"escaped braces", []string{"{{literal}}", "{{{env}}}"}, []string{"{literal}", "{staging}"}},
		{"literal-only entry kept even if empty", []string{""}, []string{""}},
	}
	for _, c := range cases {
		if got := ExpandArgs(c.args, vals); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

func TestValidateArgs(t *testing.T) {
	params := []Param{{Name: "env", Type: ParamString}}
	ok := ResolvedCommand{Name: "x", ScriptPath: "/s.sh", Params: params, Args: []string{"--env={env}"}}
	if err := validateArgs(ok); err != nil {
		t.Errorf("valid args rejected: %v", err)
	}
	bad := map[string]ResolvedCommand{
		"no script":     {Name: "x", Run: "echo", Params: params, Args: []string{"{env}"}},
		"unknown param": {Name: "x", ScriptPath: "/s.sh", Params: params, Args: []string{"{nope}"}},
		"unclosed":      {Name: "x", ScriptPath: "/s.sh", Params: params, Args: []string{"{env"}},
		"stray close":   {Name: "x", ScriptPath: "/s.sh", Params: params, Args: []string{"env}"}},
		"empty name":    {Name: "x", ScriptPath: "/s.sh", Params: params, Args: []string{"{}"}},
	}
	for name, c := range bad {
		if err := validateArgs(c); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

func TestSidecarArgsLoadAndInvalidOnesAreWarnings(t *testing.T) {
	global := t.TempDir()
	dir := filepath.Join(global, "scripts")
	exe(t, filepath.Join(dir, "deploy.sh"), "#!/bin/sh\n")
	writeFile(t, filepath.Join(dir, "deploy.lazycmd.yaml"), `
params:
  - {name: env, type: string}
args: ["--env", "{env}"]
`)
	exe(t, filepath.Join(dir, "broken.sh"), "#!/bin/sh\n")
	writeFile(t, filepath.Join(dir, "broken.lazycmd.yaml"), `
args: ["{ghost}"]
`)

	groups, warnings := Discover(global, t.TempDir())
	var got *ResolvedCommand
	names := []string{}
	for _, g := range groups {
		for i, c := range g.Commands {
			names = append(names, c.Name)
			if c.Name == "deploy" {
				got = &g.Commands[i]
			}
		}
	}
	if got == nil || !reflect.DeepEqual(got.Args, []string{"--env", "{env}"}) {
		t.Fatalf("deploy = %+v (commands %v)", got, names)
	}
	for _, n := range names {
		if n == "broken" {
			t.Error("a command with invalid args should be skipped")
		}
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "ghost") {
		t.Errorf("warnings = %v, want one about {ghost}", warnings)
	}
}

func TestCommandsFileArgsRequireScript(t *testing.T) {
	dir := t.TempDir()
	exe(t, filepath.Join(dir, "run.sh"), "#!/bin/sh\n")
	writeFile(t, filepath.Join(dir, "commands.yaml"), `
commands:
  good:
    script: run.sh
    params: [{name: a, type: string}]
    args: ["{a}"]
  bad:
    run: echo hi
    params: [{name: a, type: string}]
    args: ["{a}"]
`)
	cmds, warnings := loadCommandsFile(dir)
	if len(cmds) != 1 || cmds[0].Name != "good" || cmds[0].ScriptPath != filepath.Join(dir, "run.sh") {
		t.Errorf("cmds = %+v", cmds)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], `"bad"`) {
		t.Errorf("warnings = %v", warnings)
	}
}

// Arguments are visible to every user via ps, so a secret param may
// reach a command only through its LC_ env var.
func TestSecretParamsCantBeArgs(t *testing.T) {
	cmd := ResolvedCommand{
		Name: "login", ScriptPath: "/bin/true",
		Params: []Param{{Name: "user"}, {Name: "token", Type: ParamSecret}},
	}
	for _, args := range [][]string{{"{token}"}, {"--token={token}"}, {"--user={user}", "{user}:{token}"}} {
		cmd.Args = args
		err := validateCommand(cmd)
		if err == nil || !strings.Contains(err.Error(), "secret") || !strings.Contains(err.Error(), "$LC_TOKEN") {
			t.Errorf("args %v: err = %v, want a secret error pointing at $LC_TOKEN", args, err)
		}
	}
	cmd.Args = []string{"--user={user}"}
	if err := validateCommand(cmd); err != nil {
		t.Errorf("non-secret args should still load: %v", err)
	}
	cmd.Args = nil
	if err := validateCommand(cmd); err != nil {
		t.Errorf("a secret used only via env should load: %v", err)
	}
}

func TestSidecarWithSecretArgIsSkippedWithWarning(t *testing.T) {
	dir := t.TempDir()
	writeExecutable(t, filepath.Join(dir, "scripts", "login.sh"))
	writeFile(t, filepath.Join(dir, "scripts", "login.lazycmd.yaml"), `
params:
  - { name: token, type: secret }
args: ["--token", "{token}"]
`)
	groups, warnings := Discover(t.TempDir(), dir)
	if len(warnings) != 1 || !strings.Contains(warnings[0], "secret") {
		t.Fatalf("warnings = %v", warnings)
	}
	if findCommand(groups, "This project", "login") != nil {
		t.Error("command with a secret in args should be skipped")
	}
}

func TestParamNamesValidated(t *testing.T) {
	bad := map[string][]Param{
		"space":         {{Name: "my name", Type: ParamString}},
		"dash":          {{Name: "my-name", Type: ParamString}},
		"equals":        {{Name: "a=b", Type: ParamString}},
		"leading digit": {{Name: "1st", Type: ParamString}},
		"empty":         {{Name: "", Type: ParamString}},
		"duplicate":     {{Name: "env", Type: ParamString}, {Name: "env", Type: ParamSelect}},
		"case dup":      {{Name: "env", Type: ParamString}, {Name: "ENV", Type: ParamString}},
	}
	for name, params := range bad {
		if err := validateCommand(ResolvedCommand{Name: "x", Run: "true", Params: params}); err == nil {
			t.Errorf("%s: %+v accepted", name, params)
		}
	}
	good := []Param{{Name: "env", Type: ParamString}, {Name: "_tag2", Type: ParamString}, {Name: "also_make", Type: ParamBool}}
	if err := validateCommand(ResolvedCommand{Name: "x", Run: "true", Params: good}); err != nil {
		t.Errorf("valid names rejected: %v", err)
	}

	err := validateCommand(ResolvedCommand{Name: "x", Run: "true", Params: bad["space"]})
	if err == nil || !strings.Contains(err.Error(), "$LC_MY NAME") {
		t.Errorf("error should explain the env var: %v", err)
	}
}

func TestSidecarWithBadParamNameIsSkipped(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "s.sh"), "#!/bin/sh\n")
	if err := os.Chmod(filepath.Join(dir, "s.sh"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, "s.lazycmd.yaml"), "params:\n  - { name: my name, type: string }\n")
	cmds, w := scanScripts(dir, "global", nil)
	if len(cmds) != 0 || len(w) != 1 || !strings.Contains(w[0], "letters, digits and _") {
		t.Errorf("cmds = %+v, warnings = %v", cmds, w)
	}
}
