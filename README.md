# Workbench

One Go CLI for developer-machine configuration and tooling in existing projects.
Workbench coordinates chezmoi and native package managers; it is not a project
generator, language server, background service or whole-machine backup tool.

Releases are published on GitHub for personal use. [Acceptance](docs/acceptance.md)
separates checked behavior from remaining native platform checks.

## Start here

Developers can build with the Go version in [go.mod](go.mod):

```sh
go build -o bin/workbench ./cmd/workbench
./bin/workbench --help
./bin/workbench doctor
./bin/workbench project inspect .
```

Go may download modules and update its build cache. Doctor and project inspection
do not install or repair anything. A missing prerequisite returns nonzero.

Release users need no checkout, Go, or separately installed chezmoi:

```sh
curl -fsSL https://github.com/Sawmonabo/workbench/releases/latest/download/install.sh | sh
```

See [installation and commands](docs/usage.md) for `--version`, the `gh` form
and install options, and [switch from dotfiles](docs/switch-from-dotfiles.md)
to move a machine over.

## Daily commands

```sh
workbench plan --config-only
workbench apply --config-only --dry-run
workbench apply --config-only
workbench revert --list
workbench project inspect ./apps/api
workbench project configure ./apps/api --language python --dry-run
workbench project configure ./apps/api --language python
workbench project revert ./apps/api --list
```

Machine commands require a verified source and complete private machine answers.
Mutations display a plan and require approval; unattended use requires its exact
digest. Missing prerequisites block preview rather than trigger installation.
`pull` stages a verified bundle; `install` and `update` reuse the same activation,
setup and apply operations. See [usage](docs/usage.md) for install options,
separate setup consent, recovery selection and limits.

Python configuration supports existing uv projects/workspaces. Other languages
are detected and reported, not configured. Shared workspace owners require
selection of the workspace root; selecting a child never grants parent writes.

## Editor policy

Personal VS Code settings have one owner:
[home/.chezmoidata/vscode.json](home/.chezmoidata/vscode.json). The shared merge
preserves unrelated settings and custom color rules. Dark 2026 remains selected;
TypeScript properties use light blue, readonly properties a distinct blue.

Python uses ty for editor features, native Ruff for lint/format/imports, and
basedpyright for on-demand/CI checking—not a second always-running editor server.
Project preferences stay portable and separate from personal theme/layout data.
Removing an extension from the desired list does not uninstall it.

## Safety and support

Checkpoint recovery protects recorded files, not packages, extensions, runtime
upgrades or arbitrary external effects. Later edits block recovery before any
restore. Unsupported state is rejected without conversion or deletion.

Targets are macOS Apple Silicon/Intel, Ubuntu amd64/arm64 and WSL2 on Windows x64.
Native qualification is incomplete; Windows-host integration has not run on a
real host yet.
Read the [macOS](docs/macos.md), [Linux](docs/linux.md) and [WSL](docs/wsl.md)
notes before evaluating provisioning. Never use a live development machine for
acceptance checks.

## Development

One Go module, thin `cmd/workbench`, focused
`internal/{cli,operation,machine,release,project}` packages, canonical machine
sources in `home/`, portable policy in `project/`. Reuse native owners and shared
operations; no competing installers, compatibility layers or migration machinery.

```sh
python3 scripts/generate-source-trust.py --check
golangci-lint fmt
golangci-lint config verify
golangci-lint run ./...
go build ./...
go test ./...
scripts/render-check.sh personal pinned
```

Use [.golangci-lint-version](.golangci-lint-version). Automated tests stay near
zero: only explicit catastrophic data-loss, credential-exposure or unauthorized
execution safeguards. Routine checks use formatting, lint, builds and brief
disposable smoke checks. No coverage target or new test framework.

Machine-source changes require regenerating the compiled trust record with
`python3 scripts/generate-source-trust.py` and rebuilding before preview/apply.
This binds executable render behavior to reviewed source content.

See the [design](docs/superpowers/specs/workbench-design.md),
[contracts](docs/superpowers/specs/workbench-contracts.md),
[implementation plan](docs/superpowers/plans/workbench-implementation.md) and
[acceptance record](docs/acceptance.md). Releases are for personal use; no
redistribution license is granted.
