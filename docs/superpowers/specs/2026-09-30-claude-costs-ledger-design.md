# claude-costs ledger design

Status: implemented 2026-09-30 on branch `feat/claude-costs-ledger`; observed
checks are recorded in section 9. The implementation plan refined a few details
of the approved design (rates parsing, override validation, refresh back-off),
and checking it against real transcripts added advisor rows (section 4). The
hooks are not yet applied to any real machine. This design
supersedes the transcript-scanning design of the earlier
`home/dot_local/bin/executable_claude-costs` script and the claude-costs row in
[contracts](workbench-contracts.md).

## 1. Problem

`claude-costs` reports per-project, per-model Claude Code spend by re-reading
the session transcripts under `~/.claude/projects` on every run. Observed on one
developer machine on 2026-09-30, that gives a total several times too low, for
six independent reasons:

1. Discovery globs one directory level, so subagent transcripts under
   `<session>/subagents/` are never read. They held 97% of the files and 86% of
   the API responses.
2. Deduplication keeps the first record per request. Subagent transcripts write
   streaming-partial usage first, so output tokens were undercounted 3.5x.
3. The rate card was stale: Fable priced as Sonnet, Opus 5.5 and Sonnet 5 too
   high, cache-read rates wrong for Fable 5.1 and Opus 5.5.
4. One-hour cache writes bill at 2x input; the tool priced every write at the
   five-minute 1.25x.
5. Claude Code deletes transcripts after `cleanupPeriodDays` (default 30).
   Anything older is gone, and the per-project `lastCost` counters in
   `~/.claude.json` describe only each project's most recent session.

6. Advisor-tool calls run on a different model and their usage appears only in
   `usage.iterations`, never in the top-level counts, so it was never counted.
   On the development machine that was roughly a fifth of the corrected
   total. (Found while checking the implementation; the
   approved design did not mention it.)

Transcripts also carry no account identifier, so per-account reporting is
impossible from them after the fact.

The owner wants a complete, permanent cost record on the machine by project,
model, account and in total, priced at current list rates, while leaving
`cleanupPeriodDays` alone so session resume, rewind and subagent history keep
their current retention.

## 2. Decision

Decouple the record from the transcripts. A background ingest copies each API
response's usage into a durable SQLite ledger while the transcript still exists.
Reports read the ledger only. The ingest is triggered by Claude Code hooks
marked `async`, so it never blocks or touches a session.

Rejected alternatives:

- Daily aggregates instead of per-response rows: smaller, but bakes rates in at
  ingest time. Rates were wrong for weeks without notice; the record must stay
  reinterpretable.
- Archiving raw transcripts: complete, but 1.5 GB per six weeks, keeps prompts
  and file contents in a second place, and still reparses on every report.
- OS schedulers (launchd, systemd timers, cron): three platform paths and a new
  external effect for a job that only has work when Claude Code has just run.
- A shell-startup daily sweep: portable, but cannot attribute an account and is
  redundant with the SessionStart hook.

## 3. Components

One Python 3 script, standard library only, rewritten in place at
`home/dot_local/bin/executable_claude-costs`. Subcommands: `ingest`, `report`
(default when no subcommand is given), `rates`, `status`, `help`. The current
`models`, `json`, `csv`, `calibrate` and `clean` subcommands are removed:
`--by model`, `--json` and `--csv` replace the first three, calibration becomes
one rate source inside `rates`, and there is no cache to clean.

| Path on the machine | Role |
| --- | --- |
| `~/.local/share/claude-costs/ledger.sqlite` | The durable record. User data; the tool never deletes, renames or recreates it. |
| `~/.local/share/claude-costs/rates-official.json` | Cached rate card parsed from the pricing page, with ETag, Last-Modified and fetch time. |
| `~/.config/claude-costs/rates.json` | Manual per-model overrides, highest priority. Each entry maps a model prefix to any of the five rate fields; any other key rejects the whole file with a message naming it. The removed `cache_write` key is not translated. |
| `~/.local/state/claude-costs/ingest.log` | Bounded worker log. |
| `~/.local/state/claude-costs/ingest.lock` | Single-instance lock. |

`XDG_DATA_HOME`, `XDG_STATE_HOME` and `XDG_CONFIG_HOME` are honored when set.
The environment overrides `CLAUDE_COSTS_LEDGER`, `CLAUDE_COSTS_RATES` and
`CLAUDE_COSTS_STATE` replace the current `CLAUDE_COSTS_PROJECTS` and
`CLAUDE_COSTS_CACHE`. The old per-file cache under `~/.cache/claude-costs` is no
longer read; the tool leaves it in place.

