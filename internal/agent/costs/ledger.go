package costs

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"

	"github.com/Sawmonabo/workbench/internal/operation"
	"github.com/Sawmonabo/workbench/internal/sqlitefile"
)

// Paths are where the ledger and its companions live, inside Workbench's
// runtime folders (see [LocationsIn]): WORKBENCH_{DATA,STATE}_DIR or
// XDG_{DATA,STATE}_HOME relocate them with everything else Workbench keeps.
type Paths struct {
	Home    string
	Runtime operation.Paths // Workbench's runtime folders the paths below sit in
	Ledger  string          // the durable record, which also keeps the downloaded prices
	State   string          // ingest.log, ingest.err (the worker's output) and ingest.lock
}

// Locations resolves [Paths] from Workbench's runtime folders. It fails, with
// the reason, when they can't be used, for example a runtime folder that is
// not private.
func Locations() (Paths, error) {
	c, err := operation.Resolve(operation.Options{ReadOnly: true})
	if err != nil {
		return Paths{}, err
	}
	return LocationsIn(c.Home, c.Paths), nil
}

func (p Paths) log() string  { return filepath.Join(p.State, "ingest.log") }
func (p Paths) lock() string { return filepath.Join(p.State, "ingest.lock") }

// output is the detached worker's stdout and stderr (see
// [operation.StartDetached]).
func (p Paths) output() string { return filepath.Join(p.State, "ingest.err") }

// Open opens the ledger once the costs gate (see gate) lets it, so nothing
// opens or creates a ledger while the costs files still wait to move. The gate
// first moves the costs folders under agent/ when it may, and the ledger is
// opened where they are then. When the open fails because another process
// moved them in between (see [Paths.moved]), it opens the ledger again where
// they are now. steps, when set, is told what an upgrade of an earlier
// version's ledger is doing while the open runs it (see [UpgradeSteps]).
func (p Paths) Open(ctx context.Context, steps UpgradeSteps) (*Ledger, error) {
	return p.openAgain(ctx, true, steps)
}

// OpenExisting is [Paths.Open] for a caller that must not make a ledger: when
// there is none yet (or only an empty file), it returns nil and no error, and
// makes nothing.
func (p Paths) OpenExisting(ctx context.Context) (*Ledger, error) {
	return p.openAgain(ctx, false, nil)
}

func (p Paths) openAgain(ctx context.Context, create bool, steps UpgradeSteps) (*Ledger, error) {
	ledger, used, err := p.openGated(ctx, create, steps)
	if err != nil && used.moved() {
		ledger, _, err = p.openGated(ctx, create, steps)
	}
	return ledger, err
}

func (p Paths) openGated(
	ctx context.Context,
	create bool,
	steps UpgradeSteps,
) (*Ledger, Paths, error) {
	p, err := gate(ctx, p, false)
	if err != nil {
		return nil, p, err
	}
	if !create {
		info, err := os.Stat(p.Ledger)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			return nil, p, nil
		case err != nil:
			return nil, p, operation.FileError(p.Ledger, err)
		case info.Size() == 0:
			return nil, p, nil
		}
	}
	ledger, err := p.open(ctx, steps)
	return ledger, p, err
}

// open opens the ledger, making a missing one (see openLedger), after making
// its folder private (see [Paths.dataFolder]), which refuses one that exists
// and isn't. Only the import calls it without [Paths.Open], after its own gate
// has moved the ledger. ctx and steps are as [Paths.Open] takes them.
//
// A compaction the open's upgrade left undone is noted in the import log, and
// the next import worker tries it again (see Ledger.compact).
func (p Paths) open(ctx context.Context, steps UpgradeSteps) (*Ledger, error) {
	if _, err := p.dataFolder(""); err != nil {
		return nil, err
	}
	ledger, err := openLedger(ctx, p.Ledger, true, true, steps)
	if err == nil && ledger.compactErr != nil {
		writeLog(p, notCompacted+ledger.compactErr.Error())
	}
	return ledger, err
}

