package jobs

import "testing"

func content(b *OutputBuffer) string {
	s, _ := b.Snapshot()
	return s
}

func TestPlainLines(t *testing.T) {
	b := &OutputBuffer{}
	b.Write([]byte("hello\nworld\n"))
	if got, want := content(b), "hello\nworld\n"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestCRLFDoesNotWipeLine(t *testing.T) {
	b := &OutputBuffer{}
	b.Write([]byte("hello\r\nworld\r\n"))
	if got, want := content(b), "hello\nworld\n"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestCRLFSplitAcrossReads(t *testing.T) {
	b := &OutputBuffer{}
	b.Write([]byte("hello\r"))
	b.Write([]byte("\nworld\r"))
	b.Write([]byte("\n"))
	if got, want := content(b), "hello\nworld\n"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestBareCRRedrawsLine(t *testing.T) {
	b := &OutputBuffer{}
	b.Write([]byte("[====    ] 40%\r[========] 100%\n"))
	if got, want := content(b), "[========] 100%\n"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestBareCRAtEndOfChunkIsReallyBare(t *testing.T) {
	b := &OutputBuffer{}
	b.Write([]byte("[====    ] 40%\r"))
	b.Write([]byte("[========] 100%\n"))
	if got, want := content(b), "[========] 100%\n"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestIncompleteLastLineHasNoTrailingNewline(t *testing.T) {
	b := &OutputBuffer{}
	b.Write([]byte("done\r\nworking..."))
	if got, want := content(b), "done\nworking..."; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestScrollbackCap(t *testing.T) {
	b := &OutputBuffer{}
	for i := 0; i < maxScrollbackLines+50; i++ {
		b.Write([]byte("line\n"))
	}
	b.mu.Lock()
	n := len(b.lines)
	b.mu.Unlock()
	if n != maxScrollbackLines {
		t.Errorf("lines = %d, want %d", n, maxScrollbackLines)
	}
}

func snap(b *OutputBuffer) string { s, _ := b.Snapshot(); return s }

func TestSanitizeKeepsColorsDropsEverythingElse(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"sgr kept", "\x1b[31mred\x1b[0m", "\x1b[31mred\x1b[0m"},
		{"truecolor sgr kept", "\x1b[38;2;1;2;3mx", "\x1b[38;2;1;2;3mx"},
		{"clear screen + home", "a\x1b[2J\x1b[3J\x1b[Hb", "ab"},
		{"cursor up + erase line", "a\x1b[1A\x1b[2Kb", "ab"},
		{"osc title bel", "a\x1b]0;my title\x07b", "ab"},
		{"osc title st", "a\x1b]0;my title\x1b\\b", "ab"},
		{"private mode", "a\x1b[?25lb\x1b[?25h", "ab"},
		{"charset select", "a\x1b(Bb", "ab"},
		{"save/restore cursor", "a\x1b7b\x1b8", "ab"},
		{"bell and backspace", "a\x07\x08b", "ab"},
	}
	for _, c := range cases {
		b := &OutputBuffer{}
		b.Write([]byte(c.in))
		if got := snap(b); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

func TestSanitizeSequenceSplitAcrossWrites(t *testing.T) {
	b := &OutputBuffer{}
	for _, chunk := range []string{"a\x1b", "[2", "Jb\x1b]0;ti", "tle\x07c\x1b[3", "1mred"} {
		b.Write([]byte(chunk))
	}
	if got, want := snap(b), "abc\x1b[31mred"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestTabsExpandToEightColumnStops(t *testing.T) {
	b := &OutputBuffer{}
	b.Write([]byte("ab\tc\n\x1b[1mxyz\x1b[0m\tq"))
	if got, want := snap(b), "ab      c\n\x1b[1mxyz\x1b[0m     q"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}
