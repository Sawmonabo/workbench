package costs

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"syscall"

	"github.com/Sawmonabo/workbench/internal/display"
	"github.com/Sawmonabo/workbench/internal/operation"
	"github.com/Sawmonabo/workbench/internal/sqlitefile"
)

// OldPaths are where releases before the migrations design kept the costs
// ledger, its price caches and the import log and lock: CLAUDE_COSTS_LEDGER
// and CLAUDE_COSTS_STATE when set, else claude-costs under XDG_DATA_HOME and
// XDG_STATE_HOME when set, else under ~/.local/share and ~/.local/state, on
// macOS too. Only the costs migration reads them. A path is "" when a
// relative value named it: that resolved against whatever folder the old
// process started in and can't be found again. Notes says which, and
// Unresolved names the variable that hides the ledger's own path.
type OldPaths struct {
	Ledger, State string
	Notes         []string
	Unresolved    string
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
		return "", fmt.Sprintf("%s is relative (%s)", name, display.PathFrom(home, value))
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
			old.Notes = append(old.Notes, relative+": the costs files it names can't be found")
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
	return old
}

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

// costsMove is one price cache the migration moves beside the ledger, into
// the folder that folder makes private. A cache is derived data: when the new
// path already holds one, the old one is deleted instead of being a conflict.
type costsMove struct {
	from, to string
	folder   func() (string, error)
}

func (o OldPaths) moves(p Paths) []costsMove {
	var moves []costsMove
	if o.Ledger != "" {
		for _, tool := range Tools {
			if tool.Source != nil {
				moves = append(moves, costsMove{
					oldOfficialPath(filepath.Dir(o.Ledger), tool), officialPath(p.Ledger, tool),
					func() (string, error) { return p.dataFolder(tool.Name) },
				})
			}
		}
	}
	return slices.DeleteFunc(moves, func(m costsMove) bool { return aliased(m.from, m.to) })
}

// PriceListsMoving are the paths beside the ledger at p that [MoveFolders]
// moves a price list of the old claude-costs folders to: each whose old file
// is there, not a link, and whose new path is free. It looks at paths only.
func PriceListsMoving(old OldPaths, p Paths) []string {
	var lists []string
	for _, move := range old.apart(p).moves(p) {
		if exists(move.from) && !linked(move.from) && !exists(move.to) {
			lists = append(lists, move.to)
		}
	}
	return lists
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
		names = append(names, Paths{State: o.State}.log(), Paths{State: o.State}.lock())
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
// an owner set CLAUDE_COSTS_LEDGER or CLAUDE_COSTS_STATE to the new file or
// folder, or linked an old claude-costs folder to the new one. Such a file is
// already where it belongs; nothing moves, and nothing is deleted or locked
// through it.
func (o OldPaths) apart(p Paths) OldPaths {
	if o.Ledger != "" && aliased(o.Ledger, p.Ledger) {
		o.Ledger = ""
	}
	if o.State != "" && aliased(o.State, p.State) {
		o.State = ""
	}
	return o
}

// folders are the old claude-costs folders, each once.
func (o OldPaths) folders() []string {
	var folders []string
	for _, folder := range []string{filepath.Dir(o.Ledger), o.State} {
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
		names = append(names, o.Ledger, o.Ledger+".moved", o.Ledger+"-journal")
		names = append(names, sqlitefile.SideFiles(o.Ledger)...)
	}
	for _, move := range o.moves(p) {
		names = append(names, move.from)
	}
	return names
}

// folderScan is what the old claude-costs folders hold, home-relative: empty
// are real folders with nothing in them, unreadable says of each one whose
// listing failed why (see [operation.Unread]), and kept the files the
// migration doesn't know. Workbench didn't write those,
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
		shown := display.PathFrom(p.Home, folder)
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
			scan.unreadable = append(scan.unreadable, operation.Unread(p.Home, folder, err))
		case len(entries) == 0:
			scan.empty = append(scan.empty, shown)
		}
		for _, entry := range entries {
			if path := filepath.Join(folder, entry.Name()); !slices.Contains(known, path) {
				scan.kept = append(scan.kept, display.PathFrom(p.Home, path))
			}
		}
	}
	return scan
}
