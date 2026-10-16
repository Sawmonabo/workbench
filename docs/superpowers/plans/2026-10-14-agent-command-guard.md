# Agent Command Guard Implementation Plan

The code is authoritative: listings below predate review fixes made during implementation.

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Near out-of-memory, stop the largest shell command an AI coding agent
(Claude Code or Codex) started, with everything it started, and tell the agent
why. Never stop the agent, its harness, MCP servers, hooks or anything else.

**Architecture:** A new `internal/guard/` package behind three hidden
subcommands of the one `workbench` CLI:
- `guard pre`, a `PreToolUse` hook on `Bash`, leaves a note per call and
  starts the watcher.
- `guard watch` is one detached watcher per user. It matches notes to the
  agent's command shells, reads machine memory, and signals the largest
  matched command tree.
- `guard post`, on `PostToolUse` and Claude Code's `PostToolUseFailure`, hands
  the reason to the agent.

The two decisions that pick what may be stopped (`match`, `targets`) are pure
functions over a process snapshot, and they are the only tested code.

**Tech Stack:** Go 1.26.8, `golang.org/x/sys v0.48.0` (`unix`), Cobra,
chezmoi templates (`home/.chezmoidata/claude.json`,
`home/.chezmoitemplates/codex-hooks.json.tmpl`). No cgo.

**Spec:** `docs/superpowers/specs/2026-10-14-agent-command-guard-design.md`
(read it before any task; section numbers below refer to it).

## Global Constraints

- **No cgo.** Releases stay `CGO_ENABLED=0` on one Ubuntu runner for all four
  targets (spec 8). Raw darwin syscalls follow
  `internal/operation/image_metadata_darwin.go`: each pointer becomes a
  `uintptr` inside the `Syscall6` argument list, `//nolint:staticcheck` on the
  deprecated `SYS_*` constant, and an error or unexpected size fails closed.
- **Hooks fail open.** `guard pre` and `guard post` always exit 0 and recover
  from panics. The hook commands end in `|| true`. A guard error never blocks
  a call (exit 2 from a `PreToolUse` hook blocks it on Claude Code and denies
  it on Codex).
- **Never rewrite, wrap, re-parent or re-prioritise a command** (spec 1).
- **Never signal** PID 0 or 1, a zombie, any agent that has left a note, the
  watcher, or anything outside the tracked command's tree (spec 6.4).
- **State** lives in `<Paths.State>/guard/`, made `0700` through the
  operation layer's private-directory owner. Files are `0600`. Note IDs must
  match `^[A-Za-z0-9_-]{1,128}$` (spec 4).
- **Thresholds** (spec 6.2):
  - Linux/WSL2: polite stop at available ≤ 10 % and free swap ≤ 10 %, or PSI
    `full avg10` ≥ 50 % with available ≤ 20 %; forced stop at available ≤ 5 %
    and free swap ≤ 5 %.
  - macOS: polite stop at pressure level 2, forced stop at level 4.
- **Timings:**
  - Poll every 100 ms. SIGKILL after 5 s (or at the forced-stop line), by
    PID to every live process of the stopped command, including children
    that outlived their group leader.
  - Give up after 10 s: log the survivors, end the cooldown, and never
    choose that call again.
  - A dry-run command is logged once and never starts a cooldown.
  - Cooldown: the stopped command's processes are gone, plus 10 s.
  - Idle exit after 10 min.
  - Kill records expire after 24 h.
  - Log capped at 1 MiB, then rotated once.
  - Watcher Go memory limit 32 MiB.
  - Hook timeout 5 s.
- **Start times** are compared in the kernel's own units, never against the
  wall clock: clock ticks since boot on Linux, `kinfo_proc` start times on
  macOS. The pre hook records its own start in the note (`noted_start`).
- **A call runs once.** Once a call's tracked command has ended, its note
  never matches again, even while the note stays (a denied call's stays
  until the agent exits).
- **Tests:** AGENTS.md wins over TDD. The only test is
  `internal/guard/match_test.go` (spec 11), with its catastrophic risk
  explained beside it. Every other task verifies with the gates:
  `golangci-lint fmt`, `golangci-lint config verify`,
  `golangci-lint run ./...`, `go build ./...`, `go test ./...`. Add
  `scripts/render-check.sh ROLE MODE [wsl]` for `home/` changes.
- **Lint limits** (`.golangci.yml`): `funlen` 100 lines / 60 statements,
  `lll` 120, `golines` 100, `revive` `exported`/`package-comments`. Keep
  everything in `internal/guard` unexported except `Pre`, `Post` and
  `Watch`.
- **Smoke checks** run only in an isolated state folder (`WORKBENCH_STATE_DIR`
  in the session scratchpad) or a disposable VM. Never provision the
  developer's machine, and never create memory pressure on it.
  `<scratchpad>` in the commands below means the executing session's
  scratchpad folder, never a path in the repo or the home.
- **Commits:** conventional subject; the body ends with
  `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`. Never edit
  `CHANGELOG.md`.
- **Shell rules for every check:**
  - `rg` never takes `-r` and always names a folder.
  - zsh reads `${pipestatus[1]}` after a pipe.
  - Print `EXIT:$?`.
- **Order with the migrations plan**
  (`docs/superpowers/plans/2026-10-14-workbench-migrations.md`): this plan
  runs first. Both plans edit `AGENTS.md`'s ownership table and
  `docs/usage.md`, so edits here are placed by anchor text, never by line
  number.

## Review Focus

1. **The post hook fires the instant the stopped command dies.** The agent
   must still see the reason. The watcher writes `killed/<id>.json` *before*
   it signals (Task 5) and deletes the record if nothing was signalled. Task 8
   checks this manually.
2. **Look-alike processes:**
   - a harness `git status` beside a noted `git status`, including a Codex
     command its shell replaced with `exec`
   - a hook's own `sh -c`, or an MCP server
   - a program the command itself starts (`npm test` running `sh -c jest`)
     claiming another, still pending note
   - an agent that has left a note, nested inside another command's tree
     (`claude -p` run from a Claude `Bash` call).

   None of these may ever be matched or signalled. The Task 3 test covers
   this. A nested agent that has not run `Bash` yet has left no note: it is
   part of the outer command and stops with it. The guard has no name-based
   protection.
3. **The hook can't do its job:**
   - an installed `workbench` older than this code (unknown subcommand, exit 2)
   - input it can't read
   - a state folder Workbench refuses.

   The call must proceed unchanged, with exit 0 and no output. Task 7 checks
   these three manually. Two cases are not exercised:
   - a panic inside `Pre` or `Post`, which their deferred `recover` handles
     (Task 5)
   - a hook running past its 5-second timeout, which is the agents' own
     fail-open behaviour (spec 2, from their docs).
4. **A denied or long-pending call followed by the same command again.** The
   denied call's note stays until the agent exits (spec 6.1), and the earlier
   note takes the earlier child. So the denied note claims the next identical
   command once (logged as `tracking <denied id>`), and that call's own note
   is dropped by its post hook without a line. After that command ends, the
   denied call counts as run and never matches again, so later identical
   commands are tracked under their own IDs.
   - Task 6 checks this manually.
   - Whether a denied call fires a post hook is recorded in Task 1.
   - Task 8 confirms that a reason never reaches the wrong call's output.
5. **The watcher dies mid-session** (crash, `kill -9`, reboot). The lock is
   freed by the kernel, and the next pre hook starts a new watcher, which
   deletes notes whose agent is gone. Task 6 checks this manually.

---

### Task 1: Observe the command shapes on Linux and WSL2

This task runs outside this Mac. It needs, with the owner's go-ahead:
- **A Linux VM or machine** where Claude Code and Codex are installed and
  signed in. A container works for shapes, but agents in it need credentials;
  use the owner's own sign-in, never a shared one.
- **A WSL2 distribution** with both agents signed in (the owner's other
  computer).

If they aren't available yet, Tasks 2–7 can proceed on the macOS-observed
shapes. Task 1 must finish before Task 8 and before the docs claim Linux or
WSL2 support.

Facts so far (macOS only; spike with Claude Code 2.1.299 and Codex 0.162.0):
- **Claude Code command:** `/bin/zsh -c 'source <snapshot> … && eval
  '<escaped>' && pwd -P >| <cwd file>'`, a direct child of the agent, `pgid
  == pid`. 2.1.299 put `< /dev/null` after the `eval`, and 2.1.300 doesn't.
  The matcher looks only for `eval '<escaped>'`, so it doesn't depend on that
  suffix.
- **Codex command:** `/bin/zsh -c <command>`, a direct child of `codex`,
  `pgid == pid`. The same with `-s workspace-write` on macOS.
- **A single simple command replaces its shell.** zsh and bash `exec` it in
  place, so Codex's `zsh -c 'cargo test'` runs as `cargo test` with the same
  PID and group (spec 2, observed on macOS). Task 3 matches it by argv when it
  leads its own group.
- **Hooks:** each runs as the agent's direct child.
- **Tool use IDs:** `tool_use_id` is `toolu_…` (Claude Code) and `exec-…`
  (Codex). Codex's hook input carries `turn_id`, and Claude Code's doesn't.

**Files:**
- Create (throwaway, in the session scratchpad, never committed):
  `guard-probe/probe-hook.sh`, `guard-probe/proj/.claude/settings.json`,
  `guard-probe/proj/.codex/hooks.json`.
- Modify: `docs/superpowers/specs/2026-10-14-agent-command-guard-design.md`
  (section 2 "Not yet observed", section 6.1).

**Interfaces:**
- Produces: the confirmed (or corrected) shape rules that Task 3's `runs()`
  and `claudeForms()` implement, plus answers to two questions:
  - whether a denied call fires a post hook
  - when a `run_in_background` command's post hook fires.

- [ ] **Step 1: Write the probe hook**

`guard-probe/probe-hook.sh` (Linux, bash):

```bash
#!/bin/bash
# Throwaway guard probe. Logs every hook event, the hook's ancestry, and one
# second after each PreToolUse the exact argv (NUL shown as |) of every
# process under the agent, so the matcher's shapes can be read off. It never
# signals anything.
LOG="${GUARD_PROBE_LOG:?set GUARD_PROBE_LOG}"
in=$(cat)
shell() { case "$1" in sh|bash|zsh|dash) return 0 ;; esac; return 1; }
agent=$PPID
while shell "$(cat /proc/$agent/comm 2>/dev/null)"; do
    agent=$(awk '{print $4}' /proc/$agent/stat)
done
{
    echo "===== $(date '+%Y-%m-%dT%H:%M:%S.%N') hook $$ agent $agent ($(cat /proc/$agent/comm))"
    printf '%s\n' "$in"
} >>"$LOG"
descendants() {
    local p
    for p in $(ps -e -o pid=,ppid= | awk -v parent="$1" '$2 == parent {print $1}'); do
        echo "$p"
        descendants "$p"
    done
}
case "$in" in *'"PreToolUse"'*)
    (
        sleep 1
        {
            echo "----- tree of agent $agent, 1 s after hook $$"
            for pid in $(descendants "$agent"); do
                stat=$(cat /proc/$pid/stat 2>/dev/null) || continue
                printf '%s ppid=%s pgid=%s start=%s comm=%s argv=' "$pid" \
                    "$(echo "$stat" | awk '{print $4}')" "$(echo "$stat" | awk '{print $5}')" \
                    "$(echo "$stat" | awk '{print $22}')" "$(cat /proc/$pid/comm)"
                tr '\0' '|' </proc/$pid/cmdline
                printf ' pss=%s\n' "$(awk '/^Pss:/ {print $2}' /proc/$pid/smaps_rollup 2>&1)"
            done
            echo "----- /proc/pressure/memory"
            cat /proc/pressure/memory 2>&1
        } >>"$LOG"
    ) </dev/null >/dev/null 2>&1 &
    ;;
esac
exit 0
```

