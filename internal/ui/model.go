package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"lazycmd/internal/config"
	"lazycmd/internal/jobs"
	"lazycmd/internal/usage"
)

const pollInterval = 50 * time.Millisecond

type focusZone int

const (
	focusList focusZone = iota
	focusOutput
	focusJobs
)

// runMode picks how a selected command actually runs: through the
// normal PTY-backed panel job, or by suspending the whole TUI and
// handing the real terminal to the process directly - the fallback for
// full-screen programs (vim, less, psql) that need one.
type runMode int

const (
	runPanel runMode = iota
	runTerminal
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
	profileName string // matched central project profile, shown in the header
	usageLog    *usage.Log

	viewport    viewport.Model
	followTail  bool
	activeJob   *trackedJob
	activeHdr   string // "$ name\n", prefixed to the job's buffer content
	jobs        []*trackedJob
	jobsCursor  int
	lastVersion uint64
	polling     bool // whether a poll tick chain is currently in flight
	jobSeq      int

	activeForm     *paramsForm
	pendingRunMode runMode // which way to run cmdSpec once an open params form completes

	inputFocus bool // raw PTY focus mode: every key but esc goes to the job

	helpActive bool

	promptActive bool
	promptInput  textinput.Model

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

// WithProfile records the name of the project profile that matched this
// repo, for the header. Empty means none.
func (m Model) WithProfile(name string) Model {
	m.profileName = name
	return m
}

// Shutdown kills any jobs still running when the program exits, so a
// background command (a dev server, a proxy) doesn't outlive the UI.
func (m Model) Shutdown() {
	for _, tj := range m.jobs {
		if status, _, _ := tj.job.Snapshot(); status == jobs.StatusRunning {
			tj.job.Kill()
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

type pollMsg struct{}

func pollTick() tea.Cmd {
	return tea.Tick(pollInterval, func(time.Time) tea.Msg { return pollMsg{} })
}

// ensurePolling starts the poll loop if it isn't already running. Safe
// to call unconditionally - there's never more than one chain alive.
func (m *Model) ensurePolling() tea.Cmd {
	if m.polling {
		return nil
	}
	m.polling = true
	return pollTick()
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if wsMsg, ok := msg.(tea.WindowSizeMsg); ok {
		m.width, m.height = wsMsg.Width, wsMsg.Height
		m.layout()
		m.ready = true
		if m.activeForm != nil {
			// A form caches the width/height given to it at creation and
			// never re-measures on its own, so a resize while one's open
			// has to push the new size in explicitly - otherwise every
			// line gets padded to the stale width and then truncated
			// (with a spurious "…") back down to the modal's real one.
			w, h := m.formDims()
			m.activeForm.form = m.activeForm.form.WithWidth(w - 2).WithHeight(m.formFieldsHeight(h))
			return m.updateActiveForm(msg)
		}
		return m, nil
	}

	if m.activeForm != nil {
		return m.updateActiveForm(msg)
	}

	switch msg := msg.(type) {
	case tea.KeyMsg:
		if m.helpActive {
			m.helpActive = false
			return m, nil
		}
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

	case terminalExitedMsg:
		text := "ran " + msg.name + " in a full terminal"
		if msg.err != nil {
			text += "\nerror: " + msg.err.Error()
		}
		m.viewport.SetContent(text)
		return m, nil
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
		mode := m.pendingRunMode
		m.activeForm = nil
		m.pendingRunMode = runPanel
		if mode == runTerminal {
			return m.runInTerminal(cmdSpec, values)
		}
		return m.startJob(cmdSpec, values)
	case huh.StateAborted:
		m.activeForm = nil
		m.pendingRunMode = runPanel
		return m, nil
	}
	return m, cmd
}

// updatePoll runs on every tick while any job is running: it checks
// every running job's unfinished line for the "needs input" heuristic
// (so a background job gets a badge in the jobs panel), and refreshes
// the output panel if the currently-displayed job has new content. Only
// the active job's prompt ever pops the dialog and steals focus.
func (m Model) updatePoll(msg pollMsg) (tea.Model, tea.Cmd) {
	anyRunning := false
	for _, tj := range m.jobs {
		status, _, _ := tj.job.Snapshot()
		if status != jobs.StatusRunning {
			continue
		}
		anyRunning = true
		pending, lastWrite := tj.job.Buffer.PendingLine()
		tj.needsInput = looksLikePrompt(pending) &&
			pending != tj.dismissedForText &&
			time.Since(lastWrite) >= promptPauseThreshold
	}

	var cmds []tea.Cmd
	if m.activeJob != nil {
		_, version := m.activeJob.job.Buffer.Snapshot()
		if version != m.lastVersion {
			m.lastVersion = version
			m.viewport.SetContent(m.activeJobBody())
			if m.followTail {
				m.viewport.GotoBottom()
			}
		}
		if m.activeJob.needsInput && !m.promptActive && !m.inputFocus {
			cmds = append(cmds, m.openPromptPopup())
		}
	}

	if anyRunning {
		cmds = append(cmds, pollTick())
	} else {
		m.polling = false
	}
	return m, tea.Batch(cmds...)
}

func (m *Model) openPromptPopup() tea.Cmd {
	m.promptInput = textinput.New()
	m.promptInput.Prompt = "> "
	if !m.activeJob.job.EchoEnabled() {
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
			m.activeJob.job.Write(b)
		}
	}
	return m, nil
}

func (m Model) updatePromptPopup(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEsc:
		pending, _ := m.activeJob.job.Buffer.PendingLine()
		m.activeJob.dismissedForText = pending
		m.activeJob.needsInput = false
		m.promptActive = false
		return m, nil
	case tea.KeyEnter:
		pending, _ := m.activeJob.job.Buffer.PendingLine()
		m.activeJob.dismissedForText = pending
		m.activeJob.needsInput = false
		m.promptActive = false
		m.activeJob.job.Write([]byte(m.promptInput.Value() + "\r"))
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
	case "?":
		m.helpActive = true
		return m, nil
	case "/":
		m.filtering = true
		return m, nil
	case "esc":
		if m.filterQuery != "" {
			m.filterQuery = ""
			m.applyFilter()
		}
		return m, nil
	case "alt+1", "¡":
		m.focus = focusList
		return m, nil
	case "alt+2", "™":
		m.focus = focusJobs
		return m, nil
	case "alt+3", "£":
		m.focus = focusOutput
		return m, nil
	case "left":
		// Panels are ordered 1 Commands, 2 Jobs, 3 Output; arrows step
		// through them without wrapping.
		switch m.focus {
		case focusOutput:
			m.focus = focusJobs
		case focusJobs:
			m.focus = focusList
		}
		return m, nil
	case "right":
		switch m.focus {
		case focusList:
			m.focus = focusJobs
		case focusJobs:
			m.focus = focusOutput
		}
		return m, nil
	case "tab":
		switch m.focus {
		case focusList:
			m.focus = focusJobs
		case focusJobs:
			m.focus = focusOutput
		default:
			m.focus = focusList
		}
		return m, nil
	case "t":
		if m.focus == focusList && m.cursor >= 0 && !m.rows[m.cursor].isHeader {
			return m.selectRow(runTerminal)
		}
		return m, nil
	case "i":
		if m.focus == focusOutput && m.activeJob != nil {
			if status, _, _ := m.activeJob.job.Snapshot(); status == jobs.StatusRunning {
				m.inputFocus = true
			}
		}
		return m, nil
	}

	switch m.focus {
	case focusList:
		return m.updateListFocus(msg)
	case focusJobs:
		return m.updateJobsFocus(msg)
	default: // focusOutput
		var cmd tea.Cmd
		m.viewport, cmd = m.viewport.Update(msg)
		m.followTail = m.viewport.AtBottom()
		return m, cmd
	}
}

func (m Model) updateListFocus(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "up", "k":
		m.moveCursor(-1)
	case "down", "j":
		m.moveCursor(1)
	case "enter":
		if m.cursor >= 0 && !m.rows[m.cursor].isHeader {
			return m.selectRow(runPanel)
		}
	}
	return m, nil
}

// updateJobsFocus drives the jobs panel: switch which job's output is
// displayed, stop one, or restart it identically to how it was run the
// first time.
func (m Model) updateJobsFocus(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "up", "k":
		if m.jobsCursor > 0 {
			m.jobsCursor--
		}
	case "down", "j":
		if m.jobsCursor < len(m.jobs)-1 {
			m.jobsCursor++
		}
	case "enter":
		if m.jobsCursor >= 0 && m.jobsCursor < len(m.jobs) {
			m.switchToJob(m.jobs[m.jobsCursor])
		}
	case "x":
		if m.jobsCursor >= 0 && m.jobsCursor < len(m.jobs) {
			m.jobs[m.jobsCursor].job.Kill()
		}
	case "r":
		if m.jobsCursor >= 0 && m.jobsCursor < len(m.jobs) {
			tj := m.jobs[m.jobsCursor]
			return m.startJob(tj.cmd, tj.values)
		}
	}
	return m, nil
}

