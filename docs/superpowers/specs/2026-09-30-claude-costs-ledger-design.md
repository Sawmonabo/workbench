# claude-costs ledger design

Status: implemented 2026-09-30 as `workbench costs` (section 11); the Python
script it replaced and the command names and paths of sections 3, 5 and 7 are
superseded where section 11 differs. Observed checks are recorded in section 9.
The implementation plan refined a few details of the approved design (rates
parsing, override validation, refresh back-off), and checking it against real
transcripts added advisor rows (section 4). The hooks are not yet applied to
any real machine. This design supersedes the transcript-scanning design of the
earlier `claude-costs` script and its row in [contracts](workbench-contracts.md).

## 1. Problem

`claude-costs` reports per-project, per-model Claude Code spend by re-reading
the session transcripts under `~/.claude/projects` on every run. Observed on a
real history on 2026-09-30, that gives a total several times too low, for six
independent reasons:

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
that return within milliseconds, so they neither wait for the ingest nor touch a session.

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
| `~/.local/share/claude-costs/ledger.sqlite` | The durable record. User data; the tool never deletes, renames or recreates it. Private: created 0600 in a 0700 directory, and an older ledger left readable by others is tightened to 0600 (mode only) once it is verified. |
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
  "SessionStart": [{ "hooks": [{ "type": "command", "command": "~/.local/bin/claude-costs ingest", "timeout": 5 }] }],
  "SessionEnd":   [{ "hooks": [{ "type": "command", "command": "~/.local/bin/claude-costs ingest", "timeout": 5 }] }]
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
- `usage.speed` of `"fast"` stores the response's model as `<model>@fast` (the
  service-tier id Codex uses, section 4 of the Codex spec); any other value, or
  none, is the plain model. An advisor row keeps the advisor's own model, which
  its iteration bills; iterations carry no speed.
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
under any circumstance. Claude Code discards a SessionEnd hook's output and
exit status; the detach makes the worker outlive the session and keeps the
hook's own runtime to a process spawn on every Claude Code version. The hook
is synchronous: Claude Code kills a background (`async`) hook still running
when a headless `claude -p` session ends, so the ingest would race the exit.

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

`report` and `status` run the worker inline, with a progress line on stderr,
when a recorded tool has transcripts but no rows in the ledger (a new ledger, or
a tool added after the hooks were installed), so the tool works before the hooks
are applied. This is the same worker function; there is no second ingest path.

### Backfill limit

The first ingest tags every existing transcript with the account logged in at
that moment and source `sweep`. Sessions that ran under a different account
before that cannot be told apart afterwards. The report header says how many
rows carry each source.

## 6. Rates

A rate card maps a model-id prefix to five USD-per-million-token fields:
`input`, `output`, `cache_write_5m`, `cache_write_1h`, `cache_read`. Cost of a
row is the dot product of its five token columns with those fields. Fast-mode
prices are `@fast` rows; a model with no `@fast` row is reported unpriced when it
has fast-mode responses, never priced at the standard rate.

Matching: an override row with a matching prefix always wins, so an override
keyed `claude-opus` beats an official `claude-opus-5-5` row. Among the other
sources the longest matching prefix wins, wherever it comes from, so
`claude-opus-5-5` beats `claude-opus`; a dated snapshot suffix (`-20251101`)
does not make a prefix longer, so Claude Code's dated calibration key ties the
page's undated row for the same model. Rows equally specific go by this source
order:

1. `~/.config/claude-costs/rates.json`, the manual override file.
2. `rates-official.json`, parsed from
   `https://platform.claude.com/docs/en/about-claude/pricing.md`.
3. Calibration from `projects.*.lastModelUsage` in `.claude.json`, which pairs
   per-model token counts with the `costUSD` Claude Code computed. Ordinary
   least squares in pure Python, used only for models with at least four
   observations and a non-negative solution.
4. The built-in table, maintained at current list prices in the script.

### Refresh policy

Only an ingest run fetches (the background worker, or the inline first run of
section 5), after ingesting, and only when the cached
card is older than seven days or the ledger contains a model that no source
prices, and at most one attempt per day whatever the outcome. The request is a conditional GET carrying the stored ETag and
Last-Modified, with a five-second timeout. A 304 updates only the fetch time.
Any network error, non-200 status or parse failure leaves the existing card
untouched and writes one log line. `rates --refresh` runs the same fetch inline
and reports the outcome.

