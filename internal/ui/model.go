package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"

	"lazycast/internal/config"
	"lazycast/internal/jobs"
	"lazycast/internal/usage"
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

	warnings    []string
	stateDir    string
	projectRoot string
	usageLog    *usage.Log

	viewport    viewport.Model
	followTail  bool
	activeJob   *jobs.Job
	activeHdr   string // "$ name\n", prefixed to the job's buffer content
	jobs        []*jobs.Job
	lastVersion uint64
	pollGen     int
	jobSeq      int

	activeForm *paramsForm

	inputFocus bool // raw PTY focus mode: every key but esc goes to the job

	promptActive       bool
	promptInput        textinput.Model
	promptDismissedFor string // last pending line shown/answered, so it can't re-trigger itself

	ready bool
}

type flatRow struct {
	isHeader bool
	label    string
	cmd      config.ResolvedCommand
}

func New(groups []config.Group, warnings []string, stateDir, projectRoot string) Model {
	usageLog := usage.Load(stateDir)
	now := time.Now()
	for i := range groups {
		if groups[i].Title == "Global" {
			usageLog.SortByUsage(groups[i].Commands, projectRoot, now)
		}
	}

	all := flattenGroups(groups)
	return Model{
		allRows:     all,
		rows:        all,
		cursor:      firstSelectable(all),
		warnings:    warnings,
		stateDir:    stateDir,
		projectRoot: projectRoot,
		usageLog:    usageLog,
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
	if wsMsg, ok := msg.(tea.WindowSizeMsg); ok {
		m.width, m.height = wsMsg.Width, wsMsg.Height
		m.layout()
		m.ready = true
		if m.activeForm != nil {
			return m.updateActiveForm(msg)
		}
		return m, nil
	}

	if m.activeForm != nil {
		return m.updateActiveForm(msg)
	}

	switch msg := msg.(type) {
	case tea.KeyMsg:
		if m.promptActive {
			return m.updatePromptPopup(msg)
		}
		if m.inputFocus {
			return m.updateInputFocus(msg)
		}
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

// updateActiveForm forwards every message to the open params form. huh
// drives async field behavior (a slow options_cmd, its loading spinner)
// through its own internal message types, not just key presses, so
// everything has to pass through here while a form is open, not just
// tea.KeyMsg.
func (m Model) updateActiveForm(msg tea.Msg) (tea.Model, tea.Cmd) {
	updated, cmd := m.activeForm.form.Update(msg)
	m.activeForm.form = updated.(*huh.Form)

	switch m.activeForm.form.State {
	case huh.StateCompleted:
		cmdSpec := m.activeForm.cmd
		values := m.activeForm.values()
		m.activeForm = nil
		return m.startJob(cmdSpec, values)
	case huh.StateAborted:
		m.activeForm = nil
		return m, nil
	}
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

	var cmds []tea.Cmd
	if status == jobs.StatusRunning {
		if !m.promptActive && !m.inputFocus {
			if cmd := m.maybeOpenPrompt(); cmd != nil {
				cmds = append(cmds, cmd)
			}
		}
		cmds = append(cmds, pollTick(m.pollGen))
	}
	return m, tea.Batch(cmds...)
}

// maybeOpenPrompt checks the active job's unfinished line against the
// "looks like a prompt" heuristic. Only the job currently shown in the
// output panel is ever checked - a background job doesn't steal focus,
// it just doesn't get this treatment, since only the displayed job's
// buffer is polled at all.
func (m *Model) maybeOpenPrompt() tea.Cmd {
	pending, lastWrite := m.activeJob.Buffer.PendingLine()
	if !looksLikePrompt(pending) || pending == m.promptDismissedFor {
		return nil
	}
	if time.Since(lastWrite) < promptPauseThreshold {
		return nil
	}

	m.promptInput = textinput.New()
	m.promptInput.Prompt = "> "
	if !m.activeJob.EchoEnabled() {
		m.promptInput.EchoMode = textinput.EchoPassword
	}
	m.promptActive = true
	return m.promptInput.Focus()
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

// updateInputFocus forwards every key except esc straight to the job's
// PTY, so the running program sees exactly what a real terminal would
// have sent it. Esc is the one reserved key that leaves the mode.
func (m Model) updateInputFocus(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if msg.Type == tea.KeyEsc {
		m.inputFocus = false
		return m, nil
	}
	if m.activeJob != nil {
		if b := keyToPTYBytes(msg); b != nil {
			m.activeJob.Write(b)
		}
	}
	return m, nil
}

func (m Model) updatePromptPopup(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEsc:
		pending, _ := m.activeJob.Buffer.PendingLine()
		m.promptDismissedFor = pending
		m.promptActive = false
		return m, nil
	case tea.KeyEnter:
		pending, _ := m.activeJob.Buffer.PendingLine()
		m.promptDismissedFor = pending
		m.promptActive = false
		if m.activeJob != nil {
			m.activeJob.Write([]byte(m.promptInput.Value() + "\r"))
		}
		return m, nil
	}
	var cmd tea.Cmd
	m.promptInput, cmd = m.promptInput.Update(msg)
	return m, cmd
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
			return m.selectRow()
		}
		return m, nil
	case "i":
		if m.focus == focusOutput && m.activeJob != nil {
			if status, _, _ := m.activeJob.Snapshot(); status == jobs.StatusRunning {
				m.inputFocus = true
			}
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

// selectRow runs the selected command directly if it takes no params,
// or opens a params form first if it does.
func (m Model) selectRow() (tea.Model, tea.Cmd) {
	cmdSpec := m.rows[m.cursor].cmd
	if len(cmdSpec.Params) == 0 {
		return m.startJob(cmdSpec, nil)
	}

	_, outputWidth, bodyHeight := m.dims()
	m.activeForm = newParamsForm(cmdSpec, m.projectRoot)
	m.activeForm.form = m.activeForm.form.WithWidth(outputWidth - 2).WithHeight(bodyHeight - 2 - previewReserveLines)
	return m, m.activeForm.form.Init()
}

func (m Model) startJob(cmdSpec config.ResolvedCommand, values map[string]string) (tea.Model, tea.Cmd) {
	now := time.Now()
	m.usageLog.Record(m.projectRoot, cmdSpec.ID, now)
	_ = m.usageLog.Save(m.stateDir) // best effort; a dropped usage tick isn't worth surfacing

	m.jobSeq++
	id := fmt.Sprintf("%s-%d", cmdSpec.Name, m.jobSeq)

	job, err := jobs.Start(id, cmdSpec, values, m.stateDir)
	if err != nil {
		m.viewport.SetContent(fmt.Sprintf("failed to start %s: %v", cmdSpec.Name, err))
		return m, nil
	}

	m.jobs = append(m.jobs, job)
	m.activeJob = job
	m.activeHdr = "$ " + cmdSpec.Name + "\n"
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
	listWidthFrac       = 0.32
	keyBarHeight        = 1
	previewReserveLines = 2 // preview line + blank separator, above the form
)

// dims returns the list panel width, output panel width and body height
// (all outer, border-inclusive), shared by layout, View and selectRow so
// they can't drift apart.
func (m Model) dims() (listWidth, outputWidth, bodyHeight int) {
	listWidth = int(float64(m.width) * listWidthFrac)
	outputWidth = m.width - listWidth
	bodyHeight = m.height - keyBarHeight
	return
}

func (m *Model) layout() {
	_, outputWidth, bodyHeight := m.dims()
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

	listWidth, outputWidth, bodyHeight := m.dims()

	listPanel := renderPanel("[1] Commands", m.renderList(), listWidth, bodyHeight, m.focus == focusList)

	outputTitle := "[2] Output"
	outputBody := m.viewport.View()
	outputFocused := m.focus == focusOutput

	switch {
	case m.activeForm != nil:
		outputTitle = "[2] Params: " + m.activeForm.cmd.Name
		previewStyle := lipgloss.NewStyle().Foreground(colorTextDim)
		outputBody = previewStyle.Render(m.activeForm.preview()) + "\n\n" + m.activeForm.form.View()
		outputFocused = true
	case m.promptActive:
		// viewport.View() always pads to exactly its set Height, so
		// appending the prompt input after it would get clipped by the
		// panel's fixed line budget (same trap as the params preview).
		// Swapping in the last line instead can't overflow.
		outputTitle = "[2] Input requested"
		lines := strings.Split(m.viewport.View(), "\n")
		if n := len(lines); n > 0 {
			lines[n-1] = m.promptInput.View()
		}
		outputBody = strings.Join(lines, "\n")
		outputFocused = true
	case m.inputFocus:
		outputTitle = "[2] Output (input mode)"
		outputFocused = true
	}
	outputPanel := renderPanel(outputTitle, outputBody, outputWidth, bodyHeight, outputFocused)

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
	switch {
	case m.activeForm != nil:
		return style.Render(" tab/shift+tab move  enter next  esc cancel")
	case m.promptActive:
		return style.Render(" type your answer  enter send  esc dismiss")
	case m.inputFocus:
		return style.Render(" input mode: keys go straight to the process  esc leave")
	case m.filtering:
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
	hint := " ↑/↓ select  enter run  tab focus  / filter  q quit   [%s]"
	if m.focus == focusOutput && m.activeJob != nil {
		hint = " ↑/↓ select  enter run  tab focus  i input  / filter  q quit   [%s]"
	}
	return style.Render(fmt.Sprintf(hint, status))
}
