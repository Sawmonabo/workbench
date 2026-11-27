package cli

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"charm.land/lipgloss/v2"

	"github.com/Sawmonabo/workbench/internal/agent/costs"
	"github.com/Sawmonabo/workbench/internal/display"
)

// rowName is how a report row of tool is named: a path under the home
// directory as ~/..., and a model at a service tier as "gpt-5.6-sol (fast)".
func rowName(tool costs.Tool, name string) string {
	if base, tier := tool.SplitModel(name); tier != "" {
		return base + " (" + tier + ")"
	}
	return display.Path(name)
}

// scopeNames names the role folders a report's default view covers, such as
// "~/work" or "~/work and ~/play".
func scopeNames(roots []string) string {
	names := make([]string, len(roots))
	for i, root := range roots {
		names[i] = display.Path(root)
	}
	return strings.Join(names, " and ")
}

// costStyle colors every dollar figure.
var costStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#8FC37E"))

// minBar is the narrowest share bar; a terminal with less spare width gets
// the percentages alone.
const minBar = 8

// costsReport is one tool's whole report at width: the [WorkBench] line, then
// costsBody. It is what is printed to a pipe and, after the tabs close, to the
// scrollback.
func costsReport(tool costs.Tool, report costs.Statement, width int) string {
	var b strings.Builder
	switch {
	case tool.Source == nil:
		writeText(&b, width, 0, brandTag+" "+tool.Title+" costs are not implemented yet")
	case report.Empty():
		writeText(&b, width, 0, brandTag+" "+emptyReport(report))
	default:
		writeStyled(&b, width, 0, brandTag+" "+tool.Title+" costs", bold)
		b.WriteString(costsBody(tool, report, width))
	}
	return b.String()
}

// emptyReport is the line for a report with no rows.
func emptyReport(report costs.Statement) string {
	if report.Hidden > 0 {
		return fmt.Sprintf(
			"no responses matched in %s; %d projects elsewhere (--all shows them)",
			scopeNames(report.Scope),
			report.Hidden,
		)
	}
	return "no responses matched (check `workbench agent costs status`)"
}

// target is a row the tab view can open: a project or a model.
type target struct{ kind, name string }

// costsBody is the report under the brand line, which the tabs replace with
// their tab bar: the total and its period, then the tables, then only the
// notes that report something. A tool without a source, or without rows,
// says so.
func costsBody(tool costs.Tool, report costs.Statement, width int) string {
	body, _, _ := costsPage(tool, report, width, -1)
	return body
}

// costsPage is costsBody for the tab view when selected >= 0: the project and
// model rows it can open get a marker column, the selected one marked, and
// targets lists them in order. hits holds, for each line of the body, the
// index in targets of the row on it, or -1; it may stop before the last line.
func costsPage(
	tool costs.Tool,
	report costs.Statement,
	width, selected int,
) (body string, targets []target, hits []int) {
	var b strings.Builder
	switch {
	case tool.Source == nil:
		writeFaint(&b, width, 0, tool.Title+" costs are not implemented yet.")
		return b.String(), nil, nil
	case report.Empty():
		writeFaint(&b, width, 0, emptyReport(report))
		return b.String(), nil, nil
	}
	pal := toolPalette(tool.Name)
	opts := report.Options
	c := report.Coverage
	// The count and the days are the table's: the period and scope the total
	// below is for, not everything the ledger holds (agent costs status shows that).
	period := fmt.Sprintf(
		"%s responses · %s → %s",
		display.Number(report.Total.Calls),
		day(report.First),
		day(report.Last),
	)
	writeText(
		&b,
		width,
		0,
		pal.accent.Bold(true).Render(money(report.Total))+"  "+faint.Render(period),
	)
	writeNotes(&b, width, 0, []string{
		tool.PriceNote,
		"updated " + localTime(c.LastIngestAt),
	})
	grand := report.GrandTotal
	label := "total · " + display.Count(report.RowCount, report.By)
	total := report.Total
	mark := noMarks
	if selected >= 0 {
		mark = unmarked
	}
	base := reportSpec{
		grand: grand, tokens: opts.Tokens, cacheHeads: tool.CacheWrites, tool: tool,
		width: width, pal: pal, mark: mark,
	}
	var sections []reportSpec
	add := func(heading, head, kind string, rows []costs.Row, total *costs.Row, label string) {
		section := base
		section.heading, section.head, section.rows, section.total, section.label = heading, head, rows, total, label
		if selected >= 0 && kind != "" {
			section.kind = kind
			if offset := selected - len(targets); offset >= 0 && offset < len(rows) {
				section.mark = offset
			}
			for _, row := range rows {
				targets = append(targets, target{kind, row.Name})
			}
		}
		sections = append(sections, section)
	}
	if opts.Detail && report.By == "project" {
		for _, block := range report.Detail {
			share := "-"
			if grand > 0 {
				share = fmt.Sprintf("%.1f%%", block.Project.Cost/grand*100)
			}
			add(bold.Render(display.Path(block.Project.Name))+"  "+
				costStyle.Render(money(block.Project))+"  "+
				faint.Render(
					fmt.Sprintf("%s · %s calls", share, display.Number(block.Project.Calls)),
				),
				"model", "", block.Models, nil, "")
		}
		add("", "model (all projects)", "model", report.Models, &total, label)
	} else {
		kind := ""
		if report.By == "project" || report.By == "model" {
			kind = report.By
		}
		add("", report.By, kind, report.Rows, &total, label)
		if report.By != "model" {
			add("", "model", "model", report.Models, nil, "")
		}
	}
	if report.By != "account" && len(report.Accounts) > 1 {
		add("", "account", "", report.Accounts, nil, "")
	}
	hits = writeSections(&b, width, sections)
	writeFooter(&b, tool, width, report)
	return b.String(), targets, hits
}

