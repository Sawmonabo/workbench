package guard

import (
	"maps"
	"slices"
	"testing"

	"github.com/Sawmonabo/workbench/internal/platform"
)

// The guard may only ever stop a command an agent ran through its shell tool.
// Matching the wrong process (a git or rg the harness runs, a hook, an MCP
// server, a program the command itself started) or signalling the wrong one
// (an agent that left a note, the watcher, a process sharing the command's
// group, a PID reused since the snapshot, a zombie) would kill the user's
// agent session or an unrelated program and lose its unsaved work: the one
// catastrophic outcome this feature can have. Missing the agent's command
// shape leaves the machine to the kernel's OOM killer, which may pick the
// agent itself. The deciders are pure, so these fixed snapshots pin them. A
// Codex command its shell replaced by exec is matched by argv only when it
// leads its own group, so a harness git in the agent's group is never taken
// for a noted git. The Codex sandbox chain is the one Codex 0.162.0 and
// 0.163.0 ran on Linux, and the call IDs are those 0.163.0 set.

// start is a kernel start time, seconds after a fixed moment, in the
// platform's units.
func start(seconds float64) uint64 {
	return uint64((1_000_000 + seconds) * platform.StartSecond)
}

func process(pid, ppid, pgid int, seconds float64, args ...string) proc {
	return proc{
		Process: platform.Process{
			PID: pid, PPID: ppid, PGID: pgid, Start: start(seconds), Name: args[0],
		},
		Args: args,
	}
}

func zombie(p proc) proc {
	p.Zombie = true
	return p
}

// inFolder is p with the working folder the kernel reports for it.
func inFolder(p proc, folder string) proc {
	p.Folder = folder
	return p
}

// claudeShell is Claude Code 2.1.300's command shell on macOS, which reads
// the command from ` < /dev/null`.
func claudeShell(eval string) []string {
	return []string{
		"/bin/zsh", "-c",
		"source /Users/u/.claude/shell-snapshots/snapshot-zsh-1.sh 2>/dev/null || true && eval " +
			eval + " < /dev/null && pwd -P >| /tmp/claude-1-cwd",
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
		// One plain word goes unquoted; 210 runs a longer word.
		process(210, 100, 210, 1.0, claudeShell("makefile")...),
		process(211, 100, 211, 1.1, claudeShell("make")...),
		// Without a shell snapshot Claude Code runs a login shell.
		process(
			212,
			100,
			212,
			1.2,
			"/bin/bash",
			"-c",
			"-l",
			"eval 'npm test' < /dev/null && pwd -P >| /tmp/claude-2-cwd",
		),
		// A background call, observed with Claude Code 2.1.300.
		process(
			213,
			100,
			213,
			1.3,
			"/bin/zsh",
			"-c",
			"export CLAUDE_PLUGIN_ROOT=/Users/u/.claude/plugins/p && setopt NO_EXTENDED_GLOB 2>/dev/null || true && "+
				"eval 'sleep 25' < /dev/null && pwd -P >| /tmp/claude-13a2-cwd",
		),
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
		{
			AgentPID: 100, AgentStart: agent.Start, Kind: claude, ToolUseID: "toolu_make",
			Command: "make", NotedStart: start(0.5),
		},
		{
			AgentPID: 100, AgentStart: agent.Start, Kind: claude, ToolUseID: "toolu_npm",
			Command: "npm test", NotedStart: start(0.5),
		},
		{
			AgentPID: 100, AgentStart: agent.Start, Kind: claude, ToolUseID: "toolu_sleep",
			Command: "sleep 25", NotedStart: start(0.5),
		},
	}
	got := roots(match(s, notes, nil, nil))
	want := map[string]int{
		"toolu_a": 200, "toolu_b": 206, "toolu_make": 211, "toolu_npm": 212, "toolu_sleep": 213,
	}
	if !maps.Equal(got, want) {
		t.Fatalf("matched %v, want %v", got, want)
	}
	if again := match(s, notes[:1], map[int]bool{200: true}, nil); len(again) != 0 {
		t.Fatalf("a tracked child was matched again: %v", roots(again))
	}
}

