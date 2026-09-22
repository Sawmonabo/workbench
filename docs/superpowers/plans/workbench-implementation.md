# Workbench implementation plan

Status: implementation started; the initial read-only CLI increment exists. The remaining lifecycle/project work below is not completed. This document and the [design specification](../specs/workbench-design.md) are maintained repository inputs. All file paths are repository-relative.

## Progress

- Implemented: one Go/Cobra executable, help/version/completions, PATH-only doctor inventory, development status and bounded filename-based project inspection. Unsupported lifecycle/configure commands return nonzero without changes.
- Implemented: shared Go/linter pins, gofmt/goimports, standard lint and CI compilation of macOS/Linux arm64/amd64 targets. Existing machine render checks are unchanged. No automated test suite was added.
- Deliberately incomplete: full doctor validation, release/drift tracking, parsed workspace ownership, planning, execution/consent, persistent state/locking, recovery, installer and project configuration. No numbered phase is claimed wholly complete.
- Read-only discovery from phase 7 was brought forward because it has no mutation prerequisites. It is not yet the resolved ownership model needed for configure. Empty operation/platform packages were not created.

### Verification recorded for the initial increment

On 2026-09-22, native macOS arm64 checks passed with the recorded Go/linter pins: formatting, lint configuration validation, standard lint (zero issues), build, help/version, doctor, status and inspection. Missing tools, a missing directory, apply dry-run and project configure dry-run returned exit 1 as intended. Manual mixed-language inspection found Python/JavaScript/Rust/Go candidates, handled a directory name containing spaces, excluded hidden/dependency directories, skipped an external symlink and reported a nested repository boundary. Selecting that nested repository directly worked. Synthetic input-file hashes were unchanged afterward.

Cross-compilation succeeded for macOS/Linux arm64 and amd64, with output architectures checked. YAML syntax and a secret scan of new CLI/configuration/documentation content passed. CI itself has not run remotely, and no native Linux/WSL provisioning behavior was verified. No machine configuration was applied, global tools installed, automated tests added, commits created or changes pushed.

## Delivery objective

Deliver one Go/Cobra CLI for developer-machine configuration and existing-project tooling. Install from release bundles through a minimal POSIX bootstrap; reuse chezmoi and existing provisioning owners. Support macOS Apple Silicon/Intel, declared Linux/Ubuntu targets, and WSL with Windows integration. Native Windows without WSL is outside the initial compatibility commitment.

The application must provide doctor, status, pull, plan, apply, configuration-only apply, update, scoped configuration revert, project inspect and project configure. Project configuration uses explicit actions and repeatable language filters, never application generation. Personal VS Code data remains global machine configuration; project policy remains portable.

Do not implement a second package manager, shell CLI, per-language installer, generic plugin framework or task runner. Shared operations own planning, consent, dependency ownership, execution, locking, reporting and checkpoint behavior. Preserve current native tool functionality unless the specification explicitly requires a safety correction.

Workbench is greenfield: update the canonical implementation and all affected callers/docs in place. Remove superseded code. Do not implement backward-compatibility layers, migrations, deprecated aliases, old schema readers or dual paths. Preserve user data and native platform/tool integration; these are safety and interoperability requirements, not backward compatibility.

## Existing source and known constraints