`guard-probe/proj/.claude/settings.json` registers it on `PreToolUse`,
`PostToolUse` and `PostToolUseFailure` with `"matcher": "Bash"`, and
`guard-probe/proj/.codex/hooks.json` on `PreToolUse` and `PostToolUse`. Each
uses `"command": "<absolute path>/probe-hook.sh"` and `"timeout": 10`, the
same shape as the spike's `oom-spike/proj` files.

- [ ] **Step 2: Run each case and keep the log**

Run from `guard-probe/proj` with `GUARD_PROBE_LOG=$PWD/../events.log`. Use the
prompt `Use your Bash/shell tool exactly once to run this exact command and
nothing else: sleep 5; echo 'it''s' >nul; echo GUARDMARK`. It contains a single
quote and a `>nul` redirect, to observe Claude Code's escaping and rewrite.

1. `claude -p "$(cat ../prompt.txt)"`. Then the same with Claude Code's
   sandbox on, adding `"sandbox": {"enabled": true}` to the project settings.
2. Interactive `claude`, pasting the prompt.
3. `codex exec --ephemeral --skip-git-repo-check
   --dangerously-bypass-hook-trust -s workspace-write "$(cat ../prompt.txt)"`,
   then the same with `--dangerously-bypass-approvals-and-sandbox`.
4. Interactive `codex`, pasting the prompt.
5. Claude Code with `run_in_background`: prompt it to run `sleep 30; echo
   GUARDMARK` in the background, then wait. Note whether `PostToolUse` fires
   before the command ends.
6. A denied call: interactive `claude` without bypass permissions. Deny the
   command, and note whether `PostToolUse` or `PostToolUseFailure` fires for
   its `tool_use_id`.

Repeat 1–4 on WSL2. Record each agent's version (`claude --version`, `codex
--version`), and the exact text around `eval '…'` in Claude Code's script.
Claude Code 2.1.300 on macOS runs `… && eval '<cmd>' && pwd -P >|
/tmp/claude-…-cwd`, with no `< /dev/null`.

- [ ] **Step 3: Read off the shapes**

For each case, find the process whose argv holds `GUARDMARK`:
- its argv split at `|`
- its ancestry up to the agent (sandbox launchers such as
  `codex-linux-sandbox` and `bwrap`)
- whether `pgid == pid` for the agent's direct child
- whether `smaps_rollup` was readable inside the sandbox.

Note the exact escaped form of `it's` and what `>nul` became. Also run one
Codex case with a single command (`sleep 5`), and check two things:
- its process shows as `sleep 5` with `pgid == pid` at whatever depth the
  sandbox puts it
- whether any process the Codex harness starts by itself (`git`, a
  formatter) leads its own group. The single-command rule relies on that
  never happening with an agent command's exact argv.

- [ ] **Step 4: Update the spec**

In spec section 2, move each observed case from "Not yet observed" into the
facts list, with the agent versions. In section 6.1, correct any shape that
differs. State the `>nul` rewrite rule exactly as observed, and the answers
for the denied call and `run_in_background`.

- [ ] **Step 5: Commit**

```bash
git add docs/superpowers/specs/2026-10-14-agent-command-guard-design.md
git commit -F - <<'EOF'
docs(guard): record the Linux and WSL2 command shapes

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
EOF
echo EXIT:$?
```

If a shape differs from what Task 3 implements, change `runs()`,
`claudeForms()` and the matching test fixture in Task 3 the same way before
moving on.

---

### Task 2: Operation helpers the guard reuses

The guard needs two things only the operation layer owns today:
- **A private folder under the state root.** `ensureRoot` and
  `ensurePrivateDirectory` exist, unexported (`internal/operation/state.go:310-347`).
- **The hook's stdin read.** `internal/costs/ingest.go` `readHookInput` reads
  it with a size cap and a 500 ms timeout.

Export both at their owner and switch costs to the shared read.

**Files:**
- Modify: `internal/operation/state.go` (after `ensureRoot`, which ends at line 347)
- Modify: `internal/operation/process.go` (after `StartDetached`, which ends at line 286)
- Modify: `internal/costs/ingest.go` (`readHookInput`)

**Interfaces:**
- Produces:
  - `func PrivateDirectory(root, name string) (string, error)`: one helper for
    any runtime root. The migrations plan reuses it for the data and config
    roots.
  - `func ReadHookInput(stdin *os.File, limit int64, wait time.Duration) []byte`

- [ ] **Step 1: Add `PrivateDirectory`**

In `internal/operation/state.go`, after `ensureRoot`:

```go
// PrivateDirectory makes the folder name below root, and root itself, private
// (0700, owned by the user, no unsafe parents) and returns its path. name may
// hold a slash to make a nested folder; only its last folder is checked, so
// make each level in turn. Writers that keep their own files in Workbench's
// folders outside a [Mutation] use it, such as the agent command guard. An
// empty name is root itself.
func PrivateDirectory(root, name string) (string, error) {
	if name != "" && !filepath.IsLocal(name) {
		return "", Fail(ExitInvalid, "private_path", name+" is not a folder inside "+root)
	}
	if err := ensureRoot(root); err != nil {
		return "", err
	}
	directory := filepath.Join(root, name)
	return directory, ensurePrivateDirectory(directory)
}
```

- [ ] **Step 2: Add `ReadHookInput`**

In `internal/operation/process.go`, after `StartDetached`:

```go
// ReadHookInput reads the JSON a Claude Code or Codex hook gets on stdin: at
// most limit bytes, giving up after wait so a writer that never closes stdin
// cannot hold the hook. A nil stdin, a terminal or a timeout gives nil.
func ReadHookInput(stdin *os.File, limit int64, wait time.Duration) []byte {
	if stdin == nil || IsTerminal(stdin) {
		return nil
	}
	read := make(chan []byte, 1)
	go func() {
		data, _ := io.ReadAll(io.LimitReader(stdin, limit))
		read <- data
	}()
	select {
	case data := <-read:
		return data
	case <-time.After(wait):
		return nil
	}
}
```

- [ ] **Step 3: Switch costs to it**

Replace the body of `readHookInput` in `internal/costs/ingest.go`:

```go
func readHookInput(stdin *os.File) (event, transcript, session string) {
	var input struct {
		Event      string `json:"hook_event_name"`
		Transcript string `json:"transcript_path"`
		Session    string `json:"session_id"`
	}
	data := operation.ReadHookInput(stdin, 1<<20, 500*time.Millisecond)
	if json.Unmarshal(data, &input) != nil {
		return "", "", ""
	}
	return input.Event, input.Transcript, input.Session
}
```

`ingest.go` keeps its `io` import: other code in the file still uses it.

- [ ] **Step 4: Run the gates**

```bash
golangci-lint fmt && golangci-lint config verify && golangci-lint run ./... && go build ./... && go test ./...; echo EXIT:$?
```

Expected: `EXIT:0`.

- [ ] **Step 5: Check the costs hook still reads its input**

Use an isolated home and state, and price pages that refuse to connect, so
nothing reaches the network or the real ledger:

```bash
S=<scratchpad>/guard-hook-check && rm -rf "$S" && mkdir -p "$S/home" && chmod 700 "$S"
go build -o "$S/workbench" ./cmd/workbench
printf '%s' '{"hook_event_name":"SessionStart","transcript_path":"/nonexistent","session_id":"s1"}' |
  HOME="$S/home" WORKBENCH_STATE_DIR="$S/state" CLAUDE_COSTS_STATE="$S/costs" \
  CLAUDE_COSTS_LEDGER="$S/ledger.sqlite" CLAUDE_COSTS_PRICING_URL=http://127.0.0.1:9/ \
  CODEX_COSTS_PRICING_URL=http://127.0.0.1:9/ "$S/workbench" costs ingest; echo EXIT:$?
sleep 3; rg -n "session|SessionStart|error" "$S/costs"; echo EXIT:$?
rm -rf "$S"
```

Expected: `EXIT:0` with no output from the hook, then the worker's log lines
`session not bound: no tool owns transcript path "/nonexistent"` and
`SessionStart: 0 files, 0 records …` (each price fetch fails and the cached
card is kept). If the migrations plan already removed `CLAUDE_COSTS_*`, use
`WORKBENCH_DATA_DIR` and `WORKBENCH_STATE_DIR` instead, and read
`$S/state/costs/ingest.log`.

- [ ] **Step 6: Commit**

```bash
git add internal/operation/state.go internal/operation/process.go internal/costs/ingest.go
git commit -F - <<'EOF'
refactor(operation): share the hook stdin read and a private folder helper

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
EOF
echo EXIT:$?
```

---

### Task 3: What may be stopped — `match` and `targets`, with the one test

**Files:**
- Create: `internal/guard/guard.go` (package doc, note and process types,
  snapshot helpers)
- Create: `internal/guard/match.go` (`simpleWords`, `runs`,
  `claudeForms`, `launchers`, `commandProcesses`, `match`, `targets`)
- Create: `internal/guard/match_test.go`

**Interfaces:**
- Produces (used by Tasks 4–5):
  - `type kind string` with constants `claude`, `codex`
  - `type note struct{ AgentPID int; AgentStart uint64; Kind kind; SessionID, ToolUseID, Command string; NotedStart uint64; NotedAt time.Time; DryRun bool }`
  - `type proc struct{ PID, PPID, PGID int; Start uint64; Name string; Args []string; Zombie bool }`
  - `type snapshot map[int]proc` with `children(pid int) []proc` and
    `tree(root int, skip func(proc) bool) []proc`
  - `type command struct{ Note note; Root proc }`
  - `type target struct{ PID int; Start uint64; Group bool }`
  - `func match(s snapshot, notes []note, taken map[int]bool) []command`
  - `func targets(s snapshot, root proc, protected map[int]bool) []target`
  - `var shells map[string]bool`
  - `func simpleWords(command string) []string`
  - `func runs(n note, p proc) bool`
  - `var launchers map[string]bool` and
    `func (s snapshot) commandProcesses(child proc) []proc`
- Produces for Task 4: `const startSlack` in each platform file (Step 1b
  below), one second in `proc.Start`'s units, which Task 4 keeps.

- [ ] **Step 1: Write `guard.go`**

```go
// Package guard stops the largest shell command an AI coding agent started
// when the machine is about to run out of memory, and tells the agent why. It
// never stops the agent, its harness, MCP servers, hooks or any other
// program. See docs/superpowers/specs/2026-10-14-agent-command-guard-design.md.
package guard

import (
	"cmp"
	"slices"
	"time"
)

// kind is the agent a note came from; it decides the command shape matched.
type kind string

const (
	claude kind = "claude"
	codex  kind = "codex"
)

// note is what the pre hook leaves for one shell call. NotedStart is the pre
// hook process's own kernel start time, in the units of proc.Start, so the
// matcher compares kernel start times with each other and never with the
// wall clock; NotedAt is for the log.
type note struct {
	AgentPID   int       `json:"agent_pid"`
	AgentStart uint64    `json:"agent_start"`
	Kind       kind      `json:"kind"`
	SessionID  string    `json:"session_id"`
	ToolUseID  string    `json:"tool_use_id"`
	Command    string    `json:"command"`
	NotedStart uint64    `json:"noted_start"`
	NotedAt    time.Time `json:"noted_at"`
	DryRun     bool      `json:"dry_run,omitempty"`
}

// proc is one process in a snapshot. Start is the kernel's start time in the
// platform's own units (startSlack is one second of them) and, with the PID,
// names the process: a later process reusing the PID has another Start. Args
// stays nil until read.
type proc struct {
	PID, PPID, PGID int
	Start           uint64
	Name            string
	Args            []string
	Zombie          bool
}

// snapshot is every process at one moment, by PID.
type snapshot map[int]proc

// children are pid's direct children, oldest first.
func (s snapshot) children(pid int) []proc {
	var out []proc
	for _, p := range s {
		if p.PPID == pid && p.PID != pid {
			out = append(out, p)
		}
	}
	slices.SortFunc(out, func(a, b proc) int {
		return cmp.Or(cmp.Compare(a.Start, b.Start), cmp.Compare(a.PID, b.PID))
	})
	return out
}

// tree is root and its descendants, each parent before its children. A
// process skip reports true for is left out with everything under it.
func (s snapshot) tree(root int, skip func(proc) bool) []proc {
	p, ok := s[root]
	if !ok || skip != nil && skip(p) {
		return nil
	}
	out := []proc{p}
	for _, child := range s.children(root) {
		out = append(out, s.tree(child.PID, skip)...)
	}
	return out
}

// command is an agent command the watcher tracks: the agent's direct child
// whose tree holds the shell running one note's text.
type command struct {
	Note note
	Root proc
}

// target is one signal: to the process PID, or with Group to the process
// group it leads. Start is the process's start time in the snapshot the
// target came from.
type target struct {
	PID   int
	Start uint64
	Group bool
}
```

