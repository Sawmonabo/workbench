# Installation and commands

Install the latest release, or pin one, with one command. Each release carries
its own `install.sh`.

```sh
curl -fsSL https://github.com/Sawmonabo/workbench/releases/latest/download/install.sh | sh
curl -fsSL https://github.com/Sawmonabo/workbench/releases/latest/download/install.sh | sh -s -- --version v0.2.0

# With gh:
gh release download -R Sawmonabo/workbench -p install.sh -O - | sh
gh release download -R Sawmonabo/workbench -p install.sh -O - | sh -s -- --version v0.2.0
```

`install.sh` picks the bundle for this OS and CPU and downloads it with `gh`
when `gh` is installed and logged in to github.com, otherwise with `curl`. It
extracts only the CLI and runs `workbench update VERSION --bundle FILE`, passing
every other argument through:

- `--dry-run` verifies the bundle and shows only the install plan.

Installing never applies the machine; the CLI's last line says to run
`workbench apply`. A fresh machine's shell does not have `~/.local/bin` on
`PATH` yet, so that line and the other hints that name `workbench` give its full
path, `~/.local/bin/workbench`, until it is. The first apply writes the shell
setup that adds it and ends by telling you to open a new terminal, which finds
`workbench` and the tools it installed.

Every install, update and apply puts Workbench's own tools in place first,
without asking: chezmoi, uv, Python and TOML Kit, pinned by the release,
download into Workbench's own directory when a qualified one is missing.

The CLI checks every file against the SHA-256 manifest inside the bundle before
installing. That catches a truncated or corrupted download; it is not a
signature. The installer needs POSIX `sh`, `uname`, `mktemp`, `tar`, and `curl` or a
logged-in `gh`. Never run it as root.

## Commands

| Command | Behavior |
| --- | --- |
| `apply` | Install Workbench's tools if missing and ask any machine question your answers lack, then plan. The first apply, and any apply that finds a step or file to decide, shows the `[WorkBench]` plan: changed files with line counts, and each step with what it would do here. Space turns a step on or off, `a` applies, `q` quits. Your choices are remembered, so a later apply with nothing new prints the plan and applies it without asking. |
| `apply --choose` | Show the plan and ask even when nothing is new, with your saved choices set. |
| `apply --yes` | Never ask: apply your saved choices, and give anything new its default. Refused for a coding agent, which applies with `--approve-plan`. |
| `apply --reset` | Forget your saved choices and ask again with the defaults; what you pick is saved. |
| `apply --dry-run` | Print the plan and exit; installs, asks and changes nothing. |
| `update` | Install the latest release and its tools. Never applies; run `apply` next. `update --dry-run` shows only the install step. A local build is replaced by the latest published release, whatever their versions. |
| `update VERSION` | The same for that release; an older one goes back. `0.2.0` and `v0.2.0` both work; an empty or malformed VERSION exits 2. |
| `init --answers-from FILE` | One-time adoption of a chezmoi config's `[data]` table. |
| `init --ask KEY` | Ask a saved machine answer again, for example `machine_role`. |
| `version` | Print this Workbench version, the same as `--version`; a local build adds `(local build)`. |
| `version --list` | List the published full releases, marking the latest and the installed one; an installed pre-release or local build follows on its own line. |
| `doctor` | Workbench's version (with `(local build)` for one), what is installed and last applied, any apply that did not finish, tool versions and host checks, and a `CLAUDE_CONFIG_DIR` or `CODEX_HOME` that moves an agent away from the settings Workbench installs; no repair. |
| `revert` | Pick a saved checkpoint, then restore its files after conflict checks. |
| `project inspect/configure/revert [PATH]` | Inspect or configure an existing project; see below. |
| `costs` | What Claude Code and Codex spent, by project, from the local ledger the session hooks keep. At a terminal it opens one tab per tool (Claude Code and Codex): ←/→ or Tab and Shift+Tab switch tools, ↑/↓ select a project or model and Enter opens its page (by model or project, day and session), ← or Esc goes back, q quits and leaves the page printed. The mouse wheel moves the selection, a click selects a row and a click on the selected row opens it; the wheel scrolls a page, a report with no row to select and below the last row. With the mouse taken, hold Shift (Option in Terminal.app and iTerm2) to select text. Without a terminal, `--tool NAME` prints one tool's report. |
| `costs --by model\|account\|month` | The same by another dimension. `--since`/`--until` (a duration ago such as `2h`, `today`, `yesterday`, or a date, date and time or time of today in this machine's time zone, or RFC 3339, as `guard logs --since` takes; a response counts from `--since` up to `--until`, and an `--until` day counts to its end), `--top N`, `--sort cost\|name\|calls`, `--all` (projects outside `~/dev` and `~/repos`, of those that exist), `--detail`, `--tokens`, `--no-rollup`, `--csv` and the global `--json` shape the report. |
| `costs rates`, `costs status` | The merged price card with where each price comes from (`--refresh` refetches the official page), and the ledger's coverage, last ingest, hooks and rate card age. |
| `costs ingest` | The hook command Claude Code and Codex run on session start and end: prints nothing on stdout, exits 0, starts a detached worker. When it can't start one, for example because a Workbench folder isn't private, it says why on stderr and records nothing. `--worker` ingests in the foreground. |
| `migrate` | List the migrations that move files earlier Workbench releases left outside Workbench's folders, or remove what they left that nothing reads (old caches, the guard log's old free-text lines), and whether each has anything to do. `migrate --apply` runs them, asking the way `apply` does (`--dry-run --json`, `--approve-plan`, `--yes`); when only conflicts are left it exits 4 and names each file and what to do with it. Every `apply` runs them too, as its **Move Workbench's files** step, and the costs import moves the costs files by itself. |
| `completion bash\|zsh\|fish\|powershell` | Print the shell's Tab-completion script. The zsh (macOS) and bash (Linux) startup files Workbench applies load it already. |

Tab completes commands, flags and their values: `--by`, `--sort`, `--tool` and
`--language` offer their choices, `init --ask` the saved answers it can ask
again, `revert --checkpoint` the saved checkpoints with when each was saved,
and a project `PATH` folders only; a flag that takes no file offers no files.
A shell that was already open read its startup file before the apply changed
it, so an apply that changes `~/.zshrc` or `~/.bashrc` ends by saying to open a
new terminal or run `exec zsh` (`exec bash` on Linux). The shell startup file saves the script once per installed `workbench` in
Workbench's cache folder (`~/Library/Caches/workbench` on macOS,
`~/.cache/workbench` on Linux; `WORKBENCH_CACHE_DIR` or an absolute
`XDG_CACHE_HOME` moves it), private, and loads that copy, so a new shell does not run
`workbench` again until an update changes it. Bash needs the bash-completion
package, which Ubuntu installs; without it the shell starts as before, with no
Workbench completion.