| Source | Reuse and required attention |
| --- | --- |
| `.chezmoiroot`, `home/` | Preserve the machine deployment boundary. Product code and project policies stay outside it. |
| `home/.chezmoi.toml.tmpl` | Reuse questions, role flags, WSL detection and stored answers. Test archive-based initialization and secret-safe quoting. |
| `home/.chezmoidata/packages.toml`, `versions.toml` | Reuse canonical names/pins. Resolve duplicated ownership of management dependencies; the apt list is not currently the executed Linux package plan. |
| `home/.chezmoitemplates/vscode-settings.json.tmpl`, `home/.chezmoidata/vscode.json` | Reuse the single editor merge/data source. JSONC input is accepted but comments are not retained by this machine-settings merge. |
| `home/.chezmoiscripts/` | Reuse provisioning owners. Audit hidden writes, cleanup, elevation, architecture assumptions, partial outcomes and duplicate dependency installs. |
| `home/dot_codex/modify_private_config.toml.tmpl` | Account for Python with `tomllib`; invalid input must not silently lose unowned state. |
| `home/.chezmoiignore`, `home/.chezmoiremove` | Preserve platform/role selection. Preview and checkpoint removals, including configuration-only operations. |
| `scripts/render-check.sh`, `.github/workflows/ci.yml` | Extend the current render/lint/role/mode coverage. Simulated WSL rendering does not validate Windows integration. |

The initial read-only application exists; installer and lifecycle engines do not. Known work includes amd64-only download assumptions, some best-effort scripts returning success after skips, automatic maintenance, and script-written Windows configuration that is not automatically covered by native file diffs.

## Execution rules

- Establish a source/status baseline before edits and preserve unrelated changes.
- Keep new automated tests close to none. Add one only when it prevents a specific catastrophic failure: irreversible data loss, credential exposure, or unauthorized code execution/system mutation. State that reason beside the test. Do not add coverage targets, test-per-feature requirements or a test framework.
- Use formatting, linting, builds, code review and brief manual smoke checks for routine behavior. The verification bullets below are manual/static checks unless explicitly identified as a justified safety test; they do not authorize an automated suite per phase.
- Use isolated destinations for mutation checks; never exercise provisioning or recovery against a developer's live machine.
- Inspect the existing owner before adding a helper. Change it and its callers/documentation together, including any affected justified safety tests, instead of maintaining parallel implementations.
- Apply Go formatting during implementation. Use one pinned golangci-lint configuration for local and CI checks; no compatibility work or unrelated structural scaffolding.
- Avoid reproducing shell provisioning in Go. Add only the coordination and narrow interfaces needed for planning, consent, outcomes and dependency ownership.
- Do not make platform, performance or recovery claims from parsing checks or cross-compilation alone.
- Stop at unresolved product choices that affect safety, supported scope or external effects. Record the decision in the specification before making it a public contract.

## Dependency order

```text
1. Resolve blocking contracts and minimal validation policy
   → 2. Shared CLI/context/execution foundations
   → 3. Chezmoi integration and safe provisioning boundary
   → 4. Configuration application and recovery
   → 5. Release lifecycle and installer
   → 6. Personal editor/Python machine policy
   → 7. Workspace discovery and project configuration
   → 8. Focused platform/resource/security acceptance
   → 9. Publish-ready documentation and release artifacts
```

Develop each stage with the smallest relevant verification. Release packaging can be prototyped earlier, but do not activate an updater before its destructive-failure safeguards are verified. A Python-only smoke check is not evidence of monorepo support.

## Task 1: Resolve contracts and establish lean validation

### Files

- Update `docs/superpowers/specs/workbench-design.md` with resolved choices.
- Add only minimal package-local `testdata/` when a justified safety test needs file input; no central fixture tree.
- Reuse `scripts/render-check.sh` and `.github/workflows/ci.yml` without duplicating their render logic.

### Work

1. Inventory each existing managed target/provisioning effect: owner, platform, role, prerequisites, version mode, privilege, write scope, confirmation requirement and recovery coverage. Include Windows user environment, registry, system tuning and default-distribution changes separately from files.
2. Select supported OS/WSL versions, CPU targets and environments for short native smoke checks. Check upstream artifacts, including the current Linux Go and Windows-side x86-64 assumptions. Keep unverified targets unsupported rather than silently selecting the wrong binary.
3. Select Go/Cobra, golangci-lint v2 and management-tool versions; document ownership rules for compatible borrowed tools versus private installs. Decide how existing scripts receive the shared resolution without maintaining another installer.
4. Prove a native chezmoi configuration initialization path using an extracted source tree, with no Git checkout and no duplicated questions. Include Python-dependent modify scripts and correctly escaped machine answers.
5. Evaluate maintained project configuration editors for TOML/JSONC/YAML preservation and unsupported syntax. Record the selected libraries and limitations; no regex merge substitute.
6. Decide unattended inputs/consent, output/error contracts, checkpoint identification/retention, project recovery UX, runtime directories/overrides, current-state validation and release integrity policy. Reject unsupported Workbench state without conversion or deletion; do not design a compatibility matrix.
7. Keep additional languages/managers, native Windows, automatic maintenance and resource-budget values explicitly unresolved until evidence supports a decision.

