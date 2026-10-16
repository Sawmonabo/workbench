# Workbench migrations implementation plan

The code is authoritative: listings below predate review fixes made during implementation.

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Numbered, idempotent migrations that move the files Workbench wrote outside its runtime folders into them: the costs files (automatically, from the import worker, as well as from apply), the shell completion cache and the old `claude-costs` script's cache. Apply shows a `checking migrations` step while planning and `==> migrations:` lines while applying.

**Architecture:** A new `internal/migrate/` package holds the registry, the check/run loop, the probe text and the run record. The costs move lives in `internal/costs/move.go`, which alone opens, checkpoints and copies the ledger. Conflicts are per file, and no rename ever replaces a file. One gate in `internal/costs` runs before every ledger open: the import worker moves the files itself and imports once the ledger has moved; reports refuse until everything has moved. Apply gets one non-fixed `migrations` step, carried by `home/.chezmoiscripts/shared/run_after_05-migrations.sh.tmpl`, which runs the installed `workbench migrate --probe` / `migrate run`. `probeEffects` probes that script alone, first, under its own planning steps.

**Tech Stack:** Go 1.26 (one module), Cobra, `modernc.org/sqlite` (pure Go, `CGO_ENABLED=0`), `golang.org/x/sys` v0.48.0 (`Renameat2`, `RenameatxNp`), chezmoi templates, bash 3.2+ scripts, zsh and bash startup files.

**Spec:** `docs/superpowers/specs/2026-10-14-workbench-migrations-design.md` (as of `46a6fdc`). Read it before starting; this plan argues from it.

**Depends on:** the agent command guard plan (`docs/superpowers/plans/2026-10-14-agent-command-guard.md`), **Task 2**, which adds `operation.PrivateDirectory(root, name string) (string, error)` (guard plan commit `62acf46`; implemented on `feat/agent-guard-and-migrations` at `5a304cb`). This plan uses that one helper for every private folder and adds no other. Don't start until it has landed: `rg -n "func PrivateDirectory" internal/operation` must print one line. Every diff below was made against the implemented guard branch and applies on top of it.

## Global Constraints

- AGENTS.md wins over the TDD default. The only new test is section 9's ledger-move test (Task 3), with red→green steps and its catastrophic risk explained beside it. Every other task verifies with the gates and the manual checks it names.
- Gates, run from the repository root after every code task, for both operating systems:

  ```zsh
  golangci-lint fmt; echo EXIT:$?
  golangci-lint config verify; echo EXIT:$?
  golangci-lint run ./...; echo EXIT:$?
  GOOS=linux golangci-lint run ./...; echo EXIT:$?
  go build ./...; echo EXIT:$?
  GOOS=linux go build ./...; echo EXIT:$?
  go test ./...; echo EXIT:$?
  ```

  Expected: `EXIT:0` each, and `0 issues.` from both lint runs. `golangci-lint fmt` may rewrap long lines; keep its result.
- Any `home/` change also runs `scripts/render-check.sh personal pinned`, `scripts/render-check.sh work pinned` and `scripts/render-check.sh personal pinned wsl`.
- Lint limits: `funlen` 100 lines / 60 statements, `gocognit` 40, `nestif`, `lll` 120 columns, `exhaustive` (a `default` counts), `modernize` (use `errors.AsType`). Split a function rather than disable a linter.
- Commits: conventional subjects. Every body ends with `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`. Never edit `CHANGELOG.md`.
- **Never touch the owner's real files.** This machine has a live 671 MB ledger at `~/.local/share/claude-costs/ledger.sqlite`. Every smoke check runs a freshly built binary under `env -i HOME=<disposable home>`, in a folder made by `S=$(cd "$(mktemp -d)" && pwd -P)` (the `pwd -P` matters: macOS's `/var` is a link, which Workbench's private-folder check refuses). Never run a full `workbench apply` without `--dry-run`, not even in a disposable home: its scripts would provision this Mac's system parts (Homebrew). The apply phase is checked by running the rendered migrations script directly.
- `rg` never takes `-r` and always names a folder; never run a bare `cat`. In zsh read `${pipestatus[1]}` after a pipe. `ls` may be aliased in the owner's shell; use `command ls` where the output is checked.
- Runtime folders come from `operation.Resolve` (`internal/operation/context.go:169-264`): `WORKBENCH_<NAME>_DIR` (absolute), else an absolute `XDG_<NAME>_HOME`/workbench, else the OS default. Existing runtime folders must be `0700`.
- New folders `0700`, new files `0600` (spec section 3).
- A script's probe output is effect lines only: `NAME: TEXT`, `NAME: = TEXT` (nothing to do), `NAME: ? TEXT` (could not check) (`internal/machine/probe.go:24-27`). No migration's probe text holds `; `, which joins one step's lines.
- The diffs below were made against the implemented guard branch (`feat/agent-guard-and-migrations`, a `git archive` of its `HEAD`) and apply there with `git apply`; line numbers cited are that branch's. `AGENTS.md` and `docs/usage.md` are edited by anchor, never by line number.

## Review Focus

The inputs most likely to bite someone that the test doesn't exercise. Each has a manual check in the task named.