// selectRow runs the selected command directly if it takes no params,
// or opens a params form first if it does. mode picks whether the
// eventual run goes through the normal panel job or hands the real
// terminal to the process (for vim, less, psql - anything full-screen).
func (m Model) selectRow(mode runMode) (tea.Model, tea.Cmd) {
	cmdSpec := m.rows[m.cursor].cmd
	if len(cmdSpec.Params) == 0 {
		if mode == runTerminal {
			return m.runInTerminal(cmdSpec, nil)
		}
		return m.startJob(cmdSpec, nil)
	}

	m.pendingRunMode = mode
	w, h := m.formDims()
	m.activeForm = newParamsForm(cmdSpec, m.projectRoot)
	m.activeForm.form = m.activeForm.form.WithWidth(w - 2).WithHeight(m.formFieldsHeight(h))
	return m, m.activeForm.form.Init()
}

// terminalExitedMsg reports back once a tea.ExecProcess run (the real
// terminal was handed to the process directly) returns control.
type terminalExitedMsg struct {
	name string
	err  error
}

func (m Model) runInTerminal(cmdSpec config.ResolvedCommand, values map[string]string) (tea.Model, tea.Cmd) {
	m.usageLog.Record(m.projectRoot, cmdSpec.ID, time.Now())
	_ = m.usageLog.Save(m.stateDir)

	execCmd, err := jobs.BuildCmd(cmdSpec, values)
	if err != nil {
		m.viewport.SetContent(fmt.Sprintf("failed to start %s: %v", cmdSpec.Name, err))
		return m, nil
	}
	return m, tea.ExecProcess(execCmd, func(err error) tea.Msg {
		return terminalExitedMsg{name: cmdSpec.Name, err: err}
	})
}

