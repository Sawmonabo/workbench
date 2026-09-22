# WSL and Windows integration

Target: WSL 2.6+ with supported Ubuntu on Windows 11 24H2+, initially x64.
Workbench uses the Linux executable inside WSL and the shared
[command interface](usage.md). Native Windows and Windows ARM integration are
not qualified. Real Windows-host testing remains a release gate.

Linux guest configuration and Windows-host effects are distinct.
`--config-only` excludes all provisioning scripts. Full provisioning runs the
Linux scripts and the Windows host steps: prompt engine, fonts, `.wslconfig`,
RestartWSL helpers and editor settings. Existing Terminal settings and
PowerShell profiles are left unchanged unless `terminal-adoption` or
`powershell-adoption` is selected; `font-registry`, `windows-path`,
`default-distro` and `sysctl` are also selected `--effect`s. Windows-side script
writes are not checkpointed. No real Windows host run is recorded yet; static
rendering does not qualify these scripts.
Project inspection/configuration is not Windows-host provisioning.

The canonical WSL scripts describe these effects:

| Target/effect | Boundary |
| --- | --- |
| Windows Terminal and PowerShell profiles | Preserve unrelated content; require explicit ownership/adoption. |
| Prompt engine, themes and font files | Windows user paths, not guessed drive/username paths. |
| Font registration/loading | Registry/process effects; file recovery does not undo them. |
| VM preferences and RestartWSL helper files | File changes are distinct from executing a restart or scheduled task. |
| User PATH/default distribution | Separate external-effect selection; never choose a distribution implicitly. |
| Guest sysctl | Privileged file and live kernel effect, not ordinary user configuration. |
| Editor settings | Windows desktop and Linux remote extension hosts are different owners. |

No inspection opens a remote session, restarts WSL or changes Windows settings.
Private Workbench state stays on the Linux filesystem; chmod on a Windows mount
does not establish private Windows ACLs. A native qualification run must cover
spaces/non-default drives, actual profiles, permissions, denied privilege,
recovery conflicts and each external effect independently.

The full personal VS Code merge targets macOS/Linux user settings. Windows-side
selected settings in WSL scripts are not full Windows-profile deployment.
See [Windows Terminal](windows-terminal.md) for the source policy boundary.
