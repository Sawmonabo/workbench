package cli

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/Sawmonabo/workbench/internal/operation"
)

// ConfirmPlan is shared terminal consent for future mutation handlers. Automation
// and JSON callers leave Consent.Confirm nil and supply ApprovedDigest instead.
// Opening /dev/tty requires a controlling terminal; a pipe never approves a plan.
func ConfirmPlan(plan operation.Plan, digest string) (bool, error) {
	terminal, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return false, operation.Fail(3, "consent", "No terminal is available; supply complete inputs and --approve-plan")
	}
	defer func() { _ = terminal.Close() }()
	if err := json.NewEncoder(terminal).Encode(plan); err != nil {
		return false, err
	}
	if _, err := fmt.Fprintf(terminal, "Plan digest: %s\nApprove this exact plan? Type yes: ", digest); err != nil {
		return false, err
	}
	answer, err := bufio.NewReaderSize(terminal, 128).ReadSlice('\n')
	if err != nil {
		return false, operation.Fail(3, "consent", "No complete approval was received")
	}
	return strings.TrimSpace(string(answer)) == "yes", nil
}
