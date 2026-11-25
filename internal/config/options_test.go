package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestOptionListDecode(t *testing.T) {
	var p Param
	err := yaml.Unmarshal([]byte(`
name: repo
type: select
options:
  - dev
  - 8080
  - label: vandag
    value: /repos/vandag
  - value: /repos/bare
`), &p)
	if err != nil {
		t.Fatal(err)
	}
	want := OptionList{{Value: "dev"}, {Value: "8080"}, {Label: "vandag", Value: "/repos/vandag"}, {Value: "/repos/bare"}}
	if len(p.Options) != len(want) {
		t.Fatalf("options = %+v", p.Options)
	}
	for i := range want {
		if p.Options[i] != want[i] {
			t.Errorf("option %d = %+v, want %+v", i, p.Options[i], want[i])
		}
	}
	if p.Options[3].Display() != "/repos/bare" || p.Options[2].Display() != "vandag" {
		t.Error("Display should fall back to the value")
	}

	bad := map[string]string{
		"unknown key":  "options:\n  - { value: a, lable: b }\n",
		"no value":     "options:\n  - { label: b }\n",
		"nested value": "options:\n  - { value: [a] }\n",
		"not a list":   "options: a\n",
		"list in list": "options:\n  - [a, b]\n",
	}
	for name, src := range bad {
		var p Param
		if err := yaml.Unmarshal([]byte(src), &p); err == nil {
			t.Errorf("%s: decoded without error: %+v", name, p.Options)
		}
	}
}

func TestSidecarWithLabelsLoads(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "pick.sh"), "#!/bin/sh\n")
	chmodX(t, filepath.Join(dir, "pick.sh"))
	writeFile(t, filepath.Join(dir, "pick.lazycmd.yaml"), `params:
  - name: repo
    type: select
    label: basename
    options:
      - /r/a
      - label: Bee
        value: /r/b
    default: /r/b
`)
	cmds, w := scanScripts(dir, "global", nil)
	if len(w) != 0 || len(cmds) != 1 {
		t.Fatalf("cmds = %+v, warnings = %v", cmds, w)
	}
	p := cmds[0].Params[0]
	if p.Label != LabelBasename || len(p.Options) != 2 || p.Options[1].Label != "Bee" {
		t.Errorf("param = %+v", p)
	}

	// A bad label mode, or one on a param without options, is a warning.
	for _, body := range []string{
		"params:\n  - { name: repo, type: select, label: base, options: [a] }\n",
		"params:\n  - { name: msg, type: string, label: basename }\n",
	} {
		writeFile(t, filepath.Join(dir, "pick.lazycmd.yaml"), body)
		if cmds, w := scanScripts(dir, "global", nil); len(cmds) != 0 || len(w) != 1 {
			t.Errorf("%q: cmds = %+v, warnings = %v, want rejected", body, cmds, w)
		}
	}
}

func TestParseOptionLines(t *testing.T) {
	got := ParseOptionLines("/a/one\tOne\n\n  /a/two  \n/a/three\t\r\n\tlabel only\n")
	want := OptionList{{Value: "/a/one", Label: "One"}, {Value: "/a/two"}, {Value: "/a/three"}}
	if len(got) != len(want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("line %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestWithLabels(t *testing.T) {
	labels := func(l OptionList) string {
		var s []string
		for _, o := range l {
			s = append(s, o.Display())
		}
		return strings.Join(s, " | ")
	}

	opts := OptionList{
		{Value: "/repo/api/pom.xml"},
		{Value: "/repo/web/pom.xml"},
		{Value: "/repo/README.md"},
		{Value: "/repo/x/web/pom.xml"},
		{Value: "/repo/mine", Label: "Mine"},
		{Value: "/repo/dir/"},
	}
	if got := labels(WithLabels(opts, LabelBasename, "/repo")); got != "api/pom.xml | /repo/web/pom.xml | README.md | /repo/x/web/pom.xml | Mine | dir" {
		t.Errorf("basename = %s", got)
	}
	if got := labels(WithLabels(opts, LabelRelative, "/repo")); got != "api/pom.xml | web/pom.xml | README.md | x/web/pom.xml | Mine | dir" {
		t.Errorf("relative = %s", got)
	}
	rel := OptionList{{Value: "./a/b.txt"}, {Value: "/elsewhere/c"}, {Value: "../up"}}
	if got := labels(WithLabels(rel, LabelRelative, "/repo")); got != "a/b.txt | /elsewhere/c | ../up" {
		t.Errorf("relative paths = %s", got)
	}
	if got := labels(WithLabels(opts, "", "/repo")); !strings.HasPrefix(got, "/repo/api/pom.xml") {
		t.Errorf("no mode should leave values: %s", got)
	}
	if opts[0].Label != "" {
		t.Error("WithLabels changed its input")
	}
	for _, o := range WithLabels(opts, LabelBasename, "/repo") {
		if !strings.HasPrefix(o.Value, "/repo/") {
			t.Errorf("value changed: %+v", o)
		}
	}
}

func TestDefaultsMatchValuesNotLabels(t *testing.T) {
	groups := []Group{{Commands: []ResolvedCommand{{ID: "global:pick", Name: "pick", Params: []Param{
		{Name: "repo", Type: ParamSelect, Options: OptionList{{Label: "vandag", Value: "/r/vandag"}, {Label: "cast", Value: "/r/cast"}}},
		{Name: "many", Type: ParamMulti, Options: OptionList{{Label: "A", Value: "/a"}, {Label: "B", Value: "/b"}}},
	}}}}}
	w := ApplyDefaults(groups, map[string]map[string]any{"pick": {"repo": "/r/cast", "many": []any{"/a", "/b"}}})
	if len(w) != 0 || groups[0].Commands[0].Params[0].Default != "/r/cast" || groups[0].Commands[0].Params[1].Default != "/a,/b" {
		t.Fatalf("by value: warnings %v, params %+v", w, groups[0].Commands[0].Params)
	}
	w = ApplyDefaults(groups, map[string]map[string]any{"pick": {"repo": "vandag", "many": []any{"A"}}})
	if len(w) != 2 || !strings.Contains(w[0]+w[1], "not one of /r/vandag, /r/cast") {
		t.Errorf("a label is not a value: warnings %v", w)
	}
}

func TestProfileParamsOptionsWithLabels(t *testing.T) {
	global, repo := t.TempDir(), t.TempDir()
	writeFile(t, filepath.Join(ProfilesDir(global), "p.yaml"), `path: `+repo+`
params:
  pick:
    repo:
      options:
        - label: Vandag
          value: /r/vandag
        - /r/other
      default: /r/vandag
`)
	prof, w := LoadProfile(global, repo)
	if prof == nil || len(w) != 0 {
		t.Fatalf("profile %v, warnings %v", prof, w)
	}
	groups := []Group{{Commands: []ResolvedCommand{{ID: "global:pick", Name: "pick", Params: []Param{
		{Name: "repo", Type: ParamSelect, OptionsCmd: "ls"},
	}}}}}
	if w := ApplyParamOverrides(groups, prof.File.Params); len(w) != 0 {
		t.Fatal(w)
	}
	p := groups[0].Commands[0].Params[0]
	if p.Options[0] != (Option{Label: "Vandag", Value: "/r/vandag"}) || p.Default != "/r/vandag" || p.OptionsCmd != "" {
		t.Errorf("param = %+v", p)
	}
}

func chmodX(t *testing.T, path string) {
	t.Helper()
	if err := os.Chmod(path, 0o755); err != nil {
		t.Fatal(err)
	}
}
