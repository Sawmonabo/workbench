# Workbench

One Go CLI for developer-machine configuration and tooling in existing projects.
Workbench coordinates chezmoi and native package managers; it is not a project
generator, language server, background service or whole-machine backup tool.

Releases are published on GitHub for personal use, and
[CHANGELOG.md](CHANGELOG.md) lists what each one changed. [Acceptance](docs/acceptance.md)
separates checked behavior from remaining native platform checks.

## Start here

Developers can build with the Go version in [go.mod](go.mod):

```sh
go build -o bin/workbench ./cmd/workbench
./bin/workbench --help
./bin/workbench doctor --local-build
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
workbench apply              # decide once; later runs apply your saved choices unless something is new
workbench apply --choose     # show the plan and choose again
workbench apply --dry-run    # only show it
workbench update             # install the latest release and its tools; never applies
workbench update 0.2.0       # install that release (older goes back)
workbench init --ask machine_role   # change personal/work/both, then run apply
workbench version --list     # published releases, marking yours
workbench doctor             # what is installed and applied, tools, host
workbench revert             # pick a checkpoint and restore its files
workbench costs              # what Claude Code and Codex spent, one tab per tool
workbench project configure ./apps/api --language python --dry-run
workbench project configure ./apps/api --language python
workbench project revert ./apps/api
```

Machine commands run from a verified release, or, inside the checkout, `--local-build`.
`apply` installs Workbench's own tools and asks any machine question your saved
answers lack; `--dry-run` previews only and never installs or asks. The first
`apply` shows the plan, the files with their diffs and each step in plain words:
space turns a step off, `a` applies, and your choices are remembered. After that,
`apply` prints the plan and applies your saved choices without asking, unless
a step is new or a file Workbench owns was edited outside it; `--choose` asks
again, `--reset` forgets your choices and `--yes` never asks (a coding agent is
refused it). `update` never applies. Unattended use passes the plan's exact
digest with `--approve-plan`. See [usage](docs/usage.md) for install options, recovery selection and
limits.

Python configuration supports existing uv projects/workspaces. Other languages
are detected and reported, not configured. Shared workspace owners require
selection of the workspace root; selecting a child never grants parent writes.

## Editor policy

Personal VS Code settings have one owner:
[home/.chezmoidata/vscode.json](home/.chezmoidata/vscode.json). The shared merge
edits only the values that differ and preserves comments, unrelated settings and
custom color rules. Dark 2026 remains selected;
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
`internal/{cli,operation,machine,release,project,costs}` packages, canonical machine
sources in `home/`, portable policy in `project/`. Reuse native owners and shared
operations; no competing installers, compatibility layers or migration machinery.

```sh
python3 scripts/check-action-pins.py
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

A `--local-build` developer checkout previews and applies edited `home/` files
directly; the plan binds their content digest. A release build digests its own
machine source into `release.json` and its executable, which refuses any other
source; nothing is regenerated by hand.

See the [design](docs/superpowers/specs/workbench-design.md),
[contracts](docs/superpowers/specs/workbench-contracts.md),
[implementation plan](docs/superpowers/plans/workbench-implementation.md) and
[acceptance record](docs/acceptance.md). Releases are for personal use; no
redistribution license is granted.
