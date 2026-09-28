# Acceptance record

What Workbench has been checked to do, kept current: when behavior changes,
replace its entry instead of adding history, which git keeps. Releases publish
on `v*` tags for personal use. Implemented handlers are not evidence that every
native target is qualified; the open gates are listed below.

## Verified

Checks ran on macOS 27.0 arm64 (Go 1.26.8, golangci-lint 2.12.2, Homebrew
chezmoi 2.70.3, uv 0.12.3, Python 3.13.7), in an Ubuntu 24.04 container and in
GitHub Actions. Interactive checks ran under a pseudo-terminal. Unless an entry
says otherwise, changes went only to disposable homes and destinations with
synthetic answers.

### Continuous integration

- Every push runs `go-quality` (formatting, lint on macOS and Linux targets,
  build, the four safeguard tests, four cross-builds, the action pin check and
  shellcheck of `install.sh`), a full-history Gitleaks scan, and
  `scripts/render-check.sh` for every role and version mode on macOS and
  Ubuntu, plus the work/pinned WSL simulation. Rendering checks shell syntax,
  shellcheck, malformed-input preservation and leaks; it runs no provisioning.
- The safeguard tests cover a preview that tries to run a mutation, a revert
  blocked by a later edit or tampered evidence, a checkpoint that would remove,
  replace or loosen a folder holding Workbench's files, and a bundle executable
  that escapes its archive.

### Releases, install and update

- Tag pushes publish four bundles and an `install.sh` stamped with the tag.
  The `curl` one-liner without `gh`, the `gh` one-liner, and `update`
  downloading from GitHub each installed a release.
  `version --list` marks the latest and installed releases. `update` from an
  older release installed the latest and then reported it current. From a
  release before v0.1.2 it asks twice: the older runtime asks before
  installing, then the new one asks about the machine plan. Switching to an
  older bundle and back worked, and `update 9.9.9` names the missing release.
- `update` asks one question, about the machine plan. Installing Workbench and
  its pinned tools needs no separate approval: chezmoi, uv, Python 3.12.12 and
  TOML Kit download when missing, with a progress line. Rerunning `update`
  finishes a stage that was declined or failed. From a fresh home with no
  chezmoi or uv on `PATH`, `--install-only` (interactive or unattended, with no
  saved answers) and `--config-only` each installed all four tools without a
  prompt; `--install-only` then stops, and `--dry-run` stops before
  installing. Refusing after a fresh install exits 5, and on
  a rerun exits 3. Unattended, `update --json` stops at the machine plan with
  its digest, and `--approve-plan` applies it through the handoff to the new
  runtime.
- A fresh interactive setup asked every machine question, including with
  standard input piped as under `curl | sh`, on macOS and in the Ubuntu
  container. Setup asks only the questions the saved answers lack, and
  `update --ask KEY` asks a saved one again: work → personal dropped the tokens
  and the Codex work servers, coderabbit and `~/repos` trust, and personal →
  both asked both emails and wrote the two per-directory Git files. An unknown
  key, or `--ask` with `--install-only`, is refused before installing. The
  hidden `init --answers-from` adopts an existing chezmoi `[data]` table.
- Successive installs keep only the new and replaced releases, and remove
  setup contexts and tool versions that no kept release uses. An interrupted
  activation resumes. A staged release with a missing `release.json`, a changed
  file or another source digest is refused, and an unstamped build refuses any
  release source. Missing parents of Workbench's directories are created 0755.

### Planning and apply

- A plan shows each changed file with a summary, such as `+0 −2 lines` or
  `mode 0600 → 0644`, and flags files edited outside Workbench. It lists an
  effect only when chezmoi would run one of its scripts or change one of its
  files. A fresh home lists every effect; an up-to-date machine lists only the
  always-run apps and cleanup steps. Each outdated app or formula in
  `packages.toml` gets an `update-<name>` effect naming what else Homebrew
  would install or update. Its starting version is Homebrew's record, as the
  upgrade prints it, even for an app that numbers itself one release behind;
  an app that updated itself past the record shows its own. Pinned packages, and the chezmoi, uv and Python
  Workbench runs, are held, and a rerun keeps the digest. `--config-only`
  lists no provisioning effects.
