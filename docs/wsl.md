# WSL and Windows integration

Target: WSL 2.6+ with a supported Ubuntu or AlmaLinux 10 distribution on Windows
11 24H2+, initially x64.
Workbench uses the Linux executable inside WSL and the shared
[command interface](usage.md). Native Windows and Windows ARM integration are
not qualified. Real Windows-host testing remains a release gate.

Linux guest configuration and Windows-host effects are distinct.
Turn every step off in the `apply` plan to apply files only. Full
provisioning runs the Linux scripts and the Windows host steps you leave on or
tick. WSL networking (`.wslconfig`) is the one Windows step that is on by
default. Windows setup (`windows-files`: prompt engine, fonts, ripgrep,
editor settings and Notepad++ themes) starts off on a new WSL machine, and ticking it is
remembered. Workbench no longer writes the RestartWSL helpers (`restart-wsl.ps1`,
`Restart WSL.bat`, `setup-task.ps1`) or asks for their folder: a saved
`restart_wsl_path` answer is ignored and dropped the next time your answers
are saved, and `init --answers-from` refuses a file that still has it. Copies an earlier
release wrote are yours and stay where they are, so delete them by hand if you
no longer want them (and the scheduled task, if you created one). Existing Terminal settings and PowerShell profiles are left
unchanged unless the matching step (`terminal-adoption`, `powershell-adoption`)
is on. Those two and `font-registry` are optional steps of their own: off until
you tick them, whether or not Windows setup is on, and each installs what it
needs itself. `default-distro`, `windows-path` and `sysctl` are optional too, and
your choice for each is remembered like any other. `default-distro` ("Open new
terminals in NAME") decides which distribution opens by default, for both:
it makes this distribution the one plain `wsl` opens and, when Windows
Terminal has more than one WSL profile, sets `defaultProfile` in Terminal's
`settings.json` to this distribution's profile, changing that one key only,
keeping your comments and layout, and keeping a copy of the file beside it
(`settings.json.before-workbench.*`). It is asked only when there is a choice:
with one distribution that `wsl` already opens (Docker Desktop's
`docker-desktop` does not count), or `wsl` and new Terminal tabs already
opening this one, its line starts `= ` (for example `default-distro: =
AlmaLinux-10 is your only distribution`) and Terminal's own default, such as
PowerShell, stays. Docker Desktop left as `wsl`'s default is asked about even
with one distribution of your own (`default-distro: make AlmaLinux-10 the
default for wsl in place of docker-desktop`). Otherwise the line names what
differs, for example `default-distro: make AlmaLinux-10 the default for new
Terminal tabs (wsl already opens it)`. The profile is the one named after the
distribution; only when none is, a single profile that names it or its version
as a whole word (a renamed one) is taken, never a guess between several, and
`Ubuntu` inside `Ubuntu-24.04` does not count. So a distribution with no
profile of its own takes another distribution's profile when that is the one
renamed to hold its name as a word (`Ubuntu 24.04 LTS (work)` for `Ubuntu`). Without Windows Terminal, its `settings.json` or a profile for
this distribution, only `wsl`'s default is set and the apply says why; a
`settings.json` that is a symbolic link is never replaced, and blocks the step
with that reason. A Terminal that restores your last window's tabs
at start (`firstWindowPreference: persistedWindowLayout`) still restores them;
the default decides new tabs and a window with nothing to restore. The `.wslconfig` merge asks
nothing during apply: the WSL networking row lists each setting it will change
beforehand (for example `networkingMode is virtioproxy, will be mirrored`), keeps
the other settings, and a recovery copy is written when it changes anything. The
Terminal step rewrites `settings.json` only when it differs from the managed one,
keeping a copy of the old file and the profiles and color schemes it does not
manage (see [Windows Terminal](windows-terminal.md)), and registers the font it uses. Its color
scheme, for every profile, is the `terminal_theme` answer, asked once on WSL:
`kanagawa-dragon` (Kanagawa Dragon, the default: a near-black background with
grey text) or `catppuccin-mocha` (Catppuccin Mocha). A machine saved before
the question existed is asked it at its next `apply` at a terminal (not with
`--local-build`); until then an unattended apply, or a coding agent's, is
refused naming `terminal_theme`, and `init --answers-from FILE` with the key
added saves it. Once saved, `init --ask terminal_theme` changes it. Windows
Terminal and PowerShell open in the work folder on a machine with the work
role, else in the personal folder.

VS Code extensions (`linux-editor-extensions`, on by default) installs
Workbench's extension list into this distribution's VS Code server
(`~/.vscode-server/extensions`), which VS Code on Windows runs workspace
extensions from when it opens a folder here; Work tools does the same for the
work extension. It calls a server's own command line (`code-server`, under
`~/.vscode-server/bin` or `~/.vscode-server/cli/servers`), trying the newest
first and using the first that lists its extensions, so a broken newer server
does not block an older one that works; it touches or runs nothing on Windows,
and unsets the variables the server's launcher reads to patch its own Node
(`VSCODE_SERVER_CUSTOM_GLIBC_LINKER`, `VSCODE_SERVER_CUSTOM_GLIBC_PATH`,
`VSCODE_SERVER_PATCHELF_PATH`). Extensions that run only on the Windows side, such as
Remote - WSL and Remote - SSH (`vscode_extensions_windows_side` in
`home/.chezmoidata/packages.toml`), are left out. Until VS Code has opened a
folder in this distribution there is no server, and the step has nothing to do
(`linux-editor-extensions: = VS Code has not opened this distribution yet; open
a folder here in VS Code, then apply again`). When only VS Code Insiders has
opened it (`~/.vscode-server-insiders`), the line says so: Workbench installs
into VS Code (stable)'s server. Otherwise the line names the extensions the
server lacks, or says it has them all, read from the server's
`extensions.json`, which counts an extension VS Code marked for removal as
installed, as the server's own list does. A link or a file where a folder
belongs in the path of `~/.vscode-server/extensions` blocks the step; a server
reached through one is left out, and the step is blocked when every server is.
Each call to a server is stopped after ten minutes. An extension that still
fails after a second try blocks its own step, as on Linux.

