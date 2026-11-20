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
