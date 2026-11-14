package guard

import (
	"strconv"
	"strings"

	"github.com/Sawmonabo/workbench/internal/platform"
)

// The environment variables the guard reads from another process, the only
// ones it reads there: it keeps or logs nothing else of an environment.
// agentStamp is the Claude Code process's ID, which Claude Code gives every
// command and hook it starts. callStamp is the stamp stampLine gives every
// Bash command of a Claude Code session. codexStamp is the call's tool_use_id,
// which Codex 0.163.0 and later set on each command. modeVariable is the
// guard's own setting (off, dry-run).
const (
	agentStamp   = "CLAUDE_PID"
	callStamp    = "WORKBENCH_GUARD_CALL"
	codexStamp   = "CODEX_TOOL_CALL_ID"
	modeVariable = "WORKBENCH_GUARD"
)

// stampLine is the line the SessionStart hook appends to $CLAUDE_ENV_FILE,
// which Claude Code runs in the shell of each Bash command before the
// command. It names the agent, the shell's own process ID and the second the
// shell ran it, the second it started, so the stamp names the command's shell
// by ID and start time as the guard names every process. A shell without
// EPOCHSECONDS runs date once per command.
const stampLine = `export WORKBENCH_GUARD_CALL="$CLAUDE_PID:$$:${EPOCHSECONDS:-$(date +%s)}"`

// stamp is what a process's environment says about who started it. Readable
// is false when the kernel gives no environment for the process, so the rest
// tells nothing. Agent is CLAUDE_PID; CallAgent, CallShell and CallSecond are
// the three numbers of the call stamp, all 0 when it is absent or not
// digits:digits:digits. Codex is CODEX_TOOL_CALL_ID and Mode is
// WORKBENCH_GUARD, each as read.
type stamp struct {
	Readable                    bool
	Agent, CallAgent, CallShell int
	CallSecond                  int64
	Codex, Mode                 string
}

// readStamp reads pid's stamps from the environment it started with, in one
// read of that environment. The agent stamp and the mode can only make the
// guard do less (a stamp-named agent is protected and keeps work apart from
// another agent's call; off and dry-run keep work out or to the log), so they
// are read also where a rewritten title left them among the arguments (see
// platform.Environment.Narrowing). The call stamp and the Codex call ID tie
// work to a call, so they come from the environment proper only.
func readStamp(pid int) stamp {
	env := platform.ReadEnvironment(pid, agentStamp, callStamp, codexStamp, modeVariable)
	st := stamp{
		Readable: env.Readable,
		Agent:    int(stampNumber(env.Narrowing(agentStamp))),
		Codex:    env.Value(codexStamp),
		Mode:     env.Narrowing(modeVariable),
	}
	if parts := strings.Split(env.Value(callStamp), ":"); len(parts) == 3 &&
		digits(parts[0]) && digits(parts[1]) && digits(parts[2]) {
		st.CallAgent = int(stampNumber(parts[0]))
		st.CallShell = int(stampNumber(parts[1]))
		st.CallSecond = stampNumber(parts[2])
	}
	return st
}

// call reports whether the process carries a call stamp written by its own
// agent. A nested session a command starts, and everything its hooks start,
// inherit the outer command's call stamp beside their own CLAUDE_PID, so the
// two disagree there and the stamp does not count.
func (st stamp) call() bool {
	return st.CallAgent > 0 && st.CallAgent == st.Agent
}

// stampNumber is text as a number when it is plain digits, else 0.
func stampNumber(text string) int64 {
	if !digits(text) {
		return 0
	}
	n, err := strconv.ParseInt(text, 10, 64)
	if err != nil {
		return 0
	}
	return n
}

// digits reports whether text is 1 to 18 ASCII digits, so it holds no sign
// or space and always fits an int64.
func digits(text string) bool {
	if text == "" || len(text) > 18 {
		return false
	}
	for _, c := range text {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// cachedStamp is a stamp kept for a process with the program it was read
// for: once the process runs another program, with an environment of its
// own, the stamp is read again.
type cachedStamp struct {
	stamp
	program program
}

// stampOf is pid's stamp in s, read once per program a process runs (see
// readStamp, programSeen) and kept in stamps while it runs, a hidden one too:
// a process forked but not yet exec'd shows its parent's environment, and
// shows its own once it execs, which is another program. A stamp whose agent
// stamp names a live process that started before pid makes that process
// protected while it is in the process table (see agents), and at once in
// protected when that is not nil.
func (w *watcher) stampOf(s snapshot, pid int, protected map[int]uint64) stamp {
	p, ok := s[pid]
	if !ok {
		return stamp{}
	}
	return w.stampFor(s, p, programSeen(p), protected)
}

// stampFor is stampOf for p, a process of s, whose program now was just read.
func (w *watcher) stampFor(s snapshot, p proc, now program, protected map[int]uint64) stamp {
	key := procKey{p.PID, p.Start}
	cached, ok := w.stamps[key]
	st := cached.stamp
	if !ok || !cached.program.known() || cached.program.replaced(now) {
		st = readStamp(p.PID)
		w.stamps[key] = cachedStamp{st, now}
	}
	if a, ok := s[st.Agent]; ok && st.Agent > 1 && !a.Zombie && a.Start < p.Start {
		w.stampAgents[procKey{a.PID, a.Start}] = true
		if protected != nil {
			protected[a.PID] = a.Start
		}
	}
	return st
}