### Verification and gate

- Existing role/mode rendering produces equivalent intended configuration in isolated destinations.
- Review configuration/path hazards using minimal synthetic inputs only where necessary to establish a safety boundary.
- Record the feature/platform checklist and brief manual verification instructions in maintained documentation, not a new testing framework.
- No implementation stage relies on an invented native flag, missing tool version or undefined destructive-operation policy. Independent work may proceed, but a blocked feature cannot be advertised as complete.

## Task 2: Build the shared CLI, context and execution foundations

### Files

- Add `go.mod`, `go.sum`, `.golangci.yml`, `cmd/workbench/main.go` and `internal/cli/`.
- Add `internal/operation/` and small `internal/platform/` helpers only where required.
- Keep any justified `_test.go` files beside their owner and only necessary fixtures in its `testdata/` directory.

### Work

1. Register one Cobra command tree: `doctor`, `status`, `pull`, `plan`, `apply`, `update`, `revert`, `project inspect` and `project configure`.
2. Implement explicit PATH resolution, existing-directory validation and repeatable `--language` selection. Omitted project PATH means `.`. Unsupported explicit language requests fail before mutation.
3. Define one resolved operation context and plan/result model. A plan contains source identity, scope, observed inputs, intended target edits/removals, prerequisites, external effects and recovery limits.
4. Add one cancellable subprocess runner with argument arrays, controlled environment, bounded output and redaction. Do not build shell command strings from project paths or answers, or introduce a mock framework.
5. Add shared confirmation and read-only enforcement. The same planner backs `plan` and `apply --dry-run`; missing tools must not trigger repair.
6. Implement operation locking and atomic private state writes. Distinguish machine, project and shared release/dependency state; prevent overlapping writes without duplicating lock logic.
7. Add version/help/completion output through Cobra. Keep handlers thin and ensure incomplete handlers return an explicit unsupported/not-implemented result, never a fake success.
8. Configure `gofmt` and `goimports` plus the standard golangci-lint set in `.golangci.yml`. Reuse the same pinned tool/config locally and in CI. Keep one Go module and create focused `internal` packages only as behavior requires them; no generic `utils`, speculative public packages or layered scaffolding.

### Verification and gate

- Run formatting, lint and build checks; manually smoke-check help, current-directory defaults and language selection. Do not unit-test Cobra behavior.
- Add only a minimal safety regression for a Workbench-owned boundary that could execute unauthorized changes or disclose secrets; test its real owner, not every CLI handler.
- Inspect locking and atomic state-write behavior for corruption hazards; reuse the destructive-state safeguard in phase 4 instead of duplicating it here.
- Build the supported targets. This is a compilation gate, not platform provisioning certification.

## Task 3: Integrate chezmoi and make provisioning effects explicit

### Files

- Add `internal/machine/` for the shared native context and coordination.
- Update `home/.chezmoi.toml.tmpl`, `home/.chezmoiscripts/`, and existing shared fragments only where needed.
- Extend canonical package/version data and `.github/workflows/ci.yml`.

### Work

