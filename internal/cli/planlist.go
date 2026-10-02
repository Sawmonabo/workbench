package cli

import (
	"fmt"
	"slices"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/Sawmonabo/workbench/internal/operation"
)

// The plan view's own styles, beside the ones `workbench costs` shares from
// root.go and costs.go: the brand and key color is the costs default accent,
// and a new item wears the dark-on-yellow pill the costs tabs wear in their
// own color.
var (
	brand = toolPalette("workbench").accent
	badge = lipgloss.NewStyle().Bold(true).Padding(0, 1).
		Foreground(lipgloss.Color("#1E1E1E")).Background(lipgloss.Yellow)
)

// forTerm rewrites s for the terminal's glyphs, so a literal with › · − ↑ ↓ never
// shows mojibake.
func forTerm(s string) string { return glyphs.text.Replace(s) }

// effectTitle is the plain name of an effect; the internal name only stands in
// for an effect that has none.
func effectTitle(effect operation.Effect) string {
	if effect.Title != "" {
		return effect.Title
	}
	return effect.Name
}

// effectIndex is where the named effect is in the plan, -1 when it is not.
func effectIndex(plan operation.Plan, name string) int {
	return slices.IndexFunc(plan.Effects, func(effect operation.Effect) bool {
		return effect.Name == name
	})
}

// isLocked reports that effect is a part whose parent is checked: it is
// included with the parent, shown checked and cannot be toggled.
func isLocked(plan operation.Plan, effect operation.Effect) bool {
	if effect.Parent == "" {
		return false
	}
	parent := effectIndex(plan, effect.Parent)
	return parent >= 0 && plan.Effects[parent].Checked
}

// runs reports that the effect is part of this apply: always, checked, or
// included with its checked parent.
func runs(plan operation.Plan, effect operation.Effect) bool {
	return effect.Fixed || effect.Checked || isLocked(plan, effect)
}

// isAlready reports an effect the probe found nothing to do for. It collapses
// into the Already set line and cannot be toggled; a saved skip stays listed, and
// so does a parent while a part under it still has something to do. See
// [operation.Plan.AlreadySet].
func isAlready(plan operation.Plan, effect operation.Effect) bool {
	return plan.AlreadySet(effect)
}

// planGroup is one headed block of rows.
type planGroup struct {
	Title, Note string
	Rows        []planRow
}

// planLayout is what the list shows, in the order it is drawn: the headed
// groups, the effects behind the faint Already set line and, in the compact
// view, the saved skips named on its Off line.
type planLayout struct {
	Groups  []planGroup
	Already []int
	Off     []int
}

const (
	groupWill = iota
	groupOff
	groupOptional
	groupNone
)

// groupOf says which group an effect is drawn in, from the plan as passed in
// and not from a box the owner has since toggled, so a row never jumps. The
// compact view of an apply that does not ask lists only what runs.
func groupOf(plan operation.Plan, effect operation.Effect, compact bool) int {
	switch {
	case effect.Fixed:
		return groupWill
	case isAlready(plan, effect):
		return groupNone
	case compact:
		if runs(plan, effect) {
			return groupWill
		}
		return groupNone
	case effect.SavedSkip:
		return groupOff
	case effect.Optional:
		return groupOptional
	}
	return groupWill
}

// layoutPlan groups the effects. A part sits right under its parent wherever
// the parent is drawn; a part without a drawn parent is an ordinary row.
func layoutPlan(plan operation.Plan, compact bool) planLayout {
	groups := []planGroup{
		{Title: "Will run"},
		{Title: "Off"},
		{Title: "Optional", Note: "off unless you turn them on"},
	}
	var lay planLayout
	children := map[int][]int{}
	isChild := map[int]bool{}
	for i, effect := range plan.Effects {
		parent := effectIndex(plan, effect.Parent)
		if effect.Parent != "" && parent >= 0 &&
			groupOf(plan, plan.Effects[parent], compact) != groupNone {
			children[parent] = append(children[parent], i)
			isChild[i] = true
		}
	}
	for _, fixed := range []bool{true, false} {
		for i, effect := range plan.Effects {
			if isChild[i] || effect.Fixed != fixed {
				continue
			}
			group := groupOf(plan, effect, compact)
			switch {
			case group != groupNone:
				groups[group].Rows = append(groups[group].Rows, planRow{rowEffect, i})
				for _, child := range children[i] {
					groups[group].Rows = append(groups[group].Rows, planRow{rowEffect, child})
				}
			case isAlready(plan, effect):
				lay.Already = append(lay.Already, i)
			case effect.SavedSkip:
				lay.Off = append(lay.Off, i)
			}
		}
	}
	for _, group := range groups {
		if len(group.Rows) > 0 {
			lay.Groups = append(lay.Groups, group)
		}
	}
	return lay
}

