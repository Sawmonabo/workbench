package operation

import (
	"bytes"
	"errors"
	"maps"
	"slices"
	"strings"
	"unicode/utf8"

	"golang.org/x/sys/unix"

	"github.com/Sawmonabo/workbench/internal/display"
	"github.com/Sawmonabo/workbench/internal/platform"
)

// carriedAttribute reports whether an image carries the extended attribute
// name, which the target Workbench writes then has too and revert puts back:
// every attribute, an ACL among them where ACLs are extended attributes
// (platform.ACLsAreAttributes), but file capabilities
// (platform.CapabilityAttribute), which only a process with CAP_SETFCAP may
// set, so neither the replacement nor revert could have them, and a name
// that is not UTF-8, which the checkpoint's JSON would change. The security
// label is carried by carryLabel's rule.
func carriedAttribute(name string) bool {
	return name != "" && name != platform.CapabilityAttribute && utf8.ValidString(name)
}

// security is what a target's metadata check read that its image carries
// beside its attributes: its ACL as the system keeps it, nil for none, and
// whether it has the hidden flag.
type security struct {
	acl    []byte
	hidden bool
}

// fileMetadata refuses metadata a target Workbench writes cannot carry: any
// file flag but the hidden flag that a replacement would not have (all but
// platform.KeptFlags), the flags that stop a replace among them, and an ACL
// entry under which no file can replace it (replacedACL). h is what the
// target is to the write.
func fileMetadata(fd int, path string, h holder) (security, error) {
	return metadata(fd, path, h, false)
}

// containerMetadata is fileMetadata for a folder that holds Workbench's own
// files. Workbench never removes or replaces it and writeDirectoryImage changes
// it in place, so it may keep the flags a folder Workbench writes into may
// keep (platform.FolderFlags), and, since Workbench writes its own files in
// it, only an ACL folderACL admits and, where ACLs are extended attributes,
// no default ACL (attributeACL).
func containerMetadata(fd int, path string) (security, error) {
	return metadata(fd, path, changedFolder, true)
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
	_, err := metadata(fd, path, parentFolder, true)
	return err
}

// linkMetadata is fileMetadata for the link at path itself.
func linkMetadata(path string) (security, error) {
	return metadata(-1, path, replacedLink, false)
}

