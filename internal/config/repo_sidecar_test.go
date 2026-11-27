package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// profileWithSidecars writes a profile and returns it loaded.
func profileWithSidecars(t *testing.T, dir, body string) *Profile {
	t.Helper()
	path := filepath.Join(dir, "p.yaml")
	writeFile(t, path, body)
	var pf ProfileFile
	if err := decodeStrict(path, &pf); err != nil {
		t.Fatal(err)
	}
	return &Profile{File: pf, Source: path}
}

func TestProfileSidecarsForRepoScripts(t *testing.T) {
	repo := t.TempDir()
	pdir := t.TempDir()
	exe(t, filepath.Join(repo, "scripts", "deploy.sh"), "#!/bin/sh\n")
	exe(t, filepath.Join(repo, "bin", "release"), "#!/bin/sh\n")
	writeFile(t, filepath.Join(repo, "notes.sh"), "#!/bin/sh\n") // not executable
	writeFile(t, filepath.Join(pdir, "vandag", "release.lazycmd.yaml"),
		"desc: Cut a release\nparams:\n  - { name: version, type: string, required: true }\nargs: [\"{version}\"]\n")

	p := profileWithSidecars(t, pdir, strings.Join([]string{
		"sidecars:",
		"  scripts/deploy.sh:",
		"    desc: Deploy a service",
		"    params:",
		"      - { name: env, type: select, options: [staging, prod] }",
		"  bin/release: ./vandag/release.lazycmd.yaml",
		"  notes.sh: { desc: x }",
		"  ../outside.sh: { desc: x }",
		"  missing.sh: { desc: x }",
		"  scripts/deploy2.sh:",
		"    run: echo hi",
	}, "\n")+"\n")

	cmds, warnings := p.Includes(repo)
	byName := map[string]ResolvedCommand{}
	for _, c := range cmds {
		byName[c.Name] = c
	}
	d := byName["deploy"]
	if d.ScriptPath != filepath.Join(repo, "scripts", "deploy.sh") || d.Desc != "Deploy a service" || len(d.Params) != 1 || d.ID != "project:deploy" {
		t.Errorf("deploy = %+v", d)
	}
	r := byName["release"]
	if r.ScriptPath != filepath.Join(repo, "bin", "release") || r.Desc != "Cut a release" || len(r.Args) != 1 {
		t.Errorf("release = %+v", r)
	}
	if len(cmds) != 2 {
		t.Errorf("got %d commands, want 2: %+v", len(cmds), cmds)
	}
	all := strings.Join(warnings, "\n")
	for _, want := range []string{
		"notes.sh): not executable",
		"../outside.sh): must be a path inside the repo",
		"missing.sh): no such script",
		"deploy2.sh): no such script", // checked before the spec
	} {
		if !strings.Contains(all, want) {
			t.Errorf("warnings missing %q:\n%s", want, all)
		}
	}
}

func TestProfileSidecarRejectsRunAndUnknownKeys(t *testing.T) {
	repo := t.TempDir()
	pdir := t.TempDir()
	exe(t, filepath.Join(repo, "go.sh"), "#!/bin/sh\n")

	p := profileWithSidecars(t, pdir, "sidecars:\n  go.sh: { run: echo hi }\n")
	if _, w := p.Includes(repo); len(w) != 1 || !strings.Contains(w[0], "drop run:/script:") {
		t.Errorf("warnings = %v", w)
	}

	path := filepath.Join(pdir, "bad.yaml")
	writeFile(t, path, "sidecars:\n  go.sh: { prams: [] }\n")
	var pf ProfileFile
	if err := decodeStrict(path, &pf); err == nil || !strings.Contains(err.Error(), "prams") {
		t.Errorf("unknown key inside an inline sidecar should fail, got %v", err)
	}
}

func TestProfileSidecarReplacesRepoEntryForSameScript(t *testing.T) {
	repo := t.TempDir()
	pdir := t.TempDir()
	projectDir := filepath.Join(repo, ".lazycmd")
	exe(t, filepath.Join(projectDir, "scripts", "deploy.sh"), "#!/bin/sh\n")

	p := profileWithSidecars(t, pdir, "sidecars:\n  .lazycmd/scripts/deploy.sh:\n    desc: from the profile\n")
	extra, w := p.Includes(repo)
	if len(w) != 0 {
		t.Fatal(w)
	}
	groups, warnings := DiscoverWith(t.TempDir(), projectDir, extra)
	var found []ResolvedCommand
	for _, g := range groups {
		for _, c := range g.Commands {
			if c.Name == "deploy" {
				found = append(found, c)
			}
		}
	}
	if len(found) != 1 || found[0].Desc != "from the profile" {
		t.Errorf("deploy entries = %+v (warnings %v)", found, warnings)
	}
}

func TestRepoSidecarLookup(t *testing.T) {
	repo := t.TempDir()
	pdir := t.TempDir()
	exe(t, filepath.Join(repo, "scripts", "deploy.sh"), "#!/bin/sh\n")
	p := profileWithSidecars(t, pdir, "sidecars:\n  scripts/deploy.sh: { desc: d }\n")

	cmd, ok, err := p.RepoSidecar(repo, filepath.Join(repo, "scripts", "deploy.sh"))
	if !ok || err != nil || cmd.Desc != "d" {
		t.Errorf("RepoSidecar = %+v, %v, %v", cmd, ok, err)
	}
	if _, ok, _ := p.RepoSidecar(repo, filepath.Join(repo, "other.sh")); ok {
		t.Error("other.sh has no sidecar")
	}
}

func TestSidecarScriptAbsoluteAndHome(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(t.TempDir(), "real.sh")
	exe(t, target, "#!/bin/sh\n")
	writeFile(t, filepath.Join(dir, "x.lazycmd.yaml"), "script: "+target+"\n")
	cmds, w := loadSidecarInclude(dir, "x")
	if len(w) != 0 || len(cmds) != 1 || cmds[0].ScriptPath != target {
		t.Errorf("absolute script: = %+v %v", cmds, w)
	}

	home, _ := os.UserHomeDir()
	writeFile(t, filepath.Join(dir, "y.lazycmd.yaml"), "script: ~/lazycmd-nope.sh\n")
	cmds, _ = loadSidecarInclude(dir, "y")
	if len(cmds) != 1 || cmds[0].ScriptPath != filepath.Join(home, "lazycmd-nope.sh") {
		t.Errorf("~ script: = %+v", cmds)
	}
}

// The example profile in examples/projects must keep loading.
func TestExampleProfileSidecars(t *testing.T) {
	var pf ProfileFile
	if err := decodeStrict(filepath.Join("..", "..", "examples", "projects", "my-app.yaml"), &pf); err != nil {
		t.Fatal(err)
	}
	repo := t.TempDir()
	exe(t, filepath.Join(repo, "scripts", "release.sh"), "#!/bin/sh\n")
	cmds, w := (&Profile{File: pf, Source: filepath.Join(t.TempDir(), "my-app.yaml")}).Includes(repo)
	if len(w) != 0 || len(cmds) != 1 || cmds[0].Confirm == "" || len(cmds[0].Args) != 2 {
		t.Fatalf("cmds %+v, warnings %v", cmds, w)
	}
}
