# Go and Rust project policy and the mbx step

Status: design, Nov 17, 2026. All four steps (section 8) are built: the Go and
Rust policies, `--hooks` and `--dependabot`, and the mbx machine step
(section 5). It adds two languages to `workbench project apply`, two new flags
for every language (`--hooks`, `--dependabot`), and one new machine step that
installs mbx, a shared Rust compile cache and scheduler.

The work ships in four steps (section 8). Each step can be tried on a real
project before the next starts.

"Today" means code read in this tree. "Proposed" means this design.
"Unverified" means nobody checked it in this session, and the build step that
depends on it must check it first.

## 1. What exists today

- **Discovery** (`internal/project/inspect.go`, `discovery.go`) already finds
  `go.mod`, `go.work`, `Cargo.toml` and `Cargo.lock`. It reads them without
  running anything. Each `go.mod` and each `Cargo.toml` becomes its own
  project. A Go module is configurable as section 3.1 says, and a Cargo
  package or workspace root as section 4.1 says; a workspace member is linked
  to its root.
- **Planning** (`plan.go`) refuses any `--language` other than `python`,
  `go` and `rust`. Python settings go into `pyproject.toml` through the TOML Kit adapter
  (`adapter.py`). That adapter **adds missing settings and keeps every existing
  value and rule list**. It also records `[tool.workbench]` with
  `schema_version = 1` and `policies = ["python-v1"]`. Before it returns a
  document it parses the result and requires every existing value at its own
  key and only the requested additions new; a table added among dotted keys
  gets a `[header]` of its own, and a key that would go into an inline table
  is refused. A refusal blocks the plan with a prerequisite naming the file,
  for `Cargo.toml` as for `pyproject.toml`.
- **Python stops only for competing owners**: a standalone `ruff.toml`, `ty.toml`,
  `pyrightconfig.json` or `uv.toml`, a `[tool.pyright]` table, or `tool.ruff.extend`.
  In each case a merged setting would be ignored or refused by the tool.
  Python merges, and stops only where merging would be dead or rejected.
- **`--extensions`** merges `.vscode/extensions.json` through HuJSON (`editor.go`).
  **`--gitignore`** appends entries, and stops on any negation line it does not
  know.
- **`--ci`** (`ci.go`) needs all of these:
  - exactly one file in `.github/workflows/`;
  - a job named `test` or `tests` that runs pytest, unittest, tox or nox;
  - a `.python-version` file;
  - a workflow that round-trips byte for byte through goccy/go-yaml, with no
    anchors, aliases, merge keys or tags.

  It then adds one owned job, `workbench-python`. Its action pins come from
  `home/.chezmoidata/versions.toml` `[github_actions]`, which already pins
  `actions/checkout`, `actions/setup-go` and `golangci/golangci-lint-action`.
- **Apply and revert** (`apply.go`) write every change under one project
  checkpoint. `project revert` restores the pre-images, and a file that was
  absent before is restored as absent.
- **The plan's source identity** is the selected policy IDs joined by `_`
  (`go-v1`, `python-v1`, or `go-v1_python-v1`), plus a digest of their
  embedded assets. A plan with one policy keeps that policy's ID and digest.

## 2. Rules shared by both languages

### 2.1 The rule for existing configuration

There are two kinds of file, and each gets one rule:

1. **Settings in a shared manifest** (`Cargo.toml`, `pyproject.toml`): add
   what is missing and keep every existing value. This is Python's rule,
   run by the same adapter. An existing rule selection is the project's own
   and gets no rule: Python leaves out the Ruff selection when the project
   has one, and Rust adds no lint to an existing `[lints]` or
   `[workspace.lints]` table (section 4.2). A project that sets
   `unsafe_code = "forbid"` keeps `forbid`, and one with
   `clippy::all = "deny"` keeps `deny` with nothing added beside it.
2. **Whole files a tool reads on its own**:
   - `.golangci.yml`
   - `lefthook.yml` (any lefthook main configuration counts, section 6.1)
   - `deny.toml`
   - `rustfmt.toml`
   - `.mbx.toml`
   - `.github/dependabot.yml`

   Workbench creates one only when it is absent. **An existing one is kept
   byte for byte.** The plan lists it as "kept: the project's own", and the
   rest of the plan goes ahead.

Why not stop the whole run? Stopping would make `project apply` useless on
most configured projects: a project with its own `.golangci.yml` could not get
hooks or Dependabot either.

Why not merge into these files? Each one is the project's whole policy for
its tool. Adding linters to an existing `linters.enable` list is exactly the
"extend an existing rule selection" that Python's adapter refuses (`adapter.py`:
"never overwrite or extend an existing rule selection implicitly"). Merging
would also need YAML or TOML edits to files the project wrote by hand, with
nothing gained: a configured project has already made these choices.

**The run stops only for these**, each named in the plan:

- invalid syntax;
- an editor that cannot round-trip the file;
- a competing owner that would make a written setting dead (listed per
  language below);
- selecting a workspace member instead of its root;
- a scope or ownership problem that Python already refuses.

### 2.2 What `--language` selects

With no `--language`, every supported language found is planned. Today
`--language` only narrows, and that stays true. A repository with Rust and
TypeScript plans Rust and warns that TypeScript was left untouched. This
already happens for Python.

### 2.3 Repository-level flags

`--hooks`, `--dependabot` and `--ci` write files that Git or GitHub read from
the repository root. They need the selected folder to be the repository root
(the folder that holds `.git`), and `--hooks` and `--dependabot` also need
every selected project's owner there, since the jobs run at the root and
Dependabot reads `directory: /`. Otherwise the plan is blocked with that
reason.
When several languages are selected:

- `lefthook.yml` gets one job per language and check, named `go-lint`,
  `rust-format` and so on;
- `dependabot.yml` gets one entry per ecosystem;
- the workflow gets one owned job per language.

### 2.4 Record of what was applied

