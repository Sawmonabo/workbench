# Existing Go project policy (`go-v1`)

These files configure tooling in an existing Go module. They do not generate a
module, source code, tests or a Git repository, and planning runs no Go
command: it reads `go.mod`, `go.work` and the configuration files as text, and
never runs `go list` or `go mod download`.

Inspect with `workbench project inspect PATH`. Preview with
`workbench project apply PATH --language go --dry-run`. Without `--language`,
every supported language found is planned; `--language` only narrows.

## Which modules

- A `go.mod` at the selected folder, with no `go.work` there or in a parent
  folder up to the repository root, is configured.
- A `go.work` at or above the module blocks the Go plan: `./...` does not
  cross modules, so checks at one module would cover only part of the code.
- A `go.mod` below the selected folder is reported and left untouched; select
  its folder to configure it. When the selected folder has no `go.mod` of its
  own, the plan is blocked with that reason.
- A plan for another language (a uv project at the root, say) is never blocked
  by a Go module it does not configure; that module is named in a warning.

## Files

`golangci.yml` becomes the module's `.golangci.yml`, but only when no
golangci-lint configuration applies yet. golangci-lint reads the first
`.golangci.yml`, `.golangci.yaml`, `.golangci.toml` or `.golangci.json` it
finds in the folder it runs in and then in each parent folder, so any of
these at the module or above it inside the repository counts as the project's
own. That file is kept byte for byte and the plan says so. A kept file without
`version: "2"` is a golangci-lint v1 file, which v2 refuses; the plan says to
run `golangci-lint migrate`. A kept file that does not parse stops the plan.

The written file runs golangci-lint's `standard` set (errcheck, govet,
ineffassign, staticcheck, unused) plus errorlint, modernize, revive and
unparam, and formats with gofumpt. golangci-lint has no warning level: any
finding fails `golangci-lint run`, in the editor, a hook and CI alike. The
project owns the file from then on; Workbench never edits an existing one.

`go.mod` keeps its `go` and `toolchain` lines. govulncheck needs no file, and
the build uses Go's own cache.

`.workbench.toml` records the policies the module follows:

```toml
# Written by workbench project apply: the Workbench project policies this
# repository follows. Builds and tests never read it.
schema_version = 1
policies = ["go-v1"]
```

`go.mod` has no place for tool data, so the record is a file of its own,
written whole. A record that already lists `go-v1` is left as it is;
otherwise `go-v1` is added after the IDs it holds. A record whose
`schema_version` is not the integer 1, or that holds any other key, stops the
plan instead of being rewritten.

## Optional changes

`--extensions` adds `golang.go` to `.vscode/extensions.json` with the same
comment-preserving merge Python uses. When the `.gitignore` at the same
folder has a `.vscode/` or `.vscode/*` line and no `!.vscode/extensions.json`,
the plan still writes the file and warns that Git ignores it.

`--gitignore` appends `/bin/`, `*.test` and `/coverage.out`, each only when
the line is not there yet. A file that already has every entry is left as it
is. Otherwise a negation line other than `!.env.example` or
`!.vscode/extensions.json` stops the edit, because an appended rule could
change what it re-includes.

`--ci` adds one job, `workbench-go`, to the existing GitHub workflow that has
a `run` step running `go test` (matched as a command, so `cargo test` does
not count). It needs:

- the selected folder to be the repository root (it holds `.git`), because
  GitHub reads workflows there;
- a `go` line in `go.mod`, which the job's `actions/setup-go` reads;
- exactly one workflow under `.github/workflows/` with a `go test` step. Other
  workflows, such as a release workflow, are left alone; one that is not
  valid YAML is skipped with a warning;
- that workflow to roundtrip byte for byte through the comment-aware YAML
  editor, with no anchors, aliases, merge keys, tags or directives.

Its steps:

- `actions/checkout` with `persist-credentials: false` and `fetch-depth: 0`;
- `actions/setup-go` with `go-version-file: go.mod`;
- `golangci/golangci-lint-action` with the pinned `version` and
  `install-only: true`, then `golangci-lint run` and `go build ./...`;
- `go run golang.org/x/vuln/cmd/govulncheck@VERSION ./...`;
- the pinned gitleaks release for `linux_x64`, downloaded, checked against its
  SHA-256, unpacked, then `gitleaks git --redact --no-banner --exit-code 1`
  over the full history.

The job reads its pins from `home/.chezmoidata/versions.toml`, the same ones
the Linux machine installs: `[github_actions]` for the three actions,
`[linux_binaries]` for golangci-lint and gitleaks, `[linux_go_tools]` for
govulncheck, and `[project_ci]` for the gitleaks archive's SHA-256, keyed by
the gitleaks version. A gitleaks version with no checksum there stops the
plan. A later apply checks an existing `workbench-go` job against this
recipe: a job that differs only in its `uses:` refs, as Dependabot leaves it,
is kept and reported, and any other difference stops the plan. Where `--ci`
cannot add the job, add these steps to the workflow by hand.

