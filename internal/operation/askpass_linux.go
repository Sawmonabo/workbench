package operation

import (
	"context"
	"errors"
	"os"
)

// askpassSupported is false off macOS: Linux and WSL ask nothing and make no
// password helper.
const askpassSupported = false

// readPassword is never reached off macOS, where nothing asks for a password.
func readPassword(context.Context, *os.File) (string, error) {
	return "", errors.New("reading a password is only supported on macOS")
}
