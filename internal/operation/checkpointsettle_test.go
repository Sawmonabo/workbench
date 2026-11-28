package operation

import (
	"context"
	"maps"
	"os"
	"path/filepath"
	"testing"

	"github.com/Sawmonabo/workbench/internal/platform"
)

// Prevent a silent permission change and lost data when Workbench is killed
// between chezmoi's write and the pass after it, and the apply is then run
// again: the rerun finds the file as the configuration renders it, so it has
// nothing to write, and settling the stopped apply without that pass would
// leave chezmoi's file without the ACL, extended attributes and hidden flag
// the replaced one had, with nothing but the stopped apply's checkpoint left
// to hold them. Settling must give them back first, and revert of that
// checkpoint must still restore the file as it was.
func TestSettlingAnInterruptedApplyKeepsACLAttributesAndFlag(t *testing.T) {
	c := carryContext(t)
	path := filepath.Join(c.Scope.Root, "config")
	giveMetadata(t, path)
	changes, was := nativeChange(t, c, path)
	id := withNative(t, c, changes, func(*Checkpoint) error {
		nativeWrite(t, c, path, changes[0].After)
		return nil
	})
	if written := readHeld(t, path); written.acl != nil || written.hidden ||
		written.attributes[noteAttribute] != nil {
		t.Fatal("chezmoi's file kept metadata, so settling proves nothing")
	}

	plan := Plan{
		Source:   SourceIdentity{Release: "fixture", ContentDigest: ChangesDigest(nil)},
		Scope:    c.Scope,
		Complete: true,
	}
	state := &State{SchemaVersion: 1, PartialOperation: &PartialOperation{ID: id, Scope: c.Scope}}
	if err := WithMutation(
		context.Background(),
		c,
		plan,
		Consent{ApprovedDigest: plan.Digest(), CompleteInputs: true},
		func(context.Context, Context) (Plan, error) { return plan, nil },
		func(m *Mutation) error {
			settled, err := SettleUnfinished(m, state, nil)
			if err == nil && (settled == nil || settled.ID != id) {
				t.Fatalf("settled %+v, not apply %s", settled, id)
			}
			return err
		},
	); err != nil {
		t.Fatal(err)
	}

	want := was
	want.attributes = maps.Clone(was.attributes)
	want.mode = changes[0].After.Mode
	if platform.ACLsAreAttributes {
		want.attributes[platform.AccessACLAttribute] = chmodACL(
			t,
			c,
			was.attributes[platform.AccessACLAttribute],
			want.mode,
		)
	}
	want.expect(t, "the file the settled apply wrote", readHeld(t, path))
	revertCheckpointed(t, c, id)
	if data, err := os.ReadFile(path); err != nil || string(data) != "old" {
		t.Fatalf("revert left %q, %v", data, err)
	}
	was.expect(t, "the restored file", readHeld(t, path))
}
