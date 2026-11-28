package operation

import (
	"errors"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strconv"

	"github.com/Sawmonabo/workbench/internal/display"
	"github.com/Sawmonabo/workbench/internal/platform"
	"golang.org/x/sys/unix"
)

func validateImageGroup(c Context, path string, image Image) error {
	if image.Kind == ImageAbsent {
		return nil
	}
	if image.Group == nil {
		return Fail(ExitInvalid, "image", "An image of "+display.Path(path)+" has no group")
	}
	permitted, err := canSetGroup(*image.Group)
	if err != nil {
		return err
	}
	if !permitted {
		inherited, inheritErr := creationGroup(c, path)
		permitted = inheritErr == nil && inherited == *image.Group
	}
	if !permitted {
		h := replacedFile
		if image.Kind == ImageSymlink {
			h = replacedLink
		}
		return Fail(
			ExitBlocked,
			"metadata",
			display.Path(path)+" has group "+groupName(*image.Group)+
				", which you are not a member of, so Workbench cannot give it that group when it writes it"+
				h.retry([]string{"chgrp", groupName(groupID(os.Getegid()))}, path),
		)
	}
	return nil
}

// ImageWithGroup makes group ownership explicit without granting ownership
// changes. Existing targets retain their group, and a folder its set-group-ID
// bit; new targets use native parent inheritance: the group a new file at
// path gets, and on a folder the set-group-ID bit where a new folder takes it
// from the folder it is made in (platform.FolderTakesSetgid). Callers bind the
// completed image into their plan digest.
func ImageWithGroup(c Context, path string, image Image) (Image, error) {
	if image.Kind == ImageAbsent {
		image.Group = nil
		return image, nil
	}
	before, err := ReadImage(c, path)
	if err != nil {
		return Image{}, err
	}
	if before.Kind != ImageAbsent {
		image.Group = before.Group
		if before.Kind == ImageDirectory && image.Kind == ImageDirectory {
			image.Mode |= before.Mode & unix.S_ISGID
		}
		return image, nil
	}
	parent, err := creationParent(c, path)
	if err != nil {
		return Image{}, err
	}
	group := inheritedGroup(parent)
	image.Group = &group
	if image.Kind == ImageDirectory && platform.FolderTakesSetgid && parent.Mode&unix.S_ISGID != 0 {
		image.Mode |= unix.S_ISGID
	}
	return image, nil
}

// imageMode is the part of mode, a file's, that an image keeps: its
// permission bits, and on a folder the set-group-ID bit, which passes the
// folder's group on to what is made in it.
func imageMode(mode uint32) uint32 {
	kept := uint32(0o777)
	if mode&unix.S_IFMT == unix.S_IFDIR {
		kept |= unix.S_ISGID
	}
	return mode & kept
}

// specialBits are the set-user-ID, set-group-ID and sticky bits of mode, a
// file's, that an image does not keep (imageMode).
func specialBits(mode uint32) uint32 { return mode & 0o7000 &^ imageMode(mode) }

// canSetGroup reports whether the current user may give a file group: chown(2)
// allows only its effective group and its supplementary groups.
func canSetGroup(group uint32) (bool, error) {
	groups, err := os.Getgroups()
	if err != nil {
		return false, err
	}
	permitted := groupID(os.Getegid()) == group
	for _, member := range groups {
		permitted = permitted || groupID(member) == group
	}
	return permitted, nil
}

// creationGroup is the group a new file at path gets. [CheckPreservable] also
// asks it: an atomic native replacement must not silently change an existing
// group.
func creationGroup(c Context, path string) (uint32, error) {
	parent, err := creationParent(c, path)
	if err != nil {
		return 0, err
	}
	return inheritedGroup(parent), nil
}

