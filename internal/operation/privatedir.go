package operation

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/Sawmonabo/workbench/internal/display"
)

func ensurePrivateDirectory(path string) error { return privateDirectory(path, true) }

// privateDirectory requires the folder at path to be private: no link and
// no file from it up, nothing above it that another user owns or may write
// (see safeParents), on a file system that keeps it private, a folder
// itself, mode 0700 and this user's. With create it first makes it, 0700,
// when it is missing; without, a missing folder fails with an error matching
// fs.ErrNotExist and nothing is made.
func privateDirectory(path string, create bool) error {
	if err := safeParents(path); err != nil {
		return err
	}
	if err := privateFilesystem(path); err != nil {
		return err
	}
	if create {
		if err := os.MkdirAll(path, 0o700); err != nil {
			return err
		}
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode().Perm() != 0o700 {
		return Fail(
			ExitInvalid,
			"permissions",
			"Existing "+display.Path(
				path,
			)+" must be a private directory (mode 0700); it was not changed",
		)
	}
	return owned(path, info)
}

// ensureRoot creates a missing runtime root 0700. [MakeParents] sets the modes
// of its missing parents.
func ensureRoot(root string) error {
	if err := safeParents(root); err != nil {
		return err
	}
	if err := privateFilesystem(root); err != nil {
		return err
	}
	if err := MakeParents(root); err != nil {
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
// folder of name below it, down to the first one that is missing, must be
// private as PrivateDirectory requires, and one that is not fails it with
// PrivateDirectory's words. A missing folder ends the check with no error,
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
// must be this user's with mode 0700. With create it then makes the missing
// folders; without, a missing one fails. It never repairs modes or follows
// links while creating a selected descendant. Unlike [PrivateDirectory] it can
// check without creating.
func PrivateTree(root, target string, create bool) error {
	if !filepath.IsAbs(root) || !Within(root, target) {
		return Fail(
			ExitInvalid,
			"private_path",
			"Private target escapes the selected runtime directory",
		)
	}
	for current := target; ; current = filepath.Dir(current) {
		info, err := os.Lstat(current)
		if err == nil {
			if !info.IsDir() {
				return Fail(
					ExitInvalid,
					"private_path",
					"Private directory ancestry contains a link or non-directory",
				)
			}
			if Within(root, current) {
				stat, ok := info.Sys().(*syscall.Stat_t)
				if !ok || int(stat.Uid) != os.Geteuid() || info.Mode().Perm() != 0o700 {
					return Fail(
						ExitInvalid,
						"permissions",
						"Private runtime directories must be owned by this user with mode 0700",
					)
				}
			}
		} else if !errors.Is(err, fs.ErrNotExist) {
			return err
		} else if !create {
			return Fail(
				ExitBlocked,
				"private_path",
				"Required private runtime directory is absent",
			)
		}
		if current == filepath.Dir(current) {
			break
		}
	}
	if create {
		if err := MakeParents(root); err != nil {
			return err
		}
		return os.MkdirAll(target, 0o700)
	}
	return nil
}

// MakeParents creates the missing folders above a runtime root. They get 0755,
// the mode the machine configuration gives its own folders such as ~/.local on
// Linux, so applying it later changes nothing for a folder that holds Workbench
// state. The exception is the XDG configuration home (~/.config): the
// specification asks for 0700 and the configuration keeps it there.
func MakeParents(root string) error {
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
				return Fail(
					ExitInvalid,
					"path",
					display.Path(
						current,
					)+" is a link; Workbench keeps its own files only in real folders, so make it one",
				)
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
			stat, ok := info.Sys().(*syscall.Stat_t)
			if !ok || (stat.Uid != 0 && int(stat.Uid) != os.Geteuid()) {
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
						ShellHomePath(current)+" and retry",
				)
			}
		} else if !errors.Is(err, fs.ErrNotExist) {
			return Fail(ExitInvalid, "path", "Cannot inspect Workbench path parents")
		}
		if current == filepath.Dir(current) {
			return nil
		}
	}
}

func owned(path string, info os.FileInfo) error {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || int(stat.Uid) != os.Geteuid() {
		return Fail(
			ExitInvalid,
			"permissions",
			display.Path(path)+" must belong to the current user; it was not changed",
		)
	}
	return nil
}
