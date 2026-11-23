package guard

// This file is the history: the commands that finished, each with its
// processes and the graph of its memory over its whole run, kept in a SQLite
// file in the guard folder. Command text may hold a token, so nothing here
// logs it or puts it in an error.

import (
	"cmp"
	"context"
	"database/sql"
	"encoding/binary"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Sawmonabo/workbench/internal/operation"
	"github.com/Sawmonabo/workbench/internal/sqlitefile"
)

// The history's limits.
const (
	// historyKeepDefault is how long finished commands are kept while no time
	// is set.
	historyKeepDefault = 24 * time.Hour
	// historyRows is the most finished commands kept; historyBytes is the
	// most the pages in use may take.
	historyRows  = 20000
	historyBytes = 256 << 20
	// commandKept is the most bytes of a command's text kept.
	commandKept = 64 << 10
	// processesKept is the most processes kept for one command.
	processesKept = 10000
	// graphBars is the most bars a command's graph has.
	graphBars = 120
	// trimStep is the most commands one Trim step deletes.
	trimStep = 200
	// summaryLength is how many characters of its command a Summary holds.
	summaryLength = 300
)

// historyVersion is the user_version of a history: the one layout there is.
const historyVersion = 1

// historyPragmas run on each connection: a writer waits at most 100 ms for
// another, a commit in WAL mode skips the sync and keeps the file consistent,
// deleting a command deletes its graph and processes, the WAL file is
// checkpointed every 100 pages and held to 1 MiB, and the page cache to
// 64 KiB, so the watcher's memory does not grow with the history.
var historyPragmas = []string{
	"busy_timeout(100)",
	"synchronous(NORMAL)",
	"foreign_keys(1)",
	"wal_autocheckpoint(100)",
	"journal_size_limit(1048576)",
	"cache_size(-64)",
}

// schemaObject is a table or index of the history, as sqlite_master names it.
type schemaObject struct{ kind, name, sql string }

// historySchema is the history's tables and index, made in this order.
//
//   - commands: one row per finished command. seq is never given twice; id is
//     the call's ID, or a detached program's; kind is "" or "detached" and
//     launched_by the call that started a detached program; command is cut at
//     commandKept bytes, cut holding the bytes left out; state is done,
//     stopped or unmatched and stopped_by "", guard or you; started and ended
//     are Unix milliseconds; peak is in bytes; processes is how many it had
//     and unlisted how many of them were not kept.
//   - graphs: a command's graph, one uvarint of KiB per bar, oldest first.
//   - processes: a command's processes, n 0 the largest peak, peak in bytes.
//   - settings: "keep", the seconds finished commands stay.
var historySchema = []schemaObject{
	{"table", "commands", `CREATE TABLE commands(
  seq         INTEGER PRIMARY KEY AUTOINCREMENT,
  id          TEXT    NOT NULL UNIQUE,
  kind        TEXT    NOT NULL,
  launched_by TEXT    NOT NULL,
  agent       TEXT    NOT NULL,
  subagent    TEXT    NOT NULL,
  project     TEXT    NOT NULL,
  cwd         TEXT    NOT NULL,
  command     TEXT    NOT NULL,
  cut         INTEGER NOT NULL,
  state       TEXT    NOT NULL,
  stopped_by  TEXT    NOT NULL,
  dry_run     INTEGER NOT NULL,
  started     INTEGER NOT NULL,
  ended       INTEGER NOT NULL,
  peak        INTEGER NOT NULL,
  processes   INTEGER NOT NULL,
  unlisted    INTEGER NOT NULL)`},
	{"index", "commands_ended", `CREATE INDEX commands_ended ON commands(ended)`},
	{"table", "graphs", `CREATE TABLE graphs(
  seq  INTEGER PRIMARY KEY REFERENCES commands(seq) ON DELETE CASCADE,
  bars BLOB NOT NULL)`},
	{"table", "processes", `CREATE TABLE processes(
  seq  INTEGER NOT NULL REFERENCES commands(seq) ON DELETE CASCADE,
  n    INTEGER NOT NULL,
  pid  INTEGER NOT NULL,
  name TEXT    NOT NULL,
  peak INTEGER NOT NULL,
  PRIMARY KEY(seq, n)) WITHOUT ROWID`},
	{"table", "settings", `CREATE TABLE settings(
  key   TEXT PRIMARY KEY,
  value TEXT NOT NULL) WITHOUT ROWID`},
}

