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
when `gh` is installed and logged in, otherwise with `curl`. It extracts only the
CLI and runs `workbench update VERSION --bundle FILE`, passing every other
argument through:

- `--dry-run` verifies the bundle and shows only the install plan.

Installing never applies the machine; the CLI's last line says to run
`workbench apply`.

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
| `apply --yes` | Never ask: apply your saved choices, and give anything new its default. |
| `apply --reset` | Forget your saved choices and ask again with the defaults; what you pick is saved. |
| `apply --dry-run` | Print the plan and exit; installs, asks and changes nothing. |
| `update` | Install the latest release and its tools. Never applies; run `apply` next. `update --dry-run` shows only the install step. |
| `update VERSION` | The same for that release; an older one goes back. `0.2.0` and `v0.2.0` both work. |
| `init --answers-from FILE` | One-time adoption of a chezmoi config's `[data]` table. |
| `init --ask KEY` | Ask a saved machine answer again, for example `machine_role`. |
| `version` | Print this Workbench version, the same as `--version`. |
| `version --list` | List the published releases, marking the latest and the installed one. |
| `doctor` | What is installed and last applied, any apply that did not finish, tool versions and host checks; no repair. |
| `revert` | Pick a saved checkpoint, then restore its files after conflict checks. |
| `project inspect/configure/revert [PATH]` | Inspect or configure an existing project; see below. |
| `costs` | What Claude Code spent, by project, from the local ledger the session hooks keep. At a terminal it opens one tab per tool (Claude Code, and Codex, which is not implemented yet): ←/→ or Tab and Shift+Tab switch tools, ↑/↓ select a project or model and Enter opens its page (by model or project, day and session), ← or Esc goes back, q quits and leaves the page printed. Without a terminal, `--tool NAME` prints one tool's report. |
| `costs --by model\|account\|month` | The same by another dimension. `--since`/`--until DATE`, `--top N`, `--sort cost\|name\|calls`, `--all` (projects outside `~/dev` and `~/repos`), `--detail`, `--tokens`, `--no-rollup`, `--csv` and the global `--json` shape the report. |
| `costs rates`, `costs status` | The merged price card with where each price comes from (`--refresh` refetches the official page), and the ledger's coverage, last ingest, hooks and rate card age. |
| `costs ingest` | The hook command Claude Code runs on session start and end: silent, exits 0, starts a detached worker. `--worker` ingests in the foreground. |

`update` and `version --list` read the releases from GitHub. They need no
login; with a logged-in `gh`, Workbench asks `gh auth token` for its token, which
raises GitHub's rate limit. `update` ends with
`[WorkBench] Installed vX and its tools; run workbench apply`. When that
release is already installed it installs no release, still completes any missing
tool, and says `[WorkBench] vX is already installed; run workbench apply`, so
rerunning it finishes a setup that was stopped. The hidden
`--bundle FILE` takes a local archive or HTTPS URL instead, which is what
`install.sh` passes.

`revert` restores files only. It does not run an old executable or reverse old
scripts. At a terminal it shows the saved checkpoints, newest first, as "undo
the apply of …" (the files before that apply) or "redo the apply of …" (the
files a revert replaced). Without a terminal it lists them with their IDs and
asks for `--checkpoint ID`; `--dry-run` shows the restore plan.

Developers inside a checkout add the hidden `--local-build` (no value) to
`apply` and `doctor` to use that checkout instead of the installed release; with
it, `apply` uses your saved answers and tools as they are. `apply` and `update`
take `--destination PATH`, an existing folder to configure instead of your home.
Full provisioning requires the real home destination because native scripts
have external effects. See
[configuration ownership](chezmoi-local-overrides.md). `init --answers-from FILE`
saves the `[data]` table of an existing chezmoi config as machine answers, and
`init --ask KEY` asks one saved answer again; see
[switching from dotfiles](switch-from-dotfiles.md). `init --ask` needs a
terminal and refuses `--dry-run`, because it saves what it asks.

