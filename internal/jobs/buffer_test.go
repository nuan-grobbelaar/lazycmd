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
