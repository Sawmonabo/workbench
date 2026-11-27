package cli

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/viewport"
	"charm.land/lipgloss/v2"

	"github.com/Sawmonabo/workbench/internal/agent/guard"
	"github.com/Sawmonabo/workbench/internal/display"
)

// viewDetail is what the details hold beyond their row id: a finished
// command's parts (its whole command, graph and processes) as read from the
// history or as the snapshot sent them; whether they are read now, and why
// they cannot be; the parts the `… more` rows open, as
// the last frame drew them, the chosen one and whether to scroll it into
// sight; whether the side panel shows and which part; the parts' lines as
// last made, at most partsMade of them, and the slot the next takes; and
// the lines the panel's pane holds.
type viewDetail struct {
	id      string
	parts   *guard.Finished
	reading bool
	problem string
	picks   []morePick
	pick    int
	reveal  bool
	panel   bool
	part    detailPart
	made    [partsMade]partLines
	next    int
	paned   paneKey
}

// partsMade is how many parts' lines the details keep: beside the side
// panel the command shows at two widths, and the panel may show the
// processes.
const partsMade = 4

// detailPart is a part of the details that a `… more` row opens in the
// side panel.
type detailPart int

const (
	commandPart detailPart = iota
	processesPart
)

// morePick is a `… more` row of the details: the part it opens and its line.
type morePick struct {
	part detailPart
	line int
}

// partLines are a part's title and lines, made for the part, the row, its
// parts, the width and, for a running command's processes, the snapshot in
// key.
type partLines struct {
	key   partKey
	title string
	lines []string
}

// paneKey is the lines the side panel's pane holds, by their first line and
// count, and its size when it took them: a pane given the same lines again
// would measure every one of them.
type paneKey struct {
	first         *string
	n             int
	width, height int
}

// partKey is what a part's lines are made from.
type partKey struct {
	part         detailPart
	id           string
	parts        *guard.Finished
	width, snaps int
}

// detailsViewport is the details' pane: ↑ and ↓ scroll a line and pgup and
// pgdn a page; home and end go to its ends, and the view takes every other
// key itself, so k stops and ← goes back rather than scrolling.
func detailsViewport() viewport.Model {
	pane := viewport.New()
	pane.KeyMap = viewport.KeyMap{
		Up:       key.NewBinding(key.WithKeys("up")),
		Down:     key.NewBinding(key.WithKeys("down")),
		PageUp:   key.NewBinding(key.WithKeys("pgup")),
		PageDown: key.NewBinding(key.WithKeys("pgdown")),
	}
	return pane
}

// detailsPane is the details in room lines: in their scrolling pane, or
// beside the side panel when it is open (see split).
func (m *guardView) detailsPane(room int) []string {
	if m.cursor >= len(m.rows) {
		return nil
	}
	if m.detail.panel {
		return m.split(room)
	}
	// The pane takes its new size first, so the content it then gets pulls
	// an offset past its end back.
	m.details.SetWidth(m.width)
	m.details.SetHeight(room)
	m.details.SetContentLines(m.detailLines(m.width))
	m.reveal()
	return strings.Split(m.details.View(), "\n")
}

// reveal scrolls the details, once after the choice moved, so the chosen
// `… more` row shows.
func (m *guardView) reveal() {
	d := &m.detail
	if !d.reveal || d.pick >= len(d.picks) {
		d.reveal = false
		return
	}
	d.reveal = false
	line := d.picks[d.pick].line
	switch top := m.details.YOffset(); {
	case line < top:
		m.details.SetYOffset(line)
	case line >= top+m.details.Height():
		m.details.SetYOffset(line - m.details.Height() + 1)
	}
}

// detailIndent is the cells before a field's value: two spaces and the
// label's column.
const detailIndent = 13

