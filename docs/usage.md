# Installation and commands

Install the latest release, or pin one, with one command. Each release carries
its own `install.sh`.

```sh
curl -fsSL https://github.com/Sawmonabo/workbench/releases/latest/download/install.sh | sh
curl -fsSL https://github.com/Sawmonabo/workbench/releases/latest/download/install.sh | sh -s -- --version v0.2.0

# With gh; this also works while the repository is private.
gh release download -R Sawmonabo/workbench -p install.sh -O - | sh
gh release download -R Sawmonabo/workbench -p install.sh -O - | sh -s -- --version v0.2.0
```

`install.sh` picks the bundle for this OS and CPU and downloads it with `gh`
when `gh` is installed and logged in, otherwise with `curl`. It extracts only the
CLI and runs `workbench update VERSION --bundle FILE`, passing every other
argument through:

- `--install-only` installs Workbench without setting up tools or applying.
- `--config-only` applies configuration without provisioning; it needs existing
  compatible tools and complete answers from `--machine-config`.
- `--dry-run` verifies the bundle and shows only the install plan.
- `--effect NAME` selects an optional WSL host step (see below).

The CLI checks every file against the SHA-256 manifest inside the bundle before
installing. That catches a truncated or corrupted download; it is not a
signature. The installer needs POSIX `sh`, `uname`, `mktemp`, `tar`, and `curl` or a
logged-in `gh`. Never run it as root.

## Commands

| Command | Behavior |
| --- | --- |
| `apply` | Show what would change on this machine, ask Yes or No, then checkpoint files and apply. |
| `apply --dry-run` | Show the same plan without applying. |
| `update` | Install the latest release, then set up its tools and apply it, asking before each step. |
| `update VERSION` | The same for that release; an older one goes back. `0.2.0` and `v0.2.0` both work. |
| `version` | Print this Workbench version, the same as `--version`. |
| `version --list` | List the published releases, marking the latest and the installed one. |
| `doctor` | What is installed and last applied, any apply that did not finish, tool versions and host checks; no repair. |
| `revert` | Pick a saved checkpoint, then restore its files after conflict checks. |
| `project inspect/configure/revert [PATH]` | Inspect or configure an existing project; see below. |

`update` and `version --list` read the releases from GitHub. While the repository
is private they need a logged-in `gh`, whose token Workbench asks `gh auth token`
for; a public repository needs nothing. When that release is already
installed, `update` skips installing and still sets up its tools and applies
it, so rerunning it finishes a setup that was declined or failed. The hidden
`--bundle FILE` takes a local archive or HTTPS URL instead, which is what
`install.sh` passes.

`revert` restores files only. It does not run an old executable or reverse old
scripts. At a terminal it shows the saved checkpoints, newest first, as "undo
the apply of …" (the files before that apply) or "redo the apply of …" (the
files a revert replaced). Without a terminal it lists them with their IDs and
asks for `--checkpoint ID`; `--dry-run` shows the restore plan.

`apply` and `doctor` take `--source PATH` to use a developer checkout instead of
the installed release. `apply` and `update` take `--machine-config PATH`, a
private native `[data]` answer file, and `--destination PATH`, an existing
folder to configure instead of your home. Full provisioning requires the real
home destination because native scripts have external effects. See
[configuration ownership](chezmoi-local-overrides.md). The hidden
`init --answers-from PATH` saves the `[data]` table of an existing chezmoi
config as machine answers; see [switching from dotfiles](switch-from-dotfiles.md).

## Approval and automation

Each planned edit carries a `summary` of what changes without showing content,
such as `+3 −1 lines` or `mode 0600 → 0644`. It also says when a target was
`edited outside Workbench since it last wrote it` or is `not previously written
by Workbench`, so an approval never overwrites local edits unnoticed.

