package guard

import (
	"cmp"
	"maps"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// shells are the shells an agent runs commands and hooks through.
var shells = map[string]bool{"sh": true, "bash": true, "zsh": true, "dash": true}

// nulRedirect is a Windows-style redirect to nul, which Claude Code rewrites
// to /dev/null before it runs a command.
var nulRedirect = regexp.MustCompile(`((?:\d|&)?>{1,2})\s*nul\b`)

// claudeForms are the texts Claude Code may run for a command: as written,
// and with its nul rewrite applied.
func claudeForms(command string) []string {
	forms := []string{command}
	if rewritten := nulRedirect.ReplaceAllString(command, "${1}/dev/null"); rewritten != command {
		forms = append(forms, rewritten)
	}
	return forms
}

// plainByte is a character a simple command may hold unquoted.
func plainByte(c byte) bool {
	return 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' || '0' <= c && c <= '9' ||
		strings.IndexByte("_./:=+,@%-", c) >= 0
}

// simpleWords splits a command that is one simple command of plain words into
// the argv its shell execs it with: unquoted plain characters, and single- or
// double-quoted text without $, backquote or backslash. Any other command
// gives nil, because its words depend on expansion, redirection or more than
// one command.
func simpleWords(command string) []string {
	var words []string
	var word strings.Builder
	inWord := false
	for i := 0; i < len(command); i++ {
		switch c := command[i]; {
		case c == ' ' || c == '\t':
			if inWord {
				words, inWord = append(words, word.String()), false
				word.Reset()
			}
		case c == '\'' || c == '"':
			end := strings.IndexByte(command[i+1:], c)
			if end < 0 {
				return nil
			}
			quoted := command[i+1 : i+1+end]
			if c == '"' && strings.ContainsAny(quoted, "$`\\") {
				return nil
			}
			word.WriteString(quoted)
			inWord, i = true, i+1+end
		case plainByte(c):
			word.WriteByte(c)
			inWord = true
		default:
			return nil
		}
	}
	if inWord {
		words = append(words, word.String())
	}
	return words
}

// runs reports whether p is exactly what an agent of the note's kind runs for
// the note's text. Claude Code: `<shell> -c <script>` whose script evaluates
// the text single-quoted ('"'"' for each quote). Codex: `<shell> -c <text>`,
// or, because zsh and bash exec a single simple command in place, a process
// leading its own group whose argv is the text's plain words.
func runs(n note, p proc) bool {
	args := p.Args
	shell := len(args) == 3 && args[1] == "-c" && shells[filepath.Base(args[0])]
	switch n.Kind {
	case claude:
		if !shell {
			return false
		}
		for _, form := range claudeForms(n.Command) {
			if evaluates(args[2], form) {
				return true
			}
		}
	case codex:
		if shell && args[2] == n.Command {
			return true
		}
		words := simpleWords(n.Command)
		return words != nil && p.PGID == p.PID && slices.Equal(args, words)
	}
	return false
}

// evaluates reports whether script holds `eval '<form>'` as a whole command.
// Claude's quote escape '"'"' also starts with a quote, so a longer command
// (`echo'hi'` for the text `echo`) holds the same text followed by that
// escape; a finished command's closing quote is followed by ` && pwd ...` or
// the end of the script, never by a double quote.
func evaluates(script, form string) bool {
	text := "eval '" + strings.ReplaceAll(form, "'", `'"'"'`) + "'"
	for from := 0; ; {
		i := strings.Index(script[from:], text)
		if i < 0 {
			return false
		}
		end := from + i + len(text)
		if end == len(script) || script[end] != '"' {
			return true
		}
		from += i + 1
	}
}

// launchers start an agent's command inside a sandbox: Codex on Linux runs
// codex-linux-sandbox, which runs bwrap, which runs the shell.
var launchers = map[string]bool{"codex-linux-sandbox": true, "bwrap": true}

// commandProcesses are the processes that may run a note's command for one
// agent child: the child itself or, when it is a sandbox launcher, the first
// process below its chain of launchers. Nothing deeper counts, so a program
// the command itself starts (npm's own `sh -c jest`) never stands for a note.
func (s snapshot) commandProcesses(child proc) []proc {
	return s.launched(child, map[int]bool{})
}

// launched is commandProcesses with the PIDs already walked, so a parent loop
// from a non-atomic process scan ends the walk.
func (s snapshot) launched(child proc, seen map[int]bool) []proc {
	if seen[child.PID] {
		return nil
	}
	seen[child.PID] = true
	if len(child.Args) == 0 || !launchers[filepath.Base(child.Args[0])] {
		return []proc{child}
	}
	var out []proc
	for _, next := range s.children(child.PID) {
		out = append(out, s.launched(next, seen)...)
	}
	return out
}

// match pairs notes with the agent children that run them. A child is taken
// only when it, and the process running the command (the child itself or
// the first one below its sandbox launchers), started no earlier than one
// second before the note's pre hook, and that process has exactly the shape
// the note's agent uses for the note's text. Start times are compared in the
// kernel's own units, never against the wall clock. A child in taken, or
// still carrying its agent's command line (forked, not yet exec'd), is
// skipped; the next tick looks again. Notes go oldest first, each to its
// oldest matching child, so of two notes with the same text the earlier gets
// the earlier child. A child whose command line matches no note is never
// tracked.
func match(s snapshot, notes []note, taken map[int]bool) []command {
	notes = slices.Clone(notes)
	slices.SortFunc(notes, func(a, b note) int {
		return cmp.Or(
			cmp.Compare(a.NotedStart, b.NotedStart),
			cmp.Compare(a.ToolUseID, b.ToolUseID),
		)
	})
	used := maps.Clone(taken)
	if used == nil {
		used = map[int]bool{}
	}
	var out []command
	for _, n := range notes {
		agent, ok := s[n.AgentPID]
		if !ok || agent.Start != n.AgentStart {
			continue
		}
		earliest := n.NotedStart - min(n.NotedStart, startSlack)
		for _, child := range s.children(agent.PID) {
			if used[child.PID] || child.Start < earliest || slices.Equal(child.Args, agent.Args) {
				continue
			}
			if !slices.ContainsFunc(s.commandProcesses(child), func(p proc) bool {
				return p.Start >= earliest && runs(n, p)
			}) {
				continue
			}
			used[child.PID] = true
			out = append(out, command{Note: n, Root: child})
			break
		}
	}
	return out
}

// targets chooses what to signal to stop the tracked command whose root is
// root, from one snapshot: the root and its descendants, leaving out every
// protected process (agents that left a note, the watcher) with everything
// under it, PID 0 and 1, and zombies, which hold no memory. A process group
// is signalled as a group only when its leader is live and it and every
// process in the group are among those; any other process is signalled on
// its own. A root that is gone, or whose PID now names another process,
// gives nothing.
func targets(s snapshot, root proc, protected map[int]bool) []target {
	current, ok := s[root.PID]
	if root.PID <= 1 || !ok || current.Start != root.Start || protected[root.PID] {
		return nil
	}
	members := s.tree(root.PID, outside(protected))
	in := make(map[int]bool, len(members))
	inGroup := map[int]int{}
	for _, p := range members {
		in[p.PID] = true
		inGroup[p.PGID]++
	}
	machineGroup := map[int]int{}
	for _, p := range s {
		if inGroup[p.PGID] > 0 {
			machineGroup[p.PGID]++
		}
	}
	whole := func(group int) bool {
		leader, ok := s[group]
		return group > 1 && in[group] && ok && !leader.Zombie &&
			inGroup[group] == machineGroup[group]
	}
	var out []target
	signalled := map[int]bool{}
	for _, p := range members {
		switch {
		case p.Zombie:
		case !whole(p.PGID):
			out = append(out, target{PID: p.PID, Start: p.Start})
		case !signalled[p.PGID]:
			signalled[p.PGID] = true
			out = append(out, target{PID: p.PGID, Start: s[p.PGID].Start, Group: true})
		}
	}
	return out
}
