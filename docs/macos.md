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
results. Homebrew cleanup/tap removal and hand-installed application
replacement run only when selected with `--effect brew-maintenance` or
`--effect app-replacement`. A skipped app does not block VS Code extension
installs; the step still exits nonzero afterwards so the skip stays visible.
Do not delete native script state to force all installers to rerun.

The managed shell is zsh; `.zshrc.local` remains an unmanaged override.
Oh My Posh uses Catppuccin Mocha. Terminal font selection remains manual:
choose JetBrainsMono Nerd Font in the terminal application's font settings.
The managed terminal-font helper is advisory and does not mean GUI setup ran.

VS Code's local default user settings use the shared global merge. Custom
profiles, Settings Sync and remote extension hosts need separate ownership
review; a desktop CLI is not proof that the intended remote host was configured.
Configuration recovery does not uninstall applications, packages or extensions.
