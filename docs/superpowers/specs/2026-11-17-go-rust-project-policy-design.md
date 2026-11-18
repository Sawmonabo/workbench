# Go and Rust project policy and the mbx step

Status: design, Nov 17, 2026. Nothing here is built yet. It adds two languages
to `workbench project apply` (renamed from `project configure` in a separate
change), two new flags for every language (`--hooks`, `--dependabot`), and one
new machine step that installs mbx, a shared Rust compile cache and scheduler.

The work ships in four steps (section 8). Each step can be tried on a real
project before the next starts.

"Today" means code read in this tree. "Proposed" means this design.
"Unverified" means nobody checked it in this session, and the build step that
depends on it must check it first.

## 1. What exists today

- **Discovery** (`internal/project/inspect.go`, `discovery.go`) already finds
  `go.mod`, `go.work`, `Cargo.toml` and `Cargo.lock`. It reads them without
  running anything. Each `go.mod` and each `Cargo.toml` becomes its own
  project, marked "Recognized; configuration is not supported for this
  language". Nothing links a Cargo workspace member to its root.
- **Planning** (`configure.go`) refuses any `--language` other than `python`.
  Python settings go into `pyproject.toml` through the TOML Kit adapter
  (`adapter.py`). That adapter **adds missing settings and keeps every existing
  value and rule list**. It also records `[tool.workbench]` with
  `schema_version = 1` and `policies = ["python-v1"]`.
- **Python stops only for competing owners**: a standalone `ruff.toml`, `ty.toml`,
  `pyrightconfig.json` or `uv.toml`, a `[tool.pyright]` table, or `tool.ruff.extend`.
  In each case a merged setting would be ignored or refused by the tool. The
  approved matrix says "stop and report, as Python does". That describes Python
  wrongly. Python merges, and stops only where merging would be dead or
  rejected.
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
- **The plan's source identity** is the policy ID (`pythonpolicy.ID`) plus a
  digest of the embedded assets.

## 2. Rules shared by both languages

### 2.1 The rule for existing configuration

There are two kinds of file, and each gets one rule:

1. **Settings in a shared manifest** (`Cargo.toml`, `pyproject.toml`): add
   what is missing and keep every existing value. This is Python's rule,
   unchanged and run by the same adapter. A project that sets
   `unsafe_code = "forbid"` keeps `forbid`. A project with
   `clippy::all = "deny"` keeps `deny`. Existing rule tables are never
   extended beyond the keys the policy names.
2. **Whole files a tool reads on its own**:
   - `.golangci.yml`
   - `lefthook.yml`
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
(the folder that holds `.git`). Otherwise the plan is blocked with that reason.
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
| Nothing (work it out from the files) | Rejected. A file the project wrote by hand looks the same as one Workbench wrote. A later `go-v2` could not tell what it is upgrading. |

All three records hold the same two keys: `schema_version = 1` and
`policies = [...]`. Proposed change: the list keeps **one entry per language**,
and the current ID replaces an older ID of the same language. For example,
`python-v2` replaces `python-v1`, so the record says what the project follows
now.

The Go record:

```toml
# Written by workbench project apply: the Workbench project policies this
# repository follows. Builds and tests never read it.
schema_version = 1
policies = ["go-v1"]
```

An existing `.workbench.toml` is parsed with go-toml. If its schema version is
not 1, the run stops, the same rule `adapter.py` applies. Otherwise Workbench
writes it again with the ID added.

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
  while editing. Decision D3 asks the owner to choose. The design recommends
  `-D warnings` in the hook.

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
and `supported` true or false with the reason.

### 3.2 Files

| File | Rule | Content |
| --- | --- | --- |
| `.golangci.yml` | Whole file; created when no golangci-lint config exists. Any of `.golangci.yml`, `.yaml`, `.toml` or `.json` counts as existing. | Below. |
| Format settings | Inside `.golangci.yml` (`formatters`) | gofumpt |
| Toolchain pin | Left alone | `go.mod` owns `go` and `toolchain` |
| Supply-chain settings | None | govulncheck needs no file |
| Build settings | None | Go's own build cache |
| `.workbench.toml` | Section 2.4 | Record |