func TestMatchCodexThroughItsSandbox(t *testing.T) {
	agent := process(400, 1, 400, -3600, "codex", "exec")
	sandbox := func(pid, ppid int, seconds float64, command string) proc {
		return process(
			pid,
			ppid,
			pid,
			seconds,
			"/home/u/.codex/tmp/arg0/codex-arg0x/codex-linux-sandbox",
			"--sandbox-policy-cwd",
			"/w",
			"--",
			"/bin/bash",
			"-c",
			"if . '/home/u/.codex/shell_snapshots/1.sh' >/dev/null 2>&1; then :; fi\n\nexec '/bin/bash' -c '"+command+"'",
		)
	}
	bwrap := func(pid, ppid int, seconds float64) proc {
		return process(
			pid,
			ppid,
			pid,
			seconds,
			"bwrap",
			"--as-pid-1",
			"--new-session",
			"--die-with-parent",
		)
	}
	s := snapshotOf(
		agent,
		// The launcher runs bwrap, which runs the launcher again, which
		// starts the command in its own group.
		sandbox(401, 400, 0.2, "cargo test"),
		bwrap(402, 401, 0.2),
		sandbox(403, 402, 0.2, "cargo test"),
		process(404, 403, 403, 0.3, "cargo", "test"),
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
		// Every process from the launcher to the command must start after
		// the note: 443's command started before it, 450's launcher did.
		sandbox(440, 400, 0.3, "cargo check"),
		bwrap(441, 440, 0.3),
		sandbox(442, 441, 0.3, "cargo check"),
		process(443, 442, 442, -5, "cargo", "check"),
		sandbox(450, 400, -5, "cargo doc"),
		bwrap(451, 450, 0.3),
		sandbox(452, 451, 0.3, "cargo doc"),
		process(453, 452, 452, 0.3, "cargo", "doc"),
		// Codex's login shell.
		process(460, 400, 460, 0.7, "/bin/bash", "-lc", "cargo build"),
	)
	notes := []note{
		{
			AgentPID: 400, AgentStart: agent.Start, Kind: codex, ToolUseID: "exec-1",
			Command: "cargo test", NotedStart: start(0),
		},
		{
			AgentPID: 400, AgentStart: agent.Start, Kind: codex, ToolUseID: "exec-8",
			Command: "cargo check", NotedStart: start(0),
		},
		{
			AgentPID: 400, AgentStart: agent.Start, Kind: codex, ToolUseID: "exec-9",
			Command: "cargo doc", NotedStart: start(0),
		},
		{
			AgentPID: 400, AgentStart: agent.Start, Kind: codex, ToolUseID: "exec-10",
			Command: "cargo build", NotedStart: start(0),
		},
	}
	got := roots(match(s, notes, nil, nil))
	want := map[string]int{"exec-1": 401, "exec-10": 460}
	if !maps.Equal(got, want) {
		t.Fatalf("matched %v, want %v", got, want)
	}
}

