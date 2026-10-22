# Workbench contributor instructions

Read [README](README.md), [design](docs/superpowers/specs/workbench-design.md),
[contracts](docs/superpowers/specs/workbench-contracts.md) and the relevant
[implementation plan](docs/superpowers/plans/workbench-implementation.md).
Decisions and the reasons for them are in [docs/adr](docs/adr/).
Distinguish implemented code, observed checks and unqualified release targets.

## Development contract

- Greenfield: update canonical code, callers and docs in place. No migrations,
  compatibility readers/flags, obsolete aliases or dual paths. One exception:
  the costs ledger (`internal/costs/ledger.go`) upgrades older schema versions
  in place, in one transaction that keeps every row, because transcripts expire
  and the ledger is the only lasting record of spend. A second exception:
  numbered migrations in `internal/migrate/` may move files Workbench itself
  wrote into its runtime folders, without losing data, and delete caches,
  logs, locks and empty files Workbench itself wrote (or, in a log a running
  process keeps open, only the lines of an older format), and an old copy
  whose new copy holds the same bytes; they are idempotent, never touch other tools'
  files, never merge, and leave a file where it is when old and new data both
  exist. Migration 0005 may also move the two work-token answers
  (`jira_api_token`, `gitlab_token`) from `machine.toml` into a new
  `work.env`: it never replaces an existing `work.env`, deletes the two keys
  only once `work.env` holds the same tokens, and changes neither file when
  `work.env` holds anything else; until it has run, the answers keep the two
  keys as retired ones, never asked and carried through every write. The
  costs import worker runs the costs move itself, without asking,
  because usage left unread could be lost (ADR 0008).
- Preserve user data and native tool interoperability. Reject unsupported state
  without deleting or converting it.
- Platform tests live only in `internal/platform/`: other packages read its
  facts and calls and never test `runtime.GOOS` or WSL themselves, which a
  `forbidigo` rule enforces (ADR 0012); `release.Target()` alone names the
  build's own system.
- Reuse common functionality at its existing owner. One Go/Cobra CLI, one Go
  module, focused internal packages; no public `pkg`, generic `utils`, mock-only
  interfaces, extra task runner or speculative plugin framework.
- Keep tests near zero. Add one only for concrete catastrophic data loss
  (including silent, unrecoverable corruption of the costs ledger, the only
  lasting record of spend), credential exposure or unauthorized execution.
  Explain that risk beside it.
  No coverage target, TDD mandate, per-feature suite or broad failure matrix.
- Routine gates: `golangci-lint fmt`, `golangci-lint config verify`,
  `golangci-lint run ./...`, `go build ./...`; run the tiny safeguards with
  `go test ./...`. Pins are `go.mod` and `.golangci-lint-version`.
- Apply decides once: the first approved apply saves `skip`, `select` and
  `decided` in `machine.toml`; later applies print the plan and apply the saved
  choices without asking unless an effect is new (with something to do) or a
  Workbench-owned file (not a merged one) was edited outside it. A machine that
  has never decided asks once. `--choose` always asks, `--reset` forgets the
  choices, `--yes` never asks (refused with `--choose`, `--reset` or
  `--approve-plan`). Applying without asking needs a controlling terminal and
  no coding agent: a caller without a terminal needs `--approve-plan` or
  `--yes`, and a coding agent, even in a pseudo-terminal (`CLAUDECODE=1`, or
  `CODEX_CI`, `CODEX_THREAD_ID`, `CODEX_SANDBOX`,
  `CODEX_SANDBOX_NETWORK_DISABLED` set by Codex), always needs
  `--approve-plan` and is refused `--yes` and `--choose`. A run nobody approved
  is refused (exit 3) before Workbench downloads or saves anything. The scripts
  apply runs never prompt: ticking the step is the approval (one-time helpers
  the owner runs later may prompt). The one thing the apply asks for itself is
  the Mac password, at most once per apply, at a terminal on a Mac
  (`operation.WithAdmin`). Workbench reads it itself, without echo, and
  checks it with `/usr/bin/sudo -k -S -v`: after approval and before it writes
  anything when a checked step has to install Homebrew, otherwise the first time
  a Homebrew command (an app such as Docker Desktop) or the `touch-id-sudo`
  step needs it. It lives only in Workbench's memory for that apply, in its
  redaction set, never on disk, in the environment, arguments, logs, plan or
  `--json`. The darwin scripts give
  `SUDO_ASKPASS` to Homebrew's installer, `brew` and the `touch-id-sudo` step's
  one sudo command only, pointing at a helper in a private folder made for that
  apply, which asks Workbench over a Unix socket that answers only processes the
  apply started. Homebrew drops earlier sudo approvals each time it runs, which
  is why Workbench keeps the password, not an approval. Where Touch ID for sudo
  is on (an active `auth` line naming `pam_tid.so` in `/etc/pam.d/sudo_local` or
  `/etc/pam.d/sudo`), an attended apply never reads the password and makes no
  helper: when a checked step has to install Homebrew and no sudo approval is
  valid, Workbench runs `/usr/bin/sudo -v` at the terminal before it writes
  anything, sudo asks by Touch ID (or for its own password where Touch ID cannot
  be used), and Homebrew's own sudo asks for itself later. An unattended run (no
  terminal) never asks and makes no helper, and is refused (exit 3) before any
  change only when Homebrew must be installed with no valid sudo ticket.
  Optional steps are off until ticked, each its own choice. On WSL, Windows
  setup and each of its parts (Terminal settings, PowerShell profile, font
  registry) are separate optional steps, and ticking Windows setup ticks none of
  its parts; only WSL networking is on by default. On macOS the one optional
  step is `touch-id-sudo`, which turns on Touch ID for sudo through
  `/etc/pam.d/sudo_local`; Linux has none.
