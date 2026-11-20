# Agent Guard history design

Agent Guard keeps the commands that finished, with their processes and their
memory over the whole run, and its details show a long command or a long
process list in a side panel on demand.

## 1. Intent

Agents run long orchestrations of commands while nobody watches. Agent Guard
shows a command only while it runs and for a minute after, so a person who
comes back cannot see what ran, how much memory it took or which processes it
had. The details also list the command first, so one long command pushes
everything else off the screen.

A person must be able to look back at what ran and read every part of it:
the whole command and every process.

## 2. Goals, non-goals, anti-goals

Goals:

- Finished commands stay in the list for a time a person sets, 24 hours when
  nothing is set.
- A finished command keeps every process it had and a graph of its memory
  over its whole run.
- The details open as one column whose rows do not move while a command
  runs; a long command or process list is cut to a few lines and opens whole
  in a side panel.
- A command is found fast among thousands: by time, by size or by text.

Non-goals:

- No mouse. A plain drag keeps selecting text (ADR 14).
- No history older than the time kept, no export of it and no `--json` for
  it; `workbench agent guard logs --json` stays the machine-readable record.
- No memory reading per process over time; a process keeps its peak.
- No change to what the guard stops, or when.

Anti-goals:

- A history write that delays a tick or a stop.
- A watcher whose memory grows with the history.
- A guard that stops guarding because its history is damaged or full.
- Command text, which may hold a token, readable by another user.

## 3. What a person sees

### 3.1 The list

```
[WorkBench] Agent Guard  ● watching · 5:08:44 PM

5 running · 2,832 finished · mem 15.71/16 GB · peak 9.57 GB · swap 7.12/8 GB

   ID        STATUS       AGENT      TIME   MEMORY     PEAK     ENDED  PROJECT           COMMAND
▸  01DPWAKE  ● running    claude     2m1s   763 MB  1008 MB            workbench         python3 - <<'EOF' …
   01e6UGZc  ● running    codex     18m1s  5.71 GB  6.78 GB            payments-service  cargo build --work…
   01rsUM8K  ✗ stopped    codex   3h12m0s        —  9.57 GB   5:07 PM  payments-service  cargo test --works…
   01hxiqgs  ✓ done       claude     1m5s        —  1.17 GB   5:06 PM  workbench         go test -race -cou…
↑/↓ move  space tag  enter/→ details  / find  s sort  pgup/pgdn page  k stop  q/esc quit  1/2,837
```

1. The running commands come first, as today. The finished ones follow, dim,
   for as long as they are kept (3.4).
2. The count line counts both, with a thousands separator. Its `peak` stays
   the largest since the watcher started, and with no watcher the line holds
   the finished count alone.
3. `ENDED` is a new column, right-aligned, empty for a running row. It holds
   the time a command ended (`5:07 PM`). When more than 24 hours are kept, a
   row that ended before today also holds its date (`Nov 18 5:07 PM`).
4. Columns drop in a narrow window in today's order (SUBAGENT, PROJECT, PEAK,
   AGENT). `ENDED` never drops. With times only, PROJECT shows from 128
   columns; with dates, from 135.
5. `s` sorts the finished rows by largest peak, and again by newest. Running
   rows stay on top in their own order.
6. `pgup`, `pgdn`, `home` and `end` move the selection by a page or to an
   end. The key line ends with the selected row's number and the row count.
7. With no watcher running the list still shows the finished commands, under
   `○ not running`.
8. A detached program is drawn under its command while the list is sorted by
   newest and has no filter, and both rows are in it. Otherwise it is a row
   of its own.

### 3.2 Finding a command

`/` opens a one-line input on the bottom line. The list shows only the rows,
running and finished, whose agent, subagent, project, ID or command holds the
text, upper and lower case alike.

| While the input is open | |
| --- | --- |
| any character, backspace | edits the text; `k`, `q`, `s` and space are text |
| `↑` `↓` `pgup` `pgdn` | move the selection |
| `enter` | closes the input and keeps the filter |
| `esc` | closes the input and clears the filter |
| `ctrl+c` | quits |

