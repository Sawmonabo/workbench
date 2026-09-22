# Workbench design

Status: implementation specification for private evaluation, 2026-09-22. Core lifecycle, machine and project handlers exist; production publication and native platform qualification remain gated. Acceptance requirements below are not claims that every target has passed them.

## 1. Purpose and scope

Workbench configures a developer's machine and project tooling through one cross-platform command-line application. Priorities are correctness, responsive operation, and controlled resource use for interactive development and AI-assisted workflows.

Workbench is not an application generator, package manager replacement, language server, background agent, or whole-machine backup system. It does not create application code, select a framework, initialize project Git history, or automatically rewrite repositories when machine settings change.

Users install a published release without cloning the repository or separately installing management dependencies. Maintainers develop the application in this repository. Existing native tools remain responsible for their own installation, dependency resolution, configuration semantics, and execution.

### Greenfield development contract

Workbench is greenfield. Update canonical code, callers, configuration and documentation in place. Remove superseded implementation paths; do not add backward-compatibility layers, migration machinery, deprecated command aliases, legacy schema readers, dual-write paths or compatibility flags. There is no obligation to support earlier development versions of Workbench.

This does not authorize overwriting user-owned files or dropping platform support. Native tool interoperability, preservation of unrelated settings, and checkpoint-based recovery are current product requirements, not legacy compatibility work. Reject unsupported Workbench state without modifying it; do not silently convert or delete it.

### Required outcomes

- One Go/Cobra executable, one command tree, and shared lifecycle operations across supported platforms.
- Preserve macOS, Linux/Ubuntu, and WSL with Windows integration; platform-specific behavior must remain explicit and tested.
- Reuse the existing chezmoi deployment boundary, questionnaire, configuration merges, platform scripts, package data, and validation entry point.
- Preview before approved changes, preserve unrelated data, and report incomplete work truthfully.
- Configure existing projects, including supported components of mixed-language monorepos, without imposing an application layout.
- Keep personal editor preferences separate from portable project policy.

## 2. Current implementation inventory

The following owners contain implementation, not merely proposed package names.
Native qualification and final acceptance are tracked separately in
[acceptance](../../acceptance.md).

| Owner | Responsibility |
| --- | --- |
| `internal/cli/` | Cobra command tree, human/JSON presentation and consent selection. |
| `internal/operation/` | Context, bounded execution, plan revalidation, private state/locks and scoped checkpoints/recovery. |
| `internal/machine/` | Qualified dependencies, native questionnaire setup, exact source trust, chezmoi planning/application and effect inventory. |
| `internal/release/`, `install.sh` | Bounded verified bundles, staging, journaled activation and a minimal executable-download handoff. |
| `internal/project/`, `project/python/` | Bounded metadata discovery; existing uv Python project/workspace configuration, optional editor/ignore/CI edits and shared recovery. |
| `.chezmoiroot`, `home/` | Canonical native machine source, platform/role selection and provisioning scripts. |
| `home/.chezmoidata/` | One package/extension/version/global-editor policy source. |
| `home/.chezmoitemplates/` | Shared shell/native configuration fragments and merges. |
| `scripts/render-check.sh`, `.github/workflows/ci.yml` | Existing role/mode renders, script lint, secret scanning and Go quality gates. |

Global VS Code merging accepts JSONC and preserves unrelated values/rules but
emits JSON without comments. Project TOML/JSONC/YAML editing has stricter
round-trip preservation gates. Invalid machine Codex TOML no longer falls back
to a replacement body.

Public command details are in [usage](../../usage.md). Status inspects recorded
identities, not package health or complete drift. Doctor performs bounded local
checks but does not qualify a live editor profile. Project configuration supports
uv Python; other languages are discovery-only. No “latest” release channel or
production-qualified activation exists. WSL Ubuntu guest config-only scope is
available, but full provisioning blocks pending native Windows host/path/ACL
qualification. Do not infer native acceptance from code,
cross-compilation or static WSL rendering.

## 3. Architecture and reuse

### Implementation boundaries