`costs` figures are list-price equivalents, not subscription charges. The
ledger lives at `~/.local/share/claude-costs/ledger.sqlite` (override
`CLAUDE_COSTS_LEDGER`), because Claude Code deletes transcripts after about 30
days and the ledger is the only lasting record; Workbench only adds to it.
The first `costs` on a machine with an empty ledger ingests the transcripts
once inline. Manual rate overrides go in `~/.config/claude-costs/rates.json`
(`CLAUDE_COSTS_RATES`), keyed by model prefix, with any of `input`, `output`,
`cache_write_5m`, `cache_write_1h` and `cache_read` in USD per million tokens.
See the [ledger design](superpowers/specs/2026-09-30-claude-costs-ledger-design.md).

## Approval and automation

Each planned file carries a plain `title`, whether Workbench `merged` its settings into the file (your own keys are kept) or owns the whole file, its `added` and `removed` line counts, for a merged JSON or TOML file the number of `settings` that change, and a `summary`. A file Workbench owns that you edited since it last wrote it is marked `edited_outside`, and applying replaces your edit. The plan view also shows each file's diff (secrets masked); the diff text is for the screen only and is not in `--json` or the plan digest. For a merged JSON or TOML file whose old and new content both parse, the view lists the changed settings instead of lines (`~ hooks.SessionStart[0].hooks[0].timeout  10 → 5`, `+ … added`, `- … removed`; a list of plain values is compared as a set) and counts them as `N settings changed`, because the merge re-orders the whole file; any other file shows its line diff.

Deciding once. The first approved `apply` shows the plan and saves two things in the `[effects]` table of the private `machine.toml`: the steps you turned off (`skip`, plus `select` for optional steps you turned on) and the steps it showed you something to decide about (`decided`; a step whose check found nothing to change sits on the `Already set` line, where it cannot be turned off, so it is not recorded until it has something to do). Later `apply` runs print the plan and apply your saved choices without asking, unless a step is new (not in `decided`, `skip` or `select`) and has something to do, or a file Workbench owns whole was edited outside it. Until a first approved apply has saved `decided`, no step is marked new and the first `apply` shows the plan once. Applying without asking needs a terminal to read the plan on; with none (a cron job, an agent), `apply` refuses unless you pass `--approve-plan DIGEST` or `--yes`. Files Workbench merges into (Claude Code settings, Codex config, VS Code settings) never count as edited outside, since Claude Code and Codex rewrite them constantly. `apply --choose` always asks. `apply --reset` forgets `skip`, `select` and `decided` for this host's steps and asks again with the defaults; saved choices for steps this host does not list, such as a macOS step on Linux, are kept and ignored. `--yes` never asks. `--choose` or `--reset` with `--yes` is refused (`--reset` forgets what you turned off, so `--yes` would turn it all back on unasked), and `--choose` needs a terminal. An isolated `--destination` never saves choices, so each interactive apply there asks. `--dry-run` never saves. Steps are gated one by one: a shared script with one step off still runs its other sections.

