// Package safefile reads files that a repo controls (package.json,
// pom.xml, .lazycmd/commands.yaml, sidecars, .git/HEAD, ...) without
// letting the repo hang or exhaust lazycmd. Opening a repo must be safe,
// and a plain os.ReadFile isn't: a package.json that is a symlink to
// /dev/zero never ends, and a FIFO blocks the open forever.
//
// So files are opened non-blocking (a FIFO opens at once instead of
// waiting for a writer), only regular files are accepted, and reads stop
// at MaxSize.
package safefile

import (
	"errors"
	"fmt"
	"io"
	"os"
	"syscall"
)

// MaxSize is the most read from one file. Real config files are far
// smaller; this is only there so a huge or endless file can't be.
const MaxSize = 4 << 20

// ErrNotRegular is returned for a FIFO, device, socket or folder.
var ErrNotRegular = errors.New("not a regular file")

// Open opens path for reading if it is a regular file (after following
// symlinks), without blocking on a FIFO.
func Open(path string) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	if !info.Mode().IsRegular() {
		f.Close()
		return nil, fmt.Errorf("%s: %w", path, ErrNotRegular)
	}
	return f, nil
}

// ReadFile is os.ReadFile for files a repo controls: regular files only,
// at most MaxSize bytes.
func ReadFile(path string) ([]byte, error) {
	f, err := Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, MaxSize+1))
	if err != nil {
		return nil, err
	}
	if len(data) > MaxSize {
		return nil, fmt.Errorf("%s: bigger than %d MB", path, MaxSize>>20)
	}
	return data, nil
}

// Reader returns f limited to MaxSize bytes, for decoders that stream.
func Reader(f *os.File) io.Reader { return io.LimitReader(f, MaxSize) }
