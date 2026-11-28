package operation

import (
	"encoding/binary"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"golang.org/x/sys/unix"

	"github.com/Sawmonabo/workbench/internal/platform"
)

// Prevent silently stripping a macOS safeguard on revert: containerMetadata admits
// the "deny delete" ACL on ~/Library/Application Support, and parentMetadata the
// hidden flag on ~/Library, only because restoring a folder's mode touches
// neither. If a restore ever replaced the folder, revert would drop the ACL that
// keeps the folder holding every app's data from being deleted.
func TestFolderModeRestoreKeepsACLAndFlags(t *testing.T) {
	if platform.ACLsAreAttributes || platform.HiddenFlag == 0 {
		t.Skip("no ACL that FileSecurity reads, or no hidden flag, here")
	}
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	library := filepath.Join(root, "Library")
	folder := filepath.Join(library, "Application Support")
	if err = os.MkdirAll(folder, 0o700); err != nil {
		t.Fatal(err)
	}
	if err = os.Chmod(folder, 0o755); err != nil {
		t.Fatal(err)
	}
	if out, aclErr := exec.Command("/bin/chmod", "+a", "group:everyone deny delete", folder).
		CombinedOutput(); aclErr != nil {
		t.Fatalf("chmod +a: %v: %s", aclErr, out)
	}
	if out, flagErr := exec.Command("/usr/bin/chflags", "hidden", library).
		CombinedOutput(); flagErr != nil {
		t.Fatalf("chflags hidden: %v: %s", flagErr, out)
	}
	// The ACL stops the folder from being deleted, so lift it for TempDir's cleanup.
	t.Cleanup(func() { _ = exec.Command("/bin/chmod", "-N", folder).Run() })
	c := Context{
		Paths: Paths{
			State:  filepath.Join(folder, "workbench", "state"),
			Config: filepath.Join(folder, "workbench", "config"),
			Data:   filepath.Join(folder, "workbench", "data"),
			Cache:  filepath.Join(root, "cache"),
			Bin:    filepath.Join(root, "bin"),
		},
		Scope: Scope{Kind: "machine", Root: root},
	}
	acl := func() []platform.ACE {
		fd, openErr := unix.Open(folder, unix.O_RDONLY|unix.O_DIRECTORY, 0)
		if openErr != nil {
			t.Fatal(openErr)
		}
		defer func() { _ = unix.Close(fd) }()
		entries, _, aclErr := platform.FileSecurity(fd, "")
		if aclErr != nil {
			t.Fatal(aclErr)
		}
		return entries
	}
	want := acl()
	if len(want) != 1 {
		t.Fatalf("fixture ACL has %d entries, want 1", len(want))
	}
	loose, err := ReadImage(c, folder)
	if err != nil {
		t.Fatal(err)
	}
	private := loose
	private.Mode = 0o700
	for _, step := range [][2]Image{{loose, private}, {private, loose}} {
		if err = writeImage(c, folder, step[0], step[1]); err != nil {
			t.Fatal(err)
		}
		flags, flagErr := platform.FileFlags(-1, library)
		if !slices.Equal(acl(), want) || flagErr != nil || flags&platform.HiddenFlag == 0 {
			t.Fatal("restoring the folder's mode changed its ACL or its parent's flags")
		}
	}
}

