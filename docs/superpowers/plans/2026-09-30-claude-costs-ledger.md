# claude-costs Ledger Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the transcript-scanning `claude-costs` script with a durable SQLite ledger fed by async Claude Code hooks, priced at current rates, reported by project, model, account and month.

**Architecture:** One stdlib-only Python script at `home/dot_local/bin/executable_claude-costs` with subcommands `ingest`, `report` (default), `rates`, `status`, `help`. `ingest` is the hook command: it spawns a detached worker that copies new transcript bytes into `ledger.sqlite` under a lock, then refreshes the rate card only when stale or a model is unpriced. Reports read the ledger only. Two async hook entries in `home/.chezmoidata/claude.json` trigger the ingest.

**Tech Stack:** Python 3.9+ standard library (`sqlite3`, `json`, `fcntl`, `urllib`, `subprocess`), SQLite WAL, chezmoi-managed home files, Claude Code command hooks with `async: true`.

**Spec:** `docs/superpowers/specs/2026-09-30-claude-costs-ledger-design.md`

## Global Constraints

- Python standard library only; no NumPy, no third-party packages. Syntax must run on Python 3.9 (macOS system Python): no `match`, no `X | Y` outside annotations, keep `from __future__ import annotations`.
- The tool never deletes, renames or recreates an existing ledger. Wrong schema version or a foreign file at the ledger path fails with the path and reason.
- The hook invocation (`claude-costs ingest` without `--worker`) always exits zero and writes nothing to stdout or stderr.
- Ingest is read-only on everything under the Claude config directory.
- Network access happens only in the worker's rate refresh or in `rates --refresh`, with a five-second timeout, conditional GET, and silent fallback to the cached card.
- All smoke checks run against a scratch config directory and scratch ledger via `CLAUDE_CONFIG_DIR`, `CLAUDE_COSTS_LEDGER`, `CLAUDE_COSTS_STATE`, `CLAUDE_COSTS_RATES`, `CLAUDE_COSTS_PRICING_URL`. Never write to the real `~/.local/share/claude-costs` or apply settings to the live machine without the owner's explicit go-ahead.
- No test suite (repository rule). Each task ends with an observed smoke check whose expected output is written down.
- Conventional commit subjects. End commit messages with `Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>`.
- Ruff: `uvx ruff check --config home/dot_config/ruff/pyproject.toml home/dot_local/bin/executable_claude-costs` and `uvx ruff format --check` with the same config must pass before each commit that touches the script.

## Review Focus

1. A transcript still being written ends in a partial JSON line. Ingest must stop at the last newline, store that offset, and pick the completed line up next run without duplicating the earlier rows. Pinned in Task 2, check 3.
2. Two Claude Code sessions end within the same second and both spawn a worker. One must ingest and the other must exit on the lock, with nothing lost, because every file is re-examined by the next worker. Pinned in Task 3, check 3.
3. The pricing page changes shape, drops a column, or returns HTML. The worker must keep the previous card, log one line, and the report must keep pricing from calibration or built-in rates. Pinned in Task 4, checks 2 and 4.
4. A ledger row's model has no rate anywhere. The report must show its tokens with cost zero and an `(unpriced)` flag rather than crash or hide it. Pinned in Task 5, check 3.
5. The hook runs on a machine where the ledger directory is not writable or SQLite is locked by a long report. The hook still exits zero silently; the worker writes the error to the log, and to `meta.last_error` whenever the ledger itself could be opened. Pinned in Task 3, check 4.

## Scratch fixtures used by every task

Create once, in the session scratchpad directory (call it `$S` below):

```bash
S=${CLAUDE_SCRATCHPAD:-/tmp/claude-costs-scratch}; mkdir -p "$S/cfg/projects/-home-u-dev-app/s1/subagents" "$S/data" "$S/state" "$S/rates"
export CLAUDE_CONFIG_DIR="$S/cfg" CLAUDE_COSTS_LEDGER="$S/data/ledger.sqlite" CLAUDE_COSTS_STATE="$S/state" CLAUDE_COSTS_RATES="$S/rates/rates.json"
cat > "$S/cfg/.claude.json" <<'EOF'
{"oauthAccount": {"emailAddress": "synthetic@example.test"}, "projects": {}}
EOF
cat > "$S/cfg/projects/-home-u-dev-app/s1.jsonl" <<'EOF'
{"type":"user","cwd":"/home/u/dev/app","sessionId":"s1","timestamp":"2026-09-01T10:00:00Z","message":{"role":"user","content":"hi"}}
{"type":"assistant","cwd":"/home/u/dev/app","sessionId":"s1","requestId":"req-1","timestamp":"2026-09-01T10:00:01Z","message":{"id":"msg-1","model":"claude-opus-5-5","usage":{"input_tokens":10,"output_tokens":5,"cache_creation_input_tokens":100,"cache_read_input_tokens":1000,"cache_creation":{"ephemeral_5m_input_tokens":40,"ephemeral_1h_input_tokens":60}}}}
{"type":"assistant","cwd":"/home/u/dev/app","sessionId":"s1","requestId":"req-1","timestamp":"2026-09-01T10:00:01Z","message":{"id":"msg-1","model":"claude-opus-5-5","usage":{"input_tokens":10,"output_tokens":500,"cache_creation_input_tokens":100,"cache_read_input_tokens":1000,"cache_creation":{"ephemeral_5m_input_tokens":40,"ephemeral_1h_input_tokens":60}}}}
{"type":"assistant","cwd":"/home/u/dev/app","sessionId":"s1","requestId":"req-2","timestamp":"2026-09-02T10:00:00Z","message":{"id":"msg-2","model":"<synthetic>","usage":{"input_tokens":1,"output_tokens":1}}}
EOF
printf '%s' '{"type":"assistant","cwd":"/home/u/dev/app","sessionId":"s1","requestId":"req-4"' >> "$S/cfg/projects/-home-u-dev-app/s1.jsonl"
cat > "$S/cfg/projects/-home-u-dev-app/s1/subagents/agent-x.jsonl" <<'EOF'
{"type":"assistant","cwd":"/home/u/dev/app/.worktrees/feat","sessionId":"s1","requestId":"req-3","timestamp":"2026-09-02T11:00:00Z","message":{"id":"msg-3","model":"claude-fable-5-1","usage":{"input_tokens":0,"output_tokens":20,"cache_creation_input_tokens":300,"cache_read_input_tokens":0}}}
EOF
cat > "$S/pricing.md" <<'EOF'
# Pricing

| Model | Base Input Tokens | 5m Cache Writes | 1h Cache Writes | Cache Hits & Refreshes | Output Tokens |
| --- | --- | --- | --- | --- | --- |
| Claude Opus 5.5 | $4 / MTok | $5 / MTok | $8 / MTok | $0.20 / MTok | $20 / MTok |
| Claude Fable 5.1 | $10 / MTok | $12.50 / MTok | $20 / MTok | $0.25 / MTok | $50 / MTok |
| Claude Haiku 4.5 | $1 / MTok | $1.25 / MTok | $2 / MTok | $0.10 / MTok | $5 / MTok |

| Tool | Cost |
| --- | --- |
| Web search | $10 / 1K searches |
EOF
```

Expected ledger after a full ingest of these fixtures: two `responses` rows (`req-1` with output 500, `req-3`), `req-2` skipped as synthetic, `req-4` not yet ingested because its line has no newline.

---

### Task 1: Script skeleton, paths, ledger schema, CLI dispatch

**Files:**
- Rewrite: `home/dot_local/bin/executable_claude-costs` (entire file replaced)

**Interfaces:**
- Produces: module constants `LEDGER`, `STATE`, `RATES_FILE`, `OFFICIAL_RATES`, `PRICING_URL`, `CLAUDE_DIR`, `CLAUDE_JSON`, `PROJECTS`, `LOG`, `LOCK`, `TOKEN_COLS`, `DEFAULT_ROOTS`; functions `die(msg)`, `info(msg)`, `now() -> str`, `open_ledger(create: bool) -> sqlite3.Connection`, `get_meta(db, key) -> str | None`, `set_meta(db, key, value)`, `parse_opts(args) -> SimpleNamespace`, `main(argv)`, and the `COMMANDS` dict that later tasks add entries to.

- [ ] **Step 1: Replace the file with the skeleton**

Write this as the complete content of `home/dot_local/bin/executable_claude-costs`:

```python
#!/usr/bin/env python3
"""claude-costs — durable per-response cost ledger for Claude Code.

Claude Code deletes session transcripts after `cleanupPeriodDays` (30 by
default). `claude-costs ingest`, run by async SessionStart/SessionEnd hooks,
copies every API response's usage into a SQLite ledger while the transcript
still exists. `claude-costs` (report) reads the ledger only, so its answer is
the same whether or not the transcripts survive.

Run `claude-costs help` for usage. Design: docs/superpowers/specs/
2026-09-30-claude-costs-ledger-design.md in the Workbench repository.
"""

from __future__ import annotations

import csv
import fcntl
import json
import os
import re
import select
import sqlite3
import subprocess
import sys
import time
import urllib.error
import urllib.request
from collections import defaultdict
from datetime import datetime, timezone
from pathlib import Path
from types import SimpleNamespace

SCHEMA_VERSION = "1"
HOME = Path.home()


def _xdg(var: str, default: str) -> Path:
    return Path(os.environ.get(var) or HOME / default)


LEDGER = Path(
    os.environ.get("CLAUDE_COSTS_LEDGER")
    or _xdg("XDG_DATA_HOME", ".local/share") / "claude-costs" / "ledger.sqlite"
)
STATE = Path(
    os.environ.get("CLAUDE_COSTS_STATE") or _xdg("XDG_STATE_HOME", ".local/state") / "claude-costs"
)
RATES_FILE = Path(
    os.environ.get("CLAUDE_COSTS_RATES")
    or _xdg("XDG_CONFIG_HOME", ".config") / "claude-costs" / "rates.json"
)
OFFICIAL_RATES = LEDGER.parent / "rates-official.json"
PRICING_URL = (
    os.environ.get("CLAUDE_COSTS_PRICING_URL")
    or "https://platform.claude.com/docs/en/about-claude/pricing.md"
)
CLAUDE_DIR = Path(os.environ.get("CLAUDE_CONFIG_DIR") or HOME / ".claude")
# Claude Code keeps .claude.json inside CLAUDE_CONFIG_DIR when that is set,
# and directly in $HOME otherwise.
CLAUDE_JSON = CLAUDE_DIR / ".claude.json" if os.environ.get("CLAUDE_CONFIG_DIR") else HOME / ".claude.json"
PROJECTS = CLAUDE_DIR / "projects"
LOG = STATE / "ingest.log"
LOCK = STATE / "ingest.lock"
LOG_KEEP = 200 * 1024
RATES_MAX_AGE = 7 * 86400
FETCH_TIMEOUT = 5
# Default report scope: only projects under these roots. `--all` lifts it.
DEFAULT_ROOTS = [HOME / "dev", HOME / "repos"]

TOKEN_COLS = ("input", "output", "cache_write_5m", "cache_write_1h", "cache_read")

BOLD, DIM, RED, GRN, YEL, OFF = (
    ("\033[1m", "\033[2m", "\033[31m", "\033[32m", "\033[33m", "\033[0m")
    if sys.stdout.isatty() and os.environ.get("NO_COLOR") is None
    else ("",) * 6
)


def die(msg: str) -> None:
    print(f"{RED}claude-costs: {msg}{OFF}", file=sys.stderr)
    raise SystemExit(1)


def info(msg: str) -> None:
    print(f"{DIM}→ {msg}{OFF}", file=sys.stderr)


def now() -> str:
    return datetime.now(timezone.utc).isoformat(timespec="seconds")


# ---------------------------------------------------------------- ledger

SCHEMA = """
CREATE TABLE responses (
  request_id     TEXT PRIMARY KEY,
  ts             TEXT NOT NULL,
  model          TEXT NOT NULL,
  project        TEXT NOT NULL,
  session_id     TEXT NOT NULL,
  account        TEXT NOT NULL,
  account_source TEXT NOT NULL,
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
  path      TEXT PRIMARY KEY,
  offset    INTEGER NOT NULL,
  size      INTEGER NOT NULL,
  mtime_ns  INTEGER NOT NULL,
  last_seen TEXT NOT NULL
);
CREATE TABLE meta (key TEXT PRIMARY KEY, value TEXT NOT NULL);
"""


def get_meta(db: sqlite3.Connection, key: str) -> str | None:
    row = db.execute("SELECT value FROM meta WHERE key = ?", (key,)).fetchone()
    return row[0] if row else None


def set_meta(db: sqlite3.Connection, key: str, value: str) -> None:
    db.execute(
        "INSERT INTO meta (key, value) VALUES (?, ?) "
        "ON CONFLICT(key) DO UPDATE SET value = excluded.value",
        (key, value),
    )


def open_ledger(create: bool) -> sqlite3.Connection:
    """Open the ledger. Never deletes, renames or rebuilds an existing file."""
    exists = LEDGER.is_file()
    if not exists and not create:
        die(f"no ledger at {LEDGER}; run `claude-costs ingest --worker` first")
    LEDGER.parent.mkdir(parents=True, exist_ok=True)
    try:
        db = sqlite3.connect(str(LEDGER), timeout=5)
        db.execute("PRAGMA journal_mode=WAL")
        if exists:
            has_meta = db.execute(
                "SELECT 1 FROM sqlite_master WHERE type = 'table' AND name = 'meta'"
            ).fetchone()
            if not has_meta:
                die(f"{LEDGER} exists but is not a claude-costs ledger; move it aside")
            version = get_meta(db, "schema_version")
            if version != SCHEMA_VERSION:
                die(f"{LEDGER}: schema version {version!r}, this tool needs {SCHEMA_VERSION}")
        else:
            db.executescript(SCHEMA)
            set_meta(db, "schema_version", SCHEMA_VERSION)
            db.commit()
    except sqlite3.Error as e:
        die(f"{LEDGER}: {e}")
    return db


# ---------------------------------------------------------------- commands


def cmd_help(opts: SimpleNamespace) -> None:
    print(
        f"""{BOLD}claude-costs{OFF} — Claude Code spend from a durable local ledger

  claude-costs [report] [--by project|model|account|month] [--since D] [--until D]
               [--all] [--top N] [--sort cost|name|calls] [--compact] [--no-rollup]
               [--json | --csv]
  claude-costs ingest              hook entry: spawn a detached worker, exit at once
  claude-costs ingest --worker     run the worker inline (used by hooks and for checks)
  claude-costs rates [--refresh]   show the merged rate card and its sources
  claude-costs status              ledger coverage, last ingest, hooks, rate card age
  claude-costs help

  ledger  {LEDGER}
  state   {STATE}
  rates   {RATES_FILE}  (manual overrides, highest priority)
  Figures are list-price equivalents, not subscription charges."""
    )


COMMANDS = {"help": cmd_help}


def parse_opts(args: list[str]) -> SimpleNamespace:
    o = SimpleNamespace(
        by="project", since=None, until=None, all=False, top=0, sort="cost",
        compact=False, rollup=True, json=False, csv=False, refresh=False,
        worker=False, quiet=False, rest=[],
    )
    it = iter(args)
    for a in it:
        def value() -> str:
            v = next(it, None)
            if v is None:
                die(f"{a} needs a value")
            return v

        if a == "--by":
            o.by = value()
            if o.by not in ("project", "model", "account", "month"):
                die("--by must be project, model, account or month")
        elif a == "--since":
            o.since = value()
        elif a == "--until":
            o.until = value()
        elif a == "--top":
            o.top = int(value())
        elif a == "--sort":
            o.sort = value()
            if o.sort not in ("cost", "name", "calls"):
                die("--sort must be cost, name or calls")
        elif a == "--all":
            o.all = True
        elif a == "--compact":
            o.compact = True
        elif a == "--no-rollup":
            o.rollup = False
        elif a == "--json":
            o.json = True
        elif a == "--csv":
            o.csv = True
        elif a == "--refresh":
            o.refresh = True
        elif a == "--worker":
            o.worker = True
        elif a in ("--quiet", "-q"):
            o.quiet = True
        elif a in ("-h", "--help"):
            o.rest.insert(0, "help")
        elif a.startswith("-"):
            die(f"unknown flag {a} (see `claude-costs help`)")
        else:
            o.rest.append(a)
    return o


def main(argv: list[str]) -> None:
    opts = parse_opts(argv)
    cmd = opts.rest[0] if opts.rest else "report"
    handler = COMMANDS.get(cmd)
    if handler is None:
        die(f"unknown command {cmd!r} (see `claude-costs help`)")
    handler(opts)


if __name__ == "__main__":
    try:
        main(sys.argv[1:])
    except KeyboardInterrupt:
        raise SystemExit(130)
```

Until Task 5 registers `report`, running `claude-costs` with no subcommand prints `unknown command 'report'`. That is expected.

- [ ] **Step 2: Compile and lint**

Run:
```bash
cd "$(git rev-parse --show-toplevel)"
python3 -m py_compile home/dot_local/bin/executable_claude-costs
uvx ruff check --config home/dot_config/ruff/pyproject.toml home/dot_local/bin/executable_claude-costs
uvx ruff format --check --config home/dot_config/ruff/pyproject.toml home/dot_local/bin/executable_claude-costs
```
Expected: no output from py_compile, `All checks passed!` from ruff check. If `ruff format --check` reports the file would be reformatted, run `uvx ruff format --config home/dot_config/ruff/pyproject.toml home/dot_local/bin/executable_claude-costs` and re-check.

- [ ] **Step 3: Smoke check the ledger open path**

Run with the scratch env from the fixtures section exported:
```bash
python3 home/dot_local/bin/executable_claude-costs help | head -3
python3 - <<'EOF'
import os, runpy, sqlite3
m = runpy.run_path("home/dot_local/bin/executable_claude-costs", run_name="lib")
db = m["open_ledger"](True)
print("tables:", sorted(r[0] for r in db.execute("SELECT name FROM sqlite_master WHERE type='table'")))
print("version:", m["get_meta"](db, "schema_version"))
print("journal:", db.execute("PRAGMA journal_mode").fetchone()[0])
db.execute("UPDATE meta SET value='99' WHERE key='schema_version'"); db.commit(); db.close()
try:
    m["open_ledger"](False)
except SystemExit as e:
    print("wrong version exits:", e.code)
db = sqlite3.connect(os.environ["CLAUDE_COSTS_LEDGER"]); db.execute("UPDATE meta SET value='1' WHERE key='schema_version'"); db.commit()
EOF
```
Expected:
```
claude-costs — Claude Code spend from a durable local ledger
tables: ['files', 'meta', 'responses']
version: 1
journal: wal
claude-costs: /…/data/ledger.sqlite: schema version '99', this tool needs 1
wrong version exits: 1
```

- [ ] **Step 4: Commit**

```bash
git add home/dot_local/bin/executable_claude-costs
git commit -m "feat(claude-costs): ledger schema and command skeleton

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>"
```

---

### Task 2: Transcript parsing and the ingest worker

**Files:**
- Modify: `home/dot_local/bin/executable_claude-costs` (insert a `# ---- ingest` section between the ledger section and the commands section)

