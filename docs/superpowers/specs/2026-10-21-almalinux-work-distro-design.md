# AlmaLinux 10 work distribution design

Status: design, Oct 21, 2026. It makes AlmaLinux 10 a Linux target, so one
Windows PC can run Workbench in two WSL distributions: the Ubuntu one for
personal work and an AlmaLinux 10 one for work. Each keeps its own machine
answers. The work setup itself needs no new mechanism: the existing
`machine_role` answer already separates it. What stops it today is that
Workbench refuses every distribution but Ubuntu, and its package step only
knows apt.

## 1. Why

Work moved to an AlmaLinux 10 WSL distribution on the same Windows PC. Today
Workbench on that distribution stops at the platform check
(`internal/machine/host_linux.go` accepts only Ubuntu 22.04, 24.04 and 26.04),
so the work setup can only live on the Ubuntu distribution, next to the
personal one. `docs/linux.md` records the Ubuntu-only rule as a decision; this
design replaces it for AlmaLinux 10.

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

So EPEL is needed for `bat` and `fd`. Rust is not needed for them on
AlmaLinux. The Runtime managers step still installs rustup for the cargo
tools and runtimes, as it does on Ubuntu.

## 3. Goals, non-goals, anti-goals

Goals:

- `workbench apply` plans and applies on AlmaLinux 10 (any 10.x) on amd64 and
  arm64, natively and on WSL, with the same steps, questions and consent as
  Ubuntu.
- The work distribution is just a machine whose role is `work`. No new
  question, file or mode separates work from personal.
- Work lives in `~/repos` and personal in `~/dev`, as now.
- Any number of Workbench distributions on one Windows user, or just one,
  without any of them undoing another's Windows-side changes, and without
  Workbench naming or needing a particular distribution (section 4.7).

Non-goals:

- Rocky Linux, RHEL, CentOS Stream, Fedora or any other `ID_LIKE` match.
  Only `ID=almalinux` is accepted.
- A Windows Terminal settings file with a styled profile for more than one
  distribution (follow-up, section 8).
- Changing where the work web addresses and tool folders are kept, until
  the owner decides (section 6).
- Changing the Ubuntu targets or the Ubuntu package path.

Anti-goals (a change that does these is wrong even if it works):

- A second renderer, a distribution argument in `render-check.sh` that
  changes rendered files, or a template branch on `.chezmoi.osRelease`.
- Silent fallbacks: a missing package manager or repository counts as a
  failed or blocked part, never as success.
- Turning on any repository other than EPEL, or changing a repository the
  owner turned on or off.
- Provisioning the developer's live AlmaLinux distribution to test the code.

## 4. Design

### 4.1 Work and personal are roles, not distributions

Nothing new. The saved answers live per machine in `machine.toml`, never in
the repository, and each WSL distribution has its own. The owner sets the
AlmaLinux distribution to `work` with `workbench init --ask machine_role`
(which asks the tokens `work` needs) and then runs `apply`. The owner will
change the Ubuntu distribution to `personal` the same way; nothing in this
design depends on Ubuntu being there, and a PC with only the AlmaLinux
distribution works the same.

The owner installs Workbench on the AlmaLinux distribution from a release,
not a checkout. Per `AGENTS.md`, unreleased code reaches a real machine only
as a pre-release the owner tags, so this change reaches that distribution
through a pre-release or the next release.

With role `work`: `~/.gitconfig` uses the single `email` answer and has no
per-folder override; Codex gets the work MCP servers and trusts `~/repos`;
`~/dev` trust is removed; the shells get the work block and the `bw` wrapper;
the work tools step installs the Bitwarden CLI.

### 4.2 The platform check accepts AlmaLinux 10

`hostVersion` in `internal/machine/host_linux.go` also marks the platform
complete when `ID` is `almalinux` and the major part of `VERSION_ID` is `10`.
AlmaLinux 10 gets a minor release every six months under one major version
until 2035, unlike Ubuntu's fixed LTS list, so the check uses the major
version only. The message stays `ID VERSION_ID/arch`, plus the WSL note.

The `forbidigo` rule in `.golangci.yml` forbids only `runtime.GOOS`, and the
file already reads `/etc/os-release` behind its `_linux.go` suffix, so the
check stays where it is. Moving the `/etc/os-release` read into
`internal/platform` (ADR 0012) is the builder's call; it is not needed for
this change.

### 4.3 The package step learns dnf

`home/.chezmoiscripts/linux/run_once_before_00-packages.sh.tmpl` picks its
package manager at run time, the way its tmux part already does: `apt-get`
when present, otherwise `dnf`, otherwise the part fails with the reason. No
template branches on the distribution, so `render-check.sh` renders the same
files as before.

On dnf, as root through `sudo`, like the apt lines:

1. **EPEL** is its own step, `epel`, beside `linux-packages`, and on by
   default like it (not under Optional). It is listed only on a host whose
   package manager is dnf. Its part runs first in the same packages script,
   behind its own `WORKBENCH_EFFECT_EPEL` switch, the way `work-tools` already
   shares that script: when `rpm -q epel-release` fails it runs
   `sudo dnf install -y epel-release` (from AlmaLinux's `extras`
   repository). Its probe line is `epel: = EPEL is on` when the package is
   installed, otherwise `epel: turn on EPEL (uses sudo)`. If the owner turns
   the step off, the bat and fd parts fail saying EPEL is off. It runs no
   `dnf config-manager` and no `crb`: CRB is on by default, and Workbench
   does not change a repository the owner set. Why a step of its own:
   `workbench-contracts.md` (the paragraph on ordinary apply) asks that
   changes outside the user's files, such as Homebrew tap removal, appear as
   individually named effects, and a system-wide package source is that kind
   of change. On the owner's AlmaLinux distribution EPEL is already on, so
   the step has nothing to do there.
2. **Base packages**, replacing `build-essential curl unzip git
   ca-certificates fontconfig`: `dnf install -y gcc gcc-c++ make curl unzip
   git ca-certificates fontconfig`. Today the base part is skipped without a
   word when `apt-get` is missing, so the font part later fails on the
   missing `fc-cache`; on AlmaLinux it now runs.
3. **bat and fd**: `dnf install -y bat fd-find`. The commands are already
   `bat` and `fd`, so no `~/.local/bin` link is made. The cargo fallback
   stays only for a system with neither apt nor dnf, where it already exists.
4. **tmux**: unchanged (it already has the dnf branch). AppStream's tmux is
   3.3a; the managed `tmux.conf` uses TPM and the Dracula theme and sets no
   option newer than 3.3. The builder confirms the theme loads in the
   disposable check.
5. **gh, oh-my-posh, the Nerd Font, the Bitwarden CLI**: unchanged. They are
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

The apt lines wait up to ten minutes for the package lock. The builder checks
how dnf 4 behaves when another dnf holds its lock and makes dnf wait or fail
with the reason the same way.

`render-check.sh` treats `apt-get install` as a write in its probe check
(line 97); `dnf install` is added there.

### 4.4 Folders and the Windows start folder follow the role

The package script creates `~/repos` and `~/dev` on every machine
(`run_once_before_00-packages.sh.tmpl`, the "standard directories" line). It
now creates `~/repos` when `.has_work` and `~/dev` when `.has_personal`, so a
work-only distribution does not get an empty `~/dev`.

The Windows Terminal WSL profile and the PowerShell profile start in
`~/repos` on every machine (`run_before_00-packages-windows.sh.tmpl`: the
`wsl.exe -d … --cd ~/repos` command line, the `startingDirectory` fallback
and `$wslRepos`). With the folders following the role, a personal-only
machine would open into a folder that does not exist, so the start folder
follows the role in the same change: `~/repos` when `.has_work` (so `work`
and `both` are unchanged), otherwise `~/dev`. The Ubuntu distribution on
this PC already has no `~/repos`.

### 4.5 Plan text names the package manager

The `linux-packages` effect (`internal/machine/effects.go`, around line 183)
says "apt" in its Description, Summary, What and Undo. It names both:
packages come from apt on Ubuntu and from dnf on AlmaLinux. Undo names `apt`
or `dnf remove`. EPEL is its own step (section 4.3) and is not mentioned
here. The text is static (the effect is shared by both distributions),
so it names both rather than reading the host. `docs/usage.md` and `docs/linux.md` say the same.

### 4.6 bash reads the system file on AlmaLinux

The managed `.bashrc` (`home/private_dot_bashrc.tmpl`) sources `/etc/bashrc`
near the top, after the not-interactive return and before anything that sets
`PROMPT_COMMAND` (oh-my-posh), guarded by `[ -f /etc/bashrc ]`. Ubuntu has no
`/etc/bashrc`, so nothing changes there. On AlmaLinux this keeps
`/etc/profile.d` working in non-login shells, as the stock file does. The
Debian-only lines (`lesspipe`, `debian_chroot`) already test for their files
and stay.

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
   (checked against `wsl.exe --list --quiet`, which the deploy script already
   asks): a distribution with the step ticked writes, and records itself.
   A PC with one distribution always lands here.
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
one a person moved it to. The record holds only distribution names, never answers or
secrets. Removing it is safe: rule 1 applies again. `revert` and the effects'
Undo text name it. Workbench reads and writes it from Linux through the
Windows paths it already resolves, with the same 60-second cap and blocked
result as its other Windows calls.

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
(section 4.7, rule 1). After the owner switches the role to `work` and
installs a release with this change, the first `apply` at a terminal asks,
because `linux-packages` and `work-tools` are not in `decided` (the editor
extension step is never planned on WSL, `internal/machine/effects_linux.go`). It plans
the managed `.bashrc` and `.gitconfig` in place of the hand-written ones; the
lines added there by hand (the promptctl hook, `clear-cache`) are already in
the managed `.bashrc` for a work WSL machine. Commands already in
`~/.local/bin` that Workbench did not make are left alone, as now; `uv`
already being there means the Runtime managers step finds it and does not
install its own. The system `golang` package stays; the user-local Go comes
first on `PATH`.

