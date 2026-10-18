# Agent command guard design

Status: implemented 2026-10-14. Section 10 step 1 (shape observation) ran on
Linux on 2026-10-16 with `claude -p` and `codex exec`, Codex's sandbox on and
off; WSL2, the interactive sessions and Claude Code's own sandbox are not yet
observed. Step 4 (real pressure) ran on Linux in a memory-limited cgroup and
settled the 10 % polite line there only; see `docs/acceptance.md`.

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
  exactly as they would without the guard.
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

Observed on macOS with Claude Code 2.1.299 and 2.1.300 and Codex 0.162.0,
using throwaway hooks that logged process trees and stopped a test command,
and on 2026-10-16 in a Debian container with Claude Code 2.1.301 (`claude -p`)
and Codex 0.162.0 and 0.163.0 (`codex exec`, sandbox on and off), running the
guard's own hooks with `WORKBENCH_GUARD=dry-run`:

- **Each shell command is its own process group, directly under the agent.**
  Claude Code: `<shell> -c '<prelude> && eval <command> < /dev/null && pwd -P
  >| /tmp/claude-…-cwd'`, parent `claude`, `pgid == pid`. The prelude is
  `source ~/.claude/shell-snapshots/snapshot-….sh 2>/dev/null || true`, and
  may carry `export` and `setopt` lines (2.1.300). A `run_in_background` call
  runs in the same shape. Codex: `<shell> -c <command>`, parent `codex`,
  `pgid == pid`, the same with its sandbox on (`-s workspace-write`) or off on
  macOS.