The search runs beside the screen: typing never waits for it, the list keeps
its rows until the answer comes, and only the answer to the latest text is
shown.

While a filter is kept, the count line says so
(`5 running · 12 of 2,832 finished · find "cargo"`), `/` edits it, and `esc`
clears it; a second `esc` quits, as `esc` does today.

### 3.3 The details

```
  Folder     ~/dev/payments-service
  Agent      codex
  Subagent   —
  ID         toolu_01rsUM8KUG5P0T13AaET3YgW
  Status     ✗ stopped by guard · 3h12m0s
  Started    2:10 PM
  Ended      5:22 PM
  Memory     peak 9.57 GB
  Graph      ▁▂▃▄▅▅▆▇▇██▇▇▇▇▆▆▇▇███████▇▇▆▆█▆▆▇▇▇▆▆▅▅▄▄▃
  Command    cargo test --workspace -- --test-threads=8
  Processes  143 in all
             PID      NAME                         PEAK
             37054    rustc                     3.19 GB
             …nine more rows…
           ▸ … 133 more processes  · enter shows all
```

1. The order is Folder, Agent, Subagent, ID, In the log and Started by (a
   detached program), Status with the run time, Note, Started, Ended (a
   finished command), Memory, Graph, Command, Processes. The rows above
   Command keep their height while a command runs.
2. **Graph** is one line that covers the whole run, at most 120 bars, never
   wider than the window allows. Each bar is colored by its own size as the
   list colors a size: plain under a gigabyte, gold from there, red from
   four (`sizeStyle`). It has no caption and never opens in the panel.
3. **Command** shows whole when it takes at most 4 lines at the window's
   width. A longer one shows its first 3 lines and a row `… N more lines`.
4. **Processes** shows a count line (`27 running`, or `143 in all` for a
   finished command), the column heads and the rows. Up to 11 rows show
   whole; more show the first 10 and a row `… N more processes`. A running
   command lists its processes with their memory now, in the watcher's
   order; a finished one lists every process it had with its peak, the
   largest first. A command with none says `none`.
5. `↑` and `↓` move between the `… more` rows, the chosen one marked `▸` in
   the accent color with `· enter shows all`. With no such row they scroll
   the details, as today. `pgup` and `pgdn` always scroll.
6. `enter` or `→` on a `… more` row opens the **side panel**: the right half
   of the window shows that part whole under its title (`Command`, or
   `Processes · 143 in all, largest first`), the left half keeps the
   details, wrapped to their half, with the chosen row saying
   `· shown on the right`. In the panel `↑`, `↓`, `pgup`, `pgdn`, `home` and
   `end` scroll, and a last line says `lines 30 to 58 of 144` when not all
   fits. `esc` or `←` closes the panel; `k` still stops.
7. A window under 100 columns has no room for two halves: the panel takes
   the whole width until it is closed.
8. The details never open with the panel showing.

### 3.4 How long finished commands are kept

Finished commands are kept 24 hours. Nothing asks for that time, at setup or
later. One command shows and changes it:

```
$ workbench agent guard history
Finished commands are kept 24 hours · 2,832 kept · 3.2 MB
~/.local/state/workbench/agent/guard/history.db

$ workbench agent guard history --keep 12h
Finished commands are kept 12 hours (was 24 hours); older ones are deleted within a minute.
```

`--keep` takes hours, days or weeks (`12h`, `3d`, `1w`) from one hour to
30 days.

The oldest finished commands leave first, on one limit a person sets and two
fixed ones that bound the list and the disk:

- **Time.** A command that ended longer ago than the time kept is deleted.
- **Count.** At most 20,000 finished commands are kept.
- **Size.** The history takes at most 256 MiB on disk.

Past the count or the size, the oldest are deleted even when they are
younger than the time kept.

