package costs

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"

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

// ShellDefault is ${name:-fallback} as the old startup files, script and
// code resolved it. A relative value resolved against whatever folder the old
// process started in and can't be found again: path is then "" and relative
// says "NAME is relative (value)".
func ShellDefault(home, name, fallback string) (path, relative string) {
	switch value := os.Getenv(name); {
	case value == "":
		return fallback, ""
	case filepath.IsAbs(value):
		return filepath.Clean(value), ""
	default:
		return "", fmt.Sprintf("%s is relative (%s)", name, operation.ShowPath(home, value))
	}
}

// OldLocations resolves [OldPaths] from the environment as the old code did.
func OldLocations(home string) OldPaths {
	var old OldPaths
	var why string
	pick := func(override, xdg, fallback string, elem ...string) string {
		path, relative := ShellDefault(home, override, "")
		if path == "" && relative == "" {
			if path, relative = ShellDefault(home, xdg, filepath.Join(home, fallback)); path != "" {
				path = filepath.Join(append([]string{path}, elem...)...)
			}
		}
		why = relative
		if relative != "" {
			old.Notes = append(old.Notes, relative+", so the costs files it named can't be found")
		}
		return path
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
	return slices.DeleteFunc(moves, func(m costsMove) bool { return aliased(m.from, m.to) })
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

// sameFile reports whether two paths are two entries of one file: a move
// whose link stayed after its unlink failed (see operation.RenameNoReplace).
// One entry under two names is not (see aliased): removing either name would
// remove the file.
func sameFile(a, b string) bool {
	left, err := os.Lstat(a)
	if err != nil {
		return false
	}
	right, err := os.Lstat(b)
	return err == nil && os.SameFile(left, right) && !aliased(a, b)
}

// aliased reports whether a and b are one directory entry under two names:
// the same path, a path through a linked folder or a link to the other, or a
// name the filesystem treats as the same one, such as one differing only in
// case. A folder has no second entry, and a file reached under two names with
// a single link has only the one.
func aliased(a, b string) bool {
	if a == b {
		return true
	}
	resolvedA, errA := filepath.EvalSymlinks(a)
	resolvedB, errB := filepath.EvalSymlinks(b)
	if errA == nil && errB == nil && resolvedA == resolvedB {
		return true
	}
	left, err := os.Lstat(a)
	if err != nil {
		return false
	}
	right, err := os.Lstat(b)
	if err != nil || !os.SameFile(left, right) {
		return false
	}
	stat, ok := left.Sys().(*syscall.Stat_t)
	return left.IsDir() || ok && stat.Nlink == 1
}

// linked reports whether path is a symbolic link. A link at an old path is
// never moved or read through, and an old folder that is one is never
// removed: a moved link can point nowhere, and the file it names isn't one
// Workbench placed there.
func linked(path string) bool {
	info, err := os.Lstat(path)
	return err == nil && info.Mode()&fs.ModeSymlink != 0
}

// apart drops each old path that is its new path itself (see aliased), as when
// an owner set CLAUDE_COSTS_LEDGER, CLAUDE_COSTS_STATE or CLAUDE_COSTS_RATES
// to the new file or folder, or linked an old claude-costs folder to the new
// one. Such a file is already where it belongs; nothing moves, and nothing is
// deleted or locked through it.
func (o OldPaths) apart(p Paths) OldPaths {
	if o.Ledger != "" && aliased(o.Ledger, p.Ledger) {
		o.Ledger = ""
	}
	if o.State != "" && aliased(o.State, p.State) {
		o.State = ""
	}
	if o.Overrides != "" && aliased(o.Overrides, p.Overrides) {
		o.Overrides = ""
	}
	return o
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
// Pending or Unfinished while anything is left that a run can move or remove,
// or an old folder can't be read, Conflict when only conflicts are left. Ledger
// is the ledger's own, which alone decides whether the import may open the new
// ledger. Text names the paths, home-relative, and never holds "; ". Kept
// names, home-relative, the files in the old claude-costs folders that
// Workbench didn't write, once nothing else is left.
type FolderCheck struct {
	State, Ledger FolderState
	Text          string
	Kept          []string
}

// CheckFolders says what is left of the costs move. It looks at which paths
// exist (Lstat), reads an old and a new overrides file only when both are
// there, to tell a finished copy from two different files, and lists the old
// claude-costs folders, to find one that is empty, one it can't read, and the
// files it keeps there. It never opens the ledger, locks or changes anything.
func CheckFolders(old OldPaths, p Paths) FolderCheck {
	old = old.apart(p)
	var moved, deleted, conflicts []string
	conflict := func(from, to string) {
		conflicts = append(
			conflicts,
			"leaves "+operation.ShowPath(p.Home, from)+" and "+operation.ShowPath(p.Home, to)+
				", which hold different data (remove one)",
		)
	}
	link := func(from string) {
		conflicts = append(conflicts, "leaves "+operation.ShowPath(p.Home, from)+
			", a link (put the file it names in its place, or remove it)")
	}
	check := FolderCheck{Ledger: ledgerState(old, p)}
	switch check.Ledger {
	case FoldersConflict:
		switch {
		case old.Ledger == "":
			conflicts = append(conflicts, "can't find the old ledger because "+old.Unresolved+
				" (make it absolute, or unset it)")
		case linked(old.Ledger):
			link(old.Ledger)
		case exists(old.Ledger + ".moved"):
			conflict(old.Ledger, old.Ledger+".moved")
		default:
			conflict(old.Ledger, p.Ledger)
		}
	case FoldersUnfinished:
		moved = append(moved, operation.ShowPath(p.Home, old.Ledger)+" (finishing a stopped move)")
	case FoldersPending:
		moved = append(moved, operation.ShowPath(p.Home, old.Ledger))
	default:
	}
	for _, move := range old.moves(p) {
		switch {
		case !exists(move.from):
		case linked(move.from):
			link(move.from)
		case exists(move.to) && (move.cache || sameBytes(move.from, move.to)):
			deleted = append(deleted, operation.ShowPath(p.Home, move.from))
		case exists(move.to):
			conflict(move.from, move.to)
		default:
			moved = append(moved, operation.ShowPath(p.Home, move.from))
		}
	}
	for _, name := range old.leftovers() {
		if exists(name) {
			deleted = append(deleted, operation.ShowPath(p.Home, name))
		}
	}
	folders := old.scan(p)
	var text []string
	if len(moved) > 0 {
		text = append(text, "moves "+strings.Join(moved, ", ")+" into Workbench's folders")
	}
	if len(deleted) > 0 {
		text = append(text, "deletes "+strings.Join(deleted, ", "))
	}
	for _, folder := range folders.empty {
		text = append(text, "removes the empty folder "+folder)
	}
	for _, folder := range folders.unreadable {
		text = append(text, "can't read "+folder)
	}
	text = append(text, conflicts...)
	switch {
	case check.Ledger == FoldersUnfinished:
		check.State = FoldersUnfinished
	case len(moved) > 0 || len(deleted) > 0 || len(folders.empty) > 0 ||
		len(folders.unreadable) > 0:
		check.State = FoldersPending
	case len(conflicts) > 0:
		check.State = FoldersConflict
	default:
		check.State = FoldersDone
		check.Kept = folders.kept
	}
	check.Text = strings.Join(text, " and ")
	return check
}

// ledgerState is where the ledger alone stands, from old paths apart from the
// new ones. One file under both names, old and new or old and set aside, is a
// rename that stopped after linking the new name. Old and new both present,
// or a set-aside original with a ledger back at the old path, is a conflict
// whatever marks a stopped copy left. An old path a relative variable hides,
// or a link at the old path, is a conflict too until a ledger is at the new
// path: the old one may still hold usage, and a new empty one beside it would
// split the record.
func ledgerState(old OldPaths, p Paths) FolderState {
	switch {
	case old.Ledger == "" && exists(p.Ledger):
		return FoldersDone
	case old.Ledger == "":
		if old.Unresolved != "" {
			return FoldersConflict
		}
		return FoldersDone
	case linked(old.Ledger):
		return FoldersConflict
	case sameFile(old.Ledger, p.Ledger), sameFile(old.Ledger, old.Ledger+".moved"):
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

// MoveFolders moves the costs files from old into p's folders, deletes the
// old import log, its lock and unfinished price writes, and removes each old
// claude-costs folder left empty. The caller holds
// [operation.Paths.MigrateLock]. While it works it holds both import locks, so
// no worker of this or an older release writes the ledger mid-move, and it
// moves the ledger only once nothing has it open (see moveLedger). It merges
// nothing and overwrites nothing: a file whose old and new paths both hold
// different data stays where it is, every other file moves, and the error then
// names what is left. Running it again finishes whatever a stopped run left.
// left names, home-relative, the files it found in an old claude-costs folder
// and doesn't know; they stay, with their folder. An old folder it can't read
// is an error once everything else is done.
func MoveFolders(ctx context.Context, old OldPaths, p Paths) (left []string, err error) {
	old = old.apart(p)
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
	var ledgerErr error
	if check.Ledger == FoldersPending || check.Ledger == FoldersUnfinished {
		if _, err = operation.PrivateDirectory(p.Runtime.Data, "costs"); err != nil {
			return nil, err
		}
		// A conflict or a ledger error leaves the ledger's files where they
		// were, and the other files don't depend on them, so they still move.
		ledgerErr = moveLedger(ctx, p.Home, old.Ledger, p.Ledger, false)
		if category := operation.Category(ledgerErr); ledgerErr != nil &&
			category != "conflict" && category != "ledger" {
			return nil, ledgerErr
		}
	}
	if err = moveSmallFiles(old, p); err != nil {
		return nil, err
	}
	for _, name := range old.leftovers() {
		if err = operation.RemoveIfThere(name); err != nil {
			return nil, err
		}
	}
	if err = removeOldFolders(old); err != nil {
		return nil, err
	}
	folders := old.scan(p)
	after := CheckFolders(old, p)
	switch {
	case after.State == FoldersConflict:
		return folders.kept, conflictError(after.Text)
	case len(folders.unreadable) > 0:
		return folders.kept, errors.Join(ledgerErr, operation.Fail(
			operation.ExitFailed,
			"migrate",
			"The costs files can't all move: it "+after.Text,
		))
	}
	// A ledger error, or a copy that differs from its set-aside original, never
	// checks as a conflict, because the check never opens a ledger.
	return folders.kept, ledgerErr
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
		case !exists(move.from), linked(move.from):
			continue
		case exists(move.to) && (move.cache || sameBytes(move.from, move.to)):
			if err := os.Remove(move.from); err != nil {
				return err
			}
			continue
		case exists(move.to):
			continue
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

// folderScan is what the old claude-costs folders hold, home-relative: empty
// are real folders with nothing in them, unreadable those whose listing failed,
// and kept the files the migration doesn't know. Workbench didn't write those,
// so they stay; a file it knows that stays, such as one left in conflict, is
// not one of them. An old folder that is a link, or anything else that isn't a
// real folder, is one itself, and is never read through.
type folderScan struct {
	empty, unreadable, kept []string
}

func (o OldPaths) scan(p Paths) folderScan {
	known := o.known(p)
	var scan folderScan
	for _, folder := range o.folders() {
		shown := operation.ShowPath(p.Home, folder)
		info, err := os.Lstat(folder)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			continue
		case err == nil && !info.IsDir():
			scan.kept = append(scan.kept, shown)
			continue
		}
		entries, err := os.ReadDir(folder)
		switch {
		case err != nil:
			scan.unreadable = append(scan.unreadable, shown)
		case len(entries) == 0:
			scan.empty = append(scan.empty, shown)
		}
		for _, entry := range entries {
			if path := filepath.Join(folder, entry.Name()); !slices.Contains(known, path) {
				scan.kept = append(scan.kept, operation.ShowPath(p.Home, path))
			}
		}
	}
	return scan
}

// removeOldFolders removes each old claude-costs folder that is empty. One
// that holds anything, or isn't a real folder, such as a link, stays.
func removeOldFolders(old OldPaths) error {
	for _, folder := range old.folders() {
		if info, err := os.Lstat(folder); err != nil || !info.IsDir() {
			continue
		}
		err := os.Remove(folder)
		if err != nil && !errors.Is(err, fs.ErrNotExist) && !errors.Is(err, syscall.ENOTEMPTY) {
			return err
		}
	}
	return nil
}

// moveFile moves one small costs file and makes it 0600, never replacing a
// file at to. Across filesystems it writes a temporary copy beside to, syncs
// it, renames it into place and syncs that folder, and only then deletes from;
// a stop between the two leaves the same bytes in both, which the next run
// takes as moved.
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
	if err = operation.SyncDirectory(filepath.Dir(to)); err != nil {
		return err
	}
	return operation.SyncDirectory(filepath.Dir(from))
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
		return err
	}
	return operation.SyncDirectory(filepath.Dir(to))
}

// moveLedger moves the ledger at from to to. It first finishes whatever a
// stopped copy left (see resumeCopy). It never replaces a ledger at to: one
// there with the old one gone is a finished move, and one there with the old
// one still present is a conflict. Otherwise it opens the ledger the normal
// way, which verifies it and upgrades an older schema in place, checkpoints
// and closes it (see quiesce), and renames it, one atomic step. Across
// filesystems it copies it (see copyLedger); alwaysCopy, the tests' switch,
// copies it on one. A zero-byte ledger holds nothing and is deleted. One file
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
	return conflictError("leaves " + operation.ShowPath(home, a) + " and " +
		operation.ShowPath(home, b) + ", which hold different data (remove one)")
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
		return removeDatabase(part)
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
			undo = removeDatabase(part)
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
		shown := operation.ShowPath(home, to)
		return operation.Fail(operation.ExitFailed, "ledger", "the ledger at "+shown+" "+problem+
			", so the original at "+operation.ShowPath(home, moved)+" is kept; delete "+shown+
			", and the next run puts the original back and starts again")
	}
	if err := os.Remove(moved); err != nil {
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

// copyLedger copies the ledger to another filesystem. A transaction holds the
// write lock while VACUUM INTO writes a compact copy at
// to+".part", the copy is checked against the original (see checkCopy) and
// the original's size, time and inode are recorded, so nothing writes in
// between. The original is then closed, and only if nothing has it open and it
// still has the recorded size, time and inode is it renamed to from+".moved"
// on its own filesystem, the copy put in place (see placeCopy), and the
// original deleted. A changed original is copied again; renaming an open
// database apart from its -wal file is what corrupts it, so the lock is not
// held across the renames.
func copyLedger(ctx context.Context, home, from, to string) error {
	part := to + ".part"
	for range 3 {
		before, problem, err := vacuumCopy(ctx, from, part)
		if err != nil || problem != "" {
			removeErr := removeDatabase(part)
			switch {
			case err != nil:
				return err
			case removeErr != nil:
				return badCopy(home, from, problem, leftCopy(home, part))
			}
			return badCopy(home, from, problem, "the copy was discarded")
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
// original's stamp, all while a transaction holds the write lock.
// problem is what the check found wrong with the copy (see checkCopy).
func vacuumCopy(ctx context.Context, from, part string) (_ fileStamp, problem string, err error) {
	ledger, err := OpenLedger(from, false)
	if err != nil {
		return fileStamp{}, "", err
	}
	defer func() { _ = ledger.Close() }()
	tx, err := ledger.db.BeginTx(ctx, nil)
	switch {
	case isBusy(err):
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
// never deleted for it. It opens both without taking them over (see unowned),
// so a ledger that turns out to be another one is left as it was.
func checkCopy(ctx context.Context, path, original string) (string, error) {
	db, err := sql.Open("sqlite", unowned(path))
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
		unowned(original),
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

// unowned is the address that opens the database at path without taking it
// over as OpenLedger does: no schema upgrade, journal mode or file mode
// change, and no new file when none is there. A read-only connection can't
// remove the -wal and -shm files it makes beside a WAL database, so the
// connection may write. The check runs only queries, so the content never
// changes, and query_only refuses an ordinary write besides; like any last
// connection to close, it may fold a WAL ledger's log into the file when no
// other process has the ledger open.
func unowned(path string) string {
	query := "mode=rw&_pragma=query_only(1)&_pragma=busy_timeout(5000)"
	return (&url.URL{Scheme: "file", Path: path, RawQuery: query}).String()
}

// leftCopy is what a badCopy error says of a copy left at part.
func leftCopy(home, part string) string {
	return "the copy stays at " + operation.ShowPath(home, part) +
		"; delete it, and the next run starts again from the original"
}

// badCopy is the error of a copy of the ledger at original that checkCopy
// found problem with. The original is kept; then says what became of the copy.
func badCopy(home, original, problem, then string) error {
	return operation.Fail(
		operation.ExitFailed,
		"ledger",
		"a copy of "+operation.ShowPath(home, original)+" "+problem+
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

// settle makes the moved ledger 0600 and syncs both folders, so the move is
// durable before anything reports it done.
func settle(from, to string) error {
	if err := os.Chmod(to, 0o600); err != nil {
		return err
	}
	if err := operation.SyncDirectory(filepath.Dir(from)); err != nil {
		return err
	}
	return operation.SyncDirectory(filepath.Dir(to))
}

// removeDatabase deletes a partial copy with any journal SQLite left beside it.
func removeDatabase(path string) error {
	for _, name := range []string{path + "-wal", path + "-shm", path + "-journal", path} {
		if err := operation.RemoveIfThere(name); err != nil {
			return err
		}
	}
	return nil
}

// gate keeps the ledger closed at its new path while the costs ledger still
// waits to move there, so nothing creates an empty ledger beside the old one.
// [Paths.Open] runs it before every ledger open, and anything that writes
// beside the ledger runs it first. move is for the import worker: it moves the
// files itself, under the same locks as `workbench migrate run`, because usage
// left unread is lost once the session files it is in are deleted; it then
// reads into the new ledger once the ledger itself has moved, even when another
// costs file is left in conflict or didn't move. Every other caller is told to
// run the migration while anything waits, and stops on a ledger conflict; a
// conflict over another file alone doesn't stop it, and `workbench migrate`
// names it.
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
	var moveErr error
	if check.State != FoldersConflict {
		release, held, err := operation.TryLock(paths.Runtime.MigrateLock())
		if err != nil {
			return err
		}
		if held {
			return busy("another migration is running")
		}
		_, moveErr = MoveFolders(ctx, old, paths)
		release()
	}
	// Once the ledger has moved, nothing else the move left stops the import.
	if after := CheckFolders(old, paths); after.Ledger != FoldersDone {
		// The move's own error says why the ledger didn't move, or names a
		// copy that differs from its set-aside original, which the check
		// can't tell from a stopped copy.
		if moveErr != nil {
			return moveErr
		}
		return conflictError(after.Text)
	}
	if moveErr != nil {
		writeLog(paths, "the ledger moved, but not every costs file: "+moveErr.Error())
	}
	return nil
}
