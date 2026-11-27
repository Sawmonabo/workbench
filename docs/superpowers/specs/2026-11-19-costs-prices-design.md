# Costs prices design

The prices `workbench agent costs` downloads live in the ledger, and the
hand-written price overrides file is no longer read.

## 1. Intent

A costs report prices usage from four layers: the list Workbench ships, the
tools' own cost records, the official price page of each tool, and a file a
person could write by hand. Two of these are files beside the ledger: the
downloaded lists (`rates-official.json`, one per tool) and the overrides
(`rates.json`). Nothing ever wrote the overrides file but a person, and the
downloaded lists are small tables kept as JSON. The ledger is a database
with a schema; prices belong in it.

## 2. Goals, non-goals, anti-goals

Goals:

- The downloaded prices are rows in the ledger, with where and when each
  list was downloaded.
- The overrides file, its reader, its layer and every message that names
  it are gone.
- The costs data folder holds the ledger and nothing else.

Non-goals:

- No change to which price a model gets, or to any report's numbers, once
  the lists are downloaded again.
- No history of prices: a list replaces the one before it, as the file did.
- No command to set a price by hand.

Anti-goals:

- A report whose total changes because prices moved.
- A ledger upgrade that loses a row.

## 3. Approaches

| Approach | Trade-off |
| --- | --- |
| **Prices in two ledger tables; the price for a model still chosen in Go.** Chosen. | A proper schema and no files. A family row prices its versions (`claude-opus` prices `claude-opus-5-5`), the most specific row wins and a service tier must match (`RateCard.Lookup`), which a join on equal names cannot say; a report already sums tokens per model in SQL and prices each sum in Go, and that stays. |
| The same tables, with the choice of price written in SQL | Every report's pricing is rewritten for no change a person sees, and a mistake there is a wrong spend total. |
| Leave the lists as files | Two more files and two more folders beside a database that could hold them. |

## 4. Rules

1. **Ledger schema version 6** adds:

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
   keeps every row, as it does for earlier versions.
2. **The upgrade fills the tables from the list files.** In the same
   transaction it reads each `<tool>/rates-official.json` beside the ledger
   into `prices` and `price_lists`, so no report is priced from fewer
   prices than before, not for a second. A file that is missing or that it
   cannot read fills nothing for that tool, whose list the import worker
   then downloads at its next run. When the ledger's folder moves under
   `agent/` while the upgrade reads the files, the upgrade fails and is
   undone, and the open is done again where the ledger is then (agent
   folders design, rule 2), beside the files.
3. **A refresh** downloads first, with no transaction open, then replaces
   one tool's `prices` rows and its `price_lists` row in one short
   transaction. A page that cannot be read, or that holds no price, keeps
   the rows of any age and records the attempt, as today. It needs no
   import lock, as a session's binding needs none.
4. **The price list** a report uses is built as today, with the official
   rows read from `prices` and no overrides layer. A model without a price
   is named as unpriced, without a hint to a file.
5. **`agent costs rates`** opens the ledger as a report does. Without a
   ledger it shows the shipped list and the tools' own cost records and
   says there is no ledger yet; `--refresh` then downloads nothing.
6. **Migration `0008-price-lists`** deletes the list files and their
   emptied folders from the costs data folder, after the folder move
   (`0007-agent-folders`). It first opens the ledger, which upgrades it, so
   a file is never deleted before its prices are rows. The files are
   caches Workbench wrote.
7. **The overrides file** is unread, and nothing names it: not `status`
   (its "overrides present" line and `has_overrides` field go), not a
   report, not a message. A file a machine still has stays where it is,
   untouched. `0001-costs-folders` no longer knows it: it neither moves it
   nor looks in its folder nor reads `CLAUDE_COSTS_RATES`. It still moves
   the list files of the old folders beside the ledger, before the ledger,
   whose first open at its new path is the upgrade of rule 2, so that
   upgrade finds them.
8. **A process of the release before** refuses a version 6 ledger and
   writes nothing to it. An import worker of that release started in the
   moment of the update ends there; the usage it would have read is read by
   the next worker.

## 5. Ownership and documents

| Concern | Owner |
| --- | --- |
| The reads and writes of the tables, the refresh, the price list | `internal/agent/costs/rates.go` |
| The schema, the tables' included, and its upgrade | `internal/agent/costs/schema.go`, `upgrade.go` |
| The deletion of the list files | `internal/migrate/0008_price_lists.go` |

Changed in place: the costs sections of `docs/usage.md` and
`docs/acceptance.md`, the costs specs' current description of prices, and
`AGENTS.md` where it names the costs files.

## 6. Safeguard

The ledger is the only lasting record of spend. One test starts from a
version 5 ledger with rows and a list file beside it, and proves that after
the upgrade every row is there and the file's prices are in `prices`. It
says that risk beside it. The first costs move's safeguard proves that a
file Workbench did not write in each old folder stays where it is, byte for
byte, and is named.

## 7. Left to the builder

- The messages' exact wording.
- What `agent costs rates` shows where it listed the overrides.