// selectableRows lists the rows a cursor can stop on, in the order they are
// drawn: files, then the effects group by group, then the Already set effects.
// Fixed and Already-set effects can be read but not toggled. Parts included
// with a checked parent are not among them.
func selectableRows(plan operation.Plan) []planRow {
	rows := make([]planRow, 0, len(plan.Edits)+len(plan.Effects))
	for i, edit := range plan.Edits {
		if edit.Listed() {
			rows = append(rows, planRow{rowFile, i})
		}
	}
	for _, group := range layoutPlan(plan, false).Groups {
		for _, row := range group.Rows {
			if !isLocked(plan, plan.Effects[row.Index]) {
				rows = append(rows, row)
			}
		}
	}
	for _, i := range layoutPlan(plan, false).Already {
		rows = append(rows, planRow{rowEffect, i})
	}
	return rows
}

// listLine is one line of the list and the selectable row it belongs to, the
// zero row for a header, a gap or a note.
type listLine struct {
	Text string
	Row  planRow
}

// listPrefix is what stands left of a row's text: the cursor and box columns, or
// a plain indent in the compact view.
const (
	livePrefix    = 6
	compactPrefix = 2
	childIndent   = 3
)

// listLines is the list: groups of rows, then the Already set line and the
// notes the plan carries. width is the room the list has.
func listLines(plan operation.Plan, width int, view planView) []listLine {
	var out []listLine
	lay := layoutPlan(plan, view.Applying)
	gap := func() {
		if len(out) > 0 {
			out = append(out, listLine{})
		}
	}
	header := func(title, note string) {
		gap()
		line := bold.Render(title)
		if note != "" {
			line += "  " + faint.Render(note)
		}
		out = append(out, listLine{Text: line})
	}
	row := func(r planRow, lines []string) {
		for _, line := range lines {
			out = append(out, listLine{Text: line, Row: r})
		}
		out = append(out, verbosePanel(plan, r, width, view)...)
	}
	if plan.Files() > 0 {
		note := ""
		if view.Interactive && !view.Done {
			note = "select one to see its diff"
		}
		header("Files", note)
		for i, edit := range plan.Edits {
			if edit.Listed() {
				row(planRow{rowFile, i}, fileLines(plan, i, width, view))
			}
		}
	}
	for _, group := range lay.Groups {
		header(group.Title, group.Note)
		for _, r := range group.Rows {
			row(r, effectLines(plan, r.Index, width, view))
		}
	}
	if len(lay.Off) > 0 {
		gap()
		names := make([]string, 0, len(lay.Off))
		for _, i := range lay.Off {
			names = append(names, effectTitle(plan.Effects[i]))
		}
		out = append(out, wrapLabelled(
			"Off", strings.Join(names, ", "), width, "change with ", "workbench apply --choose",
		)...)
	}
	if len(lay.Already) > 0 {
		gap()
		out = append(out, alreadyLines(plan, lay.Already, width, view)...)
		for _, i := range lay.Already {
			out = append(out, verbosePanel(plan, planRow{rowEffect, i}, width, view)...)
		}
	}
	return append(out, noteLines(plan, width, view)...)
}