// detailLines are the selected command's details, width cells wide, in the
// order of design 3.3: a blank line, its folder, agent, subagent, full ID,
// for a detached program the ID the log gives it and, when its call is
// known, the full ID of the call that started it, status and run time, for a
// detached program the guard will not stop itself why not (see heldText),
// when it started and, once finished, when it ended, its memory, a graph of
// its memory over the whole run, its command and its processes. A command
// longer than 4 lines shows 3 and a processes list longer than 11 shows 10,
// each with a `… more` row that opens the whole of it in the side panel; the
// rows are kept in picks.
func (m *guardView) detailLines(width int) []string {
	r := m.rows[m.cursor]
	d := &m.detail
	_, command := m.partLines(r, commandPart, width-detailIndent)
	procs := m.processesOf(r)
	cuts := 0
	for _, cut := range []bool{len(command) > commandWhole, procs.n() > processesWhole} {
		if cut {
			cuts++
		}
	}
	d.pick, d.picks = min(d.pick, max(cuts-1, 0)), d.picks[:0]
	lines := append([]string{""}, m.facts(*r.view, width)...)
	lines = append(lines, faint.Render(fmt.Sprintf("  %-11s", "Graph"))+m.graph(r, width))
	if d.problem != "" {
		lines = append(lines, field("", width, guardGold.Render(d.problem))...)
	}
	head := faint.Render(fmt.Sprintf("  %-11s", "Command"))
	shown := command
	if len(command) > commandWhole {
		shown = command[:commandCut]
	}
	for _, line := range shown {
		lines = append(lines, head+line)
		head = strings.Repeat(" ", detailIndent)
	}
	if hidden := len(command) - len(shown); hidden > 0 {
		lines = m.more(lines, commandPart, "… "+commas(int64(hidden))+" more lines")
	}
	return m.processRows(lines, procs, width)
}

// facts are the details' fields above the graph: their rows keep their
// height while a command runs.
func (m *guardView) facts(c guard.CommandView, width int) []string {
	var lines []string
	add := func(name, value string) { lines = append(lines, field(name, width, value)...) }
	add("Folder", display.Path(c.Cwd))
	add("Agent", c.Agent)
	add("Subagent", cmp.Or(c.Subagent, "—"))
	add("ID", c.ID)
	if c.Kind == "detached" {
		add("In the log", shortCallID(c.ID))
	}
	if c.LaunchedBy != "" {
		add("Started by", c.LaunchedBy)
	}
	status := statusStyle(c.State).Render(forTerm(statusText(c.State, c.DryRun)))
	if c.By != "" {
		status += " by " + c.By
	}
	if took := runTime(c, m.snap.At); took != "" {
		status += forTerm(" · ") + took
	}
	add("Status", status)
	if text := heldText(c.Held); text != "" {
		add("Note", text)
	}
	now := time.Now()
	if !c.Started.IsZero() {
		add("Started", endedText(c.Started, true, now))
	}
	if !active(c) && !c.Ended.IsZero() {
		add("Ended", endedText(c.Ended, true, now))
	}
	nowStyle, peakStyle := sizeStyles(c)
	memory := "peak " + styled(memorySize(c.Peak), peakStyle)
	if active(c) {
		memory = styled(memorySize(c.Memory), nowStyle) + " now" + forTerm(" · ") + memory
	}
	add("Memory", memory)
	return lines
}

// styled is text in style, or as it is for none.
func styled(text string, style *lipgloss.Style) string {
	if style == nil {
		return text
	}
	return style.Render(text)
}

// field lays out a field's values beside its label, in a column a space past
// the longest label (Started by), a value after the first starting on its own
// line two spaces in, each wrapped to width.
func field(name string, width int, values ...string) []string {
	var lines []string
	head := faint.Render(fmt.Sprintf("  %-11s", name))
	for i, value := range values {
		for _, line := range wrapLines(value, width-detailIndent, min(i, 1)*2) {
			lines = append(lines, head+line)
			head = strings.Repeat(" ", detailIndent)
		}
	}
	return lines
}

// heldText says in plain words why the guard itself will not stop a
// detached program now (guard.CommandView.Held), "" when it may.
func heldText(held string) string {
	why := map[string]string{
		"no call": "no call is proved to have started it",
		"young":   "it was found, or started its program, less than ten seconds ago",
		"unread":  "its setting cannot be read",
	}[held]
	if why == "" {
		return ""
	}
	return "The guard will not stop this itself: " + why + ". You can stop it here."
}

// graph is the details' graph of a command's memory over its whole run, at
// most 120 bars and never wider than width leaves room for: a running
// command's from the snapshot, a finished one's from its parts, and an
// ellipsis while they are read.
func (m *guardView) graph(r listRow, width int) string {
	readings := r.view.History
	switch parts := m.partsOf(r); {
	case active(*r.view):
	case parts != nil:
		readings = parts.Graph
	case r.seq > 0:
		return faint.Render(forTerm("…"))
	}
	return memoryGraph(readings, r.view.Peak, min(guard.GraphBars, width-15))
}

