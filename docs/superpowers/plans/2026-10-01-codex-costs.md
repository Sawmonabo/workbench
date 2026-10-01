# Codex Costs Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Record every Codex response in the `workbench costs` ledger, priced from OpenAI's live price page at the service tier it ran at, and install Codex ingest hooks that Workbench itself marks trusted.

**Architecture:** A second `costs.Source` (`internal/costs/codex.go`) reads Codex rollout JSONL. Ingest gains per-file saved state, a rewrite check, parallel reads with ordered commits, and tier bookkeeping (`tier_changes`, `tier_pending`) so a subagent is priced at its root thread's tier. The service tier rides in the model id (`gpt-5.6-sol@fast`), so the report, focus pages and rate lookup need only small changes. The chezmoi Codex hooks template renders for every role and the managed `config.toml` merge writes each hook's `trusted_hash`.

**Tech Stack:** Go 1.26 (cobra, modernc SQLite, go-toml/v2, lipgloss v2), chezmoi templates, Python 3.11+ modify script.

**Spec:** `docs/superpowers/specs/2026-10-01-codex-costs-design.md`

## Global Constraints

- One Go module; no new dependencies (`github.com/pelletier/go-toml/v2` is already required).
- Tests stay near zero (AGENTS.md): the only test change is the ledger upgrade test, which guards existing rows. Every other behavior is verified by the scripted checks in the tasks, run from the scratchpad, never committed.
- Routine gates before each commit touching Go: `golangci-lint fmt`, `golangci-lint config verify`, `golangci-lint run ./...`, `go build ./...`, `go test ./...`, with the version in `.golangci-lint-version` (`go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(cat .golangci-lint-version) …`).
- Before committing `home/` or `scripts/render-check.sh` changes: `scripts/render-check.sh ROLE MODE [wsl]` for every role (`personal`, `work`, `both`), mode (`pinned`, `latest`) and with `wsl`.
- Conventional commit subjects; every commit ends with `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`. Never edit `CHANGELOG.md`.
- No personal data in tracked files: no real home paths, emails, account ids, repo names or spend. Fixtures use `/w/...` paths and `example.test` emails.
- Validation never writes the real ledger or the real `~/.codex`: set `CLAUDE_COSTS_LEDGER`, `CLAUDE_COSTS_STATE`, `CLAUDE_COSTS_RATES` to scratch paths, and `CODEX_HOME` / `HOME` to a scratch directory for fixture runs. Reading the real `~/.codex/sessions` read-only is allowed (Task 6).
- Ledger rule kept from the existing code: never delete, rename or rebuild an existing ledger; an upgrade adds columns and tables in one transaction and keeps every row.
- Ledger request ids of non-Claude tools are stored as `<tool>:<id>` (existing `Upsert` behavior).
- Codex token mapping: `input = max(input_tokens − cached_input_tokens − cache_write_input_tokens, 0)`, `cache_read = cached_input_tokens`, `cache_write_5m = cache_write_input_tokens`, `cache_write_1h = 0`, `output = output_tokens` (reasoning is inside it).
- Tier names stored in model ids: `priority` and `fast` → `fast`; `ultrafast` → `ultrafast`; `flex` → `flex`; `default`, `auto`, `standard`, empty → standard (no suffix); anything else is kept lower-cased and prices as unpriced.
- Price page: `https://developers.openai.com/api/docs/pricing.md`; `CODEX_COSTS_PRICING_URL` replaces it.

## Review Focus

1. **A legacy fork file copies its parent's `token_count` events with new timestamps** — the copies must collapse onto the parent's rows, which keep the parent's time and session (Task 3, fixture check 2).
2. **Ingest resumes mid-file in a later run** — the model, cwd and tier of earlier lines must still apply to new lines (Task 3, fixture check 4).
3. **`codex migrate-rollouts` rewrites a file in place with different bytes** — re-reading must add no rows (Task 3, fixture check 5).
4. **A subagent file is read before its root's file, and the root switched to Fast before the subagent ran** — the subagent row ends priced `@fast` once the run finishes (Task 3, fixture check 3).
5. **A model or tier the price card does not list** (`gpt-5.3-codex-spark`, a tier `scale`) — shown unpriced, never priced at a family row such as `gpt-5` (Task 3, fixture check 6).

---

### Task 1: Ledger schema 3 — saved file state, tier tables, earliest attribution

**Files:**
- Modify: `internal/costs/ledger.go`
- Modify: `internal/costs/source.go`
- Modify: `internal/costs/ledger_test.go`

**Interfaces:**
- Produces (source.go): `type TierChange struct { Thread string; Time time.Time; Tier string }`; `Usage.TierFrom string`; `FileState.Saved []byte`, `FileState.Tiers []TierChange`, unexported `FileState.cache any`; `func SplitTier(model string) (base, tier string)`.
- Produces (ledger.go): `FileRow{Offset, Size, ModTimeNanos int64; State []byte; Head string}`; `(*Tx).AddTierChange(TierChange) error`; `(*Ledger).ResolveTiers(ctx) (int, error)`; `Upsert` records a pending tier row when `u.TierFrom != ""`.

- [ ] **Step 1: Extend the source types** in `internal/costs/source.go`. Add to `Usage` (after the token fields):

```go
	// TierFrom is the root thread whose service tier prices this row when
	// the row's own transcript names none (a Codex subagent's); "" otherwise.
	TierFrom string
```

Replace `FileState` with:

```go
// FileState is what a source keeps between the lines of one transcript, and,
// through Saved, between ingest runs.
type FileState struct {
	Path     string // the transcript
	Fallback string // project decoded from the path, for records without a cwd
	LastCwd  string // Claude: the last cwd seen in this file

	// Saved is the source's own state at the stored offset; ingest stores it
	// with the offset and hands it back when it resumes the file.
	Saved []byte
	// Tiers are the service tier changes read this run; ingest stores them.
	Tiers []TierChange

	cache any // the source's decoded Saved during one read
}

// TierChange is a thread switching service tier at a time; Tier is the
// stored tier name, "" for standard.
type TierChange struct {
	Thread string
	Time   time.Time
	Tier   string
}

// tiers are the service tiers a model id can carry after "@"; each has its
// own price table.
var tiers = []string{"fast", "flex", "ultrafast"}

// SplitTier splits "gpt-5.6-sol@fast" into the model and its service tier.
// An id without a known tier suffix is all model, tier "".
func SplitTier(model string) (base, tier string) {
	if i := strings.LastIndex(model, "@"); i >= 0 && slices.Contains(tiers, model[i+1:]) {
		return model[:i], model[i+1:]
	}
	return model, ""
}
```

Add `"slices"` and `"strings"` to the imports.

- [ ] **Step 2: Schema 3** in `internal/costs/ledger.go`. Set `schemaVersion = "3"`. In `schema`, replace the `files` table and add the two tier tables:

```sql
CREATE TABLE files (
  path      TEXT PRIMARY KEY,
  offset    INTEGER NOT NULL,
  size      INTEGER NOT NULL,
  mtime_ns  INTEGER NOT NULL,
  last_seen TEXT NOT NULL,
  state     TEXT NOT NULL DEFAULT '',
  head      TEXT NOT NULL DEFAULT ''
);
CREATE TABLE tier_changes (
  thread_id TEXT NOT NULL,
  ts        TEXT NOT NULL,
  tier      TEXT NOT NULL,
  PRIMARY KEY (thread_id, ts, tier)
);
CREATE TABLE tier_pending (
  request_id TEXT PRIMARY KEY,
  root       TEXT NOT NULL
);
```

In `prepare`, change `case "1":` to `case "1", "2":`. Replace `upgrade` and its comment with:

```go
// upgrade is the only write to existing data, in one transaction that keeps
// every row: version 1 gains the tool column, versions 1 and 2 gain the saved
// file state and the tier tables.
func (l *Ledger) upgrade(ctx context.Context) error {
	return l.transact(ctx, func(tx *sql.Tx) error {
		var version string
		if err := tx.QueryRowContext(
			ctx, "SELECT value FROM meta WHERE key = 'schema_version'",
		).Scan(&version); err != nil {
			return err
		}
		var statements []string
		switch version {
		case "1":
			statements = append(statements,
				"ALTER TABLE responses ADD COLUMN tool TEXT NOT NULL DEFAULT 'claude'",
				"CREATE INDEX IF NOT EXISTS responses_tool ON responses (tool)",
			)
			fallthrough
		case "2":
			statements = append(statements,
				"ALTER TABLE files ADD COLUMN state TEXT NOT NULL DEFAULT ''",
				"ALTER TABLE files ADD COLUMN head TEXT NOT NULL DEFAULT ''",
				`CREATE TABLE IF NOT EXISTS tier_changes (thread_id TEXT NOT NULL, ts TEXT NOT NULL,
				  tier TEXT NOT NULL, PRIMARY KEY (thread_id, ts, tier))`,
				`CREATE TABLE IF NOT EXISTS tier_pending (request_id TEXT PRIMARY KEY, root TEXT NOT NULL)`,
			)
		default:
			return nil // another process upgraded it first
		}
		for _, statement := range statements {
			if _, err := tx.ExecContext(ctx, statement); err != nil {
				return err
			}
		}
		return setMeta(ctx, tx, "schema_version", schemaVersion)
	})
}
```

Update the `OpenLedger` comment's second sentence to: "a version 1 or 2 ledger is upgraded in one transaction that keeps every row, any other version fails with the path and version and writes nothing."

- [ ] **Step 3: Saved state in `files`.** Replace `FileRow`, `File` and `SetFile`:

```go
// FileRow is where ingest stopped in one transcript: the offset, the size and
// time it had then, the source's saved state there, and a digest of its
// first line, which tells a file rewritten in place from one that grew.
type FileRow struct {
	Offset, Size, ModTimeNanos int64
	State                      []byte
	Head                       string
}

// File is the stored position of a transcript.
func (l *Ledger) File(path string) (row FileRow, found bool, err error) {
	var state string
	err = l.db.QueryRow(
		"SELECT offset, size, mtime_ns, state, head FROM files WHERE path = ?", path,
	).Scan(&row.Offset, &row.Size, &row.ModTimeNanos, &state, &row.Head)
	if errors.Is(err, sql.ErrNoRows) {
		return row, false, nil
	}
	if state != "" {
		row.State = []byte(state)
	}
	return row, err == nil, err
}
```