- **Codex's Linux sandbox chain:** `codex` → `codex-linux-sandbox` (its own
  group, holding `<shell> -c '…; exec '<shell>' -c '<command>''` after its
  shell snapshot) → `bwrap` → `codex-linux-sandbox` → the command, in the
  group of the launcher that started it (`pgid` is its parent's PID). The
  test `TestMatchCodexThroughItsSandbox` in `internal/guard/match_test.go`
  holds the observed layout. Codex starts its own helpers in groups of their
  own too.
- **Codex 0.163.0 sets `CODEX_TOOL_CALL_ID`** in each command's environment
  to the call's `tool_use_id`; 0.153.0 and 0.162.0 do not. It survives the
  exec of a shell or a `#!` script, so a script that execs another program
  still names its call.
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
  agents (`toolu_…` on Claude Code; the model's call ID on Codex, whose hook
  input also carries a `turn_id`).
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
- **Claude Code's command** (the 2.1.301 bundle): the command follows `eval`
  bare when it is one word of `A-Za-z0-9_./:=@+,-`, else single-quoted with
  each `'` as `'"'"'`. The argv is `[shell, "-c", script]`, or
  `[shell, "-c", "-l", script]` (a login shell) when it has no shell
  snapshot. A redirect to `nul`, in any case, is rewritten to `/dev/null`,
  except in a command holding `<`, `$` or a backquote. A `run_in_background`
  call returns at once with its background task ID and its `PostToolUse` is
  built from that return, so its post hook fires while the command runs on:
  inferred from the bundle, not observed with the guard installed.
- **Claude Code's other command paths** (the same bundle):
  `CLAUDE_CODE_SHELL_PREFIX` runs each command through a prefix program, and
  `CLAUDE_CODE_TOOL_MEMORY_LIMIT`, or a server-side flag that is off by
  default, puts `Bash` commands in a sibling cgroup, `claude-code-bash`,
  limited to the machine's memory less the larger of 2 GiB and 15 %.
- **Codex's command** (codex-rs 0.162.0): `[shell, "-lc", command]`, or
  `-c` without a login shell (`core/src/shell.rs:27`; unified exec allows the
  login shell by default, `unified_exec.rs:113`); a brokered zsh runs `-fc` or
  `-lfc`. With a shell snapshot it runs `[shell, "-c", "…; exec '<shell>' -c
  '<command>'"]`. Codex runs hooks through `$SHELL -lc`, which may be fish.
- **Codex on Linux runs sandboxed commands through bubblewrap by default**
  (`codex-rs/linux-sandbox/README.md`), in the chain observed above. WSL2
  takes the same path. Claude Code has its own optional sandbox (bubblewrap
  on Linux).
- **Codex's interactive unified exec can deliver a command's `PostToolUse` on
  a later `write_stdin` poll**, the one that sees it exit
  (`core/src/tools/context.rs`), not when the command first returns.
- **In a memory-limited cgroup the machine-wide figures miss the shortage.**
  A container's `--memory`, a unit's `MemoryMax=` or WSL's distribution
  cgroup set `memory.max` on a cgroup v2, and the kernel's OOM killer acts
  there, while `/proc/meminfo` and `/proc/pressure/memory` stay machine-wide
  (a container limited to 256 MiB read its whole VM's `MemTotal`).
  systemd-oomd reads such a cgroup's own `memory.current`, `memory.stat`,
  `memory.pressure` and `memory.swap.*`.
- **WSL2's kernel has pressure stall information.** Microsoft's `config-wsl`
  sets `CONFIG_PSI=y` with `CONFIG_PSI_DEFAULT_DISABLED` unset, so
  `/proc/pressure/memory` exists.
- **macOS memory readings without C:** `sysctl kern.memorystatus_vm_pressure_level`
  (1 normal, 2 warning, 4 critical) through `golang.org/x/sys/unix`.
  `kern.memorystatus_level` is not a usable "available" figure: it counts
  active and inactive pages, so a runaway command's own memory counts as
  available. XNU asks programs to trim caches at warning and Jetsam kills at
  critical; warning is common in ordinary use (a machine with 42 % of its
  memory free read warning on every sample over nine seconds).
- **A process's macOS footprint without cgo.** libSystem's `proc_pid_rusage`
  with `RUSAGE_INFO_V0` fills `rusage_info_v0`, whose `ri_phys_footprint` at
  offset 72 is the figure Activity Monitor shows. Resident memory from `ps`
  is wrong both ways: it misses compressed memory and counts shared pages.
  Raw syscalls are unsupported on macOS, so `platform.Footprint`
  (`internal/platform/libc_darwin.go`) calls the function the way
  `golang.org/x/sys` calls libSystem, through the runtime's `syscall.syscall6`
  and a trampoline in `internal/platform/libc_darwin.s`, beside the `getattrlist` and
  `fgetattrlist` calls of the ACL checks. cgo would break `CGO_ENABLED=0`
  release builds and raise the binary's minimum macOS to the build machine's,
  27.0 against 12.0.
- **Codex hook trust** is written by
  `home/private_dot_codex/modify_private_config.toml.tmpl` for every hook in
  the Workbench hooks template, including `PreToolUse` and `PostToolUse`.

Not yet observed (section 10 step 1):
- both agents on WSL2
- the interactive sessions of both agents with the guard installed
- Claude Code's own sandbox
- when a `run_in_background` call's post hook fires; its command was tracked
  and its note dropped with the guard installed (Claude Code 2.1.301 on
  Linux).

## 3. Parts

The three parts are hidden subcommands of the one `workbench` CLI, in the
`internal/guard/` package, beside `workbench guard logs`, which shows the
guard log (section 4.1); `internal/cli/` owns their flags. The guard reuses
the owners the costs hook already uses: `operation.TryLock` for the
one-watcher lock (`operation.TryLockFile` and `operation.InheritLock` when a
watcher passes it on, section 6.5), `operation.StartDetached` to start the
watcher (from the command the hook ran as, `operation.Invoked`, resolving its
links, and it opens the output file itself),
`operation.AppendLog` for the capped log (and `operation.LogReader` to follow
it), `operation.ReplacePrivateFile` for notes and kill records, and the
operation layer's private-directory checks for its folder. Only the watch
loop, the hook bodies and the platform readers are new.

The watcher is started on demand by the pre hook; there is no login item or
service, and it exits after 10 idle minutes (section 6.5).

| Part | Runs | Job |
|---|---|---|
| `workbench guard pre` | `PreToolUse` hook on `Bash` | Leaves a note for the call; makes sure the watcher is running. Prints nothing. |
| `workbench guard watch` | At most one per user, started by `pre`, detached | Finds each noted command, watches machine memory, stops the largest agent command near out-of-memory, records why. |
| `workbench guard post` | `PostToolUse` (and `PostToolUseFailure` on Claude Code) on `Bash` | Gives the agent the reason if the watcher stopped this call; otherwise prints nothing. Drops the call's note, or marks a background call's posted. |
| `workbench guard logs` | A person, by hand | Shows the guard log (section 4.1). |

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
  watch.lock                          held by the running watcher, passed
                                      on at a handover (section 6.5)
  watch.log                           JSON Lines (section 4.1), cut in place
                                      to its newest half past 1 MiB
  watch.err, watch.err.1              the watcher's stdout and stderr, never
                                      cut; a watcher that starts while
                                      watch.err is past 1 MiB renames it to
                                      watch.err.1
  guard.sock                          the watcher's socket for
                                      `workbench guard memory` (section 6.5)
  notes/<tool_use_id>.json            {agent pid, agent start, kind,
                                       session_id, tool_use_id, command,
                                       noted_start, noted_at, dry_run,
                                       posted, cwd, folder, subagent,
                                       project}
  killed/<tool_use_id>.json           {reason, ...}  written by the watcher
```

- **Note fields.** `cwd` is the hook input's `cwd` (Claude Code and Codex)
  and `folder` the target of the command's leading `cd PATH &&` or
  `cd PATH;`, both as real paths so they compare with the folder the kernel
  reports for a process, and either left out when unknown. `subagent` is
  Claude Code's `agent_type` when a subagent made the call, left out
  otherwise and for Codex. `project` labels where the command ran: the
  folder name after a `.worktrees/` or `worktrees/` component of the `cd`
  folder or else `cwd`, else the name of the nearest folder at or above it
  that holds `.git`, else its last folder name; nothing acts on it.
  `session_id`, `noted_at`, `subagent` and `project` are for a person
  reading the note and the log.
- **File names.** A `tool_use_id` becomes a file name only if it matches
  `[A-Za-z0-9_-]{1,128}`. Any other call is not guarded.
- **Writes** go through `operation.ReplacePrivateFile`: a `.write-<id>`
  temporary in the same folder, fsync, rename into place, then a fsync of the
  folder, so a reader never sees half a file. A `.write-*` file over a minute
  old, left by a hook or watcher killed mid-write, is deleted from `notes/`
  and `killed/`.

**Finding the agent.** It's the hook's nearest ancestor that is not a shell
(`sh`, `bash`, `zsh`, `dash`, or `fish`, since Codex runs hooks through
`$SHELL`), identified by PID **and** start time, so a later process reusing
the PID is never taken for it. An agent may be PID 1, as in a container
started without an init; a PID 1 named `launchd`, `systemd` or `init`, or a
container's minimal init (`docker-init`, which Docker's `--init` runs and is
tini, `tini`, `dumb-init`, `catatonit`, or `podman-init`, the name Podman's
`--init` runs catatonit under), is never taken for an agent.

**Start times** are the kernel's own, never the wall clock read later:
- **Linux:** clock ticks since boot (`/proc/<pid>/stat` field 22), which a
  wall-clock step (WSL2's resync after the host sleeps) doesn't move.
- **macOS:** `kinfo_proc`'s `p_starttime`, the wall clock when the process
  began, recorded the same way for every process. Only a clock step between
  the pre hook and the command's start can shift one against the other.

`noted_start` is the pre hook process's own start time, in these units.
`noted_at` (wall clock) is only for a person reading the note.

### 4.1 Log

`watch.log` is JSON Lines written by Go's `log/slog` `JSONHandler`, one
record per event, appended through `operation.AppendLog`: the handler makes
one `Write` per record, and each becomes one appended line under the log's
1 MiB cap. Each record has `time` (RFC 3339 with the machine's offset),
`level` (`INFO`, `WARN`, `ERROR`), `msg` (a sentence for a person), `event`,
`part` (`pre`, `post` or `watch`) and `pid` (the process that wrote it).
Records about a call add `call` (its `tool_use_id`), `agent` (`claude` or
`codex`), the note's `cwd`, `project` and `subagent` (each left out when
empty, section 4), `command` (clipped as in the reason, section 6.4),
`root_pid` and `size` in bytes; `ended` and `stopping` add `took` (seconds
since the note, a float) and `peak` (the largest size measured, section
6.3), `unmatched` adds `took` only, since its command was never measured,
and `stopping` adds `by` (`guard`, or `you` for a stop from `workbench guard
memory`); a stop adds `children`, `reason` (the text the agent is told)
and `memory` (the reading: macOS's `level`, or Linux's `available`, `total`
and `full_avg10`, each with `installed`, `swap_free` and `swap_total`, in
bytes), and `dry_run` for a dry run; low memory under the floor adds `size`
(what the agent commands hold), `floor` and `memory`; a give-up adds
`still_running` (the PIDs left); a failure adds `error`, and `process` (a
PID) when one process could not be read.

| `event` | When |
|---|---|
| `started` | A watcher starts, with `release`, the release it runs. |
| `idle` | A watcher exits after 10 idle minutes. |
| `exited` | A watcher exits because its lock file changed, or on `SIGINT`, `SIGTERM` or `SIGHUP`. |
| `handover` | A watcher hands over to the installed release's watcher (section 6.5), with `old_release`, `new_release` and `new_pid`. |
| `tracking` | A noted command is found (section 6.1). |
| `ended` | A tracked command or its agent ends. |
| `unmatched` | A note goes away unmatched after its agent ran a child (section 6.1). |
| `abandoned` | A note that never got its post hook is dropped (section 6.1). |
| `memory` | Memory is low but the agent commands are under the floor (section 6.3), or recovered before a stop. |
| `stopping` | A command is stopped, with its reason; with `dry_run`, it would be. |
| `forced` | A stopped command is sent `SIGKILL`. |
| `gave-up` | A stopped command still runs 10 seconds on (section 6.4). |
| `refused` | A stop signalled nothing. |
| `error` | Something could not be read or written, or a hook panicked. |
| `cut` | The log's first line after a cut in place: `kept` lists the runs of bytes the cuts kept, each `[offset, length]` in bytes ever written to the log, and `next` is the offset of the byte after them. `workbench guard logs` never shows it. |
| `output` | Only in `watch.err`: a watcher starts writing its output there. `workbench guard logs` never shows it. |
| `raw` | Only from `workbench guard logs`: a line that is not a record. |

The watcher's own stdout and stderr (a crash's trace) go to `watch.err`,
not to the log: a write there takes no lock, so a cut of the log could lose
it, and a supervisor keeps a service's own output apart from its structured
log the same way (launchd's `StandardErrorPath`, systemd's `StandardError=`).
Nothing cuts `watch.err`: a watcher that holds the lock and finds it past
1 MiB renames it to `watch.err.1`, replacing the older one, and points its
stdout and stderr at a new `watch.err` (`operation.RotateOutput`); a rename
moves no byte, so a process still writing to the old file writes on into
`watch.err.1`. Each watcher then writes an `output` record there, the time
it started. `workbench guard logs` reads `watch.err.1`, then `watch.err`,
and shows each line that is not a record as a `raw` event, the line in its
`msg` and no time, placed among the log's records at its watcher's start (lines from
before any `output` record come first); a line of the log that is not a
record, which earlier builds wrote, is placed after the record before it.
For `--json` such a line becomes a record with `level` `ERROR`, `event`
`raw`, the line as `msg` and, from `watch.err`, `started`, its watcher's
start, encoded by the same handler, which leaves out `time` because the
line's own time is unknown. It prints one line per event, as log viewers
do: the time in the machine's zone (`Oct 17 14:41:05`), the level, the event
and then `key=value` fields in a fixed order with `cmd` last and the call's
first 8 ID characters as `id`, a value in Go quotes when it holds a space,
`"`, `=`, `\` or a control character (the [platform and guard view
design](2026-10-17-platform-and-guard-view-design.md), section 4). At a
terminal INFO is faint, WARN yellow and ERROR red. Every finished command
shows (`ended`); `tracking` is folded away, and with `--follow` a command
still running 5 seconds after it was found gets one `running` line. There is
no `--verbose`. The stop's `reason` stays in `--json` and the live view's
details. `--since` takes a Go duration before now (`10m`,
`2h`), as `docker logs --since` does, `today` and `yesterday` as
`journalctl --since` does, a local date, date and time or time of today
(`2026-10-16`, `2026-10-16 21:00`, `21:00`), or RFC 3339. A line of
`watch.err` was written while its watcher ran, so `--since` keeps it unless
the next watcher started before then. A raw line of the log was written
after the record before it, so `--since` keeps it when it keeps that record;
raw lines before the log's first record are kept when that record is.
`--follow` keeps printing new events: it waits for files that do not exist
yet and keeps its place in each by byte offset, never by a line's content:
the offset counts every byte ever written to the file, and the `cut` record
maps the bytes a cut kept to their offsets, so after a cut, one that keeps
the newest half or a migration's that drops lines from the middle, it reads
on from the first kept byte at or after its place, as a journald cursor
keeps a reader's place; `tail -F` instead rereads a file that shrank from
its start and repeats lines. It keeps each file open, so after a rotation it
reads the rest of the old `watch.err` before the new one. A file that is
replaced, or shrinks with no `cut` record, is read from its start. `--json`
prints the matching records unchanged, one per line.