func (m Model) startJob(cmdSpec config.ResolvedCommand, values map[string]string) (tea.Model, tea.Cmd) {
	now := time.Now()
	m.usageLog.Record(m.projectRoot, cmdSpec.ID, now)
	_ = m.usageLog.Save(m.stateDir) // best effort; a dropped usage tick isn't worth surfacing

	m.jobSeq++
	id := fmt.Sprintf("%s-%d", cmdSpec.Name, m.jobSeq)
	if cmdSpec.ID == config.AdhocID {
		// An ad-hoc job is named after what was typed, so the jobs panel,
		// output title and a restart all show the real command. The log
		// file name above was built from the fixed entry name instead,
		// since typed text can contain slashes.
		if typed := strings.TrimSpace(values["command"]); typed != "" {
			cmdSpec.Name = typed
		}
	}

	job, err := jobs.Start(id, cmdSpec, values, m.stateDir)
	if err != nil {
		m.viewport.SetContent(fmt.Sprintf("failed to start %s: %v", cmdSpec.Name, err))
		return m, nil
	}

	tj := &trackedJob{job: job, cmd: cmdSpec, values: values}
	m.jobs = append(m.jobs, tj)
	m.jobsCursor = len(m.jobs) - 1
	m.switchToJob(tj)
	return m, m.ensurePolling()
}

