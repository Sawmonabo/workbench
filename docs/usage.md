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

Installing never applies the machine; run `workbench apply` next. At a terminal
the install ends on `✓ Installed vX` (or `· Up to date` when that release was
already installed); piped, its last line says to run `workbench apply`. A fresh
machine's shell does not have `~/.local/bin` on `PATH` yet, so run the first
apply as `~/.local/bin/workbench apply`; that piped line and the other hints
that name `workbench` give the full path until it is. The first apply writes
the shell setup that adds it and ends by telling you to open a new terminal,
which finds `workbench` and the tools it installed.

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
| `update --list` | List the published full releases, marking the latest and the installed one; an installed pre-release or local build follows on its own line. Installs nothing, and refuses a VERSION or another `update` flag beside it. |
| `answers import FILE` | One-time adoption of a chezmoi config's `[data]` table. |
| `answers ask NAME...` | Ask saved machine answers again, for example `machine_role`. |
| `version` | Print this Workbench version, the same as `--version`; a local build adds `(local build)`. |
| `doctor` | Workbench's version (with `(local build)` for one), what is installed and last applied, any apply that did not finish, tool versions and host checks, and a `CLAUDE_CONFIG_DIR` or `CODEX_HOME` that moves an agent away from the settings Workbench installs; then, as `runtime-` lines, Node, Python, Rust, Go and bun (in `pinned` mode against their `versions.toml` pins; on macOS Go and bun come from Homebrew), whether Claude Code and Codex run the `agent costs` and `agent guard` hooks, whether the `agent guard` watcher runs (`agent-guard-watcher`, told from its lock as every entry tells it: `running` when it also answers on its socket, and, when its lock is held but nothing answers, a watcher starting or one that cannot serve, which `agent guard logs` says why), and, as `agent-folders`, a costs or guard folder still at its old path and why it stays. It reads only this machine, never the network, and repairs nothing. Only a missing prerequisite of Workbench's own returns nonzero; a missing runtime or hook is shown, and `apply` fixes it. |
| `revert` | Pick a saved checkpoint, then restore its files after conflict checks. |
| `project inspect/apply/revert [PATH]` | Inspect an existing project or apply Workbench's settings to it; see below. |
| `agent costs` | What Claude Code and Codex spent, by project, from the local ledger the session hooks keep. At a terminal it opens one tab per tool (Claude Code and Codex): ←/→ or Tab and Shift+Tab switch tools, ↑/↓ select a project or model and Enter opens its page (by model or project, day and session), ← or Esc goes back, q quits and leaves the page printed. The mouse wheel moves the selection, a click selects a row and a click on the selected row opens it; the wheel scrolls a page, a report with no row to select and below the last row. With the mouse taken, select text with your terminal's bypass: Terminal.app's View → Allow Mouse Reporting (⌘R) hands the mouse back until toggled again; other terminals use a held key such as Shift or Option. Without a terminal, `--tool NAME` prints one tool's report. |
| `agent costs --by model\|account\|month` | The same by another dimension. `--since`/`--until` (a duration ago such as `2h`, `today`, `yesterday`, or a date, date and time or time of today in this machine's time zone, or RFC 3339, as `agent guard logs --since` takes; a response counts from `--since` up to `--until`, and an `--until` day counts to its end), `--top N`, `--sort cost\|name\|calls`, `--all` (projects outside your role folders, of those that exist; `~/dev` and `~/repos` when no folder answer is saved), `--detail`, `--tokens`, `--no-rollup`, `--csv` and the global `--json` shape the report. |
| `agent costs rates`, `agent costs status` | The merged price card with where each price comes from (`--refresh` refetches the official page into the ledger, and fetches nothing before there is one), and the ledger's coverage, last ingest, hooks and the age of the official prices it keeps. |
| `agent costs ingest --worker` | Read new usage into the ledger now, in the foreground, with progress; the apply's Record AI usage step (`agent-costs-ingest`) runs it too. Without `--worker` it is the hook command Claude Code and Codex run on session start and end: prints nothing on stdout, exits 0, starts the worker detached. When it can't start one, for example because a Workbench folder isn't private, it says why on stderr and records nothing. |
| `agent guard` | A live view of the agent commands the memory usage guard tracks, where you can stop one, and of the commands that finished; see the guard paragraphs below the table. `agent guard logs` prints what the guard did, and `agent guard history` shows or sets how long finished commands are kept. |
| `migrate` | Run the migrations that move files earlier Workbench releases left outside Workbench's folders, or remove what they left that nothing reads (old caches, the guard log's old free-text lines), or move the Jira and GitLab tokens earlier releases saved in `machine.toml` into a new `work.env` (see [Roles, folders and credentials](#roles-folders-and-credentials)), or move the `costs/` and `guard/` folders earlier releases kept in Workbench's data and state folders into `agent/` beside them (see `agent guard` below), or delete the price list files earlier releases downloaded beside the ledger once the ledger holds their prices. It asks the way `apply` does: at a terminal it shows what will run and asks once; without one it needs `--approve-plan DIGEST` or `--yes` (`-y`), and a coding agent must use `--approve-plan`. When only conflicts are left it exits 4 and names each file and what to do with it. `migrate --dry-run` lists each migration and whether it has anything to do, and changes nothing; with `--json` it carries the `plan_digest` for `--approve-plan`. Every `apply` runs them too, as its **Move Workbench's files** step, and the costs import moves the costs files by itself. |
| `completion bash\|zsh\|fish\|powershell` | Print the shell's Tab-completion script. The zsh (macOS) and bash (Linux) startup files Workbench applies load it already. |

Tab completes commands, flags and their values: `--by`, `--sort`, `--tool` and
`--language` offer their choices, `answers ask` the saved answers it can ask
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

`update` and `update --list` read the releases from GitHub. They need no
login; with a logged-in `gh`, Workbench asks `gh auth token` for its token, which
raises GitHub's rate limit. A download fails when no data has arrived for a
minute, not after a fixed total time, so a slow link works. A token GitHub
refuses (HTTP 401 or 403) is reported as such and names `gh auth status`,
`GH_TOKEN` and `GITHUB_TOKEN`; a used-up rate limit says so. VERSION must be a
release tag such as `v0.2.0` (`0.2.0` is accepted); an empty or malformed value
exits 2. At a terminal, `update` draws one live view: `[WorkBench] Update`
and the release it starts from, then one line with a spinner that reads
`Checking for a newer release`, `Downloading` with a bar and the megabytes so
far, then `Installing tools`, and under it an indented row for each of
Workbench's tools (chezmoi, uv, Python, TOML Kit) as it reaches it, with its
version, its own download bar, or `PREVIOUS → NEW` when it replaced a previous
copy. The line ends as `✓ Updated to vX` in green (`✓ Installed vX` on a
machine that had no release), or as `✗ Update failed: NAME` in red with the
reason under it; Ctrl-C leaves `· Update stopped`. With either output piped or
redirected, at a terminal that reports no size, with `--json` or with
`--non-interactive`, it prints component lines and ends with `[WorkBench]
Installed vX and its tools; nothing was applied: run workbench apply`. When
that release is already installed it installs no release, still completes any
missing tool, and ends with `· Up to date`, listing only a tool it had to
fetch, or piped `[WorkBench] vX is already installed; nothing was applied:
run workbench apply`, so rerunning it finishes a setup that was stopped. It
never applies the machine; run `workbench apply` next. See
[ADR 18](adr/0018-update-draws-one-live-view.md).
Without a VERSION, a published release only moves forward: one newer than
GitHub's latest is kept. A local build, one not built by the release workflow,
is not on the release channel, so when it is the installed release `update`
replaces it with the latest published release whatever their versions, and
says so: `Replacing local build v0.2.1-dev.1 with v0.2.0, the latest published
release`. `--dry-run` shows it, with or without `--json`, and a real run
without a terminal prints it on stderr; at a terminal the view's header and
last line show the move. Name a VERSION to install another published release.
GitHub's latest release is the newest full release (`vX.Y.Z`), never a
pre-release (`vX.Y.Z-rc.N`), so a machine moves to a pre-release only when it
names one: `workbench update v0.2.1-rc.1`, or the installer with `| sh -s --
--version v0.2.1-rc.1`. A pre-release is published, so `update` without a
VERSION keeps it until `v0.2.1` is out, then installs that, since
`v0.2.1-rc.1` sorts below `v0.2.1`. `update --list` shows full releases only,
and an installed release it lacks, such as a pre-release, on its own line; `gh
release list -R Sawmonabo/workbench` and the repository's releases page also
show pre-releases. See [ADR 0011](adr/0011-release-channels.md).
The installed release runs the update, so one older than v0.1.8 first prints its
own `Installed Workbench vX; continuing with setup and apply`, and one without
the live view its own handoff line; the new release then installs only its
tools, as above. A staged copy of a release that
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
`apply`, `update`, `doctor` and `project apply` to use that checkout instead
of the installed release. With it, `update` installs the tools the checkout's
`versions.toml` pins when they are missing, and nothing else: no release is
downloaded, the machine is not applied, and it takes no VERSION or `--bundle`
(exit 2). `--dry-run` shows those tools and the plan digest. Running it is the
go-ahead, as for any update, so a coding agent can run it after a pin changes;
each tool is checked against the digest the checkout pins before it is kept. At
a terminal its view ends with `✓ Installed this checkout's tools`, or `· Up to
date: this checkout's tools` when nothing was missing; piped, with `[WorkBench]
Installed this checkout's tools; run workbench apply --local-build`, or with
`[WorkBench] This checkout's tools are in place; run workbench apply
--local-build`. `apply` installs the
same tools when they are missing (at a terminal, or with `--yes`, as for any
apply) and uses your saved answers as they are; save them with
`answers import FILE`. `apply`, `update` and `revert` take the hidden
`--destination PATH`, an existing folder to configure instead of your home.
Full provisioning requires the real home destination because native scripts
have external effects. See
[configuration ownership](chezmoi-local-overrides.md). `answers import FILE`
saves the `[data]` table of an existing chezmoi config as machine answers (FILE
must be private, mode 0600, like `machine.toml` itself), and
`answers ask KEY` asks one saved answer again; see
[switching from dotfiles](switch-from-dotfiles.md). `answers ask` needs a
terminal and refuses `--dry-run`, because it saves what it asks.

`agent costs` figures are list-price equivalents, not subscription charges. The
ledger lives in Workbench's data folder, at `agent/costs/ledger.sqlite`
(`~/Library/Application Support/workbench/data` on macOS,
`~/.local/share/workbench` on Linux; `WORKBENCH_DATA_DIR` or an absolute
`XDG_DATA_HOME` moves it with the rest), because Claude Code deletes
transcripts after about 30 days and the ledger is the only lasting record;
Workbench never deletes or rebuilds it, and only refines rows it recorded
(attribution, service tier, duplicate copies). It is private: 0600, in a 0700
folder. Earlier releases kept it in `~/.local/share/claude-costs`; the next
`apply`, or the first import after an update, moves it, and `agent costs` asks for
`workbench migrate` until then. An old ledger that is a link is never
moved: the import stops and `workbench migrate` names it until you put the
file it names in its place or remove the link. The first `agent costs` or
`agent costs status` for a tool that has transcripts but no rows in the ledger
ingests them once inline. The ledger also keeps the official prices an import
downloads from each tool's price page, at most once a day and only when the
tool's list is over a week old or a model has no price; a page that cannot be
read keeps the prices the ledger has. A model no price covers still counts its
tokens, and its cost shows a dash. `agent costs rates` lists each price with
where it comes from (the official page, the tools' own cost records or the
built-in list); before there is a ledger it shows the last two, and
`--refresh` downloads nothing. A ledger an earlier release wrote takes in the
prices it kept in `claude/rates-official.json` and `codex/rates-official.json`
beside it the first time it is opened, and the **Move Workbench's files** step
then deletes those files. That first open also upgrades such a ledger to the
current version and compacts it, about half a minute for a large one: at a
terminal, `agent costs`, `agent costs status` and `agent costs rates` show each
step and then its outcome on stderr, and `agent costs ingest --worker` shows the
steps in its own progress line.
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
out of memory. A `Bash` hook notes each command (a Claude Code `Monitor`
command too, from Claude Code 2.1.314, the first whose `Bash` hook runs for
`Monitor`) and starts one watcher per user. When memory is nearly gone, the
watcher stops the largest command an agent started, with everything it
started, or the largest program such a command started that then detached
(see below): `SIGTERM` first, `SIGKILL` after 5
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
result. Commands sent to the background, and `Monitor` commands, whose post
hook runs when the monitor starts, are guarded too: the watcher keeps
their note until the command ends, or drops it about a second after the tool
result when no command matched it, and a stop after the tool result has
returned is reported only in the log. The guard itself never stops an agent
it knows (one whose session hook ran or that left a note), the helpers it
starts (a language server, an MCP server), its hooks or anything else. A session it does not know yet
is a program like any other: see the last paragraph on detached work. The only change
to how a command runs is one variable in its environment, and a guard fault
leaves a command unguarded and never blocks it.

A program a command started that then left it (a double fork, `setsid`,
`nohup … &`, or a background job left running when the call returns) is
detached work: the guard shows it as a row of its own under the command that
started it, measures it each tick and can stop it. It names no script or tool to find it. A SessionStart
hook (`workbench agent guard session`, for Claude Code and Codex) leaves a small file
in the guard's state folder that says the agent runs, so the guard knows the
agent from its start, and under Claude Code adds one line to the session's env
file, which gives every later command a variable naming its agent and its own
shell. The hook never starts the watcher, prints nothing and never fails. At
every tick the guard records the processes of each command it tracks; a
program that was among them, or on macOS one that a recorded process forked
(by the kernel's record of which process forked it) or that carries the
command's variable, counts as that command's work once it detaches. On macOS
the guard also learns a command's shell the moment it starts, so work left
behind by a command that ends within a second is seen too, with its call,
once a watcher runs: a session's first call can end before the watcher its
hook starts is up, and what it leaves then has no call. Such a call gets no
row or log record of its own; what it left is a row of detached work naming
it;
an agent that wakes the guard more than 100 times in a second by forking is
not followed for 5 seconds, and its calls are found at the ticks meanwhile. An
app opened with `open` is never detached work: the system started it.

The guard itself stops detached work only when it can prove which call
started it, the program has run ten seconds, and its setting was read. The
row's details say why it will not, on a "Note" line: no call is proved to
have started it (work a command you typed with `!` left, or a call the guard
could not see start); it was found, or started its program, less than ten
seconds ago (a Claude Code session a command started says it is an agent well
within that, and plain Codex within a few seconds, and is then an agent, never
stopped and not shown; a Codex session whose required MCP servers start slowly can
take longer); or its setting cannot be read. While the kernel does not answer
where processes come from, the guard stops no detached work itself. You can
stop any of them from the view. The ten seconds start again when the program
runs another one and after an update, and a session's work stops being the
command's once the session is known or protected.

`WORKBENCH_GUARD=off` in a detached program's own environment keeps it out of
the guard for the life of that process, even if a program it later runs in
its place drops the variable, and the programs it starts stay out as long as
they keep the variable; `dry-run` keeps the guard's stops of it to the log. On
macOS a program that rewrites its title keeps its setting; on Linux a title
written over the environment area wipes it, and the program is treated as one
whose setting cannot be read: the guard never stops it itself, and you can
stop it from the view. On macOS, many programs
hide their environment from other
programs: the system's own, such as `/bin/zsh`, `/bin/bash`, `/bin/sleep`,
`/usr/bin/perl` and `/usr/bin/ruby`. For such a program the guard reads the
setting from the first programs below it that show their environment. When
none does, the guard never stops that work itself; it is shown and you can
stop it from the view, which sends the stop unless the agent's own setting is
`dry-run` or `off` or the call's is `dry-run`. The setting must reach every
program that should stay out: a program run with it removed (`env -u`, a build
tool that filters variables) counts as not opted out. A program run with an
empty environment (`env -i`) reads as unreadable, like a hidden one.

A detached row lasts while any of its processes runs and its agent does; when
the agent exits, every row of that agent is forgotten, not stopped, and shows
as finished. It also outlasts the watcher: the watcher keeps a small private
record of each detached row in `units/` beside its notes, so a watcher the
next command starts after the last one was killed shows the row again, with
its call, as long as its programs still run and the machine has not
restarted; its memory graph starts again then. Not seen: work a program below
the command's shell double-forks at once when nothing in it shows its
environment, work handed to a service (a container engine, a launchd or
systemd job), and another user's process. A command that names one of its own
processes as an agent (`CLAUDE_PID=$$ …`) keeps the guard off its tree. On
Linux and WSL, which keep no record of which process forked another, detached
work is found only when the guard recorded it under the command at a tick
before it detached, so a program that detaches at once is unseen there. The
guard may still stop, as the command's detached work: a session the command
started that has not said it is an agent after ten seconds (such as Codex
waiting on a slow required MCP server), and one started without the hooks
that the guard cannot recognize (on macOS, one whose programs all hide their
environment; on Linux and WSL, any). See
[ADR 0016](adr/0016-guard-detached-work.md).

`WORKBENCH_GUARD=off` in the agent's environment turns it off;
`WORKBENCH_GUARD=dry-run` only logs what it would stop. The notes, the log
(`watch.log`), the watcher's own output (`watch.err`, and `watch.err.1`
once that passes 1 MiB) and the history of finished commands (`history.db`,
see below) are in Workbench's state folder under `agent/guard/`, and
the costs import log and lock under `agent/costs/`. The `costs/` and `guard/`
folders earlier releases kept at the top of the data and state folders move
there whole, each in one swap that leaves a small file at the old path until
the machine restarts, so a process of that release fails there instead of
starting over in an empty folder. The **Move Workbench's files** step moves
them, and so does every costs and guard entry that holds the folder's lock: a
watcher of the earlier release hands over as at every update, and the new
watcher moves the guard folder before its first look, so nothing it was
watching is dropped; a folder a watcher or an import still works in stays
until that watcher or import has ended, and the next entry to take its lock
moves it. Wherever the guard folder is, the hooks, the watcher, `agent guard`,
`agent guard history` and `agent guard logs` use it only while it, its
`notes/`, `killed/` and `agents/` folders and the state folder above are
real folders (not links), yours, that neither your group nor other users can
write in, on the machine's own file system, and refuse it otherwise in the
same words: a folder that others can write in, by its mode or, on a Mac, by
an ACL entry, is refused with two fix lines, the first to move aside what is
not yours, the second the `chmod` that ends it:

```text
✗ …/notes has mode 0775
    cause  other users can write in it, so what it holds may not be Workbench's
    fix    move aside what find …/notes -not -user NAME lists
           chmod 700 …/notes
```

An ACL entry gets `chmod -N …/notes` as its second line. An entry that
writes there (the hooks, the watcher, `agent guard history --keep`) sets
a folder that others cannot write in to 0700 when it has another mode;
the view, `agent guard history` and `agent guard logs`, which only read,
and every entry at the old path leave its mode as it is, but for one you
cannot read or search, which every entry, and every other command, gives
0700 before it looks in it, saying nothing. An old folder that others
can write in is neither moved nor locked. An entry asks whether a watcher or an import works in a folder with a
shared lock on that folder's lock file, which only an exclusive lock
refuses: the watcher's or the import's own, or, for an instant, that of an
entry moving the folder, which takes it only when the folder can move. So
entries asking at the same moment never take each other for one, and
`workbench migrate` tries a lock it finds held again for about 100 ms. On a file system that cannot swap two names, such as HFS+, the folders
stay and are used where they are, and so does a folder an overlay file
system keeps in a container image's lower layer, which only a move finds
out: the first move that tries it marks the folder with an extended
attribute naming its device and inode, and from then on nothing tries again on
that mount; a copy of the folder, which takes the attribute along, is moved
like any other. An
old folder found beside one already in use is not merged: its logs, output files, locks, sockets, downloaded price
lists and empty folders are deleted, and anything else, such as a ledger or a
note, stays. `workbench migrate --dry-run` and `workbench doctor` name an old
folder that stays and why. The hooks
(pre, post and session) and the watcher are hidden `workbench agent guard` commands. After `workbench update` (or
an apply) installs another release, the running watcher hands over to that
release's watcher within about a second, once no stop is under way or
settling: the new one keeps watching the same commands, and the log shows the
handover. When the new release cannot take over, the old watcher keeps
watching and logs why once. A watcher from a build without handover keeps
running its own code until it has been idle for 10 minutes; `kill` (its
`SIGTERM`) stops it after any stop under way, and the next command's hook
starts the installed release's.

`workbench agent guard logs` shows what the guard did, oldest first, one line per
event: the time in the machine's time zone, 12-hour with AM or PM, the level, the event, then
`key=value` fields in this order, each left out when it has no value:
`id agent subagent project kind launched_by by dry_run took size peak floor
level available total still_running release old_release new_release new_pid msg
reason error process cmd`. `id` is the first 8 characters of the tool call ID after its `toolu_` or
`call_` prefix and any provider segment such as `bdrk_` or `vrtx_`; `project`
is the folder the command ran in; `kind` is `detached` on the records of
detached work;
`id` of detached work is `p` and the PID of the program that detached first,
one label for its life; `launched_by` is the short ID of the call that started
detached work, left out when that call is not proven; detached work is named by
the process name the system keeps for its first program, never by its
arguments, which may hold a token the shell expanded (its full command line
is shown in `agent guard` only); `cmd` is the command as the log records it,
on one line (backticks as `'`, blanks and newlines collapsed to one space) and
cut at 120 characters with `…`, without a leading `cd PATH &&` or `cd PATH;`,
last because it is the longest; `agent guard`'s details show it whole. Sizes are binary
(1 GB is 1024 MB) and whole `MB` below 1 GB; from 1 GB a reading that moves
(`size`, `peak`, `available`) has two decimals (`2.41GB`) and a fixed total
(`floor`, `total`) is whole (`16GB`), or has two decimals when it is not
whole (`1.86GB`); `took` has one decimal below 10 s, whole seconds below a
minute, then minutes and seconds (`1m12s`), hours and minutes (`1h12m`) and
days and hours (`1d2h`), the smaller unit left out when it is zero (`12m`,
`3d`); `level` is macOS's memory pressure level, `available`
and `total` Linux's memory. `msg` shows only for `memory`, `refused`,
`gave-up`, `error` and `raw`, where the event alone does not say what
happened. `reason` shows only for `ended`, on detached work whose last program left
it, and says why, such as `"its root turned the guard off"`. A value holding a space, `"`, `=`, `\` or a control character is
written in Go's double quotes, so each value stays on one line and logfmt
readers split the fields. The events are each command or detached work that ended, matched
no known shape (`unmatched`, only for a call the guard never found: no tick
tracked it and no fork wake-up learned its shell) or was stopped (`stopping`, then `forced` or
`gave-up` if it comes to that), low memory that stopped nothing, calls dropped
without their post hook, the watcher starting, handing over to a new release,
going idle and exiting, errors, and any line of the watcher's own output, such
as a crash's trace, as a `raw` event with its line in `msg` and no time, placed
at the start of the watcher that wrote it. Commands the watcher only started
tracking are left out; with `--follow`, one still running 5 s after the watcher
found it gets one `running` line, without `took` or `peak`.

```text
Oct 17  2:41:05 PM INFO  ended     id=4fQ8nX2a agent=claude subagent=Explore project=feature-login took=53s peak=2.41GB cmd="pnpm typecheck 2>&1 | tail -15"
Oct 17  2:41:20 PM WARN  unmatched id=7Kp3Rt9m agent=claude project=feature-login took=2.0s cmd=./scripts/nap.sh
Oct 17  2:42:31 PM WARN  stopping  id=2Bv6Yc1d agent=claude project=api-server by=guard took=1m12s size=6.21GB peak=6.21GB available=1.12GB total=16GB cmd="cargo test --workspace"
Oct 17  2:42:36 PM WARN  forced    id=2Bv6Yc1d agent=claude project=api-server cmd="cargo test --workspace"
Oct 17  2:43:02 PM WARN  memory    size=740MB floor=1GB available=1.12GB total=16GB msg="memory is low, but the agent commands are too small to stop"
Oct 17  2:44:40 PM WARN  stopping  id=9Hs5Wq4e agent=claude project=feature-login by=you took=8.0s size=1.31GB peak=1.31GB cmd="pnpm vitest run"
Oct 17  2:45:10 PM ERROR error     msg="cannot read a process" error="open /proc/81234/stat: permission denied" process=81234
Oct 17  2:50:00 PM INFO  idle
```

A long line soft-wraps on screen and stays one line, so `grep` and copying
keep the whole event. At a terminal the time and the keys are faint, the level
alone is colored (INFO green, WARN gold and bold, ERROR red and bold), the
`agent` wears its agent's hue, `by=you` and a `took` of 10 s or more are bold,
and a `size` or `peak` is bold, gold from 1 GB and red from 4 GB; the event,
`id` and other values stay plain. Piped output, `TERM=dumb` and `NO_COLOR` get
plain text. With nothing to show it says so on stderr and exits 0.

```bash
workbench agent guard logs                       # one line per event
workbench agent guard logs --since 2h            # a Go duration before now
workbench agent guard logs --since "2026-10-16 21:00"
workbench agent guard logs -f                    # keep printing new events
workbench agent guard logs --json --since today  # the log's own JSON Lines records
```

`--since` takes a duration (`10m`, `2h`), `today`, `yesterday`, a date, date
and time or time of today in the machine's time zone (`2026-10-16`,
`2026-10-16 21:00`, `21:00`), or an RFC 3339 time; a line of the watcher's
output, which has no time, is kept unless the next watcher started before
then. `--follow` (`-f`) waits for files that do not exist yet and keeps its
place by byte offset, so a cut of the log (to its newest half past 1 MiB, of
the records older than the time the history keeps, or a migration's that drops
lines from the middle) and a rotation of the output neither skip nor repeat a
line, even lines that read the same. The log keeps a record no longer than the
history keeps its command, so the start of a command a record holds goes with
it: the watcher deletes the older records once a minute, and `agent guard
history --keep` as it trims while no watcher runs (with one running, the
watcher's next cut does it); a line with no time, such as a crash's trace,
stays until the size cut. A problem the watcher logs once and that lasts (a
socket it cannot serve, a history it cannot write, a guard folder it cannot
move) is logged again before the cut by the time kept would delete its
record, and within a minute once the log's oldest record is newer than it (as
after the size cut, or when the log holds no record), so the log says why for
as long as it lasts. The log is
JSON Lines: each record has `time`, `level`, `msg`, `event` (`started`,
`handover`, `moved`, `idle`, `exited`, `tracking`, `ended`, `unmatched`, `abandoned`,
`memory`, `stopping`, `forced`, `gave-up`, `refused`, `error`), `part` and
`pid` (a log that was cut starts with a `cut` record, which the viewer never
shows), and, by event, `call`, `agent`, `subagent`, `cwd`, `project`,
`command`, `root_pid`, `kind`, `launched_by`, `by`, `took`, `size`, `peak`, `children`, `reason`,
`memory`, `floor`, `dry_run`, `still_running`, `release`, `old_release`,
`new_release`, `new_pid`, `process` and `error`; the global `--json` prints the matching
records unchanged, as JSON Lines rather than one result object, one per line, `tracking` records and full call IDs
included, and a
line of the watcher's output as
`{"level":"ERROR","msg":"<the line>","event":"raw","started":"<its watcher's start>"}`.
See the [guard design](superpowers/specs/2026-10-14-agent-command-guard-design.md),
section 4.1.

`workbench agent guard` shows, live and full screen, each command the watcher
tracks, newest first: its short ID, status (`● running`, `◌ stopping`), agent,
subagent, run time, memory now and at its peak, project and command. Below
them, dimmed, come the commands that finished, newest first, for as long as
the history keeps them (see below), with their outcome (`✓ done`,
`? unmatched` for a call the guard never found, `✗ stopped`) and, under ENDED,
the time each ended (`5:07 PM`), with its date (`Nov 18 5:07 PM`) when more
than 24 hours are kept and it ended before today.
While the list is sorted by newest and has no filter, detached work has a row
of its own directly under the command that started it, joined to it by a tree
line before its ID (`├`, and `└` for the last under a command), with the same
statuses. A command and the detached work under it move as one block, and a
command that has ended stays above its detached work while that runs.
Detached work whose call is not proven, or whose command is not in the list,
has a row among the commands, and so does every row while the list is sorted
by peak or filtered. Its ID is `p` and a process ID; it is timed from the start
of the program that detached first and shows the command line and PID of its
first program that still runs (they change when that program exits; the log
keeps one label for the row, which its details show on an "In the log" line).
A command line that might hold an environment string is shown shortened, on
macOS to its program, and ends with ` …` (`make CC=clang` shows as `make …`).
The view shows only what the guard tracks: an agent, the helpers it starts
and a running command no note matched have no row. You may stop any running
row. A stop reaches only its own row, so a command and its detached work are
stopped one by one, or together when both are tagged. With
`WORKBENCH_GUARD=dry-run` your stop only logs; it also only logs for a
detached program whose setting cannot be read, when the agent's setting is
`dry-run` or `off`.

The header's first line gives the watcher's state (`● watching`, `○ not
running` while no process holds the watcher's lock, `○ not connected` while
one may run but nothing serves the view), a `memory low` or `memory critical` badge when
memory runs short, and the clock; below a blank line, the counts (`3 running ·
2,832 finished`, commands and detached work alike; with no watcher, the
finished count alone) and, while a watcher answers, the memory in use of the
installed memory (`mem 11.27/16 GB`), the largest peak since the watcher
started and the swap in use; then a line that says `dry run: stops are logged,
not sent` when every command the watcher tracks is a dry run, or `dry run on 2
of 5 commands: …` when some are, each marked `(dry)` in its status, and a line
when the history cannot be written, was found damaged and started again, or
cannot be read (see below). The selection starts on the first running row,
so a command that has ended, dimmed above its detached work, is never selected
first, and on the first row when nothing runs; it stays there while rows come
and go until a key moves it, and `s` and a new find text take it back there. The
selected row lies on a slate band across the
window, its `▸` and ID bold in a light steel; a light terminal gets a light
band, and without color the row is bold instead. ↑ and ↓ move (there is no `j`
or `k`, since `k` stops, as in htop), PgUp and PgDn move by a page and Home and
End to the first or last row, space tags a running row and moves down, enter,
→ or `l` opens the selected one's details, `/` finds rows by their text, `s`
sorts the finished rows by their peak, largest first, and again by newest
(running rows stay on top in their own order), `k` opens the stop menu for the
tagged rows or the selected one, with `Stop (polite, force-killed after 5 s)`
chosen and `Force kill now` below it, esc, ← or `h` closes the menu or the
details, and `q`, esc on the list or ctrl+c quits. The key line ends with the
selected row's number and the row count (`1/2,837`). The view never takes the
mouse (it asks the terminal for no mouse events), so a plain drag selects text
on every screen, and in most terminals the mouse wheel moves as ↑ and ↓ do: it
moves the selection on the list, scrolls the details and moves the stop menu's
choice (enter still sends).

`/` opens a one-line input on the bottom line, and the list then shows only
the rows, running and finished, whose agent, subagent, project, ID or command
holds the text. Upper and lower case count alike, in any script, by Unicode
case folding (`é` finds `CAFÉ`), for running and finished rows by the same
rule. While the input is open every key types, `k`, `q`, `s` and space
too, except ↑, ↓, PgUp and PgDn, which move the selection, enter, which closes
the input and keeps the filter, esc, which closes it and clears the filter,
and ctrl+c, which quits. Typing never waits for the search: the list keeps its
rows until the answer comes and shows only the answer to the latest text.
While a filter is kept, the counts say how many rows show and what was found
(`5 running · 12 of 2,832 finished · find "cargo"`), `/` edits it, esc clears
it and a second esc quits.

The details show, in this order, the folder, agent, subagent and full ID, for
detached work its log label ("In the log") and the call that started it when
that is proven ("Started by"), the status with who stopped it and the run
time, a "Note" line when the guard will not stop detached work itself, saying
why, when it started and, once it has finished, when it ended, its memory (now
and at its peak while it runs, the peak once it has finished), a one-line graph
of its memory over its whole run (at most 120 bars, each the largest reading
it stands for and colored by its size as the list colors a size), the command
and its processes. The lines above the command keep their height while a
command runs. A command shows whole when it takes at most 4 lines at the
window's width, else its first 3 and a `… N more lines` row. The processes
have a count line (`27 running`; for a finished command `143 in all`, or
`10,000 of 12,400 kept` when it had more than the history keeps), their column
heads and their rows: a running command's processes with their memory now, in
the watcher's order, and every process a finished one had with its peak,
largest first, or `none`. Up to 11 show whole; more show the first 10 and a
`… N more processes` row. The details lay a long command out one step per
line, each `|`, `&&` or `||` outside quotes starting a line two spaces in
(`go test ./... 2>&1`, then `| tail -20`), and a `;` ends its line; a test in
`[[ ]]`, a `{ }` group, a `case … esac` and a command with a here-document
stay whole.

In the details ↑ and ↓ choose among the `… more` rows, the chosen one marked
`▸` and saying `· enter shows all`, and scroll a line when there are none;
PgUp and PgDn scroll a page and Home and End go to the ends. Enter, → or `l`
opens the chosen part whole in a side panel on the right half of the window,
under its title (`Command`, or `Processes · 143 in all, largest first`), while
the details stay on the left, wrapped to their half, the chosen row saying
`· shown on the right`; a window under 100 columns gives the panel the whole
width. In the panel ↑, ↓, PgUp, PgDn, Home and End scroll, a last line says
which lines show when not all fit (`lines 30 to 58 of 144`), a command longer
than the history keeps ends with how much was not kept (`… 12 KB more were not
kept`), and esc, ← or `h` closes it; `k` still stops. The details always open
with the panel closed. The watcher stops the command the way it stops for
memory, logs it `by=you` and tells the agent `Stopped by you (workbench agent
guard).` (for detached work, the call that started it, when it is proven and
its command still runs in the foreground; otherwise the stop is in the log
only); the bottom line shows its answer for 3 seconds (`stopped 4fQ8nX2a`,
`logged 4fQ8nX2a (dry run)` or `4fQ8nX2a already finished`). The watcher
serves the view on `guard.sock` beside its log, only to the same user, and
never takes a stop from a process still under an agent with a guarded
command. In a window under 60 columns or 11 lines the view asks to be widened
or made taller; narrower windows first shorten COMMAND, then drop SUBAGENT,
PROJECT, PEAK, AGENT and TIME, in that order, and ENDED stays. While no watcher
runs it says so, keeps the last rows dimmed, shows the finished commands the
history holds and reconnects on its own; the watcher starts with the next
agent command. Whether one runs is told from the watcher's lock, as `doctor`
tells it: while the lock is held and nothing answers on the socket, the view
says what `doctor` says, in one line (`Nothing answers on the watcher's
socket: its lock is held by a watcher that is starting or cannot serve; fix:
workbench agent guard logs.`). A command in a git worktree is labeled by its repository and
worktree (`api/feature-login`). Without a terminal on stdin and stdout, or
with `--non-interactive`, it exits 3 with `agent guard needs a terminal` and
the fix `workbench agent guard logs --follow`; with `--json`, which it has no output
for, it exits 3 and points at `agent guard logs --json`; and under a coding
agent (`CLAUDECODE` or a Codex variable, as `apply` detects) it exits 3 with
`agent guard is for a person at a terminal`.

```bash
workbench agent guard     # watch, tag and stop agent commands
```

The watcher keeps the commands that finished, detached work included, in a
history: `history.db` in the guard folder, with SQLite's `history.db-wal` and
`history.db-shm` beside it while it is open, all three readable by you alone,
since a command's text may hold a token. A finished command keeps its folder,
agent, subagent, project, ID, outcome and who stopped it, when it started and
ended, its peak, its memory graph and every process it had with its peak, up
to 10,000 processes (the largest) and 64 KiB of its command's text. A call
that ran and returned between two of the watcher's looks, or before any
watcher ran, is kept as done, without a graph or processes, since nothing was
measured, and a call whose command was never found is kept as unmatched. A command shows among the
finished ones, its details too, in the second it ends. The watcher writes the
history beside its once-a-second look, so a slow disk never delays a stop.
While a write fails it keeps up to 2 MiB of finished commands in memory,
dropping the oldest past that, tries again every ten seconds, logs the failure
once, and the view says the history cannot be written and why. A watcher that
hands over passes on the commands it has not written, and one that exits waits
up to a second for them to be written.

Finished commands are kept 24 hours until you set another time; nothing asks
for one.
`workbench agent guard history` shows how long they are kept, how many there
are, the history's size and its file, and, on a last line, the copies of
damaged histories it keeps, in the view's words. `--keep` sets the time, in whole hours,
days or weeks from `1h` to `30d` (`12h`, `3d`, `1w`); any other value exits 2
and sets nothing. The oldest leave first: a command that ended longer ago than
the time kept, and, past 20,000 commands or 256 MB on disk, the oldest even
when younger. A shorter time deletes the older commands at once, without
asking, with any copy of a damaged history set aside longer ago (see below)
and the guard log's records older than the time kept (by the watcher within
a minute, while one runs), whether a watcher runs or not, and says how many
commands: the command trims the
history itself in the watcher's small steps, each at most 200 commands, 2,000
of their processes and 512 KB of their text, and leaves the history to the
watcher between steps, so the watcher goes on writing meanwhile. A longer time
brings none back. The command shows sizes in KB, MB and GB of
1,024, as the guard shows memory. The global `--json` prints the same facts as fields: `path`,
`keep_seconds`, `commands` and `bytes`, or with `--keep` `path`,
`keep_seconds`, `was_seconds` and `deleted`.

On Linux:

```text
$ workbench agent guard history
Finished commands are kept 24 hours · 2,832 kept · 3 MB
~/.local/state/workbench/agent/guard/history.db
$ workbench agent guard history --keep 12h
Finished commands are kept 12 hours
    was      24 hours
    history  1,204 older ones were deleted
```

A history the guard cannot use (a file that is not a database, that SQLite
finds damaged, or whose layout is not the guard history's) is never repaired
or converted. The watcher moves it, with its `-wal` and `-shm` files, aside to
`history.damaged.db`, after moving a copy already there to
`history.damaged.2.db`, starts a new, empty history with the time kept it last
read (24 hours when it read none), read once more from the old file first when
that still answers, and logs both paths and the time the new one keeps; when
the old file no longer answers, the log also says when that time was read,
since a `--keep` set after that is lost with the old file. While a copy is
there, the view and `agent guard history` say the history was damaged and
started again and name each copy. Each copy is deleted once it is older than
the time kept, when all it held would have gone anyway, since it may hold rows
SQLite's `.recover` can rebuild. At most two copies are kept: when both names
hold copies younger than the time kept, a third history the guard cannot use
takes the place of `history.damaged.db`, whose copy, of a history started
after a damage, is deleted, while `history.damaged.2.db`, the oldest, which most
likely holds the rows from before the first damage, stays. So the history
always starts again, and the log names the copy deleted. SQLite
reads from the file only what its cache lacks, so once a minute the watcher
also checks that the file is still the one it opened and that its header is a
database's, reading it through a descriptor it keeps open beside SQLite's,
since closing one would drop SQLite's locks; a history moved elsewhere keeps
every row, since the watcher writes the rows it holds in the WAL into the
moved file before it starts a new one. Only the watcher moves the file: with
none running, the view says the history cannot be read and shows none of it,
and `agent guard history` fails the same way (exit 2) and sets nothing. A
history that cannot be written for another reason, such as a full disk or a
permission, stays where it is. See [ADR 0020](adr/0020-guard-history.md).

Workbench installs Claude Code's settings, instructions and hooks in
`~/.claude` and Codex's in `~/.codex`, and never sets `CLAUDE_CONFIG_DIR` or
`CODEX_HOME`. Either variable moves its tool to another folder, often one
per account (`alias claude-work='CLAUDE_CONFIG_DIR=~/.claude-work claude'`),
and the tool then reads none of what Workbench installs. Workbench leaves that
folder alone, as installing there would pick one of your accounts for you.
When the variable points elsewhere, set in Workbench's environment or, for
`CLAUDE_CONFIG_DIR`, in the `env` of Claude Code's user or managed settings
files (where Claude Code also takes it from; a file Claude Code skips for an
error, such as one that is not JSON, sets nothing), the `apply` plan warns,
`doctor` reports `claude-config-dir` or `codex-home` as a conflict, and `agent costs status`
checks the hooks in that folder and adds a `config` line, each naming the
variable, where it is set and how to undo it. A session started with the variable only
in that session's environment, such as through the alias, is invisible to
Workbench. The costs ingest hook follows the folder a Claude Code session's
transcript is in when the variable was stripped from the hook's environment
(`CLAUDE_CODE_SUBPROCESS_ENV_SCRUB`). See
[ADR 0010](adr/0010-agent-config-folder-moves-are-reported.md).

## Roles, folders and credentials

Each role has a folder, asked once with the role: `Work folder (under your home)`, default `~/repos`, for `work` and `both`, and `Personal folder (under your home)`, default `~/dev`, for `personal` and `both`. An answer is saved as home-relative text such as `~/repos`; a trailing `/` is dropped, each part holds only letters, digits, `.`, `_` and `-` with no `.` or `..` part, the home folder itself is refused, and on `both` the two folders differ and neither is inside the other. Git's per-folder identity, Codex's trusted folders, the costs report's default view, the folders the System packages step creates and, on WSL, the folder Windows Terminal and PowerShell open in (the work folder, else the personal one) all follow the answers; when a role folder is a symlink, the shell hook and the gate resolve it, but git's per-folder identity and the costs report's scope follow the real path, not the link: git matches a repository by its real location, so one under a linked work folder gets the default email, and a session recorded under the real path counts as outside the folder. When `machine.toml` is there but cannot be read, the costs report says so once and groups projects under `~/dev` and `~/repos`. The `work` and `both` roles are also asked, once, `Jira site URL (e.g. https://yourcompany.atlassian.net)` and `GitLab URL (e.g. https://gitlab.com/yourgroup)`, with no default, saved as `jira_url` and `gitlab_url` (an `https://` address with no spaces; a trailing `/` is dropped), and `Folder in your work folder that holds the dispatch servers`, default `ai-devtools`, saved as `dispatch_folder` (one folder name: letters, digits, `.`, `_` and `-`, not `.` or `..`). They are not secrets and stay in `machine.toml` with the other answers; a new `work.env` starts with the two addresses. A machine whose answers were saved before the folder or work questions existed is asked the missing ones on its next `apply`, the folders with the defaults, which are the folders earlier releases used; that apply needs a terminal, and an unattended one is refused until `answers import FILE` saves them. A personal-only machine is never asked the work questions. A `restart_wsl_path` answer an earlier release saved on WSL is ignored, so it blocks no run, and dropped the next time an answer or choice changes and they are saved. `answers import` refuses a FILE that still has it. `answers ask work_folder` changes one.

Credentials live in one file per role in Workbench's config folder, `work.env` and `personal.env` (`~/.config/workbench` on Linux, `~/Library/Application Support/workbench/config` on macOS), not in the answers. Each line is `NAME=value`, a `#` comment or blank: no `export`, quotes, space or tab at either end of a value (other blanks count as part of the value), or Windows line ending, and not a name the shell hook runs through: `PATH`, `path`, `HOME`, `PWD`, `PROMPT_COMMAND`, `precmd_functions`, `chpwd_functions`, the regex match results `BASH_REMATCH`, `MATCH`, `MBEGIN`, `MEND`, `match`, `mbegin` and `mend`, or one of its own (`__workbench_*`, `_wb_*`); case counts, so `http_proxy` is fine; a value is taken as written, with no expansion. A line that is not that is skipped, and the plan names its number and why (`env-files: = work.env line 3 has a space or tab at the start or end of its value and is skipped`); a `NAME=` line with no value sets nothing, so an empty name never hides a tool's own setting. The Credential files step (`env-files`, on by default) creates each file a role of the machine uses when it is missing, mode 600; `work.env` starts with `JIRA_BASE_URL`, `JIRA_EMAIL`, `JIRA_API_TOKEN`, `CONFLUENCE_BASE_URL`, `CONFLUENCE_EMAIL`, `CONFLUENCE_API_TOKEN`, `TEMPO_API_TOKEN`, `GITLAB_URL` and `GITLAB_TOKEN`, with `JIRA_BASE_URL` and `GITLAB_URL` from the `jira_url` and `gitlab_url` answers, the work email (`work_email` on `both`) as `JIRA_EMAIL` and the rest empty, and `personal.env` with no names. After that the file is yours and Workbench never writes it again. Its plan lines say `env-files: create work.env`, or, with nothing to do, which listed names are still empty (`env-files: = work.env is there, empty: JIRA_API_TOKEN, TEMPO_API_TOKEN`), that a file without its role should be deleted (`= work.env holds work credentials without the work role: delete it`; Workbench does not delete it), that it cannot read a file as a private one and why (`= Cannot read work.env: ~/.config/workbench/work.env is larger than its 1 MiB bound`), or that it is waiting for the Move Workbench's files step. That step moves the Jira and GitLab tokens earlier releases saved in `machine.toml` into a new `work.env`, with the work email as `JIRA_EMAIL` and the Jira and GitLab addresses from the `jira_url` and `gitlab_url` answers (empty when they are not answered yet), and deletes the two answers only once `work.env` holds them; a `work.env` that is already there without those same two tokens is left as it is with the tokens still in `machine.toml`, and the plan says to copy them by hand. One that cannot be read as a private file is left the same way, the plan says why, and the next run that can read it goes on. Values are never shown, logged or put in `--json`. The same step renames `costs-ingest` to `agent-costs-ingest` in the choices saved in `machine.toml` (`skip`, `select`, `decided`), so the choice made for Record AI usage still applies; until it has run, Workbench reads the saved choices under that name. Unticking the Move Workbench's files step leaves the tokens in `machine.toml` until a later apply moves them and skips the cache clean-ups; the costs files still move the next time Claude Code or Codex reports usage, so no usage is lost when session files are deleted. To undo, delete a file by hand; it is not checkpointed.

An interactive shell loads a role's file only inside that role's folder, symlinks resolved, and only while the file has no group or other permission bits (600 or 400). It reads the file line by line, never sources it, and checks again on every `cd`, `pushd` and `popd` and before every prompt. On each change it first unsets every name it loaded before, even one you have since deleted from the file, and the names the other role's file lists, so a shell or tmux pane that inherited work names drops them outside the work folder. So `cd ~/dev/site && npm install`, typed in the work folder, runs without the work names. The `claude` and `codex` wrappers load the folder's names just before they start the agent, which keeps them for its whole session, and `code` starts the editor with none of them, so an editor window opened from the work folder passes none to the windows it opens later. A command that works on another folder without changing to it (`git -C`) keeps the current folder's names, and an agent started by an editor extension has none. On Linux and WSL this is the managed `.bashrc`; on a Mac, the managed `.zshrc`. Claude Code's settings hold a `Read` deny rule for each env file, which in Claude Code's words applies to its built-in file tools (Read, Grep, Glob, @-mentions), to the file commands it recognizes in Bash (`cat`, `head`, `tail`, `sed`, `tee`) and to the targets of Bash redirections, in every permission mode. It does not stop `grep -r` or another command that reads files without naming them, a script (Python, Node or any other) that reads the file, a command inside `sh -c`, or a program called by its path; it is a guard against a casual read, not a boundary. Codex has no such rule, since it runs with full access. Both agents' instructions say to use the variables by name and never open or print the files.

The work MCP servers (`dispatch-atlassian` and `dispatch-gitlab`, built in the `dispatch_folder` checkout of the work folder, each in a folder of its own name) are built there by you, not by Workbench; while one's `dist/index.js` is missing, the plan warns that it fails in work-folder sessions until it is built. They start only for an agent session started inside the work folder, for Claude Code and Codex alike. Both agents register each server with `workbench mcp-gate --folder <work folder> --env-file <config folder>/work.env --` and the server's own command, all absolute paths written at apply time, and no credential in either agent's config. The gate takes the session's folder (Claude Code's `CLAUDE_PROJECT_DIR`, else the folder Codex starts it in), resolves symlinks and compares whole path parts, so `~/repos-old` is not inside `~/repos`. Inside, it adds `work.env`'s names to the server's environment and becomes the server; a `work.env` it cannot read as a private file (missing, a link, not yours, open to others, with a second hard link, unreadable or over 1 MiB) makes it exit with an error naming the file and what is wrong with it, so the agent shows the server as failed. Anywhere else it reads no file and starts no server: it answers as a server with no tools and returns no error, and one idle `mcp-gate` process stays for the session in place of each server, about 19 MB of memory each, so about 38 MB for the two in every Claude Code or Codex session started outside the work folder. A Claude Code session keeps what it started with even when it later changes folder (`/cd`). Codex restarts its MCP servers when the session changes folder, so there the gate decides again for the new folder. In Codex the entries are written into `~/.codex/config.toml` and enabled; an apply removes the Jira and GitLab addresses, email and tokens an earlier release wrote into their `env` tables. In Claude Code the Claude Code work servers step (`claude-mcp`, on by default) registers them for every session (user scope) with Claude Code's own `claude mcp add-json --scope user NAME JSON`, since Claude Code rewrites `~/.claude.json` itself. It changes only a server that is not registered as wanted: a hand-added entry of the same name is removed first with `claude mcp remove --scope user NAME`, and a server already registered is left alone. Its plan line says `claude-mcp: register dispatch-atlassian, dispatch-gitlab` or `claude-mcp: = registered`. A same-named entry Claude Code keeps for one project (local scope) outranks these, so the plan names it with `claude mcp remove --scope local NAME` and leaves it to you, and, once the Move Workbench's files step has nothing left to move, a replaced entry that held tokens gets the line `claude-mcp: = the old entry's credentials are not carried into work.env`. Without the work role the step removes the two entries that are there, and Codex's go with the role. When there is something to change and `claude` is not installed, the step is blocked; with nothing to change it runs no `claude` command. On a machine where Claude Code has never run (its config folder is `~/.claude` and it has no `~/.claude.json` yet), the step's first `claude` command is Claude Code's first run, which rewrites `~/.claude/settings.json` once (it drops `skipAutoPermissionPrompt`), so the step keeps a copy of the settings the apply just wrote, `~/.claude/settings.json.workbench-first-run`, and puts it back after its commands; later runs leave the file alone. A copy left by an apply that was stopped during those commands is deleted by the next run of the step. Undo is `claude mcp remove --scope user` for each server.

Bitwarden. Workbench does not install the Bitwarden CLI or define a `bw` wrapper. A `bw` installed earlier stays, and so does the vault session key the wrapper cached in `~/.config/Bitwarden CLI/session`, which is Bitwarden's file: run `bw lock` (or delete that file) and remove `bw` by hand.

## Approval and automation

Each planned file carries a plain `title`, whether Workbench `merged` its settings into the file (your own keys are kept) or owns the whole file, its `added` and `removed` line counts, for a merged JSON or TOML file the number of `settings` that change, and a `summary`. A file Workbench owns that you edited since it last wrote it (by its last checkpoint, else chezmoi's own record of what it wrote) is marked `edited_outside`, and applying replaces your edit. The plan view also shows each file's diff (secrets masked); the diff text is for the screen only and is not in `--json` or the plan digest. For a merged JSON or TOML file whose old and new content both parse, the view lists the changed settings instead of lines (`~ hooks.SessionStart[0].hooks[0].timeout  10 → 5`, `+ … added`, `- … removed`; a list of plain values is compared as a set) and counts them as `N settings changed`, because the merge re-orders the whole file; any other file shows its line diff.

Deciding once. The first approved `apply` shows the plan and saves two things in the `[effects]` table of the private `machine.toml`: the steps you turned off (`skip`, plus `select` for optional steps you turned on) and the steps it showed you something to decide about (`decided`; a step whose check found nothing to change sits on the `Already set` line, where it cannot be turned off, so it is not recorded until it has something to do). Later `apply` runs print the plan and apply your saved choices without asking, unless a step is new (not in `decided`, `skip` or `select`) and has something to do, or a file Workbench owns whole was edited outside it. Until a first approved apply has saved `decided`, no step is marked new and the first `apply` shows the plan once. Applying without asking needs a person at a terminal to read the plan on; with none (a cron job) `apply` refuses unless you pass `--approve-plan DIGEST` or `--yes`. A coding agent never counts as a person, even in a pseudo-terminal: when `CLAUDECODE=1` (Claude Code) or `CODEX_CI`, `CODEX_THREAD_ID`, `CODEX_SANDBOX` or `CODEX_SANDBOX_NETWORK_DISABLED` (Codex) is set, `apply` shows nothing to answer and refuses with `An agent runs this` and the fix `use --dry-run --json, then --approve-plan DIGEST`, `--yes` and `--choose` are refused the same way (an agent applies only with `--approve-plan DIGEST`), and so are `answers ask` and the Yes or No approval of `revert` and `project` (`revert` without `--checkpoint` still lists the checkpoint IDs). A run that nobody can approve (no `--approve-plan`, no `--yes`, no person at a terminal) is refused with exit 3 before Workbench downloads its tools or saves anything. Files Workbench merges into (Claude Code settings, Codex config, VS Code settings) never count as edited outside, since Claude Code and Codex rewrite them constantly. `apply --choose` always asks. `apply --reset` forgets `skip`, `select` and `decided` for this host's steps, including a step left off the plan because it has nothing to do, and asks again with the defaults; saved choices for steps this host does not offer, such as a macOS step on Linux, are kept and ignored. A saved `select` for a step this host does not offer is shown as a plan warning and ignored; `--reset` forgets it. `--yes` never asks. `--choose`, `--reset` or `--approve-plan` with `--yes` is refused (`--reset` forgets what you turned off, so `--yes` would turn it all back on unasked, and `--approve-plan` already approves), and `--choose` needs a terminal (exit 3 without one; with `--json` or `--non-interactive` it is an invalid combination, exit 2). An isolated `--destination` never saves choices, so each interactive apply there asks. `--dry-run` never saves. Steps are gated one by one: a shared script with one step off still runs its other sections. On macOS that covers Mac apps and editor extensions and Work tools (the work editor extension); on Linux and WSL, EPEL and System packages (on AlmaLinux), and VS Code extensions and Work tools (the work editor extension); on WSL also Windows setup with WSL networking, default distribution and Windows PATH, and Windows setup with Terminal settings, PowerShell profile and Windows fonts.

What the plan shows. One view serves the interactive list and `--dry-run`: a header with counts (files, steps that will run, new to decide), the files, then `Will run`, `Off` (steps you turned off), `Optional` and a faint `Already set` line, names only, for steps whose check found nothing to change (the cursor can rest on one to read what is already in place, but it cannot be turned off). Each step is its plain name with faint text under it, wrapped to the terminal's width and never cut: what its check found to do on this machine (`install nvm`), or what the step does when the check found nothing. A step you turned off says `skipped: install nvm`, since this apply does not do it; an optional step that is not ticked keeps the line saying what it does. The cursor row opens a detail panel (what it does, what it would do on this machine now, what it changes, who it runs as, how to undo it). A file's diff and a merged file's settings list have these masked: known secrets Workbench holds; the value of a key whose name contains token, secret, password, passwd, passphrase, api key, access key, private key, credential or authorization, or ends in key, pat, auth, cookie, session, pass or pwd (`DB_PASS`, `MYSQL_PWD`), judged on the key's last name however long the path, and everything under a table named that way; a `Bearer` value; the value after a command-line flag named like that (`--token x`, `--db-pass x`), and after `-p` or `-P` unless it is a path or variable (`mkdir -p ~/x` stays), with `-psecret` attached only on a line naming mysql, mariadb, sshpass or mongo; the password in `https://user:password@host`, and a key of 16 or more characters used as the user (a Sentry DSN); a Slack, Discord, Microsoft Teams or Zapier webhook path or any `/webhook/` path; a URL query value named like a credential (`?client_secret=`, `&sig=`); and token shapes (GitHub, GitLab, OpenAI, AWS, Slack, JWT, 40 or more hex characters, a 40 or more character letters-and-digits run). Other values are shown as they are, so a credential with none of these shapes under an ordinary key name is not masked. The diff is printed only at a terminal: never to a pipe or a file, not even with `--verbose`. A step that cannot apply on this host, such as EPEL on Ubuntu, is not listed. A step that runs from a script chezmoi reruns when it changes is listed again on the first apply after a release changes that script (the language runtimes, command-line tools and tmux plugin steps, for one), even when nothing else is new. Steps that always run with the files, such as AI safety settings, show without a box. Keys: up and down move, space turns a step on or off, enter opens the detail (a file's diff, full screen; `q` or esc to return), `a` applies, `q` or ctrl+c quits with nothing applied; a mouse click selects a row and clicking the selected row toggles it. Closing the list prints its final frame once. A saved plan run without a prompt prints a compact view headed `Applying your saved choices`. When the checked steps will need the Mac password, a line under the header says so and why (`Applying at a terminal asks for your Mac password once, before it starts, to install Homebrew.`, or, where sudo uses Touch ID, `Applying at a terminal asks for Touch ID (your Mac password where Touch ID cannot be used) before it starts, to install Homebrew.`); it follows the boxes. Add `--verbose` to see each row's detail panel and the recovery limits.

Ticking a step is the approval: scripts Workbench runs during apply never stop to ask a second question. The one thing an apply asks besides the plan is the Mac password, at most once: before it starts when it has to install Homebrew, otherwise the first time Homebrew or the `touch-id-sudo` step needs it (where Touch ID for sudo is on, sudo asks instead: see The Mac password). What a step would change is in its detail panel beforehand (WSL networking lists each `.wslconfig` setting it will change, for example `networkingMode is virtioproxy, will be mirrored`), and the recovery copy of a file it replaces is still written.

`revert` and `project` still ask "Approve this exact plan?" with Yes and No (y or n, or the arrow keys and Enter; Enter alone, esc and ctrl+c refuse).

On WSL, Windows setup is an optional step: it starts off, and ticking it is remembered. It does not include Windows Terminal settings, the PowerShell profile or Windows fonts. Those three are separate optional steps under `Optional`, off until you tick each one, and ticking Windows setup ticks none of them. A part you tick installs what it needs itself. The other Windows steps (default distribution, PATH) and kernel tuning (swapping) are optional too. The default distribution step (`default-distro`, "Open new terminals in NAME") makes this distribution the one plain `wsl` opens and, when Windows Terminal has more than one WSL profile, the one new Terminal tabs open, by setting only `defaultProfile` in Terminal's `settings.json` (comments and layout kept, a copy saved beside it); it is asked only when there is a choice, so with one distribution that `wsl` already opens its line reads `default-distro: = NAME is your only distribution` and Terminal's own default stays, while Docker Desktop left as `wsl`'s default is asked about even with one distribution of your own. (see [WSL](wsl.md)). WSL networking is the one Windows step that starts on; it keeps your other `.wslconfig` settings and saves a copy of the file first. On a Mac, Touch ID for sudo is an optional step (`touch-id-sudo`): off until you tick it, it turns on Touch ID for `sudo` in every terminal, not only Workbench's, by writing `/etc/pam.d/sudo_local`, and does nothing when Touch ID for sudo is already on (the check then shows it on the `Already set` line). On Linux outside WSL the one optional step is Rust build cache (`mbx`, below). On AlmaLinux, and wherever the package manager is dnf, the EPEL step (`epel`) is listed beside System packages and is on by default like it: it installs `epel-release`, which turns on the package source bat, fd, ripgrep, htop and ShellCheck come from, and changes no other repository (`epel: turn on EPEL (uses sudo)`, or `epel: = EPEL is on`; undo with `sudo dnf remove epel-release`; see [Linux](linux.md)). Unattended runs see the same selection through `--dry-run --json`.

Rust build cache (`mbx`) is an optional step, off until you tick it, on every system mbx publishes a build for: macOS on Apple silicon and Linux (WSL included) on amd64 and arm64. An Intel Mac does not list it, and `workbench doctor` says why on its `mbx` line (`mbx publishes no Intel Mac build, so the Rust build cache step is not offered`). [mbx](https://github.com/jdx/mr-boxington) is a shared Rust compile cache and scheduler: a crate compiled in one checkout is reused by every other checkout with the same sources, and parallel builds on the machine share one CPU and memory budget. The step downloads the mbx release `[mbx]` in `versions.toml` pins (the same version in latest mode), checks the archive against its pinned SHA-256 before unpacking it, and puts `mbx` in `~/.local/bin` (an mbx there at the pin or newer is kept, a link there is never replaced). It then runs `mbx setup`, which never prompts or picks a mise scope here: it writes mbx's Cargo shim and the check command in rust-analyzer's user `rust-analyzer.toml` (`~/.config/rust-analyzer/rust-analyzer.toml` on Linux), unless that file already has check settings. Your shell files put the shim folder (`~/.local/share/mbx/bin` on Linux, `~/Library/Application Support/mbx/bin` on a Mac) ahead of rustup's `cargo`, so plain `cargo` in a new shell runs through mbx; `command -v cargo` shows which one runs, and in a coding agent ask the agent to run it, since an agent may not read your shell files. The project's `rust-toolchain.toml` still picks the compiler. The machine's budget is worked out when the step runs: `scheduler.memory` is 60% of this machine's installed memory in whole GiB, rounded down (none under 2 GiB, where mbx's own default stays), and `scheduler.reserve_cpus` is 2 cores left for your editor and agents (`memory_percent` and `reserve_cpus` in `[mbx]`). Each is written into mbx's own config file (`~/.config/mbx/config.toml` on Linux, `~/Library/Application Support/mbx/config.toml` on a Mac) only when that file does not set it yet, so a value you set stays: change the budget with `mbx settings set scheduler.memory 24GiB` or `mbx settings set scheduler.reserve_cpus 4`, and Workbench leaves it alone from then on. The step runs without your `XDG_*` variables, so on Linux it always uses `~/.config/mbx` and `~/.local/share/mbx`, and the bash lines look for the shim there: with `XDG_CONFIG_HOME` or `XDG_DATA_HOME` set elsewhere, the mbx your shell runs does not read that budget. A project's `.mbx.toml` still wins over both, and an `MBX_*` variable over everything; Workbench sets none. A config file that is not valid TOML blocks the step with that reason and is not changed. The check reads only what `mbx --version` and `mbx setup --status` report and the config file: `mbx: install mbx 1.30.0 into ~/.local/bin; run mbx setup; set mbx's build budget: scheduler.memory 9GiB (60% of memory), scheduler.reserve_cpus 2`. Once it is done the step is not listed and its script does not run; it is listed again when mbx is missing or older than the pin, its setup is not current or a budget key is gone. Unticking it removes nothing. `apply --reset` forgets the tick even while the step is not listed, so when it has work again it is listed off and marked new, and `apply` asks. To undo it, run `mbx setup --uninstall`, then delete `~/.local/bin/mbx` and the shim folder; while that folder holds the shim, plain `cargo` runs it.

On Linux and WSL, System packages (`linux-packages`) installs bat, fd, jq, ripgrep, htop, tree, ShellCheck and tmux from apt (Ubuntu) or dnf (AlmaLinux), only the ones whose command is missing, and gh, golangci-lint, lefthook and gitleaks as release binaries in `~/.local/bin`; golangci-lint, lefthook and gitleaks are the versions `[linux_binaries]` in `versions.toml` pins (the latest releases in latest mode), each checked against its release's checksums. In pinned mode one already in `~/.local/bin` that reports another version than its pin is replaced by the pinned release, which the plan shows as `golangci-lint 2.14.0 → 2.15.0`; in latest mode it is kept, and gh, which has no pin, is kept in both modes. Command-line tools (`global-tools`) adds gofumpt and govulncheck with `go install`, at the `[linux_go_tools]` versions (the latest in latest mode). On every system each run of that step also sets the global npm, uv, cargo and Go tools that are installed, other than Codex and Claude Code, to their pins in pinned mode and to the latest in latest mode. On every system apply writes `~/.gitconfig` with `push.autoSetupRemote`, `fetch.prune` and `rerere.enabled` on and `merge.conflictStyle` set to `zdiff3` (`diff3` on Ubuntu 22.04, whose Git 2.34 does not know `zdiff3`), and adds `.DS_Store`, `__pycache__/`, `/tmp/` (a `tmp/` folder at a repository's root), `.agents/tmp/`, `.worktrees/`, `.claude/worktrees/`, `.claude/tmp/` (where Claude Code keeps its plans), `.tickets/`, `.claude/settings.local.json` and `CLAUDE.local.md` to Git's global ignore file, `~/.config/git/ignore`, which Git reads in every repository, keeping every line already in it and adding only the entries it lacks; a line that differs only by a trailing `/` or a leading `**/` (the form Claude Code writes) counts as the entry. The instructions Workbench installs for Claude Code (`~/.claude/CLAUDE.md`) and Codex (`~/.codex/AGENTS.md`) tell each agent to create git worktrees in the project's `.worktrees/` folder, adding the folder when it is missing, unless told otherwise.

Several WSL distributions on one Windows user. Each distribution keeps its own answers and choices, but the Windows Terminal settings, the PowerShell profile, `.wslconfig` and the default distribution are one per Windows user, so Workbench lets one distribution write each of those four steps and keeps a record of which, `%LOCALAPPDATA%\Workbench\writers.json`, holding only distribution names. The rules are the same in every distribution: when nobody is recorded for a step, or the recorded distribution is no longer installed (by `wsl.exe --list --quiet`), a distribution with the step ticked writes and records itself, so a PC with one distribution always writes as before; the recorded distribution writes; another installed distribution's step changes nothing whatever its saved choice, and its line reads, for example, `terminal-adoption: = written by AlmaLinux-10`. Which profile new Terminal tabs open belongs to the default distribution step: while a distribution is recorded for it (or the record or the installed list cannot be read), the Terminal settings step keeps the `defaultProfile` the file has. To move a step, tick its `… (take over from NAME)` step under `Optional` in the distribution that should write it: it is listed while another distribution is recorded, asked once as a new step, and remembered as decided but never as ticked, so a tick takes over once and a later move is `apply --choose`, which a coding agent is refused. A record or list Workbench cannot read blocks a ticked step rather than guess. The record is part of the plan, so `--approve-plan` exits 4 when another distribution changed it since the plan was made. Deleting the file is safe: nobody is recorded then.

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
workbench answers import FILE --dry-run --json   # same pattern for answers import
workbench answers import FILE --approve-plan DIGEST
```

The digest covers the files, the effects and which are checked, so a plan
approved from a dry run cannot apply a different selection. If the machine, the
release or the saved selection changed since, `--approve-plan` exits 4 with
`The approved digest does not match the current plan` and the fix `review a
new plan`, and changes nothing: it downloads and installs nothing, rewrites no
file and changes no folder's mode, but for a folder of Workbench's own files
that you cannot read or search, which gets 0700 so the plan can be read. `--approve-plan` is listed in `--help`. From a checkout, add
`--local-build` to both `apply` calls. Keep it and `--destination` identical between
them. `--yes` is for a person who trusts your saved choices, not for a caller that
did not read the plan, and a coding agent is refused it. When Workbench's own tools
are missing, `apply --dry-run` exits 3 and says to run `workbench update`, or
`workbench apply` at a terminal; from a checkout it says to run
`workbench update --local-build`, which installs the tools it pins and which an
agent may run, for example after a pin changes. A tool that is there but that
other users can change is not missing: Workbench does not run it, so
`apply --dry-run` and `doctor` exit 3 naming the tool, its mode and the
command that fixes it:

```text
✗ ~/.local/share/workbench/tools/chezmoi/2.70.3/chezmoi can be changed by other
  users (mode 0720)
    cause  Workbench runs only a program no other user can change
    fix    chmod go-w ~/.local/share/workbench/tools/chezmoi/2.70.3/chezmoi
```

`update` fixes it as well: a tool file that holds exactly the pinned bytes gets
the mode Workbench installs it with, and one that holds anything else is kept
and reported:

```
~/.local/share/workbench/tools/chezmoi/2.70.3/chezmoi differs from the tool Workbench installs
    kept  the file as it is
```

`apply` and `update` ask nothing about installing Workbench and its pinned
tools: running the command is the go-ahead, they change only Workbench's own
files (on a Mac, `apply` also refreshes Homebrew's package list, below), and
`update` keeps the replaced release. Setup asks only the machine
questions your saved answers lack; each asks once, so nothing already answered
is asked again. Esc or ctrl+c at a question stops with nothing saved, and the
next `workbench apply` asks again. To change an answer, name it with
`workbench answers ask NAME`: `workbench answers ask machine_role` asks the role
again, plus any question the new role needs (both emails for `both`, the folder
of a role the machine did not have); the next `apply` shows the plan. Answers the
new role doesn't use are dropped, except the Jira and GitLab tokens an earlier
release saved, which stay until the Move Workbench's files step moves them, or,
without the work role, until you delete them as its plan line says. Without a terminal, setup needs saved answers, which
`answers import FILE` provides. Nothing grants unspecified external
effects: the plan's digest names the effects it runs.

At a terminal, results mark each part ✓ (done), · (nothing to do) or ✗ (not
done) in color (`update` draws its own view instead, above), and a successful
run ends with a green `[WorkBench]` line
saying what it achieved, for example
`[WorkBench] Applied: 3 files, 4 steps; 1 skipped`, or, when the files match and no effect is checked,
`[WorkBench] Nothing to apply; this machine already matches`.
Plans already shown at a prompt are not repeated; `answers import`, `project apply`
and `project revert` with `--approve-plan` show no prompt, so their result keeps
the plan. Piped output, and `NO_COLOR`,
give the same lines without symbols or color.

A plan lists only the steps this apply would actually run: the provisioning
scripts chezmoi would run, the steps whose files change, and a step whose
script chezmoi would skip (a once-only script that already ran, or an on-change
script whose content has not changed) when its check finds something to do,
for example `install lefthook 2.2.2` after `~/.local/bin/lefthook` was deleted.
Every script is checked while planning. When a ticked step's check found
something to do, its script runs in that apply even where chezmoi would skip
it, and the next apply with nothing to do does not run it. On macOS the
System packages check never runs `brew`: it looks for each listed tool's link
in Homebrew's `opt` folder, so a tool removed with `brew uninstall` is listed
as `install jq (nothing is upgraded)`. On macOS, every full plan lists its
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
Node, Cargo's bin folder, `~/.local/bin`, Go's bin folders, then Homebrew's and
the system's folders, the order the managed shell files put them on PATH) and reads
how it was installed and its version from the installer's own files, without
running it. When that version is older than the target, the plan lists
`update-codex` or `update-claude-code`, for example `Update Codex 0.162.0 →
0.163.0 through its standalone installer`. The target is the `versions.toml`
pin, or in latest mode the newest release on the channel the tool's own
updater follows, looked up while planning: Codex's latest channel, and for
Claude Code the `autoUpdatesChannel` in its user settings (in
`CLAUDE_CONFIG_DIR` when that is set; `latest`, `stable` or `rc`, which
nothing publishes yet, so its lookup fails with a warning) or its managed
settings files, which override yours. Project settings are not read, as they
depend on the folder Claude Code starts in. A settings file Claude Code skips
for an error counts for nothing here either: one that is not JSON, or whose
`env`, `autoUpdatesChannel` or `minimumVersion` holds a value Claude Code
refuses, `null` included. Approving it runs that installer:
`npm install -g @openai/codex@VERSION` in the nvm Node it is in, OpenAI's
standalone installer (`chatgpt.com/codex/install.sh`, which keeps the earlier
releases in `~/.codex/packages/standalone/releases`), or `claude install VERSION`, which leaves Claude Code's
channel as it is. A copy as new as the target or newer is left alone; a
prerelease counts as older than its release. On macOS a copy from a Homebrew
cask (`codex`, `claude-code`, `claude-code@latest`) is one of the Homebrew
updates above, at Homebrew's current version, the only one a cask installs. A
copy installed any other way (apt, bun, Homebrew on Linux) is named in the
plan's warnings and left alone, and so is a vendor installer's release run
through a launcher of your own, such as a `~/bin/claude` symlink: each
installer updates only the launcher it makes, `~/.local/bin/claude` or
`~/.local/bin/codex`. A tool with more than one copy in those folders (each a
different executable, such as Claude Code from its own installer in
`~/.local/bin` and from npm in nvm's Node) is named in a warning with every
copy, and none is updated until only one is left: which one runs depends on how
the shell starts, since a login shell can reorder the folders (Ubuntu's
`~/.profile` puts `~/.local/bin` first after it reads `~/.bashrc`). A missing
copy is installed through npm by the command-line tools step.

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
`results`, `warnings`, `errors`, and applicable `operation_id`/`plan_digest`. A
result, warning or error holds its headline as `message`, and, when it has them,
its labeled lines as `lines` (`label` and `text`) and the commands that fix it as
`fix`; the text view prints the same headline with those lines under it, the fix
last.
Human results use stdout and diagnostics stderr; help, completion and the
`--version` flag remain text. Exit codes: 0 complete/unchanged, 1 check/execution failure, 2 invalid input
or state, 3 blocked prerequisites/support/consent, 4 conflict, 5 partial mutation,
130 interruption. Requested skipped work is not complete. Exit 3 covers every
consent refusal (an agent, no terminal, nothing approved, `--choose` without a
terminal); exit 2 covers flags that contradict each other and an invalid command,
flag or argument, whose cause names the reason, for example:

```text
✗ Invalid command, flags or arguments
    cause  unknown flag: --bogus
    fix    workbench --help
```

A signal that stops Workbench (SIGINT, SIGTERM, or SIGHUP when the terminal closes)
during a prompt ends it with exit 130 and writes nothing; during an apply the
checkpoint is recorded as partial or failed rather than left running. The files
chezmoi already wrote are recorded as written, after the same group fix a failed
step gets, so revert can undo them. ctrl+c while a setup step runs stops that
step and ends the apply the same way, with exit 130 and `Interrupted`, with
`kept  what already ran` and the fix `workbench apply`.
If release activation already completed before a later setup/apply failure,
the command reports partial mutation rather than implying nothing changed.

## Existing projects and monorepos

```sh
workbench project inspect .
workbench project apply ./apps/api --language python --dry-run
workbench project apply . --language python --extensions --gitignore --dry-run
workbench project apply . --language python --resolve-dependencies
workbench project apply . --language go --extensions --gitignore --ci --dry-run
workbench project apply . --language rust --extensions --gitignore --ci --dry-run
workbench project apply . --hooks --dependabot --dry-run
workbench project revert . --dry-run
workbench project revert .
workbench project revert . --checkpoint CHECKPOINT_ID
```

Existing uv Python projects/workspaces, the Go module at the selected folder and
the Cargo package or workspace root at the selected folder are configurable.
Inspection also recognizes JavaScript/TypeScript.
Without `--language` every configurable language found is planned;
`--language` only narrows. Unsupported detected projects are reported and
unchanged; explicitly requesting an unsupported language blocks before writes.
Poetry/PDM/requirements-only projects are not converted.

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
resolution as an external effect; preview still runs none. It runs uv with
`--no-sync`, so no environment is synchronized. `--allow-build-hooks`
additionally selects possible native build-code execution and requires resolution.
uv's `--no-build` is not a sandbox. Dynamic/local/direct dependencies and custom
resolver arrangements may remain blocked rather than execute unexpectedly.

A Go module gets a `.golangci.yml` (the standard linters plus errorlint,
modernize, revive and unparam, formatted with gofumpt) only when no
golangci-lint configuration exists at it or above it in the repository; an existing one is kept byte for
byte and named in the plan, with a `golangci-lint migrate` note for a v1 file.
Its `.workbench.toml` records `policies = ["go-v1"]`. A `go.work` at or above
the module blocks the Go plan, and so does selecting a folder whose only
`go.mod` is below it; select the module folder. Go planning runs no Go command
and needs no management Python.

A Cargo package or workspace root gets, in its `Cargo.toml`, Workbench's lints
(only when it has no lint table of its own), `[profile.dev] debug =
"line-tables-only"` when `debug` is missing, and a record in
`[package.metadata.workbench]` or `[workspace.metadata.workbench]`. When the
plan creates `[workspace.lints]`, each member without a `[lints]` table opts in
with `[lints] workspace = true`; other members are named. `rustfmt.toml` (with
the manifest's edition), `deny.toml` (advisories, bans and sources; licence
checks stay the project's) and `.mbx.toml` (crates shared between checkouts;
the compile budget stays in mbx's own config) are written only when the
project has none, and an existing one is kept byte for byte and named. The plan
warns when no `rust-toolchain.toml` installs rustfmt and clippy, and never
writes one. Members come from the manifests' text, as Cargo counts them;
selecting a member is blocked with "select the workspace root", and a
member listed only through a symbolic link is not followed. Rust planning
runs no Cargo or rustup command but, like Python, needs the management Python
and TOML Kit. For both, a table added among dotted keys gets a `[header]` of
its own, and a manifest that would need a key added to an inline table, or
whose merge would not read back with every existing value at its own key,
blocks the plan with a prerequisite that names the file and key; so does a
manifest using syntax newer than TOML 1.0, which that check cannot read. Added lines
end the way the manifest's first line does, CRLF or LF; existing lines keep
their own endings.

`--extensions` merges portable recommendations; it does not install extensions.
`--gitignore` appends relevant absent entries while retaining pattern order.
Both combine every selected language's entries into one edit. `--ci` supports,
for Python, a conservative existing GitHub workflow with an explicit
`.python-version` and identifiable existing test job, and adds owned
Ruff/basedpyright checks. For Go it needs the repository root, a `go` line in
`go.mod` and exactly one workflow with a `go test` step, and adds a
`workbench-go` job: golangci-lint, `go build`, govulncheck and gitleaks, at the
pins in `versions.toml`. For Rust it needs the repository root, an existing
`rust-toolchain.toml` and `Cargo.lock` there and exactly one workflow with a
`cargo test` or `cargo nextest` step, and adds a `workbench-rust` job:
`rustup toolchain install`, `cargo fmt`, `cargo clippy -D warnings`,
`cargo-deny check advisories bans sources` and gitleaks. Existing jobs are preserved. Ambiguous owners and
unsupported YAML receive a manual proposal, not a second pipeline or fabricated
tests.

`--hooks` and `--dependabot` need the selected folder to be the repository
root and every selected project at it. `--hooks` writes one `lefthook.yml`
for every selected language: before each commit a format, lint and
dependency-file check per language (Go `golangci-lint fmt --diff`,
`golangci-lint run`, `go mod tidy -diff`; Rust `cargo fmt --check`,
`cargo clippy -D warnings`, `cargo metadata --locked` and
`cargo deny --offline check bans sources`; Python `ruff format --check`,
`ruff check`, `uv lock --check`) and one gitleaks scan of the staged changes,
and before each push the tests (`go test`, `cargo test`, and `pytest` only when
the dev group lists it). It never runs `lefthook install`; run that once per
clone. `--dependabot` writes `.github/dependabot.yml` with weekly `gomod`,
`cargo` or `uv` and `github-actions` entries at `/`, each grouping its minor
and patch updates into one pull request, and
`.github/workflows/dependabot-auto-merge.yml`, which turns on GitHub
auto-merge for Dependabot's patch and minor pull requests and for no other
pull request. It is GitHub only and works best with "Allow auto-merge" turned
on and a branch rule on the default branch that requires the CI check: GitHub
then refuses a merge before that check passed; Workbench sets neither. While
GitHub lists no required check for the pull request, the workflow waits up to
40 minutes for its other checks and merges only when at least one ran and none failed;
after a failed or slow check, run the workflow's job again. A repository with
no CI check keeps its Dependabot pull requests open. Each file is written only
when the repository has none: an existing lefthook configuration (any of the
names lefthook reads) or Dependabot file is kept byte for byte, and the plan
names the ecosystems an existing Dependabot file lacks and warns when Renovate
is configured too. See the [Python policy](../project/pythonpolicy/README.md), the
[Go policy](../project/gopolicy/README.md) and the
[Rust policy](../project/rustpolicy/README.md).

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
reported with the names of the changed files and a fix that keeps the edit
(`~/.gitconfig changed since Workbench wrote it`, then `changed nothing`, then
`fix mv -n ~/.gitconfig ~/.gitconfig.edited` and `workbench revert
--checkpoint ID`): the changed file moves to a name beside it that nothing has
(`.edited`, or `.edited-2` and on), and revert takes a file that is not there
as moved aside, writes what the checkpoint holds over nothing and says so in
its plan (`~/.gitconfig is not there`, `result revert writes it as the
checkpoint holds it`). A changed folder gets the `chmod` and `chgrp` that give
back the mode, group and hidden flag Workbench wrote, after the commands that
take off an ACL Workbench did not give it and before those that take off such
an attribute (on Linux `setfacl -b`, then `chmod 0755` and `chgrp`, then
`setfattr -x NAME`); a folder that lacks an attribute or ACL Workbench gave it keeps
`undo those changes`, since no command gives one back without its value. Keep the reported checkpoint ID; do
not delete state to bypass a failure. A
folder the apply created that now holds files Workbench did not write, such as
`~/.config/tmux` with the tmux plugins a later step installed, stays with those
files in it, and so does each folder that holds it; revert restores everything
else and names each one in its plan and result (`~/.config/tmux (plugins)
stays`, with the cause `it holds files Workbench did not write`). Revert never deletes a
folder's contents.

An apply that stops after it starts writing files is recorded as unfinished.
Rerun `workbench apply`, with `--destination` for an isolated destination, as
`doctor` prints it. Each file chezmoi wrote before the stop may lack the group,
a folder's set-group-ID bit, the ACL, extended attributes, hidden flag or
SELinux label its approved image holds: chezmoi's own write leaves them out,
and a plan, which compares content, would not show them. The plan shows the
stopped apply as a warning, and `doctor` shows it in the same words: `Apply … to
~/dest did not finish`, the cause `it stopped after chezmoi wrote files, before
Workbench gave them what chezmoi leaves out`, a line for each set of files that
lack the same (`files ~/.gitconfig and ~/.zshrc lack their group, ACL, extended
attributes and hidden flag`), and `result the next apply gives each file what
it lacks before it writes anything, and leaves its content as it is`. The plan
takes each of those files as it will be once given what it lacks, so nothing
it gives is refused, and binds the stopped apply and each file as it is and as
it will be: an edit between the dry run and the apply, or the record going,
stops the apply with exit 4.

Before it writes anything, the apply removes the temporary files the stopped
run staged beside its targets, each named `.workbench-` with that run's ID and
the target's number, and only when it is that run's: your own regular file with
one link, or a link where the target is one, at exactly that name. Then it
gives each file what it lacks, on the file itself, and leaves its content
alone, so a file edited after the stop keeps the edit; what a file holds of
that metadata with another value stays. The
result names those files (`Apply … did not finish earlier`, then `result
Workbench finished it` and `changed ~/.zshrc …`). Then the record is cleared,
including when the plan changes no files because they already match, or the
new apply's record replaces it. The stopped checkpoint keeps its images and
stays revertable. A file whose group you may not give it, since you are not a
member of that group, stops the plan with exit 5, which names every such file
and gives each its own fix line (`~/.zshrc lacks its group wheel`, fix `sudo
chgrp wheel ~/.zshrc`). An unfinished apply of another
destination blocks, with the command that applies there, and so does reverting
a checkpoint other than the one the record names.

A setup step that fails never stops the others: Workbench runs chezmoi with
`--keep-going`, so every other step still runs. A step names what it could not
do (`Runtime managers  blocked  nvm's installer could not be downloaded, so nvm
was not installed`), and a step that needs one that did not finish says what is
missing and skips only what needs it (`Language runtimes  blocked  nvm, uv not
installed yet (earlier setup steps install them), so the runtimes that need them
were skipped`). A script that ends nonzero without saying why is named too: it
stopped with that exit status, and its output above says why. The apply then
ends with exit 3 and `A step could not be done (marked blocked above)`, with
`changed  everything else` and the fix `workbench apply`. chezmoi does
not record a one-time script that failed as run, so rerunning `workbench apply`
tries those steps again. When a file is not as approved after such a run, or no
step named itself, the apply ends with exit 5 and `Setup did not finish`, with
its cause, `kept  what the other steps did` and the fix lines `correct what
the output above names` and `workbench apply`. chezmoi writes the files before it runs the
scripts that follow them, so they stay as approved and the checkpoint records
them as written; where native creation gave a file a different group than the
plan approved, only that group is put back, with, on Linux, the label of a file
it replaced, and a file whose content differs is never touched. When Workbench cannot confirm every file, the message says so;
revert checks those files again and names any it cannot undo.

File recovery does not uninstall tools/extensions, undo runtime upgrades or
revert registry, environment, services, package caches or uncheckpointed script
effects. Supported bytes/types/modes/groups/link targets are recorded, along
with a link's own mode (macOS gives each link one, 0777 less the umask, and a
link Workbench writes gets its recorded mode on the link itself, never on the
file it points to; on Linux every link is 0777) and what a file or link holds
beside its content: its ACL, its extended attributes and, on macOS, the hidden
flag. A file or link Workbench or chezmoi writes in place of one of its kind
gets each of them, set on the new file or link itself, and revert puts each
back. When chezmoi writes the file, the pass after it writes the approved file
anew in its own folder with them, and an apply stopped between the two leaves
chezmoi's file, which revert accepts and puts the earlier file back over. On
Linux the ACL is the one the new mode leaves: `chmod` rewrites an ACL's owner,
mask and other entries, so `~/.bashrc` with `setfacl -m u:nobody:r` at 0640
keeps the entry for `nobody` at the configuration's 0600 with a mask of `---`.
A new file chezmoi makes, at a target that holds no file, gets on Linux the
SELinux label a file made in its own folder gets.

Some metadata still stops the plan, before any write, with the file and what
it holds (never a value), why that stops the write, and the command that
clears it. A folder keeps its
set-group-ID bit: on Linux a folder made in a set-group-ID folder takes the bit
and that folder's group (`inode_init_owner` in the kernel's `fs/inode.c`), so a
folder the apply makes in one is planned with them, and a folder that is there
keeps its own; the set-user-ID and sticky bits, and the set-group-ID bit on a
file, stop the plan. Hard links, special files, a file over 8 MiB, an extended attribute over 4 KiB and file capabilities (`setcap`, which only root may
set back) stop it, and so do the file flags a new file would not have
(`chflags`, `chattr`): `~/.zshrc has the flag uchg`, with the cause `no file
can replace it while it has that flag` and the fix `chflags nouchg ~/.zshrc`.
On macOS an ACL entry
stops it only when it denies you what the write needs, as the system decides
for your user and every group you are in: on a file or link, deleting it,
without which no file can be renamed over it; on a folder Workbench writes
into or one that holds its files, adding or removing files in it; on a folder
a plan removes, deleting it. An entry for another account is kept:
`user:nobody deny delete` on `~/.zshrc` plans, while `group:everyone deny
delete` stops with `~/.zshrc has an ACL entry that denies you deleting it`,
the cause `no file can replace it` and the fix `chmod '-a#' 0 ~/.zshrc`. Of
several entries that deny it, the refusal names those that apply to you, by
the same rule as the system, one `chmod '-a#'` line each, the highest number
first, so typing the lines once clears it. An entry that files made in a
folder inherit stops it too. On Linux a default
ACL on a folder Workbench writes into stops it, since each file made there
would take it; the folder may hold an access ACL and `user.*` attributes,
whose effect on the files in it the folder's mode bounds (an ACL entry that
lets another account write shows as group write, which stops the plan with
`chmod go-w`), but not overlayfs's `user.overlay.*` or any other attribute.
Linux shows `trusted.*` names only to a process with `CAP_SYS_ADMIN`, so one
on a file Workbench replaces is not seen and is not kept. Of the inode flags
`chattr` sets and `lsattr` shows, a file Workbench replaces may carry only
those the file system sets itself, `e`, `I`, `N`, `h` and `E`. A folder
Workbench writes into, or one that holds its files, may carry any flag but `i`
and `a`, under which nothing in it can be added, replaced or removed:
`~/.config has the flag a`, with the cause `no file in it can be added,
replaced or removed while it has that flag` and the fix
`sudo chattr -a ~/.config`. A file system that keeps no
inode flags, such as tmpfs on a kernel before Linux 6.0, counts as having
none; FUSE and 9P mounts are refused before their flags are read.

A target Workbench cannot read, or a folder on the way to one it cannot open
or search, stops the plan with what keeps it out: a mode that keeps you out,
such as `~/.config has mode 0600, so you cannot search it` with the fix
`chmod u+rwx ~/.config` (`chmod u+r` for a file), another user owning it,
with `sudo chown`, or a file where a folder belongs, with `move ~/.config
aside`.

Where a link at a target points is not checked: Workbench and chezmoi replace
or remove the link itself and never write through it, so `~/.zshrc` may link
into a dotfiles checkout or outside the home, and the linked file stays as it
is. A link into Workbench's own files (its source, its config, data, state and
cache folders, or its entry point) stops the plan with the fix `move it
aside`; a link among the folders above a target, which could send a write
elsewhere, stops it too. A link Workbench cannot read, such as one whose ACL
denies its owner reading it, stops it with the same fix: `chmod -h` cannot
change such a link, and `mv` can move it.

Native machine apply rejects group-exclusive modes of files and folders, such
as `0640` and `0750`, avoiding transient exposure before group correction; a
link chezmoi makes is made in its own folder, never in the scratch folder, so
it never holds the scratch folder's group, and its mode is not refused. The
shared direct project/recovery writer sets the group before permissions and
atomic replacement.

On Linux a file Workbench replaces, or chezmoi replaces in an apply, gets the
label the old file had, so a label set with `chcon` stays. Where SELinux is off,
new files get no label and setting one takes `CAP_SYS_ADMIN`, so a replaced file
has none, like every file made then; turning SELinux back on relabels the file
systems. A refused label stops that write before the file is replaced. A change
of label alone, such as `restorecon` makes, is not an edit: it does not stop a
revert.

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
An override that overlaps a target, or the checkout `--local-build` applies,
stops the run with the folder and the variable to change, such as
`~/.config/git is Workbench's cache folder` with the fix `set
WORKBENCH_CACHE_DIR to a folder apart from it`.

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
ACL kept. A folder with other flags, ACLs or attributes stops Workbench, naming
what the folder holds with the command that removes it, and the `chmod` that
gives the folder the configuration's mode instead.
A parent that other users can write to stops Workbench, naming the folder and
giving the command to paste, quoted for the shell where the name needs it:
`chmod go-w ~/.local`. On a Mac, a parent whose ACL lets another user or
group add or delete in it stops Workbench too, with the `chmod -a#` that
removes that one entry; deny entries, such as the `deny delete` macOS puts on
your home folder, never do.

Activation is journaled, not a multi-file atomic transaction. An interrupted
entry-point/state switch fails closed while retaining the prior runtime. Rerun
`update` for the same release; do not manually edit the active-release record.
While no update runs, a `workbench` command that is missing or links to
another release is refused with the `ln -sfn` command that links it to the
active release; a plain file there is kept, and the refusal says to move it
aside first.
An active release whose folder lost its `release.json` is repaired by
installing another release, then this one again: `update` keeps the active
release's own folder as it is.
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
commit. `main` accepts only commits whose `all-checks` CI check passed; the
changelog commit is new, so the workflow pushes it with a deploy key that the
branch rule lets through
([ADR 17](adr/0017-main-requires-ci.md)). It does so only for a tag on `main` that is also the newest full release tag: a
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
