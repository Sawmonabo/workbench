# 21. The costs ledger stores each name once and each time as a number

- Status: Accepted, November 25, 2026
- Amends: [AGENTS.md](../../AGENTS.md), the costs ledger exception

## Context

The costs ledger keeps one row per AI response, because the reports need
each response: a response is read again from a resumed or forked transcript
and must count once (dedupe by its ID), its account, plan, price tier and
thread root are corrected after it is stored, and `--since`/`--until` cut
at any millisecond. No transcript text is stored.

A copy of a real ledger, 1,587,060 responses, measured 935 MiB, about 618
bytes each, and a ledger grows by every response an agent makes. Each row
repeated long text:
the project path, the session ID twice (as the session and as its root),
the account, the plan, two evidence words, the model, a 24-character time
and the response ID (28 characters for Claude Code, 61 or 73 for Codex). Six indexes copied several of those strings again; two
of them (project and model, account) served no query, and a third (tool)
made the report read the table through the index, row by row, which on the
synthetic rows below was slower than reading it straight through.

Workbench's SQLite driver is SQLite translated to Go: it cannot load a C or
Rust extension (`load_extension` answers `not authorized`), so page or row
compression extensions are out of reach.

## Options considered

Today's layout and A were measured on the copy of the real ledger, A
after its upgrade and compaction; B to D on 400,000 synthetic rows shaped
like that ledger, scaled to 1.59 million, after `VACUUM`, with 4 KiB pages
(on those rows today's layout measured 873 MiB and A 175 MiB):

| Option | At 1.59 M rows | Trade-off |
| --- | --- | --- |
| Today's layout | 935 MiB | |
| **A. Each name once in a `names` table, numbers in the rows, time as milliseconds, response IDs as bytes, only the indexes a query uses (one partial).** Chosen. | 196 MiB | The report reads faster: 948 ms against 1.49 s for the 1,275,840 Claude Code rows of the copy in Go with Workbench's driver, and `agent costs --tool claude --all` 1.77 s against 2.21 s. The IDs are not readable with the `sqlite3` tool. |
| B. As A, IDs kept as text | about 42 MiB more than A (synthetic) | Readable IDs. |
| C. As A, `WITHOUT ROWID` keyed by the ID | 242 MiB (synthetic, text IDs) | Every other index carries the whole ID. |
| D. Rows packed per session and day into one compressed blob | 74 MiB (synthetic) | Every correction by ID unpacks and repacks a blob, and dedupe needs a separate ID set. |
| E. Page or row compression (ZIPVFS, `sqlite_zstd_vfs`, `sqlite-zstd`) | | Not loadable by the driver. |
| F. Rollups per day, session and model | | Loses each response: dedupe, corrections and millisecond windows stop working. |

Packing the two evidence codes into one integer saved 1.2 bytes a row and
times relative to the session start 2.4 bytes, which made a time index
impossible; neither is worth it. SQLite already stores small integers in
one byte and 0 or 1 in none.

Also weighed: larger pages (SQLite's notes: for a filled database 4 KiB
is as good or better on space), `STRICT` tables (the same file format, so
the same size), `auto_vacuum` (pointer-map pages and no gain for a file
that only grows) and `VACUUM` alone (the file had no free pages).

Sources: [WITHOUT ROWID tables](https://www.sqlite.org/withoutrowid.html),
[the database file format](https://sqlite.org/fileformat.html) and
[the check-in that stores 0 and 1 in no bytes](https://www.sqlite.org/src/info/ae301db8a61a74bc),
[page sizes](https://sqlite.org/pgszchng2016.html),
[STRICT tables](https://sqlite.org/stricttables.html),
[ZIPVFS](https://www.sqlite.org/zipvfs/doc/trunk/www/howitworks.wiki),
[sqlite_zstd_vfs](https://sqlite.org/forum/info/43d054311d31f99c?t=c),
[sqlite-zstd](https://docs.rs/crate/sqlite-zstd/0.3.5/source/python/README.md),
[modernc.org/sqlite](https://pkg.go.dev/modernc.org/sqlite),
[UUIDs as BLOB or TEXT](https://vespa-mrs.github.io/vespa.io/development/project_dev/database/DatabaseUuidEfficiency.html),
[TimescaleDB's columnar compression](https://www.tigerdata.com/blog/building-columnar-compression-in-a-row-oriented-database).

## Decision

- Ledger schema version 7 stores each model, project, session, account,
  plan and tool once, in `names`, and each response's references to them as
  numbers; the evidence of a field as its rank (transcript 4 down to
  unknown 0); the time as Unix milliseconds, NULL for none; the response ID
  as bytes that decode to exactly its stored text, falling back to the text
  itself for any shape the encoding does not know.
- The responses table keeps its integer row key and three indexes: the
  response ID (unique), the thread root `(tool, root)`, and `(tool, ts)` for
  the rows whose account or plan is still open to correction.
- A version 6 ledger, and through it every earlier version, is upgraded in
  place, in one transaction that keeps every row and checks every converted
  row before it commits. A value the conversion does not know (an evidence
  word, a time not in the stored layout) refuses the upgrade and changes
  nothing. The upgrade then compacts the file once with `VACUUM`, retried by
  the next import while it has not succeeded.
- The check of the waiting Codex rows stores `tiers_checked` as `2`, and the
  upgrade turns a `1` into `2`. An import worker of the release before that
  opened the ledger before the upgrade and checks it after finds no row of
  version 7 and stores `1` again, so the next check is full.

## Consequences

- The ledger shrinks to about a fifth, and reports read faster.
- The upgrade rewrites every row once: 23 to 26 seconds for the copy of
  1.59 million rows, its compaction included, and it needs free disk of
  about three times the new file (the volume held 593 MB more at the peak,
  the write-ahead log and `VACUUM`'s temporary copy). A process that opens
  the ledger meanwhile waits for it; one of the release before keeps
  reading the last version 6 state.
- A release before this one refuses a version 7 ledger and writes nothing.
  One of its import workers already running writes no row there (its
  writes name columns version 7 does not have), and its tier check only
  makes the next one full.
- AGENTS.md's ledger exception names the version 7 conversion and the
  compaction.
- One test proves that the upgrade keeps every row of every shape, and that
  a value it does not know leaves the file unchanged.
