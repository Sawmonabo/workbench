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

`<config>`, `<data>` and `<state>` are Workbench's runtime folders
(`internal/platform` `RuntimeFolders`).

| From | To | Holds |
| --- | --- | --- |
| `<data>/costs/` | `<data>/agent/costs/` | `ledger.sqlite` with its `-wal` and `-shm` |
| `<state>/costs/` | `<state>/agent/costs/` | `ingest.log`, `ingest.err`, `ingest.lock` |
| `<state>/guard/` | `<state>/agent/guard/` | `watch.log`, `watch.err`, `watch.lock`, `guard.sock`, `notes/`, `agents/`, `killed/` |

Two things stop being files:

- **The price overrides file** (`<config>/costs/rates.json`) is no longer
  read. Only a person writing it by hand ever made one; prices come from the
  list Workbench ships, the tools' own cost records and the official price
  pages. Its reader, its layer in the price list and the hint that named it
  go. A file a machine still has stays where it is, untouched, and
  `0001-costs-folders` no longer moves one.
- **The downloaded price lists** (`<data>/costs/<tool>/rates-official.json`)
  become two tables in the ledger (rule 9). The costs move deletes the
  files and their folders, which are caches: the import worker downloads
  the lists again at its next run.

Nothing else moves: `machine.toml`, the env files, `state.json`,
`activation.json`, `migrations.json`, `locks/`, `checkpoints/`, `chezmoi/`,
`releases/`, `tools/`, `application-contexts/` and the cache belong to no
`agent` command.

## 3. Goals, non-goals, anti-goals

Goals:

- Every path the code, the plan, `doctor` and the documents name is the new
  one; the code names an old folder only in the migration.
- A machine with the old folders is moved once, without a question, with
  every byte kept.

Non-goals:

- No change to a file's name or content.
- No move of any folder section 2 does not list.

Anti-goals:

- A new, empty ledger made beside the old one.
- A ledger moved apart from its `-wal` file, which can hold commits the main
  file lacks.
- Two watchers acting at once, or a running command that stops being
  watched.
- A reader for the old layout that outlives the move.

## 4. Approaches

### 4.1 How a folder moves

| Approach | Trade-off |
| --- | --- |
| **One folder rename per row of section 2.** Chosen. | A rename inside one folder tree is one step for the system: the ledger and its `-wal` move together, files a running process holds stay open, a lock stays the same lock at its new path, and nothing is copied however large the ledger is. |
| The copy the first costs move makes (a vacuumed copy, checked, then the original set aside) | Built for an old ledger on another disk. Here it copies a ledger of any size for nothing. |
| Moving file by file | A stop between two files leaves a ledger without its `-wal`, or half a guard folder with a watcher running in it. |

### 4.2 What becomes of processes of the release before

After an update, processes of the release before may still run for a while:
a watcher, and import workers started just before. They name the old
folders.

| Approach | Trade-off |
| --- | --- |
| **Move, then close the old path for ten minutes; the watcher is started anew.** Chosen. | A plain empty file is left at each old path, so a process of the release before fails there and makes nothing. The watcher of the release before exits by its own check within a second of the rename, and the move starts the new one, which reads the notes and tracks the running commands again. Their memory graphs start over, once. |
| Hand the watch over across the move | Keeps the graphs, but the new watcher and the new hooks would have to work in the old folder until the handover ends: a second layout in the running code. |
| Move only when nothing of the release before runs | The new release would have to use the old folders until then, which is the second layout again. |

## 5. Rules

1. **Migration `0007-agent-folders`** holds the moves: the costs move (the
   costs data and state folders) and the guard move. A move is pending while an old
   folder exists and done when none does. An empty plain file at an old path
   (rule 4) counts as no folder.
2. **Nothing makes a new folder before its move.** Every costs entry runs
   the costs gate, and every guard entry the guard gate, before it makes a
   folder, takes a lock or opens a file: the import worker and the hook
   that starts it, `status`, the reports and `rates`; the guard's hooks,
   watcher, view and `logs`. A gate first looks at the old path and returns
   at once when nothing is there.
3. **A move** takes the migrate lock, then the lock of the part it moves at
   its old path: the import lock (`ingest.lock`) for the costs move, so no
   import is running, and nothing for the guard move, whose watcher is
   meant to be running. It makes the private `agent/` folder, checks that
   the new folder does not exist, and renames. It never replaces a folder.
   An import lock that is held leaves the costs move for the next run: the
   worker ends as it does when another import is running.
4. **The old path is closed.** After a rename the move leaves an empty
   plain file, mode 0600, at the old path of each state and data folder. A
   process of the release before that then names the old folder fails,
   where it would have made a new, empty one: an import worker cannot open
   a ledger there, and a watcher cannot take a lock there. Any gate or run
   that finds such a file older than ten minutes deletes it.
