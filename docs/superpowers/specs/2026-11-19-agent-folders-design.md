# Agent folders design

Workbench's runtime folders follow its commands: the files of
`workbench agent costs` and `workbench agent guard` live under `agent/`.

## 1. Intent

The costs and guard commands are `workbench agent costs` and
`workbench agent guard`, and their code is `internal/agent/costs` and
`internal/agent/guard`. Their files still sit in `costs/` and `guard/` at
the top of the runtime folders, where top-level commands of those names
would keep them. A person who knows the command should find its files at
the same path.

## 2. What moves

`<data>` and `<state>` are Workbench's runtime folders (`internal/platform`
`RuntimeFolders`).

| From | To | Holds |
| --- | --- | --- |
| `<data>/costs/` | `<data>/agent/costs/` | `ledger.sqlite` with its `-wal` and `-shm` |
| `<state>/costs/` | `<state>/agent/costs/` | `ingest.log`, `ingest.err`, `ingest.lock` |
| `<state>/guard/` | `<state>/agent/guard/` | `watch.log`, `watch.err`, `watch.lock`, `guard.sock`, `notes/`, `agents/`, `killed/`, the history |

A folder moves with whatever is in it: the downloaded price lists in
`<data>/costs/<tool>/` go along, and the costs prices design deletes them
afterwards. `<config>/costs/`, which held only the price overrides that
design stops reading, does not move.

Nothing else moves: `machine.toml`, the env files, `state.json`,
`activation.json`, `migrations.json`, `locks/`, `checkpoints/`, `chezmoi/`,
`releases/`, `tools/`, `application-contexts/` and the cache belong to no
`agent` command.

## 3. Goals, non-goals, anti-goals

Goals:

- Every path the plan, `doctor` and the documents name is the new one.
- A machine with the old folders is moved once, without a question, with
  every byte kept, while agents work.

Non-goals:

- No change to a file's name or content.
- No move of any folder section 2 does not list.
- Going back to a release before this one: it does not find the moved
  files.

Anti-goals:

- A new, empty ledger made beside the old one.
- A ledger moved apart from its `-wal` file, which can hold commits the main
  file lacks.
- Two watchers acting at once, a note no watcher reads, or a running
  command or detached program that stops being watched.
- A folder renamed under a process that is working in it.

## 4. Approaches

### 4.1 How a folder moves

| Approach | Trade-off |
| --- | --- |
| **One folder rename per row of section 2.** Chosen. | A rename inside one folder tree is one step for the system: the ledger and its `-wal` move together, files a running process holds stay open, a lock stays the same lock at its new path, and nothing is copied however large the ledger is. |
| The copy the first costs move makes (a vacuumed copy, checked, then the original set aside) | Built for an old ledger on another disk. Here it copies a ledger of any size for nothing. |
| Moving file by file | A stop between two files leaves a ledger without its `-wal`, or half a guard folder with a watcher running in it. |

### 4.2 Who may move a folder that processes are using

After an update, processes of the release before may still run: a watcher,
which checks each second that its lock file is still at its old path and
exits when it is not, and hooks and import workers started just before.

| Approach | Trade-off |
| --- | --- |
| **A folder is used where it is, and only the holder of its own lock moves it.** Chosen. | The watcher's lock guards the guard folder and the import lock the costs folders. Until a folder has moved, every entry works in it at its old path, so nothing waits and nothing goes unguarded; whoever holds the lock is the only writer that matters and moves it. The watcher of the release before hands the watch over as it does at every update, and the new watcher moves the folder before its first tick, so nothing it knows is lost. |
| Move at once and start the watcher anew | The watcher of the release before exits when its folder goes. What only it knew is lost: the detached programs it follows, the calls it passed over, a stop under way. |
| Refuse to work until the folder has moved, as the first costs move does | The guard would not guard, and a session's sign-in would not be recorded, in the meantime. |

## 5. Rules

### 5.1 Both moves

1. **Migration `0007-agent-folders`** lists the guard move and the costs
   move. A move is pending while an old path holds a folder and done when
   none does. Everywhere in this design a path "exists" when it is a
   folder; a plain file there is no folder.
2. **Where a folder is.** One function per part answers for every entry of
   that part: the old folder while it exists and either the new one does
   not or the old one's lock is held; else the new one. An entry never
   makes a folder, or a file's parent, at an old path. An entry that fails
   in any read or write while the old path is no longer a folder asks again
   and does its work again, once.
3. **A move swaps.** The mover makes the private `agent/` folder and a
   plain file, mode 0600, at the new path, holding the identifier of this
   start of the system (`/proc/sys/kernel/random/boot_id` on Linux,
   `kern.bootsessionuuid` on macOS), replacing a plain file a
   stopped move left there, then swaps that file with the old folder in one
   system call (`renameat2` with `RENAME_EXCHANGE` on Linux, `renamex_np`
   with `RENAME_SWAP` on macOS). The folder is then at the new path and the
   file at the old, with no moment between. A process of the release before
   that names the old folder fails there, where it would otherwise make a
   new, empty one: an import worker cannot make a ledger, and a watcher
   cannot take a lock. A move never replaces a folder.
