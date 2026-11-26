package ui

import (
	"strings"

	"github.com/charmbracelet/bubbles/help"
	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"lazycmd/internal/ask"
	"lazycmd/internal/jobs"
	"lazycmd/internal/safetext"
)

// askPopup is an open question from a job (lazycmd ask / pick).
type askPopup struct {
	job   *trackedJob
	p     *ask.Pending
	form  *huh.Form
	value *string
	multi *[]string
}

// WithAsk connects the server jobs send questions to. Without it, jobs
// get no LC_ASK_SOCK and `lazycmd ask` falls back to the terminal.
func (m Model) WithAsk(s *ask.Server) Model {
	m.askServer = s
	return m
}

// WithWarning adds a line to the startup warnings.
func (m Model) WithWarning(w string) Model {
	w = safetext.Text(w)
	m.warnings = append(m.warnings, w)
	m.extraWarnings = append(m.extraWarnings, w)
	return m
}

// askEnv is the environment a new panel job gets so it can ask
// questions, and the token to forget once it ends.
func (m Model) askEnv(jobID string) (env []string, token string) {
	if m.askServer == nil {
		return nil, ""
	}
	token = m.askServer.Register(jobID)
	return []string{ask.EnvSock + "=" + m.askServer.Path(), ask.EnvToken + "=" + token}, token
}

// collectAsks moves new questions onto their jobs, drops questions whose
// script has gone, and cancels those of jobs that have finished. Called
// on every poll tick.
func (m *Model) collectAsks() {
	if m.askServer == nil {
		return
	}
	for {
		select {
		case p := <-m.askServer.Requests:
			if tj := m.jobByID(p.JobID); tj != nil && isRunning(tj) {
				tj.asks = append(tj.asks, p)
			} else {
				p.Answer(ask.Response{Cancelled: true})
			}
			continue
		default:
		}
		break
	}
	for _, tj := range m.jobs {
		running := isRunning(tj)
		kept := tj.asks[:0]
		for _, p := range tj.asks {
			switch {
			case !running:
				p.Answer(ask.Response{Cancelled: true})
			case !p.Gone():
				kept = append(kept, p)
			}
		}
		tj.asks = kept
		if !running && tj.askToken != "" {
			m.askServer.Unregister(tj.askToken)
			tj.askToken = ""
		}
	}
	if m.askPopup != nil && (m.askPopup.p.Gone() || !isRunning(m.askPopup.job)) {
		m.askPopup = nil // the script was stopped while asking
	}
}

func (m Model) jobByID(id string) *trackedJob {
	for _, tj := range m.jobs {
		if tj.job.ID == id {
			return tj
		}
	}
	return nil
}

func isRunning(tj *trackedJob) bool {
	status, _, _ := tj.job.Snapshot()
	return status == jobs.StatusRunning
}

// modalOpen reports whether anything already has the user's attention,
// in which case a waiting question waits: it never takes focus away.
func (m Model) modalOpen() bool {
	return m.askPopup != nil || m.activeForm != nil || m.confirm != nil || m.trustPromptOpen ||
		m.shadowConfirm != nil || m.promptActive || m.helpActive || m.inputFocus || m.filtering
}

// maybeOpenAsk opens the shown job's oldest question, if it has one and
// nothing else is open. Questions from jobs you aren't viewing wait,
// marked ? in the Jobs panel, until you view that job.
func (m *Model) maybeOpenAsk() tea.Cmd {
	tj := m.activeJob
	if tj == nil || len(tj.asks) == 0 || m.modalOpen() {
		return nil
	}
	p := tj.asks[0]
	a := &askPopup{job: tj, p: p, value: new(string), multi: new([]string)}
	title := safetext.Line(p.Req.Prompt)
	var field huh.Field
	switch {
	case p.Req.Kind == ask.KindAsk:
		*a.value = p.Req.Default
		in := huh.NewInput().Title(title).Value(a.value)
		if p.Req.Secret {
			in = in.EchoMode(huh.EchoModePassword)
		}
		field = in
	case p.Req.Multi:
		field = huh.NewMultiSelect[string]().Title(title).Options(askOptions(p.Req.Options)...).Value(a.multi).Filterable(true)
	default:
		field = huh.NewSelect[string]().Title(title).Options(askOptions(p.Req.Options)...).Value(a.value)
	}
	w, _ := m.formDims()
	a.form = huh.NewForm(huh.NewGroup(field)).WithShowHelp(true).WithTheme(formTheme()).WithKeyMap(escCancelKeyMap()).
		WithWidth(w - 2)
	m.askPopup = a
	return a.form.Init()
}

// askDims sizes the popup to its question: a text answer needs a few
// rows, a pick one per option (scrolling past what fits on screen).
func (m Model) askDims() (width, height, formHeight int) {
	width, maxH := m.formDims()
	rows := 2 // title + input
	if m.askPopup.p.Req.Kind == ask.KindPick {
		rows = 1 + len(m.askPopup.p.Req.Options)
	}
	formHeight = rows + 2            // + blank line and help line
	height = min(maxH, formHeight+6) // + borders, "asked by" line, gap and the cancel hint
	formHeight = max(3, height-6)
	return width, height, formHeight
}

func askOptions(opts []ask.Option) []huh.Option[string] {
	out := make([]huh.Option[string], len(opts))
	for i, o := range opts {
		label := o.Label
		if label == "" {
			label = o.Value
		}
		out[i] = huh.NewOption(safetext.Line(label), o.Value)
	}
	return out
}

// updateAskPopup forwards messages to the open question. Poll ticks keep
// going meanwhile, so jobs' output and other questions still arrive.
func (m Model) updateAskPopup(msg tea.Msg) (tea.Model, tea.Cmd) {
	if pm, ok := msg.(pollMsg); ok {
		return m.updatePoll(pm)
	}
	a := m.askPopup
	updated, cmd := a.form.Update(msg)
	a.form = updated.(*huh.Form)
	switch a.form.State {
	case huh.StateCompleted:
		resp := ask.Response{Value: *a.value}
		if a.p.Req.Multi {
			resp = ask.Response{Values: append([]string{}, *a.multi...)}
		}
		m.finishAsk(resp)
		return m, nil
	case huh.StateAborted:
		m.finishAsk(ask.Response{Cancelled: true})
		return m, nil
	}
	return m, cmd
}

func (m *Model) finishAsk(resp ask.Response) {
	a := m.askPopup
	a.p.Answer(resp)
	if len(a.job.asks) > 0 && a.job.asks[0] == a.p {
		a.job.asks = a.job.asks[1:]
	}
	m.askPopup = nil
}

func (m Model) renderAskPopup() string {
	width, height, formHeight := m.askDims()
	inner := max(1, width-2)
	a := m.askPopup
	a.form = a.form.WithWidth(inner).WithHeight(formHeight)
	dim := lipgloss.NewStyle().Foreground(colorTextDim)
	head := dim.Render("asked by the running job") + "\n\n"
	h := help.New()
	h.Styles = formTheme().Help
	h.Width = inner
	cancel := ansi.Truncate(h.ShortHelpView([]key.Binding{hint("esc", "cancel (the script sees exit 1)")}), inner, "…")
	content := head + a.form.View() + "\n" + cancel
	return renderPanel(a.job.cmd.Name+" asks", strings.TrimRight(content, "\n"), width, height, true)
}
