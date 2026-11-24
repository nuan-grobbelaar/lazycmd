package ui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestLooksLikePrompt(t *testing.T) {
	cases := map[string]bool{
		"Overwrite file? [y/N]":  true,
		"Overwrite file? [y/N] ": true,
		"Continue?":              true,
		"Password:":              true,
		"Password: ":             true,
		"":                       false,
		"Building...":            false,
		"[========] 100%":        false,
		"just some text, no cue": false,
	}
	for line, want := range cases {
		if got := looksLikePrompt(line); got != want {
			t.Errorf("looksLikePrompt(%q) = %v, want %v", line, got, want)
		}
	}
}

func TestKeyToPTYBytes(t *testing.T) {
	cases := []struct {
		msg  tea.KeyMsg
		want string
	}{
		{tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("hi")}, "hi"},
		{tea.KeyMsg{Type: tea.KeySpace}, " "},
		{tea.KeyMsg{Type: tea.KeyEnter}, "\r"},
		{tea.KeyMsg{Type: tea.KeyTab}, "\t"},
		{tea.KeyMsg{Type: tea.KeyBackspace}, "\x7f"},
		{tea.KeyMsg{Type: tea.KeyCtrlC}, "\x03"},
		{tea.KeyMsg{Type: tea.KeyUp}, "\x1b[A"},
		{tea.KeyMsg{Type: tea.KeyDown}, "\x1b[B"},
	}
	for _, c := range cases {
		if got := string(keyToPTYBytes(c.msg)); got != c.want {
			t.Errorf("keyToPTYBytes(%v) = %q, want %q", c.msg, got, c.want)
		}
	}
}

func TestKeyToPTYBytesUnhandledKeyIsNil(t *testing.T) {
	if got := keyToPTYBytes(tea.KeyMsg{Type: tea.KeyF1}); got != nil {
		t.Errorf("expected nil for an unhandled key, got %q", got)
	}
}