Hooks live under `enforced.hooks` in `home/.chezmoidata/claude.json`:

```json
"hooks": {
  "SessionStart": [{ "hooks": [{ "type": "command", "command": "~/.local/bin/claude-costs ingest", "async": true, "timeout": 10 }] }],
  "SessionEnd":   [{ "hooks": [{ "type": "command", "command": "~/.local/bin/claude-costs ingest", "async": true, "timeout": 10 }] }]
}
```

The command names `~/.local/bin` explicitly, so a launcher with a minimal `PATH`
(a desktop or IDE start) still finds it.

The settings modify template merges maps and replaces arrays, so these two
event arrays are owned by Workbench and any other hook event in the live file is
preserved. The bash completion file under
`home/dot_local/share/bash-completion/completions/` follows the new command
surface.

Owners per [AGENTS.md](../../../AGENTS.md): the script and completion under
`home/dot_local/`, the hook entries in `home/.chezmoidata/claude.json`, the
contracts row in this directory.

## 4. Ledger schema

SQLite, WAL journal mode, five-second busy timeout. `meta.schema_version` is
`1`; a ledger with a different version makes every command fail with the path
and version, without modifying the file.

```sql
CREATE TABLE responses (
  request_id     TEXT PRIMARY KEY,
  ts             TEXT NOT NULL,          -- RFC 3339 from the record
  model          TEXT NOT NULL,
  project        TEXT NOT NULL,          -- the record's own cwd
  session_id     TEXT NOT NULL,
  account        TEXT NOT NULL,          -- email, or "unknown"
  account_source TEXT NOT NULL,          -- "session" | "sweep"
  input          INTEGER NOT NULL,
  output         INTEGER NOT NULL,
  cache_write_5m INTEGER NOT NULL,
  cache_write_1h INTEGER NOT NULL,
  cache_read     INTEGER NOT NULL
);
CREATE INDEX responses_ts ON responses (ts);
CREATE INDEX responses_project_model ON responses (project, model);
CREATE INDEX responses_account ON responses (account);

CREATE TABLE files (
  path      TEXT PRIMARY KEY,            -- absolute transcript path
  offset    INTEGER NOT NULL,            -- bytes consumed
  size      INTEGER NOT NULL,            -- at last read
  mtime_ns  INTEGER NOT NULL,            -- at last read
  last_seen TEXT NOT NULL
);

CREATE TABLE meta (key TEXT PRIMARY KEY, value TEXT NOT NULL);
-- schema_version, last_ingest_at, last_ingest_summary, last_error
```

Row semantics:

- `request_id` is the record's `requestId`, else `message.id`, else `uuid`.
  Records with model `<synthetic>` or without a `usage` object are skipped.
- Insert is an upsert. Each token column keeps the largest value seen for the
  request. Streamed usage only grows, and Claude Code copies earlier records into
  the transcript of a resumed or forked session, sometimes mid-stream or with
  zeroed top-level counts, so file order or a later partial copy cannot lower
  the final usage. `account` and `account_source` are replaced only when the new
  source is `session`.
- `cache_write_5m` and `cache_write_1h` come from
  `usage.cache_creation.ephemeral_5m_input_tokens` and
  `ephemeral_1h_input_tokens`. When that object is absent, the whole
  `cache_creation_input_tokens` count is stored as `cache_write_5m`.
- `project` is the record's `cwd`. A record without one inherits the last cwd
  seen in the same file, else the project directory name decoded from the path.
- Each `usage.iterations` entry of type `advisor_message` becomes its own row,
  keyed `<request id>:<iteration index>`, with that entry's model and token
  counts. The index is stable as later records of the same request add
  iterations, so the upsert fills in rows instead of duplicating them. Entries
  of type `message` are already inside the top-level counts, and a
  `fallback_message` record reports the fallback model at the top level, so
  neither adds a row.

## 5. Ingest

### Process shape

`claude-costs ingest` is the hook command. It reads the hook JSON from stdin
(`hook_event_name`, `transcript_path`; both optional, so a manual invocation
with no stdin also works), spawns `claude-costs ingest --worker` with those two
values as arguments in a new session with stdin, stdout and stderr redirected
to the null device and the log, and exits zero. It prints nothing
under any circumstance. With `async: true` Claude Code already discards hook
output and exit status; the detach makes the worker outlive a SessionEnd and
keeps the hook's own runtime to a process spawn on every Claude Code version.

