package platform

import "golang.org/x/sys/unix"

// peerOf reads the peer's user and process IDs from SO_PEERCRED.
func peerOf(fd int) (Credentials, error) {
	cred, err := unix.GetsockoptUcred(fd, unix.SOL_SOCKET, unix.SO_PEERCRED)
	if err != nil {
		return Credentials{}, err
	}
	return Credentials{UID: int(cred.Uid), PID: int(cred.Pid)}, nil
}