Interactive mutations show the plan, then ask "Approve this exact plan?" with
Yes and No (y or n, or the arrow keys and Enter). No is selected first, so
Enter alone approves nothing, and esc or ctrl+c refuses. The plan lists the
files it changes, then its effects grouped by the privilege they need and what
recovery can undo, then warnings and recovery limits. `apply --dry-run` prints
the same view without asking. While Workbench plans, rechecks an approved plan or sets up
chezmoi, uv and Python, a live line on stderr names the current step and the
time so far, for example `⠧ Planning: asking Homebrew for updates (2s)`; it
clears before any prompt. Without a terminal, and in JSON or non-interactive
mode, each step prints as its own line instead. An interactive apply then hands the terminal to native
provisioning, so sudo and installers can prompt and you see their output as it
runs, and takes it back when they finish. JSON mode does not
prompt; it streams redacted provisioning output on stderr. Unattended mutation
requires complete inputs, `--non-interactive` and the exact digest from a fresh
preview:

```sh
workbench apply --config-only --dry-run --json
workbench apply --config-only --non-interactive --approve-plan PLAN_SHA256
```

Keep all source/config/destination/selection flags identical between those calls.
Changed inputs conflict instead of inheriting old consent. Release activation,
management setup and target application are separate plans: `--approve-plan`,
`--approve-setup` and `--approve-apply` approve only their respective stages.
Unattended setup also requires a complete private `--machine-config` file.
No blanket `--yes` grants unspecified external effects.

On macOS, every full plan (from `apply` or `update`) lists its
steps as effects, and approving the plan approves them. They include
`brew-maintenance`, the Homebrew cleanup dotfiles ran on every apply, and an
`update-<name>` effect for each app or command-line tool in `packages.toml`
that Homebrew reports as outdated, for example `Update docker-desktop 4.89.0 →
4.92.0`. A tool's effect also lists the other packages Homebrew would install
or update with it, for example `Update tmux 3.6a → 3.7c through Homebrew; also
installs jemalloc 5.4.0; also updates libevent 2.1.12_1 → 2.1.13`, so Homebrew
does not ask again during apply. Hold one at its version with `brew pin`
(`brew pin --cask <app>` for an app; `brew unpin` releases it). The chezmoi, uv
and Python that Workbench runs stay at the versions it qualified, and so does
any tool whose update would change them; the plan names them in its warnings.

Windows host steps on WSL run only when named with a repeatable `--effect` on
`apply` or `update`, both for the dry run and the approved run; each appears in
the plan and its digest. `workbench apply --help` lists what this host offers,
and hosts without any do not show the flag. `--effect` cannot be combined with
`--config-only`.

`--json` produces one versioned object with `schema_version`, `command`, `status`,
`results`, `warnings`, `errors`, and applicable `operation_id`/`plan_digest`.
Human results use stdout and diagnostics stderr; help/version/completion remain
text. Exit codes: 0 complete/unchanged, 1 check/execution failure, 2 invalid input
or state, 3 blocked prerequisites/support/consent, 4 conflict, 5 partial mutation,
130 interruption. Requested skipped work is not complete.
If release activation already completed before a later setup/apply failure,
the command reports partial mutation rather than implying nothing changed.

## Existing projects and monorepos

```sh
workbench project inspect .
workbench project configure ./apps/api --language python --dry-run
workbench project configure . --language python --extensions --gitignore --dry-run
workbench project configure . --language python --resolve-dependencies
workbench project revert .
workbench project revert . --checkpoint CHECKPOINT_ID
```

Only existing uv Python projects/workspaces are configurable. Inspection also
recognizes JavaScript/TypeScript, Rust and Go. Unsupported detected projects are
reported and unchanged; explicitly requesting an unsupported language blocks
before writes. Poetry/PDM/requirements-only projects are not converted.

Workspace members share their identified root lock/configuration owner. Select
that root deliberately; selecting a child cannot authorize sibling/parent edits.
Scanning excludes links, generated/dependency directories and nested repositories;
select a nested repository directly. Git ignore rules are not evaluated. Limits
are 50,000 entries, 32 levels, 1,000 candidates and a five-second cancellation
deadline; narrow the scope on incomplete results. Blocked OS calls may outlast it.