**Interfaces:**
- Consumes: `open_ledger`, `get_meta`, `set_meta`, `now`, `PROJECTS`, `CLAUDE_JSON`, `LOG`, `LOCK`, `STATE`, `LOG_KEEP`.
- Produces: `log(msg: str)`, `current_account() -> str`, `discover() -> list[Path]`, `project_from_path(f: Path) -> str`, `row_from_record(r: dict, last_cwd: str | None, fallback: str) -> tuple | None`, `ingest_file(db, f: Path, account: str, source: str) -> int | None`, `ingest_worker(event: str, transcript: str, progress: bool = False) -> None`, `refresh_rates_if_needed(db) -> str` (stub replaced in Task 4; until then returns `"rates: not configured"`).

- [ ] **Step 1: Add the ingest section**

Insert after the ledger section (after `open_ledger`) and before `# ---- commands`:

```python
# ---------------------------------------------------------------- ingest

UPSERT = """
INSERT INTO responses (request_id, ts, model, project, session_id, account, account_source,
                       input, output, cache_write_5m, cache_write_1h, cache_read)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(request_id) DO UPDATE SET
  ts = excluded.ts, model = excluded.model, project = excluded.project,
  session_id = excluded.session_id,
  input = excluded.input, output = excluded.output,
  cache_write_5m = excluded.cache_write_5m, cache_write_1h = excluded.cache_write_1h,
  cache_read = excluded.cache_read,
  account = CASE WHEN excluded.account_source = 'session' THEN excluded.account ELSE account END,
  account_source = CASE WHEN excluded.account_source = 'session' THEN 'session' ELSE account_source END
"""


def log(msg: str) -> None:
    STATE.mkdir(parents=True, exist_ok=True)
    with LOG.open("a") as fh:
        fh.write(f"{now()} {msg}\n")


def truncate_log() -> None:
    try:
        if LOG.stat().st_size > LOG_KEEP:
            tail = LOG.read_bytes()[-LOG_KEEP:]
            LOG.write_bytes(tail[tail.find(b"\n") + 1 :])
    except OSError:
        pass


def current_account() -> str:
    try:
        data = json.loads(CLAUDE_JSON.read_text())
        return (data.get("oauthAccount") or {}).get("emailAddress") or "unknown"
    except (OSError, ValueError):
        return "unknown"


def discover() -> list[Path]:
    if not PROJECTS.is_dir():
        return []
    return sorted(p for p in PROJECTS.rglob("*.jsonl") if p.is_file())


def project_from_path(f: Path) -> str:
    """Last-resort project for records without a cwd: decode the project
    directory name. Claude Code encodes '/' as '-', so names containing '-'
    are ambiguous; records normally carry their own cwd."""
    try:
        name = f.relative_to(PROJECTS).parts[0]
    except ValueError:
        name = f.parent.name
    return "/" + name.lstrip("-").replace("-", "/")


def row_from_record(r: dict, last_cwd: str | None, fallback: str) -> tuple | None:
    if r.get("type") != "assistant":
        return None
    m = r.get("message") or {}
    u = m.get("usage")
    if not isinstance(u, dict):
        return None
    model = m.get("model") or "unknown"
    if model == "<synthetic>":
        return None
    rid = r.get("requestId") or m.get("id") or r.get("uuid")
    if not rid:
        return None
    cc = u.get("cache_creation") or {}
    c5, c1 = cc.get("ephemeral_5m_input_tokens"), cc.get("ephemeral_1h_input_tokens")
    if c5 is None and c1 is None:
        c5, c1 = u.get("cache_creation_input_tokens") or 0, 0
    return (
        rid,
        r.get("timestamp") or "",
        model,
        r.get("cwd") or last_cwd or fallback,
        r.get("sessionId") or "",
        u.get("input_tokens") or 0,
        u.get("output_tokens") or 0,
        c5 or 0,
        c1 or 0,
        u.get("cache_read_input_tokens") or 0,
    )


def ingest_file(db: sqlite3.Connection, f: Path, account: str, source: str) -> int | None:
    """Ingest new bytes of one transcript in one transaction.

    Returns the number of upserted rows, or None when the file was unchanged
    or has vanished. Stops at the last complete line so a transcript still
    being written is picked up next run from that offset."""
    try:
        st = f.stat()
    except FileNotFoundError:
        db.execute("DELETE FROM files WHERE path = ?", (str(f),))
        db.commit()
        return None
    prev = db.execute("SELECT offset, size, mtime_ns FROM files WHERE path = ?", (str(f),)).fetchone()
    offset = 0
    if prev:
        if prev[1] == st.st_size and prev[2] == st.st_mtime_ns:
            return None
        if st.st_size >= prev[1] and st.st_mtime_ns >= prev[2]:
            offset = prev[0]
    fallback = project_from_path(f)
    last_cwd = None
    n = 0
    with f.open("rb") as fh:
        fh.seek(offset)
        for raw in fh:
            if not raw.endswith(b"\n"):
                break
            offset += len(raw)
            if b'"cwd"' not in raw and b'"usage"' not in raw:
                continue
            try:
                r = json.loads(raw)
            except ValueError:
                continue
            if isinstance(r, dict) and r.get("cwd"):
                last_cwd = r["cwd"]
            rec = row_from_record(r, last_cwd, fallback) if isinstance(r, dict) else None
            if rec:
                db.execute(UPSERT, rec[:5] + (account, source) + rec[5:])
                n += 1
    db.execute(
        "INSERT INTO files (path, offset, size, mtime_ns, last_seen) VALUES (?, ?, ?, ?, ?) "
        "ON CONFLICT(path) DO UPDATE SET offset = excluded.offset, size = excluded.size, "
        "mtime_ns = excluded.mtime_ns, last_seen = excluded.last_seen",
        (str(f), offset, st.st_size, st.st_mtime_ns, now()),
    )
    db.commit()
    return n


def refresh_rates_if_needed(db: sqlite3.Connection) -> str:
    return "rates: not configured"


def belongs_to_session(f: Path, transcript: str) -> bool:
    """The SessionEnd transcript is <dir>/<sid>.jsonl; its subagents live under
    <dir>/<sid>/. Both are that session's rows."""
    if not transcript:
        return False
    t = Path(transcript)
    return f == t or t.with_suffix("") in f.parents


def ingest_worker(event: str, transcript: str, progress: bool = False) -> None:
    STATE.mkdir(parents=True, exist_ok=True)
    lock_fh = LOCK.open("w")
    try:
        fcntl.flock(lock_fh, fcntl.LOCK_EX | fcntl.LOCK_NB)
    except OSError:
        log("skip: another worker holds the lock")
        return
    truncate_log()
    t0 = time.time()
    db = open_ledger(create=True)
    account = current_account()
    files = discover()
    n_files = n_rows = 0
    errors = []
    for i, f in enumerate(files, 1):
        if progress:
            print(f"\r{DIM}  ingesting {i}/{len(files)} {f.parent.name[:40]:<40}{OFF}", end="", file=sys.stderr, flush=True)
        source = "session" if event == "SessionEnd" and belongs_to_session(f, transcript) else "sweep"
        try:
            n = ingest_file(db, f, account, source)
        except (OSError, sqlite3.Error, ValueError) as e:
            db.rollback()
            errors.append(f"{f}: {type(e).__name__}: {e}")
            continue
        if n is not None:
            n_files += 1
            n_rows += n
    if progress:
        print(file=sys.stderr)
    rates_msg = refresh_rates_if_needed(db)
    summary = f"{event or 'manual'}: {n_files} files, {n_rows} rows in {time.time() - t0:.1f}s; {rates_msg}"
    set_meta(db, "last_ingest_at", now())
    set_meta(db, "last_ingest_summary", summary)
    set_meta(db, "last_error", "; ".join(errors))
    db.commit()
    log(summary + (f"; {len(errors)} errors" if errors else ""))
    for err in errors:
        log("error: " + err)
    db.close()
    lock_fh.close()
```

- [ ] **Step 2: Add the `ingest` command, worker path only for now**

In the commands section, before `COMMANDS = {...}`, add:

```python
def cmd_ingest(opts: SimpleNamespace) -> None:
    if not opts.worker:
        die("use `claude-costs ingest --worker`")
    args = [a for a in opts.rest if a != "ingest"]
    event = args[0] if args else ""
    transcript = args[1] if len(args) > 1 else ""
    ingest_worker(event, transcript, progress=not opts.quiet and sys.stderr.isatty())
```

and change the dict to `COMMANDS = {"help": cmd_help, "ingest": cmd_ingest}`.

- [ ] **Step 3: Compile, lint, then run the worker against the fixtures**

Run the compile and lint commands from Task 1 step 2, then with the scratch env exported:
```bash
rm -f "$S/data/ledger.sqlite"*
python3 home/dot_local/bin/executable_claude-costs ingest --worker SessionEnd "$S/cfg/projects/-home-u-dev-app/s1.jsonl"
sqlite3 "$S/data/ledger.sqlite" "SELECT request_id, model, project, account, account_source, output, cache_write_5m, cache_write_1h, cache_read FROM responses ORDER BY 1;"
sqlite3 "$S/data/ledger.sqlite" "SELECT substr(path, -30), offset, size FROM files ORDER BY 1;"
sqlite3 "$S/data/ledger.sqlite" "SELECT key, value FROM meta ORDER BY 1;"
```
Expected (paths abbreviated):
```
req-1|claude-opus-5-5|/home/u/dev/app|synthetic@example.test|session|500|40|60|1000
req-3|claude-fable-5-1|/home/u/dev/app/.worktrees/feat|synthetic@example.test|session|20|300|0|0
-dev-app/s1/subagents/agent-x.jsonl|<size>|<size>
…/-home-u-dev-app/s1.jsonl|<offset smaller than size>|<size>
last_error|
last_ingest_at|2026-…
last_ingest_summary|SessionEnd: 2 files, 3 rows in 0.0s; rates: not configured
schema_version|1
```
Check 1: `req-1` output is 500, the final record, not 5. Check 2: `req-2` is absent. Check 3: for `s1.jsonl`, `offset` is smaller than `size` by the length of the partial `req-4` line.

