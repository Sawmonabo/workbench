package cli

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"syscall"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/spf13/cobra"

	"github.com/Sawmonabo/workbench/internal/guard"
	"github.com/Sawmonabo/workbench/internal/operation"
	"github.com/Sawmonabo/workbench/internal/platform"
)

// guardMemoryCommand opens the live view of the commands the guard tracks. It
// is for a person: with --json, with --non-interactive or without a terminal
// on stdin and stdout, or under a coding agent, it exits 3 before the screen
// opens.
func guardMemoryCommand(o *options) *cobra.Command {
	return &cobra.Command{
		Use:   "memory",
		Short: "Watch the commands the guard tracks and stop one",
		Long: "Show, live, each agent command the guard's watcher tracks: its status, how long it " +
			"has run, its memory now and at its peak, its project, agent and subagent, and the " +
			"commands that ended in the last minute, dimmed. ↑ and ↓ move, space tags a running " +
			"command, enter opens its details (the full command, folder, memory graph and " +
			"processes), k opens the stop menu for the tagged commands or the selected one " +
			"(a polite stop, force-killed after 5 s, or a forced kill now), esc closes a menu or " +
			"the details, and q quits. The watcher signals the command, logs the stop as by=you " +
			"and tells the agent; with WORKBENCH_GUARD=dry-run it only logs. While no watcher " +
			"runs, the view says so and reconnects every second. It needs a terminal, has no " +
			"--json output and refuses --non-interactive and coding agents; workbench guard " +
			"logs --follow prints the same events.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return o.runPrinted(cmd, func(*operation.Result) error { return runGuardView(cmd, o) })
		},
	}
}

// runGuardView refuses a caller that is not a person at a terminal, then runs
// the view until it quits. It starts the program as the costs view does. A
// full-screen view prints nothing a program could read, so --json is refused,
// and --non-interactive counts as no terminal, as --choose treats it.
func runGuardView(cmd *cobra.Command, o *options) error {
	if o.json {
		return operation.Fail(
			operation.ExitBlocked,
			"json",
			"guard memory has no JSON output; use workbench guard logs --json",
		)
	}
	if o.nonInteractive || !platform.IsTerminal(os.Stdin.Fd()) ||
		!platform.IsTerminal(os.Stdout.Fd()) {
		return operation.Fail(
			operation.ExitBlocked,
			"terminal",
			"guard memory needs a terminal; use workbench guard logs --follow",
		)
	}
	if operation.AgentSession() {
		return operation.Fail(
			operation.ExitBlocked,
			"agent",
			"guard memory is for a person at a terminal",
		)
	}
	model := &guardView{width: terminalWidth(os.Stdout), height: 24, tagged: map[string]bool{}}
	program := tea.NewProgram(
		model,
		tea.WithInput(os.Stdin),
		tea.WithOutput(os.Stdout),
		tea.WithContext(cmd.Context()),
		// main owns SIGINT and SIGTERM through the command's context.
		tea.WithoutSignalHandler(),
	)
	_, err := program.Run()
	if model.client != nil {
		_ = model.client.Close()
	}
	return err
}

// guardView is the live view: the watcher's latest snapshot, the selected
// row, the tagged rows by call ID, what the screen shows (the list, the stop
// menu or one command's details), the menu's chosen line, a short notice on
// the bottom line until noticeUntil, the terminal's size, and the connection:
// the watcher's release, whether a watcher answers, and why not when none
// does. The rows stay after a disconnect, dimmed.
type guardView struct {
	client      *guard.Client
	snap        guard.Snapshot
	cursor      int
	tagged      map[string]bool
	mode        viewMode
	menu        int
	notice      string
	noticeUntil time.Time
	width       int
	height      int
	release     string
	connected   bool
	problem     string
	// dialing is set while a connection attempt runs, so a tick never starts
	// a second one: a watcher that never answers holds an attempt 3 s.
	dialing bool
	// menuFrom is the screen the stop menu opened from and returns to.
	menuFrom viewMode
}

// viewMode is what the view shows.
type viewMode int

const (
	// listMode shows the commands.
	listMode viewMode = iota
	// menuMode shows the stop menu over the list or the details.
	menuMode
	// detailMode shows the selected command's details.
	detailMode
)

