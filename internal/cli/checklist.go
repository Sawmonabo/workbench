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
// Other rows, and every row once done, hold a space in the cursor column, so
// a stacked row's box lines up and the final frame keeps the live layout.
func (m *checklistModel) rows() [][]string {
	rows := make([][]string, len(m.spec.Rows))
	for i, row := range m.spec.Rows {
		row = append([]string(nil), row...)
		row[0] = " "
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

// View is the list alone; choosePlan prints the title above it first. The
// viewport only tracks the scroll position: View cuts the visible lines
// itself, so no line is padded to the width. Once done, the cursor goes and
// the frame keeps its height, which is what Bubble Tea leaves in the
// scrollback; a frame that shrinks would leave the old one's top lines. The
// visible lines plus the final newline fill at most the terminal's height.
func (m *checklistModel) View() tea.View {
	spec := m.spec
	spec.Rows = m.rows()
	var lines []string
	cursorLine := 0
	for i, block := range fitBlocks(m.width, 2, spec) {
		if i == m.cursor {
			cursorLine = len(lines)
		}
		lines = append(lines, block...)
	}
	height := len(lines)
	if m.height > 0 {
		height = max(min(len(lines), m.height-1), 1)
	}
	m.view.SetWidth(m.width)
	m.view.SetHeight(height)
	m.view.SetContent(strings.Join(lines, "\n"))
	if !m.done {
		m.view.EnsureVisible(cursorLine, 0, 0)
	}
	// The frame ends in a newline: Bubble Tea erases the frame's last line when
	// it exits, so that line must be the empty one.
	top := min(m.view.YOffset(), len(lines))
	return tea.NewView(strings.Join(lines[top:min(top+height, len(lines))], "\n") + "\n")
}