// Prevent a silent permission change on Linux: a POSIX ACL is an extended
// attribute there, which a replacement does not have, so a target that holds
// one, or a folder whose default ACL would give the replacement one, must stop
// planning, even beside the SELinux label, which images carry. If the label's
// admission let an ACL through, the replace would drop the users and groups it
// grants or denies without a word.
func TestACLAttributesBlockBesideTheLabel(t *testing.T) {
	if !platform.ACLsAreAttributes {
		t.Skip("ACLs are not extended attributes here")
	}
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	c := Context{Scope: Scope{Kind: "project", Root: root}}
	// A POSIX ACL in its extended attribute form (linux/posix_acl_xattr.h):
	// version 2, then tag, permissions and ID per entry, with a named user and a
	// mask, so the kernel cannot fold it into the mode.
	entry := func(tag, perm uint16, id uint32) []byte {
		return binary.LittleEndian.AppendUint32(
			binary.LittleEndian.AppendUint16(binary.LittleEndian.AppendUint16(nil, tag), perm),
			id,
		)
	}
	acl := binary.LittleEndian.AppendUint32(nil, 2)
	for _, e := range [][]byte{
		entry(0x01, 6, 0xffffffff), // ACL_USER_OBJ rw
		entry(0x02, 4, 0),          // ACL_USER root r
		entry(0x04, 4, 0xffffffff), // ACL_GROUP_OBJ r
		entry(0x10, 4, 0xffffffff), // ACL_MASK r
		entry(0x20, 0, 0xffffffff), // ACL_OTHER none
	} {
		acl = append(acl, e...)
	}
	label := []byte("unconfined_u:object_r:user_home_t:s0")
	file := filepath.Join(root, "file")
	folder := filepath.Join(root, "folder")
	if err = os.WriteFile(file, []byte("data"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err = os.Mkdir(folder, 0o750); err != nil {
		t.Fatal(err)
	}
	labeled := unix.Setxattr(file, platform.LabelAttribute, label, 0) == nil
	if err = unix.Setxattr(file, "system.posix_acl_access", acl, 0); err != nil {
		t.Skipf("cannot give the fixture an ACL here: %v", err)
	}
	if err = unix.Setxattr(folder, "system.posix_acl_default", acl, 0); err != nil {
		t.Skipf("cannot give the fixture a default ACL here: %v", err)
	}
	if _, err = ReadImage(c, file); Category(err) != "metadata" {
		t.Fatalf("a file with an ACL (labeled: %v) was read as preservable: %v", labeled, err)
	}
	if _, err = ReadImage(c, filepath.Join(folder, "new")); Category(err) != "metadata" {
		t.Fatalf("a folder with a default ACL was admitted as a target's folder: %v", err)
	}
}

// Prevent a silent permission change under SELinux: a file's label decides
// which confined programs may read or write it, and the new file a replace
// renames over a target gets a label of its own, so a replacement that did not
// carry the old label would quietly change who has access. Setting a label
// takes CAP_SYS_ADMIN where no security module handles labels, so outside
// SELinux this runs only as root.
func TestReplacementCarriesTheLabel(t *testing.T) {
	if platform.LabelAttribute == "" {
		t.Skip("no label attribute here")
	}
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	c := Context{Scope: Scope{Kind: "project", Root: root}}
	path := filepath.Join(root, "config")
	if err = os.WriteFile(path, []byte("old"), 0o640); err != nil {
		t.Fatal(err)
	}
	label := []byte("unconfined_u:object_r:custom_home_t:s0")
	if err = unix.Setxattr(path, platform.LabelAttribute, label, 0); err != nil {
		t.Skipf("cannot label the fixture here: %v", err)
	}
	var old unix.Stat_t
	if err = unix.Stat(path, &old); err != nil {
		t.Fatal(err)
	}
	before, err := ReadImage(c, path)
	if err != nil {
		t.Fatal(err)
	}
	after := before
	after.Data = []byte("new")
	if err = writeImage(c, path, before, after); err != nil {
		t.Fatal(err)
	}
	var replaced unix.Stat_t
	if err = unix.Stat(path, &replaced); err != nil {
		t.Fatal(err)
	}
	got := make([]byte, 256)
	n, err := unix.Getxattr(path, platform.LabelAttribute, got)
	if replaced.Ino == old.Ino || err != nil || string(got[:n]) != string(label) {
		t.Fatalf("replacement (new inode: %v) has label %q (%v), want %q",
			replaced.Ino != old.Ino, got[:max(n, 0)], err, label)
	}
}

// Prevent credential exposure: an extended attribute can hold a secret, such
// as a token a tool keeps beside a file, and a refusal is printed, logged and
// returned in --json. A refusal of an attribute it cannot keep names the
// attribute, so the person can remove it; if it ever quoted the value, the
// secret would reach the terminal, logs and any caller reading the JSON.
func TestAttributeRefusalNamesTheAttributeNotItsValue(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	c := Context{Scope: Scope{Kind: "project", Root: root}}
	file := filepath.Join(root, "file")
	if err = os.WriteFile(file, []byte("data"), 0o600); err != nil {
		t.Fatal(err)
	}
	const name, secret = "user.workbench-test-token", "synthetic-secret-4f1c9a"
	if err = unix.Setxattr(file, name, []byte(secret), 0); err != nil {
		t.Skipf("cannot give the fixture an extended attribute here: %v", err)
	}
	_, err = ReadImage(c, file)
	if Category(err) != "metadata" {
		t.Fatalf("a file with the attribute %s was read as preservable: %v", name, err)
	}
	if message := err.Error(); !strings.Contains(message, name) ||
		strings.Contains(message, secret) {
		t.Fatalf("the refusal must name the attribute and never show its value: %s", message)
	}
}
