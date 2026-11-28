package operation

import (
	"context"
	"errors"
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
		func(m *Mutation) error { return settle(t, m, state, id) },
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

// Prevent lost data when a file chezmoi wrote is changed before the stopped
// apply is settled: someone edits it in place after Workbench was killed,
// so it holds neither the image the apply approved nor the one it replaced,
// and lacks the ACL, extended attributes and hidden flag the replaced file
// had and the apply's checkpoint alone still holds. Settling must give them
// to that file itself, the same inode, and leave the edit in it as it is: a
// file written anew with the approved content would lose the edit, and one
// left without them loses them once the record of the apply goes. Revert of
// that apply then refuses the edited file, and the fix it names must keep
// the edit: moved aside, the edit stays whole and revert restores the file
// the apply replaced.
func TestSettlingKeepsAnEditAndGivesBackItsMetadata(t *testing.T) {
	c := carryContext(t)
	path := filepath.Join(c.Scope.Root, "config")
	giveMetadata(t, path)
	changes, was := nativeChange(t, c, path)
	id := withNative(t, c, changes, func(*Checkpoint) error {
		nativeWrite(t, c, path, changes[0].After)
		return nil
	})
	const edit = "new, and a line someone added"
	if err := os.WriteFile(path, []byte(edit), 0); err != nil {
		t.Fatal(err)
	}
	edited := readHeld(t, path)
	if edited.acl != nil || edited.hidden || edited.attributes[noteAttribute] != nil {
		t.Fatal("the edited file kept metadata, so settling proves nothing")
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
		func(m *Mutation) error { return settle(t, m, state, id) },
	); err != nil {
		t.Fatal(err)
	}

	if data, err := os.ReadFile(path); err != nil || string(data) != edit {
		t.Fatalf("settling left %q, %v, not the edit", data, err)
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
	got := readHeld(t, path)
	want.expect(t, "the edited file", got)
	if got.inode != edited.inode {
		t.Fatal("settling wrote the edited file anew instead of giving it its metadata")
	}

	// Revert of that apply refuses the edited file, and its fix, the file
	// moved aside, lets it restore the earlier file and keeps the edit.
	_, err := RecoveryPlan(c, RecoverySelector{Checkpoint: id})
	problem, ok := errors.AsType[*Error](err)
	aside := path + ".edited"
	move := "mv -n " + c.ShellPath(path) + " " + c.ShellPath(aside)
	if !ok || len(problem.Fix) != 2 || problem.Fix[0] != move {
		t.Fatalf("revert of the edited file: %v, not the fix %q", err, move)
	}
	if err = os.Rename(path, aside); err != nil {
		t.Fatal(err)
	}
	revertCheckpointed(t, c, id)
	if data, readErr := os.ReadFile(path); readErr != nil || string(data) != "old" {
		t.Fatalf("revert left %q, %v", data, readErr)
	}
	was.expect(t, "the restored file", readHeld(t, path))
	if data, readErr := os.ReadFile(aside); readErr != nil || string(data) != edit {
		t.Fatalf("the edit moved aside holds %q, %v", data, readErr)
	}
	want.expect(t, "the edit moved aside", readHeld(t, aside))
}

// settle settles, under m, the apply id that state records as unfinished,
// as PlanUnfinished finds it.
func settle(t *testing.T, m *Mutation, state *State, id string) error {
	t.Helper()
	unfinished, err := PlanUnfinished(m.context, state)
	if err != nil {
		return err
	}
	settled, err := SettleUnfinished(m, unfinished)
	if err == nil && (settled == nil || settled.ID != id || len(settled.Finished) != 1) {
		t.Fatalf("settled %+v, not the file of apply %s", settled, id)
	}
	return err
}
