package cli

import (
	"fmt"
	"os"
	"time"

	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/spf13/cobra"

	"github.com/Sawmonabo/workbench/internal/agent/guard"
	"github.com/Sawmonabo/workbench/internal/operation"
	"github.com/Sawmonabo/workbench/internal/platform"
)

// runGuardView opens the live view of the commands the guard tracks and of
// those that finished, which bare `agent guard` runs. It is for a person:
// with --json, with --non-interactive or without a terminal on stdin and
// stdout, or under a coding agent, it exits 3 before the screen opens.
// Otherwise it runs the view until it quits, as every full-screen view runs
// (runFullScreen). A full-screen view prints nothing a program could read, so
// --json is refused, and --non-interactive counts as no terminal, as --choose
// treats it.
func runGuardView(cmd *cobra.Command, o *options) error {
	if o.json {
		return operation.Fail(
			operation.ExitBlocked,
			"json",
			"agent guard has no JSON output",
			workbenchFix(pathsOnly(), []string{"agent", "guard", "logs", "--json"}),
		)
	}
	if o.nonInteractive || !platform.IsTerminal(os.Stdin.Fd()) ||
		!platform.IsTerminal(os.Stdout.Fd()) {
		return operation.Fail(
			operation.ExitBlocked,
			"terminal",
			"agent guard needs a terminal",
			workbenchFix(pathsOnly(), []string{"agent", "guard", "logs", "--follow"}),
		)
	}
	if operation.AgentSession() {
		return operation.Fail(
			operation.ExitBlocked,
			"agent",
			"agent guard is for a person at a terminal",
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
		pane:    detailsViewport(),
		hist:    viewHistory{keep: guard.HistoryKeepDefault, byID: map[string]*heldRow{}},
		find:    viewFind{input: findInput()},
	}
	_, err := runFullScreen(cmd.Context(), model, os.Stdin, os.Stdout)
	// However the view ended, put the terminal's modes back (wheelmodes.go).
	model.wheel.disarm(os.Stdout)
	if model.client != nil {
		_ = model.client.Close()
	}
	if model.hist.db != nil {
		_ = model.hist.db.Close()
	}
	return err
}

// guardView is the live view: the watcher's latest snapshot, the finished
// commands of the history, the rows the list shows of both, the selected
// row, the tagged rows by call ID, what the screen shows (the list, the stop
// menu or one command's details, with or without the side panel), the menu's
// chosen line, a short notice on the bottom line until noticeUntil, the
// terminal's size, and the connection: whether a watcher answers, and why
// not when none does. The rows stay after a disconnect, dimmed.
type guardView struct {
	client *guard.Client
	// snap is the watcher's latest snapshot: the rows it tracks, as it sent
	// them, and the finished commands the history does not hold yet. snaps
	// counts the snapshots taken.
	snap  guard.Snapshot
	snaps int
	// carried are the finished commands that left the snapshot's pending
	// list, written to the history or dropped, each with the snapshot it left
	// in. Each stays until a read of the history that started after that
	// snapshot, so a row never blinks out between the two.
	carried []carriedRow
	// hist is the history as the view holds it.
	hist viewHistory
	// rows are the list's rows in its order, filtered by the find answer, and
	// branch the tree mark before the ID of each row drawn under its command,
	// by row ID (see treeRows). measured are the rows whose cells the column
	// widths are measured from at each frame; those of the history's rows are
	// kept in hist.widths. total and shown count the rows that run and those
	// that finished, before and after the filter.
	rows         []listRow
	branch       map[string]string
	measured     []int
	total, shown rowCounts
	// byPeak sorts the finished rows by their peak, largest first, in place
	// of newest first.
	byPeak bool
	// find is the find input on the bottom line and its answer.
	find        viewFind
	cursor      int
	tagged      map[string]bool
	mode        viewMode
	menu        int
	notice      string
	noticeUntil time.Time
	width       int
	height      int
	connected   bool
	problem     operation.Warning
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
	// wheel is the view's hold on the terminal modes that make the wheel
	// scroll it.
	wheel wheelModes
	// details is the details' scrolling pane and pane the side panel's, their
	// content and size set as each frame draws them, so a new snapshot keeps
	// their scroll offsets.
	details, pane viewport.Model
	// detail is what the details hold beyond the row: a finished command's
	// parts as read from the history, the `… more` rows and the side panel.
	detail viewDetail
	// first is the first list row shown and page how many rows fit, as the
	// last frame drew them, so the list scrolls only when the selection
	// leaves the window and pgup and pgdn move by what shows.
	first, page int
	// moved is set once a key moved the selection or opened a row: until
	// then the first running row, or the first row when none runs, stays
	// selected while the rows arrive.
	moved bool
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

// tickMsg comes once a second, for the clock, the notice's expiry,
// reconnecting and, with no watcher, reading the history.
type tickMsg time.Time

// The view's sizes: the narrowest and the shortest window it draws in (the
// four header lines and the six-line stop menu below them, with the key
// line), how long a notice stays, the widest a project name is shown before
// it is clipped, the narrowest window with room for the details beside the
// side panel, and the lines of a command and the processes the details show
// whole, and how many of them before a `… more` row when there are more. A
// memory graph shows at most guard.GraphBars bars, as many as it keeps.
const (
	guardViewMinWidth  = 60
	guardViewMinHeight = 11
	noticeShown        = 3 * time.Second
	projectWidth       = 32
	splitWidth         = 100
	commandWhole       = 4
	commandCut         = 3
	processesWhole     = 11
	processesCut       = 10
)

// The two stops the menu offers, polite first, as the guard's own stops go.
var stopChoices = []string{
	fmt.Sprintf("Stop (polite, force-killed after %d s)", guard.KillAfter/time.Second),
	"Force kill now",
}

// nextTick waits a second for the next tickMsg.
func nextTick() tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg { return tickMsg(t) })
}