1. **An older installed `workbench` under a newer `--local-build`**, which lacks `migrate`. Such a binary answers `migrate --help` with its general help and exit 0, so the script looks for migrate's own usage line. The plan must show "the installed workbench predates migrations", and the apply must report the step blocked and carry on. *Task 8, step 6.*
2. **A hook firing while a migration holds `migrate.lock`.** The worker must log "busy" and must **not** create an empty ledger at the new path. *Task 8, step 5.*
3. **A relative `XDG_DATA_HOME`** that hides the old ledger. The worker must not create a ledger, reports must refuse naming the variable, and nothing is created in the current folder. *Task 8, step 4.*
4. **Conflicts the owner has to settle** (two different overrides files) and ones that aren't really conflicts (the same bytes in both; a foreign `workbench-completion.bash`). `migrate --apply` must exit 4 and name the conflict, never "Nothing to migrate". *Task 8, step 7.*
5. **The ledger on another filesystem for real** (`EXDEV`, not the test's forced copy), and a runtime folder that is not private (the hook says why on stderr; `costs status` fails with the reason). *Task 9 (tmpfs in a Linux container) and Task 8, step 6.*

---

## File map

| File | Task | Responsibility |
|---|---|---|
| `docs/adr/0008-migrations-move-workbench-files.md` | 1 | New ADR: why migrations are allowed, and their limits |
| `AGENTS.md` | 1 | The second greenfield exception; an ownership row |
| `internal/operation/state.go` | 2 | `ReplacePrivateFile` (out of `WritePrivate`), `RenameNoReplace`; uses the guard's `PrivateDirectory` |
| `internal/operation/context.go`, `filesystem_{darwin,linux}.go` | 2 | `Paths.MigrateLock`; the platform no-replace renames |
| `internal/costs/move.go` | 3 | Old and new locations, `CheckFolders`, `MoveFolders`, ledger move/copy/resume, `Gate` |
| `internal/costs/move_test.go` | 3 | The one test: the ledger move never loses a row |
| `internal/costs/ledger.go`, `rates.go`, `ingest.go`, `report.go` | 3, 4 | `Paths.Runtime`; new paths; the gate at every open; the hook's stderr line |
| `internal/cli/costs.go` | 4 | Reports and status go through the gate |
| `internal/migrate/*.go` | 5 | Registry, `Locate`, `Check`, `Run`, `Probe`, `Plan`, the record, three migrations |
| `internal/cli/migrate.go`, `internal/cli/root.go` | 6 | `workbench migrate`, `--apply`, hidden `run` and `--probe` |
| `internal/machine/effects.go`, `probe.go`, `dependencies.go`, `apply.go` | 7 | The `migrations` step, probe-first planning steps, checked and unchecked probe text kept apart, script environment, `costs-ingest` text, the after-apply message |
| `internal/cli/plandetail.go` | 7 | A step's detail shows what was checked and, on a second line, what was not |
| `home/.chezmoiscripts/shared/run_after_05-migrations.sh.tmpl`, `home/.chezmoitemplates/old-locations.sh` | 7 | New script and template |
| `home/.chezmoiscripts/shared/run_after_90-costs-ingest.sh.tmpl` | 7 | Runs the worker with the old locations |
| `home/dot_zshrc.tmpl`, `home/private_dot_bashrc.tmpl` | 7 | Completion cache in Workbench's cache folder |
| `docs/usage.md`, `docs/superpowers/specs/workbench-contracts.md`, `workbench-design.md`, `2026-09-30-claude-costs-ledger-design.md` | 10 | Docs |

---

### Task 1: ADR 0008 and the AGENTS.md exception

**Files:**
- Create: `docs/adr/0008-migrations-move-workbench-files.md`
- Modify: `AGENTS.md` (Development contract, first bullet; Ownership table)

**Interfaces:** none.

- [ ] **Step 1: Write the ADR**

Create `docs/adr/0008-migrations-move-workbench-files.md` in the style of `docs/adr/0005-failed-steps-never-stop-the-others.md`:

```markdown
# 8. Migrations move Workbench's own files into its folders

- Status: Accepted, October 14, 2026
- Amends: [AGENTS.md](../../AGENTS.md), "Greenfield: … No migrations"

## Context

Workbench and the `claude-costs` script before it wrote some files outside
Workbench's runtime folders: the costs ledger, its price caches, the import
log and lock and the owner's price overrides under `claude-costs/` folders,
the shell completion cache in `~/.cache`, and the old script's parse cache.
The ledger is the only lasting record of spend: Claude Code deletes transcripts
after 30 days by default (`cleanupPeriodDays`). A release that only changed the
paths would start an empty ledger and leave the old one unread.

## Options considered

- **A. Numbered, idempotent migrations** in `internal/migrate/`, run by apply
  (one `migrations` step), by `workbench migrate`, and, for the costs files,
  by the import worker itself. Chosen.
- **B. Read the old paths forever.** A compatibility reader and a second path
  for every costs file, which AGENTS.md rules out.
- **C. Leave the files where they are.** Workbench's files stay split between
  its folders and ones named after a retired script, with `0755` folders.

## Decision

- A migration may move files Workbench itself wrote into its runtime folders,
  without losing data, and delete caches and logs Workbench itself wrote.
- Migrations are idempotent, never touch other tools' files, never merge, and
  leave a file where it is when old and new data both exist; every other file
  still moves. No rename ever replaces a file.
- The costs import worker runs the costs move itself, without asking, because
  usage left unread could be lost; it imports once the ledger has moved.
- A migration may read the old locations, including old environment
  variables, only to find what to move. No runtime code reads an old location.
- Released migrations are never renumbered or removed.
- The ledger moves by `rename`, or across filesystems by a checked copy, and
  only when no process has it open.

## Consequences

- One more exception in AGENTS.md's development contract, beside the ledger's
  in-place schema upgrade.
- `CLAUDE_COSTS_LEDGER`, `CLAUDE_COSTS_STATE` and `CLAUDE_COSTS_RATES` no
  longer relocate anything; `WORKBENCH_{DATA,STATE,CONFIG}_DIR` relocate the
  costs files with everything else.
- See the [migrations design](../superpowers/specs/2026-10-14-workbench-migrations-design.md).
```

- [ ] **Step 2: Add the exception to AGENTS.md**

In `AGENTS.md`, the first Development contract bullet ends with `…because transcripts expire and the ledger is the only lasting record of spend.` Append, in the same bullet (re-wrap to 80 columns):

```markdown
  A second exception: numbered migrations in `internal/migrate/` may move
  files Workbench itself wrote into its runtime folders, without losing data,
  and delete caches and logs Workbench itself wrote; they are idempotent,
  never touch other tools' files, never merge, and leave a file where it is
  when old and new data both exist. The costs import worker runs the costs
  move itself, without asking, because usage left unread could be lost
  (ADR 0008).
```

- [ ] **Step 3: Add the ownership row**

In the Ownership table, add a row directly after the row whose first cell is `AI-tool cost ledger, ingest, rates and reports`. If the guard plan added a row after that one, put this one after the guard's row:

```markdown
| Migrations of Workbench's own files | `internal/migrate/` (moves done by the package whose files move) |
```

- [ ] **Step 4: Check and commit**

```zsh
rg -n "A second exception|without asking|Migrations of Workbench" AGENTS.md; echo EXIT:${pipestatus[1]}
git add docs/adr/0008-migrations-move-workbench-files.md AGENTS.md
git commit -F - <<'EOF'
docs(migrate): allow migrations of Workbench's own files

ADR 0008 records why, and AGENTS.md states the exception, that the costs
import worker runs the costs move without asking, and its owner.

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
EOF
echo EXIT:$?
```

Expected: three matching lines, `EXIT:0` twice.

---

### Task 2: Operation helpers: the record write, no-replace renames, the migration lock

The guard plan's Task 2 already added `PrivateDirectory(root, name string) (string, error)`, which makes a private folder under any runtime root. This plan uses it for the costs folders (data, state, config) and the migration record, and adds no second folder helper.

**Files:**
- Modify: `internal/operation/state.go` (`WritePrivate`; after `PrivateDirectory`)
- Modify: `internal/operation/context.go` (beside `type Paths`)
- Modify: `internal/operation/filesystem_darwin.go`, `internal/operation/filesystem_linux.go`

**Interfaces:**
- Consumes: `PrivateDirectory(root, name string) (string, error)` (guard plan Task 2).
- Produces:
  - `func ReplacePrivateFile(path string, data []byte) error`
  - `func RenameNoReplace(from, to string) error`: fails with an error matching `fs.ErrExist` instead of replacing; `EXDEV` passes through.
  - `func (p Paths) MigrateLock() string` → `<state>/locks/migrate.lock`

- [ ] **Step 1: Check the guard's helper is there**

```zsh
rg -n "func PrivateDirectory" internal/operation; echo EXIT:${pipestatus[1]}
```

Expected: one line in `internal/operation/state.go`, `EXIT:0`. With none, stop: the guard plan's Task 2 must land first.

- [ ] **Step 2: Apply the operation changes**

`ReplacePrivateFile` is the tail of `WritePrivate`, moved out unchanged; `WritePrivate` ends by calling it. `RenameNoReplace` uses `renameat2` with `RENAME_NOREPLACE` on Linux and `renameatx_np` with `RENAME_EXCL` on macOS (both in the pinned `golang.org/x/sys` v0.48.0). Where the filesystem rejects the flag it links the new name and unlinks the old, which fails the same way when the target exists; where links are unsupported too, it checks and renames. A stop between the link and the unlink leaves one file under both names, which Task 3 finishes as a move.

```diff
diff --git a/internal/operation/state.go b/internal/operation/state.go
index 5851859..f4d2df7 100644
--- a/internal/operation/state.go
+++ b/internal/operation/state.go
@@ -263,6 +263,15 @@ func (m *Mutation) WritePrivate(path string, data []byte) error {
 	if err := ensurePrivateDirectory(filepath.Dir(path)); err != nil {
 		return err
 	}
+	return ReplacePrivateFile(path, data)
+}
+
+// ReplacePrivateFile writes data to path, in an existing private folder,
+// through a same-folder temporary file: create 0600, write, fsync, rename,
+// fsync the folder. An existing path must already be a private file. It is
+// for Workbench's own small records written outside a [Mutation], such as the
+// migration record; [Mutation.WritePrivate] ends with it.
+func ReplacePrivateFile(path string, data []byte) error {
 	if _, err := os.Lstat(path); err == nil {
 		if err := checkPrivateFile(path); err != nil {
 			return err
@@ -363,6 +372,36 @@ func PrivateDirectory(root, name string) (string, error) {
 	return directory, ensurePrivateDirectory(directory)
 }
 
+// RenameNoReplace renames from to to, and fails with an error matching
+// fs.ErrExist instead of replacing a file already at to. Where the filesystem
+// lacks the no-replace flag, it links to and unlinks from, which fails the
+// same way when to exists; where links are unsupported too, it checks to and
+// renames, which is safe only under the caller's own lock.
+func RenameNoReplace(from, to string) error {
+	err := renameNoReplace(from, to)
+	if !unsupported(err) {
+		return err
+	}
+	if err = os.Link(from, to); err == nil {
+		return os.Remove(from)
+	}
+	if errors.Is(err, fs.ErrExist) || errors.Is(err, syscall.EXDEV) || !unsupported(err) &&
+		!errors.Is(err, syscall.EPERM) {
+		return err
+	}
+	if _, err = os.Lstat(to); err == nil {
+		return &fs.PathError{Op: "rename", Path: to, Err: fs.ErrExist}
+	}
+	return os.Rename(from, to)
+}
+
+// unsupported reports an error a filesystem gives for a call or flag it
+// does not implement.
+func unsupported(err error) bool {
+	return errors.Is(err, syscall.EINVAL) || errors.Is(err, syscall.ENOTSUP) ||
+		errors.Is(err, syscall.EOPNOTSUPP) || errors.Is(err, syscall.ENOSYS)
+}
+
 // MakeParents creates the missing folders above a runtime root. They get 0755,
 // the mode the machine configuration gives its own folders such as ~/.local on
 // Linux, so applying it later changes nothing for a folder that holds Workbench
```

```diff
diff --git a/internal/operation/context.go b/internal/operation/context.go
index 1e1ff5a..5a07e3d 100644
--- a/internal/operation/context.go
+++ b/internal/operation/context.go
@@ -15,6 +15,11 @@ import (
 // holds the workbench command.
 type Paths struct{ Config, Data, State, Cache, Bin string }
 
+// MigrateLock is the lock one run of Workbench's migrations holds while it
+// moves files, whether `workbench migrate` or the costs import worker runs it
+// (see internal/migrate).
+func (p Paths) MigrateLock() string { return filepath.Join(p.State, "locks", "migrate.lock") }
+
 // Scope is what an operation may change: the machine destination or one
 // canonical project root.
 type Scope struct {
```

```diff
diff --git a/internal/operation/filesystem_darwin.go b/internal/operation/filesystem_darwin.go
index 44a0c20..b384af1 100644
--- a/internal/operation/filesystem_darwin.go
+++ b/internal/operation/filesystem_darwin.go
@@ -43,3 +43,8 @@ func privateFilesystem(path string) error {
 	}
 	return nil
 }
+
+// renameNoReplace is renameatx_np with RENAME_EXCL.
+func renameNoReplace(from, to string) error {
+	return unix.RenameatxNp(unix.AT_FDCWD, from, unix.AT_FDCWD, to, unix.RENAME_EXCL)
+}
```

```diff
diff --git a/internal/operation/filesystem_linux.go b/internal/operation/filesystem_linux.go
index 1416015..3b58d77 100644
--- a/internal/operation/filesystem_linux.go
+++ b/internal/operation/filesystem_linux.go
@@ -39,3 +39,8 @@ func privateFilesystem(path string) error {
 		)
 	}
 }
+
+// renameNoReplace is renameat2 with RENAME_NOREPLACE.
+func renameNoReplace(from, to string) error {
+	return unix.Renameat2(unix.AT_FDCWD, from, unix.AT_FDCWD, to, unix.RENAME_NOREPLACE)
+}
```

- [ ] **Step 3: Run the gates**

Run the gates (Global Constraints). Expected: `EXIT:0` each. The existing tests cover `WritePrivate` through apply's state writes; Task 3's test covers `RenameNoReplace`.

- [ ] **Step 4: Commit**

```zsh
git add internal/operation
git commit -F - <<'EOF'
refactor(operation): record writes and no-replace renames outside a mutation

ReplacePrivateFile writes small records outside a mutation, and
WritePrivate ends with it. RenameNoReplace never replaces a file:
renameat2 RENAME_NOREPLACE on Linux, renameatx_np RENAME_EXCL on macOS,
with a link-and-unlink fallback. Paths.MigrateLock names the one
migration lock.

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
EOF
echo EXIT:$?
```

---

### Task 3: The costs move, with the one test

The catastrophic risk (AGENTS.md): silent, unrecoverable loss or corruption of the costs ledger, the only lasting record of spend. Moving it is the riskiest thing this design does, so this is the one test. It covers the reviewer's reproduced failure: a rerun that replaced a ledger at the new path and deleted the set-aside original.

**Files:**
- Create: `internal/costs/move.go`
- Create: `internal/costs/move_test.go`
- Modify: `internal/costs/ledger.go:30-35` (`Paths` gains `Runtime`)

**Interfaces:**
- Consumes: `operation.PrivateDirectory` (guard plan Task 2); `operation.RenameNoReplace`, `operation.Paths.MigrateLock` (Task 2); `operation.TryLock(path) (release func(), held bool, err error)` (`state.go:472`); `OpenLedger(path string, create bool) (*Ledger, error)`; `(*Ledger).Close()`; `Tools`; `firstTool`; `tilde(path, home string) string` (`rates.go:523`); `officialPath` (`rates.go:336`, changed in Task 4).
- Produces (Tasks 4 and 5 rely on these):
  - `type OldPaths struct { Ledger, State, Overrides string; Notes []string; Unresolved string }`
  - `func OldLocations(home string) OldPaths`
  - `func LocationsIn(home string, runtime operation.Paths) Paths`
  - `type FolderState int` with `FoldersDone`, `FoldersPending`, `FoldersUnfinished`, `FoldersConflict`
  - `type FolderCheck struct { State, Ledger FolderState; Text string }`
  - `func CheckFolders(old OldPaths, p Paths) FolderCheck`
  - `func MoveFolders(ctx context.Context, old OldPaths, p Paths) (left []string, err error)`; its errors are `*operation.Error` with `Category` `"busy"` (`ExitBlocked`) or `"conflict"` (`ExitConflict`)
  - `func Gate(ctx context.Context, move bool) error`, and the unexported `gate(ctx, paths, move)` and `errorCategory(err) string`
  - `Paths.Runtime operation.Paths`

- [ ] **Step 1: Give `Paths` its runtime folders**

In `internal/costs/ledger.go`, add one field to `Paths` (Task 4 rewrites its doc comment):

```go
type Paths struct {
	Home      string
	Runtime   operation.Paths // Workbench's runtime folders the paths below sit in
	Ledger    string          // the durable record
	State     string          // ingest.log and ingest.lock
	Overrides string          // manual per-model rates, highest priority
}
```

- [ ] **Step 2: Write the failing test**

Create `internal/costs/move_test.go`. Its file-reading helper is `readBytes`, because `readFile` already exists in the package (`internal/costs/ingest.go:409`).

```go
package costs

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Sawmonabo/workbench/internal/operation"
)

// The ledger is the only lasting record of spend once Claude Code and Codex
// delete their session files. Moving it into Workbench's folders must never
// lose a row or leave a file SQLite reads as corrupt: not when the move
// crosses filesystems, not when a run stops part way and runs again, not while
// another process has the ledger open, never by replacing a ledger that is
// already at the new path or one that came back at the old path, and never by
// leaving the import stopped because another costs file conflicts. Any of
// these would lose spend history silently and for good.
func TestMovingTheLedgerKeepsEveryRow(t *testing.T) {
	for _, c := range []struct {
		name string
		run  func(*testing.T)
	}{
		{"same filesystem", moveSameFilesystem},
		{"copy across filesystems", moveAcrossFilesystems},
		{"stopped after writing the copy", resumeAfterWritingTheCopy},
		{"stopped after setting the original aside", resumeAfterSettingTheOriginalAside},
		{"stopped before deleting the original", resumeBeforeDeletingTheOriginal},
		{"a ledger is back at the old path during an unfinished copy", refuseALedgerBackAtTheOldPath},
		{"open in another process", refuseAnOpenLedger},
		{"old and new ledgers both hold data", refuseTwoLedgers},
		{"a conflicting overrides file leaves the ledger free to move", moveTheLedgerPastAnOverridesConflict},
		{"one ledger under both names after a stopped link", finishALinkedMove},
	} {
		t.Run(c.name, c.run)
	}
}

func moveSameFilesystem(t *testing.T) {
	ctx := context.Background()
	old, paths := moveFixture(t)
	want := seedLedger(t, old.Ledger, "req-1", "req-2")
	folder := filepath.Dir(old.Ledger)
	writeFile(t, filepath.Join(folder, "rates-official.json"), "{}", 0o644)
	writeFile(t, filepath.Join(folder, "rates-official-codex.json"), "{}", 0o644)
	writeFile(t, filepath.Join(folder, "notes.txt"), "mine", 0o644)
	writeFile(t, filepath.Join(old.State, "ingest.log"), "log\n", 0o644)
	writeFile(t, filepath.Join(old.State, "ingest.lock"), "", 0o644)
	writeFile(t, old.Overrides, `{"claude-opus-5": {"input": 1}}`, 0o644)
	left, err := MoveFolders(ctx, old, paths)
	if err != nil {
		t.Fatal(err)
	}
	if len(left) != 1 || filepath.Base(left[0]) != "notes.txt" {
		t.Fatalf("left %v, want only notes.txt", left)
	}
	assertMoved(t, old, paths, want)
	if data, err := os.ReadFile(paths.Overrides); err != nil ||
		string(data) != `{"claude-opus-5": {"input": 1}}` {
		t.Fatalf("overrides not moved byte for byte: %q, %v", data, err)
	}
	for _, gone := range []string{old.State, filepath.Dir(old.Overrides)} {
		if _, err := os.Lstat(gone); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("%s should be gone: %v", gone, err)
		}
	}
}

func moveAcrossFilesystems(t *testing.T) {
	ctx := context.Background()
	old, paths := moveFixture(t)
	want := seedLedger(t, old.Ledger, "req-1", "req-2")
	ensureLedgerFolder(t, paths)
	if err := moveLedger(ctx, old.Ledger, paths.Ledger, true); err != nil {
		t.Fatal(err)
	}
	assertMoved(t, old, paths, want)
}

func resumeAfterWritingTheCopy(t *testing.T) {
	old, paths := moveFixture(t)
	want := seedLedger(t, old.Ledger, "req-1", "req-2")
	ensureLedgerFolder(t, paths)
	writeFile(t, paths.Ledger+".part", "half a copy", 0o600)
	rerun(t, old, paths)
	assertMoved(t, old, paths, want)
}

func resumeAfterSettingTheOriginalAside(t *testing.T) {
	old, paths := moveFixture(t)
	want := seedLedger(t, old.Ledger, "req-1", "req-2")
	ensureLedgerFolder(t, paths)
	vacuumInto(t, old.Ledger, paths.Ledger+".part")
	rename(t, old.Ledger, old.Ledger+".moved")
	rerun(t, old, paths)
	assertMoved(t, old, paths, want)
}

func resumeBeforeDeletingTheOriginal(t *testing.T) {
	old, paths := moveFixture(t)
	want := seedLedger(t, old.Ledger, "req-1", "req-2")
	ensureLedgerFolder(t, paths)
	vacuumInto(t, old.Ledger, paths.Ledger)
	rename(t, old.Ledger, old.Ledger+".moved")
	rerun(t, old, paths)
	assertMoved(t, old, paths, want)
}

func refuseALedgerBackAtTheOldPath(t *testing.T) {
	ctx := context.Background()
	old, paths := moveFixture(t)
	seedLedger(t, old.Ledger, "req-1", "req-2")
	ensureLedgerFolder(t, paths)
	vacuumInto(t, old.Ledger, paths.Ledger)
	rename(t, old.Ledger, old.Ledger+".moved")
	seedLedger(t, old.Ledger, "req-3", "req-4")
	files := []string{old.Ledger, old.Ledger + ".moved", paths.Ledger}
	before := readAll(t, files)
	if check := CheckFolders(old, paths); check.State != FoldersConflict {
		t.Fatalf("the check says %d, want a conflict", check.State)
	}
	if _, err := MoveFolders(ctx, old, paths); errorCategory(err) != "conflict" {
		t.Fatalf("got %v, want conflict", err)
	}
	if err := moveLedger(
		ctx,
		old.Ledger,
		paths.Ledger,
		false,
	); errorCategory(
		err,
	) != "conflict" {
		t.Fatalf("moving the ledger directly gave %v, want conflict", err)
	}
	for i, after := range readAll(t, files) {
		if !bytes.Equal(before[i], after) {
			t.Fatalf("a conflict changed %s", files[i])
		}
	}
}

func refuseAnOpenLedger(t *testing.T) {
	ctx := context.Background()
	old, paths := moveFixture(t)
	seedLedger(t, old.Ledger, "req-1")
	other, err := sql.Open("sqlite", old.Ledger)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = other.Close() }()
	if _, err = other.Exec("SELECT COUNT(*) FROM responses"); err != nil {
		t.Fatal(err)
	}
	_, err = MoveFolders(ctx, old, paths)
	if errorCategory(err) != "busy" {
		t.Fatalf("got %v, want busy", err)
	}
	if _, err := os.Lstat(old.Ledger); err != nil {
		t.Fatalf("the original ledger moved while open: %v", err)
	}
	if _, err := os.Lstat(paths.Ledger); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a ledger appeared at the new path while the old one was open: %v", err)
	}
}

func refuseTwoLedgers(t *testing.T) {
	ctx := context.Background()
	old, paths := moveFixture(t)
	seedLedger(t, old.Ledger, "req-1")
	ensureLedgerFolder(t, paths)
	seedLedger(t, paths.Ledger, "req-9")
	files := []string{old.Ledger, paths.Ledger}
	before := readAll(t, files)
	if _, err := MoveFolders(ctx, old, paths); errorCategory(err) != "conflict" {
		t.Fatalf("got %v, want conflict", err)
	}
	for i, after := range readAll(t, files) {
		if !bytes.Equal(before[i], after) {
			t.Fatalf("a conflict changed %s", files[i])
		}
	}
}

func moveTheLedgerPastAnOverridesConflict(t *testing.T) {
	ctx := context.Background()
	old, paths := moveFixture(t)
	want := seedLedger(t, old.Ledger, "req-1", "req-2")
	writeFile(t, old.Overrides, `{"old": {}}`, 0o644)
	writeFile(t, paths.Overrides, `{"new": {}}`, 0o600)
	left, err := MoveFolders(ctx, old, paths)
	if errorCategory(err) != "conflict" {
		t.Fatalf("got %v, want conflict over the overrides", err)
	}
	if len(left) != 0 {
		t.Fatalf("named %v as files Workbench didn't write; the conflict is not one", left)
	}
	if err := checkCopy(ctx, paths.Ledger, want); err != nil {
		t.Fatalf("the ledger did not move past the overrides conflict: %v", err)
	}
	if check := CheckFolders(old, paths); check.Ledger != FoldersDone {
		t.Fatalf("the ledger checks as %d after moving, want done", check.Ledger)
	}
}

// moveFixture is a home with the old claude-costs folders, made 0755 as the
// old script made them, and Workbench's runtime folders inside it. The temp
// folder is resolved, because Workbench refuses private folders under a link
// (/var on macOS).
func moveFixture(t *testing.T) (OldPaths, Paths) {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(root, "home")
	old := OldPaths{
		Ledger:    filepath.Join(home, ".local", "share", "claude-costs", "ledger.sqlite"),
		State:     filepath.Join(home, ".local", "state", "claude-costs"),
		Overrides: filepath.Join(home, ".config", "claude-costs", "rates.json"),
	}
	for _, folder := range []string{filepath.Dir(old.Ledger), old.State} {
		if err := os.MkdirAll(folder, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	runtime := operation.Paths{
		Config: filepath.Join(home, "workbench", "config"),
		Data:   filepath.Join(home, "workbench", "data"),
		State:  filepath.Join(home, "workbench", "state"),
		Cache:  filepath.Join(home, "workbench", "cache"),
		Bin:    filepath.Join(home, ".local", "bin"),
	}
	return old, LocationsIn(home, runtime)
}

// seedLedger makes a ledger at path holding one Claude Code row per id and
// one Codex row, closes it, and returns how many rows it holds.
func seedLedger(t *testing.T, path string, ids ...string) int64 {
	t.Helper()
	ledger, err := OpenLedger(path, true)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ledger.Close() }()
	usage := []Usage{
		{Tool: "codex", RequestID: "codex-1", Model: "gpt-5", Project: "/w/c", Session: "c1"},
	}
	for _, id := range ids {
		usage = append(
			usage,
			Usage{
				Tool:      "claude",
				RequestID: id,
				Model:     "claude-opus-5",
				Project:   "/w/a",
				Session:   "s1",
			},
		)
	}
	for i := range usage {
		usage[i].Time = time.Date(2026, 9, 1, 10, i, 0, 0, time.UTC)
		usage[i].Input, usage[i].Output = 100, 200
	}
	if err := ledger.Transaction(context.Background(), nil, func(tx *Tx) error {
		for _, u := range usage {
			if err := tx.Upsert(u); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return int64(len(usage))
}

// assertMoved checks the end state of every case that moves: the new ledger
// opens, passes quick_check and holds every row, is 0600; the original and
// the copy's marks are gone; and a further run finds nothing to do and
// changes nothing.
func assertMoved(t *testing.T, old OldPaths, paths Paths, want int64) {
	t.Helper()
	if err := checkCopy(context.Background(), paths.Ledger, want); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(paths.Ledger); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("new ledger mode: %v, %v", info, err)
	}
	for _, gone := range []string{old.Ledger, old.Ledger + ".moved", paths.Ledger + ".part"} {
		if _, err := os.Lstat(gone); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("%s should be gone: %v", gone, err)
		}
	}
	before := readBytes(t, paths.Ledger)
	if check := CheckFolders(old, paths); check.State != FoldersDone {
		t.Fatalf("after the move the check says %d (%s), want done", check.State, check.Text)
	}
	if _, err := MoveFolders(context.Background(), old, paths); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, readBytes(t, paths.Ledger)) {
		t.Fatal("a run with nothing to do changed the ledger")
	}
}

func rerun(t *testing.T, old OldPaths, paths Paths) {
	t.Helper()
	if check := CheckFolders(old, paths); check.State != FoldersUnfinished {
		t.Fatalf("a stopped copy checks as %d, want unfinished", check.State)
	}
	if _, err := MoveFolders(context.Background(), old, paths); err != nil {
		t.Fatal(err)
	}
}

func ensureLedgerFolder(t *testing.T, paths Paths) {
	t.Helper()
	if _, err := operation.PrivateDirectory(paths.Runtime.Data, "costs"); err != nil {
		t.Fatal(err)
	}
}

func vacuumInto(t *testing.T, from, to string) {
	t.Helper()
	db, err := sql.Open("sqlite", from)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.Exec("VACUUM INTO ?", to); err != nil {
		t.Fatal(err)
	}
}

func writeFile(t *testing.T, path, text string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), mode); err != nil {
		t.Fatal(err)
	}
}

func readBytes(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func readAll(t *testing.T, paths []string) [][]byte {
	t.Helper()
	all := make([][]byte, len(paths))
	for i, path := range paths {
		all[i] = readBytes(t, path)
	}
	return all
}

func rename(t *testing.T, from, to string) {
	t.Helper()
	if err := os.Rename(from, to); err != nil {
		t.Fatal(err)
	}
}

// finishALinkedMove is a no-replace rename that fell back to link and unlink
// and stopped between them: one ledger under both names. It is a move to
// finish, never a conflict that would stop the import.
func finishALinkedMove(t *testing.T) {
	old, paths := moveFixture(t)
	want := seedLedger(t, old.Ledger, "req-1", "req-2")
	ensureLedgerFolder(t, paths)
	if err := os.Link(old.Ledger, paths.Ledger); err != nil {
		t.Fatal(err)
	}
	if check := CheckFolders(old, paths); check.Ledger != FoldersUnfinished {
		t.Fatalf(
			"one file under both names checks as %d (%s), want unfinished",
			check.Ledger,
			check.Text,
		)
	}
	if _, err := MoveFolders(context.Background(), old, paths); err != nil {
		t.Fatal(err)
	}
	assertMoved(t, old, paths, want)
}
```

- [ ] **Step 3: Run it to see it fail**

```zsh
go test ./internal/costs/ -run TestMovingTheLedgerKeepsEveryRow; echo EXIT:$?
```

Expected: a build failure naming `LocationsIn`, `MoveFolders`, `moveLedger`, `CheckFolders`, `checkCopy`, `errorCategory` and the `Folders…` constants; `EXIT:1`.

- [ ] **Step 4: Write `internal/costs/move.go`**

What each part guards (spec 6.1):
- `ledgerState` looks for **both ledgers** (or a set-aside original with a ledger back at the old path) **before** it looks for a stopped copy's marks, so a rerun can never take a conflict for an unfinished copy.
- `moveLedger` never renames onto an existing ledger: after `resumeCopy`, a ledger at the new path is a finished move when the old one is gone, and a conflict when it isn't. `resumeCopy` refuses to put a set-aside original back while a ledger is at the old path. Every rename that puts a file in place is `operation.RenameNoReplace`.
- `copyLedger` counts the rows, writes `VACUUM INTO` and records the original's size, time and inode, all under `BEGIN IMMEDIATE` on a second connection. It closes the original before renaming it, and copies again if the original changed or is open again.
- Conflicts are per file: `MoveFolders` moves the ledger and every other file it can, and returns the conflict for what is left. `CheckFolders` takes the same bytes in an old and a new overrides file as a copy that finished. A price cache never conflicts.
- `gate` lets the worker import once the ledger itself has moved, and keeps it from opening a ledger while the old one is hidden by a relative variable (`OldPaths.Unresolved`).
- One file under both names (a no-replace rename that linked and stopped before the unlink) is a move to finish, never a conflict: `ledgerState` checks `sameFile` first, and `moveLedger` removes the old name.
- `strangers` names only files the migration doesn't know. The old ledger, its marks and journals, and every price cache, overrides file, log and lock it moves or deletes are excluded, so a file left in conflict is reported as a conflict only. When nothing is left, `CheckFolders` names the files it keeps, so `0001`'s *Done* says what stayed.

```go
package costs

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/Sawmonabo/workbench/internal/operation"
)

// LocationsIn is where the costs files live inside Workbench's runtime
// folders: the ledger and the price caches in <data>/costs, the import log
// and lock in <state>/costs, the owner's overrides in <config>/costs.
func LocationsIn(home string, runtime operation.Paths) Paths {
	return Paths{
		Home:      home,
		Runtime:   runtime,
		Ledger:    filepath.Join(runtime.Data, "costs", "ledger.sqlite"),
		State:     filepath.Join(runtime.State, "costs"),
		Overrides: filepath.Join(runtime.Config, "costs", "rates.json"),
	}
}

// OldPaths are where releases before the migrations design kept the costs
// files: CLAUDE_COSTS_LEDGER, CLAUDE_COSTS_STATE and CLAUDE_COSTS_RATES when
// set, else claude-costs under XDG_DATA_HOME, XDG_STATE_HOME and
// XDG_CONFIG_HOME when set, else under ~/.local/share, ~/.local/state and
// ~/.config, on macOS too. Only the costs migration reads them. A path is ""
// when a relative value named it: that resolved against whatever folder the
// old process started in and can't be found again. Notes says which, and
// Unresolved names the variable that hides the ledger's own path.
type OldPaths struct {
	Ledger, State, Overrides string
	Notes                    []string
	Unresolved               string
}

// OldLocations resolves [OldPaths] from the environment as the old code did.
func OldLocations(home string) OldPaths {
	var old OldPaths
	var why string
	absolute := func(name, value string) string {
		if filepath.IsAbs(value) {
			return filepath.Clean(value)
		}
		why = fmt.Sprintf("%s is relative (%s)", name, value)
		old.Notes = append(old.Notes, why+", so the costs files it named can't be found")
		return ""
	}
	pick := func(override, xdg, fallback string, elem ...string) string {
		why = ""
		if value := os.Getenv(override); value != "" {
			return absolute(override, value)
		}
		base := os.Getenv(xdg)
		switch {
		case base == "":
			base = filepath.Join(home, fallback)
		case !filepath.IsAbs(base):
			return absolute(xdg, base)
		}
		return filepath.Join(append([]string{base}, elem...)...)
	}
	old.Ledger = pick(
		"CLAUDE_COSTS_LEDGER",
		"XDG_DATA_HOME",
		".local/share",
		"claude-costs",
		"ledger.sqlite",
	)
	old.Unresolved = why
	old.State = pick("CLAUDE_COSTS_STATE", "XDG_STATE_HOME", ".local/state", "claude-costs")
	old.Overrides = pick(
		"CLAUDE_COSTS_RATES",
		"XDG_CONFIG_HOME",
		".config",
		"claude-costs",
		"rates.json",
	)
	return old
}

func (o OldPaths) lock() string { return filepath.Join(o.State, "ingest.lock") }
func (o OldPaths) log() string  { return filepath.Join(o.State, "ingest.log") }

// oldOfficialPath is where the old code kept one tool's price cache: beside
// the ledger, rates-official.json for the first tool and
// rates-official-<tool>.json for every other.
func oldOfficialPath(folder string, tool Tool) string {
	name := "rates-official.json"
	if tool.Name != firstTool {
		name = "rates-official-" + tool.Name + ".json"
	}
	return filepath.Join(folder, name)
}

// costsMove is one costs file other than the ledger that the migration moves
// into the runtime folder root, as folder (relative to root). cache marks a
// price cache, which is derived data: when the new path already holds one,
// the old one is deleted instead of being a conflict.
type costsMove struct {
	from, to, root, folder string
	cache                  bool
}

func (o OldPaths) moves(p Paths) []costsMove {
	var moves []costsMove
	if o.Ledger != "" {
		for _, tool := range Tools {
			if tool.Source != nil {
				moves = append(moves, costsMove{
					oldOfficialPath(filepath.Dir(o.Ledger), tool), officialPath(p, tool),
					p.Runtime.Data, filepath.Join("costs", tool.Name), true,
				})
			}
		}
	}
	if o.Overrides != "" {
		moves = append(moves, costsMove{o.Overrides, p.Overrides, p.Runtime.Config, "costs", false})
	}
	return moves
}

// leftovers are the old files the migration deletes: price writes that
// stopped part way, the import log (not moved, so a log the new release
// already started is never a conflict) and the import lock.
func (o OldPaths) leftovers() []string {
	var names []string
	if o.Ledger != "" {
		for _, tool := range Tools {
			if tool.Source != nil {
				names = append(names, oldOfficialPath(filepath.Dir(o.Ledger), tool)+".tmp")
			}
		}
	}
	if o.State != "" {
		names = append(names, o.log(), o.lock())
	}
	return names
}

// exists reports whether path is there, from Lstat only. A path that can't
// be inspected counts as there, so nothing is called done that isn't.
func exists(path string) bool {
	if path == "" {
		return false
	}
	_, err := os.Lstat(path)
	return err == nil || !errors.Is(err, fs.ErrNotExist)
}

// sameFile reports whether two paths name one file: a move whose link stayed
// after its unlink failed (see operation.RenameNoReplace).
func sameFile(a, b string) bool {
	left, err := os.Lstat(a)
	if err != nil {
		return false
	}
	right, err := os.Lstat(b)
	return err == nil && os.SameFile(left, right)
}

// sameBytes reports whether two small files hold the same bytes: a move
// across filesystems that stopped after the copy, before deleting the old one.
func sameBytes(a, b string) bool {
	left, err := os.ReadFile(a)
	if err != nil {
		return false
	}
	right, err := os.ReadFile(b)
	return err == nil && bytes.Equal(left, right)
}

// FolderState is where the costs files, or the ledger alone, stand.
type FolderState int

// The states.
const (
	FoldersDone       FolderState = iota // nothing is left to move
	FoldersPending                       // old files wait to move
	FoldersUnfinished                    // a stopped copy of the ledger left its marks
	FoldersConflict                      // old and new data both exist, or the old ledger can't be found
)

// FolderCheck is what [CheckFolders] found. State is the whole migration's:
// Pending or Unfinished while anything is left that a run can move, Conflict
// when only conflicts are left. Ledger is the ledger's own, which alone
// decides whether the import may open the new ledger. Text names the paths,
// home-relative, and never holds "; ".
type FolderCheck struct {
	State, Ledger FolderState
	Text          string
}

// CheckFolders says what is left of the costs move. It looks at which paths
// exist (Lstat), reads an old and a new overrides file only when both are
// there, to tell a finished copy from two different files, and, when nothing
// is left, lists the old claude-costs folders to name the files it keeps
// there. It never opens the ledger, locks or changes anything.
func CheckFolders(old OldPaths, p Paths) FolderCheck {
	var moved, deleted, conflicts []string
	conflict := func(from, to string) {
		conflicts = append(
			conflicts,
			"leaves "+tilde(from, p.Home)+" and "+tilde(to, p.Home)+
				", which hold different data (remove one)",
		)
	}
	check := FolderCheck{Ledger: ledgerState(old, p)}
	switch check.Ledger {
	case FoldersConflict:
		if old.Ledger == "" {
			conflicts = append(conflicts, "can't find the old ledger because "+old.Unresolved+
				" (make it absolute, or unset it)")
		} else if exists(old.Ledger + ".moved") {
			conflict(old.Ledger, old.Ledger+".moved")
		} else {
			conflict(old.Ledger, p.Ledger)
		}
	case FoldersUnfinished:
		moved = append(moved, tilde(old.Ledger, p.Home)+" (finishing a stopped move)")
	case FoldersPending:
		moved = append(moved, tilde(old.Ledger, p.Home))
	default:
	}
	for _, move := range old.moves(p) {
		switch {
		case !exists(move.from):
		case exists(move.to) && (move.cache || sameBytes(move.from, move.to)):
			deleted = append(deleted, tilde(move.from, p.Home))
		case exists(move.to):
			conflict(move.from, move.to)
		default:
			moved = append(moved, tilde(move.from, p.Home))
		}
	}
	for _, name := range old.leftovers() {
		if exists(name) {
			deleted = append(deleted, tilde(name, p.Home))
		}
	}
	var text []string
	if len(moved) > 0 {
		text = append(text, "moves "+strings.Join(moved, ", ")+" into Workbench's folders")
	}
	if len(deleted) > 0 {
		text = append(text, "deletes "+strings.Join(deleted, ", "))
	}
	text = append(text, conflicts...)
	switch {
	case check.Ledger == FoldersUnfinished:
		check.State = FoldersUnfinished
	case len(moved) > 0 || len(deleted) > 0:
		check.State = FoldersPending
	case len(conflicts) > 0:
		check.State = FoldersConflict
	default:
		check.State = FoldersDone
		if kept := old.strangers(p); len(kept) > 0 {
			text = append(text, "keeps "+strings.Join(kept, ", ")+" (Workbench didn't write them)")
		}
	}
	check.Text = strings.Join(text, " and ")
	return check
}

// ledgerState is where the ledger alone stands. One file under both names is
// a move that stopped after linking the new name. Old and new both present, or
// a set-aside original with a ledger back at the old path, is a conflict
// whatever marks a stopped copy left. An old path a relative variable hides
// is a conflict too until a ledger is at the new path: the old one may still
// hold usage, and a new empty one beside it would split the record.
func ledgerState(old OldPaths, p Paths) FolderState {
	switch {
	case old.Ledger == "" && exists(p.Ledger):
		return FoldersDone
	case old.Ledger == "":
		if old.Unresolved != "" {
			return FoldersConflict
		}
		return FoldersDone
	case sameFile(old.Ledger, p.Ledger):
		return FoldersUnfinished
	case exists(old.Ledger) && (exists(p.Ledger) || exists(old.Ledger+".moved")):
		return FoldersConflict
	case exists(p.Ledger+".part") || exists(old.Ledger+".moved"):
		return FoldersUnfinished
	case exists(old.Ledger):
		return FoldersPending
	}
	return FoldersDone
}

// busy is the error of a move that found the ledger or its import in use. It
// moves nothing, and the next import or apply tries again.
func busy(why string) error {
	return operation.Fail(
		operation.ExitBlocked,
		"busy",
		why+", so nothing was moved and the next import or apply tries again",
	)
}

func conflictError(text string) error {
	return operation.Fail(
		operation.ExitConflict,
		"conflict",
		"The costs files can't all move: it "+text,
	)
}

// errorCategory is the category of an [operation.Error], "" for any other.
func errorCategory(err error) string {
	if problem, ok := errors.AsType[*operation.Error](err); ok {
		return problem.Category
	}
	return ""
}

// MoveFolders moves the costs files from old into p's folders, and deletes the
// old import log, its lock and unfinished price writes. The caller holds
// [operation.Paths.MigrateLock]. While it works it holds both import locks, so
// no worker of this or an older release writes the ledger mid-move, and it
// moves the ledger only once nothing has it open (see moveLedger). It merges
// nothing and overwrites nothing: a file whose old and new paths both hold
// different data stays where it is, every other file moves, and the error then
// names what is left. Running it again finishes whatever a stopped run left.
// left names, home-relative, the files it found in an old claude-costs folder
// and doesn't know; they stay, with their folder.
func MoveFolders(ctx context.Context, old OldPaths, p Paths) (left []string, err error) {
	check := CheckFolders(old, p)
	switch check.State {
	case FoldersDone:
		return nil, nil
	case FoldersConflict:
		return nil, conflictError(check.Text)
	default:
	}
	release, err := lockImports(old, p)
	if err != nil {
		return nil, err
	}
	defer release()
	// Check again under the locks: an import may have finished in between.
	check = CheckFolders(old, p)
	if check.Ledger == FoldersPending || check.Ledger == FoldersUnfinished {
		if _, err = operation.PrivateDirectory(p.Runtime.Data, "costs"); err != nil {
			return nil, err
		}
		if err = moveLedger(ctx, old.Ledger, p.Ledger, false); err != nil &&
			errorCategory(err) != "conflict" {
			return nil, err
		}
	}
	if err = moveSmallFiles(old, p); err != nil {
		return nil, err
	}
	for _, name := range old.leftovers() {
		if err = os.Remove(name); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return nil, err
		}
	}
	left = removeOldFolders(old, p)
	if after := CheckFolders(old, p); after.State == FoldersConflict {
		return left, conflictError(after.Text)
	}
	return left, nil
}

// lockImports takes the import lock of this release and, when the old state
// folder is there, the old one, so neither release's worker runs mid-move.
func lockImports(old OldPaths, p Paths) (release func(), err error) {
	locks := []string{p.lock()}
	if old.State != "" && exists(old.State) {
		locks = append(locks, old.lock())
	}
	var releases []func()
	release = func() {
		for _, r := range slices.Backward(releases) {
			r()
		}
	}
	for _, lock := range locks {
		r, held, err := operation.TryLock(lock)
		if err == nil && held {
			err = busy("Claude Code or Codex usage is being read into the ledger")
		}
		if err != nil {
			release()
			return nil, err
		}
		releases = append(releases, r)
	}
	return release, nil
}

// moveSmallFiles moves the price caches and the overrides. An old file whose
// new path already holds a cache, or the same bytes, is deleted: a cache is
// fetched again when stale, and the same bytes are a copy that finished. An old
// file whose new path holds other data stays.
func moveSmallFiles(old OldPaths, p Paths) error {
	for _, move := range old.moves(p) {
		switch {
		case !exists(move.from):
			continue
		case exists(move.to) && (move.cache || sameBytes(move.from, move.to)):
			if err := os.Remove(move.from); err != nil {
				return err
			}
			continue
		case exists(move.to):
			continue
		}
		if _, err := operation.PrivateDirectory(move.root, "costs"); err != nil {
			return err
		}
		if _, err := operation.PrivateDirectory(move.root, move.folder); err != nil {
			return err
		}
		if err := moveFile(move.from, move.to); err != nil {
			return err
		}
	}
	return nil
}

// folders are the old claude-costs folders, each once.
func (o OldPaths) folders() []string {
	var folders []string
	for _, folder := range []string{filepath.Dir(o.Ledger), o.State, filepath.Dir(o.Overrides)} {
		if filepath.Base(folder) == "claude-costs" && !slices.Contains(folders, folder) {
			folders = append(folders, folder)
		}
	}
	return folders
}

// known are the old paths the migration moves, deletes or leaves in conflict.
func (o OldPaths) known(p Paths) []string {
	names := o.leftovers()
	if o.Ledger != "" {
		names = append(names, o.Ledger, o.Ledger+".moved",
			o.Ledger+"-wal", o.Ledger+"-shm", o.Ledger+"-journal")
	}
	for _, move := range o.moves(p) {
		names = append(names, move.from)
	}
	return names
}

// strangers are the files, home-relative, in the old claude-costs folders that
// the migration doesn't know: Workbench didn't write them, so they stay. A
// file it knows that stays, such as one left in conflict, is not one of them.
func (o OldPaths) strangers(p Paths) []string {
	known := o.known(p)
	var left []string
	for _, folder := range o.folders() {
		entries, _ := os.ReadDir(folder)
		for _, entry := range entries {
			if path := filepath.Join(folder, entry.Name()); !slices.Contains(known, path) {
				left = append(left, tilde(path, p.Home))
			}
		}
	}
	return left
}

// removeOldFolders removes each old claude-costs folder once it is empty, and
// returns the files it keeps because Workbench didn't write them.
func removeOldFolders(old OldPaths, p Paths) []string {
	for _, folder := range old.folders() {
		_ = os.Remove(folder) // only an empty folder goes
	}
	return old.strangers(p)
}

// moveFile moves one small costs file and makes it 0600, never replacing a
// file at to. Across filesystems it writes a temporary copy beside to, syncs
// it, renames it into place, and only then deletes from; a stop between the
// two leaves the same bytes in both, which the next run takes as moved.
func moveFile(from, to string) error {
	err := operation.RenameNoReplace(from, to)
	if errors.Is(err, syscall.EXDEV) {
		if err = copyFile(from, to); err == nil {
			err = os.Remove(from)
		}
	}
	if err != nil {
		return err
	}
	if err = os.Chmod(to, 0o600); err != nil {
		return err
	}
	if err = syncFolder(filepath.Dir(from)); err != nil {
		return err
	}
	return syncFolder(filepath.Dir(to))
}

func copyFile(from, to string) error {
	data, err := os.ReadFile(from)
	if err != nil {
		return err
	}
	part := to + ".part"
	file, err := os.OpenFile(part, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	_, err = file.Write(data)
	if err == nil {
		err = file.Sync()
	}
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		err = operation.RenameNoReplace(part, to)
	}
	if err != nil {
		_ = os.Remove(part)
	}
	return err
}

func syncFolder(folder string) error {
	file, err := os.Open(folder)
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()
	return file.Sync()
}

// moveLedger moves the ledger at from to to. It first finishes whatever a
// stopped copy left (see resumeCopy). It never replaces a ledger at to: one
// there with the old one gone is a finished move, and one there with the old
// one still present is a conflict. Otherwise it opens the ledger the normal
// way, which verifies it and upgrades an older schema in place, checkpoints
// and closes it (see quiesce), and renames it, one atomic step. Across
// filesystems, or with alwaysCopy, it copies it (see copyLedger). A zero-byte
// ledger holds nothing and is deleted. One file under both names is a move
// that linked the new name and stopped before the unlink: the old name goes.
func moveLedger(ctx context.Context, from, to string, alwaysCopy bool) error {
	if sameFile(from, to) {
		// A move that linked the new name and stopped before the unlink.
		if err := os.Remove(from); err != nil {
			return err
		}
		return settle(from, to)
	}
	if err := resumeCopy(ctx, from, to); err != nil {
		return err
	}
	if exists(to) {
		if exists(from) {
			return conflictError(
				"leaves " + from + " and " + to + ", which hold different data (remove one)",
			)
		}
		return nil
	}
	info, err := os.Stat(from)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil
	case err != nil:
		return err
	case info.Size() == 0:
		return os.Remove(from)
	}
	if err = quiesce(ctx, from); err != nil {
		return err
	}
	if !alwaysCopy {
		err = operation.RenameNoReplace(from, to)
		switch {
		case err == nil:
			return settle(from, to)
		case errors.Is(err, fs.ErrExist):
			return conflictError(
				"leaves " + from + " and " + to + ", which hold different data (remove one)",
			)
		case !errors.Is(err, syscall.EXDEV):
			return err
		}
	}
	return copyLedger(ctx, from, to)
}

// resumeCopy finishes or undoes what a stopped copy left. The marks are
// to+".part", a copy being written or checked, and from+".moved", the
// original set aside once its copy was checked. A ledger back at from beside
// a set-aside original is a conflict, and nothing is touched. The original is
// deleted only after the copy in place is checked again against it.
func resumeCopy(ctx context.Context, from, to string) error {
	moved, part := from+".moved", to+".part"
	if exists(moved) && exists(from) {
		return conflictError(
			"leaves " + from + " and " + moved + ", which hold different data (remove one)",
		)
	}
	switch {
	case exists(moved) && exists(to):
		return finishCopy(ctx, moved, to)
	case exists(moved) && exists(part):
		rows, err := countRows(ctx, moved)
		if err != nil {
			return err
		}
		if err = checkCopy(ctx, part, rows); err != nil {
			return err
		}
		if err = operation.RenameNoReplace(part, to); err != nil {
			return err
		}
		if err = syncFolder(filepath.Dir(to)); err != nil {
			return err
		}
		return finishCopy(ctx, moved, to)
	case exists(moved):
		// Set aside with no copy in place: put it back and start again.
		if err := operation.RenameNoReplace(moved, from); err != nil {
			return err
		}
		return syncFolder(filepath.Dir(from))
	case exists(part):
		return removeDatabase(part)
	}
	return nil
}

// finishCopy deletes the set-aside original once the ledger at to opens,
// passes quick_check and holds every row the original holds.
func finishCopy(ctx context.Context, moved, to string) error {
	rows, err := countRows(ctx, moved)
	if err != nil {
		return err
	}
	if err = checkCopy(ctx, to, rows); err != nil {
		return err
	}
	if err = os.Remove(moved); err != nil {
		return err
	}
	return settle(moved, to)
}

// quiesce opens the ledger alone, folds its write-ahead log into it and closes
// it. SQLite deletes the -wal and -shm files only when the last connection
// closes, so either one left means another process has the ledger open, for
// example a report from the release before the update: moving it then, or its
// log apart from it, would corrupt it.
func quiesce(ctx context.Context, path string) error {
	ledger, err := OpenLedger(path, false)
	if err != nil {
		return err
	}
	var blocked, frames, done int
	err = ledger.db.QueryRowContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)").
		Scan(&blocked, &frames, &done)
	if closeErr := ledger.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if blocked != 0 || exists(path+"-wal") || exists(path+"-shm") {
		return busy("another process has the costs ledger open")
	}
	return nil
}

// copyLedger copies the ledger to another filesystem. A second connection
// holds BEGIN IMMEDIATE while the rows are counted and VACUUM INTO writes a
// compact copy at to+".part", so nothing writes in between, and the
// original's size, time and inode are recorded under that lock. The copy is
// synced and must pass quick_check with as many rows. The original is then
// closed, and only if nothing has it open and it still has the recorded size,
// time and inode is it renamed to from+".moved" on its own filesystem, the
// copy renamed into place, and the original deleted. A changed original is
// copied again; renaming an open database apart from its -wal file is what
// corrupts it, so the lock is not held across the renames.
func copyLedger(ctx context.Context, from, to string) error {
	part := to + ".part"
	for range 3 {
		rows, before, err := vacuumCopy(ctx, from, part)
		if err == nil {
			err = checkCopy(ctx, part, rows)
		}
		if err != nil {
			_ = removeDatabase(part)
			return err
		}
		if exists(from+"-wal") || exists(from+"-shm") {
			_ = removeDatabase(part)
			return busy("another process opened the costs ledger during the copy")
		}
		if now, err := fileStampOf(from); err != nil || now != before {
			if err = removeDatabase(part); err != nil {
				return err
			}
			continue
		}
		moved := from + ".moved"
		if err = operation.RenameNoReplace(from, moved); err != nil {
			return err
		}
		if err = syncFolder(filepath.Dir(from)); err != nil {
			return err
		}
		if err = operation.RenameNoReplace(part, to); err != nil {
			return err
		}
		if err = syncFolder(filepath.Dir(to)); err != nil {
			return err
		}
		return finishCopy(ctx, moved, to)
	}
	return busy("the costs ledger kept changing during the copy")
}

// fileStamp is what tells a database file that changed: its size, modification
// time and inode.
type fileStamp struct {
	size  int64
	mod   time.Time
	inode uint64
}

func fileStampOf(path string) (fileStamp, error) {
	info, err := os.Stat(path)
	if err != nil {
		return fileStamp{}, err
	}
	var inode uint64
	if sys, ok := info.Sys().(*syscall.Stat_t); ok {
		inode = sys.Ino
	}
	return fileStamp{info.Size(), info.ModTime(), inode}, nil
}

// vacuumCopy writes the copy and returns the original's row count and stamp,
// all taken while the second connection holds the write lock.
func vacuumCopy(ctx context.Context, from, part string) (int64, fileStamp, error) {
	ledger, err := OpenLedger(from, false)
	if err != nil {
		return 0, fileStamp{}, err
	}
	defer func() { _ = ledger.Close() }()
	hold, err := ledger.db.Conn(ctx)
	if err != nil {
		return 0, fileStamp{}, err
	}
	defer func() { _ = hold.Close() }()
	if _, err = hold.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return 0, fileStamp{}, busy("another process is writing the costs ledger")
	}
	defer func() { _, _ = hold.ExecContext(context.Background(), "ROLLBACK") }()
	var rows int64
	if err = hold.QueryRowContext(ctx, "SELECT COUNT(*) FROM responses").Scan(&rows); err != nil {
		return 0, fileStamp{}, err
	}
	if _, err = ledger.db.ExecContext(ctx, "VACUUM INTO ?", part); err != nil {
		return 0, fileStamp{}, err
	}
	before, err := fileStampOf(from)
	if err != nil {
		return 0, fileStamp{}, err
	}
	file, err := os.Open(part)
	if err != nil {
		return 0, fileStamp{}, err
	}
	err = file.Sync()
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return 0, fileStamp{}, err
	}
	return rows, before, syncFolder(filepath.Dir(part))
}

// checkCopy opens the copy at path as a ledger, which verifies it is one, and
// requires PRAGMA quick_check to say ok and its responses to number rows.
func checkCopy(ctx context.Context, path string, rows int64) error {
	ledger, err := OpenLedger(path, false)
	if err != nil {
		return err
	}
	defer func() { _ = ledger.Close() }()
	var result string
	if err = ledger.db.QueryRowContext(ctx, "PRAGMA quick_check").Scan(&result); err != nil {
		return err
	}
	var found int64
	if err = ledger.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM responses").
		Scan(&found); err != nil {
		return err
	}
	if result != "ok" || found != rows {
		return operation.Fail(operation.ExitFailed, "ledger", fmt.Sprintf(
			"the copy at %s failed its check (%s, %d of %d rows), so the original is kept",
			path, result, found, rows,
		))
	}
	return nil
}

func countRows(ctx context.Context, path string) (int64, error) {
	ledger, err := OpenLedger(path, false)
	if err != nil {
		return 0, err
	}
	defer func() { _ = ledger.Close() }()
	var rows int64
	err = ledger.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM responses").Scan(&rows)
	return rows, err
}

// settle makes the moved ledger 0600 and syncs both folders, so the move is
// durable before anything reports it done.
func settle(from, to string) error {
	if err := os.Chmod(to, 0o600); err != nil {
		return err
	}
	if err := syncFolder(filepath.Dir(from)); err != nil {
		return err
	}
	return syncFolder(filepath.Dir(to))
}

// removeDatabase deletes a partial copy with any journal SQLite left beside it.
func removeDatabase(path string) error {
	for _, name := range []string{path + "-wal", path + "-shm", path + "-journal", path} {
		if err := os.Remove(name); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
	}
	return nil
}

// Gate keeps the ledger closed at its new path while the costs ledger still
// waits to move there, so nothing creates an empty ledger beside the old one.
// It runs before every ledger open and before anything writes beside the
// ledger. move is for the import worker: it moves the files itself, under the
// same locks as `workbench migrate run`, because usage left unread is lost
// once the session files it is in are deleted; it then reads
// into the new ledger once the ledger itself has moved, even when another
// costs file is left in conflict. Every other caller is told to run the
// migration while anything waits, and stops on a ledger conflict; a conflict
// over another file alone doesn't stop it, and `workbench migrate` names it.
func Gate(ctx context.Context, move bool) error {
	paths, err := Locations()
	if err != nil {
		return err
	}
	return gate(ctx, paths, move)
}

func gate(ctx context.Context, paths Paths, move bool) error {
	old := OldLocations(paths.Home)
	check := CheckFolders(old, paths)
	switch {
	case check.State == FoldersDone:
		return nil
	case !move && check.State == FoldersConflict && check.Ledger == FoldersDone:
		return nil
	case !move && check.State == FoldersConflict:
		return conflictError(check.Text)
	case !move:
		return operation.Fail(
			operation.ExitBlocked,
			"migrate",
			"The costs files are moving to Workbench's folders; run `workbench migrate --apply`",
		)
	}
	if check.State != FoldersConflict {
		release, held, err := operation.TryLock(paths.Runtime.MigrateLock())
		if err != nil {
			return err
		}
		if held {
			return busy("another migration is running")
		}
		_, err = MoveFolders(ctx, old, paths)
		release()
		if err != nil && errorCategory(err) != "conflict" {
			return err
		}
	}
	if after := CheckFolders(old, paths); after.Ledger != FoldersDone {
		return conflictError(after.Text)
	}
	return nil
}
```

- [ ] **Step 5: Run the test to see it pass, on macOS and on Linux**

```zsh
go test ./internal/costs/ -run TestMovingTheLedgerKeepsEveryRow -v; echo EXIT:$?
docker run --rm -v "$PWD":/src:ro -v "$(go env GOMODCACHE)":/go/pkg/mod:ro -w /src golang:1.26 sh -c 'useradd -m t; su t -c "umask 022; mkdir -m 700 /home/t/tmp; TMPDIR=/home/t/tmp HOME=/home/t GOMODCACHE=/go/pkg/mod GOCACHE=/home/t/gc GOFLAGS=-buildvcs=false GOTOOLCHAIN=local /usr/local/go/bin/go test ./internal/costs/ -run TestMovingTheLedgerKeepsEveryRow -v"'; echo EXIT:$?
```

Expected: all ten subtests `PASS` on both, `EXIT:0`. The container runs as a user with `umask 022` and a private `TMPDIR`: its default umask 0 and shared `/tmp` make three existing operation tests fail on unchanged code, unrelated to this plan. If "same filesystem" fails as busy, the `-shm` file survived the close. Check that no other connection to the old ledger stays open in `quiesce` before changing anything else.

- [ ] **Step 6: Run the gates, then commit**

Run the gates (Global Constraints). Expected: `EXIT:0` each.

```zsh
git add internal/costs/move.go internal/costs/move_test.go internal/costs/ledger.go
git commit -F - <<'EOF'
feat(costs): move the costs files into Workbench's folders without losing a row

The ledger moves by a no-replace rename, or across filesystems by a checked
VACUUM INTO copy, only once nothing has it open; a stopped copy resumes from
its marks, a ledger back at the old path or already at the new one is a
conflict that changes nothing, and a conflict over another file never stops
the ledger.

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
EOF
echo EXIT:$?
```

---

### Task 4: The costs package uses Workbench's folders, behind the gate

**Files:**
- Modify: `internal/costs/ledger.go:24-71` (`Paths` doc, `Locations`)
- Modify: `internal/costs/rates.go` (`Rates`, `officialPath`, `writeOfficial`)
- Modify: `internal/costs/ingest.go` (`Ingest`, `Hook`)
- Modify: `internal/costs/report.go` (`Status`)
- Modify: `internal/cli/costs.go` (`costsRun` comment, `ensureIngested`)

**Interfaces:**
- Consumes: `LocationsIn`, `gate`, `Gate` (Task 3); `operation.Resolve(operation.Options) (operation.Context, error)`.
- Produces: `costs.Locations()` returns the new paths, and fails with `Resolve`'s reason when a runtime folder can't be used. Nothing reads `CLAUDE_COSTS_LEDGER`, `CLAUDE_COSTS_STATE` or `CLAUDE_COSTS_RATES` outside `OldLocations`.

- [ ] **Step 1: Replace `Locations`**

In `internal/costs/ledger.go`, replace the `Paths` doc comment and the whole `Locations` function (from `// Paths are where the ledger` through the end of `Locations`) with:

```go
// Paths are where the ledger and its companions live, inside Workbench's
// runtime folders (see [LocationsIn]): WORKBENCH_{DATA,STATE,CONFIG}_DIR or
// XDG_{DATA,STATE,CONFIG}_HOME relocate them with everything else Workbench
// keeps. The migrations design moved them there from the claude-costs folders
// ([OldLocations], [MoveFolders]).
type Paths struct {
	Home      string
	Runtime   operation.Paths // Workbench's runtime folders the paths below sit in
	Ledger    string          // the durable record
	State     string          // ingest.log and ingest.lock
	Overrides string          // manual per-model rates, highest priority
}

// Locations resolves [Paths] from Workbench's runtime folders. It fails, with
// the reason, when they can't be used, for example a runtime folder that is
// not private.
func Locations() (Paths, error) {
	c, err := operation.Resolve(operation.Options{ReadOnly: true})
	if err != nil {
		return Paths{}, err
	}
	return LocationsIn(c.Home, c.Paths), nil
}
```

`log()` and `lock()` stay as they are.

- [ ] **Step 2: Gate the readers, the worker and the reports; per-tool caches; the hook's stderr line**

```diff
diff --git a/internal/costs/rates.go b/internal/costs/rates.go
index d5332c3..80197d1 100644
--- a/internal/costs/rates.go
+++ b/internal/costs/rates.go
@@ -189,6 +189,9 @@ func Rates(ctx context.Context, refresh bool) (Card, error) {
 	if err != nil {
 		return Card{}, err
 	}
+	if err = gate(ctx, paths, false); err != nil {
+		return Card{}, err
+	}
 	var notes []string
 	if refresh {
 		for _, tool := range Tools {
@@ -331,14 +334,10 @@ func (f officialFile) rates() RateCard {
 	return card
 }
 
-// officialPath is the cache of one tool's official card: rates-official.json
-// for the first tool, rates-official-<tool>.json for every other.
+// officialPath is the cache of one tool's official card,
+// <data>/costs/<tool>/rates-official.json.
 func officialPath(paths Paths, tool Tool) string {
-	name := "rates-official.json"
-	if tool.Name != firstTool {
-		name = "rates-official-" + tool.Name + ".json"
-	}
-	return filepath.Join(filepath.Dir(paths.Ledger), name)
+	return filepath.Join(filepath.Dir(paths.Ledger), tool.Name, "rates-official.json")
 }
 
 func readOfficial(paths Paths, tool Tool) officialFile {
@@ -351,8 +350,8 @@ func readOfficial(paths Paths, tool Tool) officialFile {
 
 func writeOfficial(paths Paths, tool Tool, file officialFile) error {
 	path := officialPath(paths, tool)
-	// The ledger's directory, which `costs rates --refresh` can create first:
-	// private like the ledger. The cache itself holds public prices.
+	// The tool's folder under the ledger's, which `costs rates --refresh` can
+	// create first: private like the ledger. The cache holds public prices.
 	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
 		return err
 	}
@@ -361,7 +360,7 @@ func writeOfficial(paths Paths, tool Tool, file officialFile) error {
 		return err
 	}
 	tmp := path + ".tmp"
-	if err := os.WriteFile(tmp, data, 0o644); err != nil {
+	if err := os.WriteFile(tmp, data, 0o600); err != nil {
 		return err
 	}
 	return os.Rename(tmp, path)
```

```diff
diff --git a/internal/costs/report.go b/internal/costs/report.go
index fa9c438..cabede9 100644
--- a/internal/costs/report.go
+++ b/internal/costs/report.go
@@ -678,6 +678,9 @@ func Status(ctx context.Context) (StatusInfo, error) {
 	if err != nil {
 		return StatusInfo{}, err
 	}
+	if err = gate(ctx, paths, false); err != nil {
+		return StatusInfo{}, err
+	}
 	ledger, err := OpenLedger(paths.Ledger, true)
 	if err != nil {
 		return StatusInfo{}, err
```

```diff
diff --git a/internal/costs/ingest.go b/internal/costs/ingest.go
index d4ee3b9..9226fd0 100644
--- a/internal/costs/ingest.go
+++ b/internal/costs/ingest.go
@@ -40,6 +40,9 @@ type IngestOptions struct {
 // Every failure is also written to the log and, once the ledger is open, to
 // its last_error note; committed files are not reprocessed next time.
 //
+// It first runs the costs gate ([Gate]), which moves the costs files into
+// Workbench's folders when they still wait in the old ones.
+//
 // A worker started by a hook first binds the hook's session to the tool's
 // current sign-in, before it contends for the lock, so a worker that skips
 // because another is running loses no binding.
@@ -48,6 +51,12 @@ func Ingest(ctx context.Context, opts IngestOptions) (summary string, err error)
 	if err != nil {
 		return "", err
 	}
+	// The worker moves the costs files itself when they still wait in the
+	// claude-costs folders; it must not open a new, empty ledger beside them.
+	if err = gate(ctx, paths, true); err != nil {
+		writeLog(paths, "not read: "+err.Error())
+		return "", err
+	}
 	bindSession(ctx, paths, opts)
 	release, held, err := operation.TryLock(paths.lock())
 	for err == nil && held && opts.Wait {
@@ -748,15 +757,24 @@ func truncateLog(paths Paths) {
 // `workbench costs ingest --worker` detached with those three values and
 // returns. The hooks run synchronously (Claude Code stops a background hook
 // that is still running when a headless session ends), which costs a session
-// the few milliseconds of one process spawn. Hook prints nothing and never
-// fails: a terminal, a closed or empty stdin, or anything unreadable just
-// means no event, and the worker then sweeps everything. Reading stdin gives
-// up after half a second.
+// the few milliseconds of one process spawn. Hook writes only the new import
+// log, which no migration moves or compares, so it needs no gate: the worker
+// runs it. Hook prints nothing on stdout and never fails: a terminal, a closed
+// or empty stdin, or anything unreadable just means no event, and the worker
+// then sweeps everything. Reading stdin gives up after half a second. When
+// Workbench's folders can't be used, for example a runtime folder that is not
+// private, it says why on stderr and records nothing.
 func Hook(stdin *os.File) {
 	defer func() { _ = recover() }()
 	event, transcript, session := readHookInput(stdin)
 	paths, err := Locations()
-	if err != nil || os.MkdirAll(paths.State, 0o700) != nil {
+	if err != nil {
+		// Without Workbench's folders there is nowhere to log or record; say
+		// why on stderr, which the agent keeps, and record nothing.
+		_, _ = fmt.Fprintln(os.Stderr, "workbench costs: nothing recorded: "+err.Error())
+		return
+	}
+	if os.MkdirAll(paths.State, 0o700) != nil {
 		return
 	}
 	logFile, err := os.OpenFile(paths.log(), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
```

```diff
diff --git a/internal/cli/costs.go b/internal/cli/costs.go
index d28646c..5048a89 100644
--- a/internal/cli/costs.go
+++ b/internal/cli/costs.go
@@ -89,8 +89,9 @@ func costsCommand(o *options) *cobra.Command {
 	return cmd
 }
 
-// costsRun runs a costs command that prints its own text: only errors and
-// --json go through the result envelope.
+// costsRun runs a command that prints its own text (the costs commands and
+// migrate's apply step): only errors and --json go through the result
+// envelope.
 func (o *options) costsRun(cmd *cobra.Command, run func(*operation.Result) error) error {
 	result := operation.NewResult(cmd.CommandPath())
 	err := run(&result)
@@ -313,6 +314,11 @@ func runCostsView(
 // ensureIngested reads a tool's transcripts inline before its first report, so
 // the command works before the hooks are applied.
 func ensureIngested(cmd *cobra.Command, o *options) error {
+	// Reports never open the old ledger, and never create an empty one
+	// beside it: they ask for the migration instead.
+	if err := costs.Gate(cmd.Context(), false); err != nil {
+		return err
+	}
 	paths, err := costs.Locations()
 	if err != nil {
 		return err
```

The hook is not gated: it writes only the new import log, which no migration moves or compares, and starts the worker, which runs the gate (spec 5.4). When `Locations` fails, for example because a runtime folder isn't private, the hook says why on stderr and records nothing, and `costs status` and the reports fail with the same reason.

- [ ] **Step 3: Check nothing else reads the old variables**

```zsh
rg -n "CLAUDE_COSTS_(LEDGER|STATE|RATES)" internal cmd; echo EXIT:${pipestatus[1]}
```

Expected now: only `internal/costs/move.go` (the `OldPaths` comment and `OldLocations`). After Task 5, also `internal/migrate/migrate.go` (`OldLocationVariables`).

- [ ] **Step 4: Run the gates, then commit**

Run the gates. Expected: `EXIT:0` each; `TestMovingTheLedgerKeepsEveryRow` still passes, now with per-tool cache paths.

```zsh
git add internal/costs internal/cli/costs.go
git commit -F - <<'EOF'
feat(costs)!: keep the costs files in Workbench's runtime folders

The ledger and price caches live in <data>/costs, the import log and lock
in <state>/costs and overrides in <config>/costs. Every ledger open passes
one gate: the import worker moves the old files itself and imports once the
ledger has moved, and reports ask for `workbench migrate --apply`. The hook
says on stderr when Workbench's folders can't be used. CLAUDE_COSTS_LEDGER,
CLAUDE_COSTS_STATE and CLAUDE_COSTS_RATES no longer relocate anything.

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
EOF
echo EXIT:$?
```

---

### Task 5: The `internal/migrate` package

**Files:**
- Create: `internal/migrate/migrate.go`
- Create: `internal/migrate/0001_costs_folders.go`
- Create: `internal/migrate/0002_completion_cache.go`
- Create: `internal/migrate/0003_old_costs_cache.go`

**Interfaces:**
- Consumes: `costs.LocationsIn`, `costs.OldLocations`, `costs.CheckFolders`, `costs.MoveFolders`, `costs.Folders*` (Task 3); `operation.Resolve`, `operation.TryLock`, `operation.PrivateDirectory`, `operation.ReplacePrivateFile`, `operation.ReadPrivateInput(path, limit)` (`state.go:72`), `operation.Paths.MigrateLock`.
- Produces (Tasks 6 and 7 rely on these):
  - `type Status int` with `Done`, `Pending`, `Unfinished`, `Conflict`, `Busy`, and `String()`
  - `type Locations struct { Home string; Runtime operation.Paths; Costs costs.Paths; OldCosts costs.OldPaths; OldCache, OldScriptCache string; Notes []string }`
  - `type Migration struct { ID, Title, Doing, What string; Check func(Locations) (Status, string); Run func(context.Context, Locations) ([]string, error) }`
  - `type Finding struct { Migration; Status Status; Detail string }`
  - `var Registry []Migration`, `var OldLocationVariables []string`
  - `func IDs() []string`, `func Locate() (Locations, error)`, `func Check(l Locations) []Finding`
  - `func Run(ctx context.Context, l Locations, say func(string)) error`
  - `func Probe(l Locations, wanted []string) []string`
  - `func Plan(scope operation.Scope, findings []Finding) operation.Plan`
  - `func DoneAt(l Locations, id string) string` (this machine's time, "Oct 14 at 6:33 PM")

A *Done* migration's detail may still name a file it keeps because Workbench didn't write it (`0001`, `0002`, `0003`); `workbench migrate` shows it (spec 6.1, 6.2). A run that meets a conflict exits 4 (`ExitConflict`) and says the owner must remove one copy of each file it named; anything else that stops a migration exits 3, and the next apply tries again (spec 5.3).

- [ ] **Step 1: Write `internal/migrate/migrate.go`**

```go
// Package migrate holds Workbench's numbered, idempotent migrations: moves of
// files Workbench itself wrote outside its runtime folders into them, and
// deletes of caches and logs it wrote there (ADR 0008). A move of another
// package's files is done by that package; this package holds the list, the
// check and run loop, and the run record.
package migrate

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/Sawmonabo/workbench/internal/costs"
	"github.com/Sawmonabo/workbench/internal/operation"
)

// Status is where one migration stands.
type Status int

// The statuses. Check never reports Busy: only a run finds a lock held or the
// ledger open.
const (
	Done       Status = iota // no old file the migration knows remains
	Pending                  // an old file exists and its new path does not
	Unfinished               // a stopped run left its marks
	Conflict                 // old and new data both exist, or old data can't be found
	Busy                     // a lock is held or the ledger is open
)

func (s Status) String() string {
	return [...]string{"done", "pending", "unfinished", "conflict", "busy"}[s]
}

// Locations holds both sets of paths: the new ones from operation.Resolve and
// the old ones as the old code resolved them. A relative old value can't be
// found again; its location is "" and Notes says so.
type Locations struct {
	Home     string
	Runtime  operation.Paths
	Costs    costs.Paths
	OldCosts costs.OldPaths
	// OldCache is ${XDG_CACHE_HOME:-$HOME/.cache}, where the old startup files
	// saved the completion script; OldScriptCache is
	// ${CLAUDE_COSTS_CACHE:-$HOME/.cache/claude-costs}, the old script's cache.
	OldCache, OldScriptCache string
	Notes                    []string
}

// Migration is one numbered migration. Check changes nothing; its text says
// what a run would do, or, for a Done migration, what it found and leaves.
// Run finishes whatever is left, and running it twice only completes the
// remaining moves. Run returns the files, home-relative, it found and left
// because Workbench didn't write them.
type Migration struct {
	ID    string // "0001-costs-folders"; names it in the plan and output
	Title string // "Move the costs files into Workbench's folders"
	Doing string // "moving the costs files into Workbench's folders"
	What  string // one paragraph naming every path it touches
	Check func(Locations) (Status, string)
	Run   func(context.Context, Locations) ([]string, error)
}

// Registry lists every migration in number order. A released migration is
// never renumbered or removed; adding one is a numbered file and a line here.
var Registry = []Migration{costsFolders, completionCache, oldCostsCache}

// OldLocationVariables are the variables the old locations were read from.
// The apply passes each one that is set to its scripts as
// WORKBENCH_OLD_<NAME>, and home/.chezmoitemplates/old-locations.sh hands it
// back under its own name to the installed workbench only; keep both lists
// the same.
var OldLocationVariables = []string{
	"XDG_DATA_HOME", "XDG_STATE_HOME", "XDG_CONFIG_HOME", "XDG_CACHE_HOME",
	"CLAUDE_COSTS_LEDGER", "CLAUDE_COSTS_STATE", "CLAUDE_COSTS_RATES", "CLAUDE_COSTS_CACHE",
}

// IDs are the registry's IDs, which the apply passes to the installed binary
// as WORKBENCH_MIGRATIONS so it can say which ones it doesn't know.
func IDs() []string {
	ids := make([]string, len(Registry))
	for i, m := range Registry {
		ids[i] = m.ID
	}
	return ids
}

// Locate resolves both sets of paths. It reads paths only.
func Locate() (Locations, error) {
	c, err := operation.Resolve(operation.Options{ReadOnly: true})
	if err != nil {
		return Locations{}, err
	}
	l := Locations{
		Home:     c.Home,
		Runtime:  c.Paths,
		Costs:    costs.LocationsIn(c.Home, c.Paths),
		OldCosts: costs.OldLocations(c.Home),
	}
	l.Notes = append(l.Notes, l.OldCosts.Notes...)
	l.OldCache = l.shellDefault("XDG_CACHE_HOME", filepath.Join(c.Home, ".cache"))
	l.OldScriptCache = l.shellDefault(
		"CLAUDE_COSTS_CACHE",
		filepath.Join(c.Home, ".cache", "claude-costs"),
	)
	return l, nil
}

// shellDefault is ${name:-fallback} as the old startup files and script
// resolved it. A relative value can't be found again: it is noted instead.
func (l *Locations) shellDefault(name, fallback string) string {
	switch value := os.Getenv(name); {
	case value == "":
		return fallback
	case filepath.IsAbs(value):
		return filepath.Clean(value)
	default:
		l.Notes = append(
			l.Notes,
			name+" is relative ("+value+"), so the files it named can't be found",
		)
		return ""
	}
}

// Finding is one migration with what its Check found.
type Finding struct {
	Migration
	Status Status
	Detail string
}

// Check runs every migration's Check, in number order.
func Check(l Locations) []Finding {
	findings := make([]Finding, len(Registry))
	for i, m := range Registry {
		status, detail := m.Check(l)
		findings[i] = Finding{m, status, detail}
	}
	return findings
}

func due(f Finding) bool { return f.Status == Pending || f.Status == Unfinished }

// Run runs every migration with something to do, in number order, and says
// what it does through say, one line at a time: "checking", then for each
// migration "<ID>: <Doing>" and "<ID>: done", or "nothing to migrate". It
// checks first, and takes no lock when nothing has anything to do. Otherwise
// it holds [operation.Paths.MigrateLock], so one run moves files at a time. A
// migration that can't finish says why and the others still run; the error
// then names those that didn't finish.
func Run(ctx context.Context, l Locations, say func(string)) error {
	say("checking")
	findings := Check(l)
	var failed []string
	exit := operation.ExitBlocked
	for _, f := range findings {
		if f.Status == Conflict {
			say(f.ID + ": " + f.Detail)
			failed, exit = append(failed, f.ID), operation.ExitConflict
		}
	}
	if !slices.ContainsFunc(findings, due) {
		if len(failed) == 0 {
			say("nothing to migrate")
			return nil
		}
		return unfinished(failed, exit)
	}
	release, held, err := operation.TryLock(l.Runtime.MigrateLock())
	if err != nil {
		return err
	}
	if held {
		say("another migration run is moving files, and the next apply tries again")
		return operation.Fail(operation.ExitBlocked, "busy", "Another migration run holds the lock")
	}
	defer release()
	for _, f := range findings {
		if !due(f) {
			continue
		}
		if err = ctx.Err(); err != nil {
			return err
		}
		if err = runOne(ctx, l, f, say); err != nil {
			failed = append(failed, f.ID)
			if problem, ok := errors.AsType[*operation.Error](
				err,
			); ok &&
				problem.Category == "conflict" {
				exit = operation.ExitConflict
			}
		}
	}
	if len(failed) > 0 {
		return unfinished(failed, exit)
	}
	return nil
}

// runOne runs one migration and says how it ended; the error is why it didn't
// finish.
func runOne(ctx context.Context, l Locations, f Finding, say func(string)) error {
	say(f.ID + ": " + f.Doing)
	left, err := f.Run(ctx, l)
	if len(left) > 0 {
		say(f.ID + ": kept, because Workbench didn't write them: " + strings.Join(left, ", "))
	}
	if err != nil {
		say(f.ID + ": " + strings.Join(strings.Fields(err.Error()), " "))
		return err
	}
	say(f.ID + ": done")
	if err = markDone(l, f.ID); err != nil {
		say(f.ID + ": the migration record wasn't saved: " + err.Error())
	}
	return nil
}

// unfinished is the error of a run some migrations didn't finish. A conflict
// waits for the owner, who must remove one copy of each file the output named;
// anything else the next apply tries again.
func unfinished(ids []string, exit operation.Exit) error {
	names := strings.Join(ids, ", ")
	if exit == operation.ExitConflict {
		return operation.Fail(exit, "conflict", names+
			" can't finish until you remove one copy of each file named above")
	}
	return operation.Fail(exit, "migrate", names+
		" didn't finish (the output above says why), and the next apply tries again")
}

// Probe is what `workbench migrate --probe` prints for the apply's plan: one
// "migrations: <ID>: <paths>" line per migration with something to do,
// "migrations: ? …" for one that conflicts or that this binary doesn't know
// (wanted names the planning binary's migrations, from WORKBENCH_MIGRATIONS),
// or the one line "migrations: = none to run". No line holds "; ", which the
// plan uses to join one step's lines.
func Probe(l Locations, wanted []string) []string {
	var lines []string
	for _, f := range Check(l) {
		switch f.Status {
		case Pending, Unfinished:
			lines = append(lines, "migrations: "+f.ID+": "+f.Detail)
		case Conflict:
			lines = append(lines, "migrations: ? "+f.ID+": "+f.Detail)
		default:
		}
	}
	for _, id := range wanted {
		if !slices.Contains(IDs(), id) {
			lines = append(
				lines,
				"migrations: ? "+id+" needs a newer installed workbench (workbench update)",
			)
		}
	}
	if len(lines) == 0 {
		return []string{"migrations: = none to run"}
	}
	return lines
}

// Plan is what `workbench migrate --apply` asks approval for: one fixed effect
// per migration with something to do, named by its ID and described by its
// paths, so the digest changes when the paths do. A migration in conflict is
// a warning.
func Plan(scope operation.Scope, findings []Finding) operation.Plan {
	plan := operation.Plan{
		Scope:          scope,
		Edits:          []operation.Edit{},
		Prerequisites:  []string{},
		RecoveryLimits: []string{"Migrations are not checkpointed; their moves keep every byte"},
		Complete:       true,
	}
	for _, f := range findings {
		switch {
		case due(f):
			plan.Effects = append(plan.Effects, operation.Effect{
				Name:        f.ID,
				Description: f.Detail,
				Privilege:   "user",
				Recovery:    "moves keep every byte; move a file back by hand to undo",
				Title:       f.Title,
				Summary:     f.Detail,
				What:        f.What,
				RunsAs:      "you",
				Fixed:       true,
				Checked:     true,
			})
		case f.Status == Conflict:
			plan.Warnings = append(plan.Warnings, f.ID+": "+f.Detail)
		}
	}
	return plan
}

// record is migrations.json in Workbench's state folder: when each migration
// reached Done in a `workbench migrate` or apply run. Only `workbench migrate`
// reads it, to say when; correctness never depends on it.
type record struct {
	SchemaVersion int               `json:"schema_version"`
	Done          map[string]string `json:"done"`
}

func recordPath(l Locations) string { return filepath.Join(l.Runtime.State, "migrations.json") }

func readRecord(l Locations) record {
	r := record{SchemaVersion: 1, Done: map[string]string{}}
	data, err := operation.ReadPrivateInput(recordPath(l), 1<<20)
	if err == nil && json.Unmarshal(data, &r) == nil && r.Done != nil {
		return r
	}
	return record{SchemaVersion: 1, Done: map[string]string{}}
}

// DoneAt is when the record says id reached Done, in this machine's time
// ("Oct 14 at 6:33 PM"), "" when it doesn't say.
func DoneAt(l Locations, id string) string {
	at, err := time.Parse(time.RFC3339, readRecord(l).Done[id])
	if err != nil {
		return ""
	}
	return at.Local().Format("Jan 2 at 3:04 PM")
}

func markDone(l Locations, id string) error {
	r := readRecord(l)
	r.Done[id] = time.Now().UTC().Format(time.RFC3339)
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	if _, err = operation.PrivateDirectory(l.Runtime.State, ""); err != nil {
		return err
	}
	return operation.ReplacePrivateFile(recordPath(l), append(data, '\n'))
}

// homeRelative shows path under home as ~/…, as the plan shows home paths.
func homeRelative(home, path string) string {
	if rest, ok := strings.CutPrefix(path, home+string(filepath.Separator)); ok {
		return "~/" + rest
	}
	return path
}

// removeIfThere deletes path; a path already gone is not an error.
func removeIfThere(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}
```

- [ ] **Step 2: Write `internal/migrate/0001_costs_folders.go`**

```go
package migrate

import (
	"context"

	"github.com/Sawmonabo/workbench/internal/costs"
)

// costsFolders moves the costs files out of the claude-costs folders. The
// move belongs to internal/costs, which alone opens, checks and copies the
// ledger; the import worker runs the same move by itself (costs.Gate), without
// asking, because usage left unread could be lost.
var costsFolders = Migration{
	ID:    "0001-costs-folders",
	Title: "Move the costs files into Workbench's folders",
	Doing: "moving the costs files into Workbench's folders",
	What: "Moves the costs ledger and its price caches from the claude-costs data folder " +
		"into Workbench's data folder (costs/), and your price overrides into its config " +
		"folder, then deletes the old import log and lock and removes each emptied " +
		"claude-costs folder. A file whose old and new paths both hold different data " +
		"stays where it is, and every other file still moves. A file it doesn't know " +
		"stays, with its folder.",
	Check: func(l Locations) (Status, string) {
		check := costs.CheckFolders(l.OldCosts, l.Costs)
		switch check.State {
		case costs.FoldersPending:
			return Pending, check.Text
		case costs.FoldersUnfinished:
			return Unfinished, check.Text
		case costs.FoldersConflict:
			return Conflict, check.Text
		default:
			return Done, check.Text
		}
	},
	Run: func(ctx context.Context, l Locations) ([]string, error) {
		return costs.MoveFolders(ctx, l.OldCosts, l.Costs)
	},
}
```

- [ ] **Step 3: Write `internal/migrate/0002_completion_cache.go`**

```go
package migrate

import (
	"bufio"
	"context"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// completionCache deletes the Tab-completion script the old startup files
// saved in ${XDG_CACHE_HOME:-~/.cache}. The startup files now save it in
// Workbench's cache folder and rebuild it themselves, so it is deleted, not
// moved. Only a file whose first line is the marker they wrote counts; any
// other file of that name stays and is reported.
var completionCache = Migration{
	ID:    "0002-completion-cache",
	Title: "Delete the old shell completion cache",
	Doing: "deleting the old shell completion cache",
	What: "Deletes workbench-completion.zsh or .bash from ~/.cache (XDG_CACHE_HOME), the " +
		"Tab-completion script earlier startup files saved; the next shell saves it in " +
		"Workbench's cache folder. A file of that name Workbench didn't write stays, and " +
		"is named.",
	Check: func(l Locations) (Status, string) {
		ours, other := oldCompletions(l)
		var text []string
		if len(ours) > 0 {
			text = append(text, "deletes "+strings.Join(ours, ", "))
		}
		if len(other) > 0 {
			text = append(text, "keeps "+strings.Join(other, ", ")+" (Workbench didn't write it)")
		}
		if len(ours) == 0 {
			return Done, strings.Join(text, " and ")
		}
		return Pending, strings.Join(text, " and ")
	},
	Run: func(_ context.Context, l Locations) ([]string, error) {
		for _, path := range completionPaths(l) {
			if writtenByStartup(path) {
				if err := removeIfThere(path); err != nil {
					return nil, err
				}
			}
		}
		_, other := oldCompletions(l)
		return other, nil
	},
}

// completionMarker is the first line the old startup files wrote: "# " and
// the resolved path of the workbench they ran.
var completionMarker = regexp.MustCompile(`^# /.*/workbench$`)

func completionPaths(l Locations) []string {
	if l.OldCache == "" {
		return nil
	}
	return []string{
		filepath.Join(l.OldCache, "workbench-completion.zsh"),
		filepath.Join(l.OldCache, "workbench-completion.bash"),
	}
}

// oldCompletions are the old caches still there, home-relative: ours carry
// the marker, other files of those names don't.
func oldCompletions(l Locations) (ours, other []string) {
	for _, path := range completionPaths(l) {
		if _, err := os.Lstat(path); err != nil {
			continue
		}
		if writtenByStartup(path) {
			ours = append(ours, homeRelative(l.Home, path))
		} else {
			other = append(other, homeRelative(l.Home, path))
		}
	}
	return ours, other
}

// writtenByStartup reports whether path is a regular file whose first line is
// the marker. That one line is the only thing read.
func writtenByStartup(path string) bool {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		return false
	}
	file, err := os.Open(path)
	if err != nil {
		return false
	}
	defer func() { _ = file.Close() }()
	line, _ := bufio.NewReader(io.LimitReader(file, 4096)).ReadString('\n')
	return completionMarker.MatchString(strings.TrimSuffix(line, "\n"))
}
```

- [ ] **Step 4: Write `internal/migrate/0003_old_costs_cache.go`**

```go
package migrate

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// oldCostsCache deletes the parse cache of the claude-costs script before
// Workbench, which nothing has read since 76c3f71: derived data only. Any
// other file in its folder stays and is reported.
var oldCostsCache = Migration{
	ID:    "0003-old-costs-cache",
	Title: "Delete the old claude-costs script's cache",
	Doing: "deleting the old claude-costs script's cache",
	What: "Deletes the cached transcript summaries the claude-costs script kept in " +
		"~/.cache/claude-costs (CLAUDE_COSTS_CACHE), which nothing reads any more, and " +
		"removes the folder once it is empty. Any other file there stays, and is named.",
	Check: func(l Locations) (Status, string) {
		ours, other := oldCacheFiles(l)
		var text []string
		if len(ours) > 0 {
			text = append(text, fmt.Sprintf(
				"deletes %d cached file(s) from %s",
				len(ours),
				homeRelative(l.Home, l.OldScriptCache),
			))
		}
		if len(other) > 0 {
			text = append(text, "keeps "+strings.Join(other, ", ")+" (Workbench didn't write them)")
		}
		if len(ours) == 0 {
			return Done, strings.Join(text, " and ")
		}
		return Pending, strings.Join(text, " and ")
	},
	Run: func(_ context.Context, l Locations) ([]string, error) {
		ours, _ := oldCacheFiles(l)
		for _, name := range ours {
			if err := removeIfThere(filepath.Join(l.OldScriptCache, name)); err != nil {
				return nil, err
			}
		}
		if err := os.Remove(l.OldScriptCache); err != nil && !errors.Is(err, os.ErrNotExist) {
			_, other := oldCacheFiles(l)
			return other, nil
		}
		return nil, nil
	},
}

// oldCacheName is the old script's naming: a 32-hex digest, .json.gz, or the
// .json.tmp of a write that stopped.
var oldCacheName = regexp.MustCompile(`^[0-9a-f]{32}\.json\.(gz|tmp)$`)

// oldCacheFiles lists the old script's cache folder: ours by name, other
// files home-relative. Reading the folder is the only read.
func oldCacheFiles(l Locations) (ours, other []string) {
	if l.OldScriptCache == "" {
		return nil, nil
	}
	entries, err := os.ReadDir(l.OldScriptCache)
	if err != nil {
		return nil, nil
	}
	for _, entry := range entries {
		if entry.Type().IsRegular() && oldCacheName.MatchString(entry.Name()) {
			ours = append(ours, entry.Name())
		} else {
			other = append(
				other,
				homeRelative(l.Home, filepath.Join(l.OldScriptCache, entry.Name())),
			)
		}
	}
	return ours, other
}
```

- [ ] **Step 5: Run the gates, then commit**

Run the gates. Expected: `EXIT:0` each.

```zsh
git add internal/migrate
git commit -F - <<'EOF'
feat(migrate): registry, check and run loop, and the first three migrations

0001 moves the costs files through internal/costs, 0002 deletes the old
completion cache, 0003 deletes the old script's parse cache; files
Workbench didn't write stay and are named. A run with nothing to do takes
no lock, and the record says when each finished.

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
EOF
echo EXIT:$?
```

---

### Task 6: `workbench migrate`

**Files:**
- Create: `internal/cli/migrate.go`
- Modify: `internal/cli/root.go` (`root.AddCommand`)

**Interfaces:**
- Consumes: `migrate.Locate`, `Check`, `Run`, `Probe`, `Plan`, `DoneAt` (Task 5); `o.action(machineAction, handler)` and `o.costsRun` (`root.go:176`, `costs.go:94`); `consentFor`, `agentRefusal`, `hasTerminal`, `writePlan` (`consent.go:12`, `prompt.go:35`, `prompt.go:56`, `planview.go:23`); `operation.WithMutation` (`operation/plan.go:288`).
- Produces: `workbench migrate [--apply [--dry-run] [--approve-plan D] [--yes]]`, hidden `workbench migrate --probe` and `workbench migrate run`, each printing `==> migrations: <line>`. When only conflicts are left, `--apply` names them as warnings and exits 4.

- [ ] **Step 1: Write `internal/cli/migrate.go`**

```go
package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Sawmonabo/workbench/internal/migrate"
	"github.com/Sawmonabo/workbench/internal/operation"
)

