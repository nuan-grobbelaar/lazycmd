package ui

import (
	"errors"
	"io"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"lazycmd/internal/config"
	"lazycmd/internal/safetext"
)

// ErrCancelled is returned by RunInlineForm when the form or its confirm
// question was cancelled.
var ErrCancelled = errors.New("cancelled")

// RunInlineForm shows cmd's form in the terminal, below the prompt rather
// than on a full screen, for `lazycmd <script>`. It is the TUI's form
// (same fields, keys, checks and options_cmd handling), then the
// command's confirm question if it has one. It returns the answers. Once
// done, the form is replaced by one line saying what runs, which stays in
// the scrollback.
func RunInlineForm(cmd config.ResolvedCommand, projectDir string, in io.Reader, out io.Writer) (map[string]string, error) {
	if len(cmd.Params) == 0 && cmd.Confirm == "" {
		return map[string]string{}, nil // nothing to ask
	}
	m := &inlineForm{pf: inlineParams(cmd, projectDir)}
	if len(cmd.Params) == 0 {
		m.confirming = true // nothing to fill in: straight to the question
	}
	final, err := tea.NewProgram(m, tea.WithInput(in), tea.WithOutput(out)).Run()
	if err != nil {
		return nil, err
	}
	fm := final.(*inlineForm)
	if !fm.done {
		return nil, ErrCancelled
	}
	return fm.pf.values(), nil
}

// FormProblems lists what keeps cmd from running with its defaults as
// the answers (a required field left empty, a value not matching its
// pattern), for running without a terminal to show the form on.
func FormProblems(cmd config.ResolvedCommand, projectDir string) ([]string, map[string]string) {
	pf := inlineParams(cmd, projectDir)
	values := pf.values()
	// The form starts a select on its first option; do the same here.
	for _, p := range cmd.Params {
		if p.Type == config.ParamSelect && values[p.Name] == "" && len(p.Options) > 0 {
			values[p.Name] = p.Options[0].Value
		}
	}
	return pf.problems(), values
}

// inlineParams is newParamsForm, except that a command with no params
// gets no huh form (huh can't build one with no fields): only its
// answers (none) and preview are used then.
func inlineParams(cmd config.ResolvedCommand, projectDir string) *paramsForm {
	if len(cmd.Params) > 0 {
		return newParamsForm(cmd, projectDir)
	}
	return &paramsForm{cmd: cmd, projectDir: projectDir, cache: map[string]*optionsResult{}}
}

type inlineForm struct {
	pf            *paramsForm
	width, height int
	confirming    bool
	done          bool // answered (and confirmed): run it
	quitting      bool
}

func (m *inlineForm) Init() tea.Cmd {
	if m.confirming {
		return nil
	}
	return m.pf.form.Init()
}

func (m *inlineForm) finish(done bool) (tea.Model, tea.Cmd) {
	m.done, m.quitting = done, true
	return m, tea.Quit
}

// submit moves on from a completed form: to the confirm question, or
// straight to running.
func (m *inlineForm) submit() (tea.Model, tea.Cmd) {
	if m.pf.cmd.Confirm != "" {
		m.confirming = true
		return m, nil
	}
	return m.finish(true)
}

func (m *inlineForm) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if ws, ok := msg.(tea.WindowSizeMsg); ok {
		m.width, m.height = ws.Width, ws.Height
	}
	k, isKey := msg.(tea.KeyMsg)
	if isKey && k.String() == "ctrl+c" {
		return m.finish(false)
	}

	if m.confirming {
		if !isKey {
			return m, nil
		}
		switch k.String() {
		case "y", "Y":
			return m.finish(true)
		case "n", "N", "esc", "q":
			if len(m.pf.cmd.Params) == 0 {
				return m.finish(false)
			}
			m.confirming = false
			m.pf.form.State = huh.StateNormal
		}
		return m, nil
	}

	// The same key handling as the form in the TUI (updateActiveForm).
	if isKey {
		m.pf.notice = ""
		m.pf.syncFocus()
		if m.pf.textKey(k) {
			return m, nil
		}
		if cmd, ok := m.pf.arrowNav(k); ok {
			return m, cmd
		}
		if isRunNowKey(k) {
			if problems := m.pf.problems(); len(problems) > 0 {
				m.pf.notice = "can't run yet: " + strings.Join(problems, "; ")
				return m, nil
			}
			return m.submit()
		}
	}
	updated, cmd := m.pf.form.Update(msg)
	m.pf.form = updated.(*huh.Form)
	m.pf.syncFocus()
	switch m.pf.form.State {
	case huh.StateCompleted:
		return m.submit()
	case huh.StateAborted:
		return m.finish(false)
	}
	return m, cmd
}

func (m *inlineForm) View() string {
	dim := lipgloss.NewStyle().Foreground(colorTextDim)
	name := lipgloss.NewStyle().Foreground(colorAmber).Bold(true).Render(safetext.Line(m.pf.cmd.Name))
	preview := safetext.Line(m.pf.preview())
	width := m.width
	if width <= 0 {
		width = 80
	}
	inner := max(20, min(width, 100)-2)

	if m.quitting {
		if !m.done {
			return name + dim.Render("  cancelled") + "\n"
		}
		return name + "  " + dim.Render(ansi.Truncate(preview, max(10, width-ansi.StringWidth(m.pf.cmd.Name)-3), "…")) + "\n"
	}

	var b strings.Builder
	b.WriteString(name)
	if d := m.pf.cmd.Desc; d != "" {
		b.WriteString("  " + dim.Render(ansi.Truncate(safetext.Line(d), max(10, inner-ansi.StringWidth(m.pf.cmd.Name)-2), "…")))
	}
	b.WriteString("\n\n")

	if m.confirming {
		question := safetext.Line(config.ExpandConfirm(m.pf.cmd, m.pf.values()))
		back := "cancel"
		if len(m.pf.cmd.Params) > 0 {
			back = "back to the form"
		}
		b.WriteString(lipgloss.NewStyle().Foreground(colorYellow).Bold(true).Render(ansi.Wrap("⚠ "+question, inner, "")) + "\n")
		b.WriteString(dim.Render(ansi.Truncate(preview, inner, "…")) + "\n\n")
		b.WriteString(keyBar("y", "run it", "n/esc", back) + "\n")
		return b.String()
	}

	// The form at its natural height, as far as the terminal allows.
	lines := strings.Count(m.pf.group.Content(), "\n") + 1
	height := lines
	if m.height > 0 {
		height = min(lines, max(3, m.height-8))
	}
	m.pf.form = m.pf.form.WithWidth(inner)
	b.WriteString(m.pf.view(inner, height+lipgloss.Height(m.pf.hintLine(inner))))
	b.WriteString("\n")
	if m.pf.notice != "" {
		b.WriteString(lipgloss.NewStyle().Foreground(colorRed).Render(ansi.Wrap(m.pf.notice, inner, "")) + "\n")
	}
	b.WriteString(dim.Render(ansi.Truncate(preview, inner, "…")) + "\n")
	return b.String()
}
