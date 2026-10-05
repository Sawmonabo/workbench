# 4. Revert keeps folders that hold other files

- Status: Accepted, October 5, 2026
- Supersedes: in [the contracts](../superpowers/specs/workbench-contracts.md),
  the refusal of a whole revert when a folder it would remove holds files
  Workbench did not write.

## Context

A first apply on a new machine creates folders such as `~/.config/tmux` and
VS Code's settings folder. Later steps of the same apply, or the programs
themselves, put their own files there: the tmux plugins, VS Code's own data.
Revert removes what the apply created, but it never deletes a folder's contents,
so it could not remove those folders. It refused the whole revert instead, and
restored nothing. In clean macOS VMs a revert of a first apply was always
refused this way.

## How similar tools handle it

- **[dpkg](https://salsa.debian.org/dpkg-team/dpkg/-/blob/main/src/main/remove.c)**,
  Debian's package manager, removes a package's files and leaves a directory that
  is not empty, with the warning "directory '…' not empty so not removed".

## Options considered

- **A. Keep such folders and restore the rest.** Chosen.
- **B. Keep refusing** until the person empties the folders by hand.

## Decision

- Revert leaves each folder it would remove that now holds something it would
  not remove (a file Workbench did not write, or a folder kept for this reason),
  so a folder holding a kept folder is kept too.
- Everything else is restored. Each kept folder is named, with what is in it, in
  the revert plan and its result.
- A kept folder keeps its recorded outcome, so undoing the revert, or a later
  revert once the folder is empty, works from what is actually there.
- A later edit to a file Workbench wrote still stops the whole revert, and a
  forward apply that would remove such a folder is still refused.

## Consequences

- A revert of a first apply succeeds and leaves `~/.config/tmux` with its
  plugins, and similar folders, in place.
- Nothing Workbench did not write is ever deleted.

## Sources

- [dpkg `remove.c`](https://salsa.debian.org/dpkg-team/dpkg/-/blob/main/src/main/remove.c)
