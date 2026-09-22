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
CLI and runs `workbench install VERSION --bundle FILE`, passing every other
argument through:

- `--install-only` installs the CLI and sources without setup or configuration.
- `--config-only` applies configuration without provisioning; it needs existing
  compatible tools and complete answers from `--machine-config`.
- `--dry-run` verifies the bundle and shows only the staging/activation plan.
- `--effect NAME` selects an optional provisioning step (see below).

The CLI checks every file against the SHA-256 manifest inside the bundle before
installing. That catches a truncated or corrupted download; it is not a
signature. The installer needs POSIX `sh`, `uname`, `mktemp`, `tar`, and `curl` or a
logged-in `gh`. Never run it as root.

To update, rerun the one-liner, optionally with `--version`. `workbench install`
and `workbench update` also accept `--bundle` with a local archive or HTTPS URL.

## Machine lifecycle

| Command | Behavior |
| --- | --- |
| `doctor` | Bounded local tool/host checks; no repair, server startup or remote sessions. |
| `status` | Inspect private current-state identities; not proof of package health or a drift scan. |
| `init --answers-from PATH` | Save the `[data]` table of an existing chezmoi config as machine answers; see [switching from dotfiles](switch-from-dotfiles.md). |
| `pull [version] --bundle FILE` | Verify and stage only; does not activate or configure. |
| `install [version] --bundle FILE` | Activate the matched runtime/source, then separate setup and apply stages. |
| `update [version] --bundle FILE` | Compose the same stage/activate/setup/plan/apply owners. |
| `plan [--config-only] [--effect NAME]` | Offline native target plan; missing render dependencies/answers block. |
| `apply --dry-run [--config-only] [--effect NAME]` | Same planner as `plan`, no provisioning. |
| `apply [--config-only] [--effect NAME]` | Revalidate the approved plan, checkpoint files and invoke native apply. |
| `revert --list` | List machine checkpoints. |
| `revert --checkpoint ID [--dry-run]` | Preview/restore a selected checkpoint after conflict checks. |

`revert VERSION` selects a unique checkpoint whose **before** release matches;
ambiguous matches fail. With no selector, revert lists choices and requests an
explicit checkpoint. It does not run an old executable or reverse old scripts.

Release commands take `--bundle` with a local archive or HTTPS URL; `install.sh`
finds the right one for you. Pull also requires plan consent because staging
writes state.

After pull, plan and apply use the candidate's verified executable/source pair.
Preview does not activate it. The apply plan explicitly includes any runtime
switch; approval activates that pair through the same journaled installer owner.
A CLI-only update can activate without allocating a configuration checkpoint.

`--source PATH` selects an existing developer source tree;
`--machine-config PATH` selects a private native `[data]` answer file;
`--destination PATH` selects an existing configuration destination. Full
provisioning requires the real home destination because native scripts have
external effects. See [configuration ownership](chezmoi-local-overrides.md).

## Approval and automation

Each planned edit carries a `summary` of what changes without showing content,
such as `+3 −1 lines` or `mode 0600 → 0644`. It also says when a target was
`edited outside Workbench since it last wrote it` or is `not previously written
by Workbench`, so an approval never overwrites local edits unnoticed.

Interactive mutations show the plan and read approval from the terminal. An
interactive apply then hands the terminal to native provisioning, so sudo and
installers can prompt and you see their output as it runs. JSON mode does not
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

Optional provisioning steps run only when named with a repeatable `--effect`
on both `plan` and `apply` (and `install`/`update`); each appears in the plan and
its digest. macOS offers `brew-maintenance` (tap/cleanup hygiene that dotfiles
ran on every apply) and `app-replacement` (adopt hand-installed apps that casks
cover). `workbench plan --help` lists what this host offers. `--effect` cannot
be combined with `--config-only`.

```sh
workbench plan --effect brew-maintenance
workbench apply --effect brew-maintenance
```

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
workbench project revert . --list
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
entry-point/state switch fails closed while retaining the prior runtime. Resume
`install` with the same trusted bundle and fresh consent; do not manually edit
the active-release record. Existing processes retain their inherited environment:
Workbench never injects PATH changes into running terminals or AI sessions.

Installs keep storage bounded. The activation plan lists, as `remove` edits,
every staged release except the new one, the one it replaces and a staged
candidate, plus setup contexts and private tool versions that nothing kept or
recorded uses. They are deleted after activation succeeds, so the previous
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
