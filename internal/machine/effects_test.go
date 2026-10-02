package machine

import (
	"slices"
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

// Windows setup forces its parts on while it is checked. If that force were
// saved as the owner's choice, turning Windows setup off later would still
// replace the Terminal and PowerShell files the owner never picked on their own.
// The saved selection keeps only what the owner chose for each part. Optional
// effects exist only on a WSL host, so the check runs there.
func TestWindowsPartsForcedByTheirParentAreNotSavedAsChosen(t *testing.T) {
	if !isWSL() {
		t.Skip("optional effects exist only on WSL")
	}
	chosen := Selection{Select: []string{"font-registry"}}
	effects := applySelection(
		[]operation.Effect{
			{Name: "windows-files"},
			{Name: "terminal-adoption", Parent: "windows-files"},
			{Name: "font-registry", Parent: "windows-files"},
		},
		chosen,
		chosen,
	)
	for _, effect := range effects {
		if !effect.Checked {
			t.Fatalf("%s is not included while Windows setup is on", effect.Name)
		}
	}
	saved := selectionToSave(effects, chosen, chosen)
	if !slices.Equal(saved.Select, []string{"font-registry"}) {
		t.Fatalf("saved selection %v, want only the part the owner chose", saved.Select)
	}
}