// switchToJob makes tj the one shown in the output panel, rebuilding
// the viewport from its buffer immediately rather than waiting for the
// next poll tick - which matters for a job that's already finished,
// since a finished job stops being polled at all.
func (m *Model) switchToJob(tj *trackedJob) {
	m.activeJob = tj
	m.activeHdr = "$ " + tj.cmd.Name + "\n"
	_, version := tj.job.Buffer.Snapshot()
	m.lastVersion = version
	m.followTail = true
	m.viewport.SetContent(m.activeJobBody())
	m.viewport.GotoBottom()
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
	leftWidthFrac = 0.30
	jobsHeight    = 8 // outer height of the Jobs panel (5 rows + borders + a spare)
	headerHeight  = 1
	keyBarHeight  = 1
	// outputFooterHeight is the status line inside the output panel
	// ("following · running 0:21", hints on the right).
	outputFooterHeight  = 1
	previewReserveLines = 5 // blank separator + 3-line bordered preview box + huh trailing line, below the form
)

// dims returns the left column's width, the output panel's width, the
// Jobs panel's height, and the shared body height (all outer,
// border-inclusive), used by layout and View so they can't drift apart.
// The design stacks Commands over Jobs on the left, Output on the right.
func (m Model) dims() (leftWidth, outputWidth, jobsH, bodyHeight int) {
	leftWidth = int(float64(m.width) * leftWidthFrac)
	outputWidth = m.width - leftWidth
	bodyHeight = m.height - headerHeight - keyBarHeight
	jobsH = clampInt(jobsHeight, 3, bodyHeight/2)
	return
}

// clampInt picks desired, bounded to [min, max]. If the terminal is so
// small that max itself falls below min, actual available space wins
// over the aesthetic floor - a modal that's merely cramped beats one
// that silently overflows the screen it's centered on.
func clampInt(desired, min, max int) int {
	if max < min {
		if max < 1 {
			return 1
		}
		return max
	}
	if desired < min {
		return min
	}
	if desired > max {
		return max
	}
	return desired
}

// formDims and promptDims size the two modal popups. Fixed rather than
// derived from field count or content length - simpler, and a command
// with fewer fields than the modal has room for just leaves blank
// space at the bottom, which reads fine for a dialog.
func (m Model) formDims() (width, height int) {
	width = clampInt(70, 40, m.width-6)
	height = clampInt(30, 10, m.height-4)
	return
}

// formFieldsHeight is the height left for the huh form inside a modal
// of outer height h, after the borders, the preview box beneath it and
// the description (plus its blank line) above it.
func (m Model) formFieldsHeight(h int) int {
	reserve := 2 + previewReserveLines
	if m.activeForm != nil && m.activeForm.cmd.Desc != "" {
		reserve += 2
	}
	return max(1, h-reserve)
}

func (m Model) promptDims() (width, height int) {
	width = clampInt(60, 30, m.width-6)
	return width, 7
}

func (m *Model) layout() {
	_, outputWidth, _, bodyHeight := m.dims()
	followed := m.followTail
	m.viewport = viewport.New(outputWidth-2, max(1, bodyHeight-2-outputFooterHeight))
	m.viewport.SetContent(m.activeJobBody())
	// viewport.New always starts at the top; a resize shouldn't silently
	// stop following a job that was still being followed.
	if followed {
		m.viewport.GotoBottom()
	}
}

const welcomeText = "Select a command on the left and press enter.\n\nOutput streams here."

func (m Model) initialOutputText() string {
	if len(m.warnings) == 0 {
		return welcomeText
	}
	return "Config warnings:\n\n" + strings.Join(m.warnings, "\n")
}