A shorter time deletes the older commands without asking: the view hides
them at once and the watcher deletes them for good within a minute. A longer
time brings nothing back.

## 4. Constraints

- The watcher's own memory limit is 32 MiB (`debug.SetMemoryLimit`) and a
  tick is one second.
- The hooks run before and after every agent command and must stay as they
  are: they write and read the notes, not the history.
- Greenfield: no reader for an older layout, no flag, no second path
  (`AGENTS.md`).
- Platform tests stay in `internal/platform/` (ADR 12).
- Tests stay near zero (`AGENTS.md`); section 9 names the one this adds.

## 5. Approaches

### 5.1 Where the history lives

| Approach | Trade-off |
| --- | --- |
| **A SQLite file beside the guard log.** Chosen. | Lists the rows without reading the processes and graphs, loads one command's details when they open, and deletes old rows in place. The pure-Go driver is the one the costs ledger already links. |
| JSON Lines, as the guard log is written | The view would parse the whole file to draw the list and hold every command's processes in memory; trimming rewrites the file. |
| One file per command, as the notes are | Thousands of files to list, open and delete each minute. |

### 5.2 Where the time kept is set

The watcher and a view started from another shell must read the same value,
and a person who never sets it must never be asked.

| Approach | Trade-off |
| --- | --- |
| **A default, changed by `agent guard history --keep`, saved as a row in the history file.** Chosen. | Nothing to answer, one command for the one setting, and one value for every process that opens the file. The value goes with the file: a history started again after damage keeps it only when the watcher had read it. |
| A machine answer, asked by apply | Every machine must answer a new question at a terminal before an unattended apply runs again, for a setting most never change. |
| A saved flag on `agent guard` itself | That command opens the view; a flag that sets a value and deletes history instead reads as something else than it is. |
| An environment variable, as `WORKBENCH_GUARD` is | The watcher is one shared process that takes the environment of whichever agent started it, so two shells can disagree and nothing shows which value holds. |

### 5.3 How the details show a long part

| Approach | Trade-off |
| --- | --- |
| **One column; a long part cut to a `… more` row that opens a half-width panel.** Chosen. | The details read at a glance, nothing is hidden for good, and the rows above the command never move. |
| The panel always open beside the facts | Half the window is spent on a part nobody asked for, and a short command leaves it empty. |
| One column, every part whole | One long command or a hundred processes push the rest off the screen, which is the fault this design removes. |

### 5.4 When the history is written

| Approach | Trade-off |
| --- | --- |
| **One writer beside the tick, in the watcher.** Chosen. | The tick hands a finished command over and goes on. A slow disk, which is likely just when memory is short and the guard matters most, never delays a stop. |
| Written by the tick itself | Simpler, and usually a tenth of a millisecond, but a commit can wait on the disk, a command with 10,000 processes took 23 ms and a large trim took longer. |
| Written when the watcher exits | Nothing to see while the watcher runs, and everything lost if it is killed. |

## 6. The store

### 6.1 File

`<state>/agent/guard/history.db`, with SQLite's `history.db-wal` and
`history.db-shm` beside it, opened through `modernc.org/sqlite` as the costs
ledger is.

- Created empty with mode 0600 before SQLite opens it, in the private
  `agent/guard/` folder (see the agent folders design), so the two files SQLite makes beside it take that mode.
- `journal_mode=WAL`, so the view reads while the watcher writes.
- `auto_vacuum=INCREMENTAL`, set before any table, and an
  `incremental_vacuum` after each trim, so deleted rows give their pages
  back to the disk.
- `wal_autocheckpoint(100)` and `journal_size_limit(1048576)` hold the WAL
  file to 1 MiB; `cache_size(-64)` holds the watcher's page cache to 64 KiB.
- `busy_timeout(100)`, `synchronous(NORMAL)`, `foreign_keys(1)`.

### 6.2 Schema

