package costs

import (
	"cmp"
	"context"
	"database/sql"
	"maps"
	"slices"
	"sort"
	"strings"
	"time"
)

// The ledger keeps two series of tier changes per thread: when a selected
// tier was set (keyed by the thread id), and when it took effect for the
// thread's own turns (keyed turnTierKey(thread)).
const turnTierPrefix = "turn:"

func turnTierKey(thread string) string { return turnTierPrefix + thread }

// TurnTierFrom is the TierFrom of a row priced at thread's running-turn tier
// at time at (ledger layout), such as a subagent's tier fixed when it was
// spawned.
func TurnTierFrom(thread, at string) string { return turnTierKey(thread) + "@" + at }

// TierChange is a thread switching service tier at a time; Tier is the
// stored tier name, "" for standard. Turn marks the time the tier took effect
// for the thread's own turns, rather than when it was selected. From, when
// set, is a TierFrom whose tier the thread takes instead of Tier.
type TierChange struct {
	Thread string
	Time   time.Time
	Tier   string
	Turn   bool
	From   string
}

// splitTier splits "gpt-5.6-sol@fast", an id a Tiers tool's source composed,
// into the model and its service tier:
// the word after the last "@", a lowercase letter then lowercase letters,
// digits, "_" or "-". An id without one is all model, tier "", so a dated
// snapshot such as "claude-sonnet-4@20250514" keeps its date.
func splitTier(model string) (base, tier string) {
	if i := strings.LastIndex(model, "@"); i >= 0 && isTier(model[i+1:]) {
		return model[:i], model[i+1:]
	}
	return model, ""
}

// isTier reports whether name can be a service tier in a model id.
func isTier(name string) bool {
	for i, c := range name {
		switch {
		case c >= 'a' && c <= 'z':
		case i > 0 && (c >= '0' && c <= '9' || c == '_' || c == '-'):
		default:
			return false
		}
	}
	return name != ""
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
	// The ids of the threads' TierFroms are read from names first, so that
	// each waiting row is matched by its root's id alone.
	query += `
 WHERE p.request_id IN (SELECT unhex(value) FROM json_each(?2))
    OR p.root IN (SELECT id FROM names WHERE kind = ?4 AND (CASE WHEN instr(name, '@') > 0
         THEN substr(name, 1, instr(name, '@') - 1) ELSE name END) IN (SELECT value FROM json_each(?3)))`
	return query, []any{
		int64(noTime),
		packedIDs(maps.Keys(run.pending)),
		jsonText(slices.Collect(maps.Keys(run.threads))),
		nameSession,
	}
}
