# 9. Codex and Claude Code update through the installer that put them there

- Status: Accepted, October 16, 2026

## Context

Codex and Claude Code each ship through several channels: npm, a vendor
installer (Codex's standalone installer, Claude Code's native installer) and
Homebrew casks. The command-line tools step installed them through npm. On
macOS it did so only when they were missing, so an installed copy was never
updated; on Linux it reinstalled the pins through npm into nvm's default Node
whenever a pin changed, whatever had installed the copy the shell runs. A
vendor install earlier on PATH then stayed old, and npm added a second copy.

Each vendor documents one update path per channel, and running another one
installs a second copy rather than updating the first. Homebrew updates already
show the model to follow: one plan effect per outdated package, so approving
the plan approves the update, carried out by an apply script that runs exactly
the approved list.

## Options considered

- **A. One update effect per agent, run through the installer of the copy the
  shell runs, at the exact version the plan names.** Chosen.
- **B. Always install through npm.** Simple, but it shadows or duplicates a
  vendor install, which both vendors warn about, and npm is not the
  recommended channel for either.
- **C. Leave updates to each tool's own updater.** Claude Code's native
  install updates itself, but Codex's standalone install only updates in the
  background through its app-server daemon, Homebrew casks and npm installs do
  not update at all, and pinned mode would mean nothing.

## Decision

- The plan finds the copy the shell runs, in the order the managed shell files
  put folders on PATH (bun's globals, nvm's default Node, `~/.local/bin`, then
  Homebrew's and the system's folders), and reads how it was installed and its
  version from the installer's own files, without running it:
  - npm in an nvm Node: `.nvm/versions/node/NODE/lib/node_modules/PACKAGE`,
    the version from that package's `package.json`;
  - Codex's standalone installer:
    `.codex/packages/standalone/releases/RELEASE`, the version from its
    `codex-package.json`, or else the release name;
  - Claude Code's native installer: `.local/share/claude/versions/VERSION`;
  - a Homebrew cask (`codex`, `claude-code`, `claude-code@latest`).
- The target is the `versions.toml` pin in pinned mode, and in latest mode the
  newest release on the channel that installer installs `latest` from: the npm
  registry's `latest` tag, `releases.openai.com/codex/channels/latest`, or
  `downloads.claude.ai/claude-code-releases/latest`. Planning looks it up with
  a 10 second limit, as the runtimes probe looks up the newest Node; a failed
  lookup only warns. A copy as new as the target, or newer, is left alone, so
  nothing is downgraded; a version that is not three dotted numbers is not
  compared and is named in a warning.
- An older copy gets one `update-codex` or `update-claude-code` effect, its
  description naming both versions and the installer, so the plan digest binds
  the target. The `45-agent-updates` script, which runs on every apply on
  macOS, Linux and WSL, runs exactly the approved updates:
  - npm: `npm install -g PACKAGE@VERSION` with that Node's `npm`, the upgrade
    command both vendors document;
  - Codex standalone: `curl -fsSL https://chatgpt.com/codex/install.sh |
    CODEX_NON_INTERACTIVE=1 sh -s -- --release VERSION`, Codex's own update
    command plus the installer's `--release`;
  - Claude Code native: `claude install VERSION`, the native installer's own
    command, which its `install.sh` runs after downloading.
  A failed update is blocked on its own effect; the others still run.
- On macOS a Homebrew copy joins the Homebrew updates and is upgraded with
  `brew upgrade --cask`, at Homebrew's current version, since a cask installs
  no other. Homebrew is not run on Linux, so a Homebrew copy there is named
  in a warning, like any copy installed some other way, and left alone.
- The command-line tools step installs Codex and Claude Code through npm only
  when the command is missing, on every platform.

## Consequences

- An install of a set Codex release removes the standalone package's
  `auto-update-version` marker, which turns off its background updates until
  an install of the latest release; the effect says so.
- Claude Code's native install keeps updating itself in the background unless
  its own settings turn that off, so in pinned mode it can run ahead of the
  pin, which the plan then leaves alone.
- In latest mode a release published between `apply --dry-run` and
  `apply --approve-plan` changes the plan, and the approval is refused with
  exit 4.

## Sources

- [Claude Code setup: install, update, release channels, npm, Homebrew](https://code.claude.com/docs/en/setup)
- [Claude Code native installer script](https://claude.ai/install.sh)
- [Codex README: installation](https://github.com/openai/codex/blob/main/README.md)
- [Codex standalone installer: `--release`, `CODEX_NON_INTERACTIVE`, `auto-update-version`](https://github.com/openai/codex/blob/main/scripts/install/install.sh)
- [Codex update commands per install method](https://github.com/openai/codex/blob/main/codex-rs/tui/src/update_action.rs)
- [Codex install-method detection](https://github.com/openai/codex/blob/main/codex-rs/install-context/src/lib.rs)
- [Codex CLI documentation: install and update](https://learn.chatgpt.com/docs/codex/cli)
- [npm registry: dist-tags](https://docs.npmjs.com/cli/commands/npm-dist-tag)
- [Homebrew manual: `brew upgrade`, `brew outdated`](https://docs.brew.sh/Manpage)
