package cli

import (
	"strings"
)

// commandSteps splits a shell command into the steps a reader follows, as
// the Google Shell Style Guide lays out a long pipeline, one step per line
// (https://google.github.io/styleguide/shellguide.html#s5.5-pipelines), and
// as it does && and ||: a step after the first starts with the |, && or ||
// before it, and a ; ends its step and is dropped. Only operators that stand
// as tokens split: none inside single or double quotes, $' ' or backquotes,
// after a backslash, or inside $( ), ( ), ${ }, a { } group, [[ ]], where
// || and && are a test's own, or a case … esac, whose patterns and arms are
// its own; not the & of 2>&1, the |& that pipes both streams or the >| of a
// forced overwrite; and none in a comment, from a # that starts a word to
// the end of its line. A command
// holding a newline outside quotes, such as one with a here-document, is
// already laid out by its author, and its body is data: it stays one step,
// as does a command with no separator.
func commandSteps(command string) []string {
	var steps []string
	var step strings.Builder
	var open []string
	quote, op := byte(0), ""
	// cut ends the step, which op started, and starts one with next.
	cut := func(next string) {
		if text := strings.TrimSpace(step.String()); text != "" {
			steps = append(steps, strings.TrimSpace(op+" "+text))
		}
		step.Reset()
		op = next
	}
	for i := 0; i < len(command); i++ {
		n := quoted(&quote, command, i)
		if n == 0 {
			switch {
			case command[i] == '\n':
				return []string{command}
			case command[i] == '#' && wordStart(command, i):
				// A comment ends at the newline, which is then seen.
				if j := strings.IndexByte(command[i:], '\n'); j >= 0 {
					n = j
				} else {
					n = len(command) - i
				}
			default:
				n = nesting(&open, command, i)
			}
		}
		if n == 0 && len(open) == 0 {
			rest := command[i:]
			switch {
			case strings.HasPrefix(rest, "&&"), strings.HasPrefix(rest, "||"):
				cut(rest[:2])
				i++
				continue
			case strings.HasPrefix(rest, "|&"), strings.HasPrefix(rest, ">|"):
				n = 2
			case rest[0] == '|':
				cut("|")
				continue
			case rest[0] == ';':
				cut("")
				continue
			}
		}
		n = max(n, 1)
		step.WriteString(command[i : i+n])
		i += n - 1
	}
	cut("")
	if len(steps) == 0 {
		return []string{command}
	}
	return steps
}

// quoted is how many bytes of command from i are quoted text: an escaped
// byte, a byte inside quotes, or the quote that opens or closes them, as
// quote, the open quote or 0, records; $' ' is quote '$', in which a
// backslash escapes. It is 0 for a byte outside quotes.
func quoted(quote *byte, command string, i int) int {
	ch := command[i]
	switch {
	case *quote == '\'':
		if ch == '\'' {
			*quote = 0
		}
		return 1
	case ch == '\\' && i+1 < len(command) && (*quote != 0 || command[i+1] != '\n'):
		// A backslash before a newline outside quotes goes on the line
		// after, so the newline is seen.
		return 2
	case *quote != 0:
		if *quote == '$' && ch == '\'' || *quote != '$' && ch == *quote {
			*quote = 0
		}
		return 1
	case strings.HasPrefix(command[i:], "$'"):
		*quote = '$'
		return 2
	case ch == '\'' || ch == '"' || ch == '`':
		*quote = ch
		return 1
	}
	return 0
}

// nesting is how many bytes of command from i open or close something no
// operator inside splits, which open records: ( or $(, ${, a { group, [[ or
// case, each closed by its own ), }, }, ]] or esac. A {, [[ or case opens, and
// a group's }, a ]] or esac closes, only as a word of its own; a case
// pattern's ) closes nothing.
func nesting(open *[]string, command string, i int) int {
	top := ""
	if len(*open) > 0 {
		top = (*open)[len(*open)-1]
	}
	rest, word := command[i:], wordStart(command, i)
	switch {
	case strings.HasPrefix(rest, "${"):
		*open = append(*open, "${")
		return 2
	case rest[0] == '(':
		*open = append(*open, "(")
		return 1
	case rest[0] == '{' && word && blankAt(command, i+1):
		*open = append(*open, "{")
		return 1
	case strings.HasPrefix(rest, "[[") && word && blankAt(command, i+2):
		*open = append(*open, "[[")
		return 2
	case strings.HasPrefix(rest, "case") && word && blankAt(command, i+4):
		*open = append(*open, "case")
		return 4
	case rest[0] == ')' && top == "(",
		rest[0] == '}' && top == "${",
		rest[0] == '}' && top == "{" && word && wordEnd(command, i+1):
		*open = (*open)[:len(*open)-1]
		return 1
	case strings.HasPrefix(rest, "]]") && top == "[[" && word && wordEnd(command, i+2):
		*open = (*open)[:len(*open)-1]
		return 2
	case strings.HasPrefix(rest, "esac") && top == "case" && word && wordEnd(command, i+4):
		*open = (*open)[:len(*open)-1]
		return 4
	}
	return 0
}

// wordStart reports whether the byte at i of a command starts a word: it is
// the first, or follows a blank or an operator's |, &, ; or (.
func wordStart(command string, i int) bool {
	return i == 0 || strings.IndexByte(" \t;|&(", command[i-1]) >= 0
}

// wordEnd reports whether a word of a command ends at i: at the end, or
// before a blank or an operator's |, &, ; or ).
func wordEnd(command string, i int) bool {
	return i >= len(command) || strings.IndexByte(" \t;|&)", command[i]) >= 0
}

// blankAt reports whether the byte at i of a command is a space or a tab.
func blankAt(command string, i int) bool {
	return i < len(command) && (command[i] == ' ' || command[i] == '\t')
}
