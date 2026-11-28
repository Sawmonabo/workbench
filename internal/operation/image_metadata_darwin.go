package operation

import (
	"golang.org/x/sys/unix"

	"github.com/Sawmonabo/workbench/internal/platform"
)

// preservedAttributes are the extended attributes images carry verbatim. Each
// is small, grants no access and is commonly added by the system: download
// provenance and quarantine, Finder flags and the last used date. Every other
// attribute blocks planning, and so does an ACL except where folderACL admits it.
var preservedAttributes = []string{
	"com.apple.provenance",
	"com.apple.quarantine",
	"com.apple.FinderInfo",
	"com.apple.lastuseddate#PS",
}

func fileMetadata(fd int, path string) error { return metadata(fd, path, noACL) }

// containerMetadata is fileMetadata for a folder that holds Workbench's own
// files. Workbench never removes or replaces it, and writeDirectoryImage sets
// its mode, group and attributes through a descriptor without touching its ACL,
// so it may keep the ACL entries folderACL admits. Its flags still block.
func containerMetadata(fd int, path string) error { return metadata(fd, path, folderACL) }

func metadata(fd int, path string, acl func([]platform.ACE, bool, error) error) error {
	if err := privateFilesystem(path); err != nil {
		return err
	}
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil || stat.Flags != 0 {
		return Fail(ExitBlocked, "metadata", "Target filesystem flags cannot be preserved")
	}
	return acl(targetACL(fd, ""))
}

// parentMetadata admits the folder a target is written into. Workbench adds,
// replaces and removes entries there but never changes the folder itself, so
// the folder may keep an ACL folderACL admits, such as the "group:everyone deny
// delete" macOS puts on the home folder and its standard folders, and the
// hidden flag macOS puts on ~/Library, which only hides the folder in Finder.
func parentMetadata(fd int, path string) error {
	if err := privateFilesystem(path); err != nil {
		return err
	}
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil || stat.Flags&^unix.UF_HIDDEN != 0 {
		return Fail(ExitBlocked, "metadata", "Target folder filesystem flags cannot be preserved")
	}
	return folderACL(targetACL(fd, ""))
}

func linkMetadata(path string) error {
	if err := privateFilesystem(path); err != nil {
		return err
	}
	var stat unix.Stat_t
	if err := unix.Lstat(path, &stat); err != nil || stat.Flags != 0 {
		return Fail(ExitBlocked, "metadata", "Target link flags cannot be preserved")
	}
	return noACL(targetACL(-1, path))
}
