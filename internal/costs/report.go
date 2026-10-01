package costs

import (
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
	Tokens
}

// Block is one project of a --detail report with its own model rows.
type Block struct {
	Project Row   `json:"project"`
	Models  []Row `json:"models"`
}

// Coverage is what the ledger holds for the tool, and when it last ingested.
type Coverage struct {
	First             string `json:"first"` // YYYY-MM-DD, or "none"
	Last              string `json:"last"`
	Responses         int64  `json:"responses"`
	SessionRows       int64  `json:"session_rows"`
	SweepRows         int64  `json:"sweep_rows"`
	LastIngestAt      string `json:"last_ingest_at"`
	LastIngestSummary string `json:"last_ingest_summary"`
	LastError         string `json:"last_error"`
	RatesFetched      string `json:"rates_fetched"` // YYYY-MM-DD, or "never"
}

// ReportOptions selects what a report covers; the zero value is the default
// view (by project, in scope, cost descending).
type ReportOptions struct {
	Tool         string // the ledger's tool column
	By           string // project (default), model, account or month
	Since, Until string // inclusive YYYY-MM-DD bounds on the response time
	All          bool   // include projects outside ~/dev and ~/repos
	Top          int    // show only the first N rows; totals still cover all
	Sort         string // cost (default), name or calls
	Detail       bool   // project view: a model table per project
	NoRollup     bool   // keep every working directory separate
	Tokens       bool   // how the report is shown: the five token columns, not share, tokens and cached
}

// Statement is one tool's rows, totals and coverage, as [Report] returns them.
// (The type cannot share its function's name.)
type Statement struct {
	Options    ReportOptions `json:"-"` // what was asked; the report is laid out from it
	Tool       string        `json:"tool"`
	By         string        `json:"by"`
	Coverage   Coverage      `json:"coverage"`
	Rows       []Row         `json:"rows"` // the shown rows
	RowCount   int           `json:"row_count"`
	GrandTotal float64       `json:"grand_total"`
	Total      Row           `json:"total"`
	Models     []Row         `json:"models"`
	Accounts   []Row         `json:"accounts"`
	Detail     []Block       `json:"detail,omitempty"`
	Hidden     int           `json:"hidden_projects"` // projects the default scope left out
	Unpriced   []Unpriced    `json:"unpriced"`        // models without a rate, and why
	Overrides  string        `json:"overrides"`       // the file a rate for them goes in
	Sources    []string      `json:"rate_sources"`
}

// Empty reports that nothing matched.
func (r Statement) Empty() bool { return r.RowCount == 0 }