// creationParent is the nearest existing folder above path, where a new file
// at path would be created.
func creationParent(c Context, path string) (unix.Stat_t, error) {
	if err := c.validateImagePath(path); err != nil {
		return unix.Stat_t{}, err
	}
	for parent := filepath.Dir(path); ; parent = filepath.Dir(parent) {
		var stat unix.Stat_t
		err := unix.Lstat(parent, &stat)
		if err == nil {
			if stat.Mode&unix.S_IFMT != unix.S_IFDIR {
				return unix.Stat_t{}, Fail(
					ExitBlocked,
					"metadata",
					display.Path(parent)+" is "+kindName(uint32(stat.Mode&unix.S_IFMT))+
						", not a folder, so Workbench cannot make "+display.Path(path)+
						" under it; move it aside and retry",
				)
			}
			return stat, nil
		}
		if !errors.Is(err, unix.ENOENT) {
			return unix.Stat_t{}, Fail(
				ExitBlocked,
				"metadata",
				"Cannot read "+display.Path(parent)+" ("+err.Error()+
					"), so Workbench cannot tell which group a new "+display.Path(path)+" gets",
			)
		}
		if parent == c.Scope.Root {
			return unix.Stat_t{}, Fail(
				ExitBlocked,
				"metadata",
				display.Path(parent)+" does not exist, so Workbench cannot tell which group a new "+
					display.Path(path)+" gets",
			)
		}
	}
}

// inheritedGroup is the group a new file in folder gets: the folder's on macOS
// and below a set-group-ID folder, else the effective group.
func inheritedGroup(folder unix.Stat_t) uint32 {
	if platform.GroupFromFolder || folder.Mode&unix.S_ISGID != 0 {
		return uint32(folder.Gid)
	}
	return groupID(os.Getegid())
}

// checkStagedGroup refuses a file target whose group, the one a new file at
// path gets, native's atomic write cannot keep. Native chezmoi writes a file
// through renameio, which stages it in the temporary folder (TMPDIR) whenever a
// file renames from there to path. Workbench gives native its private staging
// folder as TMPDIR, so the file arrives with staging's group and Workbench
// gives it the approved group afterwards. When the two differ and the user
// cannot set the approved group, that correction would fail after native
// wrote, so the plan stops first.
func checkStagedGroup(c Context, path, staging string) error {
	parent, err := creationParent(c, path)
	if err != nil {
		return err
	}
	group := inheritedGroup(parent)
	if permitted, err := canSetGroup(group); err != nil || permitted {
		return err
	}
	var temporary unix.Stat_t
	if err = unix.Stat(staging, &temporary); err != nil {
		return Fail(
			ExitBlocked,
			"metadata",
			"Cannot read Workbench's temporary folder "+display.Path(staging)+": "+err.Error(),
		)
	}
	staged := inheritedGroup(temporary)
	if temporary.Dev != parent.Dev || staged == group {
		return nil
	}
	return Fail(
		ExitBlocked,
		"metadata",
		fmt.Sprintf(
			"chezmoi stages %s in Workbench's temporary folder, of group %s, and you are not a "+
				"member of group %s, which its folder gives new files; give that folder one of "+
				"your groups (chgrp), or set TMPDIR to a folder that gives new files group %s",
			c.ShowPath(path), groupName(staged), groupName(group), groupName(group),
		),
	)
}

// userName is the name of the user with ID uid, or its number when it has
// none.
func userName(uid uint32) string {
	id := strconv.FormatUint(uint64(uid), 10)
	if named, err := user.LookupId(id); err == nil {
		return named.Username
	}
	return id
}

// groupName is the name of group, or its number when it has none.
func groupName(group uint32) string {
	id := strconv.FormatUint(uint64(group), 10)
	if named, err := user.LookupGroupId(id); err == nil {
		return named.Name
	}
	return id
}

func setImageGroup(fd int, group uint32) error {
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil {
		return err
	}
	if uint32(stat.Gid) == group {
		return nil
	}
	return unix.Fchown(fd, -1, int(group))
}

// groupID converts a group ID from the os package, which uses int. Group IDs
// are 32-bit on macOS and Linux, so the conversion is exact.
func groupID(id int) uint32 { return uint32(id) }
