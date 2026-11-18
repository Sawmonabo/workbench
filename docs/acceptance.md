# Acceptance record

What Workbench has been checked to do, kept current: when behavior changes,
replace its entry instead of adding history, which git keeps. Releases publish
on `v*` tags for personal use. Implemented handlers are not evidence that every
native target is qualified; the open gates are listed below.

## Verified

Checks ran on macOS 27.0 and 27.0.1 arm64 (Go 1.27.1 and 1.27.2, go.mod 1.26.8,
golangci-lint 2.14.0, Homebrew chezmoi 2.70.3, uv 0.12.3, Python 3.13.7), in an
Ubuntu 24.04 container and in GitHub Actions. Interactive checks ran under a
pseudo-terminal. Unless an entry says otherwise, changes went only to disposable
homes and destinations with
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
  A tag with a hyphen (`vX.Y.Z-rc.N`) is published as a GitHub pre-release with
  notes for the commits since the tag before it, adds no changelog section and
  commits no `CHANGELOG.md` (observed for `v0.1.26-rc.1`: GitHub's latest
  stayed `v0.1.25` and the changelog job was skipped; the full release after a
  pre-release was checked locally with git-cliff). In a disposable home,
  `update v0.1.26-rc.1` and the `install.sh` one-liner with `--version
  v0.1.26-rc.1` each installed it, `update` without a version kept it, and
  `update --list` showed it on its own line as installed.
  git-cliff 2.14.2 with `cliff.toml`, run at a tag, rendered every release
  from the commit history with no unreleased section, and that tag's section
  alone for its release notes. The release workflow downloads git-cliff through
  `scripts/git-cliff.sh`, which checks a pinned SHA-256 (observed in the
  `v0.1.26-rc.1` run). The update handoff carries the proxy, CA, color
  and agent variables and nothing else of the caller's environment, a download
  fails after a minute without data instead of a fixed total time, and a
  modified staged copy of a release is replaced on rerun (target; not yet
  observed).
  The `curl` one-liner without `gh`, the `gh` one-liner, and `update`
  downloading from GitHub each installed a release.
  `update --list` marks the latest and installed releases; an installed
  release missing from the list, a pre-release or local build, follows on its
  own line (observed for a local build in a disposable home). Switching to an
  older bundle and back worked, and `update 9.9.9` names the missing release.
- `update` installs the release and its pinned tools and never applies the
  machine. From an older release it installs the latest and says
  `[WorkBench] Installed vX and its tools; run workbench apply`; when that
  release is already active it says `[WorkBench] vX is already installed; run
  workbench apply`. `install.sh` installs through `update` and applies nothing,
  and `update --dry-run` stops before installing. Observed on a WSL2 Ubuntu
  26.04 host from a local bundle: install and the already-installed line;
  `install.sh` was not run there. Without a version, `update` replaces a local
  build with the latest published release whatever their versions and says
  `Replacing local build vX with vY, the latest published release`, and a
  published build newer than the latest is kept. (observed on macOS in
  disposable homes from local bundles, against GitHub's latest)
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
  machine, release or saved selection changed; `answers import FILE`
  works the same way. Observed on the WSL2 host: the approved digest applied
  exactly its checked effects, a digest taken before a saved-skip change exited
  4 with nothing applied and `machine.toml` byte-identical, and a stale digest
  exits 4 even when nothing would apply; `answers import` was not rerun.
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
  `update --list` named the malformed state. With saved answers and
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
  lack, and `answers ask KEY` asks a saved one again at a terminal without
  network access: work → personal drops the tokens and the Codex work
  servers, coderabbit and `~/repos` trust, and personal → both asks both
  emails and writes the two per-directory Git files. An unknown key, or
  `--ask` with `--dry-run` or without a terminal, is refused before installing.
  `apply --dry-run` with a missing answer asks nothing and names the fix. A
  `--local-build` apply installs the tools its checkout pins when they are
  missing and uses the saved answers without setup questions, and
  `answers import` adopts an existing chezmoi `[data]` table. It refuses a
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
  files, or, as observed only in a Linux container (below), when a script
  chezmoi would skip finds something missing that its run puts right. A fresh
  home lists every effect; an up-to-date machine lists only the
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
- Codex and Claude Code updates, observed in Ubuntu 24.04 arm64 containers
  (1 GB memory, run as a normal user) with every other step skipped in
  `machine.toml`:
  - Pinned mode, Codex 0.162.0 from the standalone installer and Claude Code
    2.1.290 from the native installer: with `update-claude-code` unticked the
    apply (exit 0) set `minimumVersion` to 2.1.290 and left Claude Code at
    2.1.290. Ticked, the apply showed `Checking Claude Code 2.1.301's signed
    release manifest`, and `claude install 2.1.301` ran from the downloaded
    2.1.301 binary; `~/.local/bin/claude` then ran 2.1.301, whose SHA-256
    matched the manifest's `linux-arm64` value, and `minimumVersion` stayed
    2.1.290 until the next apply raised it to 2.1.301, its plan listing no
    Claude Code update. A `minimumVersion` of 2.1.400 set by hand stayed. The
    Codex update left exactly 0.163.0 selected with the `auto-update-version`
    marker removed.
  - Latest mode, Codex 0.162.0 and Claude Code 2.1.301: the apply (exit 0)
    left Codex 0.163.0 with the marker naming that release and Claude Code
    2.1.302, with `minimumVersion` still 2.1.301.
  - Pinned mode, Codex 0.162.0 and Claude Code 2.1.300 installed with npm in
    nvm's default Node 24.19.0: both planned `through npm`, and the apply left
    that Node's packages at 0.163.0 and 2.1.301 (exit 0).
  - The update script, given a checksum that does not match the downloaded
    binary, removed it before any `chmod` or `install` ran (a `bash -x` trace
    shows neither), linked nothing, left `~/.local/bin/claude` at 2.1.290,
    which still ran, and reported the step blocked. With the release manifest
    or its signature changed by one byte, the apply's check failed with
    `openpgp: invalid signature: RSA verification failure`.
  - With a `sha256sum` that reports a wrong value for the installed binary,
    the script removed the 2.1.302 `claude install` had linked and pointed the
    launcher at 2.1.301 again, which then ran. With the network cut after the
    verified download, `claude install` failed and left an empty
    `versions/2.1.302`, which the script removed, leaving the launcher at
    2.1.301. Under a `minimumVersion` above the target, `claude install`
    reported success and installed nothing, and the step was blocked as
    `claude install left no 2.1.302`. Each ended 0 with the step blocked.
  - On Alpine 3.20 (musl) the script named the platform `linux-arm64-musl`
    and blocked a manifest without it.
  - The installer's older `legacy-platform-npm` layout (Codex 0.130.0, no
    `codex-package.json`) and its `package` layout (0.150.0) both passed the
    check of the `current` link.
  - A `~/bin/claude` symlink into `versions/` was named in a warning, with no
    Claude Code update planned.
  - `claude install 2.1.301` left `autoUpdatesChannel` as it was (`stable` or
    unset); `claude install stable` and `claude install latest` saved it.
  - On macOS, `apply --dry-run` in a disposable home with stub installs laid
    out like each installer planned the same effects in both modes and both
    channels; a Codex 0.163.0-alpha.3 planned its update to 0.163.0 and a
    0.164.0-alpha.1 nothing; a Claude Code newer than its pin planned nothing;
    an nvm default of `lts/*` was followed to the Node it names; an isolated
    `--destination` planned no updates; a `~/go/bin/claude` symlink into
    `versions/` was named in a warning and left alone; an
    `autoUpdatesChannel` of `stable` in `CLAUDE_CONFIG_DIR`'s `settings.json`
    planned the stable release; a `minimumVersion` of 2.1.400 in its
    settings planned no update to 2.1.301 and named the floor in a warning,
    while one of 2.1.200 planned it. A native update on macOS, with its checks and
    roll-back, and Homebrew cask copies are not yet observed.
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
  A VS Code extension that failed to install did not stop the rest, and the
  step exited nonzero; such a step is reported blocked (see the macOS VM
  record below).
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
- The published v0.1.12 release, on clean macOS 15.7.7 arm64 VMs (no Homebrew,
  no Command Line Tools, sudo needing a password):
  - `curl -fsSL https://github.com/Sawmonabo/workbench/releases/latest/download/install.sh | sh`
    installed v0.1.12 and its tools and ended with `run ~/.local/bin/workbench
    apply`.
  - An interactive apply with the personal role, and another with both roles,
    asked for the Mac password once and completed every default step (exit 0,
    about 16 and 17 minutes). The both-roles apply wrote `~/.gitconfig-personal`
    and `~/.gitconfig-work` behind `includeIf` for `~/dev/` and `~/repos/`, and
    installed the Bitwarden CLI and the GitLab extension.
  - `apply --choose` with Homebrew cleanup turned off in the list saved
    `skip = ['brew-maintenance']`, applied the rest, and the next plan listed it
    under `Off`.
  - After a line was added to `~/.gitconfig`, revert of the first apply exited
    4 with nothing restored, naming the file. With the edit undone, it was still
    refused: folders the first apply created (`~/.config/tmux`, VS Code's
    folder) now held files later steps put there (tmux plugins, VS Code's own
    data).
  - In a work-role apply, the extension store returned truncated downloads for
    three VS Code extensions. The script exited 1, so the scripts after it did
    not run and the apply ended with exit 5 and an unclear message; the files
    were correct except for their group, so the checkpoint recorded them as
    unknown. A rerun planned normally.
