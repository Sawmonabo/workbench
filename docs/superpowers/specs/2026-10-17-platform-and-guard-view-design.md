# Platform package, guard log format and live memory view design

Status: design, Oct 17, 2026. First, it gathers every operating-system call
and platform fact into one package, `internal/platform`, so no other package
asks which system it runs on. Then it changes how `workbench guard logs`
prints the guard log and adds `workbench guard memory`, a live full-screen
view of the commands the guard tracks, from which a person can stop them,
built on that package. The guard parts build on the
[agent command guard design](2026-10-14-agent-command-guard-design.md); every
section number below that names "guard design" points there.

## 1. Goal and boundaries

- One package, `internal/platform`, owns every operating-system call and
  every fact that differs between macOS and Linux (section 3). Other packages
  call it and never test `runtime.GOOS` or WSL themselves. It lands first,
  on its own, changing no behavior.
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

- **Platform code in Go.** A file whose name ends in `_GOOS` (for example
  `platform_darwin.go`) builds only for that system, with no build line
  ([go/build](https://pkg.go.dev/cmd/go/internal/help@go1.26.4)); the
  standard library splits its own system calls this way. A `runtime.GOOS`
  test is evaluated at run time and every branch must compile everywhere.
- **Detect once.** Tailscale's `version/distro` package detects the Linux
  distribution and WSL once and caches the answer (`lazy.SyncValue`) for every
  caller ([distro](https://pkg.go.dev/tailscale.com@v1.98.3/version/distro)).
- **Where Workbench stands.** Its low-level calls already sit in nine
  `_darwin.go`/`_linux.go` files (`internal/guard/platform_*`,
  `internal/operation/filesystem_*`, `image_metadata_*`, `libc_darwin.go`,
  `askpass_*`), but 20 `runtime.GOOS` tests in 10 files of `machine`,
  `operation` and `costs` (plus `release.Target()`, which names the build
  itself), and six `isWSL()` calls in four files of `machine`, decide
  platform facts where they are used, and `isWSL()` reads
  `/proc/sys/kernel/osrelease` on every call.
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
- **Short IDs.** `docker ps` shows 12 characters of a container's ID and
  `--no-trunc` the whole ID ([docker container ls](https://docs.docker.com/reference/cli/docker/container/ls/));
  git shows a short prefix of an object name with `--short`
  ([git rev-parse](https://git-scm.com/docs/git-rev-parse)).
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
  (`defaultPosition = 15` in htop's [`SignalsPanel.c`](https://github.com/htop-dev/htop/blob/main/SignalsPanel.c)); Enter sends it to the
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

## 3. `internal/platform`

### 3.1 What it owns

- **Every operating-system call**: the system calls, libc calls and
  `/proc` reads that differ between macOS and Linux. The logic around a call
  stays with its owner; only the call moves. For example, the guard keeps
  deciding what to stop, and `platform` sends the signal.
- **Every platform fact**: a value that differs by system, such as the runtime
  folders' default places, the Homebrew prefix, where VS Code and Claude Code
  keep their files, or whether Homebrew casks exist.
- **WSL**, detected once at first use (`sync.OnceValue`), never again.

It imports only the standard library and `golang.org/x/sys`, never another
Workbench package, so every other package may import it.

### 3.2 Layout

```
internal/platform/
  doc.go               the package documentation
  facts_darwin.go      macOS facts
  facts_linux.go       Linux facts and WSL
  process.go           shared process type and Exited
  process_darwin.go    macOS process calls
  process_linux.go     Linux process calls
  memory.go            shared memory reading type
  memory_darwin.go     macOS memory reading
  memory_linux.go      Linux memory reading
  file.go              shared file calls and types
  file_darwin.go       macOS file calls
  file_linux.go        Linux file calls
  socket.go            shared peer credentials
  socket_darwin.go     macOS peer credentials
  socket_linux.go      Linux peer credentials
  libc_darwin.go       libc trampolines (from internal/operation)
  libc_darwin.s
```

Each exported name exists in both platform files with the same signature, so
callers compile the same on both. There is no third file for other systems:
Workbench ships for macOS and Linux only, so building for anything else fails
to compile, which replaces the run-time refusal in `operation.runtimePaths`.
When a file grows past one subject, it splits by subject with the same suffix
(`process_darwin.go`, `process_linux.go`).

### 3.3 Its interface

| Name | What it gives | Replaces |
| --- | --- | --- |
| `System` | the Go name, `"darwin"` or `"linux"`, for messages | the `runtime.GOOS` strings in `checkPlatform` |
| `WSL()` | true on WSL, read once | `machine.isWSL` |
| `RuntimeFolders(home)` | the five default runtime folders | the branch in `operation.runtimePaths` |
| `HomebrewPrefixes`, `HomebrewRepositories` | where Homebrew lives | `machine.findAgent` and `operation.homebrewRepository` |
| `HasCasks` | Homebrew casks and Mac apps exist | the `darwin` tests in `machine/updates.go` and `agents.go` |
| `ToolFolders(home, nvm)` | the folders searched for an agent's launcher, in order, with nvm's default Node in its place | the two branches in `machine.findAgent` |
| `VSCodeUserFolder(home)` | VS Code's user settings folder | the branch in `machine` doctor |
| `ClaudeManagedFolder` | Claude Code's managed settings folder | the branch in `costs.ClaudeManagedSettings` |
| `GroupFromFolder` | new files take their folder's group | the `darwin` test in `operation.inheritedGroup` |
| `TouchIDPossible` | Touch ID for sudo can exist | the test in `operation.TouchIDForSudo` |
| `AssetTriple` | the `apple-darwin` or `unknown-linux-gnu` asset name part | the map in `machine/setup.go` |
| process calls | list processes, one process's info, arguments, one environment value, physical footprint, resident memory (`Resident`), working folder (`Folder`), send a signal safely, wait for exit | `internal/guard/platform_*`, `operation.PhysicalFootprint` |
| memory reading | the system's memory pressure, available and total | `readMemory` in `internal/guard/platform_*` |
| file calls | no-replace rename, private file system check, terminal check, ACL and metadata reads | `internal/operation/filesystem_*`, the calls in `image_metadata_*` |
| `Peer(conn)` | the connected process's user and process ID | `LOCAL_PEERPID` in `operation/askpass_darwin.go`, new on Linux |

`release.Target()` keeps `runtime.GOOS` and `runtime.GOARCH`: it names the
build's own bundle, not a platform difference, and `runtime.GOARCH` stays
wherever it is used.

### 3.4 Which platform's steps exist

Which effects and steps a platform offers is Workbench policy, not an
operating-system fact, so it stays in `internal/machine`. It is chosen by
file suffix there too: `effects_darwin.go` and `effects_linux.go` each
define the platform's effect list and optional effects, and the shared code
calls them. WSL's additions on Linux read `platform.WSL()`.

### 3.5 Kept that way

A `forbidigo` rule in `.golangci.yml` forbids `runtime.GOOS` outside
`internal/platform` and `internal/release/archive.go`, so a new platform test
anywhere else fails `golangci-lint run`. AGENTS.md's ownership table gains
the row "Operating-system calls and platform facts | `internal/platform/`",
and `docs/adr/0012-platform-package.md` records why, with the sources in
section 2.

### 3.6 Checks

The move changes no behavior. Before and after it, on macOS in a disposable
home and on Linux in Docker (and the WSL simulation of `render-check.sh`):
`workbench apply --dry-run --json` gives the same plan digest, `workbench
doctor` the same lines, the guard's existing tests pass, and both systems
build and lint. No new test: AGENTS.md admits none for a move.

## 4. `workbench guard logs`

One line per event: time, level, event, then `key=value` pairs in a fixed
order, `cmd` last because it is the longest.

```
Oct 17 14:41:05 INFO  ended     id=4fQ8nX2a agent=claude subagent=Explore project=feature-login took=53s peak=2.4GB cmd="pnpm typecheck 2>&1 | tail -15"
Oct 17 14:41:20 WARN  unmatched id=7Kp3Rt9m agent=claude project=feature-login took=2.0s cmd="./scripts/nap.sh"
Oct 17 14:42:31 WARN  stopping  id=2Bv6Yc1d agent=claude project=api-server by=guard took=1m12s size=6.2GB peak=6.2GB cmd="cargo test --workspace"
Oct 17 14:42:36 WARN  forced    id=2Bv6Yc1d agent=claude project=api-server cmd="cargo test --workspace"
Oct 17 14:43:02 WARN  memory    size=3.1GB floor=4.0GB available=1.1GB total=16GB msg="memory is low, but the agent commands are too small to stop"
Oct 17 14:44:40 WARN  stopping  id=9Hs5Wq4e agent=claude project=feature-login by=you took=8.0s size=1.3GB peak=1.3GB cmd="pnpm vitest run"
Oct 17 14:45:10 ERROR error     msg="cannot read a process" error="open /proc/81234/statm: no such file or directory" process=81234
Oct 17 14:50:00 INFO  idle
```

- **Time** is the machine's zone, `Jan 2 15:04:05`, as journalctl and the
  log's own records sort.
- **Level** is the record's slog level. **Event** is the record's `event`
  (guard design 4.1), with `tracking` folded away: a command shows when it
  ends (`ended`), goes unmatched or is stopped (`stopping`, then `forced` or
  `gave-up` if it comes to that). The watcher's own events (`started`,
  `handover`, `idle`, `exited`) print the same way, dim.
- **`id`** is the first 8 characters of the tool call ID after its `toolu_`
  or `call_` prefix. `--json` keeps the full ID.
- **`project`** is where the command ran (section 6.2).
- **`cmd`** is the note's command with a leading `cd PATH &&` or `cd PATH;`
  removed; the path went into `project`.
- **Sizes** are `MB` below 1 GB, else `GB` with one decimal. **`took`** has
  one decimal below 10 s, whole seconds below a minute, else `1m12s`.
- **Colors**, on a terminal without `NO_COLOR` only: INFO dim, WARN yellow,
  ERROR red; keys dim, values plain; `id` blue; `took` of 10 s or more bold;
  `peak` or `size` of 1 GB or more yellow.
- **Order of keys:** `id agent subagent project by dry_run took size peak
  floor level available total still_running release old_release new_release
  new_pid msg error process cmd`. A key without a value is left out, and a
  key the list does not name (`part`, `pid`, `root_pid`, `children`,
  `reason`, the `memory` object's swap fields) is left off the line and kept
  in `--json`; the `memory` object flattens to `level` on macOS and
  `available` and `total` on Linux. `msg` is printed only where the event
  alone does not say what happened: `memory`, `refused`, `gave-up`, `error`
  and `raw` (a watcher output line, guard design 4.1).
- A long line soft-wraps on screen and stays one line, so `grep` and copying
  keep the whole event.
- `--since`, `--follow` and `--json` stay as they are (guard design 4.1).
  With `--follow`, a command still running 5 s after its `tracking` record
  gets one `INFO running` line with the same keys and no `took` or `peak`.
- A stop's `reason` text, the message the agent is told, stays in `--json`
  and the live view's details.

## 5. `workbench guard memory`

A full-screen Bubble Tea program in the alternate screen.

```
[WorkBench] Guard memory  ● watching · v0.1.26 · memory normal · 10.5 of 16 GB used      2:41:08 PM
2 running · 1 finished in the last minute · largest peak 6.2 GB

  ID        STATUS      AGENT   SUBAGENT  TIME  MEMORY    PEAK  PROJECT            COMMAND
▸ 4fQ8nX2a  ● run       claude  Explore    44s  2.2 GB  2.3 GB  feature-login  pnpm typecheck 2>&1 | tail -15
  3Nd7Lp8z  ● run       codex               15s  840 MB  910 MB  price-feed      cargo check -p feed
  5Jx2Gm6r  ✓ done      claude            0.6s       —   18 MB  api-server     rg -n -i 'digest' …

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
  `stopped 4fQ8nX2a` or `4fQ8nX2a already finished`.
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

## 6. Guard changes

### 6.1 Note fields

The pre hook adds to each note (guard design 4):

| Field | From |
| --- | --- |
| `cwd` | the hook input's `cwd` (Claude Code and Codex) |
| `subagent` | Claude Code's `agent_type` when the call comes from a subagent; empty otherwise and for Codex |
| `project` | section 6.2, computed once here |

### 6.2 Project name

From the command's leading `cd PATH` when it has one, else from `cwd`,
resolved against `cwd`: the folder name after a `.worktrees/` or
`worktrees/` component when the path has one, else the name of the nearest
folder at or above it that holds `.git`, else the path's last folder name.
It only labels; nothing acts on it.

### 6.3 Pairing by folder

`claims` in `internal/guard/match.go` also requires, when both are known,
that the process running the command has as its working folder, when it is
paired, either the note's `cwd` or, for a command that starts with `cd PATH`,
that folder. Linux reads
`/proc/PID/cwd`; macOS reads `proc_pidinfo` with `PROC_PIDVNODEPATHINFO`. A
process whose folder cannot be read pairs as today. Codex commands with a
call ID pair by ID as today.

### 6.4 Memory every tick

Each tick the watcher sums each tracked command's tree (guard design 6.3's
tree, the same processes a stop would signal) and keeps per command the last
120 sums and the peak. macOS uses the physical footprint, as stops do. Linux
uses `/proc/PID/statm`'s resident pages for this sum; the stop decision keeps
`smaps_rollup`'s Pss, read only when memory calls for a stop. The `ended`
and `stopping` records gain `took` (seconds, float), `peak` (bytes), `cwd`,
`project` and `subagent`; `unmatched` gains the same but `peak`, since its
command was never measured; `stopping` gains `by` (`guard` or `you`). The
view's `stopped` state is a command whose `stopping` record has been written
and whose processes are gone.

### 6.5 The socket

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

## 7. Platforms and reuse

The guard view adds no platform test. Its three new operating-system needs
are `internal/platform` calls (section 3.3), each with a macOS and a Linux
version; WSL runs the Linux one.

| Need | macOS | Linux and WSL | Platform call |
| --- | --- | --- | --- |
| A process's memory each tick | physical footprint (`proc_pid_rusage`), the figure stops use | `/proc/PID/statm` resident pages × page size; stops keep `smaps_rollup`'s Pss | `Resident(pid)` |
| A process's working folder | `proc_pidinfo` with `PROC_PIDVNODEPATHINFO`, through the libc trampolines | `os.Readlink("/proc/PID/cwd")` | `Folder(pid)` |
| The peer's user and process ID | `LOCAL_PEERCRED` (`unix.GetsockoptXucred`) and `LOCAL_PEERPID` | `SO_PEERCRED` (`unix.GetsockoptUcred`) | `Peer(conn)`, which the password helper uses too |

Whether a peer descends from a noted agent is guard logic over its own
process snapshot (`snapshot` in `internal/guard/guard.go`), not a platform
call.

Everything else reuses what Workbench already has:

- **Agent detection**: `operation.AgentSession` (`internal/operation/plan.go`).
- **Terminal view**: Bubble Tea v2, Lip Gloss v2 and Bubbles v2 as in go.mod,
  built like the costs report's full-screen view (`internal/cli/costsview.go`,
  `view.AltScreen`), with the shared styles and `painter` from
  `internal/cli/root.go` and widths from `lineWidth` and `terminalWidth`
  (`internal/cli/table.go`).
- **Log printing**: `guard.ReadLog` and the existing `--since` parser
  (`internal/cli/timeflag.go`); only the line format in
  `internal/cli/guard.go` changes. Sizes keep `guard.Gigabytes`, plus MB below
  1 GB.
- **Messages and records**: `encoding/json` for JSON Lines, and the guard's
  `slog` JSON records for the log.
- **Socket handover**: the listening socket rides the handover the lock
  already uses (`internal/guard/handover.go`: `operation.StartDetached`'s
  `Files` hands the open files over, `operation.InheritLock` takes them up).
- **Stops**: the guard's existing stop path (`stop`, `targets` and the 5 s
  escalation in `internal/guard/watch.go`, sending through
  `internal/platform`); a manual stop adds only who asked and the message.
- **No new modules.** `golang.org/x/sys` already has every call above.

## 8. Tests

The platform move adds none (section 3.6).

AGENTS.md admits tests only for data loss, credential exposure or
unauthorized execution. Each of these guards against a command being stopped
that nobody with the right to stop it chose:

1. The watcher refuses a `stop` from a peer of another user and from a peer
   under a noted agent.
2. A `stop` naming an unknown or finished ID signals nothing.
3. Two notes with the same command text in two folders pair with the
   processes in their own folders.

## 9. Checks before release

- macOS in a disposable home and Linux in a memory-capped Docker container,
  with `WORKBENCH_GUARD=dry-run` and then real stops of commands the check
  itself started: the view lists, tags, stops politely and by force, shows
  details, survives a watcher handover and a watcher exit.
- Real Claude Code and Codex commands in two worktrees of one repository at
  once: rows carry the right project and subagent, and a stop hits the row
  chosen.
- The view's and the watcher's CPU and memory over a minute with ten tracked
  commands, recorded in `docs/acceptance.md`.

## 10. Docs

- AGENTS.md: the ownership row and the `runtime.GOOS` rule (section 3.5).
- `docs/adr/0012-platform-package.md` (section 3.5).
- `docs/usage.md`: the new `guard logs` format and the `guard memory`
  command, keys and refusals.
- The guard design: note fields (section 4), log keys (4.1), pairing by
  folder (6.1), memory each tick and the socket (6.5).
- `docs/superpowers/specs/workbench-contracts.md`: the socket and its
  messages.
- `docs/adr/0013-guard-memory-view.md`: why a socket served by the watcher,
  the peer checks and the agent lock, with the sources in section 2.
- `docs/acceptance.md`: what the checks in section 9 observed.