- A live status line shows planning, the recheck after approval and setup.
  ctrl+c exits 130, writes nothing and leaves `stty -a` unchanged.
- Approval is a Yes/No prompt: `n`, Enter and esc refuse with exit 3. A
  config-only apply with nothing to change asks nothing, unless it settles an
  unfinished apply. Apply wrote the planned files, and the next plan had zero
  edits.
- In the Ubuntu container, the Linux bootstrap put the current `gh` and its
  man pages in `~/.local/bin` over an apt-installed one, the tmux step cloned
  TPM and installed the plugins, and without network it failed only that step.
  A VS Code extension that fails to install no longer stops the rest; the step
  reports it and exits nonzero.
- The Claude, Codex and VS Code merges leave a file byte for byte when no value
  changes. They keep unowned keys, JSONC comments and VS Code's 0644. A
  changed enforced value is restored. Quarantine, Finder info and last-used-date
  attributes survive apply and revert.
- On an existing macOS 27 arm64 machine with Homebrew, a full apply completed
  every effect: app and formula updates, VS Code extensions, where only missing
  ones install, and Homebrew cleanup, whose per-formula "Skipping" warnings fold
  into one count. Updating a running app quit it, and Homebrew reopened it. The interactive apply handed the terminal to provisioning
  and took it back without stopping under a job-control shell.

### Revert and recovery

- `revert` offers checkpoints newest first as "undo the apply of …" or "redo
  the apply of …". Without a terminal it lists their IDs and exits 3. It
  restored exact files, modes and attributes, including a folder mode above
  Workbench's data. A later edit to any target blocks restoring all of them.
- At the 20-checkpoint limit, a plan names the oldest settled checkpoint's
  removal as an effect. A zero-edit apply settles a recorded unfinished apply;
  one recorded for another destination blocks.

### Linux

- In an Ubuntu 24.04 container, not a native host, a new user with login
  umask 0002 installed a linux-arm64 bundle. Setup qualified all tools, and a
  config-only apply wrote files at 0644 and folders at 0755. With `~/.config`
  at 0700 the plan listed a mode-only edit to 0755, and revert restored 0700.
  With `~/.local` group-writable, the install stopped with the `chmod go-w`
  that fixes it.

### Projects

- `project inspect` lists the candidate files and the projects they make.
  `project configure` applied to a synthetic uv project, with extensions,
  ignore entries, CI integration and native uv resolution. The next plan was
  unchanged, and `project revert` restored every file exactly. `--ci` requires
  an explicit `.python-version` and exactly one workflow. Interrupting uv
  staging exits 130 with no project file changed.

### Output

- `workbench version` prints the `--version` line. At a terminal, each result
  line carries a colored ✓, · or ✗, where · marks a step that changed nothing,
  such as tools already in place. Plans shown at a prompt are not repeated,
  and a successful run ends with a green ✓ summary. Piped output and
  `NO_COLOR` print plain lines.

## Unqualified release gates

| Area | Remaining evidence or decision |
| --- | --- |
| macOS | Complete disposable-user provisioning, minimum OS and Intel runs. |
| Ubuntu | Native 22.04/24.04/26.04 amd64/arm64 bundle/provisioning checks. A container, cross-builds and CI rendering are insufficient. |
| WSL/Windows | Real WSL2.6+/Windows11 24H2+ x64 path/ACL, Terminal/PowerShell preservation and individually approved external-effect checks. Full provisioning is enabled but unqualified: host adoption, font registry, PATH, default distribution and sysctl are selected `--effect`s, and no real host run is recorded. |
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
