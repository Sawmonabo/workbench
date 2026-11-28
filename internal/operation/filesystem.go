package operation

import "github.com/Sawmonabo/workbench/internal/platform"

// foreignFilesystem is, by system (platform.System), the refusal
// privateFilesystem gives for a path that is not on a native file system.
var foreignFilesystem = map[string]string{
	"darwin": "Private Workbench state requires APFS or HFS; other protection semantics are unqualified",
	"linux":  "Private Workbench state requires a supported native Linux filesystem; Windows/network mounts are unqualified",
}

// privateFilesystem refuses a path for private state off a file system native
// to this system (platform.NativeFilesystem), including under WSL: Windows,
// 9P, FUSE and network mounts need separately verified protection semantics.
func privateFilesystem(path string) error {
	native, err := platform.NativeFilesystem(path)
	if err != nil {
		return Fail(ExitBlocked, "filesystem", "Cannot verify private state filesystem")
	}
	if !native {
		return Fail(ExitBlocked, "filesystem", foreignFilesystem[platform.System])
	}
	return nil
}
