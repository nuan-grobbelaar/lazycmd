package jobs

import (
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/x/ansi"
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
	esc       escState
	csi       []byte // parameters of the CSI sequence being parsed
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
		if b.esc != escNone {
			b.consumeEscape(p[i])
			continue
		}
		switch c := p[i]; c {
		case 0x1b:
			b.esc = escStart
		case '\t':
			// Terminals expand tabs themselves, but the panel's width
			// math can't, so a tab would shift every column after it
			// and break the box borders. Expand to 8-column stops here.
			col := ansi.StringWidth(string(b.current))
			b.current = append(b.current, strings.Repeat(" ", 8-col%8)...)
		case 0x00, 0x07, 0x08, 0x0b, 0x0c, 0x0e, 0x0f, 0x7f:
			// bell, backspace, form feed and friends: nothing to draw,
			// and some would move the real cursor.
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

type escState int

const (
	escNone   escState = iota
	escStart           // saw ESC
	escCSI             // inside ESC [ ... , collecting parameters
	escString          // inside an OSC/DCS/APC/PM string, until BEL or ST
	escStrEnd          // saw ESC inside a string; '\\' ends it
	escSkip1           // ESC ( ) * + # : one more byte belongs to it
)

// consumeEscape advances the escape-sequence state machine by one byte.
// Only SGR (colour/style, CSI ... m) survives into the buffer. Everything
// else - cursor movement, screen/line erase, titles, mode switches - is
// dropped: the buffer's text is later written verbatim onto the real
// terminal inside the TUI, so a program's "clear screen" (vite dev does
// this on every restart) would otherwise wipe the whole interface. The
// parser keeps its state across Write calls because a sequence can be
// split between two PTY reads.
func (b *OutputBuffer) consumeEscape(c byte) {
	switch b.esc {
	case escStart:
		switch c {
		case '[':
			b.esc = escCSI
			b.csi = b.csi[:0]
		case ']', 'P', 'X', '^', '_':
			b.esc = escString
		case '(', ')', '*', '+', '#':
			b.esc = escSkip1
		default:
			b.esc = escNone // two-byte escape, e.g. ESC 7 / ESC c
		}
	case escSkip1:
		b.esc = escNone
	case escString:
		switch c {
		case 0x07:
			b.esc = escNone
		case 0x1b:
			b.esc = escStrEnd
		}
	case escStrEnd:
		b.esc = escNone
	case escCSI:
		switch {
		case c >= 0x30 && c <= 0x3f, c >= 0x20 && c <= 0x2f:
			b.csi = append(b.csi, c) // parameter / intermediate byte
		case c >= 0x40 && c <= 0x7e:
			if c == 'm' && isSGRParams(b.csi) {
				b.current = append(b.current, "\x1b["...)
				b.current = append(b.current, b.csi...)
				b.current = append(b.current, 'm')
			}
			b.esc = escNone
		default:
			b.esc = escNone // malformed: abandon it
		}
	}
}

func isSGRParams(p []byte) bool {
	for _, c := range p {
		if (c < '0' || c > '9') && c != ';' && c != ':' {
			return false
		}
	}
	return true
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