// migrateCommand lists and runs Workbench's migrations (internal/migrate).
// Without flags it lists them and changes nothing. --apply runs the ones with
// something to do, under the approval rules apply uses. The hidden --probe
// and run are the apply's migrations step: they run inside an apply that
// holds the locks, so they resolve paths only and print their own lines.
func migrateCommand(o *options) *cobra.Command {
	var apply, dryRun, probe bool
	cmd := &cobra.Command{
		Use:   "migrate",
		Short: "Move files earlier Workbench releases left outside Workbench's folders",
		Long: "Without flags, list each migration and whether it has anything to do; nothing " +
			"changes. --apply runs every migration with something to do: at a terminal it shows " +
			"the list and asks once; --dry-run --json prints it with a plan_digest that " +
			"--approve-plan DIGEST runs exactly; without a terminal it needs --approve-plan or " +
			"--yes, and a coding agent must use --approve-plan. Every apply runs them too, as its " +
			"migrations step, and the costs import moves the costs files by itself.",
		Args: cobra.NoArgs,
	}
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		switch {
		case probe:
			return o.costsRun(
				cmd,
				func(*operation.Result) error { return migrateProbe(cmd.OutOrStdout()) },
			)
		case !apply && (dryRun || o.yes || o.approvePlan != ""):
			return o.costsRun(cmd, func(*operation.Result) error {
				return operation.Fail(
					operation.ExitInvalid,
					"invocation",
					"--dry-run, --yes and --approve-plan go with --apply",
				)
			})
		case !apply:
			return o.action(machineAction, migrateList)(cmd, nil)
		}
		return o.action(
			machineAction,
			func(cmd *cobra.Command, c operation.Context) (operation.Result, error) {
				return o.migrateApply(cmd, c, dryRun)
			},
		)(
			cmd,
			nil,
		)
	}
	flags := cmd.Flags()
	flags.BoolVar(&apply, "apply", false, "Run every migration that has something to do")
	flags.BoolVar(&dryRun, "dry-run", false, "With --apply, show what would run and exit")
	flags.BoolVar(
		&o.yes,
		"yes",
		false,
		"With --apply, run without asking (refused for coding agents)",
	)
	flags.BoolVar(&probe, "probe", false, "Print the apply's probe lines")
	_ = flags.MarkHidden("probe")
	o.approveFlag(cmd)
	cmd.AddCommand(migrateRunCommand(o))
	return cmd
}

