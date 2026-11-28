package cli

import (
	"fmt"
	"slices"
	"strings"

	"github.com/Sawmonabo/workbench/internal/agent/costs"
	"github.com/Sawmonabo/workbench/internal/display"
)

// reportSpec is what one table of a report is made from. total, when set, is
// the bold row under a rule, named label; heading, when set, prints above the
// table; bar, when set, is the width of the share bars.
type reportSpec struct {
	heading string
	bar     int
	kind    string // "project" or "model" when the tab view can open the rows
	mark    int    // the marked row, or unmarked or noMarks
	head    string
	rows    []costs.Row
	grand   float64
	tokens  bool
	// cacheHeads are the tool's cache-write column heads under --tokens.
	cacheHeads []string
	tool       costs.Tool // whose rows: how a model id names its tier
	total      *costs.Row
	label      string
	width      int
	pal        palette
	// mins is the least width of each column, the widest cell of that column
	// in any table of the page (writeSections), so the page's tables align.
	mins []int
}

// The mark of a reportSpec: noMarks prints no marker column (a pipe, or a
// table the tab view cannot open), unmarked a column with no row marked.
const (
	noMarks  = -2
	unmarked = -1
)

// reportTable lays one table out: the name, cost and number columns, and,
// where the terminal has room, a share bar that uses the spare width.
func reportTable(r reportSpec) tableSpec {
	// A path keeps its tail; an account keeps its email and, longer, its
	// subscription, which is all that tells one email's rows apart.
	name := column{Head: r.head, Clip: true, ClipLeft: true, Keep: 24}
	if r.head == "account" {
		name.ClipLeft, name.Keep = false, 40
	}
	spec := tableSpec{
		Header: true,
		Cols: []column{
			name,
			{Head: "cost", Right: true},
		},
	}
	if r.tokens {
		spec.Cols = append(spec.Cols,
			column{Head: "calls", Right: true, Drop: 1},
			column{Head: "input", Right: true},
			column{Head: "output", Right: true},
		)
		for i, head := range r.cacheHeads {
			spec.Cols = append(spec.Cols, column{Head: head, Right: true, Drop: 3 + i})
		}
		spec.Cols = append(spec.Cols, column{Head: "cache read", Right: true, Drop: 2})
	} else {
		spec.Cols = append(spec.Cols,
			column{Head: "share", Right: true, Drop: 2},
			column{Head: "calls", Right: true, Drop: 1},
			column{Head: "tokens", Right: true, Drop: 3},
			column{Head: "cached", Right: true, Drop: 4},
		)
	}
	for i, row := range r.rows {
		name := rowName(r.tool, row.Name)
		if row.Subscription != "" {
			name = row.Display()
		}
		if r.kind != "" && i == r.mark {
			name = r.pal.accent.Bold(true).Render(name)
		}
		spec.Rows = append(spec.Rows, reportCells(r, name, row, false))
	}
	if r.total != nil {
		spec.Total = reportCells(r, r.label, *r.total, true)
	}
	for i := range spec.Cols {
		spec.Cols[i].Head = r.pal.accent.Render(spec.Cols[i].Head)
	}
	if !r.tokens && r.bar > 0 && r.grand > 0 {
		addShareBars(&spec, r)
	}
	if r.mark != noMarks {
		addMarker(&spec, r)
	}
	for i := range min(len(spec.Cols), len(r.mins)) {
		spec.Cols[i].Min = r.mins[i]
	}
	return spec
}

// reportCells is one row's cells: name, cost, then the number columns. A
// model row no rate prices has no cost to show, so its cost cell is a dash,
// as share and cached are when they cannot be computed: "$0.00" would read
// as a price, and a cheap model really does round to that. A project, account
// or total that includes an unpriced model is otherwise priced and shows the
// sum of what is; the warning under the report names the model.
func reportCells(r reportSpec, name string, row costs.Row, total bool) []string {
	cost := costStyle.Render(money(row))
	switch {
	case total:
		cost = r.pal.accent.Render(money(row))
	case r.head == "model" && !row.Priced:
		cost = "-"
	}
	out := []string{name, cost}
	if r.tokens {
		out = append(
			out,
			display.Number(row.Calls),
			human(float64(row.Input)),
			human(float64(row.Output)),
		)
		switch len(r.cacheHeads) {
		case 0: // a tool without cache-write columns
		case 1:
			out = append(out, human(float64(row.CacheWrite5m+row.CacheWrite1h)))
		default:
			out = append(out, human(float64(row.CacheWrite5m)), human(float64(row.CacheWrite1h)))
		}
		out = append(out, human(float64(row.CacheRead)))
	} else {
		share, cached := "-", "-"
		if r.grand > 0 {
			share = fmt.Sprintf("%.1f%%", row.Cost/r.grand*100)
		}
		if v, ok := row.CachedShare(); ok {
			cached = fmt.Sprintf("%.1f%%", v*100)
		}
		out = append(out, share, faint.Render(display.Number(row.Calls)),
			faint.Render(human(float64(row.Total()))), faint.Render(cached))
	}
	return out
}

// addShareBars puts a bar of each row's share of the total in front of its
// percentage, r.bar cells wide: the share column then uses the spare width.
func addShareBars(spec *tableSpec, r reportSpec) {
	share := 2 // name, cost, share
	for i, row := range spec.Rows {
		filled := min(int(r.rows[i].Cost/r.grand*float64(r.bar)+0.5), r.bar)
		spec.Rows[i][share] = r.pal.accent.Render(strings.Repeat("█", filled)) +
			faint.Render(strings.Repeat("░", r.bar-filled)) + " " + fmt.Sprintf("%6s", row[share])
	}
	// Every cell is now as wide as the bar and its percentage, so the head sits
	// over the bar and the total's percentage under the others.
	spec.Cols[share].Right = false
	if spec.Total != nil {
		spec.Total[share] = strings.Repeat(" ", r.bar+1) + fmt.Sprintf("%6s", spec.Total[share])
	}
}

