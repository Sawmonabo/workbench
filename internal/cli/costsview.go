package cli

import (
	"strings"

	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/Sawmonabo/workbench/internal/costs"
)

// costsView is the interactive report: a tab bar with one tab per
// costs.Tools entry, the active tab's report in a viewport, refit at every
// resize, and a key line. ↑/↓ select a project or model row and enter opens
// its page; esc goes back.
type costsView struct {
	tools    []costs.Tool
	reports  map[string]costs.Statement // computed before the view starts; absent for a tool without a Source
	load     focusLoader
	active   int
	selected int
	targets  []target
	focus    *costs.Focus
	problem  string // why the last page could not open
	width    int
	height   int
	view     viewport.Model
}

// focusLoader reads one project's or model's page from the ledger.
type focusLoader func(tool costs.Tool, kind, name string) (costs.Focus, error)

// costsChrome is the lines around the viewport: tab bar, rule and key line.
const costsChrome = 3

func newCostsView(
	tools []costs.Tool,
	reports map[string]costs.Statement,
	active int,
	load focusLoader,
) *costsView {
	m := &costsView{
		tools:   tools,
		reports: reports,
		load:    load,
		active:  active,
		width:   terminalWidthDefault,
		height:  24,
		view:    viewport.New(),
	}
	m.render()
	return m
}

func (m *costsView) Init() tea.Cmd { return nil }

// render lays the active page out again at the present size, with a blank
// line above it, and keeps the selected row on screen.
func (m *costsView) render() {
	tool := m.tools[m.active]
	m.view.SetWidth(m.width)
	m.view.SetHeight(max(m.height-costsChrome, 1))
	if m.focus != nil {
		m.view.SetContent("\n" + focusBody(tool, *m.focus, m.reports[tool.Name], m.width))
		return
	}
	body, targets := costsPage(tool, m.reports[tool.Name], m.width, m.selected)
	m.targets = targets
	if m.problem != "" {
		body = yellow.Render(m.problem) + "\n" + body
	}
	m.view.SetContent("\n" + body)
	marker := glyphs.text.Replace("›")
	for i, line := range strings.Split("\n"+body, "\n") {
		if strings.HasPrefix(strings.TrimLeft(ansi.Strip(line), " "), marker) {
			m.view.EnsureVisible(i, 0, 0)
			break
		}
	}
}

// switchTab moves by step tabs, wrapping, and starts the new tab at the top
// of its report.
func (m *costsView) switchTab(step int) {
	m.active = (m.active + step + len(m.tools)) % len(m.tools)
	m.selected, m.focus, m.problem = 0, nil, ""
	m.view.GotoTop()
	m.render()
}

// open shows the selected row's page.
func (m *costsView) open() {
	if m.selected >= len(m.targets) || m.load == nil {
		return
	}
	pick := m.targets[m.selected]
	focus, err := m.load(m.tools[m.active], pick.kind, pick.name)
	if err != nil {
		m.problem = "Could not open " + shortPath(pick.name) + ": " + err.Error()
		m.render()
		return
	}
	m.focus, m.problem = &focus, ""
	m.view.GotoTop()
	m.render()
}

// back returns from a page to the report, at the row it opened.
func (m *costsView) back() {
	m.focus = nil
	m.view.GotoTop()
	m.render()
}

func (m *costsView) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.render()
	case tea.KeyPressMsg:
		key := msg.String()
		switch {
		case key == "ctrl+c", key == "q", key == "esc" && m.focus == nil:
			return m, tea.Quit
		case m.focus != nil && (key == "esc" || key == "backspace" || key == "left" || key == "h"):
			// On a page, left means back, not the next tool.
			m.back()
		case m.focus != nil && (key == "right" || key == "l"):
		case key == "tab", key == "right", key == "l":
			m.switchTab(1)
		case key == "shift+tab", key == "left", key == "h":
			m.switchTab(-1)
		case m.focus == nil && (key == "up" || key == "k"):
			m.selected = max(m.selected-1, 0)
			m.render()
		case m.focus == nil && (key == "down" || key == "j"):
			m.selected = max(min(m.selected+1, len(m.targets)-1), 0)
			m.render()
		case m.focus == nil && key == "enter":
			m.open()
		case key == "home":
			m.view.GotoTop()
		case key == "end":
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
	full, short := [][2]string{
		{"←/→ tab/shift+tab", "switch tool"},
		{"↑/↓", "select"},
		{"enter", "open"},
		{"pgup/pgdn", "scroll"},
		{"q", "quit"},
	}, [][2]string{{"←/→", "tool"}, {"↑/↓", "select"}, {"enter", "open"}, {"q", "quit"}}
	if m.focus != nil {
		full = [][2]string{
			{"← esc", "back"},
			{"↑/↓ pgup/pgdn", "scroll"},
			{"tab/shift+tab", "switch tool"},
			{"q", "quit"},
		}
		short = [][2]string{{"← esc", "back"}, {"↑/↓", "scroll"}, {"q", "quit"}}
	}
	if text := line(full); lipgloss.Width(text) <= m.width {
		return text
	}
	return fit(line(short), m.width, false)
}

func (m *costsView) View() tea.View {
	rule := faint.Render(strings.Repeat(glyphs.Rule, m.width))
	view := tea.NewView(m.tabBar() + "\n" + rule + "\n" + m.view.View() + "\n" + m.keyLine())
	view.AltScreen = true
	return view
}
