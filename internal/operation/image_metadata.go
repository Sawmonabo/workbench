package operation

import (
	"bytes"
	"errors"
	"slices"

	"golang.org/x/sys/unix"

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

// fileMetadata refuses metadata a target Workbench replaces cannot carry: any
// file flag a replacement would not have (all but platform.KeptFlags), the
// flags that stop a replace among them, any ACL and, where ACLs are extended
// attributes, any attribute but the label (attributeACL).
func fileMetadata(fd int, path string) error {
	return metadata(fd, path, platform.KeptFlags, targetFlags, noACL)
}

// containerMetadata is fileMetadata for a folder that holds Workbench's own
// files. Workbench never removes or replaces it, and writeDirectoryImage sets
// its mode, group and attributes through a descriptor without touching its ACL
// or flags, so it may keep the ACL entries folderACL admits and the flags a
// folder Workbench writes into may keep (platform.FolderFlags) but the hidden
// flag, which the checkpoint records only on a folder Workbench writes into.
// Any attribute but the label still blocks where ACLs are extended attributes.
func containerMetadata(fd int, path string) error {
	return metadata(fd, path, platform.FolderFlags&^platform.HiddenFlag, targetFlags, folderACL)
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
	return metadata(
		fd,
		path,
		platform.FolderFlags,
		"Target folder filesystem flags cannot be preserved",
		folderACL,
	)
}

// targetFlags is metadata's refusal of a target's flags.
const targetFlags = "Target filesystem flags cannot be preserved"

// metadata refuses fd at path off a native file system, with a flag outside
// kept, whose refusal is flagRefusal, or with an attribute or ACL its system
// cannot carry, as acl judges the ACL.
func metadata(
	fd int,
	path string,
	kept uint32,
	flagRefusal string,
	acl func([]platform.ACE, bool, error) error,
) error {
	if err := privateFilesystem(path); err != nil {
		return err
	}
	if flags, err := platform.FileFlags(fd, ""); err != nil || flags&^kept != 0 {
		return Fail(ExitBlocked, "metadata", flagRefusal)
	}
	if err := attributeACL(fd, "", targetAttributes); err != nil {
		return err
	}
	return acl(targetACL(fd, ""))
}

// folderACL admits the ACL of a folder whose own entries Workbench leaves
// alone: no entry may pass to new entries or deny adding or removing them.
// macOS puts one admitted entry, "group:everyone deny delete", on the home
// folder and its standard folders such as ~/Library; it only stops the folder
// itself from being deleted.
func folderACL(entries []platform.ACE, _ bool, err error) error {
	if err != nil {
		return err
	}
	const (
		inherits     = 1<<5 | 1<<6        // KAUTH_ACE_FILE_INHERIT, KAUTH_ACE_DIRECTORY_INHERIT
		kind         = 0xf                // KAUTH_ACE_KINDMASK
		deny         = 2                  // KAUTH_ACE_DENY
		childChanges = 1<<2 | 1<<5 | 1<<6 // KAUTH_VNODE_ADD_FILE, ADD_SUBDIRECTORY, DELETE_CHILD
	)
	for _, entry := range entries {
		if entry.Flags&inherits != 0 || entry.Flags&kind == deny && entry.Rights&childChanges != 0 {
			return Fail(
				ExitBlocked,
				"metadata",
				"Target folder ACL passes to new entries or denies changing them",
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
		return Fail(ExitBlocked, "metadata", "Target link flags cannot be preserved")
	}
	if err := attributeACL(
		-1,
		path,
		"Links with extended attributes or unverified ACLs cannot be preserved",
	); err != nil {
		return err
	}
	return noACL(targetACL(-1, path))
}

// noACL rejects any ACL, even an empty one, on a target Workbench replaces:
// the replacement would not carry it.
func noACL(_ []platform.ACE, present bool, err error) error {
	if err == nil && present {
		return Fail(
			ExitBlocked,
			"metadata",
			"Targets with ACLs require reviewed preservation support",
		)
	}
	return err
}

// targetACL reads the ACL of fd, or of path when it is not empty, failing
// closed when it cannot be read or its layout is unknown.
func targetACL(fd int, path string) ([]platform.ACE, bool, error) {
	entries, present, err := platform.FileSecurity(fd, path)
	switch {
	case errors.Is(err, platform.ErrUnsupportedACL):
		return nil, false, Fail(ExitBlocked, "metadata", "Unsupported target ACL metadata")
	case err != nil:
		return nil, false, Fail(ExitBlocked, "metadata", "Cannot verify target ACL semantics")
	}
	return entries, present, nil
}

// targetAttributes is attributeACL's refusal for a target or its folder.
const targetAttributes = "Targets with extended attributes or unverified ACLs cannot be preserved"

// attributeACL refuses, where ACLs are extended attributes
// (platform.ACLsAreAttributes), fd, or path itself when fd is negative, when it
// has an extended attribute images do not carry, or its names cannot be read:
// FileSecurity sees no ACL there, and an ACL is such an attribute. The label
// alone passes, by its exact name. refusal is the blocked error's message.
func attributeACL(fd int, path, refusal string) error {
	if !platform.ACLsAreAttributes {
		return nil
	}
	names, err := platform.ExtendedAttributeNames(fd, path)
	if err != nil {
		return Fail(ExitBlocked, "metadata", refusal)
	}
	for _, name := range names {
		if !slices.Contains(preservedAttributes, name) {
			return Fail(ExitBlocked, "metadata", refusal)
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
