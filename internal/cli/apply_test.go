package cli

import (
	"testing"

	"github.com/Sawmonabo/workbench/internal/operation"
)

// Unauthorized execution: with --yes, or as a bare apply, a coding agent could
// approve a plan it never read, and the plan's sudo, Homebrew and installer
// steps would then run unasked. checkApply is the gate that runs before any
// setup, so both must be consent refusals (exit 3). --approve-plan, with the
// digest of a plan the agent read, is the agent's route.
func TestAgentApplyIsRefusedBeforeSetup(t *testing.T) {
	t.Setenv("CLAUDECODE", "1")
	for name, o := range map[string]*options{
		"--yes":      {yes: true},
		"bare apply": {},
	} {
		if got := operation.ExitCode(checkApply(o)); got != operation.ExitBlocked {
			t.Errorf("agent running apply %s exited %d, want %d", name, got, operation.ExitBlocked)
		}
	}
}