// metadata refuses fd at path, or the link path itself when h is
// replacedLink, off a native file system, with a flag outside those it may
// keep, or with an ACL its rule refuses; h is what path is to the write,
// which says why. A folder Workbench writes files into, when written, may
// keep platform.FolderFlags, the ACL folderACL admits and the attributes
// attributeACL admits; a target the flags a replacement has
// (platform.KeptFlags) and the hidden flag, and the ACL replacedACL admits.
// It returns the ACL and the hidden flag it read.
func metadata(fd int, path string, h holder, written bool) (security, error) {
	kept, acl := platform.KeptFlags|platform.HiddenFlag, replacedACL
	if written {
		kept, acl = platform.FolderFlags, folderACL
	}
	if err := privateFilesystem(path); err != nil {
		return security{}, err
	}
	selector := ""
	if h == replacedLink {
		fd, selector = -1, path
	}
	flags, err := platform.FileFlags(fd, selector)
	if err != nil || flags&^kept != 0 {
		return security{}, flagsRefusal(path, h, flags&^kept, err)
	}
	if written {
		if err = attributeACL(fd, path, h); err != nil {
			return security{}, err
		}
	}
	found, err := targetACL(fd, selector, path, h)
	if err == nil {
		err = acl(path, h, found.Entries)
	}
	if err != nil {
		return security{}, err
	}
	return security{acl: found.Data, hidden: flags&platform.HiddenFlag != 0}, nil
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
func folderACL(path string, h holder, entries []platform.ACE) error {
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

// replacedACL refuses, on a file or link Workbench writes a new one in place
// of, an ACL entry that denies deleting it, which also its replacement would
// carry: no file can be renamed over such a file, nor can the replacement be
// renamed into its place or removed (vnode_authorize_delete in xnu's
// vfs_subr.c), as KAUTH_VNODE_DELETE and the generic rights that include it
// (KAUTH_ACE_GENERIC_ALL, KAUTH_ACE_GENERIC_WRITE, sys/kauth.h) say. It names
// the entry by its number in ls -le, which chmod -a# removes. Every other
// entry is carried. A folder Workbench changes in place keeps any ACL.
func replacedACL(path string, h holder, entries []platform.ACE) error {
	if !h.replaced() {
		return nil
	}
	const (
		kind   = 0xf                  // KAUTH_ACE_KINDMASK
		deny   = 2                    // KAUTH_ACE_DENY
		delete = 1<<4 | 1<<21 | 1<<23 // KAUTH_VNODE_DELETE, KAUTH_ACE_GENERIC_ALL, KAUTH_ACE_GENERIC_WRITE
	)
	for i, entry := range entries {
		if entry.Flags&kind == deny && entry.Rights&delete != 0 {
			return Fail(
				ExitBlocked,
				"metadata",
				display.Path(path)+" has an ACL entry that denies deleting it, "+h.locked()+
					h.retry(platform.ACLEntryRemoval(i), path),
			)
		}
	}
	return nil
}

// targetACL reads the ACL of fd, or of the link selector itself when it is
// not empty, failing closed when it cannot be read or its layout is unknown.
// path is the target the ACL is read for and h what it is to the write.
func targetACL(fd int, selector, path string, h holder) (platform.ACL, error) {
	acl, err := platform.FileSecurity(fd, selector)
	switch {
	case errors.Is(err, platform.ErrUnsupportedACL):
		shown := display.Path(path)
		return platform.ACL{}, Fail(
			ExitBlocked,
			"metadata",
			shown+" has an ACL in a form Workbench does not read,"+
				" so it cannot tell what the ACL allows"+h.retry(platform.ACLRemoval(), path),
		)
	case err != nil:
		return platform.ACL{}, Fail(
			ExitBlocked,
			"metadata",
			"Cannot read the ACL of "+display.Path(path)+": "+err.Error(),
		)
	}
	return acl, nil
}

// attributeACL refuses, where ACLs are extended attributes
// (platform.ACLsAreAttributes), the folder fd at path, which Workbench writes
// files into, when its attribute names cannot be read or it has one the
// files written there could take up: FileSecurity sees no ACL there, and a
// default ACL (platform.DefaultACLAttribute), which each file made in the
// folder inherits, is such an attribute. As the folder a target is written
// into (parentFolder) it refuses every attribute but the label, by its exact
// name; as a folder of Workbench's own files it changes (changedFolder), the
// default ACL alone, carrying the others, as folderACL refuses the entries
// files made in a macOS folder inherit.
func attributeACL(fd int, path string, h holder) error {
	if !platform.ACLsAreAttributes {
		return nil
	}
	names, err := platform.ExtendedAttributeNames(fd, path)
	if err != nil {
		return attributeReadError(path, "", err)
	}
	for _, name := range names {
		switch {
		case name == platform.LabelAttribute:
		case h == parentFolder:
			return attributeRefusal(path, name, h)
		case name == platform.DefaultACLAttribute:
			return attributeRefusal(path, name, parentFolder)
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
		if !carriedAttribute(name) {
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

// writeImageAttributes gives fd, or the link at link when fd is negative,
// exactly the attributes of an image, apart from the label, setting only
// those whose value differs: carryLabel writes an image's label, and one the
// image lacks is left as the system gave it, since SELinux lets no one remove
// a label. The access ACL (platform.AccessACLAttribute) comes last: setting
// it sets the mode from the ACL, which may take away the write permission a
// user.* attribute needs (xattr_permission in fs/xattr.c). path is the target
// they are written for, which is h to the write and which a failure names
// with the attribute (carryFailure).
func writeImageAttributes(
	fd int,
	link, path string,
	attributes map[string][]byte,
	h holder,
) error {
	at := path
	if fd < 0 {
		at = link
	}
	existing, err := readImageAttributes(fd, at, h)
	if err != nil {
		return err
	}
	for _, name := range slices.Sorted(maps.Keys(existing)) {
		if _, ok := attributes[name]; ok || name == platform.LabelAttribute {
			continue
		}
		if fd < 0 {
			err = unix.Lremovexattr(at, name)
		} else {
			err = unix.Fremovexattr(fd, name)
		}
		if err != nil {
			return carryFailure(path, h, true, "the extended attribute "+shellWord(name), err)
		}
	}
	names := slices.Sorted(maps.Keys(attributes))
	if i := slices.Index(names, platform.AccessACLAttribute); i >= 0 {
		names = append(slices.Delete(names, i, i+1), platform.AccessACLAttribute)
	}
	for _, name := range names {
		data := attributes[name]
		current, held := existing[name]
		switch {
		case name == platform.LabelAttribute:
			err = carryLabel(fd, at, existing, data)
		case held && bytes.Equal(current, data):
		case fd < 0:
			err = unix.Lsetxattr(at, name, data, 0)
		default:
			err = unix.Fsetxattr(fd, name, data, 0)
		}
		if err != nil {
			return carryFailure(path, h, false, "the extended attribute "+shellWord(name), err)
		}
	}
	return nil
}

// writeImageSecurity gives fd, or the link at link when fd is negative, the
// hidden flag and then the ACL of desired, each only where current, what it
// holds, differs. They come after the attributes and the mode, as their
// writes need neither, and the ACL comes last: an entry in it that denies its
// owner writing extended attributes would stop that write (xnu's
// vnode_authorize_callback), and its owner may set a flag and an ACL whatever
// the mode and the ACL allow. path and h are as for writeImageAttributes.
func writeImageSecurity(fd int, link, path string, current, desired Image, h holder) error {
	if current.Hidden != desired.Hidden {
		if err := platform.SetHidden(fd, link, desired.Hidden); err != nil {
			return carryFailure(path, h, !desired.Hidden, "the hidden flag", err)
		}
	}
	if !bytes.Equal(current.ACL, desired.ACL) {
		if err := platform.SetFileSecurity(fd, link, desired.ACL); err != nil {
			return carryFailure(path, h, desired.ACL == nil, "its ACL", err)
		}
	}
	return nil
}