// migrateRunCommand is the apply's migrations step: run every migration with
// something to do and print what happens. Ticking the step is the approval.
func migrateRunCommand(o *options) *cobra.Command {
	return &cobra.Command{
		Use:    "run",
		Short:  "Apply step: run every migration that has something to do",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return o.costsRun(cmd, func(*operation.Result) error {
				locations, err := migrate.Locate()
				if err != nil {
					return err
				}
				return migrate.Run(cmd.Context(), locations, sayMigration(cmd.OutOrStdout()))
			})
		},
	}
}

// sayMigration prints a migration line in the style of the apply's scripts.
func sayMigration(out io.Writer) func(string) {
	return func(line string) { _, _ = fmt.Fprintln(out, "==> migrations: "+line) }
}

// migrateProbe prints the probe lines; on a failure it prints nothing to
// stdout, so the script says it couldn't check.
func migrateProbe(out io.Writer) error {
	locations, err := migrate.Locate()
	if err != nil {
		return err
	}
	wanted := strings.Fields(os.Getenv("WORKBENCH_MIGRATIONS"))
	_, err = fmt.Fprintln(out, strings.Join(migrate.Probe(locations, wanted), "\n"))
	return err
}

// migrateList shows each migration and where it stands.
func migrateList(cmd *cobra.Command, _ operation.Context) (operation.Result, error) {
	result := operation.NewResult(cmd.CommandPath())
	locations, err := migrate.Locate()
	if err != nil {
		return result, err
	}
	result.Warnings = append(result.Warnings, locations.Notes...)
	for _, f := range migrate.Check(locations) {
		component := operation.Component{Name: f.ID, Title: f.ID + " " + f.Title}
		switch f.Status {
		case migrate.Done:
			component.Status, component.Message = operation.StatusUnchanged, "nothing to do"
			if at := migrate.DoneAt(locations, f.ID); at != "" {
				component.Message = "done " + at
			}
			if f.Detail != "" {
				component.Message += ", " + f.Detail
			}
		case migrate.Conflict:
			component.Status, component.Message = operation.StatusConflict, f.Detail
		default:
			component.Status = operation.StatusSkipped
			component.Message = f.Status.String() + ": " + f.Detail + "; workbench migrate --apply runs it"
		}
		result.Results = append(result.Results, component)
	}
	return result, nil
}

