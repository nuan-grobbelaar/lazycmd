package watch

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestSignatureChangesWithFilesAndFolders(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "config.yaml")
	scripts := filepath.Join(dir, "scripts")
	paths := []string{file, scripts}
	base := Signature(paths)
	if Signature(paths) != base {
		t.Fatal("the same state must give the same signature")
	}

	write(t, file, "disable: [git]\n")
	created := Signature(paths)
	if created == base {
		t.Error("creating a watched file didn't change the signature")
	}
	write(t, file, "disable: [git, npm]\n") // different size
	edited := Signature(paths)
	if edited == created {
		t.Error("editing a file didn't change the signature")
	}

	write(t, filepath.Join(scripts, "a.sh"), "#!/bin/sh\n")
	added := Signature(paths)
	if added == edited {
		t.Error("adding a file to a watched folder didn't change the signature")
	}
	if err := os.Chmod(filepath.Join(scripts, "a.sh"), 0o755); err != nil {
		t.Fatal(err)
	}
	chmoded := Signature(paths)
	if chmoded == added {
		t.Error("making a script executable didn't change the signature")
	}
	if err := os.Remove(filepath.Join(scripts, "a.sh")); err != nil {
		t.Fatal(err)
	}
	if Signature(paths) == chmoded {
		t.Error("removing a file from a watched folder didn't change the signature")
	}
}

func TestSignatureSeesRenamedOverSave(t *testing.T) {
	// Editors that save by writing a new file and renaming it over the old.
	dir := t.TempDir()
	file := filepath.Join(dir, "commands.yaml")
	write(t, file, "a: 1\n")
	before := Signature([]string{file})
	tmp := file + ".swp"
	write(t, tmp, "a: 2222\n")
	if err := os.Rename(tmp, file); err != nil {
		t.Fatal(err)
	}
	if Signature([]string{file}) == before {
		t.Error("a rename over the file wasn't noticed")
	}
}

func TestSignatureFollowsSymlinks(t *testing.T) {
	real := t.TempDir()
	write(t, filepath.Join(real, "x.sh"), "1")
	link := filepath.Join(t.TempDir(), "scripts")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	before := Signature([]string{link})
	write(t, filepath.Join(real, "y.sh"), "2")
	if Signature([]string{link}) == before {
		t.Error("a change behind a symlinked folder wasn't noticed")
	}
}

func TestWatcherWaitsForChangesToSettle(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "f")
	write(t, file, "1")
	w := New(func() []string { return []string{file} })

	if w.Changed() {
		t.Fatal("nothing changed yet")
	}
	write(t, file, "22")
	if w.Changed() {
		t.Fatal("a change is only reported once it has settled")
	}
	write(t, file, "333") // still changing: not settled
	if w.Changed() {
		t.Fatal("a state seen once is not settled")
	}
	if !w.Changed() {
		t.Fatal("the same new state twice in a row should be reported")
	}
	if w.Changed() {
		t.Fatal("a change is reported once")
	}
}

func TestWatcherPathsCanGrow(t *testing.T) {
	dir := t.TempDir()
	a, b := filepath.Join(dir, "a"), filepath.Join(dir, "b")
	write(t, a, "1")
	write(t, b, "1")
	paths := []string{a}
	w := New(func() []string { return paths })

	paths = []string{a, b} // b starts being watched
	w.Changed()
	if !w.Changed() {
		t.Error("watching a new file is a change in what's watched")
	}
	write(t, b, "22")
	w.Changed()
	if !w.Changed() {
		t.Error("the new file's changes should now be noticed")
	}
}

func TestCacheReusesAnswerUntilTTL(t *testing.T) {
	calls := 0
	get := Cache(50*time.Millisecond, func() []string { calls++; return []string{"x"} })
	get()
	get()
	if calls != 1 {
		t.Fatalf("calls = %d, want 1 within the ttl", calls)
	}
	time.Sleep(70 * time.Millisecond)
	get()
	if calls != 2 {
		t.Errorf("calls = %d, want 2 after the ttl", calls)
	}
}