- The fix for that, from a local bundle on a set-up VM:
  - With two extensions removed and the extension store blocked, the apply tried
    each one twice, reported `Mac apps and editor extensions: blocked — VS Code
    could not install foxundermoon.shell-format, timonwong.shellcheck even after
    a second try …`, still ran Homebrew cleanup and the usage step, and exited 3.
    With the store reachable again, the next apply installed both (exit 0).
  - With the cleanup script made to exit 1 and a managed file deleted, the apply
    rewrote the file, exited 5 with `A setup step failed (chezmoi failed (exit
    status 1)), so the steps after it did not run; what already ran is kept. …`,
    and the checkpoint recorded the file as written, with the group `staff`. The
    next apply settled the unfinished apply and ran the cleanup (exit 0).
- Interrupted and crashed applies, from local bundles on a clean macOS 15.7.7
  arm64 VM (Command Line Tools present) and an Ubuntu 24.04 arm64 VM:
  - On the Mac, ctrl+c at the terminal as the Runtime managers step began ended
    the apply with exit 130 and `Interrupted; what already ran is kept, and
    running workbench apply again finishes the rest.`, not as a failed setup
    step. SIGTERM to Workbench at the same point also ended it with exit 130.
    Both times the checkpoint recorded all 24 files as written, and revert
    restored them (exit 0).
  - `kill -9` of Workbench while VS Code extensions installed left the checkpoint
    `running` with all 24 outcomes unknown, and the files chezmoi wrote had the
    group `wheel` instead of `staff`. The next `apply --dry-run` planned no file
    edits. Revert said `This apply stopped before Workbench recorded every file,
    so each was checked now: …`, gave those files their approved group, removed
    the ones the apply had created and kept `~/Library/Application Support/Code`
    (exit 0).
  - On Ubuntu, a linked `~/.local/bin` stopped Workbench before planning with
    `/home/admin/.local/bin is a link; Workbench keeps its own files only in real
    folders, so make it one` (exit 2). A file of the owner's at
    `~/.local/bin/gh` made an unattended apply end with exit 3 and `System
    packages: blocked — gh was not installed: ~/.local/bin/gh already exists and
    Workbench did not make it, so it was left alone`, with the file untouched and
    everything else applied; once it was removed, the next apply installed gh
    (exit 0).
