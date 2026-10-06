package costs

import (
	"cmp"
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Sawmonabo/workbench/internal/operation"
)

// Tokens are summed token counts.
type Tokens struct {
	Input        int64 `json:"input"`
	Output       int64 `json:"output"`
	CacheWrite5m int64 `json:"cache_write_5m"`
	CacheWrite1h int64 `json:"cache_write_1h"`
	CacheRead    int64 `json:"cache_read"`
}

func (t *Tokens) add(o Tokens) {
	t.Input += o.Input
	t.Output += o.Output
	t.CacheWrite5m += o.CacheWrite5m
	t.CacheWrite1h += o.CacheWrite1h
	t.CacheRead += o.CacheRead
}

// Total is all five kinds of token.
func (t Tokens) Total() int64 {
	return t.Input + t.Output + t.CacheWrite5m + t.CacheWrite1h + t.CacheRead
}

// CachedShare is the share of prompt tokens served from the cache instead of
// re-read; ok is false when there were no prompt tokens.
func (t Tokens) CachedShare() (share float64, ok bool) {
	prompt := t.Input + t.CacheWrite5m + t.CacheWrite1h + t.CacheRead
	if prompt == 0 {
		return 0, false
	}
	return float64(t.CacheRead) / float64(prompt), true
}

// Row is one line of a report table.
type Row struct {
	Name   string  `json:"name"`
	Calls  int64   `json:"calls"`
	Cost   float64 `json:"cost"`
	Priced bool    `json:"priced"` // false when a model in the row has no rate; its cost counts as 0
	// Subscription and SubscriptionLabel are set on an account row only: the
	// subscription id ("unknown" when none) and what the report shows for it.
	// Name is then the email, and one email has a row per subscription.
	Subscription      string `json:"subscription,omitempty"`
	SubscriptionLabel string `json:"subscription_label,omitempty"`
	Tokens
}

// unknownSubscription is what the report shows for rows no evidence ties to a
// subscription.
const unknownSubscription = "unknown subscription"

// subscriptionLabel is the label the report shows for a subscription id whose
// subscriptions-table label is label: the label, else the id, else "unknown
// subscription" for "unknown" or none.
func subscriptionLabel(id, label string) string {
	switch {
	case id == "" || id == "unknown":
		return unknownSubscription
	case label != "":
		return label
	}
	return id
}

// AccountName writes an account as the report shows it, "you@example.com · Max":
// the email, a middle dot, the subscription's label. When neither the account
// nor the subscription is known it is just "unknown".
func AccountName(account, label string) string {
	if account == "" {
		account = "unknown"
	}
	if account == "unknown" && label == unknownSubscription {
		return account
	}
	return account + " · " + label
}

// Display is how the report names the row: an account row as AccountName,
// saying which part its tool's transcripts did not record ("you@example.com ·
// plan not recorded", "account not recorded · Pro", "not recorded"); any
// other by its Name.
func (r Row) Display() string {
	if r.Subscription == "" {
		return r.Name
	}
	account, label := r.Name, r.SubscriptionLabel
	noAccount := account == "" || account == "unknown"
	noPlan := label == unknownSubscription
	switch {
	case noAccount && noPlan:
		return "not recorded"
	case noAccount:
		account = "account not recorded"
	case noPlan:
		label = "plan not recorded"
	}
	return AccountName(account, label)
}

// Block is one project of a --detail report with its own model rows.
type Block struct {
	Project Row   `json:"project"`
	Models  []Row `json:"models"`
}

