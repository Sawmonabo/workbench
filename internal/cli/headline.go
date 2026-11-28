package cli

import (
	"strings"

	"github.com/charmbracelet/x/ansi"

	"github.com/Sawmonabo/workbench/internal/operation"
)

// bodyIndent is how far a body's lines stand in from their headline.
const bodyIndent = 4

// fixLabel and nextLabel label a body's first fix line and its first next
// line.
const (
	fixLabel  = "fix"
	nextLabel = "next"
)

// headlineLines is the one drawing of a headline with its body, for a
// result line, a run's summary, a warning, an error, a doctor check and a
// plan note: mark (as drawn, "" for none) and headline, wrapped to width
// under the headline's text, then bodyLines.
func headlineLines(
	width int,
	long longWord,
	paint painter,
	mark, headline string,
	body operation.Body,
) []string {
	return append(hanging(mark, headline, width, long), bodyLines(width, long, paint, body)...)
}

// bodyLines is what stands under a headline: each labeled line, then the
// fix lines, the first labeled fix, and the next lines, the first labeled
// next, indented bodyIndent with the labels in one column and each text
// wrapped under its text, never under its label, each line broken only
// between two words (see fitWords) and a word wider than its line treated
// as long says; a fix or next line is a command (see fixCommandLines). The
// labels are faint where paint colors.
func bodyLines(width int, long longWord, paint painter, body operation.Body) []string {
	type row struct {
		label, text string
		command     bool
	}
	rows := make([]row, 0, len(body.Lines)+len(body.Fix)+len(body.Next))
	for _, line := range body.Lines {
		rows = append(rows, row{line.Label, line.Text, false})
	}
	commands := func(label string, texts []string) {
		for i, text := range texts {
			if i > 0 {
				label = ""
			}
			rows = append(rows, row{label, text, true})
		}
	}
	commands(fixLabel, body.Fix)
	commands(nextLabel, body.Next)
	var lines []string
	column := 0
	for _, r := range rows {
		column = max(column, ansi.StringWidth(forTerm(r.label)))
	}
	pad := strings.Repeat(" ", bodyIndent)
	for _, r := range rows {
		label := forTerm(r.label)
		label += strings.Repeat(" ", column-ansi.StringWidth(label))
		if paint {
			label = faint.Render(label)
		}
		if !r.command {
			lines = append(lines, hanging(pad+label+"  ", r.text, width, long)...)
			continue
		}
		lines = append(lines, fixCommandLines(pad+label+"  ", r.text, width, long)...)
	}
	return lines
}

// writeSummary writes a successful run's summary with its body, in the one
// drawing: where paint colors, the summary green and bold after a check, as
// a success ends; plain, with no check, in plain output.
func writeSummary(b *strings.Builder, width int, paint painter, summary string, body operation.Body) {
	mark := ""
	if paint {
		mark = glyphs.Check + " "
	}
	for _, line := range hanging(mark, summary, width, keepLong) {
		b.WriteString(paint.style(green.Bold(true), line) + "\n")
	}
	for _, line := range bodyLines(width, keepLong, paint, body) {
		b.WriteString(line + "\n")
	}
}

// writeHeadline writes headlineLines to b, for printed output, which keeps a
// word wider than its line whole (keepLong).
func writeHeadline(
	b *strings.Builder,
	width int,
	paint painter,
	mark, headline string,
	body operation.Body,
) {
	for _, line := range headlineLines(width, keepLong, paint, mark, headline, body) {
		b.WriteString(line + "\n")
	}
}

// warningMark is what heads a warning: the word, yellow where paint colors.
func (p painter) warningMark() string { return p.style(yellow, "Warning:") + " " }

// errorMark is what heads an error: the cross, red where paint colors, in
// the terminal's glyphs, and kept in plain output, where it stands for the
// error.
func (p painter) errorMark() string {
	if !p {
		return glyphs.Cross + " "
	}
	return red.Render(glyphs.Cross) + " "
}

// writeWarnings writes each warning with its body, in the one drawing.
func writeWarnings(b *strings.Builder, width int, paint painter, warnings []operation.Warning) {
	for _, warning := range warnings {
		writeHeadline(b, width, paint, paint.warningMark(), warning.Message, warning.Body)
	}
}
