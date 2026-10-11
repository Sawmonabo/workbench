package costs

import (
	"context"
	"database/sql"
	"sort"
)

// AddMark stores an account mark of tool, once. A mark not stored before
// moves accountsMarked back to where its rows begin, so the next resolution
// infers them again: to the time of the mark, or, for a pair, to the start
// (dirtyRootsAll).
func (t *Tx) AddMark(tool string, m AccountMark) error {
	at := ""
	if !m.Time.IsZero() {
		at = m.Time.UTC().Format(timeLayout)
	}
	switch {
	case m.Kind == MarkPair && (m.Account == "" || m.Org == ""),
		m.Kind != MarkPair && at == "",
		m.Kind == MarkAccount && m.Account == "",
		m.Kind == MarkOrg && m.Org == "":
		return nil
	case m.Kind == MarkPair:
		at = ""
	}
	result, err := t.tx.ExecContext(t.ctx,
		`INSERT OR IGNORE INTO account_marks (tool, kind, at, account, org) VALUES (?, ?, ?, ?, ?)`,
		tool, m.Kind, at, m.Account, m.Org)
	if err != nil {
		return err
	}
	if n, err := result.RowsAffected(); err != nil || n == 0 {
		return err
	}
	note, err := metaIn(t.ctx, t.tx, accountsMarked)
	switch {
	case err != nil:
		return err
	case note == dirtyRootsAll, note != "" && at != "" && note <= at:
		return nil
	case at == "":
		at = dirtyRootsAll
	}
	return setMeta(t.ctx, t.tx, accountsMarked, at)
}

// loginStretches are a tool's sign-ins in time order and the organization
// inferred for the stretches between them: stretch i (i ≥ 1) runs from
// logins[i-1] up to logins[i], the last one on without end. Stretch 0, before
// the first sign-in on record, is never inferred, as nothing bounds it.
type loginStretches struct {
	logins []string
	orgs   map[int]string
}

// stretch is the index of the stretch time ts falls in.
func (s *loginStretches) stretch(ts string) int {
	return sort.Search(len(s.logins), func(i int) bool { return s.logins[i] > ts })
}

// start is where the stretch that held ts began before a sign-in at ts was
// added: rows from there on may be inferred anew. "" is the beginning.
func (s *loginStretches) start(ts string) string {
	if i := sort.SearchStrings(s.logins, ts); i > 0 {
		return s.logins[i-1]
	}
	return ""
}

// inferred is the account the marks put row r's time under, for a row of a
// tool whose transcripts name an account (Tool.OrgAccount) that no binding,
// observation or transcript names: the stretch's organization and its email
// (owners), or none.
func (b accountBasis) inferred(tool, ts string) (attribution, bool) {
	stretches := b.stretches[tool]
	if stretches == nil || ts == "" {
		return attribution{}, false
	}
	org, ok := stretches.orgs[stretches.stretch(ts)]
	if !ok {
		return attribution{}, false
	}
	subscription := tool + ":" + org
	return attribution{
		account: b.owners[subscription], subscription: subscription, source: EvidenceInferred,
	}, true
}

// valueSets maps each key to a set of values.
type valueSets[K comparable] map[K]map[string]bool

func (v valueSets[K]) add(key K, value string) {
	if v[key] == nil {
		v[key] = map[string]bool{}
	}
	v[key][value] = true
}

// only is the one value of set, "" when it holds none or several.
func only(set map[string]bool) string {
	if len(set) != 1 {
		return ""
	}
	for value := range set {
		return value
	}
	return ""
}

// timedMark is an account or organization mark as the ledger stores it.
type timedMark struct{ kind, at, account, org string }

// toolMarks are one tool's marks: its sign-ins in time order (no repeats),
// its account and organization marks, and the organizations its pairs give
// each account id.
type toolMarks struct {
	logins []string
	marks  []timedMark
	pairs  valueSets[string]
}

// loadStretches infers, for each tool whose transcripts name an account, the
// organization of each login stretch from the ledger's marks. A sign-in is
// shared by every session of the tool, so between two sign-ins one account
// answered them all.
func loadStretches(
	ctx context.Context,
	tx *sql.Tx,
	basis accountBasis,
) (map[string]*loginStretches, error) {
	byTool, err := loadMarks(ctx, tx, basis.orgTools)
	if err != nil {
		return nil, err
	}
	out := map[string]*loginStretches{}
	for tool, t := range byTool {
		out[tool] = t.infer(tool, basis)
	}
	return out, nil
}

