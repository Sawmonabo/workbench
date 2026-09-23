# macOS notes

Target: macOS 15+ on Apple Silicon and Intel. Isolated macOS arm64 checks exist;
complete disposable-user provisioning and Intel/minimum-OS qualification remain
open. See [acceptance](acceptance.md) and use the shared [installation](usage.md)
path, not a separate native installer.

The canonical Darwin scripts own Homebrew formulae/casks, runtime managers,
runtimes and global tools. Package names are in
`home/.chezmoidata/packages.toml`; version policy is in
`home/.chezmoidata/versions.toml`. Homebrew-managed uv/bun/Go can differ from
Linux pins. Compatible management dependencies are borrowed, not installed twice.

Full provisioning requires Xcode Command Line Tools/Homebrew prerequisites and
may need elevation/network access. Missing required effects return incomplete
results. Every full apply ends with `brew-maintenance`: tap-sourced
`packages.toml` formulae move to homebrew/core, unused taps are removed, and
`brew autoremove` and `brew cleanup -s --prune=all` run. Homebrew's own
automatic cleanup after installs and upgrades stays on, as it is by default.

Apps in `packages.toml` that are missing install at the cask's current version.
An app already installed outside Homebrew is left alone. Each full plan asks
Homebrew which listed apps are outdated, using Homebrew's own check without
naming casks, so an app that updated itself is not reinstalled or downgraded.
Each one appears as an `update-<app>` effect, and approving the plan updates
them with `brew upgrade --cask`. `brew pin --cask <app>` holds an app; the plan
names held apps in its warnings. A failed install or update does not block VS
Code extension installs; the step still exits nonzero afterwards so the failure
stays visible.
Do not delete native script state to force all installers to rerun.

The managed shell is zsh; `.zshrc.local` remains an unmanaged override.
Oh My Posh uses Catppuccin Mocha. Terminal font selection remains manual:
choose JetBrainsMono Nerd Font in the terminal application's font settings.
The managed terminal-font helper is advisory and does not mean GUI setup ran.

VS Code's local default user settings use the shared global merge. Custom
profiles, Settings Sync and remote extension hosts need separate ownership
review; a desktop CLI is not proof that the intended remote host was configured.
Configuration recovery does not uninstall applications, packages or extensions.
