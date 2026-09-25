package operation

import (
	"os"
	"path/filepath"
	"syscall"

	"golang.org/x/sys/unix"
)

// IsTerminal reports whether file is a terminal.
func IsTerminal(file *os.File) bool {
	_, err := unix.IoctlGetTermios(int(file.Fd()), unix.TIOCGETA)
	return err == nil
}

func privateFilesystem(path string) error {
	var stat syscall.Statfs_t
	for {
		err := syscall.Statfs(path, &stat)
		if err == nil {
			break
		}
		if !os.IsNotExist(err) || path == filepath.Dir(path) {
			return Fail(ExitBlocked, "filesystem", "Cannot verify private state filesystem")
		}
		path = filepath.Dir(path)
	}
	// Fstypename is a NUL-terminated ASCII name stored as int8.
	name := make([]byte, 0, len(stat.Fstypename))
	for _, char := range stat.Fstypename {
		if char == 0 {
			break
		}
		name = append(name, byte(char))
	}
	if string(name) != "apfs" && string(name) != "hfs" {
		return Fail(
			ExitBlocked,
			"filesystem",
			"Private Workbench state requires APFS or HFS; other protection semantics are unqualified",
		)
	}
	return nil
}
