# Guard Detached Work Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** The guard shows, measures and can stop work that detached from an agent's command, shows each agent and its helpers, and says where the memory in use is.

**Architecture:** A detached unit is tracked as a `command` whose `Detached` field is set, so the watcher's measuring, stopping, manual stops and view reuse what commands already have. One pure function, `unitRoot`, decides whether a child of the init process is a unit; it reads the kernel only through a `facts` value, so the one permitted test drives it with made-up processes. Agent, helper and unmatched rows are computed each tick from the snapshot and are never stop candidates.

**Tech Stack:** Go 1.26.8, one module, `golang.org/x/sys/unix`, the libSystem trampolines in `internal/platform/libc_darwin.go`, Cobra, Bubble Tea and lipgloss for the view.

**Spec:** [docs/superpowers/specs/2026-11-13-guard-detached-work-design.md](../specs/2026-11-13-guard-detached-work-design.md). Section numbers below ("spec 5.2") point there.

## Global Constraints

- Gates after every code task, all passing: `golangci-lint fmt`, `golangci-lint config verify`, `golangci-lint run ./...`, `go build ./...`, `go test ./...`, and `GOOS=linux go build ./...`. The pins are `go.mod` and `.golangci-lint-version` (v2.14.0).
- Tests stay near zero (`AGENTS.md`). This plan adds exactly one test, in Task 5, for unauthorized execution, with the risk explained beside it. No task adds another; behavior is proved by the gates and by the checks in Tasks 1 and 11.
- Only `internal/platform/` may test the operating system. Other packages read its facts; a `forbidigo` rule enforces it.
- Greenfield: no compatibility readers, flags or dual paths. Fields added to JSON the watcher passes or serves are plain additions.
- The guard reads only these environment names from another process and keeps or logs nothing else of an environment: `CLAUDE_PID`, `WORKBENCH_GUARD_CALL`, `CODEX_TOOL_CALL_ID`, `WORKBENCH_GUARD`.
- A hook prints nothing on stdout unless it tells the agent a stop's reason, and never fails.
- Tracked docs describe the product impersonally: no machine, session, project or path of a developer.
- Conventional commit subjects; never edit `CHANGELOG.md`; run `scripts/render-check.sh ROLE MODE [wsl]` for each affected role and mode before committing a `home/` change.
- Smoke checks use a disposable state folder (`WORKBENCH_STATE_DIR`) and throwaway sessions, never the live guard state.
- Every implementer and reviewer subagent runs on Opus.

## Review Focus

No task but Task 5 may add a test, so each line names the check that pins it.

1. **A reused process ID.** A stamp or record naming a process ID that now belongs to another process must make no unit and stop nothing. Pinned by Task 5's test cases "younger" and "dead".
2. **The kernel stops answering the lineage call.** No unit is made and the guard logs it once; commands are tracked as today. Pinned by Task 5's test case "no lineage" and Task 6 step 6.
3. **A handover while a unit is tracked.** The next watcher keeps the unit, its sizes and its call. Pinned by Task 11 check 7.
4. **A unit's root exits while its members live.** The row stays one row. Pinned by Task 11 check 3.
5. **A large tree.** A build with thousands of processes must not push the watcher past its 32 MiB limit: the version map and the call records are bounded (Tasks 4 and 6). Pinned by review of those bounds and Task 11 check 8.

---

## File structure

