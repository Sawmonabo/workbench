package operation

import "strings"

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
// another, and a filter's one line on standard error: the headline, then
// after a colon each line (a cause by its text alone, any other line after
// its label), then "fix:" and the fix lines, each part set off by "; ".
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
	if len(said) == 0 {
		return headline
	}
	return headline + ": " + strings.Join(said, "; ")
}
