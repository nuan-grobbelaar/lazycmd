package trust

import (
	"os"
	"path/filepath"
	"testing"

	"lazycmd/internal/config"
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

func fp(t *testing.T, proj string) string {
	t.Helper()
	cmds, _ := config.ProjectCommands(proj)
	return Fingerprint(proj, cmds)
}

func TestFingerprintChangesWithAnythingThatRuns(t *testing.T) {
	repo := t.TempDir()
	proj := filepath.Join(repo, ".lazycmd")
	write(t, filepath.Join(proj, "commands.yaml"), "commands:\n  a: { run: echo a }\n  b: { script: ../tools/b.sh }\n", 0o644)
	write(t, filepath.Join(proj, "scripts", "s.sh"), "#!/bin/sh\necho s\n", 0o755)
	write(t, filepath.Join(repo, "tools", "b.sh"), "#!/bin/sh\necho b\n", 0o755)

	base := fp(t, proj)
	if base != fp(t, proj) {
		t.Fatal("fingerprint is not stable")
	}

	changes := []struct {
		name string
		do   func()
		undo func()
	}{
		{"commands.yaml", func() {
			write(t, filepath.Join(proj, "commands.yaml"), "commands:\n  a: { run: echo EVIL }\n  b: { script: ../tools/b.sh }\n", 0o644)
		},
			func() {
				write(t, filepath.Join(proj, "commands.yaml"), "commands:\n  a: { run: echo a }\n  b: { script: ../tools/b.sh }\n", 0o644)
			}},
		{"script contents", func() { write(t, filepath.Join(proj, "scripts", "s.sh"), "#!/bin/sh\necho EVIL\n", 0o755) },
			func() { write(t, filepath.Join(proj, "scripts", "s.sh"), "#!/bin/sh\necho s\n", 0o755) }},
		{"script exec bit", func() { os.Chmod(filepath.Join(proj, "scripts", "s.sh"), 0o644) },
			func() { os.Chmod(filepath.Join(proj, "scripts", "s.sh"), 0o755) }},
		{"new sidecar", func() { write(t, filepath.Join(proj, "scripts", "s.lazycmd.yaml"), "desc: x\n", 0o644) },
			func() { os.Remove(filepath.Join(proj, "scripts", "s.lazycmd.yaml")) }},
		{"script outside .lazycmd", func() { write(t, filepath.Join(repo, "tools", "b.sh"), "#!/bin/sh\necho EVIL\n", 0o755) },
			func() { write(t, filepath.Join(repo, "tools", "b.sh"), "#!/bin/sh\necho b\n", 0o755) }},
	}
	for _, c := range changes {
		c.do()
		if fp(t, proj) == base {
			t.Errorf("changing %s did not change the fingerprint", c.name)
		}
		c.undo()
		if got := fp(t, proj); got != base {
			t.Fatalf("undoing %s did not restore the fingerprint", c.name)
		}
	}
}

func TestStoreCheckAllowSave(t *testing.T) {
	stateDir := t.TempDir()
	repo := t.TempDir()

	s := Load(stateDir)
	if got := s.Check(repo, "one"); got != Unknown {
		t.Fatalf("new repo = %v, want Unknown", got)
	}
	s.Allow(repo, "one")
	if err := s.Save(stateDir); err != nil {
		t.Fatal(err)
	}

	s = Load(stateDir)
	if got := s.Check(repo, "one"); got != Allowed {
		t.Errorf("after allow = %v, want Allowed", got)
	}
	if got := s.Check(repo, "two"); got != Changed {
		t.Errorf("different fingerprint = %v, want Changed", got)
	}
	if got := s.Check(t.TempDir(), "one"); got != Unknown {
		t.Errorf("other repo = %v, want Unknown", got)
	}

	info, err := os.Stat(filepath.Join(stateDir, fileName))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("trust file mode = %v, want 0600", info.Mode().Perm())
	}
}

func TestRepoKeyResolvesSymlinks(t *testing.T) {
	repo := t.TempDir()
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(repo, link); err != nil {
		t.Fatal(err)
	}
	if RepoKey(repo) != RepoKey(link) {
		t.Errorf("RepoKey(%s) != RepoKey(%s)", repo, link)
	}
}

func TestCorruptStoreAsksAgain(t *testing.T) {
	stateDir := t.TempDir()
	write(t, filepath.Join(stateDir, fileName), "{not json", 0o600)
	if got := Load(stateDir).Check(t.TempDir(), "x"); got != Unknown {
		t.Errorf("corrupt store = %v, want Unknown", got)
	}
}
