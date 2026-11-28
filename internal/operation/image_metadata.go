package operation

import (
	"bytes"
	"errors"
	"slices"
	"strings"

	"golang.org/x/sys/unix"

	"github.com/Sawmonabo/workbench/internal/display"
	"github.com/Sawmonabo/workbench/internal/platform"
)

// preservedAttributes are the extended attributes images carry. On macOS each
// is small, grants no access and is commonly added by macOS: download
// provenance and quarantine, Finder flags and the last used date. Every other
// attribute blocks planning, and so does an ACL except where folderACL admits it.
// Where ACLs are extended attributes (platform.ACLsAreAttributes), as on Linux,
// only the security label (platform.LabelAttribute) is carried, as
// carryLabel writes it, and attributeACL blocks every other attribute: an ACL
// (system.posix_acl_access, system.posix_acl_default), file capabilities
// (security.capability), and user.* and trusted.* attributes a replacement
// would not have.
var preservedAttributes = carriedAttributes()

// carriedAttributes is preservedAttributes: only the label where ACLs are
// extended attributes.
func carriedAttributes() []string {
	if platform.ACLsAreAttributes {
		return []string{platform.LabelAttribute}
	}
	return []string{
		"com.apple.provenance",
		"com.apple.quarantine",
		"com.apple.FinderInfo",
		"com.apple.lastuseddate#PS",
	}
}

// fileMetadata refuses metadata a target Workbench writes cannot carry: any
// file flag a replacement would not have (all but platform.KeptFlags), the
// flags that stop a replace among them, any ACL and, where ACLs are extended
// attributes, any attribute but the label (attributeACL). h is what the
// target is to the write.
func fileMetadata(fd int, path string, h holder) error {
	return metadata(fd, path, h, platform.KeptFlags, noACL)
}

// containerMetadata is fileMetadata for a folder that holds Workbench's own
// files. Workbench never removes or replaces it, and writeDirectoryImage sets
// its mode, group and attributes through a descriptor without touching its ACL
// or flags, so it may keep the ACL entries folderACL admits and the flags a
// folder Workbench writes into may keep (platform.FolderFlags) but the hidden
// flag, which the checkpoint records only on a folder Workbench writes into.
// Any attribute but the label still blocks where ACLs are extended attributes.
func containerMetadata(fd int, path string) error {
	return metadata(fd, path, changedFolder, platform.FolderFlags&^platform.HiddenFlag, folderACL)
}

// parentMetadata admits the folder a target is written into. Workbench adds,
// replaces and removes entries there but never changes the folder itself, so
// the folder may keep an ACL folderACL admits, such as the "group:everyone deny
// delete" macOS puts on the home folder and its standard folders, and the
// flags platform.FolderFlags names: the hidden flag macOS puts on ~/Library,
// which only hides the folder in Finder, and on Linux any inode flag but the
// immutable and append-only ones, under which those entries cannot change.
// Where ACLs are extended attributes, any attribute there but the label
// blocks planning, as on a target.
func parentMetadata(fd int, path string) error {
	return metadata(fd, path, parentFolder, platform.FolderFlags, folderACL)
}

// metadata refuses fd at path off a native file system, with a flag outside
// kept, or with an attribute or ACL its system cannot carry, as acl judges
// the ACL; h is what path is to the write, which says why.
func metadata(
	fd int,
	path string,
	h holder,
	kept uint32,
	acl func(string, holder, []platform.ACE, bool, error) error,
) error {
	if err := privateFilesystem(path); err != nil {
		return err
	}
	if flags, err := platform.FileFlags(fd, ""); err != nil || flags&^kept != 0 {
		return flagsRefusal(path, h, flags&^kept, err)
	}
	if err := attributeACL(fd, path, h); err != nil {
		return err
	}
	entries, present, err := targetACL(fd, path, h)
	return acl(path, h, entries, present, err)
}

// flagsRefusal refuses path, which is h to the write, whose flags could not
// be read, with err, or hold flags, those a write cannot keep.
func flagsRefusal(path string, h holder, flags uint32, err error) error {
	if err != nil {
		return Fail(
			ExitBlocked,
			"metadata",
			"Cannot read the file flags of "+display.Path(path)+": "+err.Error(),
		)
	}
	return flagRefusal(path, flags, h)
}