An existing config without `version: "2"` is a golangci-lint v1 file, which v2
refuses. It is kept, and the plan says "golangci-lint v2 refuses this v1
config; run `golangci-lint migrate`".

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
holds the matrix's govet, staticcheck and errcheck, plus two cheap extras that
leaving them out would need `default: none` to drop. The four linters added to
it complete the matrix's seven.

### 3.3 Optional files

**`--extensions`** adds `golang.go` to `recommendations` through the same
HuJSON merge as Python. **`--gitignore`** appends `/bin/`, `*.test` and
`/coverage.out`, skipping a line that is already there. `--hooks`,
`--dependabot` and `--ci` are in section 6.

When the root `.gitignore` has a `.vscode/` or `.vscode/*` line and no
`!.vscode/extensions.json`, `--extensions` (in every language) still writes
the file but warns that Git ignores it. Workbench does not evaluate ignore
rules beyond that literal check.

## 4. Rust (`--language rust`, policy `rust-v1`)

### 4.1 Detection, from the TOML text only

| Layout | Plan |
| --- | --- |
| Root `Cargo.toml` with `[package]` and no `[workspace]` | Single crate. Lints go in `[lints.rust]` and `[lints.clippy]`; the record goes in `[package.metadata.workbench]`. |
| Root `Cargo.toml` with `[workspace]`, virtual or not | Workspace. Lints go in `[workspace.lints.*]`; the record goes in `[workspace.metadata.workbench]`. |
| A `Cargo.toml` that the root's `members` and `exclude` cover | Member, owned by the root. Selecting it alone is blocked with "select the workspace root", as for uv workspaces. |
| A path dependency inside the workspace folder (in `[workspace.dependencies]` or the root package's dependencies) | Member too: Cargo makes path dependencies under the workspace folder members without listing them. |
| A `Cargo.toml` below the root that neither rule covers, such as a separate fuzz workspace or a vendored crate | Reported as untouched. |

Membership reuses `workspaceMember` in `discovery.go`. Cargo and uv both use
the keys `members` and `exclude`, and that function already refuses `**` and
`..` patterns for review.

One gap: discovery skips hidden folders and `vendor`, `target`, `build` and
`dist`. A `members` pattern that names one of these folders is blocked for
review, instead of silently missing a member.

Other languages in the same repository (TypeScript, Python without `uv.lock`)
stay untouched, with the existing warning.

### 4.2 Files

| Setting | Where | Rule |
| --- | --- | --- |
| Lints | `[workspace.lints.rust]` and `[workspace.lints.clippy]`, or `[lints.*]` in a single crate | Missing keys only |
| Member opt-in | `[lints]` with `workspace = true` in each member's `Cargo.toml` | Only when Workbench creates `[workspace.lints]` |
| Dev profile | `[profile.dev] debug = "line-tables-only"` in the root manifest | Missing key only |
| Format | `rustfmt.toml` | Whole file |
| Toolchain pin | `rust-toolchain.toml` | Left alone and reported |
| Supply chain | `deny.toml` | Whole file |
| Build cache | `.mbx.toml` | Whole file |
| Record | Section 2.4 | Adapter |

**Lint settings** merged through `adapter.py`:

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

**Toolchain report.** The plan warns when `rust-toolchain.toml` (or the older
`rust-toolchain`) is missing, or does not list both `rustfmt` and `clippy` in
`components`. It never writes this file.

**Competing owners that stop the run:**

- both `rustfmt.toml` and `.rustfmt.toml`, or both `deny.toml` and
  `.deny.toml` (the tool would read only one of each);
- a `[lints]` table in a single crate that is not a table of lint groups.

**`rustfmt.toml`**, created only when neither `rustfmt.toml` nor
`.rustfmt.toml` exists. The edition comes from `package.edition` or
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
file does not hold the matrix's "reserved cores". mbx reads the environment
first, then `.mbx.toml`, then the user's `mbx/config.toml`. A tracked
`scheduler.reserve_cpus` would therefore override every machine's own setting.
Cores and memory are both machine facts, so both belong to the machine step
(section 5.4).

### 4.3 Optional files

**`--extensions`** adds `rust-lang.rust-analyzer`. **`--gitignore`** appends
`target/`. `--hooks`, `--dependabot` and `--ci` are in section 6; `--ci`
requires an existing `rust-toolchain.toml`, just as Python requires
`.python-version`, so Workbench never picks a toolchain.

## 5. The mbx machine step (`workbench apply`)

### 5.1 Facts found in this session

| Fact | Finding | Source |
| --- | --- | --- |
| Latest release | v1.30.0, Nov 9, 2026 | `gh release view --repo jdx/mr-boxington` |
| Builds | `aarch64-apple-darwin`, `x86_64` and `aarch64` Linux in both `gnu` and `musl`, Windows; **no `x86_64-apple-darwin`** | the release's assets |
| Checksums | One `SHA256SUMS` file for all assets, plus a sigstore `packslip.sigstore.json` | the release's assets; one archive's hash was checked against `SHA256SUMS` |
| Archive layout | `mbx`, `LICENSE` and `NOTICE` at the top level | `tar -t` |
| glibc floor of the `gnu` builds | Highest symbol version `GLIBC_2.18` (x86_64 and aarch64), so Ubuntu 22.04 and 24.04, WSL and AlmaLinux 10 are covered | `strings` on both binaries; **never run on Linux** |
| Shim folder | macOS `~/Library/Application Support/mbx/bin`; Linux `~/.local/share/mbx/bin`, under the data folder. This folder is not the `shims_dir` setting, which holds compiler shims under the cache. | `mbx setup --status`; mbx `docs/setup.md` |
| Does `mbx setup` prompt? | Only at an interactive terminal *with mise active*. Without a terminal it picks no mise config and does not ask. `--yes` would accept a mise scope, so it must not be used. Workbench installs no mise (`rg mise home internal` finds nothing). | `mbx setup --help`; `docs/setup.md` |
| What setup writes | The Cargo shim, and `check.overrideCommand` in rust-analyzer's *user* `rust-analyzer.toml`, unless that file already has check settings. It never edits shell startup files. | `mbx setup --help` |
| The rust-analyzer warning | `check/overrideCommand: unexpected field` is expected and harmless. rust-analyzer checks its user file only against its global and local settings; the override is a workspace setting, and still applies. Upstream discusses it in rust-lang/rust-analyzer#23381. | `mbx setup --status`; `docs/setup.md` |
| Config precedence | Environment (`MBX_*`) wins, then `.mbx.toml` at the workspace root, then the user config: `~/.config/mbx/config.toml` on Linux (honours `XDG_CONFIG_HOME`), `~/Library/Application Support/mbx/config.toml` on macOS. | mbx `docs/configuration.md` |
| Default memory budget | 85% of physical memory, limited by the cgroup in a Linux container | mbx `docs/scheduling.md` |
| Writing a user setting | `mbx settings set KEY VALUE` writes the user config file, keeps its comments and refuses invalid values | `mbx settings set --help` |
| Upgrading | The shim runs the `mbx` that setup recorded while that file exists, then falls back to `mbx` on `PATH` | `docs/setup.md` |
| Failure mode without the `PATH` line | `mbx setup --status` can pass while plain `cargo` still resolves to `~/.cargo/bin/cargo` and skips the cache: the status does not check which `cargo` the shell runs | `mbx setup --help` (`--status`); `mbx doctor` checks it |

**Not established:**

- whether the Linux `gnu` builds run on each distribution;
- the rust-analyzer user file's path on Linux;
- whether `mbx setup` with stdin closed and `MISE_SHELL` unset is silent.

### 5.2 What the step does

The new effect is `mbx`, titled "Rust build cache". Its script is
`home/.chezmoiscripts/shared/run_after_32-mbx.sh.tmpl`. Like the
`touch-id-sudo` script it is a plain `run_after_` script that runs on every
apply and does nothing unless `WORKBENCH_EFFECT_MBX` says the step is ticked,
so a tick given on a later apply still runs it. It compares `mbx --version`
with the pin itself, instead of relying on `run_onchange_`. It runs after
global-tools, so rustup's Cargo already exists. The script:

1. **Downloads the pinned archive** for this target into the run's private
   download folder. It checks the archive's SHA-256 against the pin before
   extracting, using the same `shasum`/`sha256sum` split as
   `claude_sha256` in `run_after_45-agent-updates.sh.tmpl`.
   - Workbench's existing checked downloads are `acquireTool` (Go, for chezmoi
     and uv) and `claude_native` (shell). The `gh` block in
     `linux/run_once_before_00-packages.sh.tmpl` fetches the latest release
     with no checksum, so it is not the model.
   - The script then installs `mbx` into `~/.local/bin` through a temporary
     name and `mv`.
   - An existing `mbx` at the pinned version or newer is kept and reported, as
     the contract does for a newer borrowed chezmoi.
