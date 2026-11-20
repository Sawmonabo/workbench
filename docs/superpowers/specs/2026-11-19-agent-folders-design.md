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
| `<data>/costs/` | `<data>/agent/costs/` | `ledger.sqlite` with its `-wal` and `-shm`, `claude/rates-official.json`, `codex/rates-official.json` |
| `<state>/costs/` | `<state>/agent/costs/` | `ingest.log`, `ingest.err`, `ingest.lock` |
| `<config>/costs/` | `<config>/agent/costs/` | `rates.json`, a person's price overrides |
| `<state>/guard/` | `<state>/agent/guard/` | `watch.log`, `watch.err`, `watch.lock`, `guard.sock`, `notes/`, `agents/`, `killed/` |

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

| Approach | Trade-off |
| --- | --- |
| **One folder rename per row of section 2.** Chosen. | A rename inside one folder tree is one step for the system: the ledger and its `-wal` move together, files a running process holds stay open, a lock stays the same lock at its new path, and nothing is copied however large the ledger is. |
| The copy the first costs move makes (a vacuumed copy, checked, then the original set aside) | Built for an old ledger on another disk. Here it copies a ledger of any size for nothing and needs every importer stopped. |
| Moving file by file | A stop between two files leaves a ledger without its `-wal`, or half a guard folder with a watcher running in it. |

## 5. Rules

1. **Migration `0007-agent-folders`** holds the four moves. A move is
   pending when its old folder exists and its new one does not, and done
   when the old one does not exist.
2. **Both exist.** An old folder that holds nothing but logs, locks, the
   `.err` output files and empty folders is deleted. Any other is a
   conflict: both stay as they are and `workbench migrate` names them.
3. **A move** takes the migrate lock, makes the private `agent/` folder,
   checks again that the new folder does not exist, and renames. It never
   replaces a folder.
4. **Who runs it.** `workbench migrate` and the apply step run all four.
   Two more run their own, without asking, because they cannot wait for a
   person:
   - The costs gate (`internal/agent/costs` `gate`): the import worker
     moves the three costs folders before it opens the ledger; every other
     costs command is told to run `workbench migrate`, as for the first
     costs move.
   - A guard gate in `openFolders`: every guard entry (the hooks, the
     watcher, the view, `logs`) moves the guard folder before it uses it.
     The pre hook runs before every agent command; were it to make a new,
     empty folder, it would start a second watcher beside the one running
     in the old.
5. **The migrate lock is held.** The import worker fails as it does today
   (`another migration is running`). A guard hook waits for it up to one
   second, then lets the command run without a note, as it does for any
   failure of its own.
6. **Processes already running.** The rename leaves their open files open:
   an importer of the release before keeps its lock and its ledger; the
   watcher keeps its lock, which a hook then finds held at the new path,
   and its socket, which answers at the new path. A watcher of the release
   before reads its notes by the old path and cannot until it hands over to
   the new release, which an update makes it do within seconds. It must
   keep every command it tracks through that time.
7. **The first costs move** (`0001-costs-folders`) ends in the new folders,
   since it moves into wherever the costs files live; its text names
   `agent/costs/`.

## 6. Ownership and documents

| Concern | Owner |
| --- | --- |
| The list, the check and the run record | `internal/migrate/0007_agent_folders.go` |
| The costs folders' move and the gate | `internal/agent/costs/move.go` |
| The guard folder's move and the gate | `internal/agent/guard/state.go` |
| The rename itself (lock, private parent, never replace) | one function in `internal/operation`, called by both |

Changed in place: every current document that names a moved folder
(`README.md`, `docs/usage.md`, `docs/acceptance.md`, the design and
contracts specs), `AGENTS.md` (the ownership table, and the migration rules:
a migration may rename a folder Workbench wrote inside its runtime folders,
and the guard's hooks and watcher run the guard's move themselves, as the
import worker runs the costs moves), and a new ADR. Dated plans and specs
of earlier work stay as written.

## 7. The one safeguard

The ledger is the only lasting record of spend. One test proves that a
ledger whose newest rows are only in its `-wal` file holds every row after
the move, and that with an old and a new ledger both present neither
changes. It says that risk beside it.

## 8. Left to the builder

- Proving rule 6 with a real update in a disposable home: a watcher of the
  release before, tracking a running command, an update to the new build,
  and the command still tracked and stoppable afterwards. If the old
  watcher drops it, building stops there and the order of the guard's move
  is decided again.
- The messages' exact wording.
- Whether the three costs folders move under one lock hold or three.
