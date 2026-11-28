package costs

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"

	"github.com/Sawmonabo/workbench/internal/display"
	"github.com/Sawmonabo/workbench/internal/operation"
)

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
// ledger. Data is State with the empty old folders left out: they hold
// nothing, so only the migration waits for them. Text is what a run does
// and each old folder it can't read, one clause naming the paths,
// home-relative ("moves …, deletes …"), "" when nothing; Problems is what
// keeps the move from finishing, a line for each pair of files that hold
// different data and each link, then the fix of each kind. Kept names, home-relative, the files
// in the old claude-costs folders that Workbench didn't write, once nothing
// else is left.
type FolderCheck struct {
	State, Ledger, Data FolderState
	Text                string
	Problems            operation.Body
	Kept                []string
}

// CheckOwnFolders checks the costs data and state folders in use, as a check
// that only reads Workbench's own folders does (see
// [operation.CheckPrivateDirectory]): one others can write in is refused, so
// nothing reads in it what is not Workbench's, and one this user may not read
// or search gets 0700, so nothing reads in it what is not there; it changes
// nothing else. A missing folder passes.
func CheckOwnFolders(p Paths) error {
	for _, f := range [][2]string{{p.Runtime.Data, filepath.Dir(p.Ledger)}, {p.Runtime.State, p.State}} {
		name, err := filepath.Rel(f[0], f[1])
		if err != nil || !filepath.IsLocal(name) {
			continue
		}
		if err = operation.CheckPrivateDirectory(f[0], name); err != nil {
			return err
		}
	}
	return nil
}

// conflictLabel labels each problem [CheckFolders] finds.
const conflictLabel = "conflict"

// The fixes of the problems [CheckFolders] finds.
var (
	fixDiffers = operation.FixText("move one of them aside")
	fixLink    = operation.FixText("put the file each link names in its place, or remove the link")
)

// Said is the check as a headline and its body: Text and the problems, or,
// when a run has nothing to do, the one problem as the headline with its
// cause and fix, or "the costs files can't all move" above several.
func (c FolderCheck) Said() (string, operation.Body) {
	lines := c.Problems.Lines
	conflicts := 0
	for _, line := range lines {
		if line.Label == conflictLabel {
			conflicts++
		}
	}
	switch {
	case c.Text != "" || conflicts == 0:
		return c.Text, c.Problems
	case conflicts == 1:
		return lines[0].Text, operation.Body{Lines: lines[1:], Fix: c.Problems.Fix}
	}
	return "the costs files can't all move", c.Problems
}