4. **The file at the old path** stays until the machine has restarted: an
   entry that finds one holding another start's identifier deletes it,
   since no process from before can still run. The identifier, not a time,
   decides: a clock set forward after a sleep moves the time a system says
   it started.
5. **A file system that cannot swap** two names moves nothing. The folders
   stay, every entry uses them where they are (rule 2), the migration
   counts as done, and `workbench migrate` and `doctor` say why. Where only
   the swap tells, as on an overlay file system that keeps the folder in an
   image's lower layer, the migration stays pending: each run tries, leaves
   the folder in use where it is, says why and succeeds.
6. **Old and new both exist, and the old folder's lock is free.** The new
   folder is in use and nothing is merged into it. From the old one a move
   deletes what Workbench may delete: logs, `.err` output files, downloaded
   price lists, sockets, locks and empty folders, then the folder when it
   is empty. Anything else (a ledger, a note) stays, and `workbench
   migrate` and `doctor` name it.
7. **A lock taken to move is let go after the move.** The entry then
   carries on as it always does and takes what locks it needs anew, at the
   new path.

### 5.2 The guard folder

8. **Only the holder of the watcher's lock moves the guard folder.**
   - A watcher of this release that holds the lock while the folder is at
     its old path, whether it was handed the watch or took the lock
     itself, moves the folder before its first tick and works at the new
     path. One handed the watch opens nothing in the folder until it has
     read the word to start; the watcher before it writes nothing by path
     after sending that word. Its lock and its socket are the same files
     at the new path. A listener removes its socket file by the name it was
     bound with, which is the old path, so the watcher removes the file at
     the folder's current path itself. When it cannot move (rule 5, or a new folder already
     exists), it works in the old folder.
   - A hook, or `workbench migrate`, that finds the old folder takes the
     watcher's lock there without waiting. When it gets it, no watcher
     runs: it moves, lets the lock go, and then starts a watcher when the
     folder holds a note, as every mover does, since a pre hook that found
     the lock held in that instant started none.
   - When the lock is held, a watcher is running in the old folder. The
     hook writes its note there, where that watcher reads it, and the
     command is guarded as before. `workbench migrate` says the watcher
     moves the folder when it next hands over or stops.
9. **A handover that does not happen.** A watcher of the release before
   that cannot hand over keeps guarding in the old folder, as it does
   today, until it has been idle for ten minutes and exits. The next hook
   then finds the lock free and moves the folder.
10. **The socket's path** is six bytes longer. On macOS a user name longer
    than 26 characters puts it past the 100 bytes a socket path may have,
    where 32 did before: the guard still guards, and `agent guard` says it
    cannot reach the watcher.

### 5.3 The costs folders

11. **Only the holder of the import lock moves the costs folders.** A costs
    entry (the hook that starts the worker, the worker, `status`, the
    reports, `rates`) that finds an old costs folder takes the import lock
    at its old path (`<state>/costs/ingest.lock`) without waiting.
    - When it gets it, no import runs: it moves the data folder, then the
      state folder, and lets the lock go.
    - When the lock is held, an import is running in the old folders. The
      entry works there as it does today when an import runs: a worker
      binds its session and waits or ends, a report reads the ledger.

    A stop between the two moves leaves the state folder pending, and the
    next entry finishes it under the same lock.
12. **Before the first costs move.** `0001-costs-folders` and the worker's
    gate run the costs move of this design first, so the first costs move
    finds any ledger already at its new path and never takes another for
    the one in use. `0001`'s text names `agent/costs/`.

## 6. Ownership and documents

| Concern | Owner |
| --- | --- |
| The list, the check and the run record | `internal/migrate/0007_agent_folders.go` |
| Where the costs folders are, and the costs move | `internal/agent/costs/move.go` |
| Where the guard folder is, the guard move, the watcher's move | `internal/agent/guard/state.go`, `handover.go` |
| The swap with the file it leaves, and deleting that file after a restart | one function in `internal/operation`; the two system calls and the time of the last start in `internal/platform` |

Changed in place: every current document that names a moved folder
(`README.md`, `docs/usage.md`, `docs/acceptance.md`, the design and
contracts specs), `AGENTS.md` (the ownership table, and the migration rules:
a migration may swap a folder Workbench wrote inside its runtime folders
with a small file, which stays at the old path until the machine restarts,
and delete a socket; every costs and guard entry uses its folder where it is
and moves it, without asking, when it holds the folder's lock), and a new
ADR. Dated plans and specs of earlier work stay as written.

## 7. The one safeguard

The ledger is the only lasting record of spend. One test proves that a
ledger whose newest rows are only in its `-wal` file holds every row after
the move, that with an old and a new ledger both present neither changes,
and that after the move nothing can make a ledger at the old path. It says
that risk beside it.

## 8. Left to the builder

- Proving section 5 with the release before in a disposable home, before
  anything else is built on it. Its watcher tracks a running command and a
  detached program, an agent runs two commands at once, and its import
  worker has just started; the new build is installed over it. Then: the
  commands and the program are still tracked and can be stopped, every
  note has a watcher, one watcher runs, one ledger exists and a report
  reads it, and nothing is at the old paths but the files the moves left. The same
  with a watcher of the release before that is kept from handing over, and
  with the ledger open in another process while its folder moves. If the
  release before does otherwise, building stops there.
- The messages' exact wording.
