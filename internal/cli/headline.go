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
	type row struct {
		label, text string
		command     bool
	}
	rows := make([]row, 0, len(body.Lines)+len(body.Fix))
	for _, line := range body.Lines {
		rows = append(rows, row{line.Label, line.Text, false})
	}
	for i, fix := range body.Fix {
		label := ""
		if i == 0 {
			label = fixLabel
		}
		rows = append(rows, row{label, fix, true})
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
		if !r.command {
			lines = append(lines, hanging(pad+label+"  ", r.text, width)...)
			continue
		}
		lines = append(lines, fixCommandLines(pad+label+"  ", r.text, width)...)
	}
	return lines
}

// fixCommandLines is command after lead, wrapped to width only between the words
// a shell reads, each line after the first under the first's text: a word
// wider than the room stays whole, past width, as a terminal shows a long
// line, so a copied command keeps every word as it is.
func fixCommandLines(lead, command string, width int) []string {
	room := max(width-ansi.StringWidth(lead), 10)
	wrapped := ansi.Wordwrap(glyphs.text.Replace(keepQuoted(command)), room, "")
	lines := strings.Split(strings.ReplaceAll(wrapped, nbsp, " "), "\n")
	for i, line := range lines {
		if i == 0 {
			lines[i] = lead + line
		} else {
			lines[i] = strings.Repeat(" ", ansi.StringWidth(lead)) + line
		}
	}
	return lines
}

// nbsp is the space the wrap never breaks at.
const nbsp = "\u00a0"

// keepQuoted is command with each space a shell reads as part of a word (in
// '…' or $'…' quotes, or after a backslash, as operation's shellWord writes
// them) one the wrap never breaks at, so a fix line breaks only between the
// words a shell reads.
func keepQuoted(command string) string {
	var b strings.Builder
	const (
		bare = iota
		single
		ansiC
	)
	state, escaped := bare, false
	runes := []rune(command)
	for i, r := range runes {
		switch {
		case escaped:
			escaped = false
		case r == '\\' && state != single:
			escaped = true
		case r == '\'' && state == bare:
			state = single
			if i > 0 && runes[i-1] == '$' {
				state = ansiC
			}
		case r == '\'':
			state = bare
		case r == ' ' && state == bare:
			b.WriteRune(r)
			continue
		}
		if r == ' ' {
			b.WriteString(nbsp)
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
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
