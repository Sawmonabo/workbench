# Acceptance record

Date: 2026-09-22. Releases publish on `v*` tags for personal use; none is published yet.
Implemented handlers are not evidence that every native target is qualified.

## Observed checks

Native development environment: macOS 27.0 arm64, Go 1.26.4,
golangci-lint 2.12.2, chezmoi 2.70.3, uv 0.12.3, Python 3.13.7.

- Isolated native initialization used complete synthetic answers, built-in Git
  and no clone. Repeated initialization preserved configuration. A real terminal
  run also completed all five personal-role native questions and saved validated
  private answers; no simulated questionnaire substituted for that run.
- Editor-policy checks accepted the pinned ty/Ruff/basedpyright versions and
  extension setting schemas. Native user-config discovery worked. A repeated
  JSONC settings merge preserved unowned nested values/custom rules and bytes.
- Shared recovery checks refused later edits, corrupt images and escaping paths.
  Disposable apply/revert/undo restored exact files. At the 20-checkpoint
  limit, 22 further config-only applies each planned and removed the oldest
  settled checkpoint; 20 remained listed by `revert --list`.
- Native uv standalone/workspace resolution and repeated configuration/recovery
  passed in synthetic projects; no live project or global tool was changed.
- Native machine configuration applied 34 planned edits, including role-owned
  removals; repeat preview had zero edits and recovery restored original files,
  directory permissions and groups. SIGTERM after a durable running journal
  returned 130; the observed unchanged targets were recoverable. That timing
  does not qualify every possible mid-write interruption.
- All seven existing render entry points passed: personal/work/both in pinned
  and latest modes, plus work/pinned WSL simulation. These rendered synthetic
  macOS configurations, checked shell syntax/lint and malformed-input preservation,
  and ran existing leak/status-line checks. No provisioning scripts ran. Fixture
  freshness warnings were non-failing; WSL remains simulation only.
- Published-style local bundles passed stage-only, install-only, command-collision
  preservation and interrupted-activation resumption checks. Restricted-PATH
  bootstrap acquired private chezmoi/uv/CPython/TOML Kit, saved private native
  answers, then applied configuration only to a disposable destination. The
  separate setup/target approvals remained required. Remote download was not
  exercised against an actual published release.
- Final candidate checks passed: a changed-source candidate was previewed through
  the old entry point without changing the active runtime or configuration.
  Approved apply activated the matched candidate and changed the intended file.
  A CLI-only candidate activated without allocating a configuration checkpoint.
- Native apply runner: a streamed run lasted 62 seconds with no deadline and
  printed its redacted output; under a pseudo-terminal the child owned the
  foreground process group (stdin was a terminal), so sudo can prompt; a failed
  captured run reported its redacted stderr. Interactive and unattended
  config-only applies both completed into a disposable home.
- `install.sh`, with a local stand-in for the GitHub download, installed a
  darwin-arm64 bundle built by `scripts/package-release.py`: `--install-only`
  activated it, then an interactive `--config-only` install continued through
  the runtime handoff and applied configuration to a disposable home; the next
  plan had zero edits. With no release published, the real `gh` path reports
  "no release found".
- Successive local-bundle installs into a disposable home kept only the new
  and replaced releases: the third install's plan listed the oldest release, an
  unused setup context and an unused private tool version as removals, and
  deleted them after activation; reinstalling an older release kept it and the
  one it replaced.
- Plans summarized each edit (for example `+0 −2 lines` or `mode 0600 → 0644`)
  and flagged a hand-edited managed file as edited outside Workbench. A
  developer checkout with an edited `home/` template planned and applied
  without regenerating trust, and editing it again invalidated that approval;
  a tampered copy of a staged release was still refused.
- A managed file carrying quarantine, Finder info and last-used-date attributes
  planned and applied, and revert restored its content and all four attributes.
- Project configure with extensions, ignore entries, CI and native uv
  resolution applied to a synthetic uv project; the next plan was unchanged and
  revert restored every file. Interrupting uv staging exited 130 with no
  project file changed.
- Resuming an activation left running in a disposable home kept the release it
  replaces. A staged release missing its `release.json` was refused, not
  planned as a developer checkout. The installer ran through a stand-in `gh`
  with no `curl` on `PATH`. Packaging succeeded from an empty module cache.
