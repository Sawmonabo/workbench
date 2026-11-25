package costs

import (
	"cmp"
	"context"
	"database/sql"
	"errors"
	"time"
)

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
