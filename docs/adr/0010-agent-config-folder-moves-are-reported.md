# 10. Report an agent config folder moved by its variable

- Status: Accepted, October 16, 2026

## Context

Workbench installs Claude Code's settings (its enforced keys, the
`minimumVersion` floor, the costs and guard hooks), instructions and status
line in `~/.claude`, and Codex's config, hooks and instructions in `~/.codex`,
through chezmoi (`home/private_dot_claude`, `home/private_dot_codex`).

Both tools can be moved. Claude Code reads `CLAUDE_CONFIG_DIR`: "Override the
configuration directory (default: `~/.claude`). All settings, session history,
and plugins are stored under this path", set "in your shell, user settings, or
managed settings", ignored in project and local settings, and "useful for
running multiple accounts side by side", for example
`alias claude-work='CLAUDE_CONFIG_DIR=~/.claude-work claude'`. A settings
file's `env` replaces the shell's value. With
`CLAUDE_CODE_SUBPROCESS_ENV_SCRUB` set, hooks run without the variable. Codex
reads `CODEX_HOME`, "the root for Codex state, including config, auth, logs,
sessions, skills".

With either variable pointing elsewhere, the tool reads none of what Workbench
installs, while the plan listed the files as installed. `workbench costs`
already followed both variables, and `costs status` then reported the hooks
missing with `(workbench apply)`, which would not install them where the tool
looks. Workbench's managed shell files set neither variable.

## How similar tools handle it

- **[chezmoi](https://www.chezmoi.io/user-guide/frequently-asked-questions/design/)**
  "is designed to operate on your home directory": a target's path comes from
  its source name, `destDir` moves the whole tree, and a file outside it needs a
  `run_` script or a symlink, which it discourages. A location that differs
  per machine is a separate source entry chosen by `.chezmoiignore`
  ([machine differences](https://www.chezmoi.io/user-guide/manage-machine-to-machine-differences/)),
  which only works for locations known in advance.

## Options considered

- **A. Follow the variable.** Install into whatever folder the variable names.
  chezmoi cannot target a folder chosen at run time without a `run_` script
  writing files outside its diff, a second renderer the plan would not show.
  The variable is also often set per session for a second account, so there
  can be two folders and Workbench would pick one account for the user.
- **B. Refuse to plan.** Blocks every other setup step for a documented,
  intended configuration.
- **C. Install in the default folder and report the move plainly.**

## Decision

C. Workbench resolves each tool's folder as its documentation describes:
`CODEX_HOME` from the environment; `CLAUDE_CONFIG_DIR` from the environment,
replaced by the `env` of the user settings in that folder, replaced by the
managed settings files. When the folder is not `~/.claude` or `~/.codex`
(compared as the same file when both exist), the plan for the real home warns,
`doctor` reports a `claude-config-dir` or `codex-home` conflict, and
`costs status` checks the hooks in the folder the tool reads and adds a `config` line
instead of the `(workbench apply)` hint. Each names the variable, where it is
set, the folder, and the fix: unset it or point it at the default folder, or
remove it from the settings file that sets it. Claude Code copies a settings
file's value as written, so a path there must be absolute; a value that is not
(`~/x`, `x`) names a folder that depends on where Claude Code starts, so
Workbench ignores it and names it in the plan, `doctor` and `costs status`,
asking for the absolute path. While the folder is moved, the costs ingest also
reads `~/.claude/projects`, which can still hold transcripts written before
the move; the ledger keys each response by its request ID, so one read from
both folders counts once. The costs ingest hook, given a Claude Code
transcript under no folder Workbench resolves, while `CLAUDE_CONFIG_DIR` is
unset, gives its worker the folder the transcript is in, so usage from a
scrubbed hook is still recorded.

## Consequences

- Workbench never writes into a moved folder; a user who wants its settings
  there copies or links them.
- A variable set only for one command, such as through the alias, is not seen
  by Workbench at plan time; the costs hooks still record that session's usage,
  as the hook runs with the session's environment.
- Claude Code settings delivered by MDM or the server, which Workbench cannot
  read, are not consulted.

## Sources

- [Claude Code environment variables: `CLAUDE_CONFIG_DIR`, settings-file `env`, subprocess scrub](https://code.claude.com/docs/en/env-vars)
- [Claude Code settings files](https://code.claude.com/docs/en/settings)
- [Codex environment variables: `CODEX_HOME`](https://learn.chatgpt.com/docs/config-file/environment-variables)
- [chezmoi design FAQ](https://www.chezmoi.io/user-guide/frequently-asked-questions/design/)
- [chezmoi: manage machine-to-machine differences](https://www.chezmoi.io/user-guide/manage-machine-to-machine-differences/)