// snapshotMsg is a snapshot the watcher sent.
type snapshotMsg guard.Snapshot

// disconnectedMsg says the watcher's connection ended.
type disconnectedMsg struct{}

// stoppedMsg is the watcher's answer to a stop, one result per call ID; a
// stop that got no answer gives each ID the error.
type stoppedMsg []guard.StopResult

// tickMsg comes once a second, for the clock, the notice's expiry and
// reconnecting.
type tickMsg time.Time

// connectedMsg is a connection attempt's outcome: a client that said hello
// and the watcher's release, or why no watcher answered.
type connectedMsg struct {
	client  *guard.Client
	release string
	problem string
}

// The texts the view shows when no watcher answers.
const (
	watcherAbsent = "The guard's watcher is not running; it starts with the next agent " +
		"command. Reconnecting every second."
	watcherOlder = "The watcher runs an older Workbench release that does not serve this " +
		"view; run workbench update."
	watcherOther = "The watcher runs a Workbench release that speaks another protocol " +
		"version; run workbench update."
)

// The view's sizes: the narrowest window it draws in, how long a notice
// stays, and the readings the memory graph shows at most.
const (
	guardViewMinWidth = 60
	noticeShown       = 3 * time.Second
	graphReadings     = 120
)

// The two stops the menu offers, polite first, as the guard's own stops go.
var stopChoices = []string{"Stop (polite, force-killed after 5 s)", "Force kill now"}

// viewGlyphs swaps the view's own marks, which the shared glyph set does not
// know, for ASCII stand-ins where the locale is not UTF-8.
var viewGlyphs = func() *strings.Replacer {
	if glyphs.Rule == "─" {
		return strings.NewReplacer()
	}
	return strings.NewReplacer(
		"▸", ">", "●", "o", "○", ".", "◌", "~",
		"▁", "_", "▂", ".", "▃", "-", "▄", "~", "▅", "=", "▆", "+", "▇", "*", "█", "#",
	)
}()

// viewText is s in the terminal's glyphs.
func viewText(s string) string { return glyphs.text.Replace(viewGlyphs.Replace(s)) }

// nextTick waits a second for the next tickMsg.
func nextTick() tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg { return tickMsg(t) })
}

// waitSnapshot waits for the watcher's next snapshot, or for the end of the
// connection, as Bubble Tea's realtime example waits on a channel.
func waitSnapshot(ch <-chan guard.Snapshot) tea.Cmd {
	return func() tea.Msg {
		snap, ok := <-ch
		if !ok {
			return disconnectedMsg{}
		}
		return snapshotMsg(snap)
	}
}

// connectWatcher dials the watcher's socket and says hello. A missing or
// refused socket means no watcher runs; a hello that times out means a
// watcher of an older release took the socket over without serving it; a
// hello without a protocol version in common means another release.
func connectWatcher() tea.Msg {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	client, err := guard.Dial(ctx)
	switch {
	case errors.Is(err, syscall.ENOENT), errors.Is(err, syscall.ECONNREFUSED):
		return connectedMsg{problem: watcherAbsent}
	case err != nil:
		return connectedMsg{problem: "Cannot reach the guard's watcher: " + err.Error()}
	}
	release, err := client.Hello()
	if err == nil {
		return connectedMsg{client: client, release: release}
	}
	_ = client.Close()
	switch {
	case errors.Is(err, os.ErrDeadlineExceeded):
		return connectedMsg{problem: watcherOlder}
	case errors.Is(err, guard.ErrProtocolVersion):
		return connectedMsg{problem: watcherOther}
	}
	return connectedMsg{problem: watcherAbsent}
}

// Init connects and starts the clock.
func (m *guardView) Init() tea.Cmd {
	m.dialing = true
	return tea.Batch(connectWatcher, nextTick())
}

