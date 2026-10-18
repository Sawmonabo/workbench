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
- **Memory in use.** Activity Monitor's Memory Used is App Memory (the
  anonymous pages less the purgeable ones) plus Wired plus Compressed; htop
  adopted the same pages for Apple silicon
  ([htop#1439](https://github.com/htop-dev/htop/pull/1439)), and
  `host_statistics64` with `HOST_VM_INFO64` gives each count in
  `struct vm_statistics64` (`mach/vm_statistics.h`). On Linux, `free(1)`'s
  used column is "Total - Available" since procps-ng 4.0
  ([NEWS](https://sources.debian.org/src/procps/2%3A4.0.4-9/NEWS),
  [free(1)](https://manpages.debian.org/trixie/procps/free.1.en.html)).
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

Each subject's `_darwin.go` and `_linux.go` files export the same names with
the same signatures, so callers compile the same on both; a file without a
suffix holds only what is identical on both systems. There is no third file
for other systems: Workbench ships for macOS and Linux only, so building for
anything else fails to compile, which replaces the run-time refusal in
`operation.runtimePaths`.

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
| memory reading | the system's memory pressure, available, total and in use (`Used`) | `readMemory` in `internal/guard/platform_*` |
| file calls | no-replace rename, private file system check, terminal check, ACL and metadata reads | `internal/operation/filesystem_*`, the calls in `image_metadata_*` |
| `Peer(conn)` | the connected process's user and process ID | `LOCAL_PEERPID` in `operation/askpass_darwin.go`, new on Linux |

`release.Target()` keeps `runtime.GOOS` and `runtime.GOARCH`: it names the
build's own bundle, not a platform difference, and `runtime.GOARCH` stays
wherever it is used.

### 3.4 Which platform's steps exist

Which effects and steps a platform offers is Workbench policy, not an
operating-system fact, so it stays in `internal/machine`. It is chosen by
file suffix there too: `effects_darwin.go` and `effects_linux.go` each
define `platformEffects` and `platformOptionalEffects`, which pick the
platform's effects, and the shared code calls them. The effect lists
themselves stay shared in `effects.go`, because a test that runs on macOS
checks the Mac, Linux and WSL lists together; only the Mac's optional
effects live in the macOS file, since nothing on Linux uses them. WSL's
additions on Linux read `platform.WSL()`.

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
Oct 17 14:41:05 INFO  ended     id=4fQ8nX2a agent=claude subagent=Explore project=feature-login took=53s peak=2.41GB cmd="pnpm typecheck 2>&1 | tail -15"
Oct 17 14:41:20 WARN  unmatched id=7Kp3Rt9m agent=claude project=feature-login took=2.0s cmd=./scripts/nap.sh
Oct 17 14:42:31 WARN  stopping  id=2Bv6Yc1d agent=claude project=api-server by=guard took=1m12s size=6.21GB peak=6.21GB available=1.12GB total=16GB cmd="cargo test --workspace"
Oct 17 14:42:36 WARN  forced    id=2Bv6Yc1d agent=claude project=api-server cmd="cargo test --workspace"
Oct 17 14:43:02 WARN  memory    size=740MB floor=1GB available=1.12GB total=16GB msg="memory is low, but the agent commands are too small to stop"
Oct 17 14:44:40 WARN  stopping  id=9Hs5Wq4e agent=claude project=feature-login by=you took=8.0s size=1.31GB peak=1.31GB cmd="pnpm vitest run"
Oct 17 14:45:10 ERROR error     msg="cannot read a process" error="open /proc/81234/stat: permission denied" process=81234
Oct 17 14:50:00 INFO  idle
```

- **Time** is the machine's zone, `Jan _2 15:04:05` (Go's `time.Stamp`, the
  day padded to two columns, so the fields line up), as journalctl and the
  log's own records sort.
- **Level** is the record's slog level. **Event** is the record's `event`
  (guard design 4.1), with `tracking` folded away: a command shows when it
  ends (`ended`), goes unmatched or is stopped (`stopping`, then `forced` or
  `gave-up` if it comes to that). The watcher's own events (`started`,
  `handover`, `idle`, `exited`) print the same way, dim.
- **`id`** is the first 8 characters of the tool call ID after its `toolu_`
  or `call_` prefix and a provider segment such as Bedrock's `bdrk_` or
  Vertex AI's `vrtx_` (`toolu_bdrk_01…`), which every ID of that provider
  shares. `--json` keeps the full ID.
- **`project`** is where the command ran (section 6.2).
- **`cmd`** is the command as the guard log records it, clipped as the stop
  reason clips it (guard design 4.1 and 6.4): backticks as `'`, blanks and
  newlines collapsed to one space, at most 120 characters with `…`, so a
  multi-line command shows on one line, shortened; `guard memory`'s details
  show it whole. A leading `cd PATH &&` or `cd PATH;` is removed; the path
  went into `project`.
- **Quoting**, as logfmt does: a value holding a space, `"`, `=`, `\` or a
  control character is written in Go's double quotes, which keep it on one
  line, any other bare (`cmd=./scripts/nap.sh`).
- **Sizes** are binary, as Activity Monitor counts them (1 GB is 1024 MB),
  whole `MB` below 1 GB, and take one of two roles. A measurement, a reading
  that moves (`size`, `peak`, `available`, memory and swap in use), has two
  decimals from 1 GB (`peak=2.41GB`). A capacity, a fixed total (`total`,
  `floor`, installed memory, swap total, a container's limit), is a whole
  number when it is whole at two decimals (`total=16GB`) and has two decimals
  when it is not (`1.86GB` for an odd container limit). The reason the guard
  gives the agent follows the same rule. **`took`** has one decimal below
  10 s, whole seconds below a minute, else `1m12s`.
- **Colors**, at a terminal: the time and the keys faint; the level label
  alone colored, INFO green, WARN gold bold, ERROR red bold, an unknown level
  faint; the event and `id` plain; `agent` in its agent's hue (clay for Claude
  Code, steel for Codex); `by=you` bold; `took` of 10 s or more bold; `size`
  or `peak` gold bold from 1 GB and red bold from 4 GB, counted as the
  size prints, so `1.00 GB` is gold and `4.00 GB` red; every other value
  plain. A pipe, `TERM=dumb` and `NO_COLOR` get plain text, as
  [clig.dev](https://clig.dev/#output) advises. The colors are pinned hex
  values, not the terminal's own yellow, red and green, which most themes draw
  bright: the three semantic colors are a step darker and mean the same in the
  log and the live view (green routine, gold worth a look, red acted on), and
  one hue per agent lets the eye see whose command a line is. Lip Gloss
  [`Color`](https://pkg.go.dev/charm.land/lipgloss/v2#Color) downsamples them
  to what the terminal shows. Color goes only where a line needs the eye, not
  on every value.
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
[WorkBench] Guard memory  ● watching · 11:33:32 PM

3 running · 2 finished · mem 11.27/16 GB · peak 6.21 GB · swap 3.48/5 GB

   ID        STATUS     AGENT   SUBAGENT      TIME   MEMORY     PEAK  PROJECT        COMMAND
▸  01HsQEPc  ● run      claude  implementer    24s  1.52 GB  1.52 GB  api-server     go test ./... 2>&1 | tail -20
   3Nd7Lp8z  ● run      codex                  15s   840 MB   910 MB  price-feed     cargo check -p feed
   7Kp3Rt9m  ● run      claude                8.0s   320 MB  2.48 GB  feature-login  pnpm vitest run
   5Jx2Gm6r  ✓ done     claude  Explore       0.6s        —    18 MB  api-server     rg -n -i digest src
   2Bv6Yc1d  ✗ stopped  claude               1m12s        —  6.21 GB  api-server     cargo test --workspace
↑/↓ move  space tag  enter/→ details  k stop  q/esc quit
```

- **Rows** are the commands the watcher tracks, newest first, then finished
  ones, dimmed, for 60 s after they end, with their outcome: `✓ done`,
  `? unmatched`, `✗ stopped` (by the guard or by you). A row stopping and
  waiting on its 5 s shows `◌ stopping`. A dry-run command's status ends in
  `(dry)` (`● run (dry)`), in the list and in its details.
- **Header**, four lines. The first: the brand, bold; the watcher's state
  (`●` green and ` watching` while connected, else `○` gold and ` not
  running` or ` not connected`); a badge after the state when memory runs
  short, `memory low` gold and bold or `memory critical` red and bold, and
  none for `normal` or macOS's `warning`, which the guard treats as routine;
  then a dim ` · ` and the clock (`3:04:05 PM`), inline, which a window too
  narrow for the badge and the clock drops. The second is blank. The third,
  dim: the counts, `N running · M finished` (finished in the last minute),
  and, once a watcher has sent a snapshot, `mem USED/INSTALLED`: the
  snapshot's `memory.used`, the memory in use as the system's own monitor
  counts it (the used-memory figure `platform.Memory.Used` carries, section
  2), of the installed memory, bold and not dim, its unit written once when
  both share it (`11.27/16 GB`, `289 MB/16 GB`, `—/16 GB` from a watcher
  that does not send it); the used figure is a measurement and the installed
  and swap totals capacities, by the rule in section 4; `peak P`, the largest
  peak since the watcher started; and `swap USED/TOTAL`, left out where there
  is no swap. The fourth says why no watcher answers or, when every shown
  command is a dry run, `dry run: stops are logged, not sent`, when only some
  are, `dry run on 2 of 5 commands: stops are logged, not sent`, in gold, and
  is blank otherwise; the dry-run texts fit 60 columns, and a longer text
  wraps onto another line, so no line is cut.
- **Keys**: ↑ and ↓ move (no `j`/`k`, since `k` stops, as in htop);
  Space tags or untags a running row and moves down; Enter, → or `l` opens
  details; `k` opens the stop menu for the tagged rows, or the selected one;
  Esc, ← or `h` closes a menu or details; `q`, Esc on the list and ctrl+c
  quit, as the costs view's Esc does. In details ↑ and ↓ scroll a line,
  PgUp and PgDn a page, Home and End go to the top and the bottom, `k`
  stops, Esc, ← or `h` goes back and `q` quits. On the list the mouse works
  too: the wheel moves as ↑ and ↓ do, a click selects a row and a click on
  the selected row opens its details. The view turns mouse reporting on
  for the list because without it a trackpad or wheel scrolls the
  terminal's own buffer and the screen slides away; the list scrolls only
  when the selection leaves the window, so a row stays under the pointer
  that clicked it. With the mouse taken, hold Shift (Option in Terminal.app
  and iTerm2) to select text. In details, and in the stop menu over them,
  which takes keys only, mouse reporting is off, so a plain drag selects
  text, and the wheel still scrolls the details: at start the view turns on
  the terminal's alternate scroll mode (xterm's DECSET 1007 in
  [Control Sequences](https://invisible-island.net/xterm/ctlseqs/ctlseqs.html)),
  under which a wheel step on the alternate screen is sent as ↑ or ↓ while
  the mouse is not taken; mouse reporting takes precedence over it on the
  list. The mode's prior state is saved first (XTSAVE) and, when the view
  ends, however it ends, the mode is turned off and that state restored
  (XTRESTORE), so a terminal that had it on keeps it. A terminal without the
  mode ignores the sequences and scrolls its own buffer under the wheel in
  details, as before. The key line reads
  `↑/↓ move  space tag  enter/→ details  k stop  q/esc quit` on the list,
  `↑/↓ scroll  k stop  esc/← back  q quit` in details and
  `↑/↓ choose  enter send  esc/← cancel` in the menu.
- **Stop menu**: a box over the list titled with the command and its memory
  (or the number of tagged rows), with `Stop (polite, force-killed after 5
  s)` selected and `Force kill now` below it. The commands it names are
  fixed when it opens: it closes with a notice when none of them still
  runs, and Enter stops only those still running and names the others as
  finished. Enter sends, Esc, ← or `h` cancels. The answer from the watcher
  is shown for 3 s on the bottom line, for example `stopped 4fQ8nX2a`, `logged
  4fQ8nX2a (dry run)` or `4fQ8nX2a already finished`.
- **Details**, in this order: the full command, folder, agent, subagent,
  full ID, status and run time, memory now and peak, the number of
  processes and the process tree with each process's memory, and last a
  one-line graph of the command's memory over the last 120 readings. The
  command is laid out one step per line, as the Google Shell Style Guide
  lays out a long pipeline
  ([Pipelines](https://google.github.io/styleguide/shellguide.html#s5.5-pipelines)),
  which it applies to `&&` and `||` too: a `|`, `&&` or `||` that stands as
  a token outside quotes starts the next line, two spaces in, and a `;`
  ends its line and is dropped (`go test ./... 2>&1`, then `  | tail -20`).
  Nothing splits inside single or double quotes, `$' '`, backquotes, `$( )`,
  `( )`, `${ }`, a `{ }` group, `[[ ]]` (where `||` and `&&` are the test's
  own) or a `case … esac` (whose patterns and arms are its own), after a
  backslash, or in a comment, from a `#` that starts a word to the end of
  its line; nor do the `&` of `2>&1`, `|&` or `>|`. A command with no
  separator stays one wrapped line, and so does one that holds a newline
  outside quotes, such as one with a here-document: its author has laid it
  out already, and a here-document's body is data, not steps. The details
  show every line and never cut one: they lie in a pane that fills the room
  between the header and the key line and scrolls when they are taller
  than it. They open at their top; a new snapshot redraws them and keeps
  the scroll offset, pulled back to their end when they grew shorter. `k`
  stops from here too; Esc, ← or `h` goes back.
- **Narrow windows**: COMMAND shrinks first, then SUBAGENT, PROJECT, PEAK
  and AGENT drop in that order; a project label is clipped at 32 columns, a
  `REPO/WORKTREE` label from the worktree's start (`api/…ure-login`), so
  the repository always reads.
  The counts line, about 72 columns in full, drops `· swap …` first, then
  `· peak …`, and is then clipped. Below 60 columns the view says `Widen the
  window to at least 60 columns`, and below 11 lines `Make the window at
  least 11 lines tall` (the four header lines, the six-line stop menu below
  them and the key line), so the stop menu and the bottom line always show.
  Details lay out the same way in any window and scroll; at 60×11 the pane
  holds six lines, fewer when the header's fourth line wraps.
- **Colors**: the guard's pinned palette (section 4). In the rows the
  STATUS cell is green for running, gold and bold for stopping, red for
  stopped, a dim gold for unmatched and plain for done; the AGENT cell
  takes its agent's hue; MEMORY and PEAK are gold and bold from 1 GB and
  red and bold from 4 GB; the tag mark `*` is gold. Each keeps the row's
  own dimming, so a finished row stays dim (a 6.21 GB peak on a finished
  row is still red and bold). The selected row lies on a band across the
  window, `#3a4252`, a slate a few steps lighter than a dark background,
  the way lazygit's `selectedLineBgColor` marks the selected line
  ([Config.md](https://github.com/jesseduffield/lazygit/blob/master/docs/Config.md)),
  with its `▸` and ID bold in a light steel accent, `#9ec1dc`, not dimmed
  even on a finished row; the rest of the row keeps its colors and is not
  bold, and a finished row stays dim under the band. Every colored
  foreground on the selected row, the tag mark `*` too, keeps at least 3:1
  contrast with the band by WCAG 2's contrast ratio
  ([Understanding SC 1.4.3](https://www.w3.org/WAI/WCAG22/Understanding/contrast-minimum.html));
  3:1 is its floor for large text and for the parts of a view a person
  must tell apart
  ([1.4.11](https://www.w3.org/WAI/WCAG22/Understanding/non-text-contrast.html)),
  while 4.5:1 for normal text would turn the palette to pastels. A pinned
  color under it, or only just over it, is drawn on the selected row as
  the same hue lighter, to about 3.2:1 or more: on the dark band green
  `#6e9c72` (2.54:1 pinned), gold `#b9942e` (2.94:1), red `#e5737c`
  (1.61:1), dim gold `#a39145` (2.37:1, still dimmer than the gold) and
  steel `#7297aa` (3.11:1); clay keeps its pinned value (3.38:1). Off the
  band every color keeps its pinned value. The view asks the terminal for
  its background color
  ([`RequestBackgroundColor`, `BackgroundColorMsg`](https://pkg.go.dev/charm.land/bubbletea/v2@v2.0.10#BackgroundColorMsg))
  and picks the band with Lip Gloss's
  [`LightDark`](https://pkg.go.dev/charm.land/lipgloss/v2@v2.0.6#LightDark):
  on a light terminal the band is `#d6dde8` and the accent `#2f5c7e`, and
  the colors under 3:1 there are drawn darker: green `#59825d` (2.91:1
  pinned), gold `#927525` (2.52:1), clay `#a26d3e` (2.19:1) and steel
  `#587d92` (2.37:1); the red (4.58:1) and the dim gold (3.12:1) keep
  their pinned values. Until the terminal answers, and where it never does
  (some terminals and multiplexers do not), the view draws for a dark
  background. Terminals draw a finished row's dimming by blending toward
  the background, which the ratio does not measure. In the details the
  status and the memory values take the pinned colors.
- **No color** (`NO_COLOR` or no color support): the same screen without
  color, bold and dim kept, as the costs view keeps them; `▸`, `✓ ? ✗ ●`,
  `○` and the tag mark carry the state, and the badge's words the memory.
  The band and the accent are color, so without color the selected row
  keeps its `▸` and is bold instead.
- **Watcher not running**: the screen says so and that the watcher starts
  with the next agent command, keeps the last rows dimmed, and reconnects
  every second without saying so: the screen states the fact, not the
  retry. When a watcher may run but cannot serve the view (another
  release, a socket it cannot reach, or a hello it did not finish, as when
  it is exiting), the header says `not connected`, its fourth line says why,
  and the view tries again each second.
- **Refusals**, before the screen opens: with `--json` it exits 3 with
  `guard memory has no JSON output; use workbench guard logs --json`.
  Without a terminal on stdin and stdout, or with `--non-interactive`,
  `guard memory` exits 3 with `guard memory needs a terminal; use
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
resolved against `cwd`: `REPO/WORKTREE`, the name of the folder holding a
`.worktrees/` or `worktrees/` component and the name after it, when the path
has one (`api/feature-login`: the repository is the project and the worktree
its branch, as the costs report folds a worktree into its project), else the
name of the nearest folder at or above it that holds `.git`, else the path's
last folder name.
It only labels; nothing acts on it.

### 6.3 Pairing by folder

`match` in `internal/guard/match.go` prefers by folder and never rejects
by it: among the children that claim a note, it takes the one whose process
running the command has as its working folder the folder of the note's
leading `cd PATH`, above one in the note's `cwd`, above one whose folder is
unknown or neither, the oldest within a rank. Linux reads
`/proc/PID/cwd`; macOS reads `proc_pidinfo` with `PROC_PIDVNODEPATHINFO`. A
process whose folder cannot be read ranks last and still pairs as today.
Codex commands with a call ID pair by ID as today.

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
  as descriptor 6, announced by `WORKBENCH_GUARD_LISTENER=6` while
  `WORKBENCH_GUARD_HANDOVER` keeps the old watcher's PID. The old watcher
  sets unlink-on-close off before it closes its copy, so the socket file
  stays, and a connected view sees one disconnect and reconnects.
- **Who may connect.** The watcher reads each connection's peer
  credentials (`SO_PEERCRED` on Linux, `LOCAL_PEERCRED` and `LOCAL_PEERPID`
  on macOS) and closes it unless the user ID is its own. It may serve a
  snapshot to any such peer; it refuses a `stop` from a peer whose process
  is still under an agent that has a note (parents only: a detached command
  is not under it, and could use `kill` anyway).
- **Messages.** JSON Lines in both directions. The view first sends
  `{"type":"hello","versions":[1]}`; the watcher answers
  `{"type":"hello","version":1,"release":"v0.1.26"}` with the highest
  version both list, or `{"type":"error","error":"no protocol version in
  common; the watcher speaks 1"}` and closes; the view then says the watcher
  runs a different release and to run `workbench update`. Every message has
  one type, and each type uses only its own fields.
- **Snapshot**, after each tick:
  `{"type":"snapshot","snapshot":{"at":TIME,"memory":{"level":"normal","installed":N,"used":N,"available":N,"total":N,"swap_free":N,"swap_total":N},"largest_peak":N,"commands":[…]}}`,
  the snapshot nested under `snapshot`, each command with `id` (full),
  `agent`, `subagent`, `project`, `cwd`, `cmd`, `state` (`running`,
  `stopping`, `done`, `unmatched`, `stopped`), `by`, `dry_run` (the note's),
  `started`, `ended`, `memory`, `peak`, `history` (the last readings), and
  `tree` (`pid`, `name`, `memory` per process) for running ones.
- **Stop**: `{"type":"stop","ids":[…],"force":false}`. The watcher answers
  `{"type":"stopped","results":[{"id":…,"ok":true}|{"id":…,"ok":false,"error":"already finished"}]}`.
  It looks each ID up among its tracked commands, never a process number,
  and stops it the way it stops for memory (guard design 6.4: the same
  targets, start-time checks, pidfd on Linux, 5 s escalation; `force` sends
  SIGKILL at once), telling the agent `Stopped by you (workbench guard
  memory).` In dry-run mode it logs `would stop` and signals nothing.
  A person's stop is asynchronous: the answer comes once the first signal
  is sent, the tick goroutine follows the stop across ticks (the row shows
  `stopping`, then `stopped`) and escalates after 5 s, and it starts no
  cooldown, so the guard's own stops go on as before.

## 7. Platforms and reuse

The guard view adds no platform test. Its three new operating-system needs
are `internal/platform` calls (section 3.3), each with a macOS and a Linux
version; WSL runs the Linux one.

| Need | macOS | Linux and WSL | Platform call |
| --- | --- | --- | --- |
| A process's memory each tick | physical footprint (`proc_pid_rusage`), the figure stops use | `/proc/PID/statm` resident pages × page size; stops keep `smaps_rollup`'s Pss | `Resident(pid)` |
| A process's working folder | `proc_pidinfo` with `PROC_PIDVNODEPATHINFO`, through the libc trampolines | `os.Readlink("/proc/PID/cwd")` | `Folder(pid)` |
| The peer's user and process ID | `LOCAL_PEERCRED` (`unix.GetsockoptXucred`) and `LOCAL_PEERPID` | `SO_PEERCRED` (`unix.GetsockoptUcred`) | `Peer(conn)`, giving `platform.Credentials{UID, PID}`, which the password helper uses too |

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
  `internal/cli/guard.go` changes. Sizes print through `guard.Size` (a
  measurement) and `guard.Capacity` (a capacity), which the stop reason uses
  too.
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

1. The watcher refuses a `stop` from a peer under a noted agent, PID 1
   included (`TestManualStopsOnlyWhatAPersonMayStop`). The other-user case
   has no test: it is decided at accept from the peer's credentials, which
   only a real socket and a second user give, and no test opens a socket;
   it is checked by reading `handle` in `internal/guard/socket.go`.
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
