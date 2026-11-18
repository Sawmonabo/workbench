# Existing Rust project policy (`rust-v1`)

These files configure tooling in an existing Cargo package or workspace. They
do not generate a crate, source code, tests or a Git repository, and planning
runs no Cargo or rustup command: it reads `Cargo.toml` files, the toolchain
file and the configuration files as text, and never runs `cargo metadata` or
`cargo fetch`, so a `rust-toolchain.toml` cannot make rustup download a
toolchain during a preview.

Inspect with `workbench project inspect PATH`. Preview with
`workbench project apply PATH --language rust --dry-run`. Without `--language`,
every supported language found is planned; `--language` only narrows. Editing
`Cargo.toml` needs Workbench's private Python and TOML Kit, as Python
configuration does.

## Which packages

- A `Cargo.toml` at the selected folder with `[package]` and no `[workspace]`
  is a single crate.
- A `Cargo.toml` at the selected folder with `[workspace]`, with or without a
  root package, is a workspace root. Its members are the packages its
  `members` patterns list, plus every path dependency of a member (normal,
  dev or build, also per target, and a `workspace = true` entry resolved
  through `[workspace.dependencies]`) inside the workspace folder, as Cargo
  adds them. An `exclude` entry removes the folder and everything below it,
  unless `members` lists the package. A `[workspace.dependencies]` path entry
  no member uses, and a `[patch]` path, make no member.
- A package inside a workspace is configured only through the workspace root.
  Selecting it alone is blocked with "select the workspace root".
- A `members` or `exclude` pattern with `**`, `..` or a leading `/` is blocked
  for review, and so is a `members` pattern, or a member's path dependency,
  naming a folder discovery skips (hidden folders, `vendor`, `target`,
  `build`, `dist` and the other generated folders), whose members it could
  not find. The members of a blocked root are not configured either.
- A `Cargo.toml` below the selected folder that no rule above covers, such as
  a separate fuzz workspace or an excluded crate, is reported and left
  untouched. A package with a `package.workspace` key is left for review.
- A plan for another language (a uv project at the root, say) is never blocked
  by a Cargo package it does not configure; that package is named in a
  warning.

## Cargo.toml

The root manifest gets, through the comment-preserving TOML Kit adapter:

- the lints in `lints.toml`, as `[lints.rust]` and `[lints.clippy]` in a
  single crate or `[workspace.lints.rust]` and `[workspace.lints.clippy]` in a
  workspace root, but only when the manifest has no lint table there yet. An
  existing `[lints]` or `[workspace.lints]` is the project's own selection and
  is kept whole, as an existing Ruff selection is for Python; the plan says so
  when it lacks lints the policy sets. A lint added beside a group such as
  `clippy::all` at the same priority would fail clippy's deny-by-default
  `lint_groups_priority`;
- `[profile.dev] debug = "line-tables-only"` (`profile.toml`) when `debug` is
  missing there;
- the record, in Cargo's reserved metadata table: `[package.metadata.workbench]`
  in a single crate, `[workspace.metadata.workbench]` in a workspace root (a
  `[workspace.*]` table in a single crate would make it a workspace root):

```toml
[workspace.metadata.workbench]
schema_version = 1
policies = ["rust-v1"]
```

Every existing value, comment and table is kept. A table added where the
manifest writes its parent as dotted keys (`package.name = "a"`, or
`metadata.docs.x = 1` under `[package]`) gets a `[header]` of its own after
them, because a header among dotted keys would take the keys after it.
Nothing is added to an inline table (`metadata = { docs = { x = 1 } }`): the
plan stops and names the key. Before anything is written, the adapter reads
the result back and stops the plan, naming the manifest, unless every
existing value is unchanged at its own key and the only new keys are the
policy's and the record's. A record whose
`schema_version` is not the integer 1 stops the plan, and so does a `[lints]`
or `[workspace.lints]` that is not a table of lint groups, such as a single
crate's `[lints] workspace = true`.

The lints are warnings: the editor and plain `cargo clippy` show them and
clippy exits 0. CI adds `-D warnings`.

Workspace lints apply only to a member that opts in. When the plan creates
`[workspace.lints]`, every member without a `[lints]` table gets `[lints]
workspace = true`, the root package from the root manifest. A member with a
`[lints]` table of its own is left alone and named. When the project already
has `[workspace.lints]`, members are never edited, and each member that does
not opt in is named: opting it in could break the project's own CI with lints
it chose not to apply.

