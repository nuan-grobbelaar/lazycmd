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

func TestClampInt(t *testing.T) {
	cases := []struct {
		desired, min, max, want int
	}{
		{70, 40, 100, 70}, // within range: desired wins
		{70, 40, 50, 50},  // above max: clamp down
		{10, 40, 50, 40},  // below min: clamp up
		{70, 40, 34, 34},  // screen tighter than the floor: availability wins, not the floor
		{70, 40, 0, 1},    // degenerate: never return <= 0
		{70, 40, -5, 1},
	}
	for _, c := range cases {
		if got := clampInt(c.desired, c.min, c.max); got != c.want {
			t.Errorf("clampInt(%d, %d, %d) = %d, want %d", c.desired, c.min, c.max, got, c.want)
		}
	}
}