- [ ] **Step 1b: Start the platform files with `startSlack`**

`match` and its test compare start times in the kernel's units, so create
both platform files now with only their constant. Task 4 writes the rest of
each file around it.

`internal/guard/platform_darwin.go`:

```
//go:build darwin

package guard

// startSlack is one second in proc.Start's units: microseconds of
// kinfo_proc's p_starttime. That start time is the wall clock when the
// process began, which the kernel records for the pre hook and for the
// command alike, so only a clock step between the two can shift them.
const startSlack = 1_000_000
```

`internal/guard/platform_linux.go`:

```
//go:build linux

package guard

// startSlack is one second in proc.Start's units: clock ticks since boot
// (USER_HZ, 100 on every architecture Workbench ships for), the unit of
// /proc/<pid>/stat's starttime. Ticks since boot never follow a wall-clock
// step, such as WSL2's resync after the host sleeps.
const startSlack = 100
```

- [ ] **Step 2: Write the failing test**

`internal/guard/match_test.go`:

```go
package guard

import (
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
// Task 1 of the guard plan observes them.

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
		process(203, 100, 100, 0.5, "claude", "--verbose"),
		process(204, 100, 204, -5, claudeShell(`'git status'`)...),
		process(205, 100, 205, 0.6, "/bin/sh", "-c", "~/.local/bin/workbench guard post || true"),
		process(206, 100, 206, 0.7, claudeShell(`'echo '"'"'hi'"'"''`)...),
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
		{
			AgentPID: 100, AgentStart: agent.Start + 1, Kind: claude, ToolUseID: "toolu_old",
			Command: "git status", NotedStart: start(0),
		},
	}
	got := roots(match(s, notes, nil))
	want := map[string]int{"toolu_a": 200, "toolu_b": 206}
	if len(got) != len(want) || got["toolu_a"] != 200 || got["toolu_b"] != 206 {
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
```

- [ ] **Step 3: Run it to see it fail**

```bash
go test ./internal/guard/; echo EXIT:$?
```

Expected: build failure, `undefined: match` and `undefined: targets`.

- [ ] **Step 4: Write `match.go`**

```go
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
			if strings.Contains(args[2], "eval '"+strings.ReplaceAll(form, "'", `'"'"'`)+"'") {
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

// launchers start an agent's command inside a sandbox: Codex on Linux runs
// codex-linux-sandbox, which runs bwrap, which runs the shell.
var launchers = map[string]bool{"codex-linux-sandbox": true, "bwrap": true}

// commandProcesses are the processes that may run a note's command for one
// agent child: the child itself or, when it is a sandbox launcher, the first
// process below its chain of launchers. Nothing deeper counts, so a program
// the command itself starts (npm's own `sh -c jest`) never stands for a note.
func (s snapshot) commandProcesses(child proc) []proc {
	if len(child.Args) == 0 || !launchers[filepath.Base(child.Args[0])] {
		return []proc{child}
	}
	var out []proc
	for _, next := range s.children(child.PID) {
		out = append(out, s.commandProcesses(next)...)
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
	members := s.tree(root.PID, func(p proc) bool { return protected[p.PID] || p.PID <= 1 })
	in := make(map[int]bool, len(members))
	for _, p := range members {
		in[p.PID] = true
	}
	whole := func(group int) bool {
		if leader, ok := s[group]; group <= 1 || !in[group] || !ok || leader.Zombie {
			return false
		}
		for _, p := range s {
			if p.PGID == group && !in[p.PID] {
				return false
			}
		}
		return true
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
```

- [ ] **Step 5: Run the test to see it pass**

```bash
go test ./internal/guard/ -run 'TestMatch|TestTargets' -v; echo EXIT:$?
```

Expected: six `--- PASS` lines, then `EXIT:0`.

- [ ] **Step 6: Run the gates**

```bash
golangci-lint fmt && golangci-lint config verify && golangci-lint run ./... && go build ./... && go test ./...; echo EXIT:$?
```

Expected: `EXIT:0`.

- [ ] **Step 7: Commit**

```bash
git add internal/guard/guard.go internal/guard/match.go internal/guard/match_test.go \
  internal/guard/platform_darwin.go internal/guard/platform_linux.go
git commit -F - <<'EOF'
feat(guard): decide which agent command may be stopped

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
EOF
echo EXIT:$?
```

---

### Task 4: Platform readers and the memory decision

**Files:**
- Create: `internal/guard/memory.go` (`memory`, `decide`, `delay`, `reason`)
- Replace: `internal/guard/platform_darwin.go` (Task 3's stub)
- Replace: `internal/guard/platform_linux.go` (Task 3's stub)

**Interfaces:**
- Consumes: `proc`, `snapshot`, `target` (Task 3).
- Produces, on each platform:
  - `func snapshotNow() (snapshot, error)`: every process, without args
  - `func processInfo(pid int) (proc, error)`: one process, without args
  - `func readArgs(pid int) []string`
  - `func readMemory() (memory, error)`
  - `func footprint(pid int) (uint64, error)`: bytes
  - `func send(t target, sig unix.Signal) bool`: true only when a live
    (non-zombie) process with the snapshot's start time was signalled
  - `const startSlack`: one second in `proc.Start`'s units (Linux 100 clock
    ticks, macOS 1,000,000 µs)
  - `type waiter`, with `newWaiter() *waiter`, `(*waiter).watch(pid int)`,
    `(*waiter).wait(d time.Duration)` and `(*waiter).close()`
- Shared: `type action int` (`nothing`, `polite`, `forced`),
  `func decide(m memory) action`, `func delay(m memory) time.Duration` and
  `func reason(command string, children int, size uint64, m memory) string`.

- [ ] **Step 1: Write `memory.go`**

```go
package guard

import (
	"fmt"
	"strings"
	"time"
)

// action is what the machine's memory calls for.
type action int

const (
	nothing action = iota
	polite         // SIGTERM
	forced         // SIGKILL
)

// memory is one reading of the machine's memory. On macOS Level is
// kern.memorystatus_vm_pressure_level (1 normal, 2 warning, 4 critical) and
// decides alone. On Linux Level is 0, Total is earlyoom's figure
// (MemAvailable + AnonPages), so Available/Total is the share user memory can
// still take, and FullAvg10 is /proc/pressure/memory's full avg10, -1 when
// absent. Installed is the machine's RAM, for the reason text.
type memory struct {
	Level               int
	Available, Total    uint64
	Installed           uint64
	SwapFree, SwapTotal uint64
	FullAvg10           float64
}

func (m memory) availablePercent() float64 {
	if m.Total == 0 {
		return 100
	}
	return float64(m.Available) * 100 / float64(m.Total)
}

// swapFreePercent counts no swap as none free, so the swap condition is met.
func (m memory) swapFreePercent() float64 {
	if m.SwapTotal == 0 {
		return 0
	}
	return float64(m.SwapFree) * 100 / float64(m.SwapTotal)
}

// decide applies spec section 6.2. Pressure counts on Linux only with low
// memory, because it stays high for seconds after memory is freed.
func decide(m memory) action {
	if m.Level > 0 {
		switch {
		case m.Level >= 4:
			return forced
		case m.Level >= 2:
			return polite
		}
		return nothing
	}
	available, swap := m.availablePercent(), m.swapFreePercent()
	switch {
	case available <= 5 && swap <= 5:
		return forced
	case available <= 10 && swap <= 10:
		return polite
	case m.FullAvg10 >= 50 && available <= 20:
		return polite
	}
	return nothing
}

// delay is the time to the next tick. On Linux it follows earlyoom: the room
// left above the polite-stop line over fill rates of 6000 MiB/s for memory
// and 800 MiB/s for swap, clamped to 100 ms–1 s. On macOS it is 100 ms at
// warning or above, else 1 s.
func delay(m memory) time.Duration {
	if m.Level > 0 {
		if m.Level >= 2 {
			return 100 * time.Millisecond
		}
		return time.Second
	}
	const mib = 1 << 20
	memoryRoom := max((m.availablePercent()-10)/100*float64(m.Total), 0)
	swapRoom := max((m.swapFreePercent()-10)/100*float64(m.SwapTotal), 0)
	seconds := memoryRoom/(6000*mib) + swapRoom/(800*mib)
	return min(max(time.Duration(seconds*float64(time.Second)), 100*time.Millisecond), time.Second)
}

// reason is what the agent is told: the command, its size, the machine's
// state and what to try.
func reason(command string, children int, size uint64, m memory) string {
	var state string
	switch {
	case m.Level > 0:
		level := "warning"
		if m.Level >= 4 {
			level = "critical"
		}
		state = fmt.Sprintf("memory pressure %s, %s installed, %s of swap in use",
			level, gigabytes(m.Installed), gigabytes(m.SwapTotal-m.SwapFree))
	case m.SwapTotal == 0:
		state = fmt.Sprintf(
			"%s of %s available, no swap",
			gigabytes(m.Available),
			gigabytes(m.Installed),
		)
	default:
		state = fmt.Sprintf("%s of %s available, swap %.0f %% used",
			gigabytes(m.Available), gigabytes(m.Installed), 100-m.swapFreePercent())
	}
	processes := "child processes"
	if children == 1 {
		processes = "child process"
	}
	return fmt.Sprintf("workbench guard: stopped `%s` and its %d %s. The machine was nearly "+
		"out of memory (%s) and this was the largest agent command, at %s. Retry with less "+
		"parallelism or a smaller input.", clip(command), children, processes, state, gigabytes(size))
}

// clip makes a command fit one line of the reason: no newlines or
// backticks, at most 120 characters.
func clip(command string) string {
	command = strings.Join(strings.Fields(strings.ReplaceAll(command, "`", "'")), " ")
	if runes := []rune(command); len(runes) > 120 {
		return string(runes[:119]) + "…"
	}
	return command
}

func gigabytes(bytes uint64) string {
	value := float64(bytes) / (1 << 30)
	if value >= 10 {
		return fmt.Sprintf("%.0f GB", value)
	}
	return fmt.Sprintf("%.1f GB", value)
}
```

- [ ] **Step 2: Write `platform_darwin.go`** (the whole file, replacing Task 3's stub)

```go
//go:build darwin

package guard

import (
	"bytes"
	"encoding/binary"
	"errors"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"
)

// darwinZombie is SZOMB, kinfo_proc's p_stat for a zombie.
const darwinZombie = 5

// startSlack is one second in proc.Start's units: microseconds of
// kinfo_proc's p_starttime. That start time is the wall clock when the
// process began, which the kernel records for the pre hook and for the
// command alike, so only a clock step between the two can shift them.
const startSlack = 1_000_000

func fromKinfo(k *unix.KinfoProc) proc {
	started := k.Proc.P_starttime
	return proc{
		PID:    int(k.Proc.P_pid),
		PPID:   int(k.Eproc.Ppid),
		PGID:   int(k.Eproc.Pgid),
		Start:  uint64(started.Sec)*1_000_000 + uint64(started.Usec),
		Name:   unix.ByteSliceToString(k.Proc.P_comm[:]),
		Zombie: k.Proc.P_stat == darwinZombie,
	}
}

func snapshotNow() (snapshot, error) {
	all, err := unix.SysctlKinfoProcSlice("kern.proc.all")
	if err != nil {
		return nil, err
	}
	s := make(snapshot, len(all))
	for i := range all {
		p := fromKinfo(&all[i])
		s[p.PID] = p
	}
	return s, nil
}

func processInfo(pid int) (proc, error) {
	k, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil {
		return proc{}, err
	}
	if int(k.Proc.P_pid) != pid {
		return proc{}, errors.New("no such process")
	}
	return fromKinfo(k), nil
}

// readArgs reads kern.procargs2: argc as a 32-bit integer, the executable
// path, NUL padding, then argc NUL-terminated arguments.
func readArgs(pid int) []string {
	data, err := unix.SysctlRaw("kern.procargs2", pid)
	if err != nil || len(data) < 4 {
		return nil
	}
	argc := int(binary.LittleEndian.Uint32(data[:4]))
	rest := data[4:]
	end := bytes.IndexByte(rest, 0)
	if end < 0 {
		return nil
	}
	rest = bytes.TrimLeft(rest[end:], "\x00")
	args := make([]string, 0, argc)
	for len(args) < argc && len(rest) > 0 {
		end = bytes.IndexByte(rest, 0)
		if end < 0 {
			return append(args, string(rest))
		}
		args = append(args, string(rest[:end]))
		rest = rest[end+1:]
	}
	return args
}

// readMemory reads the kernel's pressure level, the RAM and vm.swapusage
// (struct xsw_usage: total, avail, used as uint64).
func readMemory() (memory, error) {
	level, err := unix.SysctlUint32("kern.memorystatus_vm_pressure_level")
	if err != nil {
		return memory{}, err
	}
	installed, err := unix.SysctlUint64("hw.memsize")
	if err != nil {
		return memory{}, err
	}
	m := memory{Level: int(level), Installed: installed, FullAvg10: -1}
	if swap, err := unix.SysctlRaw("vm.swapusage"); err == nil && len(swap) >= 24 {
		m.SwapTotal = binary.LittleEndian.Uint64(swap[0:8])
		m.SwapFree = binary.LittleEndian.Uint64(swap[8:16])
	}
	return m, nil
}

// footprint is the process's physical footprint, the figure Activity Monitor
// shows, from the raw proc_info syscall: PROC_INFO_CALL_PIDRUSAGE (9),
// flavor RUSAGE_INFO_V0 (0), whose ri_phys_footprint is the uint64 at offset
// 72. x/sys has no wrapper and cgo's proc_pid_rusage would break
// CGO_ENABLED=0 release builds, so this is the raw syscall, as
// internal/operation/image_metadata_darwin.go does for ACLs; recheck the
// layout on each major macOS release. The buffer is larger than the 96 bytes
// rusage_info_v0 holds.
func footprint(pid int) (uint64, error) {
	var info [256]byte
	_, _, errno := unix.Syscall6(
		unix.SYS_PROC_INFO, //nolint:staticcheck // See the function comment.
		9,
		uintptr(pid),
		0,
		0,
		uintptr(unsafe.Pointer(&info[0])),
		0,
	)
	if errno != 0 {
		return 0, errno
	}
	return binary.LittleEndian.Uint64(info[72:80]), nil
}

// send signals t once its start time still matches the snapshot it came
// from; a process that exited, is a zombie or whose PID was reused is left
// alone, so it reports true only for a live process.
func send(t target, sig unix.Signal) bool {
	p, err := processInfo(t.PID)
	if err != nil || p.Start != t.Start || p.Zombie {
		return false
	}
	pid := t.PID
	if t.Group {
		pid = -pid
	}
	return unix.Kill(pid, sig) == nil
}

// waiter sleeps between ticks and wakes as soon as a watched agent exits,
// through kqueue's EVFILT_PROC NOTE_EXIT. Without a kqueue it just sleeps.
type waiter struct {
	kq      int
	watched map[int]bool
}

func newWaiter() *waiter {
	kq, err := unix.Kqueue()
	if err != nil {
		kq = -1
	}
	return &waiter{kq: kq, watched: map[int]bool{}}
}

func (w *waiter) watch(pid int) {
	if w.kq < 0 || w.watched[pid] {
		return
	}
	var event unix.Kevent_t
	unix.SetKevent(&event, pid, unix.EVFILT_PROC, unix.EV_ADD|unix.EV_ONESHOT)
	event.Fflags = unix.NOTE_EXIT
	if _, err := unix.Kevent(w.kq, []unix.Kevent_t{event}, nil, nil); err == nil {
		w.watched[pid] = true
	}
}

func (w *waiter) wait(d time.Duration) {
	if w.kq < 0 {
		time.Sleep(d)
		return
	}
	events := make([]unix.Kevent_t, 8)
	timeout := unix.NsecToTimespec(d.Nanoseconds())
	n, err := unix.Kevent(w.kq, nil, events, &timeout)
	if err != nil {
		return
	}
	for _, event := range events[:n] {
		delete(w.watched, int(event.Ident))
	}
}

func (w *waiter) close() {
	if w.kq >= 0 {
		_ = unix.Close(w.kq)
	}
}
```

- [ ] **Step 3: Write `platform_linux.go`** (the whole file, replacing Task 3's stub)

```go
//go:build linux

package guard

import (
	"bytes"
	"errors"
	"os"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

// startSlack is one second in proc.Start's units: clock ticks since boot
// (USER_HZ, 100 on every architecture Workbench ships for), the unit of
// /proc/<pid>/stat's starttime. Ticks since boot never follow a wall-clock
// step, such as WSL2's resync after the host sleeps.
const startSlack = 100

// processInfo parses /proc/<pid>/stat. The name sits between the first "("
// and the last ")", so a name holding either cannot shift the fields after
// it: state (field 3), ppid (4), pgrp (5) and starttime (22).
func processInfo(pid int) (proc, error) {
	data, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return proc{}, err
	}
	open, end := bytes.IndexByte(data, '('), bytes.LastIndexByte(data, ')')
	if open < 0 || end < open || end+2 > len(data) {
		return proc{}, errors.New("unreadable stat")
	}
	fields := strings.Fields(string(data[end+2:]))
	if len(fields) < 20 {
		return proc{}, errors.New("short stat")
	}
	ppid, err1 := strconv.Atoi(fields[1])
	pgid, err2 := strconv.Atoi(fields[2])
	start, err3 := strconv.ParseUint(fields[19], 10, 64)
	if err := errors.Join(err1, err2, err3); err != nil {
		return proc{}, err
	}
	return proc{
		PID:    pid,
		PPID:   ppid,
		PGID:   pgid,
		Start:  start,
		Name:   string(data[open+1 : end]),
		Zombie: fields[0] == "Z",
	}, nil
}

func snapshotNow() (snapshot, error) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil, err
	}
	s := make(snapshot, len(entries))
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil {
			continue
		}
		if p, err := processInfo(pid); err == nil {
			s[pid] = p
		}
	}
	return s, nil
}