- Unattended runs (agents, scripts): `workbench apply --dry-run --json` prints
  the plan with `plan_digest`; `workbench apply --approve-plan DIGEST`
  applies exactly that plan and exits 4 if the machine, release or saved
  selection changed since. `workbench init --answers-from FILE --dry-run --json`
  and `--approve-plan` work the same way. `--yes` is for a person who trusts
  the saved selection, not for a caller that did not read the plan; it never
  asks, so use it only when new effects may take their defaults. A coding agent
  is refused it. From a checkout, add `--local-build` to both `apply` calls.
  After a tool pin changes in its `versions.toml`, run
  `workbench update --local-build` first: it installs the new tools without
  asking (an agent may run it) and `apply --dry-run` then plans.
- Use isolated destinations and synthetic credentials for smoke checks. Never
  provision the developer's live machine to validate code.

## Ownership

| Concern | Canonical owner |
| --- | --- |
| CLI flags/output | `internal/cli/` |
| Context, consent, locks, subprocesses, state, checkpoints | `internal/operation/` |
| Native machine planning/setup/apply | `internal/machine/` |
| Bundle verification/staging/activation | `internal/release/` |
| Existing-project discovery/planning | `internal/project/` |
| Machine questions | `home/.chezmoi.toml.tmpl` |
| Package/extension names and pins | `home/.chezmoidata/packages.toml`, `versions.toml` |
| All personal VS Code settings | `home/.chezmoidata/vscode.json` plus the merge in `internal/machine/vscode.go` |
| Claude Code settings | `home/.chezmoidata/claude.json` (`defaults`, `enforced`) plus its modify template |
| Portable Python project policy | `project/pythonpolicy/` |
| AI-tool cost ledger, ingest, rates and reports | `internal/costs/` (hooks in `home/.chezmoidata/claude.json` and `home/.chezmoitemplates/codex-hooks.json.tmpl`) |
| Operating-system calls and platform facts | `internal/platform/` |
| Agent command memory guard | `internal/guard/` (hooks in `home/.chezmoidata/claude.json` and `home/.chezmoitemplates/codex-hooks.json.tmpl`) |
| Migrations of Workbench's own files | `internal/migrate/` (moves done by the package whose files move) |

`.chezmoiroot` selects `home/`; application source, project policy and docs are
not home deployment targets. Keep chezmoi naming and platform selection in
`home/.chezmoiignore`. Use derived `has_work`, `has_personal`, `is_wsl` flags;
do not duplicate the questionnaire or role logic. Provisioning stays in the
existing platform scripts, consuming shared management dependency choices.

The Go operation layer owns writes and consent. TOML Kit is a bounded private
text adapter, not another CLI; HuJSON and go-yaml own their document formats.
Native uv owns dependency resolution. Inspection/preview must not execute project
code or fetch missing dependencies. Explicitly selected native build effects
are not sandboxed and must remain visible in consent.

## Machine-source changes

`scripts/package-release.py` digests a release's machine source into its
`release.json` and stamps the same digest into its executable, which refuses
a release source that differs. Nothing is committed for this. A developer
checkout used with `--local-build` is instead bound by its actual content digest,
which the approved plan carries, so `home/` edits preview and apply without a
rebuild. Tool version pins are read at runtime from that source's
`home/.chezmoidata/versions.toml` (`--local-build`, else the active release).
Before committing `home/` changes, run the existing
`scripts/render-check.sh ROLE MODE [wsl]` for affected roles/modes. Do not
weaken the release source check or add a second renderer. The WSL argument is
static simulation, not Windows qualification.

Invalid existing configuration must fail without fallback replacement. Global VS
Code merging accepts JSONC and edits the file the way VS Code does: only values
that differ are written and new members follow the file's own layout, so
comments, key order, a byte order mark and line endings stay, and the file is
untouched when no value changes. The shared `modify_settings.json.tmpl` runs
the hidden `workbench vscode-settings` filter (`internal/machine/vscode.go`),
which refuses, with the file untouched, invalid JSONC, a non-object, duplicate
keys or an edit it cannot prove. Project
JSONC editing preserves supported syntax. Keep unowned nested settings/custom
color rules. ty and native Ruff are the editor default; basedpyright is CLI/CI
only. Do not change deferred TypeScript/import-color policy incidentally.

## Documentation and release boundaries

Use repository-relative source paths and portable runtime notation, never a
developer's absolute paths or private scratch evidence as dependencies. Describe
Workbench directly. Keep commands aligned with the built CLI and native support
claims aligned with `docs/acceptance.md`.

Releases are personal: pushing a `v*` tag publishes the four bundles and a
tag-stamped `install.sh` through `.github/workflows/release.yml`, with that
tag's changelog section as release notes, then commits the regenerated
`CHANGELOG.md` to `main`. Write conventional commit subjects, which become the
changelog; never edit `CHANGELOG.md` by hand. They are
unsigned and grant no redistribution rights; add no LICENSE. Only that
workflow passes `scripts/package-release.py --published`. A tag with a hyphen
(`v0.2.1-rc.1`) is published as a GitHub pre-release, which `update` installs
only by name and which gets no changelog section; unreleased code reaches a
real machine only as a pre-release. A local bundle is named after the next
release with a `-dev.N` suffix (`v0.2.1-dev.1` after `v0.2.0`), is installed
only in disposable homes and CI, and `update` without a VERSION replaces it
with the latest published release (ADR 0011). Only the owner
pushes release tags. Repository visibility changes, installation on a real
machine and destructive branch/worktree cleanup still need the owner's explicit
go-ahead.
