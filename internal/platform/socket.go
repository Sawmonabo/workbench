package platform

import "net"

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
