package platform

import "golang.org/x/sys/unix"

// SwapNames swaps the files at a and b, of any kind, in one system call, so
// no moment exists in which either name is missing: renameat2 with
// RENAME_EXCHANGE. Both must exist on one file system. A file system without
// the flag fails it with ErrSwapUnsupported (see swapError).
func SwapNames(a, b string) error {
	return swapError(
		a,
		b,
		unix.Renameat2(unix.AT_FDCWD, a, unix.AT_FDCWD, b, unix.RENAME_EXCHANGE),
	)
}

// CanSwap reports whether the file system holding path, or its first
// existing folder above, can swap two names (see SwapNames), without trying.
// Linux has no call that asks, so it is whether the file system is one
// NativeFilesystem accepts: ext4, XFS, Btrfs, tmpfs and overlayfs each take
// RENAME_EXCHANGE in their rename (overlayfs refuses a folder of a lower
// layer with EXDEV, which SwapNames returns as it is). The error is a statfs
// failure.
func CanSwap(path string) (bool, error) { return NativeFilesystem(path) }
