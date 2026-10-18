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
  `version --list` showed it on its own line as installed.
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
  `version --list` marks the latest and installed releases; an installed
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
  on the next run. (target; not yet observed on a real WSL host)
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
  path under neither tool logged that the session was not bound. `costs status`
  reported Claude Code hooks as `missing (workbench apply)` for a settings file
  with the old `async` hooks and `ok` for the synchronous ones. The real Claude
  Code sign-in was not read in this run (a synthetic `oauthAccount` stood in),
  and a running session's behavior after a `/login` elsewhere is not observed.

- Fast-mode Claude Code responses are priced at `@fast` rates, a specific rate
  row beats a family row except for a manual override, the ledger is created
  0600 in a 0700 folder, and the headline counts the rows the table shows
  (target; not yet observed on a real ledger).
- `workbench migrate --apply` and the apply's **Move Workbench's files** step
  move the costs files out of the `claude-costs` folders into Workbench's
  runtime folders with every ledger row, the import worker moves them by
  itself after an update, and the old completion and `claude-costs` script
  caches are deleted (target). On 2026-10-16, in a Linux container (kernel
  7.0.14-linuxkit, Docker 29.8.2, Ubuntu 24.04 arm64) with a disposable home
  whose old files v0.1.25 wrote by ingesting a synthetic transcript,
  `workbench migrate` listed three pending migrations, `migrate --probe`
  printed one line for each, and `migrate --apply --dry-run --json` then
  `--approve-plan DIGEST` moved the ledger, both price caches and the
  overrides, deleted the old import log and lock, the completion cache and
  the old script's cache, removed every `claude-costs` folder, and wrote
  `migrations.json`. The new folders were 0700 and the files 0600, the
  ledger's row count in every table matched the count before the move, and
  `integrity_check` returned ok. With the data folder on a tmpfs, the ledger
  took the copy path across filesystems, with the same row counts and
  `integrity_check` ok. With old and new ledgers both present, both stayed
  unchanged, the output named them, the other migrations still ran, and
  `migrate --apply` and `migrate run` exited 4. `apply --local-build
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
  plan; review a new plan`) before applying anything; `migrate --apply
  --approve-plan` under another data folder exited 4 the same way.
- On 2026-10-16, on macOS, the Claude Code and Codex hooks rendered with
  Workbench's bin folder: `render-check.sh` with the default bin folder rendered
  byte-identical to hooks with the fixed `~/.local/bin` path, a bin folder
  holding a space planned Claude Code settings +5/−5 lines, Codex `config.toml`
  +4/−4 and `hooks.json` +4/−4, with the quoted path on every hook and Codex
  trusting each by its hash, and switching back rendered identical. Its stage
  "hooks follow a bin folder with a space" passed, and failed against hooks with
  the fixed path (9 `BIN FAIL` lines). `costs status` with a spaced, quoted bin
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
  update finished (`handover` in `guard logs`), and the new watcher tracked
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
  on macOS and in an Ubuntu 24.04 container as uid 1000, the Linux `init
  --answers-from FILE --dry-run --json` plan, and `render-check.sh personal
  pinned wsl`; the macOS init plan differed only in the answers-file path it
  quotes.
- On 2026-10-17, on macOS in disposable homes with a fake agent and
  `WORKBENCH_GUARD=dry-run`, `workbench guard logs` printed one line per event
  (time, level, event, then `id=`, `agent=`, `subagent=`, `project=`, `by=`,
  `dry_run=`, `took=`, `size=`, `peak=` and `cmd=` where they apply, the
  command quoted when it holds a space), a `running` line for a command still
  running after 5 s under `--follow`, the log's own records under `--json`,
  and `No guard events since …` (exit 0) for an empty `--since` window.
  `NO_COLOR` and piped output dropped the color.
- On 2026-10-17, on macOS in disposable homes, `workbench guard memory` ran
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
  `Stopped by you (workbench guard memory).` as `additionalContext`. A stop
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
  `WORKBENCH_GUARD=dry-run`, `workbench guard memory` drew the four-line
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
  agent clay) at 24-bit and 256-color depth, `guard logs` colored the level
  label alone with the agent in its hue and the event and `id` plain, and
  `NO_COLOR` printed plain text.
