package cli

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textinput"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"
	"github.com/spf13/cobra"

	"github.com/Sawmonabo/workbench/internal/agent/guard"
	"github.com/Sawmonabo/workbench/internal/operation"
	"github.com/Sawmonabo/workbench/internal/platform"
)

// runGuardView opens the live view of the commands the guard tracks and of
// those that finished, which bare `agent guard` runs. It is for a person:
// with --json, with --non-interactive or without a terminal on stdin and
// stdout, or under a coding agent, it exits 3 before the screen opens.
// Otherwise it runs the view until it quits, starting the program as the
// costs view does. A full-screen view prints nothing a program could read, so
// --json is refused, and --non-interactive counts as no terminal, as --choose
// treats it.
func runGuardView(cmd *cobra.Command, o *options) error {
	if o.json {
		return operation.Fail(
			operation.ExitBlocked,
			"json",
			"agent guard has no JSON output; use workbench agent guard logs --json",
		)
	}
	if o.nonInteractive || !platform.IsTerminal(os.Stdin.Fd()) ||
		!platform.IsTerminal(os.Stdout.Fd()) {
		return operation.Fail(
			operation.ExitBlocked,
			"terminal",
			"agent guard needs a terminal; use workbench agent guard logs --follow",
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
	program := tea.NewProgram(
		model,
		tea.WithInput(os.Stdin),
		tea.WithOutput(os.Stdout),
		tea.WithContext(cmd.Context()),
		// main owns SIGINT and SIGTERM through the command's context.
		tea.WithoutSignalHandler(),
	)
	_, err := program.Run()
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

// listRow is one row of the list: the command it shows, and where that
// comes from. A row of the snapshot has neither seq nor finished; finished is
// a command the history does not hold yet, whole; seq is the place in the
// history of a command it holds. cells are the cells of a finished row, made
// once, without its tree mark; a running row's change with each frame.
type listRow struct {
	view     *guard.CommandView
	seq      int64
	finished *guard.Finished
	cells    []string
}

// heldRow is a finished command the history holds, as the view keeps it: its
// summary and its cells, made once when it was read.
type heldRow struct {
	guard.Summary
	cells []string
}

// carriedRow is a finished command that left the snapshot's pending list in
// the snapshot numbered at.
type carriedRow struct {
	*guard.Finished
	at int
}

// rowCounts are how many rows run and how many finished.
type rowCounts struct{ running, finished int }

// viewHistory is the history as the view holds it (design 7.3): the file it
// has open, by its path and identity; the finished commands read from it,
// lowest seq first and by ID, the highest seq read and the file's lowest when
// it was last read; the time kept, whether the ENDED cells hold a date and
// on which day they were made; the widest cell of each column of the held
// rows; what is said when the file cannot be read (see
// guard.HistoryUnreadable); the copies of damaged histories the guard
// folder keeps, the newest first, each with when it was set aside; the generation of
// the rows held, which
// moves on each time they are read anew, so an answer about earlier ones is
// dropped; and whether a read runs, and whether another was asked for
// meanwhile.
type viewHistory struct {
	db           *guard.History
	path         string
	file         os.FileInfo
	held         []*heldRow
	byID         map[string]*heldRow
	last, lowest int64
	keep         time.Duration
	dated        bool
	day          string
	widths       []int
	problem      string
	damaged      []guard.DamagedCopy
	gen          int
	reading      bool
	again        bool
}

// viewFind is find (design 3.2): its input on the bottom line and whether it
// shows, the latest text asked, how many asks there were, so only the latest
// answer is taken, and the cancel of the ask that runs; and the answer the
// list shows: its text and the history's rows that hold it.
type viewFind struct {
	input  textinput.Model
	open   bool
	text   string
	asked  int
	cancel context.CancelFunc
	answer string
	seqs   map[int64]bool
}

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

// tickMsg comes once a second, for the clock, the notice's expiry,
// reconnecting and, with no watcher, reading the history.
type tickMsg time.Time

// connectedMsg is a connection attempt's outcome: a client that said hello,
// or why no watcher answered.
type connectedMsg struct {
	client  *guard.Client
	problem string
}

// foundMsg is the answer to the ask numbered asked: the seqs of the
// history's rows that hold text, asked of the rows of its generation gen.
type foundMsg struct {
	asked int
	gen   int
	text  string
	seqs  []int64
	err   error
}

// partsMsg is a finished command's parts as the history holds them, read for
// the row id from the rows of the history's generation gen, through the
// handle db.
type partsMsg struct {
	id    string
	gen   int
	db    *guard.History
	parts guard.Finished
	err   error
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

// watcherSilent is what the view says while the watcher's lock is held and
// nothing answers on its socket: doctor's words (see guard.WatcherSilent), as
// a sentence.
var watcherSilent = strings.ToUpper(guard.WatcherSilent[:1]) + guard.WatcherSilent[1:] + "."

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

// findInput is find's one-line input: a slash, then the text, with a cursor
// that does not blink, plain on any background. ↑, ↓, pgup and pgdn never
// reach it: they move the list.
func findInput() textinput.Model {
	input := textinput.New()
	input.Prompt = "/"
	styles := textinput.DefaultDarkStyles()
	styles.Focused = textinput.StyleState{Prompt: bold, Text: lipgloss.NewStyle()}
	styles.Blurred = styles.Focused
	styles.Cursor.Blink = false
	input.SetStyles(styles)
	return input
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

// dialWatcher dials the watcher's socket. absent reports a missing or
// refused socket, where nothing serves.
func dialWatcher(ctx context.Context) (client *guard.Client, absent bool, err error) {
	client, err = guard.Dial(ctx)
	return client, errors.Is(err, syscall.ENOENT) || errors.Is(err, syscall.ECONNREFUSED), err
}

// connectWatcher dials the watcher's socket and says hello. A missing or
// refused socket is told by the watcher's lock, as doctor and every entry
// tell whether a watcher runs (see guard.WatcherRuns): with the lock free no
// watcher runs, and with it held one is starting or cannot serve (see
// guard.WatcherSilent). A hello that times out means a watcher of an older
// release took the socket over without serving it; a hello without a
// protocol version in common means another release; any other failed hello
// is shown as it is.
func connectWatcher() tea.Msg {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	client, absent, err := dialWatcher(ctx)
	switch {
	case absent:
		runs, lockErr := guard.WatcherRuns()
		switch {
		case lockErr != nil:
			return connectedMsg{problem: "Cannot tell whether the guard's watcher runs: " +
				lockErr.Error()}
		case runs:
			return connectedMsg{problem: watcherSilent}
		}
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
		m.client, m.connected, m.problem = msg.client, true, ""
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

// take shows a new snapshot: a finished command that left its pending list
// stays as a carried row until the history has it, the rows are made anew
// (see rebuild), tags stay only on commands still running, and the stop menu
// closes once all it names have finished.
func (m *guardView) take(snap guard.Snapshot) {
	m.snaps++
	still := make(map[string]bool, len(snap.Pending))
	for _, f := range snap.Pending {
		still[f.ID] = true
	}
	// A snapshot's slices are never changed once taken, so its commands can
	// be pointed at after the next one replaces it.
	for i := range m.snap.Pending {
		if f := &m.snap.Pending[i]; !still[f.ID] && m.hist.byID[f.ID] == nil {
			m.carried = append(m.carried, carriedRow{Finished: f, at: m.snaps})
		}
	}
	m.snap = snap
	m.rebuild()
	for id := range m.tagged {
		if !slices.Contains(m.running([]string{id}), id) {
			delete(m.tagged, id)
		}
	}
	if m.mode == menuMode && len(m.running(m.targets)) == 0 {
		m.mode = m.menuFrom
		m.say(m.shortIDs(m.targets) + " already finished; nothing was stopped")
	}
}

// rebuild makes the list's rows anew from the snapshot, the carried rows and
// the history (design 7.3), in the list's order (see order). The selection
// stays on its command once a key moved it, and before that on the first
// running row, or the first row when none runs, so it starts on a bright
// row even under a command that has ended;
// details whose command left the list close, and
// details that show a finished command keep the parts the snapshot sent, so
// they stay once the history holds it.
func (m *guardView) rebuild() {
	selected := m.selectedID()
	running, finished := m.collect(time.Now())
	m.order(running, finished)
	m.shown, m.measured = rowCounts{}, m.measured[:0]
	for i, r := range m.rows {
		if active(*r.view) {
			m.shown.running++
		} else {
			m.shown.finished++
		}
		if r.seq == 0 || m.branch[r.view.ID] != "" {
			m.measured = append(m.measured, i)
		}
	}
	at := slices.IndexFunc(m.rows, func(r listRow) bool { return r.view.ID == selected })
	if !m.moved && len(m.rows) > 0 {
		at = m.startRow()
	}
	if at < 0 {
		m.cursor = max(min(m.cursor, len(m.rows)-1), 0)
		if m.mode == detailMode {
			m.mode = listMode
		}
		m.menuFrom = listMode
		return
	}
	m.cursor = at
	if r := m.rows[at]; r.finished != nil && m.detail.id == selected {
		m.detail.parts = r.finished
	}
}

// startRow is the row the selection starts on: the first running row, or
// the first row when none runs.
func (m *guardView) startRow() int {
	return max(slices.IndexFunc(m.rows, func(r listRow) bool { return active(*r.view) }), 0)
}

// order puts the rows in the list's order: the running rows first, then the
// finished ones newest first or by largest peak, and a detached program
// drawn under its command while the list is sorted by newest and has no
// filter (see treeRows). Otherwise every row is a row of its own and the
// running ones go newest first, as the tree puts its commands.
func (m *guardView) order(running, finished []listRow) {
	slices.SortStableFunc(finished, func(a, b listRow) int {
		newest := cmp.Or(b.view.Ended.Compare(a.view.Ended), cmp.Compare(b.seq, a.seq),
			cmp.Compare(a.view.ID, b.view.ID))
		if m.byPeak {
			return cmp.Or(cmp.Compare(b.view.Peak, a.view.Peak), newest)
		}
		return newest
	})
	if !m.byPeak && m.find.answer == "" {
		m.rows, m.branch = treeRows(append(running, finished...))
		return
	}
	slices.SortStableFunc(running, func(a, b listRow) int {
		return cmp.Or(b.view.Started.Compare(a.view.Started), cmp.Compare(a.view.ID, b.view.ID))
	})
	m.rows, m.branch = append(running, finished...), map[string]string{}
}

// collect are the snapshot's rows and the finished rows, carried and held,
// that the list shows at now, and it counts them all in m.total: one row per
// ID, the snapshot's first, then a command the history does not hold yet
// over the one it holds; no finished row that ended longer ago than the time
// kept; and of those only the rows that hold the find answer, a held row by
// the history's answer, any other as the history matches. A snapshot row
// that just ended gets the parts its pending command carries.
func (m *guardView) collect(now time.Time) (running, finished []listRow) {
	since, text := now.Add(-m.hist.keep), m.find.answer
	known := make(map[string]bool, len(m.snap.Commands)+len(m.snap.Pending)+len(m.carried))
	m.total = rowCounts{}
	for i := range m.snap.Commands {
		c := &m.snap.Commands[i]
		known[c.ID] = true
		row := listRow{view: c}
		if active(*c) {
			m.total.running++
		} else {
			m.total.finished++
			if at := slices.IndexFunc(m.snap.Pending, func(f guard.Finished) bool {
				return f.ID == c.ID
			}); at >= 0 {
				row.finished = &m.snap.Pending[at]
			}
		}
		if text == "" || c.Holds(text) {
			running = append(running, row)
		}
	}
	add := func(f *guard.Finished) {
		if known[f.ID] {
			return
		}
		known[f.ID] = true
		if f.Ended.Before(since) {
			return
		}
		m.total.finished++
		if text == "" || f.Holds(text) {
			finished = append(finished, listRow{
				view: &f.CommandView, finished: f,
				cells: finishedCells(&f.CommandView, m.hist.dated, now),
			})
		}
	}
	for i := range m.snap.Pending {
		add(&m.snap.Pending[i])
	}
	for _, c := range m.carried {
		add(c.Finished)
	}
	for _, h := range m.hist.held {
		if known[h.ID] || h.Ended.Before(since) {
			continue
		}
		m.total.finished++
		if text == "" || m.find.seqs[h.Seq] {
			finished = append(finished, listRow{view: &h.CommandView, seq: h.Seq, cells: h.cells})
		}
	}
	return running, finished
}

// treeRows are rows in the order the list shows them, and the tree mark of
// each row drawn under another, by row ID. A detached program whose call's
// command is among the rows is drawn directly under that command, with ├,
// or └ when it is the last there; one whose command is not among them keeps
// a place of its own. A command and what is under it move as one: those of
// which any row still runs come first, the newest command first, then the
// others in the order given. Under a command the rows keep the order given.
func treeRows(rows []listRow) ([]listRow, map[string]string) {
	branch := map[string]string{}
	launchers := map[string]bool{}
	for _, r := range rows {
		if r.view.Kind == "detached" && r.view.LaunchedBy != "" {
			launchers[r.view.LaunchedBy] = true
		}
	}
	heads := map[string]bool{}
	for _, r := range rows {
		if launchers[r.view.ID] && r.view.Kind != "detached" {
			heads[r.view.ID] = true
		}
	}
	under := map[string][]listRow{}
	runs := func(id string) bool {
		return slices.ContainsFunc(under[id], func(u listRow) bool { return active(*u.view) })
	}
	var first, rest []int
	for i, r := range rows {
		if r.view.Kind == "detached" && heads[r.view.LaunchedBy] {
			under[r.view.LaunchedBy] = append(under[r.view.LaunchedBy], r)
			continue
		}
		if active(*r.view) || runs(r.view.ID) {
			first = append(first, i)
		} else {
			rest = append(rest, i)
		}
	}
	// A detached program listed after its command counts once all rows are
	// placed: move any head whose rows under it run to the first ones.
	rest = slices.DeleteFunc(rest, func(i int) bool {
		if runs(rows[i].view.ID) {
			first = append(first, i)
			return true
		}
		return false
	})
	slices.SortStableFunc(first, func(a, b int) int {
		return cmp.Or(rows[b].view.Started.Compare(rows[a].view.Started),
			cmp.Compare(rows[a].view.ID, rows[b].view.ID))
	})
	out := make([]listRow, 0, len(rows))
	for _, i := range append(first, rest...) {
		out = append(out, rows[i])
		group := under[rows[i].view.ID]
		for n, r := range group {
			branch[r.view.ID] = "├ "
			if n == len(group)-1 {
				branch[r.view.ID] = "└ "
			}
			out = append(out, r)
		}
	}
	return out, branch
}

// running are those of ids whose command still runs, in the order given.
func (m *guardView) running(ids []string) []string {
	var out []string
	for _, id := range ids {
		i := slices.IndexFunc(m.snap.Commands, func(c guard.CommandView) bool { return c.ID == id })
		if i >= 0 && active(m.snap.Commands[i]) {
			out = append(out, id)
		}
	}
	return out
}

// shortIDs are rows' IDs as the view shows them (see label), joined with
// commas.
func (m *guardView) shortIDs(ids []string) string {
	short := make([]string, len(ids))
	for i, id := range ids {
		short[i] = m.label(id)
	}
	return strings.Join(short, ", ")
}

// label is the ID of the row id as the view shows it (see rowLabel), or
// its call's short ID when the list no longer has it.
func (m *guardView) label(id string) string {
	i := slices.IndexFunc(m.rows, func(r listRow) bool { return r.view.ID == id })
	if i < 0 {
		return shortCallID(id)
	}
	return rowLabel(*m.rows[i].view)
}

// rowLabel is a row's ID as the list shows it: for a detached program, p and
// the process ID of its first root that runs, since a number in a process
// list is read as a live PID and the system hands a gone root's PID out
// again; for any other row its short ID (see shortCallID). The log and
// --json keep a detached program's own ID, which its details show as the
// label the log uses.
func rowLabel(c guard.CommandView) string {
	if c.Kind == "detached" && c.PID > 0 {
		return "p" + strconv.Itoa(c.PID)
	}
	return shortCallID(c.ID)
}

// selectedID is the selected row's call ID, empty with no rows.
func (m *guardView) selectedID() string {
	if m.cursor < len(m.rows) {
		return m.rows[m.cursor].view.ID
	}
	return ""
}

// active reports whether a row still runs, so it shows its memory and can
// be tagged and stopped: it is running or stopping.
func active(c guard.CommandView) bool { return c.State == "running" || c.State == "stopping" }

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
			parts = append(parts, "logged "+m.label(r.ID)+" (dry run)")
		case r.OK:
			parts = append(parts, "stopped "+m.label(r.ID))
		default:
			parts = append(parts, m.label(r.ID)+" "+r.Error)
		}
	}
	m.say(strings.Join(parts, "; "))
}

// historyRead is what a read of the history starts from: the file the view
// has open, by its path and identity, the highest seq it holds and the
// file's lowest when last read, whether its ENDED cells hold dates and on
// which day they were made, and the snapshots taken by then.
type historyRead struct {
	db     *guard.History
	path   string
	file   os.FileInfo
	after  int64
	lowest int64
	dated  bool
	day    string
	snaps  int
}

// historyMsg is a read's outcome: what it started from; the history open
// now, nil for none, and its path and identity; whether that is a new handle
// on the file open before, found at another path once the guard folder moved;
// whether the rows held go (a file not read before, ENDED cells made anew, or
// a file that cannot be used); the rows read, above the last held unless
// reset, with their cells and the widest cell of each column; the file's
// lowest seq; the time kept, whether the ENDED cells hold dates and on which
// day they were made; why the file cannot be read, nil when it can or does
// not exist yet; and the copies of damaged histories the guard folder keeps,
// with their times.
type historyMsg struct {
	from    historyRead
	db      *guard.History
	path    string
	file    os.FileInfo
	moved   bool
	reset   bool
	rows    []*heldRow
	widths  []int
	lowest  int64
	keep    time.Duration
	dated   bool
	day     string
	err     error
	damaged []guard.DamagedCopy
}

// readHistory reads the history beside the screen (design 7.3), one read at
// a time: a read asked for while one runs runs after it.
func (m *guardView) readHistory() tea.Cmd {
	if m.hist.reading {
		m.hist.again = true
		return nil
	}
	m.hist.reading, m.hist.again = true, false
	from := historyRead{
		db: m.hist.db, path: m.hist.path, file: m.hist.file, after: m.hist.last,
		lowest: m.hist.lowest, dated: m.hist.dated, day: m.hist.day, snaps: m.snaps,
	}
	return func() tea.Msg { return from.read() }
}

// read reads the history where the guard folder is now. A missing file, or
// one without a history yet, is an empty history (design rule 10). A file
// other than the one open, by identity, is opened and read whole: the writer
// moves a damaged history aside and starts a new one at the same path, whose
// seqs start at 1 again, and a reader that kept the old file open would go on
// reading the one set aside. The file's identity is taken before it is
// opened, so a file replaced in between is opened again at the next read.
// The file open, found at another path once the guard folder moved, is read
// through a new handle at that path, from the last row held, so the rows held
// and the find answer stay as for a file that did not move: a connection the
// handle open makes from then on names the old path, where the file is no
// more. A handle that cannot be opened there leaves the one open, read as it
// is, and the next read tries again.
func (r historyRead) read() historyMsg {
	out := historyMsg{from: r, keep: guard.HistoryKeepDefault, db: r.db, path: r.path, file: r.file}
	root, err := guard.HistoryRoot()
	if err != nil {
		out.err, out.reset, out.db = err, true, nil
		return out
	}
	out.damaged = guard.HistoryDamagedCopies(root)
	path := guard.HistoryFile(root)
	file, err := os.Stat(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		out.reset, out.db, out.path, out.file = true, nil, path, nil
		return out
	case err != nil:
		out.err, out.reset, out.db, out.path = err, true, nil, path
		return out
	case r.db == nil || !os.SameFile(file, r.file):
		out.reset, out.path, out.file = true, path, file
		out.db, err = guard.OpenHistory(root, false)
		if errors.Is(err, fs.ErrNotExist) {
			return out
		}
		if err != nil {
			out.err = err
			return out
		}
	case path != r.path:
		if db, err := guard.OpenHistory(root, false); err == nil {
			out.db, out.path, out.file, out.moved = db, path, file, true
		}
	}
	if err := out.fill(r); err != nil {
		out.err, out.rows = err, nil
		// A file found unusable while open shows nothing; any other failed
		// read keeps the rows held, and the next read tries again.
		out.reset = out.reset || errors.Is(err, guard.ErrHistoryUnusable)
	}
	return out
}

// fill reads the time kept, the lowest seq and the rows after the last held,
// or every row when the rows held go: a lowest seq under the one read before
// is another file's, and the ENDED cells are made anew when they start or
// stop holding dates, or with dates on a new day.
func (out *historyMsg) fill(r historyRead) error {
	ctx := context.Background()
	keep, err := out.db.Keep(ctx)
	if err != nil {
		return err
	}
	lowest, err := out.db.Lowest(ctx)
	if err != nil {
		return err
	}
	now := time.Now()
	out.keep, out.lowest = keep, lowest
	out.dated, out.day = keep > 24*time.Hour, now.Format(time.DateOnly)
	after := r.after
	if out.reset || lowest < r.lowest || out.dated != r.dated || out.dated && out.day != r.day {
		out.reset, after = true, 0
	}
	summaries, err := out.db.After(ctx, after)
	if err != nil {
		return err
	}
	out.rows, out.widths = make([]*heldRow, len(summaries)), make([]int, len(guardColumns))
	for i := range summaries {
		row := &heldRow{Summary: summaries[i]}
		row.cells = finishedCells(&row.CommandView, out.dated, now)
		for n, cell := range row.cells {
			out.widths[n] = max(out.widths[n], ansi.StringWidth(cell))
		}
		out.rows[i] = row
	}
	return nil
}

// tookHistory takes a read's outcome: the history open now, the time kept,
// why the file cannot be read, the rows read, the rows below the file's
// lowest seq gone, and the carried rows that a read which started after they
// left the snapshot answers for. Another file drops the find answer, whose
// seqs were the one before's; rows read anew while a filter is kept ask the
// history again, so they show once they hold its text. A new handle on the
// file open before, which the guard folder's move called for, keeps the rows
// held and the find answer, and asks again what the handle before may have
// failed to answer since the move: the find kept, and the parts of the
// command whose details show. A read asked for meanwhile starts. A command's
// parts stay: its ID names it in any file.
func (m *guardView) tookHistory(msg historyMsg) tea.Cmd {
	h := &m.hist
	h.reading = false
	other := msg.db != h.db && !msg.moved
	if msg.db != h.db && h.db != nil {
		// A find or a read of a command's parts may still run on it.
		old := h.db
		go func() { _ = old.Close() }()
	}
	h.db, h.path, h.file = msg.db, msg.path, msg.file
	h.keep, h.dated, h.day, h.problem = msg.keep, msg.dated, msg.day, ""
	h.damaged = msg.damaged
	if msg.err != nil {
		h.problem = guard.HistoryUnreadable(msg.err, filepath.Dir(msg.path))
	}
	// Rows held go, and their generation moves on, only when there were
	// some, or a file was open: a missing file read each second changes
	// nothing.
	if msg.reset && (other || h.last > 0 || len(h.held) > 0) {
		h.held, h.byID, h.widths, h.last, h.lowest = nil, map[string]*heldRow{}, nil, 0, 0
		h.gen++
	}
	if other {
		m.find.seqs = nil
	}
	if msg.moved {
		m.detail.problem = ""
	}
	m.hold(msg)
	m.carried = slices.DeleteFunc(m.carried, func(c carriedRow) bool {
		return c.at <= msg.from.snaps || h.byID[c.ID] != nil
	})
	m.rebuild()
	var cmds []tea.Cmd
	if (len(msg.rows) > 0 || other || msg.moved) && m.find.text != "" {
		cmds = append(cmds, m.ask(m.find.text))
	}
	if h.again {
		cmds = append(cmds, m.readHistory())
	}
	return tea.Batch(cmds...)
}

// hold adds the rows read above the last held, widens the columns to their
// cells and drops the rows below the file's lowest seq.
func (m *guardView) hold(msg historyMsg) {
	h := &m.hist
	for _, row := range msg.rows {
		if row.Seq > h.last {
			h.held, h.byID[row.ID], h.last = append(h.held, row), row, row.Seq
		}
	}
	h.widths = append(h.widths, make([]int, max(len(msg.widths)-len(h.widths), 0))...)
	for i, width := range msg.widths {
		h.widths[i] = max(h.widths[i], width)
	}
	h.lowest = max(h.lowest, msg.lowest)
	gone := slices.IndexFunc(h.held, func(row *heldRow) bool { return row.Seq >= h.lowest })
	if gone < 0 {
		gone = len(h.held)
	}
	for _, row := range h.held[:gone] {
		delete(h.byID, row.ID)
	}
	h.held = slices.Delete(h.held, 0, gone)
}

// ask asks the history for the rows that hold text, beside the screen,
// cancelling the ask before; the list keeps its rows until the answer comes,
// and only the answer to the latest ask is taken (see found). An empty text
// clears the filter at once.
func (m *guardView) ask(text string) tea.Cmd {
	if m.find.cancel != nil {
		m.find.cancel()
		m.find.cancel = nil
	}
	m.find.asked++
	m.find.text = text
	if text == "" {
		m.find.answer, m.find.seqs = "", nil
		m.rebuild()
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.find.cancel = cancel
	asked, gen, db := m.find.asked, m.hist.gen, m.hist.db
	return func() tea.Msg {
		defer cancel()
		msg := foundMsg{asked: asked, gen: gen, text: text}
		if db != nil {
			msg.seqs, msg.err = db.Find(ctx, text)
		}
		return msg
	}
}

// found takes the answer to the latest ask: the list shows the rows that hold
// its text, from the row the selection starts on (see startRow) when the
// text is new and the list shows;
// details that show keep their row while the filter holds it. An answer
// about rows held before they were read anew asks again.
func (m *guardView) found(msg foundMsg) tea.Cmd {
	if msg.asked != m.find.asked {
		return nil
	}
	m.find.cancel = nil
	if msg.gen != m.hist.gen {
		return m.ask(msg.text)
	}
	if msg.err != nil {
		m.say("Cannot search the guard history: " +
			guard.ShowPaths(msg.err.Error(), filepath.Dir(m.hist.path)))
	}
	fresh := msg.text != m.find.answer
	m.find.answer, m.find.seqs = msg.text, make(map[int64]bool, len(msg.seqs))
	for _, seq := range msg.seqs {
		m.find.seqs[seq] = true
	}
	m.rebuild()
	if fresh && !m.inDetails() {
		m.cursor, m.first = m.startRow(), 0
	}
	return nil
}

// readParts reads, beside the screen, the parts of the finished command whose
// details show when the history holds it and the view lacks them: its whole
// command, its graph and its processes (design 7.3).
func (m *guardView) readParts() tea.Cmd {
	d := &m.detail
	if !m.inDetails() || m.cursor >= len(m.rows) || m.hist.db == nil {
		return nil
	}
	r := m.rows[m.cursor]
	if r.seq == 0 || r.view.ID != d.id || d.parts != nil || d.reading || d.problem != "" {
		return nil
	}
	d.reading = true
	id, seq, gen, db := d.id, r.seq, m.hist.gen, m.hist.db
	return func() tea.Msg {
		parts, err := db.Details(context.Background(), seq)
		return partsMsg{id: id, gen: gen, db: db, parts: parts, err: err}
	}
}

// gotParts takes a finished command's parts for the details that asked for
// them. A read that failed on rows held before they were read anew, perhaps
// from a file since closed, or through a handle since replaced, is tried
// again.
func (m *guardView) gotParts(msg partsMsg) {
	d := &m.detail
	if msg.id != d.id || !d.reading {
		return
	}
	d.reading = false
	switch {
	case msg.err == nil:
		d.parts = &msg.parts
	case msg.gen == m.hist.gen && msg.db == m.hist.db:
		d.problem = "Cannot read this command from the guard history: " +
			guard.ShowPaths(msg.err.Error(), filepath.Dir(m.hist.path))
	}
}

// press handles a key. ctrl+c always quits. While find's input is open, the
// keys of design 3.2 go to it (see pressFind); otherwise q quits, and the
// screen shown takes the key.
func (m *guardView) press(msg tea.KeyPressMsg) tea.Cmd {
	key := msg.String()
	switch {
	case key == "ctrl+c":
		return tea.Quit
	case m.find.open && m.mode == listMode:
		return m.pressFind(msg)
	case key == "q":
		return tea.Quit
	}
	switch m.mode {
	case menuMode:
		return m.pressMenu(key)
	case detailMode:
		return m.pressDetails(msg)
	case listMode:
	}
	return m.pressList(key)
}

// pressList handles a key on the list. There is no j or k to move, since k
// stops, as in htop. esc clears a kept filter, and quits when none is kept.
func (m *guardView) pressList(key string) tea.Cmd {
	last, page := max(len(m.rows)-1, 0), max(m.page, 1)
	switch key {
	case "up", "down", "pgup", "pgdown", "home", "end", "space", "enter", "right", "l", "k":
		m.moved = true
	}
	switch key {
	case "up":
		m.cursor = max(m.cursor-1, 0)
	case "down":
		m.cursor = min(m.cursor+1, last)
	case "pgup":
		m.cursor = max(m.cursor-page, 0)
	case "pgdown":
		m.cursor = min(m.cursor+page, last)
	case "home":
		m.cursor = 0
	case "end":
		m.cursor = last
	case "space":
		if m.cursor < len(m.rows) && active(*m.rows[m.cursor].view) {
			id := m.rows[m.cursor].view.ID
			if m.tagged[id] {
				delete(m.tagged, id)
			} else {
				m.tagged[id] = true
			}
		}
		m.cursor = min(m.cursor+1, last)
	case "enter", "right", "l":
		if m.cursor < len(m.rows) {
			m.openDetails()
		}
	case "k":
		m.openMenu(listMode)
	case "s":
		m.byPeak = !m.byPeak
		m.rebuild()
		m.cursor, m.first = m.startRow(), 0
	case "/":
		m.find.open = true
		m.find.input.SetValue(m.find.text)
		m.find.input.CursorEnd()
		return m.find.input.Focus()
	case "esc":
		if m.find.text != "" {
			return m.ask("")
		}
		return tea.Quit
	}
	return nil
}

// pressFind handles a key while find's input is open: enter closes it and
// keeps the filter, esc closes it and clears the filter, ↑, ↓, pgup and pgdn
// move the selection, and every other key edits the text.
func (m *guardView) pressFind(msg tea.KeyPressMsg) tea.Cmd {
	switch key := msg.String(); key {
	case "enter":
		m.find.open = false
		m.find.input.Blur()
	case "esc":
		m.find.open = false
		m.find.input.Blur()
		return m.ask("")
	case "up", "down", "pgup", "pgdown":
		return m.pressList(key)
	default:
		return m.edit(msg)
	}
	return nil
}

// edit hands a key or a paste to find's input, and asks the history again
// when its text changed.
func (m *guardView) edit(msg tea.Msg) tea.Cmd {
	var cmd tea.Cmd
	m.find.input, cmd = m.find.input.Update(msg)
	if text := m.find.input.Value(); text != m.find.text {
		return tea.Batch(cmd, m.ask(text))
	}
	return cmd
}

// pressDetails handles a key on the details. With the side panel open, ↑,
// ↓, pgup, pgdn, home and end scroll it and esc or ← closes it. Without it,
// ↑ and ↓ choose among the `… more` rows when there are any, enter or →
// opens the chosen one in the panel, and the keys scroll the details
// otherwise; esc or ← goes back to the list. k stops on both.
func (m *guardView) pressDetails(msg tea.KeyPressMsg) tea.Cmd {
	d, key := &m.detail, msg.String()
	pane := &m.details
	if d.panel {
		pane = &m.pane
	}
	picks := len(d.picks)
	switch {
	case key == "k":
		m.openMenu(detailMode)
	case key == "home":
		pane.GotoTop()
	case key == "end":
		pane.GotoBottom()
	case (key == "esc" || key == "left" || key == "h") && d.panel:
		d.panel, d.reveal = false, true
	case key == "esc" || key == "left" || key == "h":
		m.mode = listMode
	case d.panel || picks == 0:
		var cmd tea.Cmd
		*pane, cmd = pane.Update(msg)
		return cmd
	case key == "up":
		d.pick, d.reveal = max(min(d.pick, picks-1)-1, 0), true
	case key == "down":
		d.pick, d.reveal = min(d.pick+1, picks-1), true
	case key == "enter" || key == "right" || key == "l":
		d.pick = min(d.pick, picks-1)
		d.panel, d.part = true, d.picks[d.pick].part
		m.pane.GotoTop()
	default:
		var cmd tea.Cmd
		m.details, cmd = m.details.Update(msg)
		return cmd
	}
	return nil
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
			m.say(strings.TrimPrefix(m.notice+"; ", "; ") + m.shortIDs(gone) + " already finished")
		}
		return cmd
	}
	return nil
}

// openDetails shows the selected command's details from their top, with
// the parts the snapshot sent for a finished command the history does not
// hold yet, the first `… more` row chosen and the side panel closed.
func (m *guardView) openDetails() {
	r := m.rows[m.cursor]
	m.mode = detailMode
	m.detail = viewDetail{id: r.view.ID, parts: r.finished}
	m.details.GotoTop()
}

// stopTargets are the IDs a stop would name: from the details, the row
// shown; from the list, the tagged rows still running, or else the selected
// one when it runs.
func (m *guardView) stopTargets(from viewMode) []string {
	var ids []string
	if from == listMode {
		for _, c := range m.snap.Commands {
			if m.tagged[c.ID] && active(c) {
				ids = append(ids, c.ID)
			}
		}
	}
	if len(ids) == 0 && m.cursor < len(m.rows) && active(*m.rows[m.cursor].view) {
		ids = []string{m.rows[m.cursor].view.ID}
	}
	return ids
}

// openMenu opens the stop menu from a screen, its polite stop chosen, when
// there is a running row to stop and a watcher to ask.
func (m *guardView) openMenu(from viewMode) {
	ids := m.stopTargets(from)
	switch {
	case len(ids) == 0 && m.cursor < len(m.rows):
		m.say(m.label(m.selectedID()) + " already finished")
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
	m.say("asking the watcher to stop " + m.shortIDs(ids))
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

// inDetails reports whether the screen shows the details, alone or under the
// stop menu.
func (m *guardView) inDetails() bool {
	return m.mode == detailMode || (m.mode == menuMode && m.menuFrom == detailMode)
}

// headLine is the brand, the watcher's state, a badge when memory is low or
// critical, and the clock, on one line; a window too narrow for all of them
// loses the clock.
func (m *guardView) headLine() string {
	line := bold.Render("[WorkBench] Agent Guard") + "  "
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
		return commas(int64(total))
	}
	return commas(int64(shown)) + " of " + commas(int64(total))
}

// statusLines say why no watcher answers or, when shown commands are dry
// runs, that their stops are only logged: for all of them, or for how many;
// then what is wrong with the history (see historyWarning). With none of
// these they are one blank line. A long text wraps to the window; the
// dry-run texts fit 60 columns, so the stop menu never covers a second line
// of them.
func (m *guardView) statusLines() []string {
	var texts []string
	dry, commands := 0, len(m.snap.Commands)
	for _, c := range m.snap.Commands {
		if c.DryRun {
			dry++
		}
	}
	switch {
	case !m.connected && m.problem != "":
		texts = append(texts, m.problem)
	case dry > 0 && dry == commands:
		texts = append(texts, "dry run: stops are logged, not sent")
	case dry > 0:
		texts = append(
			texts,
			fmt.Sprintf("dry run on %d of %d commands: stops are logged, not sent",
				dry, commands),
		)
	}
	texts = append(texts, m.historyWarnings(time.Now())...)
	if len(texts) == 0 {
		return []string{""}
	}
	var lines []string
	for _, text := range texts {
		for line := range strings.SplitSeq(ansi.Wrap(forTerm(text), m.width, ""), "\n") {
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
func (m *guardView) historyWarnings(now time.Time) []string {
	if text := m.snap.HistoryError; m.connected && text != "" {
		return []string{text}
	}
	var texts []string
	if m.hist.problem != "" {
		texts = append(texts, m.hist.problem)
	}
	if text := guard.HistoryDamaged(m.hist.damaged, now); text != "" {
		texts = append(texts, text)
	}
	return texts
}

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
	if t.IsZero() {
		return ""
	}
	t, now = t.Local(), now.Local()
	if dated && t.Format(time.DateOnly) != now.Format(time.DateOnly) {
		return t.Format("Jan 2 3:04 PM")
	}
	return t.Format("3:04 PM")
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

// sizeStyles are the styles of a command's memory now (while it runs; a
// finished one shows none) and of its peak, each nil below a gigabyte.
func sizeStyles(c guard.CommandView) (now, peak *lipgloss.Style) {
	if active(c) {
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

// onBand is s as the selected row draws it on the light band or the dark
// one: its foreground swapped for the one [guardOnBand] lists, so it keeps at
// least 3:1 contrast with the band, and the rest of s as it is.
func onBand(s lipgloss.Style, light bool) lipgloss.Style {
	if lifted, ok := guardOnBand[light][s.GetForeground()]; ok {
		return s.Foreground(lifted)
	}
	return s
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
	add("Folder", showPath(c.Cwd))
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
		var b strings.Builder
		writeText(&b, width-detailIndent, min(i, 1)*2, value)
		for line := range strings.SplitSeq(strings.TrimSuffix(b.String(), "\n"), "\n") {
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
	var b strings.Builder
	for i, step := range commandSteps(command) {
		writeText(&b, width, min(i, 1)*2, step)
	}
	lines := strings.Split(strings.TrimSuffix(b.String(), "\n"), "\n")
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
		out[i] = l + strings.Repeat(" ", max(left-ansi.StringWidth(l), 0)) + bar + r
	}
	return out
}

// accent is the style of the selected row's ▸ and ID, of the chosen `… more`
// row and of the side panel's title: bold in the accent, or bold alone
// without color.
func (m *guardView) accent() lipgloss.Style {
	if m.noColor {
		return bold
	}
	return guardAccent(m.light)
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
		base := lines[at] + strings.Repeat(" ", max(m.width-ansi.StringWidth(lines[at]), 0))
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
		count = "  " + bold.Render(commas(int64(m.cursor+1))+"/"+commas(int64(len(m.rows))))
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
