package costs

import (
	"context"
	"database/sql"
	"maps"
	"slices"
)

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

// newRun is the empty record of an ingest run.
func newRun() *Run {
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
	rank   [2]int   // evidenceRank of the stored account_source and subscription_source
}

// Transaction runs fn and commits, or rolls back when fn fails. run is the
// ingest run's record of committed writes, or nil.
func (l *Ledger) Transaction(ctx context.Context, run *Run, fn func(*Tx) error) error {
	if run == nil {
		run = newRun()
	}
	t := &Tx{ctx: ctx, run: run, wrote: newRun(), labels: map[string]string{}}
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
