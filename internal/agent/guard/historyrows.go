package guard

// This file is the history's rows: the finished commands written, with their
// graphs and processes, and read for the view. Command text may hold a token,
// so nothing here logs it or puts it in an error.

import (
	"cmp"
	"context"
	"database/sql"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"slices"
	"strconv"
	"time"
	"unicode/utf8"

	"github.com/Sawmonabo/workbench/internal/sqlitefile"
)

// Insert writes a finished command, with its graph and processes, in one
// transaction. A command whose ID is already there is left as it is, gets no
// graph or process rows and counts as written, so writing one twice is
// harmless. It keeps at most commandKept bytes of the command's text, cut where
// a character starts, and adds the bytes left out to f.Cut; at most
// processesKept processes, the largest peaks, adding the rest to f.Unlisted;
// and at most GraphBars bars, merging neighbors by their larger value, so the
// peak stays in the graph. f is not changed.
func (h *History) Insert(ctx context.Context, f Finished) error {
	command, cut := keptCommand(f.Command)
	processes, unlisted := keptProcesses(f.Processes)
	unlisted += f.Unlisted
	bars := graphBlob(keptGraph(f.Graph))
	return h.transact(ctx, func(tx *sql.Tx) error {
		var seq int64
		err := tx.QueryRowContext(ctx, `INSERT INTO commands(id, kind, launched_by, agent,
  subagent, project, cwd, command, cut, state, stopped_by, dry_run, started, ended, peak,
  processes, unlisted)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(id) DO NOTHING RETURNING seq`,
			f.ID, f.Kind, f.LaunchedBy, f.Agent, f.Subagent, f.Project, f.Cwd, command,
			f.Cut+cut, f.State, f.By, f.DryRun, f.Started.UnixMilli(), f.Ended.UnixMilli(),
			column(f.Peak), len(processes)+unlisted, unlisted,
		).Scan(&seq)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		if len(bars) > 0 {
			if _, err := tx.ExecContext(
				ctx, "INSERT INTO graphs(seq, bars) VALUES (?, ?)", seq, bars,
			); err != nil {
				return err
			}
		}
		return insertProcesses(ctx, tx, seq, processes)
	})
}

// insertProcesses writes processes as the processes of the command at seq,
// in their order.
func insertProcesses(ctx context.Context, tx *sql.Tx, seq int64, processes []ProcessPeak) error {
	if len(processes) == 0 {
		return nil
	}
	statement, err := tx.PrepareContext(
		ctx, "INSERT INTO processes(seq, n, pid, name, peak) VALUES (?, ?, ?, ?, ?)",
	)
	if err != nil {
		return err
	}
	defer func() { _ = statement.Close() }()
	for n, p := range processes {
		if _, err := statement.ExecContext(ctx, seq, n, p.PID, p.Name, column(p.Peak)); err != nil {
			return err
		}
	}
	return nil
}

// keptCommand is command cut at commandKept bytes where a character starts,
// and the bytes left out.
func keptCommand(command string) (string, int64) {
	if len(command) <= commandKept {
		return command, 0
	}
	end := commandKept
	for end > 0 && !utf8.RuneStart(command[end]) {
		end--
	}
	return command[:end], int64(len(command) - end)
}

// keptProcesses is processes, the largest peak first, at most processesKept
// of them, and how many were left out. processes is not changed.
func keptProcesses(processes []ProcessPeak) ([]ProcessPeak, int) {
	largest := func(a, b ProcessPeak) int {
		return cmp.Or(cmp.Compare(b.Peak, a.Peak), cmp.Compare(a.PID, b.PID))
	}
	if !slices.IsSortedFunc(processes, largest) {
		processes = slices.Clone(processes)
		slices.SortStableFunc(processes, largest)
	}
	if len(processes) <= processesKept {
		return processes, 0
	}
	return processes[:processesKept], len(processes) - processesKept
}