// activeJobBody is what the output panel shows for the active job: its
// header, buffered output so far, and a trailing status line once it's
// no longer running. Used both by the poll tick and by layout, so a
// terminal resize doesn't wipe a still-displayed job back to the
// welcome text (that content lives in the job's own buffer, not in the
// viewport, precisely so a resize can rebuild it).
func (m Model) activeJobBody() string {
	if m.activeJob == nil {
		return m.initialOutputText()
	}
	content, _ := m.activeJob.job.Buffer.Snapshot()
	status, exitCode, duration := m.activeJob.job.Snapshot()
	body := m.activeHdr + content
	if status != jobs.StatusRunning {
		body += fmt.Sprintf("\n[%s, exit %d, %s]", status, exitCode, duration.Round(10*time.Millisecond))
	}
	return body
}

func (m Model) View() string {
	if !m.ready {
		return "starting lazycmd..."
	}
	if m.helpActive {
		return m.renderHelp()
	}

	base := m.renderBase()
	switch {
	case m.activeForm != nil:
		return overlayCentered(base, m.renderFormModal(), m.width, m.height)
	case m.promptActive:
		return overlayCentered(base, m.renderPromptModal(), m.width, m.height)
	}
	return base
}

func (m Model) renderBase() string {
	leftWidth, outputWidth, jobsH, bodyHeight := m.dims()

	cmdPanel := renderPanel("[1] Commands", m.renderList(leftWidth-2), leftWidth, bodyHeight-jobsH, m.focus == focusList)
	jobsPanel := renderPanel("[2] Jobs", m.renderJobsList(leftWidth-2), leftWidth, jobsH, m.focus == focusJobs)
	left := lipgloss.JoinVertical(lipgloss.Left, cmdPanel, jobsPanel)

	outputTitle := "[3] Output"
	if m.activeJob != nil {
		outputTitle += " · " + m.activeJob.job.Name
	}
	if m.inputFocus {
		outputTitle += " (input mode)"
	}
	outInner := outputWidth - 2
	outBody := m.viewport.View() + "\n" + m.renderOutputFooter(outInner)
	outputPanel := renderPanel(outputTitle, outBody, outputWidth, bodyHeight, m.focus == focusOutput)

	body := lipgloss.JoinHorizontal(lipgloss.Top, left, outputPanel)
	return lipgloss.JoinVertical(lipgloss.Left, m.renderHeader(), body, m.renderKeyBar())
}

// spread lays left and right out on one line of the given width, right
// aligned, truncating left first if they don't both fit.
func spread(left, right string, width int) string {
	rw := lipgloss.Width(right)
	if rw >= width {
		return ansi.Truncate(right, width, "")
	}
	left = ansi.Truncate(left, width-rw-1, "…")
	gap := width - lipgloss.Width(left) - rw
	return left + strings.Repeat(" ", max(0, gap)) + right
}

// gitBranch reads the current branch from .git/HEAD without invoking
// git (nothing executes just because lazycmd opened a repo). Empty
// when the directory isn't a checkout or HEAD is detached.
func gitBranch(root string) string {
	data, err := os.ReadFile(filepath.Join(root, ".git", "HEAD"))
	if err != nil {
		return ""
	}
	ref := strings.TrimSpace(string(data))
	if name, ok := strings.CutPrefix(ref, "ref: refs/heads/"); ok {
		return name
	}
	return ""
}

func (m Model) renderHeader() string {
	amber := lipgloss.NewStyle().Bold(true).Foreground(colorAmber)
	dim := lipgloss.NewStyle().Foreground(colorTextDim)
	green := lipgloss.NewStyle().Foreground(colorAccent)

	path := m.projectRoot
	if home, err := os.UserHomeDir(); err == nil {
		if rel, ok := strings.CutPrefix(path, home); ok {
			path = "~" + rel
		}
	}
	left := " " + amber.Render("lazycmd") + "  " + dim.Render(path)
	if br := gitBranch(m.projectRoot); br != "" {
		left += "  " + dim.Render("branch") + " " + green.Render(br)
	}
	if m.profileName != "" {
		left += "  " + dim.Render("profile") + " " + lipgloss.NewStyle().Foreground(colorBlue).Render(m.profileName)
	}

	var feats []string
	for _, r := range m.allRows {
		if r.isHeader && strings.HasPrefix(r.label, "Discovered") {
			name := r.label
			if _, rest, ok := strings.Cut(name, "— "); ok {
				name, _, _ = strings.Cut(rest, " (")
			}
			feats = append(feats, strings.ToLower(name))
		}
	}
	right := ""
	if len(feats) > 0 {
		right = dim.Render("features: "+strings.Join(feats, " · ")) + " "
	}
	return spread(left, right, m.width)
}