1. Pass the same explicit source, configuration, destination and persistent-state context to every native invocation. Preserve role flags and conditional work settings.
2. Reuse native target enumeration, diff and apply behavior. Audit template functions, hooks and modify scripts before claiming read-only previews.
3. Preflight rendering dependencies, including Python with `tomllib`, before attempting target rendering. Setup can approve prerequisite installation; config-only apply cannot install it silently.
4. Establish one dependency owner per tool. Coordinate clean installs, existing package-manager installations and privately owned tools. Avoid duplicate chezmoi/uv installations and preserve compatible user-owned tools.
5. Make destructive maintenance/app replacement separately approved. Correct the macOS scripts' cleanup/recovery-copy behavior at their existing owner; do not add another package/app installer in Go.
6. Make skips/failures machine-detectable and preserve retry semantics. Audit native run-once/on-change state so a skipped required action is not permanently treated as successful.
7. List script-written configuration targets and external effects for WSL and macOS helpers. Replace unsafe whole-file behavior with an explicit ownership decision or narrow merge where appropriate.
8. Keep platform/role removals visible in previews and change invalid-input configuration merges to fail safely, preserving the original input for recovery.
9. Separate capability discovery from repair. Doctor checks actual tool versions and target hosts but does not initialize environments, start language servers, open remote sessions or change WSL state.

### Verification and gate

- Reuse the existing role/mode render entry point; do not add another matrix.
- Manually check an already-owned dependency and a clean isolated environment with prerequisites absent.
- Config-only apply invokes no provisioning scripts and still reports any managed removals accurately.
- Review failure/skip reporting and manually exercise relevant changed paths. Automate malformed-input handling only where it would otherwise overwrite user data, through the shared safety test rather than per-script suites.
- Diff/doctor evaluation must not modify targets. Any helper incapable of that is blocked from the read-only path until corrected.

## Task 4: Implement configuration apply and recovery

### Files

- Extend `internal/operation/` and `internal/machine/`.
- Add the smallest package-local destructive-change safety test and inputs; use `internal/platform/` only for genuine filesystem differences.

### Work

1. Bind a plan to the selected source and observed input/target state. Validate again before writes and reject changes made after preview.
2. Record private pre-images for all supported configuration targets, including existence/type, content, permissions and symlink targets. Record post-images and operation outcomes afterward.
3. Apply using the existing native engine and approved provisioning owners. Distinguish complete, partial and failed outcomes; preserve recovery information after any interruption.
4. Implement default and explicit-checkpoint recovery selection according to the resolved public contract. A release version alone must not ambiguously choose one of several applications.
5. Preflight recovery conflicts for the complete target set before restoring. Protect later user edits, preserve correct link semantics, and handle newly created/deleted files narrowly.
6. Ensure additive editor settings recovery actually removes newly introduced keys when appropriate; selecting an older merge source alone is insufficient.
7. Report packages, extensions, registry/service effects and uncheckpointed script writes as non-reverted. Do not run provisioning scripts backward.
8. Establish bounded retention without pruning active recovery state. Ensure disk-full or unwritable-state failures stop before uncheckpointed target changes.

### Verification and gate

- Keep a minimal automated regression at the shared mutation/recovery boundary for irreversible data-loss risks: changed user files, escaping scope and loss of recovery evidence. Use a few necessary cases, not a lifecycle test matrix.
- Manually interrupt an isolated apply and verify recovery uses evidence or stops for review; do not create an interruption harness.
- Review that machine recovery cannot touch project paths and failed checkpoint creation prevents writes.
- Manually repeat apply for unchanged inputs and confirm recovery limitations appear in preview/result output.

## Task 5: Add release lifecycle and the minimal installer

### Files

- Add `internal/release/`, `install.sh`, release packaging under `scripts/`, and release workflow configuration.
- Reuse `internal/operation/` and dependency ownership from `internal/machine/`.
- Record immutable dependency and release identities in generated metadata derived from canonical source data.

### Work

