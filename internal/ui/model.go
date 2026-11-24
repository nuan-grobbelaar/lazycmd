package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"lazycast/internal/config"
)

// fakeStreamLines simulates the output of a running command. Real job
// output arrives over a PTY in a later build step.
var fakeStreamLines = []string{
	"Resolving dependencies...",
	"Building...",
	"[=====>          ] 34%",
	"[==========>     ] 61%",
	"[================] 100%",
	"Done in 1.8s",
}

const streamTickInterval = 180 * time.Millisecond

type focusZone int

const (
	focusList focusZone = iota
	focusOutput
)

type Model struct {
	width, height int
	focus         focusZone

	allRows []flatRow // every row, unfiltered
	rows    []flatRow // rows currently visible under the filter
	cursor  int       // index into rows; -1 if nothing selectable

	filtering   bool
	filterQuery string

	warnings []string

	viewport    viewport.Model
	outputLines []string
	running     bool
	ready       bool
}

type flatRow struct {
	isHeader bool
	label    string
}

func New(groups []config.Group, warnings []string) Model {
	all := flattenGroups(groups)
	return Model{
		allRows:  all,
		rows:     all,
		cursor:   firstSelectable(all),
		warnings: warnings,
	}
}

func flattenGroups(groups []config.Group) []flatRow {
	var rows []flatRow
	for _, g := range groups {
		rows = append(rows, flatRow{isHeader: true, label: g.Title})
		for _, c := range g.Commands {
			rows = append(rows, flatRow{label: c.Name})
		}
	}
	return rows
}

func firstSelectable(rows []flatRow) int {
	for i, r := range rows {
		if !r.isHeader {
			return i
		}
	}
	return -1
}

// filterRows keeps a group's header only if at least one of its commands
// matches, so filtering never leaves an orphaned header on screen.
func filterRows(all []flatRow, query string) []flatRow {
	if query == "" {
		return all
	}
	needle := strings.ToLower(query)
	var out []flatRow
	var pendingHeader *flatRow
	for _, row := range all {
		if row.isHeader {
			h := row
			pendingHeader = &h
			continue
		}
		if strings.Contains(strings.ToLower(row.label), needle) {
			if pendingHeader != nil {
				out = append(out, *pendingHeader)
				pendingHeader = nil
			}
			out = append(out, row)
		}
	}
	return out
}

func (m *Model) applyFilter() {
	m.rows = filterRows(m.allRows, m.filterQuery)
	m.cursor = firstSelectable(m.rows)
}

func (m Model) Init() tea.Cmd {
	return nil
}

// streamTickMsg advances the fake output by one line.
type streamTickMsg struct {
	cmdName string
	n       int
}

func nextStreamTick(cmdName string, n int) tea.Cmd {
	return tea.Tick(streamTickInterval, func(time.Time) tea.Msg {
		return streamTickMsg{cmdName: cmdName, n: n}
	})
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.layout()
		m.ready = true
		return m, nil

	case tea.KeyMsg:
		if m.filtering {
			return m.updateFiltering(msg)
		}
		return m.updateNormal(msg)

	case streamTickMsg:
		if msg.n >= len(fakeStreamLines) {
			m.running = false
			return m, nil
		}
		m.outputLines = append(m.outputLines, fakeStreamLines[msg.n])
		m.viewport.SetContent(strings.Join(m.outputLines, "\n"))
		m.viewport.GotoBottom()
		return m, nextStreamTick(msg.cmdName, msg.n+1)
	}

	var cmd tea.Cmd
	m.viewport, cmd = m.viewport.Update(msg)
	return m, cmd
}

func (m Model) updateFiltering(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyCtrlC:
		return m, tea.Quit
	case tea.KeyEsc:
		m.filtering = false
		m.filterQuery = ""
		m.applyFilter()
	case tea.KeyEnter:
		m.filtering = false
	case tea.KeyBackspace:
		if len(m.filterQuery) > 0 {
			m.filterQuery = m.filterQuery[:len(m.filterQuery)-1]
			m.applyFilter()
		}
	case tea.KeyRunes:
		m.filterQuery += string(msg.Runes)
		m.applyFilter()
	}
	return m, nil
}