Migration `0004-old-guard-log` removes the free-text lines earlier builds
wrote from the log (migrations design, section 6.4).

## 5. Pre hook

1. Read the hook input. Ignore anything that is not a `Bash` call with a valid
   `tool_use_id`, and stop if `WORKBENCH_GUARD=off` is set in the agent's
   environment. The note's kind is Codex when the input carries a `turn_id`,
   Claude Code when it has none and the `tool_use_id` starts `toolu_`; any
   other call is not guarded.
2. Find the agent (section 4) and write `notes/<tool_use_id>.json`.
3. Try `watch.lock` without waiting. If it is free, release it and start
   `workbench guard watch` with `operation.StartDetached`: a new session in
   `/`, with stdin on the null device and output appended to `watch.err`.
   It starts the file the hook's own command (`~/.local/bin/workbench`, or
   the absolute path the hook names) resolves to, with that command as
   `argv[0]`, so the watcher knows the installed command to follow (section
   6.5) and the file it runs. Nothing waits for it.
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

For a Codex agent and its direct children it also reads `CODEX_TOOL_CALL_ID`
from the environment each started with (macOS: the strings after the
arguments in `kern.procargs2`; Linux: `/proc/<pid>/environ`). Nothing else of
any environment is kept.

A child becomes a **tracked command** when the process running the command
has exactly the shape its agent uses for a shell command holding the text of
one of that agent's notes. That process is the child itself or, when the
child is a sandbox launcher (`codex-linux-sandbox`, `bwrap`), the first
process below the chain of launchers. Nothing deeper counts: a program the
command starts itself, such as npm's own `sh -c jest`, never stands for a
note.