```go
// SetFile stores where ingest stopped in a transcript.
func (t *Tx) SetFile(path string, row FileRow) error {
	_, err := t.tx.ExecContext(
		t.ctx,
		`INSERT INTO files (path, offset, size, mtime_ns, last_seen, state, head)
		 VALUES (?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(path) DO UPDATE SET offset = excluded.offset, size = excluded.size,
		   mtime_ns = excluded.mtime_ns, last_seen = excluded.last_seen,
		   state = excluded.state, head = excluded.head`,
		path, row.Offset, row.Size, row.ModTimeNanos, stamp(time.Now()), string(row.State), row.Head,
	)
	return err
}
```

- [ ] **Step 4: Earliest occurrence keeps the attribution.** Replace the `upsert` constant and its comment:

```go
// Streamed usage only grows, and Claude Code copies earlier records into the
// transcript of a resumed or forked session, sometimes mid-stream. The token
// columns therefore keep the largest value seen, so neither file order nor a
// later partial or zeroed copy can lower a request's final usage. A Codex fork
// copies its parent's usage with the fork's time and thread, so the time,
// model, project and session come from the earliest occurrence; on a tie the
// row read last wins, as it always has.
const upsert = `
INSERT INTO responses (request_id, ts, model, project, session_id, account, account_source,
                       input, output, cache_write_5m, cache_write_1h, cache_read, tool)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(request_id) DO UPDATE SET
  ts         = CASE WHEN ` + earlier + ` THEN excluded.ts ELSE ts END,
  model      = CASE WHEN ` + earlier + ` THEN excluded.model ELSE model END,
  project    = CASE WHEN ` + earlier + ` THEN excluded.project ELSE project END,
  session_id = CASE WHEN ` + earlier + ` THEN excluded.session_id ELSE session_id END,
  input = MAX(input, excluded.input), output = MAX(output, excluded.output),
  cache_write_5m = MAX(cache_write_5m, excluded.cache_write_5m),
  cache_write_1h = MAX(cache_write_1h, excluded.cache_write_1h),
  cache_read = MAX(cache_read, excluded.cache_read),
  account = CASE WHEN excluded.account_source = 'session' THEN excluded.account ELSE account END,
  account_source = CASE WHEN excluded.account_source = 'session' THEN 'session' ELSE account_source END
`

// earlier is true when the incoming copy is at least as old as the stored
// row, which then takes its attribution. In an UPDATE, ts is the old value.
const earlier = `(ts = '' OR (excluded.ts <> '' AND excluded.ts <= ts))`
```

- [ ] **Step 5: Pending tiers and tier changes.** Replace `Upsert` and add `storedID`, `AddTierChange` and `ResolveTiers`:

```go
// storedID is the ledger key of a response: Claude's id as is, every other
// tool's as <tool>:<id>.
func storedID(u Usage) string {
	if u.Tool == firstTool {
		return u.RequestID
	}
	return u.Tool + ":" + u.RequestID
}

// Upsert records one response; source is "session" or "sweep". The account and
// source of an existing row are replaced only by a "session" one. A row priced
// by its root thread's tier waits in tier_pending until ResolveTiers prices it.
func (t *Tx) Upsert(u Usage, source string) error {
	id := storedID(u)
	when := ""
	if !u.Time.IsZero() {
		when = u.Time.UTC().Format(timeLayout)
	}
	if _, err := t.tx.ExecContext(
		t.ctx, upsert,
		id, when, u.Model, u.Project, u.Session, u.Account, source,
		u.Input, u.Output, u.CacheWrite5m, u.CacheWrite1h, u.CacheRead, u.Tool,
	); err != nil {
		return err
	}
	if u.TierFrom == "" {
		return nil
	}
	_, err := t.tx.ExecContext(t.ctx,
		`INSERT INTO tier_pending (request_id, root) VALUES (?, ?)
		 ON CONFLICT(request_id) DO UPDATE SET root = excluded.root`, id, u.TierFrom)
	return err
}

// AddTierChange records a thread's switch of service tier.
func (t *Tx) AddTierChange(c TierChange) error {
	if c.Thread == "" || c.Time.IsZero() {
		return nil
	}
	_, err := t.tx.ExecContext(t.ctx,
		"INSERT OR IGNORE INTO tier_changes (thread_id, ts, tier) VALUES (?, ?, ?)",
		c.Thread, c.Time.UTC().Format(timeLayout), c.Tier)
	return err
}

// ResolveTiers prices each row waiting on its root thread's service tier at
// the root's latest tier change at or before the row's time, and stops it
// waiting. A row whose root has no change by then keeps waiting and is priced
// at the standard tier meanwhile. Ingest calls it after every file of a run is
// committed, so the order files are read in does not matter.
func (l *Ledger) ResolveTiers(ctx context.Context) (int, error) {
	type pending struct{ id, model, tier string }
	var resolved []pending
	err := l.transact(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `
SELECT p.request_id, r.model,
       (SELECT c.tier FROM tier_changes c
         WHERE c.thread_id = p.root AND c.ts <= r.ts ORDER BY c.ts DESC LIMIT 1)
  FROM tier_pending p JOIN responses r ON r.request_id = p.request_id`)
		if err != nil {
			return err
		}
		for rows.Next() {
			var p pending
			var tier sql.NullString
			if err := rows.Scan(&p.id, &p.model, &tier); err != nil {
				_ = rows.Close()
				return err
			}
			if tier.Valid {
				p.tier = tier.String
				resolved = append(resolved, p)
			}
		}
		if err := rows.Close(); err != nil {
			return err
		}
		if err := rows.Err(); err != nil {
			return err
		}
		for _, p := range resolved {
			model, _ := SplitTier(p.model)
			if p.tier != "" {
				model += "@" + p.tier
			}
			if _, err := tx.ExecContext(ctx,
				"UPDATE responses SET model = ? WHERE request_id = ?", model, p.id); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx,
				"DELETE FROM tier_pending WHERE request_id = ?", p.id); err != nil {
				return err
			}
		}
		return nil
	})
	return len(resolved), err
}
```

- [ ] **Step 6: Extend the upgrade safeguard** in `internal/costs/ledger_test.go`. Change the version expectations from `"2"` to `"3"` (the condition, the message `want 2, 2, \"3\"`). After the version check, add:

```go
	var stateCol, headCol, tierTables int
	if err := ledger.db.QueryRow(`SELECT
	    (SELECT COUNT(*) FROM pragma_table_info('files') WHERE name = 'state'),
	    (SELECT COUNT(*) FROM pragma_table_info('files') WHERE name = 'head'),
	    (SELECT COUNT(*) FROM sqlite_master WHERE name IN ('tier_changes', 'tier_pending'))`).
		Scan(&stateCol, &headCol, &tierTables); err != nil {
		t.Fatal(err)
	}
	if stateCol != 1 || headCol != 1 || tierTables != 2 {
		t.Fatalf("upgrade left files.state=%d files.head=%d tier tables=%d, want 1, 1, 2",
			stateCol, headCol, tierTables)
	}
```

Then add a second test for a version 2 ledger, after the first test:

```go
// A version 2 ledger (the Workbench ledger before Codex) holds rows and file
// offsets; its upgrade must keep both.
func TestLedgerUpgradesVersionTwoWithoutLoss(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ledger.sqlite")
	old, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`CREATE TABLE responses (
		  request_id TEXT PRIMARY KEY, ts TEXT NOT NULL, model TEXT NOT NULL,
		  project TEXT NOT NULL, session_id TEXT NOT NULL, account TEXT NOT NULL,
		  account_source TEXT NOT NULL, input INTEGER NOT NULL, output INTEGER NOT NULL,
		  cache_write_5m INTEGER NOT NULL, cache_write_1h INTEGER NOT NULL, cache_read INTEGER NOT NULL,
		  tool TEXT NOT NULL DEFAULT 'claude')`,
		`CREATE TABLE files (path TEXT PRIMARY KEY, offset INTEGER NOT NULL, size INTEGER NOT NULL,
		  mtime_ns INTEGER NOT NULL, last_seen TEXT NOT NULL)`,
		`CREATE TABLE meta (key TEXT PRIMARY KEY, value TEXT NOT NULL)`,
		`INSERT INTO meta VALUES ('schema_version', '2')`,
		`INSERT INTO responses VALUES ('req-1', '2026-09-01T10:00:00.000Z', 'claude-opus-5', '/w/a', 's1',
		  'a@example.test', 'sweep', 100, 200, 300, 0, 1000, 'claude')`,
		`INSERT INTO files VALUES ('/w/t.jsonl', 4096, 4096, 1, '2026-09-01T10:00:00+00:00')`,
	} {
		if _, err := old.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	if err := old.Close(); err != nil {
		t.Fatal(err)
	}
	ledger, err := OpenLedger(path, false)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ledger.Close() }()
	var rows int
	if err := ledger.db.QueryRow("SELECT COUNT(*) FROM responses").Scan(&rows); err != nil {
		t.Fatal(err)
	}
	file, found, err := ledger.File("/w/t.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	if version, _ := ledger.Meta("schema_version"); rows != 1 || !found || file.Offset != 4096 ||
		file.Head != "" || version != "3" {
		t.Fatalf("upgrade kept %d rows, file %v (found %v), version %q; want 1 row, offset 4096, version 3",
			rows, file, found, version)
	}
}
```

- [ ] **Step 7: Make ingest compile against the new `FileRow`.** In `internal/costs/ingest.go` no call changes are needed yet (`SetFile` still takes a `FileRow`); confirm with:

Run: `go build ./... && go test ./internal/costs/`
Expected: build succeeds; `ok  github.com/Sawmonabo/workbench/internal/costs`.

- [ ] **Step 8: Gates and commit.**

Run the Global Constraints gates. Expected: `0 issues.`, build ok, tests ok.

```bash
git add internal/costs/ledger.go internal/costs/source.go internal/costs/ledger_test.go
git commit -m "feat(costs): ledger schema 3 keeps per-file source state and service tier changes" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 2: Ingest — saved state, rewrite check, parallel reads, tier resolution

**Files:**
- Modify: `internal/costs/ingest.go`

**Interfaces:**
- Consumes: Task 1 `FileRow{…State, Head}`, `FileState.Saved/Tiers`, `(*Tx).AddTierChange`, `(*Ledger).ResolveTiers`.
- Produces: `readFile(ledger *Ledger, tool Tool, path string) fileRead`, `commitFile(...)`, `readEach(...)`; behavior: a Source's `FileState.Saved` survives between runs; `Tiers` are stored; tiers resolve after every run.

- [ ] **Step 1: Replace `ingestFile`** (and its comment) with `fileRead`, `readFile`, `firstLineDigest` and `commitFile`:

```go
// fileRead is one transcript read, ready to commit.
type fileRead struct {
	path     string
	vanished bool // the file is gone: forget it
	changed  bool // new bytes were read
	usage    []Usage
	tiers    []TierChange
	row      FileRow
	err      error
}

// headBytes bounds the first-line digest: a first line longer than this is
// digested by its first headBytes bytes, which are written once.
const headBytes = 64 << 10

// readFile reads the new bytes of one transcript. It resumes at the stored
// offset with the source's stored state, unless the file shrank, went back in
// time or no longer starts with the line it started with (rewritten in place,
// as `codex migrate-rollouts` does); then it reads from the start with empty
// state, and since every key is derived from content a re-read rewrites the
// same rows. It stops at the last complete line, so a transcript still being
// written is picked up next run from that offset.
func readFile(ledger *Ledger, tool Tool, path string) fileRead {
	read := fileRead{path: path}
	info, err := os.Stat(path)
	if errors.Is(err, fs.ErrNotExist) {
		read.vanished = true
		return read
	}
	if err != nil {
		read.err = err
		return read
	}
	previous, found, err := ledger.File(path)
	if err != nil {
		read.err = wrapLedger(err)
		return read
	}
	size, mtime := info.Size(), info.ModTime().UnixNano()
	if found && previous.Size == size && previous.ModTimeNanos == mtime {
		return read
	}
	file, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		read.vanished = true
		return read
	}
	if err != nil {
		read.err = err
		return read
	}
	defer func() { _ = file.Close() }()
	head, err := firstLineDigest(file)
	if err != nil {
		read.err = err
		return read
	}
	state := &FileState{Path: path}
	offset := int64(0)
	if found && size >= previous.Size && mtime >= previous.ModTimeNanos &&
		(previous.Head == "" || head == "" || previous.Head == head) {
		offset, state.Saved = previous.Offset, previous.State
	}
	if _, err := file.Seek(offset, io.SeekStart); err != nil {
		read.err = err
		return read
	}
	reader := bufio.NewReaderSize(file, 1<<20)
	for {
		line, readErr := reader.ReadBytes('\n')
		if len(line) == 0 || line[len(line)-1] != '\n' {
			if readErr != nil && !errors.Is(readErr, io.EOF) {
				read.err = readErr
				return read
			}
			break
		}
		offset += int64(len(line))
		read.usage = append(read.usage, tool.Source.Parse(bytes.TrimRight(line, "\r\n"), state)...)
	}
	read.changed, read.tiers = true, state.Tiers
	read.row = FileRow{Offset: offset, Size: size, ModTimeNanos: mtime, State: state.Saved, Head: head}
	return read
}

