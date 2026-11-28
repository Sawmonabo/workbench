# Costs ledger slim design

The costs ledger keeps every fact it keeps today in about a fifth of the
space, and every report prints the same bytes, faster
([ADR 21](../../adr/0021-costs-ledger-stores-names-once.md)).

## 1. Intent

The ledger holds one row per AI response: token counts, the time, and who,
where and with what the response was made. A copy of a real ledger,
1,587,060 responses, measured 935 MiB, because each row
repeats long text (project path, session ID twice, account, plan, evidence
words, model, a time string, the response ID) and six indexes copy it
again. The rows are needed (dedupe by ID, late corrections, millisecond
windows); the repetition is not.

## 2. Goals, non-goals, anti-goals

Goals:

- Every value the ledger holds today is still there, exactly.
- Every report, filter and view prints the same bytes: `agent costs` with
  every `--by`, `--since`/`--until`, `--top`, `--sort`, `--all`,
  `--detail`, `--tokens`, `--no-rollup`, `--csv`, `--json`, the drill-down
  pages, `agent costs status` and `agent costs rates`.
- The late corrections work as today: account and plan resolution, Codex
  price tiers, Codex thread roots, dropping held copies.
- The file is about a fifth of today's size, and a report reads faster.

Non-goals:

- No change to what the import reads, how a response is priced, or any
  other table's layout beyond `tier_pending`.
- No rollup, no compression extension, no packing of rows into blobs.

Anti-goals:

- An upgrade that loses, merges or alters a row, or that guesses a value it
  does not know.
- A report total that changes.
- An import, resolution or report that is slower than today.

## 3. Approaches

| Approach | Trade-off |
| --- | --- |
| **Names once, numbers in the rows, time in milliseconds, IDs as bytes, three indexes (one partial).** Chosen. | 196 MiB for the copy of a real ledger (935 MiB today); every SQL statement on `responses` and `tier_pending` changes. |
| The same with IDs as text | About 42 MiB more (synthetic measure); IDs readable with the `sqlite3` tool. |
| Packing rows into compressed blobs; compression extensions; rollups | See ADR 21: slower corrections, not loadable, or data lost. |

## 4. Rules

1. **Schema version 7** replaces `responses` and `tier_pending` (every other
   table stays as it is):

   ```sql
   CREATE TABLE names (
     id   INTEGER PRIMARY KEY,
     kind INTEGER NOT NULL,  -- 1 tool, 2 model, 3 project, 4 session (also a root), 5 account, 6 subscription
     name TEXT NOT NULL,
     UNIQUE (kind, name)
   );
   CREATE TABLE responses (
     id                  INTEGER PRIMARY KEY,
     request_id          BLOB NOT NULL UNIQUE,  -- rule 3
     tool                INTEGER NOT NULL,      -- names.id, as the next six
     model               INTEGER NOT NULL,
     project             INTEGER NOT NULL,
     session             INTEGER NOT NULL,
     root                INTEGER NOT NULL,      -- a session name; '' is a name too
     account             INTEGER NOT NULL,
     subscription        INTEGER NOT NULL,
     account_source      INTEGER NOT NULL,      -- rule 2
     subscription_source INTEGER NOT NULL,
     ts                  INTEGER,               -- Unix milliseconds, UTC; NULL when the response has no time
     input INTEGER NOT NULL, output INTEGER NOT NULL,
     cache_write_5m INTEGER NOT NULL, cache_write_1h INTEGER NOT NULL, cache_read INTEGER NOT NULL
   );
   CREATE INDEX responses_root ON responses (tool, root);
   CREATE INDEX responses_open ON responses (tool, ts)
     WHERE account_source < 4 OR subscription_source < 4;
   CREATE TABLE tier_pending (
     request_id BLOB PRIMARY KEY,  -- rule 3
     root       INTEGER NOT NULL   -- names.id of the root thread
   ) WITHOUT ROWID;
   ```

   Every value that is text today (including `''`, `unknown` and the tool)
   is a name, so a row reads back as exactly the text it held. The session
   and the root share kind 4, so a root that is its session is the same
   number.
2. **Evidence is its rank**, the number `evidenceRank` gives today:
   transcript 4, session 3, observed 2, inferred 1, unknown 0. SQL compares
   evidence as numbers; the `evidenceRank` CASE goes. The partial index's
   condition is the one the account resolution reads (`accountsQuery`): the
   query must state it in the same words, or SQLite cannot use the index.
