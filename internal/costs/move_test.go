package costs

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/Sawmonabo/workbench/internal/operation"
)

// The ledger is the only lasting record of spend once the session files it
// was read from are deleted. Moving it into Workbench's folders must never
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
		{"opened by another process during the copy", refuseALedgerOpenedDuringTheCopy},
		{"a link at the old ledger path", refuseALinkAtTheOldLedgerPath},
		{"old and new ledgers both hold data", refuseTwoLedgers},
		{"a conflicting overrides file leaves the ledger free to move", moveTheLedgerPastAnOverridesConflict},
		{"one ledger under both names after a stopped link", finishALinkedMove},
		{"a different ledger at the new path beside the set-aside original", refuseADifferentLedgerBesideTheOriginal},
		{"old paths that name the new files", keepFilesOldPathsAlias},
		{"an old folder linked to the new one", keepFilesThroughALinkedFolder},
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
	if _, err := MoveFolders(ctx, old, paths); err != nil {
		t.Fatal(err)
	}
	assertMoved(t, old, paths, want)
	if data, err := os.ReadFile(paths.Overrides); err != nil ||
		string(data) != `{"claude-opus-5": {"input": 1}}` {
		t.Fatalf("overrides not moved byte for byte: %q, %v", data, err)
	}
}

func moveAcrossFilesystems(t *testing.T) {
	ctx := context.Background()
	old, paths := moveFixture(t)
	want := seedLedger(t, old.Ledger, "req-1", "req-2")
	ensureLedgerFolder(t, paths)
	if err := moveLedger(ctx, paths.Home, old.Ledger, paths.Ledger, true); err != nil {
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
	if _, err := MoveFolders(ctx, old, paths); operation.Category(err) != "conflict" {
		t.Fatalf("got %v, want conflict", err)
	}
	err := moveLedger(ctx, paths.Home, old.Ledger, paths.Ledger, false)
	if operation.Category(err) != "conflict" {
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
	if operation.Category(err) != "busy" {
		t.Fatalf("got %v, want busy", err)
	}
	if _, err := os.Lstat(old.Ledger); err != nil {
		t.Fatalf("the original ledger moved while open: %v", err)
	}
	if _, err := os.Lstat(paths.Ledger); !errors.Is(err, fs.ErrNotExist) {
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
	if _, err := MoveFolders(ctx, old, paths); operation.Category(err) != "conflict" {
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
	if _, err := MoveFolders(ctx, old, paths); operation.Category(err) != "conflict" {
		t.Fatalf("got %v, want conflict over the overrides", err)
	}
	if got := readLedger(t, paths.Ledger); !slices.Equal(got, want) {
		t.Fatalf(
			"the ledger did not move past the overrides conflict: it holds %v, want %v",
			got,
			want,
		)
	}
	if check := CheckFolders(old, paths); check.Ledger != FoldersDone {
		t.Fatalf("the ledger checks as %d after moving, want done", check.Ledger)
	}
}

// moveFixture is a home with the old claude-costs data folder, made 0755 as
// the old script made it, and Workbench's runtime folders inside it. The temp
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
	if err := os.MkdirAll(filepath.Dir(old.Ledger), 0o755); err != nil {
		t.Fatal(err)
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

// seedLedger makes a ledger at path holding one Claude Code row per id, each
// with its own subscription, and one Codex row, closes it, and returns what
// it wrote as readLedger lists it.
func seedLedger(t *testing.T, path string, ids ...string) []string {
	t.Helper()
	ledger, err := OpenLedger(path, true)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ledger.Close() }()
	usage := []Usage{
		{Tool: "codex", RequestID: "codex-1", Model: "gpt-5", Project: "/w/c", Session: "c1"},
	}
	wrote := []string{"responses codex:codex-1"}
	for _, id := range ids {
		usage = append(
			usage,
			Usage{
				Tool:              "claude",
				RequestID:         id,
				Model:             "claude-opus-5",
				Project:           "/w/a",
				Session:           "s1",
				Subscription:      "plan-" + id,
				SubscriptionLabel: "Plan " + id,
			},
		)
		wrote = append(wrote, "responses "+id, "subscriptions plan-"+id+" Plan "+id)
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
	slices.Sort(wrote)
	return wrote
}

// readLedger lists the responses and subscriptions of the ledger at path,
// read through a plain connection rather than the code under test, after
// PRAGMA integrity_check says ok.
func readLedger(t *testing.T, path string) []string {
	t.Helper()
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	var result string
	if err = db.QueryRow("PRAGMA integrity_check").Scan(&result); err != nil || result != "ok" {
		t.Fatalf("integrity_check of %s: %q, %v", path, result, err)
	}
	var found []string
	for _, query := range []string{
		"SELECT 'responses ' || request_id FROM responses",
		"SELECT 'subscriptions ' || id || ' ' || label FROM subscriptions",
	} {
		rows, err := db.Query(query)
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			var row string
			if err = rows.Scan(&row); err != nil {
				t.Fatal(err)
			}
			found = append(found, row)
		}
		if err = rows.Close(); err != nil {
			t.Fatal(err)
		}
	}
	slices.Sort(found)
	return found
}

// assertMoved checks the end state of every case that moves: the new ledger
// passes integrity_check and holds exactly the rows the original was seeded
// with; the original and the copy's marks are gone; and a further run leaves
// the ledger as it was and nothing to do.
func assertMoved(t *testing.T, old OldPaths, paths Paths, want []string) {
	t.Helper()
	if got := readLedger(t, paths.Ledger); !slices.Equal(got, want) {
		t.Fatalf("the new ledger holds %v, want %v", got, want)
	}
	for _, gone := range []string{old.Ledger, old.Ledger + ".moved", paths.Ledger + ".part"} {
		if _, err := os.Lstat(gone); !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("%s should be gone: %v", gone, err)
		}
	}
	before := readBytes(t, paths.Ledger)
	if _, err := MoveFolders(context.Background(), old, paths); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, readBytes(t, paths.Ledger)) {
		t.Fatal("a run after the move changed the ledger")
	}
	if check := CheckFolders(old, paths); check.State != FoldersDone {
		t.Fatalf("after the move the check says %d (%s), want done", check.State, check.Text)
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

// keepFilesOldPathsAlias is an owner who pointed CLAUDE_COSTS_LEDGER,
// CLAUDE_COSTS_STATE and CLAUDE_COSTS_RATES at the new files. Each old path
// is then the new file itself, not a copy of it: removing it as a finished
// move would delete the only ledger and the overrides, and locking its import
// lock twice would leave the move, and every import, busy for good.
func keepFilesOldPathsAlias(t *testing.T) {
	old, paths := moveFixture(t)
	ensureLedgerFolder(t, paths)
	want := seedLedger(t, paths.Ledger, "req-1", "req-2")
	writeFile(t, paths.Overrides, `{"claude-opus-5": {"input": 1}}`, 0o600)
	writeFile(t, filepath.Join(paths.State, "ingest.log"), "log\n", 0o600)
	writeFile(t, paths.lock(), "", 0o600)
	old.Ledger, old.State, old.Overrides = paths.Ledger, paths.State, paths.Overrides
	files := []string{
		paths.Ledger, paths.Overrides, filepath.Join(paths.State, "ingest.log"), paths.lock(),
	}
	assertKept(t, old, paths, files, want)
}

// keepFilesThroughALinkedFolder is an old claude-costs folder that an owner
// replaced with a link to the new costs folder, to keep an older tool
// working: the old ledger is the new one under another name.
func keepFilesThroughALinkedFolder(t *testing.T) {
	old, paths := moveFixture(t)
	ensureLedgerFolder(t, paths)
	want := seedLedger(t, paths.Ledger, "req-1", "req-2")
	folder := filepath.Dir(old.Ledger)
	if err := os.Remove(folder); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Dir(paths.Ledger), folder); err != nil {
		t.Fatal(err)
	}
	assertKept(t, old, paths, []string{paths.Ledger}, want)
	if info, err := os.Lstat(folder); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("the owner's link at %s is gone or changed: %v, %v", folder, info, err)
	}
}

// assertKept checks that the check calls nothing left to do, and that a run
// succeeds and leaves every one of files byte for byte and the ledger's rows.
func assertKept(t *testing.T, old OldPaths, paths Paths, files, want []string) {
	t.Helper()
	before := readAll(t, files)
	if check := CheckFolders(old, paths); check.State != FoldersDone {
		t.Fatalf("the check says %d (%s), want done", check.State, check.Text)
	}
	if _, err := MoveFolders(context.Background(), old, paths); err != nil {
		t.Fatal(err)
	}
	for i, after := range readAll(t, files) {
		if !bytes.Equal(before[i], after) {
			t.Fatalf("a run changed %s", files[i])
		}
	}
	if got := readLedger(t, paths.Ledger); !slices.Equal(got, want) {
		t.Fatalf("the ledger holds %v, want %v", got, want)
	}
}

// refuseADifferentLedgerBesideTheOriginal is a copy that stopped between
// setting the original aside and putting its checked copy in place, with
// another ledger, holding as many rows, at the new path since. Matching row
// counts must never let a run delete the original; every run is a conflict
// that changes nothing.
func refuseADifferentLedgerBesideTheOriginal(t *testing.T) {
	ctx := context.Background()
	old, paths := moveFixture(t)
	seedLedger(t, old.Ledger, "orig-1", "orig-2")
	ensureLedgerFolder(t, paths)
	vacuumInto(t, old.Ledger, paths.Ledger+".part")
	rename(t, old.Ledger, old.Ledger+".moved")
	seedLedger(t, paths.Ledger, "foreign-1", "foreign-2")
	files := []string{old.Ledger + ".moved", paths.Ledger + ".part", paths.Ledger}
	before := readAll(t, files)
	for range 2 {
		if _, err := MoveFolders(ctx, old, paths); operation.Category(err) != "conflict" {
			t.Fatalf("got %v, want conflict", err)
		}
		for i, after := range readAll(t, files) {
			if !bytes.Equal(before[i], after) {
				t.Fatalf("a conflict changed %s", files[i])
			}
		}
	}
}

// refuseALedgerOpenedDuringTheCopy is a writer that opens the old ledger while
// the copy across filesystems runs. What it writes goes to the -wal file
// beside the original, which the size, time and inode the copy recorded don't
// see: setting the original aside and deleting it would lose those rows. The
// copy must stop as busy and leave the original whole.
func refuseALedgerOpenedDuringTheCopy(t *testing.T) {
	ctx := context.Background()
	old, paths := moveFixture(t)
	want := seedLedger(t, old.Ledger, "req-1", "req-2")
	ensureLedgerFolder(t, paths)
	other, err := sql.Open("sqlite", old.Ledger)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = other.Close() }()
	if _, err = other.Exec("SELECT COUNT(*) FROM responses"); err != nil {
		t.Fatal(err)
	}
	err = copyLedger(ctx, paths.Home, old.Ledger, paths.Ledger)
	if operation.Category(err) != "busy" {
		t.Fatalf("got %v, want busy", err)
	}
	if got := readLedger(t, old.Ledger); !slices.Equal(got, want) {
		t.Fatalf("the original holds %v, want %v", got, want)
	}
	for _, gone := range []string{paths.Ledger, paths.Ledger + ".part", old.Ledger + ".moved"} {
		if _, err := os.Lstat(gone); !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("%s is there after a refused copy: %v", gone, err)
		}
	}
}