// Update takes a message. Each tick schedules the next and, while no watcher
// answers and no attempt runs, tries again.
func (m *guardView) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
	case tickMsg:
		if !m.connected && !m.dialing {
			m.dialing = true
			return m, tea.Batch(connectWatcher, nextTick())
		}
		return m, nextTick()
	case connectedMsg:
		m.dialing = false
		if msg.client == nil {
			m.problem = msg.problem
			return m, nil
		}
		m.client, m.release, m.connected, m.problem = msg.client, msg.release, true, ""
		return m, waitSnapshot(m.client.Snapshots())
	case snapshotMsg:
		if m.client == nil {
			return m, nil
		}
		m.take(guard.Snapshot(msg))
		return m, waitSnapshot(m.client.Snapshots())
	case disconnectedMsg:
		if m.client != nil {
			_ = m.client.Close()
		}
		m.client, m.connected, m.problem = nil, false, watcherAbsent
		if m.mode == menuMode {
			m.mode = m.menuFrom
		}
	case stoppedMsg:
		m.answered(msg)
	case tea.KeyPressMsg:
		return m.press(msg.String())
	}
	return m, nil
}

// take shows a new snapshot: the selection stays on its command, tags stay
// only on commands still running, and details whose command left the
// snapshot close.
func (m *guardView) take(snap guard.Snapshot) {
	selected := m.selectedID()
	m.snap = snap
	at := slices.IndexFunc(
		snap.Commands,
		func(c guard.CommandView) bool { return c.ID == selected },
	)
	if at < 0 {
		m.cursor = max(min(m.cursor, len(snap.Commands)-1), 0)
		m.mode = listMode
	} else {
		m.cursor = at
	}
	for id := range m.tagged {
		i := slices.IndexFunc(snap.Commands, func(c guard.CommandView) bool { return c.ID == id })
		if i < 0 || !active(snap.Commands[i].State) {
			delete(m.tagged, id)
		}
	}
}

// selectedID is the selected command's call ID, empty with no rows.
func (m *guardView) selectedID() string {
	if m.cursor < len(m.snap.Commands) {
		return m.snap.Commands[m.cursor].ID
	}
	return ""
}

// active reports whether a command in state still runs, so it can be tagged
// and stopped.
func active(state string) bool { return state == "running" || state == "stopping" }

// say shows text on the bottom line for a few seconds.
func (m *guardView) say(text string) {
	m.notice, m.noticeUntil = text, time.Now().Add(noticeShown)
}

// answered shows the watcher's answer to a stop: "stopped 4fQ8nX2a", or the
// ID and why not ("4fQ8nX2a already finished").
func (m *guardView) answered(results []guard.StopResult) {
	parts := make([]string, 0, len(results))
	for _, r := range results {
		if r.OK {
			parts = append(parts, "stopped "+shortCallID(r.ID))
		} else {
			parts = append(parts, shortCallID(r.ID)+" "+r.Error)
		}
	}
	m.say(strings.Join(parts, "; "))
}

// press handles a key in the present mode; q and ctrl+c quit from any.
func (m *guardView) press(key string) (tea.Model, tea.Cmd) {
	if key == "q" || key == "ctrl+c" {
		return m, tea.Quit
	}
	switch m.mode {
	case menuMode:
		return m, m.pressMenu(key)
	case detailMode:
		switch key {
		case "esc":
			m.mode = listMode
		case "k":
			m.openMenu(detailMode)
		}
	default:
		m.pressList(key)
	}
	return m, nil
}

// pressList handles a key on the list. There is no j or k to move, since k
// stops, as in htop.
func (m *guardView) pressList(key string) {
	rows := len(m.snap.Commands)
	switch key {
	case "up":
		m.cursor = max(m.cursor-1, 0)
	case "down":
		m.cursor = max(min(m.cursor+1, rows-1), 0)
	case "space":
		if m.cursor < rows && active(m.snap.Commands[m.cursor].State) {
			id := m.snap.Commands[m.cursor].ID
			if m.tagged[id] {
				delete(m.tagged, id)
			} else {
				m.tagged[id] = true
			}
		}
		m.cursor = max(min(m.cursor+1, rows-1), 0)
	case "enter":
		if m.cursor < rows {
			m.mode = detailMode
		}
	case "k":
		m.openMenu(listMode)
	}
}

// pressMenu handles a key in the stop menu: ↑ and ↓ choose, enter sends and
// esc cancels.
func (m *guardView) pressMenu(key string) tea.Cmd {
	switch key {
	case "up":
		m.menu = max(m.menu-1, 0)
	case "down":
		m.menu = min(m.menu+1, len(stopChoices)-1)
	case "esc":
		m.mode = m.menuFrom
	case "enter":
		m.mode = m.menuFrom
		return m.send(m.stopTargets(m.menuFrom), m.menu == 1)
	}
	return nil
}