// keepSetting is the settings row that holds the time kept.
const keepSetting = "keep"

// ErrHistoryUnusable is what the history's functions return, wrapped with the
// path and the reason, for a file that is not a database, that SQLite calls
// corrupt, whose user_version is not 1 or whose tables are not the history's.
// Such a file is never repaired or converted; only the watcher's writer moves
// it aside (see [MoveDamaged]). A file that cannot be read or written for
// another reason, such as a full disk or a permission, is not unusable.
var ErrHistoryUnusable = errors.New("not a usable guard history")

// Finished is a command that ended, as the history keeps it: its view, the
// graph of its memory over its whole run in bytes per bar, oldest first, and
// every process it had with its peak. Cut is the bytes of the command's text
// left out and Unlisted the processes not kept, as [History.Insert] keeps
// them. A view's History and Tree are not kept.
type Finished struct {
	CommandView
	Graph     []uint64      `json:"graph"`
	Processes []ProcessPeak `json:"processes"`
	Cut       int64         `json:"cut"`
	Unlisted  int           `json:"unlisted"`
}

// ProcessPeak is one process a finished command had and the largest resident
// size it reached, in bytes.
type ProcessPeak struct {
	PID  int    `json:"pid"`
	Name string `json:"name"`
	Peak uint64 `json:"peak"`
}

// Summary is a finished command as the list shows it: its place in the
// history and its view, with the first 300 characters of its command and no
// History or Tree.
type Summary struct {
	Seq int64
	CommandView
}

// History is the history file, opened for writing by the watcher's writer
// and by the command that sets the time kept, and for reading by the view.
// opened is the file a writer opened, for [History.Check].
type History struct {
	root, path string
	write      bool
	db         *sql.DB
	opened     os.FileInfo
}

// historyFile is the history in the guard folder root; damagedFile is where
// a history that cannot be used is moved.
func historyFile(root string) string { return filepath.Join(root, "history.db") }
func damagedFile(root string) string { return filepath.Join(root, "history.damaged.db") }

// sideFiles are the suffixes of the files SQLite keeps beside a WAL database.
var sideFiles = []string{"-wal", "-shm"}

// OpenHistory opens the history in the guard folder root, a private folder
// that must exist. With write it makes a missing file, empty and 0600 before
// SQLite opens it so the files beside it are private too, writes its schema
// and switches it to WAL; the connection is the only one. Without, it opens the
// file for queries only and never makes it: a missing file, or one that holds
// no schema yet, fails with an error matching fs.ErrNotExist, an empty
// history. A file that is not a history of version 1 fails with
// [ErrHistoryUnusable] and is left as it is.
func OpenHistory(root string, write bool) (*History, error) {
	h := &History{root: root, path: historyFile(root), write: write}
	if !write {
		if _, err := os.Stat(h.path); err != nil {
			return nil, sqlitefile.FileError(h.path, err)
		}
	}
	options := sqlitefile.Options{
		Create:   write,
		ReadOnly: !write,
		Pragmas:  historyPragmas,
		Prepare:  h.prepare,
	}
	if write {
		// Every write takes its lock at BEGIN, so a transaction that reads
		// first never fails when it turns into a write.
		options.TxLock = "immediate"
	}
	db, err := sqlitefile.Open(h.path, options)
	if err != nil {
		if sqlitefile.IsDamaged(err) && !errors.Is(err, ErrHistoryUnusable) {
			err = fmt.Errorf("%w: %w", ErrHistoryUnusable, err)
		}
		return nil, err
	}
	h.db = db
	if write {
		if h.opened, err = os.Stat(h.path); err != nil {
			_ = db.Close()
			return nil, sqlitefile.FileError(h.path, err)
		}
	}
	return h, nil
}