// dataFolder makes the ledger's folder private, with the folder name below it
// ("" for none), and returns that folder. stateFolder does the same for the
// state folder. A folder is made when missing (see
// [operation.PrivateDirectory]), but never a costs folder at its old path:
// one still in use there, as where its file system can't swap two names,
// must already be a folder, so nothing is ever made where a folder moved
// away left a file. A folder in it, such as a price list's, is made.
func (p Paths) dataFolder(name string) (string, error) {
	return p.folder(p.Runtime.Data, filepath.Join(filepath.Dir(p.Ledger), name))
}

func (p Paths) stateFolder() (string, error) { return p.folder(p.Runtime.State, p.State) }

func (p Paths) folder(root, path string) (string, error) {
	f := agentFoldersIn(p.Runtime)
	for _, old := range []string{f.oldData, f.oldState} {
		if operation.Within(old, path) && !operation.IsFolder(old) {
			return "", operation.FileError(old, syscall.ENOTDIR)
		}
	}
	name, err := filepath.Rel(root, path)
	if err != nil {
		return "", err
	}
	return operation.PrivateDirectory(root, name)
}

// moved reports whether p names a costs folder at its old path that is no
// longer a folder: [MoveAgentFolders] moved it after p was resolved. An entry
// that failed with such paths resolves them again and does its work once more.
func (p Paths) moved() bool {
	f := agentFoldersIn(p.Runtime)
	return filepath.Dir(p.Ledger) == f.oldData && !operation.IsFolder(f.oldData) ||
		p.State == f.oldState && !operation.IsFolder(f.oldState)
}

// Ledger is the SQLite record of every response.
type Ledger struct {
	Path string
	db   *sql.DB
	// names are the names committed transactions stored, which a write
	// resolves to their ids (see nameSet).
	names nameCache
	// compacted is whether this open already ran the compaction an upgrade
	// leaves due, and compactErr why it failed (see Ledger.compact).
	compacted  bool
	compactErr error
	// steps is told what the open's upgrade is doing, nil for nothing.
	steps UpgradeSteps
}

// openLedger opens the ledger at path. It never deletes, renames or rebuilds
// an existing file: with upgrade, a version 1, 2, 4, 5 or 6 ledger is
// upgraded in one transaction that keeps every row, and then compacted (see
// Ledger.compact), and steps, when set, is told what the upgrade is doing;
// any other version fails with the path and version and writes nothing. A
// zero-byte file (a crash between creating the file and writing the schema)
// holds nothing and is treated as absent. create makes a missing ledger, in a
// folder that must exist, instead of failing. A cancel of ctx stops a wait
// for another process's upgrade, or this open's own upgrade, which then
// leaves the ledger as it was.
//
// The move of the costs files opens a ledger without upgrade, to verify it
// and fold its write-ahead log in, so the ledger keeps its version until it
// is first opened at its new path, beside the price lists moved there before
// it, which that upgrade takes in (see fillPrices).
//
// The ledger copies emails, organization names and project paths out of the
// tools' private files, so it is private too: its folder is private (see
// [Paths.Open]), the file is made 0600, and a ledger readable by others is
// tightened, with its -wal and -shm files, once it is verified (the mode
// only, never the content; see [sqlitefile.Open]).
func openLedger(
	ctx context.Context,
	path string,
	create, upgrade bool,
	steps UpgradeSteps,
) (*Ledger, error) {
	info, err := os.Stat(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, operation.FileError(path, err)
	}
	exists := err == nil && info.Size() > 0
	if !exists && !create {
		return nil, ledgerProblem(operation.ExitBlocked, path,
			"no ledger yet; run `workbench agent costs ingest --worker` first")
	}
	ledger := &Ledger{Path: path, steps: steps}
	// Write locks are taken at BEGIN, so two processes upgrading or ingesting
	// wait on the busy timeout instead of failing when a read turns into a write.
	// In WAL mode synchronous=NORMAL skips the sync at each commit and keeps the
	// ledger consistent; a crash can lose only the last commits, and each one
	// stores its file's offset with its rows, so the next ingest reads them again.
	// The switch to WAL comes after prepare, so only a verified ledger is
	// switched and a foreign database is never modified. WAL lets a report read
	// the last committed state while a worker ingests.
	if _, err := sqlitefile.Open(ctx, path, sqlitefile.Options{
		Create:  !exists,
		Pragmas: []string{"busy_timeout(" + busyTimeout + ")", "synchronous(NORMAL)"},
		TxLock:  "immediate",
		Prepare: func(ctx context.Context, db *sql.DB) error {
			ledger.db = db
			return ledger.prepare(ctx, exists, upgrade)
		},
	}); err != nil {
		return nil, err
	}
	return ledger, nil
}

