package costs

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"maps"
	"net/url"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite" // the pure-Go driver, name "sqlite"

	"github.com/Sawmonabo/workbench/internal/operation"
)

// Paths are where the ledger and its companions live. The environment
// overrides CLAUDE_COSTS_LEDGER, CLAUDE_COSTS_STATE and CLAUDE_COSTS_RATES
// replace them; otherwise XDG_DATA_HOME, XDG_STATE_HOME and XDG_CONFIG_HOME
// are honored, then ~/.local/share, ~/.local/state and ~/.config. The ledger
// stays at its pre-Workbench path because transcripts expire and the ledger
// is the only lasting record.
type Paths struct {
	Home      string
	Ledger    string // the durable record
	State     string // ingest.log and ingest.lock
	Overrides string // manual per-model rates, highest priority
}

// Locations resolves [Paths] from the environment.
func Locations() (Paths, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Paths{}, err
	}
	pick := func(override, xdg, fallback string, elem ...string) string {
		if v := os.Getenv(override); v != "" {
			return v
		}
		base := os.Getenv(xdg)
		if base == "" {
			base = filepath.Join(home, fallback)
		}
		return filepath.Join(append([]string{base}, elem...)...)
	}
	return Paths{
		Home: home,
		Ledger: pick(
			"CLAUDE_COSTS_LEDGER",
			"XDG_DATA_HOME",
			".local/share",
			"claude-costs",
			"ledger.sqlite",
		),
		State: pick("CLAUDE_COSTS_STATE", "XDG_STATE_HOME", ".local/state", "claude-costs"),
		Overrides: pick(
			"CLAUDE_COSTS_RATES",
			"XDG_CONFIG_HOME",
			".config",
			"claude-costs",
			"rates.json",
		),
	}, nil
}

func (p Paths) log() string  { return filepath.Join(p.State, "ingest.log") }
func (p Paths) lock() string { return filepath.Join(p.State, "ingest.lock") }

const (
	schemaVersion = "3"

	// firstTool is the tool of every row written before the tool column
	// existed, and the column's default: its request IDs are stored as they
	// are, every other tool's as <tool>:<id>.
	firstTool = "claude"
)

const schema = `
CREATE TABLE responses (
  request_id     TEXT PRIMARY KEY,
  ts             TEXT NOT NULL,
  model          TEXT NOT NULL,
  project        TEXT NOT NULL,
  session_id     TEXT NOT NULL,
  account        TEXT NOT NULL,
  account_source TEXT NOT NULL,
  input          INTEGER NOT NULL,
  output         INTEGER NOT NULL,
  cache_write_5m INTEGER NOT NULL,
  cache_write_1h INTEGER NOT NULL,
  cache_read     INTEGER NOT NULL,
  tool           TEXT NOT NULL DEFAULT 'claude'
);
CREATE INDEX responses_ts ON responses (ts);
CREATE INDEX responses_project_model ON responses (project, model);
CREATE INDEX responses_account ON responses (account);
CREATE INDEX responses_tool ON responses (tool);
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
CREATE TABLE tier_pending (
  request_id TEXT PRIMARY KEY,
  root       TEXT NOT NULL
);
CREATE TABLE meta (key TEXT PRIMARY KEY, value TEXT NOT NULL);
`

// Ledger is the SQLite record of every response.
type Ledger struct {
	Path string
	db   *sql.DB
}

