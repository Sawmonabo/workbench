# Existing Python project policy

These files configure tooling in an existing uv project. They do not generate
application metadata, source code, tests, a framework, or a Git repository.

Inspect with `workbench project inspect PATH`. Preview with
`workbench project apply PATH --language python --dry-run`. The selected
directory must include every affected workspace owner and shared lockfile.
Selecting a workspace child never authorizes writes to its parent. Other detected
languages remain visible and unchanged; explicitly selecting an unsupported
language blocks the plan.

`policy.toml` adds missing tool settings while retaining existing scalars, rule
lists, dependencies, Python constraints and build metadata. Existing standalone
Ruff, ty, basedpyright or uv configuration, a `ruff.toml` or `.ruff.toml` in a
folder below the project root (Ruff applies the nearest file there), and an
existing `[tool.pyright]` table (basedpyright rejects a file that has both),
require manual ownership review.
Native Ruff rule selections at either `tool.ruff` or `tool.ruff.lint` are retained;
`tool.ruff.extend` requires explicit inherited-policy ownership review.
Unsupported syntax or competing ownership stops the operation. The private,
pinned TOML Kit adapter preserves comments/order and rejects documents whose
unchanged parse/serialize cycle alters their bytes. A table added under dotted
keys (`tool.ruff.line-length = 100`) gets a `[header]` of its own, nothing is
added to an inline table, and a merged `pyproject.toml` that does not read back
with every existing value at its own key stops the plan and names the file. Preview requires the separately
approved management setup; it never acquires dependencies.

The required command-line checks are Ruff lint, Ruff formatting verification and
basedpyright. Existing versions in the default development dependency group are
preserved. When those dependencies are missing, select `--resolve-dependencies`
and approve the displayed external effects. uv resolves missing tools in private
metadata staging, using compatible native dependency requirements. Workbench
checkpoints exact output files before writing the project. It does not synchronize
the project environment. Network requests, downloaded metadata and any expressly
approved native execution cannot be undone by file recovery. `--no-build` is not
a sandbox: dynamic/local dependency cases need additional review and explicit
`--allow-build-hooks`; metadata-only staging may still reject unsupported layouts.

`--extensions` optionally merges `extensions.json` using a comment-preserving JSONC
editor. Contradictory recommendations or duplicate JSONC keys stop the merge.
Recommendations neither install nor disable extensions. `--gitignore` appends
only relevant Python environment/cache entries; the full `gitignore.entries`
file also provides optional test/build/coverage/secret-file examples to review.
Existing patterns and negations remain ordered; ambiguous negations stop editing.
Personal editor settings never enter a project.

`--ci` supports one existing GitHub workflow at the selected Python workspace
root, an existing explicit `.python-version`, and an existing `test`/`tests` job
whose pytest, unittest, tox or nox command can be identified. It adds a separate
owned check job to that workflow while preserving the actual test job. It does
not select a test runner or change the workflow's triggers. Its job explicitly
uses bash and the checkout root, independent of workflow-level run defaults.
The comment-aware
YAML editor rejects syntax that cannot roundtrip exactly, duplicate keys,
multiple documents, aliases, merge keys, custom tags and conflicting owned jobs.

`checks.example.yml` is an intentionally incomplete GitHub Actions example for
manual integration of other layouts. Merge appropriate steps into the workflow,
select a supported project Python version, and replace its failing final step
with the project's actual test command. Do not invent pytest ownership. CI setup
and test execution may run project code and require normal repository review.
Action pins live in `home/.chezmoidata/versions.toml` under `[github_actions]`;
the generated job reads them, and `scripts/check-action-pins.py` refuses any
workflow or example pin that differs. The commits were checked against upstream
tags on 2026-09-22:
[checkout v7.0.1](https://github.com/actions/checkout/releases/tag/v7.0.1) and
[setup-uv v10.1.0](https://github.com/astral-sh/setup-uv/releases/tag/v10.1.0).
Unsupported `--ci` requests report the precise ownership/preservation gate without
writing a duplicate workflow or changing providers/branch protection.

`--hooks` writes `lefthook.yml` at the repository root, whole, and only when
the repository has no lefthook configuration. lefthook reads its main
configuration from `lefthook`, `.lefthook` or `.config/lefthook` with the
extension `.yml`, `.yaml`, `.json`, `.jsonc` or `.toml`, and uses the first it
finds; any of them is kept byte for byte and listed as kept, and a second one
is named. lefthook merges a `lefthook-local.*` file over the written one, and
the plan says so. The Python jobs come from `lefthook.yml` in this folder:

| Hook | Job | Staged files that run it | Command |
| --- | --- | --- | --- |
| pre-commit | `python-format` | `*.py` | `uv run --locked ruff format --check` |
| pre-commit | `python-lint` | `*.py` | `uv run --locked ruff check` |
| pre-commit | `python-dependencies` | `{pyproject.toml,uv.lock,*/pyproject.toml}` | `uv lock --check` |
| pre-commit | `secrets` | any | `gitleaks git --staged --redact --no-banner` |
| pre-push | `python-test` | any | `uv run --locked pytest` |

A job with a glob runs only when a staged file matches it; lefthook's `*`
matches across folders, and a name without `*` matches at the root only.
With other languages selected, their jobs join the same file, and `secrets`
runs once, after them. The `python-test` job is written only when the dev group (with the groups it includes) lists pytest; otherwise the file has no pre-push hook and the plan says so, because Workbench invents no tests. `uv lock --check` passes offline while the manifests match the lockfile, and needs the package index once a manifest changes. Workbench never runs `lefthook install`,
which writes into `.git/hooks`: run it once per clone. The plan lists the
`git-hook-checks` effect, because once the hooks are installed every commit
and push runs these commands, which build and test project code.

`--dependabot` writes `.github/dependabot.yml`, whole, only when neither
`.github/dependabot.yml` nor `.github/dependabot.yaml` exists. It has weekly
`uv` and `github-actions` entries at `directory: /`, plus the other
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
request cancels the old one. When the job's own command merged the pull
request, it deletes the update's branch, which GitHub's "Automatically delete
head branches" was seen not to do for a merge made with the workflow token; a
pull request GitHub merges later by auto-merge leaves its branch to that
setting. The job asks for `contents: write` and
`pull-requests: write` to merge and `actions: read`, `checks: read` and
`statuses: read` to read the other checks, which a Dependabot-triggered run
gets only by declaring them; when the checks cannot be read, the job fails
with GitHub's error and the pull request stays open. This works on GitHub
only, and Workbench makes neither repository setting. The plan says so and
lists the `dependabot-auto-merge` effect.

Both need the selected folder to be the repository root (it holds `.git`) and
every selected project to be at it.

Application writes use the shared project checkpoint engine.
`workbench project revert PATH` lets you pick a checkpoint at a terminal, or
lists them for `--checkpoint UUID`, then asks you to approve its exact plan.
Later file edits cause a recovery conflict. Recovery does not invoke old project
code, reset environments or change machine configuration. Project provenance is
portable `[tool.workbench]` data with schema 1 and
`policies = ["python-v1"]`; project execution never requires Workbench.
