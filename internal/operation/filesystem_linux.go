package operation

import (
	"os"
	"path/filepath"
	"syscall"

	"golang.org/x/sys/unix"
)

func isTerminal(file *os.File) bool {
	_, err := unix.IoctlGetTermios(int(file.Fd()), unix.TCGETS)
	return err == nil
}

// Private state must stay on a native Linux filesystem, including under WSL.
// Windows/9P/FUSE/network mounts need separately verified protection semantics.
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
	switch uint64(stat.Type) {
	case 0xef53, 0x58465342, 0x9123683e, 0x01021994, 0x794c7630: // ext, XFS, Btrfs, tmpfs, overlay
		return nil
	default:
		return Fail(3, "filesystem", "Private Workbench state requires a supported native Linux filesystem; Windows/network mounts are unqualified")
	}
}
