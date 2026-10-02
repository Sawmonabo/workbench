package cli

import (
	"cmp"
	"slices"
	"strings"

	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/Sawmonabo/workbench/internal/operation"
)

// checklistModel is the live plan. Every View draws the plan with
// buildPlanFrame at the size of the last WindowSizeMsg, on the alternate
// screen like the costs tabs, so a resize refits it and a mouse position is a
// screen position. Toggling flips Checked on the model's own copy of the plan;
// enter opens the row's page in a viewport, full screen.
type checklistModel struct {
	plan          operation.Plan
	verbose       bool
	rows          []planRow // the rows a cursor can stop on
	cursor        planRow   // the zero row when there is none
	page          *planRow  // the row shown full screen, nil in the list
	width, height int
	top           int       // the list line the window starts at
	frame         planFrame // the last frame drawn, for the mouse
	view          viewport.Model
	approved      bool
	done          bool
}

// terminalWidthDefault is the width used until the terminal reports its size.
const terminalWidthDefault = 100

// newChecklist is the live list of plan, with the plan's checks as the
// starting boxes and the cursor on the first thing new to decide, else on the
// first row.
func newChecklist(plan operation.Plan, verbose bool) *checklistModel {
	m := &checklistModel{
		plan:    plan,
		verbose: verbose,
		width:   terminalWidthDefault,
		view:    viewport.New(),
	}
	m.plan.Effects = slices.Clone(plan.Effects)
	for i, effect := range m.plan.Effects {
		if isLocked(m.plan, effect) {
			m.plan.Effects[i].Checked = true
		}
	}
	m.rows = selectableRows(m.plan)
	if len(m.rows) > 0 {
		m.cursor = m.rows[0]
	}
	for _, row := range m.rows {
		if row.Kind == rowEffect && m.plan.Effects[row.Index].New {
			m.cursor = row
			break
		}
	}
	return m
}

// checkedNames lists every effect that runs: fixed ones, parts included with
// their parent, effects with nothing to do that stay checked, and the boxes
// the owner left checked.
func (m *checklistModel) checkedNames() []string {
	var names []string
	for _, effect := range m.plan.Effects {
		if runs(m.plan, effect) {
			names = append(names, effect.Name)
		}
	}
	return names
}

// toggle flips the box under the cursor. A fixed effect and a part included
// with its parent do not move. A parent takes its parts with it: checked, they
// are included; unchecked, they start unchecked and can be ticked alone.
func (m *checklistModel) toggle() {
	if m.cursor.Kind != rowEffect {
		return
	}
	effect := &m.plan.Effects[m.cursor.Index]
	if effect.Fixed || isLocked(m.plan, *effect) {
		return
	}
	effect.Checked = !effect.Checked
	for i := range m.plan.Effects {
		if m.plan.Effects[i].Parent == effect.Name {
			m.plan.Effects[i].Checked = effect.Checked
		}
	}
	m.rows = selectableRows(m.plan)
}

// move puts the cursor step rows on, stopping at the ends.
func (m *checklistModel) move(step int) {
	if len(m.rows) == 0 {
		return
	}
	at := max(slices.Index(m.rows, m.cursor), 0)
	m.cursor = m.rows[max(min(at+step, len(m.rows)-1), 0)]
}

func (m *checklistModel) Init() tea.Cmd { return nil }

func (m *checklistModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
	case tea.KeyPressMsg:
		if m.page != nil {
			return m.updatePage(msg)
		}
		switch msg.String() {
		case "up", "k":
			m.move(-1)
		case "down", "j":
			m.move(1)
		case "pgup":
			m.move(-5)
		case "pgdown":
			m.move(5)
		case "home":
			m.move(-len(m.rows))
		case "end":
			m.move(len(m.rows))
		case "space":
			m.toggle()
		case "enter":
			if m.cursor != (planRow{}) {
				page := m.cursor
				m.page = &page
				m.view.GotoTop()
			}
		case "a":
			m.approved, m.done = true, true
			return m, tea.Quit
		case "esc", "q", "ctrl+c":
			m.done = true
			return m, tea.Quit
		}
	case tea.MouseClickMsg:
		if m.page == nil && msg.Button == tea.MouseLeft {
			m.click(msg.X, msg.Y)
		}
	case tea.MouseWheelMsg:
		if m.page != nil {
			var cmd tea.Cmd
			m.view, cmd = m.view.Update(msg)
			return m, cmd
		}
		switch msg.Button {
		case tea.MouseWheelUp:
			m.move(-1)
		case tea.MouseWheelDown:
			m.move(1)
		default:
		}
	}
	return m, nil
}

// updatePage handles a key on a full-screen page: back, quit or scroll.
func (m *checklistModel) updatePage(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q", "esc", "left", "h", "backspace":
		m.page = nil
	case "ctrl+c":
		m.done = true
		return m, tea.Quit
	default:
		var cmd tea.Cmd
		m.view, cmd = m.view.Update(msg)
		return m, cmd
	}
	return m, nil
}

// click selects the row under a click in the list; clicking the selected row
// toggles it.
func (m *checklistModel) click(x, y int) {
	if y < 0 || y >= len(m.frame.Hits) || x >= m.frame.ListWidth {
		return
	}
	row := m.frame.Hits[y]
	if !slices.Contains(m.rows, row) {
		return
	}
	if row == m.cursor {
		m.toggle()
		return
	}
	m.cursor = row
}

var bold = lipgloss.NewStyle().Bold(true)

// pageChrome is the lines around a full-screen page: title, rule and keys.
const pageChrome = 3

// pageLines is the full-screen page of a row: a file's diff, an effect's detail.
func (m *checklistModel) pageLines(row planRow) (title string, lines []string) {
	if row.Kind == rowFile {
		edit := m.plan.Edits[row.Index]
		return cmp.Or(edit.Title, homePath(edit.Path)), renderDiff(edit, m.width-2)
	}
	effect := m.plan.Effects[row.Index]
	return effectTitle(effect), renderDetail(m.plan, row, m.width-2)
}

// View is the frame of the live list, or the page of the row opened full
// screen. The frame fills the terminal, keys on the last line, so what a click
// lands on is known; once dismissed, choosePlan prints the final frame to the
// terminal itself.
func (m *checklistModel) View() tea.View {
	var content string
	if m.page != nil {
		title, lines := m.pageLines(*m.page)
		m.view.SetWidth(m.width)
		m.view.SetHeight(max(m.height-pageChrome, 1))
		m.view.SetContent("  " + strings.Join(lines, "\n  "))
		content = fit(
			faint.Render(forTerm("‹ back"))+"   "+bold.Render(forTerm(title)),
			m.width,
			false,
		) + "\n" +
			faint.Render(
				strings.Repeat(glyphs.Rule, m.width),
			) + "\n" +
			m.view.View() + "\n" +
			hintLine(
				[][2]string{{"q/esc", "back"}, {"↑/↓ pgup/pgdn", "scroll"}},
				[][2]string{{"q/esc", "back"}, {"↑/↓", "scroll"}},
				m.width,
			)
	} else {
		m.frame = buildPlanFrame(
			m.plan,
			m.width,
			planView{
				Cursor:      m.cursor,
				Interactive: true,
				Done:        m.done,
				Verbose:     m.verbose,
				Height:      m.height,
				Top:         m.top,
			},
		)
		m.top = m.frame.Top
		content = strings.Join(m.frame.Lines, "\n")
	}
	view := tea.NewView(content)
	view.AltScreen = true
	view.MouseMode = tea.MouseModeCellMotion
	return view
}
