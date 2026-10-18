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

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"
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
			"commands that ended in the last minute, dimmed. The header counts the running and " +
			"finished commands and shows the memory in use of the installed memory, the largest " +
			"peak and the swap in use. ↑ and ↓ move, as the mouse wheel does, and a click " +
			"selects a row; space tags a running command, enter, → or a click on the selected " +
			"row opens its details (the full command, one step of a pipeline or && chain per " +
			"line, its folder, processes and memory graph, which scroll with ↑, ↓, pgup, pgdn, " +
			"home and end and where a drag of the mouse selects text), k opens the stop menu for the " +
			"tagged commands or the selected one (a polite stop, force-killed after 5 s, or a " +
			"forced kill now), esc or ← closes a menu or the details, and q, or esc on the " +
			"list, quits. The watcher signals the command, logs the stop as by=you " +
			"and tells the agent; with WORKBENCH_GUARD=dry-run it only logs. While no watcher " +
			"runs, the view says so and reconnects on its own. It needs a terminal, has no " +
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
	// The color profile Bubble Tea reports arrives after the first frame:
	// until then the view decides as every other colored output does.
	model := &guardView{
		width:   terminalWidth(os.Stdout),
		height:  24,
		tagged:  map[string]bool{},
		noColor: !bool(newPainter(os.Stdout)),
		details: detailsViewport(),
	}
	program := tea.NewProgram(
		model,
		tea.WithInput(os.Stdin),
		tea.WithOutput(os.Stdout),
		tea.WithContext(cmd.Context()),
		// main owns SIGINT and SIGTERM through the command's context.
		tea.WithoutSignalHandler(),
	)
	_, err := program.Run()
	// However the view ended, the terminal must not keep sending the wheel
	// as arrow keys to the shell.
	model.altScroll.disarm(os.Stdout)
	if model.client != nil {
		_ = model.client.Close()
	}
	return err
}

// guardView is the live view: the watcher's latest snapshot, the selected
// row, the tagged rows by call ID, what the screen shows (the list, the stop
// menu or one command's details), the menu's chosen line, a short notice on
// the bottom line until noticeUntil, the terminal's size, and the connection:
// whether a watcher answers, and why not when none does. The rows stay after
// a disconnect, dimmed.
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
	connected   bool
	problem     string
	// dialing is set while a connection attempt runs, so a tick never starts
	// a second one: a watcher that never answers holds an attempt 3 s.
	dialing bool
	// menuFrom is the screen the stop menu opened from and returns to.
	menuFrom viewMode
	// targets are the call IDs the open stop menu names, fixed when it opens,
	// so a stop never falls to another row once they finish.
	targets []string
	// light is set once the terminal reports a light background; until then,
	// and where it never answers, the view draws for a dark one.
	light bool
	// noColor is set when the terminal shows no color (a NO_COLOR or dumb
	// terminal), so the selected row is marked bold instead of by its band.
	noColor bool
	// altScroll is the view's hold on the terminal's alternate scroll mode.
	altScroll altScroll
	// details is the details' scrolling pane, its content and size set as
	// each frame draws it, so a new snapshot keeps its scroll offset.
	details viewport.Model
	// first is the first list row shown, and rowsTop the screen line it is
	// drawn on, as the last frame drew them, so a click finds its row; shown
	// is how many rows that frame drew.
	first, rowsTop, shown int
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

// connectedMsg is a connection attempt's outcome: a client that said hello,
// or why no watcher answered.
type connectedMsg struct {
	client  *guard.Client
	problem string
}

// The texts the view shows when no watcher answers.
const (
	watcherAbsent = "The guard's watcher is not running; it starts with the next agent " +
		"command."
	watcherOlder = "The watcher runs an older Workbench release that does not serve this " +
		"view; run workbench update."
	watcherOther = "The watcher runs a Workbench release that speaks another protocol " +
		"version; run workbench update."
)

