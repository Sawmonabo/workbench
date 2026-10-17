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
// /etc/os-release names: Ubuntu 22.04, 24.04 and 26.04 are supported. On WSL
// it adds that the Windows host steps are not yet qualified. An unreadable
// file leaves result as it is.
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
	if values["ID"] == "ubuntu" &&
		slices.Contains([]string{"22.04", "24.04", "26.04"}, values["VERSION_ID"]) {
		result.Status = operation.StatusComplete
	}
	result.Message = values["ID"] + " " + values["VERSION_ID"] + "/" + runtime.GOARCH
	if platform.WSL() {
		result.Message += "; WSL: Windows host steps are not yet qualified on a real host"
	}
}
