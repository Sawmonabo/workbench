package cli

import (
	"strings"

	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

const (
	checklistTitle       = "[WorkBench] Effects: space toggles, enter applies, esc quits"
	checklistDescription = "Unchecked effects are remembered for this machine; apply --reset forgets them."
)

// checklistModel is the effect checklist. Every View lays the rows out with
// fitBlocks at the width of the last WindowSizeMsg, so a resize refits them,
// and a viewport keeps the cursor's row on screen when the rows are taller
// than the terminal.
type checklistModel struct {
	spec          tableSpec // [cursor, box, name, delta, privilege, note]
	names         []string
	checked       []bool
	cursor        int
	width, height int
	view          viewport.Model
	approved      bool
	done          bool
}

// newChecklist lists the effects in rows, one effectRow-shaped row (box, name,
// delta, privilege, note) per name, with the given boxes checked.
func newChecklist(rows [][]string, names []string, checked []bool) *checklistModel {
	spec := tableSpec{Cols: effectColumns}
	for _, row := range rows {
		spec.Rows = append(spec.Rows, append([]string{""}, row...))
	}
	return &checklistModel{
		spec:    spec,
		names:   names,
		checked: checked,
		width:   terminalWidthDefault,
		view:    viewport.New(),
	}
}

// terminalWidthDefault is the width used until the terminal reports its size.
const terminalWidthDefault = 100

func (m *checklistModel) Init() tea.Cmd { return nil }

func (m *checklistModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
	case tea.KeyPressMsg:
		switch msg.String() {
		case "up", "k":
			m.cursor = max(m.cursor-1, 0)
		case "down", "j":
			m.cursor = min(m.cursor+1, len(m.names)-1)
		case "space":
			m.checked[m.cursor] = !m.checked[m.cursor]
		case "enter":
			m.approved, m.done = true, true
			return m, tea.Quit
		case "esc", "q", "ctrl+c":
			m.done = true
			return m, tea.Quit
		}
	}
	return m, nil
}

// rows is the spec's rows with the cursor and boxes of the present state.
func (m *checklistModel) rows() [][]string {
	rows := make([][]string, len(m.spec.Rows))
	for i, row := range m.spec.Rows {
		row = append([]string(nil), row...)
		if !m.done && i == m.cursor {
			row[0] = ">"
			row[2] = bold.Render(row[2])
		}
		row[1] = "[ ]"
		if m.checked[i] {
			row[1] = "[x]"
		}
		rows[i] = row
	}
	return rows
}

var bold = lipgloss.NewStyle().Bold(true)

func (m *checklistModel) View() tea.View {
	var header strings.Builder
	if !m.done {
		writeText(&header, m.width, 0, checklistTitle)
		writeText(&header, m.width, 0, checklistDescription)
	}
	spec := m.spec
	spec.Rows = m.rows()
	if m.done {
		spec.Cols = spec.Cols[1:]
		for i, row := range spec.Rows {
			spec.Rows[i] = row[1:]
		}
	}
	var lines []string
	cursorLine := 0
	for i, block := range fitBlocks(m.width, 2, spec) {
		if i == m.cursor {
			cursorLine = len(lines)
		}
		lines = append(lines, block...)
	}
	title := strings.Count(header.String(), "\n")
	m.view.SetWidth(m.width)
	m.view.SetHeight(len(lines))
	if m.height > 0 && !m.done {
		m.view.SetHeight(max(min(len(lines), m.height-title), 1))
	}
	m.view.SetContent(strings.Join(lines, "\n"))
	m.view.EnsureVisible(cursorLine, 0, 0)
	return tea.NewView(header.String() + m.view.View())
}
