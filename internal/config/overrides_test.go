package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func overrideGroups() ([]Param, []Group) {
	shared := []Param{
		{Name: "features", Type: ParamMulti, Options: Opts("logging", "metrics"), Default: "metrics"},
		{Name: "env", Type: ParamSelect, OptionsCmd: "ls envs", DependsOn: []string{"features"}},
		{Name: "message", Type: ParamString},
	}
	return shared, []Group{{Title: "x", Commands: []ResolvedCommand{
		{ID: "global:echo-params", Name: "echo-params", Params: shared},
		{ID: "global:other", Name: "other", Params: shared}, // same backing slice
	}}}
}

func TestParamOverridesReplaceOptionsAndDefaults(t *testing.T) {
	shared, groups := overrideGroups()
	w := ApplyParamOverrides(groups, map[string]map[string]ParamOverride{
		"echo-params": {
			"features": {Options: Opts("logging", "tracing", "audit"), Default: []any{"logging", "audit"}},
			"env":      {Options: Opts("dev", "prod"), Default: "prod"},
		},
	})
	if len(w) != 0 {
		t.Fatalf("warnings = %v", w)
	}

	got := groups[0].Commands[0].Params
	if strings.Join(got[0].Options.Values(), ",") != "logging,tracing,audit" || got[0].Default != "logging,audit" {
		t.Errorf("features = %+v", got[0])
	}
	if got[1].OptionsCmd != "" || got[1].DependsOn != nil || strings.Join(got[1].Options.Values(), ",") != "dev,prod" || got[1].Default != "prod" {
		t.Errorf("env = %+v, want options_cmd replaced by fixed options", got[1])
	}
	if shared[0].Default != "metrics" || shared[1].OptionsCmd != "ls envs" || len(groups[0].Commands[1].Params[0].Options) != 2 {
		t.Error("override leaked into the shared param slice / another command")
	}
}

func TestParamOverridesOptionsOnlyDropsStaleDefault(t *testing.T) {
	_, groups := overrideGroups()
	w := ApplyParamOverrides(groups, map[string]map[string]ParamOverride{
		"global:echo-params": {"features": {Options: Opts("logging", "audit")}}, // by id
	})
	if len(w) != 0 {
		t.Fatalf("warnings = %v", w)
	}
	if p := groups[0].Commands[0].Params[0]; p.Default != "" {
		t.Errorf("default = %q, want the sidecar's \"metrics\" dropped: it isn't an option any more", p.Default)
	}

	// Default only: checked against the existing options.
	_, groups = overrideGroups()
	w = ApplyParamOverrides(groups, map[string]map[string]ParamOverride{
		"echo-params": {"features": {Default: []any{"logging"}}},
	})
	if len(w) != 0 || groups[0].Commands[0].Params[0].Default != "logging" {
		t.Errorf("default-only override: warnings %v, param %+v", w, groups[0].Commands[0].Params[0])
	}
}

func TestParamOverridesWarnings(t *testing.T) {
	_, groups := overrideGroups()
	w := ApplyParamOverrides(groups, map[string]map[string]ParamOverride{
		"echo-params": {
			"features": {Options: Opts("a", "b"), Default: []any{"a", "zzz"}}, // default not an option
			"message":  {Options: Opts("x")},                                  // options on a string
			"ghost":    {Default: "x"},                                        // unknown param
			"env":      {},                                                    // nothing set
		},
		"nope": {"x": {Default: "y"}}, // unknown command
	})
	want := []string{`"zzz" is not one of a, b`, "only apply to select and multi", `no param "ghost"`, "set options, default, or both", `no command named "nope"`}
	if len(w) != len(want) {
		t.Fatalf("warnings = %v, want %d", w, len(want))
	}
	for _, s := range want {
		found := false
		for _, msg := range w {
			if strings.Contains(msg, s) {
				found = true
			}
		}
		if !found {
			t.Errorf("no warning containing %q in %v", s, w)
		}
	}
	got := groups[0].Commands[0].Params
	if strings.Join(got[0].Options.Values(), ",") != "a,b" || got[0].Default != "" {
		t.Errorf("features = %+v: options apply even when the default is rejected", got[0])
	}
	if len(got[2].Options) != 0 {
		t.Error("options were set on a string param")
	}
}

