package operation

import (
	"path/filepath"
	"strconv"
	"strings"
	"unicode"

	"github.com/Sawmonabo/workbench/internal/display"
)

// Line is one labeled line under a headline: the cause, or a topic a doctor
// check names in its own words.
type Line struct {
	Label string `json:"label"`
	Text  string `json:"text"`
}

// Body is what stands under a headline (an [Error]'s, a [Component]'s or a
// [Warning]'s message): labeled lines, then the fix lines, each a command to
// type or, where no command exists, one short instruction. The text view
// draws it under the headline, the fix lines last; --json carries it as
// lines and fix, each absent when empty.
type Body struct {
	Lines []Line   `json:"lines,omitempty"`
	Fix   []string `json:"fix,omitempty"`
}

// Part is a labeled line or a fix line, as [Fail], [Explain] and [Warn] take
// them.
type Part interface{ addTo(b *Body) }

func (l Line) addTo(b *Body) { b.Lines = append(b.Lines, l) }

// fixLine is one fix line of a [Body].
type fixLine string

func (f fixLine) addTo(b *Body) { b.Fix = append(b.Fix, string(f)) }

// CauseLabel labels the line that says why, when the headline does not.
const CauseLabel = "cause"

// Cause is the line that says why the headline holds, when it does not say
// so itself. It does not repeat the headline's path.
func Cause(text string) Line { return Line{Label: CauseLabel, Text: text} }

// Changed is the line that says what a refusal or failure changed, such as
// "nothing", when the headline does not say so itself.
func Changed(text string) Line { return Line{Label: "changed", Text: text} }

// Kept is the line that says what a refusal or failure left as it was, such
// as "the installed runtime", when the headline does not say so itself.
func Kept(text string) Line { return Line{Label: "kept", Text: text} }

// Outcome is the line that says what the headline leads to, such as a file
// Git will not commit, when the headline does not say so itself.
func Outcome(text string) Line { return Line{Label: "result", Text: text} }

// Fix is a fix line: the command of words, each written as one word a shell
// reads back as it (see shellWord).
func Fix(words ...string) Part { return fixLine(shellWords(words)) }

// FixPath is a fix line: the command of words, then path written from the
// home folder as one word a shell reads back (see shellHomePath).
func FixPath(path string, words ...string) Part {
	return fixLine(shellWords(words) + " " + shellHomePath(path))
}

// FixText is a fix line already written: a command whose words are already
// shell words, such as one led by [Context.WorkbenchCommand], or, where no
// command exists, one short instruction ("move it aside").
func FixText(text string) Part { return fixLine(text) }

func shellWords(words []string) string {
	quoted := make([]string, len(words))
	for i, word := range words {
		quoted[i] = shellWord(word)
	}
	return strings.Join(quoted, " ")
}

// shellHomePath is [Context.ShellPath] from the current user's home folder,
// as [display.Home] finds it, for a message made where no [Context] is at
// hand.
func shellHomePath(path string) string { return shellPath(display.Home(), path) }

// shellPath is path as one word a shell reads back, written from home as
// [Context.ShellPath] says. An empty home, or /, shortens nothing.
func shellPath(home, path string) string {
	if home != "" && filepath.Clean(home) != "/" && Within(home, path) {
		if relative, err := filepath.Rel(home, path); err == nil {
			if relative == "." {
				return "~"
			}
			return "~/" + shellWord(filepath.ToSlash(relative))
		}
	}
	return shellWord(path)
}

// shellSafe are the characters a POSIX shell reads as themselves anywhere in
// a word that does not start with them.
const shellSafe = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_./,+@:"

// shellWord is s as one word a shell reads back as s: as it is when it holds
// only shellSafe characters, otherwise in single quotes (see shellQuote), and
// one holding a control character as $'...' with every byte outside printable
// ASCII written \xHH, so it stays on one line and prints no control
// character, which bash, zsh and ksh read.
func shellWord(s string) string {
	switch {
	case s != "" && strings.Trim(s, shellSafe) == "":
		return s
	case !strings.ContainsFunc(s, unicode.IsControl):
		return shellQuote(s)
	}
	var b strings.Builder
	b.WriteString("$'")
	for i := range len(s) {
		switch c := s[i]; {
		case c == '\\' || c == '\'':
			b.WriteByte('\\')
			b.WriteByte(c)
		case c < ' ' || c >= 0x7f:
			b.WriteString(`\x` + strconv.FormatUint(uint64(c)|0x100, 16)[1:])
		default:
			b.WriteByte(c)
		}
	}
	b.WriteByte('\'')
	return b.String()
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// Explain is the body parts make, in their order: the labeled lines, then
// the fix lines.
func Explain(parts ...Part) Body {
	var b Body
	for _, part := range parts {
		part.addTo(&b)
	}
	return b
}

// With is b with the lines and fix lines of more after its own.
func (b Body) With(more Body) Body {
	return Body{
		Lines: append(append([]Line(nil), b.Lines...), more.Lines...),
		Fix:   append(append([]string(nil), b.Fix...), more.Fix...),
	}
}

// Empty reports a body with no line and no fix line.
func (b Body) Empty() bool { return len(b.Lines) == 0 && len(b.Fix) == 0 }

// Sentence is headline and b in one line, for a log, an error that wraps
// another, a filter's one line on standard error and a probe line: the
// headline, then after a colon each line (a cause by its text alone, any
// other line after its label), then "fix:" and the fix lines, each part set
// off by "; ". With no headline the lines stand alone.
func (b Body) Sentence(headline string) string {
	var said []string
	for _, line := range b.Lines {
		if line.Label == CauseLabel {
			said = append(said, line.Text)
		} else {
			said = append(said, line.Label+" "+line.Text)
		}
	}
	if len(b.Fix) > 0 {
		said = append(said, "fix: "+strings.Join(b.Fix, "; "))
	}
	switch {
	case len(said) == 0:
		return headline
	case headline == "":
		return strings.Join(said, "; ")
	}
	return headline + ": " + strings.Join(said, "; ")
}
