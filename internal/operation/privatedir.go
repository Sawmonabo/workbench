package operation

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"

	"github.com/Sawmonabo/workbench/internal/display"
	"github.com/Sawmonabo/workbench/internal/platform"
)

func ensurePrivateDirectory(path string) error { return privateDirectory(path, true) }

// privateDirectory requires the folder at path to be private: no link and
// no file from it up, nothing above it that another user owns or may write
// (see safeParents), on a file system that keeps it private, and a folder of
// Workbench's own files that others cannot write in (see [trustedFolder]).
// With create it first makes it, 0700, when it is missing, then sets it to
// 0700 when it is not (see [makeOwnFolder]); without, it uses the folder as
// it is but for one its owner may not read or search, which gets 0700 (see
// [ownFolder]), and a missing folder fails with an error matching
// fs.ErrNotExist.
func privateDirectory(path string, create bool) error {
	if err := safeOwnFolder("permissions", path); err != nil {
		return err
	}
	if err := privateFilesystem(path); err != nil {
		return err
	}
	if create {
		if err := os.MkdirAll(path, 0o700); err != nil {
			return err
		}
		return makeOwnFolder("permissions", path)
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	return ownFolder("permissions", path, info)
}

// trustedFolder refuses path, a folder that holds Workbench's own files and
// that info describes, unless it is a folder, this user's, and one neither its
// group nor other users may write in, by its mode or by its ACL (see
// [platform.OthersMayWrite], which reads it through fd and at as it says):
// what others can write in may not be Workbench's, so no command changes it
// or uses it. category is the refusal's when path is not a folder.
func trustedFolder(category, path string, info os.FileInfo, fd int, at string) error {
	if !info.IsDir() {
		return notFolderRefusal(category, path, info)
	}
	if err := owned(path, info); err != nil {
		return err
	}
	if info.Mode().Perm()&0o022 != 0 {
		return othersWriteRefusal(
			path,
			fmt.Sprintf("%s has mode %04o", display.Path(path), platform.ModeBits(info.Mode())),
			"other users can write in it, so what it holds may not be Workbench's",
			"chmod",
			"700",
		)
	}
	entry, err := platform.OthersMayWrite(fd, at)
	if err != nil {
		return Fail(ExitInvalid, "permissions", "Cannot read the ACL of "+display.Path(path),
			Cause(err.Error()))
	}
	if entry >= 0 {
		return othersWriteRefusal(
			path,
			display.Path(path)+" has an ACL that lets other users write in it",
			"what it holds may not be Workbench's",
			platform.ACLRemoval()...,
		)
	}
	return nil
}

// ownFolder is [trustedFolder] for a command that only reads or previews the
// folder at path, which info describes: one whose owner may read and search
// it is used as it is, whatever its mode (0755 stays 0755), and one whose
// owner may not is given 0700 so that it can be read (see [settleFolder]),
// the one change such a command makes.
func ownFolder(category, path string, info os.FileInfo) error {
	if err := trustedFolder(category, path, info, -1, path); err != nil {
		return err
	}
	if readable(info.Mode()) {
		return nil
	}
	return settleFolder(category, path, false)
}

// makeOwnFolder is [settleFolder] for a command that writes in the folder at
// path, which gets 0700 whatever its mode.
func makeOwnFolder(category, path string) error { return settleFolder(category, path, true) }

// settleFolder is the one rule for the mode of a folder of Workbench's own
// files: it applies [trustedFolder] to the folder at path, and then gives it
// 0700, keeping its setuid, setgid and sticky bits (see [ownMode]), when
// write and its mode is another, or, without write, when its owner may not
// read or search it. It checks and changes one descriptor, opened without
// following a link and without needing to read the folder (see
// [platform.OpenFolder]), so the folder it checks is the folder it changes;
// one macOS opens no descriptor for, as its owner may neither read nor search
// it, it judges and changes by its name in the folder above (see
// [settleByName]). A link or a file there is refused as trustedFolder refuses
// it. It prints nothing: Workbench gives its own folders the mode it makes
// them with.
func settleFolder(category, path string, write bool) error {
	folder, err := platform.OpenFolder(path)
	if errors.Is(err, fs.ErrPermission) {
		folder, err = settleByName(category, path, err)
	}
	if err != nil {
		if info, statErr := os.Lstat(path); statErr == nil {
			if refusal := trustedFolder(category, path, info, -1, path); refusal != nil {
				return refusal
			}
		}
		return err
	}
	defer func() { _ = folder.Close() }()
	info, err := folder.Stat()
	if err != nil {
		return err
	}
	if err = trustedFolder(category, path, info, int(folder.Fd()), ""); err != nil {
		return err
	}
	if info.Mode().Perm() == 0o700 || !write && readable(info.Mode()) {
		return nil
	}
	if err = platform.ChmodFolder(folder, ownMode(info.Mode())); err != nil {
		return modeRefusal(path, err)
	}
	return nil
}

// settleByName gives the folder at path 0700 by its name in the folder above
// (see [platform.ChmodFolderAt]) when its owner may neither read nor search
// it, so that OpenFolder failed with openErr: it judges it there first, by
// [trustedFolder] with its ACL read in that folder, and returns a descriptor
// of it opened after the change, refusing one that is not the folder it
// judged. Only this user and root may put another entry at its name in a
// folder [safeParents] or trustedFolder accepted, and chmod changes nothing
// another user owns, so a swap between the judgement and the change can at
// most give 0700 to another entry of this user's, which the descriptor shows.
// A folder of another mode keeps openErr.
func settleByName(category, path string, openErr error) (*os.File, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() || info.Mode().Perm()&0o500 != 0 {
		return nil, openErr
	}
	parent, err := platform.OpenFolder(filepath.Dir(path))
	if err != nil {
		return nil, err
	}
	defer func() { _ = parent.Close() }()
	name := filepath.Base(path)
	if err = trustedFolder(category, path, info, int(parent.Fd()), name); err != nil {
		return nil, err
	}
	folder, err := platform.ChmodFolderAt(parent, name, info, ownMode(info.Mode()))
	if errors.Is(err, platform.ErrFolderReplaced) {
		return nil, replacedRefusal(path)
	}
	if err != nil {
		return nil, modeRefusal(path, err)
	}
	if changed, err := folder.Stat(); err != nil || !os.SameFile(info, changed) {
		_ = folder.Close()
		if err != nil {
			return nil, err
		}
		return nil, replacedRefusal(path)
	}
	return folder, nil
}

// readable reports whether mode lets its owner read and search a folder.
func readable(mode os.FileMode) bool { return mode.Perm()&0o500 == 0o500 }

// ownMode is 0700 with mode's setuid, setgid and sticky bits, the mode
// [settleFolder] gives a folder of Workbench's own files.
func ownMode(mode os.FileMode) os.FileMode {
	return mode&(os.ModeSetuid|os.ModeSetgid|os.ModeSticky) | 0o700
}

// safeOwnFolder is safeParents for path, a folder that holds Workbench's own
// files: a folder of this user's there is judged by [trustedFolder] first, as
// safeParents judges path before the folders above it, so one that others can
// write in is refused in trustedFolder's words. A link, a file or another
// user's folder there is left to safeParents and the checks after it.
func safeOwnFolder(category, path string) error {
	if info, err := os.Lstat(path); err == nil && info.IsDir() && ownedByUser(info) {
		if err = trustedFolder(category, path, info, -1, path); err != nil {
			return err
		}
	}
	return safeParents(path)
}

// ensureRoot creates a missing runtime root 0700. [makeParents] sets the modes
// of its missing parents.
func ensureRoot(root string) error {
	if err := safeOwnFolder("permissions", root); err != nil {
		return err
	}
	if err := privateFilesystem(root); err != nil {
		return err
	}
	if err := makeParents(root); err != nil {
		return err
	}
	return ensurePrivateDirectory(root)
}

// PrivateDirectory makes root and each folder of name below it private (0700,
// owned by the user, no unsafe parents) and returns the deepest one's path.
// name may hold slashes for a nested folder; an empty name is root itself. It
// is for writers that keep their own files in Workbench's folders outside a
// [Mutation].
func PrivateDirectory(root, name string) (string, error) {
	if name != "" && !filepath.IsLocal(name) {
		return "", Fail(
			ExitInvalid,
			"private_path",
			name+" is not a folder inside "+display.Path(root),
		)
	}
	if err := ensureRoot(root); err != nil {
		return "", err
	}
	directory := root
	if name == "" {
		return directory, nil
	}
	for part := range strings.SplitSeq(filepath.ToSlash(filepath.Clean(name)), "/") {
		directory = filepath.Join(directory, part)
		if err := ensurePrivateDirectory(directory); err != nil {
			return "", err
		}
	}
	return directory, nil
}

// CheckPrivateDirectory is [PrivateDirectory] making nothing, for an entry
// that must not make a folder where it looks, or only reads: root and each
// folder of name below it, from root down to the first one that is missing,
// must pass PrivateDirectory's checks, and one that does not fails it with
// PrivateDirectory's words; a folder that passes keeps its mode, but for one
// its owner may not read or search, which gets 0700 before the folders below
// it are checked (see [ownFolder]). A missing folder ends the check with no
// error,
// since it holds nothing; a caller that needs it fails when it uses it.
// [PrivateTree] checks without creating too, but in other words, and fails
// on a missing folder.
func CheckPrivateDirectory(root, name string) error {
	if name != "" && !filepath.IsLocal(name) {
		return Fail(ExitInvalid, "private_path", name+" is not a folder inside "+display.Path(root))
	}
	directory := root
	folders := []string{root}
	if name != "" {
		for part := range strings.SplitSeq(filepath.ToSlash(filepath.Clean(name)), "/") {
			directory = filepath.Join(directory, part)
			folders = append(folders, directory)
		}
	}
	for _, folder := range folders {
		err := privateDirectory(folder, false)
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// PrivateTree rejects redirected, borrowed or permissive private trees: no
// folder from target up may be a link or a file, and each one within root
// must be a folder of Workbench's own files that others cannot write in (see
// [trustedFolder]), each that it can see judged before any is changed. It
// then goes from root down to target, so a folder whose owner may not search
// it is opened to the folders below it before they are judged: with create
// it sets each that is not 0700 to 0700 (see [makeOwnFolder]) and makes the
// missing folders, 0700; without, it uses each as it is but for one its owner
// may not read or search, which gets 0700 (see [ownFolder]), and a missing
// one fails. It never follows a link. Unlike [PrivateDirectory] it can check
// without creating.
func PrivateTree(root, target string, create bool) error {
	if !filepath.IsAbs(root) || !Within(root, target) {
		return Fail(
			ExitInvalid,
			"private_path",
			"Private target escapes the selected runtime directory",
		)
	}
	var own []string
	for current := target; ; current = filepath.Dir(current) {
		info, err := os.Lstat(current)
		switch {
		case err == nil:
			if err = treeFolder(root, current, info); err != nil {
				return err
			}
		case errors.Is(err, fs.ErrPermission) && current != root && Within(root, current):
			// A folder above it that its owner may not search hides it, and
			// it is judged below once that folder can be searched.
		case !errors.Is(err, fs.ErrNotExist):
			return err
		case !create:
			return Fail(ExitBlocked, "private_path", display.Path(current)+" does not exist")
		}
		if Within(root, current) {
			own = append(own, current)
		}
		if current == filepath.Dir(current) {
			break
		}
	}
	for _, folder := range slices.Backward(own) {
		info, err := os.Lstat(folder)
		switch {
		case errors.Is(err, fs.ErrNotExist) && create:
		case errors.Is(err, fs.ErrNotExist):
			return Fail(ExitBlocked, "private_path", display.Path(folder)+" does not exist")
		case err != nil:
			return err
		case create:
			err = makeOwnFolder("private_path", folder)
		default:
			err = ownFolder("private_path", folder, info)
		}
		if errors.Is(err, fs.ErrNotExist) && create {
			break // made below
		}
		if err != nil {
			return err
		}
	}
	if !create {
		return nil
	}
	if err := makeParents(root); err != nil {
		return err
	}
	return os.MkdirAll(target, 0o700)
}

// treeFolder refuses current, a folder at or above a [PrivateTree] target
// that info describes, unless it is a folder and, within root, a folder of
// Workbench's own files as [trustedFolder] requires.
func treeFolder(root, current string, info os.FileInfo) error {
	if !Within(root, current) {
		if !info.IsDir() {
			return notFolderRefusal("private_path", current, info)
		}
		return nil
	}
	return trustedFolder("private_path", current, info, -1, current)
}

// makeParents creates the missing folders above a runtime root. They get 0755,
// the mode the machine configuration gives its own folders such as ~/.local on
// Linux, so applying it later changes nothing for a folder that holds Workbench
// state. The exception is the XDG configuration home (~/.config): the
// specification asks for 0700 and the configuration keeps it there.
func makeParents(root string) error {
	parent := filepath.Dir(root)
	if config := configHome(); config != "" && Within(config, parent) {
		if _, err := os.Lstat(config); errors.Is(err, fs.ErrNotExist) {
			if err = os.MkdirAll(filepath.Dir(config), 0o755); err != nil {
				return err
			}
			if err = os.Mkdir(config, 0o700); err != nil && !os.IsExist(err) {
				return err
			}
		}
	}
	return os.MkdirAll(parent, 0o755)
}

// RemovePrivateDirectory deletes directory, a runtime directory below the data
// root that the approved plan lists for removal: an older release, application
// context or private tool version. It is a no-op once directory is gone.
func (m *Mutation) RemovePrivateDirectory(directory string) error {
	if err := m.Check(); err != nil {
		return err
	}
	data := m.context.Paths.Data
	if filepath.Clean(directory) != directory || directory == data || !Within(data, directory) {
		return Fail(
			ExitInvalid,
			"private_path",
			"Only directories below Workbench data are removed",
		)
	}
	info, err := os.Lstat(directory)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return Fail(ExitInvalid, "private_path", "Only private directories are removed")
	}
	id, err := NewID()
	if err != nil {
		return err
	}
	return removeDirectory(directory, filepath.Dir(directory), ".removed-"+id)
}

// removeDirectory moves directory into trash as name and syncs directory's
// parent, so the removal is durable before any file is deleted, then deletes
// the moved tree.
func removeDirectory(directory, trash, name string) error {
	removed := filepath.Join(trash, name)
	if err := os.Rename(directory, removed); err != nil {
		return err
	}
	if err := SyncDirectory(filepath.Dir(directory)); err != nil {
		return err
	}
	return os.RemoveAll(removed)
}

// safeParents rejects path, and each folder above it, that would let
// another user redirect or replace what Workbench writes there: a link, a
// file above path, a folder another user than this one or root owns, and a
// folder its group or other users may write in, by its mode (unless sticky,
// so only an entry's owner may remove it) or by its ACL (see
// [platform.OthersMayWrite]).
func safeParents(path string) error {
	for current := filepath.Clean(path); ; current = filepath.Dir(current) {
		info, err := os.Lstat(current)
		if err == nil {
			if err = safeParent(path, current, info); err != nil {
				return err
			}
		} else if !errors.Is(err, fs.ErrNotExist) {
			return Fail(ExitInvalid, "path", FileError(current, err).Error())
		}
		if current == filepath.Dir(current) {
			return nil
		}
	}
}

// safeParent is [safeParents] for current, path or a folder above it, that
// info describes.
func safeParent(path, current string, info os.FileInfo) error {
	shown := display.Path(current)
	switch {
	case info.Mode()&os.ModeSymlink != 0 && current == path:
		return Fail(ExitInvalid, "path", shown+" is a link",
			Cause("Workbench keeps its own files and folders as real ones"),
			FixText("put what the link points to in its place"))
	case info.Mode()&os.ModeSymlink != 0:
		return Fail(ExitInvalid, "path", shown+" is a link",
			Cause("Workbench keeps its own files only in real folders"),
			FixText("make it a folder"))
	case current != path && !info.IsDir():
		return Fail(ExitInvalid, "path", shown+" is a file, not a folder",
			Cause("Workbench cannot keep its files under it"))
	case !ownedByUserOrRoot(info):
		return Fail(ExitInvalid, "permissions", shown+" belongs to another user",
			Cause("Workbench keeps its files only under folders you or root own"))
	case info.Mode().Perm()&0o022 != 0 && info.Mode()&os.ModeSticky == 0:
		return Fail(ExitInvalid, "permissions", shown+" is writable by other users",
			FixPath(current, "chmod", "go-w"))
	case !info.IsDir():
		return nil
	}
	// Only the entry is removed, not the whole ACL, which may hold entries
	// the system put there, such as the deny delete of the home folder.
	entry, err := platform.OthersMayWrite(-1, current)
	switch {
	case err != nil:
		return Fail(ExitInvalid, "permissions", "Cannot read the ACL of "+shown, Cause(err.Error()))
	case entry >= 0:
		return Fail(
			ExitInvalid,
			"permissions",
			shown+" has an ACL that lets other users write in it",
			FixPath(current, platform.ACLEntryRemoval(entry)...),
		)
	}
	return nil
}

func owned(path string, info os.FileInfo) error {
	if ownedByUser(info) {
		return nil
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return Fail(ExitInvalid, "permissions", "Cannot tell who owns "+display.Path(path))
	}
	return Fail(
		ExitInvalid,
		"permissions",
		display.Path(path)+" belongs to "+userName(stat.Uid),
		Cause("Workbench writes only what you own"),
		FixPath(path, "sudo", "chown", userName(uint32(os.Geteuid()))),
	)
}

// ownedByUser reports whether info is of a file the current user owns.
func ownedByUser(info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && int(stat.Uid) == os.Geteuid()
}

// ownedByUserOrRoot reports whether info is of a file the current user or
// root owns.
func ownedByUserOrRoot(info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && (stat.Uid == 0 || int(stat.Uid) == os.Geteuid())
}

// notFolderRefusal refuses path, which info describes and which is no
// folder, where Workbench keeps its own files; category is the refusal's.
func notFolderRefusal(category, path string, info os.FileInfo) error {
	return Fail(
		ExitInvalid,
		category,
		display.Path(path)+" is "+fileKind(info)+", not a folder",
		Cause("Workbench keeps its own files in a folder there"),
		FixText("move it aside"),
	)
}

// othersWriteRefusal refuses path, a folder of Workbench's own files that its
// group or other users may write in, as headline and cause say. What it holds
// may not be Workbench's, so the command of words that ends that, typed
// alone, would keep in it what another user put there: the fix first moves
// aside what is not this user's, as find lists it, then runs that command on
// path.
func othersWriteRefusal(path, headline, cause string, words ...string) error {
	return Fail(
		ExitInvalid,
		"permissions",
		headline,
		Cause(cause),
		FixText("move aside what find "+shellHomePath(path)+" -not -user "+
			shellWord(userName(uint32(os.Geteuid())))+" lists"),
		FixPath(path, words...),
	)
}

// modeRefusal refuses path, a folder of Workbench's own files whose mode
// [settleFolder] could not change, as err says: a file system mounted read
// only, or a flag that locks the folder.
func modeRefusal(path string, err error) error {
	return Fail(
		ExitInvalid,
		"permissions",
		"Cannot give "+display.Path(path)+" mode 0700",
		Cause(FileReason(path, err).Error()),
	)
}

// replacedRefusal refuses path, a folder of Workbench's own files that
// another entry took the place of while [settleFolder] judged and changed it.
func replacedRefusal(path string) error {
	return Fail(
		ExitInvalid,
		"permissions",
		display.Path(path)+" was replaced while Workbench checked it",
	)
}
