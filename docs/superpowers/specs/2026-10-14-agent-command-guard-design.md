# Agent command guard design

Status: implemented 2026-10-14; section 10 steps 1 and 4 (Linux/WSL2 shape
observation, real-pressure check and threshold settling) not yet done; see
`docs/acceptance.md`.

An earlyoom-style guard scoped to the shell commands AI coding agents write.
When the machine is about to run out of memory, it stops the largest
agent-written command, with everything that command started, and tells the
agent why. It never stops the agent session, its harness, MCP servers, hooks
or anything else on the machine.

## 1. Goal and boundaries

- **Guarded:** each command an agent runs through its shell tool (Claude Code
  `Bash`, Codex `Bash`), with every process that command starts.
- **Not guarded:** the agents' built-in tools, MCP servers, hooks, the status
  line, the agent processes themselves and every other program. They run
  exactly as they do today.
- **Untouched commands:** the guard never rewrites, wraps, re-parents or
  re-prioritises a command. Rewriting it into a wrapper would run it one shell
  deeper, so a `cd`, an `export`, aliases and shell functions would no longer
  carry over: Claude Code records the working directory with `pwd -P >| <file>`
  after the command, in the same shell that ran it.
- **Acts only near out-of-memory:** there are no per-command limits and no
  prediction. Commands run concurrently at full speed and nothing waits on the
  guard.
- **Fails closed:** whatever goes wrong (an agent changes how it starts
  commands, the guard crashes, a hook times out), the result is an unguarded
  command, never a blocked one or a stopped process that was not an agent
  command.
- **Platforms:** macOS, Linux and WSL2, for Claude Code and Codex.

## 2. Facts this design rests on

Observed on macOS with Claude Code 2.1.299 (`claude -p`) and Codex 0.162.0
(`codex exec`), using throwaway hooks that logged process trees and stopped a
test command:

- **Each shell command is its own process group, directly under the agent.**
  Claude Code: `/bin/zsh -c 'source ~/.claude/shell-snapshots/snapshot-….sh …
  && eval '<command, single-quote escaped>' && pwd -P >| /tmp/claude-…-cwd'`,
  parent `claude`, `pgid == pid` (2.1.300; 2.1.299 also put `< /dev/null`
  after the `eval`, so the matcher looks only for the `eval '…'` part). Codex: `/bin/zsh -c
  <command>`, parent `codex`, `pgid == pid`, the same with its sandbox on
  (`-s workspace-write`) or off.
- **A single simple command replaces its shell.** zsh and bash `exec` the
  last simple command of a `-c` script in place: `/bin/zsh -c 'sleep 2'` runs
  as `sleep 2`, same PID and group, while `/bin/zsh -c 'sleep 2; true'` stays
  `zsh` (observed on macOS, zsh and bash). Claude Code's script is always
  compound, so its shell stays. A Codex command such as `cargo test` or
  `pytest -n 8` shows as that program, not as `zsh -c`.
- **Hooks are distinguishable.** A command hook runs as its own process, a
  direct child of the agent in its own group, with the hook's own command
  line. On macOS, `/bin/sh -c '<one command>'` execs that command, so the hook
  process is the agent's child.
