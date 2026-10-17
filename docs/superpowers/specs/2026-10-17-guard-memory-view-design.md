# Guard log format and live memory view design

Status: design, Oct 17, 2026. It changes how `workbench guard logs` prints the
guard log and adds `workbench guard memory`, a live full-screen view of the
commands the guard tracks, from which a person can stop them. It builds on the
[agent command guard design](2026-10-14-agent-command-guard-design.md); every
section number below that names "guard design" points there.

## 1. Goal and boundaries

- `workbench guard logs` prints one log line per event, the way log viewers
  do, with an ID that ties a command's lines together. It has no `--verbose`
  flag: every finished command is shown.
- `workbench guard memory` shows, live, each command the guard tracks: its
  status, run time, memory now and at its peak, project, agent and subagent.
  Arrow keys move, Space tags, Enter opens details, `k` stops, `q` quits.
- Only commands the guard tracks are shown or can be stopped. Any other
  process belongs to htop or Activity Monitor.
- A stop from the view goes through the watcher, the guard's one process that
  signals commands, and is logged and told to the agent like the guard's own
  stops.
- Not in scope: stopping or listing processes the guard does not track,
  signals other than the polite stop and the forced kill, a history browser
  inside the view (the log is the history), and keeping the watcher running
  while nothing is guarded.

## 2. Facts this design rests on

Each was read in the source named.

