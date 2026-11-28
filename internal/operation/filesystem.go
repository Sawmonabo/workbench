package operation

import (
	"github.com/Sawmonabo/workbench/internal/display"
	"github.com/Sawmonabo/workbench/internal/platform"
)

// nativeFilesystems is, by system (platform.System), the file systems
// privateFilesystem admits, as its refusal of a path on any other names them,
// and why it admits no other.
var nativeFilesystems = map[string]struct{ names, cause string }{
	"darwin": {"APFS or HFS", "those are the only ones whose permissions Workbench has checked"},
	"linux": {
		"ext, XFS, Btrfs, tmpfs or overlay",
		"those are the only ones whose permissions Workbench has checked, and no Windows or network mount is one",
	},
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
			"Cannot tell which file system "+display.Path(path)+" is on",
			Cause(err.Error()),
		)
	}
	if !native {
		admitted := nativeFilesystems[platform.System]
		return Fail(
			ExitBlocked,
			"filesystem",
			display.Path(path)+" is on a file system other than "+admitted.names,
			Cause(admitted.cause),
			FixText("move it onto one of those"),
		)
	}
	return nil
}