// busyTimeout is how long, in milliseconds, a connection to the ledger
// waits for a lock another connection holds before it fails with BUSY.
const busyTimeout = "5000"

func (l *Ledger) prepare(ctx context.Context, exists, upgrade bool) error {
	if !exists {
		return l.fileError(l.create(ctx))
	}
	made, err := hasMeta(ctx, l.db)
	if err != nil {
		return operation.FileError(l.Path, err)
	}
	if !made {
		return ledgerProblem(operation.ExitInvalid, l.Path,
			"not a Workbench costs ledger; move it aside")
	}
	version, err := meta(ctx, l.db, "schema_version")
	if err != nil {
		return operation.FileError(l.Path, err)
	}
	switch version {
	case schemaVersion:
	case "1", "2", "4", "5", "6":
		if !upgrade {
			break
		}
		upgraded, err := l.upgrade(ctx)
		if err != nil {
			return l.fileError(err)
		}
		if upgraded {
			l.step("compacting")
			l.compacted, l.compactErr = true, l.compact(ctx)
		}
		l.step("")
	default:
		return versionProblem(l.Path, version)
	}
	return nil
}

// versionProblem is the error of the ledger at path whose schema version is
// version, which this release cannot use.
func versionProblem(path, version string) error {
	return ledgerProblem(operation.ExitInvalid, path,
		fmt.Sprintf("schema version %q, this tool needs %s", version, schemaVersion))
}

// fileError is err as an error of the ledger's file: an [operation.Error],
// which names the file already, as it is, and nil for nil.
func (l *Ledger) fileError(err error) error {
	if _, named := errors.AsType[*operation.Error](err); named || err == nil {
		return err
	}
	return operation.FileError(l.Path, err)
}

// ledgerProblem is the error of the file at path that is not a ledger this
// release can use, for reason. Its message names path first, from the home
// folder, as every file error does (see [operation.FileError]): an
// [operation.Error] is shown by its message alone, so the path is in it.
func ledgerProblem(code operation.Exit, path, reason string) error {
	return operation.Fail(code, "ledger", operation.FileError(path, errors.New(reason)).Error())
}

// Close releases the database.
func (l *Ledger) Close() error { return l.db.Close() }

type execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// querier is the database or a transaction, to read from.
type querier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// hasMeta reports whether the database has a meta table, which every ledger
// has from its creation.
func hasMeta(ctx context.Context, q querier) (bool, error) {
	var tables int
	err := q.QueryRowContext(
		ctx,
		"SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'meta'",
	).Scan(&tables)
	return tables > 0, err
}

// meta is the stored note key, read from the database or in a transaction,
// "" when absent.
func meta(ctx context.Context, q querier, key string) (string, error) {
	var value string
	err := q.QueryRowContext(ctx, "SELECT value FROM meta WHERE key = ?", key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return value, err
}

func setMeta(ctx context.Context, x execer, key, value string) error {
	_, err := x.ExecContext(
		ctx,
		"INSERT INTO meta (key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value",
		key,
		value,
	)
	return err
}

// Meta is a stored note, "" when absent.
func (l *Ledger) Meta(ctx context.Context, key string) (string, error) {
	return meta(ctx, l.db, key)
}

// SetMeta stores a note.
func (l *Ledger) SetMeta(ctx context.Context, key, value string) error {
	return setMeta(ctx, l.db, key, value)
}

// Empty reports whether the ledger holds no responses of tool.
func (l *Ledger) Empty(ctx context.Context, tool string) (bool, error) {
	var one int
	err := l.db.QueryRowContext(ctx, `SELECT 1 FROM responses
	  WHERE tool = (SELECT id FROM names WHERE kind = ? AND name = ?) LIMIT 1`,
		nameTool, tool).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return true, nil
	}
	return false, err
}

// jsonText is values as a JSON array, for json_each. A list of strings or
// numbers always encodes.
func jsonText[T string | int64](values []T) string {
	if len(values) == 0 {
		return "[]"
	}
	encoded, _ := json.Marshal(values)
	return string(encoded)
}
