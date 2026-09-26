package operation

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// Prevent irreversible user-data loss: a later edit anywhere in the full set
// must prevent all restoration, and tampered scope/evidence must never authorize
// writes. These are shared mutation-boundary checks, not lifecycle coverage.
func TestRecoveryPreservesUserDataAndEvidence(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	c := Context{
		Paths: Paths{
			State:  filepath.Join(root, "state"),
			Config: filepath.Join(root, "config"),
			Data:   filepath.Join(root, "data"),
			Cache:  filepath.Join(root, "cache"),
			Bin:    filepath.Join(root, "bin"),
		},
		Scope: Scope{Kind: "project", Root: filepath.Join(root, "project")},
	}
	if err = os.Mkdir(c.Scope.Root, 0o700); err != nil {
		t.Fatal(err)
	}
	first, second := filepath.Join(c.Scope.Root, "a"), filepath.Join(c.Scope.Root, "b")
	before := Image{Kind: ImageFile, Mode: 0o640, Data: []byte("original")}
	after := Image{Kind: ImageFile, Mode: 0o640, Data: []byte("applied")}
	group := os.Getegid()
	groups, err := os.Getgroups()
	if err != nil {
		t.Fatal(err)
	}
	for _, candidate := range groups {
		if candidate != group {
			group = candidate
			break
		}
	}
	for _, path := range []string{first, second} {
		if err = os.WriteFile(path, before.Data, 0o640); err != nil {
			t.Fatal(err)
		}
		// A replace must retain a readable group's identity, not silently inherit
		// the parent's group and expose the configuration to different readers.
		if err = os.Chown(path, -1, group); err != nil {
			t.Fatal(err)
		}
	}
	before, err = ReadImage(c, first)
	if err != nil {
		t.Fatal(err)
	}
	after.Attributes = before.Attributes
	after.Group = before.Group
	changes := []TargetChange{
		{Path: first, Before: before, After: after},
		{Path: second, Before: before, After: after},
	}
	plan := Plan{
		Source:   SourceIdentity{Release: "fixture", ContentDigest: ImageDigest(before)},
		Scope:    c.Scope,
		Complete: true,
		Inputs:   []Input{{Name: "checkpoint-images", Digest: ChangesDigest(changes)}},
	}
	digest := plan.Digest()
	id := ""
	err = WithMutation(
		context.Background(),
		c,
		plan,
		Consent{ApprovedDigest: digest, CompleteInputs: true},
		func(context.Context, Context) (Plan, error) { return plan, nil },
		func(m *Mutation) error {
			cp, beginErr := BeginCheckpoint(m, plan, nil, changes)
			if beginErr != nil {
				return beginErr
			}
			id = cp.ID
			return cp.Apply(context.Background())
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(second, []byte("later user edit"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err = RecoveryPlan(c, RecoverySelector{Checkpoint: id})
	if ExitCode(err) != 4 {
		t.Fatalf("expected recovery conflict, got %v", err)
	}
	data, err := os.ReadFile(first)
	if err != nil || string(data) != "applied" {
		t.Fatal("conflicting recovery changed another target")
	}
	if err = os.WriteFile(second, after.Data, 0o600); err != nil {
		t.Fatal(err)
	}
	// A checkpoint image corrupted after preview must stop before any writes.
	recovery, err := RecoveryPlan(c, RecoverySelector{Checkpoint: id})
	if err != nil {
		t.Fatal(err)
	}
	digest = recovery.Digest()
	imagePath := filepath.Join(checkpointScope(c), id, "images", ImageDigest(before), "00.part")
	if err = os.WriteFile(imagePath, []byte("corrupt"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err = Recover(
		context.Background(),
		c,
		recovery,
		RecoverySelector{Checkpoint: id},
		Consent{ApprovedDigest: digest, CompleteInputs: true},
	)
	if err == nil {
		t.Fatal("corrupted recovery evidence authorized writes")
	}
	data, err = os.ReadFile(first)
	if err != nil || string(data) != "applied" {
		t.Fatal("corrupted checkpoint changed target")
	}
	// Escape rejection shares the exact target reader/writer boundary.
	if err = os.Symlink(root, filepath.Join(c.Scope.Root, "escape")); err != nil {
		t.Fatal(err)
	}
	if _, err = ReadImage(c, filepath.Join(c.Scope.Root, "escape", "outside")); err == nil {
		t.Fatal("escaping target ancestry accepted")
	}
}

// Prevent loss of Workbench's state and the user's files: a checkpoint may
// change only the mode of a folder that holds Workbench's own files, such as
// ~/.config on Linux. Removing or replacing it, or letting other users write
// to it, must be refused before anything is written.
func TestCheckpointChangesOnlyTheModeOfWorkbenchFolders(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(root, "home")
	config := filepath.Join(home, ".config")
	c := Context{
		Paths: Paths{
			Config: filepath.Join(config, "workbench"),
			State:  filepath.Join(root, "state"),
			Data:   filepath.Join(root, "data"),
			Cache:  filepath.Join(root, "cache"),
			Bin:    filepath.Join(root, "bin"),
		},
		Scope: Scope{Kind: "machine", Root: home},
	}
	for _, directory := range []string{home, config, c.Paths.Config} {
		if err = os.Mkdir(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	before, err := ReadImage(c, config)
	if err != nil {
		t.Fatal(err)
	}
	open, loose := before, before
	open.Mode, loose.Mode = 0o755, 0o775
	file := Image{Kind: ImageFile, Mode: 0o644, Data: []byte("replaced"), Group: before.Group}
	for name, after := range map[string]Image{
		"removal":     {Kind: ImageAbsent},
		"replacement": file,
		"group write": loose,
	} {
		if validateChanges(c, []TargetChange{{Path: config, Before: before, After: after}}) == nil {
			t.Errorf("%s of a folder holding Workbench's files was allowed", name)
		}
	}
	if err = validateChanges(
		c,
		[]TargetChange{{Path: config, Before: before, After: open}},
	); err != nil {
		t.Errorf("mode-only change was refused: %v", err)
	}
}