2. **Runs `mbx setup`** without flags. It unsets `MISE_SHELL` and
   `MISE_CONFIG_FILE` and closes stdin, so mbx can neither prompt nor pick a
   mise scope. Apply scripts never prompt.
3. **Sets the machine budget** (section 5.4).

Proposed pins in `home/.chezmoidata/versions.toml`. The hashes are the
v1.30.0 `SHA256SUMS` lines:

```toml
[mbx]
version = "1.30.0"
sha256 = { "darwin-arm64" = "d5b957216c8b61c2df91c9e7aaf5febd45ec74274a60d3714ebe316edc144543", "linux-amd64" = "f4992b401fe65744b7c6e7400933d2106da205b499405621d088051a31a8f3f2", "linux-arm64" = "6317bb6780404e78c3b670d95e128c85871809dfc541882b2efaa34120f45e71" }
```

`internal/machine` lists the effect only when `release.Target()` has a hash.
An Intel Mac therefore never sees it, and `doctor` says "mbx publishes no
Intel Mac build". The Linux builds are `gnu`; nothing found here needs the
`musl` ones.

### 5.3 `PATH`

In `home/dot_zshrc.tmpl` and `home/private_dot_bashrc.tmpl`, put this right
after the `. "$HOME/.cargo/env"` line:

