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

	"lazycmd/internal/ask"
	"lazycmd/internal/config"
	"lazycmd/internal/jobs"
	"lazycmd/internal/last"
	"lazycmd/internal/safetext"
	"lazycmd/internal/trust"
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

	warnings []string
	// extraWarnings are warnings that don't come from reading the config
	// (a socket that couldn't be set up, a trust answer that couldn't be
	// saved), so a reload keeps them.
	extraWarnings []string
	// reloader, when set, rebuilds the command list when files change.
	reloader    Reloader
	reloadNote  string // what the last reload changed, for the start text
	stateDir    string
	projectRoot string
	profileName string // matched central project profile, shown in the header
	usageLog    *usage.Log
	lastStore   *last.Store // last answers per repo and command, for . and R

	// flash is a one-line message in the key bar, cleared by the next
	// key: what . or R couldn't do.
	flash string

	viewport    viewport.Model
	followTail  bool
	search      searchState // the Output panel's search
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

	// shadowConfirm is the first-run warning for a project command that
	// replaces a global script of the same name; nil when not showing.
	shadowConfirm *shadowConfirm
	// shadowAcked holds command IDs the user already confirmed this
	// session, so cancelling the params form doesn't ask again.
	shadowAcked map[string]bool

	// pendingTrust is set while the repo's own .lazycmd/ commands are
	// held back waiting for the user to allow them; trustPromptOpen is
	// whether the prompt asking that is on screen.
	pendingTrust    *PendingTrust
	trustPromptOpen bool

	// confirm is the "really run?" question for a command with
	// confirm:, shown after its form and before anything runs.
	confirm *pendingConfirm

	askServer *ask.Server // where jobs' lazycmd ask/pick questions arrive; nil without
	askPopup  *askPopup   // the question being answered, if any

	ready bool
}

type flatRow struct {
	isHeader bool
	label    string
	cmd      config.ResolvedCommand
}

func New(groups []config.Group, warnings []string, stateDir, projectRoot string) Model {
	for i, w := range warnings {
		warnings[i] = safetext.Text(w)
	}
	m := Model{
		warnings:    warnings,
		stateDir:    stateDir,
		projectRoot: projectRoot,
		usageLog:    usage.Load(stateDir),
		lastStore:   last.Load(stateDir),
		shadowAcked: map[string]bool{},
	}
	m.setGroups(groups)
	return m
}

// setGroups (re)builds the command list from groups: cleans their text
// for display, orders Global by usage here, and reapplies the filter.
func (m *Model) setGroups(groups []config.Group) {
	sanitizeGroups(groups)
	now := time.Now()
	for i := range groups {
		if groups[i].Title == "Global" {
			m.usageLog.SortByUsage(groups[i].Commands, m.projectRoot, now)
		}
	}
	m.allRows = flattenGroups(groups)
	m.applyFilter()
}

// trustReviewID is the placeholder row standing in for the repo's
// commands while they aren't allowed; selecting it reopens the prompt.
const trustReviewID = "trust:review"

// PendingTrust describes a repo whose own .lazycmd/ commands haven't
// been allowed (or have changed since): which commands they are, the
// fingerprint to remember when the user allows them, and the full
// command list to switch to then.
type PendingTrust struct {
	State       trust.State
	Commands    []config.ResolvedCommand
	Fingerprint string
	FullGroups  []config.Group
}

// WithPendingTrust holds the repo's commands back behind the trust
// prompt, which opens straight away. The list shows one placeholder row
// in their place, so declining for now still leaves a way back.
func (m Model) WithPendingTrust(p PendingTrust) Model {
	m = m.holdBack(p)
	m.trustPromptOpen = true
	return m
}

// holdBack hides the repo's commands behind the trust prompt without
// opening it.
func (m Model) holdBack(p PendingTrust) Model {
	m.pendingTrust = &p

	review := config.Group{Title: "This project", Commands: []config.ResolvedCommand{{
		ID:   trustReviewID,
		Name: fmt.Sprintf("%d not allowed: review…", len(p.Commands)),
	}}}
	// The placeholder goes where the repo's commands would be: at the
	// top of "This project" (whose profile-included commands are the
	// user's own and stay), or as that group, after Global and before
	// the discovered groups.
	groups := groupsFromRows(m.allRows)
	at := len(groups)
	for i, g := range groups {
		if g.Title == "This project" {
			groups[i].Commands = append(review.Commands, g.Commands...)
			at = -1
			break
		}
		if strings.HasPrefix(g.Title, "Discovered") {
			at = i
			break
		}
	}
	if at >= 0 {
		groups = append(groups[:at], append([]config.Group{review}, groups[at:]...)...)
	}
	m.setGroups(groups)
	return m
}

