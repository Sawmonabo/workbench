# Existing Python project policy

These files configure tooling in an existing uv project. They do not generate
application metadata, source code, tests, a framework, or a Git repository.

Inspect with `workbench project inspect PATH`. Preview with
`workbench project configure PATH --language python --dry-run`. The selected
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
unchanged parse/serialize cycle alters their bytes. Preview requires the separately
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

Application writes use the shared project checkpoint engine.
`workbench project revert PATH` lets you pick a checkpoint at a terminal, or
lists them for `--checkpoint UUID`, then asks you to approve its exact plan.
Later file edits cause a recovery conflict. Recovery does not invoke old project
code, reset environments or change machine configuration. Project provenance is
portable `[tool.workbench]` data with schema 1 and
`policies = ["python-v1"]`; project execution never requires Workbench.