`update` and `version --list` read the releases from GitHub. They need no
login; with a logged-in `gh`, Workbench asks `gh auth token` for its token, which
raises GitHub's rate limit. A download fails when no data has arrived for a
minute, not after a fixed total time, so a slow link works. A token GitHub
refuses (HTTP 401 or 403) is reported as such and names `gh auth status`,
`GH_TOKEN` and `GITHUB_TOKEN`; a used-up rate limit says so. VERSION must be a
release tag such as `v0.2.0` (`0.2.0` is accepted); an empty or malformed value
exits 2. `update` ends with
`[WorkBench] Installed vX and its tools; nothing was applied: run workbench apply`.
When that release is already installed it installs no release, still completes
any missing tool, and says `[WorkBench] vX is already installed; nothing was
applied: run workbench apply`, so rerunning it finishes a setup that was stopped.
Without a VERSION, a published release only moves forward: one newer than
GitHub's latest is kept. A local build, one not built by the release workflow,
is not on the release channel, so when it is the installed release `update`
replaces it with the latest published release whatever their versions, and
says so: `Replacing local build v0.2.1-dev.1 with v0.2.0, the latest published
release`. `--dry-run` shows it, with or without `--json`, and a real run prints
it on stderr. Name a VERSION to install another published release.
GitHub's latest release is the newest full release (`vX.Y.Z`), never a
pre-release (`vX.Y.Z-rc.N`), so a machine moves to a pre-release only when it
names one: `workbench update v0.2.1-rc.1`, or the installer with `| sh -s --
--version v0.2.1-rc.1`. A pre-release is published, so `update` without a
VERSION keeps it until `v0.2.1` is out, then installs that, since
`v0.2.1-rc.1` sorts below `v0.2.1`. `version --list` shows full releases only,
and an installed release it lacks, such as a pre-release, on its own line; `gh
release list -R Sawmonabo/workbench` and the repository's releases page also
show pre-releases. See [ADR 0011](adr/0011-release-channels.md).
The installed release runs the update, so one older than v0.1.8 first prints its
own `Installed Workbench vX; continuing with setup and apply`; the new release
then installs only its tools, as above. A staged copy of a release that
was modified is replaced with the verified bundle, except the active release's
own folder, which is left alone and reported as a conflict; reinstalling the
active release removes no older release. The hidden
`--bundle FILE` takes a local archive or HTTPS URL instead, which is what
`install.sh` passes.

`revert` restores files only. It does not run an old executable or reverse old
scripts. At a terminal it shows the saved checkpoints, newest first, as "undo
the apply of …" (the files before that apply) or "redo the apply of …" (the
files a revert replaced). Without a terminal it lists them with their IDs and
asks for `--checkpoint ID`; `--dry-run` shows the restore plan.

Developers inside a checkout add the hidden `--local-build` (no value) to
`apply`, `update`, `doctor` and `project configure` to use that checkout instead
of the installed release. With it, `update` installs the tools the checkout's
`versions.toml` pins when they are missing, and nothing else: no release is
downloaded, the machine is not applied, and it takes no VERSION or `--bundle`
(exit 2). `--dry-run` shows those tools and the plan digest. Running it is the
go-ahead, as for any update, so a coding agent can run it after a pin changes;
each tool is checked against the digest the checkout pins before it is kept. It
ends with `[WorkBench] Installed this checkout's tools; run workbench apply
--local-build`, or with `[WorkBench] This checkout's tools are in place; run
workbench apply --local-build` when nothing was missing. `apply` installs the
same tools when they are missing (at a terminal, or with `--yes`, as for any
apply) and uses your saved answers as they are; save them with
`init --answers-from FILE`. `apply`, `update` and `revert` take the hidden
`--destination PATH`, an existing folder to configure instead of your home.
Full provisioning requires the real home destination because native scripts
have external effects. See
[configuration ownership](chezmoi-local-overrides.md). `init --answers-from FILE`
saves the `[data]` table of an existing chezmoi config as machine answers (FILE
must be private, mode 0600, since answers can hold tokens), and
`init --ask KEY` asks one saved answer again; see
[switching from dotfiles](switch-from-dotfiles.md). `init --ask` needs a
terminal and refuses `--dry-run`, because it saves what it asks.

`costs` figures are list-price equivalents, not subscription charges. The
ledger lives in Workbench's data folder, at `costs/ledger.sqlite`
(`~/Library/Application Support/workbench/data` on macOS,
`~/.local/share/workbench` on Linux; `WORKBENCH_DATA_DIR` or an absolute
`XDG_DATA_HOME` moves it with the rest), because Claude Code deletes
transcripts after about 30 days and the ledger is the only lasting record;
Workbench never deletes or rebuilds it, and only refines rows it recorded
(attribution, service tier, duplicate copies). It is private: 0600, in a 0700
folder. Earlier releases kept it in `~/.local/share/claude-costs`; the next
`apply`, or the first import after an update, moves it, and `costs` asks for
`workbench migrate --apply` until then. An old ledger that is a link is never
moved: the import stops and `workbench migrate` names it until you put the
file it names in its place or remove the link. The first `costs` or
`costs status` for a tool that has transcripts but no rows in the ledger
ingests them once inline. Manual rate overrides go in `costs/rates.json` in
Workbench's config folder, keyed by model
prefix, with any of `input`, `output`, `cache_write_5m`, `cache_write_1h` and
`cache_read` in USD per million tokens, each from 0 to 1,000,000.
The account table names each row's email and plan only from what Claude Code
and Codex wrote down; usage they never recorded shows `not recorded`, with a
note saying since when each tool records it. A Claude Code row belongs to the
organization its transcript names, whoever was signed in when Workbench read
it: an organization Workbench has not seen signed in shows `unknown account
(org 1234abcd)` until you sign in to that account once, which names all of its
rows. Claude Code usage from before it recorded the account counts under its
account when the records between two sign-ins show which account was signed
in, and a later record that contradicts it takes it back. Those records go when Claude Code deletes the transcript that
holds them, so the ledger keeps them as it reads them. See the
[ledger design](superpowers/specs/2026-09-30-claude-costs-ledger-design.md) and
[subscriptions](superpowers/specs/2026-10-01-codex-costs-design.md).

Claude Code and Codex shell commands are guarded against running the machine
out of memory. A `Bash` hook notes each command and starts one watcher per
user. When memory is nearly gone, the watcher stops the largest command an
agent started, with everything it started: `SIGTERM` first, `SIGKILL` after 5
seconds or when memory is nearly exhausted. Nearly gone means macOS's
critical memory pressure (its warning level never stops anything); on Linux
and WSL2, at most a tenth of usable memory left with at most a tenth of swap
free (no swap counts as none free), or memory stalling half the time with at
most a fifth left for 20 seconds. At that line the largest command is stopped
only when the agent commands together hold at least a tenth of installed
memory or 1 GiB, whichever is less, since smaller ones can't end the
shortage; at the forced line (macOS critical pressure lasting 2 seconds or
with at most a tenth of swap free; on Linux and WSL2 at most 5 % of usable
memory left and 5 % of swap free) the largest is stopped whatever its size.
On Linux and WSL2, inside a memory-limited container or unit (cgroup v2),
the limit counts as the machine's memory. The agent is told why in the tool
result. Commands sent to the background are guarded too: the watcher keeps
their note until the command ends, or drops it about a second after the tool
result when no command matched it, and a stop after the tool result has
returned is reported only in the log. It never
stops the agent, its MCP servers, hooks or anything else, never changes how a
command runs, and a guard fault leaves a command unguarded and never blocks it.
Programs a command detaches from itself (`nohup` with `setsid`) are not
tracked. `WORKBENCH_GUARD=off` in the agent's environment turns it off;
`WORKBENCH_GUARD=dry-run` only logs what it would stop. The notes, the log
(`watch.log`) and the watcher's own output (`watch.err`, and `watch.err.1`
once that passes 1 MiB) are in Workbench's state folder under `guard/`. The hooks and
the watcher are hidden `workbench guard` commands. After `workbench update` (or
an apply) installs another release, the running watcher hands over to that
release's watcher within about a second, once no stop is under way or
settling: the new one keeps watching the same commands, and the log shows the
handover. When the new release cannot take over, the old watcher keeps
watching and logs why once. A watcher from a build without handover keeps
running its own code until it has been idle for 10 minutes; `kill` (its
`SIGTERM`) stops it after any stop under way, and the next command's hook
starts the installed release's.