## Whole files

Each of these is created only when the project has none, and an existing one
is kept byte for byte and listed as kept:

- `rustfmt.toml`, with `edition` and `style_edition` from `package.edition`
  or `workspace.package.edition`. Bare rustfmt formats as edition 2015 unless
  told, so `cargo fmt` and the editor agree only with these. A manifest that
  sets no edition gets no file, and the plan says why. rustfmt reads
  `rustfmt.toml` or `.rustfmt.toml` from the file's folder and each parent,
  so either one at the root or above it inside the repository counts.
- `deny.toml`, for `cargo deny check advisories bans sources`. It has no
  `[licenses]` table, in which case cargo-deny rejects every licence, so
  every command Workbench writes names the three checks and none runs bare
  `cargo deny check`. cargo-deny reads `deny.toml`, `.deny.toml` or
  `.cargo/deny.toml` from the folder it runs in and each parent, so any of
  them at the root or above counts.
- `.mbx.toml`, at the root, with `share_workspace_root = true`, so mbx reuses
  a crate compiled in one checkout in every other checkout with the same
  sources. The machine's compile budget belongs in mbx's own user config,
  which a tracked file would override.

Two candidates for one tool in one folder (`rustfmt.toml` and
`.rustfmt.toml`, or two of the cargo-deny names) stop the plan: the tool reads
only one of them.

## Toolchain

Workbench never writes `rust-toolchain.toml`. The plan warns when neither
`rust-toolchain.toml` nor `rust-toolchain` is at the root or above it inside
the repository, or when the file does not install both `rustfmt` and `clippy`
(listed in `components`, or through the `default` or `complete` profile).

## Optional changes

`--extensions` adds `rust-lang.rust-analyzer` to `.vscode/extensions.json`
with the same comment-preserving merge Python uses. `--gitignore` appends
`target/` when the line is not there yet.

`--ci` adds one job, `workbench-rust`, to the existing GitHub workflow that
has a `run` step running `cargo test` or `cargo nextest`. It needs:

- the selected folder to be the repository root (it holds `.git`) and the
  Cargo package or workspace root;
- an existing `rust-toolchain.toml` or `rust-toolchain` there, which names the
  toolchain the job installs, so Workbench picks none;
- an existing `Cargo.lock` there, which the job's `--locked` checks against;
- exactly one workflow under `.github/workflows/` with such a step. Other
  workflows, such as a release workflow, are left alone; one that is not
  valid YAML is skipped with a warning;
- that workflow to roundtrip byte for byte through the comment-aware YAML
  editor, with no anchors, aliases, merge keys, tags or directives.

Its steps:

- `actions/checkout` with `persist-credentials: false` and `fetch-depth: 0`;
- `rustup toolchain install`, which with no name installs the active
  toolchain, the one the toolchain file names;
- `cargo fmt --all --check`;
- `cargo clippy --workspace --all-targets --locked -- -D warnings`;
- the pinned cargo-deny release for `x86_64-unknown-linux-musl`, downloaded,
  checked against its SHA-256 and unpacked, then
  `cargo-deny --locked check advisories bans sources`;
- the pinned gitleaks release for `linux_x64`, downloaded, checked against its
  SHA-256, unpacked, then `gitleaks git --redact --no-banner --exit-code 1`
  over the full history.

The job reads its pins from `home/.chezmoidata/versions.toml`:
`[github_actions]` for checkout, `[cargo_tools]` for cargo-deny,
`[linux_binaries]` for gitleaks, and `[project_ci]` for each archive's
SHA-256, keyed by its version. A version with no checksum there stops the
plan. A later apply checks an existing `workbench-rust` job against this
recipe: a job that differs only in its `uses:` refs, as Dependabot leaves it,
is kept and reported, and any other difference stops the plan. Where `--ci`
cannot add the job, add these steps to the workflow by hand.

The job compiles project code, which runs its build scripts and procedural
macros, and a CI run downloads the crates' dependencies, the RustSec advisory
database and the pinned tools; the plan lists that as the
`ci-workflow-checks` effect. File recovery cannot undo a CI run.

## Recovery

Every written file, member manifests included, and a `.vscode/` folder
created for one, is part of the one project checkpoint.
`workbench project revert PATH` restores them, and a file the apply created is
removed again. A file edited after the apply is a recovery conflict.