// stopTargets are the call IDs a stop would name: from the details, the
// command shown; from the list, the tagged commands still running, or else
// the selected one when it runs.
func (m *guardView) stopTargets(from viewMode) []string {
	var ids []string
	if from == listMode {
		for _, c := range m.snap.Commands {
			if m.tagged[c.ID] && active(c.State) {
				ids = append(ids, c.ID)
			}
		}
	}
	if len(ids) == 0 && m.cursor < len(m.snap.Commands) && active(m.snap.Commands[m.cursor].State) {
		ids = []string{m.snap.Commands[m.cursor].ID}
	}
	return ids
}

// openMenu opens the stop menu from a screen, its polite stop chosen, when
// there is a running command to stop and a watcher to ask.
func (m *guardView) openMenu(from viewMode) {
	switch {
	case len(m.stopTargets(from)) == 0 && m.cursor < len(m.snap.Commands):
		m.say(shortCallID(m.selectedID()) + " already finished")
	case len(m.stopTargets(from)) == 0:
	case !m.connected:
		m.say("The watcher is not running; nothing was stopped")
	default:
		m.mode, m.menu, m.menuFrom = menuMode, 0, from
	}
}

// send asks the watcher to stop ids, politely or by force, without waiting:
// its answer comes back as a stoppedMsg, and the rows follow the stop.
func (m *guardView) send(ids []string, force bool) tea.Cmd {
	client := m.client
	if len(ids) == 0 || client == nil {
		return nil
	}
	short := make([]string, len(ids))
	for i, id := range ids {
		short[i] = shortCallID(id)
	}
	m.say("asking the watcher to stop " + strings.Join(short, ", "))
	clear(m.tagged)
	return func() tea.Msg {
		results, err := client.Stop(ids, force)
		if err != nil {
			results = make([]guard.StopResult, len(ids))
			for i, id := range ids {
				results[i] = guard.StopResult{ID: id, Error: err.Error()}
			}
		}
		return stoppedMsg(results)
	}
}

// View draws the screen in the alternate screen, or asks for a wider window.
func (m *guardView) View() tea.View {
	text := "Widen the window to at least 60 columns"
	if m.width >= guardViewMinWidth {
		text = m.screen()
	}
	view := tea.NewView(text)
	view.AltScreen = true
	return view
}

// screen is the header, the list or the details with the stop menu over
// them when it is open, and the key line at the bottom.
func (m *guardView) screen() string {
	lines := []string{m.headLine(), m.countLine(), m.statusLine()}
	room := max(m.height-len(lines)-2, 1)
	if m.mode == detailMode || (m.mode == menuMode && m.menuFrom == detailMode) {
		lines = append(lines, m.detailLines(room+1)...)
	} else {
		lines = append(lines, m.tableLines(room+1)...)
	}
	for len(lines) < m.height-1 {
		lines = append(lines, "")
	}
	if m.mode == menuMode {
		lines = m.overlay(lines, m.menuBox())
	}
	return strings.Join(append(lines, m.keyLine()), "\n")
}

// headLine is the brand, the watcher's state and release, the machine's
// memory and, at the right, the clock.
func (m *guardView) headLine() string {
	parts := []string{bold.Render("[WorkBench] Guard memory") + " "}
	if m.connected {
		parts = append(parts, green.Render(viewText("●"))+" watching", m.release)
	} else {
		parts = append(parts, yellow.Render(viewText("○"))+" not running")
	}
	if mem := m.snap.Memory; m.connected && mem.Level != "" {
		level := mem.Level
		switch level {
		case "warning":
			level = yellow.Render(level)
		case "critical", "low":
			level = red.Render(level)
		}
		parts = append(parts, "memory "+level, memoryText(mem))
	}
	clock := time.Now().Format("3:04:05 PM")
	left := fit(strings.Join(parts[:1], "")+strings.Join(parts[1:], viewText(" · ")),
		max(m.width-len(clock)-2, 1), false)
	return left + strings.Repeat(" ", max(m.width-lipgloss.Width(left)-len(clock), 1)) + clock
}