What the plan shows. One view serves the interactive list and `--dry-run`: a header with counts (files, steps that will run, new to decide), the files, then `Will run`, `Off` (steps you turned off, and Windows setup's parts when you have not ticked them), `Optional` and a faint `Already set` line, names only, for steps whose check found nothing to change (the cursor can rest on one to read what is already in place, but it cannot be turned off). Each step is its plain name with one faint line under it; the cursor row opens a detail panel (what it does, what it would do on this machine now, what it changes, who it runs as, how to undo it). A file's diff has its known secrets and anything shaped like a credential masked, and is printed only at a terminal: never to a pipe or a file, not even with `--verbose`. A step that cannot apply on this host, such as the Linux editor extensions on WSL, is not listed. A step that runs from a script chezmoi reruns when it changes is listed again on the first apply after a release changes that script (the language runtimes, command-line tools and tmux plugin steps, for one), even when nothing else is new. Steps that always run with the files, such as AI safety settings, show without a box. Keys: up and down move, space turns a step on or off, enter opens the detail (a file's diff, full screen; `q` or esc to return), `a` applies, `q` or ctrl+c quits with nothing applied; a mouse click selects a row and clicking the selected row toggles it. Closing the list prints its final frame once. A saved plan run without a prompt prints a compact view headed `Applying your saved choices`. Add `--verbose` to see each row's detail panel and the recovery limits.

Ticking a step is the approval: scripts Workbench runs during apply never stop to ask a second question. What a step would change is in its detail panel beforehand (WSL networking lists each `.wslconfig` setting it will change, for example `networkingMode is virtioproxy, will be mirrored`), and the recovery copy of a file it replaces is still written.

`revert` and `project` still ask "Approve this exact plan?" with Yes and No (y or n, or the arrow keys and Enter; Enter alone, esc and ctrl+c refuse).

Windows setup on WSL has three parts (Windows Terminal settings, PowerShell profile, Windows fonts), listed indented under it. With Windows setup on, its parts show ticked, faint and locked, because it includes them. With Windows setup off, each part can be ticked on its own, and a part ticked on its own keeps that choice when Windows setup is turned on and off again. The other Windows steps (WSL networking, default distribution, PATH, swapping) are separate: the optional ones start off, and ticking one is remembered. Unattended runs see the same selection through `--dry-run --json`.

Probes. Each step's line is what its script would do on this machine, found by running the script in a read-only probe mode before the plan. A script reports `NAME: = TEXT` when it has nothing to do, `NAME: TEXT` for a change and `NAME: ? TEXT` when it could not check, with the reason as `TEXT`. A probe that fails or takes longer than 15 seconds does not block the plan: the step says in plain words what could not be checked, for example "Windows didn't answer in time", and apply checks again when it runs. A step that could not be checked is never shown as a change or as nothing to change. The two WSL Windows scripts make their calls to Windows together (an 8 second limit for all of them in a probe) so that a slow first call after a quiet spell still fits the 15 seconds; a call that does not answer in time or fails gives that step the "could not check" note. Probes write nothing.

While Workbench plans, rechecks an approved plan or sets up
chezmoi, uv and Python, a live line on stderr names the current step and the
time so far, for example `⠧ Planning: asking Homebrew for updates (2s)`; it
clears before any prompt. Without a terminal, and in JSON or non-interactive
mode, each step prints as its own line instead. An interactive apply then hands the terminal to native
provisioning, so sudo and installers can prompt and you see their output as it
runs, and takes it back when they finish. JSON mode does not
prompt; it streams redacted provisioning output on stderr.

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
changes nothing. From a checkout, add `--local-build` to both `apply` calls.
Keep it and `--destination` identical between them. `--yes` is for a person who trusts
your saved choices, not for a caller that did not read the plan.

`apply` and `update` ask nothing about installing Workbench and its pinned
tools: running the command is the go-ahead, they change only Workbench's own
files, and `update` keeps the replaced release. Setup asks only the machine
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
`[WorkBench] Applied: 3 files, 4 effects; 1 skipped`, or, when the files match and no effect is checked,
`[WorkBench] Nothing to apply; this machine already matches`.
Plans already shown at a prompt are not repeated. Piped output, and `NO_COLOR`,
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
any tool whose update would change them; the plan names them in its warnings.

`--json` produces one versioned object with `schema_version`, `command`, `status`,
`results`, `warnings`, `errors`, and applicable `operation_id`/`plan_digest`.
Human results use stdout and diagnostics stderr; help, completion and the
`--version` flag remain text. Exit codes: 0 complete/unchanged, 1 check/execution failure, 2 invalid input
or state, 3 blocked prerequisites/support/consent, 4 conflict, 5 partial mutation,
130 interruption. Requested skipped work is not complete.
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
are 50,000 entries, 32 levels, 1,000 candidates and a five-second cancellation
deadline; narrow the scope on incomplete results. Blocked OS calls may outlast it.

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
[portable policy](../project/python/README.md).

## Recovery and runtime storage

Machine and project checkpoints are separate scopes. Recovery checks every
recorded post-image before restoring anything; later user edits, corrupt images
or unknown interrupted outcomes block. Keep the reported checkpoint ID and
resolve conflicts manually; do not delete state to bypass a failure.

An apply that stops after it starts writing files is recorded as unfinished.
Rerun `workbench apply`: once a fresh approved plan finishes, the record is
cleared, including when that plan changes no files because they already match.

File recovery does not uninstall tools/extensions, undo runtime upgrades or revert
registry, environment, services, package caches or uncheckpointed script effects.
Supported bytes/types/modes/groups/link targets are recorded, along with the
macOS provenance, quarantine, Finder info and last-used-date attributes; other
extended attributes, ACLs, file flags, hard links and special files block
preservation.
Native machine apply currently rejects group-exclusive modes such as `0640` and
`0750`, avoiding transient exposure before group correction. The shared direct
project/recovery writer sets the group before permissions and atomic replacement.

Each scope retains the 20 most recent forward checkpoints plus their paired
recovery records. At the limit, the plan lists removal of the oldest settled
checkpoint for your approval; incomplete checkpoints are never removed. Applies
that change no files allocate none. Limits are 256 targets, 8 MiB per image,
32 MiB raw pre/post images per operation, 50 MiB serialized per pair and 1 GiB per
scope. Recovery journal/metadata capacity is reserved before forward writes;
reaching the forward ceiling does not prevent recovery.

Runtime directories follow platform/XDG defaults; absolute overrides are
`WORKBENCH_CONFIG_DIR`, `WORKBENCH_DATA_DIR`, `WORKBENCH_STATE_DIR`,
`WORKBENCH_CACHE_DIR`, `WORKBENCH_BIN_DIR`. Exact defaults and supported private
filesystems are in the [contracts](superpowers/specs/workbench-contracts.md#runtime-paths-and-context).
Do not share these overrides with project-owned directories or Windows mounts.

Workbench runs with your umask plus 022, so nothing it, chezmoi or its tools
create is writable by other users, even where the login default is 002, as on
Ubuntu. Missing parents of its directories, such as `~/.config`, are created
0755. When the configuration sets such a folder to a different safe mode, the
plan lists the change under "Folder that holds Workbench's own files; mode
only", and revert restores the old mode like any other change.
A parent that other users can write to stops Workbench with the `chmod go-w`
that fixes it.

Activation is journaled, not a multi-file atomic transaction. An interrupted
entry-point/state switch fails closed while retaining the prior runtime. Rerun
`update` for the same release with fresh consent; do not manually edit
the active-release record. Existing processes retain their inherited environment:
Workbench never injects PATH changes into running terminals or AI sessions.

Updates keep storage bounded. The activation plan lists, as `remove` edits,
every staged release except the new one and the one it replaces, plus setup
contexts and private tool versions that nothing kept or recorded uses. They are deleted after activation succeeds, so the previous
release stays available to reinstall.

## Releases

Pushing a `v*` tag runs `.github/workflows/release.yml`. It builds the four
bundles with `scripts/package-release.py`, stamps the tag into a copy of
`install.sh`, and publishes all five files as a GitHub release. Its release
notes are that tag's section of the changelog, which git-cliff builds from the
conventional commit subjects (`feat:`, `fix:`, `docs:` and so on) with
[cliff.toml](../cliff.toml). Once the release is out, the workflow commits the
regenerated [CHANGELOG.md](../CHANGELOG.md) to `main`, so pull before your next
commit. Build one bundle locally with the pinned Go toolchain and Python 3.11+:

```sh
python3 scripts/package-release.py --version v0.2.0 --target darwin-arm64 --output dist
```

Targets are `darwin-arm64`, `darwin-amd64`, `linux-arm64` and `linux-amd64`.
Existing bundle names are not overwritten. The payload holds machine sources,
portable Python policy, generated requirements and linked dependency/Go license
notices, never host answers, logs, Git metadata or checkpoints. No
redistribution license is granted.