// memoryGraph is a one-line chart of readings fitted to at most bars bars,
// each the largest of the readings it stands for, so the peak stays in it,
// drawn from ▁ to █ scaled to peak and colored by its own size as the list
// colors a size: plain under a gigabyte, gold from there, red from four
// (sizeStyle). It is a dash with no readings.
func memoryGraph(readings []uint64, peak uint64, bars int) string {
	if len(readings) == 0 || bars < 1 {
		return forTerm("—")
	}
	if len(readings) > bars {
		fitted := make([]uint64, bars)
		for i, v := range readings {
			at := i * bars / len(readings)
			fitted[at] = max(fitted[at], v)
		}
		readings = fitted
	}
	peak = max(peak, slices.Max(readings))
	levels := []rune("▁▂▃▄▅▆▇█")
	var out, run strings.Builder
	step := -1
	var style *lipgloss.Style
	flush := func() {
		if run.Len() > 0 {
			out.WriteString(styled(forTerm(run.String()), style))
			run.Reset()
		}
	}
	for _, v := range readings {
		if s := sizeStep(v); s != step {
			flush()
			step, style = s, sizeStyle(v)
		}
		level := 0
		if peak > 0 {
			level = min(int(v*uint64(len(levels))/peak), len(levels)-1)
		}
		run.WriteRune(levels[level])
	}
	flush()
	return out.String()
}

// more adds a `… more` row for part, the chosen one marked ▸ in the accent
// and saying what enter does, or that the side panel shows it.
func (m *guardView) more(lines []string, part detailPart, text string) []string {
	d := &m.detail
	chosen := len(d.picks) == d.pick
	d.picks = append(d.picks, morePick{part: part, line: len(lines)})
	if !chosen {
		return append(lines, strings.Repeat(" ", detailIndent)+faint.Render(forTerm(text)))
	}
	hint := "enter shows all"
	if d.panel {
		hint = "shown on the right"
	}
	return append(lines, strings.Repeat(" ", detailIndent-2)+
		m.accent().Render(forTerm("▸ "+text))+faint.Render(forTerm("  · "+hint)))
}

// partsOf is a finished command's parts for row r when the view has them:
// those the snapshot sent, or those read for the details.
func (m *guardView) partsOf(r listRow) *guard.Finished {
	if r.finished != nil {
		return r.finished
	}
	if r.view.ID == m.detail.id {
		return m.detail.parts
	}
	return nil
}

// processList is the processes the details list for a command: a running
// command's tree with each process's memory now, in the watcher's order, or
// every process a finished one had with its peak, the largest first, and
// how many it had beyond those kept; reading while those are read.
type processList struct {
	tree     []guard.ProcessView
	peaks    []guard.ProcessPeak
	unlisted int
	reading  bool
}

// processesOf is the processes of row r. A finished command's come with
// its parts: a snapshot row that ended this tick gets them with the next
// snapshot, a held one once they are read.
func (m *guardView) processesOf(r listRow) processList {
	parts := m.partsOf(r)
	switch {
	case active(*r.view):
		return processList{tree: r.view.Tree}
	case parts == nil:
		return processList{reading: true}
	}
	return processList{peaks: parts.Processes, unlisted: parts.Unlisted}
}

// n is how many processes the list holds.
func (p processList) n() int { return len(p.tree) + len(p.peaks) }

// count is the count line: "27 running", "143 in all", or "10,000 of
// 12,400 kept" for a finished command whose processes were not all kept.
func (p processList) count() string {
	n := int64(p.n())
	switch {
	case p.peaks == nil:
		return commas(n) + " running"
	case p.unlisted > 0:
		return commas(n) + " of " + commas(n+int64(p.unlisted)) + " kept"
	}
	return commas(n) + " in all"
}

// head is the column heads of the list.
func (p processList) head() string {
	size := "MEMORY"
	if p.peaks != nil {
		size = "PEAK"
	}
	return faint.Render(fmt.Sprintf("%-8s %-22s %10s", "PID", "NAME", size))
}

// line is process i: its PID, its name and its size, colored by the size.
func (p processList) line(i int) string {
	var pid int
	var name string
	var size uint64
	if i < len(p.tree) {
		pid, name, size = p.tree[i].PID, p.tree[i].Name, p.tree[i].Memory
	} else {
		q := p.peaks[i-len(p.tree)]
		pid, name, size = q.PID, q.Name, q.Peak
	}
	line := fmt.Sprintf(
		"%-8d %s %10s",
		pid,
		padCell(fit(name, 22, false), 22, false),
		memorySize(size),
	)
	return styled(forTerm(line), sizeStyle(size))
}

