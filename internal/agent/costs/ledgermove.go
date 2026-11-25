package costs

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"

	"github.com/Sawmonabo/workbench/internal/display"
	"github.com/Sawmonabo/workbench/internal/operation"
	"github.com/Sawmonabo/workbench/internal/sqlitefile"
)

// moveLedger moves the ledger at from to to. It first finishes whatever a
// stopped copy left (see resumeCopy). It never replaces a ledger at to: one
// there with the old one gone is a finished move, and one there with the old
// one still present is a conflict. Otherwise it opens the ledger, which
// verifies it, checkpoints and closes it (see quiesce), and renames it, one
// atomic step. It never upgrades a ledger of an earlier version: the first
// open at to does, beside the price lists moved there first (see
// moveFolders). Across filesystems it copies it (see copyLedger); alwaysCopy,
// the tests' switch, copies it on one. A zero-byte ledger holds nothing and is deleted. One file
// under both names is a move that linked the new name and stopped before the
// unlink: the old name goes.
func moveLedger(ctx context.Context, home, from, to string, alwaysCopy bool) error {
	if sameFile(from, to) {
		if err := os.Remove(from); err != nil {
			return err
		}
		return settle(from, to)
	}
	if err := resumeCopy(ctx, home, from, to); err != nil {
		return err
	}
	if exists(to) {
		if exists(from) {
			return ledgerConflict(home, from, to)
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
			return ledgerConflict(home, from, to)
		case !errors.Is(err, syscall.EXDEV):
			return err
		}
	}
	return copyLedger(ctx, home, from, to)
}

// ledgerConflict is the error of two ledgers that both stay, whether the old
// and the new or a new ledger and the set-aside original.
func ledgerConflict(home, a, b string) error {
	return conflictError("leaves " + display.PathFrom(home, a) + " and " +
		display.PathFrom(home, b) + ", which hold different data (remove one)")
}

// resumeCopy finishes or undoes what a stopped copy left. The marks are
// to+".part", a copy being written or checked, and from+".moved", the
// original set aside once its copy was checked. One file under both from and
// from+".moved" is a rename aside or back that linked the new name and
// stopped before the unlink: the original keeps its own name and the copy
// starts again. A ledger back at from beside a set-aside original is a
// conflict, and nothing is touched. The original is deleted only after the
// copy in place is checked again against it.
func resumeCopy(ctx context.Context, home, from, to string) error {
	moved, part := from+".moved", to+".part"
	if sameFile(from, moved) {
		if err := os.Remove(moved); err != nil {
			return err
		}
		if err := operation.SyncDirectory(filepath.Dir(from)); err != nil {
			return err
		}
	}
	if exists(moved) && exists(from) {
		return ledgerConflict(home, from, moved)
	}
	switch {
	case exists(moved) && exists(to):
		return finishCopy(ctx, home, moved, to)
	case exists(moved) && exists(part):
		problem, err := checkCopy(ctx, part, moved)
		switch {
		case err != nil:
			return err
		case problem != "":
			return badCopy(home, moved, problem, leftCopy(home, part))
		}
		return placeCopy(ctx, home, from, to)
	case exists(moved):
		// Set aside with no copy in place: put it back and start again.
		if err := operation.RenameNoReplace(moved, from); err != nil {
			return err
		}
		return operation.SyncDirectory(filepath.Dir(from))
	case exists(part):
		return sqlitefile.Remove(part)
	}
	return nil
}

// placeCopy renames the checked copy at to+".part" into place beside the
// original set aside at from+".moved", then finishes the move (see
// finishCopy). When the copy can't be put in place, for example because a
// ledger appeared at to, the original goes back to from and the copy is
// deleted, so the next run finds both ledgers, a conflict, never a set-aside
// original beside another ledger.
func placeCopy(ctx context.Context, home, from, to string) error {
	moved, part := from+".moved", to+".part"
	if err := operation.RenameNoReplace(part, to); err != nil {
		undo := operation.RenameNoReplace(moved, from)
		if undo == nil {
			undo = operation.SyncDirectory(filepath.Dir(from))
		}
		if undo == nil {
			undo = sqlitefile.Remove(part)
		}
		switch {
		case undo != nil:
			return errors.Join(err, undo)
		case errors.Is(err, fs.ErrExist):
			return ledgerConflict(home, from, to)
		}
		return err
	}
	if err := operation.SyncDirectory(filepath.Dir(to)); err != nil {
		return err
	}
	return finishCopy(ctx, home, moved, to)
}

// finishCopy deletes the set-aside original once the ledger at to holds
// exactly what it holds (see checkCopy). A ledger there that differs is
// another one, a conflict; one that fails integrity_check is a ledger error.
func finishCopy(ctx context.Context, home, moved, to string) error {
	problem, err := checkCopy(ctx, to, moved)
	switch {
	case err != nil:
		return err
	case problem == differs:
		return ledgerConflict(home, moved, to)
	case problem != "":
		shown := display.PathFrom(home, to)
		return operation.Fail(operation.ExitFailed, "ledger", "the ledger at "+shown+" "+problem+
			", so the original at "+display.PathFrom(home, moved)+" is kept; delete "+shown+
			", and the next run puts the original back and starts again")
	}
	if err := os.Remove(moved); err != nil {
		return err
	}
	return settle(moved, to)
}

