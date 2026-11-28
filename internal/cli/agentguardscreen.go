package cli

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/Sawmonabo/workbench/internal/agent/guard"
	"github.com/Sawmonabo/workbench/internal/display"
	"github.com/Sawmonabo/workbench/internal/operation"
)

// View draws the screen in the alternate screen, or asks for a wider or a
// taller window, so the key line and its notices always show. The view never
// takes the mouse, so a plain drag selects text on every screen, and most
// terminals send a wheel step as ↑ or ↓ (wheelmodes.go), which move the list,
// scroll the details and move the stop menu's choice, as the keys do (ADR
// 14).
func (m *guardView) View() tea.View {
	var text string
	switch {
	case m.width < guardViewMinWidth:
		text = fmt.Sprintf("Widen the window to at least %d columns", guardViewMinWidth)
	case m.height < guardViewMinHeight:
		text = fmt.Sprintf("Make the window at least %d lines tall", guardViewMinHeight)
	default:
		text = m.screen()
	}
	view := tea.NewView(text)
	view.AltScreen = true
	return view
}

// screen is the header (the head line, a blank line, the counts and the
// status lines), the list or the details with the stop menu over them when
// it is open, and the key line at the bottom.
func (m *guardView) screen() string {
	lines := []string{m.headLine(), "", m.countLine()}
	lines = append(lines, m.statusLines()...)
	header := len(lines)
	room := max(m.height-len(lines)-1, 2)
	if m.inDetails() {
		lines = append(lines, m.detailsPane(room)...)
	} else {
		// The column heads come first, then the rows.
		lines = append(lines, m.tableLines(room)...)
	}
	for len(lines) < m.height-1 {
		lines = append(lines, "")
	}
	if m.mode == menuMode {
		lines = m.overlay(lines, m.menuBox(), header)
	}
	return strings.Join(append(lines, m.keyLine()), "\n")
}

// headLine is the brand, the watcher's state, a badge when memory is low or
// critical, and the clock, on one line; a window too narrow for all of them
// loses the clock.
func (m *guardView) headLine() string {
	line := bold.Render(brandTag+" Agent Guard") + "  "
	if m.connected {
		line += guardGreen.Render(forTerm("●")) + " watching"
	} else {
		state := " not connected"
		if m.problem.Message == watcherAbsent.Message {
			state = " not running"
		}
		line += guardGold.Render(forTerm("○")) + state
	}
	dot := faint.Render(forTerm(" · "))
	if m.connected {
		// normal, and macOS's warning, are routine for the guard: no badge.
		switch level := m.snap.Memory.Level; level {
		case "low":
			line += dot + guardGold.Bold(true).Render("memory "+level)
		case "critical":
			line += dot + guardRed.Bold(true).Render("memory "+level)
		}
	}
	if clock := line + dot + time.Now().
		Format(display.TimeSeconds); ansi.StringWidth(
		clock,
	) <= m.width {
		return clock
	}
	return fit(line, m.width, false)
}

// memorySlash is a measurement (part) of a capacity (whole), the unit written
// once when both share it, as a meter reads used against total: "11.27/16
// GB", "289 MB/16 GB"; a part of 0, which a watcher without the reading
// sends, is "—/16 GB".
func memorySlash(part, whole uint64) string {
	p, w := memorySize(part), cmp.Or(capacityText(whole), "—")
	number, unit, ok := strings.Cut(p, " ")
	if ok && strings.HasSuffix(w, " "+unit) {
		p = number
	}
	return p + "/" + w
}

// memorySize is a measurement as the view shows it, [sizeText] or an em dash
// for none.
func memorySize(bytes uint64) string { return cmp.Or(sizeText(bytes), "—") }

