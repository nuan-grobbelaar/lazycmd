package ask

import (
	"errors"
	"io"
	"os"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/term"

	"lazycmd/internal/safetext"
)

// fzfThreshold is the list length from which a pick goes to fzf (when
// it's installed) rather than the inline select: fzf copes better with
// thousands of lines.
const fzfThreshold = 200

// richTerminal reports whether tty is a real terminal that can draw an
// inline form (not TERM=dumb). Swappable for tests, whose fake tty
// isn't one.
var richTerminal = func(tty io.ReadWriteCloser) bool {
	f, ok := tty.(*os.File)
	return ok && term.IsTerminal(f.Fd()) && os.Getenv("TERM") != "dumb"
}

// formAsk is askForm, swappable for tests.
var formAsk = askForm

// askForm answers req with a one-field form drawn on tty, styled like
// lazycmd's own forms: a text input for ask (masked for --secret), a
// select or multi-select with / to filter for pick. Typing works at
// once; enter answers, esc or ctrl+c cancels. When it's done the form is
// replaced by one line with the question and the answer (a secret shows
// as dots), so the script's output stays tidy.
func askForm(tty io.ReadWriteCloser, req Request) (Response, error) {
	// The script's stdout is usually a pipe ($(lazycmd ask ...)), so
	// colours are worked out for the terminal the form is drawn on.
	r := lipgloss.NewRenderer(tty)
	lipgloss.SetDefaultRenderer(r)

	prompt := safetext.Line(req.Prompt)
	labels := map[string]string{}
	var opts []huh.Option[string]
	for _, o := range req.Options {
		label := o.Label
		if label == "" {
			label = o.Value
		}
		label = safetext.Line(label)
		labels[o.Value] = label
		opts = append(opts, huh.NewOption(label, o.Value))
	}
	height := 0 // huh's own height for short lists
	if len(opts) > 10 {
		height = 12
	}

	var value string
	var values []string
	var field huh.Field
	switch {
	case req.Kind == KindAsk:
		if !req.Secret {
			value = req.Default
		}
		in := huh.NewInput().Title(prompt).Value(&value)
		if req.Secret {
			in = in.EchoMode(huh.EchoModePassword)
		}
		field = in
	case req.Multi:
		ms := huh.NewMultiSelect[string]().Title(prompt).Options(opts...).Value(&values).Filterable(true)
		if height > 0 {
			ms = ms.Height(height)
		}
		field = ms
	default:
		s := huh.NewSelect[string]().Title(prompt).Options(opts...).Value(&value)
		if height > 0 {
			s = s.Height(height)
		}
		field = s
	}

	km := huh.NewDefaultKeyMap()
	km.Quit = key.NewBinding(key.WithKeys("ctrl+c", "esc"), key.WithHelp("esc", "cancel"))
	err := huh.NewForm(huh.NewGroup(field)).
		WithTheme(huh.ThemeCharm()).
		WithKeyMap(km).
		WithShowHelp(true).
		WithInput(tty).
		WithOutput(tty).
		Run()

	dim := r.NewStyle().Foreground(lipgloss.Color("#8a93a8"))
	if errors.Is(err, huh.ErrUserAborted) {
		io.WriteString(tty, dim.Render(prompt+" (cancelled)")+"\n")
		return Response{Cancelled: true}, nil
	}
	if err != nil {
		return Response{}, err
	}

	var shown string
	resp := Response{Value: value}
	switch {
	case req.Kind == KindAsk && req.Secret:
		if value == "" {
			resp.Value = req.Default
		}
		shown = strings.Repeat("•", 6)
	case req.Kind == KindAsk:
		shown = safetext.Line(value)
	case req.Multi:
		if len(values) == 0 {
			io.WriteString(tty, dim.Render(prompt+" (nothing picked)")+"\n")
			return Response{Cancelled: true}, nil
		}
		resp = Response{Values: values}
		parts := make([]string, len(values))
		for i, v := range values {
			parts[i] = labels[v]
		}
		shown = strings.Join(parts, ", ")
	default:
		shown = labels[value]
	}
	io.WriteString(tty, dim.Render(prompt)+" "+shown+"\n")
	return resp, nil
}
