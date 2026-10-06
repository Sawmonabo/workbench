package costs

import (
	"cmp"
	"context"
	"sort"
	"time"
)

// Focus is one project's or one model's page: its total, its share of the
// report, its cost by model (for a project) or by project (for a model), by
// day over the report's period, and by session.
type Focus struct {
	Kind     string    `json:"kind"` // "project" or "model"
	Name     string    `json:"name"`
	Total    Row       `json:"total"`
	Parts    []Row     `json:"parts"`    // a project's models, or a model's projects
	Days     []Row     `json:"days"`     // Name is YYYY-MM-DD in the machine's time zone, newest first, active days only
	Sessions []Session `json:"sessions"` // costliest first
}

// Session is one session's cost within a focus.
type Session struct {
	ID    string    `json:"id"`
	Start time.Time `json:"start"`
	End   time.Time `json:"end"`
	Model string    `json:"model"` // the model that cost the most in it
	Row
}

type focusGroup struct {
	project, model, day, session, first, last string
	Row
}

// FocusOn reads the ledger only: the page of the project or model named name,
// under the same tool, period, scope and rollup as the report opts made.
func FocusOn(
	ctx context.Context,
	ledger *Ledger,
	opts ReportOptions,
	kind, name string,
) (Focus, error) {
	focus := Focus{Kind: kind, Name: name}
	tool, _ := Lookup(opts.Tool)
	paths, err := Locations()
	if err != nil {
		return focus, err
	}
	card, err := loadCard(paths)
	if err != nil {
		return focus, err
	}
	inPeriod, err := periodFilter(opts.Since, opts.Until)
	if err != nil {
		return focus, err
	}
	type key struct{ project, model, day, session string }
	byKey := map[key]*focusGroup{}
	err = scanResponses(ctx, ledger, opts.Tool, func(r response) {
		if !inPeriod(r.ts) || (kind == "model" && r.model != name) {
			return
		}
		k := key{r.project, r.model, localDay(r.ts), r.session}
		g := byKey[k]
		if g == nil {
			g = &focusGroup{
				project: k.project,
				model:   k.model,
				day:     k.day,
				session: k.session,
				first:   r.ts,
			}
			byKey[k] = g
		}
		g.Calls++
		g.add(r.Tokens)
		g.first, g.last = min(g.first, r.ts), max(g.last, r.ts)
	})
	if err != nil {
		return focus, err
	}
	sorted := make([]*focusGroup, 0, len(byKey))
	for _, g := range byKey {
		sorted = append(sorted, g)
	}
	// The order a GROUP BY gave, so parts and sessions of equal cost keep theirs.
	sort.Slice(sorted, func(i, j int) bool {
		a, b := sorted[i], sorted[j]
		return cmp.Or(
			cmp.Compare(a.project, b.project), cmp.Compare(a.model, b.model),
			cmp.Compare(a.day, b.day), cmp.Compare(a.session, b.session),
		) < 0
	})
	var groups []focusGroup
	roots := ScopeRoots(paths.Home)
	for _, g := range sorted {
		if !opts.NoRollup {
			g.project = rollup(g.project, paths.Home)
		}
		if (!opts.All && !inScope(g.project, roots)) ||
			(kind == "project" && g.project != name) {
			continue
		}
		rate, priced := card.Rows.Lookup(g.model, tool.Tiers)
		g.Priced = priced
		if priced {
			g.Cost = rate.Cost(g.Tokens)
		}
		groups = append(groups, *g)
	}
	focus.Parts = sortRows(sumBy(groups, func(g focusGroup) string {
		if kind == "model" {
			return g.project
		}
		return g.model
	}), opts.Sort)
	focus.Total = total(focus.Parts)
	focus.Days = activeDays(sumBy(groups, func(g focusGroup) string { return g.day }))
	focus.Sessions = sessions(groups)
	return focus, nil
}

// sumBy sums groups by key, in first-seen order.
func sumBy(groups []focusGroup, key func(focusGroup) string) []Row {
	var rows []Row
	index := map[string]int{}
	for _, g := range groups {
		k := key(g)
		i, ok := index[k]
		if !ok {
			i = len(rows)
			index[k] = i
			rows = append(rows, Row{Name: k, Priced: true})
		}
		rows[i].Calls += g.Calls
		rows[i].Cost += g.Cost
		rows[i].Priced = rows[i].Priced && g.Priced
		rows[i].add(g.Tokens)
	}
	return rows
}

// activeDays is the days with responses, newest first; undated responses
// come last as "unknown".
func activeDays(days []Row) []Row {
	for i := range days {
		if _, err := time.Parse(time.DateOnly, days[i].Name); err != nil {
			days[i].Name = "unknown"
		}
	}
	sort.SliceStable(days, func(i, j int) bool {
		a, b := days[i].Name, days[j].Name
		if (a == "unknown") != (b == "unknown") {
			return b == "unknown"
		}
		return a > b
	})
	return days
}

// sessions sums groups by session, costliest first, with each session's time
// span and the model that cost the most in it.
func sessions(groups []focusGroup) []Session {
	byID := map[string]*Session{}
	modelCost := map[string]map[string]float64{}
	var order []string
	for _, g := range groups {
		s, ok := byID[g.session]
		if !ok {
			s = &Session{ID: g.session, Row: Row{Name: g.session, Priced: true}}
			byID[g.session] = s
			modelCost[g.session] = map[string]float64{}
			order = append(order, g.session)
		}
		if start, err := time.Parse(time.RFC3339, g.first); err == nil &&
			(s.Start.IsZero() || start.Before(s.Start)) {
			s.Start = start
		}
		if end, err := time.Parse(time.RFC3339, g.last); err == nil && end.After(s.End) {
			s.End = end
		}
		s.Calls += g.Calls
		s.Cost += g.Cost
		s.Priced = s.Priced && g.Priced
		s.add(g.Tokens)
		modelCost[g.session][g.model] += g.Cost
	}
	out := make([]Session, 0, len(order))
	for _, id := range order {
		s := byID[id]
		best := -1.0
		for model, cost := range modelCost[id] {
			if cost > best || (cost == best && model < s.Model) {
				s.Model, best = model, cost
			}
		}
		out = append(out, *s)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Cost > out[j].Cost })
	return out
}