// folderACL admits the ACL of a folder whose own entries Workbench leaves
// alone: no entry may pass to new entries or deny adding or removing them.
// macOS puts one admitted entry, "group:everyone deny delete", on the home
// folder and its standard folders such as ~/Library; it only stops the folder
// itself from being deleted. A refusal names the entry by its number in
// ls -le, which chmod -a# removes.
func folderACL(path string, h holder, entries []platform.ACE, _ bool, err error) error {
	if err != nil {
		return err
	}
	const (
		inherits     = 1<<5 | 1<<6        // KAUTH_ACE_FILE_INHERIT, KAUTH_ACE_DIRECTORY_INHERIT
		kind         = 0xf                // KAUTH_ACE_KINDMASK
		deny         = 2                  // KAUTH_ACE_DENY
		childChanges = 1<<2 | 1<<5 | 1<<6 // KAUTH_VNODE_ADD_FILE, ADD_SUBDIRECTORY, DELETE_CHILD
	)
	for i, entry := range entries {
		inherited := entry.Flags&inherits != 0
		if inherited || entry.Flags&kind == deny && entry.Rights&childChanges != 0 {
			cause := "that denies adding or removing files in it, and Workbench writes only in a folder without one"
			if inherited {
				cause = "that" + inheritedACL
			}
			return Fail(
				ExitBlocked,
				"metadata",
				display.Path(path)+" has an ACL entry "+cause+
					h.retry(platform.ACLEntryRemoval(i), path),
			)
		}
	}
	return nil
}

// linkMetadata is fileMetadata for the link at path itself.
func linkMetadata(path string) error {
	if err := privateFilesystem(path); err != nil {
		return err
	}
	if flags, err := platform.FileFlags(-1, path); err != nil || flags&^platform.KeptFlags != 0 {
		return flagsRefusal(path, replacedLink, flags&^platform.KeptFlags, err)
	}
	if err := attributeACL(-1, path, replacedLink); err != nil {
		return err
	}
	entries, present, err := targetACL(-1, path, replacedLink)
	return noACL(path, replacedLink, entries, present, err)
}

// noACL rejects any ACL, even an empty one, on a target Workbench writes:
// the replacement would not carry it, nor would revert put it back on a
// folder. platform.ACLRemoval removes it.
func noACL(path string, h holder, _ []platform.ACE, present bool, err error) error {
	if err == nil && present {
		return Fail(
			ExitBlocked,
			"metadata",
			display.Path(path)+" has an ACL, "+h.lost()+h.retry(platform.ACLRemoval(), path),
		)
	}
	return err
}

// targetACL reads the ACL of fd, or of the link path itself when h is
// replacedLink, failing closed when it cannot be read or its layout is
// unknown.
func targetACL(fd int, path string, h holder) ([]platform.ACE, bool, error) {
	selector := ""
	if h == replacedLink {
		fd, selector = -1, path
	}
	entries, present, err := platform.FileSecurity(fd, selector)
	switch {
	case errors.Is(err, platform.ErrUnsupportedACL):
		shown := display.Path(path)
		return nil, false, Fail(
			ExitBlocked,
			"metadata",
			shown+" has an ACL in a form Workbench does not read,"+
				" so it cannot tell what the ACL allows"+h.retry(platform.ACLRemoval(), path),
		)
	case err != nil:
		return nil, false, Fail(
			ExitBlocked,
			"metadata",
			"Cannot read the ACL of "+display.Path(path)+": "+err.Error(),
		)
	}
	return entries, present, nil
}

// attributeACL refuses, where ACLs are extended attributes
// (platform.ACLsAreAttributes), fd, or path itself when fd is negative, when it
// has an extended attribute images do not carry, or its names cannot be read:
// FileSecurity sees no ACL there, and an ACL is such an attribute. The label
// alone passes, by its exact name. h is what path is to the write.
func attributeACL(fd int, path string, h holder) error {
	if !platform.ACLsAreAttributes {
		return nil
	}
	names, err := platform.ExtendedAttributeNames(fd, path)
	if err != nil {
		return attributeReadError(path, "", err)
	}
	for _, name := range names {
		if !slices.Contains(preservedAttributes, name) {
			return attributeRefusal(path, name, h)
		}
	}
	return nil
}

