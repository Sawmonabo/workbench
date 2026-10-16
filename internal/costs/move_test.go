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