// renderOutputFooter is the status line at the bottom of the output
// panel: follow/run state on the left, contextual hints on the right.
func (m Model) renderOutputFooter(width int) string {
	dim := lipgloss.NewStyle().Foreground(colorTextDim)
	if m.activeJob == nil {
		return dim.Render(" idle")
	}
	status, _, duration := m.activeJob.job.Snapshot()
	follow := "following"
	if !m.followTail {
		follow = "paused"
	}
	d := duration.Round(time.Second)
	state := fmt.Sprintf("%s %d:%02d", status, int(d.Minutes()), int(d.Seconds())%60)
	left := dim.Render(" " + follow + " · " + state)
	right := ""
	if status == jobs.StatusRunning {
		right = keyHint("i", "type into job") + dim.Render(" · ")
	}
	right += keyHint("/", "search") + dim.Render(" · ") + keyHint("y", "copy") + " "
	return spread(left, right, width)
}

func keyHint(k, desc string) string {
	return lipgloss.NewStyle().Bold(true).Foreground(colorAmber).Render(k) +
		lipgloss.NewStyle().Foreground(colorTextDim).Render(" "+desc)
}

func (m Model) renderFormModal() string {
	width, height := m.formDims()
	dim := lipgloss.NewStyle().Foreground(colorTextDim)
	inner := max(1, width-2)

	// Description and preview both wrap to the modal's width, so their
	// heights vary; the form gets whatever is left. huh pads its form to
	// the height it's given, so it must be sized from the real line
	// counts each render, or the preview box at the bottom gets clipped.
	var head string
	headLines := 0
	if d := m.activeForm.cmd.Desc; d != "" {
		head = dim.Render(ansi.Wrap(d, max(1, inner-2), "")) + "\n\n"
		headLines = strings.Count(head, "\n")
	}
	previewText := ansi.Wrap(m.activeForm.preview(), max(1, width-8), "")
	previewBox := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).BorderForeground(colorMuted).
		Padding(0, 1).Width(max(1, width-6)).
		Render(dim.Render(previewText))
	boxLines := strings.Count(previewBox, "\n") + 1

	// borders + blank line above the box + huh's trailing line
	formH := max(3, height-2-headLines-boxLines-2)
	m.activeForm.form = m.activeForm.form.WithWidth(inner).WithHeight(formH)

	content := head + m.activeForm.form.View() + "\n\n" + previewBox
	return renderPanel(m.activeForm.cmd.Name, content, width, height, true)
}

func (m Model) renderPromptModal() string {
	width, height := m.promptDims()
	pending, _ := m.activeJob.job.Buffer.PendingLine()
	dim := lipgloss.NewStyle().Foreground(colorTextDim)
	inputBox := lipgloss.NewStyle().Background(colorSelBg).Padding(0, 1).Width(max(1, width-4))
	content := lipgloss.NewStyle().Foreground(colorTextSoft).Render(pending) + "\n" +
		inputBox.Render(m.promptInput.View()) + "\n" +
		dim.Render("Detected: output paused without a newline")
	return renderPanelColored(m.activeJob.job.Name+" · waiting for input", content, width, height, colorAmber)
}

// rowMeta is the dim right-aligned annotation on a command row.
func rowMeta(c config.ResolvedCommand) string {
	switch {
	case c.Mode == config.ModeBackground:
		return "bg"
	case len(c.Params) == 1:
		return "1 param"
	case len(c.Params) > 1:
		return fmt.Sprintf("%d params", len(c.Params))
	}
	return ""
}