// sameAttributes reports whether two images' attributes match, apart from the
// label: a relabel, as restorecon makes, is the system's and no edit, so it
// neither stops a write nor makes one. carryLabel and the native correction
// (nativeLabel) compare the label themselves.
func sameAttributes(a, b map[string][]byte) bool {
	equal := func(x, y map[string][]byte) bool {
		for name, value := range x {
			if name == platform.LabelAttribute {
				continue
			}
			if other, ok := y[name]; !ok || !bytes.Equal(value, other) {
				return false
			}
		}
		return true
	}
	return equal(a, b) && equal(b, a)
}

// carryLabel gives fd, or the link at path when fd is negative, the label
// desired, an earlier image's, as sed -i, Vim and Emacs give a file they
// replace its old label; current is what the system gave the inode. A label
// it already has is not written, so the common case needs no relabel
// permission. Where the inode has none and the system refuses one (EPERM,
// EOPNOTSUPP), SELinux is off: the system labels no file, setting a label
// takes CAP_SYS_ADMIN, and turning SELinux back on relabels the whole file
// system, so the inode stays without one, as every file made meanwhile. Any
// other refusal fails the write before its rename, so a replacement never
// takes a label other than the file's it replaces.
func carryLabel(fd int, path string, current map[string][]byte, desired []byte) error {
	label, labeled := current[platform.LabelAttribute]
	if labeled && bytes.Equal(label, desired) {
		return nil
	}
	var err error
	if fd < 0 {
		err = unix.Lsetxattr(path, platform.LabelAttribute, desired, 0)
	} else {
		err = unix.Fsetxattr(fd, platform.LabelAttribute, desired, 0)
	}
	if !labeled && (errors.Is(err, unix.EPERM) || errors.Is(err, unix.EOPNOTSUPP)) {
		return nil
	}
	return err
}

func readImageAttributes(fd int, path string, h holder) (map[string][]byte, error) {
	var names [4096]byte
	var n int
	var err error
	if fd < 0 {
		n, err = unix.Llistxattr(path, names[:])
	} else {
		n, err = unix.Flistxattr(fd, names[:])
	}
	if errors.Is(err, unix.ERANGE) {
		return nil, Fail(
			ExitBlocked,
			"metadata",
			"The names of the extended attributes of "+display.Path(path)+" take more than the "+
				display.Limit(int64(len(names)))+" Workbench reads",
		)
	}
	if err != nil {
		return nil, attributeReadError(path, "", err)
	}
	attributes := map[string][]byte{}
	for name := range strings.SplitSeq(string(names[:n]), "\x00") {
		if name == "" {
			continue
		}
		if !slices.Contains(preservedAttributes, name) {
			return nil, attributeRefusal(path, name, h)
		}
		var data [4096]byte
		if fd < 0 {
			n, err = unix.Lgetxattr(path, name, data[:])
		} else {
			n, err = unix.Fgetxattr(fd, name, data[:])
		}
		if errors.Is(err, unix.ERANGE) {
			what := shellWord(name) + " of " + display.Path(path)
			return nil, Fail(
				ExitBlocked,
				"metadata",
				"The extended attribute "+what+" is larger than the "+
					display.Limit(int64(len(data)))+" Workbench keeps",
			)
		}
		if err != nil {
			return nil, attributeReadError(path, name, err)
		}
		attributes[name] = bytes.Clone(data[:n])
	}
	return attributes, nil
}

// writeImageAttributes gives fd, or the link at path when fd is negative,
// exactly the attributes of an image, apart from the label: carryLabel writes
// an image's label, and one the image lacks is left as the system gave it,
// since SELinux lets no one remove a label.
func writeImageAttributes(fd int, path string, attributes map[string][]byte, h holder) error {
	existing, err := readImageAttributes(fd, path, h)
	if err != nil {
		return err
	}
	for name := range existing {
		if _, ok := attributes[name]; ok || name == platform.LabelAttribute {
			continue
		}
		if fd < 0 {
			err = unix.Lremovexattr(path, name)
		} else {
			err = unix.Fremovexattr(fd, name)
		}
		if err != nil {
			return err
		}
	}
	for name, data := range attributes {
		switch {
		case name == platform.LabelAttribute:
			err = carryLabel(fd, path, existing, data)
		case fd < 0:
			err = unix.Lsetxattr(path, name, data, 0)
		default:
			err = unix.Fsetxattr(fd, name, data, 0)
		}
		if err != nil {
			return err
		}
	}
	return nil
}
