package config

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestConfirmDecode(t *testing.T) {
	cases := map[string]ConfirmSpec{
		"confirm: true":                {Set: true},
		"confirm: false":               {},
		`confirm: "Deploy {env}?"`:     {Set: true, Text: "Deploy {env}?"},
		"confirm: Really wipe the db?": {Set: true, Text: "Really wipe the db?"},
		"run: x":                       {},
	}
	for src, want := range cases {
		var c Command
		if err := yaml.Unmarshal([]byte(src), &c); err != nil {
			t.Errorf("%q: %v", src, err)
			continue
		}
		if c.Confirm != want {
			t.Errorf("%q: %+v, want %+v", src, c.Confirm, want)
		}
	}
	for _, src := range []string{`confirm: ""`, "confirm: [a]", "confirm: {x: 1}", "confirm: 3"} {
		var c Command
		if err := yaml.Unmarshal([]byte(src), &c); err == nil {
			t.Errorf("%q decoded: %+v", src, c.Confirm)
		}
	}
}

func TestConfirmTextAndValidation(t *testing.T) {
	cmd := ResolvedCommand{Name: "wipe {db}"}
	applySpec(&cmd, Command{Confirm: ConfirmSpec{Set: true}})
	if cmd.Confirm != "Run wipe {{db}}?" || ExpandConfirm(cmd, nil) != "Run wipe {db}?" {
		t.Errorf("default question = %q / %q", cmd.Confirm, ExpandConfirm(cmd, nil))
	}

	params := []Param{{Name: "env", Type: ParamSelect}, {Name: "token", Type: ParamSecret}, {Name: "tag", Type: ParamString}}
	ok := ResolvedCommand{Name: "d", Run: "true", Params: params, Confirm: "Deploy {tag} to {env} with {token}?"}
	if err := validateCommand(ok); err != nil {
		t.Fatal(err)
	}
	got := ExpandConfirm(ok, map[string]string{"env": "prod", "token": "s3cret", "tag": ""})
	if got != "Deploy (none) to prod with ••••••?" {
		t.Errorf("expanded = %q", got)
	}
	for _, bad := range []string{"Deploy {nope}?", "Deploy {env?", "a } b"} {
		c := ok
		c.Confirm = bad
		if err := validateCommand(c); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestApplyConfirm(t *testing.T) {
	groups := []Group{{Commands: []ResolvedCommand{
		{ID: "npm:deploy", Name: "npm: deploy"},
		{ID: "global:wipe", Name: "wipe", Confirm: "Own question?"},
		{ID: "global:d", Name: "d", Params: []Param{{Name: "env"}}},
	}}}
	w := ApplyConfirm(groups, map[string]any{
		"npm: deploy": true,
		"wipe":        false,           // can't remove
		"global:d":    "Deploy {env}?", // by id, with a placeholder
		"ghost":       true,
		"global:x":    3,
	})
	c := groups[0].Commands
	if c[0].Confirm != "Run npm: deploy?" || c[1].Confirm != "Own question?" || c[2].Confirm != "Deploy {env}?" {
		t.Errorf("confirms = %q %q %q", c[0].Confirm, c[1].Confirm, c[2].Confirm)
	}
	want := []string{"can add a confirm but not remove", `no command named "ghost"`, `no command named "global:x"`}
	if len(w) != len(want) {
		t.Fatalf("warnings = %v", w)
	}
	for _, s := range want {
		if !strings.Contains(strings.Join(w, "\n"), s) {
			t.Errorf("missing warning %q in %v", s, w)
		}
	}
	w = ApplyConfirm(groups, map[string]any{"global:d": "Deploy {nope}?"})
	if len(w) != 1 || !strings.Contains(w[0], "{nope}") || c[2].Confirm != "Deploy {env}?" {
		t.Errorf("bad placeholder: warnings %v, confirm %q", w, c[2].Confirm)
	}
}
