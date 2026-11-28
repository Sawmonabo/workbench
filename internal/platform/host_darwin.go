package platform

import "strings"

// hostRelease runs /usr/bin/sw_vers -productVersion, which prints the
// ProductVersion of SystemVersion.plist, and trims its output. The kernel's
// kern.osproductversion holds the same value, but gives a process whose
// environment sets SYSTEM_VERSION_COMPAT=1 the compatibility version 10.16
// instead on a release that keeps one, and sw_vers runs without that
// variable. A command that fails is not ok.
func hostRelease(
	run func(executable string, args ...string) (string, error),
) (name, version string, ok bool) {
	output, err := run("/usr/bin/sw_vers", "-productVersion")
	if err != nil {
		return "", "", false
	}
	return "macOS", strings.TrimSpace(output), true
}
