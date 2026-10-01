package cli

import (
	"strings"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/Sawmonabo/workbench/internal/costs"
)

// costsView is the interactive report: one tab per costs.Tools entry, the
// active tab's report in a viewport, refit at every resize, and a help line.
type costsView struct {
	tools   []costs.Tool
	reports map[string]costs.Statement // computed before the view starts; absent for a tool without a Source
	active  int
	width   int
	height  int
	view    viewport.Model
	help    help.Model
}

// tabBarHeight and helpHeight are the lines around the viewport.
const costsChrome = 2

func newCostsView(tools []costs.Tool, reports map[string]costs.Statement, active int) *costsView {
	m := &costsView{
		tools:   tools,
		reports: reports,
		active:  active,
		width:   terminalWidthDefault,
		height:  24,
		view:    viewport.New(),
		help:    help.New(),
	}
	// Faint, like every other secondary line of the report; without UTF-8 the
	// separator and arrows are ASCII.
	m.help.Styles.ShortKey, m.help.Styles.ShortDesc, m.help.Styles.ShortSeparator = faint, faint, faint
	m.help.Styles.Ellipsis = faint
	m.help.ShortSeparator = glyphs.text.Replace(" · ")
	m.help.Ellipsis = glyphs.Ellipsis
	m.render()
	return m
}

func (m *costsView) Init() tea.Cmd { return nil }

// render lays the active tab's report out again at the present size.
func (m *costsView) render() {
	tool := m.tools[m.active]
	m.view.SetWidth(m.width)
	m.view.SetHeight(max(m.height-costsChrome, 1))
	m.view.SetContent(costsBody(tool, m.reports[tool.Name], m.width))
	m.help.SetWidth(m.width)
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

// tabBar is each title padded by a space: the active one bold and underlined,
// the others faint, joined by a faint divider and clipped to the width.
func (m *costsView) tabBar() string {
	divider := faint.Render(glyphs.text.Replace("│"))
	if glyphs.Ellipsis != "…" {
		divider = faint.Render("|")
	}
	tabs := make([]string, len(m.tools))
	for i, tool := range m.tools {
		style := faint
		if i == m.active {
			style = lipgloss.NewStyle().Bold(true).Underline(true)
		}
		tabs[i] = style.Render(" " + tool.Title + " ")
	}
	return fit(strings.Join(tabs, divider), m.width, false)
}

// helpLine is the key hints, within the width.
func (m *costsView) helpLine() string {
	arrows := "↑/↓"
	if glyphs.Ellipsis != "…" {
		arrows = "up/down"
	}
	return m.help.ShortHelpView([]key.Binding{
		key.NewBinding(key.WithKeys("tab", "shift+tab"), key.WithHelp("tab/shift+tab", "switch")),
		key.NewBinding(key.WithKeys("up", "down"), key.WithHelp(arrows, "scroll")),
		key.NewBinding(key.WithKeys("q"), key.WithHelp("q", "quit")),
	})
}

func (m *costsView) View() tea.View {
	view := tea.NewView(m.tabBar() + "\n" + m.view.View() + "\n" + m.helpLine())
	view.AltScreen = true
	return view
}
