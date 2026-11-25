package platform

import (
	"bytes"
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

// ErrSwapUnsupported is what SwapNames returns, wrapped with the system's
// errno, when the file system cannot swap two names in one call: EINVAL,
// ENOTSUP, EOPNOTSUPP or ENOSYS, or EXDEV where an overlay file system keeps
// a folder in a lower layer. Nothing was swapped.
var ErrSwapUnsupported = errors.New("the file system cannot swap two names")

// swapError gives the error of a swap of a and b: nil when it swapped,
// ErrSwapUnsupported with the errno when the file system lacks the call or
// its flag, and the errno itself otherwise, each with both names.
func swapError(a, b string, err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, unix.EINVAL) || errors.Is(err, unix.ENOTSUP) ||
		errors.Is(err, unix.EOPNOTSUPP) || errors.Is(err, unix.ENOSYS) ||
		errors.Is(err, unix.EXDEV) {
		err = fmt.Errorf("%w: %w", ErrSwapUnsupported, err)
	}
	return &os.LinkError{Op: "swap", Old: a, New: b, Err: err}
}

// cannotSwapMark is the extended attribute [MarkCannotSwap] leaves on a
// folder whose file system refused to swap it.
const cannotSwapMark = "user.workbench.cannot-swap"

// MarkCannotSwap records on the folder at path that its file system refused
// to swap it (see SwapNames) where CanSwap said it could: an overlay file
// system refuses a folder of an image's lower layer with EXDEV, which no
// call tells without trying. The mark is an extended attribute holding the
// folder's device and inode, so CanSwap reads it without writing anything
// and ignores it on any other folder: one on another device (an overlay
// mounted anew, or a copy on another file system) and a copy on the same
// one, which takes the folder's extended attributes with it (cp -R on
// macOS, cp -a, rsync -X, a restore from a backup). Setting an attribute on
// a folder of an overlay's lower layer copies the folder up to the upper
// layer, which can change the inode stat reports, so the identity it holds
// is the one the folder has once an attribute is written: a mark that names
// another is written once more. It is best effort: a folder that cannot
// take the mark is tried again by the next move.
func MarkCannotSwap(path string) {
	for range 2 {
		var stat unix.Stat_t
		if unix.Lstat(path, &stat) != nil || stat.Mode&unix.S_IFMT != unix.S_IFDIR ||
			unix.Lsetxattr(path, cannotSwapMark, swapMark(&stat), 0) != nil ||
			markedCannotSwap(path) {
			return
		}
	}
}

// swapMark is the mark [MarkCannotSwap] leaves on the folder stat describes:
// its device and inode, in decimal, joined by a colon.
func swapMark(stat *unix.Stat_t) []byte {
	return fmt.Appendf(nil, "%d:%d", uint64(stat.Dev), uint64(stat.Ino))
}

// markedCannotSwap reports whether the folder at path holds the mark
// [MarkCannotSwap] left on it: one naming its device and inode now.
func markedCannotSwap(path string) bool {
	var stat unix.Stat_t
	if unix.Lstat(path, &stat) != nil || stat.Mode&unix.S_IFMT != unix.S_IFDIR {
		return false
	}
	mark := make([]byte, 64)
	n, err := unix.Lgetxattr(path, cannotSwapMark, mark)
	return err == nil && bytes.Equal(mark[:n], swapMark(&stat))
}
