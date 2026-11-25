# Agent Folders, Costs Prices and Guard History Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** One pre-release in which the costs and guard files live under `agent/`, downloaded prices live in the ledger, and Agent Guard keeps and shows the commands that finished.

**Architecture:** Three designs built in one order because they share files. First the folder move: a swap primitive in `internal/platform` and `internal/operation`, one "where is the folder" function per part, and the rule that only the holder of a folder's own lock moves it, proven against the release before in a disposable home before anything is built on it. Then a shared SQLite open (`internal/sqlitefile`) that the ledger and the new guard history both call, the price tables in the ledger, and last the history store, its writer beside the watcher's tick, and the view.

**Tech Stack:** Go (the version in `go.mod`), one module, `golang.org/x/sys/unix` (`Renameat2`, `RenamexNp`), `modernc.org/sqlite`, Cobra, Bubble Tea v2 and lipgloss.

**Spec:** three designs, read all three before any task:
[agent folders](../specs/2026-11-19-agent-folders-design.md) ("folders N" below),
[costs prices](../specs/2026-11-19-costs-prices-design.md) ("prices N"),
[agent guard history](../specs/2026-11-19-agent-guard-history-design.md) ("history N").

## Global Constraints

- Gates after every code task, all passing: `golangci-lint fmt`, `golangci-lint config verify`, `golangci-lint run ./...`, `go build ./...`, `go test ./...`.
- Tests stay near zero (`AGENTS.md`). This plan adds exactly four, each for the risk named beside it: the folder move's ledger test (folders 7), the version 5 upgrade test (prices 6), the history files' mode test (history 9), and the two first-costs-move tests changed in place (prices 6). No other test is added.
- Only `internal/platform/` may test the operating system; a `forbidigo` rule enforces it.
- Greenfield: no compatibility readers, flags, aliases or dual paths, and no text about how things used to be. The **only** exceptions, all asked for by the owner and stated in the designs, are: migration `0007-agent-folders`; migration `0008-price-lists`; the rule that an entry uses its folder where it is until it has moved (folders 5.1 rule 2); the ledger upgrade from version 5 to 6 filling the price tables from the list files (prices rule 2); and the `AGENTS.md` sentences that record them (Task 4, Task 7, Task 12). Anything beyond these is refused in review.
- Never touch the developer's real home. Every proof sets `HOME` and `WORKBENCH_CONFIG_DIR`, `WORKBENCH_DATA_DIR`, `WORKBENCH_STATE_DIR`, `WORKBENCH_CACHE_DIR`, `WORKBENCH_BIN_DIR` to folders under one scratch folder outside the repository, with synthetic credentials. Never run `apply`, `update`, `migrate` or `answers import` outside such a home.
- A hook prints nothing on stdout unless it tells the agent a stop's reason, and never fails or delays a command.
- Command text may hold a token: it never reaches a file another user can read.
- Tracked docs describe the product impersonally. Conventional commit subjects; never edit `CHANGELOG.md`.
- Every implementer and reviewer subagent runs on Opus, starts fresh, and gets the exceptions above verbatim in its brief.
- These are not parallel lanes: the tasks share `state.go`, `watch.go`, `handover.go`, `move.go` and `ledger.go`. Run them in order; review after each chunk (after Tasks 5, 7, 12).
- Any screen a person sees that changed is shown to the owner as a real render before it reaches `main` (Task 13).

## Review Focus

Each line names the step that pins it.

1. An update lands while an agent runs two commands at once: every note must have a watcher afterwards (Task 5 step 3).
2. A hook or import worker of the release before wakes after the move (a laptop asleep across the update): it must fail at the closed path and make no folder and no ledger (Task 5 step 5).
3. The history file is damaged while the watcher runs: the guard keeps guarding, the file is moved aside and a new one started (Task 9 step 6).
4. A 100 KB command and a build with 5,000 processes: the list stays readable, the panel scrolls all of it, the watcher stays inside 32 MiB (Task 11 step 5, Task 13 step 2).
5. A version 5 ledger with one list file missing and one unreadable: the upgrade succeeds, keeps every row, and the missing list is downloaded (Task 7 step 2).

---

## Chunk A: the folder move

### Task 1: The swap and the closed path