func readArgs(pid int) []string {
	data, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/cmdline")
	if err != nil || len(data) == 0 {
		return nil
	}
	return strings.Split(strings.TrimSuffix(string(data), "\x00"), "\x00")
}

// readMemory reads /proc/meminfo and, when the kernel has it,
// /proc/pressure/memory.
func readMemory() (memory, error) {
	data, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return memory{}, err
	}
	values := map[string]uint64{}
	for line := range strings.SplitSeq(string(data), "\n") {
		name, rest, ok := strings.Cut(line, ":")
		if fields := strings.Fields(rest); ok && len(fields) > 0 {
			if kib, err := strconv.ParseUint(fields[0], 10, 64); err == nil {
				values[name] = kib * 1024
			}
		}
	}
	for _, name := range []string{"MemTotal", "MemAvailable", "AnonPages", "SwapTotal", "SwapFree"} {
		if _, ok := values[name]; !ok {
			return memory{}, errors.New("/proc/meminfo has no " + name)
		}
	}
	m := memory{
		Available: values["MemAvailable"],
		Total:     values["MemAvailable"] + values["AnonPages"],
		Installed: values["MemTotal"],
		SwapFree:  values["SwapFree"],
		SwapTotal: values["SwapTotal"],
		FullAvg10: -1,
	}
	if psi, err := os.ReadFile("/proc/pressure/memory"); err == nil {
		for line := range strings.SplitSeq(string(psi), "\n") {
			if rest, ok := strings.CutPrefix(line, "full "); ok {
				for field := range strings.FieldsSeq(rest) {
					if value, ok := strings.CutPrefix(field, "avg10="); ok {
						m.FullAvg10, _ = strconv.ParseFloat(value, 64)
					}
				}
			}
		}
	}
	return m, nil
}

// footprint is the process's proportional set size (Pss), which counts a
// shared page once across the processes sharing it.
func footprint(pid int) (uint64, error) {
	data, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/smaps_rollup")
	if err != nil {
		return 0, err
	}
	for line := range strings.SplitSeq(string(data), "\n") {
		if rest, ok := strings.CutPrefix(line, "Pss:"); ok {
			kib, err := strconv.ParseUint(
				strings.TrimSuffix(strings.TrimSpace(rest), " kB"),
				10,
				64,
			)
			return kib * 1024, err
		}
	}
	return 0, errors.New("smaps_rollup has no Pss line")
}

// send signals t through a pidfd once its start time still matches the
// snapshot and it is not a zombie, so it reports true only for a live
// process: a pidfd signal never reaches a later process that reuses the
// PID. A group is signalled with kill(-pgid) while the leader's pidfd is
// held. After SIGKILL, process_mrelease frees the memory at once, as earlyoom
// does; a kernel without it just frees it at exit.
func send(t target, sig unix.Signal) bool {
	fd, err := unix.PidfdOpen(t.PID, 0)
	if err != nil {
		return false
	}
	defer func() { _ = unix.Close(fd) }()
	if p, err := processInfo(t.PID); err != nil || p.Start != t.Start || p.Zombie {
		return false
	}
	if t.Group {
		err = unix.Kill(-t.PID, sig)
	} else {
		err = unix.PidfdSendSignal(fd, sig, nil, 0)
	}
	if err != nil {
		return false
	}
	if sig == unix.SIGKILL {
		_, _, _ = unix.Syscall(unix.SYS_PROCESS_MRELEASE, uintptr(fd), 0, 0)
	}
	return true
}

// waiter sleeps between ticks and wakes as soon as a watched agent exits,
// by polling the agents' pidfds. Without a pidfd it just sleeps.
type waiter struct{ fds map[int]int }

func newWaiter() *waiter { return &waiter{fds: map[int]int{}} }

func (w *waiter) watch(pid int) {
	if _, ok := w.fds[pid]; ok {
		return
	}
	if fd, err := unix.PidfdOpen(pid, 0); err == nil {
		w.fds[pid] = fd
	}
}

func (w *waiter) wait(d time.Duration) {
	if len(w.fds) == 0 {
		time.Sleep(d)
		return
	}
	polls := make([]unix.PollFd, 0, len(w.fds))
	pids := make([]int, 0, len(w.fds))
	for pid, fd := range w.fds {
		polls = append(polls, unix.PollFd{Fd: int32(fd), Events: unix.POLLIN})
		pids = append(pids, pid)
	}
	if _, err := unix.Poll(polls, int(d.Milliseconds())); err != nil {
		if !errors.Is(err, unix.EINTR) {
			time.Sleep(d)
		}
		return
	}
	for i, poll := range polls {
		if poll.Revents != 0 {
			_ = unix.Close(int(poll.Fd))
			delete(w.fds, pids[i])
		}
	}
}

