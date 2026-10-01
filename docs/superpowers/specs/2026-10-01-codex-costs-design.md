# Codex costs design

Status: implemented, 2026-10-01. Extends section 11 of the
[claude-costs ledger design](2026-09-30-claude-costs-ledger-design.md): the
Codex tab of `workbench costs` gets a `Source`, so Codex responses land in the
same ledger, are priced from OpenAI's live price page and appear in the same
tables as Claude Code.

## 1. Facts this design rests on

Read from the Codex open-source repository (`openai/codex`, `codex-rs/`) and
checked against the rollout files of a machine with about 3,000 sessions
(22 GB) written by Codex 0.13x–0.160.

- **Only the rollout files record per-response usage.** Codex writes one JSONL
  file per thread under `$CODEX_HOME/sessions/YYYY/MM/DD/rollout-*.jsonl`
  (`CODEX_HOME` defaults to `~/.codex`; archived threads move to the flat
  `archived_sessions/`). Codex resolves a set `CODEX_HOME` to its real
  absolute path and uses `~/.codex` as is otherwise; Workbench does the same
  for the rollouts, `auth.json` and the hooks trust keys. Its SQLite databases do not help: `state_*.sqlite`
  `threads.tokens_used` is one unsplit integer per thread with no model, and
  `thread_history_*.sqlite` is a projection of the rollout without usage. The
  Codex CLI has no usage or cost command and ships no prices
  (`models_cache.json` holds context windows and tiers only).