- [ ] **Step 4: Check idempotency, the partial-line pickup, and truncation**

```bash
python3 home/dot_local/bin/executable_claude-costs ingest --worker
sqlite3 "$S/data/ledger.sqlite" "SELECT value FROM meta WHERE key='last_ingest_summary';"
printf ',"timestamp":"2026-09-03T00:00:00Z","message":{"id":"msg-4","model":"claude-opus-5-5","usage":{"input_tokens":1,"output_tokens":2}}}\n' >> "$S/cfg/projects/-home-u-dev-app/s1.jsonl"
python3 home/dot_local/bin/executable_claude-costs ingest --worker SessionStart
sqlite3 "$S/data/ledger.sqlite" "SELECT count(*), sum(output) FROM responses; SELECT account_source FROM responses WHERE request_id='req-4';"
head -c 200 "$S/cfg/projects/-home-u-dev-app/s1.jsonl" > "$S/trunc" && mv "$S/trunc" "$S/cfg/projects/-home-u-dev-app/s1.jsonl"
python3 home/dot_local/bin/executable_claude-costs ingest --worker
sqlite3 "$S/data/ledger.sqlite" "SELECT offset FROM files WHERE path LIKE '%/s1.jsonl';"
```
Expected, in order:
```
manual: 0 files, 0 rows in 0.0s; rates: not configured
3|522
sweep
<offset equal to the length of the first complete line only, i.e. 200 or less>
```
The first line shows unchanged files are skipped. The second shows `req-4` arrived once its newline did and was tagged `sweep` under SessionStart. The last shows a shrunken file was reparsed from zero.

- [ ] **Step 5: Restore the fixture and commit**

Re-run the fixture block from the top of this document to restore `s1.jsonl`, then:
```bash
git add home/dot_local/bin/executable_claude-costs
git commit -m "feat(claude-costs): ingest transcripts into the ledger by offset

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>"
```

---

### Task 3: Hook entry point, detach, lock behavior

**Files:**
- Modify: `home/dot_local/bin/executable_claude-costs` (`cmd_ingest`)

**Interfaces:**
- Consumes: `ingest_worker`, `STATE`, `LOG`.
- Produces: `cmd_ingest` handling both the hook path and `--worker`; `read_hook_input() -> tuple[str, str]`.

- [ ] **Step 1: Replace `cmd_ingest`**

```python
def read_hook_input() -> tuple[str, str]:
    """Claude Code pipes a JSON object on stdin. A terminal or an empty pipe
    yields ('', ''). Never blocks longer than half a second."""
    try:
        if sys.stdin.isatty():
            return "", ""
        ready, _, _ = select.select([sys.stdin], [], [], 0.5)
        if not ready:
            return "", ""
        h = json.loads(sys.stdin.read() or "{}")
        return str(h.get("hook_event_name") or ""), str(h.get("transcript_path") or "")
    except (OSError, ValueError):
        return "", ""


def cmd_ingest(opts: SimpleNamespace) -> None:
    args = [a for a in opts.rest if a != "ingest"]
    if opts.worker:
        event = args[0] if args else ""
        transcript = args[1] if len(args) > 1 else ""
        ingest_worker(event, transcript, progress=not opts.quiet and sys.stderr.isatty())
        return
    # Hook path: never print, never fail, never wait for the worker.
    try:
        event, transcript = read_hook_input()
        STATE.mkdir(parents=True, exist_ok=True)
        with LOG.open("ab") as log_fh:
            subprocess.Popen(
                [sys.executable, os.path.abspath(__file__), "ingest", "--worker", "--quiet", event, transcript],
                stdin=subprocess.DEVNULL,
                stdout=log_fh,
                stderr=log_fh,
                start_new_session=True,
                close_fds=True,
            )
    except Exception:  # noqa: BLE001 - the hook contract is silence
        pass
```

Also make `ingest_worker` robust to the worker being spawned when the ledger directory cannot be created: wrap its body after the lock in `try/except Exception as e:` that calls `log(f"error: {type(e).__name__}: {e}")` and returns. Concretely, change the start of `ingest_worker` to:

```python
def ingest_worker(event: str, transcript: str, progress: bool = False) -> None:
    try:
        STATE.mkdir(parents=True, exist_ok=True)
        lock_fh = LOCK.open("w")
    except OSError as e:
        print(f"claude-costs: cannot open state dir {STATE}: {e}", file=sys.stderr)
        return
    try:
        fcntl.flock(lock_fh, fcntl.LOCK_EX | fcntl.LOCK_NB)
    except OSError:
        log("skip: another worker holds the lock")
        return
    try:
        _ingest_locked(event, transcript, progress)
    except SystemExit as e:  # die() inside the worker: already printed to the log stream
        log(f"error: exit {e.code}")
    except Exception as e:  # noqa: BLE001 - background worker must record, not raise
        log(f"error: {type(e).__name__}: {e}")
    finally:
        lock_fh.close()
```

and move everything from `truncate_log()` to `db.close()` into a new function `_ingest_locked(event: str, transcript: str, progress: bool) -> None` unchanged.

- [ ] **Step 2: Compile and lint** as in Task 1 step 2.

- [ ] **Step 3: Check the hook path is silent, immediate and detached; check the lock**

```bash
rm -f "$S/data/ledger.sqlite"* "$S/state/ingest.log"
start=$(date +%s%N)
printf '{"hook_event_name":"SessionStart","transcript_path":"","cwd":"/x"}' | python3 home/dot_local/bin/executable_claude-costs ingest > "$S/out" 2> "$S/err"; echo "exit=$? ms=$(( ($(date +%s%N) - start) / 1000000 ))"
wc -c "$S/out" "$S/err"
python3 - <<'EOF'
import os, sqlite3, time
p = os.environ["CLAUDE_COSTS_LEDGER"]
for _ in range(50):
    time.sleep(0.1)
    if os.path.exists(p):
        db = sqlite3.connect(p)
        row = db.execute("SELECT value FROM meta WHERE key='last_ingest_summary'").fetchone()
        if row: print("worker finished:", row[0]); break
else:
    print("worker did not finish")
EOF
python3 - <<'EOF' &
import fcntl, os, time
fh = open(os.path.join(os.environ["CLAUDE_COSTS_STATE"], "ingest.lock"), "w"); fcntl.flock(fh, fcntl.LOCK_EX); time.sleep(3)
EOF
python3 - <<'EOF'
import time; time.sleep(0.5)
EOF
python3 home/dot_local/bin/executable_claude-costs ingest --worker; tail -1 "$S/state/ingest.log"
wait
```
Expected:
```
exit=0 ms=<under 300>
0 …/out
0 …/err
worker finished: SessionStart: 2 files, 3 rows in 0.0s; rates: not configured
2026-… skip: another worker holds the lock
```

- [ ] **Step 4: Check an unwritable ledger directory stays silent and is recorded**

```bash
rm -f "$S/data/ledger.sqlite"*
chmod 500 "$S/data"
printf '{"hook_event_name":"SessionEnd"}' | python3 home/dot_local/bin/executable_claude-costs ingest; echo "exit=$?"
python3 -c "import time; time.sleep(1)"
tail -2 "$S/state/ingest.log"
chmod 700 "$S/data"
```
Expected: `exit=0` with no other output from the hook, and the log tail contains a line beginning `claude-costs: …/data/ledger.sqlite: unable to open database file` (from `die` inside the worker, whose stderr is the log) followed by `… error: exit 1`. No ledger file was created. Later tasks recreate the ledger on their first worker run.

- [ ] **Step 5: Commit**

```bash
git add home/dot_local/bin/executable_claude-costs
git commit -m "feat(claude-costs): silent detached hook entry with single-instance lock

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>"
```

---

### Task 4: Rate card: built-in, overrides, calibration, official page, refresh policy

**Files:**
- Modify: `home/dot_local/bin/executable_claude-costs` (new `# ---- rates` section after the ingest section; replace the `refresh_rates_if_needed` stub; add `cmd_rates`)

**Interfaces:**
- Consumes: `CLAUDE_JSON`, `OFFICIAL_RATES`, `RATES_FILE`, `PRICING_URL`, `RATES_MAX_AGE`, `FETCH_TIMEOUT`, `TOKEN_COLS`, `log`, `get_meta`.
- Produces: `BUILTIN_RATES: dict`, `RATE_FIELDS = TOKEN_COLS`, `normalize_model(name: str) -> str | None`, `parse_pricing_markdown(text: str) -> dict[str, dict[str, float]]`, `refresh_official(force: bool) -> str`, `calibrated_rates() -> dict`, `load_card() -> dict[str, dict]` (each value has the five rate fields plus `"source"`), `rate_for(model: str, card: dict) -> dict | None`, `cost_of(tokens: dict, rate: dict) -> float`, `refresh_rates_if_needed(db) -> str`, `cmd_rates`.

- [ ] **Step 1: Add the rates section**

Insert after the ingest section, and delete the stub `refresh_rates_if_needed` from Task 2:

```python
# ---------------------------------------------------------------- rates

RATE_FIELDS = TOKEN_COLS  # USD per 1,000,000 tokens, same order as the ledger columns

# List prices as of 2026-09-30. Longest prefix wins, so a family row is the
# fallback for ids no specific row covers. Official page and calibration
# override these at runtime; the manual overrides file wins over everything.
BUILTIN_RATES = {
    "claude-fable-5-1": (10.0, 50.0, 12.5, 20.0, 0.25),
    "claude-fable-5": (10.0, 50.0, 12.5, 20.0, 1.0),
    "claude-opus-5-5": (4.0, 20.0, 5.0, 8.0, 0.20),
    "claude-opus-5": (5.0, 25.0, 6.25, 10.0, 0.5),
    "claude-opus-4-1": (15.0, 75.0, 18.75, 30.0, 1.5),
    "claude-opus-4": (5.0, 25.0, 6.25, 10.0, 0.5),
    "claude-opus": (5.0, 25.0, 6.25, 10.0, 0.5),
    "claude-sonnet-5": (2.0, 10.0, 2.5, 4.0, 0.20),
    "claude-sonnet-4": (3.0, 15.0, 3.75, 6.0, 0.3),
    "claude-sonnet": (3.0, 15.0, 3.75, 6.0, 0.3),
    "claude-haiku-4-5": (1.0, 5.0, 1.25, 2.0, 0.1),
    "claude-haiku": (1.0, 5.0, 1.25, 2.0, 0.1),
}

_MONEY = re.compile(r"\$\s*([0-9]+(?:\.[0-9]+)?)")


def _row(values, source: str) -> dict:
    return dict(zip(RATE_FIELDS, values), source=source)


def normalize_model(name: str) -> str | None:
    """'Claude Opus 5.5' -> 'claude-opus-5-5'; an id is returned as is."""
    s = re.sub(r"[`*_]|\[|\]\([^)]*\)", "", name).strip().lower()
    if not s.startswith("claude"):
        return None
    if " " not in s and s.startswith("claude-"):
        return s
    s = s[len("claude"):].strip()
    s = re.sub(r"[.\s]+", "-", s).strip("-")
    return f"claude-{s}" if s else None


def parse_pricing_markdown(text: str) -> dict[str, dict[str, float]]:
    """Read every Markdown table with an input and an output column."""
    card: dict[str, dict[str, float]] = {}
    lines = text.splitlines()
    i = 0
    while i < len(lines) - 1:
        head, sep = lines[i].strip(), lines[i + 1].strip()
        if not (head.startswith("|") and sep.startswith("|") and set(sep) <= set("|-: ")):
            i += 1
            continue
        headers = [c.strip().lower() for c in head.strip("|").split("|")]

        def col(*keys: str, exclude: tuple[str, ...] = ()) -> int | None:
            for j, h in enumerate(headers):
                if any(k in h for k in keys) and not any(x in h for x in exclude):
                    return j
            return None

        ci = col("input", exclude=("cache",))
        co = col("output")
        c5 = col("5m", "5-minute", exclude=("1h",))
        if c5 is None:
            c5 = col("write", exclude=("1h",))
        c1 = col("1h", "1-hour")
        cr = col("hit", "read", "refresh")
        i += 2
        if ci is None or co is None:
            continue
        while i < len(lines) and lines[i].strip().startswith("|"):
            cells = [c.strip() for c in lines[i].strip().strip("|").split("|")]
            i += 1
            model = normalize_model(cells[0]) if cells else None
            if not model:
                continue

            def money(j: int | None) -> float | None:
                if j is None or j >= len(cells):
                    return None
                m = _MONEY.search(cells[j])
                return float(m.group(1)) if m else None

            inp, out = money(ci), money(co)
            if inp is None or out is None:
                continue
            w5 = money(c5) if money(c5) is not None else inp * 1.25
            w1 = money(c1) if money(c1) is not None else inp * 2
            rd = money(cr) if money(cr) is not None else inp * 0.1
            card[model] = dict(zip(RATE_FIELDS, (inp, out, w5, w1, rd)))
    return card


def _read_official() -> dict:
    try:
        return json.loads(OFFICIAL_RATES.read_text())
    except (OSError, ValueError):
        return {}


def refresh_official(force: bool) -> str:
    """Conditional GET of the pricing page. Best effort: any failure keeps the
    cached card and returns a one-line reason."""
    cached = _read_official()
    age = time.time() - float(cached.get("fetched_at") or 0)
    if not force and cached.get("rates") and age < RATES_MAX_AGE:
        return f"rates: cached ({age / 86400:.1f}d old)"
    req = urllib.request.Request(PRICING_URL, headers={"User-Agent": "claude-costs"})
    if cached.get("etag"):
        req.add_header("If-None-Match", cached["etag"])
    if cached.get("last_modified"):
        req.add_header("If-Modified-Since", cached["last_modified"])
    try:
        with urllib.request.urlopen(req, timeout=FETCH_TIMEOUT) as resp:
            body = resp.read().decode("utf-8", errors="replace")
            etag = resp.headers.get("ETag") or ""
            last_modified = resp.headers.get("Last-Modified") or ""
    except urllib.error.HTTPError as e:
        if e.code == 304 and cached.get("rates"):
            cached["fetched_at"] = time.time()
            _write_official(cached)
            return "rates: official card not modified"
        return f"rates: fetch failed (HTTP {e.code}); keeping cached card"
    except (urllib.error.URLError, OSError, ValueError) as e:
        return f"rates: fetch failed ({type(e).__name__}: {e}); keeping cached card"
    rates = parse_pricing_markdown(body)
    if not rates:
        return "rates: fetch ok but no model rows parsed; keeping cached card"
    _write_official(
        {"fetched_at": time.time(), "etag": etag, "last_modified": last_modified, "url": PRICING_URL, "rates": rates}
    )
    return f"rates: official card updated ({len(rates)} models)"


def _write_official(data: dict) -> None:
    OFFICIAL_RATES.parent.mkdir(parents=True, exist_ok=True)
    tmp = OFFICIAL_RATES.with_suffix(".tmp")
    tmp.write_text(json.dumps(data, indent=1, sort_keys=True))
    tmp.replace(OFFICIAL_RATES)


def _solve_normal_equations(rows: list[list[float]], costs: list[float]) -> list[float] | None:
    """Least squares for four rates via A^T A x = A^T b with partial pivoting."""
    n = 4
    ata = [[sum(r[i] * r[j] for r in rows) for j in range(n)] for i in range(n)]
    atb = [sum(r[i] * c for r, c in zip(rows, costs)) for i in range(n)]
    m = [ata[i] + [atb[i]] for i in range(n)]
    for c in range(n):
        p = max(range(c, n), key=lambda r: abs(m[r][c]))
        if abs(m[p][c]) < 1e-12:
            return None
        m[c], m[p] = m[p], m[c]
        for r in range(n):
            if r != c:
                f = m[r][c] / m[c][c]
                m[r] = [a - f * b for a, b in zip(m[r], m[c])]
    return [m[i][n] / m[i][i] for i in range(n)]


def calibrated_rates() -> dict[str, dict]:
    """Solve rates from Claude Code's own (tokens, costUSD) pairs in
    lastModelUsage. Those records hold one cache-write count, so the 1h rate
    is derived as 1.6x the solved write rate (2.0 / 1.25)."""
    try:
        data = json.loads(CLAUDE_JSON.read_text())
    except (OSError, ValueError):
        return {}
    obs: dict[str, list] = defaultdict(list)
    for entry in (data.get("projects") or {}).values():
        for model, u in (entry.get("lastModelUsage") or {}).items():
            x = [
                (u.get("inputTokens") or 0) / 1e6,
                (u.get("outputTokens") or 0) / 1e6,
                (u.get("cacheCreationInputTokens") or 0) / 1e6,
                (u.get("cacheReadInputTokens") or 0) / 1e6,
            ]
            cost = u.get("costUSD") or 0
            if cost > 0 and any(x):
                obs[model].append((x, cost))
    out = {}
    for model, pairs in obs.items():
        if len(pairs) < 4:
            continue
        sol = _solve_normal_equations([p[0] for p in pairs], [p[1] for p in pairs])
        if sol is None or min(sol) < 0:
            continue
        out[model] = _row((sol[0], sol[1], sol[2], sol[2] * 1.6, sol[3]), "calibrated")
    return out


def load_card() -> dict[str, dict]:
    card = {m: _row(v, "builtin") for m, v in BUILTIN_RATES.items()}
    card.update(calibrated_rates())
    for m, v in (_read_official().get("rates") or {}).items():
        card[m] = dict(v, source="official")
    if RATES_FILE.is_file():
        try:
            user = json.loads(RATES_FILE.read_text())
        except ValueError as e:
            die(f"{RATES_FILE}: {e}")
        for m, v in user.items():
            base = card.get(m) or _row((0.0,) * 5, "override")
            card[m] = dict(base, **{k: float(v[k]) for k in RATE_FIELDS if k in v}, source="override")
    return card


def rate_for(model: str, card: dict) -> dict | None:
    best = None
    for prefix in card:
        if model.startswith(prefix) and (best is None or len(prefix) > len(best)):
            best = prefix
    return card[best] if best else None


def cost_of(tokens: dict, rate: dict) -> float:
    return sum(tokens[f] / 1_000_000 * rate[f] for f in RATE_FIELDS)


def refresh_rates_if_needed(db: sqlite3.Connection) -> str:
    card = load_card()
    models = [r[0] for r in db.execute("SELECT DISTINCT model FROM responses")]
    unpriced = [m for m in models if rate_for(m, card) is None]
    cached = _read_official()
    age = time.time() - float(cached.get("fetched_at") or 0)
    if not unpriced and cached.get("rates") and age < RATES_MAX_AGE:
        return f"rates: cached ({age / 86400:.1f}d old)"
    msg = refresh_official(force=True)
    if unpriced:
        msg += f"; unpriced: {', '.join(sorted(unpriced))}"
    return msg
