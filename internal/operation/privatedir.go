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
			return makeRefusal(path, err)
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
			false,
		)
	}
	entry, err := platform.OthersMayWrite(fd, at)
	if err != nil {
		// The folder is Workbench's own, which keeps no ACL on it, so the
		// whole ACL comes off.
		return Fail(ExitInvalid, "permissions", "Cannot read the ACL of "+display.Path(path),
			Cause(err.Error()), FixPath(path, platform.ACLRemoval()...))
	}
	if entry >= 0 {
		return othersWriteRefusal(
			path,
			display.Path(path)+" has an ACL that lets other users write in it",
			"what it holds may not be Workbench's",
			true,
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
		// As in CheckPrivateDirectory: Workbench's own mistake.
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
		// name is a folder of Workbench's own, from its code: a mistake of
		// Workbench's, which no line of the user's clears.
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
		// Callers pass a folder of their own inside root: this is
		// Workbench's own mistake, which no line of the user's clears.
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
			return lookupRefusal("private_path", current, err)
		case !create:
			// Each caller that does not create looks for target first, so
			// only a folder removed since then reaches this: the next
			// command finds it missing and says so in its own words.
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
			return lookupRefusal("private_path", folder, err)
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
				return makeRefusal(filepath.Dir(config), err)
			}
			if err = os.Mkdir(config, 0o700); err != nil && !os.IsExist(err) {
				return makeRefusal(config, err)
			}
		}
	}
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return makeRefusal(parent, err)
	}
	return nil
}

// makeRefusal refuses path, a folder Workbench makes for its own files or
// above them, which it failed with err to make: the nearest folder above it
// that is there keeps it out, named as [unreachable] names it, or, when its
// owner may search it but not write in it, with chmod u+rwx, what Workbench
// needs to make a folder in it.
func makeRefusal(path string, err error) error {
	held := filepath.Dir(path)
	info, statErr := os.Lstat(held)
	for statErr != nil && held != filepath.Dir(held) {
		held = filepath.Dir(held)
		info, statErr = os.Lstat(held)
	}
	headline := "Cannot make the folder " + display.Path(path)
	parts := unreachable(held, true, err)
	switch {
	case parts != nil:
	case statErr == nil && info.IsDir() && ownedByUser(info) &&
		errors.Is(err, fs.ErrPermission) && info.Mode().Perm()&0o200 == 0:
		parts = []Part{
			Cause(fmt.Sprintf("%s has mode %04o, so you cannot write in it",
				display.Path(held), platform.ModeBits(info.Mode()))),
			FixPath(held, "chmod", "u+rwx"),
		}
	default:
		parts = []Part{Cause(FileReason(path, err).Error())}
	}
	return Fail(ExitInvalid, "permissions", headline, parts...)
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
		// directory comes from the approved plan, which lists only folders
		// below data: Workbench's own mistake, which no line clears.
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
		// A file at a folder the plan lists for removal is left as it is:
		// what it is cannot be told, and the plan made with it there lists
		// it no more.
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
// [platform.OthersMayWrite]). One it cannot look up is refused as
// [lookupRefusal] says.
func safeParents(path string) error {
	for current := filepath.Clean(path); ; current = filepath.Dir(current) {
		info, err := os.Lstat(current)
		if err == nil {
			if err = safeParent(path, current, info); err != nil {
				return err
			}
		} else if !errors.Is(err, fs.ErrNotExist) {
			return lookupRefusal("path", current, err)
		}
		if current == filepath.Dir(current) {
			return nil
		}
	}
}

// lookupRefusal refuses path, a folder Workbench reads or writes in or a
// folder above one, which it failed with err to look up, with category: the
// cause and fix name what keeps Workbench out (see [unreachable]), a folder
// above path it cannot search, or a file in the place of one.
func lookupRefusal(category, path string, err error) error {
	parts := unreachable(path, true, err)
	if parts == nil {
		parts = []Part{Cause(FileReason(path, err).Error())}
	}
	return Fail(ExitInvalid, category, "Cannot read the folder "+display.Path(path), parts...)
}

// safeParent is [safeParents] for current, path or a folder above it, that
// info describes.
func safeParent(path, current string, info os.FileInfo) error {
	shown := display.Path(current)
	stat, _ := info.Sys().(*syscall.Stat_t)
	switch {
	case info.Mode()&os.ModeSymlink != 0:
		return linkRefusal(current, info)
	case current != path && !info.IsDir():
		return Fail(ExitInvalid, "path", shown+" is a file, not a folder",
			Cause("Workbench needs a folder there"),
			moveAsideFix(current))
	case !ownedByUserOrRoot(info) && stat != nil:
		return Fail(
			ExitInvalid,
			"permissions",
			shown+" belongs to "+userName(stat.Uid)+", not to you",
			Cause("Workbench writes only under folders you or root own"),
			FixPath(current, "sudo", "chown", userName(uint32(os.Geteuid()))),
		)
	case !ownedByUserOrRoot(info):
		// As in owned: every status carries its owner.
		return Fail(ExitInvalid, "permissions", "Cannot tell who owns "+shown)
	case info.Mode().Perm()&0o022 != 0 && info.Mode()&os.ModeSticky == 0:
		return Fail(ExitInvalid, "permissions", shown+" is writable by other users",
			Cause("another user could replace what Workbench writes under it"),
			FixPath(current, "chmod", "go-w"))
	case !info.IsDir():
		return nil
	}
	// Only the entry is removed, not the whole ACL, which may hold entries
	// the system put there, such as the deny delete of the home folder. An
	// ACL Workbench cannot read gets no line: removing all of it would take
	// those entries too, and `ls -le` shows it.
	entry, err := platform.OthersMayWrite(-1, current)
	switch {
	case err != nil:
		return Fail(ExitInvalid, "permissions", "Cannot read the ACL of "+shown, Cause(err.Error()))
	case entry >= 0:
		return Fail(
			ExitInvalid,
			"permissions",
			shown+" has an ACL that lets other users write in it",
			Cause("another user could replace what Workbench writes under it"),
			FixPath(current, platform.ACLEntryRemoval(entry)...),
		)
	}
	return nil
}

// linkRefusal refuses current, a link at or above a path Workbench reads or
// writes, which info describes: a write would go where it points. A link of
// this user's gets the lines that put the folder it points to in its place:
// rm of the link (rm(1) removes a link, not what it names), then mv -n of
// that folder to its path; a link to no folder, or to a folder above it,
// gets rm alone, and Workbench makes the folder. Another user's link, such
// as root's /tmp on macOS, gets the path it points to instead, to use in its
// place.
func linkRefusal(current string, info os.FileInfo) error {
	target, err := filepath.EvalSymlinks(current)
	pointed := err == nil && !Within(target, filepath.Dir(current))
	if pointed {
		folder, statErr := os.Stat(target)
		pointed = statErr == nil && folder.IsDir()
	}
	fix := FixPath(current, "rm")
	switch {
	case !ownedByUser(info) && err == nil:
		fix = FixText("use " + shellHomePath(target) + " in its place")
	case !ownedByUser(info):
		fix = noFix{}
	case pointed:
		fix = fixLines{
			fix,
			FixText("mv -n " + shellHomePath(target) + " " + shellHomePath(current)),
		}
	}
	return Fail(ExitInvalid, "path", display.Path(current)+" is a link",
		Cause("Workbench writes only in real folders, not where a link points"), fix)
}

func owned(path string, info os.FileInfo) error {
	if ownedByUser(info) {
		return nil
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		// Every status on macOS and Linux carries its owner: unreachable.
		return Fail(ExitInvalid, "permissions", "Cannot tell who owns "+display.Path(path))
	}
	return ownerRefusal(ExitInvalid, "permissions", path, stat.Uid, false)
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
		moveAsideFix(path),
	)
}