**Files:**
- Create: `internal/platform/swap_linux.go`, `internal/platform/swap_darwin.go`, `internal/platform/boot_darwin.go`
- Modify: `internal/platform/process_linux.go` (export the boot time it already reads)
- Create: `internal/operation/movefolder.go`

**Interfaces:**
- Produces, in `internal/platform`:
  - `func SwapNames(a, b string) error`: `unix.Renameat2(AT_FDCWD, a, AT_FDCWD, b, RENAME_EXCHANGE)` on Linux, `unix.RenamexNp(a, b, RENAME_SWAP)` on macOS.
  - `var ErrSwapUnsupported error`: what `SwapNames` returns (wrapped) for `EINVAL`, `ENOTSUP`, `EOPNOTSUPP`, `ENOSYS`, and `EXDEV`, which an overlay returns for a folder of a lower layer.
  - `func BootID() (string, error)`: `/proc/sys/kernel/random/boot_id` on Linux, `kern.bootsessionuuid` on macOS.
  - `func CanSwap(path string) (bool, error)`: whether the file system of the folder at `path` swaps two names (folders rule 5): false for a folder whose `MarkCannotSwap` mark names the device and inode it has now, else the native file system check.
  - `func MarkCannotSwap(path string)`: marks the folder at `path`, by the extended attribute `user.workbench.cannot-swap` holding its device and inode numbers, read once the attribute is written, as one whose swap the file system refused (an overlay's lower layer, which no read-only call tells apart); best effort.
- Produces, in `internal/operation`:
  - `func MoveFolder(from, to string) (moved bool, err error)`: folders 5.1 rule 3. Makes `filepath.Dir(to)` private, makes a 0600 plain file at `to` holding `platform.BootID()` (replacing a plain file there; an existing folder at `to` returns `moved=false, err=nil`), swaps, and returns `moved=false` with `errors.Is(err, platform.ErrSwapUnsupported)` when the file system cannot swap, after removing the file it made and marking `from` with `platform.MarkCannotSwap`.
  - `func CanMoveFolder(from, to string) (can bool, err error)`: whether `MoveFolder` can move, from paths and the file system only: a folder at `from`, none at `to`, and a file system that can swap or cannot be asked; `err` matches `platform.ErrSwapUnsupported` when `platform.CanSwap(from)` says it cannot. A mover that holds a lock asks first, so a move that cannot happen never takes it.
  - `func IsFolder(path string) bool`: a folder, not a link and not a plain file.
  - `func RemoveClosedPath(path string)`: deletes a plain file at `path` that holds another start's identifier (folders rule 4); does nothing otherwise.

- [ ] **Step 1:** Write the two `SwapNames` files and `BootTime`.
- [ ] **Step 2:** Write `movefolder.go`.
- [ ] **Step 3:** Prove by hand in a scratch folder with a throwaway `main` (not committed): a folder holding a file locked with `flock` by another process, a bound Unix socket and an open WAL database is swapped; afterwards the old path is a plain file, the lock is still held at the new path, a client connects to the socket at the new path, and the database's holder commits and reads its rows. Paste the output in the task report.
- [ ] **Step 4:** Gates. Commit `feat(operation): swap a folder with a file that closes its old path`.

### Task 2: The guard folder

**Files:**
- Modify: `internal/agent/guard/state.go` (`openFolders`), `hooks.go`, `watch.go` (`Watch`), `handover.go`, `client.go`, `logs.go`
- Modify: `internal/migrate/0004_old_guard_log.go`

**Interfaces:**
- Consumes: `operation.MoveFolder`, `operation.IsFolder`, `operation.RemoveClosedPath`, `operation.TryLockFile`.
- Produces, in `internal/agent/guard`:
  - `func Root(state string) string`: folders rule 2 for the guard: `<state>/guard` while it is a folder and either `<state>/agent/guard` is not or `<state>/guard/watch.lock` is held; else `<state>/agent/guard`. The one place the old name appears outside the move.
  - `openFolders(state, part string) (folders, error)` uses `Root`, makes folders only under the new root, and calls `RemoveClosedPath` on the old path.
  - `func Move(state string) (moved bool, err error)`: folders rule 8, second bullet: takes the watcher's lock at the old path without waiting; when it gets it, `operation.MoveFolder`, release, then start `agent guard watch` when the folder holds a note. Used by the hooks.
  - `func MigrateMove(state string) (moved bool, err error)`: `Move` for migration 0007, which tries a held lock again for about 100 ms (`operation.TakeLockIn`) and returns an error matching `platform.ErrSwapUnsupported`, and nothing else, when the only failure is a file system that cannot swap.
  - `(w *watcher) moveFolder() (moved bool, err error)`: rule 8, first bullet: called in `Watch` after the lock is held (taken or handed) and before the first tick; on success re-points `w.f`; when it cannot move, the watcher works where it is.
- A watcher handed the watch opens nothing in the folder before it has read the word to start (today `Watch` calls `openFolders` first; move that after `takeLock`).
- Every guard entry retries once through `Root` when a read or write fails and the old path is no longer a folder (rule 2): one helper, `func (f *folders) again() bool`, used by the three hooks; `agent guard history`, which opens nothing it could hand to `again`, asks `func HistoryFolderMoved(root string) bool` and finds the folder again with `HistoryRoot` or `MakeHistoryRoot`, which retry their own checks the same way.

- [ ] **Step 1:** `Root`, `openFolders`, `again`; route `client.go`, `logs.go` and `0004` through `Root`.
- [ ] **Step 2:** `Move` and the hooks: each hook calls `Move` when the old folder exists, then proceeds; a pre hook that could not move because the lock is held writes its note where `Root` says.
- [ ] **Step 3:** `Watch`: lock first, then `moveFolder`, then folders and the socket path.
- [ ] **Step 4:** The watcher removes its socket file at the folder's current path itself (folders rule 8): a listener removes only the name it was bound with.
- [ ] **Step 5:** Gates. Commit `feat(guard): keep the guard's files in agent/guard and move them under the watcher's lock`.

### Task 3: The costs folders

**Files:**
- Modify: `internal/agent/costs/move.go` (`LocationsIn`, `gate`, `CheckFolders`, `MoveFolders`), `ledger.go`, `ingest.go` (`startWorker`, `Ingest`), `report.go`, `rates.go`, `internal/machine/effects.go`

**Interfaces:**
- Consumes: Task 1.
- Produces, in `internal/agent/costs`:
  - `LocationsIn(home string, runtime operation.Paths) Paths` answers folders rule 2 for costs: the ledger in `<data>/costs` and the state in `<state>/costs` while those are folders and either the new ones are not or the import lock there is held; else under `agent/costs`. Data and state resolve separately (a stop between the two moves).
  - `func MoveAgentFolders(home string, runtime operation.Paths) (moved bool, err error)`: folders rule 11: import lock at the old path without waiting; data folder, then state folder; release (rule 7). Called first by every costs entry before it makes a folder, takes a lock or opens a file, by `gate` before the first costs move, and by `0001`'s check and run (rule 12).
  - `func MigrateMoveAgentFolders(home string, runtime operation.Paths) (moved bool, err error)`: `MoveAgentFolders` for migration 0007, which tries a held lock again for about 100 ms (`operation.TakeLockIn`).
- No costs entry makes `<state>/agent/costs` or `<data>/agent/costs` while an old folder exists (today `startWorker`, `Ingest` and `Status` make the state folder before the gate: reorder).

- [ ] **Step 1:** `LocationsIn` and `MoveAgentFolders`; call it at the top of `startWorker`, `Ingest`, `Status`, `Paths.Open`, `Rates`.
- [ ] **Step 2:** `gate`, `CheckFolders`, `MoveFolders`: the agent-folders move first; `0001`'s text names `agent/costs/`.
- [ ] **Step 3:** Write the safeguard `TestAgentFoldersMoveKeepsTheLedger` in `internal/agent/costs/move_test.go`, with the risk beside it (the ledger is the only lasting record of spend): a ledger whose newest rows are only in its `-wal` holds every row after `MoveAgentFolders`; with an old and a new ledger both present neither file changes; after the move `Paths.open` through the old path fails and makes nothing.
- [ ] **Step 4:** Gates. Commit `feat(costs): keep the costs files in agent/costs and move them under the import lock`.

### Task 4: Migration 0007 and the documents

**Files:**
- Create: `internal/migrate/0007_agent_folders.go`; modify `internal/migrate/migrate.go` (`Registry`, the package comment)
- Modify: `README.md`, `docs/usage.md`, `docs/acceptance.md`, `docs/superpowers/specs/workbench-design.md`, `workbench-contracts.md`, `AGENTS.md`
- Create: `docs/adr/0019-agent-folders.md`

- [ ] **Step 1:** `0007-agent-folders`: `Check` is pending while an old path holds a folder, done otherwise (a file system that cannot swap counts as done, with that reason in the text, folders rule 5); `Run` calls `costs.MigrateMoveAgentFolders` and `guard.MigrateMove`, deletes what folders rule 6 lists from an old folder left beside a new one, and returns what it left.
- [ ] **Step 2:** `doctor` names an old folder that stays (rules 5 and 6).
- [ ] **Step 3:** Documents and `AGENTS.md` as folders 6 says; ADR 19 with the three approaches of folders 4.2.
- [ ] **Step 4:** Gates. Commit `feat(migrate): move the costs and guard folders under agent/`.

### Task 5: The proof against the release before

Nothing after this task is built until every step passes. If the release before does otherwise than a step expects, stop and report.

**Files:** none tracked; the harness lives in the scratch folder.

- [ ] **Step 1: The home.** A scratch home as Global Constraints say. Install the published `v0.1.32-rc.9` into it with the published installer, `workbench apply` by `--dry-run --json` and `--approve-plan`, with synthetic answers. Build the branch as a local bundle (`scripts/package-release.py`, named as `AGENTS.md` says for a local bundle).
- [ ] **Step 2: The fake agent.** As in the guard detached work plan: a Python script that is not a shell, pipes each hook's JSON to the installed `workbench agent guard pre` and `post`, and starts the command itself. It runs one long command (`sleep 600` holding 200 MB), one program that detaches from a second command, and keeps an import worker of rc.9 starting every second.
- [ ] **Step 3: The update.** Activate the local bundle through `workbench update` in that home, so the installed command resolves to another file and the rc.9 watcher hands over by itself. While it does, the fake agent starts two commands at once. Pass, all of: the guard log shows a handover that succeeded (not a fresh start); `<state>/guard` is a plain file and `<state>/agent/guard` holds the lock, socket, notes; `workbench agent guard --json` (or the view under a pty) lists the long command, the detached program and both new commands; exactly one `agent guard watch` process runs; a stop of the long command by hand ends it.
- [ ] **Step 4: Costs.** Pass, all of: one `ledger.sqlite` exists, under `<data>/agent/costs`; `workbench agent costs` reads it with the rows rc.9 wrote; `<data>/costs` and `<state>/costs` are plain files; the import log shows no worker ended in an error other than the closed path.
- [ ] **Step 5: The late process.** Run the rc.9 binary's `agent guard pre` and `agent costs ingest --worker` directly after the move. Pass: both end without making a folder or a ledger at an old path, and the pre hook does not fail its command.
- [ ] **Step 6: No handover.** Repeat steps 1 to 3 with the rc.9 watcher kept from handing over (start it from a copy of the binary that is not the installed command). Pass: it keeps guarding in `<state>/guard`, new hooks' notes are read by it, and after it exits the next pre hook moves the folder and starts a watcher.
- [ ] **Step 7:** Write the outcome of each step, with the commands and their output, into the task report. **Chunk review.**

---

## Chunk B: prices in the ledger

### Task 6: One way to open a private SQLite file

**Files:**
- Create: `internal/sqlitefile/sqlitefile.go`
- Modify: `internal/agent/costs/ledger.go` (`OpenLedger`, `enableWAL`, `isBusy`)

**Interfaces:**
- Produces: `package sqlitefile`
  - `type Options struct { Create bool; ReadOnly bool; Pragmas []string; TxLock string; Prepare func(*sql.DB) error }`: `Prepare` checks the file, and makes its schema, before the switch to WAL, which only a file it accepted gets.
  - `func Open(path string, o Options) (*sql.DB, error)`: with `Create`, makes the file empty and 0600 before SQLite opens it when it is missing (`O_CREATE` without `O_EXCL`); opens through the `sqlite` driver with each of `Pragmas` as `_pragma=` and `_txlock=TxLock`; switches to WAL, retrying up to 50 times while the error is busy. `ReadOnly` opens `mode=rw` with `query_only(1)` and never creates (history rule 1).
  - `func IsBusy(err error) bool`
  - The one import of `modernc.org/sqlite` outside the ledger's error handling.
- `OpenLedger` keeps its checks of an existing file, `prepare` and `makePrivate`, and calls `sqlitefile.Open` with `busy_timeout(5000)`, `synchronous(NORMAL)`, `TxLock: "immediate"`.

- [ ] **Step 1:** Write the package; move the three steps out of `ledger.go` unchanged in behavior.
- [ ] **Step 2:** `go test ./internal/agent/costs/...` passes unchanged. Gates. Commit `refactor(costs): open the ledger through a shared private SQLite open`.

### Task 7: Prices

**Files:**
- Modify: `internal/agent/costs/ledger.go` (schema, `schemaVersion = "6"`, the upgrade), `rates.go`, `report.go`, `focus.go`, `move.go` and its tests, `internal/cli/agentcosts.go`
- Create: `internal/migrate/0008_price_lists.go`; modify `migrate.go` (`Registry`, the script variables)
- Modify: `docs/usage.md`, `docs/acceptance.md`, `AGENTS.md`

**Interfaces:**
- Consumes: Task 3 (`LocationsIn`), Task 6.
- Produces: tables `prices` and `price_lists` exactly as prices rule 1; `(l *Ledger) officialRates(ctx, tool) (officialFile, error)` and `(l *Ledger) replaceOfficial(ctx, tool, officialFile) error` in place of `readOfficial` and `writeOfficial`; `loadCard(ctx, home string, ledger *Ledger) (Card, error)` (no overrides layer; `Card.Overrides` and `HasOverrides` go).

- [ ] **Step 1:** Schema 6 and the upgrade from 5: in the upgrade's transaction, read each `<tool>/rates-official.json` beside the ledger into the two tables; a file that is missing or unreadable fills nothing (prices rule 2).
- [ ] **Step 2:** Safeguard `TestUpgradeToSixKeepsRowsAndPrices` in `ledger_test.go`, risk beside it: a version 5 ledger with rows, one valid list file, one missing and one unreadable; after opening, every row is there, the valid file's prices are in `prices`, and the open succeeded.
- [ ] **Step 3:** Refresh (download with no transaction open, then one short transaction, prices rule 3), `loadCard`, the reports, `agent costs rates` through the ledger (rule 5), `status` without its overrides line and field (rule 7).
- [ ] **Step 4:** `0001`: no overrides, no `CLAUDE_COSTS_RATES`, still moves the list files; its safeguard proves that a file Workbench did not write in each old folder stays, byte for byte, and is named.
- [ ] **Step 5:** `0008-price-lists` (rule 6): opens the ledger first, then deletes the list files and their emptied folders.
- [ ] **Step 6:** Proof in a scratch home holding an rc.9 ledger with rows and both list files: `workbench agent costs` before and after the new build prints the same totals; after `migrate`, the costs data folder holds only the ledger. Documents. Gates. Commit `feat(costs): keep downloaded prices in the ledger and drop the overrides file`. **Chunk review.**

---

## Chunk C: the guard history

### Task 8: The store

**Files:**
- Create: `internal/agent/guard/history.go`

**Interfaces:**
- Consumes: Task 6.
- Produces (all in `package guard`):
  - `type Finished struct { CommandView; Graph []uint64; Processes []ProcessPeak; Cut int64; Unlisted int }`, `type ProcessPeak struct { PID int; Name string; Peak uint64 }`
  - `type Summary struct { Seq int64; CommandView /* Command cut to 300 characters, no Tree, no History */ }`
  - `func OpenHistory(root string, write bool) (*History, error)`; schema and pragmas exactly as history 6.1 and 6.2.
  - `(h *History) Insert(ctx, Finished) error` (rule 4), `Trim(ctx, now time.Time) (deleted int, again bool, err error)` (rule 5: one step of at most 200 commands, 2,000 process rows and 512 KiB of command text), `Keep(ctx) (time.Duration, error)`, `SetKeep(ctx, time.Duration) error`
  - `(h *History) After(ctx, seq int64) ([]Summary, error)`, `Lowest(ctx) (int64, error)`, `Details(ctx, seq int64) (Finished, error)`, `Find(ctx, text string) ([]int64, error)`, `Count(ctx) (rows int, bytes int64, err error)`
  - `(h *History) Check() error`: the writer's look, once a minute, that the path names the file it opened and that its header is a database's (rule 9), noting the `-wal` and `-shm` beside it; `Close() error`: a writer whose path names no file, or another one, first checkpoints the WAL into the file it opened and then deletes that file's `-wal` and `-shm` still at the path.
  - `func MoveDamaged(root string, since time.Time) (to string, replaced time.Time, err error)` (rule 9: keeps at most two copies, deletes one older than `since` first, and returns when the copy it deleted to make room had been set aside) and `var ErrHistoryUnusable error`
  - Limits as constants: `historyKeepDefault = 24 * time.Hour`, `historyRows = 20000`, `historyBytes = 256 << 20`, `commandKept = 64 << 10`, `processesKept = 10000`, `graphBars = 120`.

- [ ] **Step 1:** Write the store.
- [ ] **Step 2:** Safeguard `TestHistoryFilesArePrivate` in `history_test.go`, risk beside it (command text may hold a token): after the first insert, `history.db`, `-wal` and `-shm` have no group or other permission.
- [ ] **Step 3:** With a throwaway `main`, repeat the design's largest measurement (20,000 commands of 4,000 characters) against this store and paste the figures; they must be within a factor of two of history 6.4. Gates. Commit `feat(guard): a history store for finished commands`.

### Task 9: The watcher

**Files:**
- Modify: `internal/agent/guard/watch.go` (`usage`, `measure`, `tick`, `Watch`), `view.go` (`finish`, `buildView`), `protocol.go`, `handover.go` (`carried`), `detached.go`, `manual.go`, `socket.go`, `client.go`
- Create: `internal/agent/guard/historywriter.go`

**Interfaces:**
- Consumes: Task 8; Task 2 (`moveFolder` runs before the writer opens the file, history rule 11).
- Produces: `protocolVersion = 2`; `Snapshot.Pending []Finished`, `Snapshot.HistoryError string`; `usage{ Buckets []uint64; Per int; Filled int; Last uint64; Peak uint64; Since time.Time; Processes map[procKey]*ProcessPeak; Unlisted int; Outside map[procKey]bool }` (history rules 6 and 7); `carried.Pending []Finished` in place of `Finished`; `type historyWriter` with `start(root string)`, `submit(Finished)`, `done() []string`, `failure() string`, `drain(time.Duration) (left int)`, and `lasts(name string, level slog.Level, event, msg string, args ...any)` and `ended(name string) bool` for the record of a watcher's problem that lasts, which the writer logs again once a minute: before a cut of the log by the time kept would delete it, and after a cut to the log's newest half deleted it.

- [ ] **Step 1:** `usage`: the buckets that merge by their larger value and double `Per` at 120, the latest reading, each process's peak up to 10,000.
- [ ] **Step 2:** `finish` builds a `Finished` and puts it on the pending list (at most 2 MiB, oldest dropped and counted in the log); `w.finished` and `finishedShown` go.
- [ ] **Step 3:** `historyWriter`: one goroutine, the only connection that writes; inserts, reports IDs, trims on start and each minute in steps, retries every ten seconds after a failure and logs it once (history rules 1 to 5).
- [ ] **Step 4:** Snapshot and handover (history 7.1, 7.2, rule 12); `platform.ReleaseServiceTables` stays, its comment says "a process that looks up no service or protocol by name".
- [ ] **Step 5:** Proof with the fake agent in a scratch home: ten commands finish; each appears in `history.db` within two seconds with its graph and processes; kill the watcher with `SIGKILL` mid-run and start it again: no row twice, none lost that had been written.
- [ ] **Step 6:** Proof of damage: overwrite `history.db` with noise while the watcher runs. Pass: the next finished command stays in the snapshot, the file is renamed to `history.damaged.db`, a new one is started, the guard log names both, and a memory stop still works throughout.
- [ ] **Step 7:** Gates. Commit `feat(guard): keep finished commands, their processes and their memory graph`.

### Task 10: `agent guard history`

**Files:**
- Modify: `internal/cli/agentguard.go`

- [ ] **Step 1:** `workbench agent guard history` prints the two lines of history 3.4; `--keep DURATION` takes `12h`, `3d`, `1w` from one hour to 30 days, sets the value and prints the one line; an unusable file fails with the view's text and sets nothing; `--json` gives the same facts as fields.
- [ ] **Step 2:** Gates. Commit `feat(guard): show and set how long finished commands are kept`.

### Task 11: The view

**Files:**
- Modify: `internal/cli/agentguardview.go`, `agentguardstyle.go`
- Read, do not merge: branch `spike/guard-mock` (`internal/cli/agentguardmock.go` and its patches to the view), which is the approved look. Rewrite its drawing against real data; nothing named `mock` reaches this branch.

**Interfaces:**
- Consumes: Tasks 8 to 10.

- [ ] **Step 1: The list** (history 3.1): finished rows from `After` and `Lowest` each second, the `ENDED` column with a date when more than 24 hours are kept, `s`, `pgup`, `pgdn`, `home`, `end`, the row number, the history without a watcher.
- [ ] **Step 2: Find** (history 3.2): the input line, `Find` as a Bubble Tea command, only the latest answer shown.
- [ ] **Step 3: The details and the panel** (history 3.3): the order of facts, the one-line graph colored by `sizeStyle`, the command cut at 3 lines when longer than 4, processes cut at 10 when more than 11, the chosen `… more` row, the half-width panel, the panel alone under 100 columns, the cut and unlisted notes.
- [ ] **Step 4: The warnings**: cannot be written, damaged and started again, unreadable.
- [ ] **Step 5:** Renders under a pty with `pyte`, each saved as text in the scratch folder: best case, worst case (a 100 KB command, a 5,000-process command, 20,000 finished rows), no watcher, the filter open and kept, a dated `ENDED`, each warning, at 150x42, 120x40 and 90x30. Compare the best and worst cases line by line with the same captures of the mock; list every difference in the task report.
- [ ] **Step 6:** Gates. Commit `feat(guard): show the history, find in it, and open long parts in a side panel`.

### Task 12: Documents

**Files:**
- Modify: `docs/usage.md`, `docs/acceptance.md`, `docs/superpowers/specs/2026-10-17-platform-and-guard-view-design.md`, `AGENTS.md`
- Create: `docs/adr/0020-guard-history.md`

- [ ] **Step 1:** As history 8 says, with ADR 20 for the store and its limits.
- [ ] **Step 2:** Gates. Commit `docs(guard): describe the history and its limits`. **Chunk review.**

---

## Chunk D: the release

### Task 13: The proof on the whole build, and the screens

- [ ] **Step 1:** Run Task 5, steps 1 to 6, again with the whole branch as the new build. All three pieces fire in the seconds after the update: the changed handover, the folder swap and the ledger upgrade. The same passes, and also: the history file is made under `agent/guard` only after the move, the costs totals are unchanged across the update, and the view reaches the watcher.
- [ ] **Step 2:** Measure the watcher under the two loads of ADR 16 (a build starting about 650 processes a second; 1,400 held processes) with the history written. Pass: within its 32 MiB limit. If not, stop and report (history 11).
- [ ] **Step 3:** Show the owner the renders of Task 11 step 5 and the output of `agent guard history`. Change what they ask; one approval is enough.

### Task 14: Release

- [ ] **Step 1:** Review the branch's commits. Push `feat/guard-history`, wait for `all-checks`, fast-forward `main`.
- [ ] **Step 2:** Tag `v0.1.32-rc.10`; wait for the release workflow (rerun a stalled macOS job with `gh run rerun --failed`; never re-tag).
- [ ] **Step 3:** The owner decides first whether to keep a copy of the ledger folder: this pre-release is one-way, since the release before reads neither a version 6 ledger nor the moved folders. Then `workbench update v0.1.32-rc.10`, `workbench apply --dry-run --json`, `workbench apply --approve-plan DIGEST`, `workbench doctor`.
- [ ] **Step 4:** Check on the machine: `agent guard` shows the history, `agent costs` totals match the totals noted before the update, the three old paths hold the files the moves left.
- [ ] **Step 5:** Remove the worktrees and branches of this work, including `.worktrees/guard-mock` and `spike/guard-mock`, the scratch folders, and any container, image or volume made for it.