// OpenLedger opens the ledger at path. It never deletes, renames or rebuilds
// an existing file: a version 1 or 2 ledger is upgraded in one transaction
// that keeps every row, any other version fails with the path and version and
// writes nothing. A zero-byte file (a crash between creating the file and
// writing the schema) holds nothing and is treated as absent. create makes a
// missing ledger instead of failing.
func OpenLedger(path string, create bool) (*Ledger, error) {
	info, err := os.Stat(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	exists := err == nil && info.Size() > 0
	if !exists && !create {
		return nil, operation.Fail(
			operation.ExitBlocked,
			"ledger",
			"No ledger at "+path+"; run `workbench costs ingest --worker` first",
		)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	// Write locks are taken at BEGIN, so two processes upgrading or ingesting
	// wait on the busy timeout instead of failing when a read turns into a write.
	// In WAL mode synchronous=NORMAL skips the sync at each commit and keeps the
	// ledger consistent; a crash can lose only the last commits, and each one
	// stores its file's offset with its rows, so the next ingest reads them again.
	query := "_pragma=busy_timeout(5000)&_pragma=synchronous(NORMAL)&_txlock=immediate"
	db, err := sql.Open("sqlite", (&url.URL{Scheme: "file", Path: path, RawQuery: query}).String())
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	ledger := &Ledger{Path: path, db: db}
	if err := ledger.prepare(exists); err != nil {
		_ = db.Close()
		return nil, err
	}
	return ledger, nil
}

func (l *Ledger) prepare(exists bool) error {
	ctx := context.Background()
	if exists {
		var tables int
		if err := l.db.QueryRowContext(
			ctx,
			"SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'meta'",
		).Scan(&tables); err != nil {
			return fmt.Errorf("%s: %w", l.Path, err)
		}
		if tables == 0 {
			return operation.Fail(
				operation.ExitInvalid,
				"ledger",
				l.Path+" exists but is not a Workbench costs ledger; move it aside",
			)
		}
		version, err := l.Meta("schema_version")
		if err != nil {
			return fmt.Errorf("%s: %w", l.Path, err)
		}
		switch version {
		case schemaVersion:
		case "1", "2":
			if err := l.upgrade(ctx); err != nil {
				return fmt.Errorf("%s: %w", l.Path, err)
			}
		default:
			return operation.Fail(
				operation.ExitInvalid,
				"ledger",
				fmt.Sprintf(
					"%s: schema version %q, this tool needs %s",
					l.Path,
					version,
					schemaVersion,
				),
			)
		}
	} else if err := l.create(ctx); err != nil {
		return fmt.Errorf("%s: %w", l.Path, err)
	}
	// Only a verified ledger is switched to WAL, so a foreign database is
	// never modified. WAL lets a report read the last committed state while a
	// worker ingests.
	if _, err := l.db.ExecContext(ctx, "PRAGMA journal_mode=WAL"); err != nil {
		return fmt.Errorf("%s: %w", l.Path, err)
	}
	return nil
}

func (l *Ledger) create(ctx context.Context) error {
	return l.transact(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, schema); err != nil {
			return err
		}
		return setMeta(ctx, tx, "schema_version", schemaVersion)
	})
}

