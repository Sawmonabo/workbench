# 20. The guard keeps finished commands in a bounded SQLite history

- Status: Accepted, November 22, 2026
- Amends: [AGENTS.md](../../AGENTS.md), "Reject unsupported state without
  deleting or converting it"

## Context

Agents run long chains of commands while nobody watches. A person who comes
back needs to see what ran, how much memory each command took and which
processes it had, with the whole command and every process. The guard log
records each command's end on one line, its command cut at 120 characters,
without its processes or its memory over the run.

The watcher that would keep these runs with a 32 MiB memory limit and looks
at memory once a second, and a stop must never wait on the disk, which is
slow just when memory is short. A command's text may hold a token the agent
typed. The guard must keep guarding when what it keeps is damaged or full.

## Options considered

- **A. A SQLite file beside the guard log, written by one goroutine in the
  watcher.** Chosen. The list reads only the rows it shows, the details load
  one command's graph and processes when they open, a search runs in SQLite
  over the whole text, and old rows are deleted in place. The pure-Go driver
  is the one the costs ledger links, so every release bundle still builds
  without a C toolchain.
- **B. JSON Lines, as the guard log is written.** The view would parse the
  whole file to draw the list and hold every command's processes in memory,
  and each trim rewrites the file.
- **C. One file per command, as the notes are.** Thousands of files to list,
  open and delete each minute.
- **D. SQLite written by the watcher's tick itself.** Simpler, but a commit
  can wait on the disk: one command with 10,000 processes took 23 ms.

For the time kept, a default changed only by `workbench agent guard history
--keep` and saved as a row in the history file was chosen over a machine
answer, which every machine would have to give at a terminal before its next
unattended apply, and over an environment variable, which the watcher would
take from whichever agent started it, so two shells could disagree.

## Decision

- The history is `<state>/agent/guard/history.db`, with SQLite's `-wal` and
  `-shm` files beside it, made 0600 before SQLite opens it, in WAL mode so
  the view reads while the watcher writes. A history readable by others
  (a copy, or a mode another tool set) is made private, with its `-wal` and
  `-shm` files, each time it is opened and found to be a history, since
  SQLite gives the side files the history's own mode.
- One goroutine in the watcher holds the only connection that writes
  commands. The tick hands it each finished command and goes on. Until a
  command is written, the snapshots carry it whole, so the view shows it the
  second it ends, and a handover passes it to the next watcher.
- Finished commands are kept 24 hours by default: a person who comes back
  to an agent's unattended run sees what it ran, and at the 1.1 KB to
  5.3 KB a command took when measured, a day of commands stays far inside
  the bounds below. `--keep` sets 1 hour to 30 days. Nothing asks for the
  time.
- Fixed bounds keep the watcher's memory and the disk in check whatever the
  time kept:

  | Bound | Value | What it protects |
  | --- | --- | --- |
  | Commands | 20,000 | The view's list and the watcher's 32 MiB: listing 20,000 took 35 ms, and a process set up as the watcher counted 9.4 MiB after writing them |
  | File | 256 MiB of pages in use | The disk: 20,000 commands of 4,000 characters took 107 MB |
  | Command text | 64 KiB, cut there, with the bytes left out counted | The disk and the list, whatever one command holds |
  | Processes | 10,000 per command, the largest kept, the rest counted | The watcher's memory while a command runs; one insert of 10,000 took 23 ms |
  | Memory graph | 120 bars over the whole run, neighbors merged by their larger value | The watcher's memory, whatever a run's length; the peak is always in the graph |
  | Not yet written | 2 MiB, the oldest dropped past that | The watcher's memory while writes fail, and each snapshot, well under the 16 MiB a client reads in one line |

  The oldest commands leave first: those older than the time kept, then the
  oldest past the count or the size. A trim runs when the writer starts and
  once a minute, at most 200 commands, 2,000 process rows and 512 KiB of
  command text at a time, with an incremental vacuum after each, so deleted
  rows give their pages back to the disk, and a pause as long as the step
  before the next, so the other writer (the watcher's, or `--keep`) gets
  the file within its busy timeout.
- A history the guard cannot use (not a database, damaged, or of another
  layout) is never repaired, merged or deleted in place. The watcher alone
  renames it and its `-wal` and `-shm` files to `history.damaged.db`, after
  renaming a copy already there to `history.damaged.2.db`, as logrotate
  numbers the generations it keeps, and starts an empty history with the
  time kept it last read. Each copy is deleted once it is older than the
  time kept, when all it held would have gone anyway: it may hold the only
  record of rows the time kept still covers, which SQLite's recovery can
  often rebuild. Two copies bound the disk. Where logrotate's count deletes
  the oldest whatever its age, a third history the guard cannot use while
  both copies are younger than the time kept takes the place of
  `history.damaged.db` and `history.damaged.2.db` stays: the oldest copy
  most likely holds the rows from before the first damage, while the newer
  one holds a history started after a damage, at most the commands since.
  The history always starts again, so the guard never stops keeping
  finished commands for as long as a copy lasts. SQLite reads the file only where
  its cache lacks a page, so once a minute the watcher also checks that the
  path still names the file it opened and that its 100-byte header is a
  database's, which takes microseconds where SQLite's own quick check took
  54 ms on a 100 MB file. Before it lets go of a file the path no longer
  names, the writer checkpoints the WAL into it, since SQLite does not for
  a moved database: a history moved elsewhere keeps every row. A history
  that cannot be written for another reason, such as a full disk, stays
  where it is and the write is tried again every ten seconds.

## Consequences

- A second SQLite file joins the costs ledger. `internal/sqlitefile` owns
  what both share when they open: the private create, the connection string,
  the switch to WAL and taking group and other access off a file its own
  checks accepted, with its `-wal` and `-shm` files. Each keeps its own
  checks and schema.
- AGENTS.md records the one exception to rejecting unsupported state
  without deleting it: each damaged copy goes once it is older than the time
  kept, and at a third damage within that time the newer of the two copies
  goes for the new one.
- One test proves that the history and its two side files are readable by
  their owner alone, made new or opened readable by others, since a
  command's text may hold a token.
- The history keeps nothing older than the time kept, has no export and no
  `--json` of its rows; `workbench agent guard logs --json` stays the
  machine-readable record.
- With no watcher, the view and `agent guard history` read the history but
  never move a damaged one; they say it cannot be read. `--keep` trims the
  history itself, whether a watcher runs or not, so the older commands go at
  once.
- The guard log records the start of each command, so it keeps a record no
  longer than the history's time kept, whatever its size under the 1 MiB
  cap: the watcher's writer deletes the older records once a minute, once it
  has read the time kept, and `--keep` as it trims, both in place through
  `operation.RewriteLog`, so a follower keeps its place.

## Sources

- [Agent Guard history design](../superpowers/specs/2026-11-19-agent-guard-history-design.md),
  section 6.4 for the measurements
- [logrotate(8)](https://man7.org/linux/man-pages/man8/logrotate.8.html),
  `rotate count` and `maxage`, for the generations kept
- [SQLite recovery](https://sqlite.org/recovery.html), for what a damaged
  copy can still give back
- [Costs ledger design](../superpowers/specs/2026-09-30-claude-costs-ledger-design.md),
  for the pure-Go SQLite driver