// memoryText is the machine's memory as the watcher reads it: on Linux the
// memory available of the total user memory may take, on macOS, which gives
// a pressure level instead, the installed memory and the swap in use.
func memoryText(mem guard.MemoryView) string {
	if mem.Total > 0 {
		return memorySize(mem.Available) + " of " + memorySize(mem.Total) + " available"
	}
	text := memorySize(mem.Installed) + " installed"
	if mem.SwapTotal > 0 {
		text += viewText(" · ") + "swap " + memorySize(mem.SwapTotal-mem.SwapFree) + " of " +
			memorySize(mem.SwapTotal) + " used"
	}
	return text
}

// memorySize is a size as the view shows it: whole MB under 1 GB, else
// [guard.Gigabytes]; an em dash for none.
func memorySize(bytes uint64) string {
	switch {
	case bytes == 0:
		return "—"
	case bytes < 1<<30:
		return fmt.Sprintf("%.0f MB", float64(bytes)/(1<<20))
	}
	return guard.Gigabytes(bytes)
}

// countLine counts the running commands, those that finished in the last
// minute, and gives the largest peak since the watcher started.
func (m *guardView) countLine() string {
	running := 0
	for _, c := range m.snap.Commands {
		if active(c.State) {
			running++
		}
	}
	line := fmt.Sprintf("%d running · %d finished in the last minute · largest peak %s",
		running, len(m.snap.Commands)-running, memorySize(m.snap.LargestPeak))
	return fit(viewText(line), m.width, false)
}

// statusLine says why no watcher answers, or, when a shown command is a dry
// run, that stops are only logged; else it is blank.
func (m *guardView) statusLine() string {
	if !m.connected && m.problem != "" {
		return fit(yellow.Render(m.problem), m.width, false)
	}
	if slices.ContainsFunc(m.snap.Commands, func(c guard.CommandView) bool { return c.DryRun }) {
		return yellow.Render("dry run: stops are logged, not sent")
	}
	return ""
}

// guardColumns are the list's columns. COMMAND shrinks first, to 20 cells,
// then SUBAGENT, PROJECT, PEAK and AGENT drop in that order, then COMMAND
// shrinks further.
var guardColumns = []column{
	{Head: "ID"},
	{Head: "STATUS"},
	{Head: "AGENT", Drop: 1},
	{Head: "SUBAGENT", Drop: 4},
	{Head: "TIME", Right: true},
	{Head: "MEMORY", Right: true},
	{Head: "PEAK", Right: true, Drop: 2},
	{Head: "PROJECT", Drop: 3},
	{Head: "COMMAND", Clip: true, Keep: 20},
}

// guardRowPrefix is the cells before a row: the selection mark, the tag mark and
// a space.
const guardRowPrefix = 3

// tableLines are the column heads and as many rows as fit in room lines,
// scrolled so the selected row shows.
func (m *guardView) tableLines(room int) []string {
	if len(m.snap.Commands) == 0 {
		if m.connected {
			return []string{faint.Render("No agent commands are running.")}
		}
		return nil
	}
	spec := tableSpec{Cols: guardColumns, Header: true}
	for _, c := range m.snap.Commands {
		spec.Rows = append(spec.Rows, m.cells(c))
	}
	spec = spec.plain()
	shown, widths, _ := layout(m.width, guardRowPrefix, spec)
	used := guardRowPrefix + gap*(len(shown)-1)
	for _, i := range shown {
		if !guardColumns[i].Clip {
			used += widths[i]
		}
	}
	widths[len(guardColumns)-1] = max(m.width-used, 1)
	heads := make([]string, len(guardColumns))
	for i, c := range guardColumns {
		heads[i] = c.Head
	}
	head := strings.Join(padded(heads, shown, widths), strings.Repeat(" ", gap))
	lines := []string{faint.Render(strings.Repeat(" ", guardRowPrefix) + head)}
	visible := max(room-1, 1)
	first := max(m.cursor-visible+1, 0)
	for i := first; i < min(first+visible, len(spec.Rows)); i++ {
		lines = append(lines, m.styledRow(i, spec.Rows[i], shown, widths))
	}
	return lines
}