- **Log line format.** Charm's `charmbracelet/log` prints time, a styled
  level, the message and then `key=value` pairs on one line, and styles only
  a terminal ([README](https://github.com/charmbracelet/log)). logfmt is one
  line of `key=value` pairs that "achieves pretty good readability for both
  human and computer" ([brandur.org/logfmt](https://brandur.org/logfmt)).
  journalctl's default format is one line per entry, like syslog
  ([journalctl(1)](https://www.freedesktop.org/software/systemd/man/latest/journalctl.html)).
- **Color.** "Use color with intention … if everything is a different color,
  then the color means nothing", and disable it off a terminal or when asked
  ([clig.dev, Output](https://clig.dev/#output)).
- **Short IDs.** Docker and git show a short prefix of a long unique ID; the
  full ID stays in machine output.
- **Hook input.** Claude Code gives every hook `cwd`, and tool events from a
  subagent also carry `agent_id` and `agent_type` (for example `Explore`)
  ([hooks reference](https://code.claude.com/docs/en/hooks)). Codex gives
  every hook `cwd`, and PreToolUse adds `turn_id`, `tool_use_id` and
  `tool_input`; "Subagent hooks use the parent session id", and `agent_id`
  appears only on SubagentStart and SubagentStop
  ([Codex hooks](https://learn.chatgpt.com/docs/hooks)).
- **Pairing today.** `match` in `internal/guard/match.go` pairs notes with an
  agent's children by command text, oldest note to oldest child. Claude Code
  subagents run inside the session's own process, so two identical commands
  started together in two worktrees can pair crosswise. Codex 0.163.0 and
  later set `CODEX_TOOL_CALL_ID` on each command, which pairs exactly.
- **Memory today.** The watcher measures a command's tree only when memory
  calls for a stop (`stopLargest` in `internal/guard/watch.go`), with
  `proc_pid_rusage`'s physical footprint on macOS and `smaps_rollup`'s Pss on
  Linux. The kernel builds `smaps_rollup` by walking the process's mappings
  ([proc(5) docs](https://docs.kernel.org/filesystems/proc.html)), so its cost
  grows with the process; `/proc/PID/statm`'s resident count is a counter.
- **Live terminal view.** Bubble Tea, already Workbench's terminal library,
  feeds outside events into a program through a channel and a command that
  waits for the next one ([realtime example](https://github.com/charmbracelet/bubbletea/blob/main/examples/realtime/main.go)).
  Its renderer runs at most 60 frames a second (`defaultFPS` in
  `renderer.go`, v2.0.10) and rewrites only what changed.
- **htop's kill.** `k` or F9 opens a signal menu with SIGTERM selected
  (`defaultPosition = 15` in htop's `SignalsPanel.c`); Enter sends it to the
  selected process, or to every tagged one ([htop(1)](https://www.man7.org/linux/man-pages/man1/htop.1.html)).
- **Polite, then forced.** Kubernetes sends TERM, waits a grace period, then
  KILL ([Pod termination](https://kubernetes.io/docs/concepts/workloads/pods/pod-lifecycle/)).
  The guard already escalates after 5 s (guard design 6.4).
- **Service and client.** The `docker` CLI talks to its daemon over a Unix
  socket, guarded by Unix permissions
  ([Docker security](https://docs.docker.com/engine/security/)); the
  `tailscale` CLI does the same and authenticates each connection by its peer
  credentials ([safesocket](https://pkg.go.dev/tailscale.com@v1.90.6/safesocket)).
  Docker's client and daemon negotiate "the highest version of the API
  supported by both" ([Engine API](https://docs.docker.com/reference/api/engine/)).
- **Socket permissions.** Pathname sockets honor their directory's
  permissions, but "portable programs should not rely on this feature for
  security" ([unix(7)](https://man7.org/linux/man-pages/man7/unix.7.html));
  `SO_PEERCRED` gives the connecting process's credentials. `golang.org/x/sys`
  has `GetsockoptUcred` (Linux `SO_PEERCRED`) and `GetsockoptXucred` (macOS
  `LOCAL_PEERCRED`).
- **Messages.** JSON Lines suits "passing messages between cooperating
  processes" ([jsonlines.org](https://jsonlines.org/)).

## 3. `workbench guard logs`

One line per event: time, level, event, then `key=value` pairs in a fixed
order, `cmd` last because it is the longest.

```
Oct 17 14:41:05 INFO  ended     id=01VSwRPD agent=claude subagent=Explore project=rust-gone-session took=53s peak=2.4GB cmd="pnpm typecheck 2>&1 | tail -15"
Oct 17 14:41:20 WARN  unmatched id=01Hc2wqP agent=claude project=rust-gone-session took=2.0s guarded=no cmd="./scripts/nap.sh"
Oct 17 14:42:31 WARN  stopped   id=01T4J7Ko agent=claude project=rust-wire-code by=guard size=6.2GB reason="memory critical" cmd="cargo test --workspace"
Oct 17 14:43:02 WARN  memory    available=1.1GB total=16GB stopped=none
Oct 17 14:44:40 WARN  stopped   id=01Pz8LmQ agent=claude project=rust-gone-session by=you took=8.0s peak=1.3GB cmd="pnpm vitest run"
Oct 17 14:45:10 ERROR error     msg="cannot read memory" error="proc_pid_rusage 81234: no such process"
```

- **Time** is the machine's zone, `Jan 2 15:04:05`, as journalctl and the
  log's own records sort.
- **Level** is the record's slog level. **Event** is the record's `event`
  (guard design 4.1), with `tracking` folded away: a command shows once, when
  it ends, is stopped or goes unmatched.
- **`id`** is the first 8 characters of the tool call ID after its `toolu_`
  or `call_` prefix. `--json` keeps the full ID.
- **`project`** is where the command ran (section 5.2).
- **`cmd`** is the note's command with a leading `cd PATH &&` or `cd PATH;`
  removed; the path went into `project`.
- **Sizes** are `MB` below 1 GB, else `GB` with one decimal.
- **Colors**, on a terminal without `NO_COLOR` only: INFO dim, WARN yellow,
  ERROR red; keys dim, values plain; `id` blue; `took` of 10 s or more bold;
  `peak` or `size` of 1 GB or more yellow.
- **Order of keys:** `id agent subagent project by took peak size reason
  available total stopped msg error cmd`. A key without a value is left out.
- A long line soft-wraps on screen and stays one line, so `grep` and copying
  keep the whole event.
- `--since`, `--follow` and `--json` stay as they are (guard design 4.1).
  With `--follow`, a command still running 5 s after its `tracking` record
  gets one `INFO running` line with the same keys and no `took` or `peak`.
- A stop's `reason` text, the message the agent is told, stays in `--json`
  and the live view's details.

## 4. `workbench guard memory`

A full-screen Bubble Tea program in the alternate screen.

```
[WorkBench] Guard memory  ● watching · v0.1.26 · memory normal · 10.5 of 16 GB used      2:41:08 PM
3 running · 3 finished in the last minute · largest peak 6.2 GB

  ID        STATUS      AGENT   SUBAGENT  TIME  MEMORY    PEAK  PROJECT            COMMAND
▸ 01VSwRPD  ● run       claude  Explore    44s  2.2 GB  2.3 GB  rust-gone-session  pnpm typecheck 2>&1 | tail -15
  7cE2kR0L  ● run       codex               15s  840 MB  910 MB  market-squawk      cargo check -p feed
  017akess  ✓ done      claude            0.6s       —   18 MB  rust-wire-code     rg -n -i 'digest' …

↑↓ move  space tag  enter details  k stop  q quit
```

- **Rows** are the commands the watcher tracks, newest first, then finished
  ones, dimmed, for 60 s after they end, with their outcome: `✓ done`,
  `? unmatched`, `✗ stopped` (by the guard or by you). A row stopping and
  waiting on its 5 s shows `◌ stopping`.
- **Header**: the watcher's state and release, the memory level (macOS's
  pressure level, normal, warning or critical; on Linux, normal or low by
  the guard's thresholds in guard design 6.2), memory in use, the clock,
  and counts: running, finished in the last minute, and the largest peak
  since the watcher started. In dry-run mode the header says `dry run:
  stops are logged, not sent`.
- **Keys**: ↑ and ↓ move (no `j`/`k`, since `k` stops, as in htop);
  Space tags or untags a running row and moves down; Enter opens details;
  `k` opens the stop menu for the tagged rows, or the selected one; Esc
  closes a menu or details; `q` and ctrl+c quit.
- **Stop menu**: a box over the list titled with the command and its memory
  (or the number of tagged rows), with `Stop (polite, force-killed after 5
  s)` selected and `Force kill now` below it. Enter sends, Esc cancels. The
  answer from the watcher is shown for 3 s on the bottom line, for example
  `stopped 01VSwRPD` or `01VSwRPD already finished`.
- **Details**: the full command, folder, agent, subagent, full ID, status
  and run time, memory now and peak, a one-line graph of the command's
  memory over the last 120 readings, and its process tree with each
  process's memory. `k` stops from here too; Esc goes back.
- **Narrow windows**: COMMAND shrinks first, then SUBAGENT, PROJECT, PEAK
  and AGENT drop in that order. Below 60 columns the view says `Widen the
  window to at least 60 columns`.
- **No color** (`NO_COLOR` or no color support): the same screen in plain
  text; `▸`, `✓ ? ✗ ●` and the tag mark carry the state.
- **Watcher not running**: the screen says so and that the watcher starts
  with the next agent command, keeps the last rows dimmed, and reconnects
  every second.
- **Refusals**, before the screen opens: without a terminal on stdin and
  stdout, `guard memory` exits 3 with `guard memory needs a terminal; use
  workbench guard logs --follow`. Under a coding agent (`CLAUDECODE=1` or
  the Codex variables AGENTS.md lists, the detection `apply` uses) it exits 3
  with `guard memory is for a person at a terminal`.

## 5. Guard changes

### 5.1 Note fields

The pre hook adds to each note (guard design 4):

| Field | From |
| --- | --- |
| `cwd` | the hook input's `cwd` (Claude Code and Codex) |
| `subagent` | Claude Code's `agent_type` when the call comes from a subagent; empty otherwise and for Codex |
| `project` | section 5.2, computed once here |

### 5.2 Project name

From the command's leading `cd PATH` when it has one, else from `cwd`,
resolved against `cwd`: the folder name after a `.worktrees/` or
`worktrees/` component when the path has one, else the name of the nearest
folder at or above it that holds `.git`, else the path's last folder name.
It only labels; nothing acts on it.

### 5.3 Pairing by folder

`claims` in `internal/guard/match.go` also requires, when both are known,
that the process running the command has as its working folder, when it is
paired, either the note's `cwd` or, for a command that starts with `cd PATH`,
that folder. Linux reads
`/proc/PID/cwd`; macOS reads `proc_pidinfo` with `PROC_PIDVNODEPATHINFO`. A
process whose folder cannot be read pairs as today. Codex commands with a
call ID pair by ID as today.

### 5.4 Memory every tick

Each tick the watcher sums each tracked command's tree (guard design 6.3's
tree, the same processes a stop would signal) and keeps per command the last
120 sums and the peak. macOS uses the physical footprint, as stops do. Linux
uses `/proc/PID/statm`'s resident pages for this sum; the stop decision keeps
`smaps_rollup`'s Pss, read only when memory calls for a stop. The `ended`,
`stopped` and `unmatched` records gain `took` (seconds, float), `peak`
(bytes), `cwd`, `project` and `subagent`; `stopped` gains `by` (`guard` or
`you`).

### 5.5 The socket

- **Place.** `guard.sock` in the guard's state folder, which is private
  (0700). The watcher creates it after taking the lock (guard design 6.5),
  removing a stale socket file first, and removes it when it exits.
  A handover (guard design 6.5) passes the listening socket with the lock,
  so a connected view sees one disconnect and reconnects.
- **Who may connect.** The watcher reads each connection's peer
  credentials (`SO_PEERCRED` on Linux, `LOCAL_PEERCRED` and `LOCAL_PEERPID`
  on macOS) and closes it unless the user ID is its own. It may serve a
  snapshot to any such peer; it refuses a `stop` from a peer whose process
  descends from an agent that has a note, so a command an agent runs cannot
  stop another agent's work.
- **Messages.** JSON Lines in both directions. The view first sends
  `{"type":"hello","versions":[1]}`; the watcher answers
  `{"type":"hello","version":1,"release":"v0.1.26","dry_run":false}` with
  the highest version both list, or `{"type":"error","error":"no common
  version"}` and closes; the view then says the watcher runs a different
  release and to run `workbench update`.
- **Snapshot**, after each tick:
  `{"type":"snapshot","at":TIME,"memory":{"level":"normal","used":N,"total":N},"commands":[…]}`,
  each command with `id` (full), `agent`, `subagent`, `project`, `cwd`,
  `cmd`, `state` (`running`, `stopping`, `done`, `unmatched`, `stopped`),
  `by`, `started`, `ended`, `memory`, `peak`, `history` (the last readings),
  and `tree` (`pid`, `name`, `memory` per process) for running ones.
- **Stop**: `{"type":"stop","ids":[…],"force":false}`. The watcher answers
  `{"type":"stopped","results":[{"id":…,"ok":true}|{"id":…,"ok":false,"error":"already finished"}]}`.
  It looks each ID up among its tracked commands, never a process number,
  and stops it the way it stops for memory (guard design 6.4: the same
  targets, start-time checks, pidfd on Linux, 5 s escalation; `force` sends
  SIGKILL at once), telling the agent `Stopped by you (workbench guard
  memory).` In dry-run mode it logs `would stop` and signals nothing.

## 6. Tests

AGENTS.md admits tests only for data loss, credential exposure or
unauthorized execution. Each of these guards against a command being stopped
that nobody with the right to stop it chose:

1. The watcher refuses a `stop` from a peer of another user and from a peer
   under a noted agent.
2. A `stop` naming an unknown or finished ID signals nothing.
3. Two notes with the same command text in two folders pair with the
   processes in their own folders.

## 7. Checks before release

- macOS in a disposable home and Linux in a memory-capped Docker container,
  with `WORKBENCH_GUARD=dry-run` and then real stops of commands the check
  itself started: the view lists, tags, stops politely and by force, shows
  details, survives a watcher handover and a watcher exit.
- Real Claude Code and Codex commands in two worktrees of one repository at
  once: rows carry the right project and subagent, and a stop hits the row
  chosen.
- The view's and the watcher's CPU and memory over a minute with ten tracked
  commands, recorded in `docs/acceptance.md`.

## 8. Docs

- `docs/usage.md`: the new `guard logs` format and the `guard memory`
  command, keys and refusals.
- The guard design: note fields (section 4), log keys (4.1), pairing by
  folder (6.1), memory each tick and the socket (6.5).
- `docs/superpowers/specs/workbench-contracts.md`: the socket and its
  messages.
- `docs/adr/0012-guard-memory-view.md`: why a socket served by the watcher,
  the peer checks and the agent lock, with the sources in section 2.
- `docs/acceptance.md`: what the checks in section 7 observed.