| Option | Verdict |
| --- | --- |
| Native metadata slot in the manifest | **Use where one exists.** Python keeps `[tool.workbench]`; the no-migration rule in AGENTS.md is reason enough, and `[tool.*]` is the slot PEP 518 reserves for tools. Rust uses Cargo's reserved `metadata` table: `[workspace.metadata.workbench]` in a workspace root, `[package.metadata.workbench]` in a single crate (a `[workspace.*]` table in a single crate would turn it into a workspace root). |
| `.workbench.toml` at the project root | **Use for Go.** `go.mod` has no slot for tools. One small file, written whole by Workbench, so it needs no TOML Kit. |
| Header comment in `.golangci.yml` | Rejected. The comment would exist only when Workbench created that file. It is not data, so `inspect` could not read it reliably. |
| Nothing (work it out from the files) | Rejected. A file the project wrote by hand looks the same as one Workbench wrote. |

All three records hold the same two keys: `schema_version = 1` and
`policies = [...]`. Policy IDs never change: the ID is added when absent and
left alone when present. Python stays `python-v1`.

The Go record:

```toml
# Written by workbench project apply: the Workbench project policies this
# repository follows. Builds and tests never read it.
schema_version = 1
policies = ["go-v1"]
```

An existing `.workbench.toml` is parsed with go-toml. If its schema version is
not the integer 1, or it holds any key besides `schema_version` and
`policies`, the run stops, the same rule `adapter.py` applies; rewriting the
whole file would otherwise drop that data. A record that already lists the ID
is left byte for byte. Otherwise Workbench writes it again with the ID added.

### 2.5 Strictness

The owner chose warnings locally and errors in CI. Here is what that means for
each language:

- **Go has no warning level.** `golangci-lint run` exits non-zero on any
  issue. So Go is strict everywhere: in the editor, in the hook and in CI.
- **Rust:** the lints are `"warn"` in `Cargo.toml`. The editor and plain
  `cargo clippy` show them as warnings, and clippy then exits 0. CI adds
  `-D warnings`.
- **Does a warning block a commit?** Only if the hook adds `-D warnings`.
  Without it, the Rust lint job never blocks: the commit goes through and CI
  fails later. With it, the hook matches CI, and "warnings locally" means
  while editing. The hook adds `-D warnings` (decision D3).

### 2.6 External effects and consent

Go and Rust apply **write files only**. They do not run `go mod download`,
`go list`, `cargo fetch` or `cargo metadata`, and they need no
`--resolve-dependencies` flag.

Discovery must not run `cargo metadata` in particular. A `rust-toolchain.toml`
can make rustup download a whole toolchain before Cargo even starts. Workspace
membership and the edition are read from the TOML text only (section 4.1).

Rust needs the management Python and TOML Kit to edit `Cargo.toml`, the same
prerequisite Python preview has today. Go needs neither.

The plan lists two effects, worded like the existing `ci-workflow-checks`
effect:

- **`ci-workflow-checks`:** a future CI run builds and runs project code.
- **`git-hook-checks`, new with `--hooks`:** once `lefthook install` has run,
  every commit and push runs the listed commands. They compile project code
  and run build scripts, procedural macros and tests.

Apply does not run `lefthook install`. That command writes into `.git/hooks`,
outside the checkpoint, and every new clone needs it anyway. The result line
names the command.

### 2.7 Revert

There is no new mechanism. Each written file is a `TargetChange` in the one
checkpoint:

- new files, including `.github/` or `.vscode/` folders created for them;
- edited `Cargo.toml` files, member manifests included;
- the gitignore;
- the workflow.

`project revert` restores all of them, and a file created by apply is removed
again. A file edited after apply is a recovery conflict, as it is today.

## 3. Go (`--language go`, policy `go-v1`)

### 3.1 Detection

| Layout | Plan |
| --- | --- |
| `go.mod` at the selected root, no `go.work` | Supported: one module. |
| `go.work` at or above the root | Blocked: "Go workspaces are discovery-only in go-v1". `./...` does not cross modules, so the checks would cover only part of the code. |
| Further `go.mod` files below the root | Reported as untouched. `./...` already stops at them. |
| No `go.mod` at the root, but one below | Blocked: "select the module folder". |

`inspect` reports the module with manager `go`, owner equal to its folder,
and `supported` true or false with the reason. `go.work` is looked for up to
the repository root, like every other ownership walk.

The blocks above stop a plan only when Go is requested with `--language go`
or nothing else is selected. A uv project at the root with a Go module or a
`go.work` below it still plans, and the Go module is named in a warning: a Go
module a plan does not configure never stops another language's plan. Rust
follows the same rule.

### 3.2 Files

| File | Rule | Content |
| --- | --- | --- |
| `.golangci.yml` | Whole file; created when no golangci-lint config exists. Any of `.golangci.yml`, `.yaml`, `.toml` or `.json`, at the module or in a parent folder up to the repository root, counts as existing: golangci-lint 2.15.0 searches the folder it runs in and then each parent (`golangci-lint run -v` lists the search paths and used `../.golangci.yml`). | Below. |
| Format settings | Inside `.golangci.yml` (`formatters`) | gofumpt |
| Toolchain pin | Left alone | `go.mod` owns `go` and `toolchain` |
| Supply-chain settings | None | govulncheck needs no file |
| Build settings | None | Go's own build cache |
| `.workbench.toml` | Section 2.4 | Record |

An existing config without `version: "2"` is a golangci-lint v1 file, which v2
refuses. It is kept, and the plan says "golangci-lint v2 refuses this v1
config; run `golangci-lint migrate`". The number `version: 2` counts as v2:
`golangci-lint run` 2.15.0 accepts it, though `config verify` rejects it as
"got number, want string". An existing config that does not parse stops the
run (invalid syntax, section 2.1).

```yaml
# Written by workbench project apply (go-v1). The project owns this file
# from now on; Workbench never edits an existing one.
version: "2"
linters:
  default: standard
  enable:
    - errorlint
    - modernize
    - revive
    - unparam
formatters:
  enable:
    - gofumpt
```