// upgrade is the only write to existing data, in one transaction that keeps
// every row: version 1 gains the tool column, versions 1 and 2 gain the saved
// file state and the tier tables.
func (l *Ledger) upgrade(ctx context.Context) error {
	return l.transact(ctx, func(tx *sql.Tx) error {
		var version string
		if err := tx.QueryRowContext(
			ctx, "SELECT value FROM meta WHERE key = 'schema_version'",
		).Scan(&version); err != nil {
			return err
		}
		var statements []string
		switch version {
		case "1":
			statements = append(statements,
				"ALTER TABLE responses ADD COLUMN tool TEXT NOT NULL DEFAULT 'claude'",
				"CREATE INDEX IF NOT EXISTS responses_tool ON responses (tool)",
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
			)
		default:
			return nil // another process upgraded it first
		}
		for _, statement := range statements {
			if _, err := tx.ExecContext(ctx, statement); err != nil {
				return err
			}
		}
		return setMeta(ctx, tx, "schema_version", schemaVersion)
	})
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

// Empty reports whether the ledger holds no responses.
func (l *Ledger) Empty() (bool, error) {
	var one int
	err := l.db.QueryRow("SELECT 1 FROM responses LIMIT 1").Scan(&one)
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

// DeleteFile forgets a transcript that no longer exists.
func (l *Ledger) DeleteFile(path string) error {
	_, err := l.db.Exec("DELETE FROM files WHERE path = ?", path)
	return err
}

// Tx is one transaction of ledger writes.
type Tx struct {
	ctx context.Context
	tx  *sql.Tx
	// The statements Upsert runs, prepared on first use: SQLite parses each
	// once per transaction instead of once per row.
	upsert, pend, unpend *sql.Stmt
	seen                 Seen // what earlier transactions of this run stored
	wrote                Seen // what this one stored, added to seen once it commits
}

// Seen is what one ingest run has committed: each response's stored time and
// token counts. Most rows of a first run are copies a fork made of its
// parent's usage; Upsert skips a copy that it can tell would change nothing.
// It is filled only after a commit succeeds, so it never holds a row that a
// rolled-back transaction wrote.
type Seen map[string]storedRow

type storedRow struct {
	ts     string
	tokens [5]int64 // input, output, cache_write_5m, cache_write_1h, cache_read
}

// Transaction runs fn and commits, or rolls back when fn fails. seen is the
// run's record of committed rows, or nil.
func (l *Ledger) Transaction(ctx context.Context, seen Seen, fn func(*Tx) error) error {
	t := &Tx{ctx: ctx, seen: seen, wrote: Seen{}}
	err := l.transact(ctx, func(tx *sql.Tx) error {
		t.tx = tx
		return fn(t)
	})
	if err == nil && seen != nil {
		maps.Copy(seen, t.wrote)
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
const upsert = `
INSERT INTO responses (request_id, ts, model, project, session_id, account, account_source,
                       input, output, cache_write_5m, cache_write_1h, cache_read, tool)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(request_id) DO UPDATE SET
  ts         = CASE WHEN ` + earlier + ` THEN excluded.ts ELSE ts END,
  model      = CASE WHEN ` + earlier + ` THEN excluded.model ELSE model END,
  project    = CASE WHEN ` + earlier + ` THEN excluded.project ELSE project END,
  session_id = CASE WHEN ` + earlier + ` THEN excluded.session_id ELSE session_id END,
  input = MAX(input, excluded.input), output = MAX(output, excluded.output),
  cache_write_5m = MAX(cache_write_5m, excluded.cache_write_5m),
  cache_write_1h = MAX(cache_write_1h, excluded.cache_write_1h),
  cache_read = MAX(cache_read, excluded.cache_read),
  account = CASE WHEN excluded.account_source = 'session' THEN excluded.account ELSE account END,
  account_source = CASE WHEN excluded.account_source = 'session' THEN 'session' ELSE account_source END
RETURNING ts, input, output, cache_write_5m, cache_write_1h, cache_read,
  EXISTS (SELECT 1 FROM tier_pending WHERE tier_pending.request_id = responses.request_id)
`

// earlier is true when the incoming copy is at least as old as the stored
// row, which then takes its attribution. In an UPDATE, ts is the old value.
const earlier = `(ts = '' OR (excluded.ts <> '' AND excluded.ts <= ts))`

// timeLayout is how a response's time is stored: the form Claude Code writes,
// UTC with milliseconds, which sorts as text.
const timeLayout = "2006-01-02T15:04:05.000Z"

// storedID is the ledger key of a response: Claude's id as is, every other
// tool's as <tool>:<id>.
func storedID(u Usage) string {
	if u.Tool == firstTool {
		return u.RequestID
	}
	return u.Tool + ":" + u.RequestID
}

// Upsert records one response; source is "session" or "sweep". The account and
// source of an existing row are replaced only by a "session" one. A row priced
// by its root thread's tier waits in tier_pending until ResolveTiers prices it;
// the pending row follows the copy whose attribution is stored, so a later
// copy never changes what an earlier one decided.
func (t *Tx) Upsert(u Usage, source string) error {
	id := storedID(u)
	when := ""
	if !u.Time.IsZero() {
		when = u.Time.UTC().Format(timeLayout)
	}
	// A sweep copy later than a committed row with a time, and with no count
	// above the committed ones, changes nothing: the attribution stays, MAX
	// keeps the counts, only a session copy replaces the account, and the
	// pending tier follows the stored attribution. A stored time only moves
	// earlier and counts only grow, so this holds whatever this transaction
	// wrote since.
	if seen, ok := t.seen[id]; ok && source != "session" && when != "" && seen.ts != "" &&
		when > seen.ts && u.Input <= seen.tokens[0] && u.Output <= seen.tokens[1] &&
		u.CacheWrite5m <= seen.tokens[2] && u.CacheWrite1h <= seen.tokens[3] &&
		u.CacheRead <= seen.tokens[4] {
		return nil
	}
	statement, err := t.prepared(&t.upsert, upsert)
	if err != nil {
		return err
	}
	var stored storedRow
	var pending bool
	if err := statement.QueryRowContext(
		t.ctx,
		id, when, u.Model, u.Project, u.Session, u.Account, source,
		u.Input, u.Output, u.CacheWrite5m, u.CacheWrite1h, u.CacheRead, u.Tool,
	).Scan(
		&stored.ts, &stored.tokens[0], &stored.tokens[1],
		&stored.tokens[2], &stored.tokens[3], &stored.tokens[4], &pending,
	); err != nil {
		return err
	}
	t.wrote[id] = stored
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
		_, err = statement.ExecContext(t.ctx, id)
		return err
	}
	statement, err = t.prepared(&t.pend, `INSERT INTO tier_pending (request_id, root) VALUES (?, ?)
		 ON CONFLICT(request_id) DO UPDATE SET root = excluded.root`)
	if err != nil {
		return err
	}
	_, err = statement.ExecContext(t.ctx, id, u.TierFrom)
	return err
}

// AddTierChange records a thread's switch of service tier.
func (t *Tx) AddTierChange(c TierChange) error {
	if c.Thread == "" || c.Time.IsZero() {
		return nil
	}
	_, err := t.tx.ExecContext(t.ctx,
		"INSERT OR IGNORE INTO tier_changes (thread_id, ts, tier) VALUES (?, ?, ?)",
		c.Thread, c.Time.UTC().Format(timeLayout), c.Tier)
	return err
}

// ResolveTiers prices each row waiting on its root thread's service tier at
// the root's latest tier change at or before the row's time, and stops it
// waiting. A row whose root has no change by then keeps waiting and is priced
// at the standard tier meanwhile. Ingest calls it after every file of a run is
// committed, so the order files are read in does not matter.
func (l *Ledger) ResolveTiers(ctx context.Context) (int, error) {
	type pending struct{ id, model, tier string }
	var resolved []pending
	err := l.transact(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `
SELECT p.request_id, r.model,
       (SELECT c.tier FROM tier_changes c
         WHERE c.thread_id = p.root AND c.ts <= r.ts ORDER BY c.ts DESC, c.rowid DESC LIMIT 1)
  FROM tier_pending p JOIN responses r ON r.request_id = p.request_id`)
		if err != nil {
			return err
		}
		for rows.Next() {
			var p pending
			var tier sql.NullString
			if err := rows.Scan(&p.id, &p.model, &tier); err != nil {
				_ = rows.Close()
				return err
			}
			if tier.Valid {
				p.tier = tier.String
				resolved = append(resolved, p)
			}
		}
		if err := rows.Close(); err != nil {
			return err
		}
		if err := rows.Err(); err != nil {
			return err
		}
		if len(resolved) == 0 {
			return nil
		}
		price, err := tx.PrepareContext(ctx, "UPDATE responses SET model = ? WHERE request_id = ?")
		if err != nil {
			return err
		}
		unpend, err := tx.PrepareContext(ctx, "DELETE FROM tier_pending WHERE request_id = ?")
		if err != nil {
			return err
		}
		for _, p := range resolved {
			model, _ := SplitTier(p.model)
			if p.tier != "" {
				model += "@" + p.tier
			}
			if _, err := price.ExecContext(ctx, model, p.id); err != nil {
				return err
			}
			if _, err := unpend.ExecContext(ctx, p.id); err != nil {
				return err
			}
		}
		return nil
	})
	return len(resolved), err
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