// processRows add the processes to the details: none, a count line, the
// column heads and the rows, the first 10 and a `… more` row when there are
// more than 11.
func (m *guardView) processRows(lines []string, procs processList, width int) []string {
	switch {
	case procs.reading:
		return append(lines, field("Processes", width, faint.Render(forTerm("…")))...)
	case procs.n() == 0:
		return append(lines, field("Processes", width, "none")...)
	}
	indent := strings.Repeat(" ", detailIndent)
	lines = append(lines, field("Processes", width, procs.count())...)
	lines = append(lines, indent+procs.head())
	shown := procs.n()
	if shown > processesWhole {
		shown = processesCut
	}
	for i := range shown {
		lines = append(lines, indent+procs.line(i))
	}
	if hidden := procs.n() - shown; hidden > 0 {
		lines = m.more(lines, processesPart, "… "+commas(int64(hidden))+" more processes")
	}
	return lines
}

// partLines are a part of row r whole, width cells wide, with its title: the
// command's steps wrapped (see commandLines), or the processes under their
// column heads. They are made once for a row, its parts and a width, and a
// running command's processes once for each snapshot.
func (m *guardView) partLines(r listRow, part detailPart, width int) (string, []string) {
	parts := m.partsOf(r)
	key := partKey{part: part, id: r.view.ID, parts: parts, width: width}
	if part == processesPart && active(*r.view) {
		key.snaps = m.snaps
	}
	// Lines made for an older snapshot give way first, then the oldest.
	d, slot := &m.detail, -1
	for i, made := range d.made {
		switch {
		case made.lines != nil && made.key == key:
			return made.title, made.lines
		case made.key.part == key.part && made.key.id == key.id && made.key.width == key.width:
			slot = i
		}
	}
	if slot < 0 {
		slot, d.next = d.next, (d.next+1)%partsMade
	}
	made := &d.made[slot]
	made.key = key
	switch part {
	case commandPart:
		command, cut := r.view.Command, int64(0)
		if parts != nil {
			command, cut = parts.Command, parts.Cut
		}
		made.title, made.lines = "Command", commandLines(command, cut, width)
	case processesPart:
		procs := m.processesOf(r)
		made.title = "Processes · " + procs.count()
		if procs.peaks != nil {
			made.title += ", largest first"
		}
		made.lines = make([]string, 0, procs.n()+1)
		made.lines = append(made.lines, procs.head())
		for i := range procs.n() {
			made.lines = append(made.lines, procs.line(i))
		}
	}
	return made.title, made.lines
}

// commandLines are a command's steps (see commandSteps) wrapped to width,
// each after the first two spaces in, and, when its text was cut where the
// history keeps it, a last line saying how much was left out.
func commandLines(command string, cut int64, width int) []string {
	var lines []string
	for i, step := range commandSteps(command) {
		lines = append(lines, wrapLines(step, width, min(i, 1)*2)...)
	}
	if cut > 0 {
		lines = append(lines, faint.Render(forTerm("… "+textSize(cut)+" more were not kept")))
	}
	return lines
}

// split is the details with the side panel open, height lines tall (design
// 3.3): the opened part whole on the right half under its title, scrolling,
// with a last line saying which lines show when not all fit, and the details
// on the left half, wrapped to it and scrolled so the chosen row shows. A
// window under 100 columns shows the panel alone.
func (m *guardView) split(height int) []string {
	right, left := m.width, 0
	if m.width >= splitWidth {
		right = m.width / 2
		left = m.width - right - 3
	}
	var body []string
	if left > 0 {
		body = m.detailLines(left)
		for _, pick := range m.detail.picks {
			if pick.part == m.detail.part {
				body = body[min(max(pick.line-height*2/3, 0), max(len(body)-height, 0)):]
				break
			}
		}
	}
	title, lines := m.partLines(m.rows[m.cursor], m.detail.part, right)
	m.pane.SetWidth(right)
	m.pane.SetHeight(max(height-2, 1))
	if key := (paneKey{&lines[0], len(lines), right, m.pane.Height()}); key != m.detail.paned {
		m.pane.SetContentLines(lines)
		m.detail.paned = key
	}
	pane := append(
		[]string{m.accent().Render(forTerm(title))},
		strings.Split(m.pane.View(), "\n")...)
	if total := m.pane.TotalLineCount(); total > m.pane.Height() {
		from := m.pane.YOffset() + 1
		to := min(from+m.pane.Height()-1, total)
		pane = append(pane, faint.Render(forTerm(fmt.Sprintf("lines %s to %s of %s",
			commas(int64(from)), commas(int64(to)), commas(int64(total))))))
	}
	bar := faint.Render(forTerm(" │ "))
	out := make([]string, height)
	for i := range out {
		var l, r string
		if i < len(body) {
			l = fit(body[i], left, false)
		}
		if i < len(pane) {
			r = pane[i]
		}
		if left == 0 {
			out[i] = r
			continue
		}
		out[i] = padCell(l, left, false) + bar + r
	}
	return out
}