// quiesce opens the ledger alone, without upgrading it (see openLedger),
// folds its write-ahead log into it and closes it. SQLite deletes the -wal
// and -shm files only when the last connection closes, so either one left
// means another process has the ledger open, for example a report from the
// release before the update: moving it then, or its log apart from it, would
// corrupt it.
func quiesce(ctx context.Context, path string) error {
	ledger, err := openLedger(path, false, false)
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
	if blocked != 0 || slices.ContainsFunc(sqlitefile.SideFiles(path), exists) {
		return busy("another process has the costs ledger open")
	}
	return nil
}

// copyLedger copies the ledger to another filesystem. A transaction holds the
// write lock while VACUUM INTO writes a compact copy at
// to+".part", the copy is checked against the original (see checkCopy) and
// the original's size, time and inode are recorded, so nothing writes in
// between. The original is then closed, and only if nothing has it open and it
// still has the recorded size, time and inode is it renamed to from+".moved"
// on its own filesystem, the copy put in place (see placeCopy), and the
// original deleted. A changed original is copied again; renaming an open
// database apart from its -wal file is what corrupts it, so the lock is not
// held across the renames. Each copy starts with to+".part" deleted with the
// files SQLite keeps beside a database (see [sqlitefile.Remove]), so a side
// file a delete that stopped left there never pairs with the new copy.
func copyLedger(ctx context.Context, home, from, to string) error {
	part := to + ".part"
	for range 3 {
		if err := sqlitefile.Remove(part); err != nil {
			return err
		}
		before, problem, err := vacuumCopy(ctx, from, part)
		if err != nil || problem != "" {
			removeErr := sqlitefile.Remove(part)
			switch {
			case err != nil:
				return err
			case removeErr != nil:
				return badCopy(home, from, problem, leftCopy(home, part))
			}
			return badCopy(home, from, problem, "the copy was discarded")
		}
		if slices.ContainsFunc(sqlitefile.SideFiles(from), exists) {
			_ = sqlitefile.Remove(part)
			return busy("another process opened the costs ledger during the copy")
		}
		if now, err := fileStampOf(from); err != nil || now != before {
			if err = sqlitefile.Remove(part); err != nil {
				return err
			}
			continue
		}
		if err = operation.RenameNoReplace(from, from+".moved"); err != nil {
			return err
		}
		if err = operation.SyncDirectory(filepath.Dir(from)); err != nil {
			return err
		}
		return placeCopy(ctx, home, from, to)
	}
	return busy("the costs ledger kept changing during the copy")
}

// fileStamp is what tells a database file that changed: its size, modification
// time and inode.
type fileStamp struct {
	size  int64
	mod   int64 // nanoseconds since the epoch
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
	return fileStamp{info.Size(), info.ModTime().UnixNano(), inode}, nil
}

// vacuumCopy writes the copy, checks it against the original and returns the
// original's stamp, all while a transaction holds the write lock. It opens
// the original without upgrading it (see openLedger), so the copy keeps its
// version.
// problem is what the check found wrong with the copy (see checkCopy).
func vacuumCopy(ctx context.Context, from, part string) (_ fileStamp, problem string, err error) {
	ledger, err := openLedger(from, false, false)
	if err != nil {
		return fileStamp{}, "", err
	}
	defer func() { _ = ledger.Close() }()
	tx, err := ledger.db.BeginTx(ctx, nil)
	switch {
	case sqlitefile.IsBusy(err):
		return fileStamp{}, "", busy("another process is writing the costs ledger")
	case err != nil:
		return fileStamp{}, "", err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err = ledger.db.ExecContext(ctx, "VACUUM INTO ?", part); err != nil {
		return fileStamp{}, "", err
	}
	if problem, err = checkCopy(ctx, part, from); err != nil || problem != "" {
		return fileStamp{}, problem, err
	}
	before, err := fileStampOf(from)
	if err != nil {
		return fileStamp{}, "", err
	}
	file, err := os.Open(part)
	if err != nil {
		return fileStamp{}, "", err
	}
	err = file.Sync()
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return fileStamp{}, "", err
	}
	return before, "", operation.SyncDirectory(filepath.Dir(part))
}

// differs is what checkCopy finds wrong with a copy that passes
// integrity_check but doesn't hold what its original holds.
const differs = "doesn't hold what it holds"

