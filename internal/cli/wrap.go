package cli

import (
	"os"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/Sawmonabo/workbench/internal/display"
)

// This file is the one rule for where text breaks when it is fitted to a
// width: only at a space between two words. A word is never broken at a
// hyphen or anywhere else inside it; a path is one word, with the spaces in
// its names (see joinPaths); and a fix line's words are the words a shell
// reads (see commandWords). A word wider than the line is kept whole or
// broken as the output needs (see longWord).

// longWord is what a wrap does with a word wider than its line.
type longWord int

const (
	// keepLong leaves the word whole past the width, in printed output: the
	// terminal shows the rest on its next row and copies the two rows as one
	// line, so a copied path or command stays whole, as fmt(1) keeps a long
	// word whole on a line of its own.
	keepLong longWord = iota
	// breakLong breaks the word after the last slash that fits, else at the
	// width, in a full-screen view, whose lines never pass its edge.
	breakLong
)

// writeText writes prose word-wrapped to width under indent (wrapLines).
func writeText(b *strings.Builder, width, indent int, text string) {
	for _, line := range wrapLines(text, width, indent) {
		b.WriteString(line + "\n")
	}
}

// wrapLines is text word-wrapped to width under indent, with the glyphs the
// terminal shows and each line's trailing spaces trimmed; each line of text
// starts a line, a word longer than the line is broken (breakLong), and the
// line is never narrower than one cell.
func wrapLines(text string, width, indent int) []string {
	pad := strings.Repeat(" ", indent)
	lines := proseLines(text, max(width-indent, 1), breakLong)
	for i, line := range lines {
		lines[i] = strings.TrimRight(pad+line, " ")
	}
	return lines
}

// labelledLines is value beside head, a label as it is drawn: value wraps to
// the room past head, at least least cells, each line after the first under
// the first's value; a word longer than the room is broken (breakLong).
func labelledLines(head, value string, width, least int) []string {
	return under(head, wrapLines(value, max(width-ansi.StringWidth(head), least), 0))
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

// hanging is text after lead, word-wrapped to width with each line after the
// first under the first's text, at least 10 cells of it, a word wider than
// that treated as long says.
func hanging(lead, text string, width int, long longWord) []string {
	room := max(width-ansi.StringWidth(lead), 10)
	if lead == "" {
		room = max(width, 1)
	}
	lines := proseLines(text, room, long)
	for i, line := range lines {
		lines[i] = strings.TrimRight(line, " ")
	}
	return under(lead, lines)
}

// fixCommandLines is command after lead, wrapped to width only between the
// words a shell reads (see commandWords), each line after the first under
// the first's text, at least 10 cells of it; a word wider than that is
// treated as long says.
func fixCommandLines(lead, command string, width int, long longWord) []string {
	room := max(width-ansi.StringWidth(lead), 10)
	return under(lead, fitWords(commandWords(glyphs.text.Replace(command)), room, long))
}

// under is lines after lead: the first beside it, the rest under the first.
func under(lead string, lines []string) []string {
	pad := strings.Repeat(" ", ansi.StringWidth(lead))
	for i := range lines {
		if i == 0 {
			lines[i] = lead + lines[i]
		} else {
			lines[i] = pad + lines[i]
		}
	}
	return lines
}

// proseLines is text in the terminal's glyphs, each of its lines wrapped to
// room on its own (see fitWords), a path in it kept as one word (joinPaths).
func proseLines(text string, room int, long longWord) []string {
	var lines []string
	for paragraph := range strings.SplitSeq(glyphs.text.Replace(text), "\n") {
		lines = append(lines, fitWords(joinPaths(spacedWords(paragraph)), room, long)...)
	}
	return lines
}

// word is one run of text a wrap never breaks inside, with the spaces before
// it, which a line break drops.
type word struct{ space, text string }

// spacedWords splits text at its spaces.
func spacedWords(text string) []word {
	var words []word
	for text != "" {
		rest := strings.TrimLeft(text, " ")
		space := text[:len(text)-len(rest)]
		end := strings.IndexByte(rest, ' ')
		if end < 0 {
			end = len(rest)
		}
		words = append(words, word{space: space, text: rest[:end]})
		text = rest[end:]
	}
	return words
}

// commandWords splits command into the words a shell reads: at each space
// outside '…' and $'…' quotes and not after a backslash, as operation's
// shellWord writes a word.
func commandWords(command string) []word {
	var words []word
	const (
		bare = iota
		single
		ansiC
	)
	state, escaped := bare, false
	var space, text strings.Builder
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
			if text.Len() > 0 {
				words = append(words, word{space: space.String(), text: text.String()})
				space.Reset()
				text.Reset()
			}
			space.WriteRune(r)
			continue
		}
		text.WriteRune(r)
	}
	if text.Len() > 0 {
		words = append(words, word{space: space.String(), text: text.String()})
	}
	return words
}