`default: standard` is golangci-lint's own default set: errcheck, govet,
ineffassign, staticcheck and unused (`golangci-lint help linters`, 2.15.0). That set
holds the chosen govet, staticcheck and errcheck, plus two cheap extras that
leaving them out would need `default: none` to drop. The four linters added to
it complete the chosen seven.

### 3.3 Optional files

**`--extensions`** adds `golang.go` to `recommendations` through the same
HuJSON merge as Python. **`--gitignore`** appends `/bin/`, `*.test` and
`/coverage.out`, skipping a line that is already there. `--hooks`,
`--dependabot` and `--ci` are in section 6.

When the owner's `.gitignore` (as the plan leaves it) has a `.vscode/` or
`.vscode/*` line and no `!.vscode/extensions.json`, `--extensions` (in every
language) still writes the file but warns that Git ignores it. Workbench does
not evaluate ignore rules beyond that literal check. `--gitignore` accepts
`!.vscode/extensions.json` beside `!.env.example` as a negation that no
appended entry can change, so following that warning never blocks it.

When several languages share an owner, `--extensions` and `--gitignore`
combine their entries into one edit of each file.

## 4. Rust (`--language rust`, policy `rust-v1`)

### 4.1 Detection, from the TOML text only

| Layout | Plan |
| --- | --- |
| Root `Cargo.toml` with `[package]` and no `[workspace]` | Single crate. Lints go in `[lints.rust]` and `[lints.clippy]`; the record goes in `[package.metadata.workbench]`. |
| Root `Cargo.toml` with `[workspace]`, virtual or not | Workspace. Lints go in `[workspace.lints.*]`; the record goes in `[workspace.metadata.workbench]`. |
| A `Cargo.toml` that the root's `members` patterns list | Member, owned by the root. |
| A path dependency of a member (normal, dev or build, also per target, or a `workspace = true` entry resolved through `[workspace.dependencies]`) inside the workspace folder | Member too, transitively: Cargo makes these members without listing them. A `[workspace.dependencies]` path entry that no member uses, and a `[patch]` path, make no member (`cargo metadata` on scratch workspaces). |
| A package inside a workspace, selected alone | Blocked with "select the workspace root", as for uv workspaces: Cargo builds it only as that workspace's member. |
| A member, listed or reached as a path dependency, whose `Cargo.toml` declares a `[workspace]` of its own | Blocked for review: `cargo metadata` exits 101 with "multiple workspace roots found in the same workspace". |
| A `Cargo.toml` below the root that no rule covers, such as a separate fuzz workspace or an excluded crate | Reported as untouched. |

Membership reuses `workspaceMember` in `discovery.go` for the `members`
patterns, cleaned first because Cargo accepts `./a/` for `a`, and that
function already refuses `**` and `..` patterns for review. Cargo reads an
`exclude` entry as a path prefix of whole folder names, not a glob, and it
applies to a path dependency as to a listed member. Only a `members` entry
read as a plain path, one naming the package's folder or a folder above it,
keeps a package a member under an excluded folder; a package that only a glob
such as `crates/*` lists stays excluded (`cargo metadata` on scratch
workspaces). Workbench does the same in each test of membership, so an
excluded crate is never given `[lints] workspace = true`, which Cargo would
refuse for lack of a workspace, and selecting its folder plans it alone.

One gap: discovery skips hidden folders and `vendor`, `target`, `build`,
`dist` and the other generated folders. A `members` pattern, or a member's
path dependency, that names one of these folders is blocked for review,
instead of silently missing a member. The members of a blocked root are not
configured either.

Other languages in the same repository (TypeScript, Python without `uv.lock`)
stay untouched, with the existing warning.

### 4.2 Files

| Setting | Where | Rule |
| --- | --- | --- |
| Lints | `[workspace.lints.rust]` and `[workspace.lints.clippy]`, or `[lints.*]` in a single crate | Only when the manifest has no lint table there; an existing one is kept whole |
| Member opt-in | `[lints]` with `workspace = true` in each member's `Cargo.toml` | Only when Workbench creates `[workspace.lints]` |
| Dev profile | `[profile.dev] debug = "line-tables-only"` in the root manifest | Missing key only |
| Format | `rustfmt.toml` | Whole file |
| Toolchain pin | `rust-toolchain.toml` | Left alone and reported |
| Supply chain | `deny.toml` | Whole file |
| Build cache | `.mbx.toml` | Whole file |
| Record | Section 2.4 | Adapter |

**Lint settings** merged through `adapter.py`, only into a manifest without
`[workspace.lints]` (or `[lints]` in a single crate). An existing lint table
is the project's own selection, like an existing Ruff selection, and gets no
key. Adding one beside a group breaks the build: on a scratch workspace with
`[workspace.lints.clippy] all = "deny"`, adding `unwrap_used = "warn"` made
`cargo clippy` 1.99.0 fail with "lint group `all` has the same priority (0)
as a lint" (`clippy::lint_groups_priority`, deny by default), exit 101.

```toml
[workspace.lints.rust]
unsafe_code = "deny"
unreachable_pub = "warn"

[workspace.lints.clippy]
unwrap_used = "warn"
panic = "warn"
todo = "warn"
unimplemented = "warn"
```

**Member opt-in.** Workspace lints do nothing for a member whose `Cargo.toml`
does not opt in. The rule:

- **When Workbench creates `[workspace.lints]`**, it adds `[lints]` with
  `workspace = true` to each member that has no `[lints]` table.
- **A member with a `[lints]` table of its own** is left alone and named in
  the plan.
- **When the project already has `[workspace.lints]`**, members are never
  edited. Each member that does not opt in is named in a warning.

Opting such a member in could break that project's `-D warnings` CI with
lints it chose not to apply.

**Toolchain report.** The plan warns when neither `rust-toolchain.toml` nor
`rust-toolchain` is at the root or above it in the repository, or when the
file installs neither both `rustfmt` and `clippy` in `components` nor the
`default` or `complete` profile. It never writes this file.

**Competing owners that stop the run:**

- both `rustfmt.toml` and `.rustfmt.toml`, or two of `deny.toml`,
  `.deny.toml` and `.cargo/deny.toml`, in one folder (the tool would read
  only one of each; cargo-deny 0.20.2 used `deny.toml` over `.deny.toml`);
