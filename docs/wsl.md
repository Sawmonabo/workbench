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
distribution; only when none is, a single profile that mentions it (a renamed
one) is taken, never a guess between several (`Ubuntu` is part of
`Ubuntu-24.04`). Without Windows Terminal, its `settings.json` or a profile for
this distribution, only `wsl`'s default is set and the apply says why; a
`settings.json` that is a symbolic link is never replaced, and blocks the step
with that reason. A tick you saved when this step set only `wsl`'s default
now sets Terminal's default too from the next apply, which the plan shows. A Terminal that restores your last window's tabs
at start (`firstWindowPreference: persistedWindowLayout`) still restores them;
the default decides new tabs and a window with nothing to restore. The `.wslconfig` merge asks
nothing during apply: the WSL networking row lists each setting it will change
beforehand (for example `networkingMode is virtioproxy, will be mirrored`), keeps
the other settings, and a recovery copy is written when it changes anything. The
Terminal step rewrites `settings.json` only when it differs from the managed one,
keeping a copy of the old file, and registers the font it uses. Windows
Terminal and PowerShell open in the work folder on a machine with the work
role, else in the personal folder.

Workbench names no distribution, so one PC can run it in several WSL
distributions, for example an Ubuntu one with the `personal` role and an
AlmaLinux one with `work`, each with its own answers and choices in its own
`~/.config/workbench/machine.toml` (only a distribution with the work role is
asked the Jira, GitLab and dispatch-checkout questions; see
[roles and folders](usage.md#roles-folders-and-credentials)). Most Windows steps give the same result
from any of them. Four do not, because each distribution would write its own
version of one shared thing: `terminal-adoption` (the Terminal settings, whose
WSL profile names the distribution that wrote it), `powershell-adoption` (the
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
the file still gets them; it misses the tab color and the start folder. Which
profile new tabs open is `default-distro`'s: while a distribution is recorded
for it (or the record or the installed list cannot be read), `terminal-adoption` keeps the
`defaultProfile` the file has, so writing the Terminal settings never undoes
the default another distribution chose.

A Windows side that cannot be used at all (no interop, not x64) blocks every
Windows step you ticked, and a failed Windows download (oh-my-posh, fonts,
ripgrep, Notepad++ themes) blocks the steps that need it, never the files or the
other steps; the apply goes on and ends blocked (exit 3). Workbench does not
install extensions into the Windows editor or edit its settings.
Windows-side script writes are not checkpointed. No real Windows host run is
recorded yet; static rendering does not qualify these scripts.
Project inspection/configuration is not Windows-host provisioning.

The canonical WSL scripts describe these effects:

| Target/effect | Boundary |
| --- | --- |
| Windows Terminal and PowerShell profiles | Left unchanged unless adopted; an adoption effect replaces the whole file and keeps a copy beside it. `default-distro` changes only Terminal's `defaultProfile`, and keeps a copy. |
| Prompt engine, themes and font files | Windows user paths, not guessed drive/username paths. |
| Font registration/loading | Registry/process effects; file recovery does not undo them. |
| VM preferences | A file change, distinct from restarting WSL. |
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