// cells are a command's cells in the list: short ID, status, agent,
// subagent, run time, memory now, peak, project and command on one line.
func (m *guardView) cells(c guard.CommandView) []string {
	took, now := "", "—"
	// A command that ended this tick is still tracked, without its end time.
	end := m.snap.At
	if !active(c.State) && !c.Ended.IsZero() {
		end = c.Ended
	}
	if active(c.State) {
		now = memorySize(c.Memory)
	}
	if !c.Started.IsZero() && end.After(c.Started) {
		took = tookValue(end.Sub(c.Started).Seconds())
	}
	return []string{
		shortCallID(c.ID),
		viewText(statusText(c.State)),
		c.Agent,
		c.Subagent,
		took,
		now,
		memorySize(
			c.Peak,
		),
		c.Project,
		strings.Join(strings.Fields(guard.CommandRest(c.Command)), " "),
	}
}

// statusText is a state as the list shows it: a mark that carries the state
// without color, then a word.
func statusText(state string) string {
	switch state {
	case "running":
		return "● run"
	case "stopping":
		return "◌ stopping"
	case "done":
		return "✓ done"
	case "unmatched":
		return "? unmatched"
	case "stopped":
		return "✗ stopped"
	}
	return state
}

// statusStyle colors a state: running green, stopping and unmatched yellow,
// stopped red, done plain.
func statusStyle(state string) lipgloss.Style {
	switch state {
	case "running":
		return green
	case "stopping", "unmatched":
		return yellow
	case "stopped":
		return red
	}
	return lipgloss.NewStyle()
}

// padded are cells of the shown columns, each fitted to its width and
// padded on its side, the last left unpadded.
func padded(cells []string, shown, widths []int) []string {
	out := make([]string, 0, len(shown))
	for _, i := range shown {
		cell := fit(cells[i], widths[i], false)
		pad := strings.Repeat(" ", max(widths[i]-ansi.StringWidth(cell), 0))
		if guardColumns[i].Right {
			cell = pad + cell
		} else if i != len(guardColumns)-1 {
			cell += pad
		}
		out = append(out, cell)
	}
	return out
}

// styledRow is row i with its marks: the selected row bold behind ▸, a
// tagged one marked *, a finished one or any while no watcher answers faint,
// the status in its color.
func (m *guardView) styledRow(i int, cells []string, shown, widths []int) string {
	c := m.snap.Commands[i]
	style := lipgloss.NewStyle()
	if !active(c.State) || !m.connected {
		style = style.Faint(true)
	}
	mark, tag := " ", " "
	if i == m.cursor && m.mode != detailMode {
		mark, style = viewText("▸"), style.Bold(true)
	}
	if m.tagged[c.ID] {
		tag = yellow.Render("*")
	}
	out := padded(cells, shown, widths)
	for n, col := range shown {
		if col == 1 {
			out[n] = statusStyle(c.State).Inherit(style).Render(out[n])
		} else {
			out[n] = style.Render(out[n])
		}
	}
	return mark + tag + " " + strings.Join(out, strings.Repeat(" ", gap))
}

// detailLines are the selected command's details in at most room lines: the
// full command, folder, agent, subagent, full ID, status and run time,
// memory now and at its peak, a graph of its memory, and its processes.
func (m *guardView) detailLines(room int) []string {
	if m.cursor >= len(m.snap.Commands) {
		return nil
	}
	c := m.snap.Commands[m.cursor]
	cells := m.cells(c)
	status := viewText(statusText(c.State))
	if c.By != "" {
		status += " by " + c.By
	}
	if cells[4] != "" {
		status += viewText(" · ") + cells[4]
	}
	label := func(name, value string) []string {
		var b strings.Builder
		writeText(&b, m.width-12, 0, value)
		lines := strings.Split(strings.TrimSuffix(b.String(), "\n"), "\n")
		for i := range lines {
			head := strings.Repeat(" ", 12)
			if i == 0 {
				head = faint.Render(fmt.Sprintf("  %-10s", name))
			}
			lines[i] = head + lines[i]
		}
		return lines
	}
	lines := []string{""}
	lines = append(lines, label("Command", c.Command)...)
	lines = append(lines, label("Folder", showPath(c.Cwd))...)
	lines = append(lines, label("Agent", c.Agent)...)
	lines = append(lines, label("Subagent", cmp.Or(c.Subagent, "—"))...)
	lines = append(lines, label("ID", c.ID)...)
	lines = append(lines, label("Status", status)...)
	lines = append(lines, label("Memory", cells[5]+" now"+viewText(" · ")+"peak "+cells[6])...)
	lines = append(lines, label("Graph", graph(c.History, c.Peak, m.width-14))...)
	lines = append(lines, label("Processes", processCount(len(c.Tree)))...)
	for _, p := range c.Tree {
		lines = append(lines, fmt.Sprintf("%12s%-8d %-20s %10s", "", p.PID, fit(p.Name, 20, false),
			memorySize(p.Memory)))
	}
	if len(lines) > room {
		lines = append(lines[:room-1], faint.Render(fmt.Sprintf("%12s%s %d more", "",
			glyphs.Ellipsis, len(lines)-room+1)))
	}
	return lines
}