// Check is the writer's cheap look, once a minute, at the file it holds open:
// SQLite reads a page from the file only when its page cache does not hold it,
// so a small history that fits the cache could be overwritten or replaced
// and the writer would go on writing into it until it next opened it. It
// fails with an error matching fs.ErrNotExist when the path names no file,
// and with [ErrHistoryUnusable] when it names another file (by device and
// inode) than the one opened, beside which the -wal and -shm files are the
// opened one's, or one whose header is not a database's (see
// [sqlitefile.CheckHeader]). It reads the header alone, so it costs the same
// whatever the history's size: a full integrity check reads every page.
func (h *History) Check() error {
	info, err := os.Stat(h.path)
	switch {
	case err != nil:
		return sqlitefile.FileError(h.path, err)
	case h.opened != nil && !os.SameFile(info, h.opened):
		return fmt.Errorf("%w: %s is another file than the one the writer opened",
			ErrHistoryUnusable, h.path)
	}
	err = sqlitefile.CheckHeader(h.path)
	if errors.Is(err, sqlitefile.ErrNotADatabase) {
		return fmt.Errorf("%w: %w", ErrHistoryUnusable, err)
	}
	return err
}

// Close closes the history. The last connection to close removes the -wal and
// -shm files.
func (h *History) Close() error { return h.db.Close() }

// prepare checks the file is a history of version 1, before anything changes
// it, and makes the schema of a writer's file that holds none yet.
func (h *History) prepare(db *sql.DB) error {
	ctx := context.Background()
	if h.write {
		db.SetMaxOpenConns(1)
	}
	conn, err := db.Conn(ctx)
	if err != nil {
		return h.fail(err)
	}
	defer func() { _ = conn.Close() }()
	version, objects, err := h.readSchema(ctx, conn)
	if err != nil {
		return err
	}
	if version != 0 || len(objects) > 0 {
		return h.check(version, objects)
	}
	if !h.write {
		return fmt.Errorf("%s: no history yet: %w", h.path, fs.ErrNotExist)
	}
	return h.create(ctx, conn)
}

// create makes the schema of a file that held none when prepare looked. Two
// writers can both have found it so (the watcher and the command that sets
// the time kept): the immediate transaction makes the second wait for the
// first, and it then finds a history and checks it, never taking another file
// for an empty one.
func (h *History) create(ctx context.Context, conn *sql.Conn) error {
	// Incremental vacuum must be chosen before the first table, and outside a
	// transaction; on a file that holds no table yet it writes nothing.
	if _, err := conn.ExecContext(ctx, "PRAGMA auto_vacuum = INCREMENTAL"); err != nil {
		return h.fail(err)
	}
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return h.fail(err)
	}
	defer func() { _ = tx.Rollback() }()
	version, objects, err := h.readSchema(ctx, tx)
	if err != nil {
		return err
	}
	if version != 0 || len(objects) > 0 {
		return h.check(version, objects)
	}
	for _, object := range historySchema {
		if _, err := tx.ExecContext(ctx, object.sql); err != nil {
			return h.fail(err)
		}
	}
	if _, err := tx.ExecContext(
		ctx,
		"PRAGMA user_version = "+strconv.Itoa(historyVersion),
	); err != nil {
		return h.fail(err)
	}
	return h.fail(tx.Commit())
}

// querier is what readSchema reads through: a connection or a transaction.
type querier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