| File | Responsibility |
| --- | --- |
| `internal/platform/process.go`, `process_darwin.go`, `process_linux.go` | `Process.UID`; `Environment` |
| `internal/platform/lineage_darwin.go`, `lineage_linux.go` (new) | `Lineage`, `ProcessLineage` |
| `internal/platform/facts_darwin.go`, `facts_linux.go` | `KnowsOriginalParent` |
| `internal/guard/stamp.go` (new) | the stamp names, `stamp`, `readStamp` |
| `internal/guard/records.go` (new) | `callRecord`, `records`, the agents the guard knows |
| `internal/guard/units.go` (new), `units_test.go` (new) | `facts`, `tie`, `unitRoot`; the one test |
| `internal/guard/detached.go` (new) | the watcher's finding, version recording and lifetime of units |
| `internal/guard/rows.go` (new) | agent, helper and unmatched rows; the memory sums |
| `internal/guard/hooks.go` | `Session`; `Post` marks a note ended; Monitor |
| `internal/guard/guard.go`, `watch.go`, `match.go`, `manual.go`, `socket.go`, `handover.go`, `view.go`, `protocol.go`, `logs.go`, `memory.go` | the changes each task names |
| `internal/cli/guard.go`, `guardview.go` | the hidden `session` command; rows, statuses, the memory line, log fields |
| `home/.chezmoidata/claude.json` | the SessionStart hook; `Bash|Monitor` on the post hooks |
| `docs/` | Task 10 |

---

### Task 1: Prove the open facts on macOS

No product code. Each check runs in a throwaway Claude Code or Codex session started from a scratch folder with `--settings` naming a scratch hook script, as the spec's measured facts were. Record each result in the spec's section 2: move a proved item from 2.3 to 2.1 with the versions it was measured on. A result that contradicts the spec stops the plan at that part and goes to the owner (spec 2.3).

**Files:**
- Modify: `docs/superpowers/specs/2026-11-13-guard-detached-work-design.md` (sections 2.1 and 2.3)

- [ ] **Step 1: Monitor's post hook (spec 2.3 item 3).** Hooks on PreToolUse, PostToolUse and PostToolUseFailure matched on `Bash|Monitor`, each appending its stdin and the time to a scratch file. Have the session start a Monitor command that prints, waits 5 seconds and prints again. Record: each hook's `tool_name`, whether pre and post carry the same `tool_use_id`, whether `tool_input.command` is present, and whether the post hook fired when the monitor started or when it ended. If print mode has no Monitor tool, drive an interactive session through a pseudo-terminal.
- [ ] **Step 2: bash as the agent's shell (item 4).** Start a session with `SHELL=/bin/bash` and a SessionStart hook appending `export WORKBENCH_GUARD_CALL="$CLAUDE_PID:$$"`. Run `echo "call=$WORKBENCH_GUARD_CALL agent=$CLAUDE_PID shell=$$ bash=$BASH_VERSION"` twice. Expected: `bash=` non-empty and `call` equal to `agent:shell` both times.
- [ ] **Step 3: an MCP server's environment (item 5).** Start a session with `--mcp-config` naming a stdio server that is a script writing whether `CLAUDE_PID`, `CLAUDECODE` and `WORKBENCH_GUARD_CALL` are set, and its parent's process ID, to a scratch file. Expected: `CLAUDE_PID` unset, the parent is the Claude Code process.
- [ ] **Step 4: a command typed with `!` (item 6).** Drive an interactive session through a pseudo-terminal with the same SessionStart hook; type `! echo "call=$WORKBENCH_GUARD_CALL" > SCRATCH/bang.txt`. Prove the keys were typed by the file existing. Record whether `call=` is empty. If it is not empty, stop and bring it to the owner before Task 6.
- [ ] **Step 5: Codex's call ID on detached work (item 2).** With `codex exec`, run a command that double-forks a Python sleeper into its own session. Read `CODEX_TOOL_CALL_ID` from the sleeper and compare it with the `tool_use_id` the Codex pre hook received. Expected: equal.
- [ ] **Step 6: Record and commit.**

```bash
git add docs/superpowers/specs/2026-11-13-guard-detached-work-design.md
git commit -m "docs(guard): record the facts measured for detached work"
```

---

### Task 2: What the platform reports of a process

**Files:**
- Modify: `internal/platform/process.go`, `internal/platform/process_darwin.go`, `internal/platform/process_linux.go`, `internal/platform/facts_darwin.go`, `internal/platform/facts_linux.go`
- Create: `internal/platform/lineage_darwin.go`, `internal/platform/lineage_linux.go`

