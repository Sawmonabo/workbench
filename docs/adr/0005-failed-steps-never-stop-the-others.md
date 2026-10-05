# 5. A failed setup step never stops the others

- Status: Accepted, October 5, 2026
- Supersedes: in [the contracts](../superpowers/specs/workbench-contracts.md),
  "A script that exits nonzero ends the native run".

## Context

An apply runs its setup steps as chezmoi scripts, in order. chezmoi stops at
the first script that fails, so one failed download (a VS Code extension, nvm's
installer) skipped every step after it, including ones that did not need it,
such as Homebrew cleanup or Touch ID for sudo. The apply then ended with exit 5
and a message that said only that a step failed.

## How similar tools handle it

- **[GNU make](https://www.gnu.org/software/make/manual/html_node/Errors.html)**
  with `-k` "continues to consider the other prerequisites of the pending
  targets", so independent work still runs after an error.
- **[Nix](https://nix.dev/manual/nix/latest/command-ref/opt-common.html)** with
  `--keep-going`: "if building an input of some derivation fails, Nix will still
  build the other inputs, but not the derivation itself."
- **[chezmoi](https://www.chezmoi.io/reference/command-line-flags/global/)**, the
  engine Workbench uses, has the same flag: "Keep going as far as possible after
  a encountering an error."

## Options considered

- **A. Nothing stops the apply.** Every step runs; one that needs something an
  earlier step could not install skips only that part and says what is missing.
  Chosen.
- **B. Stop only when Homebrew fails to install,** because most Mac steps need
  it. Steps that do not need it (Touch ID, tmux plugins, cost tracking) would
  still be skipped.
- **C. Keep stopping at the first failure.**

## Decision

- Workbench runs chezmoi with `--keep-going`. chezmoi does not record a
  one-time script that failed as run, so the next apply tries it again.
- Each script names what it could not do in Workbench's report file, carries on
  with the parts that do not depend on it, and ends nonzero. A part whose
  prerequisite is missing says which one, for example "nvm, uv not installed yet
  (earlier setup steps install them), so the runtimes that need them were
  skipped".
- chezmoi runs every script through a small runner Workbench writes. When a
  script ends nonzero without naming any of its steps, the runner names them
  with the exit status, so no failure goes unnamed. An interrupted script is not
  named.
- When every failed step named itself and every file is as approved, the apply
  finishes, shows those steps `blocked` with their reasons and ends with exit 3.
  Otherwise it ends with exit 5 and says the other steps ran.

## Consequences

- One failure costs only the steps that need what failed.
- Files are applied even when the packages step fails, as they already were for
  a step that failed after them.
- A step that keeps failing is reported on every apply until its cause is fixed.

## Sources

- [GNU make: Errors in Recipes](https://www.gnu.org/software/make/manual/html_node/Errors.html)
- [Nix common options](https://nix.dev/manual/nix/latest/command-ref/opt-common.html)
- [chezmoi global flags](https://www.chezmoi.io/reference/command-line-flags/global/)