- a `[lints]` or `[workspace.lints]` table that is not a table of lint
  groups, such as a single crate's `[lints] workspace = true`.

rustfmt and cargo-deny read their file from the folder they run in and each
parent: on a scratch crate, a `rustfmt.toml` or `.rustfmt.toml` in the parent
folder changed `cargo fmt --check`, and a broken `deny.toml` in the parent,
or `.deny.toml` or `.cargo/deny.toml` beside the manifest, was reported by
`cargo deny check`. So a file at the root or above it inside the repository
counts as existing, as for golangci-lint.

**`rustfmt.toml`**, created only when neither `rustfmt.toml` nor
`.rustfmt.toml` exists at the root or above it. The edition comes from `package.edition` or
`workspace.package.edition`. When neither is set, the file is not written and
the plan says why.

```toml
# Written by workbench project apply (rust-v1). Bare rustfmt reads edition
# 2015 unless told, so cargo fmt and the editor agree only with these.
edition = "2024"
style_edition = "2024"
```

**`deny.toml`**, created only when no cargo-deny config exists:

```toml
# Written by workbench project apply (rust-v1). Licence checks are left to
# the project: run `cargo deny check advisories bans sources`.
[advisories]
yanked = "deny"

[bans]
wildcards = "deny"
allow-wildcard-paths = true

[sources]
unknown-registry = "deny"
unknown-git = "deny"
```

This file has no `[licenses]` table. In that case cargo-deny 0.20.2 rejects
every licence: a test crate with `license = "MIT"` failed
`cargo deny --offline check licenses` with "license is not explicitly
allowed", exit 4. So every command Workbench writes names the three checks.
None of them runs bare `cargo deny check`.

**`.mbx.toml`**, created only when absent:

```toml
# Written by workbench project apply (rust-v1). mbx reuses a crate compiled
# in one checkout in every other checkout with the same sources. The
# machine's compile budget lives in mbx's own config file, not here.
share_workspace_root = true
```

`share_workspace_root` is one of the settings mbx accepts in a tracked
`.mbx.toml`; its configuration docs list these under "Workspace policy". The
file does not hold reserved cores. mbx reads the environment
first, then `.mbx.toml`, then the user's `mbx/config.toml`. A tracked
`scheduler.reserve_cpus` would therefore override every machine's own setting.
Cores and memory are both machine facts, so both belong to the machine step
(section 5.4).

### 4.3 Optional files

**`--extensions`** adds `rust-lang.rust-analyzer`. **`--gitignore`** appends
`target/`. `--hooks`, `--dependabot` and `--ci` are in section 6; `--ci`
requires an existing `rust-toolchain.toml` (or `rust-toolchain`), just as
Python requires `.python-version`, so Workbench never picks a toolchain.

## 5. The mbx machine step (`workbench apply`)

### 5.1 Facts found in this session

| Fact | Finding | Source |
| --- | --- | --- |
| Latest release | v1.30.0, Nov 9, 2026 | `gh release view --repo jdx/mr-boxington` |
| Builds | `aarch64-apple-darwin`, `x86_64` and `aarch64` Linux in both `gnu` and `musl`, Windows; **no `x86_64-apple-darwin`** | the release's assets |
| Checksums | One `SHA256SUMS` file for all assets, plus a sigstore `packslip.sigstore.json` | the release's assets; one archive's hash was checked against `SHA256SUMS` |
| Archive layout | `mbx`, `LICENSE` and `NOTICE` at the top level | `tar -t` |
| glibc floor of the `gnu` builds | Highest symbol version `GLIBC_2.18` (x86_64 and aarch64). Both builds run `mbx --version` on Ubuntu 22.04 (glibc 2.35), 24.04 (2.39), 26.04 (2.43) and AlmaLinux 10.3 (2.39) | `strings` on both binaries; containers, amd64 under emulation |
| Shim folder | macOS `~/Library/Application Support/mbx/bin`; Linux `~/.local/share/mbx/bin`, under the data folder. This folder is not the `shims_dir` setting, which holds compiler shims under the cache. | `mbx setup --status`; mbx `docs/setup.md` |
| Does `mbx setup` prompt? | Only at an interactive terminal *with mise active*. Without a terminal it picks no mise config and does not ask. `--yes` would accept a mise scope, so it must not be used. Workbench installs no mise (`rg mise home internal` finds nothing). | `mbx setup --help`; `docs/setup.md` |
| What setup writes | The Cargo shim (`cargo` and `mbx-target` in the shim folder), and `check.overrideCommand` in rust-analyzer's *user* `rust-analyzer.toml`, unless that file already has check settings: `~/.config/rust-analyzer/rust-analyzer.toml` on Linux. It never edits shell startup files. Run with stdin from `/dev/null` or closed and `MISE_SHELL`/`MISE_CONFIG_FILE` unset it asks nothing, exits 0 and writes only those three files; it runs without `cargo` on `PATH`. `mbx --version`, `mbx setup --status` and `mbx settings get` write nothing. | `mbx setup --help`; files newer than a marker in a container |
| The rust-analyzer warning | `check/overrideCommand: unexpected field` is expected and harmless. rust-analyzer checks its user file only against its global and local settings; the override is a workspace setting, and still applies. Upstream discusses it in rust-lang/rust-analyzer#23381. | `mbx setup --status`; `docs/setup.md` |
| Config precedence | Environment (`MBX_*`) wins, then `.mbx.toml` at the workspace root, then the user config: `~/.config/mbx/config.toml` on Linux (honours `XDG_CONFIG_HOME`), `~/Library/Application Support/mbx/config.toml` on macOS. | mbx `docs/configuration.md` |
| Default memory budget | 85% of physical memory, limited by the cgroup in a Linux container | mbx `docs/scheduling.md` |
| Writing a user setting | `mbx settings set KEY VALUE` writes the user config file, keeps its comments and refuses invalid values | `mbx settings set --help` |
| Upgrading | The shim runs the `mbx` that setup recorded while that file exists, then falls back to `mbx` on `PATH` | `docs/setup.md` |
| Failure mode without the `PATH` line | `mbx setup --status` can pass while plain `cargo` still resolves to `~/.cargo/bin/cargo` and skips the cache: the status does not check which `cargo` the shell runs | `mbx setup --help` (`--status`); `mbx doctor` checks it |

