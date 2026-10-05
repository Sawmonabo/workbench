# WSL and Windows integration

Target: WSL 2.6+ with supported Ubuntu on Windows 11 24H2+, initially x64.
Workbench uses the Linux executable inside WSL and the shared
[command interface](usage.md). Native Windows and Windows ARM integration are
not qualified. Real Windows-host testing remains a release gate.

Linux guest configuration and Windows-host effects are distinct.
Turn every step off in the `apply` plan to apply files only. Full
provisioning runs the Linux scripts and the Windows host steps you leave on or
tick. WSL networking (`.wslconfig`) is the one Windows step that is on by
default. Windows setup (`windows-files`: prompt engine, fonts, ripgrep, RestartWSL
helpers, editor settings and Notepad++ themes) starts off on a new WSL machine, and ticking it is
remembered. Existing Terminal settings and PowerShell profiles are left
unchanged unless the matching step (`terminal-adoption`, `powershell-adoption`)
is on. Those two and `font-registry` are optional steps of their own: off until
you tick them, whether or not Windows setup is on, and each installs what it
needs itself. `default-distro`, `windows-path` and `sysctl` are optional too, and
your choice for each is remembered like any other. The `.wslconfig` merge asks
nothing during apply: the WSL networking row lists each setting it will change
beforehand (for example `networkingMode is virtioproxy, will be mirrored`), keeps
the other settings, and a recovery copy is written when it changes anything. The
Terminal step rewrites `settings.json` only when it differs from the managed one,
keeping a copy of the old file, and registers the font it uses.

A Windows side that cannot be used at all (no interop, not x64) blocks every
Windows step you ticked, and a failed Windows download (oh-my-posh, fonts,
ripgrep, Notepad++ themes) blocks the steps that need it, never the files or the
other steps; the apply goes on and ends blocked (exit 3). Pointing VS Code's
todo-tree at ripgrep edits only that one setting, in the Windows user
`settings.json` and the WSL machine `settings.json`. Comments and trailing
commas are accepted and kept, a file that already has the value is left
untouched, and an empty file counts as no settings. A file that is invalid JSON,
has duplicate keys or cannot be edited without risk is left as it is, and Windows
setup is reported blocked with the reason while the other steps still run.
Windows-side script writes are not checkpointed. No real Windows host run is
recorded yet; static rendering does not qualify these scripts.
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
