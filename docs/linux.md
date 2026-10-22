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
ca-certificates fontconfig` with dnf, only those `rpm -q --whatprovides` does
not find, so an image's `curl-minimal` counts as curl; then `bat`, `fd-find`,
tmux and, in Command-line tools, podman. `bat` and `fd` come only from EPEL:
the EPEL step (`epel`), listed only where the package manager is dnf and on by
default, runs `sudo dnf install -y epel-release` from AlmaLinux's own `extras`
repository when `rpm -q epel-release` fails (`epel: turn on EPEL (uses
sudo)`, or `epel: = EPEL is on`), and changes no other repository, CRB
included. When `dnf repolist --enabled` does not list EPEL, whatever the step
says, the bat and fd parts fail saying EPEL is off, and the next apply tries
them again. dnf waits for another dnf's package lock by itself and says so, so
the scripts add no limit of their own; apt-get waits up to ten minutes.
Undo EPEL with `sudo dnf remove epel-release`. The package step creates the
work folder on a machine with the work role and the personal folder on one
with the personal role (see [roles and folders](usage.md#roles-folders-and-credentials), which also covers the
work role's Jira, GitLab and dispatch-checkout questions). Codex and Claude Code
are installed through npm only when missing; an installed copy, from npm in
nvm's default Node or from its own installer, is updated through that
installer (see [usage](usage.md)), on WSL too.

The managed shell is bash and preserves the unmanaged `.bash_aliases` hook.
It sources `/etc/bashrc` when that file exists, as AlmaLinux's stock
`.bashrc` does, so `/etc/profile.d` still runs in a shell that is not a login
shell, such as a tmux pane; Ubuntu has none. It loads each role's env file
only inside that role's folder (see [credentials](usage.md#roles-folders-and-credentials)).
Oh My Posh uses Catppuccin Mocha. Font installation/cache refresh and plugin
execution are external effects; selecting the font in a terminal is manual.

The extension step consumes the same global VS Code extension list as macOS.
It targets the available local default `code` profile, never opens a remote
connection, and reports missing desktop CLI rather than claiming completion.
An extension that still fails after a second try blocks its own step (VS Code
extensions, or Work tools for the work extension) with the reason. No failed step
stops the others (see [usage](usage.md)): a failed package, runtime or tool
install, or one missing because an earlier step could not install it, is named on
its own step, the other steps still run, the apply ends with exit 3 and the next
apply tries again.
Workbench keeps its own command in `~/.local/bin`, so when that folder or
`~/.local` is a link (into a dotfiles repository, say) or a file, it stops before
planning and names it (`/home/you/.local/bin is a link; Workbench keeps its own
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