- With two releases pinning different TOML Kit versions, installing the newer
  one kept the version the replaced release pins and removed only an unpinned
  one. A changed or unpinned workflow action failed the action pin check.
- A full read-only plan against this Mac's Homebrew (scratch home, auto-update
  off) listed `update-<app>` effects only for listed apps Homebrew reports as
  outdated by their installed version, skipped current and unlisted ones, and
  kept the same digest on a rerun; `--config-only` listed none. With a stub
  `brew`, the apps step upgraded only the plan's apps, left an app installed
  outside Homebrew alone, and still ran the extension step when an update failed.
- A read-only full plan against this Mac's real home (default Workbench
  folders, Homebrew auto-update off) completed after accepting Apple's default
  folder ACLs, keeping `~/Library`, VS Code's data folder, `~/.claude` and
  `~/.codex` private, and keeping Codex-owned config keys. It created no
  Workbench folders. The merged Codex config lost no key, and merging it again
  changed nothing.
- On this Mac, every full plan listed `brew-maintenance` without a flag, a
  `--config-only` plan did not, and `--effect brew-maintenance` was rejected
  because macOS has no optional steps left. Dry runs of `brew autoremove` and
  `brew cleanup -s --prune=all` showed nothing to remove and about 123 MB of old
  files to delete.