```

- [ ] **Step 2: Add `cmd_rates` and register it**

In the commands section:

```python
def cmd_rates(opts: SimpleNamespace) -> None:
    if opts.refresh:
        info(refresh_official(force=True))
    card = load_card()
    official = _read_official()
    calibrated = calibrated_rates()
    fetched = official.get("fetched_at")
    when = datetime.fromtimestamp(fetched, timezone.utc).date().isoformat() if fetched else "never"
    print(f"{BOLD}USD per 1M tokens{OFF}  {DIM}official card fetched: {when}{OFF}\n")
    print(f"  {DIM}{'model prefix':<28} {'input':>8} {'output':>8} {'w-5m':>8} {'w-1h':>8} {'read':>8}  source{OFF}")
    for m, r in sorted(card.items()):
        flag = ""
        c = calibrated.get(m)
        o = (official.get("rates") or {}).get(m)
        if c and o and any(abs(c[f] - o[f]) > 0.05 * max(o[f], 1e-9) for f in ("input", "output")):
            flag = f"  {YEL}calibrated disagrees >5%{OFF}"
        print(
            f"  {m:<28} {r['input']:>8.2f} {r['output']:>8.2f} {r['cache_write_5m']:>8.2f} "
            f"{r['cache_write_1h']:>8.2f} {r['cache_read']:>8.3f}  {r['source']}{flag}"
        )
    print(f"\n{DIM}Longest prefix wins. Overrides: {RATES_FILE}. Refresh: claude-costs rates --refresh{OFF}")
```

Register: `COMMANDS = {"help": cmd_help, "ingest": cmd_ingest, "rates": cmd_rates}`.

- [ ] **Step 3: Compile and lint** as in Task 1 step 2.

- [ ] **Step 4: Check the parser, the file-URL refresh, and the failure paths**

```bash
python3 - <<'EOF'
import os, runpy
m = runpy.run_path("home/dot_local/bin/executable_claude-costs", run_name="lib")
card = m["parse_pricing_markdown"](open(os.environ["S"] + "/pricing.md").read())
for k in sorted(card): print(k, card[k])
print("html:", m["parse_pricing_markdown"]("<html><body>moved</body></html>"))
print("norm:", m["normalize_model"]("**Claude Opus 5.5**"), m["normalize_model"]("`claude-haiku-4-5`"), m["normalize_model"]("Web search"))
EOF
CLAUDE_COSTS_PRICING_URL="file://$S/pricing.md" python3 home/dot_local/bin/executable_claude-costs rates --refresh 2>&1 | sed -n '1,8p'
CLAUDE_COSTS_PRICING_URL="http://127.0.0.1:9/pricing.md" python3 home/dot_local/bin/executable_claude-costs rates --refresh 2>&1 | sed -n '1,2p'
python3 -c "import json,os; d=json.load(open(os.environ['S']+'/data/rates-official.json')); print('cached models:', sorted(d['rates']))"
```
Expected:
```
claude-fable-5-1 {'input': 10.0, 'output': 50.0, 'cache_write_5m': 12.5, 'cache_write_1h': 20.0, 'cache_read': 0.25}
claude-haiku-4-5 {'input': 1.0, 'output': 5.0, 'cache_write_5m': 1.25, 'cache_write_1h': 2.0, 'cache_read': 0.1}
claude-opus-5-5 {'input': 4.0, 'output': 20.0, 'cache_write_5m': 5.0, 'cache_write_1h': 8.0, 'cache_read': 0.2}
html: {}
norm: claude-opus-5-5 claude-haiku-4-5 None
→ rates: official card updated (3 models)
USD per 1M tokens  official card fetched: 2026-09-30
…
→ rates: fetch failed (URLError: …); keeping cached card
USD per 1M tokens  official card fetched: 2026-09-30
cached models: ['claude-fable-5-1', 'claude-haiku-4-5', 'claude-opus-5-5']
```
The rates table must show `official` as the source for those three models and `builtin` for the rest.

- [ ] **Step 5: Check calibration recovers known rates**

```bash
python3 - <<'EOF'
import json, os
# five projects, one model, costs computed at 4 / 20 / 5 / 0.2 per MTok
rows = [(1e6,0,0,0),(0,1e6,0,0),(0,0,1e6,0),(0,0,0,1e6),(2e6,1e6,5e5,4e6)]
projects = {}
for i,(a,b,c,d) in enumerate(rows):
    projects[f"/p{i}"] = {"lastModelUsage": {"claude-test-9": {"inputTokens":a,"outputTokens":b,"cacheCreationInputTokens":c,"cacheReadInputTokens":d,"costUSD": (a*4+b*20+c*5+d*0.2)/1e6}}}
p = os.path.join(os.environ["CLAUDE_CONFIG_DIR"], ".claude.json")
json.dump({"oauthAccount": {"emailAddress": "synthetic@example.test"}, "projects": projects}, open(p, "w"))
EOF
python3 home/dot_local/bin/executable_claude-costs rates | grep claude-test-9
```
Expected: `claude-test-9  4.00  20.00  5.00  8.00  0.200  calibrated` (whitespace aside). Then restore the fixture `.claude.json` from the fixtures block.

- [ ] **Step 6: Check the worker's refresh policy**

```bash
python3 -c "import json,os; p=os.environ['S']+'/data/rates-official.json'; d=json.load(open(p)); d['fetched_at']=0; json.dump(d,open(p,'w'))"
CLAUDE_COSTS_PRICING_URL="file://$S/pricing.md" python3 home/dot_local/bin/executable_claude-costs ingest --worker --quiet; tail -1 "$S/state/ingest.log"
python3 home/dot_local/bin/executable_claude-costs ingest --worker --quiet; tail -1 "$S/state/ingest.log"
```
Expected: the first log line ends with `rates: official card updated (3 models)` because the card was stale; the second ends with `rates: cached (0.0d old)` because nothing needed fetching and no network call was made.

- [ ] **Step 7: Commit**

```bash
git add home/dot_local/bin/executable_claude-costs
git commit -m "feat(claude-costs): layered rate card with best-effort official refresh

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>"
```

---

### Task 5: Report and status from the ledger

**Files:**
- Modify: `home/dot_local/bin/executable_claude-costs` (new `# ---- report` section; `cmd_report`, `cmd_status`; register both)

**Interfaces:**
- Consumes: `open_ledger`, `get_meta`, `ingest_worker`, `load_card`, `rate_for`, `cost_of`, `_read_official`, `DEFAULT_ROOTS`, `RATE_FIELDS`, `CLAUDE_DIR`, `LOCK`.
- Produces: `rollup(project: str) -> str`, `in_scope(project: str, opts) -> bool`, `load_groups(db, opts) -> list[dict]`, `coverage(db) -> dict`, `cmd_report`, `cmd_status`.

- [ ] **Step 1: Add the report section**