// Coverage is what the ledger holds for the tool, and when it last ingested.
type Coverage struct {
	First                string   `json:"first"` // YYYY-MM-DD in the machine's time zone, or "none"
	Last                 string   `json:"last"`
	Responses            int64    `json:"responses"`
	AccountEvidence      Evidence `json:"account_evidence"`      // rows by the evidence of their email
	SubscriptionEvidence Evidence `json:"subscription_evidence"` // and of their subscription
	// AccountNamedSince and SubscriptionNamedSince are the time of the first
	// row whose transcript named its email, and its subscription (ledger
	// layout, "" when none did): what the tool's transcripts record starts
	// there.
	AccountNamedSince      string `json:"account_named_since"`
	SubscriptionNamedSince string `json:"subscription_named_since"`
	LastIngestAt           string `json:"last_ingest_at"`
	LastIngestSummary      string `json:"last_ingest_summary"`
	LastError              string `json:"last_error"`
	RatesFetched           string `json:"rates_fetched"` // YYYY-MM-DD in the machine's time zone, or "never"
}

// Evidence counts rows by the level of evidence one field of them rests on.
type Evidence struct {
	Transcript int64 `json:"transcript"`
	Session    int64 `json:"session"`
	Observed   int64 `json:"observed"`
	Unknown    int64 `json:"unknown"`
}

// add counts n rows at a level; any other level counts as unknown.
func (e *Evidence) add(level string, n int64) {
	switch level {
	case EvidenceTranscript:
		e.Transcript += n
	case EvidenceSession:
		e.Session += n
	case EvidenceObserved:
		e.Observed += n
	default:
		e.Unknown += n
	}
}

// ReportOptions selects what a report covers; the zero value is the default
// view (by project, in scope, cost descending).
type ReportOptions struct {
	Tool         string // the ledger's tool column
	By           string // project (default), model, account or month
	Since, Until string // inclusive YYYY-MM-DD bounds on the response's day in the machine's time zone
	All          bool   // include projects outside the project folders (Statement.Scope)
	Top          int    // show only the first N rows; totals still cover all
	Sort         string // cost (default), name or calls
	Detail       bool   // project view: a model table per project
	NoRollup     bool   // keep every working directory separate
	Tokens       bool   // how the report is shown: the five token columns, not share, tokens and cached
}

// Statement is one tool's rows, totals and coverage, as [Report] returns them.
// (The type cannot share its function's name.)
type Statement struct {
	Options  ReportOptions `json:"-"` // what was asked; the report is laid out from it
	Tool     string        `json:"tool"`
	By       string        `json:"by"`
	Coverage Coverage      `json:"coverage"` // everything the ledger holds for the tool
	// First and Last are the first and last day (YYYY-MM-DD, the machine's time
	// zone) with a response in the rows the report covers: the period and the
	// scope the total beside them is for. "none" when there are none.
	First      string     `json:"first"`
	Last       string     `json:"last"`
	Rows       []Row      `json:"rows"` // the shown rows
	RowCount   int        `json:"row_count"`
	GrandTotal float64    `json:"grand_total"`
	Total      Row        `json:"total"`
	Models     []Row      `json:"models"`
	Accounts   []Row      `json:"accounts"`
	Detail     []Block    `json:"detail,omitempty"`
	Scope      []string   `json:"scope"`           // the project folders the default view covers
	Hidden     int        `json:"hidden_projects"` // projects the default scope left out
	Unpriced   []Unpriced `json:"unpriced"`        // models without a rate, and why
	Overrides  string     `json:"overrides"`       // the file a rate for them goes in
	Sources    []string   `json:"rate_sources"`
}

// Empty reports that nothing matched.
func (r Statement) Empty() bool { return r.RowCount == 0 }

type group struct {
	project, model, account, subscription, label, month string
	// first and last are the earliest and latest dated response time (ledger
	// layout) of the group, "" for none.
	first, last string
	Row
}