// updateTrustPrompt handles the prompt: y allows the repo's commands
// (remembered until they change); anything else leaves them hidden for
// now. No is the default, so a stray enter can't allow them.
func (m Model) updateTrustPrompt(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "y", "Y":
		p := m.pendingTrust
		m.trustPromptOpen = false
		m.pendingTrust = nil
		store := trust.Load(m.stateDir)
		store.Allow(m.projectRoot, p.Fingerprint)
		if err := store.Save(m.stateDir); err != nil {
			w := "could not remember this repo as allowed: " + err.Error()
			m.warnings = append(m.warnings, w)
			m.extraWarnings = append(m.extraWarnings, w)
		}
		m.setGroups(p.FullGroups)
		m.syncOutput()
	case "ctrl+c":
		return m, tea.Quit
	case "n", "N", "esc", "q", "enter":
		m.trustPromptOpen = false
	}
	return m, nil
}

// renderTrustPrompt asks whether to allow the repo's own commands,
// listing each with what it runs.
func (m Model) renderTrustPrompt() string {
	p := m.pendingTrust
	width := clampInt(78, 40, m.width-6)
	inner := max(1, width-4)
	dim := lipgloss.NewStyle().Foreground(colorTextDim)
	soft := lipgloss.NewStyle().Foreground(colorTextSoft)
	yellow := lipgloss.NewStyle().Foreground(colorYellow).Bold(true)

	intro := "This repo defines commands in .lazycmd/ that you haven't allowed yet:"
	if p.State == trust.Changed {
		intro = "This repo's commands in .lazycmd/ have changed since you allowed them:"
	}

	const maxListed = 8
	nameW := 0
	for i, c := range p.Commands {
		if i < maxListed {
			nameW = max(nameW, ansi.StringWidth(safetext.Line(c.Name)))
		}
	}
	nameW = min(nameW, inner/3)
	var lines []string
	for i, c := range p.Commands {
		if i == maxListed {
			lines = append(lines, dim.Render(fmt.Sprintf("  … and %d more", len(p.Commands)-maxListed)))
			break
		}
		name := ansi.Truncate(safetext.Line(c.Name), nameW, "…")
		runs := c.Run
		if c.ScriptPath != "" {
			runs = m.relPath(c.ScriptPath)
		}
		line := "  " + name + strings.Repeat(" ", max(0, nameW-ansi.StringWidth(name))) + "  " + dim.Render(safetext.Line(runs))
		lines = append(lines, ansi.Truncate(line, inner, "…"))
	}

	content := yellow.Render("⚠ Allow this repo's commands?") + "\n\n" +
		soft.Render(ansi.Wrap(intro, inner, "")) + "\n\n" +
		strings.Join(lines, "\n") + "\n\n" +
		soft.Render(ansi.Wrap("Running one of them runs code from this repo. Allow them only if you trust it. Until you do they stay hidden; everything else works as usual.", inner, "")) + "\n\n" +
		keyBar("y", "allow (asked again if they change)", "n/esc", "not now")
	height := strings.Count(content, "\n") + 3
	return renderPanelColored("Allow .lazycmd/ commands", content, width, height, colorYellow)
}

// relPath shows p relative to the repo when it's inside it, else with
// ~ for the home folder.
func (m Model) relPath(p string) string {
	if rel, err := filepath.Rel(m.projectRoot, p); err == nil && !strings.HasPrefix(rel, "..") {
		return rel
	}
	return m.tildePath(p)
}