Not established: the rust-analyzer user file's path on macOS, and whether
`mbx setup --status` writes nothing there (both checked on Linux only).

### 5.2 What the step does

The new effect is `mbx`, titled "Rust build cache". Its script is
`home/.chezmoiscripts/shared/run_onchange_after_32-mbx.sh.tmpl`, rendered
only for a target with a pinned hash. It runs when its content changes, as on
a new pin, and does nothing unless `WORKBENCH_EFFECT_MBX` says the step is
ticked. An unticked step's script is left out of the apply, so chezmoi never
records it, and a tick given on a later apply runs it. Once it has run, the
plan probes it with `WORKBENCH_PROBE_RAN=1`: the probe compares
`mbx --version` with the pin and reads `mbx setup --status` and the user
config file, and a ticked step whose check finds work (mbx missing or below
the pin, the setup not current, a budget key unset) runs again, while one with
nothing to do is not listed and its script is not run. A saved tick for a step
left unlisted that way is not reported as a stale choice. It runs after
global-tools, so rustup's Cargo already exists. The script:

1. **Downloads the pinned archive** for this target into the run's private
   download folder. It checks the archive's SHA-256 against the pin before
   extracting, with `shasum -a 256` on macOS and `sha256sum` on Linux, the
   split `claude_sha256` in `run_after_45-agent-updates.sh.tmpl` makes; a
   mismatch deletes the archive and blocks the step.
   - Workbench's existing checked downloads are `acquireTool` (Go, for chezmoi
     and uv) and `claude_native` (shell). The `gh` block in
     `linux/run_onchange_before_00-packages.sh.tmpl` fetches the latest release
     with no checksum, so it is not the model.
   - The script then installs `mbx` into `~/.local/bin` through a temporary
     name and `mv`.
   - An existing `mbx` at the pinned version or newer is kept and reported, as
     the contract does for a newer borrowed chezmoi. An older one is replaced;
     a link there is never replaced.
2. **Runs `mbx setup`** without flags. It unsets `MISE_SHELL` and
   `MISE_CONFIG_FILE` and gives it `/dev/null` as stdin, so mbx can neither
   prompt nor pick a mise scope. Apply scripts never prompt.
3. **Sets the machine budget** (section 5.4).

The pins in `home/.chezmoidata/versions.toml`. The hashes are the v1.30.0
`SHA256SUMS` lines:

```toml
[mbx]
version = "1.30.0"
sha256 = { "darwin-arm64" = "d5b957216c8b61c2df91c9e7aaf5febd45ec74274a60d3714ebe316edc144543", "linux-amd64" = "f4992b401fe65744b7c6e7400933d2106da205b499405621d088051a31a8f3f2", "linux-arm64" = "6317bb6780404e78c3b670d95e128c85871809dfc541882b2efaa34120f45e71" }
memory_percent = 60
reserve_cpus = 2
```

The same pin applies in latest mode. Workbench refuses a source whose
`version` is not a plain `1.2.3`, a hash that is not 64 lowercase hex digits,
a `memory_percent` outside 1 to 100 or a negative `reserve_cpus`, since the
script is built from them. `internal/machine` lists the effect only when
`release.Target()` has a hash. An Intel Mac therefore never sees it, and
`doctor` says "mbx publishes no Intel Mac build, so the Rust build cache step
is not offered". The Linux builds are `gnu`; nothing found here needs the
`musl` ones.

### 5.3 `PATH`

In `home/dot_zshrc.tmpl` (macOS only) and `home/private_dot_bashrc.tmpl`
(Linux only), right after the `. "$HOME/.cargo/env"` line:

```sh
# mbx: plain cargo runs through the shared Rust build cache once the Rust build
# cache step has set it up. After ~/.cargo/env, which puts rustup's cargo first.
if [ -x "$HOME/.local/share/mbx/bin/cargo" ]; then
    export PATH="$HOME/.local/share/mbx/bin:$PATH"
fi
```

The zsh file names `$HOME/Library/Application Support/mbx/bin`. The folder is
fixed, not `${XDG_DATA_HOME:-…}`: the step's scripts run without the person's
`XDG_*` variables, so `mbx setup` puts the shim under `~/.local/share`
whatever the shell sets, and the line must look there.

Limits of this placement:

- `~/.zshrc` covers interactive zsh only. mbx's docs recommend `~/.zshenv`
  for non-interactive zsh, and Workbench does not own that file.
- `~/.bashrc` returns early for a non-interactive shell. The same lines also
  go into its non-interactive preamble, beside `~/.local/bin`. `bash -lc`,
  `bash -ic` and `bash -lic` then all find the shim first, on Ubuntu 24.04
  and AlmaLinux 10.
- A tool that inherits neither file still runs `mbx build` and the like.

Which agent hosts read `~/.zshrc` is unverified. The step's plan text tells
the reader to check `command -v cargo` from inside an agent command, as the
mbx docs advise.

### 5.4 The machine budget

The budget goes in mbx's user config file, not in an `MBX_SCHEDULER_MEMORY`
export: the environment outranks `.mbx.toml`, so a shell-wide export would
override a project's tracked budget. The user config file ranks below
`.mbx.toml`. `mbx settings get` cannot tell a default from a written value
(`mbx settings ls` prints values for keys absent from the file), so the step
decides from the file itself: it parses the user `mbx/config.toml` and looks
for `[scheduler]` keys. Then:

- **For each of `memory` and `reserve_cpus` absent from the file**, the step
  runs `mbx settings set`: `scheduler.memory <N>GiB`, where N is
  `memory_percent` (60) of installed memory rounded down, and
  `scheduler.reserve_cpus` with `reserve_cpus` (2). Installed memory is
  `MemTotal` on Linux and `hw.memsize` on macOS; under 2 GiB N is 0 and no
  memory budget is written.