// firstLineDigest is the SHA-256 of the file's first line (its first
// headBytes bytes when longer), or "" while that line is still being written.
func firstLineDigest(file *os.File) (string, error) {
	buf := make([]byte, headBytes)
	n, err := file.ReadAt(buf, 0)
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	buf = buf[:n]
	if i := bytes.IndexByte(buf, '\n'); i >= 0 {
		buf = buf[:i+1]
	} else if n < headBytes {
		return "", nil
	}
	sum := sha256.Sum256(buf)
	return hex.EncodeToString(sum[:]), nil
}

// commitFile stores one read in one transaction: its rows, its tier changes
// and where it stopped. A vanished file is forgotten.
func commitFile(ctx context.Context, ledger *Ledger, tool Tool, read fileRead, account, source string) error {
	if read.vanished {
		return wrapLedger(ledger.DeleteFile(read.path))
	}
	if !read.changed {
		return nil
	}
	return wrapLedger(ledger.Transaction(ctx, func(tx *Tx) error {
		for _, u := range read.usage {
			u.Tool, u.Account = tool.Name, account
			if err := tx.Upsert(u, source); err != nil {
				return err
			}
		}
		for _, change := range read.tiers {
			if err := tx.AddTierChange(change); err != nil {
				return err
			}
		}
		return tx.SetFile(read.path, read.row)
	}))
}
```

- [ ] **Step 2: Add `readEach`**, the parallel reader with ordered commits:

```go
// readEach reads the transcripts on every CPU and hands each read to commit
// in list order, so files commit one at a time in the order a serial run
// would. At most two reads per worker wait to be committed, which bounds
// memory on a first run over many large files. commit returns false to stop.
func readEach(
	ctx context.Context,
	ledger *Ledger,
	tool Tool,
	paths []string,
	commit func(int, fileRead) bool,
) {
	workers := runtime.GOMAXPROCS(0)
	var wg sync.WaitGroup
	defer wg.Wait() // after cancel below: deferred calls run last-in first-out
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	results := make([]chan fileRead, len(paths))
	for i := range results {
		results[i] = make(chan fileRead, 1)
	}
	slots := make(chan struct{}, 2*workers)
	jobs := make(chan int)
	wg.Go(func() {
		defer close(jobs)
		for i := range paths {
			select {
			case slots <- struct{}{}:
			case <-ctx.Done():
				return
			}
			select {
			case jobs <- i:
			case <-ctx.Done():
				return
			}
		}
	})
	for range workers {
		wg.Go(func() {
			for i := range jobs {
				read := fileRead{path: paths[i], err: ctx.Err()}
				if read.err == nil {
					read = readFile(ledger, tool, paths[i])
				}
				results[i] <- read
			}
		})
	}
	for i := range paths {
		var read fileRead
		select {
		case read = <-results[i]:
		case <-ctx.Done():
			return
		}
		<-slots
		if !commit(i, read) {
			return
		}
	}
}
```

(`sync.WaitGroup.Go` exists since Go 1.25; the module is on 1.26.)

- [ ] **Step 3: Use them in `ingestLocked`.** Replace the `tools:` loop (from `tools:` through the closing brace of `for _, tool := range Tools`) with:

```go
	for _, tool := range Tools {
		if tool.Source == nil {
			continue
		}
		account := tool.Source.Account(paths.Home)
		transcripts, listErr := tool.Source.Transcripts(paths.Home)
		if listErr != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", tool.Name, listErr))
		}
		stop := false
		readEach(ctx, ledger, tool, transcripts, func(i int, read fileRead) bool {
			if opts.Progress != nil {
				opts.Progress.Step(fmt.Sprintf(
					"%d/%d %s", i+1, len(transcripts), clipRunes(filepath.Base(filepath.Dir(read.path)), 40),
				))
			}
			source := "sweep"
			if opts.Event == "SessionEnd" && tool.Source.Session(read.path, opts.Transcript) {
				source = "session"
			}
			err := read.err
			if err == nil {
				err = commitFile(ctx, ledger, tool, read, account, source)
			}
			switch {
			case err == nil && read.changed:
				files++
				rows += len(read.usage)
			case err != nil && errors.Is(err, errLedger):
				// The ledger itself is unusable (locked beyond the timeout, disk
				// full): stop here, files committed so far stay committed.
				errs = append(errs, fmt.Sprintf("%s: %v", read.path, err))
				stop = true
				return false
			case err != nil:
				errs = append(errs, fmt.Sprintf("%s: %v", read.path, err))
			}
			return true
		})
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		if stop {
			break
		}
	}
	if _, err := ledger.ResolveTiers(ctx); err != nil {
		errs = append(errs, "service tiers: "+err.Error())
	}
```

Add imports `crypto/sha256`, `encoding/hex`, `runtime`, `sync`. Remove the now-unused `tools:` label.

- [ ] **Step 4: Check Claude ingest is unchanged** against a scratch copy of the real ledger (read-only source, scratch ledger):

```bash
SP=<scratchpad>/t2 && rm -rf $SP && mkdir -p $SP
cp ~/.local/share/claude-costs/ledger.sqlite $SP/ledger.sqlite
go build -o $SP/wb ./cmd/workbench
export CLAUDE_COSTS_LEDGER=$SP/ledger.sqlite CLAUDE_COSTS_STATE=$SP/state CLAUDE_COSTS_RATES=$SP/rates.json
sqlite3 $SP/ledger.sqlite "SELECT COUNT(*), SUM(input+output+cache_write_5m+cache_write_1h+cache_read) FROM responses" > $SP/before
$SP/wb costs ingest --worker
sqlite3 $SP/ledger.sqlite "SELECT value FROM meta WHERE key='schema_version'; SELECT COUNT(*), SUM(input+output+cache_write_5m+cache_write_1h+cache_read) FROM responses WHERE tool='claude'"
cat $SP/before
```

Expected: version `3`; the Claude count and token sum are at least the `before` values (new sessions since the copy only add). `$SP/state/ingest.log` ends with a `manual: N files, M records` line and no `error:` lines.

- [ ] **Step 5: Gates and commit.**

```bash
git add internal/costs/ingest.go
git commit -m "feat(costs): ingest reads transcripts in parallel, keeps per-file source state and spots rewritten files" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 3: The Codex source and tier-aware rate lookup

**Files:**
- Create: `internal/costs/codex.go`
- Modify: `internal/costs/source.go` (set the Codex source)
- Modify: `internal/costs/rates.go` (`Lookup`, `longestPrefix`)
- Modify: `internal/costs/report.go` (`ToolStatus.Skipped`)

**Interfaces:**
- Consumes: Task 1 `SplitTier`, `TierChange`, `Usage.TierFrom`, `FileState.Saved/Tiers/cache`; existing `decode`, `firstNonEmpty`, `money`, `notePattern` (claude.go).
- Produces: `codex{}` implementing `Source`; `(codex) Skipped(home string) int`; `codexHookHash(event string, group codexGroup, hook codexHook) string`; `codexTier(raw string) string`; `matchesRate(model, prefix string) bool`; `ToolStatus.Skipped int`.

- [ ] **Step 1: Tier-aware, boundary-aware rate matching** in `internal/costs/rates.go`. Add:

```go
// matchesRate reports whether the rate keyed prefix prices model: both carry
// the same service tier (none for standard), and the prefix is the whole model
// id or is followed by "-" or "@" and a digit (a version within a family, or a
// dated snapshot). So "claude-opus" prices "claude-opus-5-5" and "gpt-5"
// prices "gpt-5-2025-08-07", but "gpt-5" prices neither "gpt-5-mini" nor
// "gpt-5.3-codex-spark", and no standard rate prices a "@fast" model.
func matchesRate(model, prefix string) bool {
	modelBase, modelTier := SplitTier(model)
	prefixBase, prefixTier := SplitTier(prefix)
	if modelTier != prefixTier || !strings.HasPrefix(modelBase, prefixBase) {
		return false
	}
	rest := modelBase[len(prefixBase):]
	return rest == "" ||
		(len(rest) >= 2 && (rest[0] == '-' || rest[0] == '@') && rest[1] >= '0' && rest[1] <= '9')
}
```

In `Lookup` replace `if !strings.HasPrefix(model, prefix) {` with `if !matchesRate(model, prefix) {`. In `longestPrefix` replace `strings.HasPrefix(model, prefix)` with `matchesRate(model, prefix)`. Update the `sourceOrder` comment: "the first source with a matching rate decides, and the longest prefix decides within a source; see matchesRate."

- [ ] **Step 2: Create `internal/costs/codex.go`:**