- **Signalling the command's group stops only the command.** `kill -TERM
  -<pgid>` ended the shell and its children; the agent carried on. Claude Code
  reported exit 144, Codex exit 143.
- **A post hook's text reaches the model verbatim.** Claude Code fires
  `PostToolUseFailure` for the stopped command (`PostToolUse` when a command
  exits normally) and places `hookSpecificOutput.additionalContext` next to
  the tool result. Codex fires `PostToolUse`, also after non-zero exits (it has
  no failure event), and adds `additionalContext` as developer context.
- **Each call's `tool_use_id` is the same in its pre and post hooks**, on both
  agents (`toolu_…` on Claude Code, `exec-…` on Codex).
- **A post hook's text reaches the model even when nothing was stopped**, so
  the post hook stays silent unless a kill record names its call.

Read from documentation and source:

- **A pre hook that exits with code 2 blocks the call** on Claude Code ("a
  `PreToolUse` hook blocks the tool call") and denies it on Codex. An unknown
  `workbench` subcommand exits 2, as does a Go panic.
- **Hooks fail open on timeout.** A Claude Code hook that times out lets the
  call proceed through the normal permission flow, which runs after
  `PreToolUse`. So a call can wait for approval for a long time after its pre
  hook.
- **Codex on Linux runs sandboxed commands through bubblewrap by default**
  (`codex-rs/linux-sandbox/README.md`): `codex` → `codex-linux-sandbox` →
  `bwrap` → shell. WSL2 takes the same path. Claude Code has its own optional
  sandbox (bubblewrap on Linux).
- **Codex's interactive unified exec can deliver a command's `PostToolUse` on
  a later `write_stdin` poll**, not when the command first returns.
- **WSL2's kernel has pressure stall information.** Microsoft's `config-wsl`
  sets `CONFIG_PSI=y` with `CONFIG_PSI_DEFAULT_DISABLED` unset, so
  `/proc/pressure/memory` exists.
- **macOS memory readings without C:** `sysctl kern.memorystatus_vm_pressure_level`
  (1 normal, 2 warning, 4 critical) through `golang.org/x/sys/unix`.
  `kern.memorystatus_level` is not a usable "available" figure: it counts
  active and inactive pages, so a runaway command's own memory counts as
  available.
- **A process's macOS footprint without C.** The raw `proc_info` syscall
  (`SYS_PROC_INFO`, call 9 `PROC_INFO_CALL_PIDRUSAGE`, flavor 0) returns
  `rusage_info_v0`. Its `ri_phys_footprint` is at offset 72, and it read the
  same bytes as cgo's `proc_pid_rusage` with `CGO_ENABLED=0`. That footprint is
  the figure Activity Monitor shows. Resident memory from `ps` is wrong both
  ways: it misses compressed memory and counts shared pages.
  `internal/operation/image_metadata_darwin.go` already uses raw darwin
  syscalls for the same reason (cgo would break `CGO_ENABLED=0` release
  builds). A cgo build would also raise the binary's minimum macOS to the build
  machine's, 27.0 against 12.0 today.
- **Codex hook trust** is written by
  `home/private_dot_codex/modify_private_config.toml.tmpl` for every hook in
  the Workbench hooks template, including `PreToolUse` and `PostToolUse`.

Not yet observed, and the first step of the work (section 10):
- both agents on Linux and WSL2
- both agents' sandboxes
- the interactive sessions of both agents
- when the post hook of a `run_in_background` command fires.

## 3. Parts

All three parts are hidden subcommands of the one `workbench` CLI, in a new
`internal/guard/` package; `internal/cli/` owns their flags. The guard reuses
the owners the costs hook already uses: `operation.TryLock` for the
one-watcher lock, `operation.StartDetached` to start the watcher, and the
operation layer's private-directory checks for its folder. Only the watch
loop, the hook bodies and the platform readers are new.

| Part | Runs | Job |
|---|---|---|
| `workbench guard pre` | `PreToolUse` hook on `Bash` | Leaves a note for the call; makes sure the watcher is running. Prints nothing. |
| `workbench guard watch` | At most one per user, started by `pre`, detached | Finds each noted command, watches machine memory, stops the largest agent command near out-of-memory, records why. |
| `workbench guard post` | `PostToolUse` (and `PostToolUseFailure` on Claude Code) on `Bash` | Gives the agent the reason if the watcher stopped this call; otherwise prints nothing. Drops the call's note. |

**Exit status.** `pre` and `post` always exit 0: they recover from panics and
report errors only to the guard log. The hook commands also end in `|| true`,
so a `workbench` that does not know `guard` yet, such as an older installed
release under a newer `home/` with `--local-build`, can never block a call.

**Cost.** Each hook start costs about 30–80 ms (the `workbench` binary's
start-up), so a guarded command pays roughly 60–160 ms in total.

## 4. State

A `guard/` folder inside Workbench's own state folder, the one
`operation.Resolve` already returns as `Paths.State`, beside `activation.json`,
`checkpoints/` and `locks/`:

- macOS: `~/Library/Application Support/workbench/state/guard/`
- Linux and WSL: `~/.local/state/workbench/guard/` (`$XDG_STATE_HOME/workbench/`
  or `WORKBENCH_STATE_DIR` when set)

It is made and checked by the operation layer's private-directory owner:
`0700`, owned by the user, no symlinked parents. It is not in a shared temp
folder: those have predictable names other users could create first, and the
macOS temp folder is pruned of files older than three days.

```
<state>/guard/
  watch.lock                          held by the running watcher
  watch.log                           capped at 1 MiB, then rotated once
  notes/<tool_use_id>.json            {agent pid, agent start, kind,
                                       session_id, tool_use_id, command,
                                       noted_start, noted_at, dry_run}
  killed/<tool_use_id>.json           {reason, ...}  written by the watcher