1. Build per-target bundles containing the CLI, machine sources, project policies, licenses and generated release metadata. Exclude host state and maintainer-only files through an explicit payload definition.
2. Implement target validation, bounded downloads and archive validation. Reject unsafe members, escaping links, malformed metadata, incompatible state/runtime versions and mismatched integrity data.
3. Implement `pull` as staging only. It must not activate the runtime, upgrade tools or apply configuration.
4. Implement preview/apply against the selected immutable candidate. Keep CLI, candidate and applied-configuration versions separate. Validate a runtime handoff before activation and keep the previous working runtime available.
5. Implement `update` by calling the same pull/plan/apply operations. Reinstallation calls the same shared setup functions rather than duplicating them in the installer.
6. Implement POSIX bootstrap target detection, minimal prerequisite checks, initial download/verification and handoff. Do not add package lists, machine questions, language setup or revert logic to shell.
7. Handle interactive piped installation with terminal input, explicit noninteractive consent, install-only behavior and an honest bootstrap-only dry-run. Do not overwrite an unrelated command.
8. Validate the current state format before activation and preserve working state on rejection. Implement no historical schema readers, converters or backward-compatible runtime paths.

### Verification and gate

- Fresh installation works without a source checkout, Go compiler, preinstalled Python or manually installed management tools, while declared bootstrap utilities remain prerequisites.
- Keep a minimal automated release-safety check only for unsafe extraction or unverified executable activation; do not retest library behavior or build a broad download-error matrix.
- Manually check representative failed installation/activation and an existing command collision in an isolated destination.
- Pull changes only staging state. Preview and config-only operations never fetch missing dependencies implicitly.
- A failed candidate does not destroy the working runtime; rollback limitations remain honest.
- A manual installation smoke check from a published-style bundle, not the working tree, verifies the user path.

## Task 6: Add personal editor and Python machine policy

### Files

- Update `home/.chezmoidata/vscode.json`, `packages.toml` and `versions.toml`.
- Reuse `home/.chezmoitemplates/vscode-settings.json.tmpl` and both existing VS Code target files.
- Add proposed `home/dot_config/ty/ty.toml` and `home/dot_config/ruff/ruff.toml` only after verifying native discovery.
- Reuse existing extension provisioning; do not create a second platform extension list or new extension test suite.

### Work

1. Add the exact personal settings and machine fallback rules in design section 8. Preserve the Dark 2026 color policy, existing managed-rule identities, custom rules and unrelated nested settings.
2. Keep personal theme, zoom, JSON formatter and layout preferences in the global data source, alongside Python-scoped settings. Do not create a Python-owned global settings file.
3. Add the ty extension; retain Ruff and Python environment/debugger support. Remove Pylance/mypy from desired installation, without interpreting that as permission to mass-uninstall existing extensions.
4. Add ty, Ruff and basedpyright to the canonical uv-tool table after validating release pins. Do not remove unrelated tools or introduce Copier. basedpyright remains CLI-only by default.
5. Target the confirmed VS Code host/profile. Reuse the existing install owner for macOS and add only missing platform invocation using the same canonical data. Do not automatically connect to remote hosts.
6. Preserve host-local settings and deferred TypeScript/import-color/Todo Tree choices. Coordinate settings ownership with active profiles and Settings Sync without silently changing either.

### Verification and gate

- Manually inspect the merged settings and repeat apply. Reuse the data-loss safety regression for destructive malformed-input or unowned-value loss rather than adding editor-specific suites.
- Verify native checker configuration parsing for selected versions, without relying on old probe outcomes.
- Manually check project tool resolution and supported bundled fallbacks in the actual editor; global CLI installation is not sufficient evidence.
- Confirm only ty and native Ruff run for the default Python editor stack; basedpyright is available on demand and in project CI.
- Record a brief native check of the intended host/profile; no automated editor harness.

## Task 7: Configure existing projects and supported monorepos

### Files

- Add `internal/project/`; use only minimal package-local inputs if a justified destructive-scope test requires them.
- Add `project/python/policy.toml`, `extensions.json`, `gitignore.entries`, `checks.example.yml` and their usage documentation from design section 9.
- Reuse shared operation/context/consent/checkpoint code; do not add a Python CLI or installer.

