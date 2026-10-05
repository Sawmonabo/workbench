//go:build !darwin

package operation

import "net"

// askpassSupported is false off macOS: Linux and WSL ask nothing and make no
// password helper.
const askpassSupported = false

// peerDescendsFrom refuses every peer; nothing listens where askpassSupported is false.
func peerDescendsFrom(*net.UnixConn, int) bool { return false }