// checkMigrate is migrate --apply's consent gate, apply's rules: a run nobody
// approved is refused before anything happens, and a coding agent gets only
// --approve-plan.
func checkMigrate(o *options, dryRun bool) error {
	switch {
	case dryRun:
		return nil
	case o.yes && o.approvePlan != "":
		return operation.Fail(
			operation.ExitInvalid,
			"invocation",
			"--yes and --approve-plan both approve; pick one",
		)
	case operation.AgentSession() && (o.yes || o.approvePlan == ""):
		return operation.Fail(operation.ExitBlocked, "consent", agentRefusal)
	case o.yes || o.approvePlan != "":
		return nil
	case o.interactive() && hasTerminal():
		return nil
	}
	return operation.Fail(
		operation.ExitBlocked,
		"consent",
		"Nothing approved this run; read it with workbench migrate --apply --dry-run --json, "+
			"then pass --approve-plan DIGEST",
	)
}

// migrateApply shows "Checking migrations" while it checks, then runs the
// migrations with something to do once the plan of them is approved.
func (o *options) migrateApply(
	cmd *cobra.Command,
	c operation.Context,
	dryRun bool,
) (operation.Result, error) {
	result := operation.NewResult(cmd.CommandPath())
	if err := checkMigrate(o, dryRun); err != nil {
		return result, err
	}
	stop := c.ShowProgress("Checking migrations")
	locations, err := migrate.Locate()
	stop()
	if err != nil {
		return result, err
	}
	plan := migrate.Plan(c.Scope, migrate.Check(locations))
	result.Warnings = append(result.Warnings, locations.Notes...)
	result.Warnings = append(result.Warnings, plan.Warnings...)
	result.PlanDigest = plan.Digest()
	if dryRun || len(plan.Effects) == 0 {
		return migrateShown(result, plan, dryRun)
	}
	consent := consentFor(o, o.approvePlan)
	if o.yes {
		consent.ApprovedDigest = result.PlanDigest
		if !o.json {
			if err = writePlan(cmd.OutOrStdout(), plan); err != nil {
				return result, err
			}
		}
	}
	c.ReadOnly = false
	err = operation.WithMutation(
		cmd.Context(), c, plan, consent,
		func(_ context.Context, preview operation.Context) (operation.Plan, error) {
			current, err := migrate.Locate()
			if err != nil {
				return operation.Plan{}, err
			}
			locations = current
			return migrate.Plan(preview.Scope, migrate.Check(current)), nil
		},
		func(*operation.Mutation) error {
			return migrate.Run(cmd.Context(), locations, sayMigration(cmd.OutOrStdout()))
		},
	)
	status := operation.StatusComplete
	if err != nil {
		status = operation.StatusFailed
	}
	result.Results = append(result.Results, operation.Component{Name: "migrations", Status: status})
	return result, err
}

// migrateShown is the result of a dry run, or of a run with nothing to move:
// the plan, "Nothing to migrate", or the conflicts that need the owner.
func migrateShown(
	result operation.Result,
	plan operation.Plan,
	dryRun bool,
) (operation.Result, error) {
	component := operation.Component{
		Name:    "migrations",
		Status:  operation.StatusComplete,
		Details: plan,
	}
	var err error
	switch {
	case dryRun:
	case len(plan.Warnings) > 0:
		component.Status, component.Details = operation.StatusConflict, nil
		component.Message = "Nothing can move until you remove one copy of each file named above"
		err = operation.Fail(operation.ExitConflict, "migrate", "A migration needs you: "+
			strings.Join(plan.Warnings, ", "))
	default:
		component.Status, component.Message, component.Details = operation.StatusUnchanged, "Nothing to migrate", nil
	}
	result.Results = append(result.Results, component)
	return result, err
}
```

- [ ] **Step 2: Register it**

```diff
diff --git a/internal/cli/root.go b/internal/cli/root.go
index 49316e9..d37ce61 100644
--- a/internal/cli/root.go
+++ b/internal/cli/root.go
@@ -109,6 +109,7 @@ func newRoot(o *options) *cobra.Command {
 		vscodeSettingsCommand(o),
 		askpassCommand(o),
 		costsCommand(o),
+		migrateCommand(o),
 		guardCommand(),
 	)
 	registerCompletions(root)
```

- [ ] **Step 3: Run the gates**

Run the gates. Expected: `EXIT:0` each.

- [ ] **Step 4: Check the command and its consent rules in a disposable home**

```zsh
S=$(cd "$(mktemp -d)" && pwd -P); H=$S/home; mkdir -p $H/.local/bin
go build -o $H/.local/bin/workbench ./cmd/workbench; echo EXIT:$?
E=(env -i HOME=$H PATH=/usr/bin:/bin TMPDIR=$S LANG=C.UTF-8)
"${E[@]}" $H/.local/bin/workbench migrate; echo EXIT:$?
"${E[@]}" $H/.local/bin/workbench migrate --apply --non-interactive; echo EXIT:$?
"${E[@]}" CLAUDECODE=1 $H/.local/bin/workbench migrate --apply --yes; echo EXIT:$?
"${E[@]}" $H/.local/bin/workbench migrate --dry-run; echo EXIT:$?
"${E[@]}" $H/.local/bin/workbench migrate --probe; echo EXIT:$?
"${E[@]}" $H/.local/bin/workbench migrate run; echo EXIT:$?
```

Expected, in order:
1. Three lines, each `… unchanged — nothing to do`; `EXIT:0`.
2. `Nothing approved this run; read it with workbench migrate --apply --dry-run --json, then pass --approve-plan DIGEST`; `EXIT:3` (refused before it checks anything, as apply is).
3. `An agent runs this; use --dry-run --json then --approve-plan`; `EXIT:3`.
4. `--dry-run, --yes and --approve-plan go with --apply`; `EXIT:2`.
5. `migrations: = none to run`; `EXIT:0`.
6. `==> migrations: checking`, then `==> migrations: nothing to migrate`; `EXIT:0`.

Seeded runs are in Task 8.

- [ ] **Step 5: Commit**

```zsh
git add internal/cli/migrate.go internal/cli/root.go
git commit -F - <<'EOF'
feat(cli): workbench migrate lists and runs the migrations

