package ui

import (
	"fmt"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"lazycmd/internal/config"
	"lazycmd/internal/safetext"
	"lazycmd/internal/watch"
)

// Config reloads by itself. A timer asks a Reloader once a second whether
// the files lazycmd read have changed; when they have, it rebuilds the
// command list the way startup does, and the model swaps it in:
//
//   - the selection is kept by command id, and so is the filter
//   - running jobs and open forms are untouched: a form keeps the spec it
//     was opened with until it closes
//   - what changed is said once, in the key bar and in the Output panel's
//     start text, together with any new warnings
//   - a repo whose own commands changed is held back again, with the
//     "review" row, the same as at startup (the prompt itself only opens
//     on its own at startup)

// Loaded is a freshly built command list: what startup builds, as
// cmd/lazycmd assembles it.
type Loaded struct {
	Groups   []config.Group
	Warnings []string
	// Pending is set when the repo's own commands aren't allowed (yet, or
	// any more): Groups then leaves them out.
	Pending *PendingTrust
}

// A Reloader reports whether the command list should be rebuilt, and
// builds it. Poll runs off the UI goroutine.
type Reloader interface {
	Poll() (*Loaded, bool)
}

// FileReloader rebuilds the list with Load whenever Watcher sees a change.
type FileReloader struct {
	Watcher *watch.Watcher
	Load    func() Loaded
}

func (r FileReloader) Poll() (*Loaded, bool) {
	if !r.Watcher.Changed() {
		return nil, false
	}
	l := r.Load()
	return &l, true
}

// reloadInterval is how often files are checked; a var so tests can
// shorten it.
var reloadInterval = time.Second

type reloadTickMsg struct{}

// reloadedMsg is the answer to a check: loaded is nil when nothing changed.
type reloadedMsg struct{ loaded *Loaded }

func reloadTick() tea.Cmd {
	return tea.Tick(reloadInterval, func(time.Time) tea.Msg { return reloadTickMsg{} })
}

// WithReload makes the model rebuild its command list when r says so.
func (m Model) WithReload(r Reloader) Model {
	m.reloader = r
	return m
}

func (m Model) pollReload() tea.Cmd {
	r := m.reloader
	return func() tea.Msg {
		if l, ok := r.Poll(); ok {
			return reloadedMsg{loaded: l}
		}
		return reloadedMsg{}
	}
}

// cmdSig is what makes a command "changed" when it differs: what it runs,
// how it's described and its form.
func cmdSig(c config.ResolvedCommand) string {
	return fmt.Sprintf("%s|%s|%+v|%v|%s|%s", c.Run, c.Desc, c.Params, c.Terminal, c.Confirm, c.ScriptPath)
}

type listedCmd struct{ name, sig string }

func listed(rows []flatRow) map[string]listedCmd {
	out := map[string]listedCmd{}
	for _, r := range rows {
		if !r.isHeader && r.cmd.ID != trustReviewID {
			out[r.cmd.ID] = listedCmd{name: r.cmd.Name, sig: cmdSig(r.cmd)}
		}
	}
	return out
}

// applyLoaded swaps in a rebuilt command list.
func (m Model) applyLoaded(l *Loaded) Model {
	before := listed(m.allRows)
	selected := ""
	if m.cursor >= 0 && m.cursor < len(m.rows) && !m.rows[m.cursor].isHeader {
		selected = m.rows[m.cursor].cmd.ID
	}
	prevPending, promptWasOpen := m.pendingTrust, m.trustPromptOpen

	old := map[string]bool{}
	for _, w := range m.warnings {
		old[w] = true
	}
	var fresh []string
	warnings := make([]string, 0, len(l.Warnings)+len(m.extraWarnings))
	for _, w := range l.Warnings {
		w = safetext.Text(w)
		warnings = append(warnings, w)
		if !old[w] {
			fresh = append(fresh, w)
		}
	}
	m.warnings = append(warnings, m.extraWarnings...)

	m.pendingTrust, m.trustPromptOpen = nil, false
	m.setGroups(l.Groups)
	if l.Pending != nil {
		m = m.holdBack(*l.Pending)
		// A prompt someone is looking at stays open, showing the new
		// state; one that isn't open stays closed.
		m.trustPromptOpen = promptWasOpen && prevPending != nil
	}
	if selected != "" {
		for i, r := range m.rows {
			if !r.isHeader && r.cmd.ID == selected {
				m.cursor = i
				break
			}
		}
	}

	var parts []string
	after := listed(m.allRows)
	var added, removed, changed []string
	for id, c := range after {
		switch b, ok := before[id]; {
		case !ok:
			added = append(added, c.name)
		case b.sig != c.sig:
			changed = append(changed, c.name)
		}
	}
	for id, c := range before {
		if _, ok := after[id]; !ok {
			removed = append(removed, c.name)
		}
	}
	for _, g := range []struct {
		what  string
		names []string
	}{{"added", added}, {"removed", removed}, {"changed", changed}} {
		if len(g.names) > 0 {
			parts = append(parts, fmt.Sprintf("%d %s (%s)", len(g.names), g.what, nameList(g.names)))
		}
	}
	if l.Pending != nil && (prevPending == nil || prevPending.Fingerprint != l.Pending.Fingerprint) {
		parts = append(parts, fmt.Sprintf("%d of this repo's commands need review", len(l.Pending.Commands)))
	}
	if len(parts) > 0 || len(fresh) > 0 {
		summary := "Reloaded: " + strings.Join(parts, ", ")
		if len(parts) == 0 {
			summary = "Reloaded"
		}
		note := summary
		if len(fresh) > 0 {
			note += "\nNew warnings:\n  " + strings.Join(fresh, "\n  ")
		}
		m.reloadNote = note
		m.flash = strings.ToLower(summary[:1]) + summary[1:]
		if len(fresh) > 0 {
			m.flash += fmt.Sprintf("; %d new warning%s", len(fresh), plural(len(fresh)))
		}
	}
	if m.activeJob == nil {
		m.syncOutput()
	}
	return m
}

// nameList shows up to three names, sorted, then "…".
func nameList(names []string) string {
	sort.Strings(names)
	if len(names) > 3 {
		return strings.Join(safeNames(names[:3]), ", ") + ", …"
	}
	return strings.Join(safeNames(names), ", ")
}

func safeNames(names []string) []string {
	out := make([]string, len(names))
	for i, n := range names {
		out[i] = safetext.Line(n)
	}
	return out
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}
