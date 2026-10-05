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
// effect, its name and status, what it does, then what it would do on this PC
// now, what it changes, who it runs as and how to undo it; for a file, its
// title, owner and unified diff, which diff leaves out when the output is not a
// terminal. It returns no lines for rowNone.
func renderDetail(plan operation.Plan, row planRow, width int, diff bool) []string {
	switch row.Kind {
	case rowFile:
		if row.Index < len(plan.Edits) {
			return fileDetail(plan.Edits[row.Index], width, false, diff)
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
	isNew := effect.New && !isAlready(plan, effect)
	if isNew {
		title += " " + badge.Render("NEW")
	}
	lines := []string{fit(title, width, false), faint.Render(forTerm(effectStatus(plan, effect)))}
	if isNew {
		lines = append(lines, faint.Render("New since your last apply"))
	}
	paragraph := func(s string) {
		if s == "" {
			return
		}
		lines = append(lines, "")
		lines = append(lines, wrapPlain(s, width)...)
	}
	paragraph(effect.What)
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
	case isAlready(plan, effect):
		return "Already set: nothing to change"
	case effect.Checked:
		return "Will run"
	case effect.SavedSkip:
		return "Off: you turned it off before"
	case effect.Optional:
		return "Off: optional, turn on if you want it"
	}
	return "Off: you turned it off"
}

// fileDetail is the panel of one file: its title and counts, its path and
// owner, then the diff, clipped to width, or wrapped to it for the full-screen
// page. The diff is file content, so it is shown only when diff is set, which
// is when the output is a terminal.
func fileDetail(edit operation.Edit, width int, wrap, diff bool) []string {
	title := edit.Title
	if title == "" {
		title = homePath(edit.Path)
	}
	head := bold.Render(forTerm(title))
	if edit.Semantic {
		head += " " + yellow.Render(forTerm(settingsCount(edit)))
	} else if edit.Added+edit.Removed > 0 {
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
	if edit.Diff != "" && !diff {
		return append(
			lines,
			faint.Render(fit(forTerm("The diff is shown only at a terminal."), width, false)),
		)
	}
	if edit.Semantic && edit.Diff == "" {
		return append(lines, faint.Render(fit(forTerm(settingsNote(edit)), width, false)))
	}
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
		// A continuation repeats the line's marker, so a wrapped added or removed
		// line still reads as one without colour.
		marker, text := " ", line
		if line != "" && strings.ContainsRune("+-~ ", rune(line[0])) {
			marker, text = line[:1], line[1:]
		}
		if strings.HasPrefix(line, "@@") {
			marker, text = "", line
		}
		for _, part := range wrapPlain(text, max(width-len(marker), 1)) {
			lines = append(lines, style.Render(marker+part))
		}
	}
	if edit.DiffTruncated {
		lines = append(lines, faint.Render(forTerm("… the diff is shortened")))
	}
	if edit.Semantic && edit.Rewritten {
		lines = append(lines, "")
		for _, part := range wrapPlain(settingsNote(edit), max(width, 1)) {
			lines = append(lines, faint.Render(forTerm(part)))
		}
	}
	return lines
}

// settingsCount is the row's count for a merged file shown as a list of
// settings: how many change, or that only its layout does.
func settingsCount(edit operation.Edit) string {
	switch edit.Settings {
	case 0:
		return "layout only"
	case 1:
		return "1 setting changed"
	}
	return strconv.Itoa(edit.Settings) + " settings changed"
}

// settingsNote says what else the merge does to the file beyond the listed
// settings.
func settingsNote(edit operation.Edit) string {
	if edit.Settings == 0 {
		return "No setting changes; the file is rewritten with its keys re-ordered."
	}
	return "The whole file is rewritten with its keys re-ordered; only the settings above change."
}

// diffStyle colours a unified diff line: added green, removed red, hunk headers
// faint, context as it is. The diff carries no file headers, so a removed line
// that itself starts with "--" is still red.
func diffStyle(line string) lipgloss.Style {
	switch {
	case strings.HasPrefix(line, "@@"):
		return faint
	case strings.HasPrefix(line, "~"):
		return yellow
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
	lines := fileDetail(edit, width, true, true)
	for i, line := range lines {
		lines[i] = trimEnd(line)
	}
	return lines
}