// refuseALinkAtTheOldLedgerPath is an old ledger path that is a relative
// link to the ledger. Moving the link would leave it pointing nowhere at the
// new path, and the next open would make an empty ledger there: the rows stay
// behind the link, out of every report. It must be a conflict that changes
// nothing.
func refuseALinkAtTheOldLedgerPath(t *testing.T) {
	ctx := context.Background()
	old, paths := moveFixture(t)
	real := filepath.Join(filepath.Dir(old.Ledger), "kept.sqlite")
	seedLedger(t, real, "req-1", "req-2")
	if err := os.Symlink("kept.sqlite", old.Ledger); err != nil {
		t.Fatal(err)
	}
	before := readBytes(t, real)
	if _, err := MoveFolders(ctx, old, paths); operation.Category(err) != "conflict" {
		t.Fatalf("got %v, want conflict", err)
	}
	if target, err := os.Readlink(old.Ledger); err != nil || target != "kept.sqlite" {
		t.Fatalf("the link at the old path is %q, %v; want it kept", target, err)
	}
	if !bytes.Equal(before, readBytes(t, real)) {
		t.Fatal("a conflict changed the ledger behind the link")
	}
	if _, err := os.Lstat(paths.Ledger); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("something is at the new ledger path: %v", err)
	}
}