- **Claude Code:** a shell run as `<shell> -c <script>` or
  `<shell> -c -l <script>` whose script holds `eval <note>` followed by a
  space or the script's end, with the note quoted the way Claude Code quotes
  it: bare when it is one word of `A-Za-z0-9_./:=@+,-`, else single-quoted
  with each `'` as `'"'"'`. So a longer command that only begins with the
  note's text (`makefile` for `make`) is never taken for it. The note is also
  tried with Claude Code's own rewrite of a redirect to `nul` (any case) into
  `/dev/null`, which it skips for a command holding `<`, `$` or a backquote.
- **Codex, by call ID:** a child of the agent whose `CODEX_TOOL_CALL_ID`
  differs from the agent's own (a Codex agent running inside another Codex
  command inherits one) is that call's command and no other's: it matches
  the note with that `tool_use_id` alone, whatever its command line, as
  Codex 0.163.0 and later set it. Without one (0.162.0 and earlier) the
  shapes below decide, and a script that execs another program, whose
  command line then does not show the note's text, ends unmatched: logged,
  never stopped wrongly.
- **Codex:** a shell run with `-c`, `-lc`, `-fc` or `-lfc` whose script
  equals the note. On Linux it may sit under the sandbox chain (section 2).
- **Codex, single command:** when the note is one simple command of plain
  words (letters, digits, `_./:=+,@%-`, and single- or double-quoted words
  without `$`, backquote or backslash), a process whose argv equals those
  words and that leads its own group (`pgid == pid`) or, below a sandbox
  launcher, is in its launcher's group. Any other note matches only the shell
  form, so a command using expansion, redirection or several commands is
  matched while its shell runs it and is otherwise unguarded. Codex starts
  its own helpers in groups of their own too (section 2), and a helper
  carries no call ID of its own, so a helper started after a waiting note,
  with exactly that note's words as its argv (`git rev-parse HEAD`, say),
  would be taken for it. Once an agent has had a child carrying a call ID of
  its own, it is known to set them, and its notes match only by call ID, so
  the gap closes from Codex 0.163.0 (the version `versions.toml` pins); under
  0.162.0 and earlier it stays.
- **Timing:** the child and that process must have started at or after the
  note's `noted_start`, less one second, comparing kernel start times
  (section 4).
- **Folder:** among the children that claim a note, the watcher prefers the
  one whose process running the command has as its working folder the
  note's `folder` (its leading `cd` target, which that shell has entered),
  then one in the note's `cwd`, then one whose folder is unknown or neither,
  the oldest within a rank. It prefers and never rejects, so a folder that
  cannot be read (Linux `/proc/<pid>/cwd`; macOS `proc_pidinfo` with
  `PROC_PIDVNODEPATHINFO`) or a `cd` through a link, `~` or a variable leaves
  the choice as it was. Two identical commands started together from two
  worktrees of one repository so pair each with its own folder, which
  command text alone could not tell apart.