// countLine counts the rows that run and those that finished, commands and
// detached programs alike, with a thousands separator, and with a filter
// kept how many of each show and its text; with no watcher it counts the
// finished rows alone (design 3.1). Once a watcher has sent a snapshot it
// gives the memory in use of the installed memory, the largest peak since
// the watcher started and the swap in use, the memory figure bold and the
// rest faint. A narrow window drops the swap, then the peak, then clips the
// line.
func (m *guardView) countLine() string {
	var parts []string
	if m.connected {
		parts = append(parts, countOf(m.shown.running, m.total.running)+" running")
	}
	parts = append(parts, countOf(m.shown.finished, m.total.finished)+" finished")
	if m.find.answer != "" {
		parts = append(parts, fmt.Sprintf("find %q", m.find.answer))
	}
	counts := strings.Join(parts, " · ")
	if !m.connected || m.snap.At.IsZero() {
		return fit(faint.Render(forTerm(counts)), m.width, false)
	}
	mem := m.snap.Memory
	before, figure := forTerm(counts+" · mem "), forTerm(memorySlash(mem.Used, mem.Installed))
	tails := []string{forTerm(" · peak " + memorySize(m.snap.LargestPeak))}
	if mem.SwapTotal > 0 {
		// Swap not in use is a reading of 0, not a missing one.
		swap := memorySlash(mem.SwapTotal-mem.SwapFree, mem.SwapTotal)
		// Under half a MB in use, which prints as 0 MB, reads the same way.
		if mem.SwapFree >= mem.SwapTotal || mem.SwapTotal-mem.SwapFree < 1<<19 {
			swap = "0/" + capacityText(mem.SwapTotal)
		}
		tails = append(tails, forTerm(" · swap "+swap))
	}
	width := func() int { return ansi.StringWidth(before + figure + strings.Join(tails, "")) }
	for len(tails) > 0 && width() > m.width {
		tails = tails[:len(tails)-1]
	}
	line := faint.Render(before) + bold.Render(figure) + faint.Render(strings.Join(tails, ""))
	return fit(line, m.width, false)
}

// countOf is a count of rows, or how many of them show when a filter hides
// some: "2,832", "12 of 2,832".
func countOf(shown, total int) string {
	if shown == total {
		return display.Number(int64(total))
	}
	return display.Number(int64(shown)) + " of " + display.Number(int64(total))
}

// statusLines say why no watcher answers or, when shown commands are dry
// runs, that their stops are only logged: for all of them, or for how many;
// then what is wrong with the history (see historyWarning). With none of
// these they are one blank line. Each is drawn as every headline with its
// cause and fix is (see headlineLines), wrapped to the window between two
// words only, a path whole, and a word wider than the window broken
// (breakLong); the dry-run texts fit 60 columns, so the stop menu never
// covers a second line of them.
func (m *guardView) statusLines() []string {
	var notes []operation.Warning
	dry, commands := 0, len(m.snap.Commands)
	for _, c := range m.snap.Commands {
		if c.DryRun {
			dry++
		}
	}
	switch {
	case !m.connected && m.problem.Message != "":
		notes = append(notes, m.problem)
	case dry > 0 && dry == commands:
		notes = append(notes, operation.Warn("dry run: stops are logged, not sent"))
	case dry > 0:
		notes = append(notes, operation.Warn(
			fmt.Sprintf("dry run on %d of %d commands: stops are logged, not sent",
				dry, commands),
		))
	}
	notes = append(notes, m.historyWarnings(time.Now())...)
	if len(notes) == 0 {
		return []string{""}
	}
	var lines []string
	for _, note := range notes {
		for _, line := range headlineLines(m.width, breakLong, false, "", note.Message, note.Body) {
			lines = append(lines, guardGold.Render(line))
		}
	}
	return lines
}

// historyWarnings are what is wrong with the history at now (design rules 3
// and 9), each in the guard's own words: the watcher's while one answers and
// has one, that it cannot write the history or found it damaged and started
// it again; else that the view cannot read it, and, while copies of damaged
// histories are kept, the sentence the watcher says of them, from the copies
// and their times, which the move sets (see guard.MoveDamaged) and a watcher
// reads too.
//
// A refusal of the guard folder stands under the headline that the history
// is unreadable as its cause, with its own lines and fix, which the note of
// why no watcher answers already gives when it is the same refusal.
func (m *guardView) historyWarnings(now time.Time) []operation.Warning {
	if text := m.snap.HistoryError; m.connected && text != "" {
		return operation.WarnAll(text)
	}
	var notes []operation.Warning
	if m.hist.problem != "" {
		notes = append(notes, operation.Warn(m.hist.problem))
	}
	if refusal := m.hist.refusal; refusal != nil {
		note := operation.Warn(historyUnreadable)
		if m.connected || !sameNote(capitalized(refusal.Message), refusal.Body,
			m.problem.Message, m.problem.Body) {
			note.Body = operation.Explain(operation.Cause(refusal.Message)).With(refusal.Body)
		}
		notes = append(notes, note)
	}
	if text := guard.HistoryDamaged(m.hist.damaged, now); text != "" {
		notes = append(notes, operation.Warn(text))
	}
	return notes
}

// historyUnreadable is the headline of a history the view cannot read
// because the guard folder is refused.
const historyUnreadable = "Guard history unreadable"

