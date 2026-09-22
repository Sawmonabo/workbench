# Linux notes

Targets: Ubuntu 22.04, 24.04 and 26.04 LTS on amd64/arm64. Native release and
provisioning qualification remains open; builds and template rendering alone
do not establish it. Use the shared [installation and commands](usage.md).

The existing Linux scripts own apt base prerequisites, shell tools, fonts,
tmux/TPM, runtime managers and global tools. They consume canonical version
policy; the `packages.apt` inventory is a reference, not a promise that every
listed package is installed. Missing apt/sudo/network or required tools blocks
or fails requested work instead of counting as success.

The managed shell is bash and preserves the unmanaged `.bash_aliases` hook.
Oh My Posh uses Catppuccin Mocha. Font installation/cache refresh and plugin
execution are external effects; selecting the font in a terminal is manual.

The extension step consumes the same global VS Code extension list as macOS.
It targets the available local default `code` profile, never opens a remote
connection, and reports missing desktop CLI rather than claiming completion.
Headless hosts can use configuration-only scope; it does not install extensions.

Keep private runtime state on a qualified Linux filesystem. For Windows-host
integration use the separate [WSL qualification notes](wsl.md), not native
Windows binaries as substitutes for Linux tools.
