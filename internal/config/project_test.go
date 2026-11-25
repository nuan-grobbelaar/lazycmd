package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func exe(t *testing.T, path, body string) {
	t.Helper()
	writeFile(t, path, body)
	if err := os.Chmod(path, 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestLoadProfileMatchesByPathSubfolderAndName(t *testing.T) {
	global := t.TempDir()
	repo := filepath.Join(t.TempDir(), "vandag")
	if err := os.MkdirAll(filepath.Join(repo, "web"), 0o755); err != nil {
		t.Fatal(err)
	}
	pdir := ProfilesDir(global)

	writeFile(t, filepath.Join(pdir, "a-other.yaml"), "path: /nowhere/else\n")
	writeFile(t, filepath.Join(pdir, "b-byname.yaml"), "include: []\n") // no path, name != repo
	writeFile(t, filepath.Join(pdir, "vandag.yaml"), "defaults: {}\n")  // no path, name == repo folder

	p, w := LoadProfile(global, repo)
	if len(w) != 0 || p == nil || filepath.Base(p.Source) != "vandag.yaml" {
		t.Fatalf("by name: profile = %+v, warnings = %v", p, w)
	}

	writeFile(t, filepath.Join(pdir, "a-path.yaml"), "path: "+repo+"\n")
	p, _ = LoadProfile(global, filepath.Join(repo, "web"))
	if p == nil || filepath.Base(p.Source) != "a-path.yaml" {
		t.Fatalf("by path covering a subfolder: got %+v", p)
	}

	if p, _ := LoadProfile(global, t.TempDir()); p != nil {
		t.Errorf("unrelated repo matched %s", p.Source)
	}
}

func TestLoadProfileReportsBadFileAndKeepsGoing(t *testing.T) {
	global := t.TempDir()
	repo := filepath.Join(t.TempDir(), "r")
	_ = os.MkdirAll(repo, 0o755)
	pdir := ProfilesDir(global)
	writeFile(t, filepath.Join(pdir, "a-broken.yaml"), "pathh: typo\n")
	writeFile(t, filepath.Join(pdir, "b-good.yaml"), "path: "+repo+"\n")

	p, w := LoadProfile(global, repo)
	if p == nil || filepath.Base(p.Source) != "b-good.yaml" {
		t.Fatalf("profile = %+v", p)
	}
	if len(w) != 1 || !strings.Contains(w[0], "a-broken.yaml") {
		t.Errorf("warnings = %v, want one about a-broken.yaml", w)
	}
}

func TestProfileIncludes(t *testing.T) {
	global := t.TempDir()
	shared := t.TempDir()
	pdir := ProfilesDir(global)

	exe(t, filepath.Join(shared, "tools", "migrate.sh"), "#!/bin/sh\n")
	writeFile(t, filepath.Join(shared, "tools", "migrate.lazycmd.yaml"), "desc: Run migrations\nparams:\n  - {name: env, type: select, options: [dev, prod]}\n")
	exe(t, filepath.Join(shared, "bare", "hello.sh"), "#!/bin/sh\n")
	writeFile(t, filepath.Join(shared, "cmds", "commands.yaml"), "commands:\n  say:\n    run: echo hi\n")
	exe(t, filepath.Join(pdir, "local.sh"), "#!/bin/sh\n")

	profile := filepath.Join(pdir, "p.yaml")
	writeFile(t, profile, strings.Join([]string{
		"include:",
		"  - " + filepath.Join(shared, "tools", "migrate.lazycmd.yaml"), // sidecar; script beside it
		"  - " + filepath.Join(shared, "cmds", "commands.yaml"),
		"  - " + filepath.Join(shared, "bare"), // a folder
		"  - ./local.sh",                       // relative to the profile
		"  - " + filepath.Join(shared, "missing.sh"),
	}, "\n")+"\n")

	var pf ProfileFile
	if err := decodeStrict(profile, &pf); err != nil {
		t.Fatal(err)
	}
	cmds, warnings := (&Profile{File: pf, Source: profile}).Includes()

	byName := map[string]ResolvedCommand{}
	for _, c := range cmds {
		byName[c.Name] = c
	}
	mig := byName["migrate"]
	if mig.ScriptPath != filepath.Join(shared, "tools", "migrate.sh") || mig.Desc != "Run migrations" || len(mig.Params) != 1 {
		t.Errorf("migrate = %+v", mig)
	}
	if byName["say"].Run != "echo hi" {
		t.Errorf("say = %+v", byName["say"])
	}
	if byName["hello"].ScriptPath == "" || byName["local"].ScriptPath == "" {
		t.Errorf("folder / relative includes missing: %v", byName)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "missing.sh") || !strings.Contains(warnings[0], "not found") {
		t.Errorf("warnings = %v, want exactly the missing.sh one", warnings)
	}
}

func TestDiscoverWithExtraAndShadowing(t *testing.T) {
	global, project := t.TempDir(), t.TempDir()
	exe(t, filepath.Join(global, "scripts", "deploy.sh"), "#!/bin/sh\n")
	exe(t, filepath.Join(project, "scripts", "own.sh"), "#!/bin/sh\n")

	extra := []ResolvedCommand{
		{ID: "project:deploy", Name: "deploy", Run: "x"}, // shadows the global one
		{ID: "project:own", Name: "own", Run: "y"},       // collides with the repo's own
	}
	groups, warnings := DiscoverWith(global, project, extra)

	var titles []string
	count := map[string]int{}
	for _, g := range groups {
		titles = append(titles, g.Title)
		for _, c := range g.Commands {
			count[g.Title+"/"+c.Name]++
		}
	}
	if count["This project/deploy"] != 1 || count["Global/deploy"] != 0 {
		t.Errorf("deploy placement = %v (groups %v)", count, titles)
	}
	if count["This project/own"] != 1 {
		t.Errorf("own should stay single: %v", count)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], `"own"`) {
		t.Errorf("warnings = %v", warnings)
	}
}

func TestApplyDefaults(t *testing.T) {
	shared := []Param{
		{Name: "module", Type: ParamSelect, Options: []string{"core", "api"}},
		{Name: "also_make", Type: ParamBool},
		{Name: "features", Type: ParamMulti, Options: []string{"a", "b"}},
	}
	groups := []Group{{Title: "x", Commands: []ResolvedCommand{
		{ID: "maven:test", Name: "maven: test", Params: shared},
		{ID: "maven:verify", Name: "maven: verify", Params: shared}, // same backing slice
	}}}

	w := ApplyDefaults(groups, map[string]map[string]any{
		"maven: test": {"module": "api", "also_make": true, "features": []any{"a", "b"}},
		"maven:nope":  {"x": "y"},
	})

	got := groups[0].Commands[0].Params
	if got[0].Default != "api" || got[1].Default != "true" || got[2].Default != "a,b" {
		t.Errorf("defaults = %+v", got)
	}
	if shared[0].Default != "" || groups[0].Commands[1].Params[0].Default != "" {
		t.Error("defaults leaked into the shared param slice / another command")
	}
	if len(w) != 1 || !strings.Contains(w[0], `"maven:nope"`) {
		t.Errorf("warnings = %v", w)
	}

	// By id, and bad values are warnings that skip just that default.
	w = ApplyDefaults(groups, map[string]map[string]any{
		"maven:verify": {"module": "ghost", "bogus": "1", "also_make": []any{"x"}},
	})
	if groups[0].Commands[1].Params[0].Default != "" {
		t.Error("a select value outside its options must be ignored")
	}
	if len(w) != 3 {
		t.Errorf("warnings = %v, want 3 (bad option, unknown param, list on a bool)", w)
	}
}

func TestEnabledFeaturesProfileLayer(t *testing.T) {
	global, project := t.TempDir(), t.TempDir()
	writeFile(t, filepath.Join(global, "config.yaml"), "maven:\n  default_profile: g\ndisable: [git]\n")
	writeFile(t, filepath.Join(project, "config.yaml"), "maven:\n  other: p\n")

	profile := FeaturesFile{Maven: map[string]string{"default_profile": "prof"}, Features: []string{"git"}, Disable: []string{"vite"}}
	enabled, settings, _ := EnabledFeatures(global, project, profile)

	if !enabled["git"] || enabled["vite"] {
		t.Errorf("enabled = %v, want git re-enabled by the profile and vite disabled", enabled)
	}
	if settings["maven"]["default_profile"] != "prof" || settings["maven"]["other"] != "p" {
		t.Errorf("maven settings = %v", settings["maven"])
	}
}

func TestApplyDefaultsReferenceOtherParams(t *testing.T) {
	mk := func() []Group {
		return []Group{{Title: "x", Commands: []ResolvedCommand{{ID: "g:d", Name: "deploy", Params: []Param{
			{Name: "env", Type: ParamString},
			{Name: "service", Type: ParamString},
			{Name: "url", Type: ParamString},
			{Name: "own", Type: ParamString, Default: "${env}"}, // the command's own default is never expanded
		}}}}}
	}
	groups := mk()
	w := ApplyDefaults(groups, map[string]map[string]any{
		"deploy": {"env": "staging", "service": "${env}-api", "url": "https://${service}.example.com"},
	})
	got := map[string]string{}
	for _, p := range groups[0].Commands[0].Params {
		got[p.Name] = p.Default
	}
	if got["service"] != "staging-api" || got["url"] != "https://staging-api.example.com" {
		t.Errorf("chained refs = %v", got)
	}
	if got["own"] != "${env}" {
		t.Errorf("own default was expanded: %q", got["own"])
	}
	if len(w) != 0 {
		t.Errorf("warnings = %v", w)
	}

	// Unknown reference and a cycle are warnings that leave the text alone.
	groups = mk()
	w = ApplyDefaults(groups, map[string]map[string]any{
		"deploy": {"env": "${service}", "service": "${env}", "url": "${nope}"},
	})
	got = map[string]string{}
	for _, p := range groups[0].Commands[0].Params {
		got[p.Name] = p.Default
	}
	if got["env"] != "${service}" || got["service"] != "${env}" || got["url"] != "${nope}" {
		t.Errorf("unresolvable defaults should stay as written: %v", got)
	}
	var cyc, unk bool
	for _, m := range w {
		cyc = cyc || strings.Contains(m, "circular")
		unk = unk || strings.Contains(m, `unknown param "nope"`)
	}
	if !cyc || !unk {
		t.Errorf("warnings = %v, want a circular one and an unknown-param one", w)
	}
}