```sql
PRAGMA user_version = 1;

CREATE TABLE commands(
  seq         INTEGER PRIMARY KEY AUTOINCREMENT,  -- never given twice
  id          TEXT    NOT NULL UNIQUE,  -- the call's ID, or a detached program's
  kind        TEXT    NOT NULL,         -- '' or 'detached'
  launched_by TEXT    NOT NULL,         -- the call that started a detached program
  agent       TEXT    NOT NULL,
  subagent    TEXT    NOT NULL,
  project     TEXT    NOT NULL,
  cwd         TEXT    NOT NULL,
  command     TEXT    NOT NULL,
  cut         INTEGER NOT NULL,         -- bytes of the command left out
  state       TEXT    NOT NULL,         -- done, stopped or unmatched
  stopped_by  TEXT    NOT NULL,         -- '', guard or you
  dry_run     INTEGER NOT NULL,
  started     INTEGER NOT NULL,         -- Unix milliseconds
  ended       INTEGER NOT NULL,         -- Unix milliseconds
  peak        INTEGER NOT NULL,         -- bytes
  processes   INTEGER NOT NULL,         -- how many it had
  unlisted    INTEGER NOT NULL);        -- how many of them were not kept
CREATE INDEX commands_ended ON commands(ended);

CREATE TABLE graphs(
  seq  INTEGER PRIMARY KEY REFERENCES commands(seq) ON DELETE CASCADE,
  bars BLOB NOT NULL);                  -- one uvarint of KiB per bar, oldest first

CREATE TABLE processes(
  seq  INTEGER NOT NULL REFERENCES commands(seq) ON DELETE CASCADE,
  n    INTEGER NOT NULL,                -- 0 is the largest peak
  pid  INTEGER NOT NULL,
  name TEXT    NOT NULL,
  peak INTEGER NOT NULL,                -- bytes
  PRIMARY KEY(seq, n)) WITHOUT ROWID;

CREATE TABLE settings(
  key   TEXT PRIMARY KEY,
  value TEXT NOT NULL) WITHOUT ROWID;   -- keep: the seconds finished commands stay
```

The list reads `commands` alone. A command's graph and processes are read by
`seq` when its details open. A graph is one value, drawn as a whole and never
queried by bar, so it is a blob; a process is a row because the details list
and order them.

### 6.3 Rules

1. **One writer.** In the watcher, one goroutine owns the only connection
   that inserts and deletes commands; the tick never touches the file.
   `agent guard history --keep` writes the one `settings` row from its own
   process. The view never writes: it opens the file with `mode=rw` (a WAL reader
   must be able to write the `-shm` file) and `query_only(1)`, and never
   creates it.
2. **The tick never waits.** `finish` puts the command, with its graph and
   processes, on a pending list and hands it to the writer. The writer
   writes each command in one transaction and reports its ID; the next tick
   takes it off the list. Each snapshot carries the pending commands whole,
   so the view shows a finished command, details included, in the second it
   ends, written or not.
3. **A write that fails keeps the command in sight.** The pending list holds
   at most 2 MiB of command text, graphs and processes and drops its oldest
   past that, counting them in the log. The writer tries again every ten
   seconds and logs the failure once until a write succeeds. The snapshot
   names the failure and the view says
   `Guard history cannot be written: REASON`.
4. **Twice is harmless.** Rows go in with
   `INSERT … ON CONFLICT(id) DO NOTHING`. A command that is already there
   gets no graph or process rows and counts as written.
5. **Trim.** When it starts and then once a minute, the writer reads `keep`
   (24 hours when the row is missing) and deletes, in steps of at most 200 commands with an
   `incremental_vacuum` after each and other writes between them: the
   commands that ended before `now - keep`; then those with the lowest `seq`
   beyond 20,000; then those with the lowest `seq` while the pages in use
   (`page_count - freelist_count`) take more than 256 MiB. The view leaves
   out rows older than `keep` too, so a history no watcher trims shows no
   more than is kept.