// menuBox is the stop menu: a box titled with the row's command line and its
// memory, or the number of tagged rows, and the two stops, the chosen one marked.
func (m *guardView) menuBox() []string {
	ids := m.targets
	title := fmt.Sprintf("Stop %d tagged rows", len(ids))
	if len(ids) == 1 {
		i := slices.IndexFunc(
			m.snap.Commands,
			func(c guard.CommandView) bool { return c.ID == ids[0] },
		)
		if i >= 0 {
			c := m.snap.Commands[i]
			title = "Stop " + fit(
				commandLine(c.Command),
				max(min(m.width-24, 40), 10),
				false,
			) + " (" +
				memorySize(
					c.Memory,
				) + ")"
		}
	}
	lines := []string{bold.Render(forTerm(title)), ""}
	for i, choice := range stopChoices {
		if i == m.menu {
			lines = append(lines, bold.Render(forTerm("▸ "+choice)))
		} else {
			lines = append(lines, "  "+choice)
		}
	}
	box := lipgloss.NewStyle().Border(glyphs.border).Padding(0, 1).Render(strings.Join(lines, "\n"))
	return strings.Split(box, "\n")
}

// overlay draws box over the middle of lines, below the first header lines
// and keeping what lies left and right of it.
func (m *guardView) overlay(lines, box []string, header int) []string {
	boxWidth := lipgloss.Width(strings.Join(box, "\n"))
	left := max((m.width-boxWidth)/2, 0)
	top := min(max((len(lines)-len(box))/2, header), max(len(lines)-len(box), 0))
	for i, row := range box {
		at := top + i
		if at >= len(lines) {
			break
		}
		base := padCell(lines[at], m.width, false)
		// Truncate carries the cut part's escape codes along; the reset makes
		// sure no style the line left open, such as the selected row's band,
		// reaches the box's border.
		lines[at] = ansi.Truncate(base, left, "") + ansi.ResetStyle + row +
			ansi.TruncateLeft(base, left+boxWidth, "")
	}
	return lines
}

// keyHint is a key the bottom line names and what it does; a hint with a
// drop goes when the window is too narrow, the highest drop first.
type keyHint struct {
	key, what string
	drop      int
}

// keyLine is find's input while it is open, else the notice while it shows,
// else the keys the screen takes, the list's ending with the selected row's
// number and the row count. A narrow window drops the hints of least use
// first, then clips the line.
func (m *guardView) keyLine() string {
	if m.find.open && m.mode == listMode {
		m.find.input.SetWidth(max(m.width-2, 1))
		return m.find.input.View()
	}
	if m.notice != "" && time.Now().Before(m.noticeUntil) {
		return fit(forTerm(m.notice), m.width, false)
	}
	hints, count := m.keyHints(), ""
	if m.mode == listMode && len(m.rows) > 0 {
		count = "  " + bold.Render(
			display.Number(int64(m.cursor+1))+"/"+display.Number(int64(len(m.rows))),
		)
	}
	line := func() string {
		parts := make([]string, len(hints))
		for i, h := range hints {
			parts[i] = bold.Render(forTerm(h.key)) + " " + faint.Render(h.what)
		}
		return strings.Join(parts, "  ") + count
	}
	for ansi.StringWidth(line()) > m.width {
		drop := -1
		for i, h := range hints {
			if h.drop > 0 && (drop < 0 || h.drop > hints[drop].drop) {
				drop = i
			}
		}
		if drop < 0 {
			break
		}
		hints = slices.Delete(hints, drop, drop+1)
	}
	return fit(line(), m.width, false)
}

// keyHints are the keys the screen shown takes.
func (m *guardView) keyHints() []keyHint {
	switch m.mode {
	case menuMode:
		return []keyHint{{"↑/↓", "choose", 0}, {"enter", "send", 0}, {"esc/←", "cancel", 0}}
	case detailMode:
		switch {
		case m.detail.panel:
			return []keyHint{
				{"↑/↓", "scroll", 0},
				{"pgup/pgdn", "page", 1},
				{"esc/←", "close the panel", 0},
				{"q", "quit", 0},
			}
		case len(m.detail.picks) > 0:
			return []keyHint{
				{"↑/↓", "choose", 0},
				{"enter/→", "open", 0},
				{"pgup/pgdn", "scroll", 2},
				{"k", "stop", 1},
				{"esc/←", "back", 0},
				{"q", "quit", 0},
			}
		}
		return []keyHint{
			{"↑/↓", "scroll", 0},
			{"k", "stop", 0},
			{"esc/←", "back", 0},
			{"q", "quit", 0},
		}
	case listMode:
	}
	hints := []keyHint{
		{"↑/↓", "move", 0},
		{"space", "tag", 4},
		{"enter/→", "details", 3},
		{"/", "find", 2},
		{"s", "sort", 5},
		{"pgup/pgdn", "page", 6},
		{"k", "stop", 1},
		{"q/esc", "quit", 0},
	}
	if m.find.text != "" {
		hints = append(hints[:len(hints)-1], keyHint{"esc", "clear", 0}, keyHint{"q", "quit", 0})
	}
	return hints
}
