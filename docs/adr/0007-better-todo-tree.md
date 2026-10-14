# 7. Better Todo Tree replaces Todo Tree

- Status: Accepted, October 13, 2026
- Supersedes: in [the design](../superpowers/specs/workbench-design.md), Todo
  Tree configuration as a deferred editor choice, and the Windows-side edit
  that pointed Todo Tree at ripgrep.

## Context

Todo Tree (`gruntfuggly.todo-tree`, last released April 2023) finds ripgrep by
looking in fixed places inside VS Code's own install. VS Code 1.141 keeps its
ripgrep under `@vscode/ripgrep-universal`, which none of those places is, so
Todo Tree stops with "Failed to find vscode-ripgrep" on every platform unless
`todo-tree.ripgrep.ripgrep` names an executable.

Better Todo Tree (`fanaticpythoner.better-todo-tree`) is a maintained fork that
ships its own ripgrep in a build per platform (macOS arm64 and x64, Linux x64
and arm64, Windows x64 among them), so it needs no path.

## Options considered

- **A. Keep Todo Tree and set its ripgrep path on every platform** to the
  ripgrep Workbench installs. The path differs by machine, Settings Sync
  spreads it to machines where it is wrong, and Todo Tree gets no fixes for the
  next VS Code change.
- **B. Point Todo Tree at VS Code's own ripgrep.** That location is what just
  moved.
- **C. Switch to Better Todo Tree and set no path.** Chosen. The extension
  carries everything it needs; the risks are a young project with one
  maintainer and a Marketplace listing that was pulled once without notice and
  restored.

## Decision

- `packages.toml` lists `fanaticpythoner.better-todo-tree` in place of
  `gruntfuggly.todo-tree`.
- No ripgrep path is set anywhere. Windows setup on WSL no longer edits VS Code
  settings; it keeps installing `rg.exe`, which the Windows PATH step puts on
  the PATH.
- Workbench does not clean up after Todo Tree (AGENTS.md: no migrations). On a
  machine that had it, remove Todo Tree by hand. Better Todo Tree reads
  `better-todo-tree.ripgrep.ripgrep`, else the old `todo-tree.ripgrep.ripgrep`,
  and does not fall back to its own ripgrep when that path is missing, so delete
  an old `todo-tree.ripgrep.ripgrep` from VS Code settings where one was set.

## Consequences

- Nothing machine-specific about ripgrep is written or synced.
- On WSL the Windows editor's extensions stay the owner's to install, as before.
- If the fork is pulled from the Marketplace again, a new machine's extension
  step is blocked for that extension and retried on each apply; installed copies
  keep working.

## Sources

- [Todo Tree #929: Failed to find vscode-ripgrep](https://github.com/Gruntfuggly/todo-tree/issues/929)
- [Better Todo Tree](https://github.com/FanaticPythoner/better-todo-tree)
- [Better Todo Tree #142: removed from the Marketplace](https://github.com/FanaticPythoner/better-todo-tree/issues/142)
