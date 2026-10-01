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
  git-cliff 2.14.2 with `cliff.toml`, run at a tag, rendered every release
  from the commit history with no unreleased section, and that tag's section
  alone for its release notes.
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
  rerunning it finishes a stage that was declined or failed. (target; not yet
  observed from a fresh home)
- `apply` shows the checklist and asks once, about the checked selection.
  Unattended, `apply --dry-run --json` prints the plan with its digest, and
  `apply --approve-plan DIGEST` applies exactly that plan or exits 4 when the
  machine, release or saved selection changed; `init --answers-from FILE`
  works the same way. Observed on the WSL2 host: the approved digest applied
  exactly its checked effects, a digest taken before a saved-skip change exited
  4 with nothing applied and `machine.toml` byte-identical, and a stale digest
  exits 4 even when nothing would apply; `init --answers-from` was not rerun.
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
  `--local-build` apply uses the saved answers without setup, and the hidden
  `init --answers-from` adopts an existing chezmoi `[data]` table. (target;
  the questions were not re-asked on a real host)
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
- A live status line shows planning, the recheck after approval and setup.
  ctrl+c exits 130, writes nothing and leaves `stty -a` unchanged.
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
  changes. They keep unowned keys, JSONC comments and VS Code's 0644. A
  changed enforced value is restored. Quarantine, Finder info and last-used-date
  attributes survive apply and revert.
- On an existing macOS 27 arm64 machine with Homebrew, a full apply completed
  every effect: app and formula updates, VS Code extensions, where only missing
  ones install, and Homebrew cleanup, whose per-formula "Skipping" warnings fold
  into one count. Updating a running app quit it, and Homebrew reopened it. The interactive apply handed the terminal to provisioning
  and took it back without stopping under a job-control shell.

### On a WSL2 host

- On WSL2 with Ubuntu 26.04, from a local `v0.1.8-dev` bundle, `apply` planned
  real deltas (Go 1.27.0 → 1.27.1, the Node default 22 → 26, missing tools,
  the `.wslconfig` change marked as asking) with nothing unprobed, and probes
  wrote nothing under the home directory. The approved apply installed `fd`,
  switched the Node default, installed the missing tools, refreshed the tmux
  plugins, moved the costs hooks to `workbench costs ingest` and removed the
  replaced `claude-costs` script and its completion. A skipped `run_once_`
  script was not recorded by chezmoi and ran on a later apply once unchecked.
  Saved skips held under `--yes`, `apply --dry-run --reset` showed every effect checked,
  and `doctor` printed the applied release, date and saved skips. With no
  local VS Code desktop the editor step says it has nothing to do. The
  Windows-side effects were skipped; they remain unqualified (below).

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
  umask 0002 installed a linux-arm64 bundle and setup qualified all tools. With
  `~/.local` group-writable, the install stopped with the `chmod go-w` that
  fixes it.
- On the same umask, an apply with every effect unchecked writes files at 0644
  and folders at 0755. With `~/.config` at 0700 the plan lists a mode-only edit
  to 0755, and revert restores 0700. (target; not yet observed)

### Projects

- `project inspect` lists the candidate files and the projects they make.
  `project configure` applied to a synthetic uv project, with extensions,
  ignore entries, CI integration and native uv resolution. The next plan was
  unchanged; `project revert --dry-run` showed the restore plan and changed
  nothing, and `project revert` restored every file exactly. `--ci` requires
  an explicit `.python-version` and exactly one workflow. Interrupting uv
  staging exits 130 with no project file changed.

### Costs

- On 2026-10-01, the 2,969 Codex rollouts (23 GB) of a 16-core WSL2 host
  were ingested into an empty scratch ledger in 18 seconds at 450% CPU and a
  peak of 472 MB, against 2 minutes 54 seconds for the previous build; its
  responses and tier tables were byte-identical, as were those of a ledger
  built in two runs whose second finished four half-written rollouts. A
  second run with nothing new took 0.3 seconds. Per-model response and token
  counts matched an independent Python count for one record-era month
  (September 2026) and one legacy month (May 2026). The script takes a
  response's month from its rollout's dated folder and the ledger from its
  UTC time, so September differed by 259 responses timestamped October 1 UTC
  in three sessions whose rollouts sit in September folders (two in
  September 30, one spread over September 22 to 30); those 259 are exactly
  the ledger's October 2026 rows. The Codex hooks rendered from the `both`
  role were all `trusted` in Codex's own `hooks/list` (app server 0.159),
  with `currentHash` equal to the `trusted_hash` the config merge wrote, and
  the hash Codex had recorded for the existing approved `UserPromptSubmit`
  hook was reproduced exactly.
  Applying them to a real machine is not yet observed.

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
| macOS | Complete disposable-user provisioning, minimum OS and Intel runs. |
| Ubuntu | Native 22.04/24.04/26.04 amd64/arm64 bundle/provisioning checks. A container, cross-builds and CI rendering are insufficient. |
| WSL/Windows | Real WSL2.6+/Windows11 24H2+ x64 path/ACL, Terminal/PowerShell preservation and individually approved external-effect checks. Full provisioning is enabled but unqualified: host adoption, font registry, PATH, default distribution and sysctl are effects that start unchecked in the checklist, and no real host run is recorded. |
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
