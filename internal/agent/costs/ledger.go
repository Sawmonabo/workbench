package costs

import (
	"bytes"
	"cmp"
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"math"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

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
// they are now.
func (p Paths) Open(ctx context.Context) (*Ledger, error) { return p.openAgain(ctx, true) }

// OpenExisting is [Paths.Open] for a caller that must not make a ledger: when
// there is none yet (or only an empty file), it returns nil and no error, and
// makes nothing.
func (p Paths) OpenExisting(ctx context.Context) (*Ledger, error) {
	return p.openAgain(ctx, false)
}

func (p Paths) openAgain(ctx context.Context, create bool) (*Ledger, error) {
	ledger, used, err := p.openGated(ctx, create)
	if err != nil && used.moved() {
		ledger, _, err = p.openGated(ctx, create)
	}
	return ledger, err
}

func (p Paths) openGated(ctx context.Context, create bool) (*Ledger, Paths, error) {
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
			return nil, p, sqlitefile.FileError(p.Ledger, err)
		case info.Size() == 0:
			return nil, p, nil
		}
	}
	ledger, err := p.open()
	return ledger, p, err
}

// open opens the ledger, making a missing one (see [OpenLedger]), after making
// its folder private (see [Paths.dataFolder]), which refuses one that exists
// and isn't. Only the import calls it without [Paths.Open], after its own gate
// has moved the ledger.
//
// A compaction the open's upgrade left undone is noted in the import log, and
// the next import worker tries it again (see Ledger.compact).
func (p Paths) open() (*Ledger, error) {
	if _, err := p.dataFolder(""); err != nil {
		return nil, err
	}
	ledger, err := OpenLedger(p.Ledger, true)
	if err == nil && ledger.compactErr != nil {
		writeLog(p, notCompacted+ledger.compactErr.Error())
	}
	return ledger, err
}

// notCompacted begins the import log's line about a compaction that failed.
const notCompacted = "the ledger is upgraded but not yet compacted: "

// compactAgain runs the compaction an earlier upgrade left due, unless this
// open has tried it, and notes in the import log why it failed. The import
// worker calls it, holding the import lock.
func (l *Ledger) compactAgain(ctx context.Context, p Paths) {
	if l.compacted {
		return
	}
	if err := l.compact(ctx); err != nil {
		writeLog(p, notCompacted+err.Error())
	}
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
			return "", sqlitefile.FileError(old, syscall.ENOTDIR)
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

const (
	schemaVersion = "7"

	// firstTool is the tool of every row written before the tool column
	// existed, and the column's default: its request IDs are stored as they
	// are, every other tool's as <tool>:<id>.
	firstTool = "claude"
)

// responseSchema holds the responses, each name they repeat once in names,
// and the rows waiting for their price tier. A response refers to its tool,
// model, project, session, root, account and subscription by the id of a
// name of that kind (nameTool and the rest; the root is a session), so a
// row reads back as exactly the text it was given, "" and "unknown"
// included. Its request_id is its ledger key packed (packID), account_source
// and subscription_source are the EvidenceRank of each field's evidence, and
// ts is its time in Unix milliseconds, UTC, NULL when it has none.
// tier_pending.root is the row's TierFrom, a name of the session kind.
const responseSchema = `
CREATE TABLE names (
  id   INTEGER PRIMARY KEY,
  kind INTEGER NOT NULL,
  name TEXT NOT NULL,
  UNIQUE (kind, name)
);
CREATE TABLE responses (
  id                  INTEGER PRIMARY KEY,
  request_id          BLOB NOT NULL UNIQUE,
  tool                INTEGER NOT NULL,
  model               INTEGER NOT NULL,
  project             INTEGER NOT NULL,
  session             INTEGER NOT NULL,
  root                INTEGER NOT NULL,
  account             INTEGER NOT NULL,
  subscription        INTEGER NOT NULL,
  account_source      INTEGER NOT NULL,
  subscription_source INTEGER NOT NULL,
  ts                  INTEGER,
  input               INTEGER NOT NULL,
  output              INTEGER NOT NULL,
  cache_write_5m      INTEGER NOT NULL,
  cache_write_1h      INTEGER NOT NULL,
  cache_read          INTEGER NOT NULL
);
CREATE TABLE tier_pending (
  request_id BLOB PRIMARY KEY,
  root       INTEGER NOT NULL
) WITHOUT ROWID;
`

// responseIndexes are the indexes of responses beside its request_id: the rows
// of a thread root, and the rows whose account or plan is still open to
// correction by time, which the account resolution reads (accountsQuery
// states openRows in the same words, or SQLite cannot use the index).
const responseIndexes = `
CREATE INDEX responses_root ON responses (tool, root);
CREATE INDEX responses_open ON responses (tool, ts) WHERE ` + openRows + `;
`

// openRows is true for a row with a field its transcript did not decide.
const openRows = "account_source < 4 OR subscription_source < 4"

// The kinds of names.
const (
	nameTool = iota + 1
	nameModel
	nameProject
	nameSession // a session, a thread root or a TierFrom
	nameAccount
	nameSubscription
)

const schema = responseSchema + responseIndexes + `
CREATE TABLE files (
  path      TEXT PRIMARY KEY,
  offset    INTEGER NOT NULL,
  size      INTEGER NOT NULL,
  mtime_ns  INTEGER NOT NULL,
  last_seen TEXT NOT NULL,
  state     TEXT NOT NULL DEFAULT '',
  head      TEXT NOT NULL DEFAULT ''
);
CREATE TABLE tier_changes (
  thread_id TEXT NOT NULL,
  ts        TEXT NOT NULL,
  tier      TEXT NOT NULL,
  PRIMARY KEY (thread_id, ts, tier)
);
CREATE TABLE meta (key TEXT PRIMARY KEY, value TEXT NOT NULL);
` + subscriptionSchema + markSchema + priceSchema

// subscriptionSchema is the part of the schema that attributes rows to a
// subscription, also created by the upgrade of an older ledger. Every time is
// in timeLayout text; a response's time meets them as that text (storedTime).
//
// responses.account_source holds the evidence level of account (the email)
// and responses.subscription_source that of subscription (EvidenceTranscript,
// EvidenceSession, EvidenceObserved, EvidenceInferred or EvidenceUnknown, by
// rank): each is decided on its own, as a transcript can name a plan without
// naming an account. responses.subscription is a subscription id ("unknown"
// when none) and responses.root the session a hook names for the row.
//
//   - session_accounts: the sign-in a hook found when a session started (or
//     resumed) at since, for the root session of a tool; a row of that
//     session takes the binding with the latest since at or before its time.
//   - sign_ins: the sign-in an ingest run observed for a tool at a time.
//   - subscriptions: the display label of each subscription id.
//   - thread_parents: the thread a Codex subagent was spawned by, as its own
//     file names it; ResolveAccounts walks them so a row's root is the root
//     thread however deep the subagent is and in whatever order files are read.
const subscriptionSchema = `
CREATE TABLE session_accounts (
  tool         TEXT NOT NULL,
  session      TEXT NOT NULL,
  since        TEXT NOT NULL,
  account      TEXT NOT NULL,
  subscription TEXT NOT NULL,
  PRIMARY KEY (tool, session, since)
);
CREATE TABLE sign_ins (
  tool         TEXT NOT NULL,
  at           TEXT NOT NULL,
  account      TEXT NOT NULL,
  subscription TEXT NOT NULL,
  PRIMARY KEY (tool, at)
);
CREATE TABLE subscriptions (id TEXT PRIMARY KEY, label TEXT NOT NULL);
CREATE TABLE thread_parents (
  tool   TEXT NOT NULL,
  thread TEXT NOT NULL,
  parent TEXT NOT NULL,
  PRIMARY KEY (tool, thread)
);
`

// markSchema keeps the account marks (AccountMark) ingest reads, which outlive
// the transcripts that hold them: at is in timeLayout, "" for a pair, and an
// unused account or org is "". ResolveAccounts infers from them the account
// of rows nothing else names (accountBasis.inferred).
const markSchema = `
CREATE TABLE account_marks (
  tool    TEXT NOT NULL,
  kind    TEXT NOT NULL,
  at      TEXT NOT NULL,
  account TEXT NOT NULL,
  org     TEXT NOT NULL,
  PRIMARY KEY (tool, kind, at, account, org)
);
`

// priceSchema keeps each tool's official price list, the one a refresh
// downloaded last (see refreshOfficial): prices holds its rows, which a
// report's price list is built from (see loadCard), and price_lists where and
// when it was downloaded, when the page was last asked, and the page's
// validators for the next conditional request. Times are seconds since the
// epoch.
const priceSchema = `
CREATE TABLE prices(
  tool           TEXT NOT NULL,   -- claude, codex
  prefix         TEXT NOT NULL,   -- a model, or a family such as claude-opus
  input          REAL NOT NULL,   -- USD per million tokens, as are the rest
  output         REAL NOT NULL,
  cache_write_5m REAL NOT NULL,
  cache_write_1h REAL NOT NULL,
  cache_read     REAL NOT NULL,
  PRIMARY KEY(tool, prefix)) WITHOUT ROWID;

CREATE TABLE price_lists(
  tool          TEXT PRIMARY KEY,
  url           TEXT NOT NULL,
  fetched_at    REAL NOT NULL,    -- when the list was last downloaded
  checked_at    REAL NOT NULL,    -- when the page was last asked
  etag          TEXT NOT NULL,
  last_modified TEXT NOT NULL) WITHOUT ROWID;
`

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
}

// OpenLedger opens the ledger at path. It never deletes, renames or rebuilds
// an existing file: a version 1, 2, 4, 5 or 6 ledger is upgraded in one
// transaction that keeps every row, and then compacted (see Ledger.compact);
// any other version fails with the path and version and writes nothing. A
// zero-byte file (a crash between creating the file and writing the schema)
// holds nothing and is treated as absent. create makes a missing ledger, in a
// folder that must exist, instead of failing.
//
// The ledger copies emails, organization names and project paths out of the
// tools' private files, so it is private too: its folder is private (see
// [Paths.Open]), the file is made 0600, and a ledger readable by others is
// tightened, with its -wal and -shm files, once it is verified (the mode
// only, never the content; see [sqlitefile.Open]).
func OpenLedger(path string, create bool) (*Ledger, error) { return openLedger(path, create, true) }

// openLedger is [OpenLedger], which upgrades a ledger of an earlier version
// only with upgrade. The move of the costs files opens a ledger without it,
// to verify it and fold its write-ahead log in, so the ledger keeps its
// version until it is first opened at its new path, beside the price lists
// moved there before it, which that upgrade takes in (see fillPrices).
func openLedger(path string, create, upgrade bool) (*Ledger, error) {
	info, err := os.Stat(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, sqlitefile.FileError(path, err)
	}
	exists := err == nil && info.Size() > 0
	if !exists && !create {
		return nil, ledgerProblem(operation.ExitBlocked, path,
			"no ledger yet; run `workbench agent costs ingest --worker` first")
	}
	ledger := &Ledger{Path: path}
	// Write locks are taken at BEGIN, so two processes upgrading or ingesting
	// wait on the busy timeout instead of failing when a read turns into a write.
	// In WAL mode synchronous=NORMAL skips the sync at each commit and keeps the
	// ledger consistent; a crash can lose only the last commits, and each one
	// stores its file's offset with its rows, so the next ingest reads them again.
	// The switch to WAL comes after prepare, so only a verified ledger is
	// switched and a foreign database is never modified. WAL lets a report read
	// the last committed state while a worker ingests.
	if _, err := sqlitefile.Open(path, sqlitefile.Options{
		Create:  !exists,
		Pragmas: []string{"busy_timeout(5000)", "synchronous(NORMAL)"},
		TxLock:  "immediate",
		Prepare: func(db *sql.DB) error {
			ledger.db = db
			return ledger.prepare(exists, upgrade)
		},
	}); err != nil {
		return nil, err
	}
	return ledger, nil
}

func (l *Ledger) prepare(exists, upgrade bool) error {
	ctx := context.Background()
	if !exists {
		return l.fileError(l.create(ctx))
	}
	var tables int
	if err := l.db.QueryRowContext(
		ctx,
		"SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'meta'",
	).Scan(&tables); err != nil {
		return sqlitefile.FileError(l.Path, err)
	}
	if tables == 0 {
		return ledgerProblem(operation.ExitInvalid, l.Path,
			"not a Workbench costs ledger; move it aside")
	}
	version, err := l.Meta("schema_version")
	if err != nil {
		return sqlitefile.FileError(l.Path, err)
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
			l.compacted, l.compactErr = true, l.compact(ctx)
		}
	default:
		return ledgerProblem(operation.ExitInvalid, l.Path,
			fmt.Sprintf("schema version %q, this tool needs %s", version, schemaVersion))
	}
	return nil
}

// fileError is err as an error of the ledger's file: an [operation.Error],
// which names the file already, as it is, and nil for nil.
func (l *Ledger) fileError(err error) error {
	if _, named := errors.AsType[*operation.Error](err); named || err == nil {
		return err
	}
	return sqlitefile.FileError(l.Path, err)
}

// ledgerProblem is the error of the file at path that is not a ledger this
// release can use, for reason. Its message names path first, from the home
// folder, as every file error does (see [sqlitefile.FileError]): an
// [operation.Error] is shown by its message alone, so the path is in it.
func ledgerProblem(code operation.Exit, path, reason string) error {
	return operation.Fail(code, "ledger", sqlitefile.FileError(path, errors.New(reason)).Error())
}