// sanitizeGroups makes every displayed string that came from a file
// safe to draw (see package safetext): names, descriptions and
// defaults can come from an untrusted repo. IDs, run bodies, script
// paths and param names are left alone; they are never drawn as-is
// (param names are cleaned where the form shows them).
func sanitizeGroups(groups []config.Group) {
	for gi := range groups {
		g := &groups[gi]
		g.Title = safetext.Line(g.Title)
		for ci := range g.Commands {
			c := &g.Commands[ci]
			c.Name = safetext.Line(c.Name)
			c.Desc = safetext.Text(c.Desc)
			if len(c.Params) == 0 {
				continue
			}
			c.Params = append([]config.Param(nil), c.Params...)
			for pi := range c.Params {
				if c.Params[pi].Type == config.ParamText {
					c.Params[pi].Default = safetext.Text(c.Params[pi].Default)
				} else {
					c.Params[pi].Default = safetext.Line(c.Params[pi].Default)
				}
			}
		}
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

// groupsFromRows is flattenGroups in reverse.
func groupsFromRows(rows []flatRow) []config.Group {
	var groups []config.Group
	for _, r := range rows {
		if r.isHeader {
			groups = append(groups, config.Group{Title: r.label})
			continue
		}
		if len(groups) > 0 {
			last := &groups[len(groups)-1]
			last.Commands = append(last.Commands, r.cmd)
		}
	}
	return groups
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
	if m.reloader != nil {
		return reloadTick()
	}
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
	// The reload timer runs whatever is open (a form, a popup).
	switch msg := msg.(type) {
	case reloadTickMsg:
		return m, m.pollReload()
	case reloadedMsg:
		if msg.loaded != nil {
			m = m.applyLoaded(msg.loaded)
		}
		return m, reloadTick()
	}
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
	if m.askPopup != nil {
		return m.updateAskPopup(msg)
	}

	switch msg := msg.(type) {
	case tea.KeyMsg:
		if m.helpActive {
			m.helpActive = false
			return m, nil
		}
		if m.trustPromptOpen {
			return m.updateTrustPrompt(msg)
		}
		if m.confirm != nil {
			return m.updateConfirm(msg)
		}
		if m.shadowConfirm != nil {
			return m.updateShadowConfirm(msg)
		}
		if m.promptActive {
			return m.updatePromptPopup(msg)
		}
		if m.inputFocus {
			return m.updateInputFocus(msg)
		}
		if m.search.editing {
			return m.updateSearch(msg)
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
	if key, ok := msg.(tea.KeyMsg); ok {
		m.activeForm.notice = ""
		if cmd, ok := m.activeForm.arrowNav(key); ok {
			return m, cmd
		}
		if isRunNowKey(key) {
			if problems := m.activeForm.problems(); len(problems) > 0 {
				m.activeForm.notice = "can't run yet: " + strings.Join(problems, "; ")
				return m, nil
			}
			return m.submitForm()
		}
	}

	updated, cmd := m.activeForm.form.Update(msg)
	m.activeForm.form = updated.(*huh.Form)

	switch m.activeForm.form.State {
	case huh.StateCompleted:
		return m.submitForm()
	case huh.StateAborted:
		m.activeForm = nil
		m.pendingRunMode = runPanel
		return m, nil
	}
	return m, cmd
}

func requiredHint(n int) string {
	if n == 1 {
		return "1 required field empty"
	}
	return fmt.Sprintf("%d required fields empty", n)
}

// isRunNowKey is the "run with the answers so far" key. Terminals send
// shift+enter as a plain enter, which Bubble Tea v1 can't tell apart from
// enter, so it works only where the terminal is set to send something
// else for it; ctrl+j (a newline) and alt+enter work everywhere the
// terminal passes them through.
func isRunNowKey(k tea.KeyMsg) bool {
	switch k.String() {
	case "ctrl+j", "alt+enter", "shift+enter":
		return true
	}
	return false
}

// submitForm closes the open form and runs its command with the current
// answers, in the mode (panel job or real terminal) it was opened for.
func (m Model) submitForm() (tea.Model, tea.Cmd) {
	form := m.activeForm
	cmdSpec := form.cmd
	values := form.values()
	mode := m.pendingRunMode
	m.activeForm = nil
	m.pendingRunMode = runPanel
	return m.run(cmdSpec, mode, values, form)
}

// pendingConfirm is a run waiting for its confirm: question to be
// answered: what to run, how, with which answers, and the form it came
// from (nil if none), which "n" returns to with the answers kept.
type pendingConfirm struct {
	cmd    config.ResolvedCommand
	mode   runMode
	values map[string]string
	form   *paramsForm
}

// run is the one way a command starts once its answers are known: a
// command with confirm: asks first; then it runs as a panel job or with
// the real terminal.
func (m Model) run(cmdSpec config.ResolvedCommand, mode runMode, values map[string]string, form *paramsForm) (tea.Model, tea.Cmd) {
	if cmdSpec.Confirm != "" {
		m.confirm = &pendingConfirm{cmd: cmdSpec, mode: mode, values: values, form: form}
		return m, nil
	}
	return m.launch(cmdSpec, mode, values)
}

// launch starts cmdSpec now, past every check.
func (m Model) launch(cmdSpec config.ResolvedCommand, mode runMode, values map[string]string) (tea.Model, tea.Cmd) {
	if mode == runTerminal {
		return m.runInTerminal(cmdSpec, values)
	}
	return m.startJob(cmdSpec, values)
}

// updateConfirm answers the confirm question: y runs; n, esc or q goes
// back to the form with the answers kept (or just cancels when there was
// no form). enter does nothing, so a stray one can't run a deploy.
func (m Model) updateConfirm(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	pc := m.confirm
	switch msg.String() {
	case "y", "Y":
		m.confirm = nil
		return m.launch(pc.cmd, pc.mode, pc.values)
	case "ctrl+c":
		return m, tea.Quit
	case "n", "N", "esc", "q":
		m.confirm = nil
		if pc.form != nil {
			pc.form.form.State = huh.StateNormal
			m.activeForm = pc.form
			m.pendingRunMode = pc.mode
		}
	}
	return m, nil
}

func (m Model) renderConfirm() string {
	pc := m.confirm
	width := clampInt(64, 40, m.width-6)
	inner := max(1, width-4)
	dim := lipgloss.NewStyle().Foreground(colorTextDim)
	yellow := lipgloss.NewStyle().Foreground(colorYellow).Bold(true)

	question := safetext.Line(config.ExpandConfirm(pc.cmd, pc.values))
	back := "n/esc"
	backDesc := "cancel"
	if pc.form != nil {
		backDesc = "back to the form"
	}
	content := yellow.Render(ansi.Wrap("⚠ "+question, inner, "")) + "\n\n" +
		dim.Render(ansi.Wrap("enter does nothing here, so a stray one can't run it.", inner, "")) + "\n\n" +
		keyBar("y", "run it", back, backDesc)
	height := strings.Count(content, "\n") + 3
	return renderPanelColored(pc.cmd.Name+" · confirm", content, width, height, colorYellow)
}

// updatePoll runs on every tick while any job is running: it checks
// every running job's unfinished line for the "needs input" heuristic
// (so a background job gets a badge in the jobs panel), and refreshes
// the output panel if the currently-displayed job has new content. Only
// the active job's prompt ever pops the dialog and steals focus.
func (m Model) updatePoll(msg pollMsg) (tea.Model, tea.Cmd) {
	m.collectAsks()
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
			m.syncOutput()
			if m.followTail {
				m.viewport.GotoBottom()
			}
		}
		if cmd := m.maybeOpenAsk(); cmd != nil {
			cmds = append(cmds, cmd)
		} else if m.activeJob.needsInput && len(m.activeJob.asks) == 0 && !m.modalOpen() {
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
	m.flash = ""
	if m.focus == focusOutput {
		if next, ok := m.updateOutputSearchKey(msg); ok {
			return next, nil
		}
	}
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
	case ".":
		return m.runLastHere()
	case "R":
		if m.cursor >= 0 && !m.rows[m.cursor].isHeader {
			return m.rerunSelected()
		}
	}
	return m, nil
}

// runLastHere runs the last command run in this repo again, with the
// answers it was given (".", like a shell's !!).
func (m Model) runLastHere() (tea.Model, tea.Cmd) {
	id, entry, ok := m.lastStore.Last(m.projectRoot)
	if !ok {
		m.flash = "nothing run in this repo yet"
		return m, nil
	}
	cmdSpec, found := m.commandByID(id)
	if !found {
		m.flash = "the last command (" + safetext.Line(id) + ") isn't available here"
		return m, nil
	}
	return m.selectCommand(cmdSpec, runPanel, &entry)
}

// rerunSelected runs the selected command with the answers last given
// to it in this repo ("R"). With none stored it's a normal run.
func (m Model) rerunSelected() (tea.Model, tea.Cmd) {
	cmdSpec := m.rows[m.cursor].cmd
	entry, ok := m.lastStore.Get(m.projectRoot, cmdSpec.ID)
	if !ok {
		if len(cmdSpec.Params) > 0 {
			m.flash = "no earlier answers for " + cmdSpec.Name + ": starting from the defaults"
		}
		return m.selectCommand(cmdSpec, runPanel, nil)
	}
	return m.selectCommand(cmdSpec, runPanel, &entry)
}

// commandByID finds a listed command. Hidden commands (a repo's own,
// before they're allowed) aren't listed, so they can't be re-run.
func (m Model) commandByID(id string) (config.ResolvedCommand, bool) {
	for _, r := range m.allRows {
		if !r.isHeader && r.cmd.ID == id && id != trustReviewID {
			return r.cmd, true
		}
	}
	return config.ResolvedCommand{}, false
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
			return m.run(tj.cmd, runPanel, tj.values, nil)
		}
	}
	return m, nil
}

// selectRow runs the selected command directly if it takes no params,
// or opens a params form first if it does. mode picks whether the
// eventual run goes through the normal panel job or hands the real
// terminal to the process (for vim, less, psql - anything full-screen).
func (m Model) selectRow(mode runMode) (tea.Model, tea.Cmd) {
	return m.selectCommand(m.rows[m.cursor].cmd, mode, nil)
}

// selectCommand is selectRow for any command. prev, when set, holds the
// answers to run it with again (. and R); the same checks apply either
// way.
func (m Model) selectCommand(cmdSpec config.ResolvedCommand, mode runMode, prev *last.Entry) (tea.Model, tea.Cmd) {
	if cmdSpec.ID == trustReviewID {
		m.trustPromptOpen = true
		return m, nil
	}
	if cmdSpec.Terminal {
		mode = runTerminal // enter on a terminal: true command acts like t
	}
	if m.needsShadowConfirm(cmdSpec) {
		// Asked before the form opens, so nothing of the project's runs
		// (not even an options_cmd) until the user has said yes.
		m.shadowConfirm = &shadowConfirm{cmd: cmdSpec, mode: mode, prev: prev}
		return m, nil
	}
	return m.openOrRunWith(cmdSpec, mode, prev)
}

// shadowConfirm is the pending first-run warning for cmd, a project
// command that replaces a global script, and how it was asked to run.
type shadowConfirm struct {
	cmd  config.ResolvedCommand
	mode runMode
	prev *last.Entry // answers to re-run with, when it came from . or R
}

// needsShadowConfirm is true for a project command that hides a global
// script and has never run in this repo: the first time, the user
// confirms they mean the project's version and not their own.
func (m Model) needsShadowConfirm(c config.ResolvedCommand) bool {
	return c.Shadows != "" && !m.shadowAcked[c.ID] && !m.usageLog.Has(m.projectRoot, c.ID)
}

// updateShadowConfirm handles the warning: y runs the command (through
// its form, if it has one); anything else cancels. No is the default so
// a stray enter can't run it.
func (m Model) updateShadowConfirm(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	sc := m.shadowConfirm
	switch msg.String() {
	case "y", "Y":
		m.shadowConfirm = nil
		if m.shadowAcked == nil {
			m.shadowAcked = map[string]bool{}
		}
		m.shadowAcked[sc.cmd.ID] = true
		return m.openOrRunWith(sc.cmd, sc.mode, sc.prev)
	case "ctrl+c":
		return m, tea.Quit
	case "n", "N", "esc", "q", "enter":
		m.shadowConfirm = nil
	}
	return m, nil
}

// openOrRun runs cmdSpec directly if it takes no params, or opens its
// params form first.
func (m Model) openOrRun(cmdSpec config.ResolvedCommand, mode runMode) (tea.Model, tea.Cmd) {
	return m.openOrRunWith(cmdSpec, mode, nil)
}

// openOrRunWith is openOrRun with earlier answers. If they still fit the
// command they're used as is, without a form. If not (a secret to enter,
// a param added since, an option gone) the form opens filled in with
// them, saying why.
func (m Model) openOrRunWith(cmdSpec config.ResolvedCommand, mode runMode, prev *last.Entry) (tea.Model, tea.Cmd) {
	if len(cmdSpec.Params) == 0 {
		return m.run(cmdSpec, mode, nil, nil)
	}
	notice := ""
	if prev != nil {
		why := rerunBlocker(cmdSpec, prev.Values)
		if why == "" {
			values := map[string]string{}
			for _, p := range cmdSpec.Params {
				values[p.Name] = prev.Values[p.Name]
			}
			return m.run(cmdSpec, mode, values, nil)
		}
		cmdSpec = withDefaults(cmdSpec, prev.Values)
		notice = "filled in with your last answers; " + why
	}

	m.pendingRunMode = mode
	w, h := m.formDims()
	m.activeForm = newParamsForm(cmdSpec, m.projectRoot)
	m.activeForm.notice = notice
	m.activeForm.form = m.activeForm.form.WithWidth(w - 2).WithHeight(m.formFieldsHeight(h))
	return m, m.activeForm.form.Init()
}

// rerunBlocker says why earlier answers can't be used as they are, or ""
// when they can. Secrets are never stored, so a command with one always
// needs the form. Options from options_cmd can't be checked without
// running it, so those answers are trusted.
func rerunBlocker(c config.ResolvedCommand, values map[string]string) string {
	for _, p := range c.Params {
		name := safetext.Line(p.Name)
		if p.Type == config.ParamSecret {
			return "secrets aren't saved, so enter " + name
		}
		v, ok := values[p.Name]
		if !ok {
			return name + " is new since last time"
		}
		switch p.Type {
		case config.ParamSelect:
			if p.OptionsCmd == "" && len(p.Options) > 0 && !p.Options.Has(v) {
				return fmt.Sprintf("%q isn't an option for %s any more", safetext.Line(v), name)
			}
		case config.ParamMulti:
			if p.OptionsCmd == "" && len(p.Options) > 0 {
				for _, part := range strings.Split(v, ",") {
					if part = strings.TrimSpace(part); part != "" && !p.Options.Has(part) {
						return fmt.Sprintf("%q isn't an option for %s any more", safetext.Line(part), name)
					}
				}
			}
		case config.ParamBool:
			if v != "true" && v != "false" {
				return name + " needs a yes or no"
			}
		case config.ParamString, "":
			if err := stringValidator(p)(v); err != nil {
				return err.Error()
			}
		}
	}
	return ""
}

// withDefaults returns c with its params' defaults set to the given
// answers, for a form filled in with them. Secrets stay empty.
func withDefaults(c config.ResolvedCommand, values map[string]string) config.ResolvedCommand {
	c.Params = append([]config.Param(nil), c.Params...)
	for i := range c.Params {
		if v, ok := values[c.Params[i].Name]; ok && c.Params[i].Type != config.ParamSecret {
			c.Params[i].Default = v
		}
	}
	return c
}

// recordLast stores values as cmdSpec's last answers here, leaving out
// secrets. Best effort: a failed save only costs "run last" an entry.
func (m Model) recordLast(cmdSpec config.ResolvedCommand, values map[string]string) {
	var secrets []string
	for _, p := range cmdSpec.Params {
		if p.Type == config.ParamSecret {
			secrets = append(secrets, p.Name)
		}
	}
	m.lastStore.Record(m.projectRoot, cmdSpec.ID, values, secrets, time.Now())
	_ = m.lastStore.Save(m.stateDir)
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
	m.recordLast(cmdSpec, values)

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
	m.recordLast(cmdSpec, values)

	m.jobSeq++
	id := fmt.Sprintf("%s-%d", cmdSpec.Name, m.jobSeq)
	if cmdSpec.ID == config.AdhocID {
		// An ad-hoc job is named after what was typed, so the jobs panel,
		// output title and a restart all show the real command. The log
		// file name above was built from the fixed entry name instead,
		// since typed text can contain slashes.
		if typed := strings.TrimSpace(values["command"]); typed != "" {
			cmdSpec.Name = safetext.Line(typed)
		}
	}

	askEnv, askToken := m.askEnv(id)
	job, err := jobs.StartSized(id, cmdSpec, values, m.stateDir, m.viewport.Width, m.viewport.Height, askEnv...)
	if err != nil {
		m.viewport.SetContent(fmt.Sprintf("failed to start %s: %v", cmdSpec.Name, err))
		return m, nil
	}

	tj := &trackedJob{job: job, cmd: cmdSpec, values: values, askToken: askToken}
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
	m.syncOutput()
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
	m.syncOutput()
	// Running jobs get the new size too, and SIGWINCH with it.
	for _, tj := range m.jobs {
		tj.job.Resize(m.viewport.Width, m.viewport.Height)
	}
	// viewport.New always starts at the top; a resize shouldn't silently
	// stop following a job that was still being followed.
	if followed {
		m.viewport.GotoBottom()
	}
}

const welcomeText = "Select a command on the left and press enter.\n\nOutput streams here."

func (m Model) initialOutputText() string {
	text := welcomeText
	if len(m.warnings) > 0 {
		text = "Config warnings:\n\n" + strings.Join(m.warnings, "\n")
	}
	if m.reloadNote != "" {
		text = m.reloadNote + "\n\n" + text
	}
	return text
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
	case m.trustPromptOpen:
		return overlayCentered(base, m.renderTrustPrompt(), m.width, m.height)
	case m.activeForm != nil:
		return overlayCentered(base, m.renderFormModal(), m.width, m.height)
	case m.askPopup != nil:
		return overlayCentered(base, m.renderAskPopup(), m.width, m.height)
	case m.confirm != nil:
		return overlayCentered(base, m.renderConfirm(), m.width, m.height)
	case m.shadowConfirm != nil:
		return overlayCentered(base, m.renderShadowConfirm(), m.width, m.height)
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
	if st := m.searchStatus(); st != "" {
		outputTitle += " · " + st
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
	// The folder name and .git/HEAD both come from the repo, so both
	// are cleaned before they reach the terminal.
	left := " " + amber.Render("lazycmd") + "  " + dim.Render(safetext.Line(path))
	if br := gitBranch(m.projectRoot); br != "" {
		left += "  " + dim.Render("branch") + " " + green.Render(safetext.Line(br))
	}
	if m.profileName != "" {
		left += "  " + dim.Render("profile") + " " + lipgloss.NewStyle().Foreground(colorBlue).Render(safetext.Line(m.profileName))
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
	if m.search.editing {
		return spread(" "+keyHint("/", "")+lipgloss.NewStyle().Foreground(colorTextSoft).Render(m.search.query+"▏"),
			dim.Render("enter keep · esc clear")+" ", width)
	}
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
	if m.search.query != "" {
		left += dim.Render(" · /" + m.search.query)
		right = keyHint("n/N", "next/prev") + "  " + keyHint("esc", "clear") + " "
	} else if status == jobs.StatusRunning {
		right = keyHint("i", "type into job") + " "
	} else {
		right = keyHint("/", "search") + " "
	}
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
	previewText := ansi.Wrap(safetext.Text(m.activeForm.preview()), max(1, width-8), "")
	previewBox := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).BorderForeground(colorMuted).
		Padding(0, 1).Width(max(1, width-6)).
		Render(dim.Render(previewText))
	boxLines := strings.Count(previewBox, "\n") + 1

	notice := ""
	noticeLines := 0
	if m.activeForm.notice != "" {
		notice = lipgloss.NewStyle().Foreground(colorRed).Render(ansi.Wrap(m.activeForm.notice, inner-2, "")) + "\n"
		noticeLines = strings.Count(notice, "\n")
	}

	// borders + blank line above the box + huh's trailing line
	formH := max(3, height-2-headLines-boxLines-2-noticeLines)
	m.activeForm.form = m.activeForm.form.WithWidth(inner).WithHeight(formH)

	content := head + m.activeForm.view(inner, formH) + "\n" + notice + "\n" + previewBox
	return renderPanel(m.activeForm.cmd.Name, content, width, height, true)
}

func (m Model) renderPromptModal() string {
	width, height := m.promptDims()
	pending, _ := m.activeJob.job.Buffer.PendingLine()
	dim := lipgloss.NewStyle().Foreground(colorTextDim)
	inputBox := lipgloss.NewStyle().Background(colorSelBgDim).Padding(0, 1).Width(max(1, width-4))
	content := lipgloss.NewStyle().Foreground(colorTextSoft).Render(pending) + "\n" +
		inputBox.Render(m.promptInput.View()) + "\n" +
		dim.Render("Detected: output paused without a newline")
	return renderPanelColored(m.activeJob.job.Name+" · waiting for input", content, width, height, colorAmber)
}

// renderShadowConfirm draws the first-run warning for a project command
// that replaces a global script: which script it hides, and what the
// project's version actually runs.
func (m Model) renderShadowConfirm() string {
	sc := m.shadowConfirm
	width := clampInt(72, 40, m.width-6)
	inner := max(1, width-4)
	dim := lipgloss.NewStyle().Foreground(colorTextDim)
	soft := lipgloss.NewStyle().Foreground(colorTextSoft)
	yellow := lipgloss.NewStyle().Foreground(colorYellow).Bold(true)

	runs := sc.cmd.ScriptPath
	if runs == "" {
		runs = sc.cmd.Run
	}
	runs = m.tildePath(runs)
	lines := strings.Split(strings.TrimRight(safetext.Text(runs), "\n"), "\n")
	const maxRunLines = 6
	if len(lines) > maxRunLines {
		lines = append(lines[:maxRunLines], "…")
	}
	for i, l := range lines {
		lines[i] = ansi.Truncate("  "+l, inner, "…")
	}

	content := yellow.Render("⚠ This project replaces one of your global scripts") + "\n\n" +
		soft.Render(ansi.Wrap(fmt.Sprintf("%q here hides your own %s. Running it runs the project's version instead:", sc.cmd.Name, safetext.Line(m.tildePath(sc.cmd.Shadows))), inner, "")) + "\n\n" +
		soft.Render(strings.Join(lines, "\n")) + "\n\n" +
		dim.Render(ansi.Wrap("Asked only before its first run in this repo.", inner, "")) + "\n\n" +
		keyBar("y", "run it", "n/esc", "cancel")
	height := strings.Count(content, "\n") + 3
	return renderPanelColored(sc.cmd.Name+" · replaces a global script", content, width, height, colorYellow)
}

// tildePath shortens a path under the home folder to ~/..., for display.
func (m Model) tildePath(p string) string {
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		if rel, ok := strings.CutPrefix(p, home+string(filepath.Separator)); ok {
			return "~/" + rel
		}
	}
	return p
}

// isWarnRow marks rows drawn in yellow: a project command replacing a
// global script, and the placeholder for commands not yet allowed.
func isWarnRow(c config.ResolvedCommand) bool {
	return c.Shadows != "" || c.ID == trustReviewID
}

// rowMeta is the dim right-aligned annotation on a command row.
func rowMeta(c config.ResolvedCommand) string {
	var meta string
	switch {
	case len(c.Params) == 1:
		meta = "1 param"
	case len(c.Params) > 1:
		meta = fmt.Sprintf("%d params", len(c.Params))
	}
	if c.Terminal {
		// Runs with the real terminal on enter.
		meta = strings.TrimSpace(meta + " ⧉")
	}
	return meta
}

func (m Model) renderList(width int) string {
	headerStyle := lipgloss.NewStyle().Foreground(colorTextDim)
	plainStyle := lipgloss.NewStyle().Foreground(colorTextSoft)
	dimStyle := lipgloss.NewStyle().Foreground(colorTextDim)
	filterStyle := lipgloss.NewStyle().Foreground(colorAccent)
	selStyle := selectedRowStyle(m.focus == focusList)
	yellowStyle := lipgloss.NewStyle().Foreground(colorYellow)

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
			style := selStyle
			if isWarnRow(row.cmd) {
				style = style.Foreground(colorYellow)
			}
			line := spread(" ▸ "+row.label, rowMeta(row.cmd)+" ", width)
			b.WriteString(style.Width(width).Render(line))
		default:
			label := "   " + row.label
			if isWarnRow(row.cmd) {
				// Yellow: this project command replaces a global
				// script of the same name.
				label = yellowStyle.Render(label)
			}
			line := spread(label, dimStyle.Render(rowMeta(row.cmd)+" "), width)
			b.WriteString(plainStyle.Render(line))
		}
	}
	return b.String()
}