- On 2026-10-18, in a Debian container (`python:3-slim`, arm64) capped at
  2 GiB as uid 1000 with the same fake agent, the view's header read `mem 63
  MB/2.0 GB`, the container's `memory.current` less its `inactive_file`
  over its limit, and `→`, `h`, the 60-column count line, the pinned colors
  and `NO_COLOR` behaved as on macOS; `guard logs` colored the level and the
  agent the same way.
- On 2026-10-17, in an Ubuntu 24.04 container (Docker 29.8.2, kernel
  7.0.14-linuxkit, arm64) capped at 2 GiB, as uid 1000 with a disposable home
  and Python added to the container for the stand-in agent, whose commands ran
  in Claude Code's bash shape, `workbench guard memory` ran through `docker
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
  running row's memory graph kept its earlier readings, and `guard logs`
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
- Not yet observed: a stop under real memory pressure on macOS or WSL2, or on
  Linux outside a memory-limited cgroup; either agent's interactive session,
  Claude Code's own sandbox and either agent on WSL2 with the guard enabled,
  where a command whose shape differs would go unguarded and be logged, not
  stopped wrongly; and a real agent receiving the stop reason. `workbench
  guard memory` has not been run against real Claude Code and Codex commands,
  such as the same command in two worktrees of one repository at once, one
  through a subagent, to see each row carry its project and subagent and a
  stop hit the row chosen; nor has it shown a stop the guard chose under
  memory pressure, or a watcher of an older release holding the socket.
  Applying the hooks to a real machine is not yet observed.
- A moved agent config folder is reported, not written. On 2026-10-16, on
  macOS in a disposable home with synthetic answers, with `CLAUDE_CONFIG_DIR`
  unset, set to `~/.claude` or `~/.claude/`, `apply --local-build --dry-run
  --json` had no config-folder warning, `doctor` no `claude-config-dir` line
  and `costs status` the `(workbench apply)` hint; set to `~/alt`, the plan
  warned `CLAUDE_CONFIG_DIR in the environment points Claude Code at ~/alt, so
  it does not read the settings, instructions and costs and guard hooks
  Workbench installs in ~/.claude; unset it, or set it to ~/.claude, for them
  to apply`, `doctor` reported it as `claude-config-dir: conflict` (exit 0) and
  `costs status` showed the hooks of `~/alt` as missing without the hint, plus
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

## Unqualified release gates

| Area | Remaining evidence or decision |
| --- | --- |
| macOS | Intel runs. Clean macOS 15.7.7 arm64 VMs (the minimum OS) installed the published v0.1.12 with `install.sh` and completed personal and both-roles provisioning; a later-edit revert conflict, a step turned off in the list, failed downloads that left the other steps running, a revert of a first apply that kept the folders other files filled, and reverts of applies stopped by ctrl+c, SIGTERM and `kill -9` were observed. |
| Ubuntu | Native 22.04/24.04/26.04 amd64/arm64 bundle/provisioning checks. A container, cross-builds and CI rendering are insufficient. A 24.04 arm64 VM ran full provisioning from a local bundle once, with one step failed on the apt lock (since fixed), and later completed it from another local bundle after a blocked nvm download was reported and retried. |
| WSL/Windows | Real WSL2.6+/Windows11 24H2+ x64 path/ACL, Terminal/PowerShell preservation and individually approved external-effect checks. Full provisioning is enabled but unqualified: Windows setup, Terminal and PowerShell adoption, the font registry, PATH, default distribution and sysctl are optional effects that start off (only WSL networking starts on), and no real host run is recorded. |
| Editor | Deliberately apply to an intended local profile, then confirm project-tool selection and only ty/native Ruff active. Linux/WSL editor hosts remain unchecked. |
| Agent command guard | A stop under real memory pressure on macOS, WSL2 and Linux without a limiting cgroup (settling the remaining thresholds), Claude Code and Codex command shapes on WSL2 and in their interactive sessions, Claude Code's sandbox, and a real session of each agent showing the stop reason. |
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
