package migrate

import (
	"reflect"
	"testing"

	"github.com/pelletier/go-toml/v2"
)

// TestRenamedStepStaysOff guards a step the owner turned off: the risk is
// unauthorized execution, since a saved skip under a name the step no longer
// has would let the step run again unasked. The skip must follow the step
// both when the choices are read before 0006 has run and in the file after.
func TestRenamedStepStaysOff(t *testing.T) {
	const machine = `[data]
email = 'user@example.invalid'
machine_role = 'personal'

[effects]
decided = ['agent-costs-ingest', 'costs-ingest', 'runtimes']
skip = ['costs-ingest', 'runtimes']
`
	if got := RenameSteps([]string{"costs-ingest", "runtimes"}); !reflect.DeepEqual(
		got,
		[]string{"agent-costs-ingest", "runtimes"},
	) {
		t.Fatalf("choices read before the run are %v", got)
	}
	if RenameSteps(nil) != nil {
		t.Fatal("a missing list became an empty one, which reads as decided")
	}
	l := tokenLocations(t, machine)
	if status, _, _ := stepNames.Check(l); status != Pending {
		t.Fatalf("Check = %v, want pending", status)
	}
	runAll(t, l)
	var got, want map[string]any
	if err := toml.Unmarshal(read(t, l.Runtime.MachineConfig()), &got); err != nil {
		t.Fatal(err)
	}
	if err := toml.Unmarshal([]byte(machine), &want); err != nil {
		t.Fatal(err)
	}
	effects, _ := want["effects"].(map[string]any)
	effects["decided"] = []any{"agent-costs-ingest", "runtimes"}
	effects["skip"] = []any{"agent-costs-ingest", "runtimes"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("machine.toml is %v, want only the step name changed: %v", got, want)
	}
	if status, _, _ := stepNames.Check(l); status != Done {
		t.Errorf("Check after the run = %v, want done", status)
	}
}
