# Codex costs design

Status: implemented, 2026-10-01. Extends section 11 of the
[claude-costs ledger design](2026-09-30-claude-costs-ledger-design.md): the
Codex tab of `workbench costs` gets a `Source`, so Codex responses land in the
same ledger, are priced from OpenAI's live price page and appear in the same
tables as Claude Code.

## 1. Facts this design rests on

Read from the Codex open-source repository (`openai/codex`, `codex-rs/`) and
checked against a large real history of rollout files written by Codex
0.13x–0.160.

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
  `turn_context.model`. From 0.153 every update of the token counts with a
  response's usage follows its record (`record_observed_response_completed`
  before the update in `session/turn.rs` and `compact.rs`; checked in 0.153,
  0.154, 0.156 and 0.159), so a file such a Codex started counts its responses
  by record alone. A subagent fork copies its parent's `token_count` lines but
  not its records (`agent/control/spawn.rs` `keep_forked_rollout_item`:
  `TokenUsageRecord => false`, `EventMsg => true`).
- **`token_count`** is all an older file has: `info.total_token_usage` (running
  sum) and `info.last_token_usage` (latest response). It is re-emitted with an
  unchanged total on rate-limit updates, `info` can be null, and after
  compaction a synthetic event has zero input and output.
- **Token kinds overlap.** `input_tokens` includes `cached_input_tokens` and
  `cache_write_input_tokens`; `output_tokens` includes
  `reasoning_output_tokens`.