// writeSections writes report tables as one grid: each column is as wide as
// its widest cell in any of the tables, and one bar width serves all of them,
// all the width that leaves, so names, costs and numbers line up from table
// to table and bars compare across them. hits
// holds, for each line of b, the row on it, or -1: the rows of the sections
// with a kind are numbered in order across them, as costsPage's targets are.
func writeSections(b *strings.Builder, width int, sections []reportSpec) (hits []int) {
	sections = slices.Clone(sections)
	var mins []int
	for _, section := range sections {
		widths := naturalWidths(reportTable(section).plain())
		if mins == nil {
			mins = widths
			continue
		}
		for i := range min(len(mins), len(widths)) {
			mins[i] = max(mins[i], widths[i])
		}
	}
	bar := width
	for i := range sections {
		sections[i].mins = mins
		spec := reportTable(sections[i])
		need := tableWidth(2, spec) + 1 // the bar and the space after it
		if !sections[i].tokens {
			// addShareBars pads every percentage to 6 cells, wider than a share
			// column whose widest cell is "share" or "68.5%".
			share := 2 // name, cost, share
			if spec.Cols[0].Mark {
				share++
			}
			need += max(6-naturalWidths(spec.plain())[share], 0)
		}
		bar = min(bar, width-need)
	}
	first := 0 // the number of the section's first row
	for _, section := range sections {
		b.WriteString("\n")
		if section.heading != "" {
			writeText(b, width, 0, section.heading)
		}
		if bar >= minBar {
			section.bar = bar
		}
		lines, rows := fitRows(width, 2, reportTable(section))
		if section.kind != "" {
			// Every line of b ends in a newline, so the count is the line
			// the table starts on.
			for range strings.Count(b.String(), "\n") - len(hits) {
				hits = append(hits, -1)
			}
			for _, row := range rows {
				if row >= 0 {
					row += first
				}
				hits = append(hits, row)
			}
			first += len(section.rows)
		}
		for _, line := range lines {
			b.WriteString(line + "\n")
		}
	}
	return hits
}