```python
# ---------------------------------------------------------------- report


def rollup(project: str) -> str:
    return project.split("/.worktrees/")[0]


def in_scope(project: str, opts: SimpleNamespace) -> bool:
    if opts.all:
        return True
    p = Path(project)
    return any(p == root or root in p.parents for root in DEFAULT_ROOTS)


def human(n: float) -> str:
    for unit, div in (("B", 1e9), ("M", 1e6), ("K", 1e3)):
        if n >= div:
            return f"{n / div:,.1f}{unit}"
    return str(int(n))


def short(p: str) -> str:
    return p.replace(str(HOME), "~")


def ensure_ingested(db: sqlite3.Connection) -> sqlite3.Connection:
    """First run before the hooks exist: fill the ledger inline."""
    if db.execute("SELECT 1 FROM responses LIMIT 1").fetchone():
        return db
    db.close()
    info("ledger is empty; ingesting transcripts once inline")
    ingest_worker("", "", progress=sys.stderr.isatty())
    return open_ledger(create=True)


def load_groups(db: sqlite3.Connection, opts: SimpleNamespace) -> list[dict]:
    """One dict per (project, model, account, month) with tokens, calls, cost."""
    where, params = [], []
    if opts.since:
        where.append("substr(ts, 1, 10) >= ?")
        params.append(opts.since)
    if opts.until:
        where.append("substr(ts, 1, 10) <= ?")
        params.append(opts.until)
    sql = (
        "SELECT project, model, account, substr(ts, 1, 7), COUNT(*), "
        + ", ".join(f"SUM({c})" for c in RATE_FIELDS)
        + " FROM responses"
        + (" WHERE " + " AND ".join(where) if where else "")
        + " GROUP BY 1, 2, 3, 4"
    )
    card = load_card()
    groups = []
    for project, model, account, month, calls, *tokens in db.execute(sql, params):
        project = rollup(project) if opts.rollup else project
        if not in_scope(project, opts):
            continue
        t = dict(zip(RATE_FIELDS, tokens))
        rate = rate_for(model, card)
        groups.append(
            {
                "project": project, "model": model, "account": account, "month": month or "unknown",
                "calls": calls, "cost": cost_of(t, rate) if rate else 0.0, "priced": rate is not None, **t,
            }
        )
    return groups


def aggregate(groups: list[dict], key: str) -> list[dict]:
    out: dict[str, dict] = {}
    for g in groups:
        b = out.setdefault(g[key], {key: g[key], "calls": 0, "cost": 0.0, "priced": True, **{f: 0 for f in RATE_FIELDS}})
        b["calls"] += g["calls"]
        b["cost"] += g["cost"]
        b["priced"] = b["priced"] and g["priced"]
        for f in RATE_FIELDS:
            b[f] += g[f]
    return list(out.values())


def sort_rows(rows: list[dict], key: str, opts: SimpleNamespace) -> list[dict]:
    order = {"cost": lambda r: -r["cost"], "name": lambda r: r[key], "calls": lambda r: -r["calls"]}[opts.sort]
    return sorted(rows, key=order)


def coverage(db: sqlite3.Connection) -> dict:
    first, last, rows = db.execute("SELECT MIN(ts), MAX(ts), COUNT(*) FROM responses").fetchone()
    by_source = dict(db.execute("SELECT account_source, COUNT(*) FROM responses GROUP BY 1"))
    official = _read_official()
    fetched = official.get("fetched_at")
    return {
        "first": (first or "")[:10] or "none", "last": (last or "")[:10] or "none", "rows": rows,
        "session_rows": by_source.get("session", 0), "sweep_rows": by_source.get("sweep", 0),
        "last_ingest_at": get_meta(db, "last_ingest_at") or "never",
        "last_ingest_summary": get_meta(db, "last_ingest_summary") or "",
        "last_error": get_meta(db, "last_error") or "",
        "rates_fetched": datetime.fromtimestamp(fetched, timezone.utc).date().isoformat() if fetched else "never",
    }


def print_header(cov: dict, card: dict) -> None:
    sources = sorted({r["source"] for r in card.values()})
    print(
        f"{DIM}ledger {cov['first']} → {cov['last']}, {cov['rows']:,} responses "
        f"({cov['session_rows']:,} tagged at session end, {cov['sweep_rows']:,} by sweep); "
        f"last ingest {cov['last_ingest_at']}; rates {'/'.join(sources)}, official card {cov['rates_fetched']}{OFF}"
    )
    print(f"{DIM}figures are list-price equivalents, not subscription charges{OFF}")


def print_table(rows: list[dict], key: str, label: str) -> None:
    print(f"\n{BOLD}{label:<34} {'cost':>12} {'calls':>8} {'input':>9} {'output':>9} {'w-5m':>9} {'w-1h':>9} {'cache-r':>10}{OFF}")
    for r in rows:
        flag = "" if r["priced"] else f" {YEL}(unpriced){OFF}"
        print(
            f"  {short(str(r[key]))[:34]:<34} ${r['cost']:>11,.2f} {r['calls']:>8,} {human(r['input']):>9} "
            f"{human(r['output']):>9} {human(r['cache_write_5m']):>9} {human(r['cache_write_1h']):>9} "
            f"{human(r['cache_read']):>10}{flag}"
        )


def print_totals(groups: list[dict], shown_n: int, total_n: int, opts: SimpleNamespace) -> None:
    print(f"\n{BOLD}{'─' * 96}{OFF}")
    print_table(sort_rows(aggregate(groups, "model"), "model", opts), "model", "TOTAL BY MODEL")
    print_table(sort_rows(aggregate(groups, "account"), "account", opts), "account", "TOTAL BY ACCOUNT")
    tot = {"calls": sum(g["calls"] for g in groups), "cost": sum(g["cost"] for g in groups), "priced": all(g["priced"] for g in groups)}
    for f in RATE_FIELDS:
        tot[f] = sum(g[f] for g in groups)
    print(f"{BOLD}{'─' * 96}{OFF}")
    print(
        f"  {BOLD}{'GRAND TOTAL (all accounts)':<34} ${tot['cost']:>11,.2f} {tot['calls']:>8,} {human(tot['input']):>9} "
        f"{human(tot['output']):>9} {human(tot['cache_write_5m']):>9} {human(tot['cache_write_1h']):>9} "
        f"{human(tot['cache_read']):>10}{OFF}"
    )
    hidden = total_n - shown_n
    print(
        f"  {DIM}{shown_n} of {total_n} {opts.by}s shown"
        f"{f' ({hidden} hidden by --top, counted above)' if hidden else ''}"
        f"{'' if opts.all else ' · scope: ~/dev and ~/repos (--all for every project)'}{OFF}"
    )
    unpriced = sorted({g["model"] for g in groups if not g["priced"]})
    if unpriced:
        print(f"\n{YEL}warning:{OFF} no rate for {', '.join(unpriced)}; tokens counted, cost shown as 0. "
              f"Run `claude-costs rates --refresh` or add them to {short(str(RATES_FILE))}.")
```

- [ ] **Step 2: Add `cmd_report` and `cmd_status`, register them**

```python
def cmd_report(opts: SimpleNamespace) -> None:
    db = ensure_ingested(open_ledger(create=True))
    groups = load_groups(db, opts)
    if not groups:
        print("no responses matched (try `claude-costs --all`, or check `claude-costs status`)")
        return
    key = opts.by
    rows = sort_rows(aggregate(groups, key), key, opts)
    shown = rows[: opts.top] if opts.top else rows
    if opts.json or opts.csv:
        cols = [key, "cost", "calls", *RATE_FIELDS, "priced"]
        if opts.json:
            json.dump({"by": key, "coverage": coverage(db), "rows": shown, "grand_total": sum(r["cost"] for r in rows)}, sys.stdout, indent=1)
            print()
        else:
            w = csv.writer(sys.stdout)
            w.writerow(cols)
            for r in shown:
                w.writerow([r[c] for c in cols])
        return
    print_header(coverage(db), load_card())
    if key == "project" and not opts.compact:
        for p in shown:
            share = p["cost"] / sum(r["cost"] for r in rows) * 100 if rows and sum(r["cost"] for r in rows) else 0
            print(f"\n{BOLD}{short(p['project'])}{OFF}  {GRN}${p['cost']:,.2f}{OFF}  {DIM}{share:.1f}% of total · {p['calls']:,} calls{OFF}")
            models = sort_rows(aggregate([g for g in groups if g["project"] == p["project"]], "model"), "model", opts)
            print_table(models, "model", "  model")
    else:
        print_table(shown, key, key)
    print_totals(groups, len(shown), len(rows), opts)


def hooks_installed() -> dict[str, bool]:
    try:
        hooks = json.loads((CLAUDE_DIR / "settings.json").read_text()).get("hooks") or {}
    except (OSError, ValueError):
        hooks = {}
    out = {}
    for event in ("SessionStart", "SessionEnd"):
        out[event] = any(
            "claude-costs ingest" in (h.get("command") or "") and h.get("async") is True
            for group in hooks.get(event) or [] for h in group.get("hooks") or []
        )
    return out


def cmd_status(opts: SimpleNamespace) -> None:
    db = ensure_ingested(open_ledger(create=True))
    cov = coverage(db)
    locked = False
    try:
        with LOCK.open("a+") as fh:
            try:
                fcntl.flock(fh, fcntl.LOCK_EX | fcntl.LOCK_NB)
            except OSError:
                locked = True
    except OSError:
        pass
    size = LEDGER.stat().st_size if LEDGER.is_file() else 0
    hooks = hooks_installed()
    print(f"{BOLD}ledger{OFF}      {LEDGER} ({human(size)}B)")
    print(f"{BOLD}coverage{OFF}    {cov['first']} → {cov['last']}, {cov['rows']:,} responses "
          f"({cov['session_rows']:,} session-tagged, {cov['sweep_rows']:,} sweep-tagged)")
    print(f"{BOLD}last ingest{OFF} {cov['last_ingest_at']}  {DIM}{cov['last_ingest_summary']}{OFF}")
    print(f"{BOLD}last error{OFF}  {cov['last_error'] or 'none'}")
    print(f"{BOLD}worker{OFF}      {'running (lock held)' if locked else 'idle'}")
    print(f"{BOLD}rates{OFF}       official card fetched {cov['rates_fetched']}; overrides {'present' if RATES_FILE.is_file() else 'none'}")
    print(f"{BOLD}hooks{OFF}       SessionStart {'ok' if hooks['SessionStart'] else 'MISSING'}, "
          f"SessionEnd {'ok' if hooks['SessionEnd'] else 'MISSING'}  {DIM}({CLAUDE_DIR / 'settings.json'}){OFF}")
```

Register:
```python
COMMANDS = {"help": cmd_help, "ingest": cmd_ingest, "rates": cmd_rates, "report": cmd_report, "status": cmd_status}
```

- [ ] **Step 3: Compile and lint** as in Task 1 step 2.

- [ ] **Step 4: Check the report numbers against hand-computed costs**

With the fixture ledger from Task 4 step 6 (official card holds the three fixture models):
```bash
python3 home/dot_local/bin/executable_claude-costs --all 2>/dev/null
python3 home/dot_local/bin/executable_claude-costs --all --by account --json 2>/dev/null | python3 -c "import json,sys; d=json.load(sys.stdin); print(d['rows'][0]['account'], round(d['grand_total'], 6))"
python3 home/dot_local/bin/executable_claude-costs --all --by month --csv 2>/dev/null
python3 home/dot_local/bin/executable_claude-costs --all --no-rollup --compact 2>/dev/null | grep -c worktrees
```
Hand computation per million tokens: `req-1` on opus-5-5 is 10×4 + 500×20 + 40×5 + 60×8 + 1000×0.20 = 10,920, so $0.010920. `req-3` on fable-5-1 is 20×50 + 300×12.5 = 4,750, so $0.004750. `req-4` on opus-5-5 is 1×4 + 2×20 = 44, so $0.000044. Grand total $0.015714.

Expected: the project report shows one project `/home/u/dev/app` (the worktree folded in) with two model rows, TOTAL BY MODEL lists `claude-opus-5-5` at $0.01 and `claude-fable-5-1` at $0.00 (two-decimal display), and TOTAL BY ACCOUNT one row `synthetic@example.test`. The JSON line prints `synthetic@example.test 0.015714`. The CSV has a header row and rows for `2026-09`. The last command prints `1`, the worktree shown separately.

- [ ] **Step 5: Check an unpriced model is shown, not hidden**

```bash
sqlite3 "$S/data/ledger.sqlite" "INSERT INTO responses VALUES ('req-x','2026-09-05T00:00:00Z','claude-zeta-1','/home/u/dev/app','s1','synthetic@example.test','sweep',7,0,0,0,0);"
python3 home/dot_local/bin/executable_claude-costs --all 2>/dev/null | grep -E "unpriced|zeta"
sqlite3 "$S/data/ledger.sqlite" "DELETE FROM responses WHERE request_id='req-x';"
```
Expected: a `claude-zeta-1` row with cost `$0.00` and `(unpriced)`, plus the closing warning naming `claude-zeta-1`.