// addMarker puts a column in front for the tab view's cursor: the selected
// row's marker, a space elsewhere, so stacked rows line up too. Every table
// of the page gets it, a table the cursor cannot enter included, so their
// names start in the same cell.
func addMarker(spec *tableSpec, r reportSpec) {
	spec.Cols = slices.Insert(spec.Cols, 0, column{Mark: true})
	for i, row := range spec.Rows {
		mark := " "
		if i == r.mark {
			mark = r.pal.accent.Bold(true).Render("›")
		}
		spec.Rows[i] = slices.Insert(row, 0, mark)
	}
	if spec.Total != nil {
		spec.Total = slices.Insert(spec.Total, 0, " ")
	}
}

// writeFooter writes only the notes that report something: the --tokens key,
// the --top cut, the projects the scope hid, what the tool never recorded
// and the models without a rate, on one line when they fit.
func writeFooter(b *strings.Builder, tool costs.Tool, width int, report costs.Statement) {
	var notes []string
	if report.Options.Tokens {
		read := "cache read: prompt tokens served from it"
		note := "cache 5m / 1h: prompt tokens written to the cache with that lifetime; " + read
		switch len(tool.CacheWrites) {
		case 0: // a tool without cache-write columns
			note = "cache read: prompt tokens served from the cache"
		case 1:
			note = "cache write: prompt tokens written to the cache; " + read
		}
		notes = append(notes, note)
	}
	if report.RowCount > len(report.Rows) {
		notes = append(notes, fmt.Sprintf(
			"%d of %d %ss shown; totals cover all", len(report.Rows), report.RowCount, report.By))
	}
	if report.Hidden > 0 {
		notes = append(notes, display.Count(report.Hidden, "project")+" outside "+
			scopeNames(report.Scope)+" hidden (--all shows them)")
	}
	notes = append(notes, unrecordedNotes(tool, report)...)
	unpriced := unpricedNotes(tool, report)
	if len(notes)+len(unpriced) > 0 {
		b.WriteString("\n")
		writeNotes(b, width, 2, notes)
		// Each unpriced note is its own line under the others, a yellow
		// "warning:" before the faint text, so the one note that asks for
		// something stands out of the ones that only inform.
		for _, note := range unpriced {
			writeText(b, width, 2, yellow.Render("warning:")+" "+faint.Render(note))
		}
	}
}

// unpricedNotes name the models no rate prices, whose rows show a dash for
// their cost: one note naming them, and one for responses that name no model,
// which no rate could price. Why each is unpriced is in the JSON and the
// ingest summary. writeFooter prints each under a yellow "warning:" label.
func unpricedNotes(tool costs.Tool, report costs.Statement) []string {
	var models []string
	unnamed := false
	for _, u := range report.Unpriced {
		if u.Model == "unknown" {
			unnamed = true
			continue
		}
		models = append(models, rowName(tool, u.Model))
	}
	var notes []string
	if len(models) > 0 {
		notes = append(
			notes,
			"no rate for "+strings.Join(models, ", "),
		)
	}
	if unnamed {
		notes = append(notes, "responses that name no model are unpriced")
	}
	return notes
}

// unrecordedNotes say, for an account table that shows "not recorded", since
// when the tool's transcripts record the account and the plan: an earlier row
// is named only when the account marks infer it.
func unrecordedNotes(tool costs.Tool, report costs.Statement) []string {
	if report.By != "account" && len(report.Accounts) < 2 {
		return nil
	}
	var account, plan, unsigned bool
	for _, row := range report.Accounts {
		switch {
		case row.UnsignedOrg():
			unsigned = true
		case row.Name == "" || row.Name == "unknown":
			account = true
		}
		plan = plan || row.Subscription == "unknown"
	}
	since := func(part, at string, missing int64) string {
		switch {
		case at == "":
			return tool.Title + "'s transcripts record no " + part
		case costs.LocalDate(at) <= report.Coverage.First:
			// Named from the first row on: the rows without one are threads
			// whose transcript never named it, not an earlier era.
			come := "responses come"
			if missing == 1 {
				come = "response comes"
			}
			return fmt.Sprintf("%s %s from %s conversations that never named the %s",
				display.Number(missing), come, tool.Title, part)
		}
		return fmt.Sprintf("%s records the %s only since %s; earlier usage has no record of it",
			tool.Title, part, localTime(at))
	}
	var notes []string
	c := report.Coverage
	if tool.OrgAccount {
		// The organization a transcript names is the account: what names one
		// names the other, from the same row on.
		c.AccountNamedSince, c.AccountEvidence = c.SubscriptionNamedSince, c.SubscriptionEvidence
	}
	switch {
	case account && plan && c.AccountNamedSince != "" &&
		c.AccountNamedSince == c.SubscriptionNamedSince &&
		costs.LocalDate(c.AccountNamedSince) > c.First:
		notes = append(notes, fmt.Sprintf(
			"%s records the account and plan only since %s; earlier usage has no record of them",
			tool.Title, localTime(c.AccountNamedSince)))
	default:
		if account {
			notes = append(notes, since("account", c.AccountNamedSince, c.AccountEvidence.Unknown))
		}
		if plan {
			notes = append(
				notes,
				since("plan", c.SubscriptionNamedSince, c.SubscriptionEvidence.Unknown),
			)
		}
	}
	if unsigned {
		notes = append(notes, "unknown account (org …): not seen signed in yet; sign in to it "+
			"once to show its email and plan")
	}
	return notes
}
