# Linux notes

Targets: Ubuntu 22.04, 24.04 and 26.04 LTS on amd64/arm64, by decision;
preflight blocks other distributions, including the `dnf` ones the dotfiles
script handled. Native release and provisioning qualification remains open;
builds and template rendering alone do not establish it. Use the shared
[installation and commands](usage.md).

The existing Linux scripts own apt base prerequisites, shell tools, fonts,
tmux, runtime managers and global tools. They consume canonical version
policy; the `packages.apt` inventory is a reference, not a promise that every
listed package is installed. Missing apt/sudo/network or required tools blocks
or fails requested work instead of counting as success.

The managed shell is bash and preserves the unmanaged `.bash_aliases` hook.
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
Programs go into `~/.local/bin` only when it, and `~/.local`, are real folders:
when either is a link (into a dotfiles repository, say) or a file, the System
packages and Runtime managers steps install nothing and say so in the plan and
on their result lines (`~/.local/bin is a link to another folder, and Workbench
installs nothing through a link; make it a real folder`). A command already in
`~/.local/bin` that Workbench did not put there is left alone, and the part that
would install it says so (`gh was not installed: ~/.local/bin/gh already exists
and Workbench did not make it, so it was left alone`).
Headless hosts can turn every step off in the `apply` plan to apply files only; that does not install extensions.

Keep private runtime state on a qualified Linux filesystem. For Windows-host
integration use the separate [WSL qualification notes](wsl.md), not native
Windows binaries as substitutes for Linux tools.