- A failed step does not stop the others, and revert keeps folders that hold
  other files, from local bundles on a clean macOS 15.7.7 arm64 VM (no Homebrew,
  no Command Line Tools, sudo needing a password) and an Ubuntu 24.04 arm64 VM:
  - On the Mac, personal role, with `raw.githubusercontent.com` unreachable, an
    interactive apply asked for the Mac password once and ran every step in
    about two minutes. Homebrew packages, Runtime managers (nvm's installer; bun
    and Go missing), Language runtimes (nvm missing), Command-line tools, tmux
    plugins (tmux missing), Mac apps and Homebrew cleanup (Homebrew missing)
    each showed `blocked` with that reason; the files and the usage step were
    applied, and the apply ended with exit 3 and `A step could not be done
    (marked blocked above); everything else was applied, and the next apply tries
    it again`. The cargo tools had failed for want of the Command Line Tools,
    which that step checks for first.
  - With the host reachable, the next apply installed Homebrew, nvm, bun, Go, the
    runtimes, the tools, the tmux plugins and the apps (exit 0, about 15 minutes,
    one password prompt).
  - Revert of the first apply then restored 21 files (`~/.gitconfig` and
    `~/.zshrc`, which it had created, were removed) and kept `~/.config`,
    `~/.config/tmux` (plugins) and `~/Library/Application Support/Code` (VS
    Code's own data), each named `Stays, because it holds files Workbench did not
    write: …` in the plan and the result (exit 0).
  - On Ubuntu, with the same host unreachable, an unattended apply ended with
    exit 3: Runtime managers, Language runtimes and Command-line tools named nvm,
    and System packages, tmux plugins and the rest completed. With the host
    reachable, the same build's next apply ran the failed one-time steps again
    and installed nvm, Node 20, 22 and 24, codex, claude and pnpm (exit 0).
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
- On November 17, 2026, on macOS, chezmoi 2.70.3 alone applied the global Git
  ignore template into throwaway destinations. With no file it wrote the eight
  entries; over a file whose last line had no newline it kept both lines,
  ended the last one and appended the eight; over a file holding all eight
  the file stayed byte-identical, and a second apply over the first result
  changed nothing; over a file holding `**/.claude/settings.local.json` it
  kept that line and appended the other seven. In a repository with that file
  as the global ignore, `git status` hid a root `tmp/`, `.agents/tmp/`,
  `.worktrees/`, `.claude/worktrees/`, `.claude/settings.local.json` and
  `CLAUDE.local.md`, and still listed `pkg/tmp/`, `.claude/settings.json`,
  `.claude/skills/`, `CLAUDE.md` and `AGENTS.override.md`. Not observed: an
  agent following the worktree instruction.
  With `.claude/tmp/` and `.tickets/` added to the entries, `git check-ignore`
  matched `.claude/tmp/plans/x` and `.tickets/a.md` and not
  `.claude/settings.json`, and the render check found both entries in the
  rendered file for every role.
- Latest mode's Node, on macOS, read-only (Nov 17): nodejs.org's release list
  began with `v27.0.0-alpha.0.1.1` and `nvm version-remote node` resolved to
  it. The runtimes step's check, rendered for latest mode, reported `Node is
  26.10.0, the default will be 26.13.1`, the newest entry without a
  pre-release suffix, and with an unreachable release list the lookup
  returned nothing. Not observed: the step installing it.
- macOS packages check, read-only (Nov 18): rendered and run with
  `WORKBENCH_PROBE=1` under bash 3.2 and bash 5, with and without
  `WORKBENCH_PROBE_RAN=1`, on a Mac that had every listed formula but the newly
  listed govulncheck, it printed `macos-packages: install govulncheck (nothing
  is upgraded)` from Homebrew's `opt` links alone, `postgresql` (an alias)
  counted as installed. Not observed: a formula removed after the step ran
  being reinstalled by an apply.
- AlmaLinux 10 container, first apply in a new home (Nov 17): System packages
  ended `blocked — oh-my-posh was not installed`, its installer refusing
  `~/.local/bin` because the folder did not exist; a second apply installed
  it. The step creates `~/.local/bin` before it runs that installer (not
  observed in a container).

### On a WSL2 host

- On WSL2 with Ubuntu 26.04, from a local `v0.1.8-dev` bundle, `apply` planned
  real deltas (Go 1.27.0 → 1.27.1, the Node default 22 → 26, missing tools,
  the `.wslconfig` change) with no probe left unanswered, and probes
  wrote nothing under the home directory. The approved apply installed `fd`,
  switched the Node default, installed the missing tools, refreshed the tmux
  plugins, moved the costs hooks to `workbench agent costs ingest` and removed the
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
  just "Off"; the Linux editor-extension step was not listed on WSL, since at
  the time it installed only into a local Linux desktop. WSL now lists it and
  installs into the distribution's VS Code server. The interactive list was
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
  on the next run. (target; not yet observed on a real WSL host)
- Coding agents: under a pseudo-terminal in a scratch home, a bare `apply`,
  `apply --yes` and `apply --choose` with `CLAUDECODE=1`, or with `CODEX_CI`, `CODEX_THREAD_ID`,
  `CODEX_SANDBOX` or `CODEX_SANDBOX_NETWORK_DISABLED` set, exited 3 with "An
  agent runs this; use --dry-run --json then --approve-plan", as did `answers ask
  machine_role`, leaving `machine.toml` unchanged and downloading nothing; with
  `--json`, `--choose` exits 2. With `CLAUDECODE=1`, `--dry-run --json` and then
  `--approve-plan` with its digest applied exactly that plan to an isolated
  destination, after the consent check moved ahead of setup.
  The Codex variable names were confirmed in the
  openai/codex source (`unified_exec/process_manager.rs`, `spawn.rs`,
  `shell_environment.rs`) and in the installed binary (0.159.3).
- Target, not yet observed: the VS Code extensions and Work tools steps
  installing into a real distribution's VS Code server
  (`~/.vscode-server/extensions`) through its own `code-server`, the Windows-side
  extensions left out, and VS Code on Windows then running them in that
  distribution.

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
  an incomplete checkpoint of the same destination does not block a later
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
  held by unattended-upgrades; the Linux scripts wait up to ten minutes for
  that lock, and in the VM `apt-get -o DPkg::Lock::Timeout=600` waited out a
  held lock and installed podman (the full apply with the wait is not yet
  observed). With `~/.config` at 0755 the plan listed `mode 0755 → 0700` as a
  folder that holds Workbench's own files, mode only; the apply made it 0700,
  and `revert --checkpoint` with `--approve-plan` restored 0755. An attended
  apply then applied the saved choices (exit 0), tightened `~/.config` again
  and left the terminal echoing.
- On October 21, 2026, in throwaway podman containers on a WSL2 host, from a
  local `v0.1.28-dev.1` linux-amd64 bundle with synthetic answers and
  credentials. The containers see the WSL kernel, so Workbench treated them as
  WSL; with no `cmd.exe` every Windows step said there is no Windows interop,
  and WSL networking was turned off by hand.
  - AlmaLinux 10.2, role work: while another process held dnf's RPMDB lock,
    the first `sudo dnf` (dnf 4.20.0, installing EPEL) printed `Waiting for
    process with pid N to finish.` and went on once the lock was released, and
    no part reported a lock failure. The apply installed `epel-release`, the
    base packages except curl (the image ships `curl`), bat and fd from EPEL,
    tmux, podman and the Nerd Font (`fc-cache` succeeded); TPM installed the
    plugins and the Dracula theme loaded. The first apply stopped Command-line
    tools on an npm registry HTTP 503 for yarn; the next completed every chosen
    step.
  - AlmaLinux 10.2, role both, with CRB turned off and non-default folders
    (the work folder a symlink): bat and fd still came from EPEL. From a
    `machine.toml` with saved tokens and no folder answers, an unattended
    `apply --dry-run` refused, and an apply at a pseudo-terminal asked only
    the two folder questions, dropped a trailing `/` and kept the tokens. The
    approved apply ran migration 0005 (`work.env` 0600 with all nine names,
    the token keys gone), removed the literal tokens from an old Codex config
    (the plan's settings view showed them as `[REDACTED]  removed`), created
    `personal.env`, and registered both dispatch servers with Claude Code
    2.1.301 at user scope through `mcp-gate`, with the same command, arguments
    and `NODE_VERSION` as the Codex entries. No synthetic value appeared in a
    plan, `--json` or apply log. That apply exited 5, `Native output differs
    from the approved images`: Claude Code's first run, in the Claude Code
    work servers step, rewrote `~/.claude/settings.json` without
    `skipAutoPermissionPrompt`, and the next apply added it and
    `minimumVersion` (exit 0).
  - On October 22, 2026, re-run after the Claude Code work servers step began
    keeping a copy of `~/.claude/settings.json` through Claude Code's first
    run: a fresh AlmaLinux 10.2 container, role both, from the same kind of
    local bundle, with `~/wrk` and `~/per` (neither a symlink), CRB as the
    image has it and no saved tokens. The first approved apply exited 0 with
    every chosen step complete. `settings.json` kept
    `skipAutoPermissionPrompt` and its mode 0600, no
    `settings.json.workbench-first-run` copy was left beside it,
    both dispatch servers were registered with Claude Code 2.1.301 through
    `mcp-gate`, and a later `claude mcp list` left the file byte for byte the
    same. `minimumVersion` was not in the file after that apply: the first
    plan does not set it while Claude Code is not yet installed, so the
    earlier run's exit 5 came from `skipAutoPermissionPrompt` alone. The
    second apply added it (one file, exit 0), and the third plan listed no
    file.
  - The env-files plan lines named the empty names, each line that is not
    `NAME=value`, and a `work.env` left without the work role. A hand-added
    user entry with literal tokens was replaced, and a same-named local entry
    was reported and left alone. Switching to `personal` removed both agents'
    dispatch entries and warned that `work.env` is still there.
  - The folder hook, under `bash -i` and, with the rendered `.zshrc` (`zsh -n`
    clean), under zsh 5.9 on Linux: inside the work folder, through the symlink
    or by its real path, it exported the work names, and never an `export`
    line, a quoted value, an empty value or a name another file set; `cd
    <personal>/x && env` typed in the work folder showed no work name; `chmod
    640` stopped the load and `chmod 400` restored it; a last line without a
    newline loaded; leaving after deleting a line, moving the file or `chmod
    000` unset every loaded name and kept a function of the same name; names a
    parent shell passed down were dropped in the personal folder and the home
    folder; a non-interactive `bash -c` loaded nothing.
  - The non-default folders reached git's `includeIf`, Codex trust and the
    costs report's scope (`2 projects outside ~/per and ~/wrk hidden`). With
    the work folder a symlink, a repository under it got the default git
    email, and a transcript recorded under its real path counted as outside.
  - Ubuntu 24.04, role personal: the apt path installed build-essential,
    curl, unzip, git, bat, fd, tmux and podman (exit 0), with no EPEL step and
    no `work.env`, Claude Code entry or dispatch entry.
  - Not run: arm64; zsh on macOS; real agent sessions (the gate with a stub
    server, the deny rules); the real Windows writes and the writer record; the
    WSL apply path on AlmaLinux in an imported distribution; an image with
    `curl-minimal`, which AlmaLinux 10's repositories do not offer.

### Projects

- `project inspect` lists the candidate files and the projects they make.
  `project apply` applied to a synthetic uv project, with extensions,
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
- On 2026-11-18, on macOS 27.2 arm64 (Go 1.27.4, golangci-lint 2.15.0,
  actionlint 1.7.12, gitleaks 8.30.1), with a disposable home and scratch Go
  modules, using `--local-build` and the `--dry-run --json` then
  `--approve-plan` pair: a module with no lint configuration got
  `.golangci.yml`, `.workbench.toml`, `.vscode/extensions.json`, three ignore
  entries and a `workbench-go` job in its one workflow. `golangci-lint config
  verify` accepted the written file and `golangci-lint run` used it (exit 1 on a
  revive finding); actionlint passed the workflow; the job's govulncheck
  command (v1.9.0) and gitleaks command ran clean locally. The next plan had no
  files or effects, and `project revert` left every file, folder and mode as
  before. A module's own `.golangci.yml` stayed byte for byte and was listed
  as kept; a v1 file, and a v1 `.golangci.toml` in a parent folder, got the
  migrate note. A `go.work`, a `go.mod` only below the selection, no or two
  `go test` workflows, a selection below the repository root and a `go.mod`
  without a `go` line each blocked with their reason. With a release workflow
  using anchors beside the test workflow, only the test workflow changed. A
  `workbench-go` job with a bumped `uses:` ref was kept and reported; a changed
  `run` line stopped the plan. A Python and Go project at one root with every
  option applied as one checkpoint and reverted exactly; a uv project with a
  nested Go module and `go.work` still planned, with a warning. A uv project
  with `--extensions --gitignore --ci` applied and reverted exactly.
  golangci-lint 2.15.0 reads a configuration from parent folders, and rejects
  `version: 2` in `config verify` while `run` accepts it. Not observed: a CI
  run of the `workbench-go` job on GitHub (setup-go without a `go.sum`, the
  runner's tools), Linux, or a module with a real dependency graph.
- On 2026-11-18, on macOS 27.2 arm64 (Rust 1.99.0, rustup 1.29.3, cargo-deny
  0.20.2, actionlint 1.7.12, gitleaks 8.30.1), with a disposable home and
  scratch Cargo crates and workspaces, using `--local-build` and the
  `--dry-run --json` then `--approve-plan` pair: a crate with no
  configuration got the policy lints, `[profile.dev] debug =
  "line-tables-only"` and `[package.metadata.workbench]` in `Cargo.toml`,
  `rustfmt.toml` (edition 2024), `deny.toml`, `.mbx.toml`,
  `.vscode/extensions.json` and `target/` in `.gitignore`, and a warning that
  no `rust-toolchain.toml` pins the toolchain. `cargo check` printed no
  manifest warning, `cargo fmt --check` passed, rustfmt reported edition and
  style edition 2024 with the file and 2015 without it, `cargo clippy` warned
  on an added `unwrap()` and exited 0 (101 with `-D warnings`), and `cargo
  deny check advisories bans sources` passed while bare `cargo deny check`
  failed on the unlicensed crate (exit 4). A virtual workspace and one with a
  root package each got `[workspace.lints]`, `[workspace.metadata.workbench]`
  and `[lints] workspace = true` in each member without lints, including a
  member reached only through a path dependency and, for the root package,
  the root manifest; a member with its own `[lints]` was named and left
  alone, and clippy passed with no manifest warning. A workspace with its own
  `[workspace.lints]` (`unsafe_code = "forbid"`, `all = "deny"`, comments)
  kept it unchanged, got only the record, the dev profile setting and the
  three files, and named the member that does not opt in; adding the policy
  lints beside `all = "deny"` instead was observed to fail clippy's
  `lint_groups_priority`. An existing `rustfmt.toml`, `.rustfmt.toml`,
  `deny.toml`, `.cargo/deny.toml` and `.mbx.toml` each stayed byte for byte
  and was listed as kept; `rustfmt.toml` beside `.rustfmt.toml` stopped the
  plan. Selecting a member, a `members` pattern naming `vendor/*`, and
  `crates/**` or `../shared` patterns each blocked with their reason. `--ci`
  without `rust-toolchain.toml`, or with two workflows running `cargo test`,
  blocked; with one, beside a release workflow using anchors, only that
  workflow gained `workbench-rust`, which actionlint passed and whose
  commands (`rustup toolchain install`, `cargo fmt`, `cargo clippy -D
  warnings`, `cargo-deny --locked check advisories bans sources`, gitleaks)
  ran clean locally. A bumped `uses:` ref in the job was kept and reported; a
  changed `run` line stopped the plan. A repository with Go and Rust, each
  with its own test workflow, planned both jobs into their own workflows, and
  a uv project with a crate at its root planned both policies in one adapter
  call; a uv project with a crate below it planned Python with a warning. A
  uv project, and a uv project with a Go module, each applied with
  `--extensions --gitignore --ci` and reverted exactly.
  On 2026-11-18, manifests written as dotted keys (`package.name = "a"` at the
  root, `metadata.docs.x = 1` under `[package]`, `[tool]` with
  `ruff.line-length`, `tool.ruff.line-length` at the root) applied with every
  existing value at its own key in `cargo metadata` and `tomllib`, an inline
  `package.metadata` or `tool.ruff` blocked with a prerequisite naming it, an
  excluded crate (`crates/*` with `exclude = ["crates/skip"]`, an excluded
  parent folder, an excluded path dependency) was left untouched while a
  member named literally under an exclude was configured, each as `cargo
  metadata --no-deps` listed them, and CRLF `Cargo.toml` and `pyproject.toml`
  gained only CRLF lines.
  Every second plan had no files or effects, and every `project revert` left
  every file, folder and mode as before. Not observed: a CI run of the
  `workbench-rust` job on GitHub (the runner's rustup, the musl cargo-deny
  build), Linux, crates with registry dependencies, or `.mbx.toml` read by
  mbx.
- On 2026-11-18, on macOS 27.2 arm64 (Go 1.27.4, golangci-lint 2.15.0,
  Rust 1.99.0, cargo-deny 0.20.2, uv 0.13.7, lefthook 2.2.2, gitleaks
  8.30.1), with a disposable home and scratch Git repositories, using
  `--local-build` and the `--dry-run --json` then `--approve-plan` pair:
  `--hooks --dependabot` on a Go module, a Cargo crate and a uv project each
  wrote `lefthook.yml` and `.github/dependabot.yml` (`gomod`, `cargo` or `uv`,
  plus `github-actions`, weekly at `/`) and listed the `git-hook-checks`
  effect, and the result line named `lefthook install`. Two and three
  languages at one root got one file of each with every language's jobs and
  one `secrets` job, and `--language go` on the three-language root wrote
  only Go's. A uv project whose dev group has no pytest got no pre-push job
  and a plan line saying so. `lefthook validate` accepted every written file,
  and the written `dependabot.yml` files passed `check-jsonschema` 0.35.0's
  Dependabot schema, which refused a made-up ecosystem. In copies of the
  applied repositories with `lefthook install` run there, `git commit` exited
  1 for a formatting difference, a lint finding and a stale dependency file
  in each language and for a staged synthetic AWS key, and
  `lefthook run pre-push` exited 1 on a failing test. Each of the fifteen
  lefthook main configuration names stayed byte for byte and was listed as
  kept, a second one was named, and a `lefthook-local.yml` was named as merged
  over the written file. An existing `dependabot.yml` stayed byte for byte
  and the plan named the ecosystems it lacked; with `dependabot.yaml` beside
  it the plan named the ignored `.yaml`, and an invalid one stopped the plan.
  Every written `dependabot.yml` has a `minor-and-patch` group per ecosystem,
  and the schema refused an `update-types` value of `tiny`. Every
  `--dependabot` run wrote `.github/workflows/dependabot-auto-merge.yml`,
  which `actionlint` 1.7.12 accepted, listed the `dependabot-auto-merge`
  effect and gave the auto-merge settings warning; an existing file of that
  name, and another workflow running `gh pr merge --auto` for Dependabot, were
  kept and listed, and adding the file between the dry run and
  `--approve-plan` exited 4 with nothing written. With the workflow written,
  the Python `--ci` second plan had no changes. Each Renovate
  location (`renovate.json*` at the root or in `.github/`, `.renovaterc*`, a
  `renovate` key in `package.json`) gave the two-bots warning and the file was
  still written. A selection below the repository root, and a uv project
  below the selected root, blocked with their reasons. With every option
  (`--extensions --gitignore --ci --hooks --dependabot`) each language
  applied as one checkpoint. Every second plan had no files or effects, and
  every `project revert` left every file, folder and mode as before.
  lefthook's `*` matched across folders while `go.{mod,sum}` matched at the
  root only; `go mod tidy -diff` failed offline with an empty module cache,
  and `uv lock --check` passed offline with an empty cache until a manifest
  changed. A `pyproject.toml` or `Cargo.toml` record whose `schema_version`
  is `2`, `"1"`, `1.0` or missing stopped the plan naming the record's table
  and manifest. Not observed: Linux, a Dependabot run on GitHub (grouped pull
  requests, the auto-merge workflow on a real pull request, or `gh pr merge
  --auto` with and without a required check), a Go module
  or crate with registry dependencies in the hooks, or lefthook installed by
  the machine step.

### Costs

- On 2026-10-01, a WSL2 host's full Codex history (thousands of rollouts, tens
  of gigabytes) was ingested into an empty scratch ledger held in memory in 18
  seconds and into one on the host's disk in 24 to 26 seconds; its responses
  and tier tables were byte-identical to those of a ledger built in two runs
  whose second finished half-written rollouts. A second run with nothing new
  took 0.3 seconds. Per-model response and token counts matched an
  independent Python count for one record-era month and one legacy month,
  apart from responses timestamped on the first of the next month in UTC whose
  rollouts sit in the previous month's dated folders: the script dated them by
  folder and that run's report by UTC time. The report splits days and months
  in the machine's time zone (target; the comparison has not been repeated). The
  Codex hooks rendered from the `both` role were all `trusted` in Codex's own
  `hooks/list` (app server 0.159), with `currentHash` equal to the
  `trusted_hash` the config merge wrote, and the hash Codex had recorded for
  an existing approved `UserPromptSubmit` hook was reproduced exactly.
  Applying them to a real machine is not yet observed.
- On 2026-10-01, the same host's Claude Code transcripts and Codex rollouts,
  read through links, were ingested read-only into an empty scratch ledger, and
  an ingest without the nested-subagent root walk ingested the same files into
  another: both held the same rows, and no row's time, model, project, session,
  tool or counts differed. The Codex rows took their plan from their rollouts.
  On that first ingest every Claude Code row had email and subscription
  `unknown`, as no sign-in had been observed yet, and each Codex row carried a
  plan, an email, both or neither as its rollout named them. Rows of depth-2
  subagents of Codex 0.118 to 0.134, which that ingest stored under their parent
  thread, were stored under their root thread; no row's root names a thread that
  has a parent. Ingesting the rollouts that are not a parent of another first
  and the parents second, or the parents first, gave the same roots and
  attribution as one pass. A simulated `SessionStart` hook for a synthetic
  Claude Code session stored one `session_accounts` binding, and the session's
  rows took `session` evidence for both email and subscription; a second session
  with no hook took `observed`. The hook process returned in under a tenth of a
  second. A hook naming a Codex rollout bound a Codex session, and one naming a
  path under neither tool logged that the session was not bound. `agent costs status`
  reported Claude Code hooks as `missing (workbench apply)` for a settings file
  with the old `async` hooks and `ok` for the synchronous ones. The real Claude
  Code sign-in was not read in this run (a synthetic `oauthAccount` stood in),
  and a running session's behavior after a `/login` elsewhere is not observed.

- Fast-mode Claude Code responses are priced at `@fast` rates, a specific rate
  row beats a family row except for a manual override, the ledger is created
  0600 in a 0700 folder, and the headline counts the rows the table shows
  (target; not yet observed on a real ledger).
- `workbench migrate` and the apply's **Move Workbench's files** step
  move the costs files out of the `claude-costs` folders into Workbench's
  runtime folders with every ledger row, the import worker moves them by
  itself after an update, and the old completion and `claude-costs` script
  caches are deleted (target). On 2026-10-16, in a Linux container (kernel
  7.0.14-linuxkit, Docker 29.8.2, Ubuntu 24.04 arm64) with a disposable home
  whose old files v0.1.25 wrote by ingesting a synthetic transcript,
  `workbench migrate --dry-run` listed three pending migrations, `migrate --probe`
  printed one line for each, and `migrate --dry-run --json` then
  `--approve-plan DIGEST` moved the ledger, both price caches and the
  overrides, deleted the old import log and lock, the completion cache and
  the old script's cache, removed every `claude-costs` folder, and wrote
  `migrations.json`. The new folders were 0700 and the files 0600, the
  ledger's row count in every table matched the count before the move, and
  `integrity_check` returned ok. With the data folder on a tmpfs, the ledger
  took the copy path across filesystems, with the same row counts and
  `integrity_check` ok. With old and new ledgers both present, both stayed
  unchanged, the output named them, the other migrations still ran, and
  `migrate` and `migrate run` exited 4. `apply --local-build
  --dry-run` showed `checking migrations` and `migrations: 3 to run` (or `1
  needs attention` for the conflict), and with nothing pending `migrations:
  none to run` with the step under "Already set", on a home on ext4 and on
  the container's overlay root. The apply's migrations script, the import
  worker's own move and macOS were not observed.
- An approved plan binds Workbench's runtime folders. On 2026-10-16, on macOS
  in a disposable home with synthetic answers, `apply --local-build --dry-run
  --json` gave a different `plan_digest` for each of two `WORKBENCH_DATA_DIR`
  values and the same digest for the same folder twice, and `apply
  --local-build --approve-plan DIGEST` with the digest from one folder, run
  under the other, exited 4 (`Approval digest does not match the current
  plan; review a new plan`) before applying anything; `migrate
  --approve-plan` under another data folder exited 4 the same way.
- On 2026-10-16, on macOS, the Claude Code and Codex hooks rendered with
  Workbench's bin folder: `render-check.sh` with the default bin folder rendered
  byte-identical to hooks with the fixed `~/.local/bin` path, a bin folder
  holding a space planned Claude Code settings +5/−5 lines, Codex `config.toml`
  +4/−4 and `hooks.json` +4/−4, with the quoted path on every hook and Codex
  trusting each by its hash, and switching back rendered identical. Its stage
  "hooks follow a bin folder with a space" passed, and failed against hooks with
  the fixed path (9 `BIN FAIL` lines). `agent costs status` with a spaced, quoted bin
  path reported both tools' hooks `ok`, and `missing` when the Claude Code path
  was altered.

### Agent command guard

- Before the guard was built, the command-shape facts it matches on were
  recorded on macOS with Claude Code 2.1.299 (`claude -p`) and Codex 0.162.0
  (`codex exec`), using throwaway hooks: each shell command is its own process
  group directly under the agent, a post hook's text reaches the model, and
  `tool_use_id` is the same in a call's pre and post hooks (the
  [guard design](superpowers/specs/2026-10-14-agent-command-guard-design.md),
  section 2). Those probes did not run the guard.
- On 2026-10-14, the unit tests of the two pure decisions (which processes are
  an agent's command, and which processes a stop may signal) passed on macOS
  and in a Linux container, and the Linux-only readers compiled and linted for
  Linux without being run. A macOS smoke check in an isolated state folder,
  with a fake agent standing in for Claude Code and Codex and
  `WORKBENCH_GUARD=dry-run`, tracked one shell command and one single simple
  command once each, never tracked a denied call's command again, left
  `guard/` at 0700 and its files at 0600, and started a fresh watcher when the
  previous one was killed. The hooks exited 0 with no output for malformed
  input, unknown flags and a relative state override, and, under a release
  that has no `guard` command, exited 0 with `unknown command "guard"` on
  stderr (the `|| true` hides only the exit status). `render-check.sh` passed
  for the personal, work and both roles in pinned and latest modes and for the
  static WSL simulation: the rendered Claude Code settings carried the
  `PreToolUse`, `PostToolUse` and `PostToolUseFailure` hooks, the rendered
  Codex hooks carried `PreToolUse` and `PostToolUse` with trusted hashes, and
  a stale managed guard hook in an existing settings file was replaced while a
  user's own hook was kept.
- On 2026-10-17, on macOS in an isolated home with two locally built release
  bundles installed by `workbench update VERSION --bundle FILE` and
  `WORKBENCH_GUARD=dry-run`, a fake agent's `sleep` was tracked; installing
  the second release made the watcher hand over about a second after the
  update finished (`handover` in `agent guard logs`), and the new watcher tracked
  the same command. `pgrep` sampled every 20 ms showed the new watcher beside
  the old one only for the hand-off, before the old exited. A dangling
  `workbench` link, a file that is not executable and a build without
  handover each left the old watcher running with one logged line; pointing
  the link back at the running release did nothing, and a downgrade handed
  over once. A watcher started afresh matched a command that began before
  it. In a Linux container (Debian, arm64), switching the `workbench` link
  between two builds handed over the same way, the new watcher tracked the
  command, and a dangling link was logged once; there the release was not
  installed through `update`.
- On 2026-10-17, after the process, memory and machine readers the guard and
  the planner share moved into one package per system, the same commands gave
  the same output as before the move: `doctor`, `apply --local-build --dry-run
  --json` in a disposable home with synthetic answers (the same `plan_digest`)
  on macOS and in an Ubuntu 24.04 container as uid 1000, the Linux `answers
  import FILE --dry-run --json` plan, and `render-check.sh personal
  pinned wsl`; the macOS plan differed only in the answers-file path it
  quotes.
- On 2026-10-17, on macOS in disposable homes with a fake agent and
  `WORKBENCH_GUARD=dry-run`, `workbench agent guard logs` printed one line per event
  (time, level, event, then `id=`, `agent=`, `subagent=`, `project=`, `by=`,
  `dry_run=`, `took=`, `size=`, `peak=` and `cmd=` where they apply, the
  command quoted when it holds a space), a `running` line for a command still
  running after 5 s under `--follow`, the log's own records under `--json`,
  and `No guard events since …` (exit 0) for an empty `--since` window.
  `NO_COLOR` and piped output dropped the color.
- On 2026-10-17, on macOS in disposable homes, `workbench agent guard` ran
  against a fake agent's commands, dry run first. It listed four commands in
  four folders newest first with their project, the subagent of the one a
  subagent ran, their memory and peak, and kept the selection on its row as
  rows arrived above it. Tagging two rows and stopping them logged both stops
  as `by=you dry_run=true` and left the commands running. Details showed the
  folder, full ID, a memory graph and the process tree, and `k` there opened
  the same stop menu. Below 60 columns the view asked to be widened; at 60 it
  showed ID, status, time, memory and command, at 75 also agent and peak, and
  at 90 every column. `NO_COLOR` dropped the color and kept bold and faint,
  and an ASCII locale replaced the marks. Live, a polite stop of a command
  that ignores SIGTERM showed `◌ stopping`, then `✗ stopped` after the SIGKILL
  5 s later; a forced stop showed `✗ stopped` within two seconds; `k` on a
  finished row answered `already finished`; and each post hook returned
  `Stopped by you (workbench agent guard).` as `additionalContext`. A stop
  sent by a command the agent ran was refused (`not allowed`). A client that
  stopped reading was disconnected while another kept receiving one snapshot a
  second. The view stayed connected through a handover, in which the socket
  was passed to the new watcher rather than recreated. When the watcher was
  killed it showed `○ not running` with the last rows dimmed and reconnected
  by itself when the next hook started a watcher. Against a stand-in watcher
  that speaks another protocol version, the view said to run `workbench
  update`. With `CLAUDECODE=1`, without a terminal, with `--non-interactive`
  or with `--json`, it exited 3 before opening the screen.
- On 2026-10-18, on macOS in a disposable home with a fake agent under
  `WORKBENCH_GUARD=dry-run`, `workbench agent guard` drew the four-line
  header: the brand, `● watching` and the clock on the first line, the counts
  with `mem 7.9/16 GB`, the largest peak and `swap 4.5/6.0 GB` on the third,
  and the dry-run notice on the fourth. The `mem` figure matched `vm_stat`
  (anonymous less purgeable plus wired plus compressor pages, times the page
  size) read a second apart. `→` and `l` opened the selected command's
  details, `←` and `h` closed them, `←` closed the stop menu, and the key
  lines read `enter/→ details` and `esc/← back`. At 60 columns the count line
  dropped the swap; at 60×11 the stop menu drew below the header, and at 10
  lines the view asked for 11. The escape codes carried the pinned colors
  (the running mark and the INFO label green, the dry-run notice gold, the
  agent clay) at 24-bit and 256-color depth, `agent guard logs` colored the level
  label alone with the agent in its hue and the event and `id` plain, and
  `NO_COLOR` printed plain text.
- On 2026-10-18, in a Debian container (`python:3-slim`, arm64) capped at
  2 GiB as uid 1000 with the same fake agent, the view's header read `mem 63
  MB/2.0 GB`, the container's `memory.current` less its `inactive_file`
  over its limit, and `→`, `h`, the 60-column count line, the pinned colors
  and `NO_COLOR` behaved as on macOS; `agent guard logs` colored the level and the
  agent the same way.
- On 2026-10-17, in an Ubuntu 24.04 container (Docker 29.8.2, kernel
  7.0.14-linuxkit, arm64) capped at 2 GiB, as uid 1000 with a disposable home
  and Python added to the container for the stand-in agent, whose commands ran
  in Claude Code's bash shape, `workbench agent guard` ran through `docker
  exec -it` with the same results as on macOS: four dry-run rows with project
  and subagent, a tagged pair stopped as `by=you dry_run=true` with the
  commands still running, details with the process tree, the narrow layouts,
  then a live polite stop of a command that ignores SIGTERM (`◌ stopping`,
  `forced` 5 s later, `✗ stopped`), a forced stop, `already finished` on a
  finished row, and the stop reason from both post hooks. The header showed
  the machine's figures on the first screen (`11 GB of 11 GB available`) and
  the container's limit after that (`1.9 GB of 1.9 GB available`). Pointing
  the home's `workbench` link at a second build while four commands ran and
  three finished rows were shown handed over about a second later: the
  header's release changed, every row, peak and the largest peak stayed, a
  running row's memory graph kept its earlier readings, and `agent guard logs`
  showed one `handover` line naming both releases. Killing that watcher showed
  `○ not running`; the next hook started a fresh watcher, the view reconnected
  by itself, and the finished rows, which live only in the watcher's memory,
  were gone. Without a terminal and with `CLAUDECODE=1` it exited 3.
- On 2026-10-17, with ten tracked `sleep 60` commands, sampling `ps` every 10
  s for a minute, the watcher used 0.41 s of CPU (about 0.7 %) at 30–31 MB
  resident on macOS and 0.39 s (about 0.6 %) at 22–24 MB in the Linux
  container above; the open view used 0.42 s (about 0.7 %) at 28–31 MB on
  macOS and 1.15 s (about 1.9 %) at 26–27 MB in the container.
- On 2026-10-16, on a Mac (macOS 27.0) with 42 % of memory free and swap in
  use, `kern.memorystatus_vm_pressure_level` read 2 (warning) on 6 of 6
  samples over about 9 seconds, and a dry-run watcher that called for a
  polite stop at warning logged memory as low on its first tick, so the
  guard stops at critical (4) only. With Claude Code 2.1.300 on macOS a
  background `Bash` call ran as `/bin/zsh -c '<export and setopt lines> && eval '…' < /dev/null
  && pwd -P >| /tmp/claude-…-cwd'`, a direct child of `claude` leading its
  own group, and the tool returned while it ran; a dry run of the hooks with
  that shape tracked the command, the background post hook marked the note
  posted, and the watcher dropped it when the command ended.
- On 2026-10-16, in a Debian container with the agents signed in through
  short-lived access tokens only and `WORKBENCH_GUARD=dry-run`, each agent
  ran four shell calls: `sleep 2`, `sleep 5` in the background (Claude Code
  `run_in_background`; Codex polling it with `write_stdin`), `bash -c 'sleep
  3; true'`, and an executable `#!/bin/sh` script that `exec`s `sleep 3`.
  Claude Code 2.1.301 (`claude -p`) had all four tracked and left no notes.
  Codex ran with its sandbox off (`--dangerously-bypass-approvals-and-sandbox`)
  and on (`-s workspace-write`, through `codex-linux-sandbox` and `bwrap`):
  Codex 0.163.0, which sets `CODEX_TOOL_CALL_ID`, had 8 of 8 calls tracked,
  and Codex 0.162.0, which does not, 6 of 8, both misses the script, logged
  `ended unmatched`. Without the sandbox chain in the match (section 2), none
  of the sandboxed Codex 0.162.0 calls was tracked. `versions.toml` pins
  Codex 0.163.0 and Claude Code 2.1.301, the versions observed here.
- On 2026-10-16, a stop under real memory pressure was observed on Linux in
  a Docker 29.8.2 container (kernel 7.0.14-linuxkit, arm64,
  `python:3.12-slim`) limited to a 1 GiB cgroup v2 with no swap. A stand-in
  agent ran the real pre and post hooks in Claude Code's spawn shape, with a
  64 MiB unmatched child, a 96 MiB background command and a command growing
  by 32 MiB every 0.2, 0.1, 0.05 or 0.01 s. The guard read the cgroup's
  figures and stopped the growing command with SIGTERM alone at "0.1 GB of
  1.0 GB available, no swap" in every run (four at 0.05 s). The kernel killed
  nothing (`oom_kill 0`), the other two processes stayed alive, the post hook
  returned the stop reason as `additionalContext`, and the cgroup's peak was
  989–1057 MB of 1074 MB. With `WORKBENCH_GUARD=dry-run` the watcher logged
  the stop it would make and the kernel then killed the command (`oom_kill
  1`). Without the cgroup reading, on machine-wide figures only, the watcher
  logged no stop and the kernel killed the command. A command allocating without pause
  filled the last 10% faster than the 100 ms minimum tick and was killed by
  the kernel. That is a limit of any polling guard, not of the threshold.
  The 10% available line is settled for a confined cgroup; the 5% forced
  line, the swap conditions, the pressure condition with its 20 s hold and
  the machine-wide Linux figures were not exercised. With the stand-in agent
  as the container's PID 1, the guard stopped the command as above; a hook
  that took no PID 1 for an agent found none, and the kernel killed the
  command.
- On 2026-11-14, on macOS, detached work was checked with Claude Code
  2.1.325 (`claude -p`) in throwaway sessions whose settings held only the
  guard's hooks, with an isolated state folder, and `workbench agent guard`
  driven in a private terminal. A command that double-forked a sleeper gave
  a `detached` row naming its agent and the call that started it, and,
  through a subagent, the subagent too; a person's stop from the view ended
  it (`◌ stopping`, then `✗ stopped`), and the session's reply quoted
  `Stopped by you (workbench agent guard)`, from the main session and from
  the subagent. A command that left `/bin/sleep` running when it returned
  gave a row with its call, held `young` for ten seconds and `unread` after;
  its details read "The guard will not stop this itself: its setting cannot be
  read. You can stop it here.", and a person's stop ended it.
- On 2026-11-14, on macOS, in Claude Code 2.1.325 sessions set up the same
  way, a detached program whose root exited while its child kept running
  stayed one row with its call and peak (9 MB) and was measured
  from the child (1 MB); the ID column moved to the live child's PID, the
  log and the details' "In the log" line kept the first label, and a
  person's stop from the view ended it. A command that returned at once,
  leaving a sleeper, gave a row with its call, in the main session and
  through a subagent with its subagent, and the call was neither logged nor
  shown as `unmatched`. With a fake agent, a `sleep 4` call under `/bin/ksh`,
  a shell no matching rule knows, was still logged `unmatched`, while the
  same text under zsh was tracked and ended. When an agent exited while a
  unit whose program hides its environment and one whose program shows it
  were tracked, both ended at the same tick with no reason and showed
  `✓ done`, in a real session and with a fake agent; the processes kept
  running, forgotten, not stopped.
- On 2026-11-14, on macOS, an app a command opened with `open -gj -a
  Calculator` and a sleeper a second `PreToolUse` hook double-forked, carrying
  `CLAUDE_PID` and no call stamp, gave no row and no log record, under a real
  Claude Code session and under a fake agent. A real nested Claude Code
  session a fake agent's command started with `nohup … &` was detached work
  in no snapshot, whether the command stayed four seconds or
  returned at once; when it returned at once, the log held the session for
  one second as detached work that ended with "its root is an agent", and
  the call, which ended before the watcher its own pre hook started was up,
  was never tracked. With a stand-in nested session, a readable sleeper it
  left detached before its session hook ran was a `detached` row of the
  outer call, held `young`, and ended in the log with "what started its root
  is not this call's work" once the session registered, while the sleeper
  kept running.
- On 2026-11-16, on macOS, with a fake agent and a watcher on a state folder
  of its own, a command that left `sleep 600` detached and ended after four
  seconds was in the snapshots 20 and 75 seconds after it ended, as `done`,
  with the sleeper as detached work naming its call; 30 seconds after the
  sleeper was ended both were still there, finished, and 72 seconds after
  it both were gone while another command of the agent still ran. No
  snapshot held a row for the agent or a split of the memory in use. The
  view drew sample rows with each detached program under its command, in a
  132-column and a 60-column window and with `LC_ALL=C` (`|-` and `` `- ``
  for the tree marks). A watcher built from `v0.1.32-rc.1`, tracking such a
  sleeper 20 seconds after its command ended, handed over to this build when
  the installed file was replaced: the new watcher showed the sleeper once,
  with its call, and still held the command's row 90 seconds after the
  command ended, with no row for the agent, and the log held no error.
- On 2026-11-14, on macOS, under bash, with a fake
  agent, a program left detached by a call with `WORKBENCH_GUARD=off` as a
  command prefix and three arguments, which then rewrote its title, gave no
  row, while the same program without the prefix gave a `detached` row with
  its call.
- On 2026-11-14, on macOS with fake agents, a watcher running from a copy of
  the binary handed over to a build of another version while a detached unit
  was tracked: the new watcher showed the unit once, with its call and the
  peak measured before the handover (69,681,704 to 69,714,448 bytes, its
  memory then 6,586,848 to 6,734,304), logged `tracking` for it once, and
  held it `young` for ten seconds after the adoption. A session without
  hooks, protected only by a readable child's stamp inside a tracked call,
  was never signalled by a person's stop of that call sent right after a
  handover, in three runs; the stop reached the new watcher's second tick,
  and the first tick is pinned by the
  `aStampNamedAgentIsProtectedFromTheFirstTickAfterAHandover` row of
  `TestWatcherKeepsWhatNoCallStartedOutOfItsOwnStop`. Detached work a
  nested session left before it registered was carried across a handover
  with its call and ended with "what started its root is not this call's
  work" when the session registered.
- On 2026-11-14, on macOS with a fake agent, in a handover with the
  ancestry store full, measured with timing lines added to the handover, the
  old watcher wrote 4,054,865 bytes for 16,012 entries in 9.1 ms and the
  next watcher's read ended 1 ms after the write, with no `error` record;
  and 50 fake agents, each timed to exit in the 36 ms before the tick at
  which the watcher first looks at it, left no "cannot follow an agent's
  forks" record and no `WARN` or `ERROR` record. How many of the 50 exits
  fell in the moment macOS no longer hands out an exiting process was not
  counted.
- On 2026-11-14, on macOS with Codex 0.169.0 (`codex exec`) and a throwaway
  Codex home holding the guard's hooks, a sub-second command that left a
  sleeper in a session of its own (`setsid`) gave a `detached` row with the
  call's ID, held `young` and then cleared, and the unit ended once the agent
  was gone; the session's agent file existed while it ran and was removed
  after. A plain `&` sleeper, in its command's process group, was gone at the
  end with no `stopping` record: inferred to be ended by Codex itself, not
  observed.
- On 2026-11-14, on macOS with a fake agent, a watcher tracking one command
  that held 1,400 `sleep` processes, and one running 650 shell loops of
  `/bin/sleep 1` for 75 s (about 650 new processes a second), logged no
  `error` record and ticked once a second; the counted figure below was read
  from a build with a memory readout added. The memory the Go runtime counts, which is what its
  32 MiB limit bounds, peaked at 15.3 MiB under the held processes and
  22.8 MiB under the churn; its resident size, which that limit does not
  bound, peaked at 27.5 MiB and 32.1 MiB. The release before,
  under the same two loads, peaked at 17.3 and 18.8 MiB counted and 31.7 and
  26.1 MiB resident.
- On 2026-11-14, on macOS with stand-in agents, the scenarios of the
  [detached work design](superpowers/specs/2026-11-13-guard-detached-work-design.md)
  (sections 5 and 10) gave what it says. Opt-out and hidden programs: a
  hidden leftover of `WORKBENCH_GUARD=off` was held `unread`, a readable one
  gave no row, a root that ran itself again with the guard off ended with
  "its root turned the guard off", a hidden orphan of an opted-out root gave
  no row, and a program that strips the variable stayed one the guard may
  choose. Nested sessions: work a session left before it was known ended
  with "what started its root is not this call's work", or, tied by stamp
  alone, lost its call (`no call`), and a session without hooks was kept out
  of the call's tree once a readable process named it. Sub-second calls
  under Homebrew bash that left a subshell gave a row with the call and its
  subagent in 30 of 30 runs in each of two series, and a `/bin/sleep` left
  behind a row with its call, held `young` then `unread`. Two subagents
  starting the same command at the same moment, in one folder or two, had
  each leftover tied to its own call (40 of 40 and 20 of 20 in two series),
  none to the other. A person's stop of an outer call never reached a nested
  session known only by its agent file, nor its helper. A detached subshell that ran
  a program with the guard off ended with "its root turned the guard off",
  and one without kept its ID and call through the new program, also when
  its call returned at once; such work of a call that returned at once and
  whose own pre hook started the watcher was not seen. The session hook, run
  for a stand-in agent, added its line to the env file once, and not again
  when it ran again for a resumed session.
- Not yet observed: a stop under real memory pressure on macOS or WSL2, or on
  Linux outside a memory-limited cgroup; either agent's interactive session,
  Claude Code's own sandbox and either agent on WSL2 with the guard enabled,
  where a command whose shape differs would go unguarded and be logged, not
  stopped wrongly; and a stop the guard itself chose reaching a real agent,
  or a person's stop reaching Codex. `workbench agent guard` has been run
  against real Claude Code commands only (detached rows, through a
  subagent, and a person's stop of the chosen row), not against Codex's or
  the same command in two worktrees of one repository at once; nor has it
  shown a stop the guard chose under memory pressure, or a watcher of an
  older release holding the socket. Applying the hooks to a real machine is
  not yet observed.
- Not yet observed for detached work: the guard's own stop of it under real
  memory pressure, which was never forced. That it never chooses a unit with
  no proved call, one younger than ten seconds or one whose setting was not
  read rests on the `held` reason the snapshots showed (`no call`, `young`,
  `unread`) and on `TestGuardChoosesOnlyWhatACallProvablyStarted` and
  `TestTargetsStayInsideTheCommandTree`; that it never names a nested
  session rests on the `rootIsAKnownAgentTiedByStamp`
  and `rootIsAKnownAgentTiedByParent` rows of
  `TestUnitRootOnlyForWhatAnAgentCommandForked`. None of these checks ran on
  Linux or WSL2, and the Linux container check was not run. Also not
  observed: the editor's command-line launcher starting the editor, and a
  real session resuming with the same env file.
- A moved agent config folder is reported, not written. On 2026-10-16, on
  macOS in a disposable home with synthetic answers, with `CLAUDE_CONFIG_DIR`
  unset, set to `~/.claude` or `~/.claude/`, `apply --local-build --dry-run
  --json` had no config-folder warning, `doctor` no `claude-config-dir` line
  and `agent costs status` the `(workbench apply)` hint; set to `~/alt`, the plan
  warned `CLAUDE_CONFIG_DIR in the environment points Claude Code at ~/alt, so
  it does not read the settings, instructions and costs and guard hooks
  Workbench installs in ~/.claude; unset it, or set it to ~/.claude, for them
  to apply`, `doctor` reported it as `claude-config-dir: conflict` (exit 0) and
  `agent costs status` showed the hooks of `~/alt` as missing without the hint, plus
  a `config claude` line. The variable in `~/.claude/settings.json`'s `env` was
  named the same way with `remove it from that file`, and `CODEX_HOME` set to
  `~/codex-alt` gave `codex-home: conflict` and a `config codex` line. The
  ingest hook, with the variable unset and a transcript under
  `~/alt/projects`, recorded that transcript's response. Managed settings files
  and a real Claude Code or Codex session were not observed.

### Output

- `workbench version` prints the `--version` line, ending `(local build)` for a
  build the release workflow did not publish. At a terminal, each result
  line carries a colored ✓, · or ✗, where · marks a step that changed nothing,
  such as tools already in place. Plans shown at a prompt are not repeated,
  and a successful run ends with a green ✓ summary. Piped output and
  `TERM=dumb` print plain lines, `NO_COLOR` drops color but keeps bold and
  faint, and a non-UTF-8 locale gets ASCII in place of ─ … → ✓ ✗. Tables fit
  the terminal from 20 columns up.
- On November 17, 2026, on macOS with a local build in a disposable home,
  `--help` printed a short description with examples for the root, `agent`,
  `agent costs` and its `ingest`, `rates` and `status`, `agent guard`,
  `agent guard logs`, `answers`, `answers import`, `answers ask`, `project
  apply`, `migrate`, `update` and `version`; bare `agent` and `answers` print
  their help. Bare `agent guard` with standard input from `/dev/null`
  exited 3 with `agent guard needs a terminal`, with `--json` exited 3
  pointing at `agent guard logs --json`, and `agent guard logs --json` in an
  empty home exited 0. Bare `migrate` without a terminal exited 3 with
  `Nothing approved this plan; read it with workbench migrate --dry-run
  --json`, and under a coding agent exited 3 for bare `migrate`, `--yes` and
  `-y`; `migrate --dry-run --json` listed the five migrations with a
  `plan_digest`, and `migrate --approve-plan` with that digest exited 0 with
  nothing to migrate. `update --list` with a VERSION or `--dry-run` exited 2.
  `render-check.sh` passed for the personal, work and both roles in pinned
  mode: the Claude Code settings merge replaced copies of the managed hooks
  from another folder with the current `workbench agent costs ingest` and
  `workbench agent guard pre`, keeping the user's own hooks.
- On November 17, 2026, on macOS with a local build, `doctor` in a disposable
  home holding every Claude Code hook printed `agent-hooks-claude: ok`;
  with three of them removed it printed `absent`, naming `1 of 2 agent costs
  hooks (SessionEnd) and 2 of 4 agent guard hooks (PreToolUse, PostToolUse)`;
  with none it printed `has no agent costs or agent guard hooks`. Node, Python
  and Rust, not installed there, read `absent` on their `runtime-` lines, and
  `runtime-go` and `runtime-bun` showed the Homebrew versions. Pinned mode,
  the Linux pin comparisons and Codex's untrusted and disabled hooks were not
  observed.
- On November 17, 2026, in an Ubuntu 24.04 arm64 container (Docker 29.8.2),
  uv 0.13.9 installed Python 3.12.16, 3.13.18, 3.14.10 and 3.15.2, and
  `cargo install --locked` built cargo-audit 0.22.2 and cargo-deny 0.20.2
  with Rust 1.100.0.
- On November 17, 2026, in an Ubuntu 24.04 arm64 container as a sudo user
  with a disposable home, in pinned mode with golangci-lint 2.14.0 installed
  and its pin at 2.15.0, the packages probe printed `linux-packages:
  golangci-lint 2.14.0 → 2.15.0`, the step replaced it, and a second run
  skipped every part. Latest mode kept an installed 2.14.0. A pin with no
  release exited 1 and kept the working copy, and a lefthook that reported no
  version was replaced. With chezmoi 2.70.3 alone, a run-on-change script ran
  again when its content went back to one it had run before, which a run-once
  script did not.
- On November 17, 2026, in an Ubuntu 24.04 arm64 container (Docker 29.8.2) as
  a sudo user with a disposable home, from a local build with synthetic
  personal/pinned answers, through `--dry-run --json` and `--approve-plan`,
  first with every step unticked but System packages, Runtime managers, Move
  Workbench's files and Credential files: after the first apply,
  `~/.local/bin/lefthook` (the run-on-change packages script) and `~/.nvm`
  (the run-once runtime managers script) were deleted. The plan listed
  `linux-packages: install lefthook 2.2.2` and `runtime-managers: install
  nvm`, the approved apply ran both scripts and put both back, and the next
  plan listed neither step and its apply ran only the migrations and
  credential files scripts, which run on every apply. With only `~/.nvm`
  deleted, the apply ran the runtime managers script and not the packages
  script, whose probe found nothing to do; with Runtime managers unticked,
  the plan showed it off with `install nvm` and the apply did not run its
  script. With Language runtimes, Command-line tools and tmux plugins ticked
  too and applied, the next plan listed none of the five steps and its apply
  ran no step script. Deleting ruff, gofumpt and cargo-deny listed
  `global-tools: install ruff, cargo-deny, gofumpt`, the apply put all
  three back (uv reinstalling ruff), and the next plan did not list the
  step. With chezmoi 2.70.3 alone, a run-once and a run-on-change script that
  had run both ran again as plain `run_` scripts with the same content, and
  then under their own names neither ran. `scripts/render-check.sh` linted
  the Linux scripts there for personal, work and both pinned and both latest.
  Latest mode, macOS (whose probes changed too), WSL, AlmaLinux and a shared
  script with an unticked step were not observed.

- On November 18, 2026, the optional Rust build cache step (`mbx`, mbx
  1.30.0), in Ubuntu 24.04 and AlmaLinux 10.3 arm64 containers (Docker
  29.8.2) as a sudo user with a disposable home, from a local build with
  synthetic personal/pinned answers, through `--dry-run --json` and
  `--approve-plan`. The plan listed `Rust build cache` under `Optional`,
  unticked, and an apply without the tick installed nothing of mbx's. With
  `select = ['mbx']` in `machine.toml` the apply installed mbx and ran
  `mbx setup`; `bash -lc`, `bash -ic` and `bash -lic` then found `cargo` at
  `~/.local/share/mbx/bin/cargo`, `mbx setup --status` exited 0, `mbx doctor`
  called the shim active, and `~/.config/mbx/config.toml` held exactly
  `memory = "3GiB"` and `reserve_cpus = 2` under `[scheduler]` (60% of
  `MemTotal` 6064496 kB, rounded down), in both containers; the next plan
  did not list the step. From here on in Ubuntu only: the next apply did not
  run the script; with `~/.local/bin/mbx` deleted it
  was listed and the apply put it back. A `scheduler.memory 2GiB` set with
  `mbx settings set` and a comment survived that apply, which added only the
  unset `reserve_cpus`; a dotted `scheduler.memory` key counted as set; an
  invalid file blocked the step (exit 3) and was left as it was. An mbx
  reporting 1.29.0 was replaced (`mbx 1.29.0 → 1.30.0`). A wrong pinned
  hash blocked the step (exit 3) with `the downloaded
  mbx-aarch64-unknown-linux-gnu.tar.gz does not match its pinned SHA-256, so
  it was removed without being unpacked`, and no mbx was left in
  `~/.local/bin`. A pin without a Linux arm64 hash left the step out of the
  plan, warned about the saved tick, and `doctor` read `mbx publishes no
  build for linux-arm64`. After `mbx setup --uninstall` and deleting
  `~/.local/bin/mbx`, plain `cargo` failed through the shim (exit 127) until
  the shim folder was deleted too. Both Linux builds ran `mbx --version` on
  Ubuntu 22.04, 24.04 and 26.04 and AlmaLinux 10.3 (amd64 under
  emulation), and `scripts/render-check.sh` linted the Linux scripts there
  for personal, work and both pinned, both latest and both pinned wsl. On
  macOS the script was rendered and linted, and its check, run under bash
  3.2 and 5 in an empty home, printed `scheduler.memory 9GiB (60% of
  memory)` on a 16 GiB Mac. Not observed: the step applied on macOS, on WSL
  or on amd64; an Intel Mac's plan and its `doctor` line; zsh startup; a
  coding agent's shell finding the shim; a newer mbx kept.

## Unqualified release gates

| Area | Remaining evidence or decision |
| --- | --- |
| macOS | Intel runs. Clean macOS 15.7.7 arm64 VMs (the minimum OS) installed the published v0.1.12 with `install.sh` and completed personal and both-roles provisioning; a later-edit revert conflict, a step turned off in the list, failed downloads that left the other steps running, a revert of a first apply that kept the folders other files filled, and reverts of applies stopped by ctrl+c, SIGTERM and `kill -9` were observed. |
| Ubuntu | Native 22.04/24.04/26.04 amd64/arm64 bundle/provisioning checks. A container, cross-builds and CI rendering are insufficient. A 24.04 arm64 VM ran full provisioning from a local bundle once, with one step failed on the apt lock (since fixed), and later completed it from another local bundle after a blocked nvm download was reported and retried. |
| WSL/Windows | Real WSL2.6+/Windows11 24H2+ x64 path/ACL, Terminal/PowerShell preservation and individually approved external-effect checks. Full provisioning is enabled but unqualified: Windows setup, Terminal and PowerShell adoption, the font registry, PATH, default distribution and sysctl are optional effects that start off (only WSL networking starts on), and no real host run is recorded. |
| Editor | Deliberately apply to an intended local profile, then confirm project-tool selection and only ty/native Ruff active. Linux/WSL editor hosts remain unchecked, including the WSL VS Code server install. |
| Agent command guard | A stop under real memory pressure on macOS, WSL2 and Linux without a limiting cgroup (settling the remaining thresholds), and among detached work the guard's own choice under that pressure; detached work on Linux and WSL2; Claude Code and Codex command shapes on WSL2 and in their interactive sessions, Claude Code's sandbox, a real Codex session showing a stop reason, and a real Claude Code session showing the reason of a stop the guard chose. |
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

Never restart WSL just to test a `.wslconfig` change. Record OS/CPU,
tool versions, commands, outcome and known limits here. Reuse existing render,
lint and leak checks; do not add a new lifecycle or benchmark framework.
