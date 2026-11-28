package operation

import (
	"errors"

	"github.com/Sawmonabo/workbench/internal/platform"
)

// preservedAttributes are the extended attributes images carry verbatim. Each
// is small, grants no access and is commonly added by macOS: download
// provenance and quarantine, Finder flags and the last used date. Every other
// attribute blocks planning, and so does an ACL except where folderACL admits it.
// Where ACLs are extended attributes (platform.ACLsAreAttributes), as on Linux,
// any attribute may be one, so none is carried and attributeACL blocks each.
var preservedAttributes = carriedAttributes()

// carriedAttributes is preservedAttributes: none where ACLs are extended
// attributes.
func carriedAttributes() []string {
	if platform.ACLsAreAttributes {
		return nil
	}
	return []string{
		"com.apple.provenance",
		"com.apple.quarantine",
		"com.apple.FinderInfo",
		"com.apple.lastuseddate#PS",
	}
}

// fileMetadata refuses metadata a target Workbench replaces cannot carry:
// file flags, any ACL and, where ACLs are extended attributes, any attribute.
func fileMetadata(fd int, path string) error { return metadata(fd, path, noACL) }

// containerMetadata is fileMetadata for a folder that holds Workbench's own
// files. Workbench never removes or replaces it, and writeDirectoryImage sets
// its mode, group and attributes through a descriptor without touching its ACL,
// so it may keep the ACL entries folderACL admits. Its flags still block, and
// so does any attribute where ACLs are extended attributes.
func containerMetadata(fd int, path string) error { return metadata(fd, path, folderACL) }

func metadata(fd int, path string, acl func([]platform.ACE, bool, error) error) error {
	if err := privateFilesystem(path); err != nil {
		return err
	}
	if flags, err := platform.FileFlags(fd, ""); err != nil || flags != 0 {
		return Fail(ExitBlocked, "metadata", "Target filesystem flags cannot be preserved")
	}
	if err := attributeACL(fd, "", targetAttributes); err != nil {
		return err
	}
	return acl(targetACL(fd, ""))
}

// parentMetadata admits the folder a target is written into. Workbench adds,
// replaces and removes entries there but never changes the folder itself, so
// the folder may keep an ACL folderACL admits, such as the "group:everyone deny
// delete" macOS puts on the home folder and its standard folders, and the
// hidden flag macOS puts on ~/Library, which only hides the folder in Finder.
// Where ACLs are extended attributes, any attribute there blocks planning, as
// on a target.
func parentMetadata(fd int, path string) error {
	if err := privateFilesystem(path); err != nil {
		return err
	}
	if flags, err := platform.FileFlags(fd, ""); err != nil || flags&^platform.HiddenFlag != 0 {
		return Fail(ExitBlocked, "metadata", "Target folder filesystem flags cannot be preserved")
	}
	if err := attributeACL(fd, "", targetAttributes); err != nil {
		return err
	}
	return folderACL(targetACL(fd, ""))
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
	if flags, err := platform.FileFlags(-1, path); err != nil || flags != 0 {
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
// has any extended attribute or they cannot be counted: FileSecurity sees no
// ACL there, and any attribute may be one. refusal is the blocked error's
// message.
func attributeACL(fd int, path, refusal string) error {
	if !platform.ACLsAreAttributes {
		return nil
	}
	count, err := platform.ExtendedAttributeCount(fd, path)
	if err != nil || count != 0 {
		return Fail(ExitBlocked, "metadata", refusal)
	}
	return nil
}