- **Duplicates:** when two notes carry the same text, the earlier note
  chooses first, and in a rank goes to the earlier-started child.

A child that still carries its parent's own command line has forked but not
yet exec'd, so it is checked again on the next tick. A child whose exec'd
command line matches no note (MCP servers, hooks, git or search processes the
harness runs, helpers such as `caffeinate`) is never tracked. A shape the
guard does not recognise therefore leaves the command unguarded and logs it,
and tracks a harness process only when it has exactly a note's shape (the
Codex helper above). The log line comes when the note goes
unmatched after a child of its agent that started later than the note, and
is no tracked command, was seen alive at every tick for at least a second
(in time, not ticks, which quicken to 100 ms under pressure). A child that
lives less, such as a command that starts and ends between two ticks or a
hook's shell, gives no line. A child of the agent that lives past a second
and is not a tracked command still gives one: a helper the agent keeps
running, or the status line when a loaded machine stretches its run past a
second.

**When a note is dropped:**
- its post hook runs, for a call in the foreground
- for a call sent to the background (`run_in_background`), whose post hook
  marks the note `posted` while the command goes on: once its tracked
  command has ended, or, when no command matched it, one second after the
  watcher first reads it posted and once it has been looked for in two
  rounds (its command started before the post hook, so no later process is
  its command)
- it is abandoned: a call that was denied or cut off gets no post hook, so a
  note that never got one is dropped, with an `abandoned` log record,
  once a note its agent wrote later has gone and 10 minutes (Claude Code's
  longest foreground `Bash` timeout) have passed since the watcher first read
  it, if it never matched or its command has ended
- its agent exits.

Never on a shorter timer before the post hook, because a call may wait a long
time for permission between its pre hook and its start, while a parallel
call of the same agent finishes.

**A call runs once.** Once a call's tracked command has ended, the watcher
remembers the call until its note is dropped and never matches that note
again. Otherwise a denied call's note, which keeps the earliest start, would
claim every later command with the same text until it is abandoned, and the
reason would land under the dead call. It still claims the first
such command once (the earlier note takes the earlier child), which the log
shows as `tracking <denied call's id>`. That call's own note is then dropped
by its post hook without a line: the command it ran is tracked, and a
tracked command never marks a note as one that could have run.

### 6.2 When to act

The watcher reads machine memory each tick and decides between nothing, a
polite stop (`SIGTERM`) and a forced stop (`SIGKILL`):

| | Polite stop | Forced stop |
|---|---|---|
| Linux, WSL2 | available memory ≤ 10 % **and** free swap ≤ 10 %, or memory pressure `full avg10` ≥ 50 % **with** available memory ≤ 20 % for 20 seconds | available ≤ 5 % **and** free swap ≤ 5 % |
| macOS | pressure level critical (4) | critical **with** free swap ≤ 10 %, or critical for 2 seconds |

**Linux:**
- Available memory is earlyoom's figure: `MemAvailable` as a share of
  `MemAvailable + AnonPages`. With no swap, the swap condition counts as met.
- Pressure catches thrashing before swap is full, which the memory-and-swap
  rule misses. Pressure counts only alongside low memory, because pressure
  stays high for seconds after the memory is freed, and only once the
  condition has held for 20 seconds, as systemd-oomd's
  `DefaultMemoryPressureDurationSec` does on Fedora. The memory-and-swap
  lines act at once.
- **In a memory-limited cgroup.** When the cgroup v2 with the lowest
  `memory.max` at or above the watcher's own (from `/proc/self/cgroup`)
  limits it below the machine's memory, as a container's `--memory` or a
  unit's `MemoryMax=` does, the watcher reads that cgroup too. Available is
  the limit less what `memory.current` holds beyond `memory.stat`'s
  `inactive_file`, measured against that plus `anon`; pressure is the
  cgroup's `memory.pressure`; swap is `memory.swap.max` and
  `memory.swap.current` when that limit is below the machine's swap. It acts
  on whichever reading, the cgroup's or the machine's, calls for more; for as
  much, on the one that acts at once over one that must hold 20 seconds (as
  systemd-oomd holds only its pressure rule), then on the one with less
  available. Installed memory then means the limit.
  cgroup v1 is not read.

**macOS:**
- Uses the kernel's own pressure level. Warning (2) never calls for a stop:
  it only asks programs to trim caches and is common in ordinary use.
  Critical (4), where Jetsam starts killing, calls for a polite stop.
- A forced stop comes when critical arrives with at most a tenth of swap free
  or has lasted 2 seconds. macOS grows swap as needed, so free swap alone
  comes too late to call for a stop.

**The thresholds are constants.** In a 1 GiB cgroup on Linux the 10 %
polite line stopped a growing command before the kernel's OOM killer acted
(`docs/acceptance.md`); the other lines and holds and the macOS levels are
initial values until section 10 step 4 drives them.

**Tick length:**
- **Linux:** follows earlyoom's adaptive sleep. The room left above the
  polite-stop line is divided by fill rates of 6000 MiB/s for memory and
  800 MiB/s for swap, and the result is clamped to between 100 ms and 1 s.
- **macOS:** ticks every 100 ms at critical, where a stop may come, and
  otherwise every 1 s. Warning never leads to a stop, so it keeps the 1 s
  tick for the whole session; critical is seen within a second.