// fitWords packs words into lines of at most room cells, breaking only
// between two words and dropping the spaces at a break; the text's leading
// spaces stay. A word wider than room starts a line of its own and is kept
// whole past room or broken (cutWord), as long says. Text with no word is one
// empty line.
func fitWords(words []word, room int, long longWord) []string {
	var lines []string
	line, used := "", 0
	for i, w := range words {
		space, text := w.space, w.text
		if line == "" && i > 0 {
			space = ""
		}
		cells := ansi.StringWidth(text)
		if line != "" && used+ansi.StringWidth(space)+cells > room {
			lines = append(lines, line)
			line, used, space = "", 0, ""
		}
		for long == breakLong && used+ansi.StringWidth(space)+cells > room {
			head, rest := cutWord(text, room-used-ansi.StringWidth(space))
			lines = append(lines, line+space+head)
			line, used, space, text = "", 0, "", rest
			cells = ansi.StringWidth(text)
		}
		line += space + text
		used += ansi.StringWidth(space) + cells
	}
	return append(lines, line)
}

// cutWord is the head of text that fits in room cells, cut after the last
// slash in it that leaves something before it, else after as many cells as
// fit (at least one character), and the rest.
func cutWord(text string, room int) (head, rest string) {
	fits := ansi.Strip(ansi.Truncate(text, max(room, 1), ""))
	cells := ansi.StringWidth(fits)
	if slash := strings.LastIndexByte(fits, '/'); slash > 0 {
		cells = ansi.StringWidth(fits[:slash+1])
	}
	if cells == 0 { // the first character is wider than room
		_, cells = ansi.FirstGraphemeCluster(ansi.Strip(text), ansi.GraphemeWidth)
	}
	return ansi.Cut(text, 0, cells), ansi.Cut(text, cells, ansi.StringWidth(text))
}

// pathReach is how many words after a path's word joinPaths reads as more of
// it: a name in a path may hold that many spaces.
const pathReach = 4

// joinPaths joins each path in words into one word, with the spaces in its
// names. A path starts at a word that begins with ~/ or /, after any opening
// bracket or quote, as display.Path writes one and as an error names one; a
// space after it is part of it when the name it would then hold exists in
// the path's folder (see pathName), so the text after a path is never read
// as more of it. A path ends at a word ending in a closing mark (,;:.)]'"`).
func joinPaths(words []word) []word {
	home, homeRead := "", false
	expand := func(path string) string {
		if !homeRead {
			home, homeRead = display.Home(), true
		}
		return expandHome(home, path)
	}
	joined := make([]word, 0, len(words))
	path := "" // the path the last joined word ends inside, "" when none
	for i := 0; i < len(words); i++ {
		if path != "" {
			if n, next := pathName(path, words[i:], expand); n > 0 {
				last := &joined[len(joined)-1]
				for _, w := range words[i : i+n] {
					last.text += w.space + w.text
				}
				path, i = next, i+n-1
				continue
			}
		}
		joined = append(joined, words[i])
		path = pathStart(words[i].text)
	}
	return joined
}

// pathStart is the path text begins, "" when it begins none or the path ends
// with it: text after any opening bracket or quote, when that starts with ~/
// or / and text does not end in a closing mark.
func pathStart(text string) string {
	path := strings.TrimLeft(text, `(["'`+"`")
	if !strings.HasPrefix(path, "~/") && !strings.HasPrefix(path, "/") ||
		strings.TrimRight(path, closingMarks) != path {
		return ""
	}
	return path
}

// closingMarks end a path: what may follow one in a sentence.
const closingMarks = `,;:.)]'"` + "`"

// pathName reads words, which follow path, as more of it: n is how many of
// them, the fewest whose joined text with path names an entry that exists,
// up to the first slash or closing mark in the last, none when no such entry
// exists within pathReach words or before a word that holds a slash; next
// is the path after them, "" when it ends there.
func pathName(path string, words []word, expand func(string) string) (n int, next string) {
	name := path
	for n = 1; n <= min(pathReach, len(words)); n++ {
		w := words[n-1]
		part, _, slash := strings.Cut(w.text, "/")
		trimmed := strings.TrimRight(part, closingMarks)
		if _, err := os.Lstat(expand(name + w.space + trimmed)); err == nil {
			if strings.TrimRight(w.text, closingMarks) != w.text {
				return n, "" // the path ends with this word
			}
			return n, name + w.space + w.text
		}
		if slash || trimmed != part {
			break
		}
		name += w.space + w.text
	}
	return 0, ""
}

// expandHome is path with a leading ~ written out as home; any other path,
// and every path when home is empty, as it is.
func expandHome(home, path string) string {
	if home == "" {
		return path
	}
	if path == "~" {
		return home
	}
	if rest, ok := strings.CutPrefix(path, "~/"); ok {
		return home + "/" + rest
	}
	return path
}