3. **A response ID is stored as bytes** that decode to exactly the text it
   is stored as today (`storedID`), by its first byte:

   | Tag | Then | For the stored text |
   | --- | --- | --- |
   | `0x01` | 15 bytes, big-endian: the 20 characters read as one base-62 number, digits `0-9A-Za-z` | `req_011C` + 20 characters of `[0-9A-Za-z]` |
   | `0x02` | the bytes the hex spells | `codex:resp_` + an even count (at least 2) of lowercase hex digits |
   | `0x03` | the bytes the hex spells | `codex:tc:` + an even count (at least 2) of lowercase hex digits (a token count's digest) |
   | `0x00` | the text itself, UTF-8 | anything else (a copy key `copy:…` or `codex:copy:…`, any other shape) |

   In a ledger of 1.59 million rows, 1,263,746 IDs took tag 1, 112,202 tag
   2, 199,016 tag 3 and 12,096 tag 0, 12,093 of them an advisor call's key
   (a Claude Code ID, `:` and the call's index).

   Decoding a tag-1 or tag-2 value gives back the same text, leading zeros
   included. A key range on the text (`DropHeldCopies`) is a range on
   `0x00` + the text. Lists of IDs handed to SQL (`json_each` in
   `accountsQuery`) carry them in a form SQL can turn back into the bytes
   (hex and `unhex`, say).
4. **The upgrade** from version 6 runs after the existing steps that bring
   versions 1 to 5 to version 6, in the same transaction, which keeps every
   row:
   - it builds `names` from every distinct value, copies every row into the
     new tables, keeping each row's rowid as its `id`, and drops the old ones
     and their indexes;
   - an evidence word other than the five, or a time that is neither `''`
     nor exactly `timeLayout`, refuses the upgrade with the path and the
     value, and the file is left as it was;
   - before it commits, it checks that the new tables hold as many rows as
     the old, the same sum of each token column per tool, and that every new
     row decodes to its old row field for field; a difference rolls it back
     with an error naming the first row that differs;
   - it does not fill the price tables again (that step belongs to the
     upgrade to version 6). The open without an upgrade that the costs move
     uses (`openLedger(…, upgrade=false)`) accepts version 6 and 7.
5. **Waiting for the upgrade.** Converting a large ledger takes seconds.
   A process that opens the ledger while another upgrades it waits until the
   upgrade ends (it waits on the write lock longer than the usual five
   seconds, for as long as an upgrade of the largest ledger measured takes,
   with a margin), then finds version 7 and uses it. Readers of a version 6
   ledger, such as the release before, keep reading the last committed state
   meanwhile.
6. **Compaction.** The upgrade's transaction sets the meta key
   `compact_due`. After it commits, the same process runs `VACUUM`, then a
   checkpoint that truncates the write-ahead log, and deletes the key. When
   the `VACUUM` fails (busy, a full disk) the ledger is a valid version 7
   with free pages; the key stays, the reason goes to the import log, and
   each import worker tries again, holding the import lock, until it
   succeeds. Nothing else rewrites the file.
7. **Times meet times.** Only `responses.ts` becomes a number; every other
   table and meta key keeps its `timeLayout` text. In SQLite a number sorts
   below any text, so `ts > '2026-…Z'` matches nothing, without an error.
   Before changing a statement, the builder lists every place a response's
   time meets another time, in SQL and in Go (the account resolution's
   watermark `accounts_through:<tool>` and stretch start, `signedIn`,
   `between`, `sessionStarts`, the tier series, the `tier_pending` join, the
   time inside a copy key), and converts at that boundary: a bound handed to
   SQL is milliseconds, and `""` (no bound) leaves the condition out. The
   correction code may keep the text times it uses today for the rows it
   reads.
8. **Writes.** The import resolves each name to its number once per name
   (a cache loaded at the transaction's start and filled as it inserts new
   names) and writes numbers. The upsert keeps its rules: the earliest copy's
   time, model, project and session; MAX of each token count; a field's
   account or plan replaced only by stronger evidence; the root rule. A
   time with no value is NULL; `earlier` compares numbers.
9. **Reads.** A report loads `names` once and reads `responses` straight
   through (`WHERE +tool = ?`: with an index on `tool` first, SQLite reads
   the table through that index, row by row, which on synthetic rows
   measured 2.5 times slower; on the compacted copy of a real ledger both
   took 0.16 seconds in the `sqlite3` shell). The report code gets the
   values it gets today; how a time travels in Go is the builder's choice
   (milliseconds compare and convert to a local day faster than parsing the
   text).
10. **No query gets slower.** Each statement on `responses` and
   `tier_pending` is checked with `EXPLAIN QUERY PLAN` on a copy of a real
   ledger, old layout and new: a lookup by ID, a root move and each of the
   account resolution's real shapes (its open condition outside an `OR` of
   request IDs, roots, a watermark, a new subscription and a stretch) use an
   index; the report and the per-tool group scans read the table, as they
   read all of a tool's rows today. When SQLite does not take the partial
   index inside the `OR`, the condition is repeated inside the branch, or
   the index covers every row (about 14 MiB more at 1.59 million rows,
   synthetic measure).
11. **A process of the release before** refuses a version 7 ledger and
    writes nothing (its own version check). An import worker of that release
    started at the moment of the update ends there; the next worker reads
    what it would have read. Its writes name columns version 7 does not
    have, and fail; its tier check finds no row of version 7 yet stores
    `tiers_checked` `1`, which version 7 does not take as checked (it
    stores `2`), so the next check covers every waiting row, as the first
    check after the upgrade does.

## 5. Ownership and documents

| Concern | Owner |
| --- | --- |
| The schema, its upgrade and the compaction, the conversion and its check, the ID encoding, the names cache | `internal/agent/costs/schema.go`, `upgrade.go`, `slim.go`, `storedid.go`, `names.go` |
| The writes to `responses` | `internal/agent/costs/responses.go`, `threads.go`, `tiers.go`, `accountresolution.go`, `sessionplans.go` |
| The report's read | `internal/agent/costs/report.go`, `focus.go` |
| The model list of `agent costs rates` | `internal/agent/costs/rates.go` |

Changed in place: the schema section of the costs ledger design
(`2026-09-30-claude-costs-ledger-design.md`, section 4, written as version
7) and its row rules, the Codex costs design where it names `tier_pending`,
`docs/acceptance.md` (the costs entry and the evidence matrix row), and
AGENTS.md's ledger exception (the version 7 conversion and the one
compaction).