### Work

1. Implement bounded manifest/workspace discovery without executing project code. Recognize Python, JavaScript/TypeScript, Rust and Go boundaries; distinguish recognition from supported configuration.
2. Resolve project roots, enclosing workspaces, supported package managers, standalone configs and shared lockfile owners. Respect generated/dependency exclusions and escaping-link boundaries.
3. Make `project inspect` report this inventory read-only. Reuse it for configure; no second scan pipeline or per-language workspace registry.
4. Compose one plan for selected supported projects. Repeated language filters narrow it; explicitly unsupported language requests fail before writes. Deduplicate shared editor/CI/root-file edits and stop on conflicting proposals.
5. Implement uv-managed Python configuration using the selected maintained format-aware editor and native dependency operations. Preserve application metadata, Python constraints, build backend, source/test layout, dependency groups and existing rule choices.
6. A selected subdirectory cannot silently write its parent lockfile/configuration. Report the necessary broader scope and require a deliberate broader selection.
7. Add only relevant, reviewed recommendations/ignore entries. Keep personal settings out of projects and preserve pattern/negation order.
8. Integrate optional CI with the existing workflow. Use locked Ruff/basedpyright checks and the actual test runner; do not force pytest, a Python version, a CI provider or branch protection.
9. Implement duplicate-free repeated configuration, preview/input revalidation and explicit partial results. Project dependency resolution/builds are approved external effects, never preview work.
10. Record portable policy/release provenance using the resolved format. Implement the decided project recovery UX or explicitly withhold that capability; machine revert remains unable to restore project targets.

### Verification and gate

- Manually check a representative uv project and mixed-language workspace, including shared configuration/lockfile ownership; no exhaustive layout/manager fixture suite.
- Review ambiguous ownership, unsupported input and configuration preservation. Add a minimal project-scope safety regression only if the shared destructive-change test cannot exercise the actual project boundary.
- Unsupported detected packages are visible and unchanged. A mixed-language run does not install tooling for unsupported languages or claim the repository is wholly configured.
- Dry-run performs no installs, package resolution or lockfile writes. Repeated configuration makes no unnecessary changes.
- Compare application files and unrelated configuration before/after; no generated application code, framework, README, Git repository or fabricated tests may appear.

## Task 8: Run platform, resource and security acceptance

### Files

- Extend `.github/workflows/ci.yml` with Go formatting/lint/build checks and only the justified safety tests.
- Add brief maintained smoke-check instructions and results for supported platforms, including real WSL; do not build a new end-to-end or benchmarking framework.

### Work

1. Retain the existing personal/work/both and pinned/latest rendering checks and secret scanning. Do not expand them into a new automated feature matrix or provision ordinary development machines.
2. Record a short manual bundle/install/preview/apply smoke check in a disposable environment on each declared macOS/Linux target. Exercise update/recovery on representative changed safety paths rather than every role/version/platform combination.
3. Perform a short native WSL/Windows smoke check of the supported host integration, concentrating on changed write boundaries. A mocked WSL flag or Linux-only check is insufficient evidence of native operation.
4. Verify all paths share the same command handlers and operation implementations, with one dependency owner and no duplicated platform/package policy.
5. Review unsafe writes, links, permission failures and interruption handling; run the minimal justified safety checks, not a comprehensive filesystem/error matrix.
6. Reuse secret scanning on release payloads and inspect log/checkpoint privacy. Reuse the secret-exposure safeguard where justified; do not log secret-bearing subprocess environments.
7. Spot-check startup, inspection and resource use on a representative workspace. Record observations; add neither a benchmark framework nor routine performance tests. No continuous watcher or server is required.
8. Verify current-shell/PATH limitations are explained, not worked around by injected terminal commands or forced restarts.

### Verification and gate