type group struct {
	project, model, account, month string
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
		Overrides: paths.Overrides,
	}
	tool, _ := Lookup(opts.Tool)
	groups, hidden, err := loadGroups(ctx, ledger, card, paths.Home, opts)
	if err != nil {
		return report, err
	}
	report.Hidden = hidden
	if report.Coverage, err = coverage(ledger, card.Fetched[opts.Tool], opts.Tool); err != nil {
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

// rollup folds a session's working directory into its repository: the first
// directory below a scope root, else the path before `/.worktrees/`.
func rollup(project, home string) string {
	clean := filepath.Clean(project)
	for _, root := range scopeRoots(home) {
		if rel, ok := strings.CutPrefix(clean, root+string(filepath.Separator)); ok {
			first, _, _ := strings.Cut(rel, string(filepath.Separator))
			return filepath.Join(root, first)
		}
	}
	before, _, _ := strings.Cut(project, "/.worktrees/")
	return before
}

func scopeRoots(home string) []string {
	return []string{filepath.Join(home, "dev"), filepath.Join(home, "repos")}
}

func inScope(project, home string) bool {
	clean := filepath.Clean(project)
	for _, root := range scopeRoots(home) {
		if clean == root || strings.HasPrefix(clean, root+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

// loadGroups is one group per (project, model, account, month) with tokens,
// calls and cost, plus how many projects the default scope left out.
func loadGroups(
	ctx context.Context,
	ledger *Ledger,
	card Card,
	home string,
	opts ReportOptions,
) ([]group, int, error) {
	tool, _ := Lookup(opts.Tool)
	where, args := []string{"tool = ?"}, []any{opts.Tool}
	if opts.Since != "" {
		where, args = append(where, "substr(ts, 1, 10) >= ?"), append(args, opts.Since)
	}
	if opts.Until != "" {
		where, args = append(where, "substr(ts, 1, 10) <= ?"), append(args, opts.Until)
	}
	rows, err := ledger.db.QueryContext(ctx,
		`SELECT project, model, account, substr(ts, 1, 7), COUNT(*),
		        SUM(input), SUM(output), SUM(cache_write_5m), SUM(cache_write_1h), SUM(cache_read)
		   FROM responses WHERE `+strings.Join(where, " AND ")+`
		  GROUP BY 1, 2, 3, 4 ORDER BY 1, 2, 3, 4`, args...)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = rows.Close() }()
	var groups []group
	hidden := map[string]bool{}
	for rows.Next() {
		var g group
		if err := rows.Scan(
			&g.project, &g.model, &g.account, &g.month, &g.Calls,
			&g.Input, &g.Output, &g.CacheWrite5m, &g.CacheWrite1h, &g.CacheRead,
		); err != nil {
			return nil, 0, err
		}
		if !opts.NoRollup {
			g.project = rollup(g.project, home)
		}
		if !opts.All && !inScope(g.project, home) {
			hidden[g.project] = true
			continue
		}
		if g.month == "" {
			g.month = "unknown"
		}
		rate, priced := card.Rows.Lookup(g.model, tool.Tiers)
		g.Priced = priced
		if priced {
			g.Cost = rate.Cost(g.Tokens)
		}
		groups = append(groups, g)
	}
	return groups, len(hidden), rows.Err()
}

func (g group) key(by string) string {
	switch by {
	case "model":
		return g.model
	case "account":
		return g.account
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
			rows = append(rows, Row{Name: key, Priced: true})
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
		"name":  func(a, b Row) bool { return a.Name < b.Name },
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

func coverage(ledger *Ledger, fetched time.Time, tool string) (Coverage, error) {
	var first, last string
	var c Coverage
	if err := ledger.db.QueryRow(
		"SELECT COALESCE(MIN(ts), ''), COALESCE(MAX(ts), ''), COUNT(*) FROM responses WHERE tool = ?",
		tool,
	).Scan(&first, &last, &c.Responses); err != nil {
		return c, err
	}
	c.First, c.Last = dateOf(first), dateOf(last)
	rows, err := ledger.db.Query(
		"SELECT account_source, COUNT(*) FROM responses WHERE tool = ? GROUP BY 1", tool,
	)
	if err != nil {
		return c, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var source string
		var n int64
		if err := rows.Scan(&source, &n); err != nil {
			return c, err
		}
		switch source {
		case "session":
			c.SessionRows = n
		case "sweep":
			c.SweepRows = n
		}
	}
	if err := rows.Err(); err != nil {
		return c, err
	}
	c.LastIngestAt, c.LastIngestSummary, c.LastError = "never", "", ""
	if v, err := ledger.Meta("last_ingest_at"); err == nil && v != "" {
		c.LastIngestAt = v
	}
	c.LastIngestSummary, _ = ledger.Meta("last_ingest_summary")
	c.LastError, _ = ledger.Meta("last_error")
	c.RatesFetched = "never"
	if !fetched.IsZero() {
		c.RatesFetched = fetched.UTC().Format("2006-01-02")
	}
	return c, nil
}

func dateOf(ts string) string {
	if len(ts) < 10 {
		return "none"
	}
	return ts[:10]
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

// ToolStatus is one tool's coverage and hooks.
type ToolStatus struct {
	Name        string            `json:"name"`
	Title       string            `json:"title"`
	Implemented bool              `json:"implemented"`
	Coverage    Coverage          `json:"coverage"`
	Hooks       map[string]string `json:"hooks,omitempty"`         // event → a Hook* state
	Skipped     int               `json:"skipped_files,omitempty"` // transcripts the source cannot read (compressed)
}

// Status reads the ledger and the live hook settings; it changes nothing.
func Status(_ context.Context) (StatusInfo, error) {
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
			if status.Coverage, err = coverage(ledger, fetched, tool.Name); err != nil {
				return info, err
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
