package platform

import (
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