// focusBody is the page of one project or model: the way back, its total and
// share, then its cost by model (or project), by day and by session.
func focusBody(tool costs.Tool, focus costs.Focus, report costs.Statement, width int) string {
	var b strings.Builder
	pal := toolPalette(tool.Name)
	back := "projects"
	if report.By != "project" || focus.Kind == "model" {
		back = "report"
	}
	writeText(&b, width, 0, faint.Render("‹ "+back)+"   "+bold.Render(rowName(tool, focus.Name)))
	b.WriteString("\n")
	var notes []string
	if report.GrandTotal > 0 {
		notes = append(
			notes,
			fmt.Sprintf("%.1f%% of the total", focus.Total.Cost/report.GrandTotal*100),
		)
	}
	notes = append(notes, display.Number(focus.Total.Calls)+" calls")
	dated := slices.DeleteFunc(
		slices.Clone(focus.Days),
		func(d costs.Row) bool { return d.Name == "unknown" },
	)
	if n := len(dated); n > 0 {
		notes = append(notes, day(dated[n-1].Name)+" → "+day(dated[0].Name))
	}
	writeText(&b, width, 0, pal.accent.Bold(true).Render(money(focus.Total)))
	writeNotes(&b, width, 0, notes)
	parts := "model"
	if focus.Kind == "model" {
		parts = "project"
	}
	days := make([]costs.Row, len(focus.Days))
	for i, d := range focus.Days {
		d.Name = day(d.Name)
		days[i] = d
	}
	base := reportSpec{
		grand:      focus.Total.Cost,
		tokens:     report.Options.Tokens,
		cacheHeads: tool.CacheWrites,
		tool:       tool,
		width:      width,
		pal:        pal,
		mark:       noMarks,
	}
	partsSection, daysSection := base, base
	partsSection.head, partsSection.rows = parts, focus.Parts
	daysSection.head, daysSection.rows = "day", days
	writeSections(&b, width, []reportSpec{partsSection, daysSection})
	b.WriteString("\n")
	writeTable(&b, width, 2, sessionTable(tool, focus.Sessions, pal))
	return b.String()
}

// sessionTable lists a focus's sessions, costliest first.
func sessionTable(tool costs.Tool, sessions []costs.Session, pal palette) tableSpec {
	spec := tableSpec{Header: true, Cols: []column{
		{Head: "session"},
		{Head: "started", Clip: true},
		{Head: "length", Right: true, Drop: 2},
		{Head: "model", Drop: 3},
		{Head: "cost", Right: true},
		{Head: "calls", Right: true, Drop: 1},
	}}
	for i := range spec.Cols {
		spec.Cols[i].Head = pal.accent.Render(spec.Cols[i].Head)
	}
	for _, session := range sessions {
		id := session.ID
		if len(id) > 8 {
			id = id[:8]
		}
		started, length := "-", "-"
		if !session.Start.IsZero() {
			started = session.Start.Local().Format(display.DayTime)
			// A resumed session spans from its first response to its last.
			length = display.Duration(session.End.Sub(session.Start))
		}
		spec.Rows = append(spec.Rows, []string{
			faint.Render(id), started, faint.Render(length), rowName(tool, session.Model),
			costStyle.Render(money(session.Row)), faint.Render(display.Number(session.Calls)),
		})
	}
	return spec
}

// writeNotes writes faint notes on one line, joined by a dot, when they fit,
// and one per line when they do not.
func writeNotes(b *strings.Builder, width, indent int, notes []string) {
	if joined := strings.Join(notes, " · "); len(notes) > 1 &&
		indent+lipgloss.Width(glyphs.text.Replace(joined)) <= width {
		notes = []string{joined}
	}
	for _, note := range notes {
		writeFaint(b, width, indent, note)
	}
}

// day is a ledger date as "Sep 30", with the year when it is not this year.
func day(date string) string {
	when, err := time.Parse(time.DateOnly, date)
	if err != nil {
		return date
	}
	if when.Year() != time.Now().Year() {
		return when.Format(display.Date)
	}
	return when.Format(display.Day)
}

// human abbreviates a token count: 2.1K, 600.0M, 1.0B.
func human(n float64) string {
	for _, unit := range []struct {
		suffix string
		scale  float64
	}{{"B", 1e9}, {"M", 1e6}, {"K", 1e3}} {
		if n >= unit.scale {
			return display.Decimal(n/unit.scale, 1) + unit.suffix
		}
	}
	return strconv.FormatInt(int64(n), 10)
}

// money is a row's cost in dollars and cents, from [costs.Row.Cents], which
// rounds its exact sum once.
func money(row costs.Row) string {
	cents := row.Cents()
	return fmt.Sprintf("$%s.%02d", display.Number(cents/100), cents%100)
}

// localTime is an ingest timestamp in the machine's zone: "Sep 30 9:00 PM".
func localTime(stamp string) string {
	when, err := time.Parse(time.RFC3339, stamp)
	if err != nil {
		return stamp
	}
	return when.Local().Format(display.DayTime)
}
