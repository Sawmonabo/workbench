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
| `update` | Install the latest release and its tools. Never applies; run `apply` next. `update --dry-run` shows only the install step. |
| `update VERSION` | The same for that release; an older one goes back. `0.2.0` and `v0.2.0` both work; an empty or malformed VERSION exits 2. |
| `init --answers-from FILE` | One-time adoption of a chezmoi config's `[data]` table. |
| `init --ask KEY` | Ask a saved machine answer again, for example `machine_role`. |
| `version` | Print this Workbench version, the same as `--version`. |
| `version --list` | List the published releases, marking the latest and the installed one. |
| `doctor` | What is installed and last applied, any apply that did not finish, tool versions and host checks; no repair. |
| `revert` | Pick a saved checkpoint, then restore its files after conflict checks. |
| `project inspect/configure/revert [PATH]` | Inspect or configure an existing project; see below. |
| `costs` | What Claude Code and Codex spent, by project, from the local ledger the session hooks keep. At a terminal it opens one tab per tool (Claude Code and Codex): ←/→ or Tab and Shift+Tab switch tools, ↑/↓ select a project or model and Enter opens its page (by model or project, day and session), ← or Esc goes back, q quits and leaves the page printed. Without a terminal, `--tool NAME` prints one tool's report. |
| `costs --by model\|account\|month` | The same by another dimension. `--since`/`--until DATE` (a day in this machine's time zone), `--top N`, `--sort cost\|name\|calls`, `--all` (projects outside `~/dev` and `~/repos`), `--detail`, `--tokens`, `--no-rollup`, `--csv` and the global `--json` shape the report. |
| `costs rates`, `costs status` | The merged price card with where each price comes from (`--refresh` refetches the official page), and the ledger's coverage, last ingest, hooks and rate card age. |
| `costs ingest` | The hook command Claude Code and Codex run on session start and end: silent, exits 0, starts a detached worker. `--worker` ingests in the foreground. |

`update` and `version --list` read the releases from GitHub. They need no
login; with a logged-in `gh`, Workbench asks `gh auth token` for its token, which
raises GitHub's rate limit. A download fails when no data has arrived for a
minute, not after a fixed total time, so a slow link works. A token GitHub
refuses (HTTP 401 or 403) is reported as such and names `gh auth status`,
`GH_TOKEN` and `GITHUB_TOKEN`; a used-up rate limit says so. VERSION must be a
release tag such as `v0.2.0` (`0.2.0` is accepted); an empty or malformed value
exits 2. `update` ends with
`[WorkBench] Installed vX and its tools; run workbench apply`. When that
release is already installed it installs no release, still completes any missing
tool, and says `[WorkBench] vX is already installed; run workbench apply`, so
rerunning it finishes a setup that was stopped. A staged copy of a release that
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
ledger lives at `~/.local/share/claude-costs/ledger.sqlite` (override
`CLAUDE_COSTS_LEDGER`), because Claude Code deletes transcripts after about 30
days and the ledger is the only lasting record; Workbench never deletes or
rebuilds it, and only refines rows it recorded (attribution, service tier,
duplicate copies). It is private: 0600, in a 0700 folder. The first `costs` or
`costs status` for a tool that has transcripts but no rows in the ledger
ingests them once inline. Manual rate overrides go in
`~/.config/claude-costs/rates.json` (`CLAUDE_COSTS_RATES`), keyed by model
prefix, with any of `input`, `output`, `cache_write_5m`, `cache_write_1h` and
`cache_read` in USD per million tokens.
The account table names each row's email and plan only from what Claude Code
and Codex wrote down; usage they never recorded shows `not recorded`, with a
note saying since when each tool records it. See the
[ledger design](superpowers/specs/2026-09-30-claude-costs-ledger-design.md) and
[subscriptions](superpowers/specs/2026-10-01-codex-costs-design.md).

## Approval and automation

Each planned file carries a plain `title`, whether Workbench `merged` its settings into the file (your own keys are kept) or owns the whole file, its `added` and `removed` line counts, for a merged JSON or TOML file the number of `settings` that change, and a `summary`. A file Workbench owns that you edited since it last wrote it (by its last checkpoint, else chezmoi's own record of what it wrote) is marked `edited_outside`, and applying replaces your edit. The plan view also shows each file's diff (secrets masked); the diff text is for the screen only and is not in `--json` or the plan digest. For a merged JSON or TOML file whose old and new content both parse, the view lists the changed settings instead of lines (`~ hooks.SessionStart[0].hooks[0].timeout  10 → 5`, `+ … added`, `- … removed`; a list of plain values is compared as a set) and counts them as `N settings changed`, because the merge re-orders the whole file; any other file shows its line diff.

Deciding once. The first approved `apply` shows the plan and saves two things in the `[effects]` table of the private `machine.toml`: the steps you turned off (`skip`, plus `select` for optional steps you turned on) and the steps it showed you something to decide about (`decided`; a step whose check found nothing to change sits on the `Already set` line, where it cannot be turned off, so it is not recorded until it has something to do). Later `apply` runs print the plan and apply your saved choices without asking, unless a step is new (not in `decided`, `skip` or `select`) and has something to do, or a file Workbench owns whole was edited outside it. Until a first approved apply has saved `decided`, no step is marked new and the first `apply` shows the plan once. Applying without asking needs a person at a terminal to read the plan on; with none (a cron job) `apply` refuses unless you pass `--approve-plan DIGEST` or `--yes`. A coding agent never counts as a person, even in a pseudo-terminal: when `CLAUDECODE=1` (Claude Code) or `CODEX_CI`, `CODEX_THREAD_ID`, `CODEX_SANDBOX` or `CODEX_SANDBOX_NETWORK_DISABLED` (Codex) is set, `apply` shows nothing to answer and refuses with `An agent runs this; use --dry-run --json then --approve-plan`, `--yes` and `--choose` are refused the same way (an agent applies only with `--approve-plan DIGEST`), and so are `init --ask` and the Yes or No approval of `revert` and `project` (`revert` without `--checkpoint` still lists the checkpoint IDs). A run that nobody can approve (no `--approve-plan`, no `--yes`, no person at a terminal) is refused with exit 3 before Workbench downloads its tools or saves anything. Files Workbench merges into (Claude Code settings, Codex config, VS Code settings) never count as edited outside, since Claude Code and Codex rewrite them constantly. `apply --choose` always asks. `apply --reset` forgets `skip`, `select` and `decided` for this host's steps and asks again with the defaults; saved choices for steps this host does not list, such as a macOS step on Linux, are kept and ignored. A saved `select` for a step this host does not offer is shown as a plan warning and ignored; `--reset` forgets it. `--yes` never asks. `--choose` or `--reset` with `--yes` is refused (`--reset` forgets what you turned off, so `--yes` would turn it all back on unasked), and `--choose` needs a terminal (exit 3 without one; with `--json` or `--non-interactive` it is an invalid combination, exit 2). An isolated `--destination` never saves choices, so each interactive apply there asks. `--dry-run` never saves. Steps are gated one by one: a shared script with one step off still runs its other sections. On macOS that covers Homebrew packages and Work tools (the Bitwarden CLI), and Mac apps and editor extensions and Work tools (the work editor extension); on Linux, System packages and Work tools (the Bitwarden CLI), and VS Code extensions and Work tools (the work editor extension); on WSL, Windows setup and WSL networking.

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
`[WorkBench] Applied: 3 files, 4 effects; 1 skipped`, or, when the files match and no effect is checked,
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
any tool whose update would change them; the plan names them in its warnings.

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
[portable policy](../project/python/README.md).

## Recovery and runtime storage

Machine and project checkpoints are separate scopes. Recovery checks every
recorded post-image before restoring anything; later user edits or corrupt
images block. A file whose outcome Workbench could not record, because it was
stopped or crashed while chezmoi wrote, is checked when you revert: a file
holding exactly what the apply wrote is restored, one holding exactly what was
there before needs nothing, and the plan says the files were checked now. On a
Mac, a file chezmoi created may still have the group of its temporary folder;
revert gives it the group the apply approved, then restores it. A file holding
anything else blocks like a later edit. A later edit is reported with the names of
the changed files (`Changed since Workbench wrote them, so nothing was restored:
~/.gitconfig. …`). Keep the reported checkpoint ID and resolve conflicts
manually; do not delete state to bypass a failure. A folder the apply created
that now holds files Workbench did not write, such as `~/.config/tmux` with the
tmux plugins a later step installed, stays with those files in it, and so does
each folder that holds it; revert restores everything else and names each one
in its plan and result (`Stays, because it holds files Workbench did not write:
~/.config/tmux (plugins)`). Revert never deletes a folder's contents.

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
bundles with `scripts/package-release.py`, stamps the tag into a copy of
`install.sh`, and publishes all five files as a GitHub release. Its release
notes are that tag's section of the changelog, which git-cliff (a pinned release,
checked against its SHA-256 by `scripts/git-cliff.sh`) builds from the
conventional commit subjects (`feat:`, `fix:`, `docs:` and so on) with
[cliff.toml](../cliff.toml). Once the release is out, the workflow commits the
regenerated [CHANGELOG.md](../CHANGELOG.md) to `main`, so pull before your next
commit. It does so only for a tag on `main` that is also the newest `v*` tag: a
tag off `main` publishes its release and leaves CHANGELOG.md alone, an older
tag leaves it to the newer tag's run, and changelog commits run one at a time.
Build one bundle locally with the pinned Go toolchain and Python 3.11+:

```sh
python3 scripts/package-release.py --version v0.2.0 --target darwin-arm64 --output dist
```

Targets are `darwin-arm64`, `darwin-amd64`, `linux-arm64` and `linux-amd64`.
Existing bundle names are not overwritten. The payload holds machine sources,
portable Python policy and linked dependency/Go license notices, never host
answers, logs, Git metadata or checkpoints. No
redistribution license is granted.
