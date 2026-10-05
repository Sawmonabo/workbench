package operation

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"

	"golang.org/x/sys/unix"
)

// Prevent silently stripping a macOS safeguard on revert: containerMetadata admits
// the "deny delete" ACL on ~/Library/Application Support, and parentMetadata the
// hidden flag on ~/Library, only because restoring a folder's mode touches
// neither. If a restore ever replaced the folder, revert would drop the ACL that
// keeps the folder holding every app's data from being deleted.
func TestFolderModeRestoreKeepsACLAndFlags(t *testing.T) {
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
	if err = unix.Chflags(library, unix.UF_HIDDEN); err != nil {
		t.Fatal(err)
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
	acl := func() []aclEntry {
		fd, openErr := unix.Open(folder, unix.O_RDONLY|unix.O_DIRECTORY, 0)
		if openErr != nil {
			t.Fatal(openErr)
		}
		defer func() { _ = unix.Close(fd) }()
		entries, _, aclErr := darwinACL(fd, nil)
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
		var stat unix.Stat_t
		if !slices.Equal(acl(), want) ||
			unix.Stat(library, &stat) != nil || stat.Flags&unix.UF_HIDDEN == 0 {
			t.Fatal("restoring the folder's mode changed its ACL or its parent's flags")
		}
	}
}