The job builds and checks project code, and a CI run downloads the module's
dependencies and the pinned tools; the plan lists that as the
`ci-workflow-checks` effect. File recovery cannot undo a CI run.

`--hooks` writes `lefthook.yml` at the repository root, whole, and only when
the repository has no lefthook configuration. lefthook reads its main
configuration from `lefthook`, `.lefthook` or `.config/lefthook` with the
extension `.yml`, `.yaml`, `.json`, `.jsonc` or `.toml`, and uses the first it
finds; any of them is kept byte for byte and listed as kept, and a second one
is named. lefthook merges a `lefthook-local.*` file over the written one, and
the plan says so. The Go jobs come from `lefthook.yml` in this folder:

| Hook | Job | Staged files that run it | Command |
| --- | --- | --- | --- |
| pre-commit | `go-format` | `*.go` | `golangci-lint fmt --diff ./...` |
| pre-commit | `go-lint` | `*.go` | `golangci-lint run` |
| pre-commit | `go-dependencies` | `go.{mod,sum}` | `go mod tidy -diff` |
| pre-commit | `secrets` | any | `gitleaks git --staged --redact --no-banner` |
| pre-push | `go-test` | any | `go test ./...` |

A job with a glob runs only when a staged file matches it; lefthook's `*`
matches across folders, and a name without `*` matches at the root only.
With other languages selected, their jobs join the same file, and `secrets`
runs once, after them. `go mod tidy -diff` reads the module cache, and with an empty cache it downloads the modules, so it fails offline. Workbench never runs `lefthook install`,
which writes into `.git/hooks`: run it once per clone. The plan lists the
`git-hook-checks` effect, because once the hooks are installed every commit
and push runs these commands, which build and test project code.

`--dependabot` writes `.github/dependabot.yml`, whole, only when neither
`.github/dependabot.yml` nor `.github/dependabot.yaml` exists. It has weekly
`gomod` and `github-actions` entries at `directory: /`, plus the other
selected languages' ecosystems. An existing file is kept byte for byte, and the
plan names the ecosystems it has no entry for; a file that is not valid YAML
stops the plan. With both names, Dependabot reads `dependabot.yml`, and the
plan names the ignored `dependabot.yaml`. A
Renovate configuration (`renovate.json`, `renovate.jsonc` or `renovate.json5`
at the root or in `.github/`, `.renovaterc`, `.renovaterc.json`,
`.renovaterc.jsonc`, `.renovaterc.json5`, or a `renovate` key in the root
`package.json`) makes the plan warn that two update bots would open pull
requests for the same dependencies; the file is still written.

Each `updates` entry has one group, `minor-and-patch` (`patterns: ["*"]`,
`update-types: [minor, patch]`), so each ecosystem's minor and patch updates
arrive as one weekly pull request, and each major update as its own.
`--dependabot` also writes `.github/workflows/dependabot-auto-merge.yml`,
whole, only when no workflow of that name (`.yml` or `.yaml`) exists and no other workflow
already turns on auto-merge for Dependabot. On `pull_request`, its one job runs
only when the pull request's author is `dependabot[bot]`, reads the update
with `dependabot/fetch-metadata` (pinned by commit in `versions.toml`), and
goes on only when the `update-type` it reports, the highest change in the
pull request, is `version-update:semver-patch` or
`version-update:semver-minor`; a major update stays open. Where a branch
rule requires checks (`gh pr checks --required` lists some), it runs
`gh pr merge --auto --squash` at once and GitHub merges when they pass, also
after a check is run again; that needs "Allow auto-merge" in the repository
settings. Otherwise, or when GitHub does not take that merge, the job waits
for the pull request's other checks itself (`gh pr checks`, every 20 seconds
for up to 40 minutes) and merges only after two polls in a row show at least
one passed check and none failed, cancelled or pending. A failed check, nine
polls (about three minutes) with no other check at all, or checks that do not
finish leave the pull request open and fail the job with the reason; running
the job again, or a new push to the pull request, tries again. The merge
names the commit the run started for (`--match-head-commit`), so a commit
pushed meanwhile is never merged unchecked, and a new run for the same pull
request cancels the old one. The job asks for `contents: write` and
`pull-requests: write` to merge and `actions: read`, `checks: read` and
`statuses: read` to read the other checks, which a Dependabot-triggered run
gets only by declaring them; when the checks cannot be read, the job fails
with GitHub's error and the pull request stays open. This works on GitHub
only, and Workbench makes neither repository setting. The plan says so and
lists the `dependabot-auto-merge` effect.

Both need the selected folder to be the repository root (it holds `.git`) and
every selected project to be at it.

## Recovery

Every written file, and a `.vscode/` folder created for one, is part of the
one project checkpoint. `workbench project revert PATH` restores them, and a
file the apply created is removed again. A file edited after the apply is a
recovery conflict.
