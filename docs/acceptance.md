# Acceptance record

What Workbench has been checked to do, kept current: when behavior changes,
replace its entry instead of adding history, which git keeps. Releases publish
on `v*` tags for personal use. Implemented handlers are not evidence that every
native target is qualified; the open gates are listed below.

## Verified

Checks ran on macOS 27.0 arm64 (Go 1.27.1, go.mod 1.26.8, golangci-lint 2.14.0, Homebrew
chezmoi 2.70.3, uv 0.12.3, Python 3.13.7), in an Ubuntu 24.04 container and in
GitHub Actions. Interactive checks ran under a pseudo-terminal. Unless an entry
says otherwise, changes went only to disposable homes and destinations with
synthetic answers.

### Continuous integration

- Every push to `main` or `feat/**`, every pull request and every release tag
  (through the release workflow, which publishes only if CI passes) runs
  `go-quality` (formatting, lint on macOS and Linux targets, build, the
  safeguard tests, four cross-builds, the action pin check and shellcheck of
  `install.sh` and the scripts in `scripts/`), a full-history Gitleaks scan,
  and `scripts/render-check.sh` for every role and version mode on macOS and
  Ubuntu, plus the work/pinned WSL simulation. Rendering checks shell syntax,
  shellcheck, malformed-input preservation and leaks; it runs no provisioning.
- The safeguard tests cover a preview that tries to run a mutation, a revert
  blocked by a later edit or tampered evidence, a checkpoint that would remove,
  replace or loosen a folder holding Workbench's files, a bundle executable
  that escapes its archive, approval covering exactly the saved selection,
  credential masking in diffs and merged settings, the saved-selection rules (a
  skipped step never runs again unasked, each section of a shared script gated
  by its own step, Windows steps only when selected by name), a coding agent's
  apply refused before setup, a project marker in the home folder never making
  a project-controlled tool trusted, the Mac password staying out of files,
  sudo's arguments and environment, answered only to the apply's own processes
  and its sudo approval dropped when the apply ends, no password read or
  helper made where sudo already uses Touch ID (all macOS), and the costs ledger keeping every response and upgrade
  row.

### Releases, install and update

- Tag pushes publish four bundles and an `install.sh` stamped with the tag.
  git-cliff 2.14.2 with `cliff.toml`, run at a tag, rendered every release
  from the commit history with no unreleased section, and that tag's section
  alone for its release notes. The release workflow downloads git-cliff through
  `scripts/git-cliff.sh`, which checks a pinned SHA-256 (target; the next tag's
  run is the first to use it). The update handoff carries the proxy, CA, color
  and agent variables and nothing else of the caller's environment, a download
  fails after a minute without data instead of a fixed total time, and a
  modified staged copy of a release is replaced on rerun (target; not yet
  observed).
  The `curl` one-liner without `gh`, the `gh` one-liner, and `update`
  downloading from GitHub each installed a release.
  `version --list` marks the latest and installed releases. Switching to an
  older bundle and back worked, and `update 9.9.9` names the missing release.
- `update` installs the release and its pinned tools and never applies the
  machine. From an older release it installs the latest and says
  `[WorkBench] Installed vX and its tools; run workbench apply`; when that
  release is already active it says `[WorkBench] vX is already installed; run
  workbench apply`. `install.sh` installs through `update` and applies nothing,
  and `update --dry-run` stops before installing. Observed on a WSL2 Ubuntu
  26.04 host from a local bundle: install, the already-installed line, and
  `update` without a version keeping a local build newer than the latest
  published release; `install.sh` was not run there.
- Installing Workbench and its pinned tools needs no separate approval: from a
  fresh home with no chezmoi or uv on `PATH`, `update` downloads chezmoi, uv,
  Python 3.12.12 and TOML Kit with a progress line and without a prompt, and
  rerunning it finishes a stage that was declined or failed. (target; observed
  from a clean macOS VM's home with standard input from `/dev/null`; the rerun
  after a declined or failed stage is not observed)