func jobIcon(tj *trackedJob, status jobs.Status) string {
	g, c := jobGlyph(tj, status)
	return lipgloss.NewStyle().Foreground(c).Render(g)
}

// jobGlyph is a job's status mark and its colour.
func jobGlyph(tj *trackedJob, status jobs.Status) (string, lipgloss.Color) {
	switch {
	case tj.needsInput || len(tj.asks) > 0:
		return "?", colorAmber
	case status == jobs.StatusRunning:
		return "◐", colorBlue
	case status == jobs.StatusSucceeded:
		return "✓", colorAccent
	default:
		return "✗", colorRed
	}
}

func (m Model) renderJobsList(width int) string {
	if len(m.jobs) == 0 {
		return lipgloss.NewStyle().Foreground(colorTextDim).Render("  no jobs yet")
	}

	plainStyle := lipgloss.NewStyle().Foreground(colorTextSoft)
	dimStyle := lipgloss.NewStyle().Foreground(colorTextDim)
	amberStyle := lipgloss.NewStyle().Foreground(colorAmber)
	selStyle := selectedRowStyle(true) // drawn only while Jobs has focus

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
		right := dimStyle.Render(detail + " ")
		if tj.needsInput {
			right = amberStyle.Render("needs input ")
		}
		line := spread(" "+jobIcon(tj, status)+" "+tj.cmd.Name, right, width)
		switch {
		case m.focus == focusJobs && i == m.jobsCursor:
			// Styled pieces would end the band at their first colour
			// reset, so lay the row out plain and colour only the mark.
			g, c := jobGlyph(tj, status)
			plainRight := detail + " "
			if tj.needsInput {
				plainRight = "needs input "
			}
			plain := spread(" "+g+" "+tj.cmd.Name, plainRight, width)
			if rest, ok := strings.CutPrefix(plain, " "+g); ok {
				b.WriteString(selStyle.Render(" ") + selStyle.Foreground(c).Render(g) + selStyle.Render(rest))
			} else {
				b.WriteString(selStyle.Width(width).Render(plain))
			}
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
		hints := []string{"←/→ tab", "field", "enter", "next/run"}
		if len(m.activeForm.problems()) == 0 {
			hints = append(hints, "alt+enter", "run now")
		} else if n := m.activeForm.requiredLeft(); n > 0 {
			hints = append(hints, "*", requiredHint(n))
		}
		return keyBar(append(hints, "esc", "cancel")...)
	case m.trustPromptOpen:
		return keyBar("y", "allow this repo's commands", "n/esc", "not now")
	case m.askPopup != nil:
		if m.askPopup.p.Req.Kind == ask.KindPick {
			return keyBar("enter", "answer", "/", "filter", "esc", "cancel (the script sees exit 1)")
		}
		return keyBar("enter", "answer", "esc", "cancel (the script sees exit 1)")
	case m.confirm != nil:
		return keyBar("y", "run it", "n/esc", "back")
	case m.shadowConfirm != nil:
		return keyBar("y", "run the project's version", "n/esc", "cancel")
	case m.promptActive:
		return keyBar("enter", "send", "esc", "dismiss, job keeps waiting")
	case m.inputFocus:
		return keyBar("esc", "leave input mode") + lipgloss.NewStyle().Foreground(colorTextDim).Render("   keys go straight to the process")
	case m.search.editing:
		return keyBar("enter", "keep the search", "esc", "clear it")
	case m.filtering:
		return keyBar("enter", "confirm", "esc", "clear")
	case m.focus == focusOutput && m.search.query != "":
		return keyBar("n/N", "next/previous match", "/", "new search", "esc", "clear", "tab", "next panel", "?", "help", "q", "quit")
	case m.focus == focusOutput:
		return keyBar("↑/↓", "scroll", "/", "search", "i", "type into job", "tab", "next panel", "alt+1-3", "jump", "?", "help", "q", "quit")
	case m.focus == focusJobs:
		return keyBar("↑/↓", "select", "enter", "view", "x", "stop", "r", "restart", "tab", "next panel", "alt+1-3", "jump", "?", "help", "q", "quit")
	}
	enterDesc := "run"
	if m.cursor >= 0 && m.cursor < len(m.rows) && m.rows[m.cursor].cmd.Terminal {
		enterDesc = "run in terminal"
	}
	if m.flash != "" {
		return lipgloss.NewStyle().Foreground(colorAmber).Render(" " + m.flash)
	}
	pairs := []string{"enter", enterDesc, "t", "terminal"}
	if id, e, ok := m.lastStore.Last(m.projectRoot); ok {
		if c, found := m.commandByID(id); found {
			pairs = append(pairs, ".", c.Name+" ("+ago(time.Since(e.At))+")")
		}
	}
	pairs = append(pairs, "R", "rerun")
	return keyBar(append(pairs, "/", "filter", "tab", "next panel", "alt+1-3", "jump", "?", "help", "q", "quit")...)
}

