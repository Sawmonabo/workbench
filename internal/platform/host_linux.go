package platform

import (
	"os"
	"strings"
)

// hostRelease reads ID and VERSION_ID from /etc/os-release, each with its
// double quotes trimmed, and does not call run. An unreadable file is not ok.
func hostRelease(
	func(executable string, args ...string) (string, error),
) (name, version string, ok bool) {
	data, err := os.ReadFile("/etc/os-release")
	if err != nil {
		return "", "", false
	}
	values := map[string]string{}
	for line := range strings.SplitSeq(string(data), "\n") {
		key, value, found := strings.Cut(line, "=")
		if found {
			values[key] = strings.Trim(value, `"`)
		}
	}
	return values["ID"], values["VERSION_ID"], true
}
