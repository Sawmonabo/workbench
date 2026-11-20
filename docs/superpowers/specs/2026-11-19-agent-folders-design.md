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

Nothing else moves: `machine.toml`, the env files, `state.json`,
`activation.json`, `migrations.json`, `locks/`, `checkpoints/`, `chezmoi/`,
`releases/`, `tools/`, `application-contexts/` and the cache belong to no
`agent` command.

## 3. Goals, non-goals, anti-goals

Goals:

- Every path the code, the plan, `doctor` and the documents name is the new
  one.
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
- Two watchers acting at once, a moment with none while commands run, or a
  running command or detached program that stops being watched.
- A folder renamed under a process that still names it by its old path.

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
exits when it is not, and import workers started just before.

| Approach | Trade-off |
| --- | --- |
| **Only the holder of the folder's own lock moves it.** Chosen. | The watcher's lock guards the guard folder and the import lock the costs folders. Whoever holds the lock is the only process working in the folder, so nothing is renamed under a process that names the old path. The watcher of the release before hands the watch over as it does at every update, and the new watcher moves the folder before its first tick, so nothing it knows is lost. |
| Move at once and start the watcher anew | The watcher of the release before exits when its folder goes. What only it knew is lost: the detached programs it follows, the calls it passed over, a stop under way. |
| Move only when nothing runs | The new release would refuse to guard until then. |

## 5. Rules

### 5.1 Both moves

1. **Migration `0008-agent-folders`** lists the guard move and the costs
   move. A move is pending while an old folder exists and done when none
   does. A closed path (rule 3) counts as no folder.
2. **A rename** makes the private `agent/` folder, checks that the new
   folder does not exist, and renames. It never replaces a folder.
3. **The old path is closed in the same step.** The rename swaps the old
   folder with an empty plain file, mode 0600, made at the new path, in one
   system call (`renameat2` with `RENAME_EXCHANGE` on Linux, `renamex_np`
   with `RENAME_SWAP` on macOS): the folder is then at the new path and the
   file at the old. A process of the release before that names the old
   folder fails there, where it would otherwise make a new, empty one: an
   import worker cannot make a ledger, and a watcher cannot take a lock.
   Where the file system cannot swap, the move renames and then makes the
   file. Any entry that finds such a file older than ten minutes deletes
   it.
4. **Old and new both exist.** The new folder is the one in use, and
   nothing is merged into it. From the old one a move deletes what
   Workbench may delete: logs, `.err` output files, sockets, locks nobody
   holds and empty folders, then the folder when it is empty. Anything else
   (a ledger, a note, a lock that is held) stays, and `workbench migrate`
   and `doctor` name it.

### 5.2 The guard folder

5. **Where the guard folder is.** One function answers for every guard
   entry (the hooks, the watcher, the view, `logs`, migration 0004): the
   new folder once it exists, else the old one while it exists, else the
   new one. An entry never makes a folder or a file's parent at the old
   path. When the old folder goes away under an entry, the entry asks
   again, once.
6. **Only the holder of the watcher's lock renames the guard folder.**
   - A watcher started by a handover holds the lock it was passed. It opens
     nothing in the folder until the watcher before it has sent what it
     carries and stopped. Then, before its first tick, it renames the
     folder and works at the new path. Its lock and its socket are the
     same files there.
   - A hook, or `workbench migrate`, that finds the old folder takes the
     watcher's lock at the old path without waiting. When it gets it, no
     watcher runs: it renames, lets the lock go and carries on in the new
     folder, where a hook then starts a watcher as it always does.
   - When the lock is held, a watcher of the release before is running. The
     hook writes its note in the old folder, where that watcher reads it,
     and the command is guarded as before. `workbench migrate` says the
     watcher moves the folder when it next hands over or stops.
7. **A handover that does not happen.** A watcher of the release before
   that cannot hand over keeps guarding in the old folder, as it does
   today, until it has been idle for ten minutes and exits. The next hook
   then finds the lock free and moves the folder.
8. **The socket's path** is six bytes longer. On macOS a user name longer
   than 26 characters puts it past the 100 bytes a socket path may have,
   where 32 did before: the guard still guards, and `agent guard` says it
   cannot reach the watcher.

### 5.3 The costs folders

9. **Only the holder of the import lock renames the costs folders.** Every
   costs entry (the hook that starts the worker, the worker, `status`, the
   reports, `rates`) first looks at the old data and state paths and
   returns at once when nothing is there. Otherwise, before it makes a
   folder, takes another lock or opens a file, it takes the import lock at
   its old path (`<state>/costs/ingest.lock`) without waiting.
   - When it gets it, no import runs: it renames the data folder, then the
     state folder, and keeps the lock, which is the import lock at the new
     path, for as long as it needs one.
   - When the lock is held, an import of the release before is running. A
     hook starts no worker and a worker ends, as when another import runs;
     any other command says so and that it can be run again in a moment.

   A stop between the two renames leaves the state folder pending, and the
   next entry finishes it under the same lock.
10. **Before the first costs move.** `0001-costs-folders` and the worker's
    gate run the costs move of this design first, so the first costs move
    finds any ledger already at its new path and never takes another for
    the one in use. `0001`'s text names `agent/costs/`.

## 6. Ownership and documents

| Concern | Owner |
| --- | --- |
| The list, the check and the run record | `internal/migrate/0008_agent_folders.go` |
| The costs move, and running it before anything else in costs | `internal/agent/costs/move.go` |
| Where the guard folder is, the guard move, the watcher's move after a handover | `internal/agent/guard/state.go`, `handover.go` |
| The rename with its closed old path, and deleting a closed path | one function in `internal/operation`; the two system calls in `internal/platform` |

Changed in place: every current document that names a moved folder
(`README.md`, `docs/usage.md`, `docs/acceptance.md`, the design and
contracts specs), `AGENTS.md` (the ownership table, and the migration rules:
a migration may rename a folder Workbench wrote inside its runtime folders,
leave an empty file at the old path for ten minutes and delete a socket
nobody serves; every costs and guard entry runs its own move, without
asking, as the import worker runs the first costs move; guard entries use
the guard folder where it is until it has moved), and a new ADR. Dated
plans and specs of earlier work stay as written.

## 7. The one safeguard

The ledger is the only lasting record of spend. One test proves that a
ledger whose newest rows are only in its `-wal` file holds every row after
the move, that with an old and a new ledger both present neither changes,
and that after the move nothing can make a ledger at the old path. It says
that risk beside it.

## 8. Left to the builder

- Proving section 5 with the release before in a disposable home, before
  anything else is built on it. Its watcher tracks a running command and a
  detached program, and its import worker has just started; the new build
  is installed over it. Then: the command and the program are still
  tracked and can be stopped, one watcher runs, one ledger exists, and
  nothing is at the old paths but the empty files. The same with a watcher
  of the release before that is kept from handing over. If the release
  before does otherwise, building stops there.
- The messages' exact wording.
