package platform

import (
	"context"
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

// termiosRequest is the ioctl request that reads a terminal's settings.
const termiosRequest = unix.TCGETS

// readPassword always fails: Workbench asks for no password on Linux
// (readsPasswords).
func readPassword(context.Context, *os.File) (string, error) {
	return "", errors.New("reading a password is only supported on macOS")
}