- `apply` decides once: the first approved apply shows the plan and asks, saving
  `skip`, `select` and `decided`; a later apply with nothing new prints the plan
  and applies the saved choices without asking. Observed on an isolated home with
  synthetic answers and every effect skipped (so nothing provisioned), driven
  through a terminal: the first run, with no `decided` saved, asked with no step
  marked NEW and saved `decided` without the steps that had nothing to change
  (they sit on the `Already set` line); a second run asked nothing and printed
  `Nothing to apply`; with one step removed from `skip` and `decided` the run
  asked with that step marked NEW; `--choose` asked with nothing new (q exited
  3 with `machine.toml` unchanged and no final frame printed); `--choose --yes`
  and `--reset --yes` exited 2; `--reset` asked with the defaults and q saved
  nothing; `--yes` and `--approve-plan DIGEST` applied without asking and
  recorded `decided`; a bare `apply` with no controlling terminal (`setsid -w`,
  standard input from `/dev/null`) exited 3 and did not recreate a deleted owned
  file; an owned file edited outside Workbench asked and named the replacement
  (q exited 3). A merged file (Claude Code settings) with an added key
  planned no change. Unattended, `apply --dry-run --json` prints the plan with its digest, and
  `apply --approve-plan DIGEST` applies exactly that plan or exits 4 when the
  machine, release or saved selection changed; `init --answers-from FILE`
  works the same way. Observed on the WSL2 host: the approved digest applied
  exactly its checked effects, a digest taken before a saved-skip change exited
  4 with nothing applied and `machine.toml` byte-identical, and a stale digest
  exits 4 even when nothing would apply; `init --answers-from` was not rerun.
- Consent is checked before setup. In a scratch home with nothing installed
  and no terminal, a bare `apply` exited 3 with `Nothing approved this plan;
  read it with workbench apply --dry-run --json, then pass --approve-plan
  DIGEST`, `apply --choose` exited 3 with `--choose needs a terminal`,
  `apply --choose --json`, `apply --yes --choose`, `apply --reset --yes` and
  `apply --choose --approve-plan` exited 2, and `update ""` exited 2 with
  `VERSION must be a release tag such as v0.2.0`; none downloaded a tool or
  changed a file in the home. `apply --bogus` exited 2 naming
  `unknown flag: --bogus` (a JSON error with `--json=1`). `version` and
  `version --json` printed the version with a malformed `state.json`, and
  `version --list` named the malformed state. With saved answers and
  `select = ["no-such-effect"]`, `apply --dry-run --local-build` exited 0 with
  the warning `Saved choice no-such-effect is not offered here and is ignored;
  workbench apply --reset forgets it` and the same digest on two runs. With
  no Workbench tools installed in an isolated home, `apply --dry-run
  --local-build` exited 3 saying to run `workbench update --local-build`, and
  without `--local-build` and with no release active it exited 3 saying to
  install one with `workbench update`.
