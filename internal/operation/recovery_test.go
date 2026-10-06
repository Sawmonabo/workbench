package operation

import (
	"context"
	"os"
	"path/filepath"
	"strings"
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

// Prevent a new group write from reaching a file the person changed. After a
// native run that failed (a script exited nonzero), Workbench puts the approved
// group back on each file that already holds exactly its approved content, so
// the checkpoint records it as written. A file whose content differs, such as
// one edited meanwhile, must keep its bytes and its group: Workbench never
// verified it, and a later apply or revert would act on it as if it had.
func TestSettleFailedNativeLeavesDifferingContentAlone(t *testing.T) {
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
	approved, other := os.Getegid(), os.Getegid()
	groups, err := os.Getgroups()
	if err != nil {
		t.Fatal(err)
	}
	for _, candidate := range groups {
		if candidate != approved {
			other = candidate
			break
		}
	}
	if other == approved {
		t.Skip("needs a second group to stand in for the inherited one")
	}
	written, edited := filepath.Join(c.Scope.Root, "written"), filepath.Join(c.Scope.Root, "edited")
	var changes []TargetChange
	for _, path := range []string{written, edited} {
		if err = os.WriteFile(path, []byte("original"), 0o644); err != nil {
			t.Fatal(err)
		}
		before, readErr := ReadImage(c, path)
		if readErr != nil {
			t.Fatal(readErr)
		}
		after := before
		after.Data = []byte("applied")
		changes = append(changes, TargetChange{Path: path, Before: before, After: after})
	}
	plan := Plan{
		Source:   SourceIdentity{Release: "fixture", ContentDigest: ImageDigest(changes[0].Before)},
		Scope:    c.Scope,
		Complete: true,
		Inputs:   []Input{{Name: "checkpoint-images", Digest: ChangesDigest(changes)}},
	}
	var cp *Checkpoint
	err = WithMutation(
		context.Background(),
		c,
		plan,
		Consent{ApprovedDigest: plan.Digest(), CompleteInputs: true},
		func(context.Context, Context) (Plan, error) { return plan, nil },
		func(m *Mutation) error {
			var beginErr error
			if cp, beginErr = BeginCheckpoint(m, plan, nil, changes); beginErr != nil {
				return beginErr
			}
			if beginErr = cp.StartNative(); beginErr != nil {
				return beginErr
			}
			// Native wrote the approved content with the group it inherited, then a
			// script failed; the other file was edited by someone else meanwhile.
			for path, content := range map[string]string{written: "applied", edited: "someone's edit"} {
				if beginErr = os.WriteFile(path, []byte(content), 0o644); beginErr != nil {
					return beginErr
				}
				if beginErr = os.Chown(path, -1, other); beginErr != nil {
					return beginErr
				}
			}
			cp.SettleFailedNative()
			_ = cp.Finish(Fail(ExitPartial, "partial", "A setup step failed"))
			return nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if image, readErr := ReadImage(c, written); readErr != nil ||
		string(image.Data) != "applied" || *image.Group != uint32(approved) {
		t.Fatalf("a file holding its approved content did not get its group back: %v", readErr)
	}
	if cp.journal.Known[0] != outcomeAfter {
		t.Fatalf("a file with its approved content was recorded as %s", cp.journal.Known[0])
	}
	image, err := ReadImage(c, edited)
	if err != nil || string(image.Data) != "someone's edit" || *image.Group != uint32(other) {
		t.Fatal("a file with different content was changed")
	}
}

// Prevent a revert from deleting files Workbench never wrote. A folder the apply
// created can later hold other files, such as plugins a setup step installed;
// revert restores everything else and leaves that folder, and every folder
// holding it, with those files in it.
func TestRevertKeepsFoldersHoldingOtherFiles(t *testing.T) {
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
	// New entries take the scope folder's group; read it from one.
	probe := filepath.Join(c.Scope.Root, "probe")
	if err = os.WriteFile(probe, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	scope, err := ReadImage(c, probe)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Remove(probe); err != nil {
		t.Fatal(err)
	}
	folder := filepath.Join(c.Scope.Root, "tools")
	inner := filepath.Join(folder, "tmux")
	written := filepath.Join(inner, "tmux.conf")
	other := filepath.Join(c.Scope.Root, "notes")
	absent := Image{Kind: ImageAbsent}
	directory := Image{Kind: ImageDirectory, Mode: 0o700, Group: scope.Group}
	changes := []TargetChange{
		{Path: folder, Before: absent, After: directory},
		{Path: inner, Before: absent, After: directory},
		{
			Path:   written,
			Before: absent,
			After:  Image{Kind: ImageFile, Mode: 0o600, Data: []byte("set"), Group: scope.Group},
		},
		{
			Path:   other,
			Before: absent,
			After:  Image{Kind: ImageFile, Mode: 0o600, Data: []byte("x"), Group: scope.Group},
		},
	}
	plan := Plan{
		Source:   SourceIdentity{Release: "fixture", ContentDigest: ImageDigest(directory)},
		Scope:    c.Scope,
		Complete: true,
		Inputs:   []Input{{Name: "checkpoint-images", Digest: ChangesDigest(changes)}},
	}
	id := ""
	err = WithMutation(
		context.Background(),
		c,
		plan,
		Consent{ApprovedDigest: plan.Digest(), CompleteInputs: true},
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
	plugin := filepath.Join(inner, "plugins", "tpm")
	if err = os.MkdirAll(plugin, 0o700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(plugin, "tpm"), []byte("plugin"), 0o600); err != nil {
		t.Fatal(err)
	}
	selector := RecoverySelector{Checkpoint: id}
	recovery, err := RecoveryPlan(c, selector)
	if err != nil {
		t.Fatal(err)
	}
	if len(recovery.Warnings) != 2 {
		t.Fatalf("expected both held folders named, got %q", recovery.Warnings)
	}
	if _, err = Recover(
		context.Background(),
		c,
		recovery,
		selector,
		Consent{ApprovedDigest: recovery.Digest(), CompleteInputs: true},
	); err != nil {
		t.Fatal(err)
	}
	if data, readErr := os.ReadFile(
		filepath.Join(plugin, "tpm"),
	); readErr != nil ||
		string(data) != "plugin" {
		t.Fatal("revert removed a file Workbench did not write")
	}
	for _, gone := range []string{written, other} {
		if _, statErr := os.Lstat(gone); !os.IsNotExist(statErr) {
			t.Fatalf("revert left %s that the apply created", gone)
		}
	}
}

// Prevent a revert from undoing what a checkpoint did not write. When
// Workbench is stopped or crashes while native writes, the journal still says
// every outcome is unknown; revert then decides each file from what it holds
// now. Only a file holding exactly what the apply approved (apart from the
// group native creation gave it) or exactly what was there before may count,
// and a file holding anything else must stop the revert before any write.
func TestRevertConfirmsFilesAnInterruptedApplyLeftUnknown(t *testing.T) {
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
	approved, inherited := os.Getegid(), os.Getegid()
	groups, err := os.Getgroups()
	if err != nil {
		t.Fatal(err)
	}
	for _, candidate := range groups {
		if candidate != approved {
			inherited = candidate
			break
		}
	}
	written := filepath.Join(c.Scope.Root, "written")
	untouched := filepath.Join(c.Scope.Root, "untouched")
	edited := filepath.Join(c.Scope.Root, "edited")
	created := filepath.Join(c.Scope.Root, "created")
	var changes []TargetChange
	for _, path := range []string{written, untouched, edited} {
		if err = os.WriteFile(path, []byte("original"), 0o644); err != nil {
			t.Fatal(err)
		}
		before, readErr := ReadImage(c, path)
		if readErr != nil {
			t.Fatal(readErr)
		}
		after := before
		after.Data = []byte("applied")
		changes = append(changes, TargetChange{Path: path, Before: before, After: after})
	}
	newFile := changes[0].After
	newFile.Attributes = nil
	changes = append(
		changes,
		TargetChange{Path: created, Before: Image{Kind: ImageAbsent}, After: newFile},
	)
	plan := Plan{
		Source:   SourceIdentity{Release: "fixture", ContentDigest: ImageDigest(changes[0].Before)},
		Scope:    c.Scope,
		Complete: true,
		Inputs:   []Input{{Name: "checkpoint-images", Digest: ChangesDigest(changes)}},
	}
	id := ""
	err = WithMutation(
		context.Background(),
		c,
		plan,
		Consent{ApprovedDigest: plan.Digest(), CompleteInputs: true},
		func(context.Context, Context) (Plan, error) { return plan, nil },
		func(m *Mutation) error {
			cp, beginErr := BeginCheckpoint(m, plan, nil, changes)
			if beginErr != nil {
				return beginErr
			}
			id = cp.ID
			// Native wrote two files, one with the group its temporary file
			// had, and Workbench died before it could record anything; someone
			// edited a third.
			if beginErr = cp.StartNative(); beginErr != nil {
				return beginErr
			}
			for path, content := range map[string]string{
				written: "applied",
				created: "applied",
				edited:  "someone's edit",
			} {
				if beginErr = os.WriteFile(path, []byte(content), 0o644); beginErr != nil {
					return beginErr
				}
			}
			return os.Chown(created, -1, inherited)
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	selector := RecoverySelector{Checkpoint: id}
	if _, err = RecoveryPlan(c, selector); ExitCode(err) != ExitConflict ||
		!strings.Contains(err.Error(), "edited") {
		t.Fatalf("a file holding neither image did not stop the revert: %v", err)
	}
	if err = os.WriteFile(edited, []byte("original"), 0o644); err != nil {
		t.Fatal(err)
	}
	recovery, err := RecoveryPlan(c, selector)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = Recover(
		context.Background(),
		c,
		recovery,
		selector,
		Consent{ApprovedDigest: recovery.Digest(), CompleteInputs: true},
	); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{written, untouched, edited} {
		if data, readErr := os.ReadFile(path); readErr != nil || string(data) != "original" {
			t.Fatalf("revert did not restore %s: %q %v", path, data, readErr)
		}
	}
	if _, err = os.Lstat(created); !os.IsNotExist(err) {
		t.Fatal("revert left the file the interrupted apply created")
	}
}