### 6.3 Which command

- **Sizing:** each tracked command's size is the total over its process tree:
  - macOS: `ri_phys_footprint`
  - Linux: `Pss` from `/proc/<pid>/smaps_rollup`, which counts shared pages
    once.
- **Measured every tick:** each tick the watcher also sums each tracked
  command's tree, the processes a stop would signal, and keeps per command
  the last 120 sums (two minutes at the normal tick), its peak and the
  largest peak of any command. macOS sums the physical footprint, as stops
  do; Linux sums `/proc/<pid>/statm`'s resident pages, a counter, and reads
  `smaps_rollup`'s Pss, which the kernel builds by walking every mapping,
  only for the sizing above when memory calls for a stop. The sums give the
  `peak` of the `ended` and `stopping` records and the live view's memory;
  a handover carries them to the new watcher.
- **Choosing:** the watcher stops the largest tracked command across every
  session; ties go to the older command.
- **Who's eligible:** only agent commands are candidates, never other
  programs.
- **Memory floor, polite stops only:** at a polite stop, when the tracked
  commands not passed over hold less together than a tenth of installed
  memory, at most 1 GiB (`min(installed / 10, 1 GiB)`), they are all left
  running, and the watcher logs that once until a tick reads memory as fine
  again. Commands that small can't end a shortage they didn't cause, so
  stopping one would only lead to the next being stopped after each
  cooldown. The 1 GiB cap keeps a large machine able to stop a
  multi-gigabyte command. A forced stop has no floor. A process whose size
  can't be read counts as 0 (section 9).
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
   (with everything under it), or any process outside the tracked tree. The
   watcher and the agents are protected by PID and start time, so a later
   process that reuses a protected PID is not protected. A
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
   - **Escalate to `SIGKILL`** when memory calls for a forced stop (as
     earlyoom does; on macOS that includes critical lasting 2 seconds) or
     after 5 seconds. The 5-second rule is this design's own: an agent is
     waiting on the command, and a command that ignores `SIGTERM` must not
     hold the machine.
   - **Escalate by PID:** take a fresh snapshot and send `SIGKILL` to every
     process of the stopped command still alive with its snapshot start time,
     and to anything started since under one still running. A shell that died
     of `SIGTERM` was the group leader; once it is reaped, a child that
     ignored `SIGTERM` lives on under PID 1, outside any group the watcher may
     still signal. A `SIGKILL` round that can't read the processes or the
     notes is tried again at the next poll.
   - **Give up** after 10 seconds: log the processes still running, end the
     cooldown, and never choose that call again, so one process that can't
     be stopped (in uninterruptible I/O, for example) never keeps the watcher
     from acting, nor holds it ten seconds every round.
   - **On Linux,** open a pidfd first and call `process_mrelease` after the
     signal, as earlyoom does, so the memory frees at once. If the pidfd can't
     be opened for any reason but the process being gone (out of memory or
     descriptors, say), re-check its start time and signal by PID, as earlyoom
     does, without `process_mrelease`. A group is signalled through its
     leader's pidfd with `PIDFD_SIGNAL_PROCESS_GROUP` (Linux 6.9), else with
     `kill(-pgid)`; a group whose leader no longer leads it is not signalled.
6. **Record:** write `killed/<tool_use_id>.json` with the reason, and log a
   `stopping` record. It is written before step 5's first signal, because the agent's post
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
  - An agent, or a tracked command's root, counts as gone only once it has
    been absent from two rounds in a row: a Linux snapshot leaves out a
    process whose `/proc` entry it could not read.
  - A note that can't be read is kept, and logged once; when the notes folder
    can't be read, the watcher does nothing else that round.
  - Kill records older than a day are deleted, and `.write-*` temporaries
    over a minute old in `notes/` and `killed/`.