// checkCopy says what is wrong with the copy at path, "" when it passes PRAGMA
// integrity_check and holds exactly what the database at original holds. As
// many rows is not enough: another ledger can have as many, and an original is
// never deleted for it. It opens both without taking them over (see
// copyCheck), so a ledger that turns out to be another one is left as it was.
func checkCopy(ctx context.Context, path, original string) (string, error) {
	db, err := sqlitefile.Open(path, copyCheck)
	if err != nil {
		return "", err
	}
	defer func() { _ = db.Close() }()
	conn, err := db.Conn(ctx)
	if err != nil {
		return "", err
	}
	defer func() { _ = conn.Close() }()
	if problem, err := integrityCheck(ctx, conn); err != nil || problem != "" {
		return problem, err
	}
	if _, err = conn.ExecContext(
		ctx,
		"ATTACH DATABASE ? AS original",
		sqlitefile.Address(original, copyCheck),
	); err != nil {
		return "", err
	}
	defer func() { _, _ = conn.ExecContext(context.Background(), "DETACH DATABASE original") }()
	same, err := sameTables(ctx, conn)
	if err != nil || same {
		return "", err
	}
	return differs, nil
}

// integrityCheck runs PRAGMA integrity_check and says, on one line, its first
// problem and how many more it found; "" when it says ok. Its rows can hold
// several problems a line each, under a line naming the database.
func integrityCheck(ctx context.Context, conn *sql.Conn) (string, error) {
	rows, err := conn.QueryContext(ctx, "PRAGMA integrity_check")
	if err != nil {
		return "", err
	}
	defer func() { _ = rows.Close() }()
	var found, problems []string
	for rows.Next() {
		var row string
		if err = rows.Scan(&row); err != nil {
			return "", err
		}
		found = append(found, row)
		for line := range strings.SplitSeq(row, "\n") {
			if line = strings.TrimSpace(line); line != "" && !strings.HasPrefix(line, "*** ") {
				problems = append(problems, line)
			}
		}
	}
	switch {
	case rows.Err() != nil:
		return "", rows.Err()
	case slices.Equal(found, []string{"ok"}):
		return "", nil
	case len(problems) == 0:
		problems = found
	}
	problem := problems[0]
	if more := len(problems) - 1; more > 0 {
		problem += fmt.Sprintf(", and %d more", more)
	}
	return "fails SQLite's integrity_check (" + problem + ")", nil
}

// copyCheck is how checkCopy opens the copy and attaches the original,
// without taking either over as OpenLedger does: no schema upgrade, journal
// mode or file mode change, and no new file when none is there. The check
// runs only queries, so the content never changes; like any last connection
// to close, it may fold a WAL ledger's log into the file when no other
// process has the ledger open.
var copyCheck = sqlitefile.Options{ReadOnly: true, Pragmas: []string{"busy_timeout(5000)"}}

// leftCopy is what a badCopy error says of a copy left at part.
func leftCopy(home, part string) string {
	return "the copy stays at " + display.PathFrom(home, part) +
		"; delete it, and the next run starts again from the original"
}

// badCopy is the error of a copy of the ledger at original that checkCopy
// found problem with. The original is kept; then says what became of the copy.
func badCopy(home, original, problem, then string) error {
	return operation.Fail(
		operation.ExitFailed,
		"ledger",
		"a copy of "+display.PathFrom(home, original)+" "+problem+
			", so the original is kept and "+then,
	)
}

// sameTables reports whether the main database and the attached original
// have the same tables, each holding the same rows. Every ledger table has a
// primary key, so a row that EXCEPT finds in one and not the other, either way
// round, is a difference.
func sameTables(ctx context.Context, conn *sql.Conn) (bool, error) {
	schema := func(database string) ([][2]string, error) {
		rows, err := conn.QueryContext(ctx, "SELECT name, sql FROM "+database+
			".sqlite_master WHERE type = 'table' ORDER BY name")
		if err != nil {
			return nil, err
		}
		defer func() { _ = rows.Close() }()
		var tables [][2]string
		for rows.Next() {
			var table [2]string
			if err := rows.Scan(&table[0], &table[1]); err != nil {
				return nil, err
			}
			tables = append(tables, table)
		}
		return tables, rows.Err()
	}
	mine, err := schema("main")
	if err != nil {
		return false, err
	}
	theirs, err := schema("original")
	if err != nil || !slices.Equal(mine, theirs) {
		return false, err
	}
	for _, table := range mine {
		name := `"` + strings.ReplaceAll(table[0], `"`, `""`) + `"`
		var differ bool
		if err = conn.QueryRowContext(ctx, "SELECT EXISTS (SELECT * FROM main."+name+
			" EXCEPT SELECT * FROM original."+name+") OR EXISTS (SELECT * FROM original."+name+
			" EXCEPT SELECT * FROM main."+name+")").Scan(&differ); err != nil || differ {
			return false, err
		}
	}
	return true, nil
}

// settle makes the moved file 0600 and syncs both folders, the new name's
// first, so the move is durable before anything reports it done.
func settle(from, to string) error {
	if err := os.Chmod(to, 0o600); err != nil {
		return err
	}
	if err := operation.SyncDirectory(filepath.Dir(to)); err != nil {
		return err
	}
	return operation.SyncDirectory(filepath.Dir(from))
}
