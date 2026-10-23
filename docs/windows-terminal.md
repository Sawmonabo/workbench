# Windows Terminal policy

Windows Terminal configuration lives in the canonical WSL script
`home/.chezmoiscripts/wsl/run_before_00-packages-windows.sh.tmpl`.
Its policy uses JetBrainsMono Nerd Font, with tab/pane shortcuts and profile
presentation settings. The color scheme, for every profile, is the WSL
`terminal_theme` answer: Kanagawa Dragon (`kanagawa-dragon`, the default) or
Catppuccin Mocha (`catppuccin-mocha`); the file carries both schemes. The source is the authority for
exact values; do not maintain a duplicate settings document.

Windows-host application remains subject to the [WSL qualification gate](wsl.md).
An existing `settings.json` is left unchanged unless the `terminal-adoption`
effect is selected; adoption replaces the file with the managed one, only when it
differs, and keeps a copy beside it (`settings.json.before-workbench.*`). The
profiles and color schemes in your file that the managed one does not have
(other WSL distributions' profiles, ones you added) are kept, after the managed
ones and in your file's order, because Terminal creates a profile it generates
only once and would not bring back one that was dropped. Only this
distribution's other WSL entries go: an older generated profile of the same
name beside the one Workbench binds to, and the standalone profile Workbench
writes when Terminal has none for it. A file whose profiles cannot be read
(blank, not UTF-8, not valid JSON with comments, a repeated key, or profiles or
color schemes that are not a list) is left as it is, and the step is blocked
with the reason. PowerShell profile ownership is separate from
Terminal JSON ownership, through `powershell-adoption`.

To change policy, edit the canonical source and use a qualified Workbench
preview/approval flow. Never delete all
chezmoi script state or bypass Workbench to force a Windows rewrite. Installing
files does not authorize restarting WSL, scheduled tasks or
registry/environment changes.