func TestDefaultsCheckedAgainstOverriddenOptions(t *testing.T) {
	_, groups := overrideGroups()
	w := ApplyParamOverrides(groups, map[string]map[string]ParamOverride{
		"echo-params": {"features": {Options: Opts("a", "b")}},
	})
	w = append(w, ApplyDefaults(groups, map[string]map[string]any{
		"echo-params": {"features": []any{"b"}},
		"other":       {"features": []any{"logging", "nope"}}, // multi values are checked too now
	})...)
	if groups[0].Commands[0].Params[0].Default != "b" {
		t.Errorf("defaults: didn't win over params: %+v", groups[0].Commands[0].Params[0])
	}
	if len(w) != 1 || !strings.Contains(w[0], `"nope" is not one of`) {
		t.Errorf("warnings = %v", w)
	}
}

func TestProfileParamsDecode(t *testing.T) {
	global := t.TempDir()
	repo := t.TempDir()
	writeFile(t, filepath.Join(ProfilesDir(global), "p.yaml"), `path: `+repo+`
params:
  echo-params:
    features: { options: [logging, audit], default: [audit] }
    env: { default: prod }
`)
	prof, w := LoadProfile(global, repo)
	if prof == nil || len(w) != 0 {
		t.Fatalf("profile = %v, warnings = %v", prof, w)
	}
	o := prof.File.Params["echo-params"]
	if strings.Join(o["features"].Options.Values(), ",") != "logging,audit" || o["env"].Default != "prod" {
		t.Errorf("params = %+v", prof.File.Params)
	}

	writeFile(t, filepath.Join(ProfilesDir(global), "p.yaml"), "path: "+repo+"\nparams:\n  x:\n    y: { optons: [a] }\n")
	if prof, w := LoadProfile(global, repo); prof != nil || len(w) != 1 {
		t.Errorf("typo key: profile = %v, warnings = %v, want rejected", prof, w)
	}
}

func TestTerminalFromSpecAndProfile(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "pick.sh"), "#!/bin/sh\n")
	if err := os.Chmod(filepath.Join(dir, "pick.sh"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, "pick.lazycmd.yaml"), "terminal: true\n")
	cmds, w := scanScripts(dir, "global", nil)
	if len(w) != 0 || len(cmds) != 1 || !cmds[0].Terminal {
		t.Fatalf("sidecar terminal: cmds = %+v, warnings = %v", cmds, w)
	}

	groups := []Group{{Commands: []ResolvedCommand{
		{ID: "global:a", Name: "a"}, {ID: "maven:test", Name: "maven: test"}, {ID: "global:b", Name: "b"},
	}}}
	w = ApplyTerminal(groups, []string{"a", "maven:test", "ghost"})
	got := groups[0].Commands
	if !got[0].Terminal || !got[1].Terminal || got[2].Terminal {
		t.Errorf("terminal = %v %v %v", got[0].Terminal, got[1].Terminal, got[2].Terminal)
	}
	if len(w) != 1 || !strings.Contains(w[0], `"ghost"`) {
		t.Errorf("warnings = %v", w)
	}

	global, repo := t.TempDir(), t.TempDir()
	writeFile(t, filepath.Join(ProfilesDir(global), "p.yaml"), "path: "+repo+"\nterminal:\n  - pick\n")
	prof, w := LoadProfile(global, repo)
	if prof == nil || len(w) != 0 || len(prof.File.Terminal) != 1 || prof.File.Terminal[0] != "pick" {
		t.Errorf("profile terminal = %+v, warnings %v", prof, w)
	}
}
