package cli

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/Sawmonabo/workbench/internal/agent/guard"
	"github.com/Sawmonabo/workbench/internal/display"
)

// guardColumns are the list's columns. COMMAND shrinks first, to 20 cells,
// then SUBAGENT, PROJECT, PEAK, AGENT and TIME drop in that order, then
// COMMAND shrinks further; ENDED never drops. TIME, how long a command ran,
// goes last of them: the details say it, and a window too narrow for it
// still shows what runs, its memory now, when it ended and what it was. ID,
// which holds a detached program's tree mark, stays.
var guardColumns = []column{
	{Head: "ID"},
	{Head: "STATUS"},
	{Head: "AGENT", Drop: 2},
	{Head: "SUBAGENT", Drop: 5},
	{Head: "TIME", Right: true, Drop: 1},
	{Head: "MEMORY", Right: true},
	{Head: "PEAK", Right: true, Drop: 3},
	{Head: "ENDED", Right: true},
	{Head: "PROJECT", Drop: 4},
	{Head: "COMMAND", Clip: true, Keep: 20},
}

// The columns a row colors: the status in its state's color, the agent in
// its hue, the memory and the peak by size.
const (
	statusColumn = 1
	agentColumn  = 2
	memoryColumn = 5
	peakColumn   = 6
)

// guardRowPrefix is the cells before a row: the selection mark, the tag mark and
// a space.
const guardRowPrefix = 3

// tableLines are the column heads and as many rows as fit in room lines,
// scrolled so the selected row shows: the window moves only when the
// selection leaves it, so the rows stay put while the selection walks. The
// columns are as wide as the widest cell of the rows each frame measures
// (see rebuild) and of every row the history holds, measured once when read,
// so a frame draws only the rows that show.
func (m *guardView) tableLines(room int) []string {
	if len(m.rows) == 0 {
		switch {
		case m.find.answer != "":
			return []string{
				faint.Render(forTerm(fmt.Sprintf("No command holds %q.", m.find.answer))),
			}
		case m.connected:
			return []string{faint.Render("No agent commands are running.")}
		}
		return nil
	}
	cols := slices.Clone(guardColumns)
	for i, width := range m.hist.widths {
		cols[i].Min = width
	}
	spec := tableSpec{Cols: cols, Header: true}
	for _, i := range m.measured {
		spec.Rows = append(spec.Rows, m.rowCells(i))
	}
	shown, widths, _ := layout(m.width, guardRowPrefix, spec)
	used := guardRowPrefix + gap*(len(shown)-1)
	for _, i := range shown {
		if !cols[i].Clip {
			used += widths[i]
		}
	}
	widths[len(cols)-1] = max(m.width-used, 1)
	heads := make([]string, len(cols))
	for i, c := range cols {
		heads[i] = c.Head
	}
	head := strings.Join(padded(cols, heads, shown, widths), strings.Repeat(" ", gap))
	lines := []string{faint.Render(strings.Repeat(" ", guardRowPrefix) + head)}
	visible := max(room-1, 1)
	m.page = visible
	m.first = min(max(m.first, m.cursor-visible+1), m.cursor, max(len(m.rows)-visible, 0))
	m.first = max(m.first, 0)
	for i := m.first; i < min(m.first+visible, len(m.rows)); i++ {
		lines = append(lines, m.styledRow(i, m.rowCells(i), shown, widths))
	}
	return lines
}

// rowCells are row i's cells: a snapshot row's as this frame makes them, a
// finished row's as made once, with the tree mark before the ID of one
// drawn under its command.
func (m *guardView) rowCells(i int) []string {
	r := m.rows[i]
	if r.cells == nil {
		return m.cells(*r.view)
	}
	mark := m.branch[r.view.ID]
	if mark == "" {
		return r.cells
	}
	cells := slices.Clone(r.cells)
	cells[0] = forTerm(mark) + cells[0]
	return cells
}

// cells are a snapshot row's cells in the list: short ID, behind its tree
// mark when it is drawn under its command, status, agent, subagent, run
// time, memory now, peak, when it ended, project and command on one line.
func (m *guardView) cells(c guard.CommandView) []string {
	now, ended := "—", ""
	if active(c) {
		now = memorySize(c.Memory)
	} else if !c.Ended.IsZero() {
		ended = endedText(c.Ended, m.hist.dated, time.Now())
	}
	return termCells([]string{
		m.branch[c.ID] + rowLabel(c),
		statusText(c.State, c.DryRun),
		c.Agent,
		c.Subagent,
		runTime(c, m.snap.At),
		now,
		memorySize(c.Peak),
		ended,
		projectLabel(c.Project, projectWidth),
		commandLine(c.Command),
	})
}

// finishedCells are a finished command's cells in the list, as cells makes a
// snapshot row's, with its date when dated and it ended before the day of
// now. It runs beside the screen too, for the rows read from the history.
func finishedCells(c *guard.CommandView, dated bool, now time.Time) []string {
	return termCells([]string{
		rowLabel(*c),
		statusText(c.State, c.DryRun),
		c.Agent,
		c.Subagent,
		runTime(*c, time.Time{}),
		"—",
		memorySize(c.Peak),
		endedText(c.Ended, dated, now),
		projectLabel(c.Project, projectWidth),
		commandLine(c.Command),
	})
}

// termCells are cells in the terminal's glyphs, as the table draws them.
func termCells(cells []string) []string {
	for i, cell := range cells {
		cells[i] = forTerm(cell)
	}
	return cells
}