```sh
# mbx: plain cargo runs through the shared Rust build cache. After ~/.cargo/env,
# which puts rustup's cargo first.
{{ if eq .chezmoi.os "darwin" }}mbx_shims="$HOME/Library/Application Support/mbx/bin"{{ else }}mbx_shims="${XDG_DATA_HOME:-$HOME/.local/share}/mbx/bin"{{ end }}
[ -x "$mbx_shims/cargo" ] && export PATH="$mbx_shims:$PATH"
unset mbx_shims
```

Limits of this placement:

- `~/.zshrc` covers interactive zsh only. mbx's docs recommend `~/.zshenv`
  for non-interactive zsh, and Workbench does not own that file.
- `~/.bashrc` returns early for a non-interactive shell. The same three lines
  also go into its non-interactive preamble, beside `~/.local/bin`.
- A tool that inherits neither file still runs `mbx build` and the like.

Which agent hosts read `~/.zshrc` is unverified. The step's plan text tells
the reader to check `command -v cargo` from inside an agent command, as the
mbx docs advise.

### 5.4 The machine budget

The matrix proposed exporting `MBX_SCHEDULER_MEMORY` in the shell. **This
design drops that export.** The environment outranks `.mbx.toml`, so a
shell-wide export would override a project's tracked budget.

