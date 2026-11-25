package config

import (
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