`workbench guard logs` shows what the guard did, oldest first, one line per
event: the time in the machine's time zone, the level, the event, then
`key=value` fields in this order, each left out when it has no value:
`id agent subagent project by dry_run took size peak floor level available
total still_running release old_release new_release new_pid msg error process
cmd`. `id` is the first 8 characters of the tool call ID after its `toolu_` or
`call_` prefix and any provider segment such as `bdrk_` or `vrtx_`; `project`
is the folder the command ran in; `cmd` is the command without a leading
`cd PATH &&` or `cd PATH;`, last because it is the longest. Sizes are binary
(1 GB is 1024 MB) and whole `MB` below 1 GB; from 1 GB a reading that moves
(`size`, `peak`, `available`) has two decimals (`2.41GB`) and a fixed total
(`floor`, `total`) is whole (`16GB`), or has two decimals when it is not
whole (`1.86GB`); `took` has one decimal below 10 s, whole seconds below a
minute, else `1m12s`; `level` is macOS's memory pressure level, `available`
and `total` Linux's memory. `msg` shows only for `memory`, `refused`,
`gave-up`, `error` and `raw`, where the event alone does not say what
happened. A value holding a space, `"`, `=`, `\` or a control character is
written in Go's double quotes, so a command with a newline stays on one line and
logfmt readers split the fields. The events are each command that ended, matched
no known shape (`unmatched`) or was stopped (`stopping`, then `forced` or
`gave-up` if it comes to that), low memory that stopped nothing, calls dropped
without their post hook, the watcher starting, handing over to a new release,
going idle and exiting, errors, and any line of the watcher's own output, such
as a crash's trace, as a `raw` event with its line in `msg` and no time, placed
at the start of the watcher that wrote it. Commands the watcher only started
tracking are left out; with `--follow`, one still running 5 s after the watcher
found it gets one `running` line, without `took` or `peak`.

```text
Oct 17 14:41:05 INFO  ended     id=4fQ8nX2a agent=claude subagent=Explore project=feature-login took=53s peak=2.41GB cmd="pnpm typecheck 2>&1 | tail -15"
Oct 17 14:41:20 WARN  unmatched id=7Kp3Rt9m agent=claude project=feature-login took=2.0s cmd=./scripts/nap.sh
Oct 17 14:42:31 WARN  stopping  id=2Bv6Yc1d agent=claude project=api-server by=guard took=1m12s size=6.21GB peak=6.21GB available=1.12GB total=16GB cmd="cargo test --workspace"
Oct 17 14:42:36 WARN  forced    id=2Bv6Yc1d agent=claude project=api-server cmd="cargo test --workspace"
Oct 17 14:43:02 WARN  memory    size=740MB floor=1GB available=1.12GB total=16GB msg="memory is low, but the agent commands are too small to stop"
Oct 17 14:44:40 WARN  stopping  id=9Hs5Wq4e agent=claude project=feature-login by=you took=8.0s size=1.31GB peak=1.31GB cmd="pnpm vitest run"
Oct 17 14:45:10 ERROR error     msg="cannot read a process" error="open /proc/81234/stat: permission denied" process=81234
Oct 17 14:50:00 INFO  idle
```

A long line soft-wraps on screen and stays one line, so `grep` and copying
keep the whole event. At a terminal the time and the keys are faint, the level
alone is colored (INFO green, WARN gold and bold, ERROR red and bold), the
`agent` wears its agent's hue, `by=you` and a `took` of 10 s or more are bold,
and a `size` or `peak` is bold, gold from 1 GB and red from 4 GB; the event,
`id` and other values stay plain. Piped output, `TERM=dumb` and `NO_COLOR` get
plain text. With nothing to show it says so on stderr and exits 0.

```bash
workbench guard logs                        # one line per event
workbench guard logs --since 2h             # a Go duration before now
workbench guard logs --since "2026-10-16 21:00"
workbench guard logs -f                     # keep printing new events
workbench guard logs --json --since today   # the log's own JSON Lines records
```

`--since` takes a duration (`10m`, `2h`), `today`, `yesterday`, a date, date
and time or time of today in the machine's time zone (`2026-10-16`,
`2026-10-16 21:00`, `21:00`), or an RFC 3339 time; a line of the watcher's
output, which has no time, is kept unless the next watcher started before
then. `--follow` (`-f`) waits for files that do not exist yet and keeps its
place by byte offset, so a cut of the log (to its newest half, or a
migration's that drops lines from the middle) and a rotation of the output
neither skip nor repeat a line, even lines that read the same. The log is
JSON Lines: each record has `time`, `level`, `msg`, `event` (`started`,
`handover`, `idle`, `exited`, `tracking`, `ended`, `unmatched`, `abandoned`,
`memory`, `stopping`, `forced`, `gave-up`, `refused`, `error`), `part` and
`pid` (a log that was cut starts with a `cut` record, which the viewer never
shows), and, by event, `call`, `agent`, `subagent`, `cwd`, `project`,
`command`, `root_pid`, `by`, `took`, `size`, `peak`, `children`, `reason`,
`memory`, `floor`, `dry_run`, `still_running`, `release`, `old_release`,
`new_release`, `new_pid`, `process` and `error`; `--json` prints the matching
records unchanged, one per line, `tracking` records and full call IDs
included, and a
line of the watcher's output as
`{"level":"ERROR","msg":"<the line>","event":"raw","started":"<its watcher's start>"}`.
See the [guard design](superpowers/specs/2026-10-14-agent-command-guard-design.md),
section 4.1.

`workbench guard memory` shows, live and full screen, each command the watcher
tracks, newest first: its short ID, status (`● run`, `◌ stopping`), agent,
subagent, run time, memory now and at its peak, project and command, then,
dimmed for a minute, those that ended (`✓ done`, `? unmatched`, `✗ stopped`).
The header's first line gives the watcher's state (`● watching`, `○ not
running`, `○ not connected`), a `memory low` or `memory critical` badge when
memory runs short, and the clock; below a blank line, the counts (`3 running · 2
finished`) and, while a watcher answers, the memory in use of the installed
memory (`mem 11.27/16 GB`), the largest peak and the swap in use; then a line
that says `dry run: stops are logged, not sent` when every shown command is a
dry run, or `dry run on 2 of 5 commands: …` when some are, each marked `(dry)`
in its status. The selected row lies on a slate band across the window, its `▸`
and ID bold in a light steel; a light terminal gets a light band, and without
color the row is bold instead. ↑ and ↓ move (there is no `j` or `k`, since `k`
stops, as in htop), space tags a running command and moves down, enter, → or `l`
opens the selected one's details (the full command, folder, full ID, its
processes and their memory, and a graph of its memory over the last 120
readings), `k` opens the stop menu for the tagged commands or the selected one,
with `Stop (polite, force-killed after 5 s)` chosen and `Force kill now` below
it, esc, ← or `h` closes the menu or the details, and `q`, esc on the list or
ctrl+c quits. On the list the mouse wheel moves as ↑ and ↓ do, a click selects a
row and a click on the selected row opens its details; the view takes the mouse
so a trackpad no longer scrolls the terminal's own buffer; to select text, hold
Shift (Option in Terminal.app and iTerm2). The details lay a long command out
one step per line, each `|`, `&&` or `||` outside quotes starting a line two
spaces in (`go test ./... 2>&1`, then `| tail -20`), and a `;` ends its line; a
test in `[[ ]]`, a `{ }` group and a command with a here-document stay whole.
When the details do not fit, the command's first line, folder, status, memory
and graph stay, and one `… N more` line counts the rest. The watcher stops the
command the way it stops for memory, logs it `by=you` and tells the agent
`Stopped by you (workbench guard memory).`; the bottom line shows its answer for
3 seconds (`stopped 4fQ8nX2a`, `logged 4fQ8nX2a (dry run)`, `4fQ8nX2a already
finished`). The watcher serves the view on `guard.sock` beside its log, only to
the same user, and never takes a stop from a process still under an agent with a
guarded command. In a window under 60 columns or 11 lines the view asks to be
widened or made taller; narrower windows first shorten COMMAND, then drop
SUBAGENT, PROJECT, PEAK and AGENT. While no watcher runs it says so, keeps the
last rows dimmed and reconnects every second; the watcher starts with the next
agent command. Without a terminal on stdin and stdout, or with
`--non-interactive`, it exits 3 with `guard memory needs a terminal; use
workbench guard logs --follow`; with `--json`, which it has no output for, it
exits 3 and points at `guard logs --json`; and under a coding agent
(`CLAUDECODE` or a Codex variable, as `apply` detects) it exits 3 with `guard
memory is for a person at a terminal`.

```bash
workbench guard memory                      # watch, tag and stop agent commands
```

Workbench installs Claude Code's settings, instructions and hooks in
`~/.claude` and Codex's in `~/.codex`, and never sets `CLAUDE_CONFIG_DIR` or
`CODEX_HOME`. Either variable moves its tool to another folder, often one
per account (`alias claude-work='CLAUDE_CONFIG_DIR=~/.claude-work claude'`),
and the tool then reads none of what Workbench installs. Workbench leaves that
folder alone, as installing there would pick one of your accounts for you.
When the variable points elsewhere, set in Workbench's environment or, for
`CLAUDE_CONFIG_DIR`, in the `env` of Claude Code's user or managed settings
files (where Claude Code also takes it from), the `apply` plan warns, `doctor`
reports `claude-config-dir` or `codex-home` as a conflict, and `costs status`
checks the hooks in that folder and adds a `config` line, each naming the
variable, where it is set and how to undo it. A session started with the variable only
in that session's environment, such as through the alias, is invisible to
Workbench. The costs ingest hook follows the folder a Claude Code session's
transcript is in when the variable was stripped from the hook's environment
(`CLAUDE_CODE_SUBPROCESS_ENV_SCRUB`). See
[ADR 0010](adr/0010-agent-config-folder-moves-are-reported.md).

## Approval and automation

Each planned file carries a plain `title`, whether Workbench `merged` its settings into the file (your own keys are kept) or owns the whole file, its `added` and `removed` line counts, for a merged JSON or TOML file the number of `settings` that change, and a `summary`. A file Workbench owns that you edited since it last wrote it (by its last checkpoint, else chezmoi's own record of what it wrote) is marked `edited_outside`, and applying replaces your edit. The plan view also shows each file's diff (secrets masked); the diff text is for the screen only and is not in `--json` or the plan digest. For a merged JSON or TOML file whose old and new content both parse, the view lists the changed settings instead of lines (`~ hooks.SessionStart[0].hooks[0].timeout  10 → 5`, `+ … added`, `- … removed`; a list of plain values is compared as a set) and counts them as `N settings changed`, because the merge re-orders the whole file; any other file shows its line diff.

Deciding once. The first approved `apply` shows the plan and saves two things in the `[effects]` table of the private `machine.toml`: the steps you turned off (`skip`, plus `select` for optional steps you turned on) and the steps it showed you something to decide about (`decided`; a step whose check found nothing to change sits on the `Already set` line, where it cannot be turned off, so it is not recorded until it has something to do). Later `apply` runs print the plan and apply your saved choices without asking, unless a step is new (not in `decided`, `skip` or `select`) and has something to do, or a file Workbench owns whole was edited outside it. Until a first approved apply has saved `decided`, no step is marked new and the first `apply` shows the plan once. Applying without asking needs a person at a terminal to read the plan on; with none (a cron job) `apply` refuses unless you pass `--approve-plan DIGEST` or `--yes`. A coding agent never counts as a person, even in a pseudo-terminal: when `CLAUDECODE=1` (Claude Code) or `CODEX_CI`, `CODEX_THREAD_ID`, `CODEX_SANDBOX` or `CODEX_SANDBOX_NETWORK_DISABLED` (Codex) is set, `apply` shows nothing to answer and refuses with `An agent runs this; use --dry-run --json then --approve-plan`, `--yes` and `--choose` are refused the same way (an agent applies only with `--approve-plan DIGEST`), and so are `init --ask` and the Yes or No approval of `revert` and `project` (`revert` without `--checkpoint` still lists the checkpoint IDs). A run that nobody can approve (no `--approve-plan`, no `--yes`, no person at a terminal) is refused with exit 3 before Workbench downloads its tools or saves anything. Files Workbench merges into (Claude Code settings, Codex config, VS Code settings) never count as edited outside, since Claude Code and Codex rewrite them constantly. `apply --choose` always asks. `apply --reset` forgets `skip`, `select` and `decided` for this host's steps and asks again with the defaults; saved choices for steps this host does not list, such as a macOS step on Linux, are kept and ignored. A saved `select` for a step this host does not offer is shown as a plan warning and ignored; `--reset` forgets it. `--yes` never asks. `--choose`, `--reset` or `--approve-plan` with `--yes` is refused (`--reset` forgets what you turned off, so `--yes` would turn it all back on unasked, and `--approve-plan` already approves), and `--choose` needs a terminal (exit 3 without one; with `--json` or `--non-interactive` it is an invalid combination, exit 2). An isolated `--destination` never saves choices, so each interactive apply there asks. `--dry-run` never saves. Steps are gated one by one: a shared script with one step off still runs its other sections. On macOS that covers Homebrew packages and Work tools (the Bitwarden CLI), and Mac apps and editor extensions and Work tools (the work editor extension); on Linux, System packages and Work tools (the Bitwarden CLI), and VS Code extensions and Work tools (the work editor extension); on WSL, Windows setup and WSL networking.

What the plan shows. One view serves the interactive list and `--dry-run`: a header with counts (files, steps that will run, new to decide), the files, then `Will run`, `Off` (steps you turned off), `Optional` and a faint `Already set` line, names only, for steps whose check found nothing to change (the cursor can rest on one to read what is already in place, but it cannot be turned off). Each step is its plain name with one faint line under it; the cursor row opens a detail panel (what it does, what it would do on this machine now, what it changes, who it runs as, how to undo it). A file's diff and a merged file's settings list have these masked: known secrets Workbench holds; the value of a key whose name contains token, secret, password, passwd, passphrase, api key, access key, private key, credential or authorization, or ends in key, pat, auth, cookie, session, pass or pwd (`DB_PASS`, `MYSQL_PWD`), judged on the key's last name however long the path, and everything under a table named that way; a `Bearer` value; the value after a command-line flag named like that (`--token x`, `--db-pass x`), and after `-p` or `-P` unless it is a path or variable (`mkdir -p ~/x` stays), with `-psecret` attached only on a line naming mysql, mariadb, sshpass or mongo; the password in `https://user:password@host`, and a key of 16 or more characters used as the user (a Sentry DSN); a Slack, Discord, Microsoft Teams or Zapier webhook path or any `/webhook/` path; a URL query value named like a credential (`?client_secret=`, `&sig=`); and token shapes (GitHub, GitLab, OpenAI, AWS, Slack, JWT, 40 or more hex characters, a 40 or more character letters-and-digits run). Other values are shown as they are, so a credential with none of these shapes under an ordinary key name is not masked. The diff is printed only at a terminal: never to a pipe or a file, not even with `--verbose`. A step that cannot apply on this host, such as the Linux editor extensions on WSL, is not listed. A step that runs from a script chezmoi reruns when it changes is listed again on the first apply after a release changes that script (the language runtimes, command-line tools and tmux plugin steps, for one), even when nothing else is new. Steps that always run with the files, such as AI safety settings and, on a work machine, Bitwarden session caching, show without a box. Keys: up and down move, space turns a step on or off, enter opens the detail (a file's diff, full screen; `q` or esc to return), `a` applies, `q` or ctrl+c quits with nothing applied; a mouse click selects a row and clicking the selected row toggles it. Closing the list prints its final frame once. A saved plan run without a prompt prints a compact view headed `Applying your saved choices`. When the checked steps will need the Mac password, a line under the header says so and why (`Applying at a terminal asks for your Mac password once, before it starts, to install Homebrew.`, or, where sudo uses Touch ID, `Applying at a terminal asks for Touch ID (your Mac password where Touch ID cannot be used) before it starts, to install Homebrew.`); it follows the boxes. Add `--verbose` to see each row's detail panel and the recovery limits.