- **Forks copy usage.** A fork that copies its history (`ForkPersistence::
  Copied`) re-writes its parent's lines into the child file and appends its
  own `thread_settings_applied` after them; one that references the parent's
  history, or a paginated subagent, writes its own `thread_settings_applied`
  first and copies nothing, but starts its token counts from the parent's
  total (`session/mod.rs` `InitialHistory::Forked`, 0.146 and later; earlier
  forks always copy). A legacy-mode fork re-writes its parent's lines into
  the child file (a fresh `session_meta` first, then the parent's, then the
  parent's history with new timestamps), and a legacy subagent file keeps the
  parent's `token_count` lines. In a long history the `token_count` events that
  change the running total reduce to about a tenth as many distinct ones:
  summed naively, Codex would be counted about ten times over. Copied records keep the
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
  272K input tokens.
- **Service tier.** A thread's tier is in every `thread_settings_applied`
  event (`thread_settings.service_tier`: `default`, `priority`, and so on).
  The field is left out when the thread requests no tier, and Codex then
  sends none, which is the standard tier (`protocol.rs`
  `ThreadSettingsSnapshot`, `openai_models.rs` `service_tier_for_request`;
  the same in 0.146 and 0.159). The snapshot also names the model and cwd,
  and a newer one its `thread_id`: a fork's copy of its parent's snapshot
  keeps the parent's.
- **When a tier applies.** A snapshot holds the settings for the turns that
  start after it: "standalone updates change the settings inherited by future
  turns", and "the running turn keeps its own" (`protocol.rs`
  `ThreadSettingsOverrides`, `core/src/session/mod.rs`). A turn starts with a
  `turn_context` naming a new `turn_id` (a compaction re-emits the running
  turn's), after the snapshot that applies to it. A subagent's requests are
  sent at another tier: in Codex 0.152 and later every
  request of a spawned subagent takes its root's selected tier at that moment,
  which a root's snapshot updates at once (`capture_step_context_inner`
  replaces the step's tier with `root_service_tier`; `set_root_service_tier`
  runs when the root's settings change), whatever the subagent's own snapshot
  says (openai/codex dc2ccc6843 "Make subagents follow the root service
  tier", in 0.152.0 and not in 0.151.0). Before 0.152 a subagent's tier was
  fixed when it was spawned: its
  parent's running-turn tier (`multi_agents` `apply_spawn_agent_service_tier`
  with `turn.config.service_tier`); its own snapshot, when it has one,
  carries that tier. Codex then sends a tier only to a model whose catalog
  entry lists it, flex excepted, and only with the `fast_mode` feature on
  (stable, on by default; `session/mod.rs` `get_service_tier`,
  `openai_models.rs` `service_tier_for_request`); it keeps the catalog it
  last fetched in `$CODEX_HOME/models_cache.json`. Two app-server requests set
  a tier that no rollout line records: `turn/settings/update` changes only the
  running turn, and `turn/start`'s `serviceTierForTurn`
  (`TurnStartParams.service_tier_for_turn`, applied to that turn's copy of
  the settings after they are persisted: `session/turn_context.rs`
  `new_turn_with_sub_id_if`) only the turn it starts. The TUI and `codex exec`
  send neither (the TUI changes the thread's settings, `codex exec` passes
  `service_tier_for_turn: None`), and no client observed sends them.
  OpenAI renamed priority processing to Fast mode on 2026-07-30; the price
  page's "Fast pricing data" table prices `priority`/`fast` and its
  "Ultrafast pricing data" table prices `ultrafast`. In record-era files nearly
  every response without a tier in its own file was a subagent's, and only a
  small share of responses ran at `priority`. The rollout records the tier Codex requested,
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
(the root thread: the record's `session_id`, or for an older subagent whose
file names no more, its spawning parent from
`session_meta.source.subagent.thread_spawn`; the ledger walks the stored
parent links up to the true root, see section 4), `Cwd`, `Model`,
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
- `turn_context`: sets `Model` and `Cwd`. One with a `turn_id` other than the
  running turn's (or none) starts a turn: a tier a snapshot selected becomes
  `Tier`, and a change of the thread's running-turn tier is recorded at this
  time.
- `thread_settings_applied`: one that names another thread is a fork's copy
  and is skipped. Otherwise it selects a tier for the next turn from
  `service_tier` (standard when it is left out), records a change of the
  thread's selected tier at its own time, and sets `Model` and `Cwd` while no
  `turn_context` has: a model switched mid-turn, like a tier, applies from the
  next turn, which names it.
- `token_usage_record` whose `thread_id` is `Thread`: one row keyed
  `response_id`. A record with another `thread_id` is a copy and is skipped
  (its original is ingested from the parent's file). Any record in the file,
  its own or a copied one, sets `Records`, so a fork's copied `token_count`
  lines beside a copied record are not counted again.
- `token_count` while `Records` is false (no record of any kind in the file
  yet) and the file's `session_meta` names a Codex before 0.153, `info` is
  non-null, the running total differs from `LastTotal`, and
  `last_token_usage` has non-zero input or output: one row from
  `last_token_usage`, keyed `tc:` plus the SHA-256 of the canonical JSON
  (exactly what Go's `encoding/json` writes for the value decoded with
  `UseNumber`: sorted keys, numbers as written) of `total_token_usage`,
  `last_token_usage` and `rate_limits`. These bytes are part of every stored
  key and never change. The key holds no timestamp or file, so
  a fork's copy has its original's key and the ledger's upsert keeps one row;
  `rate_limits` separates unrelated threads that happen to reach the same
  totals. In a fork that copied its history (the line after its own
  `session_meta` is not its own `thread_settings_applied`), the first
  `token_count` whose total is more than its `last_token_usage` is a copy of
  its parent's history. It may copy an event the parent re-emitted for a
  rate-limit update (same total, other `rate_limits`), which the parent
  skipped and which matches none of the parent's keys. Its row is stored
  apart, keyed `<tool>:copy:<parent>@<fork start>:` and its key, and dropped
  at the end of a run once the parent's rollout holds that response: it is
  listed in that run, or the ledger read it after it was last written at or
  after the fork's start. Until then the copy is the only trace of the
  response and counts, and the result does not depend on which file is read
  first. A fork
  that copied nothing starts its counts from the parent's total, and its first
  `token_count` is its own response. On a real history the fork rule removed
  only a handful of rows, and each was exactly such a copy, a second row with
  no model for a response the parent's file records: for each, the parent's
  file holds an earlier event with the same total and last usage but other
  `rate_limits`, whose row the ledger holds with the parent's model, and a
  later re-emission with the copy's `rate_limits`, which has no row.
  Keying on the totals alone would also merge pairs of distinct
  responses: the first responses of two sibling subagents started with the
  same prompt, with equal counts minutes apart. Every fork file seen whose first
  `token_count` is a copy has that response recorded in another file.

Each row: `Model` = `Model` (or `unknown`), `Project` = `Cwd`, `Session` =
`Thread`, `Time` = the line's timestamp. Its tier, as section 1 says Codex
selected it: a spawned subagent's row whose `session_meta` names Codex 0.152
or later inherits its root's selected tier at the row's time; any other row
whose turn started at a named tier has `Tier`; any other subagent row
inherits its parent's running-turn tier at the subagent's `session_meta`
time. Token mapping, so
that the report's prompt total (input + writes + reads) is right:

| Ledger column | Codex value |
| --- | --- |
| `input` | `input_tokens − cached_input_tokens − cache_write_input_tokens`, at least 0 |
| `cache_read` | `cached_input_tokens` |
| `cache_write_5m` | `cache_write_input_tokens` |
| `cache_write_1h` | 0 |
| `output` | `output_tokens` (reasoning is already inside it) |

A source reports its current sign-in as `SignIn(home)`: the `email` claim of
`auth.json` (payload decoded, never verified, access and refresh tokens never
read), the subscription from the same token's plan, else `unknown`. Each row
is stored with an email, a subscription and the evidence that decided them,
as section 10 describes; there is no per-run `session` or `sweep` tag.
`Observations` is nil: Codex records no cost to calibrate against.

## 4. Ledger and ingest changes

- **Per-file state.** `files` gains `state TEXT NOT NULL DEFAULT ''` and
  `head TEXT NOT NULL DEFAULT ''`; schema version 4 (the per-file state, the
  tier tables and the subscription columns and tables of section 10 are all
  unreleased, so one version holds them). The existing upgrade path adds the
  columns and tables in one transaction and keeps every row, as it did for
  version 1 to 2; its data-preservation test covers versions 1 and 2 to 4. A
  ledger at any other version, including a development build's version 3,
  is refused with its path and version, and nothing is written.
  `FileState` gains an opaque `Saved []byte` that a source fills and ingest
  stores with the offset; Codex saves its fields as JSON and Claude saves
  nothing.
- **Tier.** A response's tier rides in its model id: `gpt-5.6-sol@fast`, no
  suffix for standard. Reports, the focus page and the rate lookup work on
  that id unchanged, apart from the lookup rule in section 5 and the label in
  section 7. The ledger gains `tier_changes (thread_id, ts, tier)`, written in
  the same transaction as the file's rows, and `tier_pending (request_id,
  root)`. Each thread has two series of changes there: its selected tier,
  keyed by the thread id and dated at the snapshot, and its running-turn
  tier, keyed `turn:<thread>` and dated at the turn start. A subagent that
  keeps the tier it started with records, at its start, a change of its
  running-turn series to `=` and its parent's series and start time, so its
  own subagents reach its parent's tier through it even when it has no rows
  of its own. `Usage` gains
  `TierFrom` for a row another thread prices: a thread id (that thread's
  selected tier at the row's time), or `turn:<thread>@<time>` (that thread's
  running-turn tier at that time), stored as `tier_pending.root`; and
  `FileState` collects the file's tier changes. A series has one tier at a
  time: a change at the same millisecond as a stored one replaces it, so of
  several snapshots in one millisecond the last in the file wins. After every
  ingest, each row in `tier_pending` takes its series' latest change at or
  before its time, following `=` changes, and then the tier Codex sent:
  none when the model's entry in `models_cache.json` does not list the tier
  (flex excepted; a model the cache does not list keeps the selected tier).
  A row whose series has no change by then is priced at the standard tier.
  Rows stay in `tier_pending`, so a change read later, even one earlier than
  rows already priced (a file still being written), prices them again. So a
  subagent is priced at the tier it ran at, whatever order the files are read
  in. A row's answer changes only when the row is written again or a series
  it reads, directly or through `=` changes, gains a change, so the check
  after a run covers only the rows that run put in `tier_pending` and those
  reading a series it wrote a change to. A row Workbench prices from its own
  file applies the same catalog rule when it is stored, and so does a
  `fast_mode = false` in `config.toml`, which leaves only flex.
  A transaction that writes either stores the `tiers_unresolved`
  note and the check clears it and stores `tiers_checked`. A run checks every
  pending row when it finds `tiers_unresolved` set (an earlier run stopped
  before its check) or `tiers_checked` absent (a new or upgraded ledger, or
  one an earlier build wrote, whose pending rows no check of this build has
  seen).
- **Attribution.** On a repeated key the token columns keep their largest
  values, as before, and the time, model, project and session come from the
  earliest occurrence (on a tie, the row read last, as before): a fork's copy,
  stamped with the fork's time and thread, never moves its original. The
  account, subscription and `account_source` (the evidence) are the exception:
  a copy with stronger evidence replaces them, an equal or weaker one keeps
  the stored values (section 10).
- **Accounts.** `responses` also gains `subscription`, `root` (the root
  session a hook names) and two evidence columns: `account_source` for the
  email and `subscription_source` for the subscription, each `transcript`,
  `session`, `observed` or `unknown` and decided on its own (section 10);
  schema 4 adds the tables `session_accounts`, `sign_ins`, `subscriptions`
  and `thread_parents`. A version 1 or 2 ledger keeps every row's email, now
  at evidence `unknown`, with subscription and its evidence `unknown`. A row
  is stored with the email of its transcript's account id when there is one
  and otherwise `unknown`, never with the tool's current sign-in; after every
  run `ResolveAccounts` raises each field to the strongest evidence, by the
  same targeted pass as the tier check: the rows the run stored (marked under
  the root they are stored with, which a later copy with stronger evidence
  does not change), those of sessions a binding or the run named, and those
  after a tool's latest observation. A transaction that stores a row marks
  its root in the `accounts_unresolved` note and `ResolveAccounts` clears it,
  so a run cancelled before it leaves the work to the next run; a missing
  `accounts_checked` forces every row.
- **Thread parents.** An older Codex subagent's `session_meta` names only the
  thread that spawned it, so the rows of a depth-2 subagent arrive under its
  parent, not the root. Ingest stores each thread's spawning parent in
  `thread_parents`, and `ResolveAccounts` first moves every row stored under
  a thread that has a parent to the root of that chain, whatever order the
  files were read in, and attributes the roots that gained rows again. Codex
  0.142 and later name the root in `session_id`, so
  their rows already carry it.
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
  once with the interrupted status, also when the signal arrives during the
  tier check, and skips the rates refresh; what it committed stays committed,
  and `tiers_unresolved` makes the next run check every row.
  The ledger runs WAL with `synchronous=NORMAL`: a crash can lose only the
  last commits, each with its file's offset, so the next run reads them again.
  Each transaction prepares its statements once, and a run remembers what it
  committed so a fork's copy that would change nothing never reaches SQLite.
  The first full read is then bounded by the single SQLite writer storing each
  distinct response once, not by reading: for tens of gigabytes of rollouts in
  a warm page cache, reading is spread over the cores and the writer takes
  most of the wall time, a few times what `rg` needs to scan the same files.
  Later runs read only appended bytes; a run with nothing new takes well
  under a second.

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
  `@` followed by a digit stays a dated snapshot. Only a tool whose source
  composes such ids (`Tool.Tiers`: Codex, and Claude Code for fast mode) has its ids split: every other
  tool's model id, such as a vendor's `foo@latest`, is read whole by the
  rate lookup, the report and the unpriced note. A rate prices a model only
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
  for Claude. The report's warning and the ingest summary name each unpriced
  model with why (not on the tool's price page, no price at its tier, or no
  model named in the transcript) and the rates overrides file a rate for it
  goes in; the report's JSON `unpriced` lists `{model, reason}`.

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
hooks.json, computes the hash of every handler in it and writes
`trusted_hash` for each key as a managed value, so whatever hooks Workbench
installs are trusted on the same apply. The entry's `enabled` key stays the
user's: a hook turned off in Codex (`enabled = false`) stays off across
applies. The entries of a hook Workbench no longer installs are dropped, so
they do not stay trusted. A second apply changes nothing.
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

Claude Code's hooks already run an ingest that reads every tool, so Codex
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

- `go build ./...`, the golangci-lint gates and `go test ./...` (see the ledger design, section 9, for the tests).
- A reference count from the rollout files, by a script outside the repository,
  matched against `workbench costs --tool codex --json` totals per model for
  one month of record-era files and one month of legacy files.
- Fork check: re-ingesting from an empty ledger gives the same row count as an
  incremental ingest, and the legacy row count equals the distinct-key count.
- Rewrite check: a copy of a rollout re-serialized line by line (as
  `migrate-rollouts` would) adds no rows.
- Timing of a first full ingest of a large history, reported with the result.
- `render-check.sh` for every role and mode, including `wsl`.

## 10. Subscriptions

One sign-in email can pay through several subscriptions: a Claude Code login
can switch between a personal Max plan and a team organization, and a ChatGPT
login between Plus, Pro and a workspace. The report keeps them apart: every
row records the subscription that paid for it, and `--by account` groups by
email and subscription, for example `you@example.com · Max` and
`you@example.com · Team (Example Org)`.

### What each tool records

- **Claude Code.** An assistant record carries only the model, usage,
  `service_tier` and request ids. From 2.1.282 (the earliest version seen
  writing it; 2.1.280 wrote none) a
  transcript also holds `attachment` records of type
  `credential_org` with an `organizationUuid`: the organization the session's
  credential belongs to from that line on. Claude Code writes one when a
  session starts or resumes, at intervals, and after a `/login` switches
  organization; a subagent's own file may hold none. Earlier versions write
  none, so their history names no organization anywhere on disk. The current
  sign-in is `~/.claude.json` `oauthAccount`: `emailAddress`,
  `organizationUuid`, `organizationName`, `organizationType` (`claude_max` and
  `claude_team` observed; other values are shown as written). Claude Code
  copies `.claude.json` to `backups/.claude.json.backup.<milliseconds>` before
  it changes it, so an organization signed out of keeps its email and type
  there. The
  `SessionStart` hook receives `session_id` on stdin, and a
  subagent's records (`<session>/subagents/agent-*.jsonl`) carry its parent's
  `sessionId`, so one binding covers the session and its subagents.
- **Codex.** Every `token_count` carries `rate_limits.plan_type` (`plus`,
  `pro` and `prolite` observed): the plan of the account that answered. A
  `token_usage_record` (0.153+) has no `rate_limits`; its plan is the latest
  `plan_type` read in the same file at or before it, kept in the file's saved
  state. A `token_usage_record` names its root `session_id` (Codex 0.142 and
  later); an older subagent's file names only the
  thread that spawned it, and the ledger walks the spawning links of its
  ancestors' files up to the root (section 4). Recent `session_meta` lines carry
  `creator_account_id`, the ChatGPT account (workspace) id that also appears
  as the `chatgpt_account_id` claim of each sign-in's ID token. The sign-ins
  are `auth.json` and, when present, `agent-overflow-accounts/*/auth.json`
  under `CODEX_HOME`; only their ID token payloads are decoded.

### Attribution, most exact first

Each row stores `account` (the email) with `account_source`, and
`subscription` (a stable id) with `subscription_source`: two fields, each
with the evidence it came from, decided on their own because a transcript can
name a plan without naming an account. A copy of a row with stronger evidence
for a field replaces that field; equal evidence keeps the stored value. A row
shows an email only with evidence for it: its transcript's account id, a
session binding, or agreeing observations (or, for a row a version 1 or 2
ledger held, the email that build stored). A value that is `unknown` is no
evidence and never raises a field.

1. `transcript`: Codex: the plan from the response's file as above is the
   subscription's evidence, and the sign-in whose `chatgpt_account_id` equals
   the file's `creator_account_id` is the email's. Claude Code: the latest
   `credential_org` read in the response's file, kept in the file's saved
   state, is the subscription (`claude:<organizationUuid>`), and the email
   signed in to that organization in `.claude.json` or one of its backups is
   the email's; an organization two emails were signed in to names no email.
   A row before its file's first `credential_org` names neither. Without these
   ids the email (and for Claude Code the subscription) takes rule 2 or 3 on
   its own, so a row whose transcript named only the plan still takes the
   email of a binding or an observation, and nothing but a transcript changes
   what a transcript decided. A row whose own file names no subscription
   takes one its session's transcript names, as transcript evidence, with the
   email that row's transcript named: a Codex row the first plan its own
   thread names at or after it (a thread's first `token_count` lines can have
   `rate_limits: null`), a Claude Code row the latest organization its root
   session names at or before it (a subagent runs in its parent's process).
   Neither crosses a session binding between the two rows, which can be
   another process under another sign-in. When a parser learns to read more
   from lines already read (`ParseRevision`), the next run forgets the tool's
   file offsets and reads its transcripts again from the start, once, in the
   same transaction that stores the revision; the re-read rewrites the same
   rows with the stronger evidence.
2. `session`: the sign-in a session started under. The hook passes the
   worker its `session_id` and transcript path (which names the tool). The
   detached worker reads the tool's current sign-in and stores
   `session_accounts(tool, session, since, account, subscription)` in one
   short transaction before it contends for the ingest lock, so a worker that
   skips because another holds the lock loses no binding, and the hook process
   itself stays instant within Codex's 3 s `SessionEnd` cap; a worker that
   cannot store it logs that and the row falls to rule 3. Rows
   match on the root session: Claude's `sessionId`, Codex's root thread
   (an older subagent's rows are moved to it first, section 4). A row of
   that session takes the binding with the latest `since` at or before its
   time, so a session resumed later under another subscription changes from
   the resume on. A binding stored after rows of its session were read
   re-attributes those rows (the same targeted pass as tier resolution).
3. `observed`: every ingest run records the current sign-in per tool in
   `sign_ins(tool, at, account, subscription)`. A row with no binding takes
   the sign-in observed at both ends of the gap around its time when the two
   agree, else `unknown`.
4. `unknown`: no evidence. The row shows email `unknown` and subscription
   `unknown`, for both tools: a Codex row with no plan and a Claude Code row
   from before the first observation read alike, and the tool's current
   sign-in is never stamped on a row it did not name. A first ingest of
   existing history therefore reports it as `unknown`, and only rows after
   the first observation, or of a bound session, take an email. The one
   exception is a row an earlier build stored: a version 1 or 2 ledger's row
   keeps its email, at evidence `unknown`, until evidence names another, and
   its subscription is `unknown`.

Subscription ids and labels: Claude `claude:<organizationUuid>`, label from
`organizationType` with the `claude_` prefix removed and the first letter
capitalised (`claude_max` → Max, `claude_pro` → Pro, `claude_team` → Team),
plus ` (organizationName)` whenever the type is not a personal plan (`claude_max`
or `claude_pro`), for example `Team (Example Org)`. Codex `codex:<plan_type>`,
label from `plan_type` (`prolite` → Pro Lite, otherwise the capitalized value);
the account id only resolves the email, so one plan is one group whichever
evidence named it. A Codex API-key sign-in is `api-key`. A Claude Code
API key leaves no `oauthAccount`, so Claude Code with none shows
`not signed in` in `costs status` and its email and subscription are
`unknown`, not `api-key`.
Labels live in a `subscriptions(id, label)` table, refreshed from the newest
sign-in that names the id, so a renamed organization relabels its history.

### Limits

- A Claude Code `/login` to another organization inside a running session:
  on a version that writes `credential_org` the next one names the new organization
  and its rows follow it. On earlier versions its rows keep the subscription
  of the session's binding, because a session binding outranks an
  observation, until a later `SessionStart` of that session (a resume,
  `/clear` or compaction) binds the new sign-in.
- Whether a running Claude Code session follows a `/login` made in another
  terminal is not stated by the official docs, which say only that parallel
  sessions on one machine "share a saved login and coordinate its renewal so
  that only one process refreshes the token at a time" (Troubleshoot
  installation and login, "Not logged in or token expired",
  <https://code.claude.com/docs/en/troubleshoot-install>); the
  Authentication page says only that running `/login` with
  `CLAUDE_CODE_OAUTH_TOKEN` set "switches the current session to the new
  login" (<https://code.claude.com/docs/en/authentication>, "Authentication
  precedence"). Upstream reports the opposite for a long-running session: its
  in-memory token is not re-read after a `/login` elsewhere
  (`anthropics/claude-code` issue 95262, v2.1.268). Workbench therefore
  assumes a running session keeps the login it started or resumed under, which
  is what rule 2 records; it does not claim the docs confirm it. A session
  started or resumed after the `/login` is bound to the new sign-in.
- A `SessionEnd` binding attributes nothing: it is stored at the exit time,
  after every row of its session, and a session that had a `SessionStart`
  binding keeps that one for all its rows. The final rows of a session that
  had none (it began before the hooks were installed) are attributed by the
  run's observation, rule 3.
- An organization's email comes from `.claude.json` and its backups, which
  Claude Code rotates (a few days deep). A stored row keeps the email it was
  given; only a later `ParseRevision` re-read after the backup is gone would
  leave that organization's rows to rules 2 and 3 for the email.
- Claude Code history written by a version that writes no `credential_org`
  (most of a history from before 2.1.282), or before its file's first
  `credential_org`, names no organization; ingested before the first
  observation it keeps subscription `unknown`. So does every Codex row before
  the first observation whose file names no creator account. Nothing on disk
  records either: the transcripts, `.claude.json` backups (a few days deep),
  Codex's state and log databases and its auth files were all searched.
- Codex rows of a thread that never names a plan (rare) keep subscription
  `unknown`. A row whose file names no `creator_account_id` (Codex before
  0.160, most of an older history) has a plan and no email until a binding or
  an observation names it.

### Report and status

- `--by account` groups by email and subscription; the per-account section
  under other groupings does too. JSON rows gain `subscription` and
  `subscription_label`. A row names what its tool did not record:
  `you@example.com · plan not recorded`, `account not recorded · Pro`, or
  `not recorded`. Under the table a note says since when the tool records
  each missing part (the first row whose transcript named it), or, when it
  did so from the first row, how many responses come from conversations that
  never named it. The JSON `coverage` has `account_named_since` and
  `subscription_named_since`.
- Only a model row carries the `unpriced` flag; a project, account or total
  that includes an unpriced model counts its cost as 0 and the warning under
  the report names the model.
- `costs status` shows each tool's current sign-in, for example
  `Claude Code  you@example.com · Max`, and, on separate lines, how many
  rows each evidence level gave their email and their subscription; the
  JSON `coverage` has `account_evidence` and `subscription_evidence`, each
  counting `transcript`, `session`, `observed` and `unknown` rows.

### Hooks this depends on

- `SessionStart` must pass `session_id` and the transcript path to the
  worker, which binds the session to the tool's current sign-in. `SessionEnd`
  passes them too, so its worker ingests the session's final rows at once; the
  binding it stores attributes nothing (see Limits), and those rows take the
  session's `SessionStart` binding or, without one, the run's observation.
- Both hooks are synchronous (no `async`), with a 5 second `timeout` on
  Claude Code and, on Codex, `SessionStart` 10 and `SessionEnd` 3 (Codex's
  cap; it also runs `SessionEnd` synchronously even with `async: true`).
  `costs.Hook` returns in 0.04 to 0.09 s (measured), so a synchronous hook
  costs a session nothing noticeable. Claude Code documents that a background hook still
  running when a headless (`-p`) session ends is killed, and a 1-second
  background hook was observed to lose its `SessionEnd` run that way.
  The "no `SessionEnd` on exit" observation was not reproduced:
  interactive sessions in an isolated `CLAUDE_CONFIG_DIR` logged a
  `SessionEnd` worker line on `/exit`, Ctrl-C, Ctrl-D, hang-up and TERM, with
  or without `async`. In one worker log, six of the nine
  `SessionStart` runs match `compact_boundary` records of the one long
  session, and no other session ended in that window, so the log does not show
  a failed exit.

## 11. Out of scope

`.jsonl.zst` rollouts; long-context pricing; Batch pricing (Codex does not
use the Batch API); `token_count.rate_limits.credits` (an account balance, not
a per-response cost); usage from Codex Cloud tasks, which run remotely and
leave no local rollout.
