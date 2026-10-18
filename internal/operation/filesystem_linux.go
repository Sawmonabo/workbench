package operation

import "github.com/Sawmonabo/workbench/internal/platform"

// Private state must stay on a native Linux filesystem, including under WSL.
// Windows/9P/FUSE/network mounts need separately verified protection semantics.
func privateFilesystem(path string) error {
	native, err := platform.NativeFilesystem(path)
	if err != nil {
		return Fail(ExitBlocked, "filesystem", "Cannot verify private state filesystem")
	}
	if !native {
		return Fail(
			ExitBlocked,
			"filesystem",
			"Private Workbench state requires a supported native Linux filesystem; Windows/network mounts are unqualified",
		)
	}
	return nil
}