**Interfaces:**
- Produces:
  - `Process.UID int`: the process's effective user. Darwin: `kinfo_proc`'s `Eproc.Ucred.Uid`. Linux: the owner of `/proc/<pid>`.
  - `func Environment(pid int, names ...string) (values map[string]string, readable bool)`: the named variables of the environment the process started with. `readable` is false when the kernel gives no environment at all (a restricted program on macOS, an unreadable `/proc/<pid>/environ`). `Environ` stays and is written through it.
  - `type Lineage struct{ Version, ParentVersion uint32 }` and `func ProcessLineage(pid int) (Lineage, bool)`. Darwin: `proc_pidinfo` flavor 17 (`PROC_PIDUNIQIDENTIFIERINFO`) into a 64-byte buffer; `p_idversion` is the int32 at byte 32 and `p_orig_ppidversion` the int32 at byte 36; false when fewer than 40 bytes come back. Linux: always false.
  - `const KnowsOriginalParent`: true on Darwin, false on Linux.

- [ ] **Step 1:** Add `UID` to `Process` and fill it in `fromKinfo` and in Linux's `ProcessInfo`.
- [ ] **Step 2:** Add `Environment` on both systems and write `Environ` through it. On Darwin it reads `kern.procargs2` once per call.
- [ ] **Step 3:** Add `Lineage`, `ProcessLineage` and `KnowsOriginalParent`, each with a comment saying what the kernel reports and, for Darwin, that the flavor is absent from the public SDK headers and what a false answer means to callers.
- [ ] **Step 4:** Prove it by hand on macOS with a throwaway `go run` program under the scratch folder that double-forks `/bin/sleep`, then prints the sleeper's `ProcessLineage`, the init process's, and `Environment(pid, "PATH")` for the sleeper and for itself. Expected: the sleeper's `ParentVersion` differs from init's `Version`; the sleeper is not readable; the program itself is.
- [ ] **Step 5:** Run the gates.
- [ ] **Step 6:** Commit: `feat(platform): report a process's user, original parent and environment`

---

### Task 3: The call stamp

**Files:**
- Create: `internal/guard/stamp.go`
- Modify: `internal/guard/hooks.go`, `internal/cli/guard.go`, `home/.chezmoidata/claude.json`

**Interfaces:**
- Consumes: `platform.Environment` (Task 2).
- Produces:
  - `const agentStamp = "CLAUDE_PID"`, `callStamp = "WORKBENCH_GUARD_CALL"`, `codexStamp = "CODEX_TOOL_CALL_ID"` (the existing `callIDVariable` is renamed to it), `modeVariable = "WORKBENCH_GUARD"`.
  - `const stampLine = "export WORKBENCH_GUARD_CALL=\"$CLAUDE_PID:$$\""`.
  - `type stamp struct { Readable bool; Agent, CallAgent, CallShell int; Codex, Mode string }`. `Agent` is `CLAUDE_PID`; `CallAgent` and `CallShell` are the two numbers of the call stamp, both 0 when it is absent or not `digits:digits`.
  - `func readStamp(pid int) stamp`.
  - `func (st stamp) call() bool`: the call stamp is present and `CallAgent == Agent`.
  - `func Session()`: the SessionStart hook.

- [ ] **Step 1:** Write `stamp.go` with the constants, `stamp`, `readStamp` and `call`.
- [ ] **Step 2:** Write `Session` in `hooks.go`: with `WORKBENCH_GUARD=off` or an empty `CLAUDE_ENV_FILE` it does nothing; otherwise it appends `stampLine` and a newline to that file (created 0600 when missing) unless a line of the file already equals `stampLine`. It prints nothing and recovers from a panic like `Pre`.
- [ ] **Step 3:** Add the hidden `workbench guard session` command beside `pre` and `post` in `internal/cli/guard.go`.
- [ ] **Step 4:** In `home/.chezmoidata/claude.json` add `{ "type": "command", "command": "workbench guard session || true", "timeout": 5 }` to the SessionStart hooks, after the costs hook.
- [ ] **Step 5:** Run `scripts/render-check.sh` for each role and mode, then the gates.
- [ ] **Step 6:** Prove it: a throwaway session whose settings run the built binary's `guard session`, then `echo "$WORKBENCH_GUARD_CALL $CLAUDE_PID $$"` twice. Expected: `agent:shell` each time, the shell differing; the env file holds the line once after a second SessionStart.
- [ ] **Step 7:** Commit: `feat(guard): stamp each command with its agent and shell`