// verbosePanel is the detail panel --verbose prints under a row in the static
// view: none in the live list or without --verbose. The row already names the
// item, so the panel starts below its title (and, for a file, its path and
// edited-outside lines); an Already set effect has no row of its own, so its
// panel keeps the title.
func verbosePanel(plan operation.Plan, r planRow, width int, view planView) []listLine {
	if !view.Verbose || view.Interactive {
		return nil
	}
	pad := strings.Repeat(" ", compactPrefix+2)
	detail := renderDetail(plan, r, width-len(pad), !view.NoDiff)
	skip := 1
	switch {
	case r.Kind == rowFile && plan.Edits[r.Index].EditedOutside:
		skip = 3
	case r.Kind == rowFile:
		skip = 2
	case isAlready(plan, plan.Effects[r.Index]):
		skip = 0
	}
	var out []listLine
	for _, line := range detail[min(skip, len(detail)):] {
		out = append(out, listLine{Text: trimEnd(pad + line)})
	}
	return out
}

// alreadyLines is the faint Already set line: the plain names only, wrapped
// between names to width. What is already in place is in each name's detail
// panel, which the cursor reaches (never to toggle) and --verbose prints. The
// name under the cursor is drawn bright and its line carries the cursor row so
// the live list scrolls to it.
func alreadyLines(plan operation.Plan, already []int, width int, view planView) []listLine {
	const label = "Already set"
	indent := len(label) + 1
	room := max(width-indent, 10)
	type name struct {
		text   string
		width  int
		cursor bool
	}
	var rows [][]name
	used := 0
	for n, i := range already {
		text := fit(forTerm(effectTitle(plan.Effects[i])), room-1, false)
		cell := name{
			text:   text,
			width:  ansi.StringWidth(text),
			cursor: !view.Done && view.Interactive && view.Cursor == (planRow{rowEffect, i}),
		}
		if n < len(already)-1 {
			cell.width++ // its comma
		}
		if len(rows) == 0 || used+1+cell.width > room {
			rows = append(rows, nil)
			used = -1
		}
		rows[len(rows)-1] = append(rows[len(rows)-1], cell)
		used += 1 + cell.width
	}
	var out []listLine
	at := 0
	for r, cells := range rows {
		prefix := strings.Repeat(" ", indent)
		if r == 0 {
			prefix = faint.Render(label) + " "
		}
		var line listLine
		parts := make([]string, len(cells))
		for c, cell := range cells {
			style := faint
			if cell.cursor {
				style, line.Row = brand.Bold(true), planRow{rowEffect, already[at]}
			}
			parts[c] = style.Render(cell.text)
			if at < len(already)-1 {
				parts[c] += faint.Render(",")
			}
			at++
		}
		line.Text = prefix + strings.Join(parts, " ")
		out = append(out, line)
	}
	return out
}

// wrapLabelled is a faint label, the names after it and an optional trailing
// note, word-wrapped to width with the continuation indented. The note (faint)
// and its emphasised end are counted in the width: they stay on the last line
// when they fit there, else stand on a line of their own.
func wrapLabelled(label, names string, width int, note, emph string) []listLine {
	var out []listLine
	indent := strings.Repeat(" ", len(label)+1)
	body := ansi.Wrap(forTerm(names), max(width-len(label)-1, 10), "")
	for i, line := range strings.Split(body, "\n") {
		line = strings.TrimRight(line, " ")
		if i == 0 {
			line = faint.Render(label) + " " + line
		} else {
			line = indent + line
		}
		out = append(out, listLine{Text: line})
	}
	if note == "" {
		return out
	}
	joined := faint.Render(forTerm(" · ")+note) + bold.Render(emph)
	last := &out[len(out)-1]
	if ansi.StringWidth(last.Text)+ansi.StringWidth(joined) <= width {
		last.Text += joined
		return out
	}
	own := faint.Render(note) + bold.Render(emph)
	return append(out, listLine{Text: indent + fit(own, width-len(indent), false)})
}