- The owner's Mac switched from dotfiles with a checkout build: `init
  --answers-from` saved the eight answers, a config-only apply wrote 10 files
  (Claude and Codex values kept, only reordered) and re-planned to zero edits,
  and doctor was clean once the dotfiles chezmoi config was retired. The full
  apply ran unattended with an approved digest and Docker Desktop held by
  `brew pin` (its update needs sudo): it installed Node 26.10.0, uv tools and
  the ty extension, updated four apps and freed 1.1 GB, then failed at the
  tmux step because TPM could not find Homebrew's `tmux` on the script PATH.
  With that fixed, the rerun completed every step and the next plan had no
  edits and no app updates. A third full apply installed, updated and removed
  nothing.
- A read-only plan on the same Mac listed an `update-<name>` effect for each of
  the 15 outdated `packages.toml` formulae (the `postgresql` alias resolved to
  `postgresql@18`), kept the Homebrew chezmoi and uv Workbench runs, named the
  pinned Docker Desktop as held and kept its digest on a rerun; `--config-only`
  listed none. With a stub `brew`, the apps step upgraded only the planned
  formulae, then casks, and reported a failed formula update as a warning.
- The owner's interactive full apply from zsh finished every step and then
  stopped (process state `T`) while taking the terminal back from chezmoi:
  termios(4) sends SIGTTOU to a background process group that sets the
  foreground. A disposable config-only apply under a job-control shell
  reproduced the stop; with SIGTTOU ignored for that call it completed and
  recorded the operation. The same harness showed the readable plan view at
  the approval prompt and in `plan`, and `--json` still carried the full plan.
- Resumed with `fg` the next day, that apply reported `configuration: partial`:
  every update had installed, but Claude Code had saved `~/.claude/settings.json`
  during the pause (same values, its own key order), so the final image check
  failed. The rebuilt binary's interactive rerun from zsh finished without
  stopping, updated VS Code and cleared the unfinished record. Two minutes
  later the relaunched VS Code saved its settings the same way, as 0644.
  With the three merges leaving a file untouched when no value changes and
  VS Code settings no longer forced to 0600, rendering against this Mac's
  live Claude, VS Code and Codex files reproduced each byte for byte. In a
  disposable home, reordered settings, a JSONC comment and VS Code's 0644
  planned zero edits and a changed enforced value was still restored; a
  zero-edit config-only apply settled a recorded unfinished apply, and one
  recorded for another destination blocked.
- Homebrew 7 asked "Do you want to proceed with the upgrade?" during that
  interactive apply because the tmux and ripgrep updates also installed
  jemalloc and updated pcre2 and libevent. A read-only plan from a copy of the
  source that also listed glib, watchman and pnpm read Homebrew's dry runs:
  pnpm was offered with nothing extra, and glib and watchman were held because
  their updates, once the dependents' own dependencies were included, would
  also update the python@3.13 Workbench runs. A single glib dry run had not
  shown that. With a stub `brew`, both upgrade commands ran with
  `HOMEBREW_NO_ASK=1`. With nothing outdated on this Mac, an unattended full
  apply completed every step in 24 seconds without a prompt, and the next plan
  had no edits and no updates.
- With the committed source fingerprint file removed, a locally packaged
  bundle carried its source digest in `release.json` and its executable. That
  executable accepted its own extracted source. It refused the same source when
  run unstamped, when one file changed with the manifest fixed up to match,
  and when `release.json` named another digest. An install-only install into
  disposable Workbench folders planned from the active release without
  `--source`, refused after an installed file changed, and a second install
  listed only an unpinned TOML Kit version for removal, reading the kept
  releases' pins from their `release.json`. A `--source` plan on this Mac had
  zero edits under the same digest the bundle carried.
- Tool version pins are read at runtime from the selected source's
  `versions.toml`. A scratch source copy pinning chezmoi 2.99.0 made the same
  unrebuilt executable report Homebrew's 2.70.3 as unqualified. On this Mac,
  `doctor --source` qualified chezmoi, Python and uv, and `doctor` with no
  source and no installed release reported blocked tool versions. From a
  locally installed bundle with no `--source`, `doctor` qualified the tools
  and `project configure --dry-run` found the TOML Kit folder named by the
  release's pin; an unstamped build refused that installed release.
- Planning shows its progress. At a pseudo-terminal, a read-only plan on this
  Mac drew `Planning: checking chezmoi, uv and Python`, then `rendering your
  configuration`, then `asking Homebrew for updates (2s)`, and cleared the line
  before printing the plan. The only terminal sequences it wrote were cursor
  hide and show, and `stty -a` matched before and after. Without a terminal the
  same steps printed as plain lines. ctrl+c and SIGINT during the Homebrew
  check ended the plan with exit 130 and no plan. Before this, they gave exit 0 and
  the warning "Homebrew's outdated check failed". An interactive config-only
  apply into a disposable home read `yes`, showed `Rechecking the plan`, wrote
  32 files and exited 0. The terminal color query seen during that apply also
  comes from native chezmoi in the previous build. Pressing ctrl+c during
  `Rechecking the plan` after `yes` exited 130 and wrote nothing.
- The commands became `apply` (with `--dry-run`), `update [VERSION]`,
  `version [--list]`, `doctor`, `revert` and `project`; `plan`, `status`,
  `pull` and `install` were removed, along with the staged-candidate path only
  `pull` used, so the candidate checks above describe removed behavior.
  Against the private repository with no releases, `version --list` printed
  "No releases published yet" through `gh`'s token and, without `gh`, "No
  published Workbench release found; a private repository needs gh auth
  login"; `update` and `update 0.2.0` exited 3 naming what was missing. A
  locally packaged v0.9.0 bundle installed into a disposable home under a
  pseudo-terminal: `update v0.9.0 --bundle` asked Yes/No, installed, handed off
  to the new runtime, planned, asked again and applied 32 files, exit 0.
  Rerunning it reported "Already installed". An unattended `--install-only`
  update approved by its dry-run digest also installed. At the approval
  prompt, `n`, Enter alone and esc each refused with exit 3; `y` then ctrl+c
  during `Rechecking the plan` exited 130, wrote nothing and left `stty -a`
  unchanged. `revert` showed "undo the apply of release v0.9.0" in its picker
  and restored the destination; without a terminal it listed both checkpoints
  with their IDs and exited 3. `doctor` reported the active release, the last
  applied source and each tool's version, owner and path. The GitHub download
  of a published bundle is untested until a release exists.
- A fresh-machine rerun found two first-install failures, now fixed. In a
  disposable home with only `~/Library` present and no chezmoi or uv on
  `PATH`, a locally packaged bundle's own executable ran `update --bundle`
  under a pseudo-terminal. The install was approved and setup declined, which
  exited 5. Before the fix, rerunning `update` reported "Already installed",
  `apply` reported the tools missing, and no command offered setup again. Now
  the rerun printed "Workbench v0.9.1 is already installed; continuing with
  setup and apply" and asked for setup. The setup line then showed downloading
  chezmoi, then uv, installing Python 3.12.12, downloading TOML Kit and
  checking the tools. Before the fix, the full plan that followed stopped with
  "Native plan would change a protected ancestor directory". Workbench had
  created the missing `~/Library/Application Support` with mode 0700, which the
  source manages at 0755; the same applies to `~/.config` and `~/.local` in a
  fresh Linux home. Missing parents of Workbench's directories are now created
  0755, and the plan reached its prompt with 28 files and 11 effects. It was
  declined, so no provisioning ran. The next rerun went straight to the setup
  prompt, and `--dry-run` or `--install-only` on the installed release printed
  "v0.9.1 is already installed", exit 0. A temporary test of the download code
  against public GitHub parsed a release list and fetched an asset through
  GitHub's redirect, both with and without a token. A missing tag mapped to
  "not found". With a scratch `HOME`, `gh` cannot read its keychain token, so
  that check used the real `HOME` with scratch Workbench directories.
  `project configure --resolve-dependencies` created a checkpoint, and the
  `project revert` picker restored `pyproject.toml` and `uv.lock` exactly.
  Project checkpoints are now labeled with their project policy, not as a
  release.
- A one-run unoptimized development binary observation reported version startup
  at 0.00 seconds displayed precision and 13,041,664 bytes maximum RSS. Inspection
  of the checkout scanned 143 entries/four candidates in 0.20 seconds with
  13,844,480 bytes maximum RSS and zero swaps. These small-scope observations are
  not performance budgets, comparative benchmarks or universal guarantees.

Integrated local checks passed: `golangci-lint fmt`, configuration verification,
`golangci-lint run ./...` (zero issues), `go build ./...`, `go test ./...` (three
small safety safeguards), the action pin check and whitespace checks.
Four macOS/Linux architecture cross-builds passed; they are compilation evidence.
A final affected personal/pinned render passed after source safeguard changes.

The final evaluation bundle passed checksum/manifest verification and offline
staging preview. Gitleaks found no leaks in the checkout or extracted payload
when run from their respective roots with the narrow canonical keyboard-shortcut
false-positive rule. Both consolidated code reviews closed their material
findings. Remote CI outcomes are recorded by the repository's GitHub Actions
runs; the local evidence here does not substitute for a successful remote run.
The initial remote run also passed Go quality/build/safety/cross-compilation and
all 13 existing Ubuntu/macOS role/mode/render jobs. These are native CI checks,
not full machine provisioning or Windows-host qualification.

## Unqualified release gates

| Area | Remaining evidence or decision |
| --- | --- |
| macOS | Complete disposable-user provisioning, minimum OS and Intel runs. Isolated arm64 checks do not qualify all native effects. |
| Ubuntu | Native 22.04/24.04/26.04 amd64/arm64 bundle/provisioning checks. Cross-builds and CI rendering are insufficient. |
| WSL/Windows | Real WSL2.6+/Windows11 24H2+ x64 path/ACL, Terminal/PowerShell preservation and individually approved external-effect checks. Full provisioning is enabled but unqualified: host adoption, font registry, PATH, default distribution and sysctl are selected `--effect`s, and no real host run is recorded. |
| Editor | Deliberately apply to an intended local profile, then confirm project-tool selection and only ty/native Ruff active. Linux/WSL editor hosts remain unchecked. |
| Release | First tagged release and a real `curl`/`gh` one-liner run; native capacity qualification. Releases stay unsigned with no redistribution license by decision. |

Windows ARM integration, native Windows, arbitrary Linux distributions and
additional project package managers/language configurators are not implemented
support commitments. No silent platform waiver is implied.

## Short qualification procedure

Use a disposable user/VM and synthetic credentials. Install a release bundle
with `install.sh` and without a checkout; run `doctor`; preview with `apply --dry-run`; apply
configuration twice and compare; exercise unchanged recovery and a later-edit
conflict. Full provisioning additionally checks actual shell/Git/editor/theme,
runtime/tool outcomes, denied privilege and optional-effect denial. WSL requires
real host paths with spaces/non-default drives and Windows permissions.

Never execute restart helpers just to test their installation. Record OS/CPU,
tool versions, commands, outcome and known limits here. Reuse existing render,
lint and leak checks; do not add a new lifecycle or benchmark framework.
