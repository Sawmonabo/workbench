package guard

import (
	"maps"
	"slices"
	"testing"
)

// The guard may only ever stop a command an agent ran through its shell tool.
// Matching the wrong process (a git or rg the harness runs, a hook, an MCP
// server, a program the command itself started) or signalling the wrong one
// (an agent that left a note, the watcher, a process sharing the command's
// group, a PID reused since the snapshot, a zombie) would kill the user's
// agent session or an unrelated program, or claim a stop that never
// happened: the one catastrophic outcome this feature can have. Both deciders
// are pure, so these fixed snapshots pin them. A Codex command its shell
// replaced by exec is matched by argv only when it leads its own group, so a
// harness git in the agent's group is never taken for a noted git. The Codex
// shapes under codex-linux-sandbox and bwrap are the spec's expectation until
// step 1 of the spec's section 10 observes them.

// start is a kernel start time, seconds after a fixed moment, in the
// platform's units.
func start(seconds float64) uint64 {
	return uint64((1_000_000 + seconds) * startSlack)
}

func process(pid, ppid, pgid int, seconds float64, args ...string) proc {
	return proc{PID: pid, PPID: ppid, PGID: pgid, Start: start(seconds), Name: args[0], Args: args}
}

func zombie(p proc) proc {
	p.Zombie = true
	return p
}

// claudeShell is Claude Code 2.1.300's command shell on macOS.
func claudeShell(eval string) []string {
	return []string{
		"/bin/zsh", "-c",
		"source /Users/u/.claude/shell-snapshots/snapshot-zsh-1.sh 2>/dev/null || true && eval " +
			eval + " && pwd -P >| /tmp/claude-1-cwd",
	}
}

func snapshotOf(procs ...proc) snapshot {
	s := snapshot{}
	for _, p := range procs {
		s[p.PID] = p
	}
	return s
}

func roots(commands []command) map[string]int {
	out := map[string]int{}
	for _, c := range commands {
		out[c.Note.ToolUseID] = c.Root.PID
	}
	return out
}

func TestMatchTracksOnlyTheAgentsCommandShell(t *testing.T) {
	agent := process(100, 1, 100, -3600, "claude", "--verbose")
	s := snapshotOf(
		agent,
		process(200, 100, 200, 0.4, claudeShell(`'git status'`)...),
		process(207, 200, 200, 0.45, "git", "status"),
		process(201, 100, 201, 0.2, "git", "status"),
		process(202, 100, 202, 0.3, "rg", "--files", "."),
		process(204, 100, 204, -5, claudeShell(`'git status'`)...),
		process(205, 100, 205, 0.6, "/bin/sh", "-c", "~/.local/bin/workbench guard post || true"),
		process(206, 100, 206, 0.7, claudeShell(`'echo '"'"'hi'"'"''`)...),
		process(208, 100, 208, 0.8, claudeShell(`'echo'"'"'hi'"'"''`)...),
		process(209, 100, 209, 0.9, claudeShell(`'git status'"'"' --short'"'"''`)...),
	)
	notes := []note{
		{
			AgentPID: 100, AgentStart: agent.Start, Kind: claude, ToolUseID: "toolu_a",
			Command: "git status", NotedStart: start(0),
		},
		{
			AgentPID: 100, AgentStart: agent.Start, Kind: claude, ToolUseID: "toolu_b",
			Command: "echo 'hi'", NotedStart: start(0.1),
		},
		// Look-alikes: 208 and 209 run longer commands that only begin with
		// these notes' text, before Claude's quote escape.
		{
			AgentPID: 100, AgentStart: agent.Start, Kind: claude, ToolUseID: "toolu_short",
			Command: "echo", NotedStart: start(0.1),
		},
		{
			AgentPID: 100, AgentStart: agent.Start, Kind: claude, ToolUseID: "toolu_git",
			Command: "git status", NotedStart: start(0.1),
		},
		// toolu_old's agent PID now names another process; the note sorts first.
		{
			AgentPID: 100, AgentStart: agent.Start + 1, Kind: claude, ToolUseID: "toolu_old",
			Command: "git status", NotedStart: start(-0.5),
		},
	}
	got := roots(match(s, notes, nil))
	want := map[string]int{"toolu_a": 200, "toolu_b": 206}
	if !maps.Equal(got, want) {
		t.Fatalf("matched %v, want %v", got, want)
	}
	if again := match(s, notes[:1], map[int]bool{200: true}); len(again) != 0 {
		t.Fatalf("a tracked child was matched again: %v", roots(again))
	}
}

