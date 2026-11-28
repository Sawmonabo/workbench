package cli

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"time"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/Sawmonabo/workbench/internal/agent/guard"
	"github.com/Sawmonabo/workbench/internal/operation"
)

// heldRow is a finished command the history holds, as the view keeps it: its
// summary and its cells, made once when it was read.
type heldRow struct {
	guard.Summary
	cells []string
}

// viewHistory is the history as the view holds it (design 7.3): the file it
// has open, by its path; the finished commands read from it,
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
	held         []*heldRow
	byID         map[string]*heldRow
	last, lowest int64
	keep         time.Duration
	dated        bool
	day          string
	widths       []int
	problem      string           // the guard's sentence of a history it cannot read
	refusal      *operation.Error // or the refusal of the guard folder that stops it
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

// historyRead is what a read of the history starts from: the file the view
// has open, by its path, the highest seq it holds and the
// file's lowest when last read, whether its ENDED cells hold dates and on
// which day they were made, and the snapshots taken by then.
type historyRead struct {
	db     *guard.History
	path   string
	after  int64
	lowest int64
	dated  bool
	day    string
	snaps  int
}

// historyMsg is a read's outcome: what it started from; the history open
// now, nil for none, and its path; whether that is a new handle
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
		db: m.hist.db, path: m.hist.path, after: m.hist.last,
		lowest: m.hist.lowest, dated: m.hist.dated, day: m.hist.day, snaps: m.snaps,
	}
	return func() tea.Msg { return from.read() }
}

// read reads the history the guard's handle follows (see
// guard.FollowHistory). No history yet is an empty history (design rule 10).
// Another file than the one open, or none, makes the rows held go; a new
// handle on the file open, found at another path once the guard folder moved,
// is read from the last row held, so the rows held and the find answer stay.
func (r historyRead) read() historyMsg {
	out := historyMsg{from: r, keep: guard.HistoryKeepDefault}
	// A Bubble Tea command (tea.Cmd) is given no context, so the history is
	// opened and read with context.Background(), as the view's other reads.
	found, err := guard.FollowHistory(context.Background(), r.db)
	out.db, out.path, out.damaged = found.History, found.Path, found.Damaged
	out.reset, out.moved = found.Other, found.Moved
	if err != nil || out.db == nil {
		out.err = err
		return out
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
	ctx := context.Background() // it runs in read's tea.Cmd, which has no context
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
	h.db, h.path = msg.db, msg.path
	h.keep, h.dated, h.day, h.problem, h.refusal = msg.keep, msg.dated, msg.day, "", nil
	h.damaged = msg.damaged
	if refusal, ok := errors.AsType[*operation.Error](msg.err); ok {
		h.refusal = refusal
	} else if msg.err != nil {
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
		// A tea.Cmd is given no context.
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
