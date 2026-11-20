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
2. **A refresh** replaces one tool's `prices` rows and its `price_lists`
   row in one transaction. A page that cannot be read keeps the rows and
   records the attempt, as today.
3. **The price list** a report uses is built as today, with the official
   rows read from `prices` and no overrides layer. A model without a price
   is named as unpriced, without a hint to a file.
4. **Migration `0007-price-caches`** deletes the downloaded list files
   (`<tool>/rates-official.json`) and their emptied folders from the costs
   data folder. They are caches: the import worker downloads each list
   again at its next run, which it does at once for a tool with no list.
   Until then reports use the shipped list and the tools' own cost records.
5. **An overrides file a machine still has** stays where it is, untouched
   and unread. `0001-costs-folders` no longer moves one, and deletes the
   downloaded lists of the old folders, which are caches, where it moved
   them before.

## 5. Ownership and documents

| Concern | Owner |
| --- | --- |
| The tables, the refresh, the price list | `internal/agent/costs/rates.go` |
| The schema and its upgrade | `internal/agent/costs/ledger.go` |
| The deletion of the list files | `internal/migrate/0007_price_caches.go` |

Changed in place: the costs sections of `docs/usage.md` and
`docs/acceptance.md`, the costs specs' current description of prices, and
`AGENTS.md` where it names the costs files.

## 6. Safeguard

The ledger's existing upgrade safeguard covers version 6: a version 5
ledger holds every row after the upgrade.

## 7. Left to the builder

- The messages' exact wording.
- Whether `agent costs rates` shows anything in place of the overrides it
  listed.
