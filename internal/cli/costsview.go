package cli

import (
	"strings"

	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/Sawmonabo/workbench/internal/costs"
)

// costsView is the interactive report: a tab bar with one tab per
// costs.Tools entry, the active tab's report in a viewport, refit at every
// resize, and a key line.
type costsView struct {
	tools   []costs.Tool
	reports map[string]costs.Statement // computed before the view starts; absent for a tool without a Source
	active  int
	width   int
	height  int
	view    viewport.Model
}

// costsChrome is the lines around the viewport: tab bar, rule and key line.
const costsChrome = 3

func newCostsView(tools []costs.Tool, reports map[string]costs.Statement, active int) *costsView {
	m := &costsView{
		tools:   tools,
		reports: reports,
		active:  active,
		width:   terminalWidthDefault,
		height:  24,
		view:    viewport.New(),
	}
	m.render()
	return m
}

func (m *costsView) Init() tea.Cmd { return nil }

// render lays the active tab's report out again at the present size, with a
// blank line above it.
func (m *costsView) render() {
	tool := m.tools[m.active]
	m.view.SetWidth(m.width)
	m.view.SetHeight(max(m.height-costsChrome, 1))
	m.view.SetContent("\n" + costsBody(tool, m.reports[tool.Name], m.width))
}

// switchTab moves by step tabs, wrapping, and starts the new tab at the top.
func (m *costsView) switchTab(step int) {
	m.active = (m.active + step + len(m.tools)) % len(m.tools)
	m.render()
	m.view.GotoTop()
}

func (m *costsView) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.render()
	case tea.KeyPressMsg:
		switch msg.String() {
		case "tab", "right", "l":
			m.switchTab(1)
		case "shift+tab", "left", "h":
			m.switchTab(-1)
		case "q", "esc", "ctrl+c":
			return m, tea.Quit
		case "home":
			m.view.GotoTop()
		case "end":
			m.view.GotoBottom()
		default:
			var cmd tea.Cmd
			m.view, cmd = m.view.Update(msg)
			return m, cmd
		}
	}
	return m, nil
}

// tabBar is the brand, then each tool: the active one a pill in its own color,
// the others faint, clipped to the width.
func (m *costsView) tabBar() string {
	parts := []string{toolPalette(m.tools[m.active].Name).accent.Bold(true).Render("[WorkBench]")}
	for i, tool := range m.tools {
		if i == m.active {
			parts = append(parts, toolPalette(tool.Name).tab.Render(tool.Title))
		} else {
			parts = append(parts, faint.Render(" "+tool.Title+" "))
		}
	}
	return fit(strings.Join(parts, " "), m.width, false)
}

// keyLine names every key: the keys in the tool's color, what they do faint.
// A terminal too narrow for every key gets the short form.
func (m *costsView) keyLine() string {
	pal := toolPalette(m.tools[m.active].Name)
	line := func(hints [][2]string) string {
		parts := make([]string, len(hints))
		for i, hint := range hints {
			parts[i] = pal.accent.Render(glyphs.text.Replace(hint[0])) + " " + faint.Render(hint[1])
		}
		return strings.Join(parts, faint.Render(glyphs.text.Replace("  ·  ")))
	}
	full := line([][2]string{
		{"←/→ tab/shift+tab", "switch tool"}, {"↑/↓ pgup/pgdn", "scroll"}, {"q", "quit"},
	})
	if lipgloss.Width(full) <= m.width {
		return full
	}
	return fit(
		line([][2]string{{"←/→", "switch"}, {"↑/↓", "scroll"}, {"q", "quit"}}),
		m.width,
		false,
	)
}

func (m *costsView) View() tea.View {
	rule := faint.Render(strings.Repeat(glyphs.Rule, m.width))
	view := tea.NewView(m.tabBar() + "\n" + rule + "\n" + m.view.View() + "\n" + m.keyLine())
	view.AltScreen = true
	return view
}
