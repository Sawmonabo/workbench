package operation

import (
	"github.com/Sawmonabo/workbench/internal/display"
	"github.com/Sawmonabo/workbench/internal/platform"
)

// foreignFilesystem is, by system (platform.System), the refusal
// privateFilesystem gives, after the path, for a path that is not on a native
// file system.
var foreignFilesystem = map[string]string{
	"darwin": " is on a file system other than APFS or HFS, the only ones whose permissions Workbench has checked; move it onto one and retry",
	"linux":  " is on a file system other than ext, XFS, Btrfs, tmpfs or overlay, such as a Windows or network mount, and those are the only ones whose permissions Workbench has checked; move it onto one and retry",
}

// privateFilesystem refuses a path for private state, or a target or its
// folder, off a file system native to this system (platform.NativeFilesystem),
// including under WSL: Windows, 9P, FUSE and network mounts need separately
// verified protection semantics.
func privateFilesystem(path string) error {
	native, err := platform.NativeFilesystem(path)
	if err != nil {
		return Fail(
			ExitBlocked,
			"filesystem",
			"Cannot tell which file system "+display.Path(path)+" is on: "+err.Error(),
		)
	}
	if !native {
		return Fail(
			ExitBlocked,
			"filesystem",
			display.Path(path)+foreignFilesystem[platform.System],
		)
	}
	return nil
}