// othersWriteRefusal refuses path, a folder of Workbench's own files that its
// group or other users may write in, by its ACL when acl is set, as headline
// and cause say. What it holds may not be Workbench's, so the fix first
// closes it, so that no other user can add anything after that line, and
// lets its owner read it: the ACL comes off, then chmod 700, which also gives
// back the group bits a Linux ACL's removal sets from its group entry
// (setfacl(1)). Then the find, which reads it after those lines, lists what
// is not this user's, to move aside: chmod 700 typed alone would let the folder pass with
// what another user put in it.
func othersWriteRefusal(path, headline, cause string, acl bool) error {
	var fixes fixLines
	if acl {
		fixes = append(fixes, FixPath(path, platform.ACLRemoval()...))
	}
	return Fail(
		ExitInvalid,
		"permissions",
		headline,
		Cause(cause),
		append(
			fixes,
			FixPath(path, "chmod", "700"),
			FixText(
				"find "+shellHomePath(
					path,
				)+" -not -user "+shellWord(
					userName(uint32(os.Geteuid())),
				),
			),
			FixText("move aside what it lists"),
		),
	)
}

// modeRefusal refuses path, a folder of Workbench's own files whose mode
// [settleFolder] could not change, as err says: a file system mounted read
// only, which has no line, or a flag that locks the folder
// ([platform.ModeLocks]), named with the command that clears it
// ([platform.ClearFlags]), after sudo when only root may.
func modeRefusal(path string, err error) error {
	headline := "Cannot give " + display.Path(path) + " mode 0700"
	flags, flagErr := platform.ModeLocks(path)
	switch {
	case errors.Is(err, syscall.EROFS):
		// No command of the user's makes a file system writable: mounting it
		// again for writing is its administrator's.
		return Fail(ExitInvalid, "permissions", headline,
			Cause("its file system is mounted read-only, which only its administrator can change"))
	case !errors.Is(err, syscall.EPERM) || flagErr != nil || flags == 0:
		return Fail(ExitInvalid, "permissions", headline, Cause(FileReason(path, err).Error()))
	}
	names := platform.FlagNames(flags)
	what, which := "the flag "+names[0], "that flag"
	if len(names) > 1 {
		what, which = "the flags "+strings.Join(names, " and "), "those flags"
	}
	clear := platform.ClearFlags(flags)
	cause := "Workbench cannot give it mode 0700 while it has " + which
	var fix Part = noFix{}
	switch {
	case clear == nil:
		cause += ", and no command of yours clears it"
	case clear[0] == "sudo":
		cause += ", which only root can clear"
		fix = FixPath(path, clear...)
	default:
		fix = FixPath(path, clear...)
	}
	return Fail(ExitInvalid, "permissions", display.Path(path)+" has "+what, Cause(cause), fix)
}

// replacedRefusal refuses path, a folder of Workbench's own files that
// another entry took the place of while [settleFolder] judged and changed it.
// It has no line: the same command judges what is there then.
func replacedRefusal(path string) error {
	return Fail(
		ExitInvalid,
		"permissions",
		display.Path(path)+" was replaced while Workbench checked it",
	)
}
