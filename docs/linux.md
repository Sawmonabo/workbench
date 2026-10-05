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
Headless hosts can turn every step off in the `apply` plan to apply files only; that does not install extensions.

Keep private runtime state on a qualified Linux filesystem. For Windows-host
integration use the separate [WSL qualification notes](wsl.md), not native
Windows binaries as substitutes for Linux tools.