func (w *waiter) close() {
	for _, fd := range w.fds {
		_ = unix.Close(fd)
	}
}
```

- [ ] **Step 4: Build for every target**

```bash
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build ./... && GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build ./... && \
GOOS=darwin GOARCH=amd64 CGO_ENABLED=0 go build ./... && CGO_ENABLED=0 go build ./...; echo EXIT:$?
```

Expected: `EXIT:0`. Don't commit yet. Nothing calls these readers until Task
5, so the `unused` linter would fail. Task 5 runs the full gates on both
platforms and commits Tasks 4 and 5 together.

---

### Task 5: State, hooks and the watcher

**Files:**
- Create: `internal/guard/state.go` (folders, JSON files, log)
- Create: `internal/guard/hooks.go` (`Pre`, `Post`, `findAgent`)
- Create: `internal/guard/watch.go` (`Watch` and its tick)

**Interfaces:**
- Consumes:
  - from Task 2: `operation.PrivateDirectory`, `operation.ReadHookInput`
  - existing: `operation.Resolve`, `operation.TryLock`
    (`internal/operation/state.go:455`) and `operation.StartDetached`
    (`internal/operation/process.go:273`)
  - from Tasks 3 and 4: everything listed there.
- Produces (used by Task 6):
  - `func Pre(stdin *os.File)`
  - `func Post(stdin *os.File, stdout io.Writer)`
  - `func Watch(ctx context.Context)`

- [ ] **Step 1: Write `state.go`**

```go
package guard

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/Sawmonabo/workbench/internal/operation"
)

const logCap = 1 << 20

// idPattern is a tool_use_id that may name a file; any other call is not
// guarded.
var idPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)

// kill is the record the watcher leaves for the post hook.
type kill struct {
	ToolUseID string    `json:"tool_use_id"`
	Reason    string    `json:"reason"`
	At        time.Time `json:"at"`
}

// folders is <state>/guard and its notes and killed folders, each private.
type folders struct{ root, notes, killed string }

func openFolders() (folders, error) {
	c, err := operation.Resolve(operation.Options{ReadOnly: true})
	if err != nil {
		return folders{}, err
	}
	var f folders
	for _, item := range []struct {
		name string
		path *string
	}{{"guard", &f.root}, {"guard/notes", &f.notes}, {"guard/killed", &f.killed}} {
		if *item.path, err = operation.PrivateDirectory(c.Paths.State, item.name); err != nil {
			return folders{}, err
		}
	}
	return f, nil
}

func (f folders) lock() string            { return filepath.Join(f.root, "watch.lock") }
func (f folders) log() string             { return filepath.Join(f.root, "watch.log") }
func (f folders) note(id string) string   { return filepath.Join(f.notes, id+".json") }
func (f folders) record(id string) string { return filepath.Join(f.killed, id+".json") }

// noteIDs are the IDs of the notes on disk. A temporary file from a write in
// progress ends in ".tmp-<pid>" and is skipped.
func (f folders) noteIDs() []string {
	entries, _ := os.ReadDir(f.notes)
	var ids []string
	for _, entry := range entries {
		if id, ok := strings.CutSuffix(entry.Name(), ".json"); ok && idPattern.MatchString(id) {
			ids = append(ids, id)
		}
	}
	return ids
}

// writeJSON writes value to path through a temporary name in the same folder,
// so a reader never sees half a file.
func writeJSON(path string, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	temporary := path + ".tmp-" + strconv.Itoa(os.Getpid())
	if err = os.WriteFile(temporary, data, 0o600); err != nil {
		return err
	}
	if err = os.Rename(temporary, path); err != nil {
		_ = os.Remove(temporary)
	}
	return err
}

func readJSON(path string, value any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, value)
}

// logf appends one line to the guard log, first moving a log over 1 MiB to
// watch.log.1, which replaces the one before.
func (f folders) logf(format string, args ...any) {
	path := f.log()
	if info, err := os.Stat(path); err == nil && info.Size() > logCap {
		_ = os.Rename(path, path+".1")
	}
	file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer func() { _ = file.Close() }()
	_, _ = fmt.Fprintf(file, "%s %d %s\n", time.Now().Format(time.RFC3339), os.Getpid(),
		fmt.Sprintf(format, args...))
}
```

- [ ] **Step 2: Write `hooks.go`**

```go
package guard

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
	"time"

	"github.com/Sawmonabo/workbench/internal/operation"
)

// hookInput is the part of a Claude Code or Codex hook's stdin the guard
// reads. Codex's carries turn_id and its tool_use_id starts "exec-"; Claude
// Code's starts "toolu_".
type hookInput struct {
	Event     string  `json:"hook_event_name"`
	Tool      string  `json:"tool_name"`
	ToolUseID string  `json:"tool_use_id"`
	SessionID string  `json:"session_id"`
	TurnID    *string `json:"turn_id"`
	Input     struct {
		Command string `json:"command"`
	} `json:"tool_input"`
}

func readInput(stdin *os.File) (hookInput, bool) {
	var input hookInput
	data := operation.ReadHookInput(stdin, 1<<20, 500*time.Millisecond)
	return input, json.Unmarshal(data, &input) == nil && idPattern.MatchString(input.ToolUseID)
}

// agentKind tells the agent from the hook input; an unknown one is not
// guarded.
func agentKind(input hookInput) (kind, bool) {
	switch {
	case strings.HasPrefix(input.ToolUseID, "toolu_") && input.TurnID == nil:
		return claude, true
	case input.TurnID != nil:
		return codex, true
	}
	return "", false
}

// findAgent is the hook's nearest ancestor that is not a shell, starting at
// pid: the agent that ran the hook through `sh -c`.
func findAgent(pid int) (proc, error) {
	for range 8 {
		p, err := processInfo(pid)
		if err != nil {
			return proc{}, err
		}
		if !shells[strings.TrimPrefix(p.Name, "-")] {
			return p, nil
		}
		if pid = p.PPID; pid <= 1 {
			break
		}
	}
	return proc{}, errors.New("no agent among the hook's ancestors")
}

// Pre is the PreToolUse hook on Bash. It leaves a note for the call and
// starts the watcher when none runs. It prints nothing and never fails:
// whatever goes wrong leaves the call unguarded, never blocked.
// WORKBENCH_GUARD=off turns it off; WORKBENCH_GUARD=dry-run notes the call so
// the watcher only logs what it would stop.
func Pre(stdin *os.File) {
	var f folders
	defer func() {
		if p := recover(); p != nil && f.root != "" {
			f.logf("pre: panic: %v", p)
		}
	}()
	mode := os.Getenv("WORKBENCH_GUARD")
	input, ok := readInput(stdin)
	if mode == "off" || !ok || input.Event != "PreToolUse" || input.Tool != "Bash" ||
		input.Input.Command == "" {
		return
	}
	agentType, known := agentKind(input)
	if !known {
		return
	}
	var err error
	if f, err = openFolders(); err != nil {
		return
	}
	agent, err := findAgent(os.Getppid())
	if err != nil {
		f.logf("pre %s: %v", input.ToolUseID, err)
		return
	}
	self, err := processInfo(os.Getpid())
	if err != nil {
		f.logf("pre %s: %v", input.ToolUseID, err)
		return
	}
	n := note{
		AgentPID: agent.PID, AgentStart: agent.Start, Kind: agentType,
		SessionID: input.SessionID, ToolUseID: input.ToolUseID, Command: input.Input.Command,
		NotedStart: self.Start, NotedAt: time.Now(), DryRun: mode == "dry-run",
	}
	if err = writeJSON(f.note(n.ToolUseID), n); err != nil {
		f.logf("pre %s: %v", n.ToolUseID, err)
		return
	}
	startWatcher(f)
}

