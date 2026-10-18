package operation

import "github.com/Sawmonabo/workbench/internal/platform"

// preservedAttributes is empty: fileMetadata blocks any extended attribute.
var preservedAttributes []string

func fileMetadata(fd int, path string) error {
	if err := privateFilesystem(path); err != nil {
		return err
	}
	count, err := platform.ExtendedAttributeCount(fd, "")
	if err != nil || count != 0 {
		return Fail(
			ExitBlocked,
			"metadata",
			"Targets with extended attributes or unverified ACLs cannot be preserved",
		)
	}
	return nil
}

// containerMetadata is fileMetadata: Linux admits no extra metadata on a
// folder that holds Workbench's own files.
func containerMetadata(fd int, path string) error { return fileMetadata(fd, path) }

// parentMetadata applies the target rules to the folder a target is written
// into: any extended attribute there blocks planning.
func parentMetadata(fd int, path string) error { return fileMetadata(fd, path) }

func linkMetadata(path string) error {
	if err := privateFilesystem(path); err != nil {
		return err
	}
	count, err := platform.ExtendedAttributeCount(-1, path)
	if err != nil || count != 0 {
		return Fail(
			ExitBlocked,
			"metadata",
			"Links with extended attributes or unverified ACLs cannot be preserved",
		)
	}
	return nil
}
