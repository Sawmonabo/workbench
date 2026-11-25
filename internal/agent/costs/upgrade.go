package costs

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/Sawmonabo/workbench/internal/sqlitefile"
)

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

// execAll runs statements in tx, in order, up to the first that fails.
func execAll(ctx context.Context, tx *sql.Tx, statements ...string) error {
	for _, statement := range statements {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return err
		}
	}
	return nil
}
