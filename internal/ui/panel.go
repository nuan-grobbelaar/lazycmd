package ui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

var (
	colorAccent   = lipgloss.Color("63")  // focused border / highlight
	colorMuted    = lipgloss.Color("240") // unfocused border
	colorTextDim  = lipgloss.Color("244")
	colorTextSoft = lipgloss.Color("252")

	titleFocusedStyle   = lipgloss.NewStyle().Bold(true).Foreground(colorAccent)
	titleUnfocusedStyle = lipgloss.NewStyle().Bold(true).Foreground(colorTextDim)
)

// renderPanel draws a box with the title cut into the top border, e.g.
// "╭─ [1] Commands ───────╮", since lipgloss has no built-in support for
// this. width/height are the box's outer dimensions, including borders.
func renderPanel(title, body string, width, height int, focused bool) string {
	borderColor := colorMuted
	titleStyle := titleUnfocusedStyle
	if focused {
		borderColor = colorAccent
		titleStyle = titleFocusedStyle
	}
	border := lipgloss.NewStyle().Foreground(borderColor)

	innerWidth := max(0, width-2)
	innerHeight := max(0, height-2)

	label := " " + title + " "
	rule := width - 3 - lipgloss.Width(label) // 3 = "╭─" (2) + "╮" (1)
	if rule < 1 {
		rule = 1
	}
	topLine := border.Render("╭─") + titleStyle.Render(label) + border.Render(strings.Repeat("─", rule)+"╮")
	bottomLine := border.Render("╰" + strings.Repeat("─", width-2) + "╯")

	lines := strings.Split(body, "\n")
	for len(lines) < innerHeight {
		lines = append(lines, "")
	}
	lines = lines[:innerHeight]

	rowStyle := lipgloss.NewStyle().Width(innerWidth)
	var b strings.Builder
	b.WriteString(topLine)
	b.WriteByte('\n')
	for _, line := range lines {
		b.WriteString(border.Render("│"))
		b.WriteString(rowStyle.Render(ansi.Truncate(line, innerWidth, "…")))
		b.WriteString(border.Render("│"))
		b.WriteByte('\n')
	}
	b.WriteString(bottomLine)
	return b.String()
}
