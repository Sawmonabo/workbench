package platform

import (
	"errors"
	"strings"

	"golang.org/x/sys/unix"
)

// BootID identifies this start of the system: kern.bootsessionuuid, a new
// UUID at each boot that no clock change moves.
func BootID() (string, error) {
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
