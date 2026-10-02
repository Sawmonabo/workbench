# Windows Terminal policy

Windows Terminal configuration lives in the canonical WSL script
`home/.chezmoiscripts/wsl/run_before_00-packages-windows.sh.tmpl`.
Its policy uses Catppuccin Mocha and JetBrainsMono Nerd Font, with tab/pane
shortcuts and profile presentation settings. The source is the authority for
exact values; do not maintain a duplicate settings document.

Windows-host application remains subject to the [WSL qualification gate](wsl.md).
An existing `settings.json` is left unchanged unless the `terminal-adoption`
effect is selected; adoption replaces the whole file with the managed one, only when it
differs, and keeps a copy beside it (`settings.json.before-workbench.*`). PowerShell profile ownership is separate from
Terminal JSON ownership, through `powershell-adoption`.

To change policy, edit the canonical source and use a qualified Workbench
preview/approval flow. Never delete all
chezmoi script state or bypass Workbench to force a Windows rewrite. Installing
helper files does not authorize executing restart helpers, scheduled tasks or
registry/environment changes.