```go
package costs

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/pelletier/go-toml/v2"
)

// codex is the Codex source: the rollout JSONL files under
// $CODEX_HOME/sessions/YYYY/MM/DD and $CODEX_HOME/archived_sessions, where
// CODEX_HOME defaults to ~/.codex. Each line is {timestamp, type, payload}.
// Codex 0.153 and later write a token_usage_record per response; older files
// have only token_count events, and a legacy fork copies its parent's lines,
// usage included, into its own file.
type codex struct{}

func (codex) Name() string { return "codex" }

func codexDir(home string) string {
	if dir := os.Getenv("CODEX_HOME"); dir != "" {
		return dir
	}
	return filepath.Join(home, ".codex")
}

// Transcripts lists every *.jsonl under sessions/ and archived_sessions/.
func (codex) Transcripts(home string) ([]string, error) {
	var files []string
	for _, root := range []string{"sessions", "archived_sessions"} {
		found, err := codexWalk(filepath.Join(codexDir(home), root), ".jsonl")
		if err != nil {
			return files, err
		}
		files = append(files, found...)
	}
	sort.Strings(files)
	return files, nil
}

// Skipped counts the compressed rollouts (.jsonl.zst) Workbench does not read.
func (codex) Skipped(home string) int {
	n := 0
	for _, root := range []string{"sessions", "archived_sessions"} {
		found, _ := codexWalk(filepath.Join(codexDir(home), root), ".zst")
		n += len(found)
	}
	return n
}

func codexWalk(root, ext string) ([]string, error) {
	if info, err := os.Stat(root); err != nil || !info.IsDir() {
		return nil, nil
	}
	var files []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		switch {
		case err != nil:
			if entry != nil && entry.IsDir() {
				return fs.SkipDir
			}
			return nil
		case filepath.Ext(path) != ext:
		case entry.Type().IsRegular():
			files = append(files, path)
		case entry.Type()&fs.ModeSymlink != 0:
			if info, statErr := os.Stat(path); statErr == nil && info.Mode().IsRegular() {
				files = append(files, path)
			}
		}
		return nil
	})
	return files, err
}

// Session reports whether file is the rollout the hook named. Codex's
// SessionEnd fires for root threads only, so a subagent's rows are sweep rows.
func (codex) Session(file, transcript string) bool {
	return transcript != "" && file == transcript
}

// Account is the email claim of the ID token Codex keeps in auth.json. The
// token is only decoded, never verified; the access and refresh tokens are
// never read. API-key and keyring sign-ins have no email there.
func (codex) Account(home string) string {
	var auth struct {
		Tokens struct {
			IDToken string `json:"id_token"`
		} `json:"tokens"`
	}
	raw, err := os.ReadFile(filepath.Join(codexDir(home), "auth.json"))
	if err != nil || !decode(raw, &auth) {
		return "unknown"
	}
	parts := strings.Split(auth.Tokens.IDToken, ".")
	if len(parts) != 3 {
		return "unknown"
	}
	payload, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(parts[1], "="))
	if err != nil {
		return "unknown"
	}
	var claims struct {
		Email   string `json:"email"`
		Profile struct {
			Email string `json:"email"`
		} `json:"https://api.openai.com/profile"`
	}
	if !decode(payload, &claims) {
		return "unknown"
	}
	return firstNonEmpty(claims.Email, claims.Profile.Email, "unknown")
}

// codexState is a rollout's state at the stored offset, saved between runs.
type codexState struct {
	Thread  string `json:"thread,omitempty"`   // this file's thread: the first session_meta id
	Root    string `json:"root,omitempty"`     // the root thread, when this is a subagent's file
	Cwd     string `json:"cwd,omitempty"`      // the latest working directory
	Model   string `json:"model,omitempty"`    // the latest turn_context model
	Tier    string `json:"tier,omitempty"`     // the latest service tier, stored name
	TierSet bool   `json:"tier_set,omitempty"` // whether this file has named a tier
	Records bool   `json:"records,omitempty"`  // whether this file has a token_usage_record of its own
	Total   string `json:"total,omitempty"`    // the last token_count running total, canonical JSON
}

func codexStateOf(file *FileState) *codexState {
	if state, ok := file.cache.(*codexState); ok {
		return state
	}
	state := &codexState{}
	if len(file.Saved) > 0 {
		_ = json.Unmarshal(file.Saved, state) // damaged state reads as empty
	}
	file.cache = state
	return state
}

func (s *codexState) save(file *FileState) {
	file.Saved, _ = json.Marshal(s)
}

// row is a usage row of this file at time ts, keyed id. A file that has named
// no tier of its own and belongs to a subagent is priced by its root's tier.
func (s *codexState) row(ts, id string) Usage {
	when, _ := time.Parse(time.RFC3339Nano, ts)
	u := Usage{
		Tool:      "codex",
		RequestID: id,
		Model:     firstNonEmpty(s.Model, "unknown"),
		Project:   firstNonEmpty(s.Cwd, "unknown"),
		Session:   s.Thread,
		Time:      when,
	}
	switch {
	case s.TierSet && s.Tier != "":
		u.Model += "@" + s.Tier
	case !s.TierSet && s.Root != "" && s.Root != s.Thread:
		u.TierFrom = s.Root
	}
	return u
}

// codexTier is the stored name of a Codex service tier: priority (renamed
// Fast mode on 2026-07-30) and fast are "fast", the standard tier is "".
func codexTier(raw string) string {
	switch tier := strings.ToLower(strings.TrimSpace(raw)); tier {
	case "priority", "fast":
		return "fast"
	case "", "default", "auto", "standard":
		return ""
	default:
		return tier
	}
}

// codexCounts are a response's token counts. Cached and cache-write input are
// part of input_tokens; reasoning is part of output_tokens.
type codexCounts struct {
	Input      int64 `json:"input_tokens"`
	Cached     int64 `json:"cached_input_tokens"`
	CacheWrite int64 `json:"cache_write_input_tokens"`
	Output     int64 `json:"output_tokens"`
}

func (c codexCounts) fill(u *Usage) {
	u.Input = max(c.Input-c.Cached-c.CacheWrite, 0)
	u.CacheRead, u.CacheWrite5m, u.Output = c.Cached, c.CacheWrite, c.Output
}

// codexMarks are the line types Parse reads; every other line, almost all of
// a rollout's bytes, is skipped without decoding.
var codexMarks = [][]byte{
	[]byte("session_meta"), []byte("turn_context"), []byte("token_usage_record"),
	[]byte("token_count"), []byte("thread_settings_applied"),
}

// Parse returns the usage rows of one rollout line. A token_usage_record of
// this file's thread is one row keyed by its response id; a copied record
// (another thread's) is skipped, its original being in the parent's file. A
// file without records is read from its token_count events: one row per
// change of the running total that is not a synthetic estimate, keyed by a
// digest of the event's counts and rate limits, so a fork's copy of an event
// lands on the original's row.
func (codex) Parse(line []byte, file *FileState) []Usage {
	if !slices.ContainsFunc(codexMarks, func(mark []byte) bool { return bytes.Contains(line, mark) }) {
		return nil
	}
	var rec struct {
		Timestamp string          `json:"timestamp"`
		Type      string          `json:"type"`
		Payload   json.RawMessage `json:"payload"`
	}
	if !decode(line, &rec) || len(rec.Payload) == 0 {
		return nil
	}
	state := codexStateOf(file)
	switch rec.Type {
	case "session_meta":
		if state.Thread != "" {
			return nil // a parent's, copied by a fork
		}
		var meta struct {
			ID        string          `json:"id"`
			SessionID string          `json:"session_id"`
			Cwd       string          `json:"cwd"`
			Source    json.RawMessage `json:"source"`
		}
		if !decode(rec.Payload, &meta) || meta.ID == "" {
			return nil
		}
		state.Thread, state.Cwd = meta.ID, firstNonEmpty(meta.Cwd, state.Cwd)
		state.Root = codexRoot(meta.ID, meta.SessionID, meta.Source)
		state.save(file)
	case "turn_context":
		var turn struct {
			Model string `json:"model"`
			Cwd   string `json:"cwd"`
		}
		if decode(rec.Payload, &turn) && (turn.Model != "" || turn.Cwd != "") {
			state.Model = firstNonEmpty(turn.Model, state.Model)
			state.Cwd = firstNonEmpty(turn.Cwd, state.Cwd)
			state.save(file)
		}
	case "token_usage_record":
		var record struct {
			ThreadID   string      `json:"thread_id"`
			SessionID  string      `json:"session_id"`
			ResponseID string      `json:"response_id"`
			Usage      codexCounts `json:"usage"`
		}
		if !decode(rec.Payload, &record) || record.ResponseID == "" ||
			(state.Thread != "" && record.ThreadID != state.Thread) {
			return nil
		}
		subagent := record.SessionID != "" && record.SessionID != record.ThreadID
		if !state.Records || (state.Root == "" && subagent) {
			state.Records = true
			if state.Root == "" && subagent {
				state.Root = record.SessionID
			}
			state.save(file)
		}
		u := state.row(rec.Timestamp, record.ResponseID)
		record.Usage.fill(&u)
		return []Usage{u}
	case "event_msg":
		return codexEvent(rec.Timestamp, rec.Payload, state, file)
	}
	return nil
}

func codexEvent(ts string, payload json.RawMessage, state *codexState, file *FileState) []Usage {
	var head struct {
		Type string `json:"type"`
	}
	if !decode(payload, &head) {
		return nil
	}
	switch head.Type {
	case "thread_settings_applied":
		var event struct {
			ThreadID string `json:"thread_id"`
			Settings struct {
				Tier *string `json:"service_tier"`
			} `json:"thread_settings"`
		}
		if !decode(payload, &event) || event.Settings.Tier == nil {
			return nil
		}
		state.Tier, state.TierSet = codexTier(*event.Settings.Tier), true
		state.save(file)
		when, _ := time.Parse(time.RFC3339Nano, ts)
		file.Tiers = append(file.Tiers, TierChange{
			Thread: firstNonEmpty(event.ThreadID, state.Thread), Time: when, Tier: state.Tier,
		})
	case "token_count":
		if state.Records {
			return nil
		}
		var event struct {
			Info *struct {
				Total json.RawMessage `json:"total_token_usage"`
				Last  json.RawMessage `json:"last_token_usage"`
			} `json:"info"`
			RateLimits json.RawMessage `json:"rate_limits"`
		}
		if !decode(payload, &event) || event.Info == nil || len(event.Info.Total) == 0 ||
			len(event.Info.Last) == 0 {
			return nil
		}
		total := canonicalJSON(event.Info.Total)
		if total == state.Total {
			return nil // re-emitted for a rate-limit update
		}
		state.Total = total
		state.save(file)
		var last codexCounts
		if !decode(event.Info.Last, &last) || (last.Input == 0 && last.Output == 0) {
			return nil // a synthetic estimate after compaction
		}
		sum := sha256.Sum256([]byte(total + "\n" + canonicalJSON(event.Info.Last) + "\n" +
			canonicalJSON(event.RateLimits)))
		u := state.row(ts, "tc:"+hex.EncodeToString(sum[:]))
		last.fill(&u)
		return []Usage{u}
	}
	return nil
}

// codexRoot is the root thread of a subagent's file: the session id when it
// differs from the thread id, else the spawning parent; "" for a root thread.
func codexRoot(id, sessionID string, source json.RawMessage) string {
	if sessionID != "" && sessionID != id {
		return sessionID
	}
	var spawned struct {
		Subagent struct {
			ThreadSpawn struct {
				Parent string `json:"parent_thread_id"`
			} `json:"thread_spawn"`
		} `json:"subagent"`
	}
	if len(source) > 0 && source[0] == '{' && decode(source, &spawned) {
		return spawned.Subagent.ThreadSpawn.Parent
	}
	return ""
}

// canonicalJSON re-encodes raw with sorted keys and exact numbers, so the same
// value gives the same text however Codex spaced or ordered it.
func canonicalJSON(raw json.RawMessage) string {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if decoder.Decode(&value) != nil {
		return string(raw)
	}
	out, err := json.Marshal(value)
	if err != nil {
		return string(raw)
	}
	return string(out)
}

// Observations is nil: Codex records no cost to calibrate against.
func (codex) Observations(string) []Observation { return nil }

// codexEvents are Codex's hook events and the labels its trust keys use.
var codexEvents = map[string]string{
	"PreToolUse": "pre_tool_use", "PermissionRequest": "permission_request",
	"PostToolUse": "post_tool_use", "PreCompact": "pre_compact", "PostCompact": "post_compact",
	"SessionStart": "session_start", "SessionEnd": "session_end",
	"UserPromptSubmit": "user_prompt_submit", "SubagentStart": "subagent_start",
	"SubagentStop": "subagent_stop", "Stop": "stop", "Interrupt": "interrupt",
}

type codexHook struct {
	Type          string  `json:"type"`
	Command       string  `json:"command"`
	Timeout       *int64  `json:"timeout"`
	Async         bool    `json:"async"`
	StatusMessage *string `json:"statusMessage"`
}

type codexGroup struct {
	Matcher *string     `json:"matcher"`
	Hooks   []codexHook `json:"hooks"`
}

// codexHookHash is the trust hash Codex records for a command hook: the
// SHA-256 of the compact, key-sorted JSON of its normalized identity
// (codex-rs hooks/src/engine/discovery.rs hook_hash and config/src/
// fingerprint.rs version_for_toml). Timeouts are normalized as Codex does,
// and the matcher counts only for events that use one. The same rule is in
// home/private_dot_codex/modify_private_config.toml.tmpl, which writes it.
func codexHookHash(event string, group codexGroup, hook codexHook) string {
	timeout := int64(600)
	if hook.Timeout != nil {
		timeout = *hook.Timeout
	}
	switch event {
	case "SessionEnd", "Interrupt":
		if hook.Timeout == nil {
			timeout = 1
		}
		timeout = min(max(timeout, 1), 3)
	default:
		timeout = max(timeout, 1)
	}
	handler := map[string]any{
		"type": "command", "command": hook.Command, "timeout": timeout, "async": hook.Async,
	}
	if hook.StatusMessage != nil {
		handler["statusMessage"] = *hook.StatusMessage
	}
	identity := map[string]any{"event_name": codexEvents[event], "hooks": []any{handler}}
	if group.Matcher != nil && !slices.Contains([]string{"UserPromptSubmit", "Stop", "Interrupt"}, event) {
		identity["matcher"] = *group.Matcher
	}
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetEscapeHTML(false)
	_ = encoder.Encode(identity)
	sum := sha256.Sum256(bytes.TrimSuffix(buf.Bytes(), []byte("\n")))
	return "sha256:" + hex.EncodeToString(sum[:])
}

// Hooks reports, for each session event, whether hooks.json runs the ingest
// hook and config.toml trusts that exact hook: Codex ignores an untrusted one.
func (codex) Hooks(home string) map[string]bool {
	dir := codexDir(home)
	path := filepath.Join(dir, "hooks.json")
	var file struct {
		Hooks map[string][]codexGroup `json:"hooks"`
	}
	if raw, err := os.ReadFile(path); err == nil {
		decode(raw, &file)
	}
	var config struct {
		Hooks struct {
			State map[string]struct {
				Enabled     *bool  `toml:"enabled"`
				TrustedHash string `toml:"trusted_hash"`
			} `toml:"state"`
		} `toml:"hooks"`
	}
	if raw, err := os.ReadFile(filepath.Join(dir, "config.toml")); err == nil {
		_ = toml.Unmarshal(raw, &config) // an unreadable config trusts nothing
	}
	out := map[string]bool{}
	for _, event := range []string{"SessionStart", "SessionEnd"} {
		trusted := false
		for gi, group := range file.Hooks[event] {
			for hi, hook := range group.Hooks {
				if hook.Type != "command" || !strings.Contains(hook.Command, "workbench") ||
					!strings.HasSuffix(hook.Command, " costs ingest") {
					continue
				}
				state := config.Hooks.State[fmt.Sprintf("%s:%s:%d:%d", path, codexEvents[event], gi, hi)]
				trusted = trusted || ((state.Enabled == nil || *state.Enabled) &&
					state.TrustedHash == codexHookHash(event, group, hook))
			}
		}
		out[event] = trusted
	}
	return out
}

// PricingURL is OpenAI's price page in Markdown; CODEX_COSTS_PRICING_URL
// replaces it for checks that must not reach the network.
func (codex) PricingURL() string {
	if url := os.Getenv("CODEX_COSTS_PRICING_URL"); url != "" {
		return url
	}
	return "https://developers.openai.com/api/docs/pricing.md"
}

// codexBuiltin is the OpenAI price page read on 2026-10-01, short-context
// columns: input, output, cache write, cache read per million tokens; a "-"
// price was filled with the input price. A tier row is "<model>@<tier>". The
// official card overrides these at runtime and the overrides file wins.
var codexBuiltin = map[string][4]float64{
	"gpt-6-astra": {10, 50, 12.5, 1}, "gpt-6.1-sol": {2, 10, 2.5, 0.1},
	"gpt-6-luna": {0.1, 0.5, 0.125, 0.01}, "gpt-6-sol": {2, 10, 2.5, 0.2},
	"gpt-5.6-sol": {4, 20, 5, 0.4}, "gpt-5.6-terra": {2, 12, 2.5, 0.2},
	"gpt-5.6-luna": {0.2, 1.2, 0.25, 0.02}, "gpt-5.5": {5, 30, 5, 0.5},
	"gpt-5.5-pro": {30, 180, 30, 30}, "gpt-5.4": {2.5, 15, 2.5, 0.25},
	"gpt-5.4-mini": {0.75, 4.5, 0.75, 0.075}, "gpt-5.4-nano": {0.2, 1.25, 0.2, 0.02},
	"gpt-5.4-pro": {30, 180, 30, 30}, "gpt-5.2": {1.75, 14, 1.75, 0.175},
	"gpt-5.2-pro": {21, 168, 21, 21}, "gpt-5.1": {1.25, 10, 1.25, 0.125},
	"gpt-5": {1.25, 10, 1.25, 0.125}, "gpt-5-mini": {0.25, 2, 0.25, 0.025},
	"gpt-5-nano": {0.05, 0.4, 0.05, 0.005}, "gpt-5-pro": {15, 120, 15, 15},
	"gpt-6-astra@flex": {5, 25, 6.25, 0.5}, "gpt-6.1-sol@flex": {1, 5, 1.25, 0.05},
	"gpt-6-luna@flex": {0.05, 0.25, 0.0625, 0.005}, "gpt-6-sol@flex": {1, 5, 1.25, 0.1},
	"gpt-5.6-sol@flex": {2, 10, 2.5, 0.2}, "gpt-5.6-terra@flex": {1, 6, 1.25, 0.1},
	"gpt-5.6-luna@flex": {0.1, 0.6, 0.125, 0.01}, "gpt-5.5@flex": {2.5, 15, 2.5, 0.25},
	"gpt-5.5-pro@flex": {15, 90, 15, 15}, "gpt-5.4@flex": {1.25, 7.5, 1.25, 0.13},
	"gpt-5.4-mini@flex": {0.375, 2.25, 0.375, 0.0375}, "gpt-5.4-nano@flex": {0.1, 0.625, 0.1, 0.01},
	"gpt-5.4-pro@flex": {15, 90, 15, 15}, "gpt-5.2@flex": {0.875, 7, 0.875, 0.0875},
	"gpt-5.1@flex": {0.625, 5, 0.625, 0.0625}, "gpt-5@flex": {0.625, 5, 0.625, 0.0625},
	"gpt-5-mini@flex": {0.125, 1, 0.125, 0.0125}, "gpt-5-nano@flex": {0.025, 0.2, 0.025, 0.0025},
	"gpt-6-astra@fast": {20, 100, 25, 2}, "gpt-6.1-sol@fast": {4, 20, 5, 0.2},
	"gpt-6-luna@fast": {0.2, 1, 0.25, 0.02}, "gpt-6-sol@fast": {4, 20, 5, 0.4},
	"gpt-5.6-sol@fast": {8, 40, 10, 0.8}, "gpt-5.6-terra@fast": {4, 24, 5, 0.4},
	"gpt-5.6-luna@fast": {0.4, 2.4, 0.5, 0.04}, "gpt-5.5@fast": {12.5, 75, 12.5, 1.25},
	"gpt-5.4@fast": {5, 30, 5, 0.5}, "gpt-5.4-mini@fast": {1.5, 9, 1.5, 0.15},
	"gpt-5.2@fast": {3.5, 28, 3.5, 0.35}, "gpt-5.1@fast": {2.5, 20, 2.5, 0.25},
	"gpt-5@fast": {2.5, 20, 2.5, 0.25}, "gpt-5-mini@fast": {0.45, 3.6, 0.45, 0.045},
	"gpt-6-astra@ultrafast": {60, 300, 75, 6},
}

func (codex) Builtin() RateCard {
	card := RateCard{}
	for prefix, v := range codexBuiltin {
		card[prefix] = Rate{
			Input: v[0], Output: v[1], CacheWrite5m: v[2], CacheWrite1h: v[2], CacheRead: v[3],
			Source: "builtin",
		}
	}
	return card
}

// codexTables are the price page's tables Workbench reads, by heading, and
// the tier suffix each gives its rows. Batch is not read: Codex does not use
// the Batch API.
var codexTables = map[string]string{
	"standard pricing data":  "",
	"flex pricing data":      "@flex",
	"fast pricing data":      "@fast",
	"ultrafast pricing data": "@ultrafast",
}

var codexModelPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9.\-]*$`)