func (m Model) renderList(width int) string {
	headerStyle := lipgloss.NewStyle().Foreground(colorTextDim)
	plainStyle := lipgloss.NewStyle().Foreground(colorTextSoft)
	dimStyle := lipgloss.NewStyle().Foreground(colorTextDim)
	filterStyle := lipgloss.NewStyle().Foreground(colorAccent)
	selStyle := lipgloss.NewStyle().Background(colorSelBg).Foreground(lipgloss.Color("#ffffff"))

	var b strings.Builder
	if m.filtering || m.filterQuery != "" {
		b.WriteString(filterStyle.Render("/ " + m.filterQuery))
		b.WriteByte('\n')
	}

	if len(m.rows) == 0 {
		msg := "  no matches"
		if len(m.allRows) == 0 {
			msg = "  no commands found\n\n  add scripts to\n  ~/.config/lazycmd/scripts\n  or .lazycmd/scripts\n  in this project"
		}
		b.WriteString(plainStyle.Render(msg))
		return b.String()
	}

	for i, row := range m.rows {
		if i > 0 {
			b.WriteByte('\n')
		}
		switch {
		case row.isHeader:
			if i > 0 {
				b.WriteByte('\n')
			}
			b.WriteString(headerStyle.Render(" " + strings.ToUpper(row.label)))
		case i == m.cursor:
			line := spread(" ▸ "+row.label, rowMeta(row.cmd)+" ", width)
			b.WriteString(selStyle.Width(width).Render(line))
		default:
			line := spread("   "+row.label, dimStyle.Render(rowMeta(row.cmd)+" "), width)
			b.WriteString(plainStyle.Render(line))
		}
	}
	return b.String()
}

func jobIcon(tj *trackedJob, status jobs.Status) string {
	switch {
	case tj.needsInput:
		return lipgloss.NewStyle().Foreground(colorAmber).Render("?")
	case status == jobs.StatusRunning && tj.cmd.Mode == config.ModeBackground:
		return lipgloss.NewStyle().Foreground(colorAccent).Render("●")
	case status == jobs.StatusRunning:
		return lipgloss.NewStyle().Foreground(colorBlue).Render("◐")
	case status == jobs.StatusSucceeded:
		return lipgloss.NewStyle().Foreground(colorAccent).Render("✓")
	default:
		return lipgloss.NewStyle().Foreground(colorRed).Render("✗")
	}
}

func (m Model) renderJobsList(width int) string {
	if len(m.jobs) == 0 {
		return lipgloss.NewStyle().Foreground(colorTextDim).Render("  no jobs yet")
	}

	plainStyle := lipgloss.NewStyle().Foreground(colorTextSoft)
	dimStyle := lipgloss.NewStyle().Foreground(colorTextDim)
	amberStyle := lipgloss.NewStyle().Foreground(colorAmber)
	selStyle := lipgloss.NewStyle().Background(colorSelBg).Foreground(lipgloss.Color("#ffffff"))

	var b strings.Builder
	for i, tj := range m.jobs {
		if i > 0 {
			b.WriteByte('\n')
		}
		status, _, duration := tj.job.Snapshot()
		d := duration.Round(time.Second)
		detail := fmt.Sprintf("%d:%02d", int(d.Minutes()), int(d.Seconds())%60)
		if status != jobs.StatusRunning && d < time.Minute {
			detail = d.String()
		}
		if tj.cmd.Mode == config.ModeBackground && status == jobs.StatusRunning {
			detail = "bg " + detail
		}
		right := dimStyle.Render(detail + " ")
		if tj.needsInput {
			right = amberStyle.Render("needs input ")
		}
		line := spread(" "+jobIcon(tj, status)+" "+tj.cmd.Name, right, width)
		switch {
		case m.focus == focusJobs && i == m.jobsCursor:
			b.WriteString(selStyle.Width(width).Render(line))
		case tj == m.activeJob:
			b.WriteString(lipgloss.NewStyle().Bold(true).Render(line))
		default:
			b.WriteString(plainStyle.Render(line))
		}
	}
	return b.String()
}

