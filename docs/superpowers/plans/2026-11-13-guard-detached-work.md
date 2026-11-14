# Guard Detached Work Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** The guard shows, measures and can stop work that detached from an agent's command, shows each agent and its helpers, and says where the memory in use is.

**Architecture:** A detached unit is tracked as a `command` whose `Detached` field is set, so the watcher's measuring, stopping, manual stops and view reuse what commands already have. One pure function, `unitRoot`, decides whether a child of the init process is a unit; it reads the kernel only through a `facts` value, so a test drives it with made-up processes. A second pure function, `choose`, is the guard's own choice of what to stop. Agent, helper and unmatched rows are computed each tick from the snapshot, live outside the tracked commands, and are never stop candidates.

**Tech Stack:** Go 1.26.8, one module, `golang.org/x/sys/unix`, the libSystem trampolines in `internal/platform/libc_darwin.go`, Cobra, Bubble Tea and lipgloss for the view.

**Spec:** [docs/superpowers/specs/2026-11-13-guard-detached-work-design.md](../specs/2026-11-13-guard-detached-work-design.md). Section numbers below ("spec 5.2") point there.

## Global Constraints

- Gates after every code task, all passing: `golangci-lint fmt`, `golangci-lint config verify`, `golangci-lint run ./...`, `go build ./...`, `go test ./...`, and `GOOS=linux go build ./...`. The pins are `go.mod` and `.golangci-lint-version` (v2.14.0).
- Tests stay near zero (`AGENTS.md`). This plan adds tests for one risk, unauthorized execution (the guard stopping a program no agent command forked, or an agent), with the risk explained beside each: `TestUnitRootOnlyForWhatAnAgentCommandForked` (Task 5), `TestGuardChoosesOnlyWhatACallProvablyStarted` (Task 6), and rows added to the existing `TestTargetsStayInsideTheCommandTree` (Task 6) and `TestManualStopsOnlyWhatAPersonMayStop` (Task 7). No task adds another; other behavior is proved by the gates and by the checks in Task 11.
- Only `internal/platform/` may test the operating system. Other packages read its facts; a `forbidigo` rule enforces it.
- Greenfield: no compatibility readers, flags or dual paths. Fields added to JSON the watcher passes or serves are plain additions.
- The guard reads only these environment names from another process and keeps or logs nothing else of an environment: `CLAUDE_PID`, `WORKBENCH_GUARD_CALL`, `CODEX_TOOL_CALL_ID`, `WORKBENCH_GUARD`.
- A running program's arguments never reach the log, a kill record or the reason told to an agent: they may hold a token the shell expanded. Only a call's text as the agent wrote it, and a program's name, are written. The full command line of a unit or row is shown in the view only.
- A hook prints nothing on stdout unless it tells the agent a stop's reason, and never fails.
- Tracked docs describe the product impersonally: no machine, session, project or path of a developer.
- Conventional commit subjects; never edit `CHANGELOG.md`; run `scripts/render-check.sh ROLE MODE [wsl]` for each affected role and mode before committing a `home/` change.
- Every implementer and reviewer subagent runs on Opus.
- **Smoke checks never touch the live guard.** Every proof step sets `WORKBENCH_STATE_DIR` to a scratch folder outside the repository and uses a binary built into that scratch folder. A throwaway Claude Code session is started from a scratch folder with `--setting-sources project --settings SCRATCH/settings.json` (so none of the user's hooks run) and with the inherited agent variables removed: `env -u CLAUDE_PID -u CLAUDECODE -u CLAUDE_CODE_SESSION_ID -u CLAUDE_CODE_ENTRYPOINT -u CLAUDE_CODE_CHILD_SESSION -u WORKBENCH_GUARD_CALL -u CLAUDE_ENV_FILE`. Nothing is installed.
- **The fake agent.** Where a step says "fake agent", it is a Python script in the scratch folder that stands for Claude Code: it is not a shell; it pipes a `PreToolUse` input with a `toolu_` ID and its own `session_id` to `workbench guard pre` through `sh -c`; it starts the command in the shell form `match.go` knows for Claude Code (see `shellScript` and the rows of `TestMatchTracksOnlyTheAgentsCommandShell`), with `CLAUDE_PID` set to its own process ID and the stamp line of Task 3 run in that shell before the command; and it pipes the `PostToolUse` input to `workbench guard post` when the command returns. Task 4 writes it; later tasks reuse it.

## Review Focus

Each line names the check that pins it.

1. **A nested agent.** A session a command starts and leaves detached must never be stopped as detached work. Pinned by Task 5's rows "the root is a known agent", Task 6's row in `TestTargetsStayInsideTheCommandTree`, the ten-second rule in `choose`, and Task 11 check 5.
2. **A reused process ID.** A stamp or record naming a process ID that now belongs to another process must make no unit, resolve no call and stop nothing. Pinned by Task 5's rows "younger" and "dead" and Task 4's `byShell`, which matches the shell's start second.
3. **The kernel stops answering the lineage call.** No unit is made and the guard logs it once; commands are tracked as today. Pinned by Task 5's row "no lineage" and Task 6 step 7.
4. **A unit's root exits while its members live.** The row stays one row, still measured and stoppable. Pinned by `live` in Task 6 and Task 11 check 3.
5. **A large tree.** A build with thousands of processes must not push the watcher past its 32 MiB limit or slow its tick: the version map, the records and the stamp cache are bounded (Tasks 4 and 6). Pinned by review of those bounds and Task 11 check 9.

---

## File structure

| File | Responsibility |
| --- | --- |
| `internal/platform/process.go`, `process_darwin.go`, `process_linux.go` | `Process.UID`; `Environment`; `StartTime` |
| `internal/platform/lineage_darwin.go`, `lineage_linux.go` (new) | `Lineage`, `ProcessLineage` |
| `internal/platform/facts_darwin.go`, `facts_linux.go` | `KnowsOriginalParent` |
| `internal/guard/stamp.go` (new) | the stamp names, `stamp`, `readStamp` |
| `internal/guard/records.go` (new) | `callRecord`, `records`, the agents the guard knows |
| `internal/guard/units.go` (new), `units_test.go` (new) | `facts`, `tie`, `unitRoot`, `choose`; their tests |
| `internal/guard/detached.go` (new) | the watcher's finding, lineage recording and lifetime of units |
| `internal/guard/rows.go` (new) | agent, helper and unmatched rows; the memory sums |
| `internal/guard/hooks.go` | `Session`; `Post` marks a note ended; Monitor |
| `internal/guard/state.go` | the folder of agent files |
| `internal/guard/guard.go`, `watch.go`, `match.go`, `manual.go`, `socket.go`, `handover.go`, `view.go`, `protocol.go`, `logs.go`, `memory.go`, `match_test.go` | the changes each task names |
| `internal/cli/guard.go`, `guardview.go` | the hidden `session` command; rows, statuses, the memory line, log fields |
| `home/.chezmoidata/claude.json`, `home/.chezmoitemplates/codex-hooks.json.tmpl` | the SessionStart hook; `Bash|Monitor` on Claude Code's post hooks |
| `docs/` | Task 10 |

---

### Task 1: Prove the open facts on macOS (done)

No product code. The results are spec 2.1 facts 13 to 20 and 2.2 fact 14.

- [x] Monitor's post hook fires when the monitor starts, names the tool `Monitor` and carries the pre hook's `tool_use_id` (fact 15).
- [x] bash 3.2 as the agent's shell gives the same stamp (facts 13 and 18).
- [x] An MCP server never carries its own agent's `CLAUDE_PID` (fact 14).
- [x] A command typed with `!` carries an agreeing call stamp and fires no hook (fact 16). This is why a unit whose call does not resolve is never chosen (spec 5.5).
- [x] Codex's call ID reaches detached work (fact 17); its hooks do not carry it (2.2 fact 14, from source).
- [x] The stamp's third part is the shell's start second under zsh and bash 3.2 (fact 18); a status line command carries no call stamp (fact 19); a SessionStart hook is a child of its agent (fact 20).

---

### Task 2: What the platform reports of a process

**Files:**
- Modify: `internal/platform/process.go`, `internal/platform/process_darwin.go`, `internal/platform/process_linux.go`, `internal/platform/facts_darwin.go`, `internal/platform/facts_linux.go`
- Create: `internal/platform/lineage_darwin.go`, `internal/platform/lineage_linux.go`

**Interfaces:**
- Produces:
  - `Process.UID int`: the process's effective user. Darwin: `kinfo_proc`'s `Eproc.Ucred.Uid`. Linux: the owner of `/proc/<pid>`.
  - `func Environment(pid int, names ...string) (values map[string]string, readable bool)`: the named variables of the environment the process started with. `readable` is false when the kernel gives no environment at all (a restricted program on macOS, an unreadable `/proc/<pid>/environ`). `Environ` stays and is written through it.
  - `func StartTime(start uint64) time.Time`: a `Process.Start` as a wall-clock time. Darwin: microseconds since the epoch. Linux: the boot time (`btime` of `/proc/stat`, read once) plus the clock ticks.
  - `type Lineage struct{ Version, ParentVersion uint32 }` and `func ProcessLineage(pid int) (Lineage, bool)`. Darwin: `proc_pidinfo` flavor 17 (`PROC_PIDUNIQIDENTIFIERINFO`) into a 64-byte buffer; `p_idversion` is the int32 at byte 32 and `p_orig_ppidversion` the int32 at byte 36; false when fewer than 40 bytes come back (the kernel returns 56). Linux: always false.
  - `const KnowsOriginalParent`: true on Darwin, false on Linux.

- [ ] **Step 1:** Add `UID` to `Process` and fill it in `fromKinfo` and in Linux's `ProcessInfo`.
- [ ] **Step 2:** Add `Environment` on both systems and write `Environ` through it. On Darwin it reads `kern.procargs2` once per call.
- [ ] **Step 3:** Add `StartTime`, `Lineage`, `ProcessLineage` and `KnowsOriginalParent`, each with a comment saying what the kernel reports and, for Darwin's lineage, that the flavor is absent from the public SDK headers, that the version is one counter for the whole system, and what a false answer means to callers.
- [ ] **Step 4:** Prove it by hand on macOS with a throwaway `go run` program under the scratch folder that double-forks `/bin/sleep`, then prints the sleeper's `ProcessLineage`, the init process's, `Environment(pid, "PATH")` for the sleeper and for itself, and `StartTime` of its own start. Expected: the sleeper's `ParentVersion` differs from init's `Version`; the sleeper is not readable; the program itself is; its start time is within a second of now.
- [ ] **Step 5:** Run the gates.
- [ ] **Step 6:** Commit: `feat(platform): report a process's user, original parent and environment`

---

### Task 3: The call stamp and the session hook

**Files:**
- Create: `internal/guard/stamp.go`
- Modify: `internal/guard/hooks.go`, `internal/guard/state.go`, `internal/cli/guard.go`, `home/.chezmoidata/claude.json`, `home/.chezmoitemplates/codex-hooks.json.tmpl`

**Interfaces:**
- Consumes: `platform.Environment` (Task 2).
- Produces:
  - `const agentStamp = "CLAUDE_PID"`, `callStamp = "WORKBENCH_GUARD_CALL"`, `codexStamp = "CODEX_TOOL_CALL_ID"` (the existing `callIDVariable` is renamed to it), `modeVariable = "WORKBENCH_GUARD"`.
  - `const stampLine = "export WORKBENCH_GUARD_CALL=\"$CLAUDE_PID:$$:${EPOCHSECONDS:-$(date +%s)}\""`.
  - `type stamp struct { Readable bool; Agent, CallAgent, CallShell int; CallSecond int64; Codex, Mode string }`. `Agent` is `CLAUDE_PID`; `CallAgent`, `CallShell` and `CallSecond` are the three numbers of the call stamp, all 0 when it is absent or not `digits:digits:digits`.
  - `func readStamp(pid int) stamp`.
  - `func (st stamp) call() bool`: the call stamp is present and `CallAgent == Agent`.
  - `type agentFile struct { PID int; Start uint64; Kind kind; At time.Time }` with JSON tags, written as `agents/<pid>-<start>.json` in the guard's state folder; `folders.agents` and `func (f folders) agent(pid int, start uint64) string`.
  - `func Session(stdin *os.File, agent string)`: the SessionStart hook; `agent` is `claude` or `codex`.

- [ ] **Step 1:** Write `stamp.go` with the constants, `stamp`, `readStamp` and `call`.
- [ ] **Step 2:** Write `Session` in `hooks.go`. It recovers from a panic like `Pre`, prints nothing, and does nothing with `WORKBENCH_GUARD=off`, an `agent` that is neither kind, or an input whose `hook_event_name` is not `SessionStart`. Otherwise:
  1. It finds its agent with `findAgent(os.Getppid())` and writes that agent's `agentFile`.
  2. It removes every file in the agents folder whose process is gone or has another start time, so the files never pile up when no watcher runs.
  3. For `claude` with a non-empty `CLAUDE_ENV_FILE`, it appends `stampLine` and a newline to that file (created 0600 when missing) unless a line of the file already equals `stampLine`.

  It never starts the watcher.
- [ ] **Step 3:** Add the hidden `workbench guard session AGENT` command beside `pre` and `post` in `internal/cli/guard.go`.
- [ ] **Step 4:** Add the hook after the costs hook in both files' SessionStart lists: `{ "type": "command", "command": "workbench guard session claude || true", "timeout": 5 }` in `home/.chezmoidata/claude.json`, and the same with `codex`, built from `$workbench` as its neighbors are, in `home/.chezmoitemplates/codex-hooks.json.tmpl`.
- [ ] **Step 5:** Run `scripts/render-check.sh` for each role and mode, then the gates.
- [ ] **Step 6:** Prove it in a throwaway session whose settings list the built binary's `guard session claude` twice under SessionStart, then run `echo "$WORKBENCH_GUARD_CALL $CLAUDE_PID $$"` in two tool calls. Expected: `agent:shell:second` each time with the shell differing; the scratch state folder holds one agent file naming the session's process; the env file holds the line once. Run `guard session claude` once more by hand with a `SessionStart` input after the session ended: the stale agent file is gone.
- [ ] **Step 7:** Commit: `feat(guard): stamp each command with its agent and shell`

---

### Task 4: Every call leaves a record, and the agents the guard knows

**Files:**
- Create: `internal/guard/records.go`
- Modify: `internal/guard/guard.go` (`note`), `internal/guard/hooks.go` (`Post`), `internal/guard/watch.go` (`notes`, `agents`, `markChildSeen`, the tracking loop in `tick`, `watcher`), `internal/guard/handover.go` (`carried`, `adopt`)

**Interfaces:**
- Consumes: `agentFile` (Task 3), `platform.StartTime` (Task 2).
- Produces:
  - `note.Ended bool` (`json:"ended,omitempty"`): set by the post hook of a call that is not a background call.
  - `type callRecord struct { ID string; Agent procKey; Kind kind; Subagent, Project, Cwd string; DryRun bool; Shell procRef; NotedAt time.Time }`, with JSON tags; `Shell` is zero until the call is tracked.
  - `type records struct`, with:
    - `add(n note)`: ignores an ID it already holds;
    - `tracked(id string, root proc)`;
    - `byID(id string) (callRecord, bool)`;
    - `byShell(agent procKey, shell int, second int64) (callRecord, bool)`: the record of that agent whose `Shell.PID` is `shell` and whose shell started, by `platform.StartTime`, no later than the end of `second` and no more than `stampSlack` before it. False when none or more than one does;
    - `prune(alive func(procKey) bool)` and `all() []callRecord`.
  - `const recordsKept = 4096`: records kept in all, the oldest dropped first. `const stampSlack = 30 * time.Second`.
  - `watcher.records *records`. `watcher.known map[procKey]kind`: the agents the guard knows (spec 5.4), from agent files and from notes, dropped when the agent is gone.
  - `carried.Records []callRecord` and `carried.Known []agentFile`.

- [ ] **Step 1:** Add `note.Ended`. In `Post`, a call that is not a background call reads its note and, when the note exists, marks it ended and writes it back; it deletes nothing and asks no lock. A background call is marked posted as today.
- [ ] **Step 2:** Write `records.go`.
- [ ] **Step 3:** In `watcher.notes`, every note read adds its agent to `known` and its record to `records`. An ended note is then deleted and treated exactly as a note that went away is treated today: it is not returned, so it is never matched, and the code after the loop logs `unmatched` under the same condition as now. It is never abandoned.
- [ ] **Step 4:** Each tick, before `agents`, read the agents folder: a file whose process is alive with that start time adds to `known`; one whose agent is gone (by `gone`, two rounds) is removed. `agents` then protects every live agent in `known`, and still returns only the agents of the notes and tracked commands for `readAgents`. `markChildSeen` builds `agentChildren` over every live agent in `known`.
- [ ] **Step 5:** Where `tick` starts tracking a command, call `records.tracked`. Call `records.prune` and prune `known` where `setsCallIDs` is pruned.
- [ ] **Step 6:** Carry `Records` and `Known` in `carried` and take them in `adopt`.
- [ ] **Step 7:** Run the gates.
- [ ] **Step 8:** Write the fake agent (see Global Constraints) and prove it in a scratch state folder: a 3-second command and a command that ends at once. Expected: after each post hook the note is gone within two seconds, the log has `tracking` and `ended` for the first and nothing for the second, and no `unmatched` or `abandoned` line. With the watcher stopped, a post hook leaves an ended note, and the watcher the next pre hook starts deletes it within two seconds.
- [ ] **Step 9:** Commit: `feat(guard): keep a record of every call and know agents from their start`

---

### Task 5: The rule for a detached unit, and its test

**Files:**
- Create: `internal/guard/units.go`, `internal/guard/units_test.go`

**Interfaces:**
- Consumes: `stamp` (Task 3), `platform.Lineage` (Task 2), `records` (Task 4).
- Produces:

```go
// owner is the call or unit in whose tree a process was seen.
type owner struct {
	ID    string // a call's ID or a unit's
	Agent procKey
	Kind  kind
}

// facts is what the unit rule reads beyond the snapshot. The watcher fills it
// from the kernel; the test fills it by hand.
type facts struct {
	self      int                                    // the watcher's user
	lineage   bool                                   // platform.KnowsOriginalParent
	read      func(pid int) (platform.Lineage, bool)
	stamp     func(pid int) stamp
	args      func(pid int) []string
	known     map[procKey]kind                       // agents the guard knows
	protected map[int]uint64
	versions  map[uint32]owner                       // a recorded version's owner
	seenUnder map[procKey]owner                      // a process seen in a tracked tree
	records   *records
}

// tie is how a unit's root is tied to an agent.
type tie struct {
	Agent  procKey
	Kind   kind
	Owner  string // the call or unit whose tree forked it, "" for a tie by stamp
	Shell  int    // the call stamp's shell, 0 without one
	Second int64  // the call stamp's second
	Codex  string // Codex's call ID, "" without one
	Mode   string // WORKBENCH_GUARD as read, "" when unread
}

type verdict int

const (
	notUnit verdict = iota // never a unit: do not ask again
	isUnit
	unread // no unit yet, but nothing readable was found: ask again while it lives
)

func unitRoot(s snapshot, r proc, f facts) (tie, verdict)
```

`unitRoot` is spec 5.2 and 5.3, in this order, each failure returning `notUnit` unless it says otherwise:

1. `r.UID == f.self`; `r`'s parent is PID 1 or a process whose name is in `initNames`; `r` is not a zombie, not protected and not under a protected process; `r` is not in `f.known`.
2. `filepath.Base` of `r`'s first argument is not `workbench`.
3. With `f.lineage`: `r`'s lineage and its parent's are both read, and `r`'s `ParentVersion` is not its parent's `Version`.
4. Tie by parent: `f.seenUnder` has `r`, or, with `f.lineage`, `f.versions` has `r`'s `ParentVersion`. The owner gives the agent and kind.
5. Otherwise, only with `f.lineage`, tie by stamp. The stamps are `r`'s own when readable, else those of the first readable processes below `r` reached through unreadable ones only; with none readable the verdict is `unread`. A `Codex` ID that `f.records.byID` resolves ties to that record's agent. Otherwise the stamps with `call()` true must be at least one and name one agent, which `f.known` holds under that process ID as `claude`.
6. A tie to a Claude Code agent fails when any stamp read for `r` has an `Agent` that is not the tie's, or a `Codex` ID that resolves to another agent. A tie to a Codex agent fails when a `Codex` ID read for `r` resolves to another agent.
7. The agent is alive in `s` with the start time `f.known` holds, and started before `r`. `Mode` is the first non-empty one among the stamps read, and `off` gives `notUnit`.

- [ ] **Step 1: Write the test.** `TestUnitRootOnlyForWhatAnAgentCommandForked`, table-driven over made-up snapshots and facts, with a comment above it naming the risk: the guard stopping a program no agent command forked, or an agent. Rows that are a unit: by stamp; by a recorded parent version; by `seenUnder` with `lineage` false; by a Codex ID while `CLAUDE_PID` names an outer agent (the unit is the Codex agent's). Then one row each that must give no unit:
  - no stamp and no recorded parent;
  - a stamp naming a dead process;
  - a stamp naming a process the guard does not know;
  - a stamp naming a process younger than `r`;
  - an init-started process carrying a stamp (its `ParentVersion` is its parent's `Version`);
  - the agent stamp without the call stamp;
  - a call stamp whose agent part differs from `CLAUDE_PID`;
  - a valid stamp with `lineage` false and nothing in `seenUnder`;
  - the root is a known agent, tied by stamp;
  - the root is a known agent, tied by parent;
  - a Workbench process;
  - a protected process, and a process under one;
  - `lineage` true with `read` answering false;
  - another user's process;
  - `WORKBENCH_GUARD=off`;
  - a tie by parent whose own stamp names another agent.

  And one row whose verdict is `unread`: an unreadable root with no readable process below it and no recorded parent.
- [ ] **Step 2:** Run `go test ./internal/guard/ -run TestUnitRoot -v`. Expected: it does not compile, `unitRoot` undefined.
- [ ] **Step 3:** Write `units.go`.
- [ ] **Step 4:** Run the test. Expected: PASS. Run the gates.
- [ ] **Step 5:** Commit: `feat(guard): decide which orphaned processes an agent command forked`

---

### Task 6: The watcher tracks detached units

**Files:**
- Create: `internal/guard/detached.go`
- Modify: `internal/guard/guard.go` (`command`), `internal/guard/watch.go`, `internal/guard/match.go` (`targets`), `internal/guard/manual.go`, `internal/guard/socket.go` (`chooseStops`), `internal/guard/view.go`, `internal/guard/memory.go` (`reason`), `internal/guard/handover.go`, `internal/guard/logs.go`, `internal/guard/units.go`, `internal/guard/units_test.go`, `internal/guard/match_test.go`

**Interfaces:**
- Consumes: Tasks 2 to 5.
- Produces:
  - `command.Detached *detached`, nil for a noted command. `type detached struct { Roots []procRef; Call string }`: `Call` is the launching call's ID, "" when it did not resolve. `command.Shown string`: the command line the view shows in place of `Note.Command`, never logged.
  - A unit's ID is `detached-<pid>-<start>` of its first root. Its `Note` is made up, never written to disk: `ToolUseID` the unit's ID; `AgentPID`, `AgentStart` and `Kind` from the tie; `Command` the root program's name (`filepath.Base` of its first argument, else the process's name); `Cwd` the root's working folder; `Project` `projectName("", Cwd)`; `Subagent` from the call's record when it resolved; `DryRun` true when that record's is or the tie's `Mode` is `dry-run`; `NotedAt` `platform.StartTime` of the root's start. `Shown` is the root's arguments joined by spaces.
  - `func (s snapshot) commandTree(c command, skip func(proc) bool) []proc`: the tree of `c.Root`, or for a unit the trees of its live roots, each process once.
  - `func live(s snapshot, c command) bool`: `c.Root` runs with its start time, or for a unit any of its roots does.
  - `func targets(s snapshot, c command, protected map[int]uint64) []target` now takes the command; for a unit it works over the trees of the live roots and gives nothing only when none is live.
  - In `units.go`:

```go
// candidate is a tracked command as the guard's own choice sees it: its size
// now and how long it has been tracked.
type candidate struct {
	c       command
	size    uint64
	tracked time.Duration
}

// choose is the guard's choice of what to stop: the largest candidate it may
// stop, the earlier root first among equals, and the sum of those it may.
func choose(cands []candidate) (chosen candidate, total uint64, ok bool)
```

    A noted command may always be chosen. A unit may be chosen only when `Detached.Call != ""` and `tracked >= unitGrace`. `const unitGrace = 10 * time.Second`.
  - `const versionsKept = time.Minute`: how long a process's version is kept after it is last seen. `const versionsLimit = 1 << 16`: the most versions kept, the oldest dropped first.
  - `watcher.stamps map[procKey]stamp`: each process's stamp, read once and dropped when the process is gone.
  - Log attributes `kind` (`detached`) and `launched_by` on a unit's `tracking`, `ended` and `stopping` records; `Event.Kind`, `Event.LaunchedBy`.

- [ ] **Step 1: Write the tests.** In `units_test.go`, `TestGuardChoosesOnlyWhatACallProvablyStarted`, with a comment naming the risk (the guard stopping work a person started with `!`, or a session that has not yet said it is an agent): of a 1 GB command, a 2 GB unit with a call tracked for a minute, an 8 GB unit without a call and a 9 GB unit with a call tracked for 3 seconds, `choose` gives the 2 GB unit and a total of 3 GB; of the last two alone it gives nothing. In `match_test.go`, change `TestTargetsStayInsideTheCommandTree` to pass a `command`, and add two rows: a unit of two roots, one gone, signals the live root's tree; a unit whose tree holds a protected agent signals neither that agent nor anything under it. Change the `chooseStops` call in `TestManualStopsOnlyWhatAPersonMayStop` to its new form (Step 2).
- [ ] **Step 2:** Add `Detached` and `Shown` to `command`, `commandTree`, `live` and `choose`. Use `commandTree` wherever `tick`, `measure`, `stopLargest`, `stop`, `stopFor` and `targets` take a command's tree, and `live` in each place that asks whether a tracked command still runs: `measure`, `stopLargest`, `refused`, `chooseStops`, `buildView` and `targets`. `stopLargest` builds the candidates and calls `choose`. `commandView` shows `Shown` when it is set. Run the tests: PASS.
- [ ] **Step 3:** In `detached.go`, record lineage at most once a second: for every process of every tracked command's and unit's tree, `seenUnder[procKey]` names the owner, and, where `platform.KnowsOriginalParent`, `versions[Version]` does, within the two constants above. `seenUnder` drops a process that is gone. Build the snapshot's parent index once per tick, not once per `tree` call.
- [ ] **Step 4:** In `detached.go`, find units each tick after matching and after Step 3: every child of an init process of the watcher's user that is not classified, by process ID and start time, goes through `unitRoot`, with stamps read through `watcher.stamps`.
  - A root tied by parent to an existing unit joins it (`Roots` grows).
  - Any other unit becomes a tracked command with `Detached` set and its `usage`, and is logged `tracking`.
  - A root whose verdict is `unread` is asked again each tick while it lives; any other verdict is kept.
  - `Call` is resolved once, here, as spec 5.5: the owner of a tie by parent (for an owner that is a unit, that unit's `Call`), else `records.byID(tie.Codex)`, else `records.byShell(tie.Agent, tie.Shell, tie.Second)`.
- [ ] **Step 5:** Agents among the units. A stamp read whose `Agent` names a live process that started before the stamped process makes that process protected while it lives (spec 5.4); `agents` adds these. Each tick a unit's root that is in `known` is dropped from the unit, and a unit left with no root is forgotten with one `ended` record whose `reason` is `its root is an agent`.
- [ ] **Step 6:** Lifetime (spec 5.6) in `forget`: a unit ends when every root has been gone for two rounds (`gone`), or its agent is gone. When a unit's root exits, the members init adopts are found by Step 4 as ties by parent of that unit and join it. A unit is never put in `w.ended`.
- [ ] **Step 7:** When `platform.KnowsOriginalParent` is true and `ProcessLineage(1)` answers false, log once per watcher, as an error event: `cannot read where processes come from; detached work is not tracked`.
- [ ] **Step 8:** Stops. `stop` writes a unit's kill record for `Detached.Call`, and only when that call's note is among the notes `stop` just read, is not posted, and no record for it is on disk; when nothing was signalled it removes only a record it wrote. `stopFor` does the same. `reason` gains the unit's wording: `workbench guard: stopped <program>, a program this call started that had detached, and its N child processes. …` with the same closing sentences as today.
- [ ] **Step 9:** Carry units at a handover: `carried` gains `Detached map[string]detached` and `Shown map[string]string`, and `adopt` takes a carried command with `Detached` without requiring its root to be its agent's child, and marks its roots and every process of their trees classified, so the new watcher does not find them again.
- [ ] **Step 10:** Run the gates.
- [ ] **Step 11:** Prove it with the fake agent in a scratch state folder:
  1. A command that double-forks a Python sleeper and waits. Expected in `guard logs --json`: `tracking` with `kind` `detached`, `launched_by` the call, and `command` the program's name, not its arguments.
  2. The same sleeper started with an argument `--token=SECRET-VALUE`: `rg SECRET-VALUE` over the scratch state folder finds nothing.
  3. End the fake agent's command and stop the sleeper: `ended` for the unit.
  4. A command that starts a second fake agent detached, which runs `guard session claude` and then idles: no `tracking` line names it after its session hook ran, and `guard memory --json` lists no unit for it.
- [ ] **Step 12:** Commit: `feat(guard): track work that detached from an agent's command`

---

### Task 7: Agent, helper and unmatched rows, and the sums

**Files:**
- Create: `internal/guard/rows.go`
- Modify: `internal/guard/guard.go` (`command`), `internal/guard/protocol.go`, `internal/guard/view.go`, `internal/guard/manual.go`, `internal/guard/socket.go`, `internal/guard/watch.go`, `internal/guard/match_test.go`

**Interfaces:**
- Consumes: `watcher.known`, `watcher.agentChildren`, `watcher.stamps`, `commandTree`, `command.Shown`.
- Produces:
  - `command.Row string`: `agent`, `helper` or `unmatched`, and `""` for a command or unit.
  - `watcher.rows map[string]command`: the rows of the last tick, by ID. They are never in `w.tracked`, so they do not keep the watcher from exiting idle, are not carried at a handover and are not ended by `forget`.
  - Row IDs `agent-<pid>-<start>`, `helper-<pid>-<start>`, `unmatched-<pid>-<start>`.
  - `CommandView.Kind string` (`json:"kind"`): `""` for a command, `detached`, `agent`, `helper`. `CommandView.PID int` (`json:"pid"`): the row's root. `CommandView.LaunchedBy string` (`json:"launched_by"`).
  - `type MemorySums struct { Commands, Detached, Agents, Helpers, Other uint64 }` with JSON tags, and `Snapshot.Sums MemorySums` (`json:"sums"`).
  - `const helperAfter = 5 * time.Second`: how long a direct child must have been seen before it is a row.
  - `func (w *watcher) buildRows(s snapshot, notes []note, taken map[int]bool, protected map[int]uint64)`: sets `w.rows` for this tick.
  - `func chooseStops(ids []string, tracked, rows map[string]command, s snapshot) (stop []command, results []StopResult)`.

- [ ] **Step 1: Write the test rows.** In `TestManualStopsOnlyWhatAPersonMayStop`, add rows: a stop naming an agent row answers `not allowed` and stops nothing; a stop naming a helper row chooses it.
- [ ] **Step 2:** `rows.go`. For each agent in `known` that is alive: an agent row sized by the agent process alone. For each direct child of it seen for `helperAfter` (from `agentChildren`), not in `taken`, and that no waiting note claims (`claims`):
  - under Claude Code, a helper when it is not a `shellScript` and no readable process of its tree carries a `CLAUDE_PID` naming that agent; an unmatched row when a readable process of its tree carries a stamp with `call()` true whose `CallShell` is that child and whose `CallSecond` fits the child's start as `byShell` judges it; otherwise no row;
  - under Codex, a helper when no readable process of its tree carries a Codex call ID; otherwise no row.

  A row's `Note` is made up like a unit's (`Command` the program's name), `Shown` its command line. Rows keep `usage` like commands, by row ID, dropped with `stoppedBy` when the row is gone. Rows are not logged while they run.
- [ ] **Step 3:** `tick` calls `buildRows` after `markChildSeen`. `drainStops` and `manualStop` pass `w.rows`, the rows of the tick before, to `chooseStops`, which checks each against this tick's snapshot with `live`: an agent row answers `not allowed`. `stopFor` writes no kill record for a row, and its log records carry `kind` and the program's name.
- [ ] **Step 4:** `buildView` appends the rows after the tracked commands, an unmatched row with the state `unmatched` and no end time, and fills `Kind`, `PID`, `LaunchedBy` and `Sums`. An unmatched row counts in `Sums.Commands`. `Other` is the memory in use less the four sums, never below zero.
- [ ] **Step 5:** Run the tests and the gates.
- [ ] **Step 6:** Commit: `feat(guard): show each agent, its helpers and where memory is`

---

### Task 8: The view and the log viewer

**Files:**
- Modify: `internal/cli/guardview.go`, `internal/cli/guard.go`

**Interfaces:**
- Consumes: `CommandView.Kind`, `PID`, `LaunchedBy`, `Snapshot.Sums` (Task 7); `Event.Kind`, `Event.LaunchedBy` (Task 6).

- [ ] **Step 1:** `shortCallID` gives `p<pid>` for an ID starting `detached-`, `agent-`, `helper-` or `unmatched-`.
- [ ] **Step 2:** `active` takes the row: it still runs when its state is running or stopping, or is `unmatched` with no end time. So a running unmatched row can be tagged and stopped, shows its memory, and is not counted as finished.
- [ ] **Step 3:** `statusText` takes the kind: a running `detached`, `helper` and `agent` row reads `● detached`, `● helper` and `● agent`; a running unmatched row reads `? unmatched`. Stopping, stopped and done read as today.
- [ ] **Step 4:** Under the count line, one line from `Snapshot.Sums`, faint, dropped when a snapshot has none and clipped to the window:
  `commands 40 MB · detached 4.20 GB · agents 1.36 GB · helpers 1.39 GB · other 5.06 GB`
- [ ] **Step 5:** The details screen adds `Started by` with the launching call's ID for a unit whose call resolved. A stop of an agent row says `agents are never stopped` on the bottom line and sends nothing.
- [ ] **Step 6:** The log viewer shows `kind` and `launched_by` (the call's short ID) after `project`. Update the field order in `guard logs --help` and the row kinds in `guard memory --help`.
- [ ] **Step 7:** Run the gates.
- [ ] **Step 8:** Prove it against the fake agent of Task 6 with a helper child added, through a pseudo-terminal: the view shows the four kinds of row and the sums line; `k` on the helper stops it; `k` on the agent is refused.
- [ ] **Step 9:** Commit: `feat(guard): show detached, agent and helper rows in the memory view`

---

### Task 9: The Monitor post hook

Spec 2.1 fact 15: the post hook fires when the monitor starts, names the tool `Monitor` and carries the pre hook's `tool_use_id`.

**Files:**
- Modify: `home/.chezmoidata/claude.json`, `internal/guard/hooks.go`

- [ ] **Step 1:** Match Claude Code's PostToolUse and PostToolUseFailure hooks on `Bash|Monitor`. The pre hook and the Codex hooks do not change.
- [ ] **Step 2:** In `Post`, a call whose tool is `Monitor` is a background call: its note is marked posted.
- [ ] **Step 3:** Run `scripts/render-check.sh` for each role and mode, then the gates.
- [ ] **Step 4:** Prove it in a throwaway session with a scratch state folder: after a Monitor command ends, its note is gone and the log has no `abandoned` line for it.
- [ ] **Step 5:** Commit: `fix(guard): clear a Monitor command's note when it ends`

---

### Task 10: Docs

**Files:**
- Create: `docs/adr/0016-guard-detached-work.md`
- Modify: `docs/usage.md`, `docs/superpowers/specs/2026-10-14-agent-command-guard-design.md`, `docs/superpowers/specs/2026-10-17-platform-and-guard-view-design.md`, `AGENTS.md`, `internal/guard/guard.go` (package comment), `README.md` if it states what the guard tracks

- [ ] **Step 1:** ADR 0016, in the form of ADR 0013: the context (work that detaches is unseen), the decision (stamps checked against the original parent; a stop only for what a call provably started; agent and helper rows the guard never stops), what was rejected and why (spec section 3), and the consequences (spec section 10).
- [ ] **Step 2:** `docs/usage.md`: replace the sentence that detached programs are not tracked; say the guard itself never stops an agent or what it starts, and that a person may stop a helper from the view; describe the row kinds and the memory line; add `kind` and `launched_by` to the log's fields; say what Linux and WSL find.
- [ ] **Step 3:** The guard design's section 12 line and the view design's section 1 line, columns and protocol section, each replaced in place.
- [ ] **Step 4:** `AGENTS.md`: the guard row of the ownership table names the SessionStart hook. The package comment says what the guard itself never stops.
- [ ] **Step 5:** Commit: `docs(guard): describe detached work and the new rows`

---

### Task 11: Checks before release

**Files:**
- Modify: `docs/acceptance.md`, `docs/superpowers/specs/2026-11-13-guard-detached-work-design.md` (section 2)

Each check uses the built binary, a scratch state folder and throwaway sessions or fake agents, as the Global Constraints say. A check that fails is fixed in the task that owns the code, then run again.

- [ ] **Check 1:** A real Claude Code command that double-forks a sleeper gives a `detached` row with its agent, and its subagent when a subagent made the call. A stop from the view ends it.
- [ ] **Check 2:** A command that leaves `/bin/sleep 60 &` behind when it returns gives a `detached` row with its call.
- [ ] **Check 3:** A unit whose root exits while a child lives stays one row, is still measured and can still be stopped.
- [ ] **Check 4:** An app started with `open` from a command gives no row, and neither does the editor's command-line launcher starting the editor. A daemon a hook starts gives no row.
- [ ] **Check 5:** A real nested session a command starts with `nohup … &` and leaves detached shows as an `agent` row, not a `detached` one, and a dry-run stop at critical memory never names it.
- [ ] **Check 6:** A sub-second Claude Code command that leaves a sleeper gives a row with a blank subagent that a dry-run stop never names; under Codex it gives a row with its call.
- [ ] **Check 7:** An agent with a language server shows an agent row and a helper row, and the five sums add up to the memory in use.
- [ ] **Check 8:** A handover while a unit is tracked: start the watcher from a copy of the binary in the scratch folder, replace that copy with a build of another version, and the next watcher shows the unit once, with its peak and its call, and logs `tracking` for it once.
- [ ] **Check 9:** With a build of a few thousand processes under a tracked command, the watcher's own size stays under 32 MiB.
- [ ] **Check 10:** In a Linux container with the Linux build and the fake agent: an orphan's parent is an init the guard knows; a sleeper that ran under the command for two seconds before it detached gives a `detached` row and a stop ends it; one that double-forks at once gives none. Record what could not be run there (a real agent, its sandbox, its cgroup, a session manager) as unqualified.
- [ ] **Step 11:** Record the results in `docs/acceptance.md`, impersonally, and move the proved items of the spec's 2.3 to 2.1.
- [ ] **Step 12:** Commit: `docs(acceptance): record the detached work checks`
