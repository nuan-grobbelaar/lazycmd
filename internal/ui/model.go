package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"lazycast/internal/config"
	"lazycast/internal/jobs"
)

const pollInterval = 50 * time.Millisecond

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
	stateDir string

	viewport    viewport.Model
	followTail  bool
	activeJob   *jobs.Job
	activeHdr   string // "$ name\n", prefixed to the job's buffer content
	jobs        []*jobs.Job
	lastVersion uint64
	pollGen     int
	jobSeq      int

	ready bool
}

type flatRow struct {
	isHeader bool
	label    string
	cmd      config.ResolvedCommand
}

func New(groups []config.Group, warnings []string, stateDir string) Model {
	all := flattenGroups(groups)
	return Model{
		allRows:  all,
		rows:     all,
		cursor:   firstSelectable(all),
		warnings: warnings,
		stateDir: stateDir,
	}
}

// Shutdown kills any jobs still running when the program exits, so a
// background command (a dev server, a proxy) doesn't outlive the UI.
func (m Model) Shutdown() {
	for _, j := range m.jobs {
		if status, _, _ := j.Snapshot(); status == jobs.StatusRunning {
			j.Kill()
		}
	}
}

func flattenGroups(groups []config.Group) []flatRow {
	var rows []flatRow
	for _, g := range groups {
		rows = append(rows, flatRow{isHeader: true, label: g.Title})
		for _, c := range g.Commands {
			rows = append(rows, flatRow{label: c.Name, cmd: c})
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

// pollMsg carries the generation it was issued for, so a stale chain left
// over from a job the user has since switched away from dies on its next
// tick instead of polling forever alongside the current one.
type pollMsg struct{ gen int }

func pollTick(gen int) tea.Cmd {
	return tea.Tick(pollInterval, func(time.Time) tea.Msg { return pollMsg{gen: gen} })
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

	case pollMsg:
		return m.updatePoll(msg)
	}

	var cmd tea.Cmd
	m.viewport, cmd = m.viewport.Update(msg)
	return m, cmd
}

func (m Model) updatePoll(msg pollMsg) (tea.Model, tea.Cmd) {
	if msg.gen != m.pollGen || m.activeJob == nil {
		return m, nil
	}

	content, version := m.activeJob.Buffer.Snapshot()
	status, exitCode, duration := m.activeJob.Snapshot()

	if version != m.lastVersion {
		m.lastVersion = version
		body := m.activeHdr + content
		if status != jobs.StatusRunning {
			body += fmt.Sprintf("\n[%s, exit %d, %s]", status, exitCode, duration.Round(10*time.Millisecond))
		}
		m.viewport.SetContent(body)
		if m.followTail {
			m.viewport.GotoBottom()
		}
	}

	if status == jobs.StatusRunning {
		return m, pollTick(m.pollGen)
	}
	return m, nil
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
	case "enter":
		if m.focus == focusList && m.cursor >= 0 && !m.rows[m.cursor].isHeader {
			return m.runSelected()
		}
		return m, nil
	}

	if m.focus == focusList {
		switch msg.String() {
		case "up", "k":
			m.moveCursor(-1)
		case "down", "j":
			m.moveCursor(1)
		}
		return m, nil
	}

	var cmd tea.Cmd
	m.viewport, cmd = m.viewport.Update(msg)
	m.followTail = m.viewport.AtBottom()
	return m, cmd
}

func (m Model) runSelected() (tea.Model, tea.Cmd) {
	row := m.rows[m.cursor]
	m.jobSeq++
	id := fmt.Sprintf("%s-%d", row.cmd.Name, m.jobSeq)

	job, err := jobs.Start(id, row.cmd, nil, m.stateDir)
	if err != nil {
		m.viewport.SetContent(fmt.Sprintf("failed to start %s: %v", row.label, err))
		return m, nil
	}

	m.jobs = append(m.jobs, job)
	m.activeJob = job
	m.activeHdr = "$ " + row.label + "\n"
	m.lastVersion = 0
	m.followTail = true
	m.viewport.SetContent(m.activeHdr)
	m.pollGen++
	return m, pollTick(m.pollGen)
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

// runningJobCount counts jobs still running other than the active one,
// i.e. commands left going in the background.
func (m Model) runningJobCount() int {
	n := 0
	for _, j := range m.jobs {
		if j == m.activeJob {
			continue
		}
		if status, _, _ := j.Snapshot(); status == jobs.StatusRunning {
			n++
		}
	}
	return n
}

func (m Model) renderKeyBar() string {
	style := lipgloss.NewStyle().Foreground(colorTextDim).Width(m.width)
	if m.filtering {
		return style.Render(" type to filter  enter confirm  esc clear")
	}

	status := "idle"
	if m.activeJob != nil {
		if s, _, _ := m.activeJob.Snapshot(); s == jobs.StatusRunning {
			status = "running"
		}
	}
	if bg := m.runningJobCount(); bg > 0 {
		status = fmt.Sprintf("%s, %d background", status, bg)
	}
	return style.Render(fmt.Sprintf(" ↑/↓ select  enter run  tab focus  / filter  q quit   [%s]", status))
}