The budget goes in the user config file instead, which ranks below
`.mbx.toml`. `mbx settings get` cannot tell a default from a written value
(`mbx settings ls` prints values for keys absent from the file), so the step
decides from the file itself: it parses the user `mbx/config.toml` and looks
for `[scheduler]` keys. Then:

- **For each of `memory` and `reserve_cpus` absent from the file**, the step
  runs `mbx settings set`: `scheduler.memory <N>GiB`, where N is 60% of
  installed memory rounded down, and `scheduler.reserve_cpus 2`.
- **A key already in the file** stays as the person left it.

A project's `.mbx.toml` still wins, and a person's own setting is never
overwritten. Decision D2 covers the 60% figure.

### 5.5 Interactions

- **rustup and `cargo_tools`:** unchanged. The project's
  `rust-toolchain.toml` still picks the compiler, and mbx never changes it.
  `cargo install` of `cargo-audit`, `cargo-deny` and `just` in global-tools
  runs before this step either way.
- **The agent command guard** (`internal/guard/memory.go`) stops agent
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
  text is `mbx setup --uninstall`, then delete `~/.local/bin/mbx`. The
  startup-file lines are checkpointed as usual.

### 5.6 On by default, or optional?

| Option | For | Against |
| --- | --- | --- |
| On wherever a build exists | Every Rust build gets the cache; a configured `.mbx.toml` works without another step | It changes what plain `cargo` does on the whole machine, writes the user rust-analyzer config, and adds a cache that grows to mbx's disk-scaled budgets (`mbx settings ls` describes the `gc.*` and `target.*` defaults) |
| **Optional, off until ticked (recommended)** | Matches `touch-id-sudo`: a machine-wide change to how a common command behaves is opted into once and then saved in `select` | A machine without the tick gets `.mbx.toml` files that do nothing, which is harmless |

This is decision D1.

## 6. The new flags, and CI

### 6.1 `--hooks`: `lefthook.yml`

Every language gets the same four pre-commit jobs, plus tests before push.
The file is written whole, and only when absent. A project's existing
`lefthook.yml` is kept and reported.

| Job | Go | Rust | Python |
| --- | --- | --- | --- |
| format (`glob`) | `golangci-lint fmt --diff ./...` (`*.go`) | `cargo fmt --all --check` (`*.rs`) | `uv run --locked ruff format --check` (`*.py`) |
| lint | `golangci-lint run` (`*.go`) | `cargo clippy --workspace --all-targets --locked -- -D warnings` (D3) (`*.rs`) | `uv run --locked ruff check` (`*.py`) |
| dependency files | `go mod tidy -diff` (`go.{mod,sum}`) | `cargo metadata --locked --format-version 1 >/dev/null && cargo deny --offline check bans sources` (`Cargo.{toml,lock}`) | `uv lock --check` (`{pyproject.toml,uv.lock}`) |
| secrets | `gitleaks git --staged --redact --no-banner` | the same | the same |
| pre-push tests | `go test ./...` | `cargo test --workspace --locked` | `uv run --locked pytest`, only when the dev group lists pytest; otherwise no pre-push job, and the plan says so (tests are never invented) |

Example for Go:

