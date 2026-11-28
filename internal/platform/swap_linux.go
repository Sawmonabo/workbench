package platform

import "golang.org/x/sys/unix"

// swapNames is renameat2 with RENAME_EXCHANGE. A file system without the
// flag fails it with ErrSwapUnsupported (see swapError).
func swapNames(a, b string) error {
	return swapError(
		a,
		b,
		unix.Renameat2(unix.AT_FDCWD, a, unix.AT_FDCWD, b, unix.RENAME_EXCHANGE),
	)
}

// canSwap reports whether the file system holding path, or its first
// existing folder above, is one NativeFilesystem accepts: Linux has no call
// that asks, and ext4, XFS, Btrfs, tmpfs and overlayfs each take
// RENAME_EXCHANGE in their rename. overlayfs refuses a folder of a lower
// layer with EXDEV, which SwapNames returns as ErrSwapUnsupported and which
// only trying tells: a folder [MarkCannotSwap] marked after such a refusal
// cannot swap. The error is a statfs failure.
func canSwap(path string) (bool, error) {
	return nativeFilesystem(path)
}
