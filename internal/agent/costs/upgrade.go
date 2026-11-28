package costs

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/Sawmonabo/workbench/internal/operation"
	"github.com/Sawmonabo/workbench/internal/sqlitefile"
)

// UpgradeSteps is told what an open's upgrade of an earlier version's ledger
// is doing: each step as it begins, [UpgradeWaiting] while it waits for
// another process's upgrade, then "" once the ledger is upgraded, by this
// open or by the process it waited for, and compacted, whether or not the
// compaction succeeded (see [Ledger.compact]). An upgrade that fails, or that
// a cancel of the open's context stops, ends without "", and the open
// returns its error. An open of a ledger that needs no upgrade never calls
// it.
type UpgradeSteps func(step string)

// UpgradeWaiting is the step of an open that waits for another process
// holding the ledger's write lock, as another Workbench does while it
// upgrades the ledger (see beginLong). The wait ends with "" when that
// process upgraded the ledger, or with this open's own steps when the ledger
// still needs its upgrade.
const UpgradeWaiting = "waiting for another Workbench to upgrade the costs ledger"

// step tells l.steps, when set, that the upgrade is at step.
func (l *Ledger) step(step string) {
	if l.steps != nil {
		l.steps(step)
	}
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
// session. upgraded is false when another process upgraded the ledger first;
// a version this release cannot use, found there then, fails it as it fails
// the open (see versionProblem).
//
// The upgrade of a large ledger takes seconds, and every other process that
// opens the ledger meanwhile waits for it (see beginLong), then finds it
// upgraded. A cancel of ctx stops the upgrade, whose transaction then rolls
// back, or the wait, and the error says which (see stopped).
func (l *Ledger) upgrade(ctx context.Context) (upgraded bool, err error) {
	conn, err := l.db.Conn(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = conn.Close() }()
	tx, waited, err := l.beginLong(ctx, conn)
	stop := upgradeStopped
	if waited {
		stop = waitStopped
	}
	defer func() { err = stopped(ctx, err, stop) }()
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	version, err := meta(ctx, tx, "schema_version")
	if err != nil {
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
	case schemaVersion:
		return false, nil // another process upgraded it first
	default:
		return false, versionProblem(l.Path, version)
	}
	stop = upgradeStopped
	l.step("converting responses")
	if err := execAll(ctx, tx, statements...); err != nil {
		return false, err
	}
	if fill {
		if err := fillPrices(ctx, tx, l.Path); err != nil {
			return false, err
		}
	}
	if err := slim(ctx, tx, l.Path, l.step); err != nil {
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

// beginLong begins the upgrade's transaction on conn, which takes the write
// lock, and reports whether it waited for another process. Its first BEGIN
// does not wait: while another process holds the lock, as one does while it
// upgrades the ledger, the open learns so at once and tells steps it waits
// (UpgradeWaiting). A connection waits on a lock for its busy timeout, and an
// upgrade holds it longer, so BEGIN is then tried again until upgradeWait has
// passed.
func (l *Ledger) beginLong(ctx context.Context, conn *sql.Conn) (*sql.Tx, bool, error) {
	if _, err := conn.ExecContext(ctx, "PRAGMA busy_timeout = 0"); err != nil {
		return nil, false, err
	}
	tx, err := conn.BeginTx(ctx, nil)
	// The connection waits its busy timeout again from here on, a cancel or
	// not.
	if _, resetErr := conn.ExecContext(
		context.WithoutCancel(ctx),
		"PRAGMA busy_timeout = "+busyTimeout,
	); resetErr != nil {
		if tx != nil {
			_ = tx.Rollback()
		}
		return nil, false, resetErr
	}
	if !sqlitefile.IsBusy(err) {
		return tx, false, err
	}
	l.step(UpgradeWaiting)
	deadline := time.Now().Add(upgradeWait)
	for {
		time.Sleep(10 * time.Millisecond)
		tx, err = conn.BeginTx(ctx, nil)
		if !sqlitefile.IsBusy(err) || time.Now().After(deadline) {
			return tx, true, err
		}
	}
}

// The errors of an open a cancel of its context stopped: during its own
// upgrade, whose transaction rolled back, while it waited for another
// process's (see beginLong), which goes on, or while it compacted the
// upgraded ledger, which the next import compacts (see Ledger.compactAgain).
const (
	upgradeStopped = "Stopped the costs ledger's upgrade: the ledger is unchanged, " +
		"and the next command that opens it upgrades it"
	waitStopped = "Stopped waiting for another Workbench to upgrade the costs ledger: " +
		"that upgrade goes on"
	compactStopped = "Stopped compacting the costs ledger: it is upgraded, " +
		"and the next import compacts it"
)

// stopped is err, or, when err is an error of an upgrade a cancel of ctx
// stopped, an interrupted [operation.Error] saying so, as message.
func stopped(ctx context.Context, err error, message string) error {
	if err == nil || ctx.Err() == nil {
		return err
	}
	return operation.Fail(operation.ExitInterrupted, "interrupted", message)
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
	if due, err := l.Meta(ctx, compactDue); err != nil || due == "" {
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
