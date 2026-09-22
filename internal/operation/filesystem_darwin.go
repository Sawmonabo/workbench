package operation

import (
	"os"
	"path/filepath"
	"syscall"
)

func privateFilesystem(path string) error {
	var stat syscall.Statfs_t
	for {
		err := syscall.Statfs(path, &stat)
		if err == nil {
			break
		}
		if !os.IsNotExist(err) || path == filepath.Dir(path) {
			return Fail(3, "filesystem", "Cannot verify private state filesystem")
		}
		path = filepath.Dir(path)
	}
	name := make([]byte, 0, len(stat.Fstypename))
	for _, char := range stat.Fstypename {
		if char == 0 {
			break
		}
		name = append(name, byte(char))
	}
	if string(name) != "apfs" && string(name) != "hfs" {
		return Fail(3, "filesystem", "Private Workbench state requires APFS or HFS; other protection semantics are unqualified")
	}
	return nil
}