Default configuration needs an existing lockfile and compatible Ruff/basedpyright
development dependencies. `--resolve-dependencies` selects uv dependency/lock
resolution as an external effect; preview still runs none. `--allow-build-hooks`
additionally selects possible native build-code execution and requires resolution.
uv's `--no-build` is not a sandbox. Dynamic/local/direct dependencies and custom
resolver arrangements may remain blocked rather than execute unexpectedly.

`--extensions` merges portable recommendations; it does not install extensions.
`--gitignore` appends relevant absent entries while retaining pattern order.
`--ci` supports a conservative existing GitHub workflow with an explicit
`.python-version` and identifiable existing test job. It preserves that job and
adds owned Ruff/basedpyright checks. Ambiguous owners/unsupported YAML receive
a manual proposal, not a second pipeline or fabricated tests. See the
[portable policy](../project/python/README.md).

## Recovery and runtime storage

Machine and project checkpoints are separate scopes. Recovery checks every
recorded post-image before restoring anything; later user edits, corrupt images
or unknown interrupted outcomes block. Keep the reported checkpoint ID and
resolve conflicts manually; do not delete state to bypass a failure.

An apply that stops after it starts writing files is recorded as unfinished.
Rerun `workbench apply`: once a fresh approved plan finishes, the record is
cleared, including when that plan changes no files because they already match.

File recovery does not uninstall tools/extensions, undo runtime upgrades or revert
registry, environment, services, package caches or uncheckpointed script effects.
Supported bytes/types/modes/groups/link targets are recorded, along with the
macOS provenance, quarantine, Finder info and last-used-date attributes; other
extended attributes, ACLs, file flags, hard links and special files block
preservation.
Native machine apply currently rejects group-exclusive modes such as `0640` and
`0750`, avoiding transient exposure before group correction. The shared direct
project/recovery writer sets the group before permissions and atomic replacement.

Each scope retains the 20 most recent forward checkpoints plus their paired
recovery records. At the limit, the plan lists removal of the oldest settled
checkpoint for your approval; incomplete checkpoints are never removed. Applies
that change no files allocate none. Limits are 256 targets, 8 MiB per image,
32 MiB raw pre/post images per operation, 50 MiB serialized per pair and 1 GiB per
scope. Recovery journal/metadata capacity is reserved before forward writes;
reaching the forward ceiling does not prevent recovery.

Runtime directories follow platform/XDG defaults; absolute overrides are
`WORKBENCH_CONFIG_DIR`, `WORKBENCH_DATA_DIR`, `WORKBENCH_STATE_DIR`,
`WORKBENCH_CACHE_DIR`, `WORKBENCH_BIN_DIR`. Exact defaults and supported private
filesystems are in the [contracts](superpowers/specs/workbench-contracts.md#runtime-paths-and-context).
Do not share these overrides with project-owned directories or Windows mounts.

Activation is journaled, not a multi-file atomic transaction. An interrupted
entry-point/state switch fails closed while retaining the prior runtime. Rerun
`update` for the same release with fresh consent; do not manually edit
the active-release record. Existing processes retain their inherited environment:
Workbench never injects PATH changes into running terminals or AI sessions.

Updates keep storage bounded. The activation plan lists, as `remove` edits,
every staged release except the new one and the one it replaces, plus setup
contexts and private tool versions that nothing kept or recorded uses. They are deleted after activation succeeds, so the previous
release stays available to reinstall.

## Releases

Pushing a `v*` tag runs `.github/workflows/release.yml`. It builds the four
bundles with `scripts/package-release.py`, stamps the tag into a copy of
`install.sh`, and publishes all five files as a GitHub release. Build one bundle
locally with the pinned Go toolchain and Python 3.11+:

```sh
python3 scripts/package-release.py --version v0.2.0 --target darwin-arm64 --output dist
```

Targets are `darwin-arm64`, `darwin-amd64`, `linux-arm64` and `linux-amd64`.
Existing bundle names are not overwritten. The payload holds machine sources,
portable Python policy, generated requirements and linked dependency/Go license
notices, never host answers, logs, Git metadata or checkpoints. No
redistribution license is granted.