None of this is run by the builder: it is the owner's step on the live
distribution, after the disposable checks in section 4.10.

### 4.9 Decision record and docs

- A new ADR (next free number) records that AlmaLinux 10 joins the Linux
  targets, why the major-version check, why EPEL is its own step, and why
  the writer of the shared Windows files is recorded on Windows. It names the Ubuntu-only
  decision in `docs/linux.md` as replaced.
- `docs/linux.md`: targets, the dnf path, EPEL, the bash note.
- `docs/superpowers/specs/workbench-contracts.md`: an AlmaLinux 10 row in the
  targets table and dnf/EPEL in the prerequisites paragraph.
- `README.md` targets line, `docs/usage.md` the `epel` step and the
  take-over line, `docs/wsl.md` the section 4.7 rules, `docs/acceptance.md` the checks actually run.

### 4.10 Checks

- Routine gates: `golangci-lint fmt`, `golangci-lint config verify`,
  `golangci-lint run ./...`, `go build ./...`, `go test ./...`.
- `scripts/render-check.sh` for each role and mode, with and without `wsl`:
  the rendered files must be unchanged apart from the edited scripts and
  `.bashrc`.
- A disposable `almalinux:10` container with a new non-root user and sudo,
  from a local bundle: `apply --local-build --dry-run --json`, then
  `--approve-plan`. It must install EPEL, the base packages, bat, fd, tmux,
  podman, the font (with `fc-cache`), and end with every chosen step complete.
  The same in an Ubuntu 24.04 container shows the apt path unchanged.
- For the WSL path, a second AlmaLinux 10 distribution imported under another
  name with a disposable home, never the live one. If that is not done, the
  acceptance notes say the WSL path on AlmaLinux was not run.
- arm64 is implemented but recorded as unqualified, as for Ubuntu, unless an
  arm64 run is made.
- The writer record (section 4.7) lives on the real Windows user, so its
  four rules are checked only with a disposable Windows user, or by the
  owner after release; otherwise the acceptance notes say they were not run
  on a real host. The rules' plan lines can still be checked in a probe with
  a record file placed by hand in a disposable folder.

## 5. Left to the builder

- Whether the `/etc/os-release` read moves into `internal/platform`.
- The exact plan wording.
- The record's file name and format, and whether one record file holds all
  four steps or each step has its own.
- How dnf waits on its lock (section 4.3).
- Whether `zsh` is installed on AlmaLinux: the managed `.zshrc` deploys only
  on macOS, so it is not needed.

## 6. Decisions and the one open question

Decided by the owner on Oct 21, 2026:

- **EPEL** is its own step, on by default (section 4.3). Researched options
  were: its own step; a part of System packages; never installed, printing
  the command; or per-user GitHub downloads of bat and fd, which bat's
  releases cannot back with a checksum file.
- **The Windows side** depends on no particular distribution: any one, or the
  only one, may write it, and the record on Windows keeps two from fighting
  (section 4.7).
- **The Ubuntu distribution** becomes `personal`. `init --ask machine_role`
  drops the answers the new role does not use, so the Jira and GitLab tokens
  leave that distribution, and Codex there loses the work servers and the
  `~/repos` trust.

Open: **where the fixed work values are kept**: the Jira and GitLab
addresses and the work-tools checkout paths under `~/repos` in
`home/.chezmoitemplates/codex-config.toml.tmpl`. (a) In the template, as now.
(b) In a static data file under `home/.chezmoidata/`, same values. (c) As
machine answers asked by `init`. (a) and (b) produce the same file on every
work machine and change only through a new release; (c) changes on one
machine through `workbench init --ask KEY`, with no release. The
`create-spec` alias pointing at a dated file in that checkout is a separate
call: keep it, or move it to the unmanaged `~/.bash_aliases` the managed
`.bashrc` already sources.

## 7. Calls made in this design

- Major-version check for AlmaLinux (any 10.x).
- Pick the package manager at run time, not in templates.
- dnf names inline beside the apt names; moving both to `packages.toml` is a
  follow-up.
- Keep `gh` as the per-user download on every distribution.
- Source `/etc/bashrc` from the managed `.bashrc` when present.
- A saved tick never takes over a Windows step another installed
  distribution writes; only `apply --choose` moves it.
- Folders, and the Windows start folder, follow the role.

## 8. Follow-ups (not in this change)

- A styled Terminal profile for each Workbench distribution. Windows
  Terminal's JSON fragments can add profiles and schemes and update existing
  profiles by GUID, but cannot set global settings such as `defaultProfile`
  ([Microsoft Learn](https://learn.microsoft.com/en-us/windows/terminal/json-fragment-extensions)).
  Whether a fragment in Workbench's own folder can update the profile WSL
  wrote in `Fragments\Microsoft.WSL` is not documented; a spike settles it.
- Install names for both apt and dnf in `home/.chezmoidata/packages.toml`,
  the owner `AGENTS.md` names, in place of the inline names.