- **A call runs once:** once a call's command has ended, its note never
  matches again, even if the note stays (a denied call's stays until it is
  abandoned, section 6.1). The watcher keeps this in memory only, so after a
  watcher restart a
  note that is still present for a call that already ran (a denied call's, or
  for its first second under the new watcher a background call's) may match
  one more command with the same text. This is known and
  bounded: the note is deleted when it is abandoned or its agent exits, and
  the match needs a
  command of that exact shape started after the note. A handover carries
  these calls to the new watcher, so it is not a restart in this sense.
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
- **Update (handover):** a watcher follows the installed command it was
  started as (its `argv[0]`), which a release activation points at the new
  release. After each tick it compares the file that command resolves to with
  the file it runs, read once at its start, by device and inode, not by path
  (`os.Executable` may give the link or its target, depending on the system).
  When they differ, it starts that file's `guard watch` with
  `WORKBENCH_GUARD_HANDOVER` set to its PID and, as descriptors 3 to 5, its
  locked `watch.lock` and two pipes, and, as descriptor 6 with
  `WORKBENCH_GUARD_LISTENER=6`, its listening `guard.sock`, as systemd
  passes sockets from descriptor 3 with `LISTEN_PID` naming the receiver. An `flock` lock belongs
  to the open file description, which a passed descriptor shares, survives
  `execve` and holds until the last descriptor on it closes (Linux and BSD
  `flock(2)`), so the lock is never free during the handover and no pre hook
  starts a third watcher. The new watcher checks that descriptor 3 is
  `watch.lock` and that no other description holds its lock
  (`operation.InheritLock`), says it is ready on descriptor 4 with its
  release, and waits on descriptor 5. The old one keeps watching meanwhile.
  Once the new one is ready, and at a tick that read memory calling for
  nothing, with no soft or critical hold running, no stop cooling down and
  no person's stop under way, the old one logs `handover`, writes on
  descriptor 5 what lives only in its memory: each tracked command's root
  (PID and start time), note and memory readings, who stopped it (`guard`
  or `you`) when it was signalled, the calls whose command ended or that are
  passed over, the commands that finished within the last minute and the
  largest peak. It then closes its copy of the socket without removing the
  file, and exits without another tick; the new one starts
  ticking only on that line, so two watchers never act at once. A stop under
  way finishes first, since it runs within a tick. The new watcher keeps the
  finished commands until a minute after each ended and the largest peak,
  and takes on each carried command with its note, the one on disk or else
  the carried one, since a post hook may have removed it while the watch
  passed. A command whose root still runs as its agent's child is tracked
  again, with its memory and whether it is passed over, and logged
  `tracking`; one whose root died during the handover is tracked without a
  log, so `forget` ends it once, as any command, after two rounds without
  it: it logs `ended` once, keeps it among the finished commands with who
  stopped it, and never matches its note again. A who-stopped mark is kept
  only with its command. Every other note is matched as any watcher matches
  it: start times are the kernel's, so a command that started before it
  still matches its note. What is not carried (refusal counts, abandonment
  clocks) restarts, which only delays an abandonment. This is nginx's executable upgrade (the old master
  starts the new binary and keeps running until it is told the new one
  works), with the readiness wait of Cloudflare's tableflip; replacing the
  process with `execve` would keep the lock but cannot be undone when the
  new file turns out not to run.
  - A command that cannot be read (a dangling link), a file that does not
    start (not executable), or a watcher not ready within 10 seconds (one of
    a release without handover finds the lock held and exits) leaves the old
    watcher running. It logs this once, closes the pipe, which tells a late
    new watcher to exit, and kills that watcher, its own unwaited child, so
    no other process can have its PID. It tries again only when the command
    resolves to another file, so two releases never take turns.
  - A watcher started by a build without handover runs that build until it
    is idle; `SIGTERM` stops it after any stop under way.
- **The socket:** the watcher serves `guard.sock`, in this 0700 folder, to
  `workbench guard memory`. It creates it after taking the lock, removing a
  stale file first, takes it over at a handover (above), and removes it when
  it exits, except when its lock file changed, since another watcher may
  serve it then. At accept it reads the peer's user and process ID
  (`platform.Peer`: `SO_PEERCRED` on Linux, `LOCAL_PEERCRED` and
  `LOCAL_PEERPID` on macOS) and closes a connection from another user;
  pathname sockets honor their folder's permissions, but unix(7) warns
  portable programs not to rely on that. Messages are JSON Lines: `hello`
  with the versions the view speaks, answered with the highest both share
  and the release, or `error` and close; a `snapshot` after each tick; a
  `stop` naming call IDs, polite or forced, answered by `stopped` with each
  ID's result (`unknown`, `already finished`, `not allowed`), or `error`
  `busy` when eight stops already wait. A `stop` from a process under an
  agent that has a note is `not allowed`, so an agent's command cannot stop
  another's work. Only the tick goroutine owns the watcher's state: the
  socket's goroutines hand it stops over a channel and get the answer on a
  channel of their own, and each snapshot goes to every client without
  blocking; a client whose four-message buffer is full, or whose write takes
  over 5 seconds, is disconnected. A person's stop goes the path of section
  6.4 (`force` sends `SIGKILL` at once), is followed across ticks with the
  same 5-second escalation, is logged `stopping` with `by` `you`, tells the
  agent `Stopped by you (workbench guard memory).`, and starts no cooldown;
  under dry-run it is only logged.
- **Crash or reboot:**
  - Commands keep running, unguarded, until the next command's pre hook starts
    a new watcher.
  - Nothing survives a reboot.
  - On start, a watcher deletes notes whose agent (PID plus start time) is
    gone, once absent from two rounds.
- **Bounded:**
  - The watcher sets a Go memory limit of 32 MiB, has no terminal, works from
    `/` so it never holds a session's folder, and logs to its capped file
    only.
  - Its life is bounded by the agents it serves plus 10 minutes.
- **Kill switch:** `WORKBENCH_GUARD=off` in the agent's environment disables
  `pre`, and `WORKBENCH_GUARD=dry-run` records it in the note.

## 7. Post hook

1. Read the hook input; with an invalid `tool_use_id`, exit 0.
2. Delete `notes/<tool_use_id>.json`, unless the call was sent to the
   background (`tool_input.run_in_background`): then rewrite the note with
   `posted: true`, and the watcher drops it (section 6.1).
3. If `killed/<tool_use_id>.json` exists, print `{"hookSpecificOutput":
   {"hookEventName": <event>, "additionalContext": <reason>}}`, delete the
   record, and exit 0. Otherwise exit 0 with no output.

**Background commands:** a command the agent sent to the background
(`run_in_background`) reaches its post hook while it runs on (section 2), so
before any stop. Its note stays until its command ends, so the watcher matches
it even when the post hook comes first. A stop writes its kill record as for
any call: one that lands before the post hook reaches the agent; one that
lands after reaches only the guard log, and its record is deleted after a
day.

## 8. Wiring