// A Codex command carrying its own call ID belongs to that call alone: a
// script the shell replaced (its argv no longer the note's words) is still
// its call's command, and a command of another call is never taken for this
// note's, even with the same words. An ID inherited from the agent, which
// itself runs inside an outer Codex command, names no command of its own.
// Under an agent seen to set call IDs, a child without one of its own is
// never a command: Codex's git helpers lead their own groups and may run a
// note's exact words, and taking one would end the note when the helper
// exits, or SIGKILL the helper at a forced stop and tell the model its call
// was stopped.
func TestMatchCodexByCallID(t *testing.T) {
	agent := process(400, 1, 400, -3600, "codex", "exec")
	inner := process(500, 1, 500, -3600, "codex", "exec")
	inner.CallID = "exec-outer"
	s := snapshotOf(
		agent,
		inner,
		// /opt/run/nap is a #! script that runs `exec sleep 3`.
		process(420, 400, 420, 0.3, "sleep", "3"),
		process(421, 400, 421, 0.2, "git", "status"),
		process(422, 400, 422, 0.4, "git", "rev-parse", "HEAD"),
		process(510, 500, 510, 0.2, "cargo", "build"),
	)
	for pid, id := range map[int]string{420: "exec-1", 421: "exec-9", 510: "exec-outer"} {
		p := s[pid]
		p.CallID = id
		s[pid] = p
	}
	notes := []note{
		{
			AgentPID: 400, AgentStart: agent.Start, Kind: codex, ToolUseID: "exec-1",
			Command: "/opt/run/nap", NotedStart: start(0),
		},
		{
			AgentPID: 400, AgentStart: agent.Start, Kind: codex, ToolUseID: "exec-2",
			Command: "git status", NotedStart: start(0),
		},
		{
			AgentPID: 500, AgentStart: inner.Start, Kind: codex, ToolUseID: "exec-3",
			Command: "cargo build", NotedStart: start(0),
		},
		{
			AgentPID: 400, AgentStart: agent.Start, Kind: codex, ToolUseID: "exec-4",
			Command: "git rev-parse HEAD", NotedStart: start(0),
		},
	}
	setsCallIDs := map[procKey]bool{{agent.PID, agent.Start}: true}
	got := roots(match(s, notes, nil, setsCallIDs))
	want := map[string]int{"exec-1": 420, "exec-3": 510}
	if !maps.Equal(got, want) {
		t.Fatalf("matched %v, want %v", got, want)
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
	got := roots(match(s, notes, nil, nil))
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
	got := roots(match(s, notes, nil, nil))
	if len(got) != 1 || got["exec-6"] != 430 {
		t.Fatalf("matched %v, want only exec-6→430", got)
	}
}

// Pairing two notes with the same text crosswise would stop the command in
// the other worktree than the one chosen: a SIGTERM nobody asked for.
func TestMatchPrefersTheCommandInTheNotesFolder(t *testing.T) {
	agent := process(100, 1, 100, -3600, "claude")
	login := "/w/.worktrees/feature-login"
	api := "/w/.worktrees/api-server"
	makeTest := claudeShell(`'make test'`)
	// The api-server note is older, its shell is younger: today's rule pairs them crosswise.
	inLogin := inFolder(process(200, 100, 200, 0.4, makeTest...), login)
	inAPI := inFolder(process(201, 100, 201, 0.5, makeTest...), api)
	noteAPI := note{
		AgentPID: 100, AgentStart: start(-3600), Kind: claude, ToolUseID: "toolu_api",
		Command: "make test", NotedStart: start(0.3), Cwd: api,
	}
	noteLogin := noteAPI
	noteLogin.ToolUseID, noteLogin.Cwd, noteLogin.NotedStart = "toolu_login", login, start(0.35)
	noFolder := noteAPI
	noFolder.ToolUseID, noFolder.Cwd = "toolu_plain", ""
	cdQuoted := noteAPI
	cdQuoted.ToolUseID, cdQuoted.Command, cdQuoted.Cwd, cdQuoted.Folder =
		"toolu_cd", "cd '/w/my repo' && make test", "/w", "/w/my repo"
	cdQuotedShell := claudeShell(`'cd '"'"'/w/my repo'"'"' && make test'`)
	cdTilde := noteAPI
	cdTilde.ToolUseID, cdTilde.Command, cdTilde.Cwd = "toolu_tilde", "cd ~/repo && make test", "/w"
	cdTildeShell := claudeShell(`'cd ~/repo && make test'`)
	for _, tc := range []struct {
		name  string
		notes []note
		procs []proc
		want  map[string]int
	}{
		{
			"sameTextTwoFolders",
			[]note{noteAPI, noteLogin},
			[]proc{agent, inLogin, inAPI},
			map[string]int{"toolu_api": 201, "toolu_login": 200},
		},
		// Without a folder on the note, the oldest shell wins, as today.
		{
			"noFolderKnown",
			[]note{noFolder},
			[]proc{agent, inLogin, inAPI},
			map[string]int{"toolu_plain": 200},
		},
		// A rival whose folder could not be read is not preferred over one that matches.
		{
			"unreadableRivalLoses",
			[]note{noteAPI},
			[]proc{agent, process(200, 100, 200, 0.4, makeTest...), inAPI},
			map[string]int{"toolu_api": 201},
		},
		// No candidate matches: the oldest wins, as today, and nothing is rejected.
		{
			"noMatchFallsBack",
			[]note{noteAPI},
			[]proc{agent, inLogin},
			map[string]int{"toolu_api": 200},
		},
		{
			"quotedFolder",
			[]note{cdQuoted},
			[]proc{
				agent, inFolder(process(200, 100, 200, 0.4, cdQuotedShell...), "/w"),
				inFolder(process(201, 100, 201, 0.5, cdQuotedShell...), "/w/my repo"),
			},
			map[string]int{"toolu_cd": 201},
		},
		// The shell sits in cwd until its cd runs, so cwd matches too.
		{
			"cwdMatchesBeforeTheCd",
			[]note{cdQuoted},
			[]proc{
				agent, inFolder(process(200, 100, 200, 0.4, cdQuotedShell...), "/elsewhere"),
				inFolder(process(201, 100, 201, 0.5, cdQuotedShell...), "/w"),
			},
			map[string]int{"toolu_cd": 201},
		},
		// `~` is the shell's to expand: the folder is unknown, cwd still counts.
		{
			"tildeIsUnknown",
			[]note{cdTilde},
			[]proc{
				agent, inFolder(process(200, 100, 200, 0.4, cdTildeShell...), "/elsewhere"),
				inFolder(process(201, 100, 201, 0.5, cdTildeShell...), "/w"),
			},
			map[string]int{"toolu_tilde": 201},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := roots(match(snapshotOf(tc.procs...), tc.notes, nil, nil))
			if !maps.Equal(got, tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestTargetsStayInsideTheCommandTree(t *testing.T) {
	cases := []struct {
		name      string
		procs     []proc
		root      int
		start     uint64
		protected map[int]uint64
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
			root: 500, start: start(1), protected: map[int]uint64{100: start(-3600)},
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
			root:      700,
			start:     start(1),
			protected: map[int]uint64{100: start(-3600), 701: start(2), 703: start(4)},
			want:      []target{{700, start(1), false}, {704, start(5), true}},
		},
		{
			name: "a zombie leader whose child lives on in its group",
			procs: []proc{
				zombie(process(900, 100, 900, 1, "zsh")),
				process(901, 900, 900, 2, "python3"),
			},
			root: 900, start: start(1), protected: map[int]uint64{100: start(-3600)},
			want: []target{{901, start(2), false}},
		},
		{
			name:  "PID reused since it was tracked",
			procs: []proc{process(800, 100, 800, 1, "zsh")},
			root:  800, start: start(0), protected: map[int]uint64{100: start(-3600)},
		},
		{
			name:  "init is never a target",
			procs: []proc{process(1, 0, 1, 0, "launchd")},
			root:  1, start: start(0), protected: map[int]uint64{},
		},
	}
	for _, c := range cases {
		s := snapshotOf(c.procs...)
		got := targets(s, proc{Process: platform.Process{PID: c.root, Start: c.start}}, c.protected)
		if !slices.Equal(got, c.want) {
			t.Errorf("%s: targets %v, want %v", c.name, got, c.want)
		}
	}
}

// After SIGTERM the watcher sends SIGKILL to what is left of the stopped
// command. Killing outside it would end the user's agent session or the
// watcher and lose unsaved work.
func TestEscalationStaysInsideTheStoppedCommand(t *testing.T) {
	cases := []struct {
		name      string
		stopped   []proc
		now       []proc
		protected map[int]uint64
		want      []int
	}{
		{
			// The agent left its first note after the stop took its processes.
			name: "nested agent that left a note mid-stop",
			stopped: []proc{
				process(700, 100, 700, 1, "zsh"),
				process(701, 700, 701, 2, "claude"),
				process(702, 701, 701, 3, "node", "mcp-server.js"),
			},
			now: []proc{
				process(700, 100, 700, 1, "zsh"),
				process(701, 700, 701, 2, "claude"),
				process(702, 701, 701, 3, "node", "mcp-server.js"),
			},
			protected: map[int]uint64{100: start(-3600), 701: start(2)},
			want:      []int{700},
		},
		{
			name:    "PID reused since the stop",
			stopped: []proc{process(800, 100, 800, 1, "zsh")},
			now: []proc{
				process(800, 1, 800, 9, "postgres"),
				process(801, 800, 800, 10, "postgres"),
			},
			protected: map[int]uint64{100: start(-3600)},
		},
		{
			name:    "watcher inside the tree",
			stopped: []proc{process(900, 100, 900, 1, "zsh")},
			now: []proc{
				process(900, 100, 900, 1, "zsh"),
				process(901, 900, 900, 2, "workbench", "guard", "watch"),
				process(902, 900, 900, 3, "pytest"),
			},
			protected: map[int]uint64{100: start(-3600), 901: start(2)},
			want:      []int{900, 902},
		},
		{
			name:    "new child under a live stopped process",
			stopped: []proc{process(1000, 100, 1000, 1, "zsh")},
			now: []proc{
				process(1000, 100, 1000, 1, "zsh"),
				process(1001, 1000, 1000, 6, "cc"),
			},
			protected: map[int]uint64{100: start(-3600)},
			want:      []int{1000, 1001},
		},
	}
	for _, c := range cases {
		kill, _ := escalation(snapshotOf(c.now...), c.stopped, c.protected)
		var got []int
		for _, p := range kill {
			got = append(got, p.PID)
		}
		if !slices.Equal(got, c.want) {
			t.Errorf("%s: kills %v, want %v", c.name, got, c.want)
		}
	}
}

// A stop nobody with the right to stop it chose, or sent by a process still
// under a noted agent, is an unauthorized SIGTERM.
func TestManualStopsOnlyWhatAPersonMayStop(t *testing.T) {
	agent := process(100, 1, 100, -3600, "claude")
	shell := process(200, 100, 200, 0.4, claudeShell(`'make'`)...)
	s := snapshotOf(
		process(1, 0, 1, -7200, "launchd"),
		agent,
		shell,
		process(300, 200, 300, 0.5, "workbench", "guard", "memory"), // under the agent's command
		process(400, 1, 400, 0.6, "zsh"),
		process(401, 400, 401, 0.7, "workbench", "guard", "memory"),
		process(500, 1, 500, -3600, "claude"), // agent-named, left no note
	)
	protected := map[int]uint64{100: start(-3600)}
	// An agent may be PID 1, in a container started without an init.
	inContainer := snapshotOf(
		process(1, 0, 1, -3600, "claude"),
		process(600, 1, 600, 0.8, "workbench", "guard", "memory"),
	)
	containerAgent := map[int]uint64{1: start(-3600)}
	for _, tc := range []struct {
		name      string
		s         snapshot
		protected map[int]uint64
		peer      procKey
		want      bool
	}{
		{"personAtATerminal", s, protected, procKey{401, start(0.7)}, true},
		{"underTheAgentsCommand", s, protected, procKey{300, start(0.5)}, false},
		{"theAgentItself", s, protected, procKey{100, start(-3600)}, false},
		{"peerExitedPIDReused", s, protected, procKey{401, start(9)}, false},
		{"unknownPeer", s, protected, procKey{999, start(0)}, false},
		{"agentWithoutANote", s, protected, procKey{500, start(-3600)}, true},
		{"underAnAgentAtPID1", inContainer, containerAgent, procKey{600, start(0.8)}, false},
		{"theAgentAtPID1", inContainer, containerAgent, procKey{1, start(-3600)}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := stopAllowed(tc.peer, tc.s, tc.protected); got != tc.want {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
	tracked := map[string]command{
		"toolu_live": {Root: shell},
		"toolu_gone": {Root: process(201, 100, 201, 0.1, "sh")},
	}
	stop, results := chooseStops([]string{"toolu_live", "toolu_gone", "toolu_never"}, tracked, s)
	if len(stop) != 1 || stop[0].Root.PID != 200 {
		t.Fatalf("chosen %v, want only the live command", stop)
	}
	want := []StopResult{
		{ID: "toolu_live", OK: true},
		{ID: "toolu_gone", Error: "already finished"},
		{ID: "toolu_never", Error: "unknown"},
	}
	if !slices.Equal(results, want) {
		t.Fatalf("results %v, want %v", results, want)
	}
}
