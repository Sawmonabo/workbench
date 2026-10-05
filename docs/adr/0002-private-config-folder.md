# 2. Keep `~/.config` private

- Status: Accepted, October 5, 2026
- Supersedes: in [the contracts](../superpowers/specs/workbench-contracts.md),
  `~/.config` among the "missing parents … created 0755"; and in
  [acceptance](../acceptance.md), the Linux entry where a `~/.config` at 0700
  "lists a mode-only edit to 0755".

## Context

The machine source declares `home/dot_config`, a folder that is not private. So
chezmoi gives `~/.config` mode 0755, and it changes an existing private folder
to that mode. chezmoi 2.70.3 in a scratch destination showed `old mode 40700`,
`new mode 40755` for it, and applying made it `drwxr-xr-x`. Other accounts on
the machine can then list the folder and read any settings file inside that is
not itself private.

The [XDG Base Directory Specification](https://specifications.freedesktop.org/basedir-spec/basedir-spec-latest),
which defines `~/.config`, says a missing folder "should be created with
permission 0700", and "if the destination directory exists already the
permissions should not be changed". Programs that follow it create `~/.config`
private, so it is often private already.

On Linux, `~/.config` also holds Workbench's own configuration
(`~/.config/workbench`). `workbench update` creates it as 0755 when it is
missing. On macOS, `~/Library/Application Support` had the same problem, and
Workbench now keeps it private (`private_Application Support`, 0700, the macOS
default).

## How similar tools handle it

- **[chezmoi](https://chezmoi.io/reference/configuration-file/umask/)**, the
  engine Workbench uses, keeps a folder at 0700 when its source name starts
  with `private_`. Otherwise it applies the mode its umask gives.
- **[yadm](https://manpages.ubuntu.com/manpages/bionic/man1/yadm.1.html)**, another
  dotfiles manager, removes other users' access from `~/.ssh`, `~/.gnupg` and
  encrypted files automatically. It only ever tightens permissions.

## Options considered

- **A. Always private.** Create `~/.config` as 0700, and tighten an existing one
  to 0700 once. Chosen.
- **B. Follow the specification strictly.** Create a missing `~/.config` as
  0700, and never change an existing one. chezmoi has no setting that leaves a
  managed folder's mode alone, so Workbench would need code of its own to
  override chezmoi for this one folder.
- **C. Keep the current behavior,** 0755, which opens a private `~/.config` to
  other accounts, against the specification.

## Decision

- The machine source names the folder `home/private_dot_config`, so chezmoi
  keeps `~/.config` at 0700.
- Workbench creates a missing `~/.config` as 0700. Other missing parents of its
  folders stay 0755.
- A `~/.config` at 0755 is tightened to 0700 once. The plan shows that as a
  mode change, and revert restores the old mode.

## Consequences

- On most machines nothing visible changes. Where the folder was open, the first
  plan shows one line, `~/.config` `0755 → 0700`.
- Other accounts on the machine can no longer read settings in `~/.config`.
  Programs the owner runs are not affected, because they run as the owner.
- The first apply on a new machine shows no change for the folder, because
  Workbench created it as 0700 already.
- **Open follow-up question,** not changed by this decision: the specification
  also asks for 0700 on the data and state folders (`~/.local/share`,
  `~/.local/state`), which Workbench creates as 0755 when they are missing.

## Sources

- [XDG Base Directory Specification](https://specifications.freedesktop.org/basedir-spec/basedir-spec-latest)
- [chezmoi `umask` and `private_` attributes](https://chezmoi.io/reference/configuration-file/umask/)
- [yadm manual](https://manpages.ubuntu.com/manpages/bionic/man1/yadm.1.html)