- **A key already in the file** stays as the person left it, whether written
  under `[scheduler]`, as a dotted key or as an inline table: the file is read
  with the management Python's `tomllib`. A file that is not valid TOML
  blocks the step and is not changed.

A project's `.mbx.toml` still wins, and a person's own setting is never
overwritten. Decision D2 covers the 60% figure.

### 5.5 Interactions

- **rustup and `cargo_tools`:** unchanged. The project's
  `rust-toolchain.toml` still picks the compiler, and mbx never changes it.
  `cargo install` of `cargo-audit`, `cargo-deny` and `just` in global-tools
  runs before this step either way.
- **The agent command guard** (`internal/agent/guard/memory.go`) stops agent
  commands only at critical memory pressure (macOS) or by its Linux
  thresholds. mbx's scheduler queues compiles against its budget and backs
  off under pressure (`scheduler.pressure = true` by default), so parallel
  agent builds should reach the guard's limits less often. A queued Cargo
  process waiting for a permit is small. Nothing in the guard changes.
- **Updates:** Workbench owns the pin. A bumped `[mbx]` re-runs the step on
  the next `apply`, which installs the new binary and runs `mbx setup`
  again. `workbench update` installs only Workbench.
- **Revert:** the binary, the shim, the rust-analyzer file and mbx's config
  file are outside the checkpoint, like the other tool installs. The undo
  text is `mbx setup --uninstall`, then delete `~/.local/bin/mbx` and the
  shim folder: `--uninstall` keeps the shim, and while it is there the
  startup-file lines put it first, so plain `cargo` would fail with "mbx is
  not active on PATH". The startup-file lines are checkpointed as usual.

### 5.6 On by default, or optional?

| Option | For | Against |
| --- | --- | --- |
| On wherever a build exists | Every Rust build gets the cache; a configured `.mbx.toml` works without another step | It changes what plain `cargo` does on the whole machine, writes the user rust-analyzer config, and adds a cache that grows to mbx's disk-scaled budgets (`mbx settings ls` describes the `gc.*` and `target.*` defaults) |
| **Optional, off until ticked (chosen)** | Matches `touch-id-sudo`: a machine-wide change to how a common command behaves is opted into once and then saved in `select` | A machine without the tick gets `.mbx.toml` files that do nothing, which is harmless |

This is decision D1.

## 6. The new flags, and CI

### 6.1 `--hooks`: `lefthook.yml`

Every language gets the same four pre-commit jobs, plus tests before push.
The file is written whole, and only when the repository has no lefthook
configuration. lefthook 2.2.2 (`internal/config/loader.go` at that tag) reads
the first of `lefthook`, `.lefthook` and `.config/lefthook` with `.yml`,
`.yaml`, `.json`, `.jsonc` or `.toml` at the repository root; any of these is
kept and reported, and a second one is named. It merges `lefthook-local.*`
over the main file, so the plan names such a file when it writes one. Each
language's jobs live in its policy folder's `lefthook.yml`.

| Job | Go | Rust | Python |
| --- | --- | --- | --- |
| format (`glob`) | `golangci-lint fmt --diff ./...` (`*.go`) | `cargo fmt --all --check` (`*.rs`) | `uv run --locked ruff format --check` (`*.py`) |
| lint | `golangci-lint run` (`*.go`) | `cargo clippy --workspace --all-targets --locked -- -D warnings` (D3) (`*.rs`) | `uv run --locked ruff check` (`*.py`) |
| dependency files | `go mod tidy -diff` (`go.{mod,sum}`) | `cargo metadata --locked --format-version 1 >/dev/null && cargo deny --offline check bans sources` (`{Cargo.toml,Cargo.lock,*/Cargo.toml}`) | `uv lock --check` (`{pyproject.toml,uv.lock,*/pyproject.toml}`) |
| secrets | `gitleaks git --staged --redact --no-banner` | the same | the same |
| pre-push tests | `go test ./...` | `cargo test --workspace --locked` | `uv run --locked pytest`, only when the dev group lists pytest; otherwise no pre-push job, and the plan says so (tests are never invented) |

Example for Go:

```yaml
# Written by workbench project apply (go-v1). Run `lefthook install` once per
# clone to turn on these Git hooks. The project owns this file; Workbench
# never edits an existing one.
pre-commit:
  parallel: true
  jobs:
    - name: go-format
      glob: "*.go"
      run: golangci-lint fmt --diff ./...
    - name: go-lint
      glob: "*.go"
      run: golangci-lint run
    - name: go-dependencies
      glob: "go.{mod,sum}"
      run: go mod tidy -diff
    - name: secrets
      run: gitleaks git --staged --redact --no-banner
pre-push:
  jobs:
    - name: go-test
      run: go test ./...
```

Checks behind these choices:

- **`golangci-lint fmt --diff` exits 1 on a difference.** Tested on a
  scratch module: golangci-lint 2.15.0 printed the gofumpt diff and exited 1.
  It runs the formatters the project's own config names. That is why it
  is used rather than `gofumpt -l`: a project formatting with goimports or
  golines is checked by its own rules.
- **`cargo metadata --locked` fails when `Cargo.lock` is stale or missing.**
  Tested on a scratch crate: exit 101.
- **`cargo deny --offline check bans sources`** keeps the advisory database
  download out of every commit. The advisories check runs in CI.
- **Globs.** lefthook 2.2.2's default matcher lets `*` cross folders: on a
  scratch repository `*.go` matched `top.go` and `a/b/deep.go`. A pattern
  without `*` matches at the root only: `go.{mod,sum}` matched `go.mod` and
  not `a/go.sum`, which suits Go, whose nested modules are separate. For a
  workspace, a member manifest changes the lockfile too, so Rust and Python
  list `*/Cargo.toml` and `*/pyproject.toml` in one brace group. Two brace
  groups side by side are unreliable: `{*/,}{pyproject.toml,uv.lock}` missed
  `a/b/pyproject.toml`, while `{pyproject.toml,uv.lock,*/pyproject.toml}`
  matched it.