// loadMarks reads the marks of the tools whose transcripts name an account.
func loadMarks(
	ctx context.Context,
	tx *sql.Tx,
	orgTools map[string]bool,
) (map[string]*toolMarks, error) {
	rows, err := tx.QueryContext(ctx,
		"SELECT tool, kind, at, account, org FROM account_marks ORDER BY tool, at")
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	byTool := map[string]*toolMarks{}
	for rows.Next() {
		var tool string
		var m timedMark
		if err := rows.Scan(&tool, &m.kind, &m.at, &m.account, &m.org); err != nil {
			return nil, err
		}
		if !orgTools[tool] {
			continue
		}
		t := byTool[tool]
		if t == nil {
			t = &toolMarks{pairs: valueSets[string]{}}
			byTool[tool] = t
		}
		switch m.kind {
		case MarkLogin:
			if n := len(t.logins); n == 0 || t.logins[n-1] != m.at {
				t.logins = append(t.logins, m.at)
			}
		case MarkPair:
			t.pairs.add(m.account, m.org)
		case MarkAccount, MarkOrg:
			t.marks = append(t.marks, m)
		}
	}
	return byTool, rows.Err()
}

// infer is the organization of each of tool's login stretches. An account id
// names an organization when a sign-in on disk pairs the two (MarkPair) or a
// stretch holds that id and that organization alone, and nothing pairs it with
// another. A stretch takes an organization when every account id seen during
// it names that one, and no organization mark, sign-in observation or session
// binding during it names another; a stretch with no account id, or an id that
// names no organization, is not inferred.
func (t *toolMarks) infer(tool string, basis accountBasis) *loginStretches {
	stretches := &loginStretches{logins: t.logins, orgs: map[int]string{}}
	accounts, orgs := valueSets[int]{}, valueSets[int]{}
	for _, m := range t.marks {
		switch s := stretches.stretch(m.at); {
		case s == 0:
		case m.kind == MarkAccount:
			accounts.add(s, m.account)
		default:
			orgs.add(s, m.org)
		}
	}
	names := t.pairs
	for s, ids := range accounts {
		if id, org := only(ids), only(orgs[s]); id != "" && org != "" {
			names.add(id, org)
		}
	}
	signedIn := signedInOrgs(tool, stretches, basis)
	for s, ids := range accounts {
		if org := named(ids, names); org != "" && agree(org, orgs[s], signedIn[s]) {
			stretches.orgs[s] = org
		}
	}
	return stretches
}

// signedInOrgs are the organizations the ledger's sign-in observations and
// session bindings of tool name during each stretch.
func signedInOrgs(tool string, stretches *loginStretches, basis accountBasis) valueSets[int] {
	out := valueSets[int]{}
	note := func(p signInPoint) {
		if org, ok := cutTool(tool, p.subscription); ok {
			if s := stretches.stretch(p.at); s > 0 {
				out.add(s, org)
			}
		}
	}
	for _, p := range basis.observed[tool] {
		note(p)
	}
	for key, points := range basis.bindings {
		if key.tool == tool {
			for _, p := range points {
				note(p)
			}
		}
	}
	return out
}

// named is the one organization every id names, "" when one names none or
// several, or two name different ones.
func named(ids map[string]bool, names valueSets[string]) string {
	orgs := map[string]bool{}
	for id := range ids {
		org := only(names[id])
		if org == "" {
			return ""
		}
		orgs[org] = true
	}
	return only(orgs)
}

// agree reports whether every organization in the sets is org.
func agree(org string, sets ...map[string]bool) bool {
	for _, set := range sets {
		for other := range set {
			if other != org {
				return false
			}
		}
	}
	return true
}

// cutTool is the organization of a subscription id tool's sign-ins name,
// "<tool>:<organization>".
func cutTool(tool, subscription string) (string, bool) {
	if len(subscription) <= len(tool)+1 || subscription[:len(tool)+1] != tool+":" {
		return "", false
	}
	return subscription[len(tool)+1:], true
}
