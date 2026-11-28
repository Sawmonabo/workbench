package operation

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"syscall"

	"github.com/Sawmonabo/workbench/internal/display"
	"github.com/Sawmonabo/workbench/internal/platform"
)

// FindExecutable ignores empty/relative PATH entries and project-local shims.
// This discovers a location only; callers must verify version/capabilities.
func FindExecutable(name, searchPath string, excluded []string) (string, error) {
	if filepath.Base(name) != name || name == "." || name == "" {
		return "", Fail(ExitInvalid, "tool", "Tool discovery requires a command basename")
	}
	for _, directory := range filepath.SplitList(searchPath) {
		if !filepath.IsAbs(directory) {
			continue
		}
		path, err := trustedExecutable(filepath.Join(directory, name), excluded, nil)
		if err == nil {
			return path, nil
		}
	}
	return "", Fail(
		ExitBlocked,
		"dependency",
		"No "+shellWord(name)+" on PATH outside project folders"+
			" is a program that other users cannot change",
	)
}

// trustedExecutable is the program path names, its links resolved, when
// Workbench runs it: outside the folders of excluded projects, and a program
// as [programRefusal] says, of this user when path or the program is in one
// of own, Workbench's own folders (see [ownProgramRefusal]).
func trustedExecutable(path string, excluded, own []string) (string, error) {
	if !filepath.IsAbs(path) {
		return "", Fail(ExitInvalid, "tool", "Subprocess executable must be absolute")
	}
	canonical, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", Fail(
			ExitBlocked,
			"dependency",
			"Cannot find the program "+display.Path(path),
			Cause(FileReason(path, err).Error()),
		)
	}
	for _, candidate := range []string{path, canonical} {
		if err := outsideProjects(filepath.Dir(candidate), excluded); err != nil {
			return "", err
		}
	}
	info, err := os.Stat(canonical)
	if err != nil {
		return "", Fail(
			ExitBlocked,
			"tool",
			"Cannot read the program "+display.Path(canonical),
			Cause(FileReason(canonical, err).Error()),
		)
	}
	if err := programRefusal(canonical, info); err != nil {
		return "", err
	}
	for _, folder := range own {
		if Within(folder, path) || Within(folder, canonical) {
			if err := ownProgramRefusal(path, canonical, folder, info); err != nil {
				return "", err
			}
			break
		}
	}
	return canonical, nil
}

// ownProgramRefusal refuses the program canonical, which info describes and
// path names, when path or canonical is in folder, one of Workbench's own,
// and the program is not this user's: Workbench installs what it runs from
// its folders as this user, and init refuses root, so a program there of
// another user, root included, or a link there to one, is not Workbench's,
// and running it would run that user's code as this one. The fix moves aside
// what is in folder, the link or the program, so the tool is installed again.
func ownProgramRefusal(path, canonical, folder string, info os.FileInfo) error {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if ok && int(stat.Uid) == os.Geteuid() {
		return nil
	}
	owner := "another user"
	if ok {
		owner = userName(stat.Uid)
	}
	moved := canonical
	if Within(folder, path) {
		moved = path
	}
	return Fail(
		ExitBlocked,
		"tool",
		display.Path(canonical)+" belongs to "+owner+", not to you",
		Cause("Workbench runs a program in its own folders only when it is yours"),
		moveAsideFix(moved),
	)
}

// CheckToolFolder refuses folder, the folder of one version of a tool in
// root, Workbench's data folder (tools/NAME/VERSION), when what it holds could
// run another user's code as this user: Workbench runs the tool's program,
// and the program reads what lies beside it (python3 imports its library and
// runs the .pth files of its site-packages). Each entry below folder, read
// with lstat and never followed, must be this user's, and no file or folder
// there may be one other users can change, by its mode or by an ACL entry
// (see [platform.OthersMayWrite]). Workbench installs every tool as this user,
// so another user's entry is not Workbench's, and a file others can change
// while they can reach it (each folder from root down to it lets another
// user search it, see [othersReach]) may hold their code already: either is
// refused with the move of the whole folder aside, then install, the lines
// the caller gives, since what else of it they changed cannot be told. What
// others can change but cannot reach is refused with the commands that close
// it to them. A folder of it that its owner may not read or search gets
// 0700 first (see [settleFolder]). folder itself and the folders above it
// are the caller's to judge (see [CheckPrivateDirectory]). A missing folder
// holds nothing to refuse.
func CheckToolFolder(root, folder string, install ...Part) error {
	reach := map[string]bool{folder: othersReach(root, folder)}
	var exposed, closable *toolEntry
	err := filepath.WalkDir(folder, func(path string, entry fs.DirEntry, walkErr error) error {
		switch {
		case walkErr != nil && path == folder && errors.Is(walkErr, fs.ErrNotExist):
			return filepath.SkipAll
		case walkErr != nil:
			return toolUnreadRefusal(path, entry != nil && entry.IsDir(), walkErr)
		case path == folder:
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return toolUnreadRefusal(path, entry.IsDir(), err)
		}
		if !ownedByUser(info) {
			return foreignToolRefusal(folder, path, info, install)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return nil
		}
		found, err := othersChange(path, info)
		if err != nil {
			return err
		}
		if info.IsDir() {
			reach[path] = reach[filepath.Dir(path)] && othersSearch(path, info)
			// A folder of the tool its owner may not read or search gets
			// 0700, the mode Workbench installs its folders with, as each
			// folder of Workbench's own files does, so that the walk reads
			// it; one others may change is refused below instead.
			if found == nil && info.Mode().Perm()&0o500 != 0o500 {
				if err = settleFolder("tool", path, false); err != nil {
					return err
				}
			}
		}
		switch {
		case found == nil:
		case !info.IsDir() && reach[filepath.Dir(path)]:
			if exposed == nil {
				exposed = found
			}
		case closable == nil:
			closable = found
		}
		return nil
	})
	switch {
	case err != nil:
		return err
	case exposed != nil:
		return Fail(ExitBlocked, "tool", exposed.headline,
			Cause("another user may have changed it, so Workbench does not run this tool"),
			moveToolAside(folder, install))
	case closable != nil:
		var fixes fixLines
		if closable.acl {
			removal := platform.ACLRemoval()
			fixes = append(
				fixes,
				FixPath(folder, append([]string{removal[0], "-R"}, removal[1:]...)...),
			)
		}
		return Fail(ExitBlocked, "tool", closable.headline,
			Cause("Workbench runs a tool only from files and folders no other user can change"),
			append(fixes, FixPath(folder, "chmod", "-R", "go-w")))
	}
	return nil
}