// startWatcher starts `workbench guard watch` detached when the lock is free.
// The watcher takes the lock itself; one that loses a race exits at once.
func startWatcher(f folders) {
	release, held, err := operation.TryLock(f.lock())
	if err != nil || held {
		return
	}
	release()
	log, err := os.OpenFile(f.log(), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer func() { _ = log.Close() }()
	executable, err := os.Executable()
	if err == nil {
		err = operation.StartDetached(executable, []string{"guard", "watch"}, log)
	}
	if err != nil {
		f.logf("pre: cannot start the watcher: %v", err)
	}
}

// Post is the PostToolUse hook on Bash, and Claude Code's PostToolUseFailure.
// It drops the call's note and, when the watcher stopped this call, prints the
// reason as additionalContext, which the agent sees beside the tool result.
// Otherwise it prints nothing. It never fails.
func Post(stdin *os.File, stdout io.Writer) {
	var f folders
	defer func() {
		if p := recover(); p != nil && f.root != "" {
			f.logf("post: panic: %v", p)
		}
	}()
	input, ok := readInput(stdin)
	var err error
	if !ok {
		return
	}
	if f, err = openFolders(); err != nil {
		return
	}
	_ = os.Remove(f.note(input.ToolUseID))
	var record kill
	if readJSON(f.record(input.ToolUseID), &record) != nil {
		return
	}
	_ = os.Remove(f.record(input.ToolUseID))
	_ = json.NewEncoder(stdout).Encode(map[string]any{
		"hookSpecificOutput": map[string]string{
			"hookEventName":     input.Event,
			"additionalContext": record.Reason,
		},
	})
}
```

- [ ] **Step 3: Write `watch.go`**

```go
package guard

import (
	"context"
	"os"
	"path/filepath"
	"runtime/debug"
	"time"

	"golang.org/x/sys/unix"

	"github.com/Sawmonabo/workbench/internal/operation"
)

const (
	idleExit  = 10 * time.Minute
	cooldown  = 10 * time.Second
	killAfter = 5 * time.Second
	giveUp    = 10 * time.Second
	killKeep  = 24 * time.Hour
)

// seenNote is a note the watcher has read, kept so a note that goes away
// without ever being matched is logged: a sign an agent changed its command
// shape.
type seenNote struct {
	command string
	matched bool
}

type watcher struct {
	f         folders
	release   func()
	lockInfo  os.FileInfo
	tracked   map[string]command
	seen      map[string]seenNote
	stopped   []proc    // the last stopped command's processes
	quietFrom time.Time // when they were all gone
	// passed are commands never chosen again: a dry-run one already logged,
	// or one still running after a stop gave up on it.
	passed map[string]bool
	// ended are calls whose tracked command has ended. A call runs once, so
	// its note, which stays until the post hook or the agent's exit (forever
	// for a denied call), is never matched again.
	ended     map[string]bool
	idleSince time.Time
	expired   time.Time
	waiter    *waiter
}

// Watch is the watcher, at most one per user, started detached by the pre
// hook. It finds each noted command, watches machine memory, and near
// out-of-memory stops the largest agent command and records why. It returns
// after ten minutes with no note and no tracked command, when its lock file
// is no longer the one it locked, or when ctx ends. It has no terminal and
// logs only to the guard log.
func Watch(ctx context.Context) {
	debug.SetMemoryLimit(32 << 20)
	f, err := openFolders()
	if err != nil {
		return
	}
	w := &watcher{
		f: f, release: func() {}, tracked: map[string]command{}, seen: map[string]seenNote{},
		passed: map[string]bool{}, ended: map[string]bool{},
		idleSince: time.Now(), waiter: newWaiter(),
	}
	defer w.waiter.close()
	if !w.lock() {
		return
	}
	defer func() { w.release() }()
	f.logf("watch: started")
	for ctx.Err() == nil {
		if !w.ownsLock() {
			f.logf("watch: the lock file changed; exiting")
			return
		}
		next := w.tick()
		if time.Since(w.idleSince) >= idleExit && !w.carryOn() {
			f.logf("watch: idle for %s; exiting", idleExit)
			return
		}
		w.waiter.wait(next)
	}
}

func (w *watcher) lock() bool {
	release, held, err := operation.TryLock(w.f.lock())
	if err != nil || held {
		return false
	}
	info, err := os.Stat(w.f.lock())
	if err != nil {
		release()
		return false
	}
	w.release, w.lockInfo = release, info
	return true
}

// ownsLock reports whether the lock file is still the one this watcher
// locked (same device and inode).
func (w *watcher) ownsLock() bool {
	info, err := os.Stat(w.f.lock())
	return err == nil && os.SameFile(info, w.lockInfo)
}

// carryOn releases the lock at idle exit, then reads the notes once more. A
// live note written meanwhile makes it take the lock again and carry on,
// unless another watcher took it first; so a note written as a watcher stops
// is never left without one. Only notes that parse and whose agent runs
// count, so a broken note cannot keep the watcher alive.
func (w *watcher) carryOn() bool {
	w.release()
	w.release = func() {}
	s, err := snapshotNow()
	if err != nil || len(w.notes(s)) == 0 || !w.lock() {
		return false
	}
	w.idleSince = time.Now()
	return true
}

// tick runs one round and returns the time to the next.
func (w *watcher) tick() time.Duration {
	s, err := snapshotNow()
	if err != nil {
		w.f.logf("watch: cannot list processes: %v", err)
		return time.Second
	}
	notes := w.notes(s)
	w.forget(s)
	agents, protected := w.agents(s, notes)
	for _, agent := range agents {
		w.waiter.watch(agent)
		for _, p := range s.tree(agent, nil) {
			p.Args = readArgs(p.PID)
			s[p.PID] = p
		}
	}
	taken := map[int]bool{}
	for _, c := range w.tracked {
		taken[c.Root.PID] = true
	}
	for _, c := range match(s, w.untracked(notes), taken) {
		w.tracked[c.Note.ToolUseID] = c
		w.seen[c.Note.ToolUseID] = seenNote{c.Note.Command, true}
		w.f.logf(
			"watch: tracking %s (pid %d): %s",
			c.Note.ToolUseID,
			c.Root.PID,
			clip(c.Note.Command),
		)
	}
	if len(notes) > 0 || len(w.tracked) > 0 {
		w.idleSince = time.Now()
	}
	w.expireKills()
	m, err := readMemory()
	if err != nil {
		w.f.logf("watch: cannot read memory: %v", err)
		return time.Second
	}
	if !w.coolingDown(s) && decide(m) != nothing && len(w.tracked) > 0 {
		w.stopLargest(s, protected)
	}
	return delay(m)
}

// notes reads the notes on disk. A note that does not parse is deleted: the
// pre hook renames a complete file into place, so a broken one never heals.
// So is a note whose agent (PID and start time) is gone. A note that goes
// away without ever being matched is logged.
func (w *watcher) notes(s snapshot) []note {
	present := map[string]bool{}
	var notes []note
	for _, id := range w.f.noteIDs() {
		var n note
		path := w.f.note(id)
		if readJSON(path, &n) != nil || n.ToolUseID != id || !s.alive(n.AgentPID, n.AgentStart) {
			_ = os.Remove(path)
			continue
		}
		present[id] = true
		if _, ok := w.seen[id]; !ok {
			w.seen[id] = seenNote{command: n.Command}
		}
		notes = append(notes, n)
	}
	for id, seen := range w.seen {
		if !present[id] {
			if !seen.matched {
				w.f.logf("watch: note %s ended unmatched: %s", id, clip(seen.command))
			}
			delete(w.seen, id)
			delete(w.ended, id)
		}
	}
	return notes
}

// forget stops tracking commands that ended or whose agent is gone. A call
// whose command ended is remembered until its note goes, so the note cannot
// claim a later command with the same text.
func (w *watcher) forget(s snapshot) {
	for id, c := range w.tracked {
		agentAlive := s.alive(c.Note.AgentPID, c.Note.AgentStart)
		if !s.alive(c.Root.PID, c.Root.Start) || !agentAlive {
			delete(w.tracked, id)
			delete(w.passed, id)
			if agentAlive {
				w.ended[id] = true
			}
		}
	}
}

// agents are the live agents of the notes and tracked commands. protected is
// them and the watcher: never signalled, nor anything under them.
func (w *watcher) agents(s snapshot, notes []note) ([]int, map[int]bool) {
	protected := map[int]bool{os.Getpid(): true}
	var agents []int
	add := func(pid int, start uint64) {
		if !protected[pid] && s.alive(pid, start) {
			protected[pid] = true
			agents = append(agents, pid)
		}
	}
	for _, n := range notes {
		add(n.AgentPID, n.AgentStart)
	}
	for _, c := range w.tracked {
		add(c.Note.AgentPID, c.Note.AgentStart)
	}
	return agents, protected
}

// untracked are the notes still waiting for their command: not tracked, and
// not a call whose command already ran.
func (w *watcher) untracked(notes []note) []note {
	var out []note
	for _, n := range notes {
		if _, ok := w.tracked[n.ToolUseID]; !ok && !w.ended[n.ToolUseID] {
			out = append(out, n)
		}
	}
	return out
}

// expireKills deletes kill records over a day old, at most once a minute.
func (w *watcher) expireKills() {
	if time.Since(w.expired) < time.Minute {
		return
	}
	w.expired = time.Now()
	entries, _ := os.ReadDir(w.f.killed)
	for _, entry := range entries {
		if info, err := entry.Info(); err == nil && time.Since(info.ModTime()) > killKeep {
			_ = os.Remove(filepath.Join(w.f.killed, entry.Name()))
		}
	}
}

// coolingDown is true from a stop until the stopped command's processes are
// gone and ten more seconds have passed, so the pressure figures catch up.
func (w *watcher) coolingDown(s snapshot) bool {
	if w.stopped == nil {
		return false
	}
	for _, p := range w.stopped {
		if s.alive(p.PID, p.Start) {
			w.quietFrom = time.Time{}
			return true
		}
	}
	if w.quietFrom.IsZero() {
		w.quietFrom = time.Now()
	}
	if time.Since(w.quietFrom) < cooldown {
		return true
	}
	w.stopped = nil
	return false
}

// stopLargest stops the tracked command with the largest footprint over its
// tree (without protected processes); a tie goes to the older command. A
// passed command is left out: a dry-run one already logged, so it neither
// repeats in the log nor hides a real one, and one a stop gave up on, so it
// cannot hold the watcher ten seconds every round.
func (w *watcher) stopLargest(s snapshot, protected map[int]bool) {
	skip := func(p proc) bool { return protected[p.PID] }
	var chosen command
	var chosenSize uint64
	var members []proc
	for id, c := range w.tracked {
		if w.passed[id] {
			continue
		}
		tree := s.tree(c.Root.PID, skip)
		var size uint64
		for _, p := range tree {
			bytes, err := footprint(p.PID)
			if err != nil {
				w.f.logf("watch: size of pid %d: %v; counted as 0", p.PID, err)
			}
			size += bytes
		}
		if members == nil || size > chosenSize ||
			size == chosenSize && c.Root.Start < chosen.Root.Start {
			chosen, chosenSize, members = c, size, tree
		}
	}
	if len(members) > 0 {
		w.stop(chosen, chosenSize, len(members)-1, protected)
	}
}

// stop re-checks memory, snapshots the command again, records the reason and
// signals it. The record is written before any signal, because the agent's
// post hook may run the moment the command dies; it is deleted when nothing
// was signalled. With WORKBENCH_GUARD=dry-run on the note it only logs.
func (w *watcher) stop(c command, size uint64, children int, protected map[int]bool) {
	m, err := readMemory()
	if err != nil || decide(m) == nothing {
		w.f.logf("watch: memory recovered before stopping %s", c.Note.ToolUseID)
		return
	}
	s, err := snapshotNow()
	if err != nil {
		return
	}
	chosen := targets(s, c.Root, protected)
	if len(chosen) == 0 {
		return
	}
	text := reason(c.Note.Command, children, size, m)
	if c.Note.DryRun {
		w.passed[c.Note.ToolUseID] = true
		w.f.logf("watch: dry run, would stop %s: %s", c.Note.ToolUseID, text)
		return
	}
	w.stopped = s.tree(c.Root.PID, func(p proc) bool { return protected[p.PID] || p.PID <= 1 })
	w.quietFrom = time.Time{}
	record := w.f.record(c.Note.ToolUseID)
	if err = writeJSON(
		record,
		kill{ToolUseID: c.Note.ToolUseID, Reason: text, At: time.Now()},
	); err != nil {
		w.f.logf("watch: cannot record the stop of %s: %v", c.Note.ToolUseID, err)
	}
	w.f.logf("watch: stopping %s: %s", c.Note.ToolUseID, text)
	if !w.signal(chosen, protected, c.Note.ToolUseID) {
		_ = os.Remove(record)
		w.f.logf("watch: %s ended before it could be stopped", c.Note.ToolUseID)
	}
}

// signal sends SIGTERM, then SIGKILL once memory reaches the forced-stop line
// or after five seconds, polling every 100 ms until the command's processes
// are gone, and gives up after ten seconds. It reports whether any live
// target was signalled.
func (w *watcher) signal(chosen []target, protected map[int]bool, id string) bool {
	sent := false
	for _, t := range chosen {
		sent = send(t, unix.SIGTERM) || sent
	}
	if !sent {
		return false
	}
	killed := false
	for start := time.Now(); time.Since(start) < giveUp; time.Sleep(100 * time.Millisecond) {
		if s, err := snapshotNow(); err == nil && !w.anyAlive(s) {
			return true
		}
		if m, err := readMemory(); !killed &&
			(time.Since(start) >= killAfter || err == nil && decide(m) == forced) {
			w.escalate(protected)
			killed = true
		}
	}
	w.abandon(id)
	return true
}

// escalate sends SIGKILL, by PID, to every stopped process still alive and to
// anything started since under one still running. It does not signal the
// group: a shell that died of SIGTERM was the group leader, and once it is
// reaped the group is no longer known to be the command's, while a child that
// ignored SIGTERM lives on under PID 1. A process started under one that has
// since exited is out of reach (spec 12).
func (w *watcher) escalate(protected map[int]bool) {
	s, err := snapshotNow()
	if err != nil {
		return
	}
	known := map[int]bool{}
	for _, p := range w.stopped {
		known[p.PID] = true
	}
	var live []proc
	for _, p := range w.stopped {
		if !s.alive(p.PID, p.Start) {
			continue
		}
		live = append(
			live,
			s.tree(p.PID, func(q proc) bool { return protected[q.PID] || q.PID <= 1 })...)
	}
	for _, p := range live {
		if !p.Zombie {
			send(target{PID: p.PID, Start: p.Start}, unix.SIGKILL)
		}
		if !known[p.PID] {
			known[p.PID] = true
			w.stopped = append(w.stopped, p)
		}
	}
}

// abandon gives up on a stopped command that is still running after ten
// seconds, such as one in uninterruptible I/O: it logs what survived, ends
// the cooldown and passes the call over from now on, so one process that
// cannot be stopped never keeps the watcher from acting again.
func (w *watcher) abandon(id string) {
	var left []int
	if s, err := snapshotNow(); err == nil {
		for _, p := range w.stopped {
			if s.alive(p.PID, p.Start) {
				left = append(left, p.PID)
			}
		}
	}
	w.f.logf("watch: gave up on %s after %s; still running: %v; not chosen again", id, giveUp, left)
	w.stopped = nil
	w.passed[id] = true
}

// alive reports whether the process pid with this start time still runs; a
// zombie holds no memory and counts as gone.
func (s snapshot) alive(pid int, start uint64) bool {
	p, ok := s[pid]
	return ok && p.Start == start && !p.Zombie
}

func (w *watcher) anyAlive(s snapshot) bool {
	for _, p := range w.stopped {
		if s.alive(p.PID, p.Start) {
			return true
		}
	}
	return false
}
```

- [ ] **Step 4: Run the gates on both platforms**

```bash
golangci-lint fmt && golangci-lint config verify && golangci-lint run ./... && go build ./... && go test ./...; echo EXIT:$?
GOOS=linux CGO_ENABLED=0 go build ./... && GOOS=linux golangci-lint run ./internal/guard/...; echo EXIT:$?
```

Expected: `EXIT:0` twice. `Pre`, `Post` and `Watch` are exported, so they
don't count as unused before Task 6 wires them. A helper reported unused
means Tasks 4 and 5 disagree on a name: fix the name, and don't add a
suppression.

- [ ] **Step 5: Commit Tasks 4 and 5**

```bash
git add internal/guard/memory.go internal/guard/platform_darwin.go internal/guard/platform_linux.go \
  internal/guard/state.go internal/guard/hooks.go internal/guard/watch.go
git commit -F - <<'EOF'
feat(guard): note agent commands and stop the largest near out-of-memory

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
EOF
echo EXIT:$?
```

---

### Task 6: The hidden `guard` commands

**Files:**
- Create: `internal/cli/guard.go`
- Modify: `internal/cli/root.go:100-112` (add to `root.AddCommand`)

**Interfaces:**
- Consumes: `guard.Pre`, `guard.Post` and `guard.Watch` (Task 5).
- Produces:
  - `workbench guard pre`, `workbench guard post`, `workbench guard watch`
  - `func guardCommand() *cobra.Command`

- [ ] **Step 1: Write `internal/cli/guard.go`**

```go
package cli

import (
	"os"

	"github.com/spf13/cobra"

	"github.com/Sawmonabo/workbench/internal/guard"
)

// guardCommand groups the agent command guard's entries. Claude Code and
// Codex run pre and post as hooks, and pre starts watch. Like askpass they
// are plain commands, not actions: they resolve no context, print no result
// envelope and take any arguments, so nothing an agent passes makes them
// exit non-zero. pre and post print only what the hook protocol reads.
func guardCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:    "guard",
		Short:  "Agent command memory guard (hooks and watcher)",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE:   showHelp,
	}
	cmd.AddCommand(
		&cobra.Command{
			Use:  "pre",
			Args: cobra.ArbitraryArgs,
			RunE: func(cmd *cobra.Command, _ []string) error {
				guard.Pre(stdinFile(cmd))
				return nil
			},
		},
		&cobra.Command{
			Use:  "post",
			Args: cobra.ArbitraryArgs,
			RunE: func(cmd *cobra.Command, _ []string) error {
				guard.Post(stdinFile(cmd), cmd.OutOrStdout())
				return nil
			},
		},
		&cobra.Command{
			Use:  "watch",
			Args: cobra.ArbitraryArgs,
			RunE: func(cmd *cobra.Command, _ []string) error {
				guard.Watch(cmd.Context())
				return nil
			},
		},
	)
	return cmd
}