// keptGraph is graph in at most GraphBars bars: while it has more, each two
// neighbors merge into the larger (see mergeNeighbors). graph is not changed.
func keptGraph(graph []uint64) []uint64 {
	for len(graph) > GraphBars {
		graph = mergeNeighbors(make([]uint64, 0, (len(graph)+1)/2), graph)
	}
	return graph
}

// mergeNeighbors appends graph to into with each two neighbors merged into
// the larger, oldest first, the last bar alone when there is an odd number,
// and gives the result: the one rule by which a running command's graph
// (see usage.add) and a finished one's (see keptGraph) hold their whole run
// in GraphBars bars. into may be graph[:0]: each bar is written only after
// the two it merges were read, so graph is merged in place.
func mergeNeighbors(into, graph []uint64) []uint64 {
	for i := 0; i < len(graph); i += 2 {
		into = append(into, slices.Max(graph[i:min(i+2, len(graph))]))
	}
	return into
}

// graphBlob is graph as the graphs table keeps it: one uvarint of KiB per
// bar, rounded up, oldest first.
func graphBlob(graph []uint64) []byte {
	blob := make([]byte, 0, len(graph)*binary.MaxVarintLen32)
	for _, bytes := range graph {
		blob = binary.AppendUvarint(blob, bytes>>10+min(bytes&(1<<10-1), 1))
	}
	return blob
}

// graphOf is a graphs row as bytes per bar, oldest first; nil for none.
func graphOf(blob []byte) ([]uint64, error) {
	if len(blob) == 0 {
		return nil, nil
	}
	graph := make([]uint64, 0, len(blob))
	for len(blob) > 0 {
		kib, n := binary.Uvarint(blob)
		if n <= 0 || kib > math.MaxUint64>>10 {
			return nil, errors.New("a graph cannot be read")
		}
		graph = append(graph, kib<<10)
		blob = blob[n:]
	}
	return graph, nil
}

// column is a size in bytes as an INTEGER column holds it.
func column(bytes uint64) int64 { return int64(min(bytes, math.MaxInt64)) }

// commandColumns are the columns of commands a CommandView holds, with the
// command's text as %s.
const commandColumns = `id, kind, launched_by, agent, subagent, project, cwd, %s, state,
  stopped_by, dry_run, started, ended, peak`

// summaryQuery reads the commandColumns of the commands after a seq, with the
// first SummaryLength characters of each command; detailsQuery reads those of
// one command, with all of it as kept.
var (
	summaryQuery = fmt.Sprintf(
		"SELECT %s, seq FROM commands WHERE seq > ? ORDER BY seq",
		fmt.Sprintf(commandColumns, "substr(command, 1, "+strconv.Itoa(SummaryLength)+")"),
	)
	detailsQuery = fmt.Sprintf(
		"SELECT %s, cut, unlisted FROM commands WHERE seq = ?",
		fmt.Sprintf(commandColumns, "command"),
	)
)

// scanCommand reads the commandColumns of row into v, then more.
func scanCommand(row interface{ Scan(...any) error }, v *CommandView, more ...any) error {
	var started, ended, peak int64
	if err := row.Scan(append([]any{
		&v.ID, &v.Kind, &v.LaunchedBy, &v.Agent, &v.Subagent, &v.Project, &v.Cwd, &v.Command,
		&v.State, &v.By, &v.DryRun, &started, &ended, &peak,
	}, more...)...); err != nil {
		return err
	}
	v.Started = time.UnixMilli(started)
	v.Ended = time.UnixMilli(ended)
	v.Peak = uint64(max(peak, 0))
	return nil
}

// After is the finished commands whose seq is above seq, lowest first, each
// with the first 300 characters of its command.
func (h *History) After(ctx context.Context, seq int64) ([]Summary, error) {
	rows, err := h.db.QueryContext(ctx, summaryQuery, seq)
	if err != nil {
		return nil, h.fail(err)
	}
	defer func() { _ = rows.Close() }()
	var summaries []Summary
	for rows.Next() {
		var s Summary
		if err := scanCommand(rows, &s.CommandView, &s.Seq); err != nil {
			return nil, h.fail(err)
		}
		summaries = append(summaries, s)
	}
	return summaries, h.fail(rows.Err())
}