### Page parsing

The page is Markdown. The parser reads each table whose header row contains a
cell matching `input` and one matching `output`. The first table that prices a
model wins; tables under a batch heading are skipped; the table under the
fast-mode heading prices `<model>@fast`, where one cell may name several models
joined by `/` (`Claude Opus 5 / Claude Opus 4.8`) and each gets the row, and the
caching multipliers of the model's standard row (1.25x, 2x, and 0.1x or 0.05x for
reads) apply on top of the fast input rate; names with parenthesized notes are
cleaned and, outside the fast-mode table, cells naming several models are
dropped. Columns are mapped by header
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

Revised 2026-09-30 after the first report on a six-week ledger: 150 lines,
one block per working directory, sub-directories of one repository listed as
separate projects, lines wider than the terminal, columns named `w-5m`,
`w-1h`, `cache-r`, and a scope note printed even when nothing was hidden.

`claude-costs [report]` reads the ledger only. The default view is one line
per repository:

```
[WorkBench]  Claude Code   Codex
──────────────────────────────────────────────────────────────────────────────────────────

$1,000.00  10,000 responses · Sep 1 → Sep 30
API list-price equivalent, not a subscription bill · updated Sep 30 9:00 PM

  project                  cost  share                                calls  tokens  cached
  ───────────────────────────────────────────────────────────────────────────────────────
  ~/dev/app             $600.00  █████████████████░░░░░░░░░░░  60.0%  6,000  600.0M   95.0%
  ~/repos/service       $250.00  ███████░░░░░░░░░░░░░░░░░░░░░  25.0%  2,500  250.0M   95.0%
  ~/dev                 $100.00  ███░░░░░░░░░░░░░░░░░░░░░░░░░  10.0%  1,000  100.0M   90.0%
  ~/dev/tools            $50.00  █░░░░░░░░░░░░░░░░░░░░░░░░░░░   5.0%    500   50.0M   90.0%
  ───────────────────────────────────────────────────────────────────────────────────────
  total · 4 projects  $1,000.00                               100.0% 10,000    1.0B   94.3%

  model                cost  share                                calls  tokens  cached
  ──────────────────────────────────────────────────────────────────────────────────────
  claude-opus-5     $700.00  ████████████████████░░░░░░░░  70.0%  7,000  700.0M   95.0%
  claude-fable-5-1  $300.00  ████████░░░░░░░░░░░░░░░░░░░░  30.0%  3,000  300.0M   92.7%

←/→ tab/shift+tab switch tool  ·  ↑/↓ pgup/pgdn scroll  ·  q quit
```

Rules:

- Rollup: a session's working directory folds into the first directory below
  a scope root (`~/dev/<name>`, `~/repos/<name>`); elsewhere a path containing
  `/.worktrees/<name>` folds into the path before that segment. `--no-rollup`
  keeps every working directory separate.
- Columns: `cost`, `share` of the grand total, `calls`, `tokens` (all five
  token kinds summed) and `cached` (share of prompt tokens served from the
  cache: `cache read / (input + cache writes + cache read)`). `--tokens`
  replaces `share`, `tokens` and `cached` with `input`, `output`, `cache 5m`,
  `cache 1h`, `cache read` and adds a one-line legend to the footer.
- Width: lines are sized to the terminal (`shutil.get_terminal_size`, 100 when
  not a terminal). The name column takes what the numeric columns leave, at
  least 24, and long names are clipped from the left with `…` so the
  distinguishing tail survives. Nothing wraps.
- Sections: the main table ends with a rule and a bold `total · N <key>s` row.
  A `model` table follows unless `--by model`; an `account` table follows only
  when the ledger holds more than one account and `--by` is not `account`.
  `--detail` (project view only) prints one block per project, header
  `<project>  $cost  share · calls`, with its own model table, then a
  `model (all projects)` table carrying the total row.