// runningJobCount counts jobs still running other than the active one,
// i.e. commands left going in the background.
func (m Model) runningJobCount() int {
	n := 0
	for _, tj := range m.jobs {
		if tj == m.activeJob {
			continue
		}
		if status, _, _ := tj.job.Snapshot(); status == jobs.StatusRunning {
			n++
		}
	}
	return n
}

func (m Model) renderKeyBar() string {
	// Truncate rather than wrap: lipgloss's Width() would wrap an
	// over-long bar onto a second row the height math never budgets,
	// pushing the top of the screen off the alt screen.
	text := ansi.Truncate(m.keyBarText(), m.width, "…")
	return lipgloss.NewStyle().Width(m.width).Render(text)
}

// keyBar renders "key label" pairs as bold amber keys with dim labels.
func keyBar(pairs ...string) string {
	parts := make([]string, 0, len(pairs)/2)
	for i := 0; i+1 < len(pairs); i += 2 {
		parts = append(parts, keyHint(pairs[i], pairs[i+1]))
	}
	return " " + strings.Join(parts, "   ")
}

func (m Model) keyBarText() string {
	switch {
	case m.activeForm != nil:
		return keyBar("tab", "next field", "enter", "next/run", "esc", "cancel")
	case m.promptActive:
		return keyBar("enter", "send", "esc", "dismiss, job keeps waiting")
	case m.inputFocus:
		return keyBar("esc", "leave input mode") + lipgloss.NewStyle().Foreground(colorTextDim).Render("   keys go straight to the process")
	case m.filtering:
		return keyBar("enter", "confirm", "esc", "clear")
	case m.focus == focusJobs:
		return keyBar("↑/↓", "select", "enter", "view", "x", "stop", "r", "restart", "tab", "next panel", "alt+1-3", "jump", "?", "help", "q", "quit")
	}
	return keyBar("enter", "run", "t", "terminal", "/", "filter", "tab", "next panel", "alt+1-3", "jump", "?", "help", "q", "quit")
}

func (m Model) renderHelp() string {
	title := lipgloss.NewStyle().Bold(true).Foreground(colorAccent)
	key := lipgloss.NewStyle().Bold(true).Foreground(colorTextSoft).Width(18)
	desc := lipgloss.NewStyle().Foreground(colorTextDim)

	row := func(k, d string) string { return key.Render(k) + desc.Render(d) }

	sections := []string{
		title.Render("Commands list") + "\n" +
			row("↑/↓, j/k", "move selection") + "\n" +
			row("enter", "run the selected command") + "\n" +
			row("t", "run it with a full terminal (vim, less, psql...)") + "\n" +
			row("/", "filter by name") + "\n" +
			row("tab", "next panel") + "\n" +
			row("alt+1/2/3", "jump to Commands / Jobs / Output") + "\n" +
			row("←/→", "previous / next panel"),

		title.Render("Output panel") + "\n" +
			row("↑/↓, pgup/pgdn", "scroll (breaks follow-tail until you reach the bottom)") + "\n" +
			row("i", "focus mode: send keys straight to the running process") + "\n" +
			row("esc", "leave focus mode"),

		title.Render("Jobs panel") + "\n" +
			row("↑/↓, j/k", "select a job") + "\n" +
			row("enter", "view it in the output panel") + "\n" +
			row("x", "stop it") + "\n" +
			row("r", "restart it with the same params"),

		title.Render("Params form / filter / prompts") + "\n" +
			row("tab/shift+tab", "move between fields") + "\n" +
			row("enter", "confirm a field, or submit the form on the last one") + "\n" +
			row("esc", "cancel"),

		title.Render("Global") + "\n" +
			row("?", "toggle this help") + "\n" +
			row("q, ctrl+c", "quit (kills any jobs still running in the background)"),
	}

	body := lipgloss.NewStyle().
		Width(m.width-4).
		Padding(1, 2).
		Render(strings.Join(sections, "\n\n"))
	return body + "\n" + lipgloss.NewStyle().Foreground(colorTextDim).Render(" press any key to close")
}
