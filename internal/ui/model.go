package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// group and item are placeholder data for the spike. Real discovery
// (scripts, sidecars, commands.yaml, features) lands in a later step.
type group struct {
	title string
	items []string
}

var fakeGroups = []group{
	{title: "Global", items: []string{"deploy-staging", "clean-docker", "tail-logs"}},
	{title: "This project", items: []string{"cloud-sql-proxy", "deploy"}},
	{title: "Discovered — maven (pom.xml)", items: []string{"maven: test", "maven: package"}},
	{title: "Discovered — npm (package.json)", items: []string{"npm: dev", "npm: lint"}},
}

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
	cursor        int // flat index into flattened command rows
	rows          []flatRow
	viewport      viewport.Model
	outputLines   []string
	running       bool
	ready         bool
}

type flatRow struct {
	isHeader bool
	label    string
}

func New() Model {
	return Model{
		rows: flattenGroups(fakeGroups),
	}
}

func flattenGroups(groups []group) []flatRow {
	var rows []flatRow
	for _, g := range groups {
		rows = append(rows, flatRow{isHeader: true, label: g.title})
		for _, item := range g.items {
			rows = append(rows, flatRow{label: item})
		}
	}
	return rows
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
		switch msg.String() {
		case "ctrl+c", "q":
			return m, tea.Quit
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
			if m.focus == focusList && !m.rows[m.cursor].isHeader {
				name := m.rows[m.cursor].label
				m.running = true
				m.outputLines = []string{"$ " + name}
				m.viewport.SetContent(strings.Join(m.outputLines, "\n"))
				return m, nextStreamTick(name, 0)
			}
			return m, nil
		}

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
	m.viewport.SetContent(welcomeText)
}

const welcomeText = "Select a command on the left and press enter.\n\nOutput streams here."

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

	var b strings.Builder
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
	status := "idle"
	if m.running {
		status = "running"
	}
	return style.Render(fmt.Sprintf(" ↑/↓ select  enter run  tab focus  / filter  q quit   [%s]", status))
}
