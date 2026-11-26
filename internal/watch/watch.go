// Package watch notices when the files lazycmd reads have changed, by
// checking file times and folder listings. It needs no dependency, and
// unlike file-system events it copes with editors that save by writing a
// new file and renaming it over the old one, and with git checkouts that
// touch many files at once.
package watch

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// Signature summarises the state of paths. A file counts by its
// modification time, size and mode (so a script becoming executable
// counts); a missing path counts as missing. A folder counts by that and
// by each entry directly in it, so adding, removing or editing a file
// there changes the signature. Symlinks are followed.
func Signature(paths []string) string {
	set := map[string]bool{}
	for _, p := range paths {
		set[filepath.Clean(p)] = true
	}
	sorted := make([]string, 0, len(set))
	for p := range set {
		sorted = append(sorted, p)
	}
	sort.Strings(sorted)

	h := sha256.New()
	for _, p := range sorted {
		info, err := os.Stat(p)
		if err != nil {
			fmt.Fprintf(h, "%s|missing\n", p)
			continue
		}
		fmt.Fprintf(h, "%s|%s\n", p, stat(info))
		if !info.IsDir() {
			continue
		}
		entries, err := os.ReadDir(p)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if ei, err := os.Stat(filepath.Join(p, e.Name())); err == nil {
				fmt.Fprintf(h, "  %s|%s\n", e.Name(), stat(ei))
			} else {
				fmt.Fprintf(h, "  %s|broken\n", e.Name())
			}
		}
	}
	return fmt.Sprintf("%x", h.Sum(nil))
}

func stat(i os.FileInfo) string {
	return fmt.Sprintf("%d|%d|%o", i.ModTime().UnixNano(), i.Size(), i.Mode())
}

// Watcher reports changes to a set of paths, once they've settled.
type Watcher struct {
	paths   func() []string
	applied string // the state last reported (or the starting one)
	pending string // a changed state seen once; reported if it's seen again
}

// New starts watching: the paths' state now is the baseline. paths is
// asked again on every check, so it can grow as new files appear.
func New(paths func() []string) *Watcher {
	return &Watcher{paths: paths, applied: Signature(paths())}
}

// Changed is called on a timer. It reports true once per change, when
// the new state has been the same on two checks in a row. That waits for
// a save, a checkout or a build to finish instead of reloading halfway
// through it, and means a change is acted on one to two intervals after
// it happens.
func (w *Watcher) Changed() bool {
	sig := Signature(w.paths())
	switch {
	case sig == w.applied:
		w.pending = ""
		return false
	case sig == w.pending:
		w.applied, w.pending = sig, ""
		return true
	default:
		w.pending = sig
		return false
	}
}

// Cache wraps fn, which may be slow (it walks folders), so it runs at
// most once per ttl and its last answer is reused in between.
func Cache(ttl time.Duration, fn func() []string) func() []string {
	var (
		mu   sync.Mutex
		at   time.Time
		last []string
	)
	return func() []string {
		mu.Lock()
		defer mu.Unlock()
		if at.IsZero() || time.Since(at) >= ttl {
			last, at = fn(), time.Now()
		}
		return last
	}
}
