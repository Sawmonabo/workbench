# 21. The costs ledger stores each name once and each time as a number

- Status: Accepted, November 25, 2026
- Amends: [AGENTS.md](../../AGENTS.md), the costs ledger exception

## Context

The costs ledger keeps one row per AI response, because the reports need
each response: a response is read again from a resumed or forked transcript
and must count once (dedupe by its ID), its account, plan, price tier and
thread root are corrected after it is stored, and `--since`/`--until` cut
at any millisecond. No transcript text is stored.

A ledger of 1,586,775 responses measured 936 MiB, about 618 bytes each,
and grows by every response an agent makes. Each row repeated long text:
the project path, the session ID twice (as the session and as its root),
the account, the plan, two evidence words, the model, a 24-character time
and the response ID (28 characters for Claude Code, 61 or 73 for Codex). Six indexes copied several of those strings again; two
of them (project and model, account) served no query, and a third (tool)
made the report read the table through the index, row by row, slower than
reading it straight through.

Workbench's SQLite driver is SQLite translated to Go: it cannot load a C or
Rust extension (`load_extension` answers `not authorized`), so page or row
compression extensions are out of reach.

## Options considered

Measured on 400,000 synthetic rows shaped like that ledger, scaled to 1.59
million, after `VACUUM` with 4 KiB pages:

| Option | At 1.59 M rows | Trade-off |
| --- | --- | --- |
| Today's layout | 873 MiB | |
| **A. Each name once in a `names` table, numbers in the rows, time as milliseconds, response IDs as bytes, only the indexes a query uses (one partial).** Chosen. | 175 MiB | The report reads 30% faster (326 ms against 464 ms for 320,142 rows in Go with Workbench's driver). The IDs are no longer readable with the `sqlite3` tool. |
| B. As A, IDs kept as text | about 217 MiB | Readable IDs for about 42 MiB more. |
| C. As A, `WITHOUT ROWID` keyed by the ID | 242 MiB (text IDs) | Every other index carries the whole ID. |
| D. Rows packed per session and day into one compressed blob | 74 MiB | Every correction by ID unpacks and repacks a blob, and dedupe needs a separate ID set. |
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

## Consequences

- The ledger shrinks to about a fifth, and reports read faster.
- The upgrade rewrites every row once: seconds, not milliseconds, on a large
  ledger, and the compaction needs free disk of about twice the new file. A
  process that opens the ledger meanwhile waits for it.
- A release before this one refuses a version 7 ledger and writes nothing.
- AGENTS.md's ledger exception names the version 7 conversion and the
  compaction.
- One test proves that the upgrade keeps every row of every shape, and that
  a value it does not know leaves the file unchanged.
