package cli

import (
	"cmp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/Sawmonabo/workbench/internal/agent/guard"
)

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

// carriedRow is a finished command that left the snapshot's pending list in
// the snapshot numbered at.
type carriedRow struct {
	*guard.Finished
	at int
}

// rowCounts are how many rows run and how many finished.
type rowCounts struct{ running, finished int }

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