func TestMatchGivesTheEarlierNoteTheEarlierChild(t *testing.T) {
	agent := process(100, 1, 100, -3600, "claude")
	s := snapshotOf(
		agent,
		process(301, 100, 301, 2.5, claudeShell(`'make'`)...),
		process(300, 100, 300, 0.5, claudeShell(`'make'`)...),
	)
	notes := []note{
		{
			AgentPID: 100, AgentStart: agent.Start, Kind: claude, ToolUseID: "toolu_d",
			Command: "make", NotedStart: start(2),
		},
		{
			AgentPID: 100, AgentStart: agent.Start, Kind: claude, ToolUseID: "toolu_c",
			Command: "make", NotedStart: start(0),
		},
	}
	got := roots(match(s, notes, nil))
	if got["toolu_c"] != 300 || got["toolu_d"] != 301 {
		t.Fatalf("matched %v, want toolu_c→300 toolu_d→301", got)
	}
}

func TestMatchCodexThroughItsSandbox(t *testing.T) {
	agent := process(400, 1, 400, -3600, "codex", "exec")
	s := snapshotOf(
		agent,
		process(
			401,
			400,
			401,
			0.2,
			"/usr/bin/codex-linux-sandbox",
			"--",
			"/bin/zsh",
			"-c",
			"cargo test",
		),
		process(402, 401, 401, 0.3, "bwrap", "--new-session", "--", "/bin/zsh", "-c", "cargo test"),
		process(403, 402, 401, 0.4, "/bin/zsh", "-c", "cargo test"),
		process(410, 400, 410, 0.1, "node", "/opt/mcp/server.js"),
		process(
			411,
			400,
			411,
			0.5,
			"/bin/sh",
			"-c",
			"'/home/u/.local/bin/workbench' guard pre || true",
		),
		process(412, 400, 412, 0.6, "/bin/zsh", "-c", "cargo test --release"),
	)
	notes := []note{{
		AgentPID: 400, AgentStart: agent.Start, Kind: codex, ToolUseID: "exec-1",
		Command: "cargo test", NotedStart: start(0),
	}}
	got := roots(match(s, notes, nil))
	if len(got) != 1 || got["exec-1"] != 401 {
		t.Fatalf("matched %v, want exec-1→401", got)
	}
}

func TestMatchCodexCommandItsShellReplaced(t *testing.T) {
	agent := process(400, 1, 400, -3600, "codex", "exec")
	s := snapshotOf(
		agent,
		process(420, 400, 420, 0.3, "pytest", "-n", "8", "-k", "slow and big"),
		process(421, 400, 400, 0.2, "git", "status"),
		process(422, 400, 422, 0.4, "cat", "/home/u/notes.txt"),
		// 423 is a fork of the agent that has not exec'd yet.
		process(423, 400, 423, 0.5, "codex", "exec"),
	)
	notes := []note{
		{
			AgentPID: 400, AgentStart: agent.Start, Kind: codex, ToolUseID: "exec-2",
			Command: `pytest -n 8 -k 'slow and big'`, NotedStart: start(0),
		},
		{
			AgentPID: 400, AgentStart: agent.Start, Kind: codex, ToolUseID: "exec-3",
			Command: "git status", NotedStart: start(0),
		},
		{
			AgentPID: 400, AgentStart: agent.Start, Kind: codex, ToolUseID: "exec-4",
			Command: "cat $HOME/notes.txt", NotedStart: start(0),
		},
		{
			AgentPID: 400, AgentStart: agent.Start, Kind: codex, ToolUseID: "exec-7",
			Command: "codex exec", NotedStart: start(0),
		},
	}
	got := roots(match(s, notes, nil))
	if len(got) != 1 || got["exec-2"] != 420 {
		t.Fatalf("matched %v, want only exec-2→420", got)
	}
}

