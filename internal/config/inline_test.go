package config

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestInlineCommand(t *testing.T) {
	repo := t.TempDir()
	global := t.TempDir()
	exe(t, filepath.Join(repo, "scripts", "plain.sh"), "#!/bin/sh\n")
	exe(t, filepath.Join(repo, "scripts", "deploy.sh"), "#!/bin/sh\n")
	writeFile(t, filepath.Join(repo, "scripts", "deploy.lazycmd.yaml"), "desc: beside\nparams:\n  - { name: env, type: select, options: [a, b] }\n")
	exe(t, filepath.Join(repo, "scripts", "release.sh"), "#!/bin/sh\n")
	writeFile(t, filepath.Join(repo, "scripts", "noexec.sh"), "#!/bin/sh\n")

	cmd, has, err := InlineCommand(global, repo, filepath.Join(repo, "scripts", "plain.sh"), nil)
	if err != nil || has || cmd.ScriptPath != filepath.Join(repo, "scripts", "plain.sh") {
		t.Errorf("plain: %+v %v %v", cmd, has, err)
	}
	cmd, has, err = InlineCommand(global, repo, filepath.Join(repo, "scripts", "deploy.sh"), nil)
	if err != nil || !has || cmd.Desc != "beside" || len(cmd.Params) != 1 {
		t.Errorf("beside: %+v %v %v", cmd, has, err)
	}
	if _, _, err := InlineCommand(global, repo, filepath.Join(repo, "scripts", "noexec.sh"), nil); err == nil || !strings.Contains(err.Error(), "not executable") {
		t.Errorf("noexec: %v", err)
	}
	if _, _, err := InlineCommand(global, repo, filepath.Join(repo, "nope.sh"), nil); err == nil {
		t.Error("missing script should fail")
	}

	// The profile's sidecar wins over the one beside the script, and its
	// defaults and confirm apply.
	p := profileWithSidecars(t, t.TempDir(), strings.Join([]string{
		"sidecars:",
		"  scripts/deploy.sh:",
		"    desc: from profile",
		"    params:",
		"      - { name: env, type: select, options: [x, y] }",
		"  scripts/release.sh: { desc: rel }",
		"defaults:",
		"  deploy: { env: y }",
		"  other: { a: b }",
		"confirm:",
		"  release: true",
	}, "\n")+"\n")
	cmd, has, err = InlineCommand(global, repo, filepath.Join(repo, "scripts", "deploy.sh"), p)
	if err != nil || !has || cmd.Desc != "from profile" || cmd.Params[0].Default != "y" {
		t.Errorf("profile: %+v %v %v", cmd, has, err)
	}
	cmd, has, err = InlineCommand(global, repo, filepath.Join(repo, "scripts", "release.sh"), p)
	if err != nil || !has || cmd.Confirm == "" {
		t.Errorf("profile confirm: %+v %v %v", cmd, has, err)
	}
}

func TestSetValues(t *testing.T) {
	cmd := ResolvedCommand{Name: "d", Params: []Param{
		{Name: "env", Type: ParamSelect, Options: Opts("a", "b")},
		{Name: "tag", Type: ParamString},
	}}
	orig := cmd.Params
	if err := SetValues(&cmd, []string{"env=b", "tag=v=1"}); err != nil {
		t.Fatal(err)
	}
	if cmd.Params[0].Default != "b" || cmd.Params[1].Default != "v=1" {
		t.Errorf("params = %+v", cmd.Params)
	}
	if orig[0].Default != "" {
		t.Error("SetValues changed the shared param slice")
	}
	for _, bad := range []string{"env=c", "nope=1", "justaword", "=x"} {
		c := ResolvedCommand{Name: "d", Params: append([]Param(nil), orig...)}
		if err := SetValues(&c, []string{bad}); err == nil {
			t.Errorf("%q should fail", bad)
		}
	}
}

// The inline examples must keep loading: greet's sidecar, plain without
// one, and release's form from the inline-demo profile.
func TestInlineExamples(t *testing.T) {
	root, _ := filepath.Abs(filepath.Join("..", ".."))
	global := t.TempDir()
	in := func(name string) string { return filepath.Join(root, "examples", "inline", name) }

	if cmd, has, err := InlineCommand(global, root, in("greet.sh"), nil); err != nil || !has || len(cmd.Params) != 3 || cmd.Confirm == "" {
		t.Errorf("greet: %+v %v %v", cmd, has, err)
	}
	if _, has, err := InlineCommand(global, root, in("plain.sh"), nil); err != nil || has {
		t.Errorf("plain: %v %v", has, err)
	}

	var pf ProfileFile
	if err := decodeStrict(filepath.Join(root, "examples", "projects", "inline-demo.yaml"), &pf); err != nil {
		t.Fatal(err)
	}
	p := &Profile{File: pf, Source: filepath.Join(t.TempDir(), "inline-demo.yaml")}
	if cmd, has, err := InlineCommand(global, root, in("release.sh"), p); err != nil || !has || len(cmd.Params) != 2 {
		t.Errorf("release: %+v %v %v", cmd, has, err)
	}
}

func TestSetValuesRefusesSecrets(t *testing.T) {
	cmd := ResolvedCommand{Name: "d", Params: []Param{{Name: "token", Type: ParamSecret}}}
	err := SetValues(&cmd, []string{"token=abc"})
	if err == nil || !strings.Contains(err.Error(), "LC_TOKEN") {
		t.Errorf("want a refusal that points at LC_TOKEN, got %v", err)
	}
}

// A sidecar beside a script describes it: its own run: would show in the
// preview while the script is what runs.
func TestSidecarBesideScriptCantSetRun(t *testing.T) {
	dir := t.TempDir()
	exe(t, filepath.Join(dir, "deploy.sh"), "#!/bin/sh\n")
	for _, body := range []string{"run: echo harmless\n", "script: other.sh\n"} {
		writeFile(t, filepath.Join(dir, "deploy.lazycmd.yaml"), body)
		if _, err := scriptCommand(dir, "deploy.sh", "project"); err == nil || !strings.Contains(err.Error(), "can't set run: or script:") {
			t.Errorf("%q: got %v", body, err)
		}
		if _, _, err := InlineCommand(t.TempDir(), dir, filepath.Join(dir, "deploy.sh"), nil); err == nil {
			t.Errorf("%q: lc should refuse it too", body)
		}
	}
}
