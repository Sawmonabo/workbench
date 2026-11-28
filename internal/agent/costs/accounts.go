package costs

import (
	"context"
	"database/sql"
	"encoding/json"
	"slices"
	"sort"
)

type (
	bindingKey  struct{ tool, session string }
	signInPoint struct{ at, account, subscription string }
	// attribution is what a row is attributed to: source is the evidence of
	// both values, "" for no decision. An empty or "unknown" value names
	// nothing and leaves that field of the row as it is. byOrg marks a row
	// whose transcript named its organization (accountBasis.attribute): its
	// email is decided anew from that organization's evidence alone.
	attribution struct {
		account, subscription, source string
		byOrg                         bool
	}
)

// accountBasis is what rows are attributed from: the session bindings and the
// sign-in observations of the ledger, each in time order, and the email that
// holds each subscription.
type accountBasis struct {
	bindings map[bindingKey][]signInPoint
	observed map[string][]signInPoint
	through  map[string]string // each tool's watermark of the last resolution
	// owners maps a subscription to the one email every sign-in to it names:
	// the bindings, the observations and the sign-ins the tools keep on disk.
	// One that two emails held maps to "" (a team's organization, whose members
	// share it), and its rows fall back to a binding or an observation of it.
	owners map[string]string
	// orgTools are the tools whose transcripts name an account (Tool.OrgAccount).
	orgTools map[string]bool
	// known are the subscriptions owners named an email for at the last
	// resolution (accountsOwned).
	known map[string]bool
	// stretches are each tool's login stretches with the organization the
	// account marks infer for them (loadStretches), and marked is where the
	// marks stored since the last resolution begin (accountsMarked).
	stretches map[string]*loginStretches
	marked    string
}

// owned are the subscriptions owners names an email for, sorted.
func (b accountBasis) owned() []string {
	var out []string
	for subscription, email := range b.owners {
		if email != "" {
			out = append(out, subscription)
		}
	}
	slices.Sort(out)
	return out
}

// claim records that a sign-in to subscription named account.
func (b accountBasis) claim(account, subscription string) {
	if account == "" || account == "unknown" || subscription == "" || subscription == "unknown" {
		return
	}
	if prev, seen := b.owners[subscription]; seen && prev != account {
		account = ""
	}
	b.owners[subscription] = account
}

func loadAccountBasis(
	ctx context.Context,
	tx *sql.Tx,
	signIns map[string][]SignIn,
) (accountBasis, error) {
	basis := accountBasis{
		bindings: map[bindingKey][]signInPoint{},
		observed: map[string][]signInPoint{},
		through:  map[string]string{},
		owners:   map[string]string{},
		orgTools: map[string]bool{},
	}
	for _, tool := range Tools {
		basis.orgTools[tool.Name] = tool.OrgAccount
	}
	for _, list := range signIns {
		for _, s := range list {
			basis.claim(s.Account, s.Subscription)
		}
	}
	rows, err := tx.QueryContext(ctx,
		"SELECT tool, session, since, account, subscription FROM session_accounts ORDER BY since")
	if err != nil {
		return basis, err
	}
	for rows.Next() {
		var key bindingKey
		var p signInPoint
		if err := rows.Scan(
			&key.tool,
			&key.session,
			&p.at,
			&p.account,
			&p.subscription,
		); err != nil {
			_ = rows.Close()
			return basis, err
		}
		basis.bindings[key] = append(basis.bindings[key], p)
		basis.claim(p.account, p.subscription)
	}
	if err := rows.Close(); err != nil {
		return basis, err
	}
	if err := rows.Err(); err != nil {
		return basis, err
	}
	rows, err = tx.QueryContext(
		ctx,
		"SELECT tool, at, account, subscription FROM sign_ins ORDER BY at",
	)
	if err != nil {
		return basis, err
	}
	for rows.Next() {
		var tool string
		var p signInPoint
		if err := rows.Scan(&tool, &p.at, &p.account, &p.subscription); err != nil {
			_ = rows.Close()
			return basis, err
		}
		basis.observed[tool] = append(basis.observed[tool], p)
		basis.claim(p.account, p.subscription)
	}
	if err := rows.Close(); err != nil {
		return basis, err
	}
	if err := rows.Err(); err != nil {
		return basis, err
	}
	for tool := range basis.observed {
		if basis.through[tool], err = meta(ctx, tx, accountsThrough+tool); err != nil {
			return basis, err
		}
	}
	if basis.stretches, err = loadStretches(ctx, tx, basis); err != nil {
		return basis, err
	}
	if basis.marked, err = meta(ctx, tx, accountsMarked); err != nil {
		return basis, err
	}
	note, err := meta(ctx, tx, accountsOwned)
	if err != nil {
		return basis, err
	}
	var known []string
	_ = json.Unmarshal([]byte(note), &known) // unreadable: every one is learned now
	basis.known = map[string]bool{}
	for _, subscription := range known {
		basis.known[subscription] = true
	}
	return basis, nil
}

