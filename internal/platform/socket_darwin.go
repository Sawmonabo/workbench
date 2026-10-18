package platform

import "golang.org/x/sys/unix"

// peerOf reads the peer's user ID from LOCAL_PEERCRED and its process ID
// from LOCAL_PEERPID: xucred carries no process ID.
func peerOf(fd int) (Credentials, error) {
	cred, err := unix.GetsockoptXucred(fd, unix.SOL_LOCAL, unix.LOCAL_PEERCRED)
	if err != nil {
		return Credentials{}, err
	}
	pid, err := unix.GetsockoptInt(fd, unix.SOL_LOCAL, unix.LOCAL_PEERPID)
	if err != nil {
		return Credentials{}, err
	}
	return Credentials{UID: int(cred.Uid), PID: pid}, nil
}
