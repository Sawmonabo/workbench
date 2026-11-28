package operation

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/Sawmonabo/workbench/internal/display"
	"golang.org/x/sys/unix"
)

func ensurePrivateDirectory(path string) error { return privateDirectory(path, true) }

// privateDirectory requires the folder at path to be private: no link and
// no file from it up, nothing above it that another user owns or may write
// (see safeParents), on a file system that keeps it private, and a folder of
// Workbench's own files as [ownFolder] requires. With create it first makes
// it, 0700, when it is missing, then sets it to 0700 when it is not (see
// [makeOwnFolder]); without, it changes nothing, and a missing folder fails
// with an error matching fs.ErrNotExist.
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

// ownFolder refuses path, a folder that holds Workbench's own files and that
// info describes, unless it is a folder, this user's, one its owner may read
// and search, and one neither its group nor other users may write in: what
// others can write in may not be Workbench's. Any other mode passes, so a
// check that only reads uses such a folder as it is and a write sets it to
// 0700 (see [makeOwnFolder]). category is the refusal's when path is not a
// folder.
func ownFolder(category, path string, info os.FileInfo) error {
	if !info.IsDir() {
		return notFolderRefusal(category, path, info)
	}
	if err := owned(path, info); err != nil {
		return err
	}
	if mode := info.Mode().Perm(); mode&0o022 != 0 || mode&0o500 != 0o500 {
		return folderModeRefusal(path, info.Mode())
	}
	return nil
}