- [ ] **Step 6: Check status and the inline first run**

```bash
python3 home/dot_local/bin/executable_claude-costs status
rm -f "$S/data/ledger.sqlite"*
python3 home/dot_local/bin/executable_claude-costs status 2>&1 | head -3
```
Expected: the first status prints seven labelled lines with `worker idle`, `last error none`, and `hooks SessionStart MISSING, SessionEnd MISSING` (the scratch config has no settings.json). The second run starts with `→ ledger is empty; ingesting transcripts once inline` and then shows coverage `2026-09-01 → 2026-09-03, 3 responses`.

- [ ] **Step 7: Commit**

```bash
git add home/dot_local/bin/executable_claude-costs
git commit -m "feat(claude-costs): report and status from the ledger

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>"
```

---

### Task 6: Hooks in claude.json, completion, documentation

**Files:**
- Modify: `home/.chezmoidata/claude.json` (add `hooks` under `claude.enforced`)
- Rewrite: `home/dot_local/share/bash-completion/completions/claude-costs`
- Modify: `docs/superpowers/specs/workbench-contracts.md:134` (the claude-costs row) and the row at line 133 (Claude settings row mentions hooks)
- Modify: `home/dot_local/bin/executable_claude-costs` docstring only if any command name changed (it should not)

**Interfaces:**
- Consumes: the command surface from Tasks 1 to 5.
- Produces: enforced hook entries that `hooks_installed()` in Task 5 recognizes (command contains `claude-costs ingest`, `async` true).

- [ ] **Step 1: Add the hooks to the enforced settings**

In `home/.chezmoidata/claude.json`, inside `"enforced"`, after the `"statusLine"` object, add:

```json
      "hooks": {
        "SessionStart": [
          {
            "hooks": [
              { "type": "command", "command": "claude-costs ingest", "async": true, "timeout": 10 }
            ]
          }
        ],
        "SessionEnd": [
          {
            "hooks": [
              { "type": "command", "command": "claude-costs ingest", "async": true, "timeout": 10 }
            ]
          }
        ]
      },
```

- [ ] **Step 2: Rewrite the completion file**

```bash
# bash completion for claude-costs
_claude_costs() {
  local cur prev cmds opts
  COMPREPLY=()
  cur=${COMP_WORDS[COMP_CWORD]}
  prev=${COMP_WORDS[COMP_CWORD-1]}
  cmds="report ingest rates status help"
  opts="--by --since --until --all --top --sort --compact --no-rollup --json --csv --refresh --worker --quiet --help"

  case "$prev" in
    --by)   COMPREPLY=($(compgen -W "project model account month" -- "$cur")); return ;;
    --sort) COMPREPLY=($(compgen -W "cost name calls" -- "$cur")); return ;;
    --top)  COMPREPLY=($(compgen -W "5 10 20 50" -- "$cur")); return ;;
    --since|--until)
      COMPREPLY=($(compgen -W "$(date +%Y-01-01) $(date +%Y-%m-01) $(date +%Y-%m-%d)" -- "$cur"))
      return ;;
  esac

  if ((COMP_CWORD == 1)) && [[ $cur != -* ]]; then
    COMPREPLY=($(compgen -W "$cmds" -- "$cur")); return
  fi
  COMPREPLY=($(compgen -W "$opts" -- "$cur"))
}
complete -F _claude_costs claude-costs
```

- [ ] **Step 3: Update the contracts inventory**

Replace the claude-costs row in `docs/superpowers/specs/workbench-contracts.md` with:

```
| `dot_local/bin/executable_claude-costs`, bash completion | All / all | Python 3.9+ standard library at use time | User/bin and completion; F. The enforced `SessionStart`/`SessionEnd` async hooks in `.chezmoidata/claude.json` run `claude-costs ingest`, which reads transcripts and writes only `~/.local/share/claude-costs` and `~/.local/state/claude-costs`; the worker fetches the public pricing page at most weekly, best effort. Do not run it to validate provisioning |
```

In the Claude settings row directly above it, change `All / hooks and work MCP only work` to `All / claude-costs hooks all, Codex hooks and work MCP only work`.

- [ ] **Step 4: Render check and settings merge check**

```bash
cd "$(git rev-parse --show-toplevel)"
python3 -c "import json; json.load(open('home/.chezmoidata/claude.json')); print('claude.json ok')"
scripts/render-check.sh personal pinned
scripts/render-check.sh work latest
scripts/render-check.sh work pinned wsl
```
Expected: `claude.json ok` and each render check exits zero (`OK [personal/pinned]` and so on). Then confirm the merged settings carry the hooks without disturbing a hook the user added themselves. `scripts/scratch-init.sh` prints the path of a throwaway chezmoi config; `chezmoi execute-template --with-stdin` feeds stdin to the modify template as `.chezmoi.stdin`:

```bash
config=$(scripts/scratch-init.sh personal pinned); scratch=$(dirname "$config")
printf '{"hooks":{"PreToolUse":[{"matcher":"Bash","hooks":[{"type":"command","command":"echo keep"}]}]},"theme":"light"}' \
  | chezmoi --config "$config" --source "$PWD" --destination "$scratch/destination" \
      --persistent-state "$scratch/state.boltdb" --cache "$scratch/cache" --no-pager --refresh-externals=never \
      execute-template --with-stdin "$(cat home/private_dot_claude/modify_private_settings.json)" \
  | python3 -c "import json,sys; d=json.load(sys.stdin); print(sorted(d['hooks']), d['hooks']['SessionEnd'][0]['hooks'][0]['async'], d['theme'])"
rm -rf "$scratch"
```
Expected: `['PreToolUse', 'SessionEnd', 'SessionStart'] True light`, showing the user's own hook and live theme survive and the two enforced events are added.

- [ ] **Step 5: Commit**

```bash
git add home/.chezmoidata/claude.json home/dot_local/share/bash-completion/completions/claude-costs docs/superpowers/specs/workbench-contracts.md
git commit -m "feat(claude): run claude-costs ingest from async session hooks

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>"
```

---

### Task 7: Observed check against real transcripts, into a scratch ledger

**Files:**
- Modify: `docs/superpowers/specs/2026-09-30-claude-costs-ledger-design.md` (Status line and section 9, recording what was observed)

**Interfaces:**
- Consumes: the finished script.
- Produces: an observed comparison recorded in the spec.

This task reads the real transcripts on the machine but writes only to a scratch ledger. It does not apply settings to the machine. Installing the hooks for real is a separate `workbench` apply that needs the owner's go-ahead.

- [ ] **Step 1: Backfill into a scratch ledger**

```bash
unset CLAUDE_CONFIG_DIR
export CLAUDE_COSTS_LEDGER="$S/real/ledger.sqlite" CLAUDE_COSTS_STATE="$S/real/state" CLAUDE_COSTS_RATES="$S/real/none.json"
python3 home/dot_local/bin/executable_claude-costs ingest --worker
python3 home/dot_local/bin/executable_claude-costs status
```
Expected: coverage starting at the oldest surviving transcript date (2026-08-18 on 2026-09-30), on the order of 69,000 responses, `last error none`, and the ingest summary ending in either `official card updated` or a `fetch failed … keeping cached card` line if offline. Either is acceptable.

- [ ] **Step 2: Compare per-session cost against Claude Code's own `lastCost`**

```bash
python3 - <<'EOF'
import json, os, runpy, sqlite3
m = runpy.run_path("home/dot_local/bin/executable_claude-costs", run_name="lib")
card = m["load_card"](); db = sqlite3.connect(os.environ["CLAUDE_COSTS_LEDGER"])
cj = json.load(open(os.path.expanduser("~/.claude.json")))
print(f"{'project':40} {'lastCost':>10} {'ledger':>10} {'diff':>7}")
for proj, e in sorted(cj.get("projects", {}).items()):
    sid, last = e.get("lastSessionId"), e.get("lastCost") or 0
    if not sid or not last:
        continue
    rows = db.execute("SELECT model, SUM(input),SUM(output),SUM(cache_write_5m),SUM(cache_write_1h),SUM(cache_read) FROM responses WHERE session_id=? GROUP BY 1", (sid,)).fetchall()
    if not rows:
        continue
    total = sum(m["cost_of"](dict(zip(m["RATE_FIELDS"], r[1:])), m["rate_for"](r[0], card) or dict.fromkeys(m["RATE_FIELDS"], 0)) for r in rows)
    print(f"{proj[-40:]:40} {last:10.2f} {total:10.2f} {(total/last-1)*100:6.1f}%")
EOF
```
Expected: every printed session within about ±10% of `lastCost`. Larger deviations mean a rate or parsing defect: check `claude-costs rates` for the model in question first.

- [ ] **Step 3: Record the observation in the spec**

Change the spec's first line to `Status: implemented 2026-09-30; observed checks below.` and append under section 9 a short list: the row count and date range ingested, the per-session comparison table, and whether the official fetch succeeded. Use exact observed numbers; do not round to what the plan predicted. Label sessions by kind (single run, resumed), never by project name or path.

- [ ] **Step 4: Commit**

```bash
git add docs/superpowers/specs/2026-09-30-claude-costs-ledger-design.md
git commit -m "docs(claude-costs): record observed ledger checks

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>"
```

- [ ] **Step 5: Report to the owner**

State plainly: what the comparison showed, that the real ledger at `~/.local/share/claude-costs` does not exist yet, and that installing the hooks needs a `workbench` apply of the changed `claude.json`, which is their call. After that apply, the first `claude-costs` run backfills inline and every later session keeps the ledger current.