Ticking a step is the approval: scripts Workbench runs during apply never stop to ask a second question. The one thing an apply asks besides the plan is the Mac password, at most once: before it starts when it has to install Homebrew, otherwise the first time Homebrew or the `touch-id-sudo` step needs it (where Touch ID for sudo is on, sudo asks instead: see The Mac password). What a step would change is in its detail panel beforehand (WSL networking lists each `.wslconfig` setting it will change, for example `networkingMode is virtioproxy, will be mirrored`), and the recovery copy of a file it replaces is still written.

`revert` and `project` still ask "Approve this exact plan?" with Yes and No (y or n, or the arrow keys and Enter; Enter alone, esc and ctrl+c refuse).

On WSL, Windows setup is an optional step: it starts off, and ticking it is remembered. It does not include Windows Terminal settings, the PowerShell profile or Windows fonts. Those three are separate optional steps under `Optional`, off until you tick each one, and ticking Windows setup ticks none of them. A part you tick installs what it needs itself. The other Windows steps (default distribution, PATH) and kernel tuning (swapping) are optional too. WSL networking is the one Windows step that starts on; it keeps your other `.wslconfig` settings and saves a copy of the file first. On a Mac, Touch ID for sudo is an optional step (`touch-id-sudo`): off until you tick it, it turns on Touch ID for `sudo` in every terminal, not only Workbench's, by writing `/etc/pam.d/sudo_local`, and does nothing when Touch ID for sudo is already on (the check then shows it on the `Already set` line). Linux has no optional steps. Unattended runs see the same selection through `--dry-run --json`.