Accept static-check output, review and brief reproducible manual observations for routine criteria. Automated checks remain limited to explicitly justified catastrophic risks. If a required platform cannot be smoke-checked, report its release gate as incomplete; do not treat compilation alone as verified native support.

## Task 9: Prepare accurate public documentation and releases

### Files

- Update `README.md`, platform documentation and CLI usage documentation to match implemented behavior.
- Update this plan/specification with resolved decisions and implementation status.
- Finalize release workflow/package configuration and maintained acceptance records.

### Work

1. Document installation prerequisites, published artifact locations, download-and-inspect steps, supported platforms and one consistent CLI interface.
2. Document configuration adoption, noninteractive usage, dry-run limits, dependency ownership, failure recovery and effects that revert does not undo.
3. Explain global versus project settings ownership and mixed-language selection with examples matching the implemented commands.
4. Manually verify documented command/flag/examples against the built executable and supported-platform claims against recorded native checks.
5. Ensure all necessary product decisions are in maintained repository documents and no workstation-specific paths or private artifacts are required.
6. Prepare signed/verified artifacts according to the resolved release policy. Publishing requires separate release authorization; writing this plan does not perform or authorize publication.

### Verification and gate

The README installation command becomes live only after its release assets exist, native smoke checks are recorded, and the minimal safety checks pass. The declared feature/platform matrix, licenses, dependency pins, checksums and applicable signatures agree with the artifacts. No product feature is labeled implemented solely because a design document describes it.

## Validation commands for implementation

Run from the repository root only after Go source and the pinned tools exist. Apply formatting locally:

```sh
golangci-lint fmt
```

Use non-mutating CI checks:

```sh
golangci-lint config verify
golangci-lint run ./...
go build ./...
```

The standard linter set includes `govet`; do not run a redundant standalone vet job. Enabled formatters report formatting issues through `run`, so CI need not rewrite files. Use this initial proposed `.golangci.yml`, validating it against the selected pinned release:

```yaml
version: "2"
linters:
  default: standard
formatters:
  enable:
    - gofmt
    - goimports
```

When justified safety tests exist, run `go test ./...` for that deliberately tiny suite. No coverage thresholds or routine race-testing matrix. Use a targeted race-detector run only when investigating a concrete concurrency risk that could corrupt user data. The existing render entry point remains:

```sh
scripts/render-check.sh personal pinned
scripts/render-check.sh personal latest
scripts/render-check.sh work pinned
scripts/render-check.sh work latest
scripts/render-check.sh both pinned
scripts/render-check.sh both latest
scripts/render-check.sh work pinned wsl
```

The last command is a simulation/lint check, not native Windows verification. These checks do not prove installation or provisioning; record brief manual release smoke results separately. Do not create a new end-to-end harness or claim these commands ran as part of writing documentation.

## Completion checklist

- [ ] All blocking decisions are resolved in the design and implemented contracts.
- [ ] One CLI, bootstrap handoff, shared lifecycle and dependency ownership are verified.
- [ ] Go formatting, standard lint and builds pass using the same pinned configuration locally and in CI.
- [ ] Canonical paths are updated in place, with no backward-compatibility layers or migration machinery.
- [ ] Required machine platform/role/version behavior is supported by existing static checks and brief native smoke results.
- [ ] New tests are near-zero and each protects against a stated catastrophic failure; no broad suite or coverage target was introduced.
- [ ] Read-only, consent, conflict and recovery safeguards have focused evidence without a test-per-feature mandate.
- [ ] Personal editor/Python policy is implemented through canonical owners.
- [ ] Existing-project and supported monorepo configuration is safe, scoped and repeatable.
- [ ] Resource observations, security checks and actual WSL smoke results support published claims.
- [ ] Release artifacts and public instructions match verified behavior.

Each checklist item requires actual evidence; writing the plan does not complete it. Implementation is now authorized, but publishing releases, committing and pushing remain separate actions. The progress section states the current implemented scope without treating the remaining plan as complete.
