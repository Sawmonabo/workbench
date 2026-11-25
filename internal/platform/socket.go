package platform

import (
	"net"
	"os"
)

// SocketPathLimit is the longest Unix socket path every supported system
// takes: macOS holds 104 bytes in a socket address, the closing NUL among
// them, and Linux 108.
const SocketPathLimit = 103

// ListenPrivate listens on a new Unix socket at path that only its owner may
// connect to: the socket file is mode 0600 before the listener is returned.
func ListenPrivate(path string) (*net.UnixListener, error) {
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		return nil, err
	}
	if err = os.Chmod(path, 0o600); err != nil {
		_ = listener.Close()
		return nil, err
	}
	return listener, nil
}

// Credentials are the user and process IDs of the process at the other end
// of a Unix socket, as the kernel recorded them when it connected.
type Credentials struct{ UID, PID int }

// Peer reads the credentials of the process connected to conn.
func Peer(conn *net.UnixConn) (Credentials, error) {
	raw, err := conn.SyscallConn()
	if err != nil {
		return Credentials{}, err
	}
	var peer Credentials
	var peerErr error
	if err = raw.Control(func(fd uintptr) { peer, peerErr = peerOf(int(fd)) }); err != nil {
		return Credentials{}, err
	}
	return peer, peerErr
}
