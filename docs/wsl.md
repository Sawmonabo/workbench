# WSL and Windows integration

Target: WSL 2.6+ with supported Ubuntu on Windows 11 24H2+, initially x64.
Workbench uses the Linux executable inside WSL and the shared
[command interface](usage.md). Native Windows and Windows ARM integration are
not qualified. Real Windows-host testing remains a release gate.

Linux guest configuration and Windows-host effects are distinct.
Turn every step off in the `apply` plan to apply files only. Full
provisioning runs the Linux scripts and the Windows host steps: prompt engine,
fonts, `.wslconfig`, RestartWSL helpers and editor settings. Existing Terminal
settings and PowerShell profiles are left unchanged unless the matching part
(`terminal-adoption`, `powershell-adoption`) is on. Those two and `font-registry`
are parts of Windows setup (`windows-files`): with Windows setup on they are
included, and with it off each can be ticked on its own, installing what it needs
itself. `default-distro`, `windows-path` and `sysctl` start off, and your choice
for each is remembered like any other. The `.wslconfig` merge asks nothing
during apply: the WSL networking row lists each setting it will change beforehand
(for example `networkingMode is virtioproxy, will be mirrored`), and a recovery
copy is written when it changes anything. The Terminal part rewrites
`settings.json` only when it differs from the managed one, keeping a copy of the
old file, and registers the font it uses. Windows-side script writes are not checkpointed. No real Windows host run is recorded yet; static
rendering does not qualify these scripts.
Project inspection/configuration is not Windows-host provisioning.

The canonical WSL scripts describe these effects:

| Target/effect | Boundary |
| --- | --- |
| Windows Terminal and PowerShell profiles | Left unchanged unless adopted; an adoption effect replaces the whole file and keeps a copy beside it. |
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