```yaml
# Written by workbench project apply (go-v1). Run `lefthook install` once per
# clone; the project owns this file from now on.
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
  replaces the matrix's `gofumpt -l`: a project formatting with goimports or
  golines is checked by its own rules.
- **`cargo metadata --locked` fails when `Cargo.lock` is stale or missing.**
  Tested on a scratch crate: exit 101.
- **`cargo deny --offline check bans sources`** keeps the advisory database
  download out of every commit. The advisories check runs in CI.
- **Glob matching across folders** is lefthook's default (unverified).
- **`go mod tidy -diff` and `uv lock --check`** may need the network on a
  cold module or index cache (unverified).

The machine supplies golangci-lint, gofumpt, lefthook, gitleaks and
govulncheck on Linux through the Linux tools change already in progress. This
design depends on it.

### 6.2 `--dependabot`: `.github/dependabot.yml`

The file is written whole, only when absent. It holds weekly entries for each
selected language's ecosystem (`gomod`, `cargo` or `uv`, values checked
against GitHub's Dependabot options reference) plus `github-actions`, all at
`directory: /`.

When a `renovate.json`, `renovate.json5` or `.github/renovate.json*` exists,
the plan warns that two update bots would compete, and still writes the file.
An existing `dependabot.yml` is kept, and the plan names the ecosystems it
lacks.

### 6.3 `--ci`

The Python contract stays for Python. Go and Rust need one relaxation:
**pick the one workflow that already runs the language's tests**. That means a
`run` step containing `go test` (Go), or `cargo test` or `cargo nextest`
(Rust). Exactly one such workflow must exist, and other workflows (such as
release workflows) are ignored.

Everything else is unchanged:

- the exact round trip;
- no anchors, aliases, merge keys or tags;
- the existing jobs are preserved;
- the new job is added as `workbench-go` or `workbench-rust`.

Without this relaxation, `--ci` would be refused on every surveyed project,
all of which have two or more workflows.

Re-apply checks an owned job against the recipe, as `checkOwnedJob` does.
Proposed change: a job that differs only in `uses:` refs is kept and
reported, because Dependabot may have bumped those refs. Any other difference
still stops the run.

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
  repository's `secret-scan` job.

The `workbench-rust` steps:

- checkout;
- `rustup toolchain install` with no name, which installs the active
  toolchain, the one `rust-toolchain.toml` names (per `rustup toolchain
  install --help` locally; the runner image's rustup version is unverified).
- the cargo-deny release archive (`x86_64-unknown-linux-musl`) is
  downloaded, and its SHA-256 checked before use;
- the three Cargo commands from section 4.3;
- gitleaks.

No Rust action needs a pin. The runner image's rustup is enough (its presence
there is unverified).

Proposed new table in `versions.toml`. `scripts/check-action-pins.py` stays
as it is, because these are not action pins:

```toml
# Tools the generated project CI jobs download, checked by SHA-256.
[project_ci]
golangci_lint = "2.14.0"
govulncheck = "v1.5.0"          # confirm the current release when building
gitleaks = "8.30.1"
gitleaks_sha256 = "551f6fc83ea457d62a0d98237cbad105af8d557003051f41f3e7ca7b3f2470eb"  # linux_x64
cargo_deny = "0.20.2"
cargo_deny_sha256 = "<from the release's .sha256 asset>"
```

## 7. Code layout

There is no new framework. Each concern stays with its existing owner.

| Concern | Owner | New or changed |
| --- | --- | --- |
| Policy assets | `project/gopolicy/` and `project/rustpolicy/`, beside `project/pythonpolicy/` | Each has `assets.go` with `ID`, plus `README.md` and the embedded files above. `pythonpolicy` gains `lefthook.yml`, and its ID moves to `python-v2`, because the ID changes whenever content does. |
| Discovery | `internal/project/discovery.go` | `goOwner` and `cargoOwner` beside `pythonOwner`; `manifestProject` calls them |
| Planning | `internal/project/configure.go` | `Plan` accepts `go` and `rust`; the source is the selected IDs joined by `,`, with a digest over their assets; new files `golang.go` and `rust.go` hold each language's additions |
| `Cargo.toml` edits | `internal/project/adapter.py` (TOML Kit) | The request names the provenance path, `tool.workbench`, `workspace.metadata.workbench` or `package.metadata.workbench`. The Ruff selection branch runs only for the Python policy. |
| Whole-file writes | `Proposal.add` | No editor; absent means create |
| `.vscode/extensions.json`, `.gitignore` | `editor.go` | Policy-aware. `mergeIgnore` now checks negations only when it has something to append. |
| Hooks and Dependabot | new `internal/project/hooks.go` | Renders and combines each language's jobs |
| Workflow | `internal/project/ci.go` | The workflow choice of section 6.3, plus `goJob` and `rustJob` |
| Flags | `internal/cli/project.go` | `--hooks` and `--dependabot`; `--gitignore` help made general |
| mbx step | `internal/machine/effects.go`, `source.go`, plus the shared script, the shell templates and `versions.toml` | One effect |

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
   - Try: a workspace with its own `[workspace.lints]` gets only the record
     and the missing whole files.
3. **`--hooks` and `--dependabot`** for Go, Rust and Python (Python moves to
   `python-v2`).
   - Try: commit a badly formatted file after `lefthook install`, and watch
     the hook refuse it.
4. **mbx step**, after the Linux tools change lands.
   - Try: apply with the step ticked. Then `command -v cargo` prints the shim,
     `mbx setup --status` passes, and `mbx settings get scheduler.memory`
     shows the machine's value. A workspace `.mbx.toml` still overrides it in
     that workspace (`mbx stats --scheduler`).

Docs follow each step: `project/*/README.md`, the contracts' project-editor
section and `docs/usage.md`.

## 9. Changes from the approved matrix

| Matrix | Here | Why |
| --- | --- | --- |
| "Stop and report, as Python does" | Merge missing in manifests; keep existing whole files; go on | Python merges; stopping would make apply useless on configured projects (2.1) |
| Export `MBX_SCHEDULER_MEMORY` | Set it in mbx's user config file, only when the key is unset | The environment outranks `.mbx.toml` (5.4) |
| Reserved cores in `.mbx.toml` | In the user config | It is a machine fact, and a tracked value would override every machine |
| Hook format check `gofumpt -l` | `golangci-lint fmt --diff ./...` | Follows the project's own formatter set |
| Hook `cargo deny check` | `cargo metadata --locked` plus `cargo deny --offline check bans sources` | A dependency-file check like the other languages; bare `cargo deny check` fails on licences with this `deny.toml`, and advisories need the network |
| CI `cargo audit` | Dropped | `cargo deny check advisories` reads the same RustSec database; one fewer tool to pin |
| Ignore `bin/`, `coverage.out` | `/bin/`, `/coverage.out` | Go writes these at the module root |
| `--ci` "as Python" | One workflow that runs the tests | Every surveyed project has several workflows |
| mbx everywhere | Not on Intel Macs | No `x86_64-apple-darwin` build |

## 10. Out of scope

- clippy pedantic, licence checks, nextest: each is used by only one of the
  surveyed projects;
- repository-specific Go rules (line length, function size, `forbidigo`);
- Go workspaces (`go.work`);
- TypeScript;
- running `lefthook install`;
- a Rust CI cache;
- remote mbx caches;
- Windows mbx.

## 11. Owner decisions

- **D1. mbx step default.**
  - Options: (a) on wherever a build exists; (b) optional, off until ticked.
  - **Recommended: (b).** It changes every `cargo` on the machine and the
    user's rust-analyzer config, as `touch-id-sudo` changes every `sudo`.
- **D2. Machine memory budget when the person has set none.**
  - Options: (a) leave mbx's default of 85% of memory; (b) write 60% of
    installed memory and `reserve_cpus = 2`.
  - **Recommended: (b).** Agents, editors and the guard's critical-pressure
    stop share the same memory, and 85% leaves little for them.
- **D3. Rust lint hook.**
  - Options: (a) `cargo clippy` with warnings only, which never blocks a
    commit; (b) with `-D warnings`, the same as CI.
  - **Recommended: (b).** A commit that passes the hook should pass CI.
    Warnings stay warnings in the editor.
- **D4. `--ci` workflow choice for Go and Rust.**
  - Options: (a) Python's exactly-one-workflow rule, which refuses every
    surveyed project; (b) the one workflow that already runs the language's
    tests.
  - **Recommended: (b).** It still never invents a test job or a second
    workflow.
