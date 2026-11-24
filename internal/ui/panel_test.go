package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// assertUniformWidth fails if any line of s isn't exactly want cells
// wide. This is the exact shape of bug this package has hit twice:
// renderPanel's top border overflowing while every other line in the
// box was correctly sized, which breaks lipgloss.JoinHorizontal and
// silently drops a whole panel's border from the composed view.
func assertUniformWidth(t *testing.T, s string, want int) {
	t.Helper()
	for i, line := range strings.Split(s, "\n") {
		if w := ansi.StringWidth(line); w != want {
			t.Errorf("line %d width = %d, want %d: %q", i, w, want, line)
		}
	}
}

func TestRenderPanelIsUniformWidth(t *testing.T) {
	// width 3 is the real floor: the top line always starts with the
	// literal "╭─" plus a closing "╮", so nothing narrower than 3 cells
	// can ever match exactly - not a bug, just two corners and a dash
	// not fitting in less space than that exists to hold them.
	cases := []struct {
		name  string
		title string
		width int
	}{
		{"normal", "[1] Commands", 32},
		{"title wider than panel", "[3] Jobs", 8},
		{"title much wider than panel", "Input requested: a-very-long-command-name", 20},
		{"minimum achievable width", "X", 3},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out := renderPanel(c.title, "hello\nworld", c.width, 6, false)
			assertUniformWidth(t, out, c.width)
			if lines := strings.Split(out, "\n"); len(lines) != 6 {
				t.Errorf("got %d lines, want 6 (height)", len(lines))
			}
		})
	}
}

// TestRenderPanelNeverPanics covers sizes below the width-3 floor,
// where exact alignment genuinely isn't achievable - the only
// guarantee left to make is that it doesn't crash. Caught a real bug
// writing this: strings.Repeat on the bottom border line used to
// compute its count as a raw `width-2` instead of the already-clamped
// innerWidth, so width 1 panicked with a negative repeat count.
func TestRenderPanelNeverPanics(t *testing.T) {
	for _, width := range []int{-5, -1, 0, 1, 2} {
		for _, height := range []int{-5, -1, 0, 1, 2} {
			renderPanel("title", "body", width, height, false)
		}
	}
}

func TestRenderPanelBodyTruncatesLongLines(t *testing.T) {
	out := renderPanel("t", "this line is way too long for the box", 15, 3, false)
	assertUniformWidth(t, out, 15)
}

func TestRenderPanelPadsShortBody(t *testing.T) {
	out := renderPanel("t", "one line", 20, 8, false)
	if lines := strings.Split(out, "\n"); len(lines) != 8 {
		t.Errorf("got %d lines, want 8", len(lines))
	}
}

func TestOverlaySplicesAtTheRightPosition(t *testing.T) {
	base := "AAAAAAAAAA\nBBBBBBBBBB\nCCCCCCCCCC"
	popup := "XX\nYY"
	got := overlay(base, popup, 3, 1)
	want := "AAAAAAAAAA\nBBBXXBBBBB\nCCCYYCCCCC"
	if got != want {
		t.Errorf("overlay() =\n%q\nwant\n%q", got, want)
	}
}

func TestOverlayOutOfBoundsRowsAreDropped(t *testing.T) {
	base := "AAAA\nBBBB"
	popup := "XX\nYY\nZZ" // row 2 (ZZ) has no corresponding base line at y=0
	got := overlay(base, popup, 0, 0)
	want := "XXAA\nYYBB"
	if got != want {
		t.Errorf("overlay() =\n%q\nwant\n%q", got, want)
	}
}

func TestOverlayCenteredPositionsInTheMiddle(t *testing.T) {
	var baseLines []string
	for i := 0; i < 10; i++ {
		baseLines = append(baseLines, strings.Repeat(".", 20))
	}
	base := strings.Join(baseLines, "\n")
	popup := "XXXX\nYYYY"

	got := overlayCentered(base, popup, 20, 10)
	lines := strings.Split(got, "\n")

	// (20-4)/2=8 columns in, (10-2)/2=4 rows down.
	if !strings.Contains(lines[4], "XXXX") {
		t.Errorf("expected XXXX on row 4, got %q", lines[4])
	}
	if !strings.Contains(lines[5], "YYYY") {
		t.Errorf("expected YYYY on row 5, got %q", lines[5])
	}
	if got := strings.Index(lines[4], "XXXX"); got != 8 {
		t.Errorf("expected XXXX at column 8, got column %d", got)
	}
}

func TestOverlayPreservesColorAcrossTheSplice(t *testing.T) {
	// A colored base line with a plain popup spliced into the middle of
	// it shouldn't bleed the base's color into or past the popup, or
	// break the escape sequence the splice cuts through.
	accent := lipgloss.NewStyle().Foreground(colorAccent).Render("XXXXXXXXXX")
	got := overlay(accent, "POP", 3, 0)
	if !strings.Contains(got, "POP") {
		t.Fatalf("popup text missing from spliced result: %q", got)
	}
	if ansi.StringWidth(strings.Split(got, "\n")[0]) != ansi.StringWidth(strings.Split(accent, "\n")[0]) {
		t.Errorf("spliced line width changed: %q", got)
	}
}
