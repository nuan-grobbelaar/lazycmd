package ui

import (
	"regexp"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// promptPauseThreshold is how long output has to sit with no trailing
// newline before its tail is considered a prompt rather than just a
// program between lines of normal output.
const promptPauseThreshold = 300 * time.Millisecond

// promptPattern matches the common shapes of an interactive prompt:
// "...[y/N]", "...?", "...:" (a trailing space is common and ignored).
var promptPattern = regexp.MustCompile(`(?i)(\[y/n\]|[?:])\s*$`)

func looksLikePrompt(line string) bool {
	line = strings.TrimRight(line, " \t")
	return line != "" && promptPattern.MatchString(line)
}

// keyToPTYBytes translates a key press to what a real terminal would
// send a program's stdin. Keys with no clear terminal equivalent are
// dropped (nil) rather than guessed at.
func keyToPTYBytes(msg tea.KeyMsg) []byte {
	switch msg.Type {
	case tea.KeyRunes:
		return []byte(string(msg.Runes))
	case tea.KeySpace:
		return []byte(" ")
	case tea.KeyEnter:
		return []byte("\r")
	case tea.KeyTab:
		return []byte("\t")
	case tea.KeyBackspace:
		return []byte{0x7f}
	case tea.KeyCtrlC:
		return []byte{0x03}
	case tea.KeyUp:
		return []byte("\x1b[A")
	case tea.KeyDown:
		return []byte("\x1b[B")
	case tea.KeyRight:
		return []byte("\x1b[C")
	case tea.KeyLeft:
		return []byte("\x1b[D")
	default:
		return nil
	}
}