## 6. Safeguard

The ledger is the only lasting record of spend. One test starts from a
version 6 ledger holding a row of each shape (a Claude ID, a Codex ID, a
copy key of each tool, an ID the encoding does not know, an undated row,
every evidence word, a root that is its session and one that is not, a
`tier_pending` row) and proves that after the upgrade every row reads back
field for field as before. A second case proves that an unknown evidence
word refuses the upgrade and leaves the file byte for byte. The encoding's
edge cases (leading zeros, the largest base-62 value, uppercase or odd hex,
`req_011C` with 19 or 21 characters) are in the first test's rows. Each
test says the risk beside it.

## 7. Proof before release

1. A copy of a real ledger, taken with SQLite's backup (never the original
   opened), upgraded by the new build: its size, the upgrade's and the
   compaction's times, the most disk the folder used meanwhile (the
   ledger, its write-ahead log and the compaction's temporary copy),
   `PRAGMA integrity_check`. The ADR's and this design's numbers are then
   replaced by these.
2. Every report of section 2, printed by the release before from the copy
   before the upgrade and by the new build after, compared byte for byte,
   both tools, with windows that cut inside a millisecond.
3. The corrections: two more copies, the meta keys `accounts_checked` and
   `tiers_checked` deleted in each so the next import resolves every row,
   one imported by the release before and one by the new build in the same
   disposable home with no transcripts; every row of `responses` and
   `tier_pending`, decoded, compared row for row.
4. The report's, an import's and the account resolution's times, before and
   after.
5. The update from the release before in a disposable home with synthetic
   transcripts, an import running during the upgrade, every row kept.

## 8. Left to the builder

- How the upgrade copies the rows (SQL with Go functions registered on the
  driver, or Go streaming), within rule 4.
- The Go types that carry a stored time and a packed ID.
- The longer lock wait's value, from the measured upgrade time.
- Messages' exact wording.