- Headline: the total in the tool's color, then the response count and first
  and last day of the rows the table covers (the same period and scope as the
  total, so `--since`, `--until` and the default scope apply), then a faint `API list-price equivalent, not a subscription bill ·
  updated <local time>`. Rate sources are not repeated here; `costs rates`
  and `costs status` show them.
- Look: each tool has its own color (Claude Code `#D97757`, Codex `#10A37F`)
  for the headline, column heads, share bars and its active tab; dollar
  figures are green; calls, tokens and cached are faint. Where the terminal
  has room, each `share` cell gets a bar of the row's share, one width for
  every table (at least 8 cells), so the table fills the terminal's width; narrower
  terminals get the percentages alone. The key line names every key, with a
  short form for narrow terminals.
- Footer: only notes that report something: `N of M <key>s shown; totals
  cover all` under `--top`; `N projects outside ~/dev and ~/repos hidden
  (--all shows them)` when N > 0; the `--tokens` legend under `--tokens`.
  Notes join with ` · ` on one line when that fits, otherwise one per line.
- Empty result: `no responses matched (check \`claude-costs status\`)`, or
  when the scope hid projects, `no responses matched in ~/dev and ~/repos;
  N projects elsewhere (--all shows them)`. Exit 0.
- `rates` uses the same column names: `input`, `output`, `cache 5m`,
  `cache 1h`, `cache read`.
- Ingest statistics (session-tagged versus sweep rows, last ingest summary,
  last error) stay in `claude-costs status`; the report header no longer
  repeats them.

