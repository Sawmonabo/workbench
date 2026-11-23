# Linux notes

Targets: Ubuntu 22.04, 24.04 and 26.04 LTS and AlmaLinux 10 (any 10.x) on
amd64/arm64. Preflight blocks every other distribution, Rocky, RHEL, CentOS
Stream and Fedora included: AlmaLinux is accepted by `ID=almalinux` alone,
never by `ID_LIKE`. [ADR 0015](adr/0015-almalinux-and-role-folders.md)
replaces the earlier Ubuntu-only decision. Native release and provisioning
qualification remains open for both; builds and template rendering alone do
not establish it. AlmaLinux 10.2 amd64 has run only in throwaway containers on
a WSL2 host (see [acceptance](acceptance.md#linux)). Use the shared
[installation and commands](usage.md).

The existing Linux scripts own base prerequisites, shell tools, fonts, tmux,
runtime managers and global tools. They pick the package manager when they
run, `apt-get` when present, else `dnf`, so one rendered script serves both
distributions. They consume canonical version policy; the `packages.apt`
inventory is a reference, not a promise that every listed package is
installed. Missing apt or dnf, sudo, network or required tools blocks or fails
requested work instead of counting as success.

On AlmaLinux, System packages installs `gcc gcc-c++ make curl unzip git
ca-certificates fontconfig libatomic` with dnf (the Node builds nvm installs
need `libatomic.so.1`, which AlmaLinux's gcc does not bring; its line lists
`libatomic` while it is missing), only those `rpm -q --whatprovides` does
not find, so a name an installed package already provides is not installed
again; then `bat`, `fd-find`, `jq`, `tree`, `ripgrep`, `htop`, `ShellCheck`,
tmux and, in Command-line tools, podman. `bat`, `fd`, `ripgrep`, `htop` and
`ShellCheck` come only from EPEL (`jq` and `tree` from AlmaLinux's own
repositories):
the EPEL step (`epel`), listed only where the package manager is dnf and on by
default, runs `sudo dnf install -y epel-release` from AlmaLinux's own `extras`
repository when `rpm -q epel-release` fails (`epel: turn on EPEL (uses
sudo)`, or `epel: = EPEL is on`), and changes no other repository, CRB
included. When `dnf repolist --enabled` does not list EPEL, whatever the step
says, the parts that need it fail saying EPEL is off, and the next apply tries
them again. dnf waits for another dnf's package lock by itself and says so, so
the scripts add no limit of their own; apt-get waits up to ten minutes.
On every distribution the same step puts the GitHub CLI, golangci-lint,
lefthook and gitleaks in `~/.local/bin` from each project's own GitHub
releases (the last three checked against the release's checksums file, at the
`[linux_binaries]` pins in pinned mode, the latest release in latest mode), and
Command-line tools adds gofumpt and govulncheck with `go install`
(`[linux_go_tools]`); the Mac gets all five from Homebrew. The step runs when
its script's content changes, which a pin change in pinned mode does. In pinned
mode a golangci-lint, lefthook or gitleaks in `~/.local/bin` that reports a
version other than its pin is then replaced by the pinned release, and the plan
names it
(`golangci-lint 2.14.0 → 2.15.0`); the new copy takes the old one's place
only once it is downloaded and checked, so a failed download keeps the working
copy. A link there is kept. In latest mode an installed copy of the three is
kept, and the GitHub CLI, which has no pin, is kept in both modes.
Undo EPEL with `sudo dnf remove epel-release`. The package step creates the
work folder on a machine with the work role and the personal folder on one
with the personal role (see [roles and folders](usage.md#roles-folders-and-credentials), which also covers the
work role's Jira, GitLab and dispatch-checkout questions). Codex and Claude Code
are installed through npm only when missing; an installed copy, from npm in
nvm's default Node or from its own installer, is updated through that
installer (see [usage](usage.md)), on WSL too. Runtime managers installs Go
from go.dev into `~/.local/share/go-versions` and links it as
`~/.local/bin/go`; that step and the Go tools in Command-line tools use that
link when it is there, so a system Go (AlmaLinux's `golang` package in
`/usr/bin`) neither hides it nor makes each apply install Go again.

The managed shell is bash and preserves the unmanaged `.bash_aliases` hook.
It sources `/etc/bashrc` when that file exists, as AlmaLinux's stock
`.bashrc` does, so `/etc/profile.d` still runs in a shell that is not a login
shell, such as a tmux pane; Ubuntu has none. Before it stops for a shell that
is not interactive, it puts `~/.local/bin` on PATH when it is missing, so
`bash -lc` and `wsl -d DISTRO -- COMMAND` find `workbench` on AlmaLinux too,
whose `~/.bash_profile` only reads `~/.bashrc` (Ubuntu's `~/.profile` adds it
itself). It loads each role's env file
only inside that role's folder (see [credentials](usage.md#roles-folders-and-credentials)).
Oh My Posh uses Catppuccin Mocha. Font installation/cache refresh and plugin
execution are external effects; selecting the font in a terminal is manual.

The extension step consumes the same global VS Code extension list as macOS.
It targets the available local default `code` profile, never opens a remote
connection, and reports missing desktop CLI rather than claiming completion.
On WSL it installs into the distribution's VS Code server instead, leaving out
the extensions that run only on the Windows side (see [WSL](wsl.md)).
An extension that still fails after a second try blocks its own step (VS Code
extensions, or Work tools for the work extension) with the reason. No failed step
stops the others (see [usage](usage.md)): a failed package, runtime or tool
install, or one missing because an earlier step could not install it, is named on
its own step, the other steps still run, the apply ends with exit 3 and the next
apply tries again.
Workbench keeps its own command in `~/.local/bin`, so when that folder or
`~/.local` is a link (into a dotfiles repository, say) or a file, it stops before
planning and names it (`~/.local/bin is a link; Workbench keeps its own
files only in real folders, so make it one`). The System packages and Runtime
managers steps check the same before they install programs there, and name it in
the plan and on their result lines. A command already in
`~/.local/bin` that Workbench did not put there is left alone, and the part that
would install it says so (`gh was not installed: ~/.local/bin/gh already exists
and Workbench did not make it, so it was left alone`).
Headless hosts can turn every step off in the `apply` plan to apply files only; that does not install extensions.

Keep private runtime state on a qualified Linux filesystem. For Windows-host
integration use the separate [WSL qualification notes](wsl.md), not native
Windows binaries as substitutes for Linux tools.

Workbench no longer installs the Bitwarden CLI. A `bw` installed earlier stays,
and so does the session key its wrapper cached in `~/.config/Bitwarden
CLI/session`: run `bw lock` (or delete that file) and remove `bw` by hand.