// ParsePricing reads the short-context columns of the Standard, Flex, Fast
// and Ultrafast tables. A "-" cached-input or cache-write price bills those
// tokens at the input price. The first row of a model in a table wins.
func (codex) ParsePricing(page string) RateCard {
	card := RateCard{}
	lines := strings.Split(page, "\n")
	suffix, inTable := "", false
	for i := 0; i < len(lines)-1; {
		head, sep := strings.TrimSpace(lines[i]), strings.TrimSpace(lines[i+1])
		if strings.HasPrefix(head, "#") {
			suffix, inTable = "", false
			if s, ok := codexTables[strings.ToLower(strings.TrimSpace(strings.TrimLeft(head, "#")))]; ok {
				suffix, inTable = s, true
			}
		}
		if !strings.HasPrefix(head, "|") || !strings.HasPrefix(sep, "|") ||
			strings.Trim(sep, "|-: ") != "" {
			i++
			continue
		}
		var headers []string
		for h := range strings.SplitSeq(strings.Trim(head, "|"), "|") {
			headers = append(headers, strings.ToLower(strings.TrimSpace(h)))
		}
		i += 2
		if !inTable {
			continue
		}
		column := func(name string) int { return slices.Index(headers, "short context "+name) }
		ci, cc, cw, co := column("input"), column("cached input"), column("cache writes"), column("output")
		if ci < 0 || co < 0 {
			continue
		}
		for ; i < len(lines) && strings.HasPrefix(strings.TrimSpace(lines[i]), "|"); i++ {
			var cells []string
			for c := range strings.SplitSeq(strings.Trim(strings.TrimSpace(lines[i]), "|"), "|") {
				cells = append(cells, strings.TrimSpace(c))
			}
			model := strings.ToLower(strings.TrimSpace(notePattern.ReplaceAllString(cells[0], "")))
			if !codexModelPattern.MatchString(model) {
				continue
			}
			key := model + suffix
			if _, seen := card[key]; seen {
				continue
			}
			in, okIn := money(cells, ci)
			out, okOut := money(cells, co)
			if !okIn || !okOut {
				continue
			}
			rate := Rate{Input: in, Output: out, CacheWrite5m: in, CacheWrite1h: in, CacheRead: in}
			if v, ok := money(cells, cw); ok {
				rate.CacheWrite5m, rate.CacheWrite1h = v, v
			}
			if v, ok := money(cells, cc); ok {
				rate.CacheRead = v
			}
			card[key] = rate
		}
	}
	return card
}
```

- [ ] **Step 3: Set the source** in `internal/costs/source.go`: `{Name: "codex", Title: "Codex", Source: codex{}},`. Update the `Tools` comment to "Tools is every tab, in order." and the `Name()` comment on the interface to `// "claude", "codex"`.

- [ ] **Step 4: Report skipped compressed rollouts.** In `internal/costs/report.go` add to `ToolStatus`:

```go
	Skipped     int             `json:"skipped_files,omitempty"` // transcripts the source cannot read (compressed)
```

and in `Status`, after `status.Hooks = tool.Source.Hooks(paths.Home)`:

```go
			if counter, ok := tool.Source.(interface{ Skipped(string) int }); ok {
				status.Skipped = counter.Skipped(paths.Home)
			}
```

- [ ] **Step 5: Build the fixture.** Write `<scratchpad>/codex-fixture.py` (not committed). It builds a scratch `CODEX_HOME` covering every Review Focus case; the files are named so the subagent's file sorts before its root's:

```python
#!/usr/bin/env python3
"""Build a scratch CODEX_HOME for the Codex ingest checks."""
import json, os, sys

home = sys.argv[1]
day = os.path.join(home, "sessions", "2026", "09", "10")
os.makedirs(day, exist_ok=True)
os.makedirs(os.path.join(home, "archived_sessions"), exist_ok=True)
open(os.path.join(home, "archived_sessions", "old.jsonl.zst"), "wb").write(b"\0")

def ts(minute):
    return "2026-09-10T10:%02d:00.000Z" % minute

def line(minute, kind, payload):
    return json.dumps({"timestamp": ts(minute), "type": kind, "payload": payload}, separators=(",", ":"))

def usage(i, c, w, o):
    return {"input_tokens": i, "cached_input_tokens": c, "cache_write_input_tokens": w,
            "output_tokens": o, "reasoning_output_tokens": 0, "total_tokens": i + o}

def record(minute, thread, session, resp, u):
    return line(minute, "token_usage_record", {"thread_id": thread, "session_id": session,
                "turn_id": "t", "response_id": resp, "usage": u})

def settings(minute, thread, tier):
    return line(minute, "event_msg", {"type": "thread_settings_applied", "thread_id": thread,
                "thread_settings": {"model": "gpt-5.6-sol", "service_tier": tier}})

def tc(minute, total, last, used):
    return line(minute, "event_msg", {"type": "token_count",
                "info": {"total_token_usage": total, "last_token_usage": last, "model_context_window": 258400},
                "rate_limits": {"primary": {"used_percent": used, "window_minutes": 300}}})

def write(name, lines):
    with open(os.path.join(day, name), "w") as f:
        f.write("\n".join(lines) + "\n")

R, S, L, F, G = "root-0001", "sub-0002", "legacy-0003", "fork-0004", "recfork-0005"
# Root thread: standard until minute 3, then priority (Fast).
write("rollout-2026-09-10T10-01-00-%s.jsonl" % R, [
    line(1, "session_meta", {"id": R, "session_id": R, "cwd": "/w/proj", "source": "cli"}),
    line(1, "turn_context", {"model": "gpt-5.6-sol", "cwd": "/w/proj"}),
    settings(1, R, "default"),
    record(2, R, R, "resp-r1", usage(1000, 400, 100, 50)),
    settings(3, R, "priority"),
    record(4, R, R, "resp-r2", usage(2000, 1500, 0, 80)),
])
# Subagent of the root, no tier of its own; sorts before the root's file.
write("rollout-2026-09-10T10-00-00-%s.jsonl" % S, [
    line(1, "session_meta", {"id": S, "session_id": R, "cwd": "/w/proj",
         "source": {"subagent": {"thread_spawn": {"parent_thread_id": R, "depth": 1}}}}),
    line(1, "turn_context", {"model": "gpt-5.6-sol", "cwd": "/w/proj"}),
    record(2, S, R, "resp-s0", usage(300, 0, 0, 10)),
    record(5, S, R, "resp-s1", usage(400, 100, 0, 20)),
])
# Legacy file: token_count only, with a rate-limit re-emit and a synthetic estimate.
e1 = (usage(100, 0, 0, 10), usage(100, 0, 0, 10))
e2 = (usage(300, 50, 0, 30), usage(200, 50, 0, 20))
synthetic_total = usage(310, 50, 0, 30)
synthetic_last = {"input_tokens": 0, "cached_input_tokens": 0, "cache_write_input_tokens": 0,
                  "output_tokens": 0, "reasoning_output_tokens": 0, "total_tokens": 10}
write("rollout-2026-09-10T10-02-00-%s.jsonl" % L, [
    line(1, "session_meta", {"id": L, "cwd": "/w/legacy", "source": "cli"}),
    line(1, "turn_context", {"model": "gpt-5.4", "cwd": "/w/legacy"}),
    tc(2, e1[0], e1[1], 1.0),
    tc(2, e1[0], e1[1], 2.0),          # same total: a rate-limit re-emit
    tc(3, e2[0], e2[1], 2.0),
    tc(4, synthetic_total, synthetic_last, 2.0),
    line(5, "turn_context", {"model": "gpt-5.3-codex-spark", "cwd": "/w/legacy"}),
    tc(6, usage(500, 50, 0, 40), usage(190, 0, 0, 10), 3.0),  # an unpriced model
])
# Legacy fork of L: copies L's lines (new timestamps), then one event of its own.
write("rollout-2026-09-10T10-30-00-%s.jsonl" % F, [
    line(30, "session_meta", {"id": F, "forked_from_id": L, "cwd": "/w/legacy", "source": "cli"}),
    line(30, "session_meta", {"id": L, "cwd": "/w/legacy", "source": "cli"}),
    line(30, "turn_context", {"model": "gpt-5.4", "cwd": "/w/legacy"}),
    tc(30, e1[0], e1[1], 1.0),
    tc(30, e2[0], e2[1], 2.0),
    tc(31, usage(700, 50, 0, 60), usage(400, 0, 0, 30), 4.0),
])
# Record-era fork of R: a copied record of R's, then its own; tier "scale" is unknown.
write("rollout-2026-09-10T10-40-00-%s.jsonl" % G, [
    line(40, "session_meta", {"id": G, "session_id": G, "forked_from_id": R, "cwd": "/w/proj", "source": "cli"}),
    line(40, "session_meta", {"id": R, "session_id": R, "cwd": "/w/proj", "source": "cli"}),
    record(40, R, R, "resp-r1", usage(1000, 400, 100, 50)),
    line(40, "turn_context", {"model": "gpt-5.6-sol", "cwd": "/w/proj"}),
    settings(41, G, "scale"),
    record(42, G, G, "resp-g1", usage(600, 0, 0, 30)),
])
print(day)
```

- [ ] **Step 6: Run the fixture checks.** Each `Expected` is a check from the Review Focus or the spec:

```bash
SP=<scratchpad>/t3 && rm -rf $SP && mkdir -p $SP/home
go build -o $SP/wb ./cmd/workbench
python3 <scratchpad>/codex-fixture.py $SP/codex
export HOME=$SP/home CODEX_HOME=$SP/codex CLAUDE_CONFIG_DIR=$SP/claude \
  CLAUDE_COSTS_LEDGER=$SP/ledger.sqlite CLAUDE_COSTS_STATE=$SP/state CLAUDE_COSTS_RATES=$SP/rates.json \
  CLAUDE_COSTS_PRICING_URL=http://127.0.0.1:9/none CODEX_COSTS_PRICING_URL=http://127.0.0.1:9/none
$SP/wb costs ingest --worker
Q() { sqlite3 -separator ' ' $SP/ledger.sqlite "$1"; }
```

1. Rows, mapping and models:
   `Q "SELECT request_id, model, input, cache_read, cache_write_5m, output, session_id FROM responses WHERE tool='codex' ORDER BY ts, request_id"`
   Expected exactly 9 rows:
   - `codex:resp-r1 gpt-5.6-sol 500 400 100 50 root-0001`
   - `codex:resp-s0 gpt-5.6-sol 300 0 0 10 sub-0002`
   - `codex:resp-r2 gpt-5.6-sol@fast 500 1500 0 80 root-0001`
   - `codex:resp-s1 gpt-5.6-sol@fast 300 100 0 20 sub-0002`
   - two `codex:tc:…` rows `gpt-5.4` for the legacy file, session `legacy-0003` (inputs 100 and 150), and one `gpt-5.3-codex-spark` row (input 190)
   - one `codex:tc:…` `gpt-5.4` row with session `fork-0004` (input 400)
   - `codex:resp-g1 gpt-5.6-sol@scale 600 0 0 30 recfork-0005`
2. Fork copies (Review Focus 1): the two legacy `gpt-5.4` rows from `legacy-0003` keep minutes 02 and 03:
   `Q "SELECT substr(ts,12,5), session_id FROM responses WHERE model='gpt-5.4' ORDER BY ts"` → `10:02 legacy-0003`, `10:03 legacy-0003`, `10:31 fork-0004`. And `resp-r1` keeps `root-0001` (the copy in `recfork-0005` is skipped).
