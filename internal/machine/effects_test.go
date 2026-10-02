package machine

import (
	"testing"

	"github.com/Sawmonabo/workbench/internal/operation"
)

// The no-prompt apply runs the saved selection without asking, so an effect the
// owner skipped must stay unchecked however it was decided: checking it would
// run provisioning (sudo, installers) the owner declined, with no prompt left
// to stop it.
func TestSavedSelectionNeverChecksASkippedEffect(t *testing.T) {
	saved := Selection{
		Skip:    []string{"runtimes"},
		Decided: []string{"runtimes", "global-tools"},
	}
	effects := applySelection(
		[]operation.Effect{{Name: "runtimes"}, {Name: "global-tools"}},
		saved,
		saved,
	)
	if effects[0].Checked {
		t.Fatal("a skipped effect was checked")
	}
	if !effects[1].Checked || effects[1].New {
		t.Fatal("a decided effect was not kept as chosen")
	}
}
