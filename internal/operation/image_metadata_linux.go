package operation

import "golang.org/x/sys/unix"

// preservedAttributes is empty: fileMetadata blocks any extended attribute.
var preservedAttributes []string

func replaceImage(fd int, from, to string, absent bool) error {
	if absent {
		return unix.Renameat2(fd, from, fd, to, unix.RENAME_NOREPLACE)
	}
	return unix.Renameat(fd, from, fd, to)
}

func fileMetadata(fd int, path string) error {
	if err := privateFilesystem(path); err != nil {
		return err
	}
	size, err := attributeNames(
		func(names []byte) (int, error) { return unix.Flistxattr(fd, names) },
	)
	if err != nil || size != 0 {
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
	size, err := attributeNames(
		func(names []byte) (int, error) { return unix.Llistxattr(path, names) },
	)
	if err != nil || size != 0 {
		return Fail(
			ExitBlocked,
			"metadata",
			"Links with extended attributes or unverified ACLs cannot be preserved",
		)
	}
	return nil
}

// attributeNames is the length of a file's extended attribute name list as
// list reads it. A call without a buffer only bounds that length: on
// overlayfs it also counts the trusted.overlay.* names the read leaves out,
// so the names are read into a buffer that size. A list that grew in between
// fails with ERANGE.
func attributeNames(list func(names []byte) (int, error)) (int, error) {
	size, err := list(nil)
	if err != nil || size == 0 {
		return size, err
	}
	return list(make([]byte, size))
}