// commandLine is a command as the list shows it, on one line: of its first
// characters, as many as the history's summaries hold (guard.SummaryLength),
// the rest past the guard's own prefix (see guard.CommandRest), each run of
// white space one space. A row so reads the same before and after its
// command finishes, and a long command costs a frame no more than a short
// one.
func commandLine(command string) string {
	n := 0
	for at := range command {
		if n == guard.SummaryLength {
			command = command[:at]
			break
		}
		n++
	}
	return strings.Join(strings.Fields(guard.CommandRest(command)), " ")
}

// runTime is how long a command ran: until at while it runs (the snapshot's
// time, so every row agrees), else until it ended; "" when that is unknown.
// A command that ended this tick is still tracked, without its end time.
func runTime(c guard.CommandView, at time.Time) string {
	end := at
	if !active(c) && !c.Ended.IsZero() {
		end = c.Ended
	}
	if c.Started.IsZero() || !end.After(c.Started) {
		return ""
	}
	return tookValue(end.Sub(c.Started).Seconds())
}

// endedText is a time on the machine's clock as the list and the details
// show it, "5:07 PM", with its date ("Nov 18 5:07 PM") when dated and it is
// not on the day of now; "" for none.
func endedText(t time.Time, dated bool, now time.Time) string {
	switch {
	case t.IsZero():
		return ""
	case dated:
		return display.Clock(t, now)
	}
	return t.Local().Format(display.Time)
}

// projectLabel fits a project label to width: a label naming a worktree under
// its repository ("api/feature-login") keeps the repository's name and clips
// the worktree's from its start ("api/…ure-login"), so the project always
// reads; any other label is clipped at its end.
func projectLabel(project string, width int) string {
	if ansi.StringWidth(project) <= width {
		return project
	}
	repo, tree, ok := strings.Cut(project, "/")
	if !ok || ansi.StringWidth(repo)+2 >= width {
		return fit(project, width, false)
	}
	return repo + "/" + fit(tree, width-ansi.StringWidth(repo)-1, true)
}

// statusText is a row's state as the list shows it: a mark that carries the
// state without color, then the state's word, and "(dry)" for a dry-run
// command, whose stops are only logged.
func statusText(state string, dry bool) string {
	text := state
	switch state {
	case "running":
		text = "● running"
	case "stopping":
		text = "◌ stopping"
	case "done":
		text = "✓ done"
	case "unmatched":
		text = "? unmatched"
	case "stopped":
		text = "✗ stopped"
	}
	if dry {
		text += " (dry)"
	}
	return text
}

// padded are cells of the shown columns of cols, each fitted to its width
// and padded on its side, the last column left unpadded.
func padded(cols []column, cells []string, shown, widths []int) []string {
	out := make([]string, 0, len(shown))
	for _, i := range shown {
		cell := fit(cells[i], widths[i], false)
		pad := strings.Repeat(" ", max(widths[i]-ansi.StringWidth(cell), 0))
		if cols[i].Right {
			cell = pad + cell
		} else if i != len(cols)-1 {
			cell += pad
		}
		out = append(out, cell)
	}
	return out
}

// styledRow is row i with its marks: a tagged one marked *, a finished one or
// any while no watcher answers faint, the status, the agent and a size of a
// gigabyte or more in their colors. The selected row, behind ▸, lies on a
// band across the window, its ▸ and ID bold in the accent and its colors,
// the * too, as [onBand] draws them; every piece of it, the gaps and the fill
// to the right edge too, is drawn on the band, since each Render ends its own
// style. Without color there is no band, and the selected row is bold
// instead.
func (m *guardView) styledRow(i int, cells []string, shown, widths []int) string {
	c := *m.rows[i].view
	style := lipgloss.NewStyle()
	if !active(c) || !m.connected {
		style = style.Faint(true)
	}
	selected := i == m.cursor && m.mode != detailMode
	band, mark, markStyle := lipgloss.NewStyle(), " ", style
	switch {
	case selected && m.noColor:
		mark, style = forTerm("▸"), style.Bold(true)
		markStyle = style
	case selected:
		pick := lipgloss.LightDark(!m.light)
		band = band.Background(pick(guardBandLight, guardBandDark))
		mark, style = forTerm("▸"), style.Inherit(band)
		markStyle = m.accent().Inherit(band)
	}
	tag := band.Render(" ")
	if m.tagged[c.ID] {
		gold := guardGold
		if selected && !m.noColor {
			gold = onBand(gold, m.light)
		}
		tag = gold.Inherit(band).Render("*")
	}
	now, peak := sizeStyles(c)
	status := statusStyle(c.State)
	colors := map[int]*lipgloss.Style{
		statusColumn: &status,
		agentColumn:  agentStyle(c.Agent),
		memoryColumn: now,
		peakColumn:   peak,
	}
	out := padded(guardColumns, cells, shown, widths)
	for n, col := range shown {
		switch color := colors[col]; {
		case col == 0:
			out[n] = markStyle.Render(out[n])
		case color != nil && selected && !m.noColor:
			out[n] = onBand(*color, m.light).Inherit(style).Render(out[n])
		case color != nil:
			out[n] = color.Inherit(style).Render(out[n])
		default:
			out[n] = style.Render(out[n])
		}
	}
	row := markStyle.Render(mark) + tag + band.Render(" ") +
		strings.Join(out, band.Render(strings.Repeat(" ", gap)))
	if selected {
		row += band.Render(strings.Repeat(" ", max(m.width-ansi.StringWidth(row), 0)))
	}
	return row
}