// readSchema reads the file's user_version and its own tables and indexes,
// which leave out SQLite's own (sqlite_sequence and the index of a UNIQUE
// column), in one statement so both come from one state of the file.
func (h *History) readSchema(
	ctx context.Context,
	q querier,
) (version int64, objects []schemaObject, err error) {
	rows, err := q.QueryContext(ctx, `SELECT user_version, type, name, sql
FROM pragma_user_version LEFT JOIN (
  SELECT type, name, sql FROM sqlite_master WHERE name NOT LIKE 'sqlite\_%' ESCAPE '\')
ORDER BY name`)
	if err != nil {
		return 0, nil, h.fail(err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var kind, name, text sql.NullString
		if err := rows.Scan(&version, &kind, &name, &text); err != nil {
			return 0, nil, h.fail(err)
		}
		if name.Valid {
			objects = append(objects, schemaObject{kind.String, name.String, text.String})
		}
	}
	return version, objects, h.fail(rows.Err())
}

// check accepts a file whose user_version is 1 and whose own tables and
// indexes are those of historySchema, each made by the same statement up to
// spacing, and nothing else.
func (h *History) check(version int64, objects []schemaObject) error {
	if version != historyVersion {
		return h.unusable(fmt.Sprintf("its user_version is %d, not %d", version, historyVersion))
	}
	want := slices.Clone(historySchema)
	slices.SortFunc(want, func(a, b schemaObject) int { return cmp.Compare(a.name, b.name) })
	if !slices.EqualFunc(objects, want, func(have, want schemaObject) bool {
		return have.kind == want.kind && have.name == want.name &&
			spaced(have.sql) == spaced(want.sql)
	}) {
		return h.unusable("its tables are not the guard history's")
	}
	return nil
}

// spaced is statement with each run of white space as one space.
func spaced(statement string) string { return strings.Join(strings.Fields(statement), " ") }

// unusable is [ErrHistoryUnusable] for this file, with reason.
func (h *History) unusable(reason string) error {
	return fmt.Errorf("%s: %w: %s", h.path, ErrHistoryUnusable, reason)
}

// fail names the file in an error SQLite returned, and makes a file that is
// not a database, or that SQLite calls corrupt, [ErrHistoryUnusable]. It
// returns nil for nil.
func (h *History) fail(err error) error {
	switch {
	case err == nil || errors.Is(err, ErrHistoryUnusable):
		return err
	case sqlitefile.IsDamaged(err):
		return fmt.Errorf("%s: %w: %w", h.path, ErrHistoryUnusable, err)
	default:
		return fmt.Errorf("%s: %w", h.path, err)
	}
}

// transact runs fn in one transaction, committed when fn returns nil.
func (h *History) transact(ctx context.Context, fn func(*sql.Tx) error) error {
	tx, err := h.db.BeginTx(ctx, nil)
	if err != nil {
		return h.fail(err)
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return h.fail(err)
	}
	return h.fail(tx.Commit())
}

// Insert writes a finished command, with its graph and processes, in one
// transaction. A command whose ID is already there is left as it is, gets no
// graph or process rows and counts as written, so writing one twice is
// harmless. It keeps at most commandKept bytes of the command's text, cut where
// a character starts, and adds the bytes left out to f.Cut; at most
// processesKept processes, the largest peaks, adding the rest to f.Unlisted;
// and at most graphBars bars, merging neighbors by their larger value, so the
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

// keptGraph is graph in at most graphBars bars: while it has more, each two
// neighbors merge into the larger. graph is not changed.
func keptGraph(graph []uint64) []uint64 {
	for len(graph) > graphBars {
		merged := make([]uint64, 0, (len(graph)+1)/2)
		for i := 0; i < len(graph); i += 2 {
			merged = append(merged, slices.Max(graph[i:min(i+2, len(graph))]))
		}
		graph = merged
	}
	return graph
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
// first summaryLength characters of each command; detailsQuery reads those of
// one command, with all of it as kept.
var (
	summaryQuery = fmt.Sprintf(
		"SELECT %s, seq FROM commands WHERE seq > ? ORDER BY seq",
		fmt.Sprintf(commandColumns, "substr(command, 1, "+strconv.Itoa(summaryLength)+")"),
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
// or command holds ?1, case ignored (see [sqlitefile.ContainsFold]).
var findQuery = "SELECT seq FROM commands WHERE " + sqlitefile.ContainsFold +
	"(?1, agent, subagent, project, id, command) ORDER BY seq"

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

// Keep is how long finished commands are kept: the settings row, or
// historyKeepDefault while it is missing. A row that is not a positive number
// of seconds fails, and is left as it is.
func (h *History) Keep(ctx context.Context) (time.Duration, error) {
	var value string
	err := h.db.QueryRowContext(
		ctx, "SELECT value FROM settings WHERE key = ?", keepSetting,
	).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return historyKeepDefault, nil
	}
	if err != nil {
		return 0, h.fail(err)
	}
	seconds, err := strconv.ParseInt(value, 10, 64)
	if err != nil || seconds < 1 || seconds > int64(math.MaxInt64/time.Second) {
		return 0, fmt.Errorf("%s: the time kept, %q, is not a number of seconds", h.path, value)
	}
	return time.Duration(seconds) * time.Second, nil
}

// SetKeep sets how long finished commands are kept, in whole seconds; the
// next trim deletes those that ended longer ago.
func (h *History) SetKeep(ctx context.Context, keep time.Duration) error {
	seconds := int64(keep / time.Second)
	if seconds < 1 {
		return fmt.Errorf(
			"the time finished commands are kept must be a second or more, not %v",
			keep,
		)
	}
	_, err := h.db.ExecContext(ctx, `INSERT INTO settings(key, value) VALUES (?, ?)
ON CONFLICT(key) DO UPDATE SET value = excluded.value`, keepSetting, strconv.FormatInt(seconds, 10))
	return h.fail(err)
}

// Trim is one step of the trim, for the writer: it deletes at most trimStep
// commands in one transaction, first those that ended before now less the
// time kept, then those with the lowest seq beyond historyRows, then those with
// the lowest seq while the pages in use take more than historyBytes, and then
// gives the pages they freed back to the disk. again reports that it stopped
// at trimStep with more to delete, for another step after other writes. It
// also deletes the copy [MoveDamaged] made once its files were all last
// written before now less the time kept, when all it holds would have gone
// anyway.
func (h *History) Trim(ctx context.Context, now time.Time) (again bool, err error) {
	if !h.write {
		return false, fmt.Errorf("%s: a history opened for reading is never trimmed", h.path)
	}
	keep, err := h.Keep(ctx)
	if err != nil {
		return false, err
	}
	since := now.Add(-keep)
	removeDamagedBefore(h.root, since)
	cutoff := since.UnixMilli()
	deleted := 0
	err = h.transact(ctx, func(tx *sql.Tx) error {
		n, err := deleteFirst(ctx, tx, trimStep, "WHERE ended < ? ORDER BY ended", cutoff)
		deleted += n
		if err != nil || deleted == trimStep {
			return err
		}
		var rows int
		if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM commands").Scan(&rows); err != nil {
			return err
		}
		if over := rows - historyRows; over > 0 {
			n, err = deleteFirst(ctx, tx, min(over, trimStep-deleted), "ORDER BY seq")
			deleted += n
			if err != nil {
				return err
			}
		}
		n, err = deleteForSize(ctx, tx, trimStep-deleted)
		deleted += n
		return err
	})
	if err != nil || deleted == 0 {
		return false, err
	}
	if _, err := h.db.ExecContext(ctx, "PRAGMA incremental_vacuum"); err != nil {
		return false, h.fail(err)
	}
	if deleted < trimStep {
		return false, nil
	}
	err = h.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM commands WHERE ended < ?1)
  OR (SELECT count(*) FROM commands) > ?2 OR (`+pagesInUse+`) > ?3`,
		cutoff, historyRows, historyBytes,
	).Scan(&again)
	return again, h.fail(err)
}

// pagesInUse is a query of the bytes the pages in use take: those of the file
// less those on its free list.
const pagesInUse = `SELECT (page_count - freelist_count) * page_size
  FROM pragma_page_count, pragma_freelist_count, pragma_page_size`

// deleteFirst deletes the first limit commands that order lists, order
// taking args, and their graphs and processes. It reports how many it
// deleted.
func deleteFirst(
	ctx context.Context,
	tx *sql.Tx,
	limit int,
	order string,
	args ...any,
) (int, error) {
	result, err := tx.ExecContext(
		ctx,
		"DELETE FROM commands WHERE seq IN (SELECT seq FROM commands "+order+" LIMIT ?)",
		append(args, limit)...,
	)
	if err != nil {
		return 0, err
	}
	n, err := result.RowsAffected()
	return int(n), err
}

// deleteForSize deletes the commands with the lowest seq, at most limit,
// while the pages in use take more than historyBytes, each time as many as
// the bytes over the limit take at the history's average size of a command.
// It reports how many it deleted.
func deleteForSize(ctx context.Context, tx *sql.Tx, limit int) (int, error) {
	deleted := 0
	for deleted < limit {
		var rows int
		var used int64
		if err := tx.QueryRowContext(
			ctx, "SELECT (SELECT count(*) FROM commands), ("+pagesInUse+")",
		).Scan(&rows, &used); err != nil {
			return deleted, err
		}
		if used <= historyBytes || rows == 0 {
			break
		}
		over := (used - historyBytes) * int64(rows)
		want := int(min((over+used-1)/used, int64(limit-deleted)))
		n, err := deleteFirst(ctx, tx, max(want, 1), "ORDER BY seq")
		deleted += n
		if err != nil || n == 0 {
			return deleted, err
		}
	}
	return deleted, nil
}

// removeDamagedBefore deletes the copy [MoveDamaged] made in the guard folder
// root once each of its files was last written before since. It is best
// effort: a copy it cannot delete stays, and the view keeps naming it.
func removeDamagedBefore(root string, since time.Time) {
	copied := damagedFile(root)
	names := []string{copied}
	for _, side := range sideFiles {
		names = append(names, copied+side)
	}
	found := false
	for _, name := range names {
		info, err := os.Lstat(name)
		if err == nil && !info.ModTime().Before(since) {
			return
		}
		found = found || err == nil
	}
	if !found {
		return
	}
	for _, name := range names {
		_ = operation.RemoveIfThere(name)
	}
}

// MoveDamaged moves the history in the guard folder root, a file that cannot
// be used, aside to history.damaged.db with its -wal and -shm files, and
// returns that path, for the writer to start an empty history. It replaces a
// copy already there, so there is one at most: each side file of the copy is
// replaced, or deleted when the history has none, before the history itself
// is moved, so no side file of another database is ever left beside a
// history, where SQLite would read it as that history's. Its files are
// then marked as written now, so the copy is kept for the time kept from the
// moment it was set aside. The history must not be open.
func MoveDamaged(root string) (to string, err error) {
	from, to := historyFile(root), damagedFile(root)
	if _, err := os.Lstat(from); err != nil {
		return "", err
	}
	for _, side := range sideFiles {
		err := os.Rename(from+side, to+side)
		if errors.Is(err, fs.ErrNotExist) {
			err = operation.RemoveIfThere(to + side)
		}
		if err != nil {
			return "", err
		}
	}
	if err := os.Rename(from, to); err != nil {
		return "", err
	}
	// The copy is kept for the time kept from now, when it was set aside,
	// not from when it was last written (see removeDamagedBefore).
	now := time.Now()
	for _, name := range []string{to, to + sideFiles[0], to + sideFiles[1]} {
		if err := os.Chtimes(name, now, now); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return to, err
		}
	}
	return to, nil
}
