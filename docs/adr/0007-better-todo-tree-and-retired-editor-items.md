# 7. Better Todo Tree replaces Todo Tree; retired editor items are data

- Status: Accepted, October 13, 2026
- Supersedes: in [the design](../superpowers/specs/workbench-design.md), Todo
  Tree configuration as a deferred editor choice, and the Windows-side edit
  that pointed Todo Tree at ripgrep.

## Context

Todo Tree (`gruntfuggly.todo-tree`, last released April 2023) finds ripgrep by
looking in fixed places inside VS Code's own install. VS Code 1.141 keeps its
ripgrep under `@vscode/ripgrep-universal`, which none of those places is, so
Todo Tree stops with "Failed to find vscode-ripgrep" on every platform unless
`todo-tree.ripgrep.ripgrep` names an executable. Workbench set that path only on
WSL, to a path that exists on that one machine.

Better Todo Tree (`fanaticpythoner.better-todo-tree`) is a maintained fork that
ships its own ripgrep in a build per platform (macOS arm64 and x64, Linux x64
and arm64, Windows x64 among them), so it needs no path at all. It reads
`better-todo-tree.ripgrep.ripgrep`, else the old `todo-tree.ripgrep.ripgrep`,
and copies old `todo-tree.*` settings into its own names when it starts. When
either path is set but missing, it does not fall back to its own ripgrep. Both
are ordinary user settings, which Settings Sync carries to other computers.

## Options considered

- **A. Keep Todo Tree and set its ripgrep path on every platform** to the
  ripgrep Workbench installs. The path differs by machine (Homebrew prefix,
  `/usr/bin`, Windows), Settings Sync spreads it to machines where it is wrong,
  and Todo Tree gets no fixes for the next VS Code change.
- **B. Point Todo Tree at VS Code's own ripgrep.** That location is what just
  moved.
- **C. Switch to Better Todo Tree and remove both path settings.** Chosen. The
  extension carries everything it needs; the risks are a young project with one
  maintainer and a Marketplace listing that was pulled once without notice and
  restored.

## Decision

- `packages.toml` lists `fanaticpythoner.better-todo-tree`, and a new
  `vscode_extensions_retired` list holds `gruntfuggly.todo-tree`. The editor
  extension step removes each retired extension that is installed before it
  installs the list, since the fork reuses the original's view names. The plan
  names the retired extensions in that step's line.
- `vscode.json` sets `todo-tree.ripgrep.ripgrep` and
  `better-todo-tree.ripgrep.ripgrep` to `null`. The settings merge reads a
  managed null as "remove this setting", as a JSON merge patch (RFC 7396) does;
  removing what is not there changes nothing, and a blank or missing file stays
  so. On WSL, Windows setup takes the same null settings out of the Windows user
  `settings.json` and the WSL machine `settings.json`, the two files it edits
  without managing them, and no longer sets a path. Windows keeps its `rg.exe`,
  which the Windows PATH step puts on the PATH.
- Retiring another extension or setting is a data change: add the extension to
  `vscode_extensions_retired`, or set the setting to `null` in `vscode.json`.
  No script names a retired item.

The owner chose to remove the retired extension and settings rather than leave
them, an exception to the no-migration rule in AGENTS.md: a left-over path makes
the replacement fail.

## Consequences

- No ripgrep path is set anywhere, so nothing machine-specific syncs between
  computers.
- Workbench can no longer set a VS Code setting to `null`; none of its settings
  needs one.
- On WSL the Windows editor's extensions stay the owner's to install, as before:
  Better Todo Tree there, and Todo Tree's removal, are done by hand.
- If the fork is pulled from the Marketplace again, a new machine's extension
  step is blocked for that extension and retried on each apply; installed copies
  keep working.

## Sources

- [Todo Tree #929: Failed to find vscode-ripgrep](https://github.com/Gruntfuggly/todo-tree/issues/929)
- [Better Todo Tree](https://github.com/FanaticPythoner/better-todo-tree)
- [Better Todo Tree #142: removed from the Marketplace](https://github.com/FanaticPythoner/better-todo-tree/issues/142)
- [RFC 7396: JSON Merge Patch](https://www.rfc-editor.org/rfc/rfc7396)
