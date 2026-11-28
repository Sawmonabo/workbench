package platform

import (
	"context"
	"os"

	"golang.org/x/sys/unix"
)

// IsTerminal reports whether fd is a terminal: whether its terminal settings
// can be read.
func IsTerminal(fd uintptr) bool {
	_, err := unix.IoctlGetTermios(int(fd), termiosRequest)
	return err == nil
}

// TerminalSize is the width and height, in cells, the terminal at fd
// reports. A pseudo-terminal opened with no size reports 0 for both.
func TerminalSize(fd uintptr) (width, height int, err error) {
	size, err := unix.IoctlGetWinsize(int(fd), unix.TIOCGWINSZ)
	if err != nil {
		return 0, 0, err
	}
	return int(size.Col), int(size.Row), nil
}

// ReadPassword reads a line a person types at terminal with echo off, ending
// with ctx, and keeps at most its first 1024 bytes, without the newline. It
// never leaves a read waiting, echo is restored however it ends, and the
// terminal stays canonical, so ctrl+c still signals. Where ReadsPasswords is
// false, as on Linux, it always fails.
func ReadPassword(ctx context.Context, terminal *os.File) (string, error) {
	return readPassword(ctx, terminal)
}
