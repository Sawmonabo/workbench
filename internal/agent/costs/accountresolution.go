package costs

import (
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"maps"
	"slices"
	"strings"
)

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

// markAccountsUnresolved adds sessions to accountsUnresolved.
func markAccountsUnresolved(ctx context.Context, tx *sql.Tx, keys ...string) error {
	note, err := meta(ctx, tx, accountsUnresolved)
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
	checked, err := meta(ctx, tx, accountsChecked)
	if err != nil {
		return nil, false, err
	}
	note, err := meta(ctx, tx, accountsUnresolved)
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
