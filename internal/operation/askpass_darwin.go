package operation

import (
	"context"
	"errors"
	"io"
	"os"

	"golang.org/x/sys/unix"
)

// askpassSupported says the apply asks for the Mac password here and makes the
// password helper, kept to the apply's own processes by the ancestry of
// whichever process connects to its socket; elsewhere no helper is made.
const askpassSupported = true

// passwordLimit bounds what readPassword keeps of one typed line.
const passwordLimit = 1024

// readPassword reads a line from terminal with echo off, ending with ctx. It
// never leaves a read waiting: it polls for a typed line and reads only once one
// is there. A read left blocked on the terminal would make the terminal's close,
// when the apply ends, wait for that read in an uninterruptible state, so a
// ctrl+c at the prompt would hang until someone pressed return. Echo is restored
// however it ends; the terminal stays canonical, so ctrl+c still signals.
func readPassword(ctx context.Context, terminal *os.File) (string, error) {
	fd := int(terminal.Fd())
	saved, err := unix.IoctlGetTermios(fd, unix.TIOCGETA)
	if err != nil {
		return "", err
	}
	quiet := *saved
	quiet.Lflag &^= unix.ECHO
	quiet.Lflag |= unix.ICANON | unix.ISIG
	quiet.Iflag |= unix.ICRNL
	if err = unix.IoctlSetTermios(fd, unix.TIOCSETA, &quiet); err != nil {
		return "", err
	}
	defer func() { _ = unix.IoctlSetTermios(fd, unix.TIOCSETA, saved) }()
	var line []byte
	chunk := make([]byte, 256)
	for {
		if err = ctx.Err(); err != nil {
			return "", err
		}
		ready, pollErr := unix.Poll([]unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}, 100)
		if errors.Is(pollErr, unix.EINTR) || ready == 0 {
			continue
		}
		if pollErr != nil {
			return "", pollErr
		}
		n, readErr := unix.Read(fd, chunk)
		switch {
		case errors.Is(readErr, unix.EINTR) || errors.Is(readErr, unix.EAGAIN):
			continue
		case readErr != nil:
			return "", readErr
		case n == 0:
			return "", io.EOF
		}
		for _, b := range chunk[:n] {
			if b == '\n' {
				return string(line), nil
			}
			if len(line) < passwordLimit {
				line = append(line, b)
			}
		}
	}
}