// create writes the schema of a ledger that was missing when OpenLedger looked.
// Two processes can both have seen it missing (the first apply's ingest and a
// session hook, say): the immediate transaction makes the second wait for the
// first, and it then finds the ledger made and leaves it as it is. A plain
// CREATE TABLE IF NOT EXISTS would instead accept a foreign database that
// happens to share a table name.
func (l *Ledger) create(ctx context.Context) error {
	return l.transact(ctx, func(tx *sql.Tx) error {
		var made int
		if err := tx.QueryRowContext(
			ctx,
			"SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'meta'",
		).Scan(&made); err != nil {
			return err
		}
		if made > 0 {
			// Another process created it while this one waited; it must be a
			// ledger of this version, as prepare requires of an existing one.
			var version string
			if err := tx.QueryRowContext(
				ctx, "SELECT value FROM meta WHERE key = 'schema_version'",
			).Scan(&version); err != nil || version != schemaVersion {
				return ledgerProblem(operation.ExitInvalid, l.Path,
					"a database appeared that is not a Workbench costs ledger of schema version "+
						schemaVersion+"; move it aside")
			}
			return nil
		}
		if _, err := tx.ExecContext(ctx, schema); err != nil {
			return err
		}
		return setMeta(ctx, tx, "schema_version", schemaVersion)
	})
}

