# 15. AlmaLinux 10, role folders, env files and the MCP launch gate

- Status: Accepted, October 21, 2026

## Context

Work moved to an AlmaLinux 10 WSL distribution on the same Windows PC as the
personal Ubuntu one. Workbench stopped there at the platform check, which
accepted only Ubuntu 22.04, 24.04 and 26.04: `docs/linux.md` recorded that
Ubuntu-only rule as a decision. AlmaLinux 10 ships dnf 4, not apt, and `bat`,
`fd-find` and `ripgrep` come only from EPEL, which a fresh image does not
have. It gets a minor release every six months under one major version until
2035.

The roles had fixed folders (`~/repos` for work, `~/dev` for personal). The
work tokens were copied into `machine.toml`, the Codex dispatch server
entries and, by hand, Claude Code's own entries, and Workbench installed the
Bitwarden CLI and a `bw` wrapper for work machines. The work MCP servers were
kept out of personal projects only by an instruction to Codex, and Claude
Code started its hand-added entries in every session. Two Workbench
distributions on one Windows user each keep their own choices, so each could
rewrite the shared Windows Terminal settings, PowerShell profile,
`.wslconfig` and default distribution without knowing about the other.

## Decision

- **AlmaLinux 10 joins the Linux targets.** The platform check accepts
  `ID=almalinux` with major version 10, any 10.x, because AlmaLinux moves
  minor releases on its own schedule where Ubuntu has a fixed LTS list. Only
  `ID=almalinux` is accepted, never an `ID_LIKE` match such as Rocky or
  RHEL. The package scripts pick `apt-get`, else `dnf`, at run time, so no
  template branches on the distribution and `render-check.sh` renders one
  script for both. This replaces the Ubuntu-only decision in
  `docs/linux.md`.
- **EPEL is a step of its own, `epel`, on by default.** It adds a package
  source for the whole system, and the contracts ask that a change outside
  the user's files, such as Homebrew tap removal, be an individually named
  effect. It is listed only where the package manager is dnf
  (`platform.PackageManager`), installs `epel-release` from AlmaLinux's own
  `extras` repository when `rpm -q` does not find it, and changes no other
  repository, CRB included. `bat` and `fd` fail saying EPEL is off when
  `dnf repolist --enabled` does not list it, whatever the step's switch says.
- **The writer of each shared Windows file is recorded on Windows.** The
  distributions share one Windows user and nothing else, so the record lives
  in `%LOCALAPPDATA%\Workbench\writers.json` and holds only distribution
  names. A step nobody writes, or whose recorded distribution is no longer
  installed, is written by the distribution that has it ticked, which records
  itself; another installed distribution's step changes nothing; moving it is
  a person's tick of `take-over-<step>`. The record's digest is a plan input,
  so an approved plan is refused (exit 4) once another distribution changed
  it.
- **Each role has a folder answer**, `work_folder` and `personal_folder`,
  defaults `~/repos` and `~/dev`, saved as home-relative text of plain
  segments. The two never match or nest on `both`, so one folder never loads
  both roles' credentials or matches both Git identities. Git, Codex trust,
  the costs report, the package script's folder creation and the Windows
  start folder read the answers.
- **Credentials live in per-folder env files read by name.** `work.env` and
  `personal.env` in Workbench's config folder, mode 600, hold `NAME=value`
  lines under the names the owner's tools already read. The shell exports a
  file's names only inside its role's folder, agents and tools read them from
  the environment, and no credential is written into an agent's config, a
  rendered file or a plan. A file read by name needs no keyring, which the
  AlmaLinux distribution does not have, and no new tool such as direnv for
  two fixed folders. Migration 0005 moves the tokens earlier releases saved in
  `machine.toml` into a new `work.env`.
- **The work MCP servers start through a launch gate.** Neither agent can
  scope a server to a folder and everything below it, and an entry written
  into each work repository would write into the owner's repositories and
  miss later clones. Both agents register each dispatch server with
  `workbench mcp-gate --folder <work folder> --env-file <config>/work.env --`
  and the server's command. Inside the work folder the gate adds the env
  file's names and replaces itself with the server; anywhere else it answers
  as a server with no tools and reads no file. Codex's entries are therefore
  enabled, and Claude Code's are registered at user scope through its own
  `claude mcp` commands, since Claude Code rewrites `~/.claude.json` itself.
- **Bitwarden goes**, by the owner's decision. With the credentials in
  `work.env` nothing Workbench sets up reads the vault, and shell wrappers
  that fetch tokens at launch were considered and rejected, so the CLI
  install, the `bw` wrapper and the session-caching step are removed. A `bw` already installed
  stays, and so does the session key the wrapper cached in Bitwarden's own
  folder, which Workbench does not touch: `bw lock` clears it.

## Consequences

- AlmaLinux 10 is implemented on amd64 and arm64, natively and on WSL, with
  the same steps and consent as Ubuntu plus `epel`; its native qualification
  is recorded, or still open, in the acceptance record, as for Ubuntu.
- A machine with saved answers is asked its folder questions on the first
  apply after upgrading, with today's folders as defaults; that apply needs a
  terminal, or `init --answers-from FILE`.
- Work names never reach a shell, command or agent started outside the work
  folder through Workbench's shell files, except a command that acts on
  another folder without changing to it (`git -C`) and an agent session
  started in the work folder, which keeps its environment and servers for
  the whole session.
- Outside the work folder each agent session keeps one small idle `mcp-gate`
  process per dispatch server, with no Node and no tools.
- A PC with one distribution behaves as before: it is always the writer.
- `env-files` creates each missing file once; after that the file is the
  owner's and Workbench never writes it.

## Sources

- [AlmaLinux wiki: WSL](https://wiki.almalinux.org/documentation/wsl.html):
  `AlmaLinux-10` is published for WSL.
- [AlmaLinux blog, CRB on by default](https://almalinux.org/blog/2025-09-08-enabling-crb-by-default-for-almalinux10/).
- [Fedora packages: bat](https://packages.fedoraproject.org/pkgs/rust-bat/bat/),
  [fd-find](https://packages.fedoraproject.org/pkgs/rust-fd-find/fd-find/),
  [ripgrep](https://packages.fedoraproject.org/pkgs/rust-ripgrep/ripgrep/)
  and [gh](https://packages.fedoraproject.org/pkgs/gh/gh/) in EPEL 10.
- [dnf/lock.py](https://github.com/rpm-software-management/dnf/blob/master/dnf/lock.py)
  and the [dnf.conf reference](https://dnf.readthedocs.io/en/latest/conf_ref.html):
  dnf 4 waits for a held lock unless `exit_on_lock` is set.
- [Claude Code MCP](https://code.claude.com/docs/en/mcp): scopes, their
  order, and `CLAUDE_PROJECT_DIR` in a stdio server's environment.
- [Claude Code permissions](https://code.claude.com/docs/en/permissions):
  a rule path starting `//` is absolute.
- [pydantic-settings](https://pydantic.dev/docs/validation/latest/concepts/pydantic_settings/):
  environment variables win over a dotenv file.
- [direnv guide](https://www.deployhq.com/guides/direnv): the per-folder
  alternative considered.