// toolEntry is a file or folder of a tool's folder that other users may
// change: the headline that says so, and whether its ACL is why.
type toolEntry struct {
	headline string
	acl      bool
}

// othersChange is the [toolEntry] of path, which info describes, when other
// users may change it, by its mode or its ACL, or nil when they may not.
func othersChange(path string, info os.FileInfo) (*toolEntry, error) {
	if mode := info.Mode(); mode.Perm()&0o022 != 0 {
		return &toolEntry{headline: fmt.Sprintf("%s can be changed by other users (mode %04o)",
			display.Path(path), platform.ModeBits(mode))}, nil
	}
	entry, err := platform.OthersMayWrite(-1, path)
	switch {
	case err != nil:
		return nil, Fail(ExitBlocked, "tool", "Cannot read the ACL of "+display.Path(path),
			Cause(err.Error()), FixPath(path, platform.ACLRemoval()...))
	case entry >= 0:
		return &toolEntry{
			headline: display.Path(path) + " has an ACL that lets other users change it",
			acl:      true,
		}, nil
	}
	return nil, nil
}

// othersSearch reports whether a user other than this one may search the
// folder at path, which info describes, by its mode or its ACL (see
// [platform.OthersMaySearch]); an ACL it cannot read counts as one that lets
// them.
func othersSearch(path string, info os.FileInfo) bool {
	if info.Mode().Perm()&0o011 != 0 {
		return true
	}
	entry, err := platform.OthersMaySearch(-1, path)
	return err != nil || entry >= 0
}

// othersReach reports whether a user other than this one may reach folder:
// whether they may search each folder from root down to it (see
// [othersSearch]). One that is missing reaches nothing.
func othersReach(root, folder string) bool {
	for current := folder; ; current = filepath.Dir(current) {
		info, err := os.Lstat(current)
		if err != nil || !othersSearch(current, info) {
			return false
		}
		if current == root || current == filepath.Dir(current) {
			return true
		}
	}
}

// foreignToolRefusal refuses the tool at folder for path, an entry in it of
// another user, which info describes.
func foreignToolRefusal(folder, path string, info os.FileInfo, install []Part) error {
	owner := "another user"
	if stat, ok := info.Sys().(*syscall.Stat_t); ok {
		owner = userName(stat.Uid)
	}
	return Fail(ExitBlocked, "tool", display.Path(path)+" belongs to "+owner+", not to you",
		Cause("Workbench runs a tool only when every file of it is yours"),
		moveToolAside(folder, install))
}

// moveToolAside is the fix lines of a tool at folder that may hold another
// user's code: the move of the whole folder aside, to a name beside it, then
// install, the lines that install the tool again.
func moveToolAside(folder string, install []Part) Part {
	return append(
		fixLines{moveAsideFix(folder)},
		install...)
}

// toolUnreadRefusal refuses a tool for path in its folder, a folder when
// folder is set, which it failed with err to read (see [unreachable]).
func toolUnreadRefusal(path string, folder bool, err error) error {
	what := ""
	if folder {
		what = "the folder "
	}
	parts := unreachable(path, folder, err)
	if parts == nil {
		parts = []Part{Cause(FileReason(path, err).Error())}
	}
	return Fail(ExitBlocked, "tool", "Cannot read "+what+display.Path(path), parts...)
}