func (m Model) updateNormal(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c", "q":
		return m, tea.Quit
	case "/":
		m.filtering = true
		return m, nil
	case "esc":
		if m.filterQuery != "" {
			m.filterQuery = ""
			m.applyFilter()
		}
		return m, nil
	case "tab":
		if m.focus == focusList {
			m.focus = focusOutput
		} else {
			m.focus = focusList
		}
		return m, nil
	case "up", "k":
		if m.focus == focusList {
			m.moveCursor(-1)
		}
		return m, nil
	case "down", "j":
		if m.focus == focusList {
			m.moveCursor(1)
		}
		return m, nil
	case "enter":
		if m.focus == focusList && m.cursor >= 0 && !m.rows[m.cursor].isHeader {
			name := m.rows[m.cursor].label
			m.running = true
			m.outputLines = []string{"$ " + name}
			m.viewport.SetContent(strings.Join(m.outputLines, "\n"))
			return m, nextStreamTick(name, 0)
		}
		return m, nil
	}
	return m, nil
}

func (m *Model) moveCursor(delta int) {
	next := m.cursor + delta
	for next >= 0 && next < len(m.rows) && m.rows[next].isHeader {
		next += delta
	}
	if next >= 0 && next < len(m.rows) {
		m.cursor = next
	}
}

const (
	listWidthFrac = 0.32
	keyBarHeight  = 1
)

func (m *Model) layout() {
	listWidth := int(float64(m.width) * listWidthFrac)
	outputWidth := m.width - listWidth
	bodyHeight := m.height - keyBarHeight

	m.viewport = viewport.New(outputWidth-2, bodyHeight-2)
	m.viewport.SetContent(m.initialOutputText())
}

const welcomeText = "Select a command on the left and press enter.\n\nOutput streams here."

func (m Model) initialOutputText() string {
	if len(m.warnings) == 0 {
		return welcomeText
	}
	return "Config warnings:\n\n" + strings.Join(m.warnings, "\n")
}

func (m Model) View() string {
	if !m.ready {
		return "starting lazycast..."
	}

	listWidth := int(float64(m.width) * listWidthFrac)
	outputWidth := m.width - listWidth
	bodyHeight := m.height - keyBarHeight

	listPanel := renderPanel("[1] Commands", m.renderList(), listWidth, bodyHeight, m.focus == focusList)
	outputPanel := renderPanel("[2] Output", m.viewport.View(), outputWidth, bodyHeight, m.focus == focusOutput)

	body := lipgloss.JoinHorizontal(lipgloss.Top, listPanel, outputPanel)
	keyBar := m.renderKeyBar()

	return lipgloss.JoinVertical(lipgloss.Left, body, keyBar)
}

func (m Model) renderList() string {
	headerStyle := lipgloss.NewStyle().Bold(true).Foreground(colorTextDim)
	selectedStyle := lipgloss.NewStyle().Foreground(colorAccent).Bold(true)
	plainStyle := lipgloss.NewStyle().Foreground(colorTextSoft)
	filterStyle := lipgloss.NewStyle().Foreground(colorAccent)

	var b strings.Builder
	if m.filtering || m.filterQuery != "" {
		b.WriteString(filterStyle.Render("/ " + m.filterQuery))
		b.WriteByte('\n')
	}

	if len(m.rows) == 0 {
		b.WriteString(plainStyle.Render("  no matches"))
		return b.String()
	}

	for i, row := range m.rows {
		if i > 0 {
			b.WriteByte('\n')
		}
		switch {
		case row.isHeader:
			b.WriteString(headerStyle.Render(row.label))
		case i == m.cursor:
			b.WriteString(selectedStyle.Render("› " + row.label))
		default:
			b.WriteString(plainStyle.Render("  " + row.label))
		}
	}
	return b.String()
}

func (m Model) renderKeyBar() string {
	style := lipgloss.NewStyle().Foreground(colorTextDim).Width(m.width)
	if m.filtering {
		return style.Render(" type to filter  enter confirm  esc clear")
	}
	status := "idle"
	if m.running {
		status = "running"
	}
	return style.Render(fmt.Sprintf(" ↑/↓ select  enter run  tab focus  / filter  q quit   [%s]", status))
}