// processCount says how many processes a command's tree holds now.
func processCount(n int) string {
	switch n {
	case 0:
		return "none running"
	case 1:
		return "1 process"
	}
	return fmt.Sprintf("%d processes", n)
}

// graph is a one-line chart of the last readings, at most width and 120 of
// them, each a bar from ▁ to █ scaled to peak.
func graph(history []uint64, peak uint64, width int) string {
	bars := []rune("▁▂▃▄▅▆▇█")
	history = history[max(len(history)-min(graphReadings, max(width, 1)), 0):]
	if len(history) == 0 {
		return "—"
	}
	var b strings.Builder
	for _, v := range history {
		level := 0
		if peak > 0 {
			level = min(int(v*uint64(len(bars))/peak), len(bars)-1)
		}
		b.WriteRune(bars[level])
	}
	return viewText(b.String())
}

// menuBox is the stop menu: a box titled with the command and its memory, or
// the number of tagged commands, and the two stops, the chosen one marked.
func (m *guardView) menuBox() []string {
	ids := m.stopTargets(m.menuFrom)
	title := fmt.Sprintf("Stop %d tagged commands", len(ids))
	if len(ids) == 1 {
		i := slices.IndexFunc(
			m.snap.Commands,
			func(c guard.CommandView) bool { return c.ID == ids[0] },
		)
		if i >= 0 {
			c := m.snap.Commands[i]
			text := strings.Join(strings.Fields(guard.CommandRest(c.Command)), " ")
			title = "Stop " + fit(text, max(min(m.width-24, 40), 10), false) + " (" +
				memorySize(c.Memory) + ")"
		}
	}
	lines := []string{bold.Render(viewText(title)), ""}
	for i, choice := range stopChoices {
		if i == m.menu {
			lines = append(lines, bold.Render(viewText("▸ "+choice)))
		} else {
			lines = append(lines, "  "+choice)
		}
	}
	box := lipgloss.NewStyle().Border(glyphs.border).Padding(0, 1).Render(strings.Join(lines, "\n"))
	return strings.Split(box, "\n")
}

// overlay draws box over the middle of lines, keeping what lies left and
// right of it.
func (m *guardView) overlay(lines, box []string) []string {
	boxWidth := lipgloss.Width(strings.Join(box, "\n"))
	left := max((m.width-boxWidth)/2, 0)
	top := max((len(lines)-len(box))/2, 3)
	for i, row := range box {
		at := top + i
		if at >= len(lines) {
			break
		}
		base := lines[at] + strings.Repeat(" ", max(m.width-ansi.StringWidth(lines[at]), 0))
		lines[at] = ansi.Truncate(base, left, "") + row + ansi.TruncateLeft(base, left+boxWidth, "")
	}
	return lines
}

// keyLine is the notice while it shows, else the keys the screen takes.
func (m *guardView) keyLine() string {
	if m.notice != "" && time.Now().Before(m.noticeUntil) {
		return fit(viewText(m.notice), m.width, false)
	}
	keys := [][2]string{
		{"↑/↓", "move"}, {"space", "tag"}, {"enter", "details"}, {"k", "stop"}, {"q", "quit"},
	}
	switch m.mode {
	case listMode:
	case menuMode:
		keys = [][2]string{{"↑/↓", "choose"}, {"enter", "send"}, {"esc", "cancel"}}
	case detailMode:
		keys = [][2]string{{"k", "stop"}, {"esc", "back"}, {"q", "quit"}}
	}
	parts := make([]string, len(keys))
	for i, key := range keys {
		parts[i] = bold.Render(viewText(key[0])) + " " + faint.Render(key[1])
	}
	return fit(strings.Join(parts, "  "), m.width, false)
}