// noteLines are what the plan says besides its rows: the prerequisites and,
// under --verbose, the recovery limits.
func noteLines(plan operation.Plan, width int, view planView) []listLine {
	var out []listLine
	sections := []struct {
		title string
		lines []string
	}{{"Prerequisites", plan.Prerequisites}}
	if view.Verbose {
		sections = append(sections, struct {
			title string
			lines []string
		}{"Recovery limits", plan.RecoveryLimits})
	}
	for _, section := range sections {
		if len(section.lines) == 0 {
			continue
		}
		out = append(out, listLine{}, listLine{Text: bold.Render(section.title)})
		for _, line := range section.lines {
			for _, part := range wrapPlain(line, width-2) {
				out = append(out, listLine{Text: "  " + part})
			}
		}
	}
	return out
}

// wrapPlain word-wraps text to width cells, trimming each line's end.
func wrapPlain(s string, width int) []string {
	lines := strings.Split(ansi.Wrap(forTerm(s), max(width, 1), ""), "\n")
	for i := range lines {
		lines[i] = strings.TrimRight(lines[i], " ")
	}
	return lines
}

// rowPrefix is the cursor and box columns of a row (or the plain indent of the
// compact view, which has no boxes), and its width.
func rowPrefix(
	view planView,
	row planRow,
	box string,
	boxStyle lipgloss.Style,
	depth int,
) (string, int) {
	indent := strings.Repeat(" ", depth*childIndent)
	if view.Applying {
		return strings.Repeat(" ", compactPrefix) + indent, compactPrefix + len(indent)
	}
	cursor := " "
	if !view.Done && view.Cursor == row {
		cursor = brand.Render(forTerm("›"))
	}
	return cursor + " " + boxStyle.Render(box) + " " + indent, livePrefix + len(indent)
}

// effectLines is an effect's row: its plain name and tags, then one faint line
// saying what it does. Nothing else stands on the row.
func effectLines(plan operation.Plan, index, width int, view planView) []string {
	effect := plan.Effects[index]
	row := planRow{rowEffect, index}
	locked := isLocked(plan, effect)
	depth := 0
	if parent := effectIndex(plan, effect.Parent); effect.Parent != "" && parent >= 0 &&
		groupOf(plan, plan.Effects[parent], view.Applying) != groupNone {
		depth = 1
	}
	box, boxStyle := "[ ]", faint
	switch {
	case effect.Fixed, isAlready(plan, effect):
		// Always runs, or nothing to change: no box, since there is no choice.
		box = "   "
	case locked:
		box = "[x]"
	case effect.Checked:
		box, boxStyle = "[x]", green
	}
	prefix, prefixWidth := rowPrefix(view, row, box, boxStyle, depth)
	titleStyle := bold
	switch {
	case locked, isAlready(plan, effect):
		titleStyle = faint
	case !view.Done && view.Interactive && view.Cursor == row:
		titleStyle = brand.Bold(true)
	}
	tags := ""
	if effect.Fixed {
		tags += " " + faint.Render("always")
	}
	if effect.New && !isAlready(plan, effect) {
		tags += " " + badge.Render("NEW")
	}
	title := fit(
		forTerm(effectTitle(effect)),
		max(width-prefixWidth-ansi.StringWidth(tags), 8),
		false,
	)
	summary := effect.Summary
	if summary == "" {
		summary = effect.Description
	}
	pad := strings.Repeat(" ", prefixWidth)
	lines := []string{prefix + titleStyle.Render(title) + tags}
	if summary != "" {
		lines = append(
			lines,
			pad+faint.Render(fit(forTerm(summary), max(width-prefixWidth, 8), false)),
		)
	}
	return lines
}

