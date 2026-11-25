// Package safetext makes text from files lazycmd doesn't control (a
// repo's .lazycmd/, package.json, pom.xml, .git/HEAD, options_cmd
// output) safe to draw on the terminal.
//
// Such text is written verbatim into the TUI, so an escape sequence in a
// command name or a branch name would reach the real terminal: OSC 52
// can overwrite the clipboard, OSC 0 the window title, and SGR "conceal"
// or same-colour text can hide part of a name. Every control character
// is therefore replaced with a visible stand-in (ESC shows as ^[), which
// both defuses it and makes the tampering obvious.
package safetext

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// Line cleans s for display on a single line: newlines and tabs become
// spaces, and every other control character is made visible.
func Line(s string) string { return clean(s, false) }

// Text is Line but keeps newlines, for multi-line text such as a
// command's description or a warning list.
func Text(s string) string { return clean(s, true) }

func clean(s string, keepNewlines bool) string {
	if isSafe(s, keepNewlines) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s) + 8)
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && size <= 1 {
			// Invalid UTF-8, including raw 8-bit C1 bytes (0x9b is a
			// one-byte CSI for terminals that honour it).
			b.WriteRune(utf8.RuneError)
			i++
			continue
		}
		i += size
		switch {
		case r == '\n' && keepNewlines:
			b.WriteByte('\n')
		case r == '\n', r == '\t', r == '\r':
			b.WriteByte(' ')
		case r < 0x20 || r == 0x7f:
			b.WriteByte('^')
			b.WriteRune(r ^ 0x40)
		case r >= 0x80 && r <= 0x9f, isBidiControl(r):
			fmt.Fprintf(&b, "<U+%04X>", r)
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// isSafe is the fast path: most text has nothing to replace.
func isSafe(s string, keepNewlines bool) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 0x80 {
			return false // let the slow path decode and check it
		}
		if c < 0x20 || c == 0x7f {
			if c == '\n' && keepNewlines {
				continue
			}
			return false
		}
	}
	return true
}

// isBidiControl reports the Unicode bidirectional overrides and
// isolates, which reorder how surrounding text is displayed and can make
// a name read differently from what it is ("trojan source").
func isBidiControl(r rune) bool {
	return (r >= 0x202a && r <= 0x202e) || (r >= 0x2066 && r <= 0x2069) || r == 0x200e || r == 0x200f || r == 0x061c
}