// A program the command starts is not the agent's command: npm test runs
// `sh -c jest`, which must not claim an earlier, still pending `jest` note.
func TestMatchOnlyTheAgentsOwnChild(t *testing.T) {
	agent := process(400, 1, 400, -3600, "codex", "exec")
	s := snapshotOf(
		agent,
		process(430, 400, 430, 1.2, "npm", "test"),
		process(431, 430, 430, 1.5, "sh", "-c", "jest"),
	)
	notes := []note{
		{
			AgentPID: 400, AgentStart: agent.Start, Kind: codex, ToolUseID: "exec-5",
			Command: "jest", NotedStart: start(0),
		},
		{
			AgentPID: 400, AgentStart: agent.Start, Kind: codex, ToolUseID: "exec-6",
			Command: "npm test", NotedStart: start(1),
		},
	}
	got := roots(match(s, notes, nil))
	if len(got) != 1 || got["exec-6"] != 430 {
		t.Fatalf("matched %v, want only exec-6→430", got)
	}
}

func TestTargetsStayInsideTheCommandTree(t *testing.T) {
	cases := []struct {
		name      string
		procs     []proc
		root      int
		start     uint64
		protected map[int]bool
		want      []target
	}{
		{
			name: "group shared with an untracked process",
			procs: []proc{
				process(500, 100, 500, 1, "zsh"),
				process(501, 500, 500, 2, "sleep"),
				process(502, 500, 502, 3, "make"),
				process(503, 502, 502, 4, "cc"),
				process(600, 1, 500, 5, "stray"),
			},
			root: 500, start: start(1), protected: map[int]bool{100: true},
			want: []target{{500, start(1), false}, {501, start(2), false}, {502, start(3), true}},
		},
		{
			name: "a noted agent and the watcher inside the tree",
			procs: []proc{
				process(700, 100, 700, 1, "zsh"),
				process(701, 700, 701, 2, "claude"),
				process(702, 701, 701, 3, "node", "mcp-server.js"),
				process(703, 700, 700, 4, "workbench", "guard", "watch"),
				process(704, 700, 704, 5, "pytest"),
			},
			root: 700, start: start(1), protected: map[int]bool{100: true, 701: true, 703: true},
			want: []target{{700, start(1), false}, {704, start(5), true}},
		},
		{
			name: "a zombie leader whose child lives on in its group",
			procs: []proc{
				zombie(process(900, 100, 900, 1, "zsh")),
				process(901, 900, 900, 2, "python3"),
			},
			root: 900, start: start(1), protected: map[int]bool{100: true},
			want: []target{{901, start(2), false}},
		},
		{
			name:  "PID reused since it was tracked",
			procs: []proc{process(800, 100, 800, 1, "zsh")},
			root:  800, start: start(0), protected: map[int]bool{100: true},
		},
		{
			name:  "init is never a target",
			procs: []proc{process(1, 0, 1, 0, "launchd")},
			root:  1, start: start(0), protected: map[int]bool{},
		},
	}
	for _, c := range cases {
		s := snapshotOf(c.procs...)
		got := targets(s, proc{PID: c.root, Start: c.start}, c.protected)
		if !slices.Equal(got, c.want) {
			t.Errorf("%s: targets %v, want %v", c.name, got, c.want)
		}
	}
}
