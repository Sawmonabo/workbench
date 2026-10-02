# Workbench contributor instructions

Read [README](README.md), [design](docs/superpowers/specs/workbench-design.md),
[contracts](docs/superpowers/specs/workbench-contracts.md) and the relevant
[implementation plan](docs/superpowers/plans/workbench-implementation.md).
Distinguish implemented code, observed checks and unqualified release targets.

## Development contract

- Greenfield: update canonical code, callers and docs in place. No migrations,
  compatibility readers/flags, obsolete aliases or dual paths.
- Preserve user data and native tool interoperability. Reject unsupported state
  without deleting or converting it.
- Reuse common functionality at its existing owner. One Go/Cobra CLI, one Go
  module, focused internal packages; no public `pkg`, generic `utils`, mock-only
  interfaces, extra task runner or speculative plugin framework.
- Keep tests near zero. Add one only for concrete catastrophic data loss,
  credential exposure or unauthorized execution. Explain that risk beside it.
  No coverage target, TDD mandate, per-feature suite or broad failure matrix.
- Routine gates: `golangci-lint fmt`, `golangci-lint config verify`,
  `golangci-lint run ./...`, `go build ./...`; run the tiny safeguards with
  `go test ./...`. Pins are `go.mod` and `.golangci-lint-version`.
- Apply decides once: the first approved apply saves `skip`, `select` and
  `decided` in `machine.toml`; later applies print the plan and apply the saved
  choices without asking unless an effect is new (with something to do) or a
  Workbench-owned file (not a merged one) was edited outside it. A machine that
  has never decided asks once. `--choose` always asks, `--reset` forgets the
  choices, `--yes` never asks (refused with `--choose` or `--reset`). Applying
  without asking needs a controlling terminal; a caller without one needs
  `--approve-plan`. Scripts apply runs never prompt: ticking the step is the
  approval (one-time helpers the owner runs later may prompt). A Windows setup
  part ticked on its own keeps its own choice while Windows setup is on.
- Unattended runs (agents, scripts): `workbench apply --dry-run --json` prints
  the plan with `plan_digest`; `workbench apply --approve-plan DIGEST`
  applies exactly that plan and exits 4 if the machine, release or saved
  selection changed since. `workbench init --answers-from FILE --dry-run --json`
  and `--approve-plan` work the same way. `--yes` is for a person who trusts
  the saved selection, not for a caller that did not read the plan; it never
  asks, so use it only when new effects may take their defaults. From a
  checkout, add `--local-build` to both `apply` calls.
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
| All personal VS Code settings | `home/.chezmoidata/vscode.json` plus the shared merge |
| Claude Code settings | `home/.chezmoidata/claude.json` (`defaults`, `enforced`) plus its modify template |
| Portable Python project policy | `project/python/` |

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

Invalid existing configuration must fail without fallback replacement. Global
VS Code merging accepts JSONC, leaves the file untouched when no value changes
and otherwise emits JSON without comments; project JSONC editing preserves
supported syntax. Keep unowned nested settings/custom color rules. ty and native Ruff are the editor default; basedpyright is CLI/CI only.
Do not change deferred TypeScript/import-color/Todo Tree policy incidentally.

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
unsigned and grant no redistribution rights; add no LICENSE. Only the owner
pushes release tags. Repository visibility changes, installation on a real
machine and destructive branch/worktree cleanup still need the owner's explicit
go-ahead.
