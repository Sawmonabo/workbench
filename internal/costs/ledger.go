package costs

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
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
	schemaVersion = "2"

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
  last_seen TEXT NOT NULL
);
CREATE TABLE meta (key TEXT PRIMARY KEY, value TEXT NOT NULL);
`

// Ledger is the SQLite record of every response.
type Ledger struct {
	Path string
	db   *sql.DB
}

// OpenLedger opens the ledger at path. It never deletes, renames or rebuilds
// an existing file: a version 1 ledger gains the tool column in one
// transaction, any other version fails with the path and version and writes
// nothing. A zero-byte file (a crash between creating the file and writing the
// schema) holds nothing and is treated as absent. create makes a missing
// ledger instead of failing.
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
	query := "_pragma=busy_timeout(5000)&_txlock=immediate"
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
		case "1":
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

// upgrade is the only write to existing data: the tool column, its index and
// the new version, in one transaction that keeps every row.
func (l *Ledger) upgrade(ctx context.Context) error {
	return l.transact(ctx, func(tx *sql.Tx) error {
		var version string
		if err := tx.QueryRowContext(
			ctx, "SELECT value FROM meta WHERE key = 'schema_version'",
		).Scan(&version); err != nil {
			return err
		}
		if version != "1" {
			return nil // another process upgraded it first
		}
		for _, statement := range []string{
			"ALTER TABLE responses ADD COLUMN tool TEXT NOT NULL DEFAULT 'claude'",
			"CREATE INDEX IF NOT EXISTS responses_tool ON responses (tool)",
		} {
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

// FileRow is where ingest stopped in one transcript.
type FileRow struct {
	Offset, Size, ModTimeNanos int64
}

// File is the stored position of a transcript.
func (l *Ledger) File(path string) (row FileRow, found bool, err error) {
	err = l.db.QueryRow(
		"SELECT offset, size, mtime_ns FROM files WHERE path = ?", path,
	).Scan(&row.Offset, &row.Size, &row.ModTimeNanos)
	if errors.Is(err, sql.ErrNoRows) {
		return row, false, nil
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
}

// Transaction runs fn and commits, or rolls back when fn fails.
func (l *Ledger) Transaction(ctx context.Context, fn func(*Tx) error) error {
	return l.transact(ctx, func(tx *sql.Tx) error { return fn(&Tx{ctx, tx}) })
}

// Streamed usage only grows, and Claude Code copies earlier records into the
// transcript of a resumed or forked session, sometimes mid-stream. The token
// columns therefore keep the largest value seen, so neither file order nor a
// later partial or zeroed copy can lower a request's final usage.
const upsert = `
INSERT INTO responses (request_id, ts, model, project, session_id, account, account_source,
                       input, output, cache_write_5m, cache_write_1h, cache_read, tool)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(request_id) DO UPDATE SET
  ts = excluded.ts, model = excluded.model, project = excluded.project,
  session_id = excluded.session_id,
  input = MAX(input, excluded.input), output = MAX(output, excluded.output),
  cache_write_5m = MAX(cache_write_5m, excluded.cache_write_5m),
  cache_write_1h = MAX(cache_write_1h, excluded.cache_write_1h),
  cache_read = MAX(cache_read, excluded.cache_read),
  account = CASE WHEN excluded.account_source = 'session' THEN excluded.account ELSE account END,
  account_source = CASE WHEN excluded.account_source = 'session' THEN 'session' ELSE account_source END
`

// timeLayout is how a response's time is stored: the form Claude Code writes,
// UTC with milliseconds, which sorts as text.
const timeLayout = "2006-01-02T15:04:05.000Z"

// Upsert records one response; source is "session" or "sweep". The account and
// source of an existing row are replaced only by a "session" one.
func (t *Tx) Upsert(u Usage, source string) error {
	id := u.RequestID
	if u.Tool != firstTool {
		id = u.Tool + ":" + id
	}
	when := ""
	if !u.Time.IsZero() {
		when = u.Time.UTC().Format(timeLayout)
	}
	_, err := t.tx.ExecContext(
		t.ctx, upsert,
		id, when, u.Model, u.Project, u.Session, u.Account, source,
		u.Input, u.Output, u.CacheWrite5m, u.CacheWrite1h, u.CacheRead, u.Tool,
	)
	return err
}

// SetFile stores where ingest stopped in a transcript.
func (t *Tx) SetFile(path string, row FileRow) error {
	_, err := t.tx.ExecContext(
		t.ctx,
		`INSERT INTO files (path, offset, size, mtime_ns, last_seen) VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT(path) DO UPDATE SET offset = excluded.offset, size = excluded.size,
		   mtime_ns = excluded.mtime_ns, last_seen = excluded.last_seen`,
		path, row.Offset, row.Size, row.ModTimeNanos, stamp(time.Now()),
	)
	return err
}

// stamp is the time format of last_ingest_at and the log: RFC 3339 in UTC to
// the second, as the ledger has always recorded it.
func stamp(t time.Time) string { return t.UTC().Format("2006-01-02T15:04:05-07:00") }
