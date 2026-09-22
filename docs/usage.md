# Installation and commands

Workbench currently supports explicitly trusted **private evaluation bundles**,
not a published release channel. Production qualification is tracked in
[acceptance](acceptance.md). Run installation/provisioning evaluation in a
disposable user or VM, not an ordinary development account.

## Evaluation installation

An operator supplies a bundle, its standalone executable and trusted SHA-256
values. Check the expected operating system/architecture and inspect `install.sh`
before running it. A checksum downloaded with an artifact detects corruption;
it does not independently establish publisher authenticity.

For example, with reviewed local files and real trusted hashes substituted:

```sh
WORKBENCH_BOOTSTRAP_FILE=./workbench-evaluation-darwin-arm64 \
WORKBENCH_BOOTSTRAP_SHA256=TRUSTED_EXECUTABLE_SHA256 \
sh install.sh --bundle ./workbench-evaluation-darwin-arm64.tar.gz \
  --sha256 TRUSTED_BUNDLE_SHA256 --evaluation --install-only
```

`--install-only` installs the matched CLI/source release without management setup
or configuration application. Omit it to continue through separately approved
setup and machine plans. `--config-only` skips prerequisite acquisition and
provisioning; it therefore requires existing compatible tools and complete answers.

For authenticated assets, use `WORKBENCH_BOOTSTRAP_URL` instead of the local file
and provide `WORKBENCH_GITHUB_TOKEN` through the environment. Credentials are
accepted only for the Workbench GitHub release-asset API endpoint
`https://api.github.com/repos/Sawmonabo/workbench/releases/assets/ID`.
Never put the token in command arguments or commit it. No assets are published
at this time. There is no anonymous private-repository installation promise.

The bootstrap requires POSIX `sh`, `uname`, `mktemp`, `mkdir`, `chmod`, HTTPS
`curl`, `rm`, `rmdir`, and `sha256sum` or `shasum`; local input also needs `cp`.
Go handles archive verification/extraction, so users need neither Go, Python nor
`tar` for runtime-only installation. Approved management setup can acquire its
own qualified tools. Never run the entire installer as root.

`sh install.sh --dry-run` performs only bootstrap target/prerequisite inspection.
It downloads nothing and cannot preview machine configuration. By contrast,
the CLI's release `--dry-run` verifies the supplied bundle (including a download
when an HTTPS location is selected) and previews staging/activation only.

## Machine lifecycle

| Command | Behavior |
| --- | --- |
| `doctor` | Bounded local tool/host checks; no repair, server startup or remote sessions. |
| `status` | Inspect private current-state identities; not proof of package health or a drift scan. |
| `pull [version] --bundle FILE --sha256 HASH` | Verify and stage only; does not activate or configure. |
| `install [version] --bundle FILE --sha256 HASH --evaluation` | Activate the matched runtime/source, then separate setup and apply stages. |
| `update [version] --bundle FILE --sha256 HASH --evaluation` | Compose the same stage/activate/setup/plan/apply owners. |
| `plan [--config-only]` | Offline native target plan; missing render dependencies/answers block. |
| `apply --dry-run [--config-only]` | Same planner as `plan`, no provisioning. |
| `apply [--config-only]` | Revalidate the approved plan, checkpoint files and invoke native apply. |
| `revert --list` | List machine checkpoints. |
| `revert --checkpoint ID [--dry-run]` | Preview/restore a selected checkpoint after conflict checks. |

`revert VERSION` selects a unique checkpoint whose **before** release matches;
ambiguous matches fail. With no selector, revert lists choices and requests an
explicit checkpoint. It does not run an old executable or reverse old scripts.

There is no automatic “latest” channel yet: release commands require `--bundle`
and an operator-trusted `--sha256`. Bundle URLs may use the authenticated API
endpoint above. Pull also requires plan consent because staging writes state.

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

Interactive mutations show the plan and read approval from the terminal. JSON
mode does not prompt. Unattended mutation requires complete inputs,
`--non-interactive` and the exact digest from a fresh preview:

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
Supported bytes/types/modes/groups/link targets and macOS provenance are recorded;
unsupported ACLs, metadata, hard links and special files block preservation.
Native machine apply currently rejects group-exclusive modes such as `0640` and
`0750`, avoiding transient exposure before group correction. The shared direct
project/recovery writer sets the group before permissions and atomic replacement.

Each scope retains at most 20 forward checkpoints plus their paired recovery
records, without automatic pruning. Limits are 256 targets, 8 MiB per image,
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

## Maintainer bundles

Packaging requires the pinned Go toolchain and Python 3.11+ on the build host:

```sh
python3 scripts/package-release.py --version evaluation --target darwin-arm64 --output dist
```

Choose the actual target from `darwin-arm64`, `darwin-amd64`, `linux-arm64`,
`linux-amd64`. Output contains the bundle, standalone executable and checksum
files; existing immutable bundle names are not overwritten. The explicit payload
includes machine sources, portable Python policy, generated requirements and
linked dependency/Go license notices—not host answers, logs, Git metadata or
checkpoints. Inspect payloads and run the existing secret scanner before sharing.

`.github/workflows/release-bundles.yml` produces private, short-retention CI
artifacts only; it does not publish releases or sign them. These artifacts do not
grant a Workbench redistribution license or establish an independent trust root.
