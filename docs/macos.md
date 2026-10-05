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
network access. Installing Homebrew needs the Mac password: an `apply` at a
terminal asks for it once, after approval and before it changes anything (see
[usage](usage.md)). Without a terminal nobody is asked, and installing Homebrew
needs a sudo ticket that is still valid, or the apply stops before any change.
Homebrew drops earlier sudo approvals each time it runs, so an app that needs
the password, such as Docker Desktop, is asked for it by Homebrew itself while
it installs or updates. Missing required effects return incomplete results.
Planning reads Homebrew's package index as it is and never refreshes it, so
`apply --dry-run` and `apply --approve-plan` show the updates as of the last
refresh. An `apply` not given `--approve-plan` runs `brew update` first, before
it plans (a failure only warns), so the plan lists current updates. Every
full apply ends with `brew-maintenance`: it runs `brew update` unless the apply
already did (so a machine that applies only with `--approve-plan` still gets a
fresh index for its next plan), tap-sourced `packages.toml` formulae move to
homebrew/core (the core copy is downloaded first, and if the move fails the
tapped formula is reinstalled), unused taps are removed, and `brew autoremove`
and `brew cleanup -s --prune=all` run. Cleanup's one warning per outdated
formula it skips is folded into a single count; `brew outdated` lists them. VS
Code extensions install only when missing. Homebrew's own automatic cleanup
after installs and upgrades stays on, as it is by default.

Apps in `packages.toml` that are missing install at the cask's current version.
An app already installed outside Homebrew is left alone. Each full plan asks
Homebrew which listed apps are outdated, using Homebrew's own check without
naming casks, so an app that updated itself is not reinstalled or downgraded.
Each one appears as an `update-<app>` effect, and approving the plan updates
them with `brew upgrade --cask`. Listed command-line tools (formulae) work the
same way with `brew upgrade --formula`, except the chezmoi, uv and Python that
Workbench itself runs, which stay at the versions it qualified. A tool's effect
also names what Homebrew's own dry run says the upgrade brings with it: new or
outdated dependencies and outdated installed packages that depend on it. So the
plan's approval is the only question and the updates run with
`HOMEBREW_NO_ASK=1`. A tool whose upgrade would also change Workbench's own
chezmoi, uv or Python is not updated. `brew pin` holds a tool and
`brew pin --cask <app>` an app; the plan names held and kept packages in its
warnings. A failed install or update does not block VS Code
extension installs; the step still exits nonzero afterwards so the failure
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
