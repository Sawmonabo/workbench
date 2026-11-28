package costs

import (
	"cmp"
	"context"
	"database/sql"
	"slices"
	"sort"
	"strings"
)

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
			if from.emailNamed && r.accountRank < evidenceRank(EvidenceTranscript) {
				account, accountSource = from.account, evidenceRank(EvidenceTranscript)
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
