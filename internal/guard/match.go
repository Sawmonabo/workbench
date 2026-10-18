package guard

import (
	"cmp"
	"maps"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/Sawmonabo/workbench/internal/platform"
)

// shells are the shells an agent runs commands and hooks through.
var shells = map[string]bool{"sh": true, "bash": true, "zsh": true, "dash": true}

// nulRedirect is Claude Code's rewrite of a Windows-style redirect to nul
// (any case) into /dev/null, which it skips for a command holding <, $ or a
// backquote. Its lookahead for the character after nul is the second group.
var nulRedirect = regexp.MustCompile(`(\d?&?>+[ \t]*)[Nn][Uu][Ll](\s|$|[|&;)\n])`)

// claudeForms are the texts Claude Code may run for a command: as written,
// and with its nul rewrite applied.
func claudeForms(command string) []string {
	forms := []string{command}
	if strings.ContainsAny(command, "<$`") {
		return forms
	}
	if rewritten := nulRedirect.ReplaceAllString(
		command,
		"${1}/dev/null${2}",
	); rewritten != command {
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

// shellScript is the script of a shell an agent runs a command with:
// `<shell> -c <script>`, Codex's `-lc`, `-fc` and `-lfc`, and Claude Code's
// `<shell> -c -l <script>` when it has no shell snapshot.
func shellScript(args []string) (string, bool) {
	if len(args) < 3 || !shells[filepath.Base(args[0])] {
		return "", false
	}
	switch {
	case len(args) == 3 && (args[1] == "-c" || args[1] == "-lc" || args[1] == "-fc" || args[1] == "-lfc"):
		return args[2], true
	case len(args) == 4 && args[1] == "-c" && args[2] == "-l":
		return args[3], true
	}
	return "", false
}

// callIDVariable is the environment variable Codex 0.163.0 and later set on
// each command to its call's tool_use_id.
const callIDVariable = "CODEX_TOOL_CALL_ID"

// runs reports whether p is exactly what an agent of the note's kind runs for
// the note's text. Claude Code: a shell script that evaluates the text as it
// quotes it. Codex: a shell whose script is the text or, because zsh and bash
// exec a single simple command in place, a process whose argv is the text's
// plain words and that leads its own group or, launched below a sandbox
// launcher, its launcher's.
func runs(n note, p proc, launched bool) bool {
	script, shell := shellScript(p.Args)
	switch n.Kind {
	case claude:
		if !shell {
			return false
		}
		for _, form := range claudeForms(n.Command) {
			if evaluates(script, form) {
				return true
			}
		}
	case codex:
		if shell && script == n.Command {
			return true
		}
		words := simpleWords(n.Command)
		return words != nil && slices.Equal(p.Args, words) &&
			(p.PGID == p.PID || launched && p.PGID == p.PPID)
	}
	return false
}

// plainWord is the text Claude Code's shell quoting leaves bare.
var plainWord = regexp.MustCompile(`^[A-Za-z0-9_./:=@+,-]+$`)

// claudeQuoted is form as Claude Code quotes it after `eval `: bare when it
// is one plain word, else single-quoted with each ' as '"'"'.
func claudeQuoted(form string) string {
	if plainWord.MatchString(form) {
		return form
	}
	return "'" + strings.ReplaceAll(form, "'", `'"'"'`) + "'"
}

// evaluates reports whether script holds `eval <form>` as a whole command:
// Claude Code follows it with ` < /dev/null` or ` && pwd -P ...`, so a longer
// command starting with the same text (`echo'hi'` for `echo`, `makefile` for
// `make`) is followed by another character.
func evaluates(script, form string) bool {
	text := "eval " + claudeQuoted(form)
	for from := 0; ; {
		i := strings.Index(script[from:], text)
		if i < 0 {
			return false
		}
		end := from + i + len(text)
		if end == len(script) || script[end] == ' ' {
			return true
		}
		from += i + 1
	}
}

// launchers start an agent's command inside a sandbox: Codex on Linux runs
// codex-linux-sandbox, which runs bwrap, which runs codex-linux-sandbox
// again, which starts the command in its own group.
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
// only when it started no earlier than one second before the note's pre hook
// and claims the note. Start times are compared in the
// kernel's own units, never against the wall clock. A child in taken, or
// still carrying its agent's command line (forked, not yet exec'd), is
// skipped; the next tick looks again. Notes go oldest first, each to its
// oldest matching child whose command runs in the note's cd folder, else in
// its cwd (noteFolders, see folderRank), else to its oldest matching child,
// so of two notes with the same text the one from the other folder is not
// given the other's command, and otherwise the earlier gets the earlier
// child. The folder only prefers: one that is unknown, unreadable or reached
// through a link, ~ or a variable leaves the choice as it was and never
// leaves a command untracked. A child whose command line matches no note is
// never tracked. setsCallIDs are the Codex agents seen to give a command its
// own call ID (see claims).
func match(s snapshot, notes []note, taken map[int]bool, setsCallIDs map[procKey]bool) []command {
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
		earliest := n.NotedStart - min(n.NotedStart, platform.StartSecond)
		dirs := noteFolders(n)
		var chosen proc
		best := -2
		for _, child := range s.children(agent.PID) {
			if used[child.PID] || child.Start < earliest || slices.Equal(child.Args, agent.Args) {
				continue
			}
			if !claims(s, n, agent, child, earliest, setsCallIDs[procKey{agent.PID, agent.Start}]) {
				continue
			}
			if rank := s.folderRank(child, dirs); rank > best {
				chosen, best = child, rank
			}
			if best == len(dirs)-1 {
				break
			}
		}
		if best > -2 {
			used[chosen.PID] = true
			out = append(out, command{Note: n, Root: chosen})
		}
	}
	return out
}

// folderRank is the index in dirs (noteFolders: cwd, then the leading cd's
// folder) of the last folder that a process which may run a note's command
// for child (commandProcesses) has as its known working folder, or -1 for
// none. A shell in the cd's folder has run that cd, so it ranks above one
// still in cwd, which another call with the same text from that cwd may be.
func (s snapshot) folderRank(child proc, dirs []string) int {
	rank := -1
	for _, p := range s.commandProcesses(child) {
		if p.Folder != "" {
			rank = max(rank, slices.Index(dirs, p.Folder))
		}
	}
	return rank
}

// claims reports whether child runs n's command. A child of a Codex agent
// with a CallID of its own, not the one it inherited with the agent's
// environment, is that call's command and no other's. A Codex agent that sets
// call IDs (setsCallIDs) gives one to every command, so under it only a child
// carrying n's own ID claims n: its git helpers lead their own groups too and
// may run a note's exact words. Otherwise the process running the command
// (child, or the first below its sandbox launchers) must have started no
// earlier than earliest and have exactly the shape n's agent uses for n's
// text.
func claims(s snapshot, n note, agent, child proc, earliest uint64, setsCallIDs bool) bool {
	ownID := child.CallID != "" && child.CallID != agent.CallID
	if n.Kind == codex && (ownID || setsCallIDs) {
		return ownID && child.CallID == n.ToolUseID
	}
	return slices.ContainsFunc(s.commandProcesses(child), func(p proc) bool {
		return p.Start >= earliest && runs(n, p, p.PID != child.PID)
	})
}

// targets chooses what to signal to stop the tracked command whose root is
// root, from one snapshot: the root and its descendants, leaving out every
// protected process (agents that left a note, the watcher) with everything
// under it, PID 0 and 1, and zombies, which hold no memory. A process group
// is signalled as a group only when its leader is live and it and every
// process in the group are among those; any other process is signalled on
// its own. A root that is gone, or whose PID now names another process,
// gives nothing.
func targets(s snapshot, root proc, protected map[int]uint64) []target {
	if current, ok := s[root.PID]; !ok || current.Start != root.Start {
		return nil
	}
	members := s.tree(root.PID, outside(protected))
	inGroup := map[int]int{}
	for _, p := range members {
		inGroup[p.PGID]++
	}
	machineGroup := map[int]int{}
	for _, p := range s {
		if inGroup[p.PGID] > 0 {
			machineGroup[p.PGID]++
		}
	}
	// A leader still in its group is counted in machineGroup, so the counts
	// agree only when it is among the members too. One that moved to another
	// group would take the signal there through a Linux pidfd.
	whole := func(group int) bool {
		leader, ok := s[group]
		return group > 1 && ok && !leader.Zombie && leader.PGID == group &&
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
