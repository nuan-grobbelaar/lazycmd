package ui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

var (
	// Palette from the design mock (hex; terminals without truecolor
	// get lipgloss's nearest-colour downsampling).
	colorAccent   = lipgloss.Color("#8be0b0") // focus: borders, titles, success
	colorMuted    = lipgloss.Color("#363c50") // unfocused border
	colorTextDim  = lipgloss.Color("#8a93a8")
	colorTextSoft = lipgloss.Color("#d5dbe8")
	colorAmber    = lipgloss.Color("#f5c26b") // keys, brand, needs-input
	colorRed      = lipgloss.Color("#ff8294")
	colorBlue     = lipgloss.Color("#86b6ff")
	colorYellow   = lipgloss.Color("#f2e05c") // project command that replaces a global script
	colorSelBg    = lipgloss.Color("#232a3d") // selected-row background

	titleFocusedStyle   = lipgloss.NewStyle().Foreground(colorAccent)
	titleUnfocusedStyle = lipgloss.NewStyle().Foreground(colorTextDim)
)

// renderPanel draws a box with the title cut into the top border, e.g.
// "╭─ [1] Commands ───────╮", since lipgloss has no built-in support for
// this. width/height are the box's outer dimensions, including borders.
func renderPanel(title, body string, width, height int, focused bool) string {
	if focused {
		return renderPanelColored(title, body, width, height, colorAccent)
	}
	return renderPanelColored(title, body, width, height, colorMuted)
}

// renderPanelColored is renderPanel with an explicit border colour (the
// amber input popup uses it). The title takes the border colour when
// that colour is not the muted unfocused one.
func renderPanelColored(title, body string, width, height int, borderColor lipgloss.Color) string {
	titleStyle := titleFocusedStyle.Foreground(borderColor)
	if borderColor == colorMuted {
		titleStyle = titleUnfocusedStyle
	}
	border := lipgloss.NewStyle().Foreground(borderColor)

	innerWidth := max(0, width-2)
	innerHeight := max(0, height-2)

	// 3 = "╭─" (2) + "╮" (1); the label is clamped to what's left so the
	// top line is always exactly `width` wide even when the panel is
	// narrower than its own title - otherwise this line alone overflows
	// while every other line in the box is correctly sized, producing a
	// ragged block that breaks downstream composition (JoinHorizontal,
	// the overlay splice).
	label := ansi.Truncate(" "+title+" ", max(0, width-3), "")
	rule := max(0, width-3-lipgloss.Width(label))
	topLine := border.Render("╭─") + titleStyle.Render(label) + border.Render(strings.Repeat("─", rule)+"╮")
	bottomLine := border.Render("╰" + strings.Repeat("─", innerWidth) + "╯")

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

// overlayCentered splices popup over the middle of base. Bubble Tea v1
// has no overlay primitive, so this is how a modal popup actually gets
// drawn: render the full base view as if nothing were open, then paste
// the popup's own bordered box on top of it at the right coordinates.
func overlayCentered(base, popup string, screenWidth, screenHeight int) string {
	popupLines := strings.Split(popup, "\n")
	popupWidth := 0
	for _, l := range popupLines {
		if w := ansi.StringWidth(l); w > popupWidth {
			popupWidth = w
		}
	}
	x := max(0, (screenWidth-popupWidth)/2)
	y := max(0, (screenHeight-len(popupLines))/2)
	return overlay(base, popup, x, y)
}

// overlay pastes popup onto base at column x, row y. ansi.Cut keeps
// each spliced piece ANSI-aware (it won't break an escape sequence or
// miscount a wide character), which plain string slicing can't do.
func overlay(base, popup string, x, y int) string {
	baseLines := strings.Split(base, "\n")
	for i, popupLine := range strings.Split(popup, "\n") {
		row := y + i
		if row < 0 || row >= len(baseLines) {
			continue
		}
		pw := ansi.StringWidth(popupLine)
		left := ansi.Cut(baseLines[row], 0, x)
		right := ansi.Cut(baseLines[row], x+pw, 1<<20)
		baseLines[row] = left + popupLine + right
	}
	return strings.Join(baseLines, "\n")
}