// The view's sizes: the narrowest and the shortest window it draws in (the
// four header lines and the six-line stop menu below them, with the key
// line), how long a notice stays, the readings the memory graph shows at
// most, and the widest a project name is shown before it is clipped.
const (
	guardViewMinWidth  = 60
	guardViewMinHeight = 11
	noticeShown        = 3 * time.Second
	graphReadings      = 120
	projectWidth       = 32
)

// The two stops the menu offers, polite first, as the guard's own stops go.
var stopChoices = []string{"Stop (polite, force-killed after 5 s)", "Force kill now"}

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
// hello without a protocol version in common means another release; any
// other failed hello is shown as it is.
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
	if err = client.Hello(); err == nil {
		return connectedMsg{client: client}
	}
	_ = client.Close()
	switch {
	case errors.Is(err, os.ErrDeadlineExceeded):
		return connectedMsg{problem: watcherOlder}
	case errors.Is(err, guard.ErrProtocolVersion):
		return connectedMsg{problem: watcherOther}
	}
	// A watcher answered the dial, so one runs: it may be exiting or handing
	// over, which the next attempt finds out.
	return connectedMsg{problem: "The watcher did not finish the hello (" + err.Error() +
		")."}
}

// Init connects, starts the clock, asks the terminal for its background,
// which picks the selected row's band, and turns on alternate scroll
// (altscroll.go).
func (m *guardView) Init() tea.Cmd {
	m.dialing = true
	return tea.Batch(connectWatcher, nextTick(), tea.RequestBackgroundColor, m.altScroll.arm())
}

// Update takes a message. Each tick schedules the next and, while no watcher
// answers and no attempt runs, tries again.
func (m *guardView) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	m.altScroll.note(msg)
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
	case tea.BackgroundColorMsg:
		m.light = !msg.IsDark()
	case tea.ColorProfileMsg:
		m.noColor = msg.Profile <= colorprofile.ASCII
	case tea.MouseClickMsg:
		if m.mode == listMode && msg.Button == tea.MouseLeft {
			m.click(msg.Y)
		}
	case tea.MouseWheelMsg:
		// In the details the view turns mouse reporting off, so the wheel
		// comes as ↑ and ↓ (alternate scroll, altscroll.go); a wheel event
		// the terminal sent before reporting went off still scrolls them.
		switch {
		case m.mode == detailMode:
			m.details, _ = m.details.Update(msg)
		case m.mode != listMode:
		case msg.Button == tea.MouseWheelUp:
			m.pressList("up")
		case msg.Button == tea.MouseWheelDown:
			m.pressList("down")
		}
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
		m.client, m.connected, m.problem = msg.client, true, ""
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
		return m.press(msg)
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
		if m.mode == detailMode {
			m.mode = listMode
		}
		m.menuFrom = listMode
	} else {
		m.cursor = at
	}
	for id := range m.tagged {
		if !slices.Contains(m.running([]string{id}), id) {
			delete(m.tagged, id)
		}
	}
	if m.mode == menuMode && len(m.running(m.targets)) == 0 {
		m.mode = m.menuFrom
		m.say(shortIDs(m.targets) + " already finished; nothing was stopped")
	}
}

// running are those of ids whose command still runs, in the order given.
func (m *guardView) running(ids []string) []string {
	var out []string
	for _, id := range ids {
		i := slices.IndexFunc(m.snap.Commands, func(c guard.CommandView) bool { return c.ID == id })
		if i >= 0 && active(m.snap.Commands[i].State) {
			out = append(out, id)
		}
	}
	return out
}

