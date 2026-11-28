package platform

import (
	"bufio"
	"bytes"
	"errors"
	"os"
	"strconv"
)

// newFolderMode is mode less the Umask of /proc/self/status.
func newFolderMode(mode uint32) (uint32, error) {
	status, err := os.ReadFile("/proc/self/status")
	if err != nil {
		return 0, err
	}
	lines := bufio.NewScanner(bytes.NewReader(status))
	for lines.Scan() {
		value, found := bytes.CutPrefix(lines.Bytes(), []byte("Umask:"))
		if !found {
			continue
		}
		mask, err := strconv.ParseUint(string(bytes.TrimSpace(value)), 8, 32)
		if err != nil {
			return 0, err
		}
		return mode &^ uint32(mask) & 0o777, nil
	}
	return 0, errors.New("/proc/self/status has no Umask line")
}
