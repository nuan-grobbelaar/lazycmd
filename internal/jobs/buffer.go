package jobs

import (
	"strings"
	"sync"
	"time"
)

const maxScrollbackLines = 10000

// OutputBuffer is a line buffer for PTY output. A reader goroutine writes
// to it; the UI reads it on its own poll tick. All access goes through
// the mutex, and nothing here ever touches a tea.Model directly.
type OutputBuffer struct {
	mu        sync.Mutex
	lines     []string
	current   []byte
	pendingCR bool // last chunk ended in '\r'; its meaning depends on this chunk's first byte
	version   uint64
	lastWrite time.Time
}

// Write appends a chunk of raw PTY output. A PTY in cooked mode (the
// default) turns every '\n' a program writes into "\r\n", so a lone '\r'
// not followed by '\n' is what actually signals a redrawn progress bar;
// that's the only case that resets the current line instead of
// committing it. The '\r' and '\n' can land in different reads, so a
// trailing '\r' is held as pendingCR until the next chunk resolves it.
func (b *OutputBuffer) Write(p []byte) {
	b.mu.Lock()
	defer b.mu.Unlock()

	i := 0
	if b.pendingCR {
		b.pendingCR = false
		if len(p) > 0 && p[0] == '\n' {
			b.commitLine()
			i = 1
		} else {
			b.current = b.current[:0]
		}
	}

	for ; i < len(p); i++ {
		switch c := p[i]; c {
		case '\r':
			switch {
			case i+1 < len(p) && p[i+1] == '\n':
				i++
				b.commitLine()
			case i+1 < len(p):
				b.current = b.current[:0]
			default:
				b.pendingCR = true
			}
		case '\n':
			b.commitLine()
		default:
			b.current = append(b.current, c)
		}
	}
	b.version++
	b.lastWrite = time.Now()
}

func (b *OutputBuffer) commitLine() {
	b.lines = append(b.lines, string(b.current))
	b.current = b.current[:0]
	if over := len(b.lines) - maxScrollbackLines; over > 0 {
		b.lines = b.lines[over:]
	}
}

// Touch bumps the version without writing anything, so a poller notices
// a state change (e.g. the job finished) even if its last read produced
// no new output.
func (b *OutputBuffer) Touch() {
	b.mu.Lock()
	b.version++
	b.mu.Unlock()
}

// Snapshot returns the buffer's current text and version. Callers should
// skip re-rendering when the version hasn't changed since their last call.
func (b *OutputBuffer) Snapshot() (content string, version uint64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	var sb strings.Builder
	for _, l := range b.lines {
		sb.WriteString(l)
		sb.WriteByte('\n')
	}
	sb.Write(b.current)
	return sb.String(), b.version
}

// PendingLine returns the current incomplete line (no trailing newline
// yet) and when it was last written to, for prompt detection: a pause
// after a line with no newline is what distinguishes a prompt awaiting
// input from a program that's merely between lines of normal output.
func (b *OutputBuffer) PendingLine() (line string, lastWrite time.Time) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return string(b.current), b.lastWrite
}
