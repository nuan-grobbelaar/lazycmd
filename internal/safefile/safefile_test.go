package safefile

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// within fails the test if fn doesn't return in time: the whole point is
// that nothing here can hang.
func within(t *testing.T, fn func()) {
	t.Helper()
	done := make(chan struct{})
	go func() { fn(); close(done) }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("still running after 3s")
	}
}

func TestReadFileRefusesWhatCouldHang(t *testing.T) {
	dir := t.TempDir()
	zero := filepath.Join(dir, "package.json")
	if err := os.Symlink("/dev/zero", zero); err != nil {
		t.Fatal(err)
	}
	fifo := filepath.Join(dir, "fifo")
	if err := syscall.Mkfifo(fifo, 0o644); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{zero, fifo, dir} {
		within(t, func() {
			if _, err := ReadFile(p); !errors.Is(err, ErrNotRegular) {
				t.Errorf("%s: got %v, want ErrNotRegular", p, err)
			}
		})
	}
}

func TestReadFileSizeCap(t *testing.T) {
	dir := t.TempDir()
	big := filepath.Join(dir, "big")
	f, _ := os.Create(big)
	f.Truncate(MaxSize + 1) // sparse, so this is cheap
	f.Close()
	if _, err := ReadFile(big); err == nil {
		t.Error("a file over MaxSize should be refused")
	}
	ok := filepath.Join(dir, "ok")
	os.WriteFile(ok, []byte("hi"), 0o644)
	if data, err := ReadFile(ok); err != nil || string(data) != "hi" {
		t.Errorf("ReadFile = %q, %v", data, err)
	}
}