// Report reads the ledger only and prints nothing: the rows of one tool by project, model,
// account or month, priced from the current rate card.
func Report(ctx context.Context, ledger *Ledger, opts ReportOptions) (Statement, error) {
	paths, err := Locations()
	if err != nil {
		return Statement{}, err
	}
	card, err := loadCard(paths)
	if err != nil {
		return Statement{}, err
	}
	if opts.By == "" {
		opts.By = "project"
	}
	report := Statement{
		Options: opts, Tool: opts.Tool, By: opts.By, Sources: card.Sources(),
		Overrides: paths.Overrides, First: "none", Last: "none",
	}
	tool, _ := Lookup(opts.Tool)
	groups, hidden, tally, err := loadGroups(ctx, ledger, card, paths.Home, opts)
	if err != nil {
		return report, err
	}
	report.Scope, report.Hidden = scopeRoots(paths.Home), hidden
	if report.Coverage, err = tally.coverage(ledger, card.Fetched[opts.Tool]); err != nil {
		return report, err
	}
	seen := map[string]bool{}
	for _, g := range groups {
		if !g.Priced && !seen[g.model] {
			seen[g.model] = true
			report.Unpriced = append(report.Unpriced, card.unpriced(tool, g.model))
		}
	}
	sort.Slice(report.Unpriced, func(i, j int) bool {
		return report.Unpriced[i].Model < report.Unpriced[j].Model
	})
	if len(groups) == 0 {
		return report, nil
	}
	report.First, report.Last = span(groups)
	rows := sortRows(aggregate(groups, opts.By), opts.Sort)
	report.RowCount = len(rows)
	report.Rows = rows
	if opts.Top > 0 && opts.Top < len(rows) {
		report.Rows = rows[:opts.Top]
	}
	report.Total = total(rows)
	report.GrandTotal = report.Total.Cost
	report.Models = sortRows(aggregate(groups, "model"), opts.Sort)
	report.Accounts = sortRows(aggregate(groups, "account"), opts.Sort)
	if opts.By == "project" && opts.Detail {
		for _, project := range report.Rows {
			var mine []group
			for _, g := range groups {
				if g.project == project.Name {
					mine = append(mine, g)
				}
			}
			report.Detail = append(
				report.Detail,
				Block{project, sortRows(aggregate(mine, "model"), opts.Sort)},
			)
		}
	}
	return report, nil
}

// span is the first and last local day with a dated response among groups,
// "none" for a bound no group has.
func span(groups []group) (first, last string) {
	var lo, hi string
	for _, g := range groups {
		if g.first != "" && (lo == "" || g.first < lo) {
			lo = g.first
		}
		hi = max(hi, g.last)
	}
	return LocalDate(lo), LocalDate(hi)
}

// rollup folds a session's working directory into its repository: the first
// directory below a scope root, else the path before `/.worktrees/`.
func rollup(project, home string) string {
	clean := filepath.Clean(project)
	for _, root := range projectRoots(home) {
		if rel, ok := strings.CutPrefix(clean, root+string(filepath.Separator)); ok {
			first, _, _ := strings.Cut(rel, string(filepath.Separator))
			return filepath.Join(root, first)
		}
	}
	before, _, _ := strings.Cut(project, "/.worktrees/")
	return before
}

// projectRoots are the project folders: ~/dev for personal projects and
// ~/repos for work ones, the folders the machine roles use.
func projectRoots(home string) []string {
	return []string{filepath.Join(home, "dev"), filepath.Join(home, "repos")}
}

// scopeRoots are the project folders that exist on this machine, which the
// default view covers and the report names; both when neither exists.
func scopeRoots(home string) []string {
	var roots []string
	for _, root := range projectRoots(home) {
		if info, err := os.Stat(root); err == nil && info.IsDir() {
			roots = append(roots, root)
		}
	}
	if len(roots) == 0 {
		return projectRoots(home)
	}
	return roots
}

