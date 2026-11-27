package jobs

import (
	"strings"
	"testing"
	"time"

	"lazycmd/internal/config"
)

func TestQueryScannerAnswersTerminalQueries(t *testing.T) {
	SetDarkBackground(true)
	defer SetDarkBackground(true)

	var q queryScanner
	if got := string(q.scan([]byte("\x1b]11;?\x1b\\"))); got != "\x1b]11;rgb:0000/0000/0000\x1b\\" {
		t.Errorf("background = %q", got)
	}
	if got := string(q.scan([]byte("\x1b]10;?\x07"))); got != "\x1b]10;rgb:ffff/ffff/ffff\x1b\\" {
		t.Errorf("foreground = %q", got)
	}
	if got := string(q.scan([]byte("\x1b[6n"))); got != "\x1b[1;1R" {
		t.Errorf("cursor = %q", got)
	}
	// What termenv sends: the colour question, then the cursor one.
	if got := string(q.scan([]byte("hello \x1b]11;?\x1b\\\x1b[6n world"))); got != "\x1b]11;rgb:0000/0000/0000\x1b\\\x1b[1;1R" {
		t.Errorf("both = %q", got)
	}
	if got := q.scan([]byte("plain output with \x1b[31mcolour\x1b[0m and no questions\n")); len(got) != 0 {
		t.Errorf("answered a question nobody asked: %q", got)
	}

	SetDarkBackground(false)
	if got := string(q.scan([]byte("\x1b]11;?\x1b\\"))); got != "\x1b]11;rgb:ffff/ffff/ffff\x1b\\" {
		t.Errorf("light background = %q", got)
	}
}

func TestQueryScannerHandlesAQuerySplitAcrossReads(t *testing.T) {
	SetDarkBackground(true)
	var q queryScanner
	var got string
	for _, part := range []string{"abc\x1b]1", "1;", "?\x1b", "\\def\x1b[", "6n"} {
		got += string(q.scan([]byte(part)))
	}
	if got != "\x1b]11;rgb:0000/0000/0000\x1b\\\x1b[1;1R" {
		t.Errorf("replies = %q: one answer for each question, however the reads fall", got)
	}
}

// A program in the panel that asks for the background colour gets an
// answer at once, instead of waiting out its timeout.
func TestJobGetsAnswersToTerminalQueries(t *testing.T) {
	SetDarkBackground(true)
	script := `stty raw -echo
printf '\033]11;?\033\\'
head -c 25 | od -An -c
printf '\033[6n'
head -c 6 | od -An -c`
	j, err := Start("q1", config.ResolvedCommand{Name: "q", Run: script}, nil, testDir(t))
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	waitForStatus(t, j, StatusSucceeded, 4*time.Second)
	if took := time.Since(start); took > 3*time.Second {
		t.Errorf("took %s", took)
	}
	out, _ := j.Buffer.Snapshot()
	flat := strings.Join(strings.Fields(out), "")
	if !strings.Contains(flat, `]11;rgb:0000/0000/0000`) || !strings.Contains(flat, `033[1;1R`) {
		t.Errorf("the program got no answer: %q", out)
	}
}