5. **Who runs a move.** `workbench migrate` and the apply step run both.
   The import worker runs the costs move itself, and every other costs
   command is told to run `workbench migrate`, as for the first costs move.
   Every guard entry runs the guard move itself: the pre hook runs before
   every agent command and cannot wait for a person.
6. **The watcher across the guard move.** A watcher of the release before
   checks each second that its lock file is still at its path, and exits
   when it is not, so it exits within a second of the rename. Whoever ran
   the guard move then starts `agent guard watch` when the lock was held,
   and a watcher that finds the lock held waits for it up to five seconds
   before it gives up. The new watcher reads the notes, which moved with
   the folder, and tracks the running commands again; their graphs and
   peaks start from that moment. A watcher started by a handover while the
   old folder still exists exits at once and changes nothing, so the
   watcher of the release before keeps guarding until a move runs.
7. **Old and new both exist.** The new folder is the one in use, and
   nothing is merged into it. From the old one the move deletes what
   Workbench may delete: logs, `.err` output files, price caches, sockets,
   locks nobody holds and empty folders, then the folder when it is empty.
   Anything else (a ledger, a note, a lock that is held)
   stays, and `workbench migrate` and `doctor` name it.
8. **Earlier migrations.** `0001-costs-folders` ends in the new folders,
   since it moves into wherever the costs files live, and its text names
   `agent/costs/`. `0004-old-guard-log` names the log at its new path.
9. **Prices in the ledger.** Ledger schema version 6 adds two tables:

   ```sql
   CREATE TABLE prices(
     tool           TEXT NOT NULL,   -- claude, codex
     prefix         TEXT NOT NULL,   -- a model, or a family such as claude-opus
     input          REAL NOT NULL,   -- USD per million tokens, as are the rest
     output         REAL NOT NULL,
     cache_write_5m REAL NOT NULL,
     cache_write_1h REAL NOT NULL,
     cache_read     REAL NOT NULL,
     PRIMARY KEY(tool, prefix)) WITHOUT ROWID;

   CREATE TABLE price_lists(
     tool          TEXT PRIMARY KEY,
     url           TEXT NOT NULL,
     fetched_at    REAL NOT NULL,    -- when the list was last downloaded
     checked_at    REAL NOT NULL,    -- when the page was last asked
     etag          TEXT NOT NULL,
     last_modified TEXT NOT NULL) WITHOUT ROWID;
   ```

   The ledger upgrades a version 5 file in place, in one transaction that
   keeps every row, as it does for earlier versions. A refresh replaces one
   tool's rows in one transaction. Which price a model gets is still decided
   in Go (`RateCard.Lookup`): a family row prices its versions, the most
   specific row wins and a service tier must match, which a join on equal
   names cannot say. A report sums tokens per model in SQL and prices each
   sum from the list, as it does today; the list's official rows now come
   from `prices`.

## 6. Ownership and documents

| Concern | Owner |
| --- | --- |
| The list, the check and the run record | `internal/migrate/0007_agent_folders.go` |
| The costs move and gate | `internal/agent/costs/move.go` |
| The price tables, their refresh and the schema upgrade | `internal/agent/costs/rates.go`, `ledger.go` |
| The guard move and gate, and starting the watcher after it | `internal/agent/guard/state.go` |
| The rename itself (private parent, never replace, the file left at the old path and its removal) | one function in `internal/operation`, called by both |

Changed in place: every current document that names a moved folder
(`README.md`, `docs/usage.md`, `docs/acceptance.md`, the design and
contracts specs), `AGENTS.md` (the ownership table, and the migration rules:
a migration may rename a folder Workbench wrote inside its runtime folders
and leave an empty file at the old path for ten minutes, and the guard's
entries run the guard's move themselves, as the import worker runs the
costs move), and a new ADR. Dated plans and specs of earlier work stay as
written.

## 7. The one safeguard

The ledger is the only lasting record of spend. One test proves that a
ledger whose newest rows are only in its `-wal` file holds every row after
the move, that with an old and a new ledger both present neither changes,
and that after the move nothing can make a ledger at the old path. It says
that risk beside it. The ledger's existing upgrade safeguard covers
version 6.

## 8. Left to the builder

- Proving rules 4 and 6 with the release before in a disposable home: its
  watcher tracking a running command and its import worker started just
  before, an update to the new build, then the command tracked and
  stoppable again within five seconds, one watcher, one ledger, and
  nothing at the old paths but the empty files. If the release before does
  otherwise, building stops there.
- The messages' exact wording.