3. Subagent tier (Review Focus 4): `resp-s1` is `@fast` (its time 10:05 is after the root's switch at 10:03) and `resp-s0` is standard; `Q "SELECT COUNT(*) FROM tier_pending"` → `0`.
4. Resume (Review Focus 2): append a record to the root and re-ingest:
   ```bash
   f=$(ls $CODEX_HOME/sessions/2026/09/10/*root-0001.jsonl)
   printf '%s\n' '{"timestamp":"2026-09-10T10:50:00.000Z","type":"token_usage_record","payload":{"thread_id":"root-0001","session_id":"root-0001","response_id":"resp-r3","usage":{"input_tokens":10,"cached_input_tokens":0,"cache_write_input_tokens":0,"output_tokens":1,"total_tokens":11}}}' >> "$f"
   $SP/wb costs ingest --worker
   Q "SELECT model, project FROM responses WHERE request_id='codex:resp-r3'"
   ```
   Expected `gpt-5.6-sol@fast /w/proj`, and the log's last line reports `1 files, 1 records`.
5. Rewrite (Review Focus 3): re-serialize every line of the legacy file with spaces (as `migrate-rollouts` re-writes it) and re-ingest:
   ```bash
   f=$(ls $CODEX_HOME/sessions/2026/09/10/*legacy-0003.jsonl)
   python3 -c 'import json,sys; p=sys.argv[1]; L=[json.dumps(json.loads(l)) for l in open(p)]; open(p,"w").write("\n".join(L)+"\n")' "$f"
   $SP/wb costs ingest --worker
   Q "SELECT COUNT(*) FROM responses WHERE tool='codex'"
   ```
   Expected `10` (the 9 plus `resp-r3`), unchanged by the rewrite.
6. Pricing and unpriced rows (Review Focus 5): serve a price page and refresh:
   ```bash
   mkdir -p $SP/www && curl -sL --max-time 20 https://developers.openai.com/api/docs/pricing.md -o $SP/www/pricing.md
   (cd $SP/www && python3 -m http.server 8765 >/dev/null 2>&1 &) ; sleep 1
   CODEX_COSTS_PRICING_URL=http://127.0.0.1:8765/pricing.md $SP/wb costs rates --refresh 2>&1 | grep 'rates codex'
   $SP/wb costs --tool codex --by model --json | python3 -c 'import json,sys; d=json.load(sys.stdin)["results"][0]["details"]; print(sorted(d["unpriced"])); print({r["name"]: round(r["cost"],6) for r in d["rows"]})'
   pkill -f 'http.server 8765'
   ```
   Expected: `rates codex: official card updated (N models)` with N ≥ 50; unpriced is exactly `['gpt-5.3-codex-spark', 'gpt-5.6-sol@scale']`; `gpt-5.6-sol@fast` costs `(500*8 + 1500*0.8 + 80*40 + 300*8 + 100*0.8 + 20*40 + 10*8 + 1*40)/1e6`. (If `costs --json` nests its statement differently, read the shape from `internal/cli/costs.go` `costsRun` and adapt the one-liner; the numbers must match.)
7. Status: `$SP/wb costs status --json` shows the codex tool `implemented: true`, `skipped_files: 1`, hooks `SessionStart: false, SessionEnd: false` (no hooks.json in the fixture).

- [ ] **Step 7: Gates and commit.**

```bash
git add internal/costs/codex.go internal/costs/source.go internal/costs/rates.go internal/costs/report.go
git commit -m "feat(costs): record codex responses from rollout files, priced at the service tier they ran at" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 4: Report — one cache-write column, tier labels, Codex notes

**Files:**
- Modify: `internal/costs/source.go` (`Tool.CacheWrites`, `Tool.PriceNote`)
- Modify: `internal/cli/costs.go`

**Interfaces:**
- Consumes: `costs.SplitTier`; `costs.ToolStatus.Skipped`.
- Produces: `Tool.CacheWrites []string`, `Tool.PriceNote string`; `rowName(name string) string` in cli.

- [ ] **Step 1: Tool fields.** In `internal/costs/source.go` extend `Tool`:

```go
type Tool struct {
	Name        string   // "claude", "codex": the ledger's tool column and --tool
	Title       string   // "Claude Code", "Codex": the tab label
	Source      Source
	CacheWrites []string // the report's cache-write column heads: one per cache lifetime the tool bills
	PriceNote   string   // what the figures are, under the total
}
```

and set `Tools`:

```go
var Tools = []Tool{
	{
		Name: "claude", Title: "Claude Code", Source: claude{},
		CacheWrites: []string{"cache 5m", "cache 1h"},
		PriceNote:   "API list-price equivalent, not a subscription bill",
	},
	{
		Name: "codex", Title: "Codex", Source: codex{},
		CacheWrites: []string{"cache write"},
		PriceNote:   "OpenAI API list-price equivalent, not a ChatGPT plan bill",
	},
}
```

- [ ] **Step 2: Cache columns per tool** in `internal/cli/costs.go`. Add `cacheHeads []string` to `reportSpec`. In `reportTable` replace the two `cache 5m` / `cache 1h` columns in the `r.tokens` branch with a loop:

```go
		spec.Cols = append(spec.Cols,
			column{Head: "calls", Right: true, Drop: 1},
			column{Head: "input", Right: true},
			column{Head: "output", Right: true},
		)
		for i, head := range r.cacheHeads {
			spec.Cols = append(spec.Cols, column{Head: head, Right: true, Drop: 3 + i})
		}
		spec.Cols = append(spec.Cols, column{Head: "cache read", Right: true, Drop: 2})
```

In `reportCells` replace the two cache-write cells with:

```go
		out = append(out, commas(row.Calls), human(float64(row.Input)), human(float64(row.Output)))
		if len(r.cacheHeads) == 1 {
			out = append(out, human(float64(row.CacheWrite5m+row.CacheWrite1h)))
		} else {
			out = append(out, human(float64(row.CacheWrite5m)), human(float64(row.CacheWrite1h)))
		}
		out = append(out, human(float64(row.CacheRead)))
```

Set `cacheHeads: tool.CacheWrites` in the `base := reportSpec{…}` of both `costsPage` and `focusBody`. In `costsPage` replace the literal `"API list-price equivalent, not a subscription bill"` with `tool.PriceNote`.

- [ ] **Step 3: Footer note per tool.** Change `writeFooter(b *strings.Builder, width int, report costs.Statement)` to take `tool costs.Tool` first and update its one caller (`writeFooter(&b, width, report)` → `writeFooter(&b, tool, width, report)`). Replace the tokens note with:

```go
	if report.Options.Tokens {
		note := "cache 5m / 1h: prompt tokens written to the cache with that lifetime; "
		if len(tool.CacheWrites) == 1 {
			note = "cache write: prompt tokens written to the cache; "
		}
		notes = append(notes, note+"cache read: prompt tokens served from it")
	}
```

- [ ] **Step 4: Tier labels.** Add next to `shortPath`:

```go
// rowName is how a report row is named: a path under the home directory as
// ~/..., and a model at a service tier as "gpt-5.6-sol (fast)".
func rowName(name string) string {
	if base, tier := costs.SplitTier(name); tier != "" {
		return base + " (" + tier + ")"
	}
	return shortPath(name)
}
```

Use it in place of `shortPath(row.Name)` in `reportTable`, of `shortPath(focus.Name)` in `focusBody`'s title line, and for `session.Model` in `sessionTable` (`rowName(session.Model)`). Leave `shortPath` for paths that are never models (the detail block heading `shortPath(block.Project.Name)` stays).

- [ ] **Step 5: Status lines for Codex.** In the status printer (the loop over `info.Tools`), make the hook state say why it is missing for Codex, and report skipped files:

```go
			for _, event := range []string{"SessionStart", "SessionEnd"} {
				state := "MISSING"
				if tool.Name == "codex" {
					state = "MISSING or untrusted (workbench apply)"
				}
				if tool.Hooks[event] {
					state = "ok"
				}
				hooks = append(hooks, event+" "+state)
			}
			add("hooks"+suffix, strings.Join(hooks, ", "))
			if tool.Skipped > 0 {
				add("skipped"+suffix, fmt.Sprintf("%d compressed rollouts (.jsonl.zst) not read", tool.Skipped))
			}
```

- [ ] **Step 6: See it.** With the Task 3 fixture environment (re-run Task 3 Step 6 setup and checks 1 to 6 if the scratch is gone):

Run: `$SP/wb costs --tool codex --by model --tokens | cat` and `$SP/wb costs --tool codex | cat` and `$SP/wb costs status | cat`
Expected: the token table's heads are `model cost calls input output cache write cache read` (one cache-write column); model rows include `gpt-5.6-sol (fast)` and `gpt-5.6-sol`, while `gpt-5.6-sol@scale` (not a known tier) keeps its raw id and is flagged unpriced; the note under the total reads `OpenAI API list-price equivalent, not a ChatGPT plan bill`; status shows `skipped codex  1 compressed rollouts (.jsonl.zst) not read`. `$SP/wb costs --tool claude --tokens | cat` against a Claude fixture or the scratch ledger of Task 2 still shows `cache 5m` and `cache 1h`.

- [ ] **Step 7: Gates and commit.**

```bash
git add internal/costs/source.go internal/cli/costs.go
git commit -m "feat(costs): the codex tab shows one cache-write column and names each service tier" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 5: Codex ingest hooks for every role, trusted by Workbench

**Files:**
- Create: `home/.chezmoitemplates/codex-hooks.json.tmpl`
- Modify: `home/private_dot_codex/private_hooks.json.tmpl`
- Modify: `home/private_dot_codex/modify_private_config.toml.tmpl`
- Modify: `home/.chezmoitemplates/codex-config.toml.tmpl` (comment only)
- Modify: `scripts/render-check.sh`
- Modify: `docs/superpowers/specs/workbench-contracts.md` (ownership row)

**Interfaces:**
- Consumes: Task 3 `codexHookHash` rule (the Python here must produce the same value); `Hooks(home)` matches commands containing `workbench` and ending ` costs ingest`.

- [ ] **Step 1: The shared hooks body.** Create `home/.chezmoitemplates/codex-hooks.json.tmpl`:

```
{{- $ingest := printf "%s/.local/bin/workbench" .chezmoi.homeDir | replace "'" "'\"'\"'" | printf "'%s' costs ingest" -}}
{
  "hooks": {
    "SessionStart": [
      {
        "hooks": [
          { "type": "command", "command": {{ $ingest | toJson }}, "timeout": 10 }
        ]
      }
    ],
    "SessionEnd": [
      {
        "hooks": [
          { "type": "command", "command": {{ $ingest | toJson }}, "timeout": 3 }
        ]
      }
    ]{{ if .has_work }},
    "UserPromptSubmit": [
      {
        "matcher": "*",
        "hooks": [
          {
            "type": "command",
            "command": {{ printf "%s/.promptctl/bin/codex-prefix-hook" .chezmoi.homeDir | replace "'" "'\"'\"'" | printf "'%s'" | toJson }}
          }
        ]
      }
    ]{{ end }}
  }
}
```

Replace the whole of `home/private_dot_codex/private_hooks.json.tmpl` with:

```
{{ includeTemplate "codex-hooks.json.tmpl" . -}}
```

- [ ] **Step 2: Write the trust.** In `home/private_dot_codex/modify_private_config.toml.tmpl`:

Add `import hashlib` to the imports. After the `MANAGED = …` line add:

```python
# The hooks.json chezmoi writes beside this file, and where Codex finds it.
# Codex runs a hook from hooks.json only when [hooks.state."<path>:<event>:
# <group>:<handler>"] holds its trusted_hash, so every hook Workbench installs
# gets that entry here as a managed value and Codex never asks.
HOOKS = json.loads({{ includeTemplate "codex-hooks.json.tmpl" . | toJson }})
HOOKS_PATH = {{ printf "%s/.codex/hooks.json" .chezmoi.homeDir | toJson }}
HOOK_EVENTS = {
    "PreToolUse": "pre_tool_use", "PermissionRequest": "permission_request",
    "PostToolUse": "post_tool_use", "PreCompact": "pre_compact", "PostCompact": "post_compact",
    "SessionStart": "session_start", "SessionEnd": "session_end",
    "UserPromptSubmit": "user_prompt_submit", "SubagentStart": "subagent_start",
    "SubagentStop": "subagent_stop", "Stop": "stop", "Interrupt": "interrupt",
}
NO_MATCHER_EVENTS = {"UserPromptSubmit", "Stop", "Interrupt"}
```

Before `def main():` add:

```python
def hook_hash(event, group, handler):
    """Codex's trust hash for a command hook: SHA-256 of the compact, key-sorted
    JSON of its normalized identity (codex-rs hooks/src/engine/discovery.rs
    hook_hash; internal/costs/codex.go codexHookHash checks the same rule)."""
    unsupported = set(handler) - {"type", "command", "timeout", "async", "statusMessage"}
    if handler.get("type") != "command" or unsupported:
        raise ValueError("cannot trust hook {} {}: unsupported fields".format(event, sorted(unsupported)))
    timeout = handler.get("timeout")
    if event in ("SessionEnd", "Interrupt"):
        timeout = min(max(1 if timeout is None else timeout, 1), 3)
    else:
        timeout = max(600 if timeout is None else timeout, 1)
    normalized = {"type": "command", "command": handler["command"], "timeout": timeout,
                  "async": bool(handler.get("async", False))}
    if handler.get("statusMessage") is not None:
        normalized["statusMessage"] = handler["statusMessage"]
    identity = {"event_name": HOOK_EVENTS[event], "hooks": [normalized]}
    if group.get("matcher") is not None and event not in NO_MATCHER_EVENTS:
        identity["matcher"] = group["matcher"]
    text = json.dumps(identity, sort_keys=True, separators=(",", ":"), ensure_ascii=False)
    return "sha256:" + hashlib.sha256(text.encode("utf-8")).hexdigest()


def trust_tables():
    """The managed [hooks.state] entry of every hook in HOOKS."""
    lines = []
    for event, groups in HOOKS.get("hooks", {}).items():
        for group_index, group in enumerate(groups):
            for handler_index, handler in enumerate(group.get("hooks", [])):
                key = "{}:{}:{}:{}".format(HOOKS_PATH, HOOK_EVENTS[event], group_index, handler_index)
                lines += [
                    "",
                    "[hooks.state.{}]".format(json.dumps(key, ensure_ascii=False)),
                    "enabled = true",
                    "trusted_hash = {}".format(json.dumps(hook_hash(event, group, handler))),
                ]
    return lines
```

At the top of `main()`, before `managed = tomllib.loads(MANAGED)`, add:

```python
    global MANAGED
    MANAGED = MANAGED.rstrip("\n") + "\n" + "\n".join(trust_tables()) + "\n"
```

- [ ] **Step 3: Comment in `home/.chezmoitemplates/codex-config.toml.tmpl`.** In the header comment, change `the tables Codex rewrites itself (notice.model_migrations, tui.model_availability_nux, hooks,` so the hooks clause reads `hooks, except the trust of the hooks Workbench installs, which the modify template writes,`. Keep the line wrapping of that comment block.

- [ ] **Step 4: render-check.** In `scripts/render-check.sh` delete the line

```bash
    [ -e "$dest/.codex/hooks.json" ] && { echo "LEAK: .codex/hooks.json deployed on personal"; fail=1; }
```

(the personal-role grep for `promptctl` above it still catches the work hook), and before the `claude code status line` block add:

```bash
echo "==> [$role/$mode] codex ingest hooks are installed and trusted"
for event in session_start session_end; do
    grep -q "costs ingest" "$dest/.codex/hooks.json" \
        && grep -q "hooks.json:$event:0:0" "$dest/.codex/config.toml" \
        || { echo "HOOKS FAIL: codex $event hook or its trust missing"; fail=1; }
done
```

- [ ] **Step 5: Contracts row.** In `docs/superpowers/specs/workbench-contracts.md`, in the row naming `private_dot_codex/{AGENTS.md,modify_private_config.toml,private_hooks.json}`, change `All / costs hooks all, Codex hooks and work MCP only work` to `All / costs hooks all (Claude Code and Codex, Codex's trusted by the config merge), promptctl hook and work MCP only work`.

- [ ] **Step 6: Render checks.**

Run: `for r in personal work both; do for m in pinned latest; do for x in "" wsl; do scripts/render-check.sh $r $m $x >/dev/null 2><scratchpad>/rc.err && echo "ok $r $m $x" || { echo "FAIL $r $m $x"; tail -5 <scratchpad>/rc.err; }; done; done; done`
Expected: 12 `ok` lines.

- [ ] **Step 7: The hash matches Codex's own.** Render the modify script and hooks.json the way render-check does. `.chezmoi.homeDir` is the real home directory in any chezmoi run, so the rendered hook paths and commands are this machine's:

```bash
D=<scratchpad>/t5 && rm -rf $D && mkdir -p $D/codex
config=$(scripts/scratch-init.sh both pinned)
chezmoi --config "$config" --source "$PWD" execute-template < home/private_dot_codex/modify_private_config.toml.tmpl > $D/modify.py
chezmoi --config "$config" --source "$PWD" execute-template < home/.chezmoitemplates/codex-hooks.json.tmpl > $D/codex/hooks.json
rm -rf "$(dirname "$config")"
python3 $D/modify.py < ~/.codex/config.toml > $D/config.out
```

Then compare with a short Python check (written to a file, not a heredoc): load `~/.codex/config.toml` and `$D/config.out` with `tomllib`; for every key of the input's `hooks.state` ending in `user_prompt_submit:0:0`, the output's `trusted_hash` must equal the input's; the output's keys missing from the input must be exactly the `session_start` and `session_end` entries.

Expected: the existing approved hook's hash is reproduced (the Python rule matches what Codex recorded when the owner approved the prompt hook), and exactly the two new entries appear. Nothing under `~/.codex` is written.

- [ ] **Step 8: Go agrees with Python.** Point a scratch `CODEX_HOME` at the rendered files, with the trust keys moved to its path:

```bash
sed "s#$HOME/.codex/hooks.json#$D/codex/hooks.json#g" $D/config.out > $D/codex/config.toml
CODEX_HOME=$D/codex CLAUDE_COSTS_LEDGER=$D/l.sqlite CLAUDE_COSTS_STATE=$D/s CLAUDE_COSTS_RATES=$D/r.json \
  <scratchpad>/t3/wb costs status | grep 'hooks codex'
```

Expected `hooks codex  SessionStart ok, SessionEnd ok`.

- [ ] **Step 9: Commit.**

```bash
git add home/.chezmoitemplates/codex-hooks.json.tmpl home/private_dot_codex/private_hooks.json.tmpl \
  home/private_dot_codex/modify_private_config.toml.tmpl home/.chezmoitemplates/codex-config.toml.tmpl \
  scripts/render-check.sh docs/superpowers/specs/workbench-contracts.md
git commit -m "feat(home): codex runs the costs ingest hooks on every role, trusted by the config merge" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 6: Real-data verification and docs

**Files:**
- Modify: `README.md` (one line)
- Modify: `docs/acceptance.md` (observed results)
- Modify: `docs/superpowers/specs/2026-10-01-codex-costs-design.md` (status line, one wording fix)

- [ ] **Step 1: First full ingest of the real rollouts into a scratch ledger, timed.**

```bash
SP=<scratchpad>/t6 && rm -rf $SP && mkdir -p $SP
go build -o $SP/wb ./cmd/workbench
export CLAUDE_COSTS_LEDGER=$SP/ledger.sqlite CLAUDE_COSTS_STATE=$SP/state CLAUDE_COSTS_RATES=$SP/rates.json
/usr/bin/time -v $SP/wb costs ingest --worker 2> $SP/time.txt
grep -E 'Elapsed|Maximum resident' $SP/time.txt; tail -3 $SP/state/ingest.log
```

Expected: no `error:` lines in the log; record the elapsed time and peak memory for the report. A second run right after reads nothing new (`0 files` or only files being written).

- [ ] **Step 2: Independent reference count.** Write `<scratchpad>/codex-reference.py`, which applies the spec's rules in Python without any Workbench code, for one record-era month (`2026/09`) and one legacy month (`2026/05`):

```python
#!/usr/bin/env python3
"""Count Codex responses and tokens per model the way the spec says, for comparison."""
import collections, glob, hashlib, json, os, sys

def canonical(v):
    return json.dumps(v, sort_keys=True, separators=(",", ":"))

month = sys.argv[1]  # e.g. 2026/09
rows = {}
for path in sorted(glob.glob(os.path.expanduser("~/.codex/sessions/%s/**/*.jsonl" % month), recursive=True)):
    thread = model = None
    total = None
    records = False
    for raw in open(path, "rb"):
        if not any(m in raw for m in (b"session_meta", b"turn_context", b"token_usage_record", b"token_count")):
            continue
        try:
            o = json.loads(raw)
        except ValueError:
            continue
        t, p = o.get("type"), o.get("payload") or {}
        if t == "session_meta" and thread is None:
            thread = p.get("id")
        elif t == "turn_context" and p.get("model"):
            model = p["model"]
        elif t == "token_usage_record" and p.get("response_id") and p.get("thread_id") == thread:
            records = True
            rows[p["response_id"]] = (model, p["usage"])
        elif t == "event_msg" and p.get("type") == "token_count" and not records and p.get("info"):
            tot = canonical(p["info"]["total_token_usage"])
            if tot == total:
                continue
            total = tot
            last = p["info"]["last_token_usage"]
            if not last.get("input_tokens") and not last.get("output_tokens"):
                continue
            key = hashlib.sha256((tot + "\n" + canonical(last) + "\n" + canonical(p.get("rate_limits"))).encode()).hexdigest()
            rows.setdefault("tc:" + key, (model, last))
per = collections.defaultdict(lambda: [0, 0, 0])
for model, u in rows.values():
    m = per[model or "unknown"]
    m[0] += 1
    m[1] += max(u.get("input_tokens", 0) - u.get("cached_input_tokens", 0) - u.get("cache_write_input_tokens", 0), 0)
    m[2] += u.get("output_tokens", 0)
for model in sorted(per):
    print(model, *per[model])
```

Note: rows outside the month that a fork in the month copies can make the reference and the ledger differ for legacy months; compare per-model counts and accept differences only where they trace to such a cross-month copy, which you must name.

Compare with the ledger, base model and tier folded together:

```bash
for M in 2026/09 2026/05; do
  python3 <scratchpad>/codex-reference.py $M > $SP/ref-${M//\//-}.txt
  sqlite3 -separator ' ' $SP/ledger.sqlite "SELECT CASE WHEN instr(model,'@')>0 THEN substr(model,1,instr(model,'@')-1) ELSE model END m, COUNT(*), SUM(input), SUM(output) FROM responses WHERE tool='codex' AND substr(ts,1,7)='${M//\//-}' GROUP BY m ORDER BY m" > $SP/led-${M//\//-}.txt
  diff $SP/ref-${M//\//-}.txt $SP/led-${M//\//-}.txt && echo "match $M"
done
```

Expected: `match 2026/09`. For `2026/05`, a match or only differences explained by cross-month fork copies (named in the report). The ledger groups by the row's own month, the reference by the file's directory month; a session spanning midnight at a month end can move a few rows — explain any such row by its id.

- [ ] **Step 3: Re-ingest from empty equals incremental.** Run the ingest again into a fresh scratch ledger and compare `SELECT COUNT(*), SUM(input), SUM(output) FROM responses WHERE tool='codex'` with Step 1's ledger (after a second incremental ingest there). Expected: equal, apart from responses written between the two runs (check `MAX(ts)`).

- [ ] **Step 4: Docs.** `README.md`: change `workbench costs              # what Claude Code spent, one tab per tool` to `workbench costs              # what Claude Code and Codex spent, one tab per tool`. `docs/acceptance.md`: under the costs section, add an observed bullet with the date, stating that Codex rollouts of this WSL host were ingested into a scratch ledger in the measured time, that the per-model counts matched an independent count for one record-era and one legacy month, that a re-ingest from empty matched, and that the Codex hooks were rendered trusted and Codex's recorded hash for the existing approved hook was reproduced (no machine paths, emails or spend). Spec: change the status line to `Status: implemented, 2026-10-01.`, and in section 6 replace "so whatever hooks Workbench installs are trusted on the same apply and none it removed stay trusted" with "so whatever hooks Workbench installs are trusted on the same apply; an entry left for a hook Workbench removed trusts nothing, since that hook no longer exists".

- [ ] **Step 5: Gates, personal-data scan and commit.**

Run the Go gates, then `git diff main --stat` and `git diff main | grep -nE '/home/[a-z]|@gmail|fortressinfosec|\$[0-9]+\.[0-9]{2}'` → no matches outside fixture-free docs.

```bash
git add README.md docs/acceptance.md docs/superpowers/specs/2026-10-01-codex-costs-design.md
git commit -m "docs: codex costs observed on a WSL host" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

- [ ] **Step 6: Live-machine step — owner go-ahead required.** Installing the hooks and trust on this machine is an apply of the live machine (AGENTS.md). Do not run it without the owner's explicit go-ahead. With it: `workbench apply --dry-run --local-build --json` (read the checklist: it must show only the Codex hooks and config change as file changes), then `workbench apply --approve-plan DIGEST --local-build`, then start and end one `codex exec "reply ok"` session and confirm `workbench costs status` shows `hooks codex SessionStart ok, SessionEnd ok` and the ingest log has a `SessionEnd` line.
