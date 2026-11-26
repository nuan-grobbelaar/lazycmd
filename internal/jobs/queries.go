package jobs

import (
	"bytes"
	"sync/atomic"
)

// Programs that draw in colour (anything built on termenv or lipgloss,
// which includes lazycmd itself) ask the terminal for its background
// colour and cursor position at start-up, then wait up to five seconds
// for an answer. The panel's pseudo-terminal has no terminal behind it to
// answer, so each such program would sit there for five seconds. The
// reader answers these few questions itself, so they cost nothing.
//
// The answers are made up from one fact: whether the real terminal has a
// dark background (SetDarkBackground), so programs pick colours that
// suit it.

var darkBackground atomic.Bool

func init() { darkBackground.Store(true) }

// SetDarkBackground says whether the terminal lazycmd runs in has a dark
// background, for the answer to a background-colour query.
func SetDarkBackground(dark bool) { darkBackground.Store(dark) }

const (
	oscBackground = "\x1b]11;?"
	oscForeground = "\x1b]10;?"
	cursorReport  = "\x1b[6n"
	longestQuery  = len(oscBackground) // they are all about this long
)

// queryScanner finds terminal queries in a job's output, which can
// arrive split across reads.
type queryScanner struct{ tail []byte }

// scan returns the replies to the queries in p, to be written to the job.
func (q *queryScanner) scan(p []byte) []byte {
	data := append(q.tail, p...)
	var replies []byte
	consumed := 0

	for {
		best, kind := -1, ""
		for _, k := range []string{oscBackground, oscForeground, cursorReport} {
			if i := bytes.Index(data[consumed:], []byte(k)); i >= 0 && (best < 0 || i < best) {
				best, kind = i, k
			}
		}
		if best < 0 {
			break
		}
		consumed += best + len(kind)
		switch kind {
		case oscBackground:
			replies = append(replies, colourReply("11", darkBackground.Load())...)
		case oscForeground:
			replies = append(replies, colourReply("10", !darkBackground.Load())...)
		case cursorReport:
			replies = append(replies, "\x1b[1;1R"...)
		}
	}

	// Keep the end, in case a query is cut in two by the read.
	keep := min(len(data)-consumed, longestQuery-1)
	q.tail = append([]byte(nil), data[len(data)-keep:]...)
	return replies
}

// colourReply is an xterm colour report: black when dark, else white.
func colourReply(which string, black bool) string {
	c := "ffff/ffff/ffff"
	if black {
		c = "0000/0000/0000"
	}
	return "\x1b]" + which + ";rgb:" + c + "\x1b\\"
}