func stdinFile(cmd *cobra.Command) *os.File {
	file, _ := cmd.InOrStdin().(*os.File)
	return file
}
```

- [ ] **Step 2: Register it**

In `internal/cli/root.go` `newRoot`, add `guardCommand(),` after
`costsCommand(o),` in `root.AddCommand(...)`.

- [ ] **Step 3: Run the gates**

```bash
golangci-lint fmt && golangci-lint config verify && golangci-lint run ./... && go build ./... && go test ./...; echo EXIT:$?
```

Expected: `EXIT:0`.

- [ ] **Step 4: Smoke-check with a synthetic agent, in dry-run, in an isolated state folder**

This never signals anything: notes are dry-run, and this Mac is at pressure
level 1. It checks the hook protocol, the note, the watcher start, matching,
the post hook and a crashed watcher (Review Focus 5).

Write `guard-smoke/agent.py` in the session scratchpad. It's a stand-in for
Codex: it pipes hook input to the hooks and runs the command as `/bin/zsh -c
<command>` in its own process group, the shape spec 2 observed.

```python
import json, subprocess, sys
# agent.py WORKBENCH ID COMMAND [ID COMMAND ...] runs each call in turn from
# this one process, as one agent session would. An ID starting with "denied-"
# is a call the user denied: its pre hook runs, the command never does, and no
# post hook fires.
workbench, calls = sys.argv[1], sys.argv[2:]
def hook(name, event, tool_use_id, command):
    payload = {"hook_event_name": event, "tool_name": "Bash", "tool_use_id": tool_use_id,
               "session_id": "s1", "turn_id": "t1", "tool_input": {"command": command}}
    out = subprocess.run(["/bin/sh", "-c", f"'{workbench}' guard {name} || true"],
                         input=json.dumps(payload), capture_output=True, text=True)
    print(f"{tool_use_id} {name}: exit {out.returncode} stdout {out.stdout!r}", flush=True)
for tool_use_id, command in zip(calls[::2], calls[1::2]):
    hook("pre", "PreToolUse", tool_use_id, command)
    if tool_use_id.startswith("denied-"):
        continue
    child = subprocess.Popen(["/bin/zsh", "-c", command], process_group=0)
    child.wait()
    # Python reports a process ended by a signal as minus the signal: -15 for
    # SIGTERM, -9 for SIGKILL.
    print(f"{tool_use_id} command: returncode {child.returncode}", flush=True)
    hook("post", "PostToolUse", tool_use_id, command)
```

Its arguments are the `workbench` to run, then one or more `tool_use_id`
and command pairs, run in turn from the one process, as one agent session
would. An ID starting with `denied-` is a call the user denied: only its pre
hook runs.

Run it:

```bash
S=<scratchpad>/guard-smoke && mkdir -p "$S" && chmod 700 "$S"
go build -o "$S/workbench" ./cmd/workbench
export WORKBENCH_STATE_DIR="$S/state" WORKBENCH_GUARD=dry-run
python3 -I "$S/agent.py" "$S/workbench" exec-smoke-1 'sleep 3; echo done'; echo EXIT:$?
G="$S/state/guard"; ls -la "$G" "$G/notes" "$G/killed"; echo EXIT:$?
rg -n "started|tracking|unmatched" "$G/watch.log"; echo EXIT:$?
```

Expected:
- `exec-smoke-1 pre: exit 0 stdout ''`, `done`, `exec-smoke-1 command:
  returncode 0`, then `exec-smoke-1 post: exit 0 stdout ''`.
- `$G` is `drwx------`, `notes/` is empty after post, and `killed/` is empty.
- The log has `watch: started` and `watch: tracking exec-smoke-1 (pid N):
  sleep 3; echo done`.

Run it once more with a single command, `python3 -I "$S/agent.py"
"$S/workbench" exec-smoke-2 'sleep 3'`. The log must show `tracking
exec-smoke-2 … sleep 3`: zsh execs `sleep` in place, and the single-command
rule matches it.

Then check that a denied call claims at most one later command (Review Focus
4):

```bash
python3 -I "$S/agent.py" "$S/workbench" denied-1 'sleep 3' exec-r1 'sleep 3' exec-r2 'sleep 3' \
  exec-r3 'sleep 3' >/dev/null; echo EXIT:$?
rg -n "tracking (denied|exec-r)|exec-r. ended unmatched" "$S/state/guard/watch.log"; echo EXIT:$?
```

Expected:
- `tracking denied-1` once, for the first command.
- No `ended unmatched` line: `exec-r1`'s note is dropped by its post hook
  silently, because the command it ran is tracked under `denied-1`.
- `tracking exec-r2` and `tracking exec-r3`.
- `denied-1` is never tracked a second time.

Then check that a crashed watcher frees the lock:

```bash
pgrep -f "$S/workbench guard watch"; echo EXIT:$?
pkill -KILL -f "$S/workbench guard watch"; sleep 1
python3 -I "$S/agent.py" "$S/workbench" exec-smoke-3 'sleep 2'; echo EXIT:$?
pgrep -f "$S/workbench guard watch"; echo EXIT:$?
rg -c "watch: started" "$S/state/guard/watch.log"; echo EXIT:$?
```

Expected: a new watcher PID, and the count of `watch: started` rises by one.
Finally, `pkill -TERM -f "$S/workbench guard watch"` and remove `$S`. The
`-f` pattern names the scratch binary only, so no other process can match.

- [ ] **Step 5: Commit**

```bash
git add internal/cli/guard.go internal/cli/root.go
git commit -F - <<'EOF'
feat(cli): add the hidden guard hook and watcher commands

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
EOF
echo EXIT:$?
```

---

### Task 7: Wire the hooks for Claude Code and Codex

**Files:**
- Modify: `home/.chezmoidata/claude.json`, `claude.enforced.hooks`
  (after `"SessionEnd"`)
- Modify: `home/private_dot_claude/modify_private_settings.json:19-43`, the
  copy check
- Modify: `home/.chezmoitemplates/codex-hooks.json.tmpl`

**Interfaces:**
- Consumes: the `guard pre|post` commands (Task 6).
- Produces the enforced hooks:
  - Claude Code: `PreToolUse`, `PostToolUse`, `PostToolUseFailure`, matcher
    `Bash`
  - Codex: `PreToolUse`, `PostToolUse`, matcher `Bash`.

  Codex trust entries follow from the existing `hook_keys()`/`hook_hash()` in
  `home/private_dot_codex/modify_private_config.toml.tmpl:185-232`, which
  cover every event in `HOOK_EVENTS`.

- [ ] **Step 1: Claude Code hooks**

In `home/.chezmoidata/claude.json`, inside `"hooks"` after the `SessionEnd`
array, add:

```json
        "PreToolUse": [
          {
            "matcher": "Bash",
            "hooks": [
              { "type": "command", "command": "~/.local/bin/workbench guard pre || true", "timeout": 5 }
            ]
          }
        ],
        "PostToolUse": [
          {
            "matcher": "Bash",
            "hooks": [
              { "type": "command", "command": "~/.local/bin/workbench guard post || true", "timeout": 5 }
            ]
          }
        ],
        "PostToolUseFailure": [
          {
            "matcher": "Bash",
            "hooks": [
              { "type": "command", "command": "~/.local/bin/workbench guard post || true", "timeout": 5 }
            ]
          }
        ]
```

- [ ] **Step 2: Treat a live guard hook as a copy of the managed one**

In `home/private_dot_claude/modify_private_settings.json`:
- Replace the condition
  `{{- if or (contains "workbench costs" $command) (contains ".local/bin/claude-costs" $command) -}}`
  with
  `{{- if or (contains "workbench costs" $command) (contains "workbench guard" $command) (contains ".local/bin/claude-costs" $command) -}}`.
- Extend the comment above it: "A group that runs `workbench guard` is
  likewise a copy of the managed guard hook."

- [ ] **Step 3: Codex hooks**

In `home/.chezmoitemplates/codex-hooks.json.tmpl`, add a second line under
the `$ingest` line:

```
{{- $guard := printf "%s/.local/bin/workbench" .chezmoi.homeDir | replace "'" "'\"'\"'" | printf "'%s' guard" -}}
```

Then replace the end of the `SessionEnd` array:

```
          { "type": "command", "command": {{ $ingest | toJson }}, "timeout": 3 }
        ]
      }
    ]{{ if .has_work }},
```

with:

```
          { "type": "command", "command": {{ $ingest | toJson }}, "timeout": 3 }
        ]
      }
    ],
    "PreToolUse": [
      {
        "matcher": "Bash",
        "hooks": [
          { "type": "command", "command": {{ printf "%s pre || true" $guard | toJson }}, "timeout": 5 }
        ]
      }
    ],
    "PostToolUse": [
      {
        "matcher": "Bash",
        "hooks": [
          { "type": "command", "command": {{ printf "%s post || true" $guard | toJson }}, "timeout": 5 }
        ]
      }
    ]{{ if .has_work }},
```

The work role's `UserPromptSubmit` stays last.

- [ ] **Step 4: Render every role and mode**

```bash
for role in personal work both; do for mode in pinned latest; do
  scripts/render-check.sh "$role" "$mode" || echo "FAIL $role $mode"
