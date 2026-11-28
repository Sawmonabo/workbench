package platform

import (
	"errors"
	"strings"

	"golang.org/x/sys/unix"
)

// bootID is kern.bootsessionuuid, a new UUID at each boot that no clock
// change moves.
func bootID() (string, error) {
	id, err := unix.Sysctl("kern.bootsessionuuid")
	if err != nil {
		return "", err
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return "", errors.New("kern.bootsessionuuid is empty")
	}
	return id, nil
}