Probes. Each step's line is what its script would do on this machine, found by running the script in a read-only probe mode before the plan. A script reports `NAME: = TEXT` when it has nothing to do, `NAME: TEXT` for a change and `NAME: ? TEXT` when it could not check, with the reason as `TEXT`. A probe that fails or takes longer than 15 seconds does not block the plan: the step says in plain words what could not be checked, for example "Windows didn't answer in time", and apply checks again when it runs. A step that could not be checked is never shown as a change or as nothing to change. The two WSL Windows scripts make their calls to Windows together (an 8 second limit for all of them in a probe) so that a slow first call after a quiet spell still fits the 15 seconds; a call that does not answer in time or fails gives that step the "could not check" note. Probes write nothing. An apply caps every call it makes to Windows at 60 seconds, and a call that fails or does not answer blocks only the step it belongs to: the script says so, the other steps still run, and that step's result line reads `blocked` with the reason in plain words (`PowerShell didn't report where your profile is (Windows didn't answer within 60 seconds); your profile was not changed`). A Windows side that cannot be used at all (no interop, not x64) blocks every Windows step the same way. A failed Windows download (oh-my-posh, fonts, ripgrep, Notepad++ themes) blocks the steps that need it, and kernel tuning without sudo blocks its own step; the files and the other steps still apply. The apply then exits 3 after finishing the rest. The Mac apps and editor extension scripts work the same way: an app that did not install, an update Homebrew did not finish (blocking each `update-<name>` step it covered) or an extension that still fails after a second try blocks only its own step, with the reason, and the next apply tries again. The calls' temporary files are removed and any call still running is stopped when the script ends, is interrupted or fails.

While Workbench plans, rechecks an approved plan, refreshes Homebrew's
package list or sets up chezmoi, uv and Python, a live line on stderr names the
current step and the time so far, for example `⠧ Planning: asking Homebrew for updates (2s)`; it
clears before any prompt. Without a terminal, and in JSON or non-interactive
mode, each step prints as its own line instead. An interactive apply then hands the terminal to native
provisioning, so installers can prompt and you see their output as it
runs, and takes it back when they finish. JSON mode does not
prompt; it streams redacted provisioning output on stderr.

The Mac password. On a Mac, an `apply` at a terminal asks for the Mac password at
most once (where Touch ID for sudo is on it asks nothing itself: see the next
paragraph). When its checked steps have to install Homebrew, it asks after you
approve and before it changes anything, because Homebrew's installer cannot ask
for it. Otherwise it asks the first time a Homebrew command needs it, for
example when an app such as Docker Desktop installs or updates, or the first time
the optional `touch-id-sudo` step's `sudo` does. Workbench reads
the password itself, without echo, and checks it with `sudo` before it
remembers it, so a wrong one is asked again. A sudo ticket that is already valid
(a recent `sudo`, or passwordless sudo) is used for the up-front step and
nothing is asked there. Homebrew drops earlier sudo approvals each time it runs,
on purpose, so Workbench keeps the password, not an approval, and answers each
later request of the apply with it through `SUDO_ASKPASS`, given to Homebrew's
commands and the `touch-id-sudo` step's one `sudo` command only. The password
stays in Workbench's memory until the apply ends; it is never written to a file,
the environment, a command's arguments, a log, the plan or `--json`. The helper
that passes it on answers only programs the apply started, and its folder is
removed when the apply ends, however it ends. Three wrong passwords, an account that is not an administrator
or a refusal end the apply as blocked (exit 3) and ctrl+c as interrupted
(exit 130), with nothing changed when it happens up front. Without a terminal,
which includes every `--approve-plan` run by a script or a coding agent, nobody
is asked and no helper is made: a valid ticket is used, and without one the
apply is refused (exit 3, before any change) only when Homebrew has to be
installed, because its installer cannot ask; apps and app updates go ahead as
before, with Homebrew asking for the password itself.

