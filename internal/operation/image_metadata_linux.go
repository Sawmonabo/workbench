package operation

import "golang.org/x/sys/unix"

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
	size, err := unix.Flistxattr(fd, nil)
	if err != nil || size != 0 {
		return Fail(
			3,
			"metadata",
			"Targets with extended attributes or unverified ACLs cannot be preserved",
		)
	}
	return nil
}

func linkMetadata(path string) error {
	if err := privateFilesystem(path); err != nil {
		return err
	}
	size, err := unix.Llistxattr(path, nil)
	if err != nil || size != 0 {
		return Fail(
			3,
			"metadata",
			"Links with extended attributes or unverified ACLs cannot be preserved",
		)
	}
	return nil
}
