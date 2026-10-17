package machine

import (
	"context"
	"fmt"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/Sawmonabo/workbench/internal/operation"
)

// hostVersion completes the platform check with the macOS version sw_vers
// reports: macOS 15 or later is supported. A failed sw_vers leaves result as
// it is.
func hostVersion(ctx context.Context, c operation.Context, result *operation.Component) {
	output, err := operation.Run(
		ctx,
		c,
		nil,
		operation.Process{
			Executable:  "/usr/bin/sw_vers",
			Args:        []string{"-productVersion"},
			Directory:   "/",
			Environment: []string{"PATH=/usr/bin:/bin"},
			Timeout:     10 * time.Second,
		},
	)
	if err != nil {
		return
	}
	major, _ := strconv.Atoi(strings.Split(strings.TrimSpace(output.Stdout), ".")[0])
	result.Message = fmt.Sprintf(
		"macOS %s/%s; native provisioning qualification still required",
		strings.TrimSpace(output.Stdout),
		runtime.GOARCH,
	)
	if major >= 15 {
		result.Status = operation.StatusComplete
	}
}