Touch ID for sudo. Where `/etc/pam.d/sudo_local` or `/etc/pam.d/sudo` has an
active `auth` line naming `pam_tid.so`, `sudo` asks for Touch ID before any
password, so an apply at a terminal does not ask for the password at all:
Workbench reads none and makes no `SUDO_ASKPASS` helper. When the checked steps
have to install Homebrew and no sudo approval is valid, Workbench runs
`sudo -v` at the terminal before it changes anything, after one line saying
why, and sudo asks for your fingerprint, or with its own password prompt where
Touch ID cannot be used (over SSH, in tmux, with the lid closed, with no
sensor). Homebrew's installer then uses that approval. Later, Homebrew's own
`sudo` asks again, by Touch ID or password, each time an app such as Docker
Desktop needs administrator rights, because Homebrew cancels earlier approvals,
so stay at the Mac for those steps. Three wrong passwords, an account that is
not an administrator or a refusal end the apply as blocked (exit 3) and ctrl+c
as interrupted (exit 130), with nothing changed; an approval that Workbench's
`sudo -v` created is dropped when the apply ends. Unattended runs and Linux do
not change. The optional `touch-id-sudo` step turns Touch ID for sudo on: with no
`/etc/pam.d/sudo_local` it creates the file from Apple's
`sudo_local.template` with its `pam_tid.so` line switched on (owned by root,
read-only, as the template is); with one already there it switches on or adds
only that line and keeps every other line. It never edits `/etc/pam.d/sudo`. Its
one `sudo` command gets the password the way any other `sudo` in the apply
does; without a terminal it never asks, so it needs a valid sudo ticket and is
otherwise blocked. Revert does not undo it: delete `/etc/pam.d/sudo_local`, or comment out
its `pam_tid.so` line again. A system update keeps the file.

Unattended runs. Agents and scripts read the plan first, then apply exactly that plan:

```sh
workbench apply --dry-run --json      # read .plan_digest
workbench apply --approve-plan DIGEST # applies exactly that plan, exit 4 if it changed
workbench init --answers-from FILE --dry-run --json   # same pattern for init
workbench init --answers-from FILE --approve-plan DIGEST
```

The digest covers the files, the effects and which are checked, so a plan
approved from a dry run cannot apply a different selection. If the machine, the
release or the saved selection changed since, `--approve-plan` exits 4 with
`Approval digest does not match the current plan; review a new plan` and
changes nothing. `--approve-plan` is listed in `--help`. From a checkout, add
`--local-build` to both `apply` calls. Keep it and `--destination` identical between
them. `--yes` is for a person who trusts your saved choices, not for a caller that
did not read the plan, and a coding agent is refused it. When Workbench's own tools
are missing, `apply --dry-run` exits 3 and says to run `workbench update`, or
`workbench apply` at a terminal; from a checkout it says to run
`workbench update --local-build`, which installs the tools it pins and which an
agent may run, for example after a pin changes.

