package operation

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"

	"github.com/Sawmonabo/workbench/internal/platform"
)

// Prevent a silent permission change and lost data: an ACL decides which
// other accounts may read a file (on macOS one that denies an account keeps
// it out of a file every account may otherwise read), an extended attribute
// holds data no other copy has (Finder tags, a tool's note), and the hidden
// flag keeps a file out of Finder. A replace that wrote the new file without
// them would let those accounts in and lose the attributes for good, since
// nothing else holds them. The replacement must have the ACL, each attribute
// and the flag, on Linux with the ACL's mask as chmod gives it the new mode.
func TestReplaceKeepsACLAttributesAndFlag(t *testing.T) {
	c := carryContext(t)
	path := filepath.Join(c.Scope.Root, "config")
	giveMetadata(t, path)
	before, err := ReadImage(c, path)
	if err != nil {
		t.Fatal(err)
	}
	was := readHeld(t, path)
	after := Carried(
		before,
		Image{Kind: ImageFile, Mode: 0o640, Data: []byte("new"), Group: before.Group},
	)
	applyCheckpointed(t, c, []TargetChange{{Path: path, Before: before, After: after}})

	want := was
	want.mode = 0o640
	if platform.ACLsAreAttributes {
		want.attributes[platform.AccessACLAttribute] = chmodACL(
			t,
			c,
			was.attributes[platform.AccessACLAttribute],
			0o640,
		)
	}
	got := readHeld(t, path)
	if got.inode == was.inode {
		t.Fatal("the file was changed in place, not replaced")
	}
	want.expect(t, "the replacement", got)
}

// Prevent lost data and a silent permission change on revert: revert is the
// way back from an apply, so a file Workbench wrote without the ACL,
// attributes and hidden flag it had must get each back exactly, with its
// read-only mode, or the accounts the ACL kept out stay let in and the
// attributes stay lost. The mode is read-only, which a user.* attribute on
// Linux and any attribute on macOS cannot be written under, so the restore
// must write them before it.
func TestRevertRestoresACLAttributesAndFlag(t *testing.T) {
	c := carryContext(t)
	path := filepath.Join(c.Scope.Root, "config")
	giveMetadata(t, path)
	before, err := ReadImage(c, path)
	if err != nil {
		t.Fatal(err)
	}
	was := readHeld(t, path)
	bare := Image{Kind: ImageFile, Mode: 0o640, Data: []byte("new"), Group: before.Group}
	id := applyCheckpointed(t, c, []TargetChange{{Path: path, Before: before, After: bare}})
	if applied := readHeld(t, path); applied.acl != nil || applied.hidden ||
		applied.attributes[noteAttribute] != nil ||
		applied.attributes[platform.AccessACLAttribute] != nil {
		t.Fatal("the apply kept metadata its image does not have, so the revert proves nothing")
	}
	revertCheckpointed(t, c, id)
	if data, readErr := os.ReadFile(path); readErr != nil || string(data) != "old" {
		t.Fatalf("revert left %q, %v", data, readErr)
	}
	was.expect(t, "the restored file", readHeld(t, path))
}

// Prevent the same permission change and lost data where chezmoi writes the
// file: chezmoi's new file has none of the ACL, attributes and hidden flag of
// the one it replaces, so the pass after it must give them back, or they are
// lost as surely as by a replace that dropped them.
func TestNativeReplaceKeepsACLAttributesAndFlag(t *testing.T) {
	c := carryContext(t)
	path := filepath.Join(c.Scope.Root, "config")
	giveMetadata(t, path)
	changes, was := nativeChange(t, c, path)
	withNative(t, c, changes, func(cp *Checkpoint) error {
		nativeWrite(t, c, path, changes[0].After)
		if err := cp.FinalizeNative(); err != nil {
			return err
		}
		return cp.Finish(nil)
	})
	want := was
	want.mode = changes[0].After.Mode
	if platform.ACLsAreAttributes {
		want.attributes[platform.AccessACLAttribute] = chmodACL(
			t,
			c,
			was.attributes[platform.AccessACLAttribute],
			want.mode,
		)
	}
	want.expect(t, "the file chezmoi wrote", readHeld(t, path))
}

