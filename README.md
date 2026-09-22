# Workbench

A developer-machine and project-tooling CLI, under development. The current
increment provides read-only inventories. It does not install tools, configure
projects, apply machine settings, or manage releases yet.

## Run from source

Use the Go version declared in [go.mod](go.mod). From the repository root:

```sh
go run ./cmd/workbench --help
go run ./cmd/workbench doctor
go run ./cmd/workbench status
go run ./cmd/workbench project inspect .
go run ./cmd/workbench project inspect ./internal
```

Go may download module dependencies and write its build cache. The resulting
Workbench executable itself does not make network requests, execute subprocesses
or modify files in this increment. To build it locally:

```sh
go build -o bin/workbench ./cmd/workbench
./bin/workbench --help
```

There is no published release or working one-line installer. Do not use the
machine provisioning sources as an unattended installation path; their safety
corrections are still planned.

## Available behavior

| Command | Current behavior |
| --- | --- |
| `doctor` | Finds chezmoi, uv, Python and VS Code command locations outside project-controlled PATH entries without executing them. Missing tools return nonzero. Versions, machine answers and editor profiles are not validated. |
| `status` | Shows CLI build version and validates existing private current-state metadata. Release activation and drift inspection remain unavailable. It does not claim the machine is configured. |
| `project inspect [PATH]` | Lists manifest, workspace-marker, lockfile, checksum and selected Python-config filenames under an existing directory. Defaults to `.`. No file contents are parsed. |
| `--help`, `--version`, `completion` | Cobra help, build version and shell completion output. Completion output is not installed automatically. |

`pull`, `plan`, `apply`, `update`, `revert` and `project configure` are reserved
commands that return an explicit not-implemented error without changing anything.
This includes their dry-run forms: no planner exists yet. `project configure`
accepts repeatable `--language` flags, but no language has configuration support.

Successful inventories return 0; check failures return 1; invalid inputs or
unsupported current-state formats return 2; missing prerequisites and unavailable
operations return 3. The shared operation layer reserves 4 for conflicts, 5 for
partial mutations, and 130 for interruption. Mutation commands remain unavailable.

Use `--json` for one result object with `schema_version`, `command`, `status`,
`results`, `warnings` and `errors`. Normal output sends results to stdout and
diagnostics to stderr. Help, version and shell completion retain Cobra's text output.

`--source`, `--machine-config` and `--destination` resolve explicit existing paths.
Machine answer files must be private regular files. Runtime directory overrides
are `WORKBENCH_CONFIG_DIR`, `WORKBENCH_DATA_DIR`, `WORKBENCH_STATE_DIR`,
`WORKBENCH_CACHE_DIR` and `WORKBENCH_BIN_DIR`; each must be absolute. See the
[runtime contracts](docs/superpowers/specs/workbench-contracts.md#runtime-paths-and-context)
for XDG/platform defaults. Inventories never create these directories or answers.
Unsupported or malformed existing state fails without conversion or deletion.
Private runtime storage requires APFS/HFS on macOS or ext/XFS/Btrfs/tmpfs/overlay
on Linux; Windows, network and other unqualified filesystems are blocked. This
keeps WSL private state on the Linux side; it does not certify Windows target ACLs.

`--non-interactive` and `--approve-plan SHA256` expose the consent vocabulary for
future mutation handlers; they do not enable unfinished commands. The shared
guard binds approval to a plan rechecked under shared and scope locks. Native
planners, checkpoints and release activation still need their own implementation.

### Inspection boundaries

Inspection recognizes Python, JavaScript/TypeScript, Rust and Go manifest names.
These are candidates, not validated projects. It does not yet resolve workspace
members, package-manager ownership, enclosing projects, shared lockfiles or Git
ignore rules. It cannot be used as an apply/configure plan.

The scanner skips hidden directories, dependency/build/cache directories,
symlinks, special files and nested repositories. Nested repositories are reported
as boundaries; inspect one directly to select it. It uses directory-scoped Go
filesystem handles and never reads candidate contents. Output paths are quoted.

Each scan is limited to 50,000 directory entries, 32 levels, 1,000 candidates and
a five-second cancellation deadline. These are conservative operational limits,
not benchmark claims. Filesystem calls already blocked inside the OS may outlast
the deadline. Hitting a limit returns a partial inventory and a nonzero exit;
select a narrower path. An empty inventory does not establish that a directory
has no projects outside these discovery rules.

## Development

One Go module, one executable, and focused `internal` packages. Cobra is the only
direct Go dependency. Existing native provisioning remains under `home/`; the
CLI does not duplicate or invoke it yet.

Use the golangci-lint release in [.golangci-lint-version](.golangci-lint-version).
The shared [.golangci.yml](.golangci.yml) enables gofmt, goimports and the standard
linter set:

```sh
golangci-lint fmt
golangci-lint config verify
golangci-lint run ./...
go build ./...
```

CI uses the same Go/linter pins and compiles macOS/Linux arm64 and amd64 targets.
Cross-compilation is not proof of native provisioning or Windows integration.
Existing machine render/lint/leak checks remain separate and unchanged.

Keep automated tests close to none: add one only for a concrete catastrophic
data-loss, credential-exposure or unauthorized-execution risk. Use static checks
and brief manual smoke checks for routine features. There is no coverage target,
test framework or backward-compatibility/migration layer.

## Structure and next steps

- `cmd/workbench/`: executable entry point.
- `internal/cli/`: command presentation and read-only diagnostics.
- `internal/project/`: bounded filename inventory.
- `internal/operation/`: resolved context, results, consent, bounded subprocesses,
  private state writes and locks; these primitives do not enable provisioning.
- `home/`: canonical chezmoi configuration and existing provisioning owners.
- `scripts/`: existing render validation; not an alternate Workbench CLI.
- `docs/superpowers/`: maintained product design and implementation plan.

See the [design](docs/superpowers/specs/workbench-design.md) and
[implementation plan](docs/superpowers/plans/workbench-implementation.md) for
remaining work. Before file-changing commands ship, resolve configuration
adoption, consent, dependency ownership, checkpoint recovery and release trust.
No compatibility with earlier development versions is promised; user-owned data
must still be preserved.
