package operation

import (
	"net"

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