- **Each line** is `{timestamp, ordinal?, type, payload}`. The types that
  matter: `session_meta` (thread id, `forked_from_id`, cwd), `turn_context`
  (model, cwd; one per turn), `token_usage_record`, and `event_msg` with
  `payload.type` `token_count` or `thread_settings_applied` (a snapshot of
  the thread's settings: model, cwd, service tier).
- **`token_usage_record`** (Codex 0.153 and later) is written once per
  completed response: `thread_id`, `turn_id`, `response_id`, and `usage`, the
  response's own counts. It carries no model; the model is the latest
  `turn_context.model`.
- **`token_count`** is all an older file has: `info.total_token_usage` (running
  sum) and `info.last_token_usage` (latest response). It is re-emitted with an
  unchanged total on rate-limit updates, `info` can be null, and after
  compaction a synthetic event has zero input and output.
- **Token kinds overlap.** `input_tokens` includes `cached_input_tokens` and
  `cache_write_input_tokens`; `output_tokens` includes
  `reasoning_output_tokens`.
- **Forks copy usage.** A legacy-mode fork re-writes its parent's lines into
  the child file (a fresh `session_meta` first, then the parent's, then the
  parent's history with new timestamps), and a legacy subagent file keeps the
  parent's `token_count` lines. On the sample machine 3,335,720 `token_count`
  events that change the running total reduce to 347,339 distinct ones: summed
  naively, Codex would be counted about ten times over. Copied records keep the
  parent's `thread_id`. Paginated forks and resumes copy nothing.
- **Files are rewritten in place** by `codex migrate-rollouts` (legacy to
  paginated), with usage lines preserved but bytes and offsets changed.
- **Hooks.** Codex runs `SessionStart` and `SessionEnd` command hooks from
  `$CODEX_HOME/hooks.json` and pipes JSON with `hook_event_name` and
  `transcript_path` (the rollout path) on stdin: the same two fields
  `costs.Hook` already reads. `SessionEnd` fires for root threads only, with a
  timeout of at most 3 s. A non-managed hook runs only once Codex has recorded
  a `trusted_hash` for it under `[hooks.state]` in `config.toml`; an untrusted
  hook silently does nothing.
- **Account.** `auth.json` `tokens.id_token` is a JWT whose payload has an
  `email` claim. API-key, keyring and other modes have no email there.
- **Prices.** `https://developers.openai.com/api/docs/pricing.md` is a Markdown
  page whose "Standard pricing data" table gives, per model, short- and
  long-context input, cached input, cache writes and output per million tokens,
  followed by Batch, Flex, Fast and Ultrafast tables, each headed "<Tier>
  pricing data". Long-context prices apply above
  272K input tokens; the largest Codex request on the sample machine was 255,813.
- **Service tier.** A thread's tier is in every `thread_settings_applied`
  event (`thread_settings.service_tier`: `default`, `priority`, and so on).
  The field is left out when the thread requests no tier, and Codex then
  sends none, which is the standard tier (`protocol.rs`
  `ThreadSettingsSnapshot`, `openai_models.rs` `service_tier_for_request`;
  the same in 0.146 and 0.159). A subagent's own snapshot carries the tier it
  was spawned with, so one without a tier ran at standard. The snapshot also
  names the model and cwd, and a newer one its `thread_id`: a fork's copy of
  its parent's snapshot keeps the parent's.
  OpenAI renamed priority processing to Fast mode on 2026-07-30; the price
  page's "Fast pricing data" table prices `priority`/`fast` and its
  "Ultrafast pricing data" table prices `ultrafast`. Subagents run at their
  root thread's current tier (`core/src/agent/control/service_tier.rs`,
  `child_config.rs` `apply_spawn_agent_service_tier`) and their files carry no
  tier of their own: in one month of record-era files 6,167 of 6,501
  responses without a tier in their own file were subagents'. Of that
  month's 26,007 responses, 112 ran at `priority`; 279 rollout files set
  `priority` at some point. The rollout records the tier Codex requested,
  not the one OpenAI served; the API's served tier is not saved.

## 2. Decision

Add `codex{}` in `internal/costs/codex.go` as the second `Source` and set it on
the `codex` entry of `costs.Tools`. Ingest, the ledger, rates and reports stay
generic; the changes outside the source are the persisted per-file state, the
rewrite check, parallel parsing and the one-cache-write column, all described
below.

## 3. Reading a rollout

`Transcripts(home)` lists `*.jsonl` under `$CODEX_HOME/sessions` and
`$CODEX_HOME/archived_sessions`. `.jsonl.zst` files (a compression feature off
by default) are not read; `costs status` reports how many were skipped.

`Parse` keeps per-file state: `Thread` (the first `session_meta.id`), `Root`
(the root thread: the record's `session_id`, or
`session_meta.source.subagent.thread_spawn` up the chain), `Cwd`, `Model`,
`Tier`, `Records` (whether this file holds a record, its own or a copied one)
and `LastTotal` (the last `token_count` running total). Codex starts every
line `{"timestamp":…,["ordinal":N,]"type":…`, and an `event_msg` continues
`"payload":{"type":…`, so `Parse` reads the line's type from those first
bytes and decodes only `session_meta`, `turn_context`, `token_usage_record`
lines and `token_count` and `thread_settings_applied` events; every other
line, most of a rollout's bytes, is skipped unread past its head. A line
with any other head is decoded if it contains one of those five names.

- `session_meta`: the first one sets `Thread` and `Cwd`; later ones are copies
  from a parent and are ignored.
- `turn_context`: sets `Model` and `Cwd`.
- `thread_settings_applied`: one that names another thread is a fork's copy
  and is skipped. Otherwise it sets `Tier` from `service_tier` (standard when
  it is left out), records a tier change `(Thread, timestamp, tier)`, and
  sets `Model` and `Cwd` while no `turn_context` has: Codex applies a changed
  model to the turns that start after the snapshot (`ThreadSettingsOverrides`
  "change the settings inherited by future turns"), and each turn's
  `turn_context` names it, so a model switched mid-turn must not relabel the
  rest of the running turn.
- `token_usage_record` whose `thread_id` is `Thread`: one row keyed
  `response_id`. A record with another `thread_id` is a copy and is skipped
  (its original is ingested from the parent's file). Any record in the file,
  its own or a copied one, sets `Records`, so a fork's copied `token_count`
  lines beside a copied record are not counted again.
- `token_count` while `Records` is false (no record of any kind in the file
  yet), `info` is non-null, the running total differs from `LastTotal`, and
  `last_token_usage` has non-zero input or output: one row from
  `last_token_usage`, keyed `tc:` plus the SHA-256 of the canonical JSON
  (exactly what Go's `encoding/json` writes for the value decoded with
  `UseNumber`: sorted keys, numbers as written) of `total_token_usage`,
  `last_token_usage` and `rate_limits`. These bytes are part of every stored
  key and never change. The key holds no timestamp or file, so
  a fork's copy has its original's key and the ledger's upsert keeps one row;
  `rate_limits` separates unrelated threads that happen to reach the same
  totals. On the sample machine this key leaves 347,339 rows; keying on the
  totals alone leaves 347,329.

Each row: `Model` = `Model` (or `unknown`), `Project` = `Cwd`, `Session` =
`Thread`, `Time` = the line's timestamp, `Tier` = `Tier`, or for a file that
has no tier of its own, `inherit` with the root thread id. Token mapping, so
that the report's prompt total (input + writes + reads) is right:

| Ledger column | Codex value |
| --- | --- |
| `input` | `input_tokens − cached_input_tokens − cache_write_input_tokens`, at least 0 |
| `cache_read` | `cached_input_tokens` |
| `cache_write_5m` | `cache_write_input_tokens` |
| `cache_write_1h` | 0 |
| `output` | `output_tokens` (reasoning is already inside it) |

`Session(file, transcript)` is `file == transcript`: a Codex `SessionEnd` names
the root thread's rollout, so subagent files ingested in the same run are
tagged `sweep`. `Account` is the `email` claim of `auth.json` (payload decoded,
never verified, access and refresh tokens never read), else `unknown`.
`Observations` is nil: Codex records no cost to calibrate against.

## 4. Ledger and ingest changes

- **Per-file state.** `files` gains `state TEXT NOT NULL DEFAULT ''` and
  `head TEXT NOT NULL DEFAULT ''`; schema version 3. The existing upgrade path
  adds both columns in one transaction and keeps every row, as it did for
  version 1 to 2; its data-preservation test grows to cover version 2 to 3.
  `FileState` gains an opaque `Saved []byte` that a source fills and ingest
  stores with the offset; Codex saves its fields as JSON and Claude saves
  nothing.
- **Tier.** A response's tier rides in its model id: `gpt-5.6-sol@fast`, no
  suffix for standard. Reports, the focus page and the rate lookup work on
  that id unchanged, apart from the lookup rule in section 5 and the label in
  section 7. The ledger gains `tier_changes (thread_id, ts, tier)`, written in
  the same transaction as the file's rows, and `tier_pending (request_id,
  root)`. `Usage` gains `TierFrom` (the root thread, for a row whose own file
  names no tier) and `FileState` collects the file's tier changes. A thread
  has one tier at a time: a change at the same millisecond as a stored one
  replaces it, so of several snapshots in one millisecond the last in the file
  wins. After every ingest, each pending row takes the root's latest tier
  change at or before its time and stops waiting; one whose root has no change
  yet stays pending and is priced at the standard tier meanwhile. So a
  subagent is priced at the tier its root had when it ran, whatever order the
  files are read in. A pending row's answer changes only when the row is
  written again or its root gains a change, so the check after a run covers
  only the rows that run put in `tier_pending` and those whose root it wrote a
  change for. A transaction that writes either stores the `tiers_unresolved`
  note and the check clears it; a run that finds it set (an earlier run
  stopped before its check) checks every pending row.
- **Attribution.** On a repeated key the token columns keep their largest
  values, as before, and the time, model, project and session come from the
  earliest occurrence (on a tie, the row read last, as before): a fork's copy,
  stamped with the fork's time and thread, never moves its original.
- **Rewrite check.** `head` is the SHA-256 of the file's first line, `""`
  while that line is still being written. When it differs from a stored
  non-empty one, ingest reads the file from offset 0 with empty state; a
  rewrite caught mid-first-line so stores offset 0 and is read whole once that
  line is complete. Every key is derived from content, so a full re-read only rewrites
  the same rows.
- **Speed.** Files are parsed concurrently (`GOMAXPROCS` workers), up to
  1 GiB of transcript ahead of the commits, and committed one transaction per
  file in list order, so a crash still leaves every committed file consistent.
  A cancelled run (Ctrl-C) stops each read within about 1 MiB and exits at
  once; what it committed stays committed.
  The ledger runs WAL with `synchronous=NORMAL`: a crash can lose only the
  last commits, each with its file's offset, so the next run reads them again.
  Each transaction prepares its statements once, and a run remembers what it
  committed so a fork's copy that would change nothing never reaches SQLite.
  The first full read is then bounded by the single SQLite writer storing each
  distinct response once, not by reading: on the sample machine (2,969 files,
  23 GB, 16 cores, warm page cache) it takes 18 s, of which reading is about
  45 CPU-seconds spread over the cores and the writer about 15 s of the wall
  time; `rg` scans the same files in 4.5 s. Later runs read only appended
  bytes; a run with nothing new takes 0.3 s.

## 5. Rates

- `PricingURL` is the page above; `CODEX_COSTS_PRICING_URL` replaces it for
  checks that must not reach the network. Refresh policy, overrides and the
  `official`/`builtin`/`override` labels are unchanged.
- `ParsePricing` reads the table under the "Standard pricing data" heading
  and every other "<Tier> pricing data" table (one word) except Batch, which
  Codex does not use. The model cell's note in parentheses is dropped
  (`gpt-5.5 (<272K context length)` is `gpt-5.5`). Columns: short-context
  input, cached input, cache writes, output. A `-` cache-write price bills
  writes at the input price, and a `-` cached-input price bills reads at the
  input price. Long-context columns are not read; section 1 shows no Codex
  request reaching 272K. A tier table is read the same way into
  `<model>@<tier>` rows: today `@flex`, `@fast` and `@ultrafast`, and a
  "Priority pricing data" table would give `@fast`. A tier in a model id is
  the word after its last `@` (a lowercase letter, then lowercase letters,
  digits, `_` or `-`), so a tier OpenAI adds later needs no code change, and
  `@` followed by a digit stays a dated snapshot. A rate prices a model only
  at the same tier, and only when its key is the whole model id or is followed by `-` or `@` and a
  digit, so `gpt-5` prices a dated `gpt-5-2025-08-07` but neither
  `gpt-5-mini` nor `gpt-5.3-codex-spark`; a `priority` or `fast` row is priced from
  `@fast`, any other tier from its own table (`ultrafast` from
  `@ultrafast`, `flex` from `@flex`), and `default`, `auto` or `''` from the
  standard table. A tier row with no tier price on either card, or a tier
  name that is not such a word, is reported unpriced rather than priced at
  standard. A root's tier, whatever its name, carries over to its
  subagents' rows.
- `Builtin` is the standard table read on 2026-10-01, longest prefix wins, used
  until the first refresh and when the page cannot be reached. A model on
  neither card (such as `gpt-5.3-codex-spark` today) is reported unpriced, as
  for Claude.

## 6. Hooks

`home/private_dot_codex/private_hooks.json.tmpl` renders for every role: the
`SessionStart` and `SessionEnd` entries run `~/.local/bin/workbench costs
ingest` (`SessionEnd` with `"timeout": 3`, its maximum), and the existing
`UserPromptSubmit` entry stays inside `{{ if .has_work }}`. `render-check.sh`
drops its "hooks.json deployed on personal" leak check; its personal-role
`promptctl` grep still catches the real leak. `.codex/hooks.json` is no longer
listed in `home/.chezmoidata/role-targets.json`, so every role receives it.

**Trust is written by Workbench, so Codex never asks.** Codex keys trust as
`[hooks.state."<hooks.json path>:<event>:<group>:<handler>"]` with
`trusted_hash = "sha256:<hex>"`, where the hex is the SHA-256 of the compact,
key-sorted JSON of the hook's normalized identity (`codex-rs/hooks/src/engine/
discovery.rs` `hook_hash`, `codex-rs/config/src/fingerprint.rs`
`version_for_toml`):
`{"event_name": "<snake_case event>", "matcher": <only when the event uses
one>, "hooks": [{"type": "command", "command": ..., "timeout": <normalized:
600 by default, SessionEnd 1 by default and at most 3>, "async": <bool>,
"statusMessage": <only when set>}]}`. This was checked against the hash Codex
itself recorded when the existing `UserPromptSubmit` hook was approved: it
matches exactly. `modify_private_config.toml.tmpl` includes the rendered
hooks.json, computes the hash of every handler in it and writes `enabled =
true` and `trusted_hash` for each key as managed values, so whatever hooks
Workbench installs are trusted on the same apply; an entry left for a hook
Workbench removed trusts nothing, since that hook no longer exists.
`Hooks(home)` reports a state per event, as Codex decides whether to run a
hook (`discovery.rs`: enabled unless `enabled = false`, and trusted when the
recorded hash matches): `ok` when `hooks.json` has the command and its entry
records the hash computed the same way, `disabled` when the entry sets
`enabled = false`, `untrusted` when it records no hash or another one (as
after Codex changed its normalization), and `missing` without the command.
Claude Code's `Hooks` reports `ok` or `missing`. `costs status` prints the
state words, adding `(workbench apply)` once to a hooks line with a `missing`
or `untrusted` event, and `costs status --json` gives each event's state as a
string.

Claude Code's hooks already run an ingest that sweeps every tool, so Codex
hooks only matter while Codex is used without Claude Code; `workbench costs`
and `costs ingest` catch up in any case.

## 7. Report

The model table shows a non-standard tier as part of the name
(`gpt-5.6-sol (fast)` beside `gpt-5.6-sol`), so the faster turns and their
cost are visible on their own. The Codex tab shows one `cache write` column
instead of `cache 5m` and `cache 1h` (`Tool` gains the cache-write column
labels), and its footer reads
`OpenAI API list-price equivalent, not a ChatGPT plan bill`.
Everything else (tabs, drill-down, `--tool codex`, JSON and CSV output) works
unchanged once the source is set.

## 8. Open decisions for the owner

1. **Hook trust.** Decided 2026-10-01: Workbench writes the trust (section 6).
2. **Service tier.** Decided 2026-10-01: each response is priced at the tier
   it ran at, subagents at their root's tier (sections 3 to 5).

## 9. Verification

- `go build ./...`, the golangci-lint gates and `go test ./...` (the upgrade
  test is the only new test: it guards existing ledger rows).
- A reference count from the rollout files, by a script outside the repository,
  matched against `workbench costs --tool codex --json` totals per model for
  one month of record-era files and one month of legacy files.
- Fork check: re-ingesting from an empty ledger gives the same row count as an
  incremental ingest, and the legacy row count equals the distinct-key count.
- Rewrite check: a copy of a rollout re-serialized line by line (as
  `migrate-rollouts` would) adds no rows.
- Timing of the first full ingest on this machine, reported with the result.
- `render-check.sh` for every role and mode, including `wsl`.

## 10. Out of scope

`.jsonl.zst` rollouts; long-context pricing; Batch pricing (Codex does not
use the Batch API); `token_count.rate_limits.credits` (an account balance, not
a per-response cost); usage from Codex Cloud tasks, which run remotely and
leave no local rollout.
