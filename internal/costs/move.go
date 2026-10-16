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