`apply` and `update` ask nothing about installing Workbench and its pinned
tools: running the command is the go-ahead, they change only Workbench's own
files (on a Mac, `apply` also refreshes Homebrew's package list, below), and
`update` keeps the replaced release. Setup asks only the machine
questions your saved answers lack; each asks once, so nothing already answered
is asked again. Esc or ctrl+c at a question stops with nothing saved, and the
next `workbench apply` asks again. To change an answer, name it with
`workbench init --ask KEY`: `workbench init --ask machine_role` asks the role
again, plus any question the new role needs (both emails for `both`, the tokens
for `work`); the next `apply` shows the plan. Answers the new role doesn't use
are dropped. Without a terminal, setup needs saved answers, which
`init --answers-from FILE` provides. Nothing grants unspecified external
effects: the plan's digest names the effects it runs.

At a terminal, results mark each part ✓ (done), · (nothing to do) or ✗ (not
done) in color, and a successful run ends with a green `[WorkBench]` line
saying what it achieved, for example
`[WorkBench] Applied: 3 files, 4 steps; 1 skipped`, or, when the files match and no effect is checked,
`[WorkBench] Nothing to apply; this machine already matches`.
Plans already shown at a prompt are not repeated; `init`, `project configure`
and `project revert` with `--approve-plan` show no prompt, so their result keeps
the plan. Piped output, and `NO_COLOR`,
give the same lines without symbols or color.

A plan lists only the steps this apply would actually run: the provisioning
scripts chezmoi would run (a once-only script that already ran is left out, as
is an on-change script whose content has not changed) and the steps whose files
change. On macOS, every full plan lists its
steps as effects, and checking them approves them. They include
`brew-maintenance`, the Homebrew cleanup dotfiles ran on every apply, and an
`update-<name>` effect for each app or command-line tool in `packages.toml`
that Homebrew reports as outdated, for example `Update docker-desktop 4.89.0 →
4.92.0`. The first version is Homebrew's record, or the app's own version when
the app updated itself past it. A tool's effect also lists the other packages Homebrew would install
or update with it, for example `Update tmux 3.6a → 3.7c through Homebrew; also
installs jemalloc 5.4.0; also updates libevent 2.1.12_1 → 2.1.13`, so Homebrew
does not ask again during apply. Hold one at its version with `brew pin`
(`brew pin --cask <app>` for an app; `brew unpin` releases it). The chezmoi, uv
and Python that Workbench runs stay at the versions it qualified, and so does
any tool whose update would change them; the plan does not mention them.

On every platform the plan also covers Codex and Claude Code. It finds the copy
your shell runs (on Linux and WSL `~/bin`, then bun's globals, nvm's default
Node, Cargo's and Go's bin folders, `~/.local/bin`, then Homebrew's and the
system's folders, the order the managed shell files put them on PATH) and reads
how it was installed and its version from the installer's own files, without
running it. When that version is older than the target, the plan lists
`update-codex` or `update-claude-code`, for example `Update Codex 0.162.0 →
0.163.0 through its standalone installer`. The target is the `versions.toml`
pin, or in latest mode the newest release on the channel the tool's own
updater follows, looked up while planning: Codex's latest channel, and for
Claude Code the `autoUpdatesChannel` in its user settings (in
`CLAUDE_CONFIG_DIR` when that is set; `stable` or `latest`) or its managed
settings files, which override yours. Project settings are not read, as they
depend on the folder Claude Code starts in. Approving it runs that installer:
`npm install -g @openai/codex@VERSION` in the nvm Node it is in, OpenAI's
standalone installer, or `claude install VERSION`, which leaves Claude Code's
channel as it is. A copy as new as the target or newer is left alone; a
prerelease counts as older than its release. On macOS a copy from a Homebrew
cask (`codex`, `claude-code`, `claude-code@latest`) is one of the Homebrew
updates above, at Homebrew's current version, the only one a cask installs. A
copy installed any other way (apt, bun, Homebrew on Linux) is named in the
plan's warnings and left alone, and so is a vendor installer's release run
through a launcher of your own, such as a `~/bin/claude` symlink: each
installer updates only the launcher it makes, `~/.local/bin/claude` or
`~/.local/bin/codex`. A missing copy is installed through npm by the
command-line tools step.

Both tools keep their own background updates where the mode allows. In latest
mode the standalone installer runs with `--release latest`, as `codex update`
does, which keeps Codex updating itself; the apply checks before that the
channel still offers the planned release. In pinned mode it runs with
`--release VERSION`, which turns Codex's background updates off so it stays at
the pin; `codex update` turns them back on. In both modes the release the
installer leaves selected must then be the planned one, or the step is
blocked; in latest mode a newer release published in between may remain
installed.

Claude Code keeps updating itself on its channel. In pinned mode Workbench
sets `minimumVersion` in its settings, the floor below which those updates
install nothing: the pin, or the installed version while that is older, so
the floor reaches the pin at the first apply after the update has run and
never holds back an update that has not. A higher floor already set stays.
Claude Code can run ahead of the pin, which the plan then leaves alone. A
floor stays when you switch to latest mode, where it is at most a version
you already had; Claude Code clears it itself when you switch its channel back
to latest in `/config`.

Before a native Claude Code update the apply checks the GPG signature on
Anthropic's release manifest for that version, downloads that version's binary
for your platform (named as Anthropic's `install.sh` names it) and runs its
`claude install VERSION` only if it matches the manifest. `claude install`
downloads the version again itself, so the binary it installs is checked as
well, even when the install fails: one that does not match is removed, the
launcher points at the previous version again, and the step is blocked. A
`claude` started between the install and that check runs the unchecked binary.
A `minimumVersion` above the target, in your settings or managed settings (the
higher one counts), makes `claude install` install nothing, so the plan then
names it in a warning and lists no Claude Code update.

In latest mode every plan looks the versions up again, including the recheck
an interactive apply runs after you approve: a release published since the
plan you approved, or a lookup that fails at the recheck, changes the plan, so
the approval is refused (exit 4), as it is between `--dry-run` and
`--approve-plan`. Run the apply again to plan the new release. See
[ADR 0009](adr/0009-agent-updates-through-their-own-installer.md).

The update list is as fresh as Homebrew's package index, which planning reads as
it is and never refreshes. An `apply` not given `--approve-plan` runs `brew update`
first, after setting up its tools and before it plans, so the plan lists current
updates; a failure only warns, and the plan uses the index as it was. `apply
--dry-run` and `apply --approve-plan` never refresh, so an approved digest still
matches, and `brew-maintenance` runs `brew update` at the end of an apply that
did not refresh at its start.

`--json` produces one versioned object with `schema_version`, `command`, `status`,
`results`, `warnings`, `errors`, and applicable `operation_id`/`plan_digest`.
Human results use stdout and diagnostics stderr; help, completion and the
`--version` flag remain text. Exit codes: 0 complete/unchanged, 1 check/execution failure, 2 invalid input
or state, 3 blocked prerequisites/support/consent, 4 conflict, 5 partial mutation,
130 interruption. Requested skipped work is not complete. Exit 3 covers every
consent refusal (an agent, no terminal, nothing approved, `--choose` without a
terminal); exit 2 covers flags that contradict each other and an invalid command,
flag or argument, whose message names the reason, for example
`Invalid command, flags or arguments: unknown flag: --bogus; run workbench --help`.
A signal that stops Workbench (SIGINT, SIGTERM, or SIGHUP when the terminal closes)
during a prompt ends it with exit 130 and writes nothing; during an apply the
checkpoint is recorded as partial or failed rather than left running. The files
chezmoi already wrote are recorded as written, after the same group fix a failed
step gets, so revert can undo them. ctrl+c while a setup step runs stops that
step and ends the apply the same way, with exit 130 and `Interrupted; what
already ran is kept, and running workbench apply again finishes the rest.`
If release activation already completed before a later setup/apply failure,
the command reports partial mutation rather than implying nothing changed.

## Existing projects and monorepos

```sh
workbench project inspect .
workbench project configure ./apps/api --language python --dry-run
workbench project configure . --language python --extensions --gitignore --dry-run
workbench project configure . --language python --resolve-dependencies
workbench project revert . --dry-run
workbench project revert .
workbench project revert . --checkpoint CHECKPOINT_ID
```

Only existing uv Python projects/workspaces are configurable. Inspection also
recognizes JavaScript/TypeScript, Rust and Go. Unsupported detected projects are
reported and unchanged; explicitly requesting an unsupported language blocks
before writes. Poetry/PDM/requirements-only projects are not converted.

Workspace members share their identified root lock/configuration owner. Select
that root deliberately; selecting a child cannot authorize sibling/parent edits.
Scanning excludes links, generated/dependency directories and nested repositories;
select a nested repository directly. Git ignore rules are not evaluated. Limits
are 50,000 entries, 32 levels, 1,000 candidates, 8 MiB per metadata file, 16 MiB
of metadata in total and a five-second cancellation deadline; narrow the scope on
incomplete results. Blocked OS calls may outlast it. Lockfiles and configuration
of other languages are listed but never read.

Default configuration needs an existing lockfile and compatible Ruff/basedpyright
development dependencies. `--resolve-dependencies` selects uv dependency/lock
resolution as an external effect; preview still runs none. `--allow-build-hooks`
additionally selects possible native build-code execution and requires resolution.
uv's `--no-build` is not a sandbox. Dynamic/local/direct dependencies and custom
resolver arrangements may remain blocked rather than execute unexpectedly.

`--extensions` merges portable recommendations; it does not install extensions.
`--gitignore` appends relevant absent entries while retaining pattern order.
`--ci` supports a conservative existing GitHub workflow with an explicit
`.python-version` and identifiable existing test job. It preserves that job and
adds owned Ruff/basedpyright checks. Ambiguous owners/unsupported YAML receive
a manual proposal, not a second pipeline or fabricated tests. See the
[portable policy](../project/pythonpolicy/README.md).

## Recovery and runtime storage

Machine and project checkpoints are separate scopes. Recovery checks every
recorded post-image before restoring anything; later user edits or corrupt
images block. A file whose outcome Workbench could not record, because it was
stopped or crashed while chezmoi wrote, is checked when you revert: a file
holding exactly what the apply wrote is restored, one holding exactly what was
there before needs nothing, and the plan says the files were checked now. On a
Mac, a file chezmoi created may still have the group of the temporary folder
Workbench gave it; revert gives it the group the apply approved, then restores
it. A file holding anything else blocks like a later edit. A later edit is
reported with the names of the changed files (`Changed since Workbench wrote
them, so nothing was restored: ~/.gitconfig. …`). Keep the reported checkpoint
ID and resolve conflicts manually; do not delete state to bypass a failure. A
folder the apply created that now holds files Workbench did not write, such as
`~/.config/tmux` with the tmux plugins a later step installed, stays with those
files in it, and so does each folder that holds it; revert restores everything
else and names each one in its plan and result (`Stays, because it holds files
Workbench did not write: ~/.config/tmux (plugins)`). Revert never deletes a
folder's contents.

An apply that stops after it starts writing files is recorded as unfinished.
Rerun `workbench apply`: once a fresh approved plan finishes, the record is
cleared, including when that plan changes no files because they already match.
An unfinished apply of the same destination does not block a later apply, which
replaces the record; the unfinished checkpoint keeps its images and stays
revertable. An unfinished apply of another destination blocks, and so does
reverting a checkpoint other than the one the record names.

A setup step that fails never stops the others: Workbench runs chezmoi with
`--keep-going`, so every other step still runs. A step names what it could not
do (`Runtime managers: blocked — nvm's installer could not be downloaded, so nvm
was not installed`), and a step that needs one that did not finish says what is
missing and skips only what needs it (`Language runtimes: blocked — nvm, uv not
installed yet (earlier setup steps install them), so the runtimes that need them
were skipped`). A script that ends nonzero without saying why is named too: it
stopped with that exit status, and its output above says why. The apply then
ends with exit 3 and `A step could not be done (marked blocked above);
everything else was applied, and the next apply tries it again`. chezmoi does
not record a one-time script that failed as run, so rerunning `workbench apply`
tries those steps again. When a file is not as approved after such a run, or no
step named itself, the apply ends with exit 5 and `Setup did not finish (…); the
other steps ran and what they did is kept. The output above says why; fix that,
then run workbench apply again.` chezmoi writes the files before it runs the
scripts that follow them, so they stay as approved and the checkpoint records
them as written; where native creation gave a file a different group than the
plan approved, only that group is put back, and a file whose content differs is
never touched. When Workbench cannot confirm every file, the message says so;
revert checks those files again and names any it cannot undo.

File recovery does not uninstall tools/extensions, undo runtime upgrades or revert
registry, environment, services, package caches or uncheckpointed script effects.
Supported bytes/types/modes/groups/link targets are recorded, along with the
macOS provenance, quarantine, Finder info and last-used-date attributes; other
extended attributes, ACLs, file flags, hard links and special files block
preservation. The exception is what macOS puts on its own folders: the default
"deny delete" ACL on a folder Workbench writes into or one that holds its files,
and the hidden flag on a folder Workbench writes into, such as `~/Library`.
Native machine apply currently rejects group-exclusive modes such as `0640` and
`0750`, avoiding transient exposure before group correction. The shared direct
project/recovery writer sets the group before permissions and atomic replacement.

Each scope retains the 20 most recent forward checkpoints plus their paired
recovery records. At the limit, the plan lists removal of the oldest settled
checkpoint as a fixed step, `checkpoint-retention`, so your approval covers it.
When none is settled, it names the oldest running, partial or unknown checkpoint
once a newer checkpoint exists, and the plan says "superseded incomplete"; the
checkpoint the recorded unfinished apply names is never removed. Applies that
change no files allocate none. Limits are 256 targets, 8 MiB per image,
32 MiB raw pre/post images per operation, 50 MiB serialized per pair and 1 GiB per
scope. Recovery journal capacity is reserved before forward writes; a recovery
on a full disk can still stop after restoring files (exit 5) with the checkpoint
retained, and reaching the forward ceiling does not prevent recovery. A failed
reservation (a `.prepare-*` folder, which holds no recovery data) is discarded
automatically, a journal slot torn by an interrupted save is skipped for the
other one (only when both are invalid does the scope report "Invalid recovery
journal"), and a stray Finder `.DS_Store` in the scope is ignored.

Runtime directories follow platform/XDG defaults; absolute overrides are
`WORKBENCH_CONFIG_DIR`, `WORKBENCH_DATA_DIR`, `WORKBENCH_STATE_DIR`,
`WORKBENCH_CACHE_DIR`, `WORKBENCH_BIN_DIR`. Exact defaults and supported private
filesystems are in the [contracts](superpowers/specs/workbench-contracts.md#runtime-paths-and-context).
Do not share these overrides with project-owned directories or Windows mounts.

Workbench runs with your umask plus 022, so nothing it, chezmoi or its tools
create is writable by other users, even where the login default is 002, as on
Ubuntu. Missing parents of its directories, such as `~/.local`, are created
0755, except `~/.config`, which is created 0700 as the XDG specification asks.
When the configuration sets such a folder to a different safe mode, the
plan lists the change under "Folder that holds Workbench's own files; mode
only", and revert restores the old mode like any other change. The
configuration keeps `~/.config` private (0700), so a `~/.config` that other
users can list sees that edit. On a Mac it likewise keeps
`~/Library/Application Support` private (0700), as macOS
creates it, so a Mac whose folder was loosened sees that edit, with the folder's
ACL kept. A folder with other flags, ACLs or attributes stops Workbench with the
`chmod` that matches the configuration.
A parent that other users can write to stops Workbench with the `chmod go-w`
that fixes it.

Activation is journaled, not a multi-file atomic transaction. An interrupted
entry-point/state switch fails closed while retaining the prior runtime. Rerun
`update` for the same release; do not manually edit the active-release record.
Existing processes retain their inherited environment: Workbench never injects
PATH changes into running terminals or AI sessions. A private tool or TOML Kit
file is written to a `.NAME.partial` file beside its final path and linked into
place, so an interrupted install leaves no truncated tool; the next `update` or
`apply` removes a leftover `.NAME.partial` and installs again.

Updates hand off to the new runtime with a rebuilt environment: Workbench's own
directories, the terminal and presentation settings (`TERM`, `COLORTERM`,
`NO_COLOR`, `CLICOLOR`, `CLICOLOR_FORCE`, `COLUMNS`, `LANG`, `LC_ALL`,
`LC_CTYPE`, `TMPDIR`), the WSL variables, the proxy and CA settings below and the
coding-agent markers (`CLAUDECODE`, `CODEX_CI`, `CODEX_THREAD_ID`,
`CODEX_SANDBOX`, `CODEX_SANDBOX_NETWORK_DISABLED`). `--verbose`, `--json`,
`--non-interactive` and `--destination` carry over as arguments. Nothing else of
the caller's environment (tokens, `UV_*`, other `GIT_*`) reaches the new runtime.
Standard proxy (`HTTPS_PROXY`, `HTTP_PROXY`, `ALL_PROXY`, `NO_PROXY`, in either
case) and CA (`SSL_CERT_FILE`, `SSL_CERT_DIR`, `CURL_CA_BUNDLE`, and for the
setup scripts' tools `NODE_EXTRA_CA_CERTS`, `CARGO_HTTP_CAINFO` and
`GIT_SSL_CAINFO`) variables reach every download and tool Workbench runs: the
update handoff, the private chezmoi, uv, Python and TOML Kit downloads, project
dependency resolution, Homebrew's package list and the scripts apply runs. Other
ecosystem-specific variables are not carried.

Updates keep storage bounded. The activation plan lists, as `remove` edits,
every staged release except the new one and the one it replaces, plus setup
contexts and private tool versions that nothing kept or recorded uses. They are
deleted after activation succeeds, so the previous release stays available to
reinstall.

## Releases

Pushing a `v*` tag runs `.github/workflows/release.yml`. It first runs the CI
checks and publishes only if they pass, then builds the four
bundles with `scripts/package-release.py --published`, which marks their
executables as published releases, stamps the tag into a copy of
`install.sh`, and publishes all five files as a GitHub release. A tag with a
hyphen, such as `v0.2.1-rc.1`, is a pre-release: the same bundles are
published as a GitHub pre-release, which never becomes the latest release, so
only a machine that names it installs it. Cut one to try unreleased code on a
real machine:

```sh
git tag v0.2.1-rc.1 && git push origin v0.2.1-rc.1
```

A full release's notes are that tag's section of the changelog, which git-cliff (a pinned release,
checked against its SHA-256 by `scripts/git-cliff.sh`) builds from the
conventional commit subjects (`feat:`, `fix:`, `docs:` and so on) with
[cliff.toml](../cliff.toml). A pre-release gets no changelog section: its
commits belong to the full release that follows, and its notes list the commits
since the tag before it. Once a full release is out, the workflow commits the
regenerated [CHANGELOG.md](../CHANGELOG.md) to `main`, so pull before your next
commit. It does so only for a tag on `main` that is also the newest full release tag: a
tag off `main` publishes its release and leaves CHANGELOG.md alone, an older
tag leaves it to the newer tag's run, and changelog commits run one at a time.
Build one bundle locally with the pinned Go toolchain and Python 3.11+:

```sh
python3 scripts/package-release.py --version v0.2.1-dev.1 --target darwin-arm64 --output dist
```

A bundle built without `--published` is a local build. Name it after the next
release with a `-dev.N` suffix, such as `v0.2.1-dev.1` after `v0.2.0`, so it
sorts below v0.2.1, the release it leads to; `update` without a VERSION
replaces it with the latest published release. Install local builds only in
disposable homes and CI; unreleased code reaches a real machine as a
pre-release.

Targets are `darwin-arm64`, `darwin-amd64`, `linux-arm64` and `linux-amd64`.
Existing bundle names are not overwritten. The payload holds machine sources,
portable Python policy and linked dependency/Go license notices, never host
answers, logs, Git metadata or checkpoints. No
redistribution license is granted.