Use Go for the application and [Cobra](https://github.com/spf13/cobra) for commands, flags, help and completions. Cobra is not the provisioning engine. Use a small POSIX shell bootstrap only to acquire a verified executable and hand off to it. Users do not need the Go compiler to run a release.

| Responsibility | Owner |
| --- | --- |
| Machine target discovery, templates, differences and application | Chezmoi, invoked with one explicit context. |
| Machine questions and role selection | `home/.chezmoi.toml.tmpl`; no parallel questionnaire schema. |
| Package and runtime installation | Existing platform scripts and their native package managers, after safety corrections. |
| Personal editor data and merge | Existing shared VS Code data/template. |
| Project dependency changes | The supported project's native package manager. |
| Project configuration edits | TOML Kit, HuJSON and go-yaml with the preservation limits in the [implementation contracts](workbench-contracts.md#project-editors). Go owns planning and writes; no ad hoc TOML/JSONC/YAML rewriting. |
| Release acquisition, operation state, confirmation, locks and scoped checkpoints | Shared Workbench Go code. |
| OS-specific paths, permissions and interoperability | Small platform-specific functions around the shared operations. |

Do not add a second CLI in shell or Python, a separate installer for each language, a generic plugin framework, a replacement template engine, or an additional task runner. The same helper must own genuinely repeated behavior. Reuse does not mean forcing different package managers or unrelated file formats through an artificial abstraction.

### Source organization

All paths below are repository-relative. Package boundaries describe working owners, not empty abstractions.

```text
install.sh                         Minimal initial download and handoff
go.mod / go.sum                    Application dependencies
.golangci.yml                     Shared Go formatting and lint configuration
.golangci-lint-version            Shared pinned linter release
cmd/workbench/main.go              Single executable entry point
internal/cli/                      Thin Cobra handlers
internal/operation/                Shared plans, consent, state and checkpoints
internal/release/                  Bundle verification, staging and selection
internal/machine/                  Chezmoi context and provisioning coordination
internal/project/                  Discovery and project configuration
home/                             Existing canonical machine configuration
project/python/                   Portable configuration policy, not app scaffolding
scripts/                          Existing validation plus release tooling
docs/superpowers/specs/            Product specifications
docs/superpowers/plans/            Dependency-ordered implementation plans
.github/workflows/                 Validation and release automation
```

Package boundaries are organizational, not a requirement to create empty abstractions. Keep shared dependency resolution in the existing machine/release coordination until another real caller justifies extraction.

Use one Go module, a thin `cmd/workbench` entry point and focused `internal` packages, consistent with the [official Go module layout guidance](https://go.dev/doc/modules/layout). Create packages only when working code needs them. Do not introduce a public `pkg` tree, generic `utils` packages, repository/service layers, or interfaces solely to support mocks. Keep the few justified `_test.go` files beside their implementation and minimal file fixtures in that package's `testdata/`; do not create a standalone test framework or central fixture hierarchy. Prefer the standard library and reuse maintained dependencies for real gaps.

### Formatting, linting and minimal tests

Formatting, static analysis and builds are the routine quality gates. Pin one golangci-lint v2 release compatible with the selected Go toolchain. Use one `.golangci.yml` with `gofmt` and `goimports` formatters and the standard linter set; do not enable every available linter or introduce duplicate standalone analysis jobs. Apply formatting during implementation and check it without rewriting files in CI. The [golangci-lint command reference](https://golangci-lint.run/docs/configuration/cli/) distinguishes `fmt` for formatting from `run` for lint and formatting diagnostics. Keep editor and CI tooling aligned with that configuration.

Keep new automated tests close to none. Add a test only for a concrete catastrophic failure: irreversible user-data loss, credential exposure, or unauthorized code execution/system mutation. Examples include destructive apply/revert conflicts, escaping archive paths, and preview paths accidentally running privileged changes. Each test must identify the failure it prevents; do not label ordinary correctness concerns critical to justify expanding the suite.

No test-per-feature rule, coverage target, TDD mandate, UI/help snapshots, broad unit/integration matrix, or new end-to-end framework. Use code review, compilation, static checks and short recorded manual smoke checks for ordinary behavior. Reuse existing render/lint/leak checks without multiplying them into a new test system. Run necessary safety checks only in isolated destinations, never against a developer's live configuration. Preservation of tests in a user's project is separate from this policy for Workbench itself.

### One source per fact

- Package and extension names stay in `home/.chezmoidata/packages.toml`; pins stay in `home/.chezmoidata/versions.toml`.
- Extend those owners for necessary management-tool metadata; generate release metadata rather than manually maintaining duplicate version lists.
- Preserve `personal`, `work`, and `both` roles and `pinned`/`latest` modes. Secrets remain in private runtime configuration, not product source or operation output.
- Management dependency compatibility is a release requirement, distinct from the user's preferred runtime version policy. Never silently downgrade a borrowed tool to satisfy a private pin.
- Record whether a dependency is user-owned, package-manager-owned, or privately installed by Workbench. Coordinate scripts and the resolver so only one owner installs it. Existence on PATH alone is not a compatibility check.
- Project dependencies and lockfiles belong to the project. A project environment and a machine-global executable serve different purposes; neither may be silently substituted for the other.

## 4. Command contract

This table defines the command contract. Exact flags, evaluation-only release constraints and supported project cases are documented in [usage](../../usage.md).

| Command | Contract |
| --- | --- |
| `workbench doctor` | Local, read-only checks of context, tools, configuration and available editor targets. Report missing, unsupported, warning and failure states. Do not repair, install, start servers or contact remote hosts. |
| `workbench status` | Inspect recorded CLI/configuration/state identities without fetching or applying; complete drift inspection remains unavailable. |
| `workbench pull [version] --bundle FILE --sha256 HASH` | Acquire and verify an explicitly trusted release candidate; stage only. No published latest channel exists. |
| `workbench plan` | Preview the selected candidate, or the current selected source when no candidate exists. Show file changes, removals, prerequisites and planned external effects. |
| `workbench apply --dry-run` | Use the same planner and scope as `plan`; no application or provisioning. |
| `workbench apply` | Preflight, preview, confirm, checkpoint, execute approved operations, validate and record the result. |
| `workbench apply --config-only` | Exclude provisioning scripts and installs. Preview managed configuration removals as well as writes. Missing render prerequisites block rather than trigger installation. |
| `workbench install` / `workbench update` | Compose shared staging, journaled activation, separate setup and approved apply; require an explicit bundle/hash and `--evaluation` while production is gated. |
| `workbench revert [version]` | Preview configuration recovery using a retained operation checkpoint and its release. Never infer recoverable contents from a version number alone. |
| `workbench project inspect [PATH]` | Read-only discovery of languages, project boundaries, tool ownership, shared configuration and unsupported cases. PATH defaults to `.` and must exist. |
| `workbench project configure [PATH] [--language NAME ...]` | Preview and configure supported tooling in existing projects. Repeated language flags narrow selection; no flags means supported detected languages. |
| `workbench project configure [PATH] --dry-run` | Read-only configuration preview: no installs, dependency resolution, lockfile writes or execution of project code. |

Examples:

```sh
workbench project inspect .
workbench project configure ./apps/api --language python --dry-run
workbench project configure ./apps/api --language python
workbench project configure . --language python --language typescript --dry-run
```

The last example specifies intended selection syntax, not implemented TypeScript support. An explicitly requested unsupported language fails before writes. Discovery must report unsupported detected components prominently rather than silently claiming that the entire repository is configured.

Inspection and preview default to offline operation. Missing dependencies/answers produce an actionable result rather than repairs. Normal mutations require confirmation; noninteractive mutation requires complete private inputs and approval of the exact plan digest. Consent and output use the shared [contract](workbench-contracts.md#consent-and-output).

Success means the requested supported scope completed and passed its checks. Requested work that failed or was skipped cannot be reported as complete. Return nonzero for failures, unresolved conflicts and incomplete requested operations; preserve a detailed result distinguishing them. The same exit codes and JSON envelope cover inspection, planning, mutation and recovery.

## 5. Installation and release lifecycle

### Bootstrap

Publish one installer entry point for macOS, Linux and WSL after release acceptance passes. The shell detects the target, downloads/verifies a standalone executable and hands off; the same Go CLI owns archive validation/extraction and lifecycle operations. No manual repository clone or separately installed chezmoi is required. The repository is private; authenticated acquisition requires explicit credentials or operator-supplied local artifacts.

Document required bootstrap utilities and detect them before work. Do not promise operation on a system lacking the required shell/download/checksum utilities. The implemented utility set is documented in [usage](../../usage.md#evaluation-installation); clean native bootstrap qualification remains open. Read interactive answers from the terminal, not the pipe carrying the installer. Without a terminal, require explicit inputs and consent. Offer install-only behavior and a download-and-inspect alternative.

The installer may present a bootstrap-only dry-run before management tools exist; it must explicitly state when a full configuration preview is unavailable. It cannot claim to have rendered files it could not inspect. Do not change already-running terminals' environments or inject commands into them.

### Release payload and identity

Each supported OS/architecture bundle contains the compiled CLI, `.chezmoiroot`, `home/`, selected `project/` policies, required licenses and generated release metadata. Exclude Git history, maintainer-only material, generated host configuration, credentials, environments and recovery state.

Resolve a requested release once per operation. Verify version, target compatibility, integrity, expected layout and file types. Reject archive traversal, escaping links, absolute archive members and entries that could overwrite unrelated files. A checksum delivered by the same publisher detects corruption; it is not independent proof against publisher compromise. Signing/attestation policy remains a release decision.

Keep the working runtime and recoverable state intact until activation succeeds. Reinstallation and updates reuse the same CLI operations. The shell bootstrap's first download is the only necessary pre-CLI transport path; it uses the same generated metadata and verification contract, not another package list or updater.

### Runtime context

Resolve runtime locations from the user's supported platform conventions and applicable XDG overrides. Do not hardcode a username, checkout path or home directory. Maintain explicit locations for the entry point, immutable releases, privately owned tools, machine answers and private operation/checkpoint state.

One context resolver supplies the same source release, machine configuration, destination and native persistent state to every chezmoi call. Reuse an existing configuration only through explicit adoption that preserves answers/secrets. Do not maintain two competing active source selectors.

Archive-based setup reuses native configuration templating without cloning a repository or requiring external Git. Use `chezmoi --use-builtin-git=true init` in a verified private application context: native init creates empty local Git metadata, without a remote, commits or checkout download. This metadata is generated private state, never part of published assets. Initialization is an approved setup stage, never an implicit effect of pull, doctor or plan. The [native initialization proof](workbench-contracts.md#native-initialization) defines paths and source identity. Do not replace Git with a no-op command or duplicate the questions.

The CLI itself does not require a Python installation, but existing modify scripts can. Preflight must account for every rendering prerequisite, including Python with `tomllib`, before claiming clean-machine application works. Installing those prerequisites is an explicitly approved setup stage; previews and configuration-only apply must not install them implicitly.

An optional `--source PATH` developer override selects an existing source tree; ordinary release installation and project configuration work without one. Hash its inputs for every plan and never change the checkout during inspection. Paths and other overrides are defined in the implementation contracts.

Exact canonical machine source hashes are generated by `scripts/generate-source-trust.py` and embedded in the executable. Changed templates/data require regenerating and rebuilding; unknown source code cannot run during preview merely because its filename is familiar. Native chezmoi remains the renderer. A narrow private source selection handles role-owned removals that native ignore rules otherwise suppress.

Activation is journaled across the state record and entry-point symlink, not falsely described as a multi-file atomic switch. A crash mismatch blocks ordinary operations; explicitly resume the same installer with the trusted bundle and fresh consent. Previous runtime files remain available. Update uses a verified same-process executable handoff, not a permanent forwarding launcher.

### Apply state and reproducibility

Distinguish installed CLI version, candidate release and successfully applied configuration release. Record operations as planned, running, complete, partial or failed; do not label a partially configured machine as up to date.

Plans bind to a release identity, input configuration and the inspected target state. Recheck inputs before mutation and stop or re-plan after a material change. Package effects that cannot be simulated are displayed as intended operations, not fabricated exact results. In latest mode, disclose provider resolution and require renewed consent if it materially expands the approved scope.

Use native [chezmoi application](https://www.chezmoi.io/reference/commands/apply/) and differences for owned machine targets. Audit template evaluation, hooks and modify scripts: excluding provisioning scripts alone does not prove that rendering has no effects. Any temporary computation must not change managed targets or persistent setup state.

The same operation functions serve initial setup, later apply and update. Activate the CLI and sources as one release unit, retaining the working runtime until activation succeeds. Use the current Workbench state format only; reject unsupported state before writes instead of implementing schema conversion or historical-version compatibility. Configuration recovery uses supported checkpoints through the current implementation, not an older executable interpreting newer state.

## 6. Platform support and provisioning safety

| Required environment | Shared execution | Platform-specific responsibilities |
| --- | --- | --- |
| macOS, Apple Silicon and Intel | One CLI source, corresponding compiled artifact, existing chezmoi context | Homebrew discovery, native paths, application/extension handling, shell startup, permissions and manual terminal-font completion. |
| Linux/Ubuntu | Same CLI and command contract, existing Linux provisioning | Supported distribution/architecture detection, package-manager access, runtime installs, desktop availability and shell startup. |
| WSL with Windows integration | Linux artifact inside WSL and the same operations | Windows interoperability, path translation, Terminal/PowerShell configuration, fonts, VM preferences and editor-host boundaries. |

Specify tested minimum OS/WSL versions and CPU targets before release. Linux ARM coverage cannot be inferred from the CLI: native acquisition must select the matching architecture, and Windows integration remains x64-only until qualified. Reject unsupported combinations before target writes. Native Windows without WSL and arbitrary Linux distributions are separate expansions.

Maintain one feature/platform checklist derived from current source owners, covering shell and Git settings, AI-tool configuration, editor settings, themes/fonts, tmux, runtime managers/runtimes/global tools, role-dependent work integration, Windows settings and system tuning. A single passing binary build is not proof of this coverage.

### Provisioning invariants

- Keep the existing provisioning implementation as the owner. Fix its safety/reporting at that owner; do not create competing Go and shell installers for the same component.
- Automatic Homebrew cleanup, tap removal, package replacement and deletion of recovery copies must not run during ordinary update without separately disclosed approval. Preserve recoverable copies when restoration fails.
- Best-effort scripts must surface structured or otherwise reliably testable outcomes. Exit zero plus a warning is not evidence that requested setup completed.
- Existing package data includes chezmoi/uv while bootstrap coordination may also require them. Resolve one installation owner before either path runs; never install duplicate global/private copies by default.
- Preserve the declared platform-specific pin behavior until deliberately changed. Some macOS tools are Homebrew-managed rather than installed at the versions recorded for Linux. Report this honestly.
- Review role-driven removals and `.chezmoiremove` as destructive target operations with previews and checkpoints.
- Validate machine answers and preserve correct quoting when rendering TOML, shell and PowerShell. Invalid existing configuration must stop for review rather than silently discard unrelated application-owned state.
- WSL scripts modify files, user environment/registry state, default distribution and system settings. Enumerate effects before approval. File recovery does not undo those other effects. Do not restart services or WSL merely to inspect/configure a project.
- Windows Terminal/PowerShell writes require explicit ownership/adoption and preservation. Native Windows path/ACL qualification is still a blocking gate for full integration.
- The full personal VS Code settings merge currently targets macOS and Linux. WSL scripts separately adjust selected Windows/remote editor keys. Do not describe this as full Windows-hosted settings deployment.

Native package-manager calls may need elevation, network access or executable build hooks. Disclose these operations and request only necessary privilege. Never run the entire bootstrap as root. Missing privilege is an incomplete/blocked operation, not permission to skip silently.

## 7. Recovery, privacy and bounded operation

Before modifying configuration, record the exact affected paths, previous existence, content, object type, permissions, group and link targets. Record post-application images and the owning scope afterward. Include supported script-written configuration targets explicitly; unsupported external effects stay separately reported.

Revert uses recorded checkpoints, not reverse-running old scripts. Preflight all recovery targets against recorded post-images. If the user changed a target afterward, stop and show a conflict; do not silently overwrite their work. Remove a newly created file only when its prior absence and unchanged post-image are established. Never recursively delete a broad directory to implement recovery.

An additive settings merge does not remove newly introduced keys merely because an older release is selected. Recovery must restore a suitable pre-image or require a reviewed merge. Do not build a generic per-key version-control system.

Reverting does not promise to uninstall packages/extensions, reverse a runtime upgrade, restore services/registry state or undo arbitrary cleanup. Keep these limits in plan, result and recovery output. Ambiguous version-to-checkpoint selection fails instead of choosing silently. The [checkpoint contract](workbench-contracts.md#checkpoints-and-current-state) defines selection and retention. Its forward-operation ceiling must not block recovery: each forward checkpoint reserves a paired recovery checkpoint and bounded journal capacity before writes, reused for restore, undo and interrupted retries without deleting checkpoints. Implemented conservative limits are 8 MiB per image, 32 MiB raw images/256 targets per operation, 50 MiB serialized per pair and 1 GiB per scope; native capacity qualification remains a release gate.

Machine and project checkpoints share implementation but have distinct scopes. Machine revert must never restore project files. `project revert [PATH] --checkpoint ID` uses the same recovery owner within the selected project scope.

Protect answers, logs and snapshots with private permissions appropriate to their filesystem. Do not persist secret values in public manifests, diagnostics or project provenance. Redact known sensitive fields and avoid logging subprocess arguments/environment wholesale. Do not send telemetry or start resident services by default.

Serialize overlapping mutations using locks with clear ownership. Coordinate shared Workbench release/state operations and project scopes so different commands cannot race on the same files. Detect changed inputs even when another application does not honor the lock. Recover from interruption with explicit partial state, not an assumed transaction over external package managers.

Use bounded archive extraction, downloads, subprocess output, caches and retained checkpoints. Stop when a safe space/size constraint cannot be satisfied; never silently prune active recovery material. Conservative engineering limits are explicit in the contracts; their existence is not a measured performance guarantee. No continuous workspace watcher, repeated whole-tree polling or duplicate language-server process is part of Workbench.

## 8. Personal editor and Python policy

### Global editor ownership

All portable personal VS Code preferences belong in `home/.chezmoidata/vscode.json` under `vscode.settings`, applied through the existing shared merge. Language-scoped preferences live there too; Python does not own a separate global settings file.

Preserve the existing Dark 2026 theme and managed TypeScript/TSX property rules: ordinary properties use `#9CDCFE`, readonly properties use `#79C0FF`. Preserve the current named-rule identities from the canonical data so reapplication replaces only owned rules and retains custom ones. These colors identify symbol roles, not whether a name resolves.

The canonical global data contains these personal preferences; applying them must not replace unrelated live settings:

```json
{
  "window.zoomLevel": 0.5,
  "claudeCode.preferredLocation": "panel",
  "editor.unicodeHighlight.allowedCharacters": { "§": true },
  "explorer.confirmDelete": false,
  "explorer.confirmDragAndDrop": false,
  "git.confirmSync": false,
  "extensions.ignoreRecommendations": false,
  "[json]": { "editor.defaultFormatter": "esbenp.prettier-vscode" },
  "python.languageServer": "None",
  "ty.disableLanguageServices": false,
  "ty.diagnosticMode": "openFilesOnly",
  "ty.importStrategy": "fromEnvironment",
  "ruff.nativeServer": "on",
  "ruff.importStrategy": "fromEnvironment",
  "ruff.configurationPreference": "filesystemFirst",
  "[python]": {
    "editor.defaultFormatter": "charliermarsh.ruff",
    "editor.formatOnSave": true,
    "editor.codeActionsOnSave": {
      "source.fixAll.ruff": "explicit",
      "source.organizeImports.ruff": "explicit"
    }
  }
}
```

These are product preferences, not claims that disabling deletion/sync confirmations is safer. Do not enable unsafe Ruff fixes. Preserve unowned nested settings. Validate keys and enums against the release's extension schemas and the [ty editor settings](https://docs.astral.sh/ty/reference/editor-settings/) and [Ruff editor settings](https://docs.astral.sh/ruff/editors/settings/) references.

Keep host mappings, temporary extension paths and credentials out of portable data. Inventory VS Code profile, local/remote host and Settings Sync ownership before applying. A desktop CLI, Windows shim and remote extension-host CLI are not interchangeable. Do not open remote sessions to perform discovery.

TypeScript service selection, imported-name colors, and changes to Todo Tree configuration remain outside this specification's new editor policy. Preserve their existing values and current platform behavior; do not turn those deferred choices into global overrides. Preserve other unowned remote-networking and schema-detection preferences.

### Checker responsibilities

- ty owns Python editor language features and open-file diagnostics.
- Native Ruff owns linting, formatting and import organization.
- basedpyright is a command-line/CI verifier, not a second always-running editor server.
- Whole-project ty checks remain available on demand. No benchmark-based RAM/CPU advantage is asserted here; validate responsiveness and resource use on representative repositories.
- Editor environment selection prefers project tools with supported bundled fallbacks. Verify actual resolution; installing a global executable alone does not configure every AI client or editor.

Update the canonical extension list by adding `astral-sh.ty`, keeping `charliermarsh.ruff`, `ms-python.python`, `ms-python.debugpy`, `ms-python.vscode-python-envs` and existing notebook support, and removing Pylance/mypy from desired installation. Do not add the basedpyright/Pyright extensions or the Python Ruff server to this default stack.

Removing a desired-list entry does not uninstall an extension. Inspect the selected profile and obtain consent for targeted conflicting-extension removal/disablement; never delete extension directories or uninstall every unlisted extension. Report extension changes as external effects outside configuration-only revert.

Extend the existing `[uv_tools]` table without removing unrelated tools. Verified selected pins are ty `0.0.82`, Ruff `0.16.8` and basedpyright `1.40.1`. They are not assertions of latest versions or mandatory project downgrades. No Copier dependency is required.

### Machine fallback policy

Implemented `home/dot_config/ty/ty.toml` contents:

```toml
[rules]
dynamic-function-decorator-return = "error"
missing-type-argument = "error"
possibly-unresolved-reference = "warn"
unsound-return-statement = "error"
```

Implemented `home/dot_config/ruff/pyproject.toml` contents (Ruff's native user fallback filename):

```toml
[tool.ruff]
unsafe-fixes = false

[tool.ruff.lint]
extend-select = ["I"]
```

These are fallbacks, not an override of project policy. Confirm native discovery on every supported host/XDG configuration before claiming these source targets are sufficient. Reuse native configuration precedence rather than injecting settings through command wrappers.

## 9. Project configuration and monorepos

### Discovery and scope

`project inspect` performs one bounded scan using native manifests and workspace definitions, honoring exclusions for dependencies, caches and generated output. Recognize project identity from manifests such as `pyproject.toml`, `package.json`, workspace manifests, `Cargo.toml` and `go.mod`; a file extension alone does not authorize configuration.

Resolve selected directory, project boundaries, package manager, supported language policies, configuration owners and lockfile ownership before constructing edits. Reuse this same discovery result for configure. Do not execute repository scripts, plugin code or package-manager installs to discover metadata.

A monorepo is not one language or necessarily one package manager. Compose one change plan for all selected supported projects. Deduplicate shared root configuration, editor recommendations and CI edits. Conflicting proposed values stop the operation for a decision; language handlers cannot apply independently and overwrite each other.

A subdirectory selection must not silently authorize parent/sibling writes. Show required shared-root changes and ask the user to select an appropriate broader scope. Normalize paths safely, do not traverse escaping symlinks, and report ambiguous nested repositories/workspaces instead of guessing ownership.

Implement existing uv-managed Python projects first. Manually verify a representative mixed-language workspace with non-Python packages left intact; automate only a qualifying destructive-scope safeguard. Detecting TypeScript/Rust/Go does not imply that configuration for those languages is implemented. Poetry, PDM, requirements-only layouts and additional language policies require their own explicit support decisions and verification.

### Reusable Python assets

`project/python/policy.toml` contains tool settings only, never complete application metadata:

```toml
[tool.ty.rules]
dynamic-function-decorator-return = "error"
missing-type-argument = "error"
possibly-unresolved-reference = "warn"
unsound-return-statement = "error"

[tool.ruff]
line-length = 88
unsafe-fixes = false

[tool.ruff.lint]
extend-select = ["I", "B", "UP", "ANN", "PYI", "RUF100"]

[tool.basedpyright]
typeCheckingMode = "recommended"
failOnWarnings = true
reportMatchNotExhaustive = "error"
```

Validate native configuration acceptance, including [basedpyright configuration](https://docs.basedpyright.com/latest/configuration/config-files/), before release. Inspect separately located tool configs and project-specific rule choices; do not blindly append tables or erase existing policies. No fixed source/test layout, Python baseline, package name, build backend or test framework is introduced.

`project/python/extensions.json` supplies these optional recommendations:

```json
{
  "recommendations": [
    "astral-sh.ty", "charliermarsh.ruff", "ms-python.python", "ms-python.debugpy"
  ],
  "unwantedRecommendations": [
    "ms-python.vscode-pylance", "matangover.mypy", "detachhead.basedpyright"
  ]
}
```

Merge unique recommendations after review and resolve contradictions. Recommendations do not install or disable extensions. Personal theme, zoom and machine-specific settings never enter this file or the project.

Optional `project/python/gitignore.entries` covers `.venv/`, `__pycache__/`, `*.py[cod]`, `.pytest_cache/`, `.ruff_cache/`, `dist/`, `*.egg-info/`, `.coverage`, `htmlcov/`, `.env`, `.env.*` and the `!.env.example` exception. Preserve existing pattern order and negations; add only relevant absent entries, without replacing the file.

### Dependencies, checks and CI

Use the project's native dependency operations for compatible development-tool versions. Preserve its Python constraints, environment, build system, dependency groups and lockfile. Missing project metadata is a request for user direction, not permission to run project initialization. Unsupported managers receive guidance rather than conversion.

For an approved uv project, the checks are:

```sh
uv run --locked ruff check .
uv run --locked ruff format --check .
uv run --locked basedpyright
```

Run the project's existing tests; use `uv run --locked pytest` only when pytest is its chosen runner. `uv run --locked ty check` is an optional whole-project check, not a second required CI gate. Checks do not fix files; deliberate formatting/fix commands remain separate and require review of their changes.

An optional `project/python/checks.example.yml` must demonstrate read-only repository permissions, checkout without persisted credentials, uv setup, locked development-dependency synchronization, the three checks above and the real test command. Set a bounded job timeout and cancel superseded checks. Select a supported Python version from the project, validate pinned action/tool references before publication, and adapt an existing workflow instead of creating a duplicate pipeline. Do not alter branch protection or change CI providers automatically.

Project configuration may approve package resolution/environment changes, but these must be listed as external effects and never run during dry-run. Record a portable Workbench policy/release identity without embedding installation paths, using the `[tool.workbench]` format in the implementation contracts. Never make project execution depend on Workbench being installed.

## 10. Acceptance criteria

These are behavioral acceptance requirements, not a mandate for an automated test per row. Use the minimal-test policy in section 3: static checks and brief manual evidence by default, narrowly scoped automated tests only for catastrophic failures.

| Area | Required evidence |
| --- | --- |
| Distribution | A clean supported environment installs from a release with no checkout or manual management-tool setup; failure leaves the previous installation usable. |
| Single ownership | Installer/reinstall/update share operations and canonical metadata; each dependency/configuration has one owner; no per-platform or per-language lifecycle copies. |
| Read-only behavior | Doctor/status/inspect and previews do not install, repair, execute project code, modify targets or resolve project dependencies. Missing prerequisites are reported. |
| Application | Preview and execution bind to the same release/scope/input state; repeated apply/configure converges; changed inputs conflict; failed/skipped requested work is not called success. |
| Recovery | A minimal destructive-change regression check protects user data and later edits; manual review/checks verify remaining checkpoint behavior and explicit external-effect limits. |
| Platforms | Record a short native release smoke check on declared macOS architectures, supported Ubuntu targets and real WSL/Windows integration; do not build a full automated lifecycle matrix. Existing role/mode rendering remains available. |
| Editor | One global source and merge; unrelated settings/custom rules survive; correct profile/host is targeted; Python has one type-language server plus native Ruff. |
| Projects | Existing metadata/source/tests survive; supported mixed-language workspaces use one deduplicated plan; unsupported scopes fail safely; no application scaffolding appears. |
| Privacy/resources | Bundles exclude host data, output is redacted, recovery storage is private/bounded, scans/processes are bounded, and no background service is required. |
| Go quality | One module and focused internal packages; formatting, standard lint and builds pass; any automated test has an explicit catastrophic-risk justification. |
| Greenfield | Canonical implementation and callers are updated in place; no backward-compatibility layers, migration machinery or parallel obsolete paths. |
| Documentation | Product instructions describe only verified support; examples distinguish available commands from proposals; decisions and verification instructions are maintained in the repository. |

Spot-check CLI startup, workspace inspection and resource use on representative workspaces. Record the environment and observations; do not create a benchmark framework or fabricate comparative performance numbers. Set budgets only if measurement establishes a need.

## 11. Implementation contracts and remaining gates

The maintained [implementation contracts](workbench-contracts.md) select native initialization, management ownership/prerequisites, runtime paths, unattended consent, output, checkpoint selection, project editors and initial platform acceptance targets. They also inventory each current file/provisioning effect and its recovery boundary. These remain binding implementation requirements; the acceptance record distinguishes isolated checks from native release qualification.

Production release remains blocked on native platform smoke evidence, publication/license authority, approved publisher trust/signature policy, and qualification of the conservative archive/checkpoint bounds. Optional maintenance and application replacement remain disabled unless separately selected and approved. Additional languages/managers, native Windows, automatic maintenance and performance/resource budgets remain unresolved; do not silently expand support. See the [implementation plan](../plans/workbench-implementation.md) for work order.
