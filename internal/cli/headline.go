package cli

import (
	"strings"

	"github.com/charmbracelet/x/ansi"

	"github.com/Sawmonabo/workbench/internal/operation"
)

// bodyIndent is how far a body's lines stand in from their headline.
const bodyIndent = 4

// fixLabel labels a body's first fix line.
const fixLabel = "fix"

// headlineLines is the one drawing of a headline with its body, for a
// result line, a warning, an error, a doctor check and a plan note: mark (as
// drawn, "" for none) and headline, wrapped to width under the headline's
// text; then each labeled line and the fix lines, the first labeled fix,
// indented bodyIndent with the labels in one column and each text wrapped
// under its text, never under its label. The labels are faint where paint
// colors.
func headlineLines(
	width int,
	paint painter,
	mark, headline string,
	body operation.Body,
) []string {
	lines := hanging(mark, headline, width)
	type row struct{ label, text string }
	rows := make([]row, 0, len(body.Lines)+len(body.Fix))
	for _, line := range body.Lines {
		rows = append(rows, row{line.Label, line.Text})
	}
	for i, fix := range body.Fix {
		label := ""
		if i == 0 {
			label = fixLabel
		}
		rows = append(rows, row{label, fix})
	}
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
		lines = append(lines, hanging(pad+label+"  ", r.text, width)...)
	}
	return lines
}

// hanging is text after lead, word-wrapped to width with each line after the
// first under the first's text.
func hanging(lead, text string, width int) []string {
	if lead == "" {
		return wrapLines(text, width, 0)
	}
	return labelledLines(lead, text, width, 10)
}

// writeHeadline writes headlineLines to b.
func writeHeadline(
	b *strings.Builder,
	width int,
	paint painter,
	mark, headline string,
	body operation.Body,
) {
	for _, line := range headlineLines(width, paint, mark, headline, body) {
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
