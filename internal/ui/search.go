package ui

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
)

// Search in the Output panel. "/" opens a query line, every match is
// highlighted, n / N step through them, esc clears. It searches what the
// job printed with the colour codes left out, so a coloured word is found
// the same as a plain one, and it ignores case unless the query has a
// capital letter ("smart case"). Searching stops the output following,
// the same as scrolling up does.

const (
	maxMatches = 100000 // a search with more hits than this stops counting

	hlOn     = "\x1b[7m"        // every match: reverse video
	hlOff    = "\x1b[27m"       //
	hlCurOn  = "\x1b[1;30;43m"  // the current match: black on amber
	hlCurOff = "\x1b[22;39;49m" //
)

// match is one hit: a line of the output and the part of that line's
// visible text (counted in characters) that matched.
type match struct{ line, start, end int }

type searchState struct {
	editing bool   // the query line is open and takes keys
	query   string // "" means no search
	cur     int    // index into matches
	matches []match
}

// visibleText is a line without its escape sequences, with where each
// visible character began in the original, so a match can be mapped back.
func visibleText(line string) (text []rune, offs []int) {
	for i := 0; i < len(line); {
		if line[i] == 0x1b {
			i = skipEscape(line, i)
			continue
		}
		r, size := utf8.DecodeRuneInString(line[i:])
		text = append(text, r)
		offs = append(offs, i)
		i += size
	}
	offs = append(offs, len(line))
	return text, offs
}

// skipEscape returns the index after the escape sequence starting at
// line[i]: a CSI (ESC [ ... final byte), an OSC (ESC ] ... BEL or ESC \),
// or any other two-byte escape.
func skipEscape(line string, i int) int {
	if i+1 >= len(line) {
		return len(line)
	}
	switch line[i+1] {
	case '[':
		for j := i + 2; j < len(line); j++ {
			if line[j] >= 0x40 && line[j] <= 0x7e {
				return j + 1
			}
		}
		return len(line)
	case ']':
		for j := i + 2; j < len(line); j++ {
			if line[j] == 0x07 {
				return j + 1
			}
			if line[j] == 0x1b && j+1 < len(line) && line[j+1] == '\\' {
				return j + 2
			}
		}
		return len(line)
	}
	return i + 2
}

// foldRunes lowercases rune by rune, so positions stay one to one.
func foldRunes(r []rune) []rune {
	out := make([]rune, len(r))
	for i, c := range r {
		out[i] = unicode.ToLower(c)
	}
	return out
}

// findMatches lists every match of query in body (lines apart by \n),
// non-overlapping, in order.
func findMatches(body, query string) []match {
	if query == "" {
		return nil
	}
	needle := []rune(query)
	fold := query == strings.ToLower(query) // smart case
	if fold {
		needle = foldRunes(needle)
	}
	var out []match
	for ln, line := range strings.Split(body, "\n") {
		text, _ := visibleText(line)
		if len(text) < len(needle) {
			continue
		}
		if fold {
			text = foldRunes(text)
		}
		for i := 0; i+len(needle) <= len(text); {
			if equalRunes(text[i:i+len(needle)], needle) {
				out = append(out, match{line: ln, start: i, end: i + len(needle)})
				if len(out) >= maxMatches {
					return out
				}
				i += len(needle)
				continue
			}
			i++
		}
	}
	return out
}

