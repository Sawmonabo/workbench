package cli

import (
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/Sawmonabo/workbench/internal/operation"
)

// labelWidth is the column the detail panel's labels (On this PC, Changes, Runs
// as, To undo) take, with a space after.
const labelWidth = 11

// renderDetail is the detail panel for a row, width columns wide: for an
// effect, its name and status, what it does, its relation to its parent, then
// what it would do on this PC now, what it changes, who it runs as and how to
// undo it; for a file, its title, owner and unified diff. It returns no lines
// for rowNone.
func renderDetail(plan operation.Plan, row planRow, width int) []string {
	switch row.Kind {
	case rowFile:
		if row.Index < len(plan.Edits) {
			return fileDetail(plan.Edits[row.Index], width, false)
		}
	case rowEffect:
		if row.Index < len(plan.Effects) {
			return effectDetail(plan, plan.Effects[row.Index], width)
		}
	case rowNone:
	}
	return nil
}

// effectDetail is the panel of one effect.
func effectDetail(plan operation.Plan, effect operation.Effect, width int) []string {
	title := bold.Render(forTerm(effectTitle(effect)))
	if effect.New && !isAlready(effect) {
		label := "NEW"
		if release := releaseLabel(plan.Source); release != "" && release != "developer" {
			label += " in " + release
		}
		title += " " + badge.Render(label)
	}
	lines := []string{fit(title, width, false), faint.Render(forTerm(effectStatus(plan, effect)))}
	paragraph := func(s string) {
		if s == "" {
			return
		}
		lines = append(lines, "")
		lines = append(lines, wrapPlain(s, width)...)
	}
	paragraph(effect.What)
	paragraph(relation(plan, effect))
	here := effect.Delta
	if here == "" {
		here = effect.ProbeNote
	}
	pairs := [][2]string{
		{"On this PC", here},
		{"Changes", effect.Touches},
		{"Runs as", effect.RunsAs},
		{"To undo", effect.Undo},
	}
	gap := true
	for _, pair := range pairs {
		if pair[1] == "" {
			continue
		}
		if gap {
			lines = append(lines, "")
			gap = false
		}
		lines = append(lines, labelled(pair[0], pair[1], width)...)
	}
	return lines
}

// labelled is one label, value row of the panel: the label faint in its own
// column, the value wrapped beside it.
func labelled(label, value string, width int) []string {
	room := max(width-labelWidth-1, 8)
	var lines []string
	for i, part := range wrapPlain(value, room) {
		head := strings.Repeat(" ", labelWidth)
		if i == 0 {
			head = faint.Render(label + strings.Repeat(" ", max(labelWidth-len(label), 0)))
		}
		lines = append(lines, head+" "+part)
	}
	return lines
}

// effectStatus says whether the effect runs and why.
func effectStatus(plan operation.Plan, effect operation.Effect) string {
	switch {
	case effect.Fixed:
		return "Always runs"
	case isLocked(plan, effect):
		parent := plan.Effects[effectIndex(plan, effect.Parent)]
		return "Will run, with " + effectTitle(parent)
	case effect.Checked:
		return "Will run"
	case effect.SavedSkip:
		return "Off: you turned it off before"
	case effect.Optional:
		return "Off: optional, turn on if you want it"
	}
	return "Off: you turned it off"
}

// relation says how a part and its parent belong together.
func relation(plan operation.Plan, effect operation.Effect) string {
	if parent := effectIndex(plan, effect.Parent); effect.Parent != "" && parent >= 0 {
		name := effectTitle(plan.Effects[parent])
		if isLocked(plan, effect) {
			return "Included, because " + name + " is on."
		}
		return "Part of " + name + ". Pick it on its own, or turn on " + name +
			" to get all of its parts."
	}
	parts := 0
	for _, other := range plan.Effects {
		if other.Parent == effect.Name {
			parts++
		}
	}
	if parts > 0 {
		name := effectTitle(effect)
		return "Includes the parts listed under it. To pick only some of them, leave " + name +
			" off and tick those parts."
	}
	return ""
}

// fileDetail is the panel of one file: its title and counts, its path and
// owner, then the diff, clipped to width, or wrapped to it for the full-screen
// page.
func fileDetail(edit operation.Edit, width int, wrap bool) []string {
	title := edit.Title
	if title == "" {
		title = homePath(edit.Path)
	}
	head := bold.Render(forTerm(title))
	if edit.Added+edit.Removed > 0 {
		head += " " + green.Render(forTerm("+"+strconv.Itoa(edit.Added))) + " " +
			red.Render(forTerm("−"+strconv.Itoa(edit.Removed)))
	}
	lines := []string{
		fit(head, width, false),
		faint.Render(fit(forTerm(homePath(edit.Path)+" · "+fileOwner(edit)), width, false)),
	}
	if edit.EditedOutside {
		lines = append(lines, yellow.Render(fit(forTerm(editedNote), width, false)))
	}
	lines = append(lines, "")
	if edit.Diff == "" {
		note := "No text diff to show."
		if edit.Summary != "" {
			note = edit.Summary + ". No text diff to show."
		}
		return append(lines, faint.Render(fit(forTerm(note), width, false)))
	}
	for line := range strings.SplitSeq(strings.TrimSuffix(edit.Diff, "\n"), "\n") {
		style := diffStyle(line)
		if !wrap {
			lines = append(lines, style.Render(fit(forTerm(line), width, false)))
			continue
		}
		for _, part := range wrapPlain(line, width) {
			lines = append(lines, style.Render(part))
		}
	}
	if edit.DiffTruncated {
		lines = append(lines, faint.Render(forTerm("… the diff is shortened")))
	}
	return lines
}

// diffStyle colours a unified diff line: added green, removed red, hunk and
// file headers faint, context as it is.
func diffStyle(line string) lipgloss.Style {
	switch {
	case strings.HasPrefix(line, "+++"), strings.HasPrefix(line, "---"),
		strings.HasPrefix(line, "@@"):
		return faint
	case strings.HasPrefix(line, "+"):
		return green
	case strings.HasPrefix(line, "-"):
		return red
	}
	return lipgloss.NewStyle()
}

// renderDiff is the full-screen unified diff of one file, styled and wrapped at
// width columns; the caller scrolls it.
func renderDiff(edit operation.Edit, width int) []string {
	lines := fileDetail(edit, width, true)
	for i, line := range lines {
		lines[i] = trimEnd(line)
	}
	return lines
}
