package operation

import (
	"context"
	"errors"
	"io"
	"net"
	"os"

	"golang.org/x/sys/unix"
)

// askpassSupported says this system can tell which process connected to the
// password helper's socket, which is how the helper is kept to the apply's own
// processes; elsewhere no helper is made.
const askpassSupported = true

// askpassAncestry bounds the walk up a process tree, far deeper than any real one.
const askpassAncestry = 256

// peerDescendsFrom reports whether the process that connected to conn was started,
// directly or not, by the process ancestor, and is not ancestor itself. It reads
// the peer's process ID from the socket (LOCAL_PEERPID) and walks parent process
// IDs from the kernel's process table. A peer that cannot be found, such as one
// that already exited, or whose parent chain reaches launchd first, is refused.
func peerDescendsFrom(conn *net.UnixConn, ancestor int) bool {
	raw, err := conn.SyscallConn()
	if err != nil {
		return false
	}
	pid, sockErr := 0, error(nil)
	if err = raw.Control(func(fd uintptr) {
		pid, sockErr = unix.GetsockoptInt(int(fd), unix.SOL_LOCAL, unix.LOCAL_PEERPID)
	}); err != nil || sockErr != nil {
		return false
	}
	for range askpassAncestry {
		if pid <= 1 {
			return false
		}
		info, infoErr := unix.SysctlKinfoProc("kern.proc.pid", pid)
		if infoErr != nil {
			return false
		}
		pid = int(info.Eproc.Ppid)
		if pid == ancestor {
			return true
		}
	}
	return false
}

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