The worker:

1. Takes a non-blocking exclusive lock on `ingest.lock`. If held, exits.
2. Truncates the log to its most recent 200 KB.
3. Discovers every `*.jsonl` recursively under `<config dir>/projects`, where
   the config dir is `CLAUDE_CONFIG_DIR` if set, else `~/.claude`.
4. Reads the account email from `<config dir>/.claude.json` at
   `oauthAccount.emailAddress`, else `unknown`. (Claude Code keeps that file at
   `~/.claude.json` when `CLAUDE_CONFIG_DIR` is unset.) Rows are tagged with source
   `session` for rows read from the `transcript_path` argument when the event
   argument is `SessionEnd`, and `sweep` for every other row.
5. For each file whose size or mtime differs from the `files` row, parses from
   the stored offset. If the size shrank or the mtime moved backwards, parses
   from zero. Only lines containing `"usage"` or `"cwd"` are JSON-decoded, as
   today. One transaction per file; the `files` row is updated in it.
6. Runs the rate refresh check in section 6.
7. Writes `last_ingest_at`, a one-line summary to `last_ingest_summary` and
   the log, and clears `last_error`.

A malformed line is skipped. A file that disappears mid-run is skipped and its
`files` row deleted. Any other exception is logged with the file in progress
and stored in `last_error`; committed files are not reprocessed next time.

### Inline first run

`report` and `status` on a ledger with no `responses` rows run the worker
inline with a progress line on stderr, so the tool works before the hooks are
applied. This is the same worker function; there is no second ingest path.

### Backfill limit

The first ingest tags every existing transcript with the account logged in at
that moment and source `sweep`. Sessions that ran under a different account
before that cannot be told apart afterwards. The report header says how many
rows carry each source.

## 6. Rates

A rate card maps a model-id prefix to five USD-per-million-token fields:
`input`, `output`, `cache_write_5m`, `cache_write_1h`, `cache_read`. Cost of a
row is the dot product of its five token columns with those fields. Matching is
longest prefix within a source, so `claude-opus-5-5` beats `claude-opus`.

Sources, in this order; the first source with a matching prefix decides, so an
override keyed `claude-opus` beats an official `claude-opus-5-5` row:

1. `~/.config/claude-costs/rates.json`, the manual override file.
2. `rates-official.json`, parsed from
   `https://platform.claude.com/docs/en/about-claude/pricing.md`.
3. Calibration from `projects.*.lastModelUsage` in `.claude.json`, which pairs
   per-model token counts with the `costUSD` Claude Code computed. Ordinary
   least squares in pure Python, used only for models with at least four
   observations and a non-negative solution.
4. The built-in table, maintained at current list prices in the script.

### Refresh policy

Only the background worker fetches, after ingesting, and only when the cached
card is older than seven days or the ledger contains a model that no source
prices, and at most one attempt per day whatever the outcome. The request is a conditional GET carrying the stored ETag and
Last-Modified, with a five-second timeout. A 304 updates only the fetch time.
Any network error, non-200 status or parse failure leaves the existing card
untouched and writes one log line. `rates --refresh` runs the same fetch inline
and reports the outcome.

### Page parsing

The page is Markdown. The parser reads each table whose header row contains a
cell matching `input` and one matching `output`. The first table that prices a
model wins; tables under batch or fast-mode headings are skipped, and names with
parenthesized notes are cleaned while cells naming several models are dropped. Columns are mapped by header
text: `input`, `output`, `5m` or `write` for the five-minute write, `1h` for
the one-hour write, `read` for cache read. Missing write columns are derived as
1.25x and 2x input. A row is accepted when its first cell names a model and the
mapped cells parse as dollar amounts. Display names normalize to ids by
lowercasing, dropping a leading `claude`, and replacing spaces and dots with
hyphens, so `Claude Opus 5.5` becomes `claude-opus-5-5`. A card with zero
accepted rows is a parse failure.

### Display

`claude-costs rates` prints the merged card, the source of each row, the
official card's fetch date, and flags any model whose official and calibrated
rates differ by more than five percent.

## 7. Reporting

`claude-costs [report]` reads the ledger only.