```

- **File names.** A `tool_use_id` becomes a file name only if it matches
  `[A-Za-z0-9_-]{1,128}`. Any other call is not guarded.
- **Writes** go to a temp name first and are then renamed into place.

**Finding the agent.** It's the hook's nearest ancestor that is not a shell,
identified by PID **and** start time, so a later process reusing the PID is
never taken for it.

**Start times** are the kernel's own, never the wall clock read later:
- **Linux:** clock ticks since boot (`/proc/<pid>/stat` field 22), which a
  wall-clock step (WSL2's resync after the host sleeps) doesn't move.
- **macOS:** `kinfo_proc`'s `p_starttime`, the wall clock when the process
  began, recorded the same way for every process. Only a clock step between
  the pre hook and the command's start can shift one against the other.

`noted_start` is the pre hook process's own start time, in these units.
`noted_at` (wall clock) is only for the log.

## 5. Pre hook

1. Read the hook input. Ignore anything that is not a `Bash` call with a valid
   `tool_use_id`, and stop if `WORKBENCH_GUARD=off` is set in the agent's
   environment.
2. Find the agent (section 4) and write `notes/<tool_use_id>.json`.
3. Try `watch.lock` without waiting. If it is free, release it and start
   `workbench guard watch` with `operation.StartDetached`: a new session, with
   stdin on the null device and output to the guard log. Nothing waits for it.
   The watcher takes the lock itself, and a second watcher that loses the race
   exits at once.
4. Exit 0 with no output, so the call proceeds unchanged.

## 6. Watcher

One watcher serves every agent session of the user. It alone decides which
command to stop, so two commands are never stopped for the same shortage.

### 6.1 Finding agent commands

Each tick, for each agent named in a note, the watcher lists that agent's
child processes:
- **macOS:** `kern.proc.all` through `SysctlKinfoProcSlice`, plus
  `kern.procargs2` for command lines.
- **Linux:** `/proc/*/stat` and `/proc/<pid>/cmdline`.

A child becomes a **tracked command** when the process running the command
has exactly the shape its agent uses for a shell command holding the text of
one of that agent's notes. That process is the child itself or, when the
child is a sandbox launcher (`codex-linux-sandbox`, `bwrap`), the first
process below the chain of launchers. Nothing deeper counts: a program the
command starts itself, such as npm's own `sh -c jest`, never stands for a
note.

- **Claude Code:** a shell run with `-c` whose script contains `eval '<note>'`,
  with the note escaped the way Claude Code escapes it (`'` as `'"'"'`, and
  its `>nul` → `>/dev/null` rewrite applied first).
- **Codex:** a shell whose `-c` argument equals the note. On Linux it may sit
  under `codex-linux-sandbox` and `bwrap`.
- **Codex, single command:** when the note is one simple command of plain
  words (letters, digits, `_./:=+,@%-`, and single- or double-quoted words
  without `$`, backquote or backslash), a process that leads its own group
  (`pgid == pid`) and whose argv equals those words. Any other note matches
  only the shell form, so a command using expansion, redirection or several
  commands is matched while its shell runs it and is otherwise unguarded.
  Section 10 step 1 observes whether a Codex harness process can lead its own
  group with an agent command's argv.
- **Timing:** the child and that process must have started at or after the
  note's `noted_start`, less one second, comparing kernel start times
  (section 4).
- **Duplicates:** when two notes carry the same text, the earlier note goes to
  the earlier-started child.

A child that still carries its parent's own command line has forked but not
yet exec'd, so it is checked again on the next tick. A child whose exec'd
command line matches no note (MCP servers, hooks, git or search processes the
harness runs, helpers such as `caffeinate`) is never tracked. A shape the
guard does not recognise therefore leaves the command unguarded and logs it,
and never tracks a harness process. The log line comes when the note goes
unmatched after a child of its agent that started later than the note, and
is no tracked command, was seen alive at every tick for at least a second
(in time, not ticks, which quicken to 100 ms under pressure). A child that
lives less, such as a command that starts and ends between two ticks or a
hook's shell, gives no line. A child of the agent that lives past a second
and is not a tracked command still gives one: a helper the agent keeps
running, or the status line when a loaded machine stretches its run past a
second.

**When a note is dropped:**
- its post hook runs
- its agent exits.

Never on a timer, because a call may wait a long time for permission between
its pre hook and its start. A denied call's note stays until its agent exits.

**A call runs once.** Once a call's tracked command has ended, the watcher
remembers the call until its note is dropped and never matches that note
again. Otherwise a denied call's note, which keeps the earliest start, would
claim every later command with the same text for the rest of the session,
and the reason would land under the dead call. It still claims the first
such command once (the earlier note takes the earlier child), which the log
shows as `tracking <denied call's id>`. That call's own note is then dropped
by its post hook without a line: the command it ran is tracked, and a
tracked command never marks a note as one that could have run.

### 6.2 When to act

The watcher reads machine memory each tick and decides between nothing, a
polite stop (`SIGTERM`) and a forced stop (`SIGKILL`):

| | Polite stop | Forced stop |
|---|---|---|
| Linux, WSL2 | available memory ≤ 10 % **and** free swap ≤ 10 %, or memory pressure `full avg10` ≥ 50 % **with** available memory ≤ 20 % | available ≤ 5 % **and** free swap ≤ 5 % |
| macOS | pressure level warning (2) | pressure level critical (4) |

**Linux:**
- Available memory is earlyoom's figure: `MemAvailable` as a share of
  `MemAvailable + AnonPages`. With no swap, the swap condition counts as met.
- Pressure catches thrashing before swap is full, which the memory-and-swap
  rule misses. Pressure counts only alongside low memory, because pressure
  stays high for seconds after the memory is freed.

**macOS:**
- Uses the kernel's own pressure level, which is also what triggers its
  built-in killing.
- There's no swap condition: macOS grows swap as needed, so "swap nearly full"
  comes too late.

**The thresholds are constants.** They're settled from the runs in section 10
before release.

**Tick length:**
- **Linux:** follows earlyoom's adaptive sleep. The room left above the
  polite-stop line is divided by fill rates of 6000 MiB/s for memory and
  800 MiB/s for swap, and the result is clamped to between 100 ms and 1 s.
- **macOS:** ticks every 100 ms at warning or above, otherwise every 1 s.

### 6.3 Which command

- **Sizing:** each tracked command's size is the total over its process tree:
  - macOS: `ri_phys_footprint`
  - Linux: `Pss` from `/proc/<pid>/smaps_rollup`, which counts shared pages
    once.
- **Choosing:** the watcher stops the largest tracked command across every
  session; ties go to the older command.
- **Who's eligible:** only agent commands are candidates, never other
  programs.
- **Cooldown:** after a stop, nothing else is stopped until that command's
  processes are gone and the pressure figures have had 10 seconds to catch up,
  or until the stop gives up (section 6.4). A dry-run command is logged once,
  then left out of the choice, and never starts a cooldown.

### 6.4 Stopping it

1. **Re-check:** read memory again. If it has recovered, do nothing (as
   earlyoom does).
2. **Snapshot** the tracked command's process tree: PID, parent, group and
   start time of each member.
3. **Choose targets** from that snapshot. A process group is signalled as a
   group only when its leader is live and every member of the group is in the
   tree; any other member is signalled by PID. A zombie holds no memory and is
   never a target. A target is dropped if its start time has changed since
   the snapshot or it has become a zombie, and only a live process signalled
   counts as stopped.
4. **Never signal** PID 0 or 1, the watcher, any agent that has left a note
   (with everything under it), or any process outside the tracked tree. A
   nested agent that has not run its shell tool yet has left no note: it is
   part of the outer command and stops with it. So does a nested agent whose
   own calls have all finished: with no note and nothing tracked, it is no
   longer protected. There is no protection by process name. The notes are
   read again just before the targets are chosen, before `SIGKILL` and at
   each poll, so an agent that leaves its first note during a stop is
   protected too: it and every stopped process under it are dropped from the
   stop, which then neither signals them, waits for them, nor lists them when
   it gives up.
5. **Signal:**
   - **First** `SIGTERM`, then poll every 100 ms.
   - **Escalate to `SIGKILL`** when memory reaches the forced-stop line (as
     earlyoom does) or after 5 seconds. The 5-second rule is this design's
     own: an agent is waiting on the command, and a command that ignores
     `SIGTERM` must not hold the machine.
   - **Escalate by PID:** take a fresh snapshot and send `SIGKILL` to every
     process of the stopped command still alive with its snapshot start time,
     and to anything started since under one still running. A shell that died
     of `SIGTERM` was the group leader; once it is reaped, a child that
     ignored `SIGTERM` lives on under PID 1, outside any group the watcher may
     still signal.
   - **Give up** after 10 seconds: log the processes still running, end the
     cooldown, and never choose that call again, so one process that can't
     be stopped (in uninterruptible I/O, for example) never keeps the watcher
     from acting, nor holds it ten seconds every round.
   - **On Linux,** open a pidfd first and call `process_mrelease` after the
     signal, as earlyoom does, so the memory frees at once. If the pidfd can't
     be opened for any reason but the process being gone (out of memory or
     descriptors, say), re-check its start time and signal by PID, as earlyoom
     does, without `process_mrelease`.
6. **Record:** write `killed/<tool_use_id>.json` with the reason and a log
   line. It is written before step 5's first signal, because the agent's post
   hook may run the moment the command dies, and deleted if nothing was
   signalled. If the record can't be written, the stop goes ahead anyway,
   because the machine comes first, and the agent then gets no reason. A stop
   that signalled nothing starts no cooldown. If the command is still running
   (the signal was refused, as for a root-owned process, or no target was
   left to signal), the call is not chosen again after three such stops in a
   row, so it is not retried every tick; if it is gone or could not be
   checked, the next tick tries again.

With `WORKBENCH_GUARD=dry-run` set for that agent, steps 5 and 6 only log.

The reason names the command, its size, the machine's state and what to try:

```
workbench guard: stopped `uv run pytest -n 8` and its 6 child processes. The
machine was nearly out of memory (1.1 GB of 32 GB available, swap 94 % used)
and this was the largest agent command, at 9.3 GB. Retry with less
parallelism or a smaller input.
```

### 6.5 Life cycle

- **Agents end:**
  - The watcher waits on each noted agent's exit (macOS `kqueue`
    `EVFILT_PROC`/`NOTE_EXIT`; Linux pidfd) and also checks each agent's PID
    and start time every tick.
  - When an agent is gone, its notes are deleted and its commands are no
    longer tracked.
  - Kill records older than a day are deleted.
- **A call runs once:** once a call's command has ended, its note never
  matches again, even if the note stays (a denied call's stays until its agent
  exits). The watcher keeps this in memory only, so after a watcher restart a
  note that is still present for a call that already ran (only a denied call
  keeps one) may match one more command with the same text. This is known and
  bounded: the note is deleted when its agent exits, and the match needs a
  command of that exact shape started after the note.
- **Idle exit:**
  - The watcher exits when it has had no note and no tracked command for 10
    minutes.
  - Before exiting it releases the lock, reads the notes once more, and if a
    live note arrived, or the process list can't be read, it takes the lock
    again and carries on. A note that doesn't parse is deleted, since the pre
    hook only renames complete files into place, so a broken note never keeps
    the watcher alive. If another watcher took the lock first, it exits.
  - So a note written as a watcher stops is never left without one.
- **One at a time:** the `flock` on `watch.lock` belongs to the running
  watcher. The operating system releases it whenever the watcher dies, so a
  crashed watcher never looks alive. Each tick the watcher checks that the
  lock file is still the one it locked (same device and inode), and exits if
  not.
- **Crash or reboot:**
  - Commands keep running, unguarded, until the next command's pre hook starts
    a new watcher.
  - Nothing survives a reboot.
  - On start, a watcher deletes notes whose agent (PID plus start time) is
    gone.
- **Bounded:**
  - The watcher sets a Go memory limit of 32 MiB, has no terminal, and logs to
    its capped file only.
  - Its life is bounded by the agents it serves plus 10 minutes.
- **Kill switch:** `WORKBENCH_GUARD=off` in the agent's environment disables
  `pre`, and `WORKBENCH_GUARD=dry-run` records it in the note.

## 7. Post hook

1. Read the hook input; with an invalid `tool_use_id`, exit 0.
2. Delete `notes/<tool_use_id>.json`.
3. If `killed/<tool_use_id>.json` exists, print `{"hookSpecificOutput":
   {"hookEventName": <event>, "additionalContext": <reason>}}`, delete the
   record, and exit 0. Otherwise exit 0 with no output.

**Background commands:** a command the agent sent to the background
(`run_in_background`) may reach its post hook before any stop (section 10
observes when). It stays tracked after its note is dropped. If the watcher
stops it later, the reason goes only to the guard log.

## 8. Wiring

- **Claude Code:** `home/.chezmoidata/claude.json` `enforced.hooks` gains
  `PreToolUse`, `PostToolUse` and `PostToolUseFailure` entries matching `Bash`.
  They run `~/.local/bin/workbench guard pre || true` and `… guard post ||
  true`, each with a 5-second timeout.
- **Codex:** `home/.chezmoitemplates/codex-hooks.json.tmpl` gains `PreToolUse`
  and `PostToolUse` entries matching `Bash`, with the costs hooks' quoting and
  the same `|| true`. Their trust hashes come from the existing modify
  template.
- **Both** use the installed `workbench`, so a release activation updates the
  guard with everything else.
- **Release builds stay as they are:** `CGO_ENABLED=0`, one Ubuntu runner, all
  four targets.

## 9. Platform readers

| | macOS | Linux, WSL2 |
|---|---|---|
| Machine | `kern.memorystatus_vm_pressure_level`, `vm.swapusage` for the reason text, `hw.memsize` | `/proc/meminfo`, `/proc/pressure/memory` |
| Processes | `kern.proc.all`, `kern.procargs2` | `/proc/<pid>/stat`, `/proc/<pid>/cmdline` |
| Size | raw `proc_info` `PROC_INFO_CALL_PIDRUSAGE`, `ri_phys_footprint` | `/proc/<pid>/smaps_rollup` `Pss` |
| Agent exit | `kqueue` `EVFILT_PROC` `NOTE_EXIT` | pidfd |

The raw darwin syscall follows `image_metadata_darwin.go`:
- each pointer becomes a `uintptr` inside the `Syscall6` argument list
- an error or unexpected size fails closed: the command counts as size 0 and
  is logged
- the layout is rechecked on each major macOS release.

## 10. Order of work

1. **Observe the shapes the matcher depends on.** Run the throwaway probe
   under each case and record the agent versions:
   - Claude Code and Codex on Linux and WSL2, each with its sandbox on and off
   - both agents' interactive sessions as well as `claude -p` and `codex exec`
   - a `run_in_background` command.

   Adjust section 6.1 to what is seen.
2. **`internal/guard`:** platform readers, the watcher, the pre and post hooks,
   and the CLI.
3. **Wiring** (section 8), with `scripts/render-check.sh` for the affected
   roles.
4. **Check under real pressure** in a disposable VM or container with capped
   memory, on Linux and on macOS. A runaway command must be stopped, while the
   agent, its MCP servers and a second, smaller agent command survive, and the
   agent must show the reason. Settle the section 6.2 thresholds from these
   runs.
5. **Docs:**
   - `docs/usage.md`: what the guard does, `WORKBENCH_GUARD`.
   - `docs/acceptance.md`: the support claims observed in step 4.
   - The design, contracts and README, where they list hooks.
   - The AGENTS.md ownership table gets a row: "Agent command memory guard |
     `internal/guard/` (hooks in `home/.chezmoidata/claude.json` and the Codex
     hooks template)".

Each new agent version can change the shapes in section 6.1. A changed shape
leaves commands unguarded and logged (section 1), so the observation in step 1
is repeated when the guard log shows unmatched notes.

## 11. Test

One test file, for the one catastrophic risk: stopping something that is not
the agent's command, such as the agent session, an MCP server or an unrelated
program. Both functions that decide what may be stopped are pure, and the
test runs them on fixed process snapshots:

- **Matching (6.1):**
  - a harness `git status` or `rg` beside a note `git status`
  - a forked-but-not-exec'd child
  - a process started before the note
  - two identical notes
  - the Codex sandbox chain
  - a Codex command its shell replaced with `exec`, beside a harness `git` in
    the agent's group
  - a program the command starts (`npm test` running `sh -c jest`) and an
    earlier pending `jest` note.

  Only the agent's real command shell is tracked.
- **Targets (6.4):**
  - a group shared with an untracked process
  - an agent that left a note, or the watcher, inside the tree
  - a zombie group leader whose child lives on in its group
  - PID reuse.

  Nothing outside the tracked tree, no agent and not the watcher is ever a
  target.

## 12. Not in scope

- Guarding MCP servers, hooks, built-in tools or the agents' own memory.
- Processes that leave the command's tree (daemonized or `nohup` + `setsid`
  children that re-parent to init). Once they leave, they are not tracked.
- A process started, after the snapshot, under one that then exits before
  the escalation: for example, a `SIGTERM` trap that starts a child in the
  background and exits, or every snapshot process dying before 5 seconds
  while something it started lives on. That orphan sits under PID 1, outside
  every process the watcher knows, and is not stopped. The guard may stop too
  little here, never too much.
- Per-command or per-project memory limits.
- Changing any process's `oom_score_adj` or priority.
- Native Windows. Under WSL2 the guard sees the WSL virtual machine's memory,
  which `.wslconfig` caps.