- A fresh interactive setup asks every machine question from `apply`,
  including with standard input piped as under `curl | sh`. Esc or ctrl+c at
  a question exits 3, saves no answers and names `workbench apply`, which
  then asks every question. Setup asks only the questions the saved answers
  lack, and `init --ask KEY` asks a saved one again at a terminal without
  network access: work → personal drops the tokens and the Codex work
  servers, coderabbit and `~/repos` trust, and personal → both asks both
  emails and writes the two per-directory Git files. An unknown key, or
  `--ask` with `--dry-run` or without a terminal, is refused before installing.
  `apply --dry-run` with a missing answer asks nothing and names the fix. A
  `--local-build` apply installs the tools its checkout pins when they are
  missing and uses the saved answers without setup questions, and
  `init --answers-from` adopts an existing chezmoi `[data]` table. It refuses a
  FILE open to other users, naming the file, its mode and `chmod 600`, and
  answers with a missing, invalid or unknown key, naming the key and never its
  value (observed in a scratch home).
  `update --local-build` installs those tools alone: in an isolated home with a
  scratch copy of the checkout, `--dry-run` listed the four tools and wrote
  nothing, the install put chezmoi, uv, Python 3.12.12 and TOML Kit in place
  without a prompt, a repeat found nothing to do, and with `CLAUDECODE=1` set a
  bumped uv pin installed only the new uv (a wrong digest was refused and kept
  nothing) after which `apply --dry-run --local-build` planned instead of
  blocking. (target; the questions were not re-asked on a real host, and
  apply's own tool install was not observed)
- Successive installs keep only the new and replaced releases, and remove
  setup contexts and tool versions that no kept release uses. An interrupted
  activation resumes. A staged release with a missing `release.json`, a changed
  file or another source digest is refused, and an unstamped build refuses any
  release source. Missing parents of Workbench's directories are created 0755,
  except `~/.config`, which is created 0700 (observed on an Ubuntu 24.04 VM).

### Planning and apply

- A plan shows each changed file with a summary, such as `+0 −2 lines` or
  `mode 0600 → 0644`, and flags files edited outside Workbench. It lists an
  effect only when chezmoi would run one of its scripts or change one of its
  files. A fresh home lists every effect; an up-to-date machine lists only the
  always-run apps and cleanup steps. A new CI action pin in `versions.toml`
  leaves the runtimes and global-tools scripts unchanged, while bumping a pin
  they install changes them. Each outdated app or formula in
  `packages.toml` gets an `update-<name>` effect naming what else Homebrew
  would install or update. Its starting version is Homebrew's record, as the
  upgrade prints it, even for an app that numbers itself one release behind;
  an app that updated itself past the record shows its own. Pinned packages, and the chezmoi, uv and Python
  Workbench runs, are held, and a rerun keeps the digest.
  Unchecking every effect applies files only and is saved like any other
  selection. (target; not yet observed on a real host)
- On macOS, an apply not given `--approve-plan` runs `brew update` after tool
  setup and before it plans, so the update effects are current; `--dry-run`,
  `--approve-plan` and an isolated `--destination` never do, and a failed
  refresh only warns. (target; not yet observed on a real host)
- A live status line shows planning, the recheck after approval and setup.
  ctrl+c exits 130, writes nothing and leaves `stty -a` unchanged. SIGTERM
  and SIGHUP during the checklist, the checkpoint picker or the Yes/No prompt
  end it the same way (target; not yet observed).
- `revert` and `project` approve with a Yes/No prompt: `n`, Enter and esc
  refuse with exit 3. Apply wrote the planned files, and the next plan had zero
  edits.
- An `apply` whose files all match, with no effect checked and no changed
  selection to save, asks nothing and says
  `[WorkBench] Nothing to apply; this machine already matches`, unless it
  settles an unfinished apply. Observed on the WSL2 host, including the
  settling: an apply stopped by a failed step finished on the next run.
- In the Ubuntu container, the Linux bootstrap put the current `gh` and its
  man pages in `~/.local/bin` over an apt-installed one, the tmux step cloned
  TPM and installed the plugins, and without network it failed only that step.
  A VS Code extension that fails to install no longer stops the rest; the step
  reports it and exits nonzero.
- The Claude, Codex and VS Code merges leave a file byte for byte when no value
  changes. They keep unowned keys and VS Code's 0644, and the VS Code merge keeps
  JSONC comments, key order and layout (observed in an isolated destination). A
  changed enforced value is restored. Quarantine, Finder info and last-used-date
  attributes survive apply and revert.
- On an existing macOS 27 arm64 machine with Homebrew, a full apply completed
  every effect: app and formula updates, VS Code extensions, where only missing
  ones install, and Homebrew cleanup, whose per-formula "Skipping" warnings fold
  into one count. Updating a running app quit it, and Homebrew reopened it. The interactive apply handed the terminal to provisioning
  and took it back without stopping under a job-control shell.
- On a clean macOS 15.7.7 arm64 VM (no Homebrew, no Command Line Tools, no sudo
  ticket, `~/.local/bin` not on `PATH`), from a local bundle with synthetic
  personal/pinned answers: `update` ended `run ~/.local/bin/workbench apply`. The
  interactive apply's plan said `Applying at a terminal asks for your Mac
  password once, before it starts, to install Homebrew.` Every effect completed
  (exit 0, about 15 minutes); the
  runtimes step, planned before nvm and rustup existed, read `install Node … once
  nvm is in place, … the default; …; install Rust … once rustup is in place and
  make it the default`; the run ended `open a new terminal so your shell finds
  workbench and the other tools`. A second apply from a new login shell found
  `workbench` on `PATH`, applied the saved choices without asking or a password
  and changed no file (the GitHub credential helper was already written on the
  first apply, before `gh` existed). On another clean clone, with no terminal and
  no sudo ticket, `apply --dry-run --json` marked only `macos-packages`
  `needs_admin`, and `apply --approve-plan DIGEST` exited 3 with `Installing
  Homebrew needs your Mac password; run ~/.local/bin/workbench apply in a
  terminal`, leaving `machine.toml` and Workbench's state byte-identical and
  nothing else written; the latest-mode plan read `install the newest Node once
  nvm is in place; …; install stable Rust once rustup is in place and make it the
  default`.
- The Mac password, on clean macOS 15.7.7 arm64 VMs with no sudo ticket:
  - An interactive apply that installs Homebrew asked for it once, without echo,
    before it changed anything. A wrong one got `Sorry, try again.` and was asked
    again.
  - Homebrew's installer, the Command Line Tools and Docker Desktop's package
    (which links into `/usr/local/bin`) then installed with no further prompt.
    Every step completed (exit 0, about 15 minutes).
  - No `wb-askpass-*` folder was left, and no sudo approval was still valid
    afterwards.
  - On a set-up VM with Docker Desktop removed, an apply of the saved choices
    asked once, when Homebrew first needed it. It asked again after a wrong
    answer, reinstalled Docker Desktop, and left no folder and no approval.
  - ctrl+c at the up-front prompt exited 130 within a second, with nothing
    changed and the terminal still echoing.
  - ctrl+c at the helper's prompt ended the apply as interrupted provisioning
    (exit 5). The terminal was still echoing, and no folder or helper process
    remained.
  - Without a terminal, `apply --approve-plan` of a plan that installs Homebrew
    exited 3. `machine.toml` and Workbench's state stayed byte-identical, and no
    folder was made.
  - Three wrong answers ending the apply blocked, and a coding agent's apply,
    were observed only with a stand-in sudo.
- Touch ID for sudo, on clean macOS 15.7.7 arm64 VMs with no sudo ticket. A
  virtual Mac has no fingerprint sensor, so sudo there always falls back to its
  password:
  - With Touch ID for sudo off and only `touch-id-sudo` ticked, an interactive
    apply asked once, when the step's sudo needed it (`A step of this apply needs
    your Mac password; Workbench asks for it once.`). With no `sudo_local` it
    created the file from Apple's template, owned by root:wheel, mode 0444, with
    only the `pam_tid.so` line switched on. With a `sudo_local` already there (the
    template plus a comment line, mode 0644) it switched on only that line and
    kept the comment and the mode. No folder or approval was left, sudo still
    took the password afterwards, and the next plan showed the step on the
    `Already set` line. Revert of the apply left `sudo_local` unchanged.
  - Without a terminal, `apply --approve-plan` with the step ticked reported it
    blocked (`sudo needs your Mac password, which only an apply at a terminal asks
    for; /etc/pam.d/sudo_local was not changed`), exited 3 after the rest, and
    left the file byte-identical.
  - With Touch ID for sudo on and a plan that installs Homebrew, the plan line
    read `Applying at a terminal asks for Touch ID (your Mac password where Touch
    ID cannot be used) before it starts, to install Homebrew.` The apply read no
    password itself: it printed its one line, and sudo asked once with its own
    prompt. Homebrew's installer then used that approval, and the apply ended
    with exit 0 after about 90 seconds, with no helper folder, the terminal
    echoing and no approval left. ctrl+c at sudo's prompt exited 130 with
    nothing installed and the terminal echoing.
  - Observed only with a stand-in sudo: three wrong passwords or a refusal ending
    the apply blocked, and an approval that was already valid left alone.
  - Target, not yet observed: an actual touch on a real Mac, and each app that
    needs administrator rights asking for its own touch.

### On a WSL2 host

- On WSL2 with Ubuntu 26.04, from a local `v0.1.8-dev` bundle, `apply` planned
  real deltas (Go 1.27.0 → 1.27.1, the Node default 22 → 26, missing tools,
  the `.wslconfig` change) with no probe left unanswered, and probes
  wrote nothing under the home directory. The approved apply installed `fd`,
  switched the Node default, installed the missing tools, refreshed the tmux
  plugins, moved the costs hooks to `workbench costs ingest` and removed the
  replaced `claude-costs` script and its completion. A skipped `run_once_`
  script was not recorded by chezmoi and ran on a later apply once unchecked.
  Saved skips held under `--yes`, `apply --dry-run --reset` showed every effect checked,
  and `doctor` printed the applied release, date and saved skips. With no
  local VS Code desktop the editor step says it has nothing to do. The
  Windows-side effects were skipped; they remain unqualified (below).

- Plan view and probes, from a local checkout on the WSL2 host, read-only
  (`apply --dry-run`): the header counts files, steps that will run and, once a
  `decided` list is saved, new to decide; files carry a plain title, merged or
  owned, and +N −N counts for a line diff (a line that moves between lists
  counts as removed and added) or "N settings changed" for a merged JSON or
  TOML file, whose panel lists the changed key paths; steps show a plain name
  and one line; effects with nothing to change collapse into one `Already set` line of names; the `Off` heading is
  just "Off"; the Linux editor-extension step is absent on WSL. The interactive list was
  rendered from a scratch harness at 120 and 80 columns with the cursor on a
  file (its diff, wrapped lines keeping their `+`/`−`) and on Windows setup, and
  a no-prompt view at 30 to 80 columns had no line wider than the terminal. With
  synthetic tokens planted in an isolated Codex config, `--dry-run --verbose`
  printed no diff text and none of the tokens through a pipe, and at a terminal
  printed the diff with every token masked. The two WSL Windows probes ran
  against the real host in about 1.2 s and 0.6 s (warm); against stand-in Windows
  tools that hang, fail or answer nothing, each step whose call did not answer
  showed the "could not check" note and the others kept their answer: all
  calls hanging took 8.6 s, `wsl.exe` and `powershell.exe` hanging 9.2 s, and
  nothing was shown as a change or as nothing to change for a call that had not
  answered. The Terminal part, run alone against stand-in Windows
  tools, registered the fonts, wrote `settings.json` once, then said it was up to
  date on a second run with no new copy, and kept a `before-workbench` copy when
  the file had been edited. A `.wslconfig` that already says `memory = 8GB`
  probed and applied as unchanged. Not observed: the 15 s limit on a cold first
  Windows call (warm only, it cannot be made cold on demand), the list in a
  graphical terminal, and any Windows-side apply on a real host. The macOS
  package probe reports what apply does without checking what is installed: a
  check made Homebrew download its formula data into the home folder, which CI's
  macOS render check caught.
- Windows calls on WSL, against stand-in `cmd.exe`, `wsl.exe`, `powershell.exe`
  and `reg.exe` in a scratch home, never the real host: with every apply-time
  call hanging, `wsl.exe --set-default`, the PATH PowerShell, the profile
  PowerShell and the font-registration PowerShell each stopped at 60 s, 120 s
  for two in a row; a hung or failing call blocked only its own step (the
  others still ran and wrote), the script named it on stderr, and through
  `apply --yes` or `--approve-plan` its result line read `blocked` with the
  reason in plain words and the apply exited 3 after finishing the rest. SIGINT,
  SIGTERM and SIGHUP sent to the script, and Ctrl-C to its whole process group,
  left no temporary folder and no stand-in process; the earlier fragment left
  both. Not observed: whether `timeout -k 1` ends the Windows side of a real
  interop call.
- Windows setup and each of its parts (Terminal settings, PowerShell profile,
  font registry) are optional steps, off until ticked, and ticking Windows
  setup ticks none of its parts; only WSL networking starts on. A Windows side
  that cannot be used at all (no interop, not x64) blocks each ticked Windows
  step instead of stopping the apply, a failed Windows download (oh-my-posh,
  fonts, ripgrep, Notepad++ themes) or kernel tuning without sudo blocks only
  the steps that need it, an interrupted oh-my-posh or font copy is repeated
  on the next run, and VS Code's todo-tree path is set by a one-setting edit
  that keeps comments and every other byte and refuses invalid JSON or
  duplicate keys. (target; not yet observed on a real WSL host)
- Coding agents: under a pseudo-terminal in a scratch home, a bare `apply`,
  `apply --yes` and `apply --choose` with `CLAUDECODE=1`, or with `CODEX_CI`, `CODEX_THREAD_ID`,
  `CODEX_SANDBOX` or `CODEX_SANDBOX_NETWORK_DISABLED` set, exited 3 with "An
  agent runs this; use --dry-run --json then --approve-plan", as did `init --ask
  machine_role`, leaving `machine.toml` unchanged and downloading nothing; with
  `--json`, `--choose` exits 2. With `CLAUDECODE=1`, `--dry-run --json` and then
  `--approve-plan` with its digest applied exactly that plan to an isolated
  destination, after the consent check moved ahead of setup.
  The Codex variable names were confirmed in the
  openai/codex source (`unified_exec/process_manager.rs`, `spawn.rs`,
  `shell_environment.rs`) and in the installed binary (0.159.3).

### Revert and recovery

- `revert` offers checkpoints newest first as "undo the apply of …" or "redo
  the apply of …". Without a terminal it lists their IDs and exits 3. It
  restored exact files, modes and attributes, including a folder mode above
  Workbench's data. A later edit to any target blocks restoring all of them.
- In an isolated destination shaped like a Mac's folders (`Library` private,
  hidden and with the `group:everyone deny delete` ACL; `Library/Application
  Support` with the same ACL), an apply with `Application Support` at 0755 listed
  a mode-only edit to 0700 and applied it, keeping the ACL and the hidden flag;
  revert restored 0755 with both intact. With the folder already at 0700 the plan
  listed no edit for it. An inheritable ACL, a flag on the folder itself and an ACL
  on an ordinary managed folder still stopped planning. On a clean macOS 15.7.7
  VM, where the folder is 0700 by default, the first apply listed no edit for it
  and kept its mode and ACL and `Library`'s hidden flag. (target; the 0755
  tightening and its revert are not observed on a real Mac)
- At the 20-checkpoint limit, a plan names the oldest settled checkpoint's
  removal as an effect (a fixed step, with a superseded-incomplete checkpoint
  as the fallback when none is settled). A zero-edit apply settles a recorded
  unfinished apply; one recorded for another destination blocks any apply, and
  an incomplete checkpoint of the same destination no longer blocks a later
  apply. (target; the fixed step, the fallback and the unblocked later apply
  are not yet observed on a real host)

### Linux

- In an Ubuntu 24.04 container, not a native host, a new user with login
  umask 0002 installed a linux-arm64 bundle and setup qualified all tools. With
  `~/.local` group-writable, the install stopped with the `chmod go-w` that
  fixes it.
- On the same umask, an apply with every effect unchecked writes files at 0644
  and folders at 0755, except `~/.config` at 0700. (target; not yet observed in
  the container)
- On an Ubuntu 24.04 arm64 VM (not a container; login umask 0002), from a local
  linux-arm64 bundle with synthetic personal/pinned answers: saving the answers
  created `~/.config` 0700 (`~/.local` and `~/.local/share` 0755) and the plan
  listed no edit for it. A full apply without a terminal (`--approve-plan`)
  completed every step except podman, whose apt install found the package lock
  held by unattended-upgrades; the Linux scripts now wait up to ten minutes for
  that lock, and in the VM `apt-get -o DPkg::Lock::Timeout=600` waited out a
  held lock and installed podman (the full apply with the wait is not yet
  observed). With `~/.config` at 0755 the plan listed `mode 0755 → 0700` as a
  folder that holds Workbench's own files, mode only; the apply made it 0700,
  and `revert --checkpoint` with `--approve-plan` restored 0755. An attended
  apply then applied the saved choices (exit 0), tightened `~/.config` again
  and left the terminal echoing.

### Projects

- `project inspect` lists the candidate files and the projects they make.
  `project configure` applied to a synthetic uv project, with extensions,
  ignore entries, CI integration and native uv resolution. The next plan was
  unchanged; `project revert --dry-run` showed the restore plan and changed
  nothing, and `project revert` restored every file exactly. `--ci` requires
  an explicit `.python-version` and exactly one workflow. Interrupting uv
  staging exits 130 with no project file changed. `project inspect` of a
  scratch tree holding a 9 MiB `package-lock.json` completed with exit 0, and
  a selection inside a repository reported no project above the repository
  root. A `[tool.pyright]` table or a `ruff.toml` below the project root stops
  configure, and block-scalar `run:` steps are read by `--ci` (target; not yet
  observed).

### Costs

- On 2026-10-01, a WSL2 host's full Codex history (thousands of rollouts, tens
  of gigabytes) was ingested into an empty scratch ledger, in memory and on
  disk, about ten times faster than the previous build; its responses and tier
  tables were byte-identical, as were those of a ledger built in two runs
  whose second finished half-written rollouts. A second run with nothing new
  took well under a second. Per-model response and token counts matched an
  independent Python count for one record-era month and one legacy month,
  apart from responses timestamped on the first of the next month in UTC whose
  rollouts sit in the previous month's dated folders: the script dated them by
  folder and that build by UTC time. The report now splits days and months in
  the machine's time zone (target; the comparison has not been repeated). The
  Codex hooks rendered from the `both` role were all `trusted` in Codex's own
  `hooks/list` (app server 0.159), with `currentHash` equal to the
  `trusted_hash` the config merge wrote, and the hash Codex had recorded for
  an existing approved `UserPromptSubmit` hook was reproduced exactly.
  Applying them to a real machine is not yet observed.
- On 2026-10-01, the same host's Claude Code transcripts and Codex rollouts,
  read through links, were ingested read-only into an empty scratch ledger,
  and the build before this change ingested the same files into another: both
  held the same rows, and no row's time, model, project, session, tool or
  counts differed. The Codex rows took their plan from their rollouts. On that
  first ingest every Claude Code row had email and subscription `unknown`, as
  no sign-in had been observed yet, and each Codex row carried a plan, an
  email, both or neither as its rollout named them. Rows of depth-2 subagents
  of Codex 0.118 to 0.134, stored under their parent thread by the earlier
  build, were stored under their root thread by this one; no row's root still
  names a thread that has a parent. Ingesting the rollouts that are not a
  parent of another first and the parents second, or the parents first, gave
  the same roots and attribution as one pass. A simulated `SessionStart` hook
  for a synthetic Claude Code session stored one `session_accounts` binding,
  and the session's rows took `session` evidence for both email and
  subscription; a second session with no hook took `observed`. The hook
  process returned in under a tenth of a second. A hook naming a Codex rollout
  bound a Codex session, and one naming a path under neither tool logged that
  the session was not bound. `costs status` reported Claude Code hooks as
  `missing (workbench apply)` for a settings file with the old `async` hooks
  and `ok` for the synchronous ones. The real Claude Code sign-in was not read
  in this run (a synthetic `oauthAccount` stood in), and a running session's
  behavior after a `/login` elsewhere is not observed.

- Fast-mode Claude Code responses are priced at `@fast` rates, a specific rate
  row beats a family row except for a manual override, the ledger is created
  0600 in a 0700 folder, and the headline counts the rows the table shows
  (target; not yet observed on a real ledger).

### Output

- `workbench version` prints the `--version` line. At a terminal, each result
  line carries a colored ✓, · or ✗, where · marks a step that changed nothing,
  such as tools already in place. Plans shown at a prompt are not repeated,
  and a successful run ends with a green ✓ summary. Piped output and
  `TERM=dumb` print plain lines, `NO_COLOR` drops color but keeps bold and
  faint, and a non-UTF-8 locale gets ASCII in place of ─ … → ✓ ✗. Tables fit
  the terminal from 20 columns up.

## Unqualified release gates

| Area | Remaining evidence or decision |
| --- | --- |
| macOS | Intel runs; `install.sh` from a published release on a clean Mac; on a clean Mac, revert with a later-edit conflict, a step turned off, and the work and both roles. A clean macOS 15.7.7 arm64 VM (the minimum OS) completed personal/pinned provisioning from a local bundle. |
| Ubuntu | Native 22.04/24.04/26.04 amd64/arm64 bundle/provisioning checks. A container, cross-builds and CI rendering are insufficient. A 24.04 arm64 VM ran full provisioning from a local bundle once, with one step failed on the apt lock (since fixed, not rerun). |
| WSL/Windows | Real WSL2.6+/Windows11 24H2+ x64 path/ACL, Terminal/PowerShell preservation and individually approved external-effect checks. Full provisioning is enabled but unqualified: Windows setup, Terminal and PowerShell adoption, the font registry, PATH, default distribution and sysctl are optional effects that start off (only WSL networking starts on), and no real host run is recorded. |
| Editor | Deliberately apply to an intended local profile, then confirm project-tool selection and only ty/native Ruff active. Linux/WSL editor hosts remain unchecked. |
| Release | A one-liner run on a clean machine through full provisioning; native capacity qualification. Releases stay unsigned with no redistribution license by decision. |

Windows ARM integration, native Windows, arbitrary Linux distributions and
additional project package managers/language configurators are not implemented
support commitments. No silent platform waiver is implied.

## Short qualification procedure

Use a disposable user/VM and synthetic credentials. Install a release with
`install.sh` and without a checkout; run `doctor`; preview with `apply
--dry-run`; apply configuration twice and compare; exercise unchanged recovery
and a later-edit conflict. Full provisioning additionally checks actual
shell/Git/editor/theme, runtime/tool outcomes, denied privilege and
optional-effect denial. WSL requires real host paths with spaces/non-default
drives and Windows permissions.

Never execute restart helpers just to test their installation. Record OS/CPU,
tool versions, commands, outcome and known limits here. Reuse existing render,
lint and leak checks; do not add a new lifecycle or benchmark framework.