done; done; scripts/render-check.sh both pinned wsl; echo EXIT:$?
```

Expected: no `FAIL` line, then `EXIT:0`.

- [ ] **Step 5: Inspect the rendered hooks and trust entries**

Render the `both` role into a disposable destination, never the real home:

```bash
D=<scratchpad>/guard-render && rm -rf "$D" && mkdir -p "$D/home" "$D/bin"
go build -o "$D/bin/workbench" ./cmd/workbench
config=$(scripts/scratch-init.sh both pinned)
PATH="$D/bin:$PATH" chezmoi --config "$config" --source "$PWD" --destination "$D/home" \
  --persistent-state "$D/state.boltdb" --cache "$D/cache" --no-pager --use-builtin-diff \
  --refresh-externals=never --use-builtin-git=true apply --exclude scripts >/dev/null; echo EXIT:$?
python3 -I -c 'import json,sys; print(sorted(json.load(open(sys.argv[1]))["hooks"]))' "$D/home/.codex/hooks.json"
python3 -I -c 'import json,sys; print(sorted(k for k in json.load(open(sys.argv[1]))["hooks"] if "Tool" in k))' "$D/home/.claude/settings.json"
rg -n "pre_tool_use|post_tool_use" -A1 "$D/home/.codex/config.toml"; echo EXIT:$?
rm -rf "$D" "$(dirname "$config")"
```

Expected:
- `EXIT:0` after the render.
- `['PostToolUse', 'PreToolUse', 'SessionEnd', 'SessionStart', 'UserPromptSubmit']`.
- `['PostToolUse', 'PostToolUseFailure', 'PreToolUse']`.
- Two `[hooks.state."…/.codex/hooks.json:pre_tool_use:0:0"]` /
  `…:post_tool_use:0:0` tables, each with a `trusted_hash`.

The Codex paths name the real home, because `.chezmoi.homeDir` is the real
home; only the destination is disposable.

- [ ] **Step 6: The hooks fail open (Review Focus 3)**

The "older release" is a build of `main`, which has no `guard` command:

```bash
S=<scratchpad>/guard-open && rm -rf "$S" && mkdir -p "$S/old"
git archive main | tar -x -C "$S/old" && (cd "$S/old" && go build -o "$S/old-workbench" ./cmd/workbench)
go build -o "$S/workbench-dev" ./cmd/workbench
echo '{}' | /bin/sh -c "'$S/old-workbench' guard pre || true"; echo EXIT:$?
echo 'not json' | /bin/sh -c "'$S/workbench-dev' guard pre || true"; echo EXIT:$?
printf '%s' '{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_use_id":"toolu_x","tool_input":{"command":"true"}}' |
  WORKBENCH_STATE_DIR=relative/path /bin/sh -c "'$S/workbench-dev' guard pre || true"; echo EXIT:$?
rm -rf "$S"
```

Expected: `EXIT:0` three times, and nothing on stdout:
- **First line:** the release without `guard` prints `Error: Invalid
  command, flags or arguments: unknown command "guard" for "workbench"; run
  workbench --help` on stderr and exits 2, and `|| true` turns that into 0.
  The agents show a hook's stderr only in their verbose or transcript views.
- **Second line:** unreadable input is ignored.
- **Third line:** the relative `WORKBENCH_STATE_DIR` makes `Resolve` fail,
  which must not block.

- [ ] **Step 7: Commit**

```bash
git add home/.chezmoidata/claude.json home/private_dot_claude/modify_private_settings.json home/.chezmoitemplates/codex-hooks.json.tmpl
git commit -F - <<'EOF'
feat(guard): run the guard hooks in Claude Code and Codex

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
EOF
echo EXIT:$?
```

---

### Task 8: Check under real memory pressure and settle the thresholds

This task needs the owner's go-ahead for the environment. Checked on this
Mac: Docker Desktop is installed (`docker info`: a 12.5 GB linuxkit 7.0.14 VM,
no containers running), and none of `podman orb limactl colima tart` is
installed.
- **Not valid: a container's `--memory` cap.** It doesn't change
  `/proc/meminfo` or `/proc/pressure/memory`, which the guard reads, so the
  cgroup's own OOM killer acts first.
- **Linux, choose one:**
  - (a) Install Lima and start a 4 GB VM with 1 GB swap.
  - (b) Use Docker Desktop's VM, in an uncapped container, which fills that
    VM. It's the owner's app and may restart.
  - (c) Use a cloud VM.
- **macOS:** a macOS VM with 4–8 GB, e.g. Tart (`brew install
  cirruslabs/cli/tart`, roughly a 25 GB image). Never create pressure on the
  owner's own Mac.
- **WSL2:** the owner's WSL machine, with `.wslconfig` `memory=4GB`.

**Files:**
- Modify: `internal/guard/memory.go` (threshold constants, only if the runs
  show a change is needed)
- Modify: `docs/superpowers/specs/2026-10-14-agent-command-guard-design.md`
  section 6.2 (the settled values and the runs behind them)

- [ ] **Step 1: The synthetic agent under pressure (no credentials)**

In the VM, use Task 6's `agent.py` without `WORKBENCH_GUARD`. Start two
commands concurrently, each from its own `agent.py` with distinct
`tool_use_id`s (`exec-big`, `exec-small`):
- **The big one** grows without bound in non-compressible memory:
  `python3 -c 'import os,time
a=[]
while True: a.append(os.urandom(64<<20)); time.sleep(0.05)'`.
- **The small one** holds 200 MB: `python3 -c 'import os,time; a=os.urandom(200<<20); time.sleep(120)'`.

Expected:
- The big one's `agent.py` prints `exec-big command: returncode -15`
  (SIGTERM) or `-9` (SIGKILL), and its `post` line prints
  `additionalContext` beginning `workbench guard: stopped`.
- The small one, `agent.py` and every other process survive.
- `watch.log` shows `stopping exec-big` before the record was read.
- The agent sees the reason, which covers Review Focus 1.

Then repeat with a big command whose child ignores SIGTERM, compound so the
shell stays the group leader:

`python3 -c 'import os,signal,time
signal.signal(signal.SIGTERM, signal.SIG_IGN)
a=[]
while True: a.append(os.urandom(64<<20)); time.sleep(0.05)'; echo after`

Expected:
- The shell dies of SIGTERM (`returncode -15`).
- The Python child, now under PID 1, dies of SIGKILL about five seconds
  after the stop, or sooner if memory reaches the forced-stop line.
- `pgrep -f os.urandom` finds nothing afterwards.
- The log has no `gave up` line.
- The next stop is not held back.

- [ ] **Step 2: Real agents (owner-run, signed in)**

With the hooks installed by `workbench apply` in the VM:
- Have Claude Code run the big command through `Bash`, alongside a small
  command and while an MCP server is configured.
- Repeat with Codex.

Expected:
- Only the big command stops.
- The agent, its MCP server and the small command survive.
- The agent's reply quotes the reason.
- With `WORKBENCH_GUARD=dry-run` in the agent's environment, nothing stops and
  the log says `dry run, would stop`.
- Repeat Task 1's denied-call case and confirm the reason never reaches
  another call (Review Focus 4).

- [ ] **Step 3: Settle the thresholds**

From the `watch.log` timings and the machine's behaviour:
- Did the polite stop come early enough to avoid the kernel's own OOM killer
  or a frozen machine?
- Did it come late enough not to stop commands on normal load?

Change the constants only if a run shows they are wrong, and record the runs
in spec section 6.2.

- [ ] **Step 4: Run the gates and commit**

```bash
golangci-lint fmt && golangci-lint config verify && golangci-lint run ./... && go build ./... && go test ./...; echo EXIT:$?
git add internal/guard/memory.go docs/superpowers/specs/2026-10-14-agent-command-guard-design.md
git commit -F - <<'EOF'
docs(guard): settle the stop thresholds from pressure runs

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
EOF
echo EXIT:$?
```

---

### Task 9: Docs

**Files:**
- Modify: `docs/usage.md`
- Modify: `docs/acceptance.md`, under `### Costs`, a new `### Agent command
  guard` section after it
- Modify: `docs/superpowers/specs/workbench-design.md`, the package table
  (after the `internal/costs/` row)
- Modify: `docs/superpowers/specs/workbench-contracts.md`, a row after the
  `workbench costs` row
- Modify: `docs/chezmoi-local-overrides.md`, the enforced hook events
  sentence
- Modify: `AGENTS.md`, the ownership table

- [ ] **Step 1: `docs/usage.md`**

After the paragraph that ends with
`[subscriptions](superpowers/specs/2026-10-01-codex-costs-design.md).`, add:

```markdown
Claude Code and Codex shell commands are guarded against running the machine
out of memory. A `Bash` hook notes each command and starts one watcher per
user; when memory is nearly gone (macOS memory pressure warning, or on Linux
and WSL2 less than about a tenth of memory and swap left), the watcher stops
the largest command an agent started, with everything that command started,
and the agent is told why in the tool result. It never stops the agent, its
MCP servers, hooks or anything else, and never changes how a command runs.
`WORKBENCH_GUARD=off` in the agent's environment turns it off;
`WORKBENCH_GUARD=dry-run` only logs what it would stop. Its notes and log are
in Workbench's state folder under `guard/`. See the
[guard design](superpowers/specs/2026-10-14-agent-command-guard-design.md).
```

If the migrations plan already rewrote the costs paragraph, place this right
after the costs paragraph wherever it now ends.

- [ ] **Step 2: `docs/acceptance.md`**

Add `### Agent command guard` after the `### Costs` section, listing only
what Tasks 6 and 8 observed, with agent versions and platforms, in the
section's existing style: what ran, what was seen. A platform without an
observed run is not claimed.

- [ ] **Step 3: Design, contracts and local overrides**

- **`workbench-design.md` package table:** after the `internal/costs/` row,
  add
  `| \`internal/guard/\` | Agent command memory guard: Bash hooks that note each agent shell command, one watcher per user that stops the largest one near out-of-memory and tells the agent why; see the [guard design](2026-10-14-agent-command-guard-design.md). |`
- **`workbench-contracts.md`:** after the `workbench costs` row, add
  `| \`workbench guard\` (\`internal/guard/\`) and the \`Bash\` hooks in \`.chezmoidata/claude.json\` and \`.chezmoitemplates/codex-hooks.json.tmpl\` | All / all | None beyond Workbench | Hooks; F. The enforced \`PreToolUse\`, \`PostToolUse\` (and Claude Code \`PostToolUseFailure\`) hooks run \`~/.local/bin/workbench guard pre\|post \|\| true\`, write only \`<state>/guard/\`, and start one detached watcher per user that signals only agent command trees, never the agent, the watcher or other programs; the hooks always exit 0 |`
- **`chezmoi-local-overrides.md`:** change "the hook lists of the enforced
  events (`SessionStart`, `SessionEnd`)" to "the hook lists of the enforced
  events (`SessionStart`, `SessionEnd`, `PreToolUse`, `PostToolUse`,
  `PostToolUseFailure`)". Change "A hook group that runs `workbench costs`"
  to "A hook group that runs `workbench costs` or `workbench guard`".

- [ ] **Step 4: `AGENTS.md` ownership row**

Directly after the row starting
`| AI-tool cost ledger, ingest, rates and reports |`, add:

```markdown
| Agent command memory guard | `internal/guard/` (hooks in `home/.chezmoidata/claude.json` and `home/.chezmoitemplates/codex-hooks.json.tmpl`) |
```

If the migrations plan already added its `Migrations of Workbench's own
files` row after the same anchor, put this row directly after the costs row
anyway; the order between the two new rows doesn't matter.

- [ ] **Step 5: Run the gates and commit**

```bash
golangci-lint fmt && golangci-lint config verify && golangci-lint run ./... && go build ./... && go test ./...; echo EXIT:$?
git add docs/usage.md docs/acceptance.md docs/superpowers/specs/workbench-design.md docs/superpowers/specs/workbench-contracts.md docs/chezmoi-local-overrides.md AGENTS.md
git commit -F - <<'EOF'
docs(guard): document the agent command memory guard

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
EOF
echo EXIT:$?
```
