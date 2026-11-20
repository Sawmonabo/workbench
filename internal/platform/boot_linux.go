package platform

import (
	"errors"
	"os"
	"strings"
)

// BootID identifies this start of the system: the kernel's
// /proc/sys/kernel/random/boot_id, a new UUID at each boot that no clock
// change moves.
func BootID() (string, error) {
	data, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if err != nil {
		return "", err
	}
	id := strings.TrimSpace(string(data))
	if id == "" {
		return "", errors.New("/proc/sys/kernel/random/boot_id is empty")
	}
	return id, nil
}
