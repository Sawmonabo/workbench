package cli

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// writeText writes prose word-wrapped to width under indent (wrapLines).
func writeText(b *strings.Builder, width, indent int, text string) {
	for _, line := range wrapLines(text, width, indent) {
		b.WriteString(line + "\n")
	}
}

// wrapLines is text word-wrapped to width under indent, with the glyphs the
// terminal shows and each line's trailing spaces trimmed; a word longer than
// the line is broken, and the line is never narrower than one cell.
func wrapLines(text string, width, indent int) []string {
	pad := strings.Repeat(" ", indent)
	lines := strings.Split(ansi.Wrap(glyphs.text.Replace(text), max(width-indent, 1), ""), "\n")
	for i, line := range lines {
		lines[i] = strings.TrimRight(pad+line, " ")
	}
	return lines
}

// labelledLines is value beside head, a label as it is drawn: value wraps to
// the room past head, at least least cells, each line after the first under
// the first's value.
func labelledLines(head, value string, width, least int) []string {
	headWidth := ansi.StringWidth(head)
	lines := wrapLines(value, max(width-headWidth, least), 0)
	for i := range lines {
		if i == 0 {
			lines[i] = head + lines[i]
		} else {
			lines[i] = strings.Repeat(" ", headWidth) + lines[i]
		}
	}
	return lines
}

// writeBold is writeText in bold.
func writeBold(b *strings.Builder, width, indent int, text string) {
	writeStyled(b, width, indent, text, bold)
}

// writeFaint is writeText for secondary notes.
func writeFaint(b *strings.Builder, width, indent int, text string) {
	writeStyled(b, width, indent, text, faint)
}

// writeStyled wraps text like writeText and styles each line, so a wrapped
// note never carries a style across a line break.
func writeStyled(b *strings.Builder, width, indent int, text string, style lipgloss.Style) {
	for _, line := range wrapLines(text, width, indent) {
		if line != "" {
			line = style.Render(line)
		}
		b.WriteString(line + "\n")
	}
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