func inScope(project string, roots []string) bool {
	clean := filepath.Clean(project)
	for _, root := range roots {
		if clean == root || strings.HasPrefix(clean, root+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

// loadGroups is one group per (project, model, account, subscription, month)
// with tokens, calls and cost, plus how many projects the default scope left
// out and the tool's coverage tally, all from one pass over its responses.
func loadGroups(
	ctx context.Context,
	ledger *Ledger,
	card Card,
	home string,
	opts ReportOptions,
) ([]group, int, coverageTally, error) {
	tool, _ := Lookup(opts.Tool)
	var tally coverageTally
	inPeriod, err := periodFilter(opts.Since, opts.Until)
	if err != nil {
		return nil, 0, tally, err
	}
	labels, err := ledger.labels(ctx)
	if err != nil {
		return nil, 0, tally, err
	}
	type key struct{ project, model, account, subscription, month string }
	byKey := map[key]*group{}
	err = scanResponses(ctx, ledger, opts.Tool, func(r response) {
		tally.add(r)
		if !inPeriod(r.ts) {
			return
		}
		k := key{r.project, r.model, r.account, r.subscription, localMonth(r.ts)}
		g := byKey[k]
		if g == nil {
			g = &group{
				project: k.project, model: k.model, account: k.account,
				subscription: k.subscription, label: labels[k.subscription], month: k.month,
			}
			byKey[k] = g
		}
		g.Calls++
		g.add(r.Tokens)
		if r.ts != "" && (g.first == "" || r.ts < g.first) {
			g.first = r.ts
		}
		g.last = max(g.last, r.ts)
	})
	if err != nil {
		return nil, 0, tally, err
	}
	sorted := make([]*group, 0, len(byKey))
	for _, g := range byKey {
		sorted = append(sorted, g)
	}
	// The order a GROUP BY gave, so rows of equal cost keep their order.
	sort.Slice(sorted, func(i, j int) bool {
		a, b := sorted[i], sorted[j]
		return cmp.Or(
			cmp.Compare(a.project, b.project), cmp.Compare(a.model, b.model),
			cmp.Compare(a.account, b.account), cmp.Compare(a.subscription, b.subscription),
			cmp.Compare(a.month, b.month),
		) < 0
	})
	var groups []group
	hidden, roots := map[string]bool{}, scopeRoots(home)
	for _, g := range sorted {
		if !opts.NoRollup {
			g.project = rollup(g.project, home)
		}
		if !opts.All && !inScope(g.project, roots) {
			hidden[g.project] = true
			continue
		}
		if g.month == "" {
			g.month = "unknown"
		}
		rate, priced := card.Rows.Lookup(g.model, tool.Tiers)
		g.label = subscriptionLabel(g.subscription, g.label)
		g.Priced = priced
		if priced {
			g.Cost = rate.Cost(g.Tokens)
		}
		groups = append(groups, *g)
	}
	return groups, len(hidden), tally, nil
}

// response is one ledger row as a report reads it.
type response struct {
	project, model, account, subscription, ts, session string
	accountSource, subscriptionSource                  string
	Tokens
}

// scanResponses calls visit with every response of tool, in no set order. A
// report reads them once and sums them here: grouping in SQL sorted every row
// by its text columns and worked out each local month with the 'localtime'
// modifier, which took several times as long as reading the rows.
func scanResponses(ctx context.Context, ledger *Ledger, tool string, visit func(response)) error {
	rows, err := ledger.db.QueryContext(ctx,
		`SELECT project, model, account, subscription, ts, session_id, account_source,
		        subscription_source, input, output, cache_write_5m, cache_write_1h, cache_read
		   FROM responses WHERE tool = ?`, tool)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var r response
		if err := rows.Scan(
			&r.project, &r.model, &r.account, &r.subscription, &r.ts, &r.session,
			&r.accountSource, &r.subscriptionSource,
			&r.Input, &r.Output, &r.CacheWrite5m, &r.CacheWrite1h, &r.CacheRead,
		); err != nil {
			return err
		}
		visit(r)
	}
	return rows.Err()
}

func (g group) key(by string) string {
	switch by {
	case "model":
		return g.model
	case "account":
		return g.account + "\x00" + g.subscription // one row per email and subscription
	case "month":
		return g.month
	}
	return g.project
}

// aggregate sums groups by one dimension, in first-seen order.
func aggregate(groups []group, by string) []Row {
	var rows []Row
	index := map[string]int{}
	for _, g := range groups {
		key := g.key(by)
		i, ok := index[key]
		if !ok {
			i = len(rows)
			index[key] = i
			row := Row{Name: key, Priced: true}
			if by == "account" {
				row.Name = g.account
				row.Subscription, row.SubscriptionLabel = g.subscription, g.label
			}
			rows = append(rows, row)
		}
		rows[i].Calls += g.Calls
		rows[i].Cost += g.Cost
		rows[i].Priced = rows[i].Priced && g.Priced
		rows[i].add(g.Tokens)
	}
	return rows
}

func sortRows(rows []Row, by string) []Row {
	less := map[string]func(a, b Row) bool{
		"name":  func(a, b Row) bool { return a.Display() < b.Display() },
		"calls": func(a, b Row) bool { return a.Calls > b.Calls },
	}[by]
	if less == nil {
		less = func(a, b Row) bool { return a.Cost > b.Cost }
	}
	sort.SliceStable(rows, func(i, j int) bool { return less(rows[i], rows[j]) })
	return rows
}

func total(rows []Row) Row {
	t := Row{Priced: true}
	for _, r := range rows {
		t.Calls += r.Calls
		t.Cost += r.Cost
		t.Priced = t.Priced && r.Priced
		t.add(r.Tokens)
	}
	return t
}

// coverageTally is what a pass over a tool's responses learns for its
// Coverage.
type coverageTally struct {
	responses   int64
	first, last string // the earliest dated and the latest response time
	// accountSince and subscriptionSince are the earliest time of a row whose
	// transcript named that, once named is set.
	accountSince, subscriptionSince string
	accountNamed, subscriptionNamed bool
	account, subscription           Evidence
}

func (t *coverageTally) add(r response) {
	t.responses++
	if r.ts != "" && (t.first == "" || r.ts < t.first) {
		t.first = r.ts
	}
	t.last = max(t.last, r.ts)
	if r.accountSource == EvidenceTranscript && (!t.accountNamed || r.ts < t.accountSince) {
		t.accountSince, t.accountNamed = r.ts, true
	}
	if r.subscriptionSource == EvidenceTranscript &&
		(!t.subscriptionNamed || r.ts < t.subscriptionSince) {
		t.subscriptionSince, t.subscriptionNamed = r.ts, true
	}
	t.account.add(r.accountSource, 1)
	t.subscription.add(r.subscriptionSource, 1)
}

// coverage reads every response of tool for its Coverage.
func coverage(
	ctx context.Context,
	ledger *Ledger,
	fetched time.Time,
	tool string,
) (Coverage, error) {
	var tally coverageTally
	if err := scanResponses(ctx, ledger, tool, tally.add); err != nil {
		return Coverage{}, err
	}
	return tally.coverage(ledger, fetched)
}

// coverage is the tally's Coverage, with the ledger's last ingest and the
// time the official rates were fetched.
func (t coverageTally) coverage(ledger *Ledger, fetched time.Time) (Coverage, error) {
	c := Coverage{
		First: LocalDate(t.first), Last: LocalDate(t.last), Responses: t.responses,
		AccountEvidence: t.account, SubscriptionEvidence: t.subscription,
		AccountNamedSince: t.accountSince, SubscriptionNamedSince: t.subscriptionSince,
	}
	c.LastIngestAt, c.LastIngestSummary, c.LastError = "never", "", ""
	if v, err := ledger.Meta("last_ingest_at"); err == nil && v != "" {
		c.LastIngestAt = v
	}
	c.LastIngestSummary, _ = ledger.Meta("last_ingest_summary")
	c.LastError, _ = ledger.Meta("last_error")
	c.RatesFetched = "never"
	if !fetched.IsZero() {
		c.RatesFetched = fetched.Local().Format(time.DateOnly)
	}
	return c, nil
}

// StatusInfo is what `workbench costs status` shows.
type StatusInfo struct {
	Ledger            string       `json:"ledger"`
	LedgerBytes       int64        `json:"ledger_bytes"`
	LastIngestAt      string       `json:"last_ingest_at"`
	LastIngestSummary string       `json:"last_ingest_summary"`
	LastError         string       `json:"last_error"`
	WorkerRunning     bool         `json:"worker_running"` // a worker holds the lock
	Overrides         string       `json:"overrides"`      // path of the manual rates file
	HasOverrides      bool         `json:"has_overrides"`
	Tools             []ToolStatus `json:"tools"`
}

// SignInStatus is who a tool is signed in as now. Account is "unknown" when
// nobody is; Subscription is its id ("unknown" when none) and Label what the
// report shows for it.
type SignInStatus struct {
	Account      string `json:"account"`
	Subscription string `json:"subscription"`
	Label        string `json:"subscription_label"`
}

// Display writes the sign-in as the report names an account, "you@example.com · Max".
func (s SignInStatus) Display() string { return AccountName(s.Account, s.Label) }

// ToolStatus is one tool's coverage, sign-in and hooks.
type ToolStatus struct {
	Name        string            `json:"name"`
	Title       string            `json:"title"`
	Implemented bool              `json:"implemented"`
	Coverage    Coverage          `json:"coverage"`
	SignIn      SignInStatus      `json:"sign_in"`
	Hooks       map[string]string `json:"hooks,omitempty"`         // event → a Hook* state
	Skipped     int               `json:"skipped_files,omitempty"` // transcripts the source cannot read (compressed)
}

// Status reads the ledger (creating an empty one when none exists) and the live
// hook settings; it ingests and fetches nothing itself.
func Status(ctx context.Context) (StatusInfo, error) {
	paths, err := Locations()
	if err != nil {
		return StatusInfo{}, err
	}
	ledger, err := OpenLedger(paths.Ledger, true)
	if err != nil {
		return StatusInfo{}, err
	}
	defer func() { _ = ledger.Close() }()
	info := StatusInfo{Ledger: paths.Ledger, Overrides: paths.Overrides}
	if stat, err := os.Stat(paths.Ledger); err == nil {
		info.LedgerBytes = stat.Size()
	}
	if _, err := os.Stat(paths.Overrides); err == nil {
		info.HasOverrides = true
	}
	info.LastIngestAt = "never"
	if v, _ := ledger.Meta("last_ingest_at"); v != "" {
		info.LastIngestAt = v
	}
	info.LastIngestSummary, _ = ledger.Meta("last_ingest_summary")
	info.LastError, _ = ledger.Meta("last_error")
	if release, held, err := operation.TryLock(paths.lock()); err == nil {
		if held {
			info.WorkerRunning = true
		} else {
			release()
		}
	}
	for _, tool := range Tools {
		status := ToolStatus{Name: tool.Name, Title: tool.Title, Implemented: tool.Source != nil}
		if tool.Source != nil {
			var fetched time.Time
			if f := readOfficial(paths, tool).FetchedAt; f > 0 {
				fetched = time.Unix(int64(f), 0)
			}
			if status.Coverage, err = coverage(ctx, ledger, fetched, tool.Name); err != nil {
				return info, err
			}
			signIn := tool.Source.SignIn(paths.Home)
			status.SignIn = SignInStatus{
				Account:      cmp.Or(signIn.Account, "unknown"),
				Subscription: cmp.Or(signIn.Subscription, "unknown"),
				Label:        subscriptionLabel(signIn.Subscription, signIn.Label),
			}
			status.Hooks = tool.Source.Hooks(paths.Home)
			if counter, ok := tool.Source.(interface{ Skipped(string) int }); ok {
				status.Skipped = counter.Skipped(paths.Home)
			}
		}
		info.Tools = append(info.Tools, status)
	}
	return info, nil
}