--apply asks the way apply does (--dry-run --json, --approve-plan, --yes,
agents refused --yes), shows "Checking migrations" while it checks, and
exits 4 naming the conflicts when only conflicts are left. The hidden
--probe and run are the apply's migrations step.

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
EOF
echo EXIT:$?
```

---

### Task 7: Apply's `migrations` step, planning steps, scripts and startup files

**Files:**
- Modify: `internal/machine/effects.go`, `internal/machine/probe.go`, `internal/machine/dependencies.go`
- Create: `home/.chezmoiscripts/shared/run_after_05-migrations.sh.tmpl`, `home/.chezmoitemplates/old-locations.sh`
- Modify: `home/.chezmoiscripts/shared/run_after_90-costs-ingest.sh.tmpl`, `home/dot_zshrc.tmpl`, `home/private_dot_bashrc.tmpl`

**Interfaces:**
- Consumes: `migrate.IDs`, `migrate.OldLocationVariables` (Task 5); `workbench migrate --probe` / `migrate run` (Task 6).
- Produces: effect `migrations` (non-fixed, on by default, source `05-migrations`, switch `WORKBENCH_EFFECT_MIGRATIONS`). Planning steps `checking migrations`, then `migrations: none to run`, `migrations: N to run`, `migrations: N need attention` (or both, comma-joined), or `migrations: couldn't check` only when the probe itself failed or timed out.

- [ ] **Step 1: The `migrations` effect and the `costs-ingest` text**

The effect goes first in `sharedEffects`, so the plan lists it before the other shared steps. chezmoi runs scripts in the order of their full source paths (`.chezmoiscripts/darwin/…`, `linux/…`, `shared/…`, then `wsl/…`), so the migrations script always runs after the files and before `shared/run_after_90-costs-ingest`. On macOS and Linux that is after the platform's own scripts; on WSL it is before the `wsl/` ones.

```diff
diff --git a/internal/machine/effects.go b/internal/machine/effects.go
index b052b41..77f90b4 100644
--- a/internal/machine/effects.go
+++ b/internal/machine/effects.go
@@ -60,6 +60,7 @@ var effectSources = map[string][]string{
 	"global-tools":            {"30-global-tools"},
 	"tmux-plugins":            {"40-tmux-plugins"},
 	"costs-ingest":            {"90-costs-ingest"},
+	"migrations":              {"05-migrations"},
 	"macos-packages":          {"00-packages"},
 	"macos-apps-extensions":   {"50-apps-and-extensions"},
 	"brew-maintenance":        {"60-cleanup"},
@@ -106,6 +107,18 @@ var aiSecurityEffect = operation.Effect{
 
 // sharedEffects are the script owners every platform lists.
 var sharedEffects = []operation.Effect{
+	{
+		Name:        "migrations",
+		Description: "The installed workbench moves files earlier Workbench releases wrote into Workbench's own folders and deletes the old caches; never other tools' files",
+		Privilege:   "user",
+		Recovery:    "external; moves keep every byte, so moving a file back by hand restores the old layout",
+		Title:       "Move Workbench's files",
+		Summary:     "files earlier releases left outside Workbench's folders move into them",
+		What:        "Moves the files earlier Workbench releases wrote outside Workbench's own folders, such as the costs ledger, into them, and deletes the old caches they left. It never touches other tools' files, merges nothing, and leaves a file where it is when old and new data both exist. Unticking it skips the cache clean-ups and only delays the costs move: the next time Claude Code or Codex reports usage, Workbench moves the costs files itself, so no usage is lost when session files expire.",
+		Touches:     "the old claude-costs folders and completion cache, and Workbench's own folders",
+		RunsAs:      "you",
+		Undo:        "Not reverted automatically: every byte is kept, so moving the files back by hand restores the old layout for an older release",
+	},
 	{
 		Name:        "runtime-managers",
 		Description: "Native nvm/rustup and platform-owned uv/bun/Go; user runtime directories and default links",
@@ -162,9 +175,8 @@ var sharedEffects = []operation.Effect{
 		Title:       "Record AI usage",
 		Summary:     "new usage shows up in workbench costs",
 		What:        "Reads your latest Claude Code and Codex sessions into the costs ledger, the same thing the session hooks do.",
-		Touches:     "~/.local/share/claude-costs/ledger.sqlite",
 		RunsAs:      "you; downloads the official price pages",
-		Undo:        "Nothing to undo: the ledger is rebuilt from the session files",
+		Undo:        "Nothing to undo: rows already recorded stay, even after the session files they came from are deleted.",
 	},
 }
 
@@ -270,6 +282,11 @@ var windowsHostEffects = []operation.Effect{
 // results. Optional effects are deliberately absent until separately selected.
 func provisioningEffects(answers Answers) []operation.Effect {
 	effects := slices.Clone(sharedEffects)
+	for i := range effects {
+		if effects[i].Name == "costs-ingest" {
+			effects[i].Touches = costsLedgerTouches()
+		}
+	}
 	if runtime.GOOS == "darwin" {
 		effects = append(effects, macOSEffects...)
 	} else {
@@ -287,6 +304,15 @@ func provisioningEffects(answers Answers) []operation.Effect {
 	return effects
 }
 
+// costsLedgerTouches is where the costs ledger lives by default, shown the way
+// the plan shows home paths.
+func costsLedgerTouches() string {
+	if runtime.GOOS == "darwin" {
+		return "~/Library/Application Support/workbench/data/costs/ledger.sqlite"
+	}
+	return "~/.local/share/workbench/costs/ledger.sqlite"
+}
+
 // macOSOptionalEffects are the Mac steps that run only when selected by name,
 // like [optionalEffects] on WSL. The owning script checks WORKBENCH_EFFECT_<NAME>;
 // see [effectVariable]. Touch ID for sudo changes how every sudo on the Mac asks,
```

- [ ] **Step 2: The script environment**

The old-location variables reach every script as `WORKBENCH_OLD_<NAME>`, since chezmoi gives all scripts one environment and an `XDG_*_HOME` under its own name would change where installers put things (spec 5.1). `migrate` doesn't import `machine`, so there is no cycle.

```diff
diff --git a/internal/machine/dependencies.go b/internal/machine/dependencies.go
index 2287154..d1d2e3b 100644
--- a/internal/machine/dependencies.go
+++ b/internal/machine/dependencies.go
@@ -12,6 +12,7 @@ import (
 	"strings"
 	"time"
 
+	"github.com/Sawmonabo/workbench/internal/migrate"
 	"github.com/Sawmonabo/workbench/internal/operation"
 	"github.com/pelletier/go-toml/v2"
 )
@@ -240,6 +241,24 @@ func scriptEnvironment(c operation.Context, dependencies []operation.Dependency)
 	// The scripts download packages and tools, so they need the user's proxy
 	// and CA settings; nothing else of the caller's environment reaches them.
 	environment = append(environment, operation.NetworkEnvironment()...)
+	// The migrations and costs-ingest scripts run the installed workbench,
+	// which must find the runtime folders this apply resolved and, to find
+	// the files earlier releases left, the owner's old-location variables.
+	// Those reach the scripts as WORKBENCH_OLD_<NAME>, so no installer sees
+	// them; home/.chezmoitemplates/old-locations.sh hands them back.
+	environment = append(
+		environment,
+		"WORKBENCH_CONFIG_DIR="+c.Paths.Config,
+		"WORKBENCH_DATA_DIR="+c.Paths.Data,
+		"WORKBENCH_STATE_DIR="+c.Paths.State,
+		"WORKBENCH_CACHE_DIR="+c.Paths.Cache,
+		"WORKBENCH_MIGRATIONS="+strings.Join(migrate.IDs(), " "),
+	)
+	for _, name := range migrate.OldLocationVariables {
+		if value := os.Getenv(name); value != "" {
+			environment = append(environment, "WORKBENCH_OLD_"+name+"="+value)
+		}
+	}
 	return append(
 		environment,
 		"WORKBENCH_TOOL_PATH="+path,
```

- [ ] **Step 3: Probe the migrations script first, with its own planning steps**

`probeMigrations` is a separate method (keeps `probeEffects` within `funlen`). `probeResult` keeps the answered text and the unanswered text in separate fields, and `merge` joins each separately, so a script that prints both (the migrations script with `0001` in conflict and `0002` pending) keeps both. `fillProbed` sets the effect's `Delta` from the answered lines and its `ProbeNote` from the unanswered ones; `parseProbe` reads one script's lines. Change lines and `?` lines are also counted apart, so the outcome step can say "1 to run, 1 needs attention". This holds for every step, including ones two scripts carry (`windows-files`, `work-tools`).

```diff
diff --git a/internal/machine/probe.go b/internal/machine/probe.go
index d98840c..eaedb5c 100644
--- a/internal/machine/probe.go
+++ b/internal/machine/probe.go
@@ -4,6 +4,7 @@ import (
 	"context"
 	"encoding/json"
 	"errors"
+	"fmt"
 	"os"
 	"path/filepath"
 	"regexp"
@@ -26,21 +27,64 @@ const probeTimeout = 15 * time.Second
 // signal; nothing matches on the words of TEXT.
 var probeLine = regexp.MustCompile(`^([a-z0-9-]+): ([=?] )?(.+)$`)
 
-// probeResult is what the probe lines of one effect said: their text joined
-// with "; ", whether every line reported no change, and whether any line could
-// not check.
+// probeResult is what the probe lines of one effect said, kept apart by kind:
+// the text of the lines that answered and of those that could not check, each
+// joined with "; ", whether every line reported no change, and how many lines
+// were changes and how many could not check. Keeping them apart lets a step
+// show what was checked beside what was not, even when one script printed
+// both.
 type probeResult struct {
-	text       string
-	noChange   bool
-	unanswered bool
+	answered, unanswered string
+	noChange             bool
+	changes, checks      int
 }
 
 // merge adds another result for the same effect.
 func (r probeResult) merge(other probeResult) probeResult {
 	return probeResult{
-		r.text + "; " + other.text,
+		joinProbeText(r.answered, other.answered),
+		joinProbeText(r.unanswered, other.unanswered),
 		r.noChange && other.noChange,
-		r.unanswered || other.unanswered,
+		r.changes + other.changes,
+		r.checks + other.checks,
+	}
+}
+
+// joinProbeText joins two texts of one kind with "; ", skipping an empty one.
+func joinProbeText(a, b string) string {
+	if a == "" || b == "" {
+		return a + b
+	}
+	return a + "; " + b
+}
+
+// migrationsScript carries the migrations step. Its probe runs alone, before
+// the others, under its own planning steps, so an apply says that it checks
+// migrations and what it found.
+const migrationsScript = "05-migrations"
+
+// probeTally gathers the probe results of every script, from the parallel
+// probes as well as the migrations probe.
+type probeTally struct {
+	mu      sync.Mutex
+	results map[string][]probeResult
+	outcome map[string]probeOutcome
+}
+
+// record keeps one script's lines and how its probe ended, for each effect
+// the script carries. A failed or timed-out probe is never overwritten by
+// another script's answer for the same effect.
+func (t *probeTally) record(name string, lines map[string]probeResult, status string) {
+	t.mu.Lock()
+	defer t.mu.Unlock()
+	for effect, result := range lines {
+		t.results[effect] = append(t.results[effect], result)
+	}
+	for _, effect := range scriptEffects(name) {
+		if previous := t.outcome[effect].status; previous != "failed" &&
+			previous != "timeout" {
+			t.outcome[effect] = probeOutcome{status, probeNote(name, status)}
+		}
 	}
 }
 
@@ -63,7 +107,6 @@ func (p *preparation) probeEffects(ctx context.Context, c operation.Context) err
 	if err = os.Mkdir(directory, 0o700); err != nil {
 		return ctx.Err()
 	}
-	c.Step("checking what each step would do here")
 	environment := scriptEnvironment(c, p.Plan.Dependencies)
 	environment = append(
 		environment,
@@ -76,34 +119,23 @@ func (p *preparation) probeEffects(ctx context.Context, c operation.Context) err
 			environment[i] = "PATH=" + filepath.Join(p.scratch, "bin") + ":" + rest
 		}
 	}
-	var (
-		mu      sync.Mutex
-		wg      sync.WaitGroup
-		results = map[string][]probeResult{}
-		outcome = map[string]probeOutcome{}
-	)
+	tally := &probeTally{results: map[string][]probeResult{}, outcome: map[string]probeOutcome{}}
+	p.probeMigrations(ctx, c, directory, environment, scripts, tally)
+	c.Step("checking what each step would do here")
+	var wg sync.WaitGroup
 	for name, contents := range scripts {
-		if !p.active[name] {
+		if !p.active[name] || name == migrationsScript {
 			continue
 		}
 		wg.Add(1)
 		go func(name, contents string) {
 			defer wg.Done()
 			lines, status := p.runProbe(ctx, c, directory, environment, name, contents)
-			mu.Lock()
-			defer mu.Unlock()
-			for effect, result := range lines {
-				results[effect] = append(results[effect], result)
-			}
-			for _, effect := range scriptEffects(name) {
-				if previous := outcome[effect].status; previous != "failed" &&
-					previous != "timeout" {
-					outcome[effect] = probeOutcome{status, probeNote(name, status)}
-				}
-			}
+			tally.record(name, lines, status)
 		}(name, contents)
 	}
 	wg.Wait()
+	results, outcome := tally.results, tally.outcome
 	if err = ctx.Err(); err != nil {
 		return err
 	}
@@ -122,29 +154,78 @@ func (p *preparation) probeEffects(ctx context.Context, c operation.Context) err
 			// would read as a change, or as nothing to change.
 			continue
 		}
-		texts, noChange, why := make([]string, 0, len(found)), len(found) > 0, []string{}
-		for _, result := range found {
-			if result.unanswered {
-				why = append(why, result.text)
-				continue
-			}
-			texts = append(texts, result.text)
-			noChange = noChange && result.noChange
+		fillProbed(effect, found)
+	}
+	return nil
+}
+
+// fillProbed sets an effect from what every script that carries it answered:
+// what the probes checked is its Delta and what they could not check its
+// ProbeNote, side by side, so a step whose scripts printed both shows both.
+func fillProbed(effect *operation.Effect, found []probeResult) {
+	texts, noChange, why := make([]string, 0, len(found)), len(found) > 0, []string{}
+	for _, result := range found {
+		if result.answered != "" {
+			texts = append(texts, result.answered)
 		}
-		if len(why) > 0 {
-			slices.Sort(why)
-			effect.Probe = "unanswered"
-			effect.ProbeNote = strings.Join(
-				slices.Compact(why),
-				"; ",
-			) + "; checked again when applied"
-			continue
+		if result.unanswered != "" {
+			why = append(why, result.unanswered)
 		}
-		slices.Sort(texts)
-		effect.Delta = strings.Join(texts, "; ")
-		effect.NoChange = noChange
+		noChange = noChange && result.noChange
 	}
-	return nil
+	slices.Sort(texts)
+	effect.Delta = strings.Join(texts, "; ")
+	if len(why) > 0 {
+		slices.Sort(why)
+		effect.Probe = "unanswered"
+		effect.ProbeNote = strings.Join(slices.Compact(why), "; ") + "; checked again when applied"
+		return
+	}
+	effect.NoChange = noChange
+}
+
+// probeMigrations runs the migrations script's probe alone under its own
+// planning steps: "checking migrations", then what it found.
+func (p *preparation) probeMigrations(
+	ctx context.Context,
+	c operation.Context,
+	directory string,
+	environment []string,
+	scripts map[string]string,
+	tally *probeTally,
+) {
+	contents, ok := scripts[migrationsScript]
+	if !ok || !p.active[migrationsScript] {
+		return
+	}
+	c.Step("checking migrations")
+	lines, status := p.runProbe(ctx, c, directory, environment, migrationsScript, contents)
+	tally.record(migrationsScript, lines, status)
+	result, found := lines["migrations"]
+	c.Step(migrationsOutcome(result, found, status))
+}
+
+// migrationsOutcome is the planning step that says what the migrations probe
+// found: how many migrations the apply would run and how many need the owner
+// (a conflict, or one the installed workbench doesn't know), none to run, or
+// that the probe itself failed or timed out.
+func migrationsOutcome(result probeResult, found bool, status string) string {
+	if status != "ok" || !found {
+		return "migrations: couldn't check"
+	}
+	var parts []string
+	if result.changes > 0 {
+		parts = append(parts, fmt.Sprintf("%d to run", result.changes))
+	}
+	if result.checks == 1 {
+		parts = append(parts, "1 needs attention")
+	} else if result.checks > 1 {
+		parts = append(parts, fmt.Sprintf("%d need attention", result.checks))
+	}
+	if len(parts) == 0 {
+		return "migrations: none to run"
+	}
+	return "migrations: " + strings.Join(parts, ", ")
 }
 
 // probeOutcome is how one effect's probe ended: the status for --json and the
@@ -235,24 +316,35 @@ func (p *preparation) runProbe(
 		}
 		return nil, "failed"
 	}
+	lines, ok := parseProbe(output.Stdout)
+	if !ok {
+		return nil, "failed"
+	}
+	return lines, "ok"
+}
+
+// parseProbe reads one script's probe output into a result per effect. It is
+// false for anything but at least one effect line and nothing else.
+func parseProbe(stdout string) (map[string]probeResult, bool) {
 	lines := map[string]probeResult{}
-	for line := range strings.SplitSeq(strings.TrimSpace(output.Stdout), "\n") {
+	for line := range strings.SplitSeq(strings.TrimSpace(stdout), "\n") {
 		match := probeLine.FindStringSubmatch(line)
 		if match == nil {
-			return nil, "failed"
+			return nil, false
 		}
-		next := probeResult{
-			text:       match[3],
-			noChange:   match[2] == "= ",
-			unanswered: match[2] == "? ",
+		var next probeResult
+		switch match[2] {
+		case "":
+			next.answered, next.changes = match[3], 1
+		case "= ":
+			next.answered, next.noChange = match[3], true
+		case "? ":
+			next.unanswered, next.checks = match[3], 1
 		}
 		if previous, ok := lines[match[1]]; ok {
 			next = previous.merge(next)
 		}
 		lines[match[1]] = next
 	}
-	if len(lines) == 0 {
-		return nil, "failed"
-	}
-	return lines, "ok"
+	return lines, len(lines) > 0
 }
```

A step's detail now shows both: `On this PC` with what was checked, and `Not checked` with the note. After an apply, a step whose plan carried a note reports its summary, not the partial text, which would read as the whole of what ran:

```diff
diff --git a/internal/cli/plandetail.go b/internal/cli/plandetail.go
index 8c39490..0691b1c 100644
--- a/internal/cli/plandetail.go
+++ b/internal/cli/plandetail.go
@@ -52,12 +52,15 @@ func effectDetail(plan operation.Plan, effect operation.Effect, width int) []str
 		lines = append(lines, wrapPlain(s, width)...)
 	}
 	paragraph(effect.What)
-	here := effect.Delta
+	// What the check found here, and beside it what it could not check:
+	// a step can have both when its script answered only part.
+	here, unchecked := effect.Delta, effect.ProbeNote
 	if here == "" {
-		here = effect.ProbeNote
+		here, unchecked = unchecked, ""
 	}
 	pairs := [][2]string{
 		{"On this PC", here},
+		{"Not checked", unchecked},
 		{"Changes", effect.Touches},
 		{"Runs as", effect.RunsAs},
 		{"To undo", effect.Undo},
```

```diff
diff --git a/internal/machine/apply.go b/internal/machine/apply.go
index 4039db8..2886ab6 100644
--- a/internal/machine/apply.go
+++ b/internal/machine/apply.go
@@ -418,6 +418,16 @@ func (a *applyRun) reportEffects(plan operation.Plan) error {
 	)
 }
 
+// effectDone is what a step that ran did, as its plan said: the probe's text,
+// unless part of it could not be checked when planning, when that text would
+// read as the whole of what ran.
+func effectDone(effect operation.Effect) string {
+	if effect.ProbeNote != "" {
+		return ""
+	}
+	return effect.Delta
+}
+
 func effectResults(effects []operation.Effect, blocked map[string]string) []operation.Component {
 	results := make([]operation.Component, 0, len(effects))
 	for _, effect := range effects {
@@ -425,7 +435,7 @@ func effectResults(effects []operation.Effect, blocked map[string]string) []oper
 			Name:     effect.Name,
 			Title:    cmp.Or(effect.Title, effect.Name),
 			Status:   operation.StatusComplete,
-			Message:  cmp.Or(effect.Delta, effect.Summary, effect.Description),
+			Message:  cmp.Or(effectDone(effect), effect.Summary, effect.Description),
 			Recovery: effect.Recovery,
 		}
 		if reason, ok := blocked[effect.Name]; ok {
```

- [ ] **Step 4: The scripts**

Create `home/.chezmoitemplates/old-locations.sh`:

```bash
# old_locations COMMAND... runs COMMAND with the owner's old-location
# variables, which the apply passes to its scripts as WORKBENCH_OLD_<NAME> so
# no installer sees them (internal/migrate OldLocationVariables; keep the two
# lists the same). Only the installed workbench needs them, to find the files
# earlier releases left.
old_locations() {
    local name passed
    local assignments=()
    for name in XDG_DATA_HOME XDG_STATE_HOME XDG_CONFIG_HOME XDG_CACHE_HOME \
        CLAUDE_COSTS_LEDGER CLAUDE_COSTS_STATE CLAUDE_COSTS_RATES CLAUDE_COSTS_CACHE; do
        passed=WORKBENCH_OLD_$name
        [ -z "${!passed:-}" ] || assignments+=("$name=${!passed}")
    done
    # bash 3.2 (macOS) under set -u rejects "${a[@]}" for an empty array.
    env ${assignments[@]+"${assignments[@]}"} "$@"
}
```

Create `home/.chezmoiscripts/shared/run_after_05-migrations.sh.tmpl`. An older installed `workbench` answers `migrate --help` with its general help and exit 0, so `knows_migrations` looks for migrate's own usage line. No probe line holds `; `. A run that didn't finish is reported through `part_blocked` with a reason that doesn't promise a retry: a conflict waits for the owner.