// programRefusal is why Workbench does not run the program at path, which
// info describes, or nil when it does: it runs only a file marked
// executable that no user but its owner can change.
func programRefusal(path string, info os.FileInfo) error {
	mode := info.Mode().Perm()
	sudo := []string{}
	if !ownedByUser(info) {
		sudo = []string{"sudo"}
	}
	switch {
	case !info.Mode().IsRegular():
		return Fail(ExitBlocked, "tool",
			display.Path(path)+" is "+fileKind(info)+", not a program Workbench can run")
	case mode&0o111 == 0:
		return Fail(
			ExitBlocked,
			"tool",
			fmt.Sprintf(
				"%s is not marked executable (mode %04o)",
				display.Path(path),
				uint32(mode),
			),
			FixPath(path, append(sudo, "chmod", "+x")...),
		)
	case mode&0o022 != 0:
		return Fail(
			ExitBlocked,
			"tool",
			fmt.Sprintf(
				"%s can be changed by other users (mode %04o)",
				display.Path(path),
				uint32(mode),
			),
			Cause("Workbench runs only a program no other user can change"),
			FixPath(path, append(sudo, "chmod", "go-w")...),
		)
	}
	return nil
}

func outsideProjects(directory string, excluded []string) error {
	for _, root := range excluded {
		if root != "" && Within(root, directory) {
			return Fail(
				ExitBlocked,
				"tool",
				display.Path(directory)+" is in the project "+display.Path(root)+
					", and Workbench does not run a project's own programs",
			)
		}
	}
	homes := userHomes()
	for parent := directory; ; parent = filepath.Dir(parent) {
		// A marker in the home directory itself (a stray ~/package.json or a
		// tracked home) does not make Workbench's own directories and the
		// user's home-level tool directories project-controlled.
		homeLevel := slices.Contains(homes, parent) && homeToolDirectory(directory, homes)
		for _, marker := range projectMarkers {
			if homeLevel {
				break
			}
			if _, err := os.Lstat(filepath.Join(parent, marker)); err == nil {
				if marker == ".git" && homebrewRepository(parent, directory) {
					continue
				}
				return Fail(
					ExitBlocked,
					"tool",
					display.Path(directory)+" is in a project",
					Cause(display.Path(parent)+" holds "+marker+
						", and Workbench does not run a project's own programs"),
				)
			} else if !errors.Is(err, fs.ErrNotExist) {
				return Fail(
					ExitBlocked,
					"tool",
					"Cannot tell whether "+display.Path(directory)+" is in a project",
					Cause("cannot read "+FileError(filepath.Join(parent, marker), err).Error()),
				)
			}
		}
		if parent == filepath.Dir(parent) {
			return nil
		}
	}
}

// userHomes returns the home directory as configured and with symlinks resolved.
func userHomes() []string {
	home, err := os.UserHomeDir()
	if err != nil || !filepath.IsAbs(home) {
		return nil
	}
	homes := []string{filepath.Clean(home)}
	if canonical, err := filepath.EvalSymlinks(home); err == nil && canonical != homes[0] {
		homes = append(homes, canonical)
	}
	return homes
}

// homeToolDirectory reports whether directory lies in Workbench's own default
// runtime locations or a conventional per-user tool directory under home.
// Anything else under home (node_modules, a project's bin, a venv) stays
// subject to the marker scan.
func homeToolDirectory(directory string, homes []string) bool {
	for _, name := range []string{"CONFIG", "DATA", "STATE", "CACHE", "BIN"} {
		// Overrides are used verbatim by runtimePaths, so honor them the same way.
		root := os.Getenv("WORKBENCH_" + name + "_DIR")
		if filepath.IsAbs(root) && slices.Contains(homes, filepath.Dir(filepath.Clean(root))) &&
			Within(filepath.Clean(root), directory) {
			return true
		}
	}
	for _, home := range homes {
		for _, root := range []string{
			filepath.Join(home, ".local"),
			filepath.Join(home, ".cache", "workbench"),
			filepath.Join(home, ".config", "workbench"),
			filepath.Join(home, ".cargo", "bin"),
			filepath.Join(home, "go", "bin"),
			filepath.Join(home, "Library", "Application Support", "workbench"),
			filepath.Join(home, "Library", "Caches", "workbench"),
		} {
			if Within(root, directory) {
				return true
			}
		}
	}
	return false
}

// Homebrew itself is a Git checkout at its standard installation locations.
// Recognize its installed-tool directories, not arbitrary repositories with a
// Homebrew-shaped name. Explicit project exclusions and nested markers still win.
func homebrewRepository(repository, directory string) bool {
	if !slices.Contains(platform.HomebrewRepositories, repository) {
		return false
	}
	installed := false
	for _, name := range []string{"bin", "sbin", "Cellar", "opt"} {
		if Within(filepath.Join(repository, name), directory) {
			installed = true
			break
		}
	}
	if !installed {
		return false
	}
	for _, name := range []string{"bin/brew", "Library/Homebrew/brew.sh"} {
		info, err := os.Lstat(filepath.Join(repository, name))
		if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o022 != 0 {
			return false
		}
		if !ownedByUserOrRoot(info) {
			return false
		}
	}
	return true
}
