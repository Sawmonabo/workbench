package costs

import (
	"cmp"
	"context"
	"database/sql"
	"slices"
	"strings"
)

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
