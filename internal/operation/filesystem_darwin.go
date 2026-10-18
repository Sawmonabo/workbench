package operation

import "github.com/Sawmonabo/workbench/internal/platform"

func privateFilesystem(path string) error {
	native, err := platform.NativeFilesystem(path)
	if err != nil {
		return Fail(ExitBlocked, "filesystem", "Cannot verify private state filesystem")
	}
	if !native {
		return Fail(
			ExitBlocked,
			"filesystem",
			"Private Workbench state requires APFS or HFS; other protection semantics are unqualified",
		)
	}
	return nil
}