6. **Every process.** A tracked command keeps each process it has had, by
   PID and start, with its name and its peak resident size: at most 10,000,
   counting the rest in `unlisted`. The details then say
   `10,000 of 12,400 kept`.
7. **The whole run in at most 120 bars.** A tracked command keeps at most
   120 buckets, each the largest of `per` readings. When all 120 are full,
   neighbors merge by their larger value and `per` doubles. The peak is
   always in the graph. A running and a finished command draw the same
   series.
8. **A long command.** Command text past 64 KiB is cut there, `cut` holding
   the bytes left out; the panel's last line says
   `… 12 KB more were not kept`.
9. **A history that cannot be used is moved aside.** A file that is not a
   database, that SQLite calls corrupt, whose `user_version` is not 1 or
   whose tables are not these is never repaired, merged or deleted in
   place. The writer renames it and its `-wal` and `-shm` files to
   `history.damaged.db` (and `-wal`, `-shm`), replacing a damaged copy
   already there, starts an empty history with the time kept it last read (24
   hours when it never read one, which it logs), and logs both names once. While
   the copy exists the view says
   `Guard history was damaged and started again at TIME; the old file is
   PATH`. A trim deletes the copy once it is older than `keep`, when all it
   holds would have gone anyway. Only the writer moves a file: a view with
   no watcher says `Guard history unreadable: REASON` and shows none, and
   `agent guard history` fails with the same text and sets nothing.
   A file that cannot be written for another reason (a full disk, a
   permission) is left where it is and falls under rule 3.
10. **No file yet** is an empty history. The view tries again each second;
    `agent guard history --keep` creates the file as the watcher would.
11. **Handover and exit.** A watcher hands over only when nothing is pending
    or the last write failed; what is pending then crosses to the next
    watcher, whose writer takes it. A watcher that exits while commands are
    pending gives the writer one second, then logs how many were not
    written.

### 6.4 Measured

With the schema above, the ledger's driver and 120-bar graphs, on an Apple
M-series laptop. The process counts are an assumed mix (seven in ten commands
with 1 to 3 processes, a quarter with 4 to 20, the rest up to 150; 7.8 on
average), not a measured one.

| Finished commands | File | Insert one | List all | One command's processes |
| --- | --- | --- | --- | --- |
| 593, 200-character commands | 0.7 MB | 0.13 ms | 1.1 ms | 0.03 ms |
| 2,832, 200-character commands | 3.2 MB | 0.11 ms | 4.6 ms | 0.04 ms |
| 2,832, 4,000-character commands | 15.1 MB | 0.14 ms | 12.9 ms | 0.16 ms |
| 2,832, each with 143 processes | 13.8 MB | 0.76 ms | 4.8 ms | 0.12 ms |
| 20,000, 200-character commands | 22.8 MB | 0.11 ms | 34.9 ms | 0.10 ms |

The slowest single insert was 8 ms. Deleting half the rows and vacuuming took
6 ms to 253 ms and halved each file. A command costs about 1.1 KB at 200
characters and 5.3 KB at 4,000, so the length of the commands, not the number
of processes, sets the size.

The largest case, 20,000 commands of 4,000 characters with 1 to 14 processes
each, in a process set up as the watcher is (32 MiB limit, 50 GC percent,
`cache_size(-64)`):

| | |
| --- | --- |
| File | 107 MB |
| Opening the file and making the schema | 0.2 to 0.7 MiB more counted against the limit, 2.4 MiB more resident |
| After writing all 20,000 | 9.4 MiB counted, 15.7 MiB resident, the program's own text included (5.5 and 7.1 before the file was opened) |
| Inserting one command with 10,000 processes | 23 ms |
| A trim step of 200 commands | 8 ms to delete, 5 ms to vacuum |
| A filter over every command's whole text | 72 to 226 ms |

## 7. The watcher, the socket and the view

### 7.1 Watcher (`internal/agent/guard`)

- `usage` keeps the buckets and `per` of rule 7 in place of the latest 120
  sizes, the latest reading, which a stop by hand records when the tree can
  no longer be read, and the processes of rule 6.
