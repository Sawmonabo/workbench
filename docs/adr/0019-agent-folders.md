# 19. The costs and guard folders move under agent/, by the holder of their lock

- Status: Accepted, November 22, 2026
- Amends: [AGENTS.md](../../AGENTS.md), "Greenfield: … No migrations", and
  [ADR 8](0008-migrations-move-workbench-files.md)

## Context

The files of `workbench agent costs` and `workbench agent guard` sat in
`costs/` and `guard/` at the top of Workbench's data and state folders, where
top-level commands of those names would keep them. They live under
`agent/`: `agent/costs/` in the data and state folders and `agent/guard/` in
the state folder. A machine updated from a release before has the old
folders, and processes of that release may still run in them for a while
after the update: the guard's watcher, which checks each second that its lock
file is still at its old path and exits when it is not, and hooks and import
workers started just before.

A folder moves in one rename inside its runtime folder, so the ledger moves
with its `-wal` file, open files stay open and a lock stays the same lock at
its new path. The open question is who may move a folder that processes are
using.

## Options considered

- **A. A folder is used where it is, and only the holder of its own lock
  moves it.** Chosen. The watcher's lock guards the guard folder and the
  import lock the costs folders. Until a folder has moved, every entry works
  in it at its old path, so nothing waits and nothing goes unguarded, and the
  holder of the lock, the only writer that matters, moves it. The watcher of
  the release before hands the watch over as it does at every update, and the
  new watcher moves the folder before its first tick, so nothing it knows is
  lost.
- **B. Move at once and start the watcher anew.** The watcher of the release
  before exits when its folder goes, and what only it knew is lost: the
  detached programs it follows, the calls it passed over, a stop under way.
- **C. Refuse to work until the folder has moved**, as the first costs move
  does. The guard would not guard, and a session's sign-in would not be
  recorded, in the meantime.

## Decision

- Each folder moves by swapping it, in one system call (`renameat2` with
  `RENAME_EXCHANGE` on Linux, `renamex_np` with `RENAME_SWAP` on macOS), with
  a 0600 file holding the identifier of this start of the system. The folder
  is then at its new path and the file at the old one, with no moment between,
  so a process of the release before fails there instead of making a new,
  empty ledger or taking a new lock. An entry deletes the file once the
  identifier is another start's. A move never replaces a folder.
- One function per part says where its folder is: the old one while it is a
  folder and either the new one is not or the old one's lock is held, else the
  new one. An entry never makes a folder at an old path. Every entry that asks
  whether a watcher or an import holds a lock (that function, a pre hook,
  `workbench migrate` and `doctor`, `agent costs status`) asks with a shared
  lock taken without waiting for an instant: only the exclusive lock of a
  watcher or an import refuses it, so two entries asking at once never take
  each other for one.
- A watcher of this release that holds the lock in the old folder moves it
  before its first tick. A hook, `workbench migrate` and every costs entry
  take the lock exclusively without waiting and move only when they get it;
  when it is held, they work in the old folder, and the holder, or the next
  entry after it, moves it. A watcher or an import worker taking its lock
  tries six times 20 ms apart, so an asker's instant does not turn it away.
- Migration `0007-agent-folders` lists the moves. A folder a watcher or an
  import works in, or one on a file system that cannot swap two names (HFS+,
  which `CanSwap` tells by the volume's `VOL_CAP_INT_RENAME_SWAP`), counts as
  done for the migration and blocks no apply; `migrate --dry-run` and
  `doctor` say why it stays. An overlay file system refuses to swap only a
  folder of an image's lower layer (`EXDEV`), which no call tells without
  trying: such a folder stays listed as pending, and each run tries it,
  leaves it in use where it is, says why and succeeds, so it blocks no apply
  either. An old folder beside one already in use is not
  merged: its logs, output files, locks, sockets, downloaded price lists and
  empty folders are deleted, under the old folder's lock held shared, which
  keeps a watcher or an import from starting there and which no asker takes
  for one, and anything else stays and is named.

## Consequences

- A migration may swap a folder Workbench wrote inside its runtime folders
  with a small file, which stays at the old path until the machine restarts,
  and delete a socket; AGENTS.md records it beside the other migration rules.
- Every costs and guard entry looks at both paths until its folder has moved,
  which is the one place the old names appear outside the move.
- On HFS+, and for a folder in an image's lower layer on an overlay file
  system, the folders stay at their old paths and are used there.
- The guard's socket path is six bytes longer; on macOS a user name longer
  than 26 characters puts it past the 100 bytes Workbench accepts for one, and
  `agent guard` then says it cannot reach the watcher while the guard still
  guards.
- A release before this one does not find the moved files.

## Sources

- [Agent folders design](../superpowers/specs/2026-11-19-agent-folders-design.md)
- [Workbench migrations design](../superpowers/specs/2026-10-14-workbench-migrations-design.md)