func equalRunes(a, b []rune) bool {
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// highlight puts the matches' highlighting into body. The text keeps its
// own escape sequences; only the start and end of each match get one.
func highlight(body string, matches []match, cur int) string {
	if len(matches) == 0 {
		return body
	}
	byLine := map[int][]int{} // line -> indexes into matches
	for i, m := range matches {
		byLine[m.line] = append(byLine[m.line], i)
	}
	lines := strings.Split(body, "\n")
	for ln, idxs := range byLine {
		line := lines[ln]
		_, offs := visibleText(line)
		var b strings.Builder
		pos := 0
		for _, i := range idxs {
			m := matches[i]
			on, off := hlOn, hlOff
			if i == cur {
				on, off = hlCurOn, hlCurOff
			}
			b.WriteString(line[pos:offs[m.start]])
			b.WriteString(on)
			// An escape sequence inside the match (a colour reset, say)
			// would switch the highlight off, so it's put back after each.
			seg := line[offs[m.start]:offs[m.end]]
			for k := 0; k < len(seg); {
				if seg[k] == 0x1b {
					end := skipEscape(seg, k)
					b.WriteString(seg[k:end])
					b.WriteString(on)
					k = end
					continue
				}
				b.WriteByte(seg[k])
				k++
			}
			b.WriteString(off)
			pos = offs[m.end]
		}
		b.WriteString(line[pos:])
		lines[ln] = b.String()
	}
	return strings.Join(lines, "\n")
}

// syncOutput puts the active job's output (or the start text) in the
// panel, with the search's matches found and highlighted. Everything that
// refreshes the panel goes through here, so a search follows new output
// and a different job.
func (m *Model) syncOutput() {
	body := m.activeJobBody()
	if m.search.query != "" {
		m.search.matches = findMatches(body, m.search.query)
		if m.search.cur >= len(m.search.matches) {
			m.search.cur = max(0, len(m.search.matches)-1)
		}
		body = highlight(body, m.search.matches, m.search.cur)
	} else {
		m.search.matches, m.search.cur = nil, 0
	}
	m.viewport.SetContent(body)
}

// showMatch scrolls so the current match is on screen, a third of the way
// down when it has to move, and stops following the output.
func (m *Model) showMatch() {
	if len(m.search.matches) == 0 {
		return
	}
	line := m.search.matches[m.search.cur].line
	top, h := m.viewport.YOffset, m.viewport.Height
	if line < top || line >= top+h {
		m.viewport.SetYOffset(max(0, line-h/3))
	}
	m.followTail = false
}

// startSearch opens the query line.
func (m Model) startSearch() Model {
	m.search = searchState{editing: true}
	m.syncOutput()
	return m
}

func (m Model) clearSearch() Model {
	m.search = searchState{}
	m.syncOutput()
	return m
}

// updateSearch takes keys while the query line is open.
func (m Model) updateSearch(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyCtrlC:
		return m, tea.Quit
	case tea.KeyEsc:
		return m.clearSearch(), nil
	case tea.KeyEnter:
		m.search.editing = false
		if m.search.query == "" {
			return m.clearSearch(), nil
		}
		return m, nil
	case tea.KeyBackspace:
		if r := []rune(m.search.query); len(r) > 0 {
			m.search.query = string(r[:len(r)-1])
		}
	case tea.KeyRunes, tea.KeySpace: // a space arrives as KeySpace, with the space in Runes
		m.search.query += string(msg.Runes)
	default:
		return m, nil
	}
	m.syncOutput()
	// Jump to the first match from the top of what's on screen, so
	// typing lands on the nearest hit.
	m.search.cur = 0
	for i, mt := range m.search.matches {
		if mt.line >= m.viewport.YOffset {
			m.search.cur = i
			break
		}
	}
	m.syncOutput()
	m.showMatch()
	return m, nil
}

// updateOutputSearchKey handles the search keys when the Output panel
// has focus. It reports false for any other key.
func (m Model) updateOutputSearchKey(msg tea.KeyMsg) (Model, bool) {
	switch msg.String() {
	case "/":
		if m.activeJob == nil {
			m.flash = "no output to search yet"
			return m, true
		}
		return m.startSearch(), true
	case "n", "N":
		if m.search.query == "" {
			return m, false
		}
		if n := len(m.search.matches); n > 0 {
			if msg.String() == "n" {
				m.search.cur = (m.search.cur + 1) % n
			} else {
				m.search.cur = (m.search.cur - 1 + n) % n
			}
			m.syncOutput()
			m.showMatch()
		}
		return m, true
	case "esc":
		if m.search.query == "" {
			return m, false
		}
		return m.clearSearch(), true
	}
	return m, false
}

// searchStatus is "3/17" for the panel title, or "no matches".
func (m Model) searchStatus() string {
	if m.search.query == "" {
		return ""
	}
	if len(m.search.matches) == 0 {
		return "no matches"
	}
	count := fmt.Sprint(len(m.search.matches))
	if len(m.search.matches) >= maxMatches {
		count += "+"
	}
	return fmt.Sprintf("%d/%s", m.search.cur+1, count)
}