Workbench names no distribution, so one PC can run it in several WSL
distributions, for example an Ubuntu one with the `personal` role and an
AlmaLinux one with `work`, each with its own answers and choices in its own
`~/.config/workbench/machine.toml` (only a distribution with the work role is
asked the Jira, GitLab and dispatch-checkout questions; see
[roles and folders](usage.md#roles-folders-and-credentials)). Most Windows steps give the same result
from any of them. Four do not, because each distribution would write its own
version of one shared thing: `terminal-adoption` (the Terminal settings, whose
WSL profile names the distribution that wrote it and whose color scheme is its
answer), `powershell-adoption` (the
profile starts in that distribution's home), `wsl-preferences` (one
`.wslconfig` sized from that distribution's answers) and `default-distro` (which
distribution `wsl` and new Terminal tabs open).
For those Workbench keeps `%LOCALAPPDATA%\Workbench\writers.json`, which
names the distribution that writes each step and nothing else, and every
distribution follows the same rules:

1. Nobody is recorded for the step, or the recorded distribution is no longer
   installed (`wsl.exe --list --quiet`): a distribution with the step ticked
   writes, and records itself. A PC with one distribution always lands here.
2. This distribution is recorded: it writes, as before.
3. Another installed distribution is recorded: the step changes nothing,
   whatever this distribution's saved choice, and its plan line says so, for
   example `terminal-adoption: = written by AlmaLinux-10`. A saved tick never
   takes over, so two distributions with the step ticked do not take the file
   back and forth.
4. Moving it is a person's act: while another distribution is recorded, the
   plan offers `… (take over from NAME)` under `Optional`, off. Ticking it runs
   the step here and records this distribution. It is asked once as a new
   step and then only in `apply --choose`, which a coding agent is refused,
   so an agent never moves it.

When the record cannot be read, or another distribution is recorded and the
installed list cannot be read, a ticked step is blocked with the reason rather
than guessed. Each write replaces the whole
file at once (a new file, then a rename), so two distributions applying
together leave one whole record. The record's content is an input of the
plan, so `--approve-plan` exits 4 once another distribution changed it. The scripts
read and write it each as a call of its own, after the group of Windows calls
they make together, with the same cap as any other Windows call: 60 seconds in
an apply and 8 seconds in a probe. Deleting it is safe: rule 1 applies
again. The four steps' Undo text names it. Windows Terminal's theme and font
sit in the settings' profile defaults, so a distribution that does not write
the file still gets them, and its own profile, which the writer keeps with
every other profile it does not manage, keeps whatever tab color and start
folder it has. Which
profile new tabs open is `default-distro`'s: while a distribution is recorded
for it (or the record or the installed list cannot be read), `terminal-adoption` keeps the
`defaultProfile` the file has, so writing the Terminal settings never undoes
the default another distribution chose.

A Windows side that cannot be used at all (no interop, not x64) blocks every
Windows step you ticked, and a failed Windows download (oh-my-posh, fonts,
ripgrep, Notepad++ themes) blocks the steps that need it, never the files or the
other steps; the apply goes on and ends blocked (exit 3). Workbench does not
install extensions into the Windows editor or edit its settings; its extension
step installs only into this distribution's VS Code server.
Windows-side script writes are not checkpointed. No real Windows host run is
recorded yet; static rendering does not qualify these scripts.
Project inspection/configuration is not Windows-host provisioning.

The canonical WSL scripts describe these effects:

| Target/effect | Boundary |
| --- | --- |
| Windows Terminal and PowerShell profiles | Left unchanged unless adopted; an adoption effect replaces the file and keeps a copy beside it, and Terminal adoption keeps the profiles and color schemes it does not manage. `default-distro` changes only Terminal's `defaultProfile`, and keeps a copy. |
| Prompt engine, themes and font files | Windows user paths, not guessed drive/username paths. |
| Font registration/loading | Registry/process effects; file recovery does not undo them. |
| VM preferences | A file change, distinct from restarting WSL. |
| User PATH/default distribution | Separate external-effect selection; never choose a distribution implicitly. |
| Guest sysctl | Privileged file and live kernel effect, not ordinary user configuration. |
| Editor settings | Windows desktop and Linux remote extension hosts are different owners. |
| Editor extensions | Installed only into this distribution's VS Code (stable) server, with its own command line; extensions that declare only the `ui` kind, and the remote extensions, left out; never uninstalled. |

No inspection opens a remote session, restarts WSL or changes Windows settings.
Private Workbench state stays on the Linux filesystem; chmod on a Windows mount
does not establish private Windows ACLs. A native qualification run must cover
spaces/non-default drives, actual profiles, permissions, denied privilege,
recovery conflicts and each external effect independently.

The full personal VS Code merge targets macOS/Linux user settings. Windows-side
selected settings in WSL scripts are not full Windows-profile deployment.
See [Windows Terminal](windows-terminal.md) for the source policy boundary.