// fileLines is a file's row: its friendly title with coloured line counts, then
// a faint line with the path and whose file it is, and a yellow line when the
// owned file was edited outside Workbench.
func fileLines(plan operation.Plan, index, width int, view planView) []string {
	edit := plan.Edits[index]
	prefix, prefixWidth := rowPrefix(view, planRow{rowFile, index}, "   ", faint, 0)
	title := edit.Title
	if title == "" {
		title = homePath(edit.Path)
	}
	counts := ""
	if edit.Semantic {
		counts = " " + yellow.Render(forTerm(settingsCount(edit)))
	} else if edit.Added+edit.Removed > 0 {
		counts = " " + green.Render(forTerm(fmt.Sprintf("+%d", edit.Added))) + " " +
			red.Render(forTerm(fmt.Sprintf("−%d", edit.Removed)))
	} else if word := actionWord(edit); word != "" {
		counts = " " + faint.Render(word)
	}
	titleStyle := bold
	if !view.Done && view.Interactive && view.Cursor == (planRow{rowFile, index}) {
		titleStyle = brand.Bold(true)
	}
	title = fit(forTerm(title), max(width-prefixWidth-ansi.StringWidth(counts), 8), false)
	pad := strings.Repeat(" ", prefixWidth)
	lines := []string{
		prefix + titleStyle.Render(title) + counts,
		pad + faint.Render(
			fit(
				forTerm(homePath(edit.Path)+" · "+fileOwner(edit)),
				max(width-prefixWidth, 8),
				false,
			),
		),
	}
	if edit.EditedOutside {
		lines = append(
			lines,
			pad+yellow.Render(fit(forTerm(editedNote), max(width-prefixWidth, 8), false)),
		)
	}
	return lines
}

const editedNote = "edited outside Workbench: your edit will be replaced"

// actionWord names what happens to a file when its diff counts are empty (a
// file with no text diff to show).
func actionWord(edit operation.Edit) string {
	switch edit.Action {
	case "create":
		return "new"
	case "remove":
		return "removed"
	case "modify":
		return "changed"
	}
	return ""
}

// fileOwner says whose a file is: a merged one keeps the owner's own keys, any
// other is written whole by Workbench.
func fileOwner(edit operation.Edit) string {
	if edit.Merged {
		return "your own settings kept"
	}
	return "owned by Workbench"
}

// planCounts are the header's counts: files, steps that will run, new to decide.
func planCounts(plan operation.Plan) (files, steps, undecided int) {
	for _, effect := range plan.Effects {
		if runs(plan, effect) && (effect.Fixed || !effect.NoChange) {
			steps++
		}
		if effect.New && !isAlready(plan, effect) && !effect.Fixed {
			undecided++
		}
	}
	return plan.Files(), steps, undecided
}

// releaseLabel names the release a plan comes from for the header.
func releaseLabel(source operation.SourceIdentity) string {
	switch {
	case source.Release == "":
		return ""
	case source.Release == "developer":
		return "developer checkout " + shortDigest(source.ContentDigest)
	case source.Release[0] >= '0' && source.Release[0] <= '9':
		return "v" + source.Release
	}
	return source.Release
}

// planHeader is the brand line, the counts, and what must not be missed: an
// incomplete plan and the warnings, which stand above the list.
func planHeader(plan operation.Plan, width int, view planView) []string {
	files, steps, undecided := planCounts(plan)
	title := "Plan for this PC"
	if view.Applying {
		title = "Applying your saved choices"
	}
	if label := releaseLabel(plan.Source); label != "" {
		title += forTerm(" · ") + label
	}
	first := brand.Bold(true).Render("[WorkBench]") + " " + title
	chips := []string{
		bold.Render(fmt.Sprint(files)) + faint.Render(" file"+plural(files)),
		bold.Render(fmt.Sprint(steps)) + faint.Render(" step"+plural(steps)+" will run"),
	}
	if undecided > 0 {
		chips = append(chips, yellow.Bold(true).Render(fmt.Sprint(undecided))+
			yellow.Render(" new to decide"))
	}
	line := first + "   " + strings.Join(chips, "   ")
	lines := []string{fit(line, width, false)}
	if ansi.StringWidth(line) > width {
		lines = []string{fit(first, width, false), fit(strings.Join(chips, "   "), width, false)}
	}
	if !plan.Complete {
		for _, part := range wrapPlain("This plan is incomplete and cannot be applied as shown.", width) {
			lines = append(lines, yellow.Render(part))
		}
	}
	for _, warning := range plan.Warnings {
		for i, part := range wrapPlain(warning, width-9) {
			prefix := "         "
			if i == 0 {
				prefix = yellow.Render("Warning:") + " "
			}
			lines = append(lines, prefix+part)
		}
	}
	return lines
}