| Flag | Meaning |
| --- | --- |
| `--by project` | Default. One block per project with nested model rows. |
| `--by model`, `--by account`, `--by month` | Flat table on that dimension. |
| `--since`, `--until` | Inclusive `YYYY-MM-DD` bounds on the response timestamp. |
| `--all` | Include projects outside `~/dev` and `~/repos`. |
| `--top N`, `--sort cost|name|calls`, `--compact` | As today. |
| `--no-rollup` | Keep git worktrees separate. By default a path containing `/.worktrees/<name>` folds into the path before that segment. |
| `--json`, `--csv` | Machine output of the same rows. |

Every human report ends with totals by model, totals by account, and the grand
total across all accounts. Percentages and the grand total always cover every
in-scope row, not only the rows shown by `--top`.

The header shows: earliest and latest response, row count, rows by account
source, last ingest time, rate card date and sources, and one line noting that
figures are list-price equivalents, not subscription charges.

`claude-costs status` shows the ledger path and size, the same coverage lines,
last ingest summary, last error, whether a worker currently holds the lock,
rate card age, and whether both hooks are present in the live
`<config dir>/settings.json`.

## 8. Failure handling

- Hook invocation: always exit zero, never any output.
- Worker: per-file transactions, errors logged and stored in `meta`, resume from
  committed offsets next run.
- Unreadable, locked-beyond-timeout or wrong-version ledger: `report`, `status`
  and `rates` fail with the path and reason. Nothing is deleted or rebuilt.
- Rates: any failure keeps the previous card; a model with no rate is counted
  in tokens and shown with cost zero and an `(unpriced)` flag, as today.
- Concurrency: the lock serializes workers; WAL lets a report read the last
  committed state during an ingest.

## 9. Verification

No test suite, per the repository rule: the ledger only inserts and upserts, so
there is no catastrophic-loss path to guard. Observed checks before commit:

1. Backfill on a machine with real transcripts, then compare the ledger's cost for each
   session whose transcript still exists against that project's `lastCost` in
   `~/.claude.json`. Agreement within a few percent validates parsing and rates
   together, for sessions that ran once. A resumed session's `lastCost` also
   counts earlier runs and is expected to exceed the ledger's figure.
2. With the hooks applied, start and end a session and confirm `status` shows a
   newer ingest time, with no output or delay in the session.
3. `python3 -m py_compile` on the script and a Ruff check with the repository's
   configuration.
4. `scripts/render-check.sh` for the affected roles and modes after the
   `claude.json` change.

### Observed 2026-09-30

Backfill of one developer machine's real transcripts into a scratch ledger
(read-only on `~/.claude`; nothing written to the real ledger paths):

- 1,164 transcript files and 71,138 ledger rows covering six weeks, ingested in
  5.2 seconds. The official pricing page fetch succeeded and priced 19 models.
  All rows are sweep-tagged because no hook has run.
- The ledger total was about eight times the total from the script it replaces
  and about 1.4 times the sum of the per-project `lastCost` counters, which only
  describe each project's most recent run.
- Per-session comparison with the `~/.claude.json` counters, after advisor rows:

| Session | Kind | Cost, ledger / counter | Output tokens, ledger / counter |
| --- | --- | --- | --- |
| A | single run | 1.00 | 1.00 |
| B | single run | 0.96 | 0.99 |
| C | single run | 0.91 | 0.95 |
| D | resumed | 1.79 | 1.77 |
| E | resumed | 2.40 | 2.81 |

  The single-run sessions agree within 9%. The resumed sessions were resumed, so
  their transcripts hold more than the counters, which describe only the last
  run. Session C is the lowest and is explained by Claude Code's own short
  internal calls, which the counters include and no transcript records. Before
  advisor rows it was 43% low, which is how they were found.
- Every smoke check in the implementation plan matched its written expectation,
  including the hook returning in 0.02 seconds with no output, the worker
  surviving the hook, lock contention, an unwritable data directory, an invalid
  override file, and a foreign file at the ledger path.
- `render-check.sh` passed for personal/pinned, work/latest and
  work/pinned/wsl, and the settings merge kept a user-added hook and theme
  while adding both enforced events. These ran in a scratch copy with WSL host
  detection disabled, because the WSL sizing prompts need a terminal.

Not yet verified: a live session with the hooks applied (check 2 above), which
needs a `workbench` apply of the changed `claude.json`; the same render checks on
a real WSL host; macOS with its system Python 3.9.

## 10. Out of scope

Recovering transcripts already deleted, usage from API keys or other tools,
actual subscription charges, and Windows-native Claude Code installations.