// Lowest is the lowest seq in the history or, when it holds none, the seq the
// next command will get, so a reader that drops its rows below Lowest drops
// every row of a history emptied since it read them.
func (h *History) Lowest(ctx context.Context) (int64, error) {
	var seq int64
	err := h.db.QueryRowContext(ctx, `SELECT coalesce(min(seq),
  (SELECT seq FROM sqlite_sequence WHERE name = 'commands') + 1, 1) FROM commands`).Scan(&seq)
	return seq, h.fail(err)
}

// Details is the finished command at seq whole: its command as kept, its graph
// and its processes, the largest peak first. A seq no longer there fails with
// an error matching sql.ErrNoRows.
func (h *History) Details(ctx context.Context, seq int64) (Finished, error) {
	var f Finished
	err := h.transact(ctx, func(tx *sql.Tx) error {
		row := tx.QueryRowContext(ctx, detailsQuery, seq)
		if err := scanCommand(row, &f.CommandView, &f.Cut, &f.Unlisted); err != nil {
			return err
		}
		var blob []byte
		err := tx.QueryRowContext(ctx, "SELECT bars FROM graphs WHERE seq = ?", seq).Scan(&blob)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if f.Graph, err = graphOf(blob); err != nil {
			return fmt.Errorf("command %d: %w", seq, err)
		}
		rows, err := tx.QueryContext(
			ctx, "SELECT pid, name, peak FROM processes WHERE seq = ? ORDER BY n", seq,
		)
		if err != nil {
			return err
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			var p ProcessPeak
			var peak int64
			if err := rows.Scan(&p.PID, &p.Name, &peak); err != nil {
				return err
			}
			p.Peak = uint64(max(peak, 0))
			f.Processes = append(f.Processes, p)
		}
		return rows.Err()
	})
	return f, err
}

// findQuery reads the seq of each command whose agent, subagent, project, ID
// or command holds ?1, case ignored (see [sqlitefile.ContainsFold]): the
// columns of the fields [CommandView.Holds] looks in.
var findQuery = "SELECT seq FROM commands WHERE " + sqlitefile.ContainsFold +
	"(?1, agent, subagent, project, id, command) ORDER BY seq"

// Holds reports whether c's agent, subagent, project, ID or command holds
// text, case ignored as [History.Find] ignores it (see
// [sqlitefile.HoldsFold]), for the commands a view holds itself: it finds
// them as Find finds those of the history.
func (c *CommandView) Holds(text string) bool {
	return sqlitefile.HoldsFold(text, c.Agent, c.Subagent, c.Project, c.ID, c.Command)
}

// Find is the seq of each finished command, lowest first, whose agent,
// subagent, project, ID or whole command holds text, upper and lower case
// alike in any script, as strings.EqualFold tells them (see
// [sqlitefile.ContainsFold]).
func (h *History) Find(ctx context.Context, text string) ([]int64, error) {
	rows, err := h.db.QueryContext(ctx, findQuery, text)
	if err != nil {
		return nil, h.fail(err)
	}
	defer func() { _ = rows.Close() }()
	var seqs []int64
	for rows.Next() {
		var seq int64
		if err := rows.Scan(&seq); err != nil {
			return nil, h.fail(err)
		}
		seqs = append(seqs, seq)
	}
	return seqs, h.fail(rows.Err())
}

// Count is how many finished commands the history holds and the size of its
// database in bytes.
func (h *History) Count(ctx context.Context) (rows int, bytes int64, err error) {
	err = h.db.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM commands),
  page_count * page_size FROM pragma_page_count, pragma_page_size`).Scan(&rows, &bytes)
	return rows, bytes, h.fail(err)
}
