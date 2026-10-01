package cli

import (
	"slices"
	"strings"

	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/Sawmonabo/workbench/internal/operation"
)

const (
	checklistTitle       = "[WorkBench] Effects: space toggles, enter applies, esc quits"
	checklistDescription = "Unchecked effects are remembered for this machine; apply --reset forgets them."
)

// checklistModel is the effect checklist. Every View draws the plan with
// renderPlan at the width of the last WindowSizeMsg, so a resize refits it, and
// a viewport keeps the cursor's row on screen when the lines are taller than
// the terminal. Toggling flips Checked on the model's own copy of the plan.
type checklistModel struct {
	plan          operation.Plan
	verbose       bool
	selectable    []int // indices in plan.Effects of the rows a cursor can stop on
	cursor        int   // position in selectable
	width, height int
	view          viewport.Model
	approved      bool
	done          bool
}

// newChecklist is the live list of plan's effects, with the plan's checks as
// the starting boxes.
func newChecklist(plan operation.Plan, verbose bool) *checklistModel {
	m := &checklistModel{
		plan:    plan,
		verbose: verbose,
		width:   terminalWidthDefault,
		view:    viewport.New(),
	}
	m.plan.Effects = slices.Clone(plan.Effects)
	for i, effect := range m.plan.Effects {
		if !effect.Fixed {
			m.selectable = append(m.selectable, i)
		}
	}
	return m
}

// checkedNames lists the non-fixed effects whose box is checked.
func (m *checklistModel) checkedNames() []string {
	var names []string
	for _, i := range m.selectable {
		if m.plan.Effects[i].Checked {
			names = append(names, m.plan.Effects[i].Name)
		}
	}
	return names
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
			m.cursor = min(m.cursor+1, len(m.selectable)-1)
		case "space":
			effect := &m.plan.Effects[m.selectable[m.cursor]]
			effect.Checked = !effect.Checked
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

var bold = lipgloss.NewStyle().Bold(true)

// View is the list alone; choosePlan prints the title above it first. The
// viewport only tracks the scroll position: View cuts the visible lines
// itself, so no line is padded to the width. Once done, the cursor goes and
// the frame keeps its height, which is what Bubble Tea leaves in the
// scrollback; a frame that shrinks would leave the old one's top lines. The
// visible lines plus the final newline fill at most the terminal's height.
func (m *checklistModel) View() tea.View {
	cursor := -1
	if len(m.selectable) > 0 {
		cursor = m.selectable[m.cursor]
	}
	lines, cursorLine := renderPlan(
		m.plan,
		m.width,
		planView{Cursor: cursor, Interactive: true, Done: m.done, Verbose: m.verbose},
	)
	height := len(lines)
	if m.height > 0 {
		height = max(min(len(lines), m.height-1), 1)
	}
	m.view.SetWidth(m.width)
	m.view.SetHeight(height)
	m.view.SetContent(strings.Join(lines, "\n"))
	if !m.done {
		m.view.EnsureVisible(max(cursorLine, 0), 0, 0)
	}
	// The frame ends in a newline: Bubble Tea erases the frame's last line when
	// it exits, so that line must be the empty one.
	top := min(m.view.YOffset(), len(lines))
	return tea.NewView(strings.Join(lines[top:min(top+height, len(lines))], "\n") + "\n")
}