- **Claude Code:** `home/.chezmoidata/claude.json` `enforced.hooks` gains
  `PreToolUse`, `PostToolUse` and `PostToolUseFailure` entries matching `Bash`.
  They name `workbench guard pre || true` and `workbench guard post || true`,
  each with a 5-second timeout, and the settings merge
  (`modify_private_settings.json`) puts the installed `workbench` in front:
  `~/.local/bin/workbench` for the default bin folder, else its absolute
  path, single-quoted.
- **Codex:** `home/.chezmoitemplates/codex-hooks.json.tmpl` gains `PreToolUse`
  and `PostToolUse` entries matching `Bash`, running the installed
  `workbench` on its absolute, single-quoted path, as the costs hooks do, with
  the same `|| true`. Their trust hashes come from the existing modify
  template.
- **Both** use the installed `workbench` in the bin folder the apply resolved
  (`WORKBENCH_BIN_DIR`), the one its migrations and costs-ingest scripts run:
  the apply passes the path as the `workbench_bin` answer and the
  `workbench-command` template quotes it. A release activation updates the
  guard with everything else.
- **Release builds stay as they are:** `CGO_ENABLED=0`, one Ubuntu runner, all
  four targets.

## 9. Platform readers

| | macOS | Linux, WSL2 |
|---|---|---|
| Machine | `kern.memorystatus_vm_pressure_level`, `vm.swapusage`, `hw.memsize` | `/proc/meminfo`, `/proc/pressure/memory`; in a limiting cgroup v2 (`/proc/self/cgroup`) its `memory.max`, `memory.current`, `memory.stat`, `memory.pressure`, `memory.swap.max`, `memory.swap.current` |
| Processes | `kern.proc.all`, `kern.procargs2` (with the environment after the arguments, for Codex's call ID) | `/proc/<pid>/stat`, `/proc/<pid>/cmdline`, `/proc/<pid>/environ` (Codex's call ID) |
| Size | libSystem `proc_pid_rusage`, `ri_phys_footprint` (`platform.Footprint`) | `/proc/<pid>/smaps_rollup` `Pss` |
| Agent exit | `kqueue` `EVFILT_PROC` `NOTE_EXIT` | pidfd |

The macOS footprint comes from `platform.Footprint`
(`internal/platform/libc_darwin.go`), which calls libSystem through one
trampoline file, `internal/platform/libc_darwin.s`, with the ACL checks' `getattrlist` and
`fgetattrlist` (section 2):
- an error fails closed: the process counts as size 0 and is logged once,
  unless it only means the process exited, which counts as 0 unlogged
- the `rusage_info_v0` layout is rechecked on each major macOS release.

## 10. Order of work

1. **Observe the shapes the matcher depends on.** Run the throwaway probe
   under each case and record the agent versions:
   - Claude Code and Codex on Linux and WSL2, each with its sandbox on and off
   - both agents' interactive sessions as well as `claude -p` and `codex exec`
   - a `run_in_background` command.

   Adjust section 6.1 to what is seen. Done on Linux with `claude -p` and
   `codex exec`, sandbox on and off, and a background command of each
   (section 2); WSL2, the interactive sessions and Claude Code's sandbox
   remain.
2. **`internal/guard`:** platform readers, the watcher, the pre and post hooks,
   and the CLI.
3. **Wiring** (section 8), with `scripts/render-check.sh` for the affected
   roles.
4. **Check under real pressure** in a disposable VM or container with capped
   memory, on Linux and on macOS. A runaway command must be stopped, while the
   agent, its MCP servers and a second, smaller agent command survive, and the
   agent must show the reason. Settle the section 6.2 thresholds from these
   runs. Done on Linux in a container limited to a 1 GiB cgroup with no swap,
   which settled the 10 % polite line there (`docs/acceptance.md`); macOS,
   WSL2, Linux without a limiting cgroup, the pressure condition, the forced
   line and the swap conditions remain.
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
  - Claude Code's one-word command (`make`) beside a longer look-alike
    (`makefile`), and look-alikes that only begin with a note's text
  - Claude Code's `-c -l` login shell and its background call's shape, with
    an `export` and `setopt` prelude
  - the observed Codex sandbox chain, and two chains where the launcher
    started before the note, or the command did
  - Codex's `-lc` login shell
  - Codex commands by call ID: a `#!` script the shell replaced, another
    call's command with the note's words, and an ID inherited from an outer
    Codex command
  - a Codex command its shell replaced with `exec`, beside a harness `git` in
    the agent's group
  - a program the command starts (`npm test` running `sh -c jest`) and an
    earlier pending `jest` note.

  Only the agent's real command shell is tracked.
- **Targets (6.4):**
  - a group shared with an untracked process
  - an agent that left a note, or the watcher, inside the tree
  - a zombie group leader whose child lives on in its group
  - PID reuse
  - PID 1.

  Nothing outside the tracked tree, no agent and not the watcher is ever a
  target.
- **Escalation (6.4):**
  - a nested agent that left its first note mid-stop
  - a PID reused since the stop
  - the watcher inside the tree
  - a new child under a live stopped process.

  `SIGKILL` reaches only what is left of the stopped command.

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
  which `.wslconfig` caps, or the distribution's cgroup when its `memory.max`
  is lower (section 6.2).
- Commands Claude Code runs through `CLAUDE_CODE_SHELL_PREFIX`: the prefix
  program, not the agent's shell, runs them, so they are unguarded and logged
  unmatched.
- Claude Code's `claude-code-bash` tool cgroup (section 2): the guard reads
  only cgroups at or above its watcher, so where Claude Code turns that cgroup
  on, the kernel may kill a command inside it before the guard acts.
- cgroup v1: its memory files are not read.