---

### Task 4: Every call leaves a record

**Files:**
- Create: `internal/guard/records.go`
- Modify: `internal/guard/guard.go` (`note`), `internal/guard/hooks.go` (`Post`), `internal/guard/watch.go` (`notes`, the tracking loop in `tick`, `watcher`), `internal/guard/handover.go` (`carried`, `adopt`)

**Interfaces:**
- Produces:
  - `note.Ended bool` (`json:"ended,omitempty"`): set by the post hook of a call that is not a background call.
  - `type callRecord struct { ID string; Agent procKey; Kind kind; Subagent, Project, Cwd string; DryRun bool; Shell procRef; NotedAt time.Time }`, with JSON tags; `Shell` is zero until the call is tracked.
  - `type records struct`, with `add(n note)`, `tracked(id string, root proc)`, `byID(id string) (callRecord, bool)`, `byShell(agent procKey, shell int, before uint64) (callRecord, bool)` (false when no record or more than one of that agent has that shell ID with a start before `before`), `prune(alive func(procKey) bool)` and `all() []callRecord`.
  - `const recordsKept = 4096`: records kept per agent, the oldest dropped first.
  - `watcher.records *records` and `watcher.known map[procKey]kind`: the agents the guard has read a note from, dropped when the agent is gone.
  - `carried.Records []callRecord`, `carried.Known`.

- [ ] **Step 1:** Add `note.Ended`. In `Post`, a call that is not a background call marks its note ended when a watcher holds the watch lock (`operation.TryLock` reports it held) and deletes the note, as today, when none does.
- [ ] **Step 2:** Write `records.go`.
- [ ] **Step 3:** In `watcher.notes`, every note read adds its agent to `known` and its record to `records`. An ended note is then deleted and treated exactly as a note that went away is treated today: it is not returned, so it is never matched, and the code after the loop logs `unmatched` under the same condition as now. It is never abandoned.
- [ ] **Step 4:** Where `tick` starts tracking a command, call `records.tracked`. Call `records.prune` and prune `known` where `setsCallIDs` is pruned.
- [ ] **Step 5:** Carry `Records` and `Known` in `carried` and take them in `adopt`.
- [ ] **Step 6:** Run the gates.
- [ ] **Step 7:** Prove it with a fake agent in a disposable state folder: a 3-second command and a command that ends at once. Expected: after each post hook the note is gone within two seconds, the log has `tracking` and `ended` for the first and nothing for the second, and no `unmatched` or `abandoned` line; with the watcher stopped, a post hook removes its note itself.
- [ ] **Step 8:** Commit: `feat(guard): keep a record of every call after its note`

---

### Task 5: The rule for a detached unit, and its test

**Files:**
- Create: `internal/guard/units.go`, `internal/guard/units_test.go`

**Interfaces:**
- Consumes: `stamp` (Task 3), `platform.Lineage` (Task 2), `records` (Task 4).
- Produces:

```go
// facts is what the unit rule reads beyond the snapshot. The watcher fills it
// from the kernel; the test fills it by hand.
type facts struct {
	self           int                                    // the watcher's user
	needsLineage   bool                                   // platform.KnowsOriginalParent
	lineage        func(pid int) (platform.Lineage, bool)
	stamp          func(pid int) stamp
	args           func(pid int) []string
	known          map[procKey]kind                       // agents the guard knows
	protected      map[int]uint64
	versions       map[uint32]string                      // a recorded version's owner: a call or unit ID
	seenUnder      map[procKey]string                     // a process seen in a tracked tree, and its owner
	records        *records
}

// tie is how a unit's root is tied to an agent.
type tie struct {
	Agent procKey
	Kind  kind
	Owner string // the call or unit whose tree forked it, "" for a tie by stamp
	Shell int    // the call stamp's shell, 0 without one
	Codex string // Codex's call ID, "" without one
	Mode  string // WORKBENCH_GUARD as read, "" when unread
}

func unitRoot(s snapshot, r proc, f facts) (tie, bool)
```