// Init connects, reads the history, starts the clock, asks the terminal for
// its background, which picks the selected row's band, and turns on the
// modes that make the wheel scroll the view (wheelmodes.go).
func (m *guardView) Init() tea.Cmd {
	m.dialing = true
	m.rebuild()
	return tea.Batch(
		connectWatcher, m.readHistory(), nextTick(), tea.RequestBackgroundColor, m.wheel.arm(),
	)
}

// Update takes a message. Each tick schedules the next and, while no watcher
// answers and no attempt runs, tries again; with no watcher it reads the
// history too, which a snapshot asks for otherwise. Whatever the message, the
// parts of a finished command whose details show and which the view lacks
// are read from the history.
func (m *guardView) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	cmd := m.update(msg)
	return m, tea.Batch(cmd, m.readParts())
}

func (m *guardView) update(msg tea.Msg) tea.Cmd {
	m.wheel.note(msg)
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
	case tea.BackgroundColorMsg:
		m.light = !msg.IsDark()
	case tea.ColorProfileMsg:
		m.noColor = msg.Profile <= colorprofile.ASCII
	case tickMsg:
		return m.tick()
	case connectedMsg:
		m.dialing = false
		if msg.client == nil {
			m.problem = msg.problem
			return nil
		}
		m.client, m.connected, m.problem = msg.client, true, operation.Warning{}
		m.rebuild()
		return waitSnapshot(m.client.Snapshots())
	case snapshotMsg:
		if m.client == nil {
			return nil
		}
		m.take(guard.Snapshot(msg))
		return tea.Batch(waitSnapshot(m.client.Snapshots()), m.readHistory())
	case disconnectedMsg:
		if m.client != nil {
			_ = m.client.Close()
		}
		m.client, m.connected, m.problem = nil, false, watcherAbsent
		if m.mode == menuMode {
			m.mode = m.menuFrom
		}
		m.rebuild()
	case stoppedMsg:
		m.answered(msg)
	case historyMsg:
		return m.tookHistory(msg)
	case foundMsg:
		return m.found(msg)
	case partsMsg:
		m.gotParts(msg)
	case tea.PasteMsg:
		if m.find.open {
			return m.edit(msg)
		}
	case tea.KeyPressMsg:
		return m.press(msg)
	}
	return nil
}

// tick schedules the next tick, reconnects while no watcher answers and no
// attempt runs, and reads the history while none does.
func (m *guardView) tick() tea.Cmd {
	cmds := []tea.Cmd{nextTick()}
	if !m.connected {
		cmds = append(cmds, m.readHistory())
		if !m.dialing {
			m.dialing = true
			cmds = append(cmds, connectWatcher)
		}
	}
	return tea.Batch(cmds...)
}

// say shows text on the bottom line for a few seconds.
func (m *guardView) say(text string) {
	m.notice, m.noticeUntil = text, time.Now().Add(noticeShown)
}

// inDetails reports whether the screen shows the details, alone or under the
// stop menu.
func (m *guardView) inDetails() bool {
	return m.mode == detailMode || (m.mode == menuMode && m.menuFrom == detailMode)
}