// Prevent lost data and a silent permission change when Workbench stops
// between chezmoi's write and the pass after it: revert must still put back
// the ACL, attributes and hidden flag the replaced file had, since nothing
// else holds them.
func TestRevertAfterInterruptedNativeRestoresACLAttributesAndFlag(t *testing.T) {
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
		t.Fatal("chezmoi's file kept metadata, so the revert proves nothing")
	}
	revertCheckpointed(t, c, id)
	if data, err := os.ReadFile(path); err != nil || string(data) != "old" {
		t.Fatalf("revert left %q, %v", data, err)
	}
	was.expect(t, "the restored file", readHeld(t, path))
}

// nativeChange is the change of path, which giveMetadata wrote, to new content
// of mode 0640 that carries its metadata, and what path holds now.
func nativeChange(t *testing.T, c Context, path string) ([]TargetChange, held) {
	t.Helper()
	before, err := ReadImage(c, path)
	if err != nil {
		t.Fatal(err)
	}
	after := Carried(
		before,
		Image{Kind: ImageFile, Mode: 0o640, Data: []byte("new"), Group: before.Group},
	)
	return []TargetChange{{Path: path, Before: before, After: after}}, readHeld(t, path)
}

// withNative runs native under a new checkpoint of changes in c, after the
// journal marks every outcome unknown, as an apply hands the targets to
// chezmoi, and returns the checkpoint's ID.
func withNative(
	t *testing.T,
	c Context,
	changes []TargetChange,
	native func(*Checkpoint) error,
) string {
	t.Helper()
	plan := Plan{
		Source:   SourceIdentity{Release: "fixture", ContentDigest: ChangesDigest(changes)},
		Scope:    c.Scope,
		Complete: true,
		Inputs:   []Input{{Name: "checkpoint-images", Digest: ChangesDigest(changes)}},
	}
	id := ""
	err := WithMutation(
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
			if beginErr = cp.StartNative(); beginErr != nil {
				return beginErr
			}
			return native(cp)
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// nativeWrite writes image's data at path as chezmoi does: a new file of
// image's mode in a folder beside c's, renamed over path.
func nativeWrite(t *testing.T, c Context, path string, image Image) {
	t.Helper()
	scratch := filepath.Join(filepath.Dir(c.Scope.Root), "scratch")
	if err := os.MkdirAll(scratch, 0o700); err != nil {
		t.Fatal(err)
	}
	staged := filepath.Join(scratch, filepath.Base(path))
	if err := os.WriteFile(staged, image.Data, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(staged, os.FileMode(image.Mode)); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(staged, path); err != nil {
		t.Fatal(err)
	}
}

// Prevent credential exposure: a dotfile such as ~/.netrc is often a link
// into a dotfiles folder, pointing at a secret of mode 0600. A link's ACL,
// attributes and flag belong to the link: giving them to the file it points
// to would let every account read the secret through an entry the link has
// ("everyone allow read"), and put the link's data on it. A replace and its
// revert must set each on the new link itself and leave both files it points
// to as they were.
func TestLinkMetadataStaysOnTheLink(t *testing.T) {
	c := carryContext(t)
	in := func(name string) string { return filepath.Join(c.Scope.Root, name) }
	for _, name := range []string{"secret", "other"} {
		if err := os.WriteFile(in(name), []byte(name), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	link := in("netrc")
	if err := os.Symlink("secret", link); err != nil {
		t.Fatal(err)
	}
	giveLinkMetadata(t, link)
	files := map[string]held{"secret": readHeld(t, in("secret")), "other": readHeld(t, in("other"))}
	before, err := ReadImage(c, link)
	if err != nil {
		t.Fatal(err)
	}
	was := readHeld(t, link)
	after := Carried(
		before,
		Image{Kind: ImageSymlink, Mode: before.Mode, Link: "other", Group: before.Group},
	)
	id := applyCheckpointed(t, c, []TargetChange{{Path: link, Before: before, After: after}})
	check := func(when, target string) {
		t.Helper()
		if got, readErr := os.Readlink(link); readErr != nil || got != target {
			t.Fatalf("%s the link points to %q, %v, not %q", when, got, readErr, target)
		}
		was.expect(t, when+" the link", readHeld(t, link))
		for name, file := range files {
			file.expect(t, when+" "+name, readHeld(t, in(name)))
		}
	}
	check("after the apply", "other")
	revertCheckpointed(t, c, id)
	check("after the revert", "secret")
}

// noteAttribute is the user's own attribute each fixture file holds.
const noteAttribute = "user.workbench-note"

// carryContext is a project Context in a new folder, with Workbench's own
// folders beside it.
func carryContext(t *testing.T) Context {
	t.Helper()
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
	return c
}

// giveMetadata writes path, of mode 0444, with the user's own attribute and
// an ACL: on macOS one entry that denies the account nobody reading it, a
// Finder tag and the hidden flag; on Linux a named entry for root and a mask.
func giveMetadata(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := unix.Setxattr(
		path,
		noteAttribute,
		[]byte("a note no other copy has"),
		0,
	); err != nil {
		t.Skipf("cannot give the fixture an extended attribute here: %v", err)
	}
	if platform.ACLsAreAttributes {
		if err := unix.Setxattr(path, platform.AccessACLAttribute, posixACL(), 0); err != nil {
			t.Skipf("cannot give the fixture an ACL here: %v", err)
		}
	} else {
		tags := []byte(
			"bplist00\xa1\x01_\x10\x0eWorkbench test\x08\x0a\x00\x00\x00\x00\x00\x00\x01\x01",
		)
		if err := unix.Setxattr(path, "com.apple.metadata:_kMDItemUserTags", tags, 0); err != nil {
			t.Fatal(err)
		}
		run(t, "/bin/chmod", "+a", "user:nobody deny read", path)
		run(t, "/usr/bin/chflags", "hidden", path)
	}
	if err := os.Chmod(path, 0o444); err != nil {
		t.Fatal(err)
	}
}

// giveLinkMetadata gives the link at path an attribute and, on macOS, an ACL
// entry that lets every account read and the hidden flag, each on the link
// itself. Linux lets no one but a process with CAP_SYS_ADMIN give a link an
// attribute (trusted.*, fs/xattr.c), and keeps no ACL or flag on one.
func giveLinkMetadata(t *testing.T, path string) {
	t.Helper()
	if platform.ACLsAreAttributes {
		err := unix.Lsetxattr(path, "trusted.workbench-note", []byte("a note"), 0)
		if errors.Is(err, unix.EPERM) || errors.Is(err, unix.EOPNOTSUPP) {
			t.Skipf("cannot give a link an attribute here: %v", err)
		}
		if err != nil {
			t.Fatal(err)
		}
		return
	}
	if err := unix.Lsetxattr(path, noteAttribute, []byte("a note"), 0); err != nil {
		t.Fatal(err)
	}
	run(t, "/bin/chmod", "-h", "+a", "group:everyone allow read", path)
	run(t, "/usr/bin/chflags", "-h", "hidden", path)
}

// posixACL is a POSIX ACL in its extended attribute form
// (linux/posix_acl_xattr.h): version 2, then tag, permissions and ID per
// entry, with a named user and a mask, so the kernel cannot fold it into the
// mode.
func posixACL() []byte {
	acl := binary.LittleEndian.AppendUint32(nil, 2)
	for _, entry := range []struct {
		tag, perm uint16
		id        uint32
	}{
		{0x01, 6, 0xffffffff}, // ACL_USER_OBJ rw
		{0x02, 4, 0},          // ACL_USER root r
		{0x04, 4, 0xffffffff}, // ACL_GROUP_OBJ r
		{0x10, 4, 0xffffffff}, // ACL_MASK r
		{0x20, 0, 0xffffffff}, // ACL_OTHER none
	} {
		acl = binary.LittleEndian.AppendUint16(acl, entry.tag)
		acl = binary.LittleEndian.AppendUint16(acl, entry.perm)
		acl = binary.LittleEndian.AppendUint32(acl, entry.id)
	}
	return acl
}

// chmodACL is the access ACL acl becomes on a file chmod gives mode, read
// from a file in c's folder that gets acl and then mode.
func chmodACL(t *testing.T, c Context, acl []byte, mode uint32) []byte {
	t.Helper()
	reference := filepath.Join(filepath.Dir(c.Scope.Root), "reference")
	if err := os.WriteFile(reference, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := unix.Setxattr(reference, platform.AccessACLAttribute, acl, 0); err != nil {
		t.Fatal(err)
	}
	if err := unix.Chmod(reference, mode); err != nil {
		t.Fatal(err)
	}
	return readHeld(t, reference).attributes[platform.AccessACLAttribute]
}

// held is what the file or link at a path holds itself beside its content:
// its inode, mode, ACL, extended attributes and hidden flag.
type held struct {
	inode      uint64
	mode       uint32
	acl        []byte
	attributes map[string][]byte
	hidden     bool
}

// readHeld reads what path itself holds, never through a link.
func readHeld(t *testing.T, path string) held {
	t.Helper()
	var stat unix.Stat_t
	if err := unix.Lstat(path, &stat); err != nil {
		t.Fatal(err)
	}
	acl, err := platform.FileSecurity(-1, path)
	if err != nil {
		t.Fatal(err)
	}
	attributes, err := readImageAttributes(atPath(path), path, replacedFile)
	if err != nil {
		t.Fatal(err)
	}
	flags, err := platform.FileFlags(-1, path)
	if err != nil {
		t.Fatal(err)
	}
	return held{
		inode:      stat.Ino,
		mode:       uint32(stat.Mode) & 0o777,
		acl:        acl.Data,
		attributes: attributes,
		hidden:     flags&platform.HiddenFlag != 0,
	}
}

// expect fails unless got, read from what, holds the mode, ACL, attributes
// (apart from the label, which the system gives) and hidden flag h holds.
// It names what differs, never a value.
func (h held) expect(t *testing.T, what string, got held) {
	t.Helper()
	switch {
	case got.mode != h.mode:
		t.Fatalf("%s has mode %04o, want %04o", what, got.mode, h.mode)
	case !bytes.Equal(got.acl, h.acl):
		t.Fatalf("%s has another ACL (%d bytes, want %d)", what, len(got.acl), len(h.acl))
	case !sameAttributes(got.attributes, h.attributes):
		t.Fatalf(
			"%s has other extended attributes (%d, want %d)",
			what,
			len(got.attributes),
			len(h.attributes),
		)
	case got.hidden != h.hidden:
		t.Fatalf("%s has the hidden flag %v, want %v", what, got.hidden, h.hidden)
	}
}

// run runs a command that sets up a fixture.
func run(t *testing.T, command ...string) {
	t.Helper()
	if out, err := exec.Command(command[0], command[1:]...).CombinedOutput(); err != nil {
		t.Fatalf("%v: %v: %s", command, err, out)
	}
}

// applyCheckpointed applies changes in c under a new checkpoint and returns
// its ID.
func applyCheckpointed(t *testing.T, c Context, changes []TargetChange) string {
	t.Helper()
	plan := Plan{
		Source:   SourceIdentity{Release: "fixture", ContentDigest: ChangesDigest(changes)},
		Scope:    c.Scope,
		Complete: true,
		Inputs:   []Input{{Name: "checkpoint-images", Digest: ChangesDigest(changes)}},
	}
	id := ""
	err := WithMutation(
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
	return id
}

// revertCheckpointed restores the images checkpoint id holds of what it
// changed in c.
func revertCheckpointed(t *testing.T, c Context, id string) {
	t.Helper()
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
}