// CheckFolders says what is left of the costs move. It looks at which paths
// exist (Lstat) and lists the old claude-costs folders, to find one that is
// empty, one it can't read, and the files it keeps there. It never opens the
// ledger, locks or changes anything.
func CheckFolders(old OldPaths, p Paths) FolderCheck {
	old = old.apart(p)
	var moved, deleted []string
	var problems []operation.Part
	var differs, links bool
	conflict := func(from, to string) {
		problems = append(problems, differsLine(p.Home, from, to))
		differs = true
	}
	link := func(from string) {
		problems = append(problems, operation.Line{
			Label: conflictLabel,
			Text:  display.PathFrom(p.Home, from) + " is a link",
		})
		links = true
	}
	check := FolderCheck{Ledger: ledgerState(old, p)}
	switch check.Ledger {
	case FoldersConflict:
		switch {
		case old.Ledger == "":
			// old.Unresolved names the variable: "CLAUDE_COSTS_LEDGER is
			// relative (…)".
			variable, _, _ := strings.Cut(old.Unresolved, " ")
			problems = append(problems,
				operation.Line{Label: conflictLabel, Text: "the old ledger can't be found"},
				operation.Cause(old.Unresolved),
				operation.FixText("make "+variable+" absolute, or unset it"))
		case linked(old.Ledger):
			link(old.Ledger)
		case exists(old.Ledger + ".moved"):
			conflict(old.Ledger, old.Ledger+".moved")
		default:
			conflict(old.Ledger, p.Ledger)
		}
	case FoldersUnfinished:
		moved = append(moved, display.PathFrom(p.Home, old.Ledger)+" (finishing a stopped move)")
	case FoldersPending:
		moved = append(moved, display.PathFrom(p.Home, old.Ledger))
	default:
	}
	for _, move := range old.moves(p) {
		switch {
		case !exists(move.from):
		case linked(move.from):
			link(move.from)
		case exists(move.to):
			deleted = append(deleted, display.PathFrom(p.Home, move.from))
		default:
			moved = append(moved, display.PathFrom(p.Home, move.from))
		}
	}
	for _, name := range old.leftovers() {
		if exists(name) {
			deleted = append(deleted, display.PathFrom(p.Home, name))
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
	text = append(text, folders.unreadable...)
	if differs {
		problems = append(problems, fixDiffers)
	}
	if links {
		problems = append(problems, fixLink)
	}
	check.Problems = operation.Explain(problems...)
	check.settle(
		len(moved) > 0 || len(deleted) > 0 || len(folders.unreadable) > 0,
		len(check.Problems.Lines) > 0,
		folders,
	)
	check.Text = strings.Join(text, " and ")
	return check
}

// settle sets Data, State and Kept from the ledger's state, whether a run has
// data to move or delete or a folder to read, whether conflicts are left, and
// the old folders.
func (c *FolderCheck) settle(work, conflicts bool, folders folderScan) {
	switch {
	case c.Ledger == FoldersUnfinished:
		c.Data = FoldersUnfinished
	case work:
		c.Data = FoldersPending
	case conflicts:
		c.Data = FoldersConflict
	default:
		c.Data = FoldersDone
	}
	c.State = c.Data
	if len(folders.empty) > 0 && (c.Data == FoldersDone || c.Data == FoldersConflict) {
		c.State = FoldersPending
	}
	if c.State == FoldersDone {
		c.Kept = folders.kept
	}
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
		why,
		operation.Changed("nothing moved"),
		operation.Line{Label: "next run", Text: "the next import or apply tries again"},
	)
}

// conflictError is the error of a move that check leaves in conflict: its
// problems, and what a run would still do once they are dealt with.
func conflictError(check FolderCheck) error {
	return operation.FailWith(
		operation.ExitConflict,
		"conflict",
		"The costs files can't all move",
		check.left(),
	)
}

// differsLine is the problem of the files at a and b, which hold different
// data.
func differsLine(home, a, b string) operation.Line {
	return operation.Line{
		Label: conflictLabel,
		Text: display.PathFrom(
			home,
			a,
		) + " and " + display.PathFrom(
			home,
			b,
		) + " hold different data",
	}
}

// left is check's problems, after what a run would still do.
func (c FolderCheck) left() operation.Body {
	if c.Text == "" {
		return c.Problems
	}
	return operation.Explain(operation.Line{Label: "would also", Text: c.Text}).With(c.Problems)
}

// MoveFolders moves the costs files from old into p's folders, the price
// lists before the ledger, deletes the old import log, its lock and
// unfinished price writes, and removes each old claude-costs folder left
// empty. The caller holds
// [operation.Paths.MigrateLock]. While it works it holds both import locks, so
// no worker of this or an older release writes the ledger mid-move, and it
// moves the ledger only once nothing has it open (see moveLedger). It merges
// nothing and overwrites nothing: a file whose old and new paths both hold
// different data stays where it is, every other file moves, and the error then
// names what is left. Running it again finishes whatever a stopped run left.
// left names, home-relative, the files it found in an old claude-costs folder
// and doesn't know; they stay, with their folder. An old folder it can't read
// is an error once everything else is done.
//
// It first moves the costs folders under agent/ when it may (see
// [MoveAgentFolders]) and moves the files to where the costs folders are
// then, so it finds a ledger already in use where it is and never takes
// another for it. A move of those folders that fails leaves them where they
// are, which is the agent folders migration's to report.
func MoveFolders(ctx context.Context, old OldPaths, p Paths) (left []string, err error) {
	p, _ = locate(p.Home, p.Runtime)
	return moveFolders(ctx, old, p)
}

func moveFolders(ctx context.Context, old OldPaths, p Paths) (left []string, err error) {
	old = old.apart(p)
	check := CheckFolders(old, p)
	switch check.State {
	case FoldersDone:
		return nil, nil
	case FoldersConflict:
		return nil, conflictError(check)
	default:
	}
	release, err := lockImports(old, p)
	if err != nil {
		return nil, err
	}
	defer release()
	// Check again under the locks: an import may have finished in between.
	check = CheckFolders(old, p)
	// The price lists move first, beside where the ledger goes, so the first
	// open of a ledger of an earlier version there upgrades it with their
	// prices (see fillPrices); the move itself never upgrades it (see
	// moveLedger). A list that can't move doesn't keep the ledger from
	// moving: its upgrade then fills nothing for that tool, whose list the
	// import downloads again.
	listsErr := moveSmallFiles(old, p)
	var ledgerErr error
	if check.Ledger == FoldersPending || check.Ledger == FoldersUnfinished {
		if _, err = p.dataFolder(""); err != nil {
			return nil, errors.Join(listsErr, err)
		}
		// A conflict or a ledger error leaves the ledger's files where they
		// were, and the other files don't depend on them, so they still move.
		ledgerErr = moveLedger(ctx, p.Home, old.Ledger, p.Ledger, false)
		if category := operation.Category(ledgerErr); ledgerErr != nil &&
			category != "conflict" && category != "ledger" {
			return nil, errors.Join(listsErr, ledgerErr)
		}
	}
	if listsErr != nil {
		return nil, listsErr
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
	case after.State == FoldersConflict, after.Ledger == FoldersConflict:
		// The text names the ledger's conflict, so ledgerErr would repeat it.
		return folders.kept, conflictError(after)
	case len(folders.unreadable) > 0:
		return folders.kept, errors.Join(ledgerErr, operation.FailWith(
			operation.ExitFailed,
			"migrate",
			"The costs files can't all move",
			after.left(),
		))
	}
	// A ledger error, or a copy that differs from its set-aside original, never
	// checks as a conflict, because the check never opens a ledger.
	return folders.kept, ledgerErr
}

// lockImports takes the import lock of this release, in the costs state
// folder made private first (see [Paths.stateFolder]), and, when the old
// claude-costs state folder is there, the one in it, so neither release's
// worker runs mid-move. That folder is outside Workbench's runtime folders,
// may be one the person named (CLAUDE_COSTS_STATE) and holds only the old
// import log and lock, which the move deletes: nothing is made there, and a
// folder gone meanwhile has no lock to take.
func lockImports(old OldPaths, p Paths) (release func(), err error) {
	if _, err = p.stateFolder(); err != nil {
		return nil, err
	}
	locks := []string{p.lock()}
	if old.State != "" && exists(old.State) {
		locks = append(locks, Paths{State: old.State}.lock())
	}
	var releases []func()
	release = func() {
		for _, r := range slices.Backward(releases) {
			r()
		}
	}
	for i, lock := range locks {
		f, held, err := operation.TryLockIn(lock)
		switch {
		case i > 0 && errors.Is(err, fs.ErrNotExist):
			continue
		case err == nil && held:
			err = busy("Claude Code or Codex usage is being read into the ledger")
		}
		if err != nil {
			release()
			return nil, err
		}
		releases = append(releases, func() { _ = f.Close() })
	}
	return release, nil
}

// moveSmallFiles moves the price caches beside the ledger. An old cache whose
// new path already holds one is deleted: a cache is fetched again when stale.
func moveSmallFiles(old OldPaths, p Paths) error {
	for _, move := range old.moves(p) {
		switch {
		case !exists(move.from), linked(move.from):
			continue
		case exists(move.to):
			if err := os.Remove(move.from); err != nil {
				return err
			}
			continue
		}
		folder, err := move.folder()
		if err != nil {
			return err
		}
		operation.RemoveStaleWrites(folder)
		if err := moveFile(move.from, move.to); err != nil {
			return err
		}
	}
	return nil
}

// removeOldFolders removes each old claude-costs folder that is empty. One
// that holds anything, or isn't a real folder, such as a link, stays.
func removeOldFolders(old OldPaths) error {
	for _, folder := range old.folders() {
		if !operation.IsFolder(folder) {
			continue
		}
		if err := operation.RemoveIfEmpty(folder); err != nil {
			return err
		}
	}
	return nil
}

// moveFile moves one small costs file and makes it 0600, never replacing a
// file at to (see settle). Across filesystems it writes a copy at to through
// [operation.CreatePrivateFile], and only then deletes from; a stop between
// the two leaves the same bytes in both, which the next run takes as moved.
// The temporary file of a copy that was killed is deleted before the next
// move into its folder (see [operation.RemoveStaleWrites]).
func moveFile(from, to string) error {
	err := operation.RenameNoReplace(from, to)
	if errors.Is(err, syscall.EXDEV) {
		var data []byte
		if data, err = os.ReadFile(from); err == nil {
			err = operation.CreatePrivateFile(to, data)
		}
		if err == nil {
			err = os.Remove(from)
		}
	}
	if err != nil {
		return err
	}
	return settle(from, to)
}

// gate keeps the ledger closed at its new path while the costs ledger still
// waits to move there, so nothing creates an empty ledger beside the old one.
// [Paths.Open] runs it before every ledger open, and anything that writes
// beside the ledger runs it first. It first moves the costs folders under
// agent/ when it may (see [MoveAgentFolders]) and returns the costs paths as
// they are after that, which the caller then works with, so the move below
// finds a ledger already in use where it is and never takes another for it.
//
// move is for the import worker: it makes the state folder, once the costs
// folders are where they stay, and logs there a move of them that failed; it
// moves the files itself, under the same locks as `workbench migrate run`,
// because usage left unread is lost once the session files it is in are
// deleted; it then reads into the new ledger once the ledger itself has moved,
// even when another costs file is left in conflict or didn't move. Every other
// caller is told to run the migration while any data waits to move, and stops
// on a ledger conflict; a conflict over another file alone doesn't stop it,
// and neither does an empty old folder, which holds nothing. `workbench
// migrate` names both.
func gate(ctx context.Context, paths Paths, move bool) (Paths, error) {
	paths, folders := locate(paths.Home, paths.Runtime)
	if move {
		if _, err := paths.stateFolder(); err != nil {
			return paths, err
		}
		if folders != nil {
			writeLog(paths, "the costs folders stay where they are: "+folders.Error())
		}
	}
	// CheckFolders reads what the costs folders hold, so each is judged as
	// Workbench's own folders are first: one others can write in is refused
	// before anything in it is read; the state folder, which only the import
	// worker writes in, is used as it is by every other caller, but for one
	// its owner may not read or search, which gets 0700; the data
	// folder, which every caller writes in, then gets 0700 when it has another
	// mode, as one its owner may not read or search would hide the ledger.
	if name, err := filepath.Rel(paths.Runtime.State, paths.State); !move && err == nil &&
		filepath.IsLocal(name) {
		if err = operation.CheckPrivateDirectory(paths.Runtime.State, name); err != nil {
			return paths, err
		}
	}
	if operation.IsFolder(filepath.Dir(paths.Ledger)) {
		if _, err := paths.dataFolder(""); err != nil {
			return paths, err
		}
	}
	old := OldLocations(paths.Home)
	check := CheckFolders(old, paths)
	switch {
	case check.State == FoldersDone:
		return paths, nil
	case !move && check.Data == FoldersDone:
		return paths, nil
	case !move && check.Data == FoldersConflict && check.Ledger == FoldersDone:
		return paths, nil
	case !move && check.Data == FoldersConflict:
		return paths, conflictError(check)
	case !move:
		return paths, operation.Fail(
			operation.ExitBlocked,
			"migrate",
			"The costs files are moving to Workbench's folders",
			operation.Fix("workbench", "migrate"),
		)
	}
	var moveErr error
	if check.State != FoldersConflict {
		release, held, err := operation.TryLock(paths.Runtime.State, paths.Runtime.MigrateLock())
		if err != nil {
			return paths, err
		}
		if held {
			return paths, busy("another migration is running")
		}
		_, moveErr = moveFolders(ctx, old, paths)
		release()
	}
	// Once the ledger has moved, nothing else the move left stops the import.
	if after := CheckFolders(old, paths); after.Ledger != FoldersDone {
		// The move's own error says why the ledger didn't move, or names a
		// copy that differs from its set-aside original, which the check
		// can't tell from a stopped copy.
		if moveErr != nil {
			return paths, moveErr
		}
		return paths, conflictError(after)
	}
	if moveErr != nil {
		writeLog(paths, "the ledger moved, but not every costs file: "+moveErr.Error())
	}
	return paths, nil
}