// ago is a short "how long ago" for the key bar.
func ago(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	}
	return fmt.Sprintf("%dd ago", int(d.Hours()/24))
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
			row("⧉ after a name", "enter runs it with the full terminal (terminal: true)") + "\n" +
			row(".", "run the last command run in this repo again, same answers") + "\n" +
			row("R", "run the selected command with the answers you last gave it") + "\n" +
			row("/", "filter by name") + "\n" +
			row("yellow name", "a project command replacing your global script of that name") + "\n" +
			row("tab", "next panel") + "\n" +
			row("alt+1/2/3", "jump to Commands / Jobs / Output") + "\n" +
			row("←/→", "previous / next panel"),

		title.Render("Output panel") + "\n" +
			row("↑/↓, pgup/pgdn", "scroll (breaks follow-tail until you reach the bottom)") + "\n" +
			row("/", "search the output (smart case: capitals make it exact)") + "\n" +
			row("n / N", "next / previous match; esc clears the search") + "\n" +
			row("i", "focus mode: send keys straight to the running process") + "\n" +
			row("esc", "leave focus mode"),

		title.Render("Jobs panel") + "\n" +
			row("↑/↓, j/k", "select a job") + "\n" +
			row("enter", "view it in the output panel") + "\n" +
			row("x", "stop it") + "\n" +
			row("r", "restart it with the same params") + "\n" +
			row("?", "the job is waiting for an answer: view it to answer"),

		title.Render("Params form / filter / prompts") + "\n" +
			row("tab/shift+tab, ←/→", "move between fields (in a text field with text, ←/→ move the cursor)") + "\n" +
			row("space", "toggle a yes/no field") + "\n" +
			row("enter", "confirm a field, or submit the form on the last one") + "\n" +
			row("alt+enter, ctrl+j", "run now, once every required field is set") + "\n" +
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
