# Codex costs design

Status: proposed, 2026-10-01. Extends section 11 of the
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
  `archived_sessions/`). Its SQLite databases do not help: `state_*.sqlite`
  `threads.tokens_used` is one unsplit integer per thread with no model, and
  `thread_history_*.sqlite` is a projection of the rollout without usage. The
  Codex CLI has no usage or cost command and ships no prices
  (`models_cache.json` holds context windows and tiers only).
- **Each line** is `{timestamp, ordinal?, type, payload}`. The types that
  matter: `session_meta` (thread id, `forked_from_id`, cwd), `turn_context`
  (model, cwd; one per turn), `token_usage_record`, and `event_msg` with
  `payload.type` `token_count` or `thread_settings_applied` (service tier).
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
  followed by Batch, Flex and Priority tables. Long-context prices apply above
  272K input tokens; the largest Codex request on the sample machine was 255,813.
  Eleven turns there ran at the priority tier.

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

`Parse` keeps per-file state: `Thread` (the first `session_meta.id`), `Cwd`,
`Model`, `Tier`, `Records` (whether this file has yielded a record) and
`LastTotal` (the last `token_count` running total). Before decoding, a line
must contain one of `"session_meta"`, `"turn_context"`, `"token_usage_record"`,
`"token_count"` or `"thread_settings_applied"`; every other line, most of the
22 GB, is skipped by a byte search.

- `session_meta`: the first one sets `Thread` and `Cwd`; later ones are copies
  from a parent and are ignored.
- `turn_context`: sets `Model` and `Cwd`.
- `thread_settings_applied`: sets `Tier` from `service_tier`.
- `token_usage_record` whose `thread_id` is `Thread`: one row keyed
  `response_id`; sets `Records`. A record with another `thread_id` is a copy
  and is skipped (its original is ingested from the parent's file).
- `token_count` while `Records` is false, `info` is non-null, the running total
  differs from `LastTotal`, and `last_token_usage` has non-zero input or
  output: one row from `last_token_usage`, keyed `tc:` plus the SHA-256 of the
  canonical JSON (Go-marshalled, sorted keys) of `total_token_usage`,
  `last_token_usage` and `rate_limits`. The key holds no timestamp or file, so
  a fork's copy has its original's key and the ledger's upsert keeps one row;
  `rate_limits` separates unrelated threads that happen to reach the same
  totals. On the sample machine this key leaves 347,339 rows; keying on the
  totals alone leaves 347,329.

Each row: `Model` = `Model` (or `unknown`), `Project` = `Cwd`, `Session` =
`Thread`, `Time` = the line's timestamp. Token mapping, so that the report's
prompt total (input + writes + reads) is right:

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
- **Rewrite check.** `head` is the SHA-256 of the file's first line. When it
  differs from the stored one, ingest reads the file from offset 0 with empty
  state. Every key is derived from content, so a full re-read only rewrites
  the same rows.
- **Speed.** Files are parsed concurrently (`GOMAXPROCS` workers) and committed
  one transaction per file in list order, so a crash still leaves every
  committed file consistent. With the byte prefilter the first full read of the
  sample machine should be bounded by disk speed: `rg` scans the same 22 GB in
  4.5 s with a warm page cache. Later runs read only appended bytes.

## 5. Rates

- `PricingURL` is the page above; `CODEX_COSTS_PRICING_URL` replaces it for
  checks that must not reach the network. Refresh policy, overrides and the
  `official`/`builtin`/`override` labels are unchanged.
- `ParsePricing` reads only the table under the "Standard pricing data"
  heading, skipping Batch, Flex and Priority the way the Claude parser skips
  batch tables. The model cell's note in parentheses is dropped
  (`gpt-5.5 (<272K context length)` is `gpt-5.5`). Columns: short-context
  input, cached input, cache writes, output. A `-` cache-write price bills
  writes at the input price, and a `-` cached-input price bills reads at the
  input price. Long-context columns are not read; section 1 shows no Codex
  request reaching 272K.
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
`promptctl` grep still catches the real leak.

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
Workbench installs are trusted on the same apply and none it removed stay
trusted. `Hooks(home)` reports an event as installed when `hooks.json` has the
command and the recorded `trusted_hash` equals the hash computed the same way;
a mismatch (Codex changed its normalization) shows as `untrusted` in
`costs status`.

Claude Code's hooks already run an ingest that sweeps every tool, so Codex
hooks only matter while Codex is used without Claude Code; `workbench costs`
and `costs ingest` catch up in any case.

## 7. Report

The Codex tab shows one `cache write` column instead of `cache 5m` and
`cache 1h` (`Tool` gains the cache-write column labels), and its footer reads
`list-price equivalents at OpenAI API prices, not ChatGPT plan charges`.
Everything else (tabs, drill-down, `--tool codex`, JSON and CSV output) works
unchanged once the source is set.

## 8. Open decisions for the owner

1. **Hook trust.** Decided 2026-10-01: Workbench writes the trust (section 6).
2. **Priority tier.** Either (a) price priority turns at standard rates and
   keep the tier only in the per-file state (recommended for now: eleven turns
   on the sample machine), or (b) store the model as `<model>@priority` and
   also parse the Priority table, so those turns are priced and shown
   separately.

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

`.jsonl.zst` rollouts; long-context pricing; Batch and Flex pricing (Codex
does not use them); `token_count.rate_limits.credits` (an account balance, not
a per-response cost); usage from Codex Cloud tasks, which run remotely and
leave no local rollout.
