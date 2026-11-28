package operation

import (
	"errors"

	"github.com/Sawmonabo/workbench/internal/platform"
)

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