// shortIDs are call IDs as the view shows them, joined with commas.
func shortIDs(ids []string) string {
	short := make([]string, len(ids))
	for i, id := range ids {
		short[i] = shortCallID(id)
	}
	return strings.Join(short, ", ")
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

// answered shows the watcher's answer to a stop: "stopped 4fQ8nX2a",
// "logged 4fQ8nX2a (dry run)" for a dry-run command, which goes on running,
// or the ID and why not ("4fQ8nX2a already finished").
func (m *guardView) answered(results []guard.StopResult) {
	parts := make([]string, 0, len(results))
	for _, r := range results {
		dry := slices.ContainsFunc(m.snap.Commands, func(c guard.CommandView) bool {
			return c.ID == r.ID && c.DryRun
		})
		switch {
		case r.OK && dry:
			parts = append(parts, "logged "+shortCallID(r.ID)+" (dry run)")
		case r.OK:
			parts = append(parts, "stopped "+shortCallID(r.ID))
		default:
			parts = append(parts, shortCallID(r.ID)+" "+r.Error)
		}
	}
	m.say(strings.Join(parts, "; "))
}

// press handles a key in the present mode; q and ctrl+c quit from any, esc
// from the list, as in the costs view. In the details the keys that scroll
// go to their pane.
func (m *guardView) press(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	if key == "q" || key == "ctrl+c" || (key == "esc" && m.mode == listMode) {
		return m, tea.Quit
	}
	switch m.mode {
	case menuMode:
		return m, m.pressMenu(key)
	case detailMode:
		switch key {
		case "esc", "left", "h":
			m.mode = listMode
		case "k":
			m.openMenu(detailMode)
		case "home":
			m.details.GotoTop()
		case "end":
			m.details.GotoBottom()
		default:
			var cmd tea.Cmd
			m.details, cmd = m.details.Update(msg)
			return m, cmd
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
	case "enter", "right", "l":
		if m.cursor < rows {
			m.openDetails()
		}
	case "k":
		m.openMenu(listMode)
	}
}

// pressMenu handles a key in the stop menu: ↑ and ↓ choose, enter sends and
// esc or ← cancels.
func (m *guardView) pressMenu(key string) tea.Cmd {
	switch key {
	case "up":
		m.menu = max(m.menu-1, 0)
	case "down":
		m.menu = min(m.menu+1, len(stopChoices)-1)
	case "esc", "left", "h":
		m.mode = m.menuFrom
	case "enter":
		m.mode = m.menuFrom
		ids := m.running(m.targets)
		var gone []string
		for _, id := range m.targets {
			if !slices.Contains(ids, id) {
				gone = append(gone, id)
			}
		}
		cmd := m.send(ids, m.menu == 1)
		if len(gone) > 0 {
			m.say(strings.TrimPrefix(m.notice+"; ", "; ") + shortIDs(gone) + " already finished")
		}
		return cmd
	}
	return nil
}

// click selects the list row on screen line y; a click on the selected row
// opens its details. A click on the header, the column heads or below the
// rows does nothing.
func (m *guardView) click(y int) {
	at := y - m.rowsTop
	if at < 0 || at >= m.shown || m.first+at >= len(m.snap.Commands) {
		return
	}
	if m.first+at == m.cursor {
		m.openDetails()
		return
	}
	m.cursor = m.first + at
}

// openDetails shows the selected command's details from their top.
func (m *guardView) openDetails() {
	m.mode = detailMode
	m.details.GotoTop()
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
	ids := m.stopTargets(from)
	switch {
	case len(ids) == 0 && m.cursor < len(m.snap.Commands):
		m.say(shortCallID(m.selectedID()) + " already finished")
	case len(ids) == 0:
	case !m.connected:
		m.say("No watcher is connected; nothing was stopped")
	default:
		m.mode, m.menu, m.menuFrom, m.targets = menuMode, 0, from, ids
	}
}

// send asks the watcher to stop ids, politely or by force, without waiting:
// its answer comes back as a stoppedMsg, and the rows follow the stop.
func (m *guardView) send(ids []string, force bool) tea.Cmd {
	client := m.client
	if len(ids) == 0 || client == nil {
		m.notice = ""
		return nil
	}
	m.say("asking the watcher to stop " + shortIDs(ids))
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

// View draws the screen in the alternate screen, or asks for a wider or a
// taller window, so the key line and its notices always show. On the list
// mouse reporting is on, as the apply checklist does (checklist.go): without
// it a trackpad or wheel scrolls the terminal's own buffer and the screen
// slides away; with it the wheel moves and a click selects. In the details
// it is off, so a plain drag selects text, and the terminal sends a wheel
// step as ↑ or ↓, which scroll the details, because the view turned
// alternate scroll mode on (altscroll.go); mouse reporting takes precedence
// over it. The stop menu keeps the mouse taken, so a wheel step there is
// dropped (Update) instead of arriving as ↑ or ↓ and moving its choice.
func (m *guardView) View() tea.View {
	var text string
	m.shown = 0
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
	view.MouseMode = tea.MouseModeCellMotion
	if m.mode == detailMode {
		view.MouseMode = tea.MouseModeNone
	}
	return view
}

// screen is the header (the head line, a blank line, the counts and the
// status lines), the list or the details with the stop menu over them when it
// is open, and the key line at the bottom.
func (m *guardView) screen() string {
	lines := append([]string{m.headLine(), "", m.countLine()}, m.statusLines()...)
	header := len(lines)
	room := max(m.height-len(lines)-2, 1)
	if m.inDetails() {
		// The pane takes its new size first, so the content it then gets
		// pulls an offset past its end back.
		m.details.SetWidth(m.width)
		m.details.SetHeight(room + 1)
		m.details.SetContentLines(m.detailLines())
		lines = append(lines, strings.Split(m.details.View(), "\n")...)
	} else {
		// The column heads come first, then the rows.
		m.rowsTop = header + 1
		lines = append(lines, m.tableLines(room+1)...)
	}
	for len(lines) < m.height-1 {
		lines = append(lines, "")
	}
	if m.mode == menuMode {
		lines = m.overlay(lines, m.menuBox(), header)
	}
	return strings.Join(append(lines, m.keyLine()), "\n")
}

// inDetails reports whether the screen shows the details, alone or under the
// stop menu.
func (m *guardView) inDetails() bool {
	return m.mode == detailMode || (m.mode == menuMode && m.menuFrom == detailMode)
}

// headLine is the brand, the watcher's state, a badge when memory is low or
// critical, and the clock, on one line; a window too narrow for all of them
// loses the clock.
func (m *guardView) headLine() string {
	line := bold.Render("[WorkBench] Guard memory") + "  "
	if m.connected {
		line += guardGreen.Render(forTerm("●")) + " watching"
	} else {
		state := " not connected"
		if m.problem == watcherAbsent {
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
	if clock := line + dot + time.Now().Format("3:04:05 PM"); ansi.StringWidth(clock) <= m.width {
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

// countLine counts the running commands and those that finished in the last
// minute and, once a watcher has sent a snapshot, gives the memory in use of the
// installed memory, the largest peak since the watcher started and the swap
// in use, the memory figure bold and the rest faint. A narrow window drops
// the swap, then the peak, then clips the line.
func (m *guardView) countLine() string {
	running := 0
	for _, c := range m.snap.Commands {
		if active(c.State) {
			running++
		}
	}
	counts := fmt.Sprintf("%d running · %d finished", running, len(m.snap.Commands)-running)
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

// statusLines say why no watcher answers or, when shown commands are dry
// runs, that their stops are only logged: for all of them, or for how many;
// else they are one blank line. A long text wraps to the window; the dry-run
// texts fit 60 columns, so the stop menu never covers a second line of them.
func (m *guardView) statusLines() []string {
	text := ""
	dry := 0
	for _, c := range m.snap.Commands {
		if c.DryRun {
			dry++
		}
	}
	switch {
	case !m.connected && m.problem != "":
		text = m.problem
	case dry > 0 && dry == len(m.snap.Commands):
		text = "dry run: stops are logged, not sent"
	case dry > 0:
		text = fmt.Sprintf("dry run on %d of %d commands: stops are logged, not sent",
			dry, len(m.snap.Commands))
	}
	if text == "" {
		return []string{""}
	}
	lines := strings.Split(ansi.Wrap(forTerm(text), m.width, ""), "\n")
	for i, line := range lines {
		lines[i] = guardGold.Render(line)
	}
	return lines
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
// selection leaves it, so a row stays under the pointer that clicked it.
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
	head := strings.Join(padded(guardColumns, heads, shown, widths), strings.Repeat(" ", gap))
	lines := []string{faint.Render(strings.Repeat(" ", guardRowPrefix) + head)}
	visible := max(room-1, 1)
	m.first = min(max(m.first, m.cursor-visible+1), m.cursor, max(len(spec.Rows)-visible, 0))
	m.first = max(m.first, 0)
	for i := m.first; i < min(m.first+visible, len(spec.Rows)); i++ {
		lines = append(lines, m.styledRow(i, spec.Rows[i], shown, widths))
	}
	m.shown = len(lines) - 1
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
		forTerm(statusText(c.State, c.DryRun)),
		c.Agent,
		c.Subagent,
		took,
		now,
		memorySize(c.Peak),
		projectLabel(c.Project, projectWidth),
		strings.Join(strings.Fields(guard.CommandRest(c.Command)), " "),
	}
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

// statusText is a state as the list shows it: a mark that carries the state
// without color, then a word, and "(dry)" for a dry-run command, whose stops
// are only logged.
func statusText(state string, dry bool) string {
	text := state
	switch state {
	case "running":
		text = "● run"
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

// sizeStyles are the styles of a command's memory now (while it runs; a
// finished one shows none) and of its peak, each nil below a gigabyte.
func sizeStyles(c guard.CommandView) (now, peak *lipgloss.Style) {
	if active(c.State) {
		now = sizeStyle(c.Memory)
	}
	return now, sizeStyle(c.Peak)
}

// statusStyle colors a state: running green, stopping gold and bold,
// stopped red, unmatched a dim gold, done plain.
func statusStyle(state string) lipgloss.Style {
	switch state {
	case "running":
		return guardGreen
	case "stopping":
		return guardGold.Bold(true)
	case "stopped":
		return guardRed
	case "unmatched":
		return guardDimGold
	}
	return lipgloss.NewStyle()
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
	c := m.snap.Commands[i]
	style := lipgloss.NewStyle()
	if !active(c.State) || !m.connected {
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
		markStyle = band.Foreground(pick(guardAccentLight, guardAccentDark)).Bold(true)
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

// onBand is s as the selected row draws it on the light band or the dark
// one: its foreground swapped for the one [guardOnBand] lists, so it keeps at
// least 3:1 contrast with the band, and the rest of s as it is.
func onBand(s lipgloss.Style, light bool) lipgloss.Style {
	if lifted, ok := guardOnBand[light][s.GetForeground()]; ok {
		return s.Foreground(lifted)
	}
	return s
}

// detailLines are the selected command's details, every line of them, in
// this order: a blank line, the full command, one step of it per line, its
// folder, agent, subagent, full ID, status and run time, memory now and at
// its peak, its processes, and a graph of its memory. The details pane
// scrolls through them; none is cut.
func (m *guardView) detailLines() []string {
	if m.cursor >= len(m.snap.Commands) {
		return nil
	}
	c := m.snap.Commands[m.cursor]
	cells := m.cells(c)
	status := statusStyle(c.State).Render(forTerm(statusText(c.State, c.DryRun)))
	if c.By != "" {
		status += " by " + c.By
	}
	if cells[4] != "" {
		status += forTerm(" · ") + cells[4]
	}
	lines := []string{""}
	// add lays out a field's values under its label, a value after the first
	// starting on its own line two spaces in.
	add := func(name string, values ...string) {
		head := faint.Render(fmt.Sprintf("  %-10s", name))
		for i, value := range values {
			var b strings.Builder
			writeText(&b, m.width-12, min(i, 1)*2, value)
			for line := range strings.SplitSeq(strings.TrimSuffix(b.String(), "\n"), "\n") {
				lines = append(lines, head+line)
				head = strings.Repeat(" ", 12)
			}
		}
	}
	add("Command", commandSteps(c.Command)...)
	add("Folder", showPath(c.Cwd))
	add("Agent", c.Agent)
	add("Subagent", cmp.Or(c.Subagent, "—"))
	add("ID", c.ID)
	add("Status", status)
	now, peak := cells[memoryColumn], cells[peakColumn]
	nowStyle, peakStyle := sizeStyles(c)
	if nowStyle != nil {
		now = nowStyle.Render(now)
	}
	if peakStyle != nil {
		peak = peakStyle.Render(peak)
	}
	add("Memory", now+" now"+forTerm(" · ")+"peak "+peak)
	add("Processes", processCount(len(c.Tree)))
	for _, p := range c.Tree {
		lines = append(lines, forTerm(fmt.Sprintf("%12s%-8d %-20s %10s", "", p.PID,
			fit(p.Name, 20, false), memorySize(p.Memory))))
	}
	add("Graph", graph(c.History, c.Peak, m.width-14))
	return lines
}

// commandSteps splits a shell command into the steps a reader follows, as
// the Google Shell Style Guide lays out a long pipeline, one step per line
// (https://google.github.io/styleguide/shellguide.html#s5.5-pipelines), and
// as it does && and ||: a step after the first starts with the |, && or ||
// before it, and a ; ends its step and is dropped. Only operators that stand
// as tokens split: none inside single or double quotes, $' ' or backquotes,
// after a backslash, or inside $( ), ( ), ${ }, a { } group, [[ ]], where
// || and && are a test's own, or a case … esac, whose patterns and arms are
// its own; not the & of 2>&1, the |& that pipes both streams or the >| of a
// forced overwrite; and none in a comment, from a # that starts a word to
// the end of its line. A command
// holding a newline outside quotes, such as one with a here-document, is
// already laid out by its author, and its body is data: it stays one step,
// as does a command with no separator.
func commandSteps(command string) []string {
	var steps []string
	var step strings.Builder
	var open []string
	quote, op := byte(0), ""
	// cut ends the step, which op started, and starts one with next.
	cut := func(next string) {
		if text := strings.TrimSpace(step.String()); text != "" {
			steps = append(steps, strings.TrimSpace(op+" "+text))
		}
		step.Reset()
		op = next
	}
	for i := 0; i < len(command); i++ {
		n := quoted(&quote, command, i)
		if n == 0 {
			switch {
			case command[i] == '\n':
				return []string{command}
			case command[i] == '#' && wordStart(command, i):
				// A comment ends at the newline, which is then seen.
				if j := strings.IndexByte(command[i:], '\n'); j >= 0 {
					n = j
				} else {
					n = len(command) - i
				}
			default:
				n = nesting(&open, command, i)
			}
		}
		if n == 0 && len(open) == 0 {
			rest := command[i:]
			switch {
			case strings.HasPrefix(rest, "&&"), strings.HasPrefix(rest, "||"):
				cut(rest[:2])
				i++
				continue
			case strings.HasPrefix(rest, "|&"), strings.HasPrefix(rest, ">|"):
				n = 2
			case rest[0] == '|':
				cut("|")
				continue
			case rest[0] == ';':
				cut("")
				continue
			}
		}
		n = max(n, 1)
		step.WriteString(command[i : i+n])
		i += n - 1
	}
	cut("")
	if len(steps) == 0 {
		return []string{command}
	}
	return steps
}

// quoted is how many bytes of command from i are quoted text: an escaped
// byte, a byte inside quotes, or the quote that opens or closes them, as
// quote, the open quote or 0, records; $' ' is quote '$', in which a
// backslash escapes. It is 0 for a byte outside quotes.
func quoted(quote *byte, command string, i int) int {
	ch := command[i]
	switch {
	case *quote == '\'':
		if ch == '\'' {
			*quote = 0
		}
		return 1
	case ch == '\\' && i+1 < len(command) && (*quote != 0 || command[i+1] != '\n'):
		// A backslash before a newline outside quotes goes on the line
		// after, so the newline is seen.
		return 2
	case *quote != 0:
		if *quote == '$' && ch == '\'' || *quote != '$' && ch == *quote {
			*quote = 0
		}
		return 1
	case strings.HasPrefix(command[i:], "$'"):
		*quote = '$'
		return 2
	case ch == '\'' || ch == '"' || ch == '`':
		*quote = ch
		return 1
	}
	return 0
}

// nesting is how many bytes of command from i open or close something no
// operator inside splits, which open records: ( or $(, ${, a { group, [[ or
// case, each closed by its own ), }, }, ]] or esac. A {, [[ or case opens, and
// a group's }, a ]] or esac closes, only as a word of its own; a case
// pattern's ) closes nothing.
func nesting(open *[]string, command string, i int) int {
	top := ""
	if len(*open) > 0 {
		top = (*open)[len(*open)-1]
	}
	rest, word := command[i:], wordStart(command, i)
	switch {
	case strings.HasPrefix(rest, "${"):
		*open = append(*open, "${")
		return 2
	case rest[0] == '(':
		*open = append(*open, "(")
		return 1
	case rest[0] == '{' && word && blankAt(command, i+1):
		*open = append(*open, "{")
		return 1
	case strings.HasPrefix(rest, "[[") && word && blankAt(command, i+2):
		*open = append(*open, "[[")
		return 2
	case strings.HasPrefix(rest, "case") && word && blankAt(command, i+4):
		*open = append(*open, "case")
		return 4
	case rest[0] == ')' && top == "(",
		rest[0] == '}' && top == "${",
		rest[0] == '}' && top == "{" && word && wordEnd(command, i+1):
		*open = (*open)[:len(*open)-1]
		return 1
	case strings.HasPrefix(rest, "]]") && top == "[[" && word && wordEnd(command, i+2):
		*open = (*open)[:len(*open)-1]
		return 2
	case strings.HasPrefix(rest, "esac") && top == "case" && word && wordEnd(command, i+4):
		*open = (*open)[:len(*open)-1]
		return 4
	}
	return 0
}

// wordStart reports whether the byte at i of a command starts a word: it is
// the first, or follows a blank or an operator's |, &, ; or (.
func wordStart(command string, i int) bool {
	return i == 0 || strings.IndexByte(" \t;|&(", command[i-1]) >= 0
}

// wordEnd reports whether a word of a command ends at i: at the end, or
// before a blank or an operator's |, &, ; or ).
func wordEnd(command string, i int) bool {
	return i >= len(command) || strings.IndexByte(" \t;|&)", command[i]) >= 0
}

// blankAt reports whether the byte at i of a command is a space or a tab.
func blankAt(command string, i int) bool {
	return i < len(command) && (command[i] == ' ' || command[i] == '\t')
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
	return forTerm(b.String())
}

// menuBox is the stop menu: a box titled with the command and its memory, or
// the number of tagged commands, and the two stops, the chosen one marked.
func (m *guardView) menuBox() []string {
	ids := m.targets
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
		base := lines[at] + strings.Repeat(" ", max(m.width-ansi.StringWidth(lines[at]), 0))
		// Truncate carries the cut part's escape codes along; the reset makes
		// sure no style the line left open, such as the selected row's band,
		// reaches the box's border.
		lines[at] = ansi.Truncate(base, left, "") + ansi.ResetStyle + row +
			ansi.TruncateLeft(base, left+boxWidth, "")
	}
	return lines
}

// keyLine is the notice while it shows, else the keys the screen takes.
func (m *guardView) keyLine() string {
	if m.notice != "" && time.Now().Before(m.noticeUntil) {
		return fit(forTerm(m.notice), m.width, false)
	}
	keys := [][2]string{
		{"↑/↓", "move"}, {"space", "tag"}, {"enter/→", "details"}, {"k", "stop"}, {"q/esc", "quit"},
	}
	switch m.mode {
	case listMode:
	case menuMode:
		keys = [][2]string{{"↑/↓", "choose"}, {"enter", "send"}, {"esc/←", "cancel"}}
	case detailMode:
		keys = [][2]string{{"↑/↓", "scroll"}, {"k", "stop"}, {"esc/←", "back"}, {"q", "quit"}}
	}
	parts := make([]string, len(keys))
	for i, key := range keys {
		parts[i] = bold.Render(forTerm(key[0])) + " " + faint.Render(key[1])
	}
	return fit(strings.Join(parts, "  "), m.width, false)
}