// makeOwnFolder applies [ownFolder] to the folder at path, for a path that
// writes there, and then sets its mode to 0700 when it is not, keeping its
// setuid, setgid and sticky bits. It checks and changes one descriptor,
// opened without following a link, so the folder it checks is the folder it
// changes. A link or a file there is refused as ownFolder refuses it.
func makeOwnFolder(category, path string) error {
	folder, err := os.OpenFile(
		path,
		os.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC,
		0,
	)
	if err != nil {
		if info, statErr := os.Lstat(path); statErr == nil {
			if refusal := ownFolder(category, path, info); refusal != nil {
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
	if err = ownFolder(category, path, info); err != nil {
		return err
	}
	if info.Mode().Perm() == 0o700 {
		return nil
	}
	return folder.Chmod(info.Mode()&(os.ModeSetuid|os.ModeSetgid|os.ModeSticky) | 0o700)
}

// safeOwnFolder is safeParents for path, a folder that holds Workbench's own
// files: a folder of this user's there is judged by [ownFolder] first, as
// safeParents judges path before the folders above it, so one that others can
// write in is refused in ownFolder's words. A link, a file or another user's
// folder there is left to safeParents and the checks after it.
func safeOwnFolder(category, path string) error {
	if info, err := os.Lstat(path); err == nil && info.IsDir() && ownedByUser(info) {
		if err = ownFolder(category, path, info); err != nil {
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

// CheckPrivateDirectory is [PrivateDirectory] changing nothing, for an entry
// that must not make a folder where it looks, or only reads: root and each
// folder of name below it, down to the first one that is missing, must pass
// PrivateDirectory's checks, and one that does not fails it with
// PrivateDirectory's words; a folder that passes but is not 0700 keeps its
// mode. A missing folder ends the check with no error,
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
// must be a folder of Workbench's own files as [ownFolder] requires. With
// create it then sets each of those that is not 0700 to 0700 (see
// [makeOwnFolder]) and makes the missing folders, 0700; without, it changes
// nothing, and a missing one fails. It never follows a link. Unlike
// [PrivateDirectory] it can check without creating.
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
		if err == nil {
			if err = treeFolder(root, current, info); err != nil {
				return err
			}
			if Within(root, current) {
				own = append(own, current)
			}
		} else if !errors.Is(err, fs.ErrNotExist) {
			return err
		} else if !create {
			return Fail(ExitBlocked, "private_path", display.Path(current)+" does not exist")
		}
		if current == filepath.Dir(current) {
			break
		}
	}
	if !create {
		return nil
	}
	for _, folder := range own {
		if err := makeOwnFolder("private_path", folder); err != nil {
			return err
		}
	}
	if err := makeParents(root); err != nil {
		return err
	}
	return os.MkdirAll(target, 0o700)
}

// treeFolder refuses current, a folder at or above a [PrivateTree] target
// that info describes, unless it is a folder and, within root, a folder of
// Workbench's own files as [ownFolder] requires.
func treeFolder(root, current string, info os.FileInfo) error {
	if !Within(root, current) {
		if !info.IsDir() {
			return notFolderRefusal("private_path", current, info)
		}
		return nil
	}
	return ownFolder("private_path", current, info)
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

// Reject symlink parents rather than letting an override redirect private writes.
func safeParents(path string) error {
	for current := filepath.Clean(path); ; current = filepath.Dir(current) {
		info, err := os.Lstat(current)
		if err == nil {
			if info.Mode()&os.ModeSymlink != 0 {
				fix := "Workbench keeps its own files only in real folders, so make it one"
				if current == path {
					fix = "Workbench keeps its own files and folders as real ones, " +
						"so put what the link points to in its place"
				}
				return Fail(ExitInvalid, "path", display.Path(current)+" is a link; "+fix)
			}
			if current != path && !info.IsDir() {
				return Fail(
					ExitInvalid,
					"path",
					display.Path(
						current,
					)+" is a file, not a folder, so Workbench cannot keep its files under it",
				)
			}
			if !ownedByUserOrRoot(info) {
				return Fail(
					ExitInvalid,
					"permissions",
					display.Path(
						current,
					)+" belongs to another user; Workbench keeps its files only under folders you or root own",
				)
			}
			if info.Mode().Perm()&0o022 != 0 && info.Mode()&os.ModeSticky == 0 {
				return Fail(
					ExitInvalid,
					"permissions",
					display.Path(current)+" is writable by other users; run chmod go-w "+
						shellHomePath(current)+" and retry",
				)
			}
		} else if !errors.Is(err, fs.ErrNotExist) {
			return Fail(ExitInvalid, "path", FileError(current, err).Error())
		}
		if current == filepath.Dir(current) {
			return nil
		}
	}
}

func owned(path string, info os.FileInfo) error {
	if ownedByUser(info) {
		return nil
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return Fail(ExitInvalid, "permissions", "Cannot tell who owns "+display.Path(path))
	}
	return Fail(ExitInvalid, "permissions", notYours(path, stat.Uid, false))
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
		display.Path(path)+" is "+fileKind(info)+
			", not a folder, so Workbench cannot keep its files there; move it aside and retry",
	)
}

// folderModeRefusal refuses path, a folder of Workbench's own files of the
// mode mode. When its group or other users may write in it, what it holds
// may not be Workbench's, so a chmod alone would keep in it what another user
// put there: the refusal names the find that lists what is not this user's,
// to move aside before the chmod. Otherwise it is a folder its owner may not
// read or search, which Workbench cannot use, and the chmod is enough.
func folderModeRefusal(path string, mode os.FileMode) error {
	shown := fmt.Sprintf("%s has mode %04o", display.Path(path), modeBits(mode))
	if mode.Perm()&0o022 == 0 {
		return Fail(
			ExitInvalid,
			"permissions",
			shown+", and Workbench needs to read and search a folder of its own files"+
				retry([]string{"chmod", "700"}, path),
		)
	}
	folder := shellHomePath(path)
	user := shellWord(userName(uint32(os.Geteuid())))
	return Fail(
		ExitInvalid,
		"permissions",
		shown+", so other users can write in it and what it holds may not be Workbench's; "+
			"move aside what find "+folder+" -not -user "+user+" lists, "+
			"then run chmod 700 "+folder+" and retry",
	)
}

// modeBits is mode's permission bits with its setuid, setgid and sticky bits
// where chmod and ls put them, so a refusal shows 1777 for a sticky folder
// any user can write in.
func modeBits(mode os.FileMode) uint32 {
	bits := uint32(mode.Perm())
	if mode&os.ModeSetuid != 0 {
		bits |= unix.S_ISUID
	}
	if mode&os.ModeSetgid != 0 {
		bits |= unix.S_ISGID
	}
	if mode&os.ModeSticky != 0 {
		bits |= unix.S_ISVTX
	}
	return bits
}