```bash
#!/bin/bash
# Move files earlier Workbench releases wrote outside Workbench's own folders
# into them, and delete the old caches, through the installed workbench: the
# binary the Claude Code and Codex hooks run, so the one that moves the costs
# files is the one that writes them next. Checking is a few Lstat calls, and a
# run with nothing to do takes no lock. chezmoi runs scripts in the order of
# their full source paths (darwin/, linux/, shared/, then wsl/), so this always
# runs after the files and before shared/run_after_90-costs-ingest.
set -euo pipefail
workbench="$HOME/.local/bin/workbench"
{{ template "old-locations.sh" }}
# knows_migrations: the installed workbench has the migrate command. An older
# one answers `migrate --help` with its general help, which has no migrate
# usage line, and exits 0.
knows_migrations() {
    local usage
    usage=$("$workbench" migrate --help 2>/dev/null || true)
    [[ $usage == *"workbench migrate ["* ]]
}
if [ "${WORKBENCH_PROBE:-}" = 1 ]; then
    if [ ! -x "$workbench" ]; then
        echo "migrations: = Workbench is not installed here yet, so there is nothing to move"
    elif ! knows_migrations; then
        echo "migrations: ? the installed workbench predates migrations, so run workbench update"
    elif ! old_locations "$workbench" migrate --probe 2>/dev/null; then
        echo "migrations: ? workbench migrate --probe failed, so the migrations are checked again when applied"
    fi
    exit 0
fi
if [[ "${WORKBENCH_EFFECT_MIGRATIONS:-}" != 1 ]]; then
    echo 'WORKBENCH_EFFECT migrations disabled (not selected)' >&2
    exit 0
fi
{{ template "effect-report.sh" }}
if [ ! -x "$workbench" ]; then
    echo "==> migrations: no installed workbench at $workbench, so nothing was moved"
    exit 0
fi
if ! knows_migrations; then
    part_blocked migrations "the installed workbench predates migrations, so run workbench update and apply again"
    exit 0
fi
old_locations "$workbench" migrate run ||
    part_blocked migrations "a migration couldn't finish (the output above says why and what to do)"
```

Run the costs-ingest worker with the old locations, so it can find the files it moves:

```diff
diff --git a/home/.chezmoiscripts/shared/run_after_90-costs-ingest.sh.tmpl b/home/.chezmoiscripts/shared/run_after_90-costs-ingest.sh.tmpl
index 46345ff..6be0118 100644
--- a/home/.chezmoiscripts/shared/run_after_90-costs-ingest.sh.tmpl
+++ b/home/.chezmoiscripts/shared/run_after_90-costs-ingest.sh.tmpl
@@ -5,6 +5,7 @@
 # the format that binary reads; a first read of a large history takes a while.
 set -euo pipefail
 workbench="$HOME/.local/bin/workbench"
+{{ template "old-locations.sh" }}
 if [ "${WORKBENCH_PROBE:-}" = 1 ]; then
     if [ -x "$workbench" ]; then
         echo "costs-ingest: $("$workbench" --version) reads new usage into the ledger"
@@ -23,5 +24,5 @@ if [ ! -x "$workbench" ]; then
 fi
 echo "==> costs: reading new usage into the ledger"
 {{ template "effect-report.sh" }}
-"$workbench" costs ingest --worker ||
+old_locations "$workbench" costs ingest --worker ||
     part_failed costs-ingest "reading new usage into the costs ledger failed (the output above says why); the next apply reads it" || exit 1
```

- [ ] **Step 5: The completion cache in the startup files**

```diff
diff --git a/home/dot_zshrc.tmpl b/home/dot_zshrc.tmpl
index 35d5f14..bda958f 100644
--- a/home/dot_zshrc.tmpl
+++ b/home/dot_zshrc.tmpl
@@ -69,19 +69,38 @@ if command -v uv &> /dev/null; then
 fi
 
 # Workbench: Tab completion for its commands and flags. The script is saved
-# once per installed workbench and sourced after compinit, so a new shell
-# runs workbench only after an update or the first time.
+# once per installed workbench in Workbench's cache folder, picked the way
+# Workbench picks it, and sourced after compinit, so a new shell runs
+# workbench only after an update or the first time. A relative
+# WORKBENCH_CACHE_DIR, which Workbench refuses, saves nothing: completion then
+# loads straight from workbench.
 if (( ${+commands[workbench]} )); then
     _workbench_bin=${commands[workbench]:A}
-    _workbench_completion=${XDG_CACHE_HOME:-$HOME/.cache}/workbench-completion.zsh
-    _workbench_saved=
-    [[ -r $_workbench_completion ]] && read -r _workbench_saved < "$_workbench_completion"
-    if [[ $_workbench_saved != "# $_workbench_bin" ]]; then
-        mkdir -p "${_workbench_completion:h}" &&
-            { print -r -- "# $_workbench_bin"; workbench completion zsh; } >| "$_workbench_completion"
+    if [[ $WORKBENCH_CACHE_DIR == /* ]]; then
+        _workbench_cache=$WORKBENCH_CACHE_DIR
+    elif [[ -n $WORKBENCH_CACHE_DIR ]]; then
+        _workbench_cache=
+    elif [[ $XDG_CACHE_HOME == /* ]]; then
+        _workbench_cache=$XDG_CACHE_HOME/workbench
+    else
+        _workbench_cache=$HOME/Library/Caches/workbench
     fi
-    source "$_workbench_completion"
-    unset _workbench_bin _workbench_completion _workbench_saved
+    if [[ -n $_workbench_cache ]]; then
+        _workbench_completion=$_workbench_cache/completion.zsh
+        _workbench_saved=
+        [[ -r $_workbench_completion ]] && read -r _workbench_saved < "$_workbench_completion"
+        if [[ $_workbench_saved != "# $_workbench_bin" ]]; then
+            # The folder is made private as Workbench makes it; an existing
+            # one keeps its mode. The file is written 0600 in a subshell, so
+            # this shell's umask stays as it is.
+            mkdir -p "${_workbench_cache:h}" && mkdir -m 700 -p "$_workbench_cache" &&
+                ( umask 077; { print -r -- "# $_workbench_bin"; workbench completion zsh; } >| "$_workbench_completion" )
+        fi
+        [[ -r $_workbench_completion ]] && source "$_workbench_completion"
+    else
+        source <(workbench completion zsh)
+    fi
+    unset _workbench_bin _workbench_cache _workbench_completion _workbench_saved
 fi
 
 # --- Aliases ---
```

```diff
diff --git a/home/private_dot_bashrc.tmpl b/home/private_dot_bashrc.tmpl
index f1b04f5..fbae324 100644
--- a/home/private_dot_bashrc.tmpl
+++ b/home/private_dot_bashrc.tmpl
@@ -79,20 +79,39 @@ export BUN_INSTALL="$HOME/.bun"
 export PATH="$BUN_INSTALL/bin:$PATH"
 
 # Workbench: Tab completion for its commands and flags. The script is saved
-# once per installed workbench, so a new shell runs workbench only after an
-# update or the first time. It comes after the PATH lines that find workbench
-# and needs the bash-completion package loaded above (Ubuntu ships it).
+# once per installed workbench in Workbench's cache folder, picked the way
+# Workbench picks it, so a new shell runs workbench only after an update or the
+# first time. It comes after the PATH lines that find workbench and needs the
+# bash-completion package loaded above (Ubuntu ships it). A relative
+# WORKBENCH_CACHE_DIR, which Workbench refuses, saves nothing: completion then
+# loads straight from workbench.
 if command -v workbench &> /dev/null && declare -F _get_comp_words_by_ref &> /dev/null; then
     _workbench_bin=$(readlink -f "$(command -v workbench)")
-    _workbench_completion=${XDG_CACHE_HOME:-$HOME/.cache}/workbench-completion.bash
-    _workbench_saved=
-    [ -r "$_workbench_completion" ] && read -r _workbench_saved < "$_workbench_completion"
-    if [ "$_workbench_saved" != "# $_workbench_bin" ]; then
-        mkdir -p "$(dirname "$_workbench_completion")" &&
-            { printf '# %s\n' "$_workbench_bin"; workbench completion bash; } > "$_workbench_completion"
+    if [[ $WORKBENCH_CACHE_DIR == /* ]]; then
+        _workbench_cache=$WORKBENCH_CACHE_DIR
+    elif [[ -n $WORKBENCH_CACHE_DIR ]]; then
+        _workbench_cache=
+    elif [[ $XDG_CACHE_HOME == /* ]]; then
+        _workbench_cache=$XDG_CACHE_HOME/workbench
+    else
+        _workbench_cache=$HOME/.cache/workbench
     fi
-    . "$_workbench_completion"
-    unset _workbench_bin _workbench_completion _workbench_saved
+    if [ -n "$_workbench_cache" ]; then
+        _workbench_completion=$_workbench_cache/completion.bash
+        _workbench_saved=
+        [ -r "$_workbench_completion" ] && read -r _workbench_saved < "$_workbench_completion"
+        if [ "$_workbench_saved" != "# $_workbench_bin" ]; then
+            # The folder is made private as Workbench makes it; an existing
+            # one keeps its mode. The file is written 0600 in a subshell, so
+            # this shell's umask stays as it is.
+            mkdir -p "$(dirname "$_workbench_cache")" && mkdir -m 700 -p "$_workbench_cache" &&
+                ( umask 077; { printf '# %s\n' "$_workbench_bin"; workbench completion bash; } > "$_workbench_completion" )
+        fi
+        [ -r "$_workbench_completion" ] && . "$_workbench_completion"
+    else
+        . <(workbench completion bash)
+    fi
+    unset _workbench_bin _workbench_cache _workbench_completion _workbench_saved
 fi
 
 # --- Oh My Posh prompt ---
```

- [ ] **Step 6: Run the gates and the render checks**

Run the gates, then:

```zsh
scripts/render-check.sh personal pinned; echo EXIT:$?
scripts/render-check.sh work pinned; echo EXIT:$?
scripts/render-check.sh personal pinned wsl; echo EXIT:$?
```

Expected: `OK [personal/pinned]`, `OK [work/pinned]`, `OK [personal/pinned/wsl-lint]`, `EXIT:0` each. The render check runs the new script's probe with no installed workbench, so it must print exactly `migrations: = Workbench is not installed here yet, so there is nothing to move`.

- [ ] **Step 7: Check the startup files in a disposable home**

```zsh
S=$(cd "$(mktemp -d)" && pwd -P); H=$S/home; mkdir -p $H/.local/bin
go build -o $H/.local/bin/workbench ./cmd/workbench; echo EXIT:$?
cp "$(scripts/scratch-init.sh personal pinned)" $S/answers.toml; chmod 600 $S/answers.toml
chezmoi --config $S/answers.toml --source "$PWD" execute-template < home/dot_zshrc.tmpl > $H/.zshrc; zsh -n $H/.zshrc; echo EXIT:$?
env -i HOME=$H PATH=$H/.local/bin:/usr/bin:/bin TERM=dumb zsh -i -c 'whence -w _workbench' 2>/dev/null; echo EXIT:$?
stat -f '%Sp %N' "$H/Library/Caches/workbench" "$H/Library/Caches/workbench/completion.zsh"
rm -rf "$H/Library/Caches/workbench"; mkdir -m 755 -p "$H/Library/Caches/workbench"
env -i HOME=$H PATH=$H/.local/bin:/usr/bin:/bin TERM=dumb zsh -i -c 'whence -w _workbench' 2>/dev/null; echo EXIT:$?
stat -f '%Sp %N' "$H/Library/Caches/workbench" "$H/Library/Caches/workbench/completion.zsh"
env -i HOME=$H PATH=/usr/bin:/bin $H/.local/bin/workbench version; echo EXIT:$?
rm -rf "$H/Library/Caches/workbench"
env -i HOME=$H PATH=$H/.local/bin:/usr/bin:/bin TERM=dumb WORKBENCH_CACHE_DIR=rel zsh -i -c 'whence -w _workbench' 2>/dev/null; echo EXIT:$?
command ls "$H/Library/Caches"
chezmoi --config $S/answers.toml --source "$PWD" execute-template < home/private_dot_bashrc.tmpl > $S/bashrc; bash -n $S/bashrc; echo EXIT:$?
```

Expected:
- `_workbench: function`, then `drwx------` for the folder and `-rw-------` for `completion.zsh`.
- With a `0755` folder made first: the folder stays `drwxr-xr-x`, the file is `-rw-------`, and `workbench version` fails with `Existing …/Library/Caches/workbench must be a private directory (mode 0700); it was not changed`, `EXIT:2`. Workbench reports it; the shell doesn't change it.
- `WORKBENCH_CACHE_DIR=rel` still gives `_workbench: function`, and `~/Library/Caches` stays empty.
- `bash -n` exits 0.

- [ ] **Step 8: Commit**

```zsh
git add internal/machine home
git commit -F - <<'EOF'
feat(apply): a migrations step, shown while planning and applying

The plan probes the migrations script first under "checking migrations",
then says how many will run and how many need attention; the script prints
==> migrations: lines and runs the installed workbench with the apply's
runtime folders and the owner's old-location variables, telling an older
binary by its help. The completion cache moves to Workbench's cache folder,
private, and costs-ingest's Undo no longer claims a rebuild.

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
EOF
echo EXIT:$?
```

---

### Task 8: End-to-end check on macOS in a disposable home

No code changes, unless a check fails: then fix the cause in the task that owns it, and rerun from step 1.

**Files:** none (scratch only, under `$S`).

- [ ] **Step 1: Build, and write the seeding helpers**

```zsh
S=$(cd "$(mktemp -d)" && pwd -P); echo $S
go build -o $S/workbench ./cmd/workbench; echo EXIT:$?
cat > $S/seed.zsh <<'SEED'
# seed HOME: the "before" layout of spec section 3.1, with a version 1 ledger
# (the claude-costs script's schema) holding two rows.
seed() {
    local h=$1
    mkdir -p $h/.local/bin $h/.local/share/claude-costs $h/.local/state/claude-costs \
        $h/.config/claude-costs $h/.cache/claude-costs
    chmod 755 $h/.local/share/claude-costs $h/.local/state/claude-costs
    cp $S/workbench $h/.local/bin/workbench
    python3 -I - $h/.local/share/claude-costs/ledger.sqlite <<'PY'
import sqlite3, sys
db = sqlite3.connect(sys.argv[1])
db.executescript("""
CREATE TABLE responses (request_id TEXT PRIMARY KEY, ts TEXT NOT NULL, model TEXT NOT NULL,
  project TEXT NOT NULL, session_id TEXT NOT NULL, account TEXT NOT NULL, account_source TEXT NOT NULL,
  input INTEGER NOT NULL, output INTEGER NOT NULL, cache_write_5m INTEGER NOT NULL,
  cache_write_1h INTEGER NOT NULL, cache_read INTEGER NOT NULL);
CREATE INDEX responses_ts ON responses (ts);
CREATE INDEX responses_project_model ON responses (project, model);
CREATE INDEX responses_account ON responses (account);
CREATE TABLE files (path TEXT PRIMARY KEY, offset INTEGER NOT NULL, size INTEGER NOT NULL,
  mtime_ns INTEGER NOT NULL, last_seen TEXT NOT NULL);
CREATE TABLE meta (key TEXT PRIMARY KEY, value TEXT NOT NULL);
INSERT INTO meta VALUES ('schema_version', '1');
INSERT INTO responses VALUES ('req-1','2026-09-01T10:00:00.000Z','claude-opus-5','/w/a','s1','a@example.test','sweep',100,200,300,0,1000);
INSERT INTO responses VALUES ('req-2','2026-09-02T10:00:00.000Z','claude-opus-5','/w/b','s2','a@example.test','sweep',1,2,3,4,5);
""")
db.commit(); db.close()
PY
    chmod 600 $h/.local/share/claude-costs/ledger.sqlite
    print '{}' > $h/.local/share/claude-costs/rates-official.json
    print '{}' > $h/.local/share/claude-costs/rates-official-codex.json
    print 'mine' > $h/.local/share/claude-costs/notes.txt
    print 'log' > $h/.local/state/claude-costs/ingest.log
    : > $h/.local/state/claude-costs/ingest.lock
    print '{"claude-opus-5": {"input": 1}}' > $h/.config/claude-costs/rates.json
    print '# /opt/homebrew/bin/workbench\n#compdef workbench' > $h/.cache/workbench-completion.zsh
    : > $h/.cache/claude-costs/0123456789abcdef0123456789abcdef.json.gz
}
run() { env -i HOME=$1 PATH=/usr/bin:/bin:/usr/sbin:/sbin TMPDIR=$S LANG=C.UTF-8 \
    CLAUDE_COSTS_PRICING_URL=http://127.0.0.1:9/none CODEX_COSTS_PRICING_URL=http://127.0.0.1:9/none \
    "${@:2}"; }
SEED
source $S/seed.zsh; echo EXIT:$?
```

- [ ] **Step 2: The planning steps and the plan row, before and after**

The plan needs Workbench's tools and machine answers in the disposable home. `update --local-build` downloads chezmoi, uv and Python into it, and nothing else. `scripts/scratch-init.sh` writes its answers under the system temp folder, which `init --answers-from` refuses (`/var` is a link on macOS), so copy the file into `$S` and make it `0600` first.

```zsh
H=$S/plan; seed $H
run $H $H/.local/bin/workbench update --local-build > $S/update.out 2>&1; echo EXIT:$?; tail -2 $S/update.out
cp "$(scripts/scratch-init.sh personal pinned)" $S/answers.toml; chmod 600 $S/answers.toml
D=$(run $H $H/.local/bin/workbench init --answers-from $S/answers.toml --dry-run --json | python3 -I -c 'import json,sys; print(json.load(sys.stdin)["plan_digest"])')
run $H $H/.local/bin/workbench init --answers-from $S/answers.toml --approve-plan $D; echo EXIT:$?
run $H $H/.local/bin/workbench apply --local-build --dry-run --non-interactive > $S/plan1.out 2>&1; echo EXIT:$?
rg -n 'checking migrations|migrations: |checking what each|Move Workbench' $S/plan1.out; echo EXIT:${pipestatus[1]}
```

Expected: `[WorkBench] Installed this checkout's tools`, `answers: complete`, then, among the planning lines (plain lines on stderr without a terminal):

```
    checking migrations
    migrations: 3 to run
    checking what each step would do here
```

and the plan lists `[x] Move Workbench's files`. The step's detail (`--dry-run --json`, effect `migrations`, `delta`) names `0001-costs-folders: moves ~/.local/share/claude-costs/ledger.sqlite, …`, `0002-completion-cache: deletes ~/.cache/workbench-completion.zsh` and `0003-old-costs-cache: deletes 1 cached file(s) from ~/.cache/claude-costs`.

Now the apply phase, through the rendered script with the step ticked, then the plan again:

```zsh
chezmoi --config $S/answers.toml --source "$PWD" execute-template < home/.chezmoiscripts/shared/run_after_05-migrations.sh.tmpl > $S/05.sh; echo EXIT:$?
run $H WORKBENCH_EFFECT_MIGRATIONS=1 /bin/bash $S/05.sh; echo EXIT:$?
run $H WORKBENCH_EFFECT_MIGRATIONS=1 /bin/bash $S/05.sh; echo EXIT:$?
run $H $H/.local/bin/workbench apply --local-build --dry-run --non-interactive > $S/plan2.out 2>&1; echo EXIT:$?
rg -n 'checking migrations|migrations: |Already set' $S/plan2.out; echo EXIT:${pipestatus[1]}
```

Expected:
- First run:

  ```
  ==> migrations: checking
  ==> migrations: 0001-costs-folders: moving the costs files into Workbench's folders
  ==> migrations: 0001-costs-folders: kept, because Workbench didn't write them: ~/.local/share/claude-costs/notes.txt
  ==> migrations: 0001-costs-folders: done
  ==> migrations: 0002-completion-cache: deleting the old shell completion cache
  ==> migrations: 0002-completion-cache: done
  ==> migrations: 0003-old-costs-cache: deleting the old claude-costs script's cache
  ==> migrations: 0003-old-costs-cache: done
  ```

- Second run: `==> migrations: checking`, `==> migrations: nothing to migrate`; `EXIT:0`.
- The second plan shows `    migrations: none to run`, and `Move Workbench's files` sits on the `Already set` line.

- [ ] **Step 3: The after layout**

```zsh
A="$H/Library/Application Support/workbench"
stat -f '%Sp %N' "$A/data/costs" "$A/data/costs/ledger.sqlite" "$A/data/costs/claude/rates-official.json" "$A/data/costs/codex/rates-official.json" "$A/config/costs/rates.json" "$A/state/migrations.json"; echo EXIT:$?
python3 -I -c 'import sqlite3,sys; print(sqlite3.connect(sys.argv[1]).execute("SELECT COUNT(*) FROM responses").fetchone())' "$A/data/costs/ledger.sqlite"
command ls -A $H/.local/share/claude-costs
command ls $H/.local/state/claude-costs $H/.config/claude-costs $H/.cache/claude-costs $H/.cache/workbench-completion.zsh 2>&1
run $H $H/.local/bin/workbench migrate; echo EXIT:$?
```

Expected: the folder `drwx------` and the files `-rw-------`; `(2,)`. `~/.local/share/claude-costs` holds only `notes.txt`, and the other four paths are gone (`No such file or directory`). `migrate` shows each migration as `done <date> at <time>` in this machine's time.

- [ ] **Step 4: A relative `XDG_DATA_HOME`** (Review Focus 3)

```zsh
H=$S/relative; seed $H; mkdir -p $S/cwd; cd $S/cwd
run $H XDG_DATA_HOME=rel $H/.local/bin/workbench migrate; echo EXIT:$?
print '{"hook_event_name":"SessionStart","transcript_path":"","session_id":""}' | run $H XDG_DATA_HOME=rel $H/.local/bin/workbench costs ingest; sleep 3
A="$H/Library/Application Support/workbench"; command ls "$A/data/costs" 2>&1; rg -n "not read" "$A/state/costs/ingest.log"
run $H XDG_DATA_HOME=rel $H/.local/bin/workbench costs status; echo EXIT:$?
command ls -A $S/cwd | wc -l; command ls $H/.local/share/claude-costs
cd -
```

Expected:
- `migrate` lists `0001` as pending, moving the overrides and deleting the log and lock "and can't find the old ledger because XDG_DATA_HOME is relative (rel) (make it absolute, or unset it)", with a warning for the variable.
- After the hook, `data/costs` doesn't exist (no ledger was created), and the log has `not read: The costs files can't all move: it can't find the old ledger because XDG_DATA_HOME is relative (rel) …`.
- `costs status` fails with the same reason, `EXIT:4`.
- `$S/cwd` is empty (`0`), and the old ledger is still in `~/.local/share/claude-costs`.

- [ ] **Step 5: A hook firing while a migration holds the lock** (Review Focus 2), then a hook moving the files by itself