- `finish` puts the command on the pending list of rule 2. The list of
  commands finished within a minute (`w.finished`, `finishedShown`) goes,
  with its pruning in `buildView`.
- The watcher still calls `platform.ReleaseServiceTables`. That call's rule
  becomes "a process that looks up no service or protocol by name": the
  SQLite driver's library never does (its source for macOS and Linux names
  neither `getservby…` nor `getprotoby…`), and the store opened and wrote
  with the tables released.
- A command whose call started a detached program is no longer held in the
  snapshot for that program's sake: the view finds it in the history.
- What a watcher hands the next one (`carried`) holds the pending list in
  place of `Finished` (rule 11). `usage` crosses as it is kept.

### 7.2 Socket

`protocolVersion` becomes 2. A snapshot carries the tracked commands, each
with the whole-run graph in `History`, the pending finished commands whole
(at most 2 MiB, well under the 16 MiB a client reads in one line) and the
store's failure, when it has one. A view and a watcher of two releases say so as they do today
(`watcherOther`).

### 7.3 View (`internal/cli/agentguardview.go`)

- Rows are the snapshot's commands and the history's, one row per ID, the
  snapshot's winning.
- The view holds one summary per finished command: the `commands` columns
  with the first 300 characters of the command. Each second, and at once
  after a snapshot, it asks for the rows with a `seq` above the last it
  has and for the lowest `seq` in the file; it drops the rows below that
  one and the rows older than `keep`.
- The filter of 3.2 runs in SQLite over the whole command text, so the view
  never holds it, and as a Bubble Tea command, so the screen never waits.
- Opening the details of a finished command loads its graph and processes
  by `seq`; opening the panel on its command loads the command whole.
- Without a watcher the view reads the history each second as above.

## 8. Ownership and documents

| Concern | Owner |
| --- | --- |
| The history file: schema, open, writes, trim, moving a damaged one aside, reads, the `keep` setting | `internal/agent/guard/history.go` |
| `agent guard history` and its `--keep` flag | `internal/cli/agentguard.go` |
| The steps both SQLite files share when they open: the driver, the private create before SQLite opens the file, the connection string and the switch to WAL with its retry when two creators meet | a new `internal/sqlitefile`, which `OpenLedger` calls too; the ledger keeps its own checks of an existing file, its schema and `makePrivate` |
| The list, the filter, the details and the panel | `internal/cli/agentguardview.go` |

Documents changed in place: the guard sections of `docs/usage.md` and
`docs/acceptance.md`, the guard view rules in
`docs/superpowers/specs/2026-10-17-platform-and-guard-view-design.md`, the
guard row of the ownership table in `AGENTS.md`, one sentence in its
development contract (the guard moves aside a history it cannot use and
starts a new one, deleting the copy once it is older than the time kept),
and a new ADR 19 for the store and its limits.

## 9. The one safeguard

A command's text may hold a token. One test proves that `history.db`,
`history.db-wal` and `history.db-shm` are readable by their owner alone after
the watcher's first write, and says that risk beside it.

## 10. To render before it reaches `main`

Four things have no approved render yet: the list with the input of 3.2
open and with a filter kept, an `ENDED` column with dates, the history
warnings (cannot be written, damaged and started again, unreadable), and the
output of `agent guard history`, alone and with `--keep`.

## 11. Left to the builder

- The exact wording of each message, within the texts given here.
- Whether the filter is worth a full-text index; without one it answers in
  72 to 226 ms on the largest case, which 3.2 allows.
- How the tracked processes are held. The watcher must stay inside its
  32 MiB limit, unchanged, under the two loads ADR 16 measured (a build
  starting about 650 processes a second, and 1,400 held processes), with
  the history written; the builder measures both. If it does not hold,
  building stops there: choosing between fewer processes kept for a command
  and a higher limit is not the builder's call.
- The split of the work into commits and lanes.