| Flag | Meaning |
| --- | --- |
| `--by project` | Default. One line per repository. |
| `--by model`, `--by account`, `--by month` | One line per value of that dimension. |
| `--since`, `--until` | Inclusive `YYYY-MM-DD` bounds on the response's day in the machine's local time zone; months and days in the report (`--by month`, a focus page's day table) and the dates of `status` coverage are local too. |
| `--all` | Include projects outside `~/dev` and `~/repos`. |
| `--top N` | Show the first N rows; totals and shares still cover every row. |
| `--sort cost\|name\|calls` | Row order; cost descending by default. |
| `--detail` | Per-project blocks with model rows (project view). |
| `--tokens` | The five token columns instead of `share`, `tokens`, `cached`. |
| `--no-rollup` | Keep every working directory separate. |
| `--json`, `--csv` | Machine output of the same rows; JSON adds `hidden_projects` and top-level `first` and `last` (the local days of the rows the table covers, `none` when empty); `coverage` keeps meaning the whole ledger and is what `status` shows. |

`--compact` is removed: the default view is the compact one.

`claude-costs status` shows the ledger path and size, coverage, rows by
account source, last ingest summary, last error, whether a worker currently
holds the lock, rate card age, and whether both hooks are present in the live
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

`internal/costs/ledger_test.go` guards a resumed session's smaller copy lowering
stored usage, the version 1 and 2 upgrades dropping rows, and a weaker copy
overwriting a stronger account attribution; `codex_test.go` guards a fork's copy
being counted twice or dropped. Everything else is observed checks before
commit:

1. Backfill on a machine with real transcripts, then compare the ledger's cost for each
   session whose transcript still exists against that project's `lastCost` in
   `~/.claude.json`. Agreement within a few percent validates parsing and rates
   together, for sessions that ran once. A resumed session's `lastCost` also
   counts earlier runs and is expected to exceed the ledger's figure.
2. With the hooks applied, start and end a session and confirm `status` shows a
   newer ingest time, with no output or delay in the session.
3. The repository's Go gates (`go build`, `go vet`, `go test`, golangci-lint).
4. `scripts/render-check.sh` for the affected roles and modes after the
   `claude.json` change.

### Observed 2026-09-30

Backfill of several weeks of real transcripts into a scratch ledger
(read-only on `~/.claude`; nothing written to the real ledger paths):

- About a thousand transcript files, ingested in seconds. The official pricing
  page fetch succeeded and priced every model the transcripts named.
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

Recovering transcripts already deleted, usage from API keys, actual
subscription charges, and Windows-native Claude Code installations. Other
tools (Codex) are not ingested yet; section 11 shapes the code so adding one
is a new source file, not a redesign.

## 11. `workbench costs`: one CLI, generic sources

Decided 2026-09-30. The Python script becomes a Workbench command, and the
code separates what differs per AI tool from what does not, so a later Codex
source is one file plus one line.

### Command surface

| Command | Replaces | Does |
| --- | --- | --- |
| `workbench costs` | `claude-costs [report]` | The section 7 report; at a terminal, one tab per tool. |
| `workbench costs ingest` | `claude-costs ingest` | Hook entry: silent, exit 0, starts a detached worker. `--worker` runs it inline. |
| `workbench costs rates` | `claude-costs rates` | Merged rate card; `--refresh` refetches. |
| `workbench costs status` | `claude-costs status` | Ledger coverage, last ingest, hooks, rate card age. |

Report flags are section 7's, plus `--tool NAME`. Every screen
starts with the `[WorkBench]` brand like the other commands; `--json` emits the
Workbench result envelope with the rows as details, and `--csv` stays. Cobra
generates completions, so the bash completion file is removed with the script.
Hooks in `home/.chezmoidata/claude.json` call `~/.local/bin/workbench costs
ingest`, the release symlink Workbench installs.

### Tabs

At a terminal (stdin and stdout both terminals, no `--json` or `--csv`),
`workbench costs` opens a full-screen view with one tab per tool:
`Claude Code` and `Codex`. Tab and Shift+Tab (or ← and →) switch between
the Claude Code and Codex tables, the report scrolls,
q or esc quits and leaves the last tab's report printed in the scrollback.
Every resize refits the table. The Codex tab says `Codex costs are not
implemented yet.` until a Codex source exists. Off a terminal the command
prints one tool's report, `--tool NAME` (default `claude`); `--tool codex`
prints `[WorkBench] Codex costs are not implemented yet` and exits 0. Tabs are
the split by tool, so there is no `--by tool`; flags set what every tab shows.

In the view, ↑/↓ move a `›` marker over the project and model rows and Enter
opens that row's page; ←, Esc or Backspace goes back (on a page ← never switches tools; Tab and Shift+Tab still do). A project's page shows its
total and share, its cost by model, by day (active days, newest first) and
by session (start, length, the model that cost the most, cost, calls),
costliest first. A model's page shows the same with its cost by project.
Pages read the ledger with the report's period, scope and rollup, and quitting
from one leaves that page printed.

### Package shape

```
internal/costs/
  source.go   Source interface, Usage row, the source list
  claude.go   Claude Code: transcripts, record parsing, advisor rows, account, prices
  ledger.go   SQLite ledger: open, schema check, max-wins upsert, files, meta
  ingest.go   worker over every source: offsets, lock, log, rate refresh
  rates.go    card resolution: override first, then the most specific prefix, then official, calibrated, built-in
  report.go   groups, rollup, scope, totals; returns rows and prints nothing
internal/cli/
  costs.go      the four commands and their flags; the plain report
  costsview.go  the tabbed terminal view
  table.go      terminal-width tables, shared with the apply checklist
```

```go
// Source is one AI tool whose transcripts the ledger records. Everything a
// source knows is about its own files and prices; it never writes.
type Source interface {
	Name() string                               // "claude"; later "codex"
	Transcripts(home string) ([]string, error)  // files to ingest
	Parse(line []byte, file *FileState) []Usage // usage rows in one line
	Account(home string) string                 // who was signed in, or "unknown"
	Builtin() RateCard                          // prices shipped with Workbench
	PricingURL() string                         // official price page, "" when none
	ParsePricing(page string) RateCard          // that page's prices
	Observations(home string) []Observation     // the tool's own (tokens, cost) pairs, for calibration; nil when none
	Hooks(home string) map[string]bool          // hook event → whether the ingest hook is installed
	Session(file, transcript string) bool       // is file part of the session the hook's transcript names
}

// Usage is one billed response, whatever tool produced it.
type Usage struct {
	Tool, RequestID, Model, Project, Session, Account string
	Time                                              time.Time
	Input, Output, CacheWrite5m, CacheWrite1h, CacheRead int64
}

// Tool is one tab. A nil Source is a tool Workbench knows of but does not
// record yet: its tab says so and ingest skips it.
type Tool struct {
	Name, Title string // "codex", "Codex"
	Source      Source
}

var Tools = []Tool{
	{Name: "claude", Title: "Claude Code", Source: claude{}},
	{Name: "codex", Title: "Codex"}, // adding Codex is setting its Source
}
```

Rules:

- Everything outside a source file is tool-agnostic. A source never touches
  the ledger, the lock or the output; ingest, rates and report never parse a
  transcript.
- Token mapping for any source: input, output and cache read as named; cache
  writes by lifetime where the tool reports them, else 0; reasoning output
  counts as output, the rate it is billed at. A source with fewer token kinds
  leaves the others 0.
- `FileState` carries what a source needs between lines of one file (the last
  `cwd` for Claude) and the fallback project decoded from the path.
- Each source owns its built-in prices, its official pricing-page parser and
  its calibration observations; fetching, caching (ETag, back-off) and solving
  are generic. The Claude cache stays `rates-official.json`; another source
  caches to `rates-official-<tool>.json`. Overrides stay one `rates.json` keyed
  by model prefix.
- The hook passes `transcript_path`; ingest asks each source (`Session`) whether
  a file belongs to the session that transcript names, to tag that file's rows
  `session`.
- No registration API, no configuration-driven source list, no sub-package per
  tool ([AGENTS.md](../../../AGENTS.md): no speculative frameworks).

### Ledger compatibility

The ledger stays at `~/.local/share/claude-costs/ledger.sqlite` with the same
overrides (`CLAUDE_COSTS_LEDGER`, `CLAUDE_COSTS_STATE`, `CLAUDE_COSTS_RATES`),
because transcripts expire and the ledger is the only lasting record. Schema
version 2 adds one column, `tool TEXT NOT NULL DEFAULT 'claude'`; opening a
version 1 ledger adds it in one transaction and sets the version, keeping every
row. Versions 1 and 2 are upgraded in place to version 4 in one transaction that
keeps every row (the Codex costs design, section 4, lists what version 4
adds); any other version is refused without modification. Later ingests refine
the account, subscription and tier of rows they recorded and drop duplicate fork
copies (Codex costs design, sections 3 and 4); nothing else rewrites existing
rows. Claude request IDs stay as they are; another tool's IDs are stored as `<tool>:<id>`, so the
primary key never collides and the table is not rebuilt. Any other version is
refused without modification, as section 4 says.

SQLite comes from a pure-Go driver (`modernc.org/sqlite`), so the four release
bundles still cross-compile without a C toolchain.

### Terminal width

Every table, here and in the apply checklist, fits the terminal: width from the
terminal, else `COLUMNS`, else 100 when not a terminal. The report's name
column first shortens to 24 characters. Then, while a row is too wide, the
next column drops by priority (report: `cached`, then `tokens`, then `share`,
then `calls`; checklist: privilege tag, then the saved note). Then the name or
delta column clips further, paths from the left and deltas from the right,
with `…`, down to 12 characters. Below that every row prints as a stacked
block: its leading cells (`[x]  runtimes`) on one line, then one value per
line, each clipped to the width. Headings and notes wrap at word boundaries.
No line is wider than the terminal at any width, and a wide terminal does not
spread the columns: they keep their natural widths, two spaces apart.

Rendering is the Charm stack the CLI already uses, so it behaves the same in
every terminal Workbench runs in: `charmbracelet/x/term` reads the size,
`lipgloss/v2/table` lays out and styles the cells (bold head over a faint
rule, numbers right-aligned, a bold total under a faint rule, secondary
columns faint), `charmbracelet/x/ansi` measures, clips and wraps by display
cell, so wide characters and escape codes count correctly, and `lipgloss.Fprint`
writes, choosing the terminal's color profile: plain text to a pipe or for
`TERM=dumb`, and no color (bold and faint stay) under `NO_COLOR`. The result
marks (`✓`, `✗`) follow the same profile. Workbench owns only the fit rule
above, which lipgloss does not have.

Characters follow the locale: under a UTF-8 locale (the first of `LC_ALL`,
`LC_CTYPE`, `LANG` that is set) output uses `─ … → · ✓ ✗`; otherwise it uses
`- ... -> - + x`, swapped before anything is measured so the fit rule still
holds. Interactive views (the apply checklist, the costs tabs) lay out again
on every resize.