```zsh
H=$S/hook; seed $H
A="$H/Library/Application Support/workbench"; L="$A/state/locks/migrate.lock"
mkdir -p "${L:h}"; chmod 700 "$A" "$A/state" "${L:h}"
python3 -I -c 'import fcntl,sys,time; f=open(sys.argv[1],"w"); fcntl.flock(f,fcntl.LOCK_EX); time.sleep(6)' "$L" &
sleep 1
print '{"hook_event_name":"SessionStart","transcript_path":"","session_id":""}' | run $H $H/.local/bin/workbench costs ingest; echo EXIT:$?
sleep 2; command ls "$A/data/costs" 2>&1; rg -n 'not read' "$A/state/costs/ingest.log"
wait
print '{"hook_event_name":"SessionStart","transcript_path":"","session_id":""}' | run $H $H/.local/bin/workbench costs ingest; echo EXIT:$?
sleep 3; command ls "$A/data/costs" $H/.local/share/claude-costs
python3 -I -c 'import sqlite3,sys; print(sqlite3.connect(sys.argv[1]).execute("SELECT COUNT(*) FROM responses").fetchone())' "$A/data/costs/ledger.sqlite"
```

Expected: while the lock is held, `data/costs` doesn't exist and the log has `not read: another migration is running, so nothing was moved and the next import or apply tries again`. After the lock is released, the second hook's worker moves the files: `data/costs` holds `claude`, `codex` and `ledger.sqlite`, the old folder holds only `notes.txt`, and the ledger has `(2,)` rows.

- [ ] **Step 6: An older installed binary, and a runtime folder that isn't private** (Review Focus 1 and 5)

Build a binary from before this plan (it has no `migrate`) and install it in a seeded home:

```zsh
git worktree add --detach $S/old-src main; (cd $S/old-src && go build -o $S/old-workbench ./cmd/workbench); git worktree remove $S/old-src
H=$S/older; seed $H; cp $S/old-workbench $H/.local/bin/workbench
run $H WORKBENCH_PROBE=1 /bin/bash $S/05.sh; echo EXIT:$?
run $H WORKBENCH_EFFECT_MIGRATIONS=1 WORKBENCH_EFFECT_REPORT=$S/report /bin/bash $S/05.sh; echo EXIT:$?
cat $S/report
H=$S/broken; seed $H; mkdir -p "$H/Library/Application Support/workbench/state"; chmod 755 "$H/Library/Application Support/workbench"
run $H WORKBENCH_PROBE=1 /bin/bash $S/05.sh; echo EXIT:$?
print '{"hook_event_name":"SessionStart","transcript_path":"","session_id":""}' | run $H $H/.local/bin/workbench costs ingest; echo EXIT:$?
run $H $H/.local/bin/workbench costs status; echo EXIT:$?
```

Expected:
- The older binary: the probe prints exactly `migrations: ? the installed workbench predates migrations, so run workbench update`, `EXIT:0`. The run prints `WORKBENCH_EFFECT migrations blocked: the installed workbench predates migrations, so run workbench update and apply again` on stderr, ends `EXIT:0`, and the report file holds that line.
- The `0755` runtime folder: the probe prints `migrations: ? workbench migrate --probe failed, so the migrations are checked again when applied`. The hook prints `workbench costs: nothing recorded: Existing …/Library/Application Support/workbench/state must be a private directory (mode 0700); it was not changed` on stderr and exits 0, and `costs status` fails with the same reason, `EXIT:2`.

- [ ] **Step 7: Conflicts, a finished copy, and a foreign file** (Review Focus 4)

First the plan, in the home with Workbench's tools: a conflict beside a pending migration must show both, in the planning step and in the step's fields.

```zsh
H=$S/plan
mkdir -p $H/.config/claude-costs; print '{"other": {}}' > $H/.config/claude-costs/rates.json
print '# /opt/homebrew/bin/workbench' > $H/.cache/workbench-completion.zsh
run $H $H/.local/bin/workbench apply --local-build --dry-run --non-interactive 2>&1 | rg 'migrations: '
run $H $H/.local/bin/workbench apply --local-build --dry-run --json 2>/dev/null | python3 -I -c 'import json,sys; [print("delta:", e.get("delta"), "\nnote:", e.get("probe_note")) for e in json.load(sys.stdin)["results"][0]["details"]["effects"] if e["name"]=="migrations"]'
rm -r $H/.config/claude-costs $H/.cache/workbench-completion.zsh
```

Expected: `    migrations: 1 to run, 1 needs attention`; `delta: 0002-completion-cache: deletes ~/.cache/workbench-completion.zsh` and `note: 0001-costs-folders: leaves ~/.config/claude-costs/rates.json and ~/Library/Application Support/workbench/config/costs/rates.json, which hold different data (remove one); checked again when applied`. The checked part stays the step's detail and the conflict is its note, never one hiding the other.

Then the runs:

```zsh
H=$S/conflict; seed $H
run $H WORKBENCH_EFFECT_MIGRATIONS=1 /bin/bash $S/05.sh >/dev/null; echo EXIT:$?
mkdir -p $H/.config/claude-costs; print '{"old": {}}' > $H/.config/claude-costs/rates.json
print 'my own completion' > $H/.cache/workbench-completion.bash
run $H $H/.local/bin/workbench migrate
D=$(run $H $H/.local/bin/workbench migrate --apply --dry-run --json | python3 -I -c 'import json,sys; print(json.load(sys.stdin)["plan_digest"])')
run $H $H/.local/bin/workbench migrate --apply --approve-plan $D; echo EXIT:$?
mkdir -p $H/.local/state/claude-costs; print 'log' > $H/.local/state/claude-costs/ingest.log
run $H $H/.local/bin/workbench migrate run; echo EXIT:$?
print '{"claude-opus-5": {"input": 1}}' > $H/.config/claude-costs/rates.json
D=$(run $H $H/.local/bin/workbench migrate --apply --dry-run --json | python3 -I -c 'import json,sys; print(json.load(sys.stdin)["plan_digest"])')
run $H $H/.local/bin/workbench migrate --apply --approve-plan $D; echo EXIT:$?
command ls -A $H/.config
run $H $H/.local/bin/workbench migrate
```

Expected:
- `migrate` shows `0001` as `conflict — leaves ~/.config/claude-costs/rates.json and ~/Library/Application Support/workbench/config/costs/rates.json, which hold different data (remove one)`, and `0002` as `done …, keeps ~/.cache/workbench-completion.bash (Workbench didn't write it)`.
- `--apply` with only that conflict prints `migrations: conflict — Nothing can move until you remove one copy of each file named above`, the conflict as a warning and an error, `EXIT:4`; never "Nothing to migrate".
- `migrate run` with an old import log to delete beside the conflict deletes the log, prints `0001-costs-folders: kept, because Workbench didn't write them: ~/.local/share/claude-costs/notes.txt` (not `rates.json`: the conflict is reported as a conflict only), the conflict, and `Error: 0001-costs-folders can't finish until you remove one copy of each file named above`, `EXIT:4`.
- With the same bytes in the old overrides file as the new one, `--apply` runs `0001`, which deletes the old file as a finished copy: `0001-costs-folders: done`, `EXIT:0`, and `~/.config` is empty.
- The last `migrate` shows `0001` as `done <date> at <time>, keeps ~/.local/share/claude-costs/notes.txt (Workbench didn't write them)`.

- [ ] **Step 8: Record the evidence**

Paste each step's command output into the task's review notes (not into tracked docs). No commit, unless a fix was needed: then commit the fix in the owning task's style, and rerun this task.

---

### Task 9: End-to-end check on Linux, across filesystems

This machine has Docker (29.8.2, checked with `docker version`). The container stands in for Linux; WSL itself is not qualified by this. Workbench refuses to run as root, so the script makes a user. The data folder sits on a `tmpfs` mounted `exec` (Workbench runs its tools from there), a different filesystem from the home, so the ledger move takes the real `EXDEV` copy path (Review Focus 5). The checkout is mounted read-only for `--local-build`.

**Files:** none (scratch only).

- [ ] **Step 1: Build for Linux, seed a ledger, write the script**

```zsh
S=$(cd "$(mktemp -d)" && pwd -P)
GOOS=linux GOARCH=$(go env GOARCH) CGO_ENABLED=0 go build -o $S/workbench ./cmd/workbench; echo EXIT:$?
cp "$(scripts/scratch-init.sh personal pinned)" $S/answers.toml
chezmoi --config $S/answers.toml --source "$PWD" execute-template < home/.chezmoiscripts/shared/run_after_05-migrations.sh.tmpl > $S/05.sh; echo EXIT:$?
python3 -I - $S/ledger.sqlite <<'PY'
import sqlite3, sys
db = sqlite3.connect(sys.argv[1])
db.executescript("""
CREATE TABLE responses (request_id TEXT PRIMARY KEY, ts TEXT NOT NULL, model TEXT NOT NULL,
  project TEXT NOT NULL, session_id TEXT NOT NULL, account TEXT NOT NULL, account_source TEXT NOT NULL,
  input INTEGER NOT NULL, output INTEGER NOT NULL, cache_write_5m INTEGER NOT NULL,
  cache_write_1h INTEGER NOT NULL, cache_read INTEGER NOT NULL);
CREATE INDEX responses_ts ON responses (ts);
CREATE INDEX responses_project_model ON responses (project, model);
CREATE INDEX responses_account ON responses (account);
CREATE TABLE files (path TEXT PRIMARY KEY, offset INTEGER NOT NULL, size INTEGER NOT NULL,
  mtime_ns INTEGER NOT NULL, last_seen TEXT NOT NULL);
CREATE TABLE meta (key TEXT PRIMARY KEY, value TEXT NOT NULL);
INSERT INTO meta VALUES ('schema_version', '1');
INSERT INTO responses VALUES ('req-1','2026-09-01T10:00:00.000Z','claude-opus-5','/w/a','s1','a@example.test','sweep',100,200,300,0,1000);
INSERT INTO responses VALUES ('req-2','2026-09-02T10:00:00.000Z','claude-opus-5','/w/b','s2','a@example.test','sweep',1,2,3,4,5);
INSERT INTO responses VALUES ('req-3','2026-09-03T10:00:00.000Z','claude-opus-5','/w/b','s3','a@example.test','sweep',1,2,3,4,5);
""")
db.commit(); db.close()
PY
cat > $S/linux.sh <<'SH'
set -euo pipefail
if [ "$(id -u)" = 0 ]; then
    apt-get update -qq >/dev/null && apt-get install -y -qq sqlite3 ca-certificates >/dev/null
    useradd -m wb
    exec runuser -u wb -- bash /s/linux.sh
fi
export HOME=/home/wb WORKBENCH_DATA_DIR=/other/data
export CLAUDE_COSTS_PRICING_URL=http://127.0.0.1:9/none CODEX_COSTS_PRICING_URL=http://127.0.0.1:9/none
W=$HOME/.local/bin/workbench
seed() {
    mkdir -p $HOME/.local/bin $HOME/.local/share/claude-costs $HOME/.local/state/claude-costs $HOME/.cache
    [ -e $W ] || cp /s/workbench $W
    cp /s/ledger.sqlite $HOME/.local/share/claude-costs/ledger.sqlite
    echo '{}' > $HOME/.local/share/claude-costs/rates-official.json
    printf '# /usr/local/bin/workbench\n' > $HOME/.cache/workbench-completion.bash
}
rows() { sqlite3 "$1" 'SELECT COUNT(*) FROM responses'; }
seed
echo "== filesystems"; stat -f -c '%T %n' $HOME/.local/share /other
echo "== rows before: $(rows $HOME/.local/share/claude-costs/ledger.sqlite)"
echo "== planning"
cd /src
$W update --local-build >/tmp/update.out 2>&1 || { tail -20 /tmp/update.out; exit 1; }
cp /s/answers.toml /tmp/answers.toml; chmod 600 /tmp/answers.toml
D=$($W init --answers-from /tmp/answers.toml --dry-run --json | sed -n 's/.*"plan_digest":"\([0-9a-f]*\)".*/\1/p')
$W init --answers-from /tmp/answers.toml --approve-plan "$D" >/dev/null
$W apply --local-build --dry-run --non-interactive 2>&1 | grep -E 'checking migrations|migrations: |checking what each' || true
cd /
echo "== list"; $W migrate
D=$($W migrate --apply --dry-run --json | sed -n 's/.*"plan_digest":"\([0-9a-f]*\)".*/\1/p')
echo "== apply"; $W migrate --apply --approve-plan "$D"
ls -la /other/data/costs /other/data/costs/claude
echo "== rows after: $(rows /other/data/costs/ledger.sqlite)"
test ! -e $HOME/.local/share/claude-costs && echo "old folder gone"
test ! -e $HOME/.cache/workbench-completion.bash && echo "old completion gone"
echo "== script"; WORKBENCH_EFFECT_MIGRATIONS=1 bash /s/05.sh
echo "== hook on a fresh seed"
rm -rf /other/data/costs; seed
printf '%s' '{"hook_event_name":"SessionStart","transcript_path":"","session_id":""}' | $W costs ingest
sleep 3
echo "== rows after the hook: $(rows /other/data/costs/ledger.sqlite)"
test ! -e $HOME/.local/share/claude-costs/ledger.sqlite && echo "old ledger gone"
SH
echo EXIT:$?
```

- [ ] **Step 2: Run it**

```zsh
docker run --rm --tmpfs /other:mode=1777,exec -v $S:/s:ro -v "$PWD":/src:ro ubuntu:24.04 bash /s/linux.sh 2>&1 | grep -v debconf; echo EXIT:${pipestatus[1]}
```

Expected:
- `overlayfs /home/wb/.local/share` and `tmpfs /other`: different filesystems.
- `== rows before: 3`.
- The planning lines `    checking migrations`, `    migrations: 2 to run`, `    checking what each step would do here`.
- `migrate` lists `0001` and `0002` pending; `--apply --approve-plan` prints `0001-costs-folders: done` and `0002-completion-cache: done`, then `migrations: complete`.
- `/other/data/costs` is `drwx------`, holding a `-rw-------` `ledger.sqlite` and `claude/rates-official.json`.
- `== rows after: 3`, `old folder gone`, `old completion gone`.
- The script's run prints `==> migrations: nothing to migrate`.
- On a fresh seed, the hook's worker moves the ledger by itself: `== rows after the hook: 3` and `old ledger gone`.
- `EXIT:0`.

- [ ] **Step 3: Record the evidence** as in Task 8, step 8.

---

### Task 10: Docs

**Files:**
- Modify: `docs/usage.md`
- Modify: `docs/superpowers/specs/workbench-contracts.md` (the `workbench costs` row)
- Modify: `docs/superpowers/specs/workbench-design.md` (package table; Runtime context)
- Modify: `docs/superpowers/specs/2026-09-30-claude-costs-ledger-design.md` (lines 76-86, 243, 585-586)

**Interfaces:** none.

- [ ] **Step 1: `docs/usage.md`**

In the Commands table, add a row after the row starting `` | `costs ingest` ``:

```markdown
| `migrate` | List the migrations that move files earlier Workbench releases left outside Workbench's folders, and whether each has anything to do. `migrate --apply` runs them, asking the way `apply` does (`--dry-run --json`, `--approve-plan`, `--yes`); every `apply` runs them too, as its **Move Workbench's files** step, and the costs import moves the costs files by itself. |
```

Replace the costs paragraph from `` `costs` figures are list-price equivalents `` through `` (`CLAUDE_COSTS_RATES`), keyed by model `` with:

```markdown
`costs` figures are list-price equivalents, not subscription charges. The
ledger lives in Workbench's data folder, at `costs/ledger.sqlite`
(`~/Library/Application Support/workbench/data` on macOS,
`~/.local/share/workbench` on Linux; `WORKBENCH_DATA_DIR` or `XDG_DATA_HOME`
moves it with the rest), because Claude Code deletes transcripts after about
30 days and the ledger is the only lasting record; Workbench never deletes or
rebuilds it, and only refines rows it recorded (attribution, service tier,
duplicate copies). It is private: 0600, in a 0700 folder. Releases before the
migrations kept it in `~/.local/share/claude-costs`; the first import after an
update moves it, and `costs` asks for `workbench migrate --apply` until then.
The first `costs` or `costs status` for a tool that has transcripts but no rows
in the ledger ingests them once inline. Manual rate overrides go in
`costs/rates.json` in Workbench's config folder, keyed by model
```

(the sentence then continues `prefix, with any of` as before).

In the completion paragraph, replace ``The shell startup file saves the script once per installed `workbench` under`` + `` `~/.cache` (`XDG_CACHE_HOME`) `` with ``The shell startup file saves the script once per installed `workbench` in Workbench's cache folder (`~/Library/Caches/workbench` on macOS, `~/.cache/workbench` on Linux, or `WORKBENCH_CACHE_DIR` / `XDG_CACHE_HOME`), private,``. Keep the rest of the paragraph.

- [ ] **Step 2: `workbench-contracts.md`**

In the row whose first cell starts `` `workbench costs` (`internal/costs/`) ``, replace ``writes only `~/.local/share/claude-costs` and `~/.local/state/claude-costs` `` with ``writes only `costs/` in Workbench's data, state and config folders``. At the end of that row's last cell, before the closing `|`, add: `` `workbench migrate` (`internal/migrate/`, the apply's `run_after_05-migrations` step) moves the files earlier releases wrote outside Workbench's folders into them, and the import worker moves the costs files itself (see the [migrations design](2026-10-14-workbench-migrations-design.md)).``

- [ ] **Step 3: `workbench-design.md`**

In the package table, after the `` `internal/costs/` `` row, add:

```markdown
| `internal/migrate/` | Numbered, idempotent migrations that move files earlier releases wrote into Workbench's runtime folders and delete their old caches; see the [migrations design](2026-10-14-workbench-migrations-design.md) and ADR 0008. |
```

In `### Runtime context`, at the end of its first paragraph, add: `Every file Workbench writes lives under these runtime folders; numbered migrations move files earlier releases wrote elsewhere (see the [migrations design](2026-10-14-workbench-migrations-design.md)).`

- [ ] **Step 4: The costs ledger design**

In `docs/superpowers/specs/2026-09-30-claude-costs-ledger-design.md`:
- **Paths table (lines 76-80):** replace the paths with `<data>/costs/ledger.sqlite`, `<data>/costs/<tool>/rates-official.json`, `<config>/costs/rates.json`, `<state>/costs/ingest.log` and `<state>/costs/ingest.lock`. In the ledger row, change "the tool never deletes, renames or recreates it" to "the tool never deletes or recreates it, and moved it once, into Workbench's data folder".
- **The paragraph after the table (lines 82-86):** replace it with: `<data>, <state> and <config> are Workbench's runtime folders (WORKBENCH_{DATA,STATE,CONFIG}_DIR, else XDG_{DATA,STATE,CONFIG}_HOME/workbench when absolute, else the platform default); the [migrations design](2026-10-14-workbench-migrations-design.md) moved the files there, and CLAUDE_COSTS_LEDGER, CLAUDE_COSTS_RATES and CLAUDE_COSTS_STATE no longer relocate them. The old per-file cache under ~/.cache/claude-costs is deleted by migration 0003.`
- **Rate order (line 243):** `` 1. `~/.config/claude-costs/rates.json`, the manual override file. `` becomes `` 1. `<config>/costs/rates.json`, the manual override file. ``
- **Ledger compatibility (lines 585-586):** replace the first sentence (`The ledger stays at … the only lasting record.`) with: `The ledger lives in Workbench's data folder, moved there from ~/.local/share/claude-costs by migration 0001 (see the [migrations design](2026-10-14-workbench-migrations-design.md)); CLAUDE_COSTS_LEDGER, CLAUDE_COSTS_STATE and CLAUDE_COSTS_RATES no longer relocate anything.`

- [ ] **Step 5: Check no doc still names the old write paths as current**

```zsh
rg -n "claude-costs/(ledger|rates|ingest)|CLAUDE_COSTS_(LEDGER|STATE|RATES)|workbench-completion" docs README.md AGENTS.md --glob '!docs/superpowers/plans/**' --glob '!docs/superpowers/specs/2026-10-14-workbench-migrations-design.md' --glob '!docs/adr/0008-*'; echo EXIT:${pipestatus[1]}
```

Expected: only lines that name the old locations as old: `usage.md`'s "Releases before the migrations kept it in ~/.local/share/claude-costs", and the ledger design's paragraph after the paths table and its compatibility sentence. Older plans in `docs/superpowers/plans/` are history and stay.

- [ ] **Step 6: Commit**

```zsh
git add docs
git commit -F - <<'EOF'
docs(migrate): costs paths, workbench migrate and the completion cache

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
EOF
echo EXIT:$?
```

---

## Spec coverage

| Spec section | Task |
|---|---|
| 1. AGENTS.md exception (with the worker running 0001 without asking), ADR 0008 | 1 |
| 2.2 inventory, old locations (relative values) | 3 (`OldLocations`), 5 (`shellDefault`), 8 step 4 |
| 3. Target layout, 0700/0600 | 3, 4, 7 step 5, 8 step 3, 9 |
| 4. Package, `Migration`, statuses, Check (Lstat plus three small reads), Run (per-file conflicts), record, ownership | 3, 5 |
| 5.1 Script environment (`WORKBENCH_*_DIR`, `WORKBENCH_OLD_<NAME>`, `WORKBENCH_MIGRATIONS`) | 7 steps 2 and 4 |
| 5.2 One `migrations` step, probe lines and outcome counts, planning steps, apply lines, script order and older-binary check | 7, 8 steps 2 and 6 |
| 5.3 `workbench migrate`, `--apply` with `Checking migrations`, consent, conflict exit 4 | 6, 8 step 7 |
| 5.4 Worker moves by itself and imports once the ledger moved; reports refuse; relative variable; ungated hook; `Resolve` errors visible | 3 (`gate`), 4, 8 steps 4-6 |
| 5.5 One run at a time; import locks; checkpoint, close, `-wal`/`-shm` gone | 3, 5 (`Run`) |
| 6.1 `0001-costs-folders`: conflicts per file, caches and finished copies, no-replace renames, the copy with its re-check, resume table with its conflict rows | 2, 3, 5 |
| 6.2 `0002-completion-cache` (foreign file reported), startup files | 5, 7 step 5, 8 step 7 |
| 6.3 `0003-old-costs-cache` | 5 |
| 7. `costs-ingest` Undo and Touches | 7 step 1 |
| 8. Files changed, render checks | 1-7, 10 |
| 9. The one test, eight cases (ten subtests) | 3 |
| 10. Order of work, checks in disposable homes on macOS and Linux | Tasks in that order; 8, 9 |
| 11. Not in scope | Nothing in this plan touches other tools' files or splits the ledger |