// attribute is what the ledger's evidence says of row r.
//
// A row of a tool whose transcripts name an account (Tool.OrgAccount), whose
// transcript named its organization, is that organization's: its email is the
// one every sign-in to it names (owners), as transcript evidence; else that of
// the evidence below when it is a sign-in to the same organization; else none.
// The sign-in that was current when the row was made or read may be another:
// a /login in a running session moves its later responses to another
// organization without a new binding.
func (b accountBasis) attribute(r accountRow) attribution {
	if r.subscriptionSource != EvidenceTranscript || !b.orgTools[r.tool] {
		want := b.signedIn(r.tool, r.root, r.ts)
		if want.source == EvidenceUnknown {
			if inferred, ok := b.inferred(r.tool, r.ts); ok {
				return inferred
			}
		}
		return want
	}
	if email := b.owners[r.subscription]; email != "" {
		return attribution{email, r.subscription, EvidenceTranscript, true}
	}
	want := b.signedIn(r.tool, r.root, r.ts)
	if want.source != "" && want.subscription != r.subscription {
		want = attribution{subscription: "unknown", source: EvidenceUnknown}
	}
	want.byOrg = true
	return want
}

// signedIn is the sign-in the ledger's evidence puts a row of tool, its root
// session and its time ts under: the binding of its session with the latest
// since at or before ts; else the sign-in observed on both sides of ts when
// the two agree; else unknown, which names no email and no subscription and
// so changes nothing in a row (a row an earlier build stored keeps its email,
// which no report shows). A row before the first observation is unknown. A
// row after the last one has no decision yet (source ""): a later observation
// may decide it.
func (b accountBasis) signedIn(tool, root, ts string) attribution {
	if bound := b.bindings[bindingKey{tool, root}]; root != "" {
		if i := sort.Search(len(bound), func(i int) bool { return bound[i].at > ts }); i > 0 {
			return attribution{bound[i-1].account, bound[i-1].subscription, EvidenceSession, false}
		}
	}
	seen := b.observed[tool]
	i := sort.Search(len(seen), func(i int) bool { return seen[i].at > ts }) // the first after ts
	if i == 0 {
		return attribution{subscription: "unknown", source: EvidenceUnknown}
	}
	before, after := seen[i-1], seen[i-1]
	if before.at < ts {
		if i == len(seen) {
			return attribution{}
		}
		after = seen[i]
	}
	if before.account != after.account || before.subscription != after.subscription ||
		before.account == "unknown" && before.subscription == "unknown" {
		return attribution{subscription: "unknown", source: EvidenceUnknown}
	}
	return attribution{before.account, before.subscription, EvidenceObserved, false}
}

// accountRow is a response as the account resolution reads it: its id, and
// each field as its text, the time in timeLayout.
type accountRow struct {
	id                                                                       int64
	tool, ts, root, account, accountSource, subscription, subscriptionSource string
}

// update is the change that takes a row to what the evidence says, ok false
// when nothing changes. The email and the subscription are raised each on its
// own evidence, since a transcript can decide the plan and leave the email to a
// binding or an observation: a field takes the new value when its evidence is
// at least as strong as the stored one, never weaker, and a field decided by
// its transcript never changes. A value that is "unknown" is no evidence and
// leaves the field as it is, so a row keeps an email an earlier build stored
// until something names a real one.
//
// A row attributed by its organization (byOrg) is the exception: its
// organization is final, and its email takes the organization's evidence
// whatever the stored one rests on, which loses its standing when there is none
// (the text stays, and no report shows it) because it may be another
// organization's. An inferred field is the other: when the marks no longer
// infer anything for the row (a sign-in found later split its stretch, say),
// it goes back to unknown.
func (r accountRow) update(want attribution) (next accountRow, ok bool) {
	next = r
	rank := evidenceRank(want.source)
	named := want.account != "" && want.account != "unknown"
	if want.source == EvidenceUnknown && !want.byOrg {
		if r.accountSource == EvidenceInferred {
			next.account, next.accountSource = "unknown", EvidenceUnknown
		}
		if r.subscriptionSource == EvidenceInferred {
			next.subscription, next.subscriptionSource = "unknown", EvidenceUnknown
		}
	}
	switch {
	case want.byOrg && want.source != "":
		next.accountSource = EvidenceUnknown
		if named && rank > 0 {
			next.account, next.accountSource = want.account, want.source
		}
		return next, next != r
	case want.byOrg:
		return r, false
	}
	if rank > 0 && rank >= evidenceRank(r.accountSource) && named {
		next.account, next.accountSource = want.account, want.source
	}
	if rank > 0 && rank >= evidenceRank(r.subscriptionSource) &&
		want.subscription != "" && want.subscription != "unknown" {
		next.subscription, next.subscriptionSource = want.subscription, want.source
	}
	return next, next != r
}
