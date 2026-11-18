package machine

import (
	"context"
	"os"
	"runtime"
	"slices"
	"strings"

	"github.com/Sawmonabo/workbench/internal/operation"
	"github.com/Sawmonabo/workbench/internal/platform"
)

// hostVersion completes the platform check with the distribution
// /etc/os-release names: Ubuntu 22.04, 24.04 and 26.04 and AlmaLinux 10 are
// supported. AlmaLinux is checked by its major version only, since it ships a
// new 10.x minor release every six months. On WSL it adds that the Windows
// steps have not been checked on a real host. An unreadable file leaves
// result as it is.
func hostVersion(_ context.Context, _ operation.Context, result *operation.Component) {
	data, err := os.ReadFile("/etc/os-release")
	if err != nil {
		return
	}
	values := map[string]string{}
	for line := range strings.SplitSeq(string(data), "\n") {
		key, value, ok := strings.Cut(line, "=")
		if ok {
			values[key] = strings.Trim(value, `"`)
		}
	}
	major, _, _ := strings.Cut(values["VERSION_ID"], ".")
	ubuntu := values["ID"] == "ubuntu" &&
		slices.Contains([]string{"22.04", "24.04", "26.04"}, values["VERSION_ID"])
	almalinux := values["ID"] == "almalinux" && major == "10"
	result.Message = values["ID"] + " " + values["VERSION_ID"] + " on " + runtime.GOARCH
	if ubuntu || almalinux {
		result.Status = operation.StatusComplete
	} else {
		result.Message += "; Workbench supports Ubuntu 22.04, 24.04 and 26.04 and AlmaLinux 10"
	}
	if platform.WSL() {
		result.Message += "; WSL: the Windows steps have not been checked on a real Windows host"
	}
}