- **The jobs block a commit.** In scratch repositories with the files
  `project apply` wrote and `lefthook install` run there, `git commit` exited
  1 for a gofumpt, rustfmt or Ruff formatting difference, an errcheck, clippy
  (`-D warnings`) or Ruff finding, a `go.mod` that `go mod tidy -diff`
  changes, a `Cargo.toml` that `cargo metadata --locked` refuses, a
  `pyproject.toml` that `uv lock --check` (uv 0.13.7) refuses, and a staged
  synthetic AWS key that gitleaks 8.30.1 found; `lefthook run pre-push` exited
  1 on a failing test. `lefthook validate` accepted each written file.
- **`go mod tidy -diff` needs the module cache**: with an empty cache and
  `GOPROXY=off` it exited 1 ("module lookup disabled"), and with the cache
  filled it exited 0 offline. **`uv lock --check`** exited 0 offline with an
  empty cache while the manifest matched the lockfile, and exited 1 offline
  once a manifest named a package missing from the cache.
- **`cargo deny --offline check bans sources`** passed on a scratch crate
  ("bans ok, sources ok").

The machine supplies golangci-lint, gofumpt, lefthook, gitleaks and
govulncheck on Linux (`[linux_binaries]` and `[linux_go_tools]`).

### 6.2 `--dependabot`: `.github/dependabot.yml`

The file is written whole, only when neither `.github/dependabot.yml` nor
`.github/dependabot.yaml` exists (GitHub reads either). It holds weekly
entries for each selected language's ecosystem (`gomod`, `cargo` or `uv`)
plus `github-actions`, all at `directory: /`. GitHub's Dependabot options
reference lists these four YAML values and `weekly` as a `schedule.interval`;
for `github-actions`, `/` makes Dependabot search `.github/workflows`. The
written files passed `check-jsonschema` 0.35.0 against SchemaStore's
`dependabot-2.0.json`.

Renovate reads `renovate.json`, `renovate.jsonc` and `renovate.json5` at the
root or in `.github/` (or `.gitlab/`, only on GitLab), `.renovaterc`,
`.renovaterc.json`, `.renovaterc.jsonc`, `.renovaterc.json5` and a `renovate`
key in `package.json` (`lib/config/app-strings.ts`). When one of these exists,
the plan warns that two update bots would compete, and still writes the file.
An existing Dependabot file is kept, and the plan names the ecosystems it
lacks; both names at once, or a file that is not valid YAML, stop the plan.

### 6.3 `--ci`

The Python contract stays for Python. Go and Rust need one relaxation:
**pick the one workflow that already runs the language's tests**. That means a
`run` step running `go test` (Go), or `cargo test` or `cargo nextest`
(Rust), each matched as a whole command, so `cargo test` is not taken for
`go test`. Exactly one such workflow must exist, and other workflows (such as
release workflows) are ignored.

Everything else is unchanged:

- the exact round trip;
- no anchors, aliases, merge keys or tags;
- the existing jobs are preserved;
- the new job is added as `workbench-go` or `workbench-rust`.

Without this relaxation, `--ci` would be refused on every surveyed project,
all of which have two or more workflows.

Re-apply checks an owned job against the recipe, as `checkOwnedJob` does. A
`workbench-go` job that differs only in `uses:` refs is kept and reported,
because Dependabot may have bumped those refs. Any other difference still
stops the run. `workbench-python` still stops on any difference.

Jobs of several languages aimed at one workflow are merged into one edit of
it. Go's `--ci` also needs a `go` line in `go.mod`, which
`go-version-file` reads.

The `workbench-go` steps:

- `actions/checkout`, with `persist-credentials: false` and `fetch-depth: 0`
  for gitleaks;
- `actions/setup-go`, with `go-version-file: go.mod`;
- `golangci/golangci-lint-action`, with `version` from the pin and
  `install-only: true`;
- `golangci-lint run`;
- `go build ./...`;
- `go run golang.org/x/vuln/cmd/govulncheck@<pin> ./...`;
- gitleaks: download, check its SHA-256, then
  `gitleaks git --redact --no-banner --exit-code 1`, the same steps as this
  repository's `secret-scan` job. Each is a one-line `run`: goccy/go-yaml's
  `MergeFromFile` re-indents a merged job but not the text of a block scalar
  in it, and the merge's reparse check refused the result.

Checked at the pinned commits: `actions/setup-go` v7.0.0's `action.yml` has
`go-version-file`, and `golangci/golangci-lint-action` v9.3.0's has `version`
and `install-only`. actionlint 1.7.12 passed a workflow with the job.

The `workbench-rust` steps:

- checkout, as for Go;
- `rustup toolchain install` with no name, which installs the active
  toolchain, the one `rust-toolchain.toml` names. rustup 1.29.3's help says
  "Install or update the given toolchains, or by default the active
  toolchain", and in a scratch crate it printed "the active toolchain
  `1.99.0-aarch64-apple-darwin` has been installed ... overridden by
  `.../rust-toolchain.toml`". The `actions/runner-images` readme for
  ubuntu-24.04 lists Rustup 1.29.3, the same version (not run there);
- `cargo fmt --all --check`;
- `cargo clippy --workspace --all-targets --locked -- -D warnings`, so
  `--ci` also needs an existing `Cargo.lock` at the root;
- the cargo-deny release archive (`x86_64-unknown-linux-musl`) is
  downloaded, its SHA-256 checked, and `cargo-deny` unpacked from its one
  top-level folder (`tar -t` lists `cargo-deny-0.20.2-x86_64-unknown-linux-musl/cargo-deny`),
  then `cargo-deny --locked check advisories bans sources`;
- gitleaks, the same four steps as Go's.

Each `run` is one line, for the reason Go's are. No Rust action needs a pin:
the runner image's rustup is enough.
actionlint 1.7.12 passed a workflow with the job.

