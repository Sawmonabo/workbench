package guard

// This file is the history: the commands that finished, each with its
// processes and the graph of its memory over its whole run, kept in a SQLite
// file in the guard folder; here its limits and schema, and its open, check
// and close. Its rows are in historyrows.go, the time kept and the trim in
// historytrim.go, the copies of damaged histories in historydamaged.go, the
// watcher's writer in historywriter.go, how the view and `agent guard
// history` find and follow it in historyfollow.go, and what is said of it in
// historymessages.go.
// Command text may hold a token, so nothing in them logs it or puts it in an
// error.

import (
	"cmp"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/Sawmonabo/workbench/internal/operation"
	"github.com/Sawmonabo/workbench/internal/sqlitefile"
)

// The history's limits.
const (
	// HistoryKeepDefault is how long finished commands are kept while no time
	// is set.
	HistoryKeepDefault = 24 * time.Hour
	// historyRows is the most finished commands kept; historyBytes is the
	// most the pages in use may take.
	historyRows  = 20000
	historyBytes = 256 << 20
	// commandKept is the most bytes of a command's text kept.
	commandKept = 64 << 10
	// processesKept is the most processes kept for one command.
	processesKept = 10000
	// GraphBars is the most bars a command's memory graph has, running or
	// finished.
	GraphBars = 120
	// trimStep is the most commands one Trim step deletes, and trimProcesses
	// and trimText the most process rows and bytes of command text it deletes
	// after its first command: deleting those, and the vacuum that gives
	// their pages back, take most of a step's time, so a step of the largest
	// commands takes about as long as one of 200 small ones.
	trimStep      = 200
	trimProcesses = 2000
	trimText      = 512 << 10
	// SummaryLength is how many characters of its command a [Summary] holds.
	SummaryLength = 300
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
// and by the command that sets the time kept, and for reading by the view
// (see [FollowHistory]). A writer holds file, a descriptor of the file it
// opened, from just after SQLite opened it until after the connection has
// closed, for [History.Check], which reads the header through it (see
// [sqlitefile.CheckHeader]); opened is that file's identity, and sides its
// -wal and -shm files as last seen at its path, for [History.Close]. For a
// reader FollowHistory opened, opened is the identity of the file at its
// path just before it was opened.
type History struct {
	root, path string
	write      bool
	db         *sql.DB
	file       *os.File
	opened     os.FileInfo
	sides      []os.FileInfo
}

// HistoryFile is the history in the guard folder root; damagedFile is where
// a history that cannot be used is moved, and olderDamagedFile where the copy
// already there goes first (see [MoveDamaged]), numbered as logrotate numbers
// the generations it keeps, the higher the older.
func HistoryFile(root string) string      { return filepath.Join(root, "history.db") }
func damagedFile(root string) string      { return filepath.Join(root, "history.damaged.db") }
func olderDamagedFile(root string) string { return filepath.Join(root, "history.damaged.2.db") }

// OpenHistory opens the history in the guard folder root, a private folder
// that must exist. With write it makes a missing file, empty and 0600 before
// SQLite opens it so the files beside it are private too, writes its schema
// and switches it to WAL; the connection is the only one. Without, it opens the
// file for queries only and never makes it: a missing file, or one that holds
// no schema yet, fails with an error matching fs.ErrNotExist, an empty
// history. Either way, a history readable by others is made private, with its
// -wal and -shm files, once it is found to be one (see [sqlitefile.Open]),
// since a command's text may hold a token. A file that is not a history of
// version 1 fails with [ErrHistoryUnusable] and is left as it is.
func OpenHistory(root string, write bool) (*History, error) {
	h := &History{root: root, path: HistoryFile(root), write: write}
	if !write {
		if _, err := os.Stat(h.path); err != nil {
			return nil, operation.FileError(h.path, err)
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
		// The one descriptor of the file this process opens beside SQLite's
		// (see History.Check); it closes only after the connection.
		file, err := os.Open(h.path)
		var info os.FileInfo
		if err == nil {
			info, err = file.Stat()
		}
		if err != nil {
			_ = db.Close()
			if file != nil {
				_ = file.Close()
			}
			return nil, operation.FileError(h.path, err)
		}
		h.file, h.opened = file, info
		// SQLite opens the -wal and -shm files at the first read after the
		// switch to WAL, so one is made before they are noted.
		_, _ = db.ExecContext(context.Background(), "PRAGMA user_version")
		h.sides = sidesAt(h.path)
	}
	return h, nil
}

// sidesAt are the files at the -wal and -shm names of the database at path,
// each nil where there is none.
func sidesAt(path string) []os.FileInfo {
	sides := sqlitefile.SideFiles(path)
	infos := make([]os.FileInfo, len(sides))
	for i, side := range sides {
		infos[i], _ = os.Lstat(side)
	}
	return infos
}

// Check is the writer's cheap look, once a minute, at the file it holds open:
// SQLite reads a page from the file only when its page cache does not hold it,
// so a small history that fits the cache could be overwritten or replaced
// and the writer would go on writing into it until it next opened it. It
// fails with an error matching fs.ErrNotExist when the path names no file,
// and with [ErrHistoryUnusable] when it names another file (by device and
// inode) than the one opened, or one whose header is not a database's (see
// [sqlitefile.CheckHeader]). It reads the header alone, through the
// descriptor the writer holds, never opening the path, so the writer's
// SQLite locks stay its own; and so it costs the same whatever the history's
// size: a full integrity check reads every page. While the path names the
// opened file, it notes the -wal and -shm files beside it again, for
// [History.Close].
func (h *History) Check() error {
	if !h.write {
		return operation.FileError(h.path,
			errors.New("a history opened for reading is never checked"))
	}
	info, err := os.Stat(h.path)
	switch {
	case err != nil:
		return operation.FileError(h.path, err)
	case !os.SameFile(info, h.opened):
		return h.unusable("it is another file than the one the writer opened")
	}
	h.sides = sidesAt(h.path)
	err = sqlitefile.CheckHeader(h.file)
	if errors.Is(err, sqlitefile.ErrNotADatabase) {
		return fmt.Errorf("%w: %w", ErrHistoryUnusable, err)
	}
	return err
}

// Close closes the history. The last connection to close checkpoints the WAL
// and removes the -wal and -shm files, but SQLite does neither once the path
// no longer names the database it opened (sqlite3PagerClose checks
// databaseIsUnmoved), and the WAL is part of the database's persistent state:
// a history moved elsewhere would lose the rows written since the last
// checkpoint, and the next file at the path would be read with the opened
// one's side files, which SQLite lists among the ways to corrupt a database.
// So a writer whose path names no file, or another file, first checkpoints
// the WAL into the file it opened, wherever that is now, and empties it; then
// it closes and deletes the -wal and -shm files at the path that are the ones
// it last saw beside the file it opened (see [History.Check]). Side files that
// came with another file stay with it. The checkpoint waits at most the busy
// timeout for another connection to that file; a WAL it could not empty is
// deleted all the same. A writer's descriptor of the file it opened closes
// last, once the connection has (see [sqlitefile.CheckHeader]).
func (h *History) Close() error {
	err := h.closeDB()
	if h.file != nil {
		err = cmp.Or(err, h.file.Close())
	}
	return err
}

// closeDB is [History.Close] but for the writer's descriptor.
func (h *History) closeDB() error {
	if !h.write {
		return h.db.Close()
	}
	if info, err := os.Stat(h.path); err == nil && os.SameFile(info, h.opened) {
		return h.db.Close()
	}
	_, _ = h.db.ExecContext(context.Background(), "PRAGMA wal_checkpoint(TRUNCATE)")
	err := h.db.Close()
	for i, side := range sqlitefile.SideFiles(h.path) {
		info, statErr := os.Lstat(side)
		if statErr == nil && h.sides[i] != nil && os.SameFile(info, h.sides[i]) {
			err = cmp.Or(err, operation.RemoveIfThere(side))
		}
	}
	return err
}

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
		return operation.FileError(h.path, fmt.Errorf("no history yet: %w", fs.ErrNotExist))
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

// unusable is [ErrHistoryUnusable] for this file, with reason, naming the
// file as every file error does (see [operation.FileError]).
func (h *History) unusable(reason string) error {
	return operation.FileError(h.path, fmt.Errorf("%w: %s", ErrHistoryUnusable, reason))
}

// fail names the file in an error SQLite returned, as every file error does
// (see [operation.FileError]), and makes a file that is not a database, or
// that SQLite calls corrupt, [ErrHistoryUnusable]. It returns nil for nil.
func (h *History) fail(err error) error {
	switch {
	case err == nil || errors.Is(err, ErrHistoryUnusable):
		return err
	case sqlitefile.IsDamaged(err):
		return operation.FileError(h.path, fmt.Errorf("%w: %w", ErrHistoryUnusable, err))
	default:
		return operation.FileError(h.path, err)
	}
}

// transact runs fn in one transaction (see [sqlitefile.Transact]), its error
// naming the file (see History.fail).
func (h *History) transact(ctx context.Context, fn func(*sql.Tx) error) error {
	return h.fail(sqlitefile.Transact(ctx, h.db, fn))
}
