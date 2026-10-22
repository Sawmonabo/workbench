# AlmaLinux 10 work distribution design

Status: design, Oct 21, 2026. It makes AlmaLinux 10 a Linux target, so one
Windows PC can run Workbench in two WSL distributions: the Ubuntu one for
personal work and an AlmaLinux 10 one for work. Each keeps its own machine
answers. Work and personal stay roles (`machine_role`), and the same design
holds when both roles share one machine (`both`). The change has six parts:

1. AlmaLinux 10 passes the platform check and the package step learns dnf,
   with EPEL as its own step.
2. Each role has a folder the owner picks (defaults `~/repos` and `~/dev`).
3. Work credentials live in one file, `work.env` (and personal ones in
   `personal.env`), each loaded only inside its own folder, in place of the
   token copies Workbench writes today. A migration moves the saved tokens.
4. The work MCP servers start only for agent sessions inside the work folder,
   for Claude Code and Codex alike, through a Workbench launch gate.
5. A record on Windows keeps two distributions from fighting over the shared
   Windows files.
6. Bitwarden is removed.

## 1. Why

Work moved to an AlmaLinux 10 WSL distribution on the same Windows PC. Today
Workbench on that distribution stops at the platform check
(`internal/machine/host_linux.go` accepts only Ubuntu 22.04, 24.04 and 26.04),
so the work setup can only live on the Ubuntu distribution, next to the
personal one. `docs/linux.md` records the Ubuntu-only rule as a decision; this
design replaces it for AlmaLinux 10.

The work credentials are copied into four places today (section 4.11), the
folders are fixed, and the work MCP servers are kept out of personal projects
only by an instruction to Codex (section 4.14). Roles can change and can share
a machine, so these have to hold on any machine, not only on a work-only
distribution.

## 2. What was observed

On the AlmaLinux distribution, read only (`wsl.exe -d AlmaLinux-10`), Oct 21,
2026:

- `/etc/os-release`: `ID="almalinux"`, `VERSION_ID="10.2"`,
  `ID_LIKE="rhel centos fedora"`, `PLATFORM_ID="platform:el10"`,
  `SUPPORT_END=2035-06-01`. x86_64, WSL2 kernel, systemd as PID 1, glibc 2.39,
  `dnf` 4.20 (no dnf5).
- The user is in `wheel`; `umask` is 0022 in a login shell.
- Repositories enabled: `baseos`, `appstream`, `crb`, `extras`, `epel`.
  `dnf history` shows `epel-release` was installed by hand, so a fresh image
  does not have EPEL. `epel-release` is offered by AlmaLinux's own `extras`
  repository. `/usr/bin/crb` exists.
- Installed: `gcc`, `make`, `git`, `curl`, `unzip`, `tmux` 3.3a, `podman`
  5.8.2, `golang` 1.26.7, `jq`, `python3` 3.12, `bash-completion`.
  Missing: `g++`, `fontconfig` (so no `fc-cache`), `bat`, `fd`, `ripgrep`,
  `zsh`.
- Where each missing package comes from (`dnf list`): `fontconfig` and
  `gcc-c++` from `appstream`; `zsh` from `baseos`; `bat` 0.24.0, `fd-find`
  10.4.2, `ripgrep` 14.1.1 and `gh` 2.97.0 only from `epel`. Their commands are
  `/usr/bin/bat`, `/usr/bin/fd` and `/usr/bin/rg` (`dnf repoquery -l`), not
  the `batcat`/`fdfind` names Ubuntu uses.
- Workbench v0.1.27 is already installed there, with a `machine.toml` whose
  role is `both` and whose `[effects]` choices match the Ubuntu
  distribution's exactly: it was copied over, not decided on AlmaLinux.
- `~/repos` holds the work checkouts; there is no `~/dev`. On the Ubuntu
  distribution there is `~/dev` and no `~/repos`.
- `~/.local/bin` already holds commands Workbench did not put there (among
  them `uv`, `node`, `npm`, `claude`, `codex`, `win-browser`).
- The stock `~/.bashrc` sources `/etc/bashrc`. On AlmaLinux bash does not
  read `/etc/bashrc` by itself (Debian and Ubuntu bash read
  `/etc/bash.bashrc` on their own), and `/etc/bashrc` is what runs
  `/etc/profile.d/*.sh` in a shell that is not a login shell, such as a tmux
  pane. Workbench's managed `.bashrc` does not source it.
- Claude Code has the two dispatch servers as hand-added user-scope entries
  in `~/.claude.json`, with literal tokens, so they start in every Claude Code
  session. Workbench's Codex template renders them with literal tokens and
  `enabled = false`, so Codex never starts them
  (`home/.chezmoitemplates/codex-config.toml.tmpl`).

On the web:

- AlmaLinux publishes `AlmaLinux-10` for WSL through the Store and
  `wsl --install` ([AlmaLinux wiki: WSL](https://wiki.almalinux.org/documentation/wsl.html)).
- CRB is on by default on AlmaLinux 10 since a September 2025 update
  ([AlmaLinux blog](https://almalinux.org/blog/2025-09-08-enabling-crb-by-default-for-almalinux10/)).
- EPEL 10.2 carries `bat`, `fd-find` and `gh`
  ([bat](https://packages.fedoraproject.org/pkgs/rust-bat/bat/),
  [fd-find](https://packages.fedoraproject.org/pkgs/rust-fd-find/fd-find/),
  [gh](https://packages.fedoraproject.org/pkgs/gh/gh/)), and `ripgrep`
  ([ripgrep](https://packages.fedoraproject.org/pkgs/rust-ripgrep/ripgrep/)).
- Claude Code: a stdio server's environment gets `CLAUDE_PROJECT_DIR`, "the
  project root"; an unset `${VAR}` with no default is passed as the literal
  text with a warning in `claude mcp list` and `/mcp`; `/mcp` flags servers
  that advertise the tools capability but expose none; local scope outranks
  project scope, which outranks user scope, whole entry by whole entry
  ([MCP docs](https://code.claude.com/docs/en/mcp)).
- Codex: a stdio server starts in the session's working folder
  (`current_dir`, `codex-rs/rmcp-client/src/stdio_server_launcher.rs`) with
  only a core environment (`HOME`, `PATH`, `SHELL`, `USER` and a few more)
  plus the names in `env_vars`, and the entry's `env` table is applied last,
  so it wins (`create_env_for_mcp_server`, `codex-rs/rmcp-client/src/utils.rs`).

So EPEL is needed for `bat` and `fd`. Rust is not needed for them on
AlmaLinux. The Runtime managers step still installs rustup for the cargo
tools and runtimes, as it does on Ubuntu.

## 3. Goals, non-goals, anti-goals

Goals:

- `workbench apply` plans and applies on AlmaLinux 10 (any 10.x) on amd64 and
  arm64, natively and on WSL, with the same steps, questions and consent as
  Ubuntu, plus the `epel` step.
- Work and personal stay roles. A machine may be `work`, `personal` or
  `both`, and every rule below holds for all three, including when the role
  changes later.
- Each role lives in a folder the owner picks, default `~/repos` for work and
  `~/dev` for personal.
- Work credentials never reach a shell, command, editor or agent session
  started outside the work folder, and the work MCP servers never start
  there. Two exceptions, for Claude Code and Codex alike (sections 4.11
  and 4.14):
  - A command that acts on another folder without the shell changing to it
    (`git -C`, or a script that changes folder itself).
  - An agent session started in the work folder. It keeps its environment
    and servers for the whole session, even if it later works in another
    folder.
- Any number of Workbench distributions on one Windows user, or just one,
  without any of them undoing another's Windows-side changes, and without
  Workbench naming or needing a particular distribution (section 4.7).

Non-goals:

- Rocky Linux, RHEL, CentOS Stream, Fedora or any other `ID_LIKE` match.
  Only `ID=almalinux` is accepted.
- A Windows Terminal settings file with a styled profile for more than one
  distribution (follow-up, section 8).
- New MCP servers. The owner's own dispatch servers stay; how they start and
  get their credentials changes (sections 4.11 and 4.14).
- An OS keyring. The AlmaLinux distribution has no `secret-tool` or
  `gnome-keyring-daemon`; a private env file referenced by name is what both
  agents document.
- Changing the Ubuntu targets or the Ubuntu package path.
- Installing the work VS Code extension into Windows VS Code from WSL (it is
  not installed there today either).

Anti-goals (a change that does these is wrong even if it works):

- A second renderer, a distribution argument in `render-check.sh` that
  changes rendered files, or a template branch on `.chezmoi.osRelease`.
- Silent fallbacks: a missing package manager or repository counts as a
  failed or blocked part, never as success.
- Turning on any repository other than EPEL, or changing a repository the
  owner turned on or off.
- A credential value in a plan, `--json`, log, rendered file, agent config or
  the repository.
- Files written into the owner's repositories.
- Provisioning the developer's live AlmaLinux distribution to test the code.

## 4. Design

### 4.1 Work and personal are roles, not distributions

The saved answers live per machine in `machine.toml`, never in the
repository, and each WSL distribution has its own. The owner sets the
AlmaLinux distribution to `work` with `workbench init --ask machine_role` and
then runs `apply`. Nothing in this design depends on Ubuntu being there, and
a PC with only the AlmaLinux distribution works the same.

The owner installs Workbench on the AlmaLinux distribution from a release,
not a checkout. Per `AGENTS.md`, unreleased code reaches a real machine only
as a pre-release the owner tags, so this change reaches that distribution
through a pre-release or the next release.

The Ubuntu distribution becomes `personal`. Order matters: the owner switches
it with the release already installed there, before installing this change.
Today's release drops the answers the new role does not use, so the token
answers leave that distribution's `machine.toml` then, and migration 0005
(section 4.11) has nothing to move there. Done the other way round, the
migration writes a `work.env` on Ubuntu while it is still `both`; after the
switch the plan warns about it (section 4.11) and the owner deletes it.

With role `work` (and for the work half of `both`):

- `~/.gitconfig` uses the work email for the work folder.
- Both agents get the two dispatch servers through the launch gate
  (section 4.14), and Codex trusts the work folder.
- The shells load `work.env` inside the work folder (section 4.11).
- The work tools step installs the work VS Code extension on macOS and on
  Linux outside WSL. On WSL it is not planned: its only part needs a local
  VS Code (section 4.12).

### 4.2 The platform check accepts AlmaLinux 10

`hostVersion` in `internal/machine/host_linux.go` also marks the platform
complete when `ID` is `almalinux` and the major part of `VERSION_ID` is `10`.
AlmaLinux 10 gets a minor release every six months under one major version
until 2035, unlike Ubuntu's fixed LTS list, so the check uses the major
version only. The message stays `ID VERSION_ID/arch`, plus the WSL note.

The `forbidigo` rule in `.golangci.yml` forbids only `runtime.GOOS`, and the
file already reads `/etc/os-release` behind its `_linux.go` suffix, so the
check may stay where it is. The new package-manager fact (section 4.3) does
belong in `internal/platform`; moving the `/etc/os-release` read there too is
the builder's call.

### 4.3 The package step learns dnf

`home/.chezmoiscripts/linux/run_once_before_00-packages.sh.tmpl` picks its
package manager at run time, the way its tmux part already does: `apt-get`
when present, otherwise `dnf`, otherwise the part fails with the reason. No
template branches on the distribution, so `render-check.sh` renders the same
package script for every distribution.

On dnf, as root through `sudo`, like the apt lines:

1. **EPEL** is its own step, `epel`, beside `linux-packages`, and on by
   default like it (not under Optional). It is listed only on a host whose
   package manager is dnf: `internal/platform` gains that fact (dnf when
   `dnf` is found and `apt-get` is not), and `platformEffects` in
   `internal/machine/effects_linux.go` lists `epel` from it. `effectSources`
   gets `epel: 00-packages`, and the linux list in
   `TestSharedScriptsGateEverySectionOnItsOwnEffect`
   (`internal/machine/effects_test.go`) gets `epel`, so the shared-script
   safeguard covers the new switch.
   Its part runs first in the same packages script, behind its own
   `WORKBENCH_EFFECT_EPEL` switch, the way `work-tools` already shares that
   script: when `rpm -q epel-release` fails it runs
   `sudo dnf install -y epel-release` (from AlmaLinux's `extras`
   repository). Its probe line is `epel: = EPEL is on` when the package is
   installed, otherwise `epel: turn on EPEL (uses sudo)`. It runs no
   `dnf config-manager` and no `crb`: CRB is on by default, and Workbench
   does not change a repository the owner set. Why a step of its own:
   `workbench-contracts.md` (the paragraph on ordinary apply) asks that
   changes outside the user's files, such as Homebrew tap removal, appear as
   individually named effects, and a system-wide package source is that kind
   of change. On the owner's AlmaLinux distribution EPEL is already on, so
   the step has nothing to do there.
2. **Base packages**, replacing `build-essential curl unzip git
   ca-certificates fontconfig`: `gcc gcc-c++ make curl unzip git
   ca-certificates fontconfig`, installing only the ones `rpm -q` does not
   find, so an image that ships `curl-minimal` keeps it rather than hitting
   a package conflict. Today the base part is skipped without a word when
   `apt-get` is missing, so the font part later fails on the missing
   `fc-cache`; on AlmaLinux it now runs.
3. **bat and fd**: `dnf install -y bat fd-find`. The commands are already
   `bat` and `fd`, so no `~/.local/bin` link is made. When the EPEL
   repository is not enabled (`dnf repolist --enabled` does not list it,
   whatever the `epel` switch says), the bat and fd parts fail saying EPEL is
   off. The packages script is `run_once`, so a failed part makes chezmoi run
   it again on the next apply; that is intended. The cargo fallback stays
   only for a system with neither apt nor dnf, where it already exists.
4. **tmux**: unchanged (it already has the dnf branch). AppStream's tmux is
   3.3a; the managed `tmux.conf` uses TPM and the Dracula theme and sets no
   option newer than 3.3. The builder confirms the theme loads in the
   disposable check.
5. **gh, oh-my-posh, the Nerd Font**: unchanged. They are
   per-user downloads into `~/.local/bin` or `~/.local/share/fonts`.
   EPEL's `gh` is current, but the per-user copy keeps one install shape and
   one probe on every distribution.

The dnf package names sit inline beside the apt names, as the apt names do
today. `AGENTS.md` makes `home/.chezmoidata/packages.toml` the owner of
package names, but its `apt` list is a reference the scripts never read
(`docs/linux.md`); moving both the apt and the dnf install names there is a
follow-up (section 8), not part of this change.

`run_onchange_after_30-global-tools.sh.tmpl` installs podman with `apt-get`
only; on dnf it runs `dnf install -y podman`.

Editing these two scripts changes their content, so chezmoi runs them again
once on every existing machine, Ubuntu included. Every part already checks
before it installs, so on a machine that is set up the rerun changes nothing.

The apt lines wait up to ten minutes for the package lock. AlmaLinux 10
ships dnf 4 (`dnf-4.20`, Red Hat advisory RHBA-2025:20106), which waits for
a held lock by itself: it checks every second and prints `Waiting for
process with pid N to finish.` once per holder
([dnf/lock.py](https://github.com/rpm-software-management/dnf/blob/master/dnf/lock.py)),
and fails at once only when `exit_on_lock` is set, which is off by default
([dnf.conf reference](https://dnf.readthedocs.io/en/latest/conf_ref.html)).
The dnf lines therefore add no limit of their own: a `timeout` wrapper would
keep sudo from asking for its password and would kill a slow install as if
the lock were held. A failed dnf line fails its part with dnf's own message
above it, as the apt lines do.

`render-check.sh` treats `apt-get install` as a write in its probe check
(line 97); `dnf install` is added there.

### 4.4 Folders and the Windows start folder follow the role

The package script creates `~/repos` and `~/dev` on every machine
(`run_once_before_00-packages.sh.tmpl`, the "standard directories" line). It
now creates the work folder when `.has_work` and the personal folder when
`.has_personal` (section 4.13), so a work-only distribution does not get an
empty personal folder.

The Windows Terminal WSL profile and the PowerShell profile start in
`~/repos` on every machine (`run_before_00-packages-windows.sh.tmpl`: the
`wsl.exe -d … --cd ~/repos` command line, the `startingDirectory` fallback
and `$wslRepos`). With the folders following the role, a personal-only
machine would open into a folder that does not exist, so the start folder
follows the role in the same change: the work folder when `.has_work` (so
`work` and `both` keep today's start folder with the default answer),
otherwise the personal folder.

### 4.5 Plan text names the package manager

The `linux-packages` effect (`internal/machine/effects.go`, around line 183)
says "apt" in its Description, Summary, What and Undo. It names both:
packages come from apt on Ubuntu and from dnf on AlmaLinux. Undo names `apt`
or `dnf remove`. EPEL is its own step (section 4.3) and is not mentioned
here. The text is static (the effect is shared by both distributions), so it
names both rather than reading the host, and it says "the work and personal
folders" rather than naming `~/repos` and `~/dev`. `docs/usage.md` and
`docs/linux.md` say the same.

### 4.6 bash reads the system file on AlmaLinux

The managed `.bashrc` (`home/private_dot_bashrc.tmpl`) sources `/etc/bashrc`
near the top, after the not-interactive return and before anything that sets
`PROMPT_COMMAND` (oh-my-posh, then the folder hook of section 4.11), guarded
by `[ -f /etc/bashrc ]`. Ubuntu has no `/etc/bashrc`, so nothing changes
there. On AlmaLinux this keeps `/etc/profile.d` working in non-login shells,
as the stock file does. The Debian-only lines (`lesspipe`, `debian_chroot`)
already test for their files and stay.

### 4.7 One Windows side, any number of distributions

Every Windows-side step already works from whichever distribution runs it:
the scripts use `WSL_DISTRO_NAME`, and nothing names Ubuntu. A PC with only
the AlmaLinux distribution needs no change. The problem is two or more
Workbench distributions on one Windows user. Each keeps its choices in its
own Linux home (`~/.config/workbench/machine.toml`), so each decides about
the same Windows files without knowing about the others.

Which steps that matters for:

| Effect | Shared result | Needs one writer? |
| --- | --- | --- |
| `windows-files` | oh-my-posh, themes, fonts, `rg.exe` | No: same bytes from any distribution. |
| `font-registry`, `windows-path` | Font registry, user PATH | No: same result from any distribution. |
| `sysctl` | `/etc/sysctl.d/99-dev.conf` inside that distribution | No: per distribution, same value. |
| `wsl-preferences` | `%USERPROFILE%\.wslconfig`, one VM for all distributions | Yes: its sizing comes from each distribution's own answers. |
| `terminal-adoption` | Windows Terminal `settings.json`: one WSL profile, named after the distribution that wrote it, set as default | Yes: each write replaces the file. |
| `powershell-adoption` | The PowerShell profile, which starts in that distribution's home | Yes. |
| `default-distro` | Which distribution plain `wsl` opens | Yes. |

**The record of who writes them lives on Windows.** Workbench keeps one small
file in a Workbench folder in the Windows user's local app data
(`%LOCALAPPDATA%`), next to the Windows files it writes, naming the
distribution that writes each "yes" step. The rules, the same in every
distribution and naming none:

1. **Nobody recorded, or the recorded distribution is no longer installed**
   (checked against `wsl.exe --list --quiet`): a distribution with the step
   ticked writes, and records itself. A PC with one distribution always
   lands here.
2. **This distribution is recorded**: it writes, as today.
3. **Another installed distribution is recorded**: the step changes nothing
   and its plan line says so, for example
   `terminal-adoption: = written by AlmaLinux-10`, whatever this
   distribution's saved choice. A saved tick never takes over, so two
   distributions that both have the step ticked do not take the file back
   and forth on every apply.
4. **Moving it is a person's act**: `workbench apply --choose` in the
   distribution that should write offers the step as, for example,
   `terminal-adoption: take over from Ubuntu-26.04 and replace settings.json`.
   Ticking it there is the consent; that apply moves the record. A coding
   agent is already refused `--choose`, so an agent never moves it.

So a step has one writer: the first distribution that ticked it, or the last
one a person moved it to. The record holds only distribution names, never
answers or secrets. Removing it is safe: rule 1 applies again. `revert` and
the effects' Undo text name it. Workbench reads and writes it from Linux
through the Windows paths it already resolves, with the same 60-second cap
and blocked result as its other Windows calls.

Both Windows scripts read the record and the installed list: today only
`run_after_10-deploy-windows-configs.sh.tmpl` asks `wsl.exe --list --quiet`,
and `terminal-adoption` and `powershell-adoption` run in
`run_before_00-packages-windows.sh.tmpl`, so that script asks too. The
record's content is an input of the plan, so `--approve-plan` exits 4 when
another distribution changed the writer since the plan was made. Each write
of the record replaces the whole file at once (write a new file, then
rename), so two distributions applying at the same time leave one whole
record, and the other sees it on its next plan.

Windows Terminal's theme and font sit in the managed file's
`profiles.defaults`, so every distribution's profile gets them, including
those of distributions that do not write the file. What those miss is the
tab colour, the start folder (WSL's own profile starts in `~`) and being the
default.

Both distributions on this PC are modern WSL distributions: each ships
`/etc/wsl-distribution.conf` with a `[windowsterminal]` `ProfileTemplate`,
and WSL wrote each one's profile as a fragment under
`%LOCALAPPDATA%\Microsoft\Windows Terminal\Fragments\Microsoft.WSL`.

### 4.8 The AlmaLinux distribution as it is now

Workbench v0.1.27 is there with role `both` and choices copied from Ubuntu:
`decided` names `costs-ingest`, `global-tools`, `powershell-adoption`,
`runtime-managers`, `runtimes`, `terminal-adoption`, `tmux-plugins`,
`windows-files` and `wsl-preferences`; `select` holds only `windows-files`;
`skip` holds `wsl-preferences`. So Terminal settings and the PowerShell
profile are decided and off, the default distribution was never decided and
is off until ticked, and WSL networking is skipped. The Ubuntu
distribution's own choices are the same, so neither distribution writes the
Terminal settings or PowerShell profile today, and no writer is recorded yet
(section 4.7, rule 1).

After the owner switches the role to `work` and installs a release with this
change, the first `apply` must run at a terminal: it asks the new work folder
question (section 4.13), and it asks about the steps that are new or not
decided and have something to do: `migrations` (0005 has the tokens to
move), `linux-packages` and `claude-mcp` (section 4.11). `epel` (EPEL was
installed by hand) and `env-files` (migration 0005 creates `work.env`) have
nothing to do there, so they sit under Already set and are not asked. `work-tools` is not planned on WSL
(section 4.12). It plans the managed
`.bashrc` and `.gitconfig` in place of the hand-written ones; the lines added
there by hand (the promptctl hook, `clear-cache`) are already in the managed
`.bashrc` for a work WSL machine. Commands already in `~/.local/bin` that
Workbench did not make are left alone, as now; `uv` already being there means
the Runtime managers step finds it and does not install its own. The system
`golang` package stays; the user-local Go comes first on `PATH`.

None of this is run by the builder: it is the owner's step on the live
distribution, after the disposable checks in section 4.10.

### 4.9 Decision record and docs

- A new ADR, 0015, records that AlmaLinux 10 joins the Linux targets, why the
  major-version check, why EPEL is its own step, why the writer of the shared
  Windows files is recorded on Windows, why each role has a folder answer,
  why credentials live in per-folder env files read by name, why the work MCP
  servers start through a launch gate, and why Bitwarden goes. It names the
  Ubuntu-only decision in `docs/linux.md` as replaced.
- `AGENTS.md`:
  - The numbered-migration exception gains one case: migration 0005 may move
    the two token answers from `machine.toml` into a new `work.env` under
    the rules in section 4.11.
  - The Ownership table gains rows for the env files and the `env-files` and
    `claude-mcp` effects (`internal/machine/`), and for the hidden
    `mcp-gate` command and the two steps' hidden subcommands
    (`internal/cli/`).
  - The paragraph on optional steps names `epel` as on by default.
- The `internal/migrate` package comment, `migrate.Plan`'s recovery limits and
  the `migrations` effect's Description, What and Undo
  (`internal/machine/effects.go`) say what 0005 does.
- `docs/superpowers/specs/workbench-contracts.md`:
  - An AlmaLinux 10 row in the targets table, and dnf and EPEL in the
    prerequisites paragraph.
  - The security-sensitive effects (line 80): no `bitwarden-session-cache`;
    `claude-mcp` and `env-files` added.
  - What Config holds (line 39): no tokens; the env files.
  - The MCP credentials row (line 135).
  - The packages rows (lines 142–143): no Bitwarden, apt or dnf, and the role
    folders.
- `README.md` targets line.
- `docs/usage.md`:
  - The `epel`, `env-files` and `claude-mcp` steps, and the take-over line.
  - The folder questions.
  - That answers no longer hold tokens (line 139) and that a role change no
    longer asks for them (line 468).
  - No Bitwarden in the step-gating text (line 366) or the fixed steps
    (line 368).
- `docs/wsl.md`: the section 4.7 rules.
- `docs/linux.md`: targets, the dnf path, EPEL and the bash note.
- `docs/acceptance.md`: a new entry for the checks actually run. Earlier
  entries are records of past runs and stay as written.

### 4.10 Checks

- Routine gates: `golangci-lint fmt`, `golangci-lint config verify`,
  `golangci-lint run ./...`, `go build ./...`, `go test ./...`.
- `scripts/render-check.sh` for each role and mode, with and without `wsl`,
  after `scripts/scratch-init.sh` learns the new prompts (section 4.13).
  Expected rendered changes, and no others:
  - **Scripts.**
    - The linux and darwin `00-packages`.
    - `30-global-tools`.
    - Both Windows scripts (`00-packages-windows` and
      `10-deploy-windows-configs`).
    - The new `06-env-files` and `46-claude-mcp`.
  - **`.bashrc` and `.zshrc`.** `/etc/bashrc`, the folder hook, the `cd`
    wrappers, the `codex()` and `code()` wrappers, and no Bitwarden.
  - **`.gitconfig` and its includes.** The folder answers.
  - **`~/.codex/config.toml`.** The gate entries, `enabled = true`, no `env`
    credentials, and the folder answers in trust.
  - **Codex `AGENTS.md` and Claude Code `CLAUDE.md`.** The text to use the
    variables and never open or print the env files, for roles with an env
    file. The folder answers in Codex's.
  - **Claude Code `settings.json`.** The env-file deny rules.

  The personal-role leak check stays and must pass.
- Safeguard tests, each with its risk written beside it, as `AGENTS.md`
  allows for credential exposure and loss:
  - Migration 0005 never loses a token. A conflict leaves both files
    byte for byte, a run after a crash finishes the job, and no value appears
    in its output.
  - The gate's folder test, for both agents' inputs, never counts a folder
    outside the work folder as inside, for example `~/repos-old` against
    `~/repos`, or a symlink pointing out of it.
- A disposable `almalinux:10` container with a new non-root user and sudo,
  from a local bundle: `init --answers-from FILE` (with the folder answers),
  then `apply --local-build --dry-run --json`, then `--approve-plan`. It must
  install EPEL, the base packages, bat, fd, tmux, podman, the font (with
  `fc-cache`), and end with every chosen step complete. The same in an Ubuntu
  24.04 container shows the apt path unchanged.
- For the WSL path, a second AlmaLinux 10 distribution imported under another
  name with a disposable home, never the live one. If that is not done, the
  acceptance notes say the WSL path on AlmaLinux was not run.
- In a disposable home, with synthetic credentials only, on role `both` with
  non-default folder answers:
  - **Env files.**
    - Both files are created 0600. The plan lists empty names, and never a
      value (plan, `--json`, logs).
    - In a Claude Code session in the work folder, the Read tool and `cat`
      on `work.env` are denied.
  - **Shell hook.**
    - A new shell in the work folder has the work names. `cd` to the
      personal folder swaps them for the personal ones. Elsewhere it has
      neither, even when a parent shell passed them down.
    - Typed in the work folder, `cd <personal folder>/x && env` shows no
      work name. So do `claude`, `codex` and `code` started the same way.
    - In a Claude Code and a Codex session started in the personal folder,
      a command `cd <work folder>/x && env` shows no work name (bash, and
      zsh on macOS).
  - **Gate.**
    - Claude Code and Codex, each started in a work repository, list the
      dispatch tools from a stub server.
    - Started in the personal folder and in `~`, they list none, start no
      stub server, and show no error or warning (`/mcp`, `claude mcp list`,
      Codex's startup line).
    - Each agent started from a plain environment (no shell hook) in a work
      repository still gives the stub server the work names.
  - **Migration.**
    - A `machine.toml` with synthetic tokens on role `both` ends with
      `work.env` holding all nine names at 0600 and the token keys gone.
    - A re-ask of the folder questions before the migration keeps the
      tokens, and so does `init --answers-from` with a file that has the
      folder answers and no tokens.
    - Running it twice changes nothing.
    - An existing `work.env` is a reported conflict with both files
      unchanged.
  - **Stale Codex keys.** A `~/.codex/config.toml` holding synthetic literal
    tokens in the dispatch `env` tables ends without them, with the values
    redacted in the plan diff.
  - **One definition.** The rendered Codex dispatch entries and the Claude
    Code entries `claude-mcp` registers name the same command, arguments and
    `NODE_VERSION`.
  - **Claude Code entries.** A hand-added user-scope entry with synthetic
    literal tokens is replaced by the gate entry. A same-named local-scope
    entry is reported, not touched.
  - **Role switch.**
    - Switching to `personal` removes both agents' dispatch entries, loads no
      `work.env` and warns that the file is still there.
    - Non-default folders reach git, Codex trust, the costs report and the
      Windows start folder.
- arm64 is implemented but recorded as unqualified, as for Ubuntu, unless an
  arm64 run is made.
- The writer record (section 4.7) lives on the real Windows user, so its
  four rules are checked only with a disposable Windows user, or by the
  owner after release; otherwise the acceptance notes say they were not run
  on a real host. The rules' plan lines can still be checked in a probe with
  a record file placed by hand in a disposable folder.

### 4.11 Credentials live in per-folder env files

**Today** the Jira token alone has copies in Workbench's `machine.toml`, in
the dispatch servers' `env` in `~/.codex/config.toml` (Workbench writes
both), in the dispatch servers' `env` in `~/.claude.json` (added by hand),
and in the owner's atlassian-cli `~/.config/atlassian-cli/.env`; the GitLab
token is in the first three. Each consumer keeps its own copy. The Jira and
GitLab addresses and the Jira email are rendered into the Codex template.

**The design:** one file per role, in Workbench's config folder:
`work.env` and `personal.env`, mode 0600, holding credentials and addresses
as environment variables under the names the owner's tools already use.
`work.env` starts as:

```bash
JIRA_BASE_URL=
JIRA_EMAIL=
JIRA_API_TOKEN=
CONFLUENCE_BASE_URL=
CONFLUENCE_EMAIL=
CONFLUENCE_API_TOKEN=
TEMPO_API_TOKEN=
GITLAB_URL=
GITLAB_TOKEN=
```

`personal.env` starts with no names, since Workbench saves no personal
credential today. The format is `NAME=value` lines, `#` comments and blank
lines, nothing else (no `export`, no quotes or expansion, no carriage return
at a line's end), so the shell hook and the gate read it the same way. A
`NAME=` line with an empty value sets nothing, in the hook and the gate
alike, so a name the owner has not filled in never hides a tool's own
setting.

**How the two new steps run.** `env-files` and `claude-mcp` (item 2 below)
run the way the `migrations` step does: a shared chezmoi script calls a
hidden Workbench subcommand, which does the work in Go and writes with its own
private write, as `workbench migrate run` does. chezmoi itself cannot write
the env files, since Workbench refuses chezmoi targets inside its own
folders.

- **The scripts.** `run_after_06-env-files` runs after `05-migrations`, so the
  migration goes first. `run_after_46-claude-mcp` runs after
  `30-global-tools` and `45-agent-updates`, so `claude` is installed.
- **Switches and plan lines.** Each script carries its own
  `WORKBENCH_EFFECT_*` switch and, under `WORKBENCH_PROBE`, prints the
  subcommand's plan lines.
- **Listed on every machine.** Each has an `effectSources` entry and is
  listed on every machine. With nothing to do, its probe line starts with
  `=`, so it sits under Already set and is never asked.
- **Names stay in Go.** The entry names and env file names live in Go, not
  in the rendered scripts, so render-check's personal leak check still
  passes.
- **Inputs.** Each subcommand runs as a child of the locked apply, as
  `migrate run` does. It reads the role and the folder answers from
  `machine.toml`, its own executable path for the gate command, and the
  config folder from Workbench's usual path rules.
- **One definition of the dispatch entries.** The Codex template and the
  `claude-mcp` subcommand both describe the two entries: names, checkout
  path, `NODE_VERSION` and gate arguments. A check in 4.10 compares the
  rendered Codex entries with the Claude Code entries, so the two cannot
  drift.

**Who writes the files.** `env-files`, on by default, creates each file its
role uses when it is missing (0600). After that the file is the owner's:
Workbench never writes it again. The one other writer is migration 0005,
which only creates `work.env`. The step's plan lines:

- `env-files: create work.env` when it is missing.
- `env-files: = work.env is there, empty: JIRA_API_TOKEN, TEMPO_API_TOKEN`,
  naming each listed name that is empty or missing. It is a nothing-to-do
  line, so a name the owner leaves empty on purpose does not keep the step
  asking. Values are read only to tell empty from not; they are never kept,
  shown, logged or put in `--json`.
- `env-files: = work.env holds work credentials without the work role:
  delete it` (and the same for `personal.env` on a machine without the
  personal role). No plan line holds `; `, which the plan view uses to join
  lines. Workbench does not delete it.
- `env-files: = waiting for migration 0005 to create work.env` while 0005
  still has work to do.

Every line but `create` is a nothing-to-do line (`=`), so the step is asked
only when it will create a file.

Undo: delete the file by hand; it is not checkpointed.

**Loaded by folder, not everywhere** (owner's decision). The managed
`.bashrc` and `.zshrc` load `work.env` only while the shell's current folder
is the work folder or below it, and `personal.env` only inside the personal
folder, the way git already switches identity per folder with `includeIf`.
The two folders never overlap (section 4.13), so both files may use the same
name (a `GITLAB_TOKEN` for work and another for personal) without colliding.

The hook is one shell function:

- **Where it runs.** On every change of folder and before every prompt:
  - **bash:** from `cd`, `pushd` and `popd` wrappers, and appended to
    `PROMPT_COMMAND` after `oh-my-posh init` (never replacing it).
  - **zsh:** registered with `add-zsh-hook chpwd` and `add-zsh-hook precmd`
    (`precmd` also runs before the first prompt), never as functions named
    `chpwd` or `cd`.

  In both shells the hook does nothing unless the shell is interactive.
  Both agents load the user's shell file to snapshot its functions for the
  commands they run. bash's managed `.bashrc` already returns early when not
  interactive, and the zsh guard does the same job, so an agent session
  started in the personal folder never loads work names through a captured
  hook.

  So `cd ~/dev/site && npm install`, typed in the work folder, already runs
  without the work names.
- **What it does.** It compares the current folder (symlinks resolved) with
  the two folder answers. It unsets every name the hook loaded, which it
  keeps as a list of names (never values), so a name stays out even after
  the owner deletes its line, moves the file or makes it unreadable. It also
  unsets every name listed in the file of a role folder the shell is not in,
  whoever set it, so a child shell or tmux pane that inherited work names
  drops them in the personal folder. Then it exports the names of the file
  of the folder it is in, when that file has no group or other permission
  bits. The `code()` wrapper below removes both lists.
- **Where the files are.** The absolute path of Workbench's config folder is
  rendered at apply time, as for the gate.
- **Agents and the editor.** The existing `claude()` wrapper and a new
  `codex()` wrapper run the hook just before starting the agent. A new
  `code()` wrapper starts the editor with every name from both files
  removed, so an editor opened from the work folder does not pass the work
  names to its other windows and the agents started in them.

An agent takes the environment of the shell that started it for its whole
session. What is accepted:

- **Commands that act on another folder without changing the shell's
  folder** (`git -C`, `make -C`) keep the current folder's names.
- **Agents started by an IDE extension** never have the names. Their
  dispatch servers still work, because the gate reads the file itself
  (section 4.14), but commands they run do not see the names.

Considered: direnv, which does this generally with an `.envrc` per folder and
a `direnv allow` approval per file
([direnv guide](https://www.deployhq.com/guides/direnv)). For exactly two
fixed folders the hook needs no new tool and no files in the owner's folders.

Every consumer reads the values by name:

| Consumer | Claude Code | Codex |
| --- | --- | --- |
| The dispatch MCP servers | No credential in the entry. The gate reads `work.env` and passes its names to the server it starts (section 4.14). | The same: no `env` credentials and no `env_vars`. The gate reads `work.env`, which also gets past Codex starting servers with only a core environment. |
| Commands the agent runs (atlassian-cli to write tickets, glab, curl) | The Bash tool inherits the session's environment. | Commands inherit it too: the filter on names containing KEY, SECRET or TOKEN is off unless configured (`ignore_default_excludes` defaults to true, `codex-rs/config/src/shell_environment_policy.rs`). |
| Reading the files themselves | Denied: `Read(//<absolute config folder>/work.env)` and the same for `personal.env` join the enforced deny list beside `Read(./.env)`. Two leading slashes, because in Claude Code one slash anchors at `~/.claude/`, not the filesystem root ([permissions](https://code.claude.com/docs/en/permissions)). The settings modify template adds them with the folder resolved at apply time, since `claude.json`'s list is static. Then `cat` and the Read tool cannot put the tokens in a transcript. `env` or `printenv` in the Bash tool still can, as with any credential in the environment, so `home/private_dot_claude/CLAUDE.md.tmpl` says to use the variables and never open or print the files. | No deny rule: Codex runs with full access and no sandbox by the owner's earlier decision. Its `AGENTS.md` says the same as Claude Code's `CLAUDE.md`: use the variables, never open or print the files. |

atlassian-cli needs no change: it loads settings with pydantic-settings,
where "environment variables will always take priority over values loaded
from a dotenv file" ([pydantic-settings](https://pydantic.dev/docs/validation/latest/concepts/pydantic_settings/)).
Its own `~/.config/atlassian-cli/.env` becomes a redundant copy the owner may
delete, as are any other copies made by hand; Workbench does not look for
them.

What changes in each agent's config:

1. **Codex:** the dispatch entries in
   `home/.chezmoitemplates/codex-config.toml.tmpl` lose their literal tokens,
   addresses and email, keep `NODE_VERSION = "default"` in `env`, and use the
   gate as their command (section 4.14). `enabled` becomes `true`, because
   the gate is now what limits them to the work folder. The modify script
   (`home/private_dot_codex/modify_private_config.toml.tmpl`) keeps every
   key it does not set, so it gains a `RETIRED_KEYS` list, applied like
   `INACTIVE_TABLES`, that removes `JIRA_BASE_URL`, `JIRA_EMAIL`,
   `JIRA_API_TOKEN`, `GITLAB_URL` and `GITLAB_TOKEN` from the two dispatch
   `env` tables of an existing file. Otherwise the old literal token stays on
   disk, and Codex applies the `env` table last, so it would win. The plan's
   diff shows those lines removed, values redacted (`secretLine` matches
   `TOKEN =`). On a machine without the work role, `INACTIVE_TABLES` removes
   the two entries, as today.
2. **Claude Code:** a new effect, `claude-mcp`, on by default, run as
   described under "How the two new steps run" above.
   - **Work role.** It registers the two servers at user scope with Claude
     Code's own commands, `claude mcp remove --scope user NAME`, then
     `claude mcp add-json --scope user NAME JSON`. The JSON holds the gate
     command and `NODE_VERSION`, no credential.
   - **Without the work role.** It removes them with
     `claude mcp remove --scope user`, so a role change takes them away.
   - **Probe.** It compares the user-scope entries in `~/.claude.json` with
     the wanted ones, reading only names, scopes and the command, never
     printing `env` values. Claude Code rewrites that file constantly, so
     Workbench changes it only through `claude`, as it already leaves agent
     updates to the agents' own installers (ADR 0009).
   - **Same-named entries elsewhere.** A same-named entry at local scope
     (in `~/.claude.json`) outranks user scope, so the plan warns and names
     the `claude mcp remove --scope local NAME` command. Workbench does not
     remove it. Project-scope entries live in each repository's `.mcp.json`
     and are not looked for.
   - **Old literal tokens.** Replacing a hand-added entry deletes Claude
     Code's copy of its literal tokens. When migration 0005 found no saved
     tokens to move, the plan line says those credentials are not carried
     into `work.env`.
   - **Edge cases.** Removing a name that is not there is not a failure.
     When `claude` is missing, the part is blocked with the reason.
   - **Undo.** `claude mcp remove --scope user NAME`.
3. **The token questions go.** `jira_api_token` and `gitlab_token` leave
   `home/.chezmoi.toml.tmpl` and the questions, and the `work` validation in
   `internal/machine/answers.go`. Until migration 0005 has moved them, they
   stay known as retired keys:
   - `answerKeys` accepts them, but they are never asked, and `init --ask`
     does not offer them (`Askable`).
   - `secrets()` keeps redacting them.
   - Every write of the answers carries them through unchanged when its
     input lacks them, so nothing drops them before the migration runs:
     - a re-ask, for example the new folder questions;
     - `init --answers-from FILE` (`AdoptionPlan`).

   They stay accepted for as long as a machine may still hold them. Ending
   that is a later change of its own.

**Migration 0005 moves the saved tokens** (owner's decision).
`internal/migrate/0005_work_tokens.go` runs in the `migrations` step of an
approved apply, or `workbench migrate --apply`, never in a preview. The plan
lists it under that step when the retired keys are present. It reads
`machine.toml` with go-toml itself, since `internal/migrate` cannot import
`internal/machine`.

- **Role has work, no `work.env`.** It writes `work.env` (0600, one write of
  a new file then a rename that never replaces an existing file) with all
  nine names:
  - The two tokens.
  - `JIRA_EMAIL` from the saved work email (`work_email` on `both`,
    otherwise `email`).
  - `JIRA_BASE_URL` and `GITLAB_URL` set to the addresses the Codex template
    renders today. Those literals move from the template into the migration,
    so they are not copied.
  - The rest empty.

  Then it rewrites `machine.toml` (private, same mode) without the two keys.
- **A later run, or one after a crash between the two writes.** When
  `work.env` holds the same two token values, it only removes the keys.
  That is idempotent, and nothing is lost.
- **`work.env` exists with anything else.** It changes neither file and the
  plan says to copy the tokens by hand and delete the two keys. It never
  merges, following the migration rules in `AGENTS.md`.
- **Role has no work.** It does nothing, and the plan says the keys are
  there and should be removed.
- No value is printed, logged or put in `--json`.
- While 0005 still has work to do, `env-files` does not create `work.env`,
  and the plan says it is waiting for the migration. If the owner unticks
  the `migrations` step, the tokens stay in `machine.toml`, still redacted,
  until a later apply runs it.

### 4.12 Bitwarden is removed

What goes:

- The Bitwarden CLI install: the `work-tools` part of the linux and darwin
  `run_once_before_00-packages.sh.tmpl` scripts, with their probe lines and
  header comments.
- The `bw` wrapper `home/.chezmoitemplates/bw-wrapper.sh`, and its includes
  in `home/private_dot_bashrc.tmpl` and `home/dot_zshrc.tmpl`.
- The words "bw login" in the `.bashrc` comment about browser opens.
- In `internal/machine/effects.go`: the `bitwarden-session-cache` effect, its
  `effectSources` entry and the comment above `workToolsEffect`.
- The Bitwarden comment in `effects_test.go`.
- The `packages.toml` comment.
- The `bitwarden` word in `render-check.sh`'s personal leak pattern.
- The docs listed in section 4.9.

The `work-tools` effect keeps only the work VS Code extension, and its text
says so. It loses `00-packages` from its `effectSources` on both platforms,
since both packages scripts lose their Bitwarden part. On Linux its only
other script, `35-vscode-extensions`, renders nothing on WSL, so it is not
planned on WSL. The shared-script test's lists of owners of `00-packages`
drop it.

A `bw` already installed on a machine stays until the owner deletes it. So
does the vault session key the wrapper cached in
`~/.config/Bitwarden CLI/session`: that folder is Bitwarden's, and Workbench
does not touch other tools' files. The notice to run `bw lock` (or delete
that file) goes where the owner sees it on every platform, WSL included:

- The subject of the commit that removes Bitwarden, which becomes the
  changelog and release notes.
- `docs/usage.md` and `docs/linux.md`.
- The `work-tools` Undo text, on the platforms that still plan that step.

### 4.13 The role and its folders are asked on every machine

Every install, on any platform or distribution, asks the role as it does
today (`personal`, `work` or `both`, in `home/.chezmoi.toml.tmpl`). New: it
also asks the folder for each role it has, once, with a default:

| Role | Question | Default |
| --- | --- | --- |
| `work`, `both` | Work folder | `~/repos` |
| `personal`, `both` | Personal folder | `~/dev` |

They are saved as `work_folder` and `personal_folder` in `machine.toml`, as
home-relative text such as `~/repos`. Validation, in
`internal/machine/answers.go` and again in the template with `fail`:

- Below the home folder, not the home folder itself.
- Segments of letters, digits, `.`, `_` and `-` only, and no `.` or `..`
  segment. This rules out spaces and quoting trouble in every place below.
- On `both`, the two differ and neither contains the other. Otherwise both
  env files would load in one place, both git identities would match, and
  the gate would start the work servers in a personal folder.

Each consumer expands the answer its own way:

- git: `gitdir:~/repos/`.
- Codex: an absolute trust key.
- Windows Terminal and PowerShell: `\\wsl.localhost\<distribution>` plus the
  absolute path.
- The shell hook and the gate: the absolute path.

Every place that names `~/repos` or `~/dev` today reads the answer instead:

- `dot_gitconfig.tmpl`: the `includeIf "gitdir:…"` lines and its header
  comment; the comments in `dot_gitconfig-work.tmpl` and
  `dot_gitconfig-personal.tmpl`.
- `codex-config.toml.tmpl` (the trusted projects and the gate arguments),
  `modify_private_config.toml.tmpl` (`INACTIVE_TABLES`) and
  `private_dot_codex/AGENTS.md.tmpl`.
- The `claude-mcp` gate arguments (section 4.11).
- The package script's folder creation (section 4.4).
- The Windows Terminal and PowerShell start folder (section 4.4).
- The `linux-packages` effect text in `internal/machine/effects.go`.
- `internal/costs/report.go` `projectRoots` and `internal/cli/costs.go`
  `scopeNames`, so the default costs view covers the chosen folders. They
  read the saved answers, and fall back to `~/dev` and `~/repos` when none
  are saved.
- `home/.chezmoi.toml.tmpl`: the question text and its header comment.
- `scripts/scratch-init.sh`, which answers prompts by their exact text: drop
  the two token prompts, answer the two folder prompts, and match the new
  email prompt text. Without this, `render-check.sh` stops at a prompt.

When a folder answer changes, or a role switch drops one, Codex keeps the
old folder's trust entry: the modify script keeps tables it does not manage,
and `INACTIVE_TABLES` knows only the current answers. That entry trusts a
path and holds no credential, so it is accepted and stays until the owner
removes it.

A machine that already has saved answers is asked the new folder questions
on its first apply after upgrading, with the defaults, which are today's
folders, so nothing moves. That apply needs a terminal. An unattended run
(`--approve-plan`, `--yes`, a coding agent) is refused before any change,
with the message to run `apply` at a terminal or `init --answers-from FILE`,
as for any missing answer (`validateAnswers` never fills in a missing
answer on its own).

### 4.14 Work MCP servers start only inside the work folder

On a `both` machine (or any machine, if the role changes later), the dispatch
servers must run only for agent sessions started inside the work folder, and
never start, cost memory or add tools elsewhere.

**What exists today.** Claude Code starts its hand-added entries in every
session, wherever it was started. Codex never starts its entries
(`enabled = false`). On `both` machines Codex's `AGENTS.md` adds a
"Path-scoped tool policy" telling the model not to call the dispatch or
CodeRabbit tools under the personal folder
(`home/private_dot_codex/AGENTS.md.tmpl`); Claude Code gets no such text.

**Neither agent can scope a server to a folder and everything below it:**

- Codex reads project config (`.codex/config.toml`) only from folders between
  the repository root (the nearest `.git`, `project_root_markers`) and the
  working folder (`discover_project_layers` in
  `codex-rs/config/src/loader/mod.rs`), so a file in the work folder itself is
  never read from inside a repository there. Its `[projects."<path>"]` table
  holds only `trust_level` (`ProjectConfig` in
  `codex-rs/config/src/config_toml.rs`).
- Claude Code reads shared project settings only from the session's working
  folder ([settings](https://code.claude.com/docs/en/settings)); local-scope
  MCP servers belong to one project path, and `.mcp.json` is per project
  ([MCP scopes](https://code.claude.com/docs/en/mcp)).

Per-repository entries (a `.codex/config.toml` or `.mcp.json` written into
every work repository) would gate correctly but write into the owner's
repositories and miss every repository cloned after an apply, so they are
rejected.

**The design: Workbench gates the launch itself.** Both agents register each
dispatch server with the resolved Workbench executable as its command:

```
<workbench> mcp-gate --folder <work folder> --env-file <config folder>/work.env -- <home>/.nvm/nvm-exec node <work folder>/<checkout>/dispatch-gitlab/dist/index.js
```

`<workbench>` is the path Workbench already renders for its hooks
(`workbench_bin`), and `<checkout>` is the checkout the Codex template names
today. Every path is absolute and rendered at apply time, so the gate reads
no `machine.toml` and needs nothing from an environment Codex trims. A
changed folder answer re-renders the Codex entry and re-registers the Claude
Code entry on the next apply.

How the gate decides:

- **Which folder.** It takes `CLAUDE_PROJECT_DIR` when it is set (Claude Code
  sets it to the project root), otherwise its own working folder (Codex
  starts it in the session's folder). It resolves symlinks and compares by
  whole path segments, so `~/repos-old` is not inside `~/repos`.
- **For the whole session.** Neither agent restarts a user-scope server when
  the session changes folder (Claude Code's `/cd`), so a session started in
  the work folder keeps the servers and one started elsewhere never gets
  them.
- **Inside the work folder.** It reads `work.env` (it must be a regular file
  owned by the user with no group or other permission bits), adds each name
  with a value
  to the environment, overriding an inherited one, and replaces itself with
  the server (`exec`). No extra process stays, and the server runs through
  `nvm-exec` with `NODE_VERSION=default`, as today. When the file is missing
  or unsafe (any group or other permission bit set), it exits with an error
  naming the file, so the agent shows the server as failed, which is the
  true state.
- **Anywhere else.** It never starts the server and reads no file. It answers
  the MCP `initialize` request with no capabilities (no `tools`, so Claude
  Code's `/mcp` does not flag it), answers `ping`, answers `tools/list`,
  `prompts/list`, `resources/list` and `resources/templates/list` with empty
  lists, replies "method not found" to anything else, and exits when the
  agent closes its input. The empty lists matter for Codex: it asks for the
  tool list at startup whatever the server declares, and an error there
  fails the server's startup (`codex-rs/codex-mcp/src/rmcp_client.rs`). So
  neither agent shows an error, and neither gets tools (no context cost).
  What stays is one small Workbench process per session, with no Node; the
  builder measures it and the plan text states it. The builder confirms in
  both agents that this server stays quiet and that an empty tool list does
  not hold Codex's startup until its timeout.

Cost inside the work folder: every Claude Code session there starts the two
servers, as every Claude Code session anywhere does today. Every Codex
session there now starts them too, where today Codex never does; the builder
records whether Codex sub-agent threads start their own copies.

A `work`-only machine uses the same gate, so changing its role to `both`
later needs nothing new. A machine without the work role registers no
dispatch servers (section 4.11).

The gate is a hidden subcommand in `internal/cli/`, like
`workbench vscode-settings`.

The "Path-scoped tool policy" text stays for what the gate cannot cover: the
CodeRabbit plugin in Codex, which is not a server Workbench launches. It
names the folder answers.

## 5. Left to the builder

- Whether the `/etc/os-release` read moves into `internal/platform`.
- The exact plan wording.
- The record's file name and format, and whether one record file holds all
  four steps or each step has its own.
- Whether `zsh` is installed on AlmaLinux: the managed `.zshrc` deploys only
  on macOS, so it is not needed.
- How the shell hook avoids reading the env files before every prompt (for
  example only when the folder or the file's change time differs).
- How small the gate's idle process is, measured in both agents
  (section 4.14).

## 6. Decisions

Decided by the owner on Oct 21, 2026:

- **EPEL** is its own step, on by default (section 4.3). Researched options
  were: its own step; a part of System packages; never installed, printing
  the command; or per-user GitHub downloads of bat and fd, which bat's
  releases cannot back with a checksum file.
- **The Windows side** depends on no particular distribution: any one, or the
  only one, may write it, and the record on Windows keeps two from fighting
  (section 4.7).
- **The Ubuntu distribution** becomes `personal`, switched before this change
  is installed there (section 4.1).
- **Work credentials and addresses** live in one `work.env`. The shells
  export its names inside the work folder for the owner's tools and the
  agents' commands, and the launch gate reads it for the dispatch servers
  (sections 4.11 and 4.14). Researched and
  rejected: new vendor-hosted MCP servers (the owner uses the tokens for their
  own dispatch servers and for direct ticket work, and an extra MCP server
  per agent process costs memory and context), and shell wrappers that fetch
  tokens at launch.
- **Bitwarden** is removed (section 4.12).
- **Saved tokens** move into `work.env` through a numbered migration, and
  personal machines get a matching `personal.env` (section 4.11).
- **The role and each role's folder** are asked on every machine, defaults
  `~/repos` and `~/dev` (section 4.13).
- **Each env file loads only inside its own folder** (section 4.11).
- **Work MCP servers start only inside the work folder**, for both agents,
  including on a machine that holds both roles (section 4.14).
- **The `create-spec` alias**, which pointed at a session scratch file, was
  removed on its own (commit `0016ebb`).

## 7. Calls made in this design

- Major-version check for AlmaLinux (any 10.x).
- Pick the package manager at run time, not in templates; the `epel` step is
  listed from a package-manager fact in `internal/platform`.
- dnf names inline beside the apt names; moving both to `packages.toml` is a
  follow-up.
- Keep `gh` as the per-user download on every distribution.
- Source `/etc/bashrc` from the managed `.bashrc` when present.
- A saved tick never takes over a Windows step another installed
  distribution writes; only `apply --choose` moves it. The record is a plan
  input.
- Folders, and the Windows start folder, follow the role.
- Folder answers are home-relative, limited to plain characters, and never
  equal or nested.
- A shell hook rather than direnv. It runs on every change of folder, and it
  unsets the other folder's names whoever set them. The `claude()` and
  `codex()` wrappers run it before starting an agent, and a `code()` wrapper
  starts the editor with no names.
- `env-files` and `claude-mcp` follow the `migrations` pattern: a shared
  script calls a hidden subcommand, and the names stay in Go.
- The gate answers the list requests with empty lists, because Codex fails a
  server whose tool list returns an error.
- The gate takes its folder and env file as arguments rendered at apply time,
  reads `work.env` itself, and passes no credential through either agent's
  config, so there is no `${VAR}` in Claude Code and no `env_vars` in Codex.
- Codex's dispatch entries become `enabled = true`, since the gate limits
  them now.
- Workbench changes Claude Code's entries only through `claude mcp`, and
  reports, never removes, same-named local-scope entries; project-scope
  `.mcp.json` entries are not looked for.
- Migration 0005 also fills the email and the two addresses, so ticket work
  keeps working after the upgrade, and it never merges into an existing
  `work.env`.
- Two safeguard tests: no token loss in 0005, and no work folder match
  outside the work folder in the gate.

## 8. Follow-ups (not in this change)

- A styled Terminal profile for each Workbench distribution. Windows
  Terminal's JSON fragments can add profiles and schemes and update existing
  profiles by GUID, but cannot set global settings such as `defaultProfile`
  ([Microsoft Learn](https://learn.microsoft.com/en-us/windows/terminal/json-fragment-extensions)).
  Whether a fragment in Workbench's own folder can update the profile WSL
  wrote in `Fragments\Microsoft.WSL` is not documented; a spike settles it.
- Install names for both apt and dnf in `home/.chezmoidata/packages.toml`,
  the owner `AGENTS.md` names, in place of the inline names.