`unitRoot` is spec 5.2 and 5.3, in this order, each failure returning false:

1. `r.UID == f.self`; `r`'s parent is PID 1 or a process whose name is in `initNames`; `r` is not protected and not under a protected process; `r` is not a zombie.
2. `filepath.Base` of `r`'s first argument is not `workbench`.
3. With `needsLineage`: `r`'s lineage and PID 1's are both known and `r`'s `ParentVersion` is not PID 1's `Version`.
4. Tie by parent: `f.seenUnder` has `r`, or `f.versions` has `r`'s `ParentVersion`. The owner's record gives the agent.
5. Otherwise tie by stamp. The stamps are `r`'s own when readable, else those of the first readable processes below `r` reached through unreadable ones only. Claude Code: a stamp with `call()` true whose `Agent` is in `f.known` under that process ID; at least one, and no readable one naming another agent. Codex: a `Codex` ID that `f.records.byID` resolves.
6. By either tie, no stamp read for `r` names an agent other than the tie's.
7. The agent is alive in `s` with the start time `f.known` holds, and started before `r`. `Mode` is not `off`.

- [ ] **Step 1: Write the test.** `TestUnitRootOnlyForWhatAnAgentCommandForked`, table-driven over made-up snapshots and facts, with a comment above it naming the risk: the guard stopping a program no agent command forked. One row that is a unit by stamp and one by parent, then one row each that must give no unit:
  - no stamp and no recorded parent;
  - a stamp naming a dead process;
  - a stamp naming a process the guard does not know;
  - a stamp naming a process younger than `r`;
  - an init-started process carrying a stamp (its `ParentVersion` is PID 1's `Version`);
  - the agent stamp without the call stamp;
  - a call stamp whose agent part differs from `CLAUDE_PID`;
  - a Workbench process;
  - a protected process, and a process under one;
  - `needsLineage` with `lineage` answering false;
  - another user's process;
  - `WORKBENCH_GUARD=off`;
  - a tie by parent whose own stamp names another agent.

  A second test function in the same file, `TestGuardNeverChoosesAnAgentOrHelper`, is written in Task 7, which owns that code; it is part of the same one risk.
- [ ] **Step 2:** Run `go test ./internal/guard/ -run TestUnitRoot -v`. Expected: it does not compile, `unitRoot` undefined.
- [ ] **Step 3:** Write `units.go`.
- [ ] **Step 4:** Run the test. Expected: PASS. Run the gates.
- [ ] **Step 5:** Commit: `feat(guard): decide which orphaned processes an agent command forked`

---

### Task 6: The watcher tracks detached units

**Files:**
- Create: `internal/guard/detached.go`
- Modify: `internal/guard/guard.go` (`command`), `internal/guard/watch.go`, `internal/guard/match.go` (`targets`), `internal/guard/manual.go`, `internal/guard/memory.go` (`reason`), `internal/guard/handover.go`, `internal/guard/logs.go`

**Interfaces:**
- Consumes: Tasks 2 to 5.
- Produces:
  - `command.Detached *detached`, nil for a noted command. `type detached struct { Roots []procRef; Call string }`: `Call` is the launching call's ID, "" when it did not resolve.
  - A unit's ID is `detached-<pid>-<start>` of its first root. Its `Note` is made up, never written to disk: `ToolUseID` the unit's ID, `AgentPID`, `AgentStart` and `Kind` from the tie, `Command` the root's command line joined by spaces, `Cwd` the root's working folder, `Project` `projectName("", Cwd)`, `Subagent` and `DryRun` from the call's record when it resolved, `NotedAt` the root's start as a time, `DryRun` also true when the tie's `Mode` is `dry-run`.
  - `func (s snapshot) commandTree(c command, skip func(proc) bool) []proc`: the tree of `c.Root`, or for a unit the trees of its live roots, each process once.
  - `func targets(s snapshot, c command, protected map[int]uint64) []target` now takes the command.
  - `const versionsKept = time.Minute`: how long a process's version is kept after it is last seen. `const versionsLimit = 1 << 16`: the most versions kept, the oldest dropped first.
  - Log attributes `kind` (`detached`) and `launched_by` on a unit's `tracking`, `ended` and `stopping` records; `Event.Kind`, `Event.LaunchedBy`.

- [ ] **Step 1:** Add `Detached` to `command` and `commandTree`; use `commandTree` wherever `tick`, `measure`, `stopLargest`, `stop`, `stopFor` and `targets` take a command's tree, and pass `targets` the command.
- [ ] **Step 2:** In `detached.go`, record lineage each tick: for every process of every tracked command's and unit's tree, `seenUnder[procKey]` and `versions[Version]` name the owner, within the two constants above.
- [ ] **Step 3:** In `detached.go`, find units each tick after matching: every child of init of the watcher's user that is not classified, by process ID and start time, goes through `unitRoot`. A root that is a unit by parent of an existing unit joins it (`Roots` grows). Any other becomes a tracked command with `Detached` set and its `usage`, and is logged `tracking`. A root that failed only for want of a readable stamp is kept and tried again each tick while it lives, reading each process's stamp once. Resolve `Call` as spec 5.5: the owner of a tie by parent, else `records.byID(tie.Codex)`, else `records.byShell(tie.Agent, tie.Shell, r.Start)`.
- [ ] **Step 4:** Protect every live process a stamp named as an agent that started before the stamped process (spec 5.4): `agents` adds them to `protected`.
- [ ] **Step 5:** Lifetime (spec 5.6) in `forget`: a unit ends when none of its roots' trees has a live process for two rounds, or its agent is gone. When a unit's root exits, the members init adopts are found by Step 3 as ties by parent of that unit and join it. A unit is never put in `w.ended`.
- [ ] **Step 6:** When `platform.KnowsOriginalParent` is true and `ProcessLineage(1)` answers false, log once per watcher, as an error event: `cannot read where processes come from; detached work is not tracked`.
- [ ] **Step 7:** Stops. A unit's kill record is written for `Detached.Call` only when that call's note is on disk and not ended; otherwise none. `reason` gains the unit's wording: `workbench guard: stopped <command>, a program this call started that had detached, and its N child processes. …` with the same closing sentences as today.
- [ ] **Step 8:** Carry units at a handover: `carried` gains `Detached map[string]detached`, and `adopt` takes a carried command with `Detached` without requiring its root to be its agent's child.
- [ ] **Step 9:** Run the gates.
- [ ] **Step 10:** Prove it with a fake agent in a disposable state folder: a command that double-forks a Python sleeper and waits. Expected in `guard logs --json`: `tracking` with `kind` `detached` and `launched_by` the call. Then end the fake agent's command and stop the sleeper: `ended` for the unit.
- [ ] **Step 11:** Commit: `feat(guard): track work that detached from an agent's command`

---

### Task 7: Agent, helper and unmatched rows, and the sums

**Files:**
- Create: `internal/guard/rows.go`
- Modify: `internal/guard/protocol.go`, `internal/guard/view.go`, `internal/guard/manual.go`, `internal/guard/socket.go`, `internal/guard/units_test.go`

**Interfaces:**
- Consumes: `watcher.known`, `watcher.agentChildren`, `readStamp`, `commandTree`.
- Produces:
  - `CommandView.Kind string` (`json:"kind"`): `""` for a command, `detached`, `agent`, `helper`. `CommandView.PID int` (`json:"pid"`): the row's root. `CommandView.LaunchedBy string` (`json:"launched_by"`).
  - Row IDs `agent-<pid>-<start>`, `helper-<pid>-<start>`, `unmatched-<pid>-<start>`.
  - `type MemorySums struct { Commands, Detached, Agents, Helpers, Other uint64 }` with JSON tags, and `Snapshot.Sums MemorySums` (`json:"sums"`).
  - `const helperAfter = 5 * time.Second`: how long a direct child must have been seen before it is a row.
  - `func (w *watcher) rows(s snapshot, notes []note, taken map[int]bool) []command`: this tick's agent, helper and unmatched rows as made-up commands with `Row` set. `command.Row string` is `agent`, `helper` or `unmatched`, and `""` for a command or unit.
  - `func stoppable(c command) (auto, byHand bool)`: a command or unit: true, true. A helper or unmatched row: false, true. An agent row: false, false.

- [ ] **Step 1:** `rows.go`. For each agent in `known` that is alive: an agent row sized by the agent process alone. For each direct child of it seen for `helperAfter` (from `agentChildren`), not in `taken`, and that no waiting note claims (`claims`): a helper when it is not a `shellScript` and no readable process of its tree carries `CLAUDE_PID`; an unmatched row when a readable process of its tree carries a call stamp whose `CallShell` is that child; otherwise no row. Rows keep `usage` like commands, dropped when the row is gone, and are not logged.
- [ ] **Step 2:** `buildView` appends the rows after the tracked commands, an unmatched row with the state `unmatched` and no end time, and fills `Kind`, `PID`, `LaunchedBy` and `Sums`. An unmatched row counts in `Sums.Commands`. `Other` is the memory in use less the four sums, never below zero.
- [ ] **Step 3:** `stopLargest` skips any command for which `stoppable` gives `auto` false. `chooseStops` looks an ID up among the rows too: an agent row answers `not allowed`. `stopFor` writes no kill record for a row and logs nothing for it.
- [ ] **Step 4:** Add `TestGuardNeverChoosesAnAgentOrHelper` to `units_test.go`: `stoppable` gives `auto` false for each of the three rows and `byHand` false for an agent row.
- [ ] **Step 5:** Run the gates.
- [ ] **Step 6:** Commit: `feat(guard): show each agent, its helpers and where memory is`

---

### Task 8: The view and the log viewer

**Files:**
- Modify: `internal/cli/guardview.go`, `internal/cli/guard.go`

**Interfaces:**
- Consumes: `CommandView.Kind`, `PID`, `LaunchedBy`, `Snapshot.Sums` (Task 7); `Event.Kind`, `Event.LaunchedBy` (Task 6).

- [ ] **Step 1:** `shortCallID` gives `p<pid>` for an ID starting `detached-`, `agent-`, `helper-` or `unmatched-`.
- [ ] **Step 2:** `statusText` takes the kind: a running `detached`, `helper` and `agent` row reads `● detached`, `● helper` and `● agent`; a running unmatched row reads `? unmatched` and shows its memory now. Stopping, stopped and done read as today.
- [ ] **Step 3:** Under the count line, one line from `Snapshot.Sums`, faint, dropped when a snapshot has none and clipped to the window:
  `commands 40 MB · detached 4.20 GB · agents 1.36 GB · helpers 1.39 GB · other 5.06 GB`
- [ ] **Step 4:** The details screen adds `Started by` with the launching call's ID for a unit whose call resolved. A stop of an agent row says `agents are never stopped` on the bottom line and sends nothing.
- [ ] **Step 5:** The log viewer shows `kind` and `launched_by` (the call's short ID) after `project`. Update the field order in `guard logs --help` and the row kinds in `guard memory --help`.
- [ ] **Step 6:** Run the gates.
- [ ] **Step 7:** Prove it against the fake agent of Task 6 with a helper child added: the view shows the four kinds of row and the sums line; `k` on the helper stops it; `k` on the agent is refused.
- [ ] **Step 8:** Commit: `feat(guard): show detached, agent and helper rows in the memory view`

---

### Task 9: The Monitor post hook

Built only as Task 1 step 1 found it. If the post hook fires when the monitor ends, or carries no `tool_use_id` equal to the pre hook's, stop and bring it to the owner.

**Files:**
- Modify: `home/.chezmoidata/claude.json`, `internal/guard/hooks.go`

- [ ] **Step 1:** Match Claude Code's PostToolUse and PostToolUseFailure hooks on `Bash|Monitor`. The pre hook and the Codex hooks do not change.
- [ ] **Step 2:** In `Post`, a call whose tool is `Monitor` is a background call: its note is marked posted.
- [ ] **Step 3:** Run `scripts/render-check.sh` for each role and mode, then the gates.
- [ ] **Step 4:** Prove it in a throwaway session with a disposable state folder: after a Monitor command ends, its note is gone and the log has no `abandoned` line for it.
- [ ] **Step 5:** Commit: `fix(guard): clear a Monitor command's note when it ends`

---

### Task 10: Docs

**Files:**
- Create: `docs/adr/0016-guard-detached-work.md`
- Modify: `docs/usage.md`, `docs/superpowers/specs/2026-10-14-agent-command-guard-design.md`, `docs/superpowers/specs/2026-10-17-platform-and-guard-view-design.md`, `AGENTS.md`, `internal/guard/guard.go` (package comment), `README.md` if it states what the guard tracks

- [ ] **Step 1:** ADR 0016, in the form of ADR 0013: the context (work that detaches is unseen), the decision (stamps checked against the original parent; agent and helper rows the guard never stops), what was rejected and why (spec section 3), and the consequences (spec section 10).
- [ ] **Step 2:** `docs/usage.md`: replace the sentence that detached programs are not tracked; say the guard itself never stops an agent or what it starts, and that a person may stop a helper from the view; describe the row kinds and the memory line; add `kind` and `launched_by` to the log's fields.
- [ ] **Step 3:** The guard design's section 12 line and the view design's section 1 line, columns and protocol section, each replaced in place.
- [ ] **Step 4:** `AGENTS.md`: the guard row of the ownership table names the SessionStart hook. The package comment says what the guard itself never stops.
- [ ] **Step 5:** Commit: `docs(guard): describe detached work and the new rows`

---

### Task 11: Checks before release

**Files:**
- Modify: `docs/acceptance.md`, `docs/superpowers/specs/2026-11-13-guard-detached-work-design.md` (section 2)

Each check uses the built binary, a disposable state folder and throwaway sessions or fake agents. A check that fails is fixed in the task that owns the code, then run again.

- [ ] **Check 1:** A real Claude Code command that double-forks a sleeper gives a `detached` row with its agent, and its subagent when a subagent made the call. A stop from the view ends it.
- [ ] **Check 2:** A command that leaves `/bin/sleep 60 &` behind when it returns gives a `detached` row with its call.
- [ ] **Check 3:** A unit whose root exits while a child lives stays one row.
- [ ] **Check 4:** An app started with `open` from a command gives no row. A daemon a hook starts gives no row.
- [ ] **Check 5:** A sub-second Claude Code command that leaves a sleeper gives a row with a blank subagent; under Codex it gives a row with its call.
- [ ] **Check 6:** An agent with a language server shows an agent row and a helper row, and the five sums add up to the memory in use.
- [ ] **Check 7:** Installing a new local build while a unit is tracked hands over, and the next watcher shows the unit with its peak and its call.
- [ ] **Check 8:** With a build of a few thousand processes under a tracked command, the watcher's own size stays under 32 MiB.
- [ ] **Check 9:** In a Linux container with the Linux build and a fake agent: an orphan's parent is an init the guard knows, its environment is readable, a double-forked sleeper gives a `detached` row, and a stop ends it. Record what could not be run there (a real agent, its sandbox, its cgroup) as unqualified.
- [ ] **Step 10:** Record the results in `docs/acceptance.md`, impersonally, and move the proved items of the spec's 2.3 to 2.1.
- [ ] **Step 11:** Commit: `docs(acceptance): record the detached work checks`