// upgrade is the only write to existing data, in one transaction that keeps
// every row: version 1 gains the tool column, versions 1 and 2 gain the saved
// file state, the tier tables and the subscription columns and tables,
// versions 1, 2 and 4 gain the account marks table, versions 1, 2, 4 and 5
// gain the price tables, filled in the same transaction from the price list
// files beside the ledger (see fillPrices), so no report is priced from fewer
// prices than before, and every version below 7 has its responses and
// tier_pending converted to names and numbers (see slim), which leaves the
// compaction due. A row upgraded from version 1 or 2 keeps its email, its
// subscription is unknown and its evidence is unknown, as the earlier
// "session" and "sweep" sources are gone; a Claude Code row's root is its
// session. upgraded is false when another process upgraded the ledger first.
//
// The upgrade of a large ledger takes seconds, and every other process that
// opens the ledger meanwhile waits for it (see beginLong), then finds it
// upgraded.
func (l *Ledger) upgrade(ctx context.Context) (upgraded bool, err error) {
	tx, err := l.beginLong(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	var version string
	if err := tx.QueryRowContext(
		ctx, "SELECT value FROM meta WHERE key = 'schema_version'",
	).Scan(&version); err != nil {
		return false, err
	}
	var statements []string
	fill := false
	switch version {
	case "1":
		statements = append(statements,
			"ALTER TABLE responses ADD COLUMN tool TEXT NOT NULL DEFAULT 'claude'",
		)
		fallthrough
	case "2":
		statements = append(
			statements,
			"ALTER TABLE files ADD COLUMN state TEXT NOT NULL DEFAULT ''",
			"ALTER TABLE files ADD COLUMN head TEXT NOT NULL DEFAULT ''",
			`CREATE TABLE IF NOT EXISTS tier_changes (thread_id TEXT NOT NULL, ts TEXT NOT NULL,
			  tier TEXT NOT NULL, PRIMARY KEY (thread_id, ts, tier))`,
			`CREATE TABLE IF NOT EXISTS tier_pending (request_id TEXT PRIMARY KEY, root TEXT NOT NULL)`,
			"ALTER TABLE responses ADD COLUMN subscription TEXT NOT NULL DEFAULT 'unknown'",
			"ALTER TABLE responses ADD COLUMN subscription_source TEXT NOT NULL DEFAULT 'unknown'",
			"ALTER TABLE responses ADD COLUMN root TEXT NOT NULL DEFAULT ''",
			"UPDATE responses SET account_source = '"+EvidenceUnknown+"'",
			"UPDATE responses SET root = session_id WHERE tool = '"+firstTool+"'",
			subscriptionSchema,
		)
		fallthrough
	case "4":
		statements = append(statements, markSchema)
		fallthrough
	case "5":
		statements, fill = append(statements, priceSchema), true
	case "6":
	default:
		return false, nil // another process upgraded it first
	}
	if err := execAll(ctx, tx, statements...); err != nil {
		return false, err
	}
	if fill {
		if err := fillPrices(ctx, tx, l.Path); err != nil {
			return false, err
		}
	}
	if err := slim(ctx, tx, l.Path); err != nil {
		return false, err
	}
	if err := setMeta(ctx, tx, compactDue, "1"); err != nil {
		return false, err
	}
	if err := setMeta(ctx, tx, "schema_version", schemaVersion); err != nil {
		return false, err
	}
	return true, tx.Commit()
}

// upgradeWait is how long an open waits for another process's upgrade (see
// beginLong): the upgrade of the largest ledger measured, 1.59 million rows,
// took 20 to 24 seconds, and a slower computer may take a few times that.
const upgradeWait = 2 * time.Minute

// beginLong begins the upgrade's transaction, which takes the write lock. A
// connection waits on a lock for its busy timeout, five seconds, and an
// upgrade holds it longer: while another process holds it, BEGIN is tried
// again until upgradeWait has passed.
func (l *Ledger) beginLong(ctx context.Context) (*sql.Tx, error) {
	deadline := time.Now().Add(upgradeWait)
	for {
		tx, err := l.db.BeginTx(ctx, nil)
		if !sqlitefile.IsBusy(err) || time.Now().After(deadline) {
			return tx, err
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// compactDue is the meta note the upgrade to version 7 stores: the rows it
// rewrote left the file's old pages free, and compact gives them back.
const compactDue = "compact_due"

// compact rewrites the file without its free pages once an upgrade left the
// note compactDue: VACUUM, then the note's deletion, then a checkpoint that
// empties the write-ahead log VACUUM filled. Nothing else rewrites the file.
// A VACUUM that fails (another process writing past the busy timeout, a full
// disk) leaves a valid ledger with free pages and the note, and the next
// import worker tries again (see ingestLocked).
func (l *Ledger) compact(ctx context.Context) error {
	if due, err := l.Meta(compactDue); err != nil || due == "" {
		return err
	}
	if _, err := l.db.ExecContext(ctx, "VACUUM"); err != nil {
		return fmt.Errorf("compacting the ledger: %w", err)
	}
	if _, err := l.db.ExecContext(ctx, "DELETE FROM meta WHERE key = ?", compactDue); err != nil {
		return err
	}
	_, err := l.db.ExecContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)")
	return err
}

// slim converts responses and tier_pending of version 6, in the upgrade's
// transaction, to the layout of responseSchema: names from every distinct
// value, each row under its rowid with its names' ids, its packed key, its
// evidence ranks and its time in milliseconds, and each waiting row with its
// packed key and its TierFrom's id. An evidence word other than the five, or
// a time that is neither "" nor exactly in timeLayout, refuses the upgrade,
// naming the file and the value. Before the old tables are dropped every
// converted row is read back and compared with its old one (see checkSlim).
func slim(ctx context.Context, tx *sql.Tx, path string) error {
	if err := execAll(ctx, tx,
		"ALTER TABLE responses RENAME TO responses_6",
		"ALTER TABLE tier_pending RENAME TO tier_pending_6",
		responseSchema,
	); err != nil {
		return err
	}
	names, err := newSlimNames(ctx, tx)
	if err != nil {
		return err
	}
	defer names.close()
	insert, err := tx.PrepareContext(ctx, insertRows(slimBatch))
	if err != nil {
		return err
	}
	defer func() { _ = insert.Close() }()
	rows, err := tx.QueryContext(ctx, `SELECT rowid, `+textColumns+` FROM responses_6`)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	var r textRow
	dest := r.fields()
	batch := make([]any, 0, slimBatch*len(dest))
	for rows.Next() {
		if err := rows.Scan(dest...); err != nil {
			return err
		}
		accountRank, ok := storedRank(r.text[colAccountSource])
		if !ok {
			return slimRefusal(path, r, "the account evidence", colAccountSource)
		}
		subscriptionRank, ok := storedRank(r.text[colSubscriptionSource])
		if !ok {
			return slimRefusal(path, r, "the plan evidence", colSubscriptionSource)
		}
		var when any
		if ts := r.text[colTime]; len(ts) > 0 {
			ms, ok := parseStoredTime(string(ts))
			if !ok {
				return slimRefusal(path, r, "the time", colTime)
			}
			when = ms
		}
		batch = append(batch, r.rowid, packID(string(r.text[colID])))
		for col := colTool; col <= colSubscription; col++ {
			id, err := names.id(nameKinds[col], r.text[col])
			if err != nil {
				return err
			}
			batch = append(batch, id)
		}
		batch = append(batch, accountRank, subscriptionRank, when,
			r.tokens[0], r.tokens[1], r.tokens[2], r.tokens[3], r.tokens[4])
		if len(batch) == cap(batch) {
			if _, err := insert.ExecContext(ctx, batch...); err != nil {
				return err
			}
			batch = batch[:0]
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if len(batch) > 0 {
		if _, err := tx.ExecContext(ctx, insertRows(len(batch)/len(dest)), batch...); err != nil {
			return err
		}
	}
	if err := slimPending(ctx, tx, names); err != nil {
		return err
	}
	if err := checkSlim(ctx, tx); err != nil {
		return err
	}
	return execAll(ctx, tx, "DROP TABLE responses_6", "DROP TABLE tier_pending_6", responseIndexes)
}

// execAll runs statements in tx, in order, up to the first that fails.
func execAll(ctx context.Context, tx *sql.Tx, statements ...string) error {
	for _, statement := range statements {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return err
		}
	}
	return nil
}

// slimPending converts tier_pending (see slim).
func slimPending(ctx context.Context, tx *sql.Tx, names *slimNames) error {
	insert, err := tx.PrepareContext(
		ctx,
		"INSERT INTO tier_pending (request_id, root) VALUES (?, ?)",
	)
	if err != nil {
		return err
	}
	defer func() { _ = insert.Close() }()
	rows, err := tx.QueryContext(ctx, "SELECT request_id, root FROM tier_pending_6")
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var id string
		var root sql.RawBytes
		if err := rows.Scan(&id, &root); err != nil {
			return err
		}
		rootID, err := names.id(nameSession, root)
		if err != nil {
			return err
		}
		if _, err := insert.ExecContext(ctx, packID(id), rootID); err != nil {
			return err
		}
	}
	return rows.Err()
}

// slimBatch is how many rows slim inserts in one statement.
const slimBatch = 100

// insertRows is the statement that inserts n converted responses, each with
// its id and packedColumns.
func insertRows(n int) string {
	return `INSERT INTO responses (id, ` + packedColumns + `) VALUES ` +
		strings.TrimSuffix(strings.Repeat("(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?),", n), ",")
}

// slimRefusal is the error of a value slim cannot convert: what, in the
// column col of the response r.
func slimRefusal(path string, r textRow, what string, col int) error {
	return ledgerProblem(operation.ExitInvalid, path, fmt.Sprintf(
		"the response %q holds %s %q, which this release cannot convert to schema version %s; "+
			"the ledger was left as it was",
		r.text[colID], what, r.text[col], schemaVersion))
}

// slimNames are the names slim stores, numbered from 1 as it meets them.
type slimNames struct {
	ctx    context.Context
	insert *sql.Stmt
	ids    [nameSubscription + 1]map[string]int64
	next   int64
}

func newSlimNames(ctx context.Context, tx *sql.Tx) (*slimNames, error) {
	insert, err := tx.PrepareContext(ctx, "INSERT INTO names (id, kind, name) VALUES (?, ?, ?)")
	if err != nil {
		return nil, err
	}
	n := &slimNames{ctx: ctx, insert: insert, next: 1}
	for kind := range n.ids {
		n.ids[kind] = map[string]int64{}
	}
	return n, nil
}

func (n *slimNames) close() { _ = n.insert.Close() }

// id is the id of name, a name of kind, which it stores when it meets it
// first.
func (n *slimNames) id(kind int, name []byte) (int64, error) {
	if id, ok := n.ids[kind][string(name)]; ok {
		return id, nil
	}
	id := n.next
	if _, err := n.insert.ExecContext(n.ctx, id, kind, string(name)); err != nil {
		return 0, err
	}
	n.ids[kind][string(name)], n.next = id, id+1
	return id, nil
}

// textRow is a response as version 6 stored it, read for slim and checkSlim:
// its rowid, its text columns in the order of textColumns, valid until the
// next row is read, and its token counts.
type textRow struct {
	rowid  int64
	text   [colTime + 1]sql.RawBytes
	tokens [5]int64
}

// The text columns of a response, in the order of textColumns and
// packedColumns; nameKinds gives the kind of name of each name column.
const (
	colID = iota
	colTool
	colModel
	colProject
	colSession
	colRoot
	colAccount
	colSubscription
	colAccountSource
	colSubscriptionSource
	colTime
)

var nameKinds = [...]int{
	colTool: nameTool, colModel: nameModel, colProject: nameProject, colSession: nameSession,
	colRoot: nameSession, colAccount: nameAccount, colSubscription: nameSubscription,
}

// textColumns are the columns of a version 6 response, in the order of
// textRow.
const textColumns = `request_id, tool, model, project, session_id, root, account,
  subscription, account_source, subscription_source, ts,
  input, output, cache_write_5m, cache_write_1h, cache_read`

// packedColumns are the same columns of a response as responseSchema stores
// it.
const packedColumns = `request_id, tool, model, project, session, root, account,
  subscription, account_source, subscription_source, ts,
  input, output, cache_write_5m, cache_write_1h, cache_read`

func (r *textRow) fields() []any {
	out := []any{&r.rowid}
	for i := range r.text {
		out = append(out, &r.text[i])
	}
	for i := range r.tokens {
		out = append(out, &r.tokens[i])
	}
	return out
}

// checkSlim compares what slim wrote with the version 6 tables it read, before
// they are dropped: as many rows in each table, the same sum of each token
// column per tool, and every converted row read back through names, decoded,
// equal to its old row field for field. The first difference is the error,
// which rolls the upgrade back.
func checkSlim(ctx context.Context, tx *sql.Tx) error {
	if err := checkSlimTotals(ctx, tx); err != nil {
		return err
	}
	names, err := loadNames(ctx, tx)
	if err != nil {
		return err
	}
	// Both tables in the order of their rowids, which slim kept: the rows
	// pair up one by one.
	olds, err := tx.QueryContext(
		ctx,
		`SELECT rowid, `+textColumns+` FROM responses_6 ORDER BY rowid`,
	)
	if err != nil {
		return err
	}
	defer func() { _ = olds.Close() }()
	news, err := tx.QueryContext(ctx, `SELECT id, `+packedColumns+` FROM responses ORDER BY id`)
	if err != nil {
		return err
	}
	defer func() { _ = news.Close() }()
	var old textRow
	var p packedRow
	oldDest, newDest := old.fields(), p.fields()
	var buf []byte
	for olds.Next() {
		if err := olds.Scan(oldDest...); err != nil {
			return err
		}
		p = packedRow{}
		if news.Next() {
			if err := news.Scan(newDest...); err != nil {
				return err
			}
		}
		var same bool
		if same, buf, err = p.decodesTo(old, names, buf); !same || err != nil {
			return fmt.Errorf("the conversion to schema version %s changed the response %q "+
				"(row %d) to %s (%v)", schemaVersion, old.text[colID], old.rowid, p, err)
		}
	}
	if err := olds.Err(); err != nil {
		return err
	}
	if err := news.Err(); err != nil {
		return err
	}
	return checkSlimPending(ctx, tx, names)
}

// checkSlimTotals checks that the converted tables hold as many rows as the
// version 6 ones and the same sum of each token column per tool.
func checkSlimTotals(ctx context.Context, tx *sql.Tx) error {
	for _, query := range []string{
		`SELECT (SELECT COUNT(*) FROM responses_6), (SELECT COUNT(*) FROM responses)`,
		`SELECT (SELECT COUNT(*) FROM tier_pending_6), (SELECT COUNT(*) FROM tier_pending)`,
	} {
		var before, after int64
		if err := tx.QueryRowContext(ctx, query).Scan(&before, &after); err != nil {
			return err
		}
		if before != after {
			return fmt.Errorf("the conversion to schema version %s kept %d of %d rows",
				schemaVersion, after, before)
		}
	}
	sums := func(query string) (map[string][5]int64, error) {
		rows, err := tx.QueryContext(ctx, query)
		if err != nil {
			return nil, err
		}
		defer func() { _ = rows.Close() }()
		out := map[string][5]int64{}
		for rows.Next() {
			var tool string
			var s [5]int64
			if err := rows.Scan(&tool, &s[0], &s[1], &s[2], &s[3], &s[4]); err != nil {
				return nil, err
			}
			out[tool] = s
		}
		return out, rows.Err()
	}
	const totals = `SUM(input), SUM(output), SUM(cache_write_5m), SUM(cache_write_1h), SUM(cache_read)`
	before, err := sums(`SELECT tool, ` + totals + ` FROM responses_6 GROUP BY tool`)
	if err != nil {
		return err
	}
	after, err := sums(`SELECT n.name, ` + totals + ` FROM responses
	  JOIN names n ON n.id = responses.tool GROUP BY n.name`)
	if err != nil {
		return err
	}
	if !maps.Equal(before, after) {
		return fmt.Errorf("the conversion to schema version %s changed the token sums per tool "+
			"from %v to %v", schemaVersion, before, after)
	}
	return nil
}

// checkSlimPending compares every converted tier_pending row, decoded, with
// its old one.
func checkSlimPending(ctx context.Context, tx *sql.Tx, names []nameKey) error {
	rows, err := tx.QueryContext(ctx, "SELECT request_id, root FROM tier_pending")
	if err != nil {
		return err
	}
	converted := map[string]string{}
	for rows.Next() {
		var packed []byte
		var root int64
		if err := rows.Scan(&packed, &root); err != nil {
			_ = rows.Close()
			return err
		}
		id, err := unpackID(packed)
		if err != nil || root < 1 || root >= int64(len(names)) || names[root].kind != nameSession {
			_ = rows.Close()
			return fmt.Errorf("the conversion to schema version %s wrote a waiting row "+
				"it cannot read (%x, %d)", schemaVersion, packed, root)
		}
		converted[id] = names[root].name
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if err := rows.Err(); err != nil {
		return err
	}
	rows, err = tx.QueryContext(ctx, "SELECT request_id, root FROM tier_pending_6")
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var id, root string
		if err := rows.Scan(&id, &root); err != nil {
			return err
		}
		if got, ok := converted[id]; !ok || got != root {
			return fmt.Errorf("the conversion to schema version %s changed the waiting row %q "+
				"from %q to %q", schemaVersion, id, root, got)
		}
	}
	return rows.Err()
}

// packedRow is a response as responseSchema stores it, as checkSlim reads it
// beside its old row: its id, its packed key, and the columns after it in the
// order of packedColumns (the text columns one place before their col
// constant, then the five token counts), each NULL when there is no such row.
type packedRow struct {
	rowid sql.NullInt64
	id    sql.RawBytes
	cols  [colTime + 5]sql.NullInt64
}

func (p *packedRow) fields() []any {
	out := []any{&p.rowid, &p.id}
	for i := range p.cols {
		out = append(out, &p.cols[i])
	}
	return out
}

func (p packedRow) String() string {
	cols := make([]string, len(p.cols))
	for i, c := range p.cols {
		cols[i] = "NULL"
		if c.Valid {
			cols[i] = strconv.FormatInt(c.Int64, 10)
		}
	}
	return fmt.Sprintf("%d %x %s", p.rowid.Int64, []byte(p.id), strings.Join(cols, " "))
}

// decodesTo reports whether the row, read through names, is old field for
// field. buf is room for a time's text, returned for the next call.
func (p packedRow) decodesTo(old textRow, names []nameKey, buf []byte) (bool, []byte, error) {
	if !p.rowid.Valid || p.rowid.Int64 != old.rowid {
		return false, buf, nil
	}
	id, err := unpackID(p.id)
	if err != nil || id != string(old.text[colID]) {
		return false, buf, err
	}
	for col := colTool; col <= colSubscription; col++ {
		v := p.cols[col-1]
		if !v.Valid || v.Int64 < 1 || v.Int64 >= int64(len(names)) {
			return false, buf, nil
		}
		if name := names[v.Int64]; name.kind != nameKinds[col] ||
			name.name != string(old.text[col]) {
			return false, buf, nil
		}
	}
	for _, col := range []int{colAccountSource, colSubscriptionSource} {
		v := p.cols[col-1]
		if !v.Valid || v.Int64 < 0 || v.Int64 >= int64(len(evidenceWords)) ||
			evidenceWords[v.Int64] != string(old.text[col]) {
			return false, buf, nil
		}
	}
	switch ts := p.cols[colTime-1]; {
	case !ts.Valid:
		if len(old.text[colTime]) > 0 {
			return false, buf, nil
		}
	default:
		buf = time.UnixMilli(ts.Int64).UTC().AppendFormat(buf[:0], timeLayout)
		if !bytes.Equal(buf, old.text[colTime]) {
			return false, buf, nil
		}
	}
	for i, n := range old.tokens {
		if v := p.cols[colTime+i]; !v.Valid || v.Int64 != n {
			return false, buf, nil
		}
	}
	return true, buf, nil
}

// loadNames reads every name, by its id: the zero nameKey for an id that is
// none.
func loadNames(ctx context.Context, q querier) ([]nameKey, error) {
	rows, err := q.QueryContext(ctx, "SELECT id, kind, name FROM names ORDER BY id")
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	names := []nameKey{{}}
	for rows.Next() {
		var id int64
		var key nameKey
		if err := rows.Scan(&id, &key.kind, &key.name); err != nil {
			return nil, err
		}
		if id < 1 {
			continue
		}
		for int64(len(names)) <= id {
			names = append(names, nameKey{})
		}
		names[id] = key
	}
	return names, rows.Err()
}

// nameKey is a name of a kind.
type nameKey struct {
	kind int
	name string
}

// nameCache is the names a Ledger has met in committed transactions, by name
// and by id. A name is never deleted or renamed, so a committed id stays
// right for as long as the ledger exists, whatever other processes write.
type nameCache struct {
	mu     sync.Mutex
	loaded bool
	ids    map[nameKey]int64
	byID   map[int64]string
}

// nameSet resolves names to their ids, and back, inside one transaction: from
// the ledger's cache, which it loads whole on its first use, else from the
// table, adding a name that is missing. The names it added join the cache only
// once the transaction commits (keep): a rolled-back one never stored them.
type nameSet struct {
	ctx         context.Context
	tx          *sql.Tx
	cache       *nameCache
	added       map[nameKey]int64
	addedByID   map[int64]string
	find, write *sql.Stmt
}

func (l *Ledger) nameSet(ctx context.Context, tx *sql.Tx) *nameSet {
	return &nameSet{
		ctx: ctx, tx: tx, cache: &l.names,
		added: map[nameKey]int64{}, addedByID: map[int64]string{},
	}
}

// load fills the cache from the table, once per Ledger. The caller holds
// the cache's lock.
func (n *nameSet) load() error {
	if n.cache.loaded {
		return nil
	}
	names, err := loadNames(n.ctx, n.tx)
	if err != nil {
		return err
	}
	n.cache.ids, n.cache.byID = map[nameKey]int64{}, map[int64]string{}
	for id, key := range names {
		if key.kind != 0 {
			n.cache.ids[key], n.cache.byID[int64(id)] = int64(id), key.name
		}
	}
	n.cache.loaded = true
	return nil
}

// cached is the id the cache or this transaction knows for key.
func (n *nameSet) cached(key nameKey) (int64, bool, error) {
	if id, ok := n.added[key]; ok {
		return id, true, nil
	}
	n.cache.mu.Lock()
	defer n.cache.mu.Unlock()
	if err := n.load(); err != nil {
		return 0, false, err
	}
	id, ok := n.cache.ids[key]
	return id, ok, nil
}

// lookup is the id of name, a name of kind, found false when the ledger
// holds no such name, and so no row that names it.
func (n *nameSet) lookup(kind int, name string) (id int64, found bool, err error) {
	key := nameKey{kind, name}
	if id, ok, err := n.cached(key); ok || err != nil {
		return id, ok, err
	}
	if n.find == nil {
		if n.find, err = n.tx.PrepareContext(n.ctx,
			"SELECT id FROM names WHERE kind = ? AND name = ?"); err != nil {
			return 0, false, err
		}
	}
	switch err = n.find.QueryRowContext(n.ctx, kind, name).Scan(&id); {
	case errors.Is(err, sql.ErrNoRows):
		return 0, false, nil
	case err != nil:
		return 0, false, err
	}
	// Another process committed it since the cache was loaded.
	n.cache.mu.Lock()
	n.cache.ids[key], n.cache.byID[id] = id, name
	n.cache.mu.Unlock()
	return id, true, nil
}

// id is the id of name, a name of kind, which it adds when the ledger holds
// none.
func (n *nameSet) id(kind int, name string) (int64, error) {
	id, found, err := n.lookup(kind, name)
	if found || err != nil {
		return id, err
	}
	if n.write == nil {
		if n.write, err = n.tx.PrepareContext(n.ctx,
			"INSERT INTO names (kind, name) VALUES (?, ?) RETURNING id"); err != nil {
			return 0, err
		}
	}
	if err := n.write.QueryRowContext(n.ctx, kind, name).Scan(&id); err != nil {
		return 0, err
	}
	n.added[nameKey{kind, name}], n.addedByID[id] = id, name
	return id, nil
}

// name is the name whose id is id.
func (n *nameSet) name(id int64) (string, error) {
	if name, ok := n.addedByID[id]; ok {
		return name, nil
	}
	n.cache.mu.Lock()
	defer n.cache.mu.Unlock()
	if err := n.load(); err != nil {
		return "", err
	}
	if name, ok := n.cache.byID[id]; ok {
		return name, nil
	}
	var key nameKey
	if err := n.tx.QueryRowContext(n.ctx, "SELECT kind, name FROM names WHERE id = ?", id).
		Scan(&key.kind, &key.name); err != nil {
		return "", fmt.Errorf("the name %d of a response: %w", id, err)
	}
	// Another process committed it since the cache was loaded.
	n.cache.ids[key], n.cache.byID[id] = id, key.name
	return key.name, nil
}

// found is the id of each of keys that the ledger holds as a name of kind,
// by the name: the others name no row.
func (n *nameSet) found(kind int, keys []string) (map[string]int64, error) {
	ids := map[string]int64{}
	for _, key := range keys {
		id, ok, err := n.lookup(kind, key)
		if err != nil {
			return nil, err
		}
		if ok {
			ids[key] = id
		}
	}
	return ids, nil
}

// keep adds the names this transaction stored to the cache, once it has
// committed.
func (n *nameSet) keep() {
	n.cache.mu.Lock()
	defer n.cache.mu.Unlock()
	for key, id := range n.added {
		n.cache.ids[key], n.cache.byID[id] = id, key.name
	}
}

// transactNames is transact with the transaction's nameSet, whose names join
// the cache once it commits.
func (l *Ledger) transactNames(ctx context.Context, fn func(*sql.Tx, *nameSet) error) error {
	var names *nameSet
	err := l.transact(ctx, func(tx *sql.Tx) error {
		names = l.nameSet(ctx, tx)
		return fn(tx, names)
	})
	if err == nil {
		names.keep()
	}
	return err
}

// noTime is a response time that is none, a NULL ts: it sorts before every
// time.
const noTime = math.MinInt64

// storedTime is a response time in timeLayout, "" for noTime.
func storedTime(ms int64) string {
	if ms == noTime {
		return ""
	}
	return time.UnixMilli(ms).UTC().Format(timeLayout)
}

// parseStoredTime is the time text holds in milliseconds, ok only when text
// is exactly in timeLayout, as storedTime writes it back.
func parseStoredTime(text string) (ms int64, ok bool) {
	when, err := time.Parse(timeLayout, text)
	if err != nil {
		return 0, false
	}
	ms = when.UnixMilli()
	return ms, storedTime(ms) == text
}

// responseTime is a response's time in milliseconds, noTime for the zero time.
func responseTime(t time.Time) int64 {
	if t.IsZero() {
		return noTime
	}
	return t.UnixMilli()
}

// sqlTime is a response time as SQL stores it, NULL for noTime.
func sqlTime(ms int64) any {
	if ms == noTime {
		return nil
	}
	return ms
}

// evidenceWords are the evidence levels by rank (EvidenceRank).
var evidenceWords = [...]string{
	EvidenceUnknown, EvidenceInferred, EvidenceObserved, EvidenceSession, EvidenceTranscript,
}

// evidenceWord is the evidence level of a stored rank.
func evidenceWord(rank int64) string {
	if rank < 0 || rank >= int64(len(evidenceWords)) {
		return EvidenceUnknown
	}
	return evidenceWords[rank]
}

// storedRank is EvidenceRank of a stored word, ok false for a word that is
// no evidence level: EvidenceRank ranks it as unknown.
func storedRank(word []byte) (rank int, ok bool) {
	rank = EvidenceRank(string(word))
	return rank, rank > 0 || string(word) == EvidenceUnknown
}

func (l *Ledger) transact(ctx context.Context, fn func(*sql.Tx) error) error {
	tx, err := l.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

// Close releases the database.
func (l *Ledger) Close() error { return l.db.Close() }

type execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// querier is the database or a transaction, to read from.
type querier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
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
func (l *Ledger) Meta(key string) (string, error) {
	var value string
	err := l.db.QueryRow("SELECT value FROM meta WHERE key = ?", key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return value, err
}

// SetMeta stores a note.
func (l *Ledger) SetMeta(key, value string) error {
	return setMeta(context.Background(), l.db, key, value)
}

// Empty reports whether the ledger holds no responses of tool.
func (l *Ledger) Empty(tool string) (bool, error) {
	var one int
	err := l.db.QueryRow(`SELECT 1 FROM responses
	  WHERE tool = (SELECT id FROM names WHERE kind = ? AND name = ?) LIMIT 1`,
		nameTool, tool).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return true, nil
	}
	return false, err
}

// FileRow is where ingest stopped in one transcript: the offset, the size and
// time it had then, the source's saved state there, and a digest of its
// first line, which tells a file rewritten in place from one that grew.
type FileRow struct {
	Offset, Size, ModTimeNanos int64
	State                      []byte
	Head                       string
}

// File is the stored position of a transcript.
func (l *Ledger) File(path string) (row FileRow, found bool, err error) {
	var state string
	err = l.db.QueryRow(
		"SELECT offset, size, mtime_ns, state, head FROM files WHERE path = ?", path,
	).Scan(&row.Offset, &row.Size, &row.ModTimeNanos, &state, &row.Head)
	if errors.Is(err, sql.ErrNoRows) {
		return row, false, nil
	}
	if state != "" {
		row.State = []byte(state)
	}
	return row, err == nil, err
}

// readSince reports whether the ledger read a transcript whose path contains
// text when that transcript had been written at or after since (ledger
// layout).
func (l *Ledger) readSince(text, since string) bool {
	when, err := time.Parse(timeLayout, since)
	if err != nil {
		return false
	}
	var one int
	return l.db.QueryRow(
		"SELECT 1 FROM files WHERE instr(path, ?) > 0 AND mtime_ns >= ? LIMIT 1",
		text, when.UnixNano(),
	).Scan(&one) == nil
}

// parseRevisionKey is the meta key of the parser revision tool's
// transcripts were last read with (reparser).
func parseRevisionKey(tool string) string { return "parse_revision:" + tool }

// ForgetFiles makes ingest read again from the start every transcript under
// roots, once: when revision is not the one stored for tool it forgets where
// ingest stopped in those files and stores revision, in one transaction, so a
// run that stops half-way does not start the re-read over. The rows stay; a
// re-read rewrites them under the same keys.
func (l *Ledger) ForgetFiles(ctx context.Context, tool, revision string, roots []string) error {
	return l.transact(ctx, func(tx *sql.Tx) error {
		var stored string
		err := tx.QueryRowContext(ctx, "SELECT value FROM meta WHERE key = ?", parseRevisionKey(tool)).
			Scan(&stored)
		switch {
		case errors.Is(err, sql.ErrNoRows):
		case err != nil:
			return err
		case stored == revision:
			return nil
		}
		for _, root := range roots {
			root = filepath.Clean(root)
			if _, err := tx.ExecContext(
				ctx,
				"DELETE FROM files WHERE instr(path, ?) = 1",
				root+string(filepath.Separator),
			); err != nil {
				return err
			}
		}
		return setMeta(ctx, tx, parseRevisionKey(tool), revision)
	})
}

// DeleteFile forgets a transcript that no longer exists.
func (l *Ledger) DeleteFile(path string) error {
	_, err := l.db.Exec("DELETE FROM files WHERE path = ?", path)
	return err
}

// Tx is one transaction of ledger writes.
type Tx struct {
	ctx   context.Context
	tx    *sql.Tx
	names *nameSet
	// The statements Upsert runs, prepared on first use: SQLite parses each
	// once per transaction instead of once per row.
	upsert, pend, unpend *sql.Stmt
	run                  *Run              // what earlier transactions of this run stored
	wrote                *Run              // what this one stored, added to run once it commits
	labels               map[string]string // the subscription labels Upsert stored, by id
}

// Run is what one ingest run has committed. It is filled only after a commit
// succeeds, so it never holds what a rolled-back transaction wrote.
type Run struct {
	// seen is each response's stored time and token counts. Most rows of a
	// first run are copies a fork made of its parent's usage; Upsert skips a
	// copy that it can tell would change nothing.
	seen map[string]storedRow
	// pending and threads are what ResolveTiers checks again: the rows put
	// in tier_pending and the threads whose tier changes were written.
	pending, threads map[string]bool
	// marked is whether tiersUnresolved has been stored this run.
	marked bool
	// roots are the sessions (rootKey) whose rows this run stored: what
	// ResolveAccounts attributes again, as pending and threads scope
	// ResolveTiers.
	roots map[string]bool
}

// rootKey identifies a root session in a Run.
func rootKey(tool, root string) string { return tool + ":" + root }

// NewRun is the empty record of an ingest run.
func NewRun() *Run {
	return &Run{
		seen:    map[string]storedRow{},
		pending: map[string]bool{},
		threads: map[string]bool{},
		roots:   map[string]bool{},
	}
}

type storedRow struct {
	ts     int64    // milliseconds, noTime for none
	tokens [5]int64 // input, output, cache_write_5m, cache_write_1h, cache_read
	rank   [2]int   // EvidenceRank of the stored account_source and subscription_source
}

// tiersUnresolved is the meta note a transaction that writes tier_pending or
// tier_changes stores, and ResolveTiers clears: while it is set, rows of an
// earlier run that stopped before ResolveTiers may still wait unchecked.
// tiersChecked is the note every ResolveTiers stores, tiersRule. Any other
// value, or none, means no check this build makes has run on the ledger (it
// is new, upgraded, or written by an earlier build), so its waiting rows may
// never have been checked and the next check is full. An import worker of the
// release before that opened the ledger before the upgrade to version 7 and
// checks it after matches no row of version 7, yet clears tiersUnresolved;
// the value it stores makes the next check full too.
const (
	tiersUnresolved = "tiers_unresolved"
	tiersChecked    = "tiers_checked"
	tiersRule       = "2" // 2: checked on schema version 7
)

// fullTierCheckDue reports whether the next ResolveTiers must check every
// waiting row rather than only those an ingest run wrote.
func (l *Ledger) fullTierCheckDue() (bool, error) {
	unresolved, err := l.Meta(tiersUnresolved)
	if err != nil {
		return false, err
	}
	checked, err := l.Meta(tiersChecked)
	return unresolved != "" || checked != tiersRule, err
}

// Transaction runs fn and commits, or rolls back when fn fails. run is the
// ingest run's record of committed writes, or nil.
func (l *Ledger) Transaction(ctx context.Context, run *Run, fn func(*Tx) error) error {
	if run == nil {
		run = NewRun()
	}
	t := &Tx{ctx: ctx, run: run, wrote: NewRun(), labels: map[string]string{}}
	err := l.transactNames(ctx, func(tx *sql.Tx, names *nameSet) error {
		t.tx, t.names = tx, names
		if err := fn(t); err != nil {
			return err
		}
		// Rows stored without evidence wait for ResolveAccounts, which a run
		// cancelled before it would leave for the next one.
		if len(t.wrote.roots) > 0 {
			if err := markAccountsUnresolved(
				ctx,
				tx,
				slices.Collect(maps.Keys(t.wrote.roots))...); err != nil {
				return err
			}
		}
		if !run.marked && (len(t.wrote.pending) > 0 || len(t.wrote.threads) > 0) {
			t.wrote.marked = true
			return setMeta(ctx, tx, tiersUnresolved, "1")
		}
		return nil
	})
	if err == nil {
		maps.Copy(run.seen, t.wrote.seen)
		maps.Copy(run.pending, t.wrote.pending)
		maps.Copy(run.threads, t.wrote.threads)
		maps.Copy(run.roots, t.wrote.roots)
		run.marked = run.marked || t.wrote.marked
	}
	return err
}

// prepared is the statement in slot, prepared on first use in this transaction.
func (t *Tx) prepared(slot **sql.Stmt, query string) (*sql.Stmt, error) {
	if *slot == nil {
		statement, err := t.tx.PrepareContext(t.ctx, query)
		if err != nil {
			return nil, err
		}
		*slot = statement
	}
	return *slot, nil
}

// Streamed usage only grows, and Claude Code copies earlier records into the
// transcript of a resumed or forked session, sometimes mid-stream. The token
// columns therefore keep the largest value seen, so neither file order nor a
// later partial or zeroed copy can lower a request's final usage. A Codex fork
// copies its parent's usage with the fork's time and thread, so the time,
// model, project and session come from the earliest occurrence; on a tie the
// row read last wins, as it always has.
//
// Every name is its id and the root's ?17 is the id of the session name "",
// for a row whose tool names no session.
var upsert = `
INSERT INTO responses (request_id, tool, model, project, session, root, account, subscription,
                       account_source, subscription_source, ts,
                       input, output, cache_write_5m, cache_write_1h, cache_read)
VALUES (?1, ?2, ?3, ?4, ?5, ?6, ?7, ?8, ?9, ?10, ?11, ?12, ?13, ?14, ?15, ?16)
ON CONFLICT(request_id) DO UPDATE SET
  ts      = CASE WHEN ` + earlier + ` THEN excluded.ts ELSE ts END,
  model   = CASE WHEN ` + earlier + ` THEN excluded.model ELSE model END,
  project = CASE WHEN ` + earlier + ` THEN excluded.project ELSE project END,
  session = CASE WHEN ` + earlier + ` THEN excluded.session ELSE session END,
  root    = CASE WHEN excluded.root <> ?17 AND (root = ?17 OR ` + earlier + `)
                 THEN excluded.root ELSE root END,
  input = MAX(input, excluded.input), output = MAX(output, excluded.output),
  cache_write_5m = MAX(cache_write_5m, excluded.cache_write_5m),
  cache_write_1h = MAX(cache_write_1h, excluded.cache_write_1h),
  cache_read = MAX(cache_read, excluded.cache_read),
  account        = CASE WHEN ` + strongerAccount + ` THEN excluded.account ELSE account END,
  account_source = CASE WHEN ` + strongerAccount + ` THEN excluded.account_source ELSE account_source END,
  subscription   = CASE WHEN ` + strongerSubscription + ` THEN excluded.subscription ELSE subscription END,
  subscription_source = CASE WHEN ` + strongerSubscription + `
                             THEN excluded.subscription_source ELSE subscription_source END
RETURNING ts, input, output, cache_write_5m, cache_write_1h, cache_read,
  EXISTS (SELECT 1 FROM tier_pending WHERE tier_pending.request_id = responses.request_id),
  account_source, subscription_source, root
`

// strongerAccount and strongerSubscription are true when the incoming copy
// rests on stronger evidence than the stored row for that field, which then
// takes the copy's value; equal evidence keeps the stored one. The two are
// independent: a copy may name the plan and not the account. In an UPDATE the
// stored columns are the old values.
const (
	strongerAccount      = "excluded.account_source > account_source"
	strongerSubscription = "excluded.subscription_source > subscription_source"
)

// earlier is true when the incoming copy is at least as old as the stored
// row, which then takes its attribution. In an UPDATE, ts is the old value.
const earlier = `(ts IS NULL OR (excluded.ts IS NOT NULL AND excluded.ts <= ts))`

// timeLayout is the text of a time the ledger keeps as text, and of a
// response's time where it meets one (storedTime): the form Claude Code
// writes, UTC with milliseconds, which sorts as text.
const timeLayout = "2006-01-02T15:04:05.000Z"

// A report names days and months in the machine's time zone, the one the
// session times beside them are shown in: a response belongs to the day on the
// wall clock where it was made, not to the UTC day it is stored under.

// periodFilter reports whether a response time in milliseconds falls from
// since up to, not including, until, a zero time for no bound, each bound to
// the millisecond it falls in. A period covers dated responses only: an
// undated one (noTime) sorts before every time, so the end bound alone would
// count it while the start left it out.
func periodFilter(since, until time.Time) func(ts int64) bool {
	if since.IsZero() && until.IsZero() {
		return func(int64) bool { return true }
	}
	lo, hi := int64(math.MinInt64), int64(math.MaxInt64)
	if !since.IsZero() {
		lo = since.UnixMilli()
	}
	if !until.IsZero() {
		hi = until.UnixMilli()
	}
	return func(ts int64) bool { return ts != noTime && ts >= lo && ts < hi }
}

// LocalDate is the machine-local calendar day (YYYY-MM-DD) of a response time
// in timeLayout, "none" when ts holds no time.
func LocalDate(ts string) string {
	when, err := time.Parse(time.RFC3339, ts)
	if err != nil {
		return "none"
	}
	return localDate(when.UnixMilli())
}

// localDate is LocalDate of a response time in milliseconds.
func localDate(ms int64) string { return cmp.Or(localDay(ms), "none") }

// localDay is the machine-local calendar day (YYYY-MM-DD) of a response time
// in milliseconds, "" for noTime.
func localDay(ms int64) string {
	if ms == noTime {
		return ""
	}
	return time.UnixMilli(ms).Format(time.DateOnly)
}

// labels is the subscriptions table: each subscription id's label.
func (l *Ledger) labels(ctx context.Context) (map[string]string, error) {
	rows, err := l.db.QueryContext(ctx, "SELECT id, label FROM subscriptions")
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	labels := map[string]string{}
	for rows.Next() {
		var id, label string
		if err := rows.Scan(&id, &label); err != nil {
			return nil, err
		}
		labels[id] = label
	}
	return labels, rows.Err()
}

// storedID is the ledger key of a response: Claude's id as is, every other
// tool's as <tool>:<id>.
func storedID(u Usage) string {
	id := u.RequestID
	if u.CopyOf != "" {
		id = copyMark + u.CopyOf + ":" + id
	}
	if u.Tool == firstTool {
		return id
	}
	return u.Tool + ":" + id
}

// copyMark starts the stored key of a row with a CopyOf, after the tool.
const copyMark = "copy:"

// DropHeldCopies deletes tool's rows stored with a CopyOf whose thread's
// transcript now holds the response, by holds: the original's row counts it.
func (l *Ledger) DropHeldCopies(
	ctx context.Context,
	tool string,
	holds func(thread, since string) bool,
) error {
	prefix := tool + ":" + copyMark
	if tool == firstTool {
		prefix = copyMark
	}
	// A key range on the unique request_id: every key that starts with
	// prefix, which is stored as its text (packID).
	from := packID(prefix)
	end := slices.Clone(from)
	end[len(end)-1]++
	rows, err := l.db.QueryContext(ctx,
		"SELECT request_id FROM responses WHERE request_id >= ? AND request_id < ?",
		from, end)
	if err != nil {
		return err
	}
	var held [][]byte
	for rows.Next() {
		var packed []byte
		if err := rows.Scan(&packed); err != nil {
			_ = rows.Close()
			return err
		}
		id, err := unpackID(packed)
		if err != nil {
			_ = rows.Close()
			return err
		}
		// The key is "<thread>@<time>:<id>", the time in timeLayout.
		thread, rest, ok := strings.Cut(strings.TrimPrefix(id, prefix), "@")
		if ok && len(rest) > len(timeLayout) && holds(thread, rest[:len(timeLayout)]) {
			held = append(held, packed)
		}
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if len(held) == 0 {
		return rows.Err()
	}
	return l.transact(ctx, func(tx *sql.Tx) error {
		for _, id := range held {
			for _, table := range []string{"responses", "tier_pending"} {
				if _, err := tx.ExecContext(ctx,
					"DELETE FROM "+table+" WHERE request_id = ?", id); err != nil {
					return err
				}
			}
		}
		return nil
	})
}

// Upsert records one response. Its account (email), subscription and the
// evidence of each come from the Usage: a source sets Subscription and
// SubscriptionEvidence only when its transcript names the plan, and ingest
// sets Account and AccountEvidence only when the transcript names the account
// (EvidenceTranscript); every other value is "unknown" with EvidenceUnknown
// ("" counts as unknown), and ResolveAccounts raises the evidence once a
// binding or observation names the row's session or time. The account and
// the subscription of an existing row, each with its evidence, are replaced
// only by a copy with stronger evidence for that field. A row priced
// by its root thread's tier waits in tier_pending until ResolveTiers prices it;
// the pending row follows the copy whose attribution is stored, so a later
// copy never changes what an earlier one decided.
func (t *Tx) Upsert(u Usage) error {
	id := storedID(u)
	accountEvidence := cmp.Or(u.AccountEvidence, EvidenceUnknown)
	subscriptionEvidence := cmp.Or(u.SubscriptionEvidence, EvidenceUnknown)
	rank := [2]int{EvidenceRank(accountEvidence), EvidenceRank(subscriptionEvidence)}
	when := responseTime(u.Time)
	// A copy later than a committed row with a time, with no count above the
	// committed ones and no stronger evidence, changes nothing: the
	// attribution stays, MAX keeps the counts, and the pending tier follows
	// the stored attribution. A stored time only moves earlier, counts and
	// evidence only grow, so this holds whatever this transaction wrote since.
	if seen, ok := t.run.seen[id]; ok && rank[0] <= seen.rank[0] && rank[1] <= seen.rank[1] &&
		when != noTime && seen.ts != noTime && when > seen.ts &&
		u.Input <= seen.tokens[0] && u.Output <= seen.tokens[1] &&
		u.CacheWrite5m <= seen.tokens[2] &&
		u.CacheWrite1h <= seen.tokens[3] &&
		u.CacheRead <= seen.tokens[4] {
		return nil
	}
	if u.Subscription != "" && t.labels[u.Subscription] != u.SubscriptionLabel {
		if err := setLabel(t.ctx, t.tx, u.Subscription, u.SubscriptionLabel); err != nil {
			return err
		}
		t.labels[u.Subscription] = u.SubscriptionLabel
	}
	values := []any{packID(id)}
	for _, name := range []struct {
		kind  int
		value string
	}{
		{nameTool, u.Tool},
		{nameModel, u.Model},
		{nameProject, u.Project},
		{nameSession, u.Session},
		{nameSession, u.Root},
		{nameAccount, cmp.Or(u.Account, "unknown")},
		{nameSubscription, cmp.Or(u.Subscription, "unknown")},
	} {
		id, err := t.names.id(name.kind, name.value)
		if err != nil {
			return err
		}
		values = append(values, id)
	}
	// No row has the root "" while the ledger holds no such name, and no
	// row's root is -1.
	noRoot, found, err := t.names.lookup(nameSession, "")
	if err != nil {
		return err
	} else if !found {
		noRoot = -1
	}
	values = append(values, rank[0], rank[1], sqlTime(when),
		u.Input, u.Output, u.CacheWrite5m, u.CacheWrite1h, u.CacheRead, noRoot)
	statement, err := t.prepared(&t.upsert, upsert)
	if err != nil {
		return err
	}
	var stored storedRow
	var storedTs sql.NullInt64
	var pending bool
	var root int64
	if err := statement.QueryRowContext(t.ctx, values...).Scan(
		&storedTs, &stored.tokens[0], &stored.tokens[1],
		&stored.tokens[2], &stored.tokens[3], &stored.tokens[4], &pending,
		&stored.rank[0], &stored.rank[1], &root,
	); err != nil {
		return err
	}
	stored.ts = noTime
	if storedTs.Valid {
		stored.ts = storedTs.Int64
	}
	t.wrote.seen[id] = stored
	// The stored root, not the incoming one: a copy with stronger evidence
	// changes the row's account but keeps the root of the copy that set it,
	// and that is the session a late resolution must look at. "" for the
	// rows of a tool that name no session.
	storedRoot, err := t.names.name(root)
	if err != nil {
		return err
	}
	t.wrote.roots[rootKey(u.Tool, storedRoot)] = true
	if stored.ts != when {
		return nil // an earlier copy's attribution is kept, and so is its tier
	}
	if u.TierFrom == "" {
		if !pending {
			return nil
		}
		statement, err := t.prepared(&t.unpend, "DELETE FROM tier_pending WHERE request_id = ?")
		if err != nil {
			return err
		}
		_, err = statement.ExecContext(t.ctx, values[0])
		return err
	}
	from, err := t.names.id(nameSession, u.TierFrom)
	if err != nil {
		return err
	}
	statement, err = t.prepared(&t.pend, `INSERT INTO tier_pending (request_id, root) VALUES (?, ?)
		 ON CONFLICT(request_id) DO UPDATE SET root = excluded.root`)
	if err != nil {
		return err
	}
	if _, err = statement.ExecContext(t.ctx, values[0], from); err != nil {
		return err
	}
	t.wrote.pending[id] = true
	return nil
}

// AddThreadParent records that tool's thread was spawned by parent, as the
// thread's own file names it. It changes no row: ResolveAccounts, which
// every run ends with, moves the rows stored under a parent to the root.
func (t *Tx) AddThreadParent(tool string, link ThreadParent) error {
	if link.Thread == "" || link.Parent == "" || link.Thread == link.Parent {
		return nil
	}
	_, err := t.tx.ExecContext(t.ctx,
		`INSERT INTO thread_parents (tool, thread, parent) VALUES (?, ?, ?)
		 ON CONFLICT(tool, thread) DO UPDATE SET parent = excluded.parent`,
		tool, link.Thread, link.Parent)
	return err
}

// AddTierChange records a thread's switch of service tier, in its selected
// or its running-turn series. A series has one tier at a time: a change at
// the same time as a stored one replaces it, so of several snapshots in one
// millisecond the last one read wins, as the last one written does in Codex.
// A change that takes its tier from another series is stored as "=" and that
// TierFrom.
func (t *Tx) AddTierChange(c TierChange) error {
	if c.Thread == "" || c.Time.IsZero() {
		return nil
	}
	key, when, tier := c.Thread, c.Time.UTC().Format(timeLayout), c.Tier
	if c.Turn {
		key = turnTierKey(c.Thread)
	}
	if c.From != "" {
		tier = "=" + c.From
	}
	if _, err := t.tx.ExecContext(t.ctx,
		"DELETE FROM tier_changes WHERE thread_id = ? AND ts = ? AND tier <> ?",
		key, when, tier); err != nil {
		return err
	}
	if _, err := t.tx.ExecContext(t.ctx,
		"INSERT OR IGNORE INTO tier_changes (thread_id, ts, tier) VALUES (?, ?, ?)",
		key, when, tier); err != nil {
		return err
	}
	t.wrote.threads[key] = true
	return nil
}

// The meta notes of ResolveAccounts. accountsUnresolved lists the sessions
// (rootKey, as a JSON array) that BindSession bound since the last
// resolution, "*" when too many to list; ResolveAccounts attributes their rows
// again and clears it. accountsChecked is stored by every ResolveAccounts as
// accountsRule, the revision of its rules: without it, or with another, no
// resolution under these rules has run on the ledger (it is new, upgraded, or
// an earlier build resolved it), so every row may be waiting. accountsThrough+tool is the latest sign-in
// observation of a tool at the end of the last resolution: a row after it had
// no observation on its far side then, so a later one may decide it.
// accountsOwned lists the subscriptions (a JSON array) whose email the last
// resolution knew (accountBasis.owners): one missing from it is learned now,
// and only its rows are read again for it. accountsMarked is the time of the
// earliest account mark stored since the last resolution, "*" for a pair
// (AddMark): the rows its stretch may infer are read again.
const (
	accountsOwned      = "accounts_owned"
	accountsUnresolved = "accounts_unresolved"
	accountsChecked    = "accounts_checked"
	accountsRule       = "3" // 3: a row nothing else names takes the account its login stretch's marks infer
	accountsMarked     = "accounts_marked"
	accountsThrough    = "accounts_through:"
	dirtyRootsAll      = "*"
	maxDirtyRoots      = 256
)

// noSignIn reports whether s names nothing: no evidence of any account.
func noSignIn(s SignIn) bool {
	return (s.Account == "" || s.Account == "unknown") && s.Subscription == ""
}

// setLabel stores or refreshes the display label of a subscription id.
func setLabel(ctx context.Context, x execer, id, label string) error {
	if id == "" || label == "" {
		return nil
	}
	_, err := x.ExecContext(
		ctx,
		`INSERT INTO subscriptions (id, label) VALUES (?, ?)
		 ON CONFLICT(id) DO UPDATE SET label = excluded.label`,
		id, label,
	)
	return err
}

// BindSession records the sign-in s a tool's session started or resumed under
// at time at: a session_accounts row for (tool, session, at), replacing one at
// the same time. The worker a SessionStart or SessionEnd hook started calls
// it, in its own short transaction, before it contends for the ingest lock;
// session is the root session the hook names (Usage.Root). It also stores s's
// subscription label in subscriptions.
//
// It holds nothing across calls, so it is safe before the ingest lock is taken:
// the write waits on the busy timeout behind a running worker. A sign-in that
// names nothing is not a binding (the session's rows then fall to an
// observation), and one that repeats the binding already in effect at at
// changes no row's answer and is not stored. A stored binding marks its
// session in accountsUnresolved, so the next ResolveAccounts attributes rows of
// that session read earlier.
func (l *Ledger) BindSession(
	ctx context.Context,
	tool, session string,
	at time.Time,
	s SignIn,
) error {
	if tool == "" || session == "" || at.IsZero() || noSignIn(s) {
		return nil
	}
	since := at.UTC().Format(timeLayout)
	account, subscription := cmp.Or(s.Account, "unknown"), cmp.Or(s.Subscription, "unknown")
	return l.transact(ctx, func(tx *sql.Tx) error {
		if err := setLabel(ctx, tx, s.Subscription, s.Label); err != nil {
			return err
		}
		var inEffect, inEffectSubscription string
		err := tx.QueryRowContext(ctx,
			`SELECT account, subscription FROM session_accounts
			  WHERE tool = ? AND session = ? AND since <= ? ORDER BY since DESC LIMIT 1`,
			tool, session, since).Scan(&inEffect, &inEffectSubscription)
		if err == nil && inEffect == account && inEffectSubscription == subscription {
			return nil
		}
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO session_accounts (tool, session, since, account, subscription)
			 VALUES (?, ?, ?, ?, ?)
			 ON CONFLICT(tool, session, since) DO UPDATE SET
			   account = excluded.account, subscription = excluded.subscription`,
			tool, session, since, account, subscription); err != nil {
			return err
		}
		return markAccountsUnresolved(ctx, tx, rootKey(tool, session))
	})
}

// markAccountsUnresolved adds sessions to accountsUnresolved.
func markAccountsUnresolved(ctx context.Context, tx *sql.Tx, keys ...string) error {
	note, err := metaIn(ctx, tx, accountsUnresolved)
	if err != nil || note == dirtyRootsAll {
		return err
	}
	var listed []string
	if note != "" && json.Unmarshal([]byte(note), &listed) != nil {
		return setMeta(ctx, tx, accountsUnresolved, dirtyRootsAll) // unreadable: every row
	}
	for _, key := range keys {
		if !slices.Contains(listed, key) {
			listed = append(listed, key)
		}
	}
	value := dirtyRootsAll
	if len(listed) <= maxDirtyRoots {
		encoded, err := json.Marshal(listed)
		if err != nil {
			return err
		}
		value = string(encoded)
	}
	return setMeta(ctx, tx, accountsUnresolved, value)
}

// metaIn is a stored note read inside a transaction, "" when absent.
func metaIn(ctx context.Context, tx *sql.Tx, key string) (string, error) {
	var value string
	err := tx.QueryRowContext(ctx, "SELECT value FROM meta WHERE key = ?", key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return value, err
}

// ObserveSignIn records that tool was signed in as s at time at: a sign_ins
// row, and s's subscription label in subscriptions, refreshed when s names
// one. Every ingest run calls it once per tool, in a transaction of its own.
//
// A run of equal consecutive observations only needs its first and its last
// (the sign-ins on both sides of a time agree whichever of them it falls
// between), so an observation that repeats the latest two moves the latest one
// forward instead of adding a row.
func (t *Tx) ObserveSignIn(tool string, at time.Time, s SignIn) error {
	if tool == "" || at.IsZero() {
		return nil
	}
	when := at.UTC().Format(timeLayout)
	account, subscription := cmp.Or(s.Account, "unknown"), cmp.Or(s.Subscription, "unknown")
	if err := setLabel(t.ctx, t.tx, s.Subscription, s.Label); err != nil {
		return err
	}
	rows, err := t.tx.QueryContext(t.ctx,
		"SELECT at, account, subscription FROM sign_ins WHERE tool = ? ORDER BY at DESC LIMIT 2",
		tool)
	if err != nil {
		return err
	}
	var latest string
	repeats := 0
	for rows.Next() {
		var rowAt, rowAccount, rowSubscription string
		if err := rows.Scan(&rowAt, &rowAccount, &rowSubscription); err != nil {
			_ = rows.Close()
			return err
		}
		if rowAccount == account && rowSubscription == subscription && rowAt < when {
			if repeats == 0 {
				latest = rowAt
			}
			repeats++
		}
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if repeats == 2 {
		if _, err := t.tx.ExecContext(t.ctx,
			"DELETE FROM sign_ins WHERE tool = ? AND at = ?", tool, latest); err != nil {
			return err
		}
	}
	_, err = t.tx.ExecContext(t.ctx,
		`INSERT INTO sign_ins (tool, at, account, subscription) VALUES (?, ?, ?, ?)
		 ON CONFLICT(tool, at) DO UPDATE SET account = excluded.account,
		   subscription = excluded.subscription`,
		tool, when, account, subscription)
	return err
}

type (
	bindingKey  struct{ tool, session string }
	signInPoint struct{ at, account, subscription string }
	// attribution is what a row is attributed to: source is the evidence of
	// both values, "" for no decision. An empty or "unknown" value names
	// nothing and leaves that field of the row as it is. byOrg marks a row
	// whose transcript named its organization (accountBasis.attribute): its
	// email is decided anew from that organization's evidence alone.
	attribution struct {
		account, subscription, source string
		byOrg                         bool
	}
)

// accountBasis is what rows are attributed from: the session bindings and the
// sign-in observations of the ledger, each in time order, and the email that
// holds each subscription.
type accountBasis struct {
	bindings map[bindingKey][]signInPoint
	observed map[string][]signInPoint
	through  map[string]string // each tool's watermark of the last resolution
	// owners maps a subscription to the one email every sign-in to it names:
	// the bindings, the observations and the sign-ins the tools keep on disk.
	// One that two emails held maps to "" (a team's organization, whose members
	// share it), and its rows fall back to a binding or an observation of it.
	owners map[string]string
	// orgTools are the tools whose transcripts name an account (Tool.OrgAccount).
	orgTools map[string]bool
	// known are the subscriptions owners named an email for at the last
	// resolution (accountsOwned).
	known map[string]bool
	// stretches are each tool's login stretches with the organization the
	// account marks infer for them (loadStretches), and marked is where the
	// marks stored since the last resolution begin (accountsMarked).
	stretches map[string]*loginStretches
	marked    string
}

// owned are the subscriptions owners names an email for, sorted.
func (b accountBasis) owned() []string {
	var out []string
	for subscription, email := range b.owners {
		if email != "" {
			out = append(out, subscription)
		}
	}
	slices.Sort(out)
	return out
}

// claim records that a sign-in to subscription named account.
func (b accountBasis) claim(account, subscription string) {
	if account == "" || account == "unknown" || subscription == "" || subscription == "unknown" {
		return
	}
	if prev, seen := b.owners[subscription]; seen && prev != account {
		account = ""
	}
	b.owners[subscription] = account
}

func loadAccountBasis(
	ctx context.Context,
	tx *sql.Tx,
	signIns map[string][]SignIn,
) (accountBasis, error) {
	basis := accountBasis{
		bindings: map[bindingKey][]signInPoint{},
		observed: map[string][]signInPoint{},
		through:  map[string]string{},
		owners:   map[string]string{},
		orgTools: map[string]bool{},
	}
	for _, tool := range Tools {
		basis.orgTools[tool.Name] = tool.OrgAccount
	}
	for _, list := range signIns {
		for _, s := range list {
			basis.claim(s.Account, s.Subscription)
		}
	}
	rows, err := tx.QueryContext(ctx,
		"SELECT tool, session, since, account, subscription FROM session_accounts ORDER BY since")
	if err != nil {
		return basis, err
	}
	for rows.Next() {
		var key bindingKey
		var p signInPoint
		if err := rows.Scan(
			&key.tool,
			&key.session,
			&p.at,
			&p.account,
			&p.subscription,
		); err != nil {
			_ = rows.Close()
			return basis, err
		}
		basis.bindings[key] = append(basis.bindings[key], p)
		basis.claim(p.account, p.subscription)
	}
	if err := rows.Close(); err != nil {
		return basis, err
	}
	if err := rows.Err(); err != nil {
		return basis, err
	}
	rows, err = tx.QueryContext(
		ctx,
		"SELECT tool, at, account, subscription FROM sign_ins ORDER BY at",
	)
	if err != nil {
		return basis, err
	}
	for rows.Next() {
		var tool string
		var p signInPoint
		if err := rows.Scan(&tool, &p.at, &p.account, &p.subscription); err != nil {
			_ = rows.Close()
			return basis, err
		}
		basis.observed[tool] = append(basis.observed[tool], p)
		basis.claim(p.account, p.subscription)
	}
	if err := rows.Close(); err != nil {
		return basis, err
	}
	if err := rows.Err(); err != nil {
		return basis, err
	}
	for tool := range basis.observed {
		if basis.through[tool], err = metaIn(ctx, tx, accountsThrough+tool); err != nil {
			return basis, err
		}
	}
	if basis.stretches, err = loadStretches(ctx, tx, basis); err != nil {
		return basis, err
	}
	if basis.marked, err = metaIn(ctx, tx, accountsMarked); err != nil {
		return basis, err
	}
	note, err := metaIn(ctx, tx, accountsOwned)
	if err != nil {
		return basis, err
	}
	var known []string
	_ = json.Unmarshal([]byte(note), &known) // unreadable: every one is learned now
	basis.known = map[string]bool{}
	for _, subscription := range known {
		basis.known[subscription] = true
	}
	return basis, nil
}

// attribute is what the ledger's evidence says of row r.
//
// A row of a tool whose transcripts name an account (Tool.OrgAccount), whose
// transcript named its organization, is that organization's: its email is the
// one every sign-in to it names (owners), as transcript evidence; else that of
// the evidence below when it is a sign-in to the same organization; else none.
// The sign-in that was current when the row was made or read may be another:
// a /login in a running session moves its later responses to another
// organization without a new binding.
func (b accountBasis) attribute(r accountRow) attribution {
	if r.subscriptionSource != EvidenceTranscript || !b.orgTools[r.tool] {
		want := b.signedIn(r.tool, r.root, r.ts)
		if want.source == EvidenceUnknown {
			if inferred, ok := b.inferred(r.tool, r.ts); ok {
				return inferred
			}
		}
		return want
	}
	if email := b.owners[r.subscription]; email != "" {
		return attribution{email, r.subscription, EvidenceTranscript, true}
	}
	want := b.signedIn(r.tool, r.root, r.ts)
	if want.source != "" && want.subscription != r.subscription {
		want = attribution{subscription: "unknown", source: EvidenceUnknown}
	}
	want.byOrg = true
	return want
}

// signedIn is the sign-in the ledger's evidence puts a row of tool, its root
// session and its time ts under: the binding of its session with the latest
// since at or before ts; else the sign-in observed on both sides of ts when
// the two agree; else unknown, which names no email and no subscription and
// so changes nothing in a row (a row an earlier build stored keeps its email,
// which no report shows). A row before the first observation is unknown. A
// row after the last one has no decision yet (source ""): a later observation
// may decide it.
func (b accountBasis) signedIn(tool, root, ts string) attribution {
	if bound := b.bindings[bindingKey{tool, root}]; root != "" {
		if i := sort.Search(len(bound), func(i int) bool { return bound[i].at > ts }); i > 0 {
			return attribution{bound[i-1].account, bound[i-1].subscription, EvidenceSession, false}
		}
	}
	seen := b.observed[tool]
	i := sort.Search(len(seen), func(i int) bool { return seen[i].at > ts }) // the first after ts
	if i == 0 {
		return attribution{subscription: "unknown", source: EvidenceUnknown}
	}
	before, after := seen[i-1], seen[i-1]
	if before.at < ts {
		if i == len(seen) {
			return attribution{}
		}
		after = seen[i]
	}
	if before.account != after.account || before.subscription != after.subscription ||
		before.account == "unknown" && before.subscription == "unknown" {
		return attribution{subscription: "unknown", source: EvidenceUnknown}
	}
	return attribution{before.account, before.subscription, EvidenceObserved, false}
}

// accountRow is a response as the account resolution reads it: its id, and
// each field as its text, the time in timeLayout.
type accountRow struct {
	id                                                                       int64
	tool, ts, root, account, accountSource, subscription, subscriptionSource string
}

// update is the change that takes a row to what the evidence says, ok false
// when nothing changes. The email and the subscription are raised each on its
// own evidence, since a transcript can decide the plan and leave the email to a
// binding or an observation: a field takes the new value when its evidence is
// at least as strong as the stored one, never weaker, and a field decided by
// its transcript never changes. A value that is "unknown" is no evidence and
// leaves the field as it is, so a row keeps an email an earlier build stored
// until something names a real one.
//
// A row attributed by its organization (byOrg) is the exception: its
// organization is final, and its email takes the organization's evidence
// whatever the stored one rests on, which loses its standing when there is none
// (the text stays, and no report shows it) because it may be another
// organization's. An inferred field is the other: when the marks no longer
// infer anything for the row (a sign-in found later split its stretch, say),
// it goes back to unknown.
func (r accountRow) update(want attribution) (next accountRow, ok bool) {
	next = r
	rank := EvidenceRank(want.source)
	named := want.account != "" && want.account != "unknown"
	if want.source == EvidenceUnknown && !want.byOrg {
		if r.accountSource == EvidenceInferred {
			next.account, next.accountSource = "unknown", EvidenceUnknown
		}
		if r.subscriptionSource == EvidenceInferred {
			next.subscription, next.subscriptionSource = "unknown", EvidenceUnknown
		}
	}
	switch {
	case want.byOrg && want.source != "":
		next.accountSource = EvidenceUnknown
		if named && rank > 0 {
			next.account, next.accountSource = want.account, want.source
		}
		return next, next != r
	case want.byOrg:
		return r, false
	}
	if rank > 0 && rank >= EvidenceRank(r.accountSource) && named {
		next.account, next.accountSource = want.account, want.source
	}
	if rank > 0 && rank >= EvidenceRank(r.subscriptionSource) &&
		want.subscription != "" && want.subscription != "unknown" {
		next.subscription, next.subscriptionSource = want.subscription, want.source
	}
	return next, next != r
}

// accountsQuery selects the rows ResolveAccounts attributes: those with a
// field not decided by its transcript (openRows), all when scope is nil,
// else the rows scope stored, the rows of its sessions and of the sessions in
// dirty, each tool's rows after its watermark, and the rows of a subscription
// whose email owners names for the first time (not in known) whose email no
// transcript decided, so a sign-in seen for the first time names every row of
// its organization. That condition reads every row, so it is added only
// when there is such a subscription. When account marks were stored since the
// last resolution (basis.marked), the unknown and inferred rows from the
// start of the stretch they changed are read too.
//
// A watermark and a stretch start are timeLayout text, and meet a row's time
// as milliseconds (sinceBound); no bound leaves the time out of the
// condition. A tool, a session or a subscription the ledger holds no name of
// has no rows, and adds no condition. Each condition on a tool's times states
// openRows again, so SQLite reads it through responses_open.
func accountsQuery(
	scope *Run,
	dirty []string,
	basis accountBasis,
	names *nameSet,
) (string, []any, error) {
	query := `SELECT id, tool, ts, root, account, account_source,
	  subscription, subscription_source FROM responses
	 WHERE ts IS NOT NULL AND (` + openRows + `)`
	if scope == nil {
		return query, nil, nil
	}
	ids := make([]string, 0, len(scope.seen))
	for id := range scope.seen {
		ids = append(ids, hex.EncodeToString(packID(id)))
	}
	conds := []string{"request_id IN (SELECT unhex(value) FROM json_each(?))"}
	args := []any{jsonText(ids)}
	roots := map[string][]string{}
	for _, key := range slices.Concat(slices.Collect(maps.Keys(scope.roots)), dirty) {
		if tool, root, ok := strings.Cut(key, ":"); ok {
			roots[tool] = append(roots[tool], root)
		}
	}
	tools, err := names.found(nameTool, slices.Concat(slices.Collect(maps.Keys(roots)),
		slices.Collect(maps.Keys(basis.through)), slices.Collect(maps.Keys(basis.stretches))))
	if err != nil {
		return "", nil, err
	}
	for _, tool := range slices.Sorted(maps.Keys(roots)) {
		rootIDs, err := names.found(nameSession, roots[tool])
		if err != nil {
			return "", nil, err
		}
		if id, ok := tools[tool]; ok && len(rootIDs) > 0 {
			conds = append(conds, "(tool = ? AND root IN (SELECT value FROM json_each(?)))")
			args = append(args, id, jsonText(slices.Sorted(maps.Values(rootIDs))))
		}
	}
	for _, tool := range slices.Sorted(maps.Keys(basis.through)) {
		if id, ok := tools[tool]; ok {
			cond, bound := sinceBound("ts > ?", basis.through[tool])
			conds = append(conds, "(tool = ? AND "+cond+" AND ("+openRows+"))")
			args = append(append(args, id), bound...)
		}
	}
	var unseen []string
	for _, subscription := range basis.owned() {
		if !basis.known[subscription] {
			unseen = append(unseen, subscription)
		}
	}
	learned, err := names.found(nameSubscription, unseen)
	if err != nil {
		return "", nil, err
	}
	if len(learned) > 0 {
		conds = append(conds, `(subscription_source IN (4, 1) AND account_source < 4
		   AND subscription IN (SELECT value FROM json_each(?)))`)
		args = append(args, jsonText(slices.Sorted(maps.Values(learned))))
	}
	if basis.marked != "" {
		// Rows the new marks may infer anew: from the start of the stretch
		// the earliest of them changed, of the tools that infer.
		for _, tool := range slices.Sorted(maps.Keys(basis.stretches)) {
			id, ok := tools[tool]
			if !ok {
				continue
			}
			since := ""
			if basis.marked != dirtyRootsAll {
				since = basis.stretches[tool].start(basis.marked)
			}
			cond, bound := sinceBound("ts >= ?", since)
			conds = append(conds, "(tool = ? AND "+cond+" AND account_source IN (0, 1) AND ("+
				openRows+"))")
			args = append(append(args, id), bound...)
		}
	}
	return query + " AND (" + strings.Join(conds, " OR ") + ")", args, nil
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

// sinceBound is cond, a condition on ts with one argument, for since, a time
// in timeLayout, and since in milliseconds; for "" (no bound) it is a
// condition every dated row meets and no argument.
func sinceBound(cond, since string) (string, []any) {
	ms, ok := parseStoredTime(since)
	if !ok {
		return "ts IS NOT NULL", nil
	}
	return cond, []any{ms}
}

// accountsDue reads which sessions BindSession bound since the last
// resolution, and whether every row is due instead: no resolution has run on
// the ledger, or the note lists too many sessions or cannot be read.
func accountsDue(ctx context.Context, tx *sql.Tx) (dirty []string, full bool, err error) {
	checked, err := metaIn(ctx, tx, accountsChecked)
	if err != nil {
		return nil, false, err
	}
	note, err := metaIn(ctx, tx, accountsUnresolved)
	if err != nil {
		return nil, false, err
	}
	if checked != accountsRule || note == dirtyRootsAll {
		return nil, true, nil
	}
	if note != "" && json.Unmarshal([]byte(note), &dirty) != nil {
		return nil, true, nil
	}
	return dirty, false, nil
}

// ResolveAccounts gives each row the strongest evidence it has for its email
// and, apart, for its subscription: the session binding with the latest since
// at or before its time (EvidenceSession), else the sign-in observed on both
// sides of its time when the two agree (EvidenceObserved), else nothing, and
// the field stays "unknown" (or the email an earlier build stored). A field
// decided by its transcript never changes, so a row whose transcript named its
// plan but not its account still takes the email of a binding or an
// observation. A field is never given weaker evidence than it has, and a row
// after a tool's latest observation waits for a later one. A row whose
// transcript named its organization takes that organization's email instead
// (accountBasis.attribute), learned from every sign-in the ledger recorded and
// from signIns, the ones each tool keeps on disk now. Rows of a Codex
// subagent stored under a spawning parent are first moved to the root thread of
// the links ingest recorded (moveRowsToRoots).
//
// As ResolveTiers prices only what a run could have changed, with scope set
// only these rows are attributed again: those scope stored, those of the
// sessions it stored rows of or that BindSession bound since the last
// resolution, and those after a tool's latest observation at the last
// resolution. A nil scope, or a ledger no resolution has run on, attributes
// every row. Either way it clears accountsUnresolved and stores
// accountsChecked. Ingest calls it after every file of a run is committed, as
// it calls ResolveTiers.
func (l *Ledger) ResolveAccounts(
	ctx context.Context,
	scope *Run,
	signIns map[string][]SignIn,
) error {
	return l.transactNames(ctx, func(tx *sql.Tx, names *nameSet) error {
		dirty, full, err := accountsDue(ctx, tx)
		if err != nil {
			return err
		}
		moved, err := moveRowsToRoots(ctx, tx, names)
		if err != nil {
			return err
		}
		dirty = append(dirty, moved...)
		if err := fillSessionPlans(ctx, tx, names); err != nil {
			return err
		}
		basis, err := loadAccountBasis(ctx, tx, signIns)
		if err != nil {
			return err
		}
		if full {
			scope = nil
		}
		query, args, err := accountsQuery(scope, dirty, basis, names)
		if err != nil {
			return err
		}
		changes, err := accountChanges(ctx, tx, names, basis, query, args)
		if err != nil {
			return err
		}
		for _, c := range changes {
			account, err := names.id(nameAccount, c.account)
			if err != nil {
				return err
			}
			subscription, err := names.id(nameSubscription, c.subscription)
			if err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx,
				`UPDATE responses SET account = ?, account_source = ?,
				  subscription = ?, subscription_source = ? WHERE id = ?`,
				account, EvidenceRank(c.accountSource),
				subscription, EvidenceRank(c.subscriptionSource), c.id); err != nil {
				return err
			}
		}
		return finishAccounts(ctx, tx, basis)
	})
}

// accountChanges reads the rows of accountsQuery's query and args, with
// their names and their time as text, and returns each one basis attributes
// anew.
func accountChanges(
	ctx context.Context,
	tx *sql.Tx,
	names *nameSet,
	basis accountBasis,
	query string,
	args []any,
) ([]accountRow, error) {
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var changes []accountRow
	for rows.Next() {
		var r accountRow
		var ts int64
		var ids [4]int64
		var ranks [2]int64
		if err := rows.Scan(
			&r.id, &ids[0], &ts, &ids[1], &ids[2], &ranks[0], &ids[3], &ranks[1],
		); err != nil {
			return nil, err
		}
		for i, to := range []*string{&r.tool, &r.root, &r.account, &r.subscription} {
			if *to, err = names.name(ids[i]); err != nil {
				return nil, err
			}
		}
		r.ts = storedTime(ts)
		r.accountSource, r.subscriptionSource = evidenceWord(ranks[0]), evidenceWord(ranks[1])
		if next, ok := r.update(basis.attribute(r)); ok {
			changes = append(changes, next)
		}
	}
	return changes, rows.Err()
}

// fillSessionPlans gives a row of a tool with a SessionPlan, whose
// transcript names no subscription, the one its session's transcript names
// there (Tool.SessionPlan), as transcript evidence; with it the email of that
// row, when its transcript named that and the row's own evidence for the email
// is weaker. A Codex row looks in its own thread, a Claude Code row in its
// root session, whose subagents run in its process. A session start or resume
// a hook bound between the two rows may be another process under another
// sign-in, so a row never takes a subscription across one. Only sessions that
// hold rows of both kinds are read.
func fillSessionPlans(ctx context.Context, tx *sql.Tx, names *nameSet) error {
	for _, tool := range Tools {
		if tool.SessionPlan == "" {
			continue
		}
		toolID, found, err := names.lookup(nameTool, tool.Name)
		if err != nil {
			return err
		}
		if !found {
			continue
		}
		group := "root"
		if tool.SessionPlan == planNext {
			group = "session"
		}
		bound, err := sessionStarts(ctx, tx, tool.Name)
		if err != nil {
			return err
		}
		all, err := mixedPlanRows(ctx, tx, toolID, group)
		if err != nil {
			return err
		}
		// Each group's rows in time order, a tie in the order of their ledger
		// keys; the groups are apart, so their order does not matter.
		slices.SortFunc(all, func(a, b planRow) int {
			return cmp.Or(cmp.Compare(a.group, b.group), cmp.Compare(a.ts, b.ts),
				strings.Compare(a.key, b.key))
		})
		if tool.SessionPlan == planNext {
			slices.Reverse(all)
		}
		var from *planRow
		for i := range all {
			r := &all[i]
			if from != nil && from.group != r.group {
				from = nil
			}
			if r.named {
				from = r
				continue
			}
			if from == nil {
				continue
			}
			root, err := names.name(r.root)
			if err != nil {
				return err
			}
			if bound.between(root, storedTime(from.ts), storedTime(r.ts)) {
				continue
			}
			account, accountSource := r.account, any(nil)
			if from.emailNamed && r.accountRank < EvidenceRank(EvidenceTranscript) {
				account, accountSource = from.account, EvidenceRank(EvidenceTranscript)
			}
			if _, err := tx.ExecContext(ctx,
				`UPDATE responses SET subscription = ?, subscription_source = 4,
				   account = ?, account_source = COALESCE(?, account_source)
				 WHERE id = ?`,
				from.subscription, account, accountSource, r.id,
			); err != nil {
				return err
			}
		}
	}
	return nil
}

// planRow is a row fillSessionPlans reads: its id, its group (its root or
// its session), root, subscription and account ids, its time, its ledger
// key, whether its transcript named its subscription and its email, and the
// rank of its email's evidence.
type planRow struct {
	id, group, root, subscription, account int64
	ts                                     int64
	key                                    string
	named, emailNamed                      bool
	accountRank                            int
}

// mixedPlanRows reads the rows of the tool toolID in each group (the column
// group) that holds rows with and without a subscription their transcript
// named.
func mixedPlanRows(ctx context.Context, tx *sql.Tx, toolID int64, group string) ([]planRow, error) {
	rows, err := tx.QueryContext(ctx, `
		WITH mixed AS (
		  SELECT `+group+` FROM responses WHERE +tool = ?1 GROUP BY `+group+`
		  HAVING MAX(subscription_source = 4) = 1 AND MIN(subscription_source = 4) = 0)
		SELECT id, request_id, `+group+`, root, IFNULL(ts, ?2), subscription,
		       subscription_source = 4, account, account_source = 4, account_source
		FROM responses WHERE +tool = ?1 AND `+group+` IN (SELECT `+group+` FROM mixed)`,
		toolID, int64(noTime))
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var all []planRow
	for rows.Next() {
		var r planRow
		var packed []byte
		if err := rows.Scan(&r.id, &packed, &r.group, &r.root, &r.ts, &r.subscription,
			&r.named, &r.account, &r.emailNamed, &r.accountRank); err != nil {
			return nil, err
		}
		if r.key, err = unpackID(packed); err != nil {
			return nil, err
		}
		all = append(all, r)
	}
	return all, rows.Err()
}

// boundTimes are the times hooks bound each root session of a tool, in order.
type boundTimes map[string][]string

func sessionStarts(ctx context.Context, tx *sql.Tx, tool string) (boundTimes, error) {
	rows, err := tx.QueryContext(ctx,
		"SELECT session, since FROM session_accounts WHERE tool = ? ORDER BY session, since", tool)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	bound := boundTimes{}
	for rows.Next() {
		var session, since string
		if err := rows.Scan(&session, &since); err != nil {
			return nil, err
		}
		bound[session] = append(bound[session], since)
	}
	return bound, rows.Err()
}

// between reports whether root was bound after the earlier of a and b and at
// or before the later one.
func (b boundTimes) between(root, x, y string) bool {
	lo, hi := min(x, y), max(x, y)
	times := b[root]
	i := sort.SearchStrings(times, lo)
	for ; i < len(times) && times[i] == lo; i++ {
	}
	return i < len(times) && times[i] <= hi
}

// maxThreadDepth bounds a walk up thread_parents, so a cycle that damaged
// data could hold ends. Codex's own depth limit is far below it.
const maxThreadDepth = 64

// moveRowsToRoots gives each row stored under a thread that has a parent
// the root of that thread's chain, and returns the rootKeys of the roots that
// gained rows. A parser knows only the spawning parent of an old subagent, so
// a depth-2 subagent's rows arrive under its parent; the parent's own link
// may be read before or after them, in this run or another, so the walk is
// done here, over every link, whatever order the files were read in. A row
// already under its root is not touched, so it is idempotent.
func moveRowsToRoots(ctx context.Context, tx *sql.Tx, names *nameSet) ([]string, error) {
	rows, err := tx.QueryContext(ctx, "SELECT tool, thread, parent FROM thread_parents")
	if err != nil {
		return nil, err
	}
	parents := map[bindingKey]string{}
	for rows.Next() {
		var key bindingKey
		var parent string
		if err := rows.Scan(&key.tool, &key.session, &parent); err != nil {
			_ = rows.Close()
			return nil, err
		}
		parents[key] = parent
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	var moved []string
	for _, key := range slices.SortedFunc(maps.Keys(parents), func(a, b bindingKey) int {
		return cmp.Or(cmp.Compare(a.tool, b.tool), cmp.Compare(a.session, b.session))
	}) {
		root := parents[key]
		for range maxThreadDepth {
			next, ok := parents[bindingKey{key.tool, root}]
			if !ok {
				break
			}
			root = next
		}
		if root == key.session {
			continue // a cycle in damaged data: no root to move to
		}
		tool, found, err := names.lookup(nameTool, key.tool)
		if err != nil || !found {
			if err != nil {
				return nil, err
			}
			continue // no rows of the tool
		}
		under, found, err := names.lookup(nameSession, key.session)
		if err != nil || !found {
			if err != nil {
				return nil, err
			}
			continue // no rows under the thread
		}
		var one int
		switch err := tx.QueryRowContext(ctx,
			"SELECT 1 FROM responses WHERE tool = ? AND root = ? LIMIT 1", tool, under,
		).Scan(&one); {
		case errors.Is(err, sql.ErrNoRows):
			continue
		case err != nil:
			return nil, err
		}
		rootID, err := names.id(nameSession, root)
		if err != nil {
			return nil, err
		}
		result, err := tx.ExecContext(
			ctx,
			"UPDATE responses SET root = ? WHERE tool = ? AND root = ?",
			rootID,
			tool,
			under,
		)
		if err != nil {
			return nil, err
		}
		if n, err := result.RowsAffected(); err != nil {
			return nil, err
		} else if n > 0 && !slices.Contains(moved, rootKey(key.tool, root)) {
			moved = append(moved, rootKey(key.tool, root))
		}
	}
	return moved, nil
}

// finishAccounts stores that a resolution ran: each tool's latest observation
// as its watermark, the subscriptions whose email it knew, and no session left
// to attribute.
func finishAccounts(ctx context.Context, tx *sql.Tx, basis accountBasis) error {
	for tool, points := range basis.observed {
		if err := setMeta(ctx, tx, accountsThrough+tool, points[len(points)-1].at); err != nil {
			return err
		}
	}
	owned, err := json.Marshal(basis.owned())
	if err != nil {
		return err
	}
	if err := setMeta(ctx, tx, accountsOwned, string(owned)); err != nil {
		return err
	}
	if _, err := tx.ExecContext(
		ctx,
		"DELETE FROM meta WHERE key IN (?, ?)",
		accountsUnresolved, accountsMarked,
	); err != nil {
		return err
	}
	return setMeta(ctx, tx, accountsChecked, accountsRule)
}

// tierSeries are the ledger's tier changes, by series key, in time order.
type tierSeries map[string][]tierChangeRow

type tierChangeRow struct{ ts, tier string }

func loadTierSeries(ctx context.Context, tx *sql.Tx) (tierSeries, error) {
	rows, err := tx.QueryContext(
		ctx,
		"SELECT thread_id, ts, tier FROM tier_changes ORDER BY thread_id, ts",
	)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	series := tierSeries{}
	for rows.Next() {
		var key string
		var c tierChangeRow
		if err := rows.Scan(&key, &c.ts, &c.tier); err != nil {
			return nil, err
		}
		series[key] = append(series[key], c)
	}
	return series, rows.Err()
}

// splitTierFrom splits a TierFrom into its series key and the time it is
// read at, "" for the row's own time.
func splitTierFrom(from string) (key, at string) {
	if i := strings.LastIndex(from, "@"); i >= 0 {
		return from[:i], from[i+1:]
	}
	return from, ""
}

// tierAt is series key's tier at time at: its latest change at or before
// then, following a change that takes another series' tier. found is false
// when no change is that early, or a chain of such changes loops.
func (s tierSeries) tierAt(key, at string) (tier string, found bool) {
	for range 64 {
		changes := s[key]
		i := sort.Search(len(changes), func(i int) bool { return changes[i].ts > at })
		if i == 0 {
			return "", false
		}
		from, ok := strings.CutPrefix(changes[i-1].tier, "=")
		if !ok {
			return changes[i-1].tier, true
		}
		key, at = splitTierFrom(from)
	}
	return "", false
}

// dependents adds to keys every series whose changes take their tier from a
// series in keys, until none is left to add.
func (s tierSeries) dependents(keys map[string]bool) map[string]bool {
	readers := map[string][]string{}
	for key, changes := range s {
		for _, c := range changes {
			if from, ok := strings.CutPrefix(c.tier, "="); ok {
				source, _ := splitTierFrom(from)
				readers[source] = append(readers[source], key)
			}
		}
	}
	out := maps.Clone(keys)
	queue := slices.Collect(maps.Keys(keys))
	for len(queue) > 0 {
		key := queue[len(queue)-1]
		queue = queue[:len(queue)-1]
		for _, reader := range readers[key] {
			if !out[reader] {
				out[reader] = true
				queue = append(queue, reader)
			}
		}
	}
	return out
}

// ResolveTiers prices each row whose tier another thread decides (its
// TierFrom, kept in tier_pending: a series and a time, by default the row's
// own) at that series' latest change at or before that time, through any
// change that takes another series' tier, and as served says Codex sent it.
// A row whose series has no change by then is priced at the standard tier.
// Ingest calls it after every file of a run is committed, so the order files
// are read in does not matter.
//
// A row's answer changes only when the row is written again or a series it
// reads, directly or through other series, gains a change, so with run set
// only the rows run put in tier_pending and those reading a series run wrote
// a change to are priced again; a nil run prices every such row. A row keeps
// its TierFrom, so a change read later, even one earlier than the row,
// prices it again. Either way it clears tiersUnresolved and stores
// tiersChecked. It returns how many rows changed price.
func (l *Ledger) ResolveTiers(
	ctx context.Context,
	run *Run,
	served map[string]func(model, tier string) string,
) (int, error) {
	changed := 0
	err := l.transactNames(ctx, func(tx *sql.Tx, names *nameSet) error {
		series, err := loadTierSeries(ctx, tx)
		if err != nil {
			return err
		}
		var scope *Run
		if run != nil {
			scope = &Run{pending: run.pending, threads: series.dependents(run.threads)}
		}
		query, args := pendingQuery(scope)
		prices, err := tierPrices(ctx, tx, names, series, served, query, args)
		if err != nil {
			return err
		}
		for _, p := range prices {
			model, err := names.id(nameModel, p.model)
			if err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx,
				"UPDATE responses SET model = ? WHERE id = ?", model, p.id); err != nil {
				return err
			}
		}
		changed = len(prices)
		if _, err := tx.ExecContext(
			ctx,
			"DELETE FROM meta WHERE key = ?",
			tiersUnresolved,
		); err != nil {
			return err
		}
		return setMeta(ctx, tx, tiersChecked, tiersRule)
	})
	return changed, err
}

// tierPrice is a row ResolveTiers prices anew: its id and its model with the
// tier its time is served at.
type tierPrice struct {
	id    int64
	model string
}

// tierPrices reads the rows of pendingQuery's query and args and returns each
// one whose tier at its time, in series as the tool serves it (served), is
// not the one its model names.
func tierPrices(
	ctx context.Context,
	tx *sql.Tx,
	names *nameSet,
	series tierSeries,
	served map[string]func(model, tier string) string,
	query string,
	args []any,
) ([]tierPrice, error) {
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var prices []tierPrice
	for rows.Next() {
		var id, ms int64
		var ids [2]int64
		var from string
		if err := rows.Scan(&id, &ids[0], &ms, &from, &ids[1]); err != nil {
			return nil, err
		}
		model, err := names.name(ids[0])
		if err != nil {
			return nil, err
		}
		tool, err := names.name(ids[1])
		if err != nil {
			return nil, err
		}
		key, at := splitTierFrom(from)
		owner, _ := Lookup(tool)
		base, _ := owner.SplitModel(model)
		want := base
		if tier, _ := series.tierAt(key, cmp.Or(at, storedTime(ms))); tier != "" {
			if serve := served[tool]; serve != nil {
				tier = serve(base, tier)
			}
			if tier != "" {
				want = base + "@" + tier
			}
		}
		if want != model {
			prices = append(prices, tierPrice{id, want})
		}
	}
	return prices, rows.Err()
}

// pendingQuery selects the rows ResolveTiers prices, with their id, model,
// time (noTime for none), TierFrom and tool: run's, or every one when run is
// nil.
func pendingQuery(run *Run) (string, []any) {
	query := `
SELECT r.id, r.model, IFNULL(r.ts, ?1), n.name, r.tool
  FROM tier_pending p JOIN responses r ON r.request_id = p.request_id
  JOIN names n ON n.id = p.root`
	if run == nil {
		return query, []any{int64(noTime)}
	}
	ids := make([]string, 0, len(run.pending))
	for id := range run.pending {
		ids = append(ids, hex.EncodeToString(packID(id)))
	}
	// The ids of the threads' TierFroms are read from names first, so that
	// each waiting row is matched by its root's id alone.
	query += `
 WHERE p.request_id IN (SELECT unhex(value) FROM json_each(?2))
    OR p.root IN (SELECT id FROM names WHERE kind = ?4 AND (CASE WHEN instr(name, '@') > 0
         THEN substr(name, 1, instr(name, '@') - 1) ELSE name END) IN (SELECT value FROM json_each(?3)))`
	return query, []any{
		int64(noTime),
		jsonText(ids),
		jsonText(slices.Collect(maps.Keys(run.threads))),
		nameSession,
	}
}

// SetFile stores where ingest stopped in a transcript.
func (t *Tx) SetFile(path string, row FileRow) error {
	_, err := t.tx.ExecContext(
		t.ctx,
		`INSERT INTO files (path, offset, size, mtime_ns, last_seen, state, head)
		 VALUES (?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(path) DO UPDATE SET offset = excluded.offset, size = excluded.size,
		   mtime_ns = excluded.mtime_ns, last_seen = excluded.last_seen,
		   state = excluded.state, head = excluded.head`,
		path,
		row.Offset,
		row.Size,
		row.ModTimeNanos,
		stamp(time.Now()),
		string(row.State),
		row.Head,
	)
	return err
}

// stamp is the time format of last_ingest_at and the log: RFC 3339 in UTC to
// the second, as the ledger has always recorded it.
func stamp(t time.Time) string { return t.UTC().Format("2006-01-02T15:04:05-07:00") }