Each tool keeps its one pin in `versions.toml`: golangci-lint and gitleaks in
`[linux_binaries]`, govulncheck in `[linux_go_tools]`, the actions in
`[github_actions]`. The one new table holds only what had no pin, keyed by the
version above it so a bump without its checksum stops `--ci`.
`scripts/check-action-pins.py` stays as it is, because these are not action
pins:

```toml
[project_ci]
gitleaks_linux_x64_sha256 = { "8.30.1" = "551f6fc83ea457d62a0d98237cbad105af8d557003051f41f3e7ca7b3f2470eb" }
cargo_deny_linux_x64_sha256 = { "0.20.2" = "9f12ed4c49936e09b48bf862b595cde2fe64fcbd9d74dfacac6131ca824c8d5f" }
```

The gitleaks hash is the `gitleaks_8.30.1_linux_x64.tar.gz` line of that
release's `gitleaks_8.30.1_checksums.txt`. The cargo-deny hash is the content
of the release's `cargo-deny-0.20.2-x86_64-unknown-linux-musl.tar.gz.sha256`
asset, and matched the downloaded archive. cargo-deny's version is the one
`[cargo_tools]` already pins.

## 7. Code layout

There is no new framework. Each concern stays with its existing owner.

| Concern | Owner | New or changed |
| --- | --- | --- |
| Policy assets | `project/gopolicy/` and `project/rustpolicy/`, beside `project/pythonpolicy/` | Each has `assets.go` with `ID`, plus `README.md` and the embedded files above. All three hold `lefthook.yml` (the language's jobs) and the `Ecosystem` constant for Dependabot. |
| Discovery | `internal/project/discovery.go` | `goOwner` and `cargoOwner` beside `pythonOwner`; `manifestProject` calls them |
| Planning | `internal/project/plan.go` | `Plan` accepts `go` and `rust`; the source is the selected IDs joined by `_` (a release identifier allows no `,`), with a digest over their assets; new files `golang.go` and `rust.go` hold each language's additions |
| `Cargo.toml` edits | `internal/project/adapter.py` (TOML Kit) | The request names the provenance path, `tool.workbench`, `workspace.metadata.workbench` or `package.metadata.workbench`. The Ruff selection branch runs only for the Python policy. |
| Whole-file writes | `Proposal.add` | No editor; absent means create |
| `.vscode/extensions.json`, `.gitignore` | `editor.go` | Policy-aware. `mergeIgnore` now checks negations only when it has something to append. |
| Hooks and Dependabot | new `internal/project/hooks.go` | Renders and combines each language's jobs |
| Workflow | `internal/project/ci.go` | The workflow choice of section 6.3 and the shared merge; `goJob`, the Go workflow choice and the Go pins (read from the selected source's `versions.toml`) live in `golang.go`, and `rustJob` and the Rust pins in `rust.go`; the workflow choice (`testWorkflow`) and the pin reading (`readSourceVersions`) are shared |
| Flags | `internal/cli/project.go` | `--hooks` and `--dependabot`; `--gitignore` help made general |
| mbx step | `internal/machine/effects.go` (the effect, its source and the doctor line), `source.go` (the `[mbx]` pin and its checks), `plan.go` (stale choices judged against offered steps), plus the shared script, the shell templates and `versions.toml` | One effect |

## 8. Order of work

Each step is its own branch and pre-release. "Try" means a dry run, then an
apply, then a revert, on a real project.

1. **Go policy** (`go-v1`): discovery, `.golangci.yml`, `.workbench.toml`,
   `--extensions`, `--gitignore`, `--ci`. It needs no TOML Kit.
   - Try: an existing Go module with no lint configuration gets the full
     file set, then reverts to its exact prior state.
   - Try: a module with its own `.golangci.yml` reports it as kept.
2. **Rust policy** (`rust-v1`): static workspace ownership, the adapter's
   provenance path, lints, member opt-in, profile, `rustfmt.toml`,
   `deny.toml`, `.mbx.toml`, `--extensions`, `--gitignore`, `--ci`.
   - Try: a workspace with its own `[workspace.lints]` gets only the record,
     the dev profile setting where it is missing, and the missing whole files.
3. **`--hooks` and `--dependabot`** for Go, Rust and Python.
   - Try: commit a badly formatted file after `lefthook install`, and watch
     the hook refuse it.
4. **mbx step**, after the Linux tools change lands.
   - Try: apply with the step ticked. Then `command -v cargo` prints the shim,
     `mbx setup --status` passes, and `mbx settings get scheduler.memory`
     shows the machine's value. A workspace `.mbx.toml` still overrides it in
     that workspace (`mbx stats --scheduler`).

Docs follow each step: `project/*/README.md`, the contracts' project-editor
section and `docs/usage.md`.

## 9. Out of scope

- clippy pedantic, licence checks, nextest: each is used by only one of the
  surveyed projects;
- repository-specific Go rules (line length, function size, `forbidigo`);
- Go workspaces (`go.work`);
- TypeScript;
- running `lefthook install`;
- a Rust CI cache;
- remote mbx caches;
- Windows mbx.

## 10. Decisions

- **D1. mbx step default.**
  - Options: (a) on wherever a build exists; (b) optional, off until ticked.
  - **Decided: (b).** It changes every `cargo` on the machine and the
    user's rust-analyzer config, as `touch-id-sudo` changes every `sudo`.
- **D2. Machine memory budget when the person has set none.**
  - Options: (a) leave mbx's default of 85% of memory; (b) write 60% of
    installed memory and `reserve_cpus = 2`.
  - **Decided: (b).** Agents, editors and the guard's critical-pressure
    stop share the same memory, and 85% leaves little for them.
- **D3. Rust lint hook.**
  - Options: (a) `cargo clippy` with warnings only, which never blocks a
    commit; (b) with `-D warnings`, the same as CI.
  - **Decided: (b).** A commit that passes the hook should pass CI.
    Warnings stay warnings in the editor.
- **D4. `--ci` workflow choice for Go and Rust.**
  - Options: (a) Python's exactly-one-workflow rule, which refuses every
    surveyed project; (b) the one workflow that already runs the language's
    tests.
  - **Decided: (b).** It still never invents a test job or a second
    workflow.
