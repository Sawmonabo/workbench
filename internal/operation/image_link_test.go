package operation

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

// Prevent credential exposure: a dotfile such as ~/.netrc is often a link into
// a dotfiles folder, pointing at a secret of mode 0600. A replacement or a
// revert that set a link's mode through the link, or wrote the replacing file
// through it, would make the secret readable by everyone (0705) or overwrite
// it. Each must act on the link itself and leave the file it points to as it
// was, and the revert must give each link back its own mode, which on macOS
// decides whether its owner can still read it.
func TestLinkReplaceAndRevertKeepTheLinkAndLeaveItsFile(t *testing.T) {
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
	in := func(name string) string { return filepath.Join(c.Scope.Root, name) }
	for _, name := range []string{"secret", "other"} {
		if err = os.WriteFile(in(name), []byte(name), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// Modes no umask gives a new link, so a link left with the mode it was
	// made with is told apart; where links keep no mode of their own (Linux),
	// every link is 0777.
	kept, replaced, retargeted := uint32(0o705), uint32(0o750), uint32(0o570)
	for _, name := range []string{"kept", "replaced"} {
		if err = os.Symlink("secret", in(name)); err != nil {
			t.Fatal(err)
		}
	}
	err = unix.Fchmodat(unix.AT_FDCWD, in("kept"), kept, unix.AT_SYMLINK_NOFOLLOW)
	if errors.Is(err, unix.EOPNOTSUPP) {
		kept, replaced, retargeted = 0o777, 0o777, 0o777
	} else if err != nil {
		t.Fatal(err)
	} else if err = unix.Fchmodat(
		unix.AT_FDCWD,
		in("replaced"),
		replaced,
		unix.AT_SYMLINK_NOFOLLOW,
	); err != nil {
		t.Fatal(err)
	}
	link, err := ReadImage(c, in("kept"))
	if err != nil {
		t.Fatal(err)
	}
	file, err := ReadImage(c, in("replaced"))
	if err != nil {
		t.Fatal(err)
	}
	moved := link
	moved.Mode, moved.Link = retargeted, "other"
	written := Image{Kind: ImageFile, Mode: 0o644, Data: []byte("applied"), Group: file.Group}
	changes := []TargetChange{
		{Path: in("kept"), Before: link, After: moved},
		{Path: in("replaced"), Before: file, After: written},
	}
	plan := Plan{
		Source:   SourceIdentity{Release: "fixture", ContentDigest: ImageDigest(link)},
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
	expectLink(t, in("kept"), "other", retargeted)
	if data, readErr := os.ReadFile(in("replaced")); readErr != nil || string(data) != "applied" {
		t.Fatalf("replaced link holds %q, %v", data, readErr)
	}
	expectFiles(t, in)

	recovery, err := RecoveryPlan(c, RecoverySelector{Checkpoint: id})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = Recover(
		context.Background(),
		c,
		recovery,
		RecoverySelector{Checkpoint: id},
		Consent{ApprovedDigest: recovery.Digest(), CompleteInputs: true},
	); err != nil {
		t.Fatal(err)
	}
	expectLink(t, in("kept"), "secret", kept)
	expectLink(t, in("replaced"), "secret", replaced)
	expectFiles(t, in)
}

// expectLink fails unless path is a link to target with its own mode mode.
func expectLink(t *testing.T, path, target string, mode uint32) {
	t.Helper()
	var stat unix.Stat_t
	if err := unix.Lstat(path, &stat); err != nil {
		t.Fatal(err)
	}
	got, err := os.Readlink(path)
	if err != nil {
		t.Fatal(err)
	}
	if stat.Mode&unix.S_IFMT != unix.S_IFLNK || got != target || uint32(stat.Mode)&0o777 != mode {
		t.Fatalf("%s is mode %04o to %q, not a link of mode %04o to %q",
			filepath.Base(path), stat.Mode&0o777, got, mode, target)
	}
}

// expectFiles fails unless the files the links point to keep their bytes and
// their mode 0600.
func expectFiles(t *testing.T, in func(string) string) {
	t.Helper()
	for _, name := range []string{"secret", "other"} {
		info, err := os.Lstat(in(name))
		if err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(in(name))
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode() != 0o600 || string(data) != name {
			t.Fatalf("%s became %v holding %q", name, info.Mode(), data)
		}
	}
}
