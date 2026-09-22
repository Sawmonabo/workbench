# Windows Terminal policy

Windows Terminal configuration lives in the canonical WSL script
`home/.chezmoiscripts/wsl/run_before_00-packages-windows.sh.tmpl`.
Its policy uses Catppuccin Mocha and JetBrainsMono Nerd Font, with tab/pane
shortcuts and profile presentation settings. The source is the authority for
exact values; do not maintain a duplicate settings document.

Windows-host application remains subject to the [WSL qualification gate](wsl.md).
Existing user profiles and unrelated settings must survive; malformed input
must stop, not fall back to whole-file replacement. PowerShell profile ownership
is separate from Terminal JSON ownership.

To change policy, edit the canonical source, regenerate compiled source trust,
and use a qualified Workbench preview/approval flow. Never delete all
chezmoi script state or bypass Workbench to force a Windows rewrite. Installing
helper files does not authorize executing restart helpers, scheduled tasks or
registry/environment changes.
