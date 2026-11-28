package machine

import (
	"context"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/Sawmonabo/workbench/internal/operation"
	"github.com/Sawmonabo/workbench/internal/platform"
)

// supportNote is, by system (platform.System), what the platform check adds
// to its line for a release Workbench does not support.
var supportNote = map[string]string{
	"darwin": "; Workbench supports macOS 15 or later",
	"linux":  "; Workbench supports Ubuntu 22.04, 24.04 and 26.04 and AlmaLinux 10",
}

// hostVersion completes the platform check with the release
// platform.HostRelease names (see supportedRelease). On WSL it adds that the
// Windows steps have not been checked on a real host. A release that cannot be
// learned, such as a failed sw_vers or an unreadable /etc/os-release, leaves
// result as it is.
func hostVersion(ctx context.Context, c operation.Context, result *operation.Component) {
	name, version, ok := platform.HostRelease(
		func(executable string, args ...string) (string, error) {
			output, err := operation.Run(
				ctx,
				c,
				nil,
				operation.Process{
					Executable:  executable,
					Args:        args,
					Directory:   "/",
					Environment: []string{"PATH=/usr/bin:/bin"},
					Timeout:     10 * time.Second,
				},
			)
			return output.Stdout, err
		},
	)
	if !ok {
		return
	}
	result.Message = name + " " + version + " on " + runtime.GOARCH
	if supportedRelease(platform.System, name, version) {
		result.Status = operation.StatusComplete
	} else {
		result.Message += supportNote[platform.System]
	}
	if platform.WSL() {
		result.Message += "; WSL: the Windows steps have not been checked on a real Windows host"
	}
}

// supportedRelease reports whether Workbench supports the release
// platform.HostRelease names on system (platform.System): macOS 15 or later on
// darwin, and Ubuntu 22.04, 24.04 and 26.04 and AlmaLinux 10 on linux. A
// release name counts only on its own system, because on Linux it is whatever
// ID /etc/os-release holds: one that says macOS is no supported Mac. AlmaLinux
// is checked by its major version only, since it ships a new 10.x minor
// release every six months.
func supportedRelease(system, name, version string) bool {
	major, _, _ := strings.Cut(version, ".")
	switch {
	case system == "darwin" && name == "macOS":
		number, _ := strconv.Atoi(major)
		return number >= 15
	case system == "linux" && name == "ubuntu":
		return slices.Contains([]string{"22.04", "24.04", "26.04"}, version)
	case system == "linux" && name == "almalinux":
		return major == "10"
	}
	return false
}
