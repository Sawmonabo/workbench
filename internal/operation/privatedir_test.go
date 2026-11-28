package operation

import (
	"os"
	"path/filepath"
	"testing"
)

// folderAt makes the folder at path with mode and checks that it has it, so a
// umask cannot leave the test with another mode than the one it names.
func folderAt(t *testing.T, path string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(path, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
	assertMode(t, path, mode)
}

// assertMode fails the test unless the folder at path has mode, its
// permission, setuid, setgid and sticky bits.
func assertMode(t *testing.T, path string, mode os.FileMode) {
	t.Helper()
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	kept := os.ModePerm | os.ModeSetuid | os.ModeSetgid | os.ModeSticky
	if got := info.Mode() & kept; got != mode {
		t.Fatalf("%s has mode %v, want %v", path, got, mode)
	}
}

// testRoot is a temporary folder with its links resolved, since macOS keeps
// temporary folders under /var, a link, which the private checks refuse.
func testRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return root
}

// Prevent running another user's program as this one: a folder of
// Workbench's own files that its group or other users can write in may hold
// what one of them put there, such as a program in place of a tool Workbench
// runs. Giving it 0700, as a write does and as a command that only reads does
// for a folder its owner cannot read or search, would keep that in it,
// private and trusted, so both must refuse the folder and leave its mode as
// it is: also one its owner can neither read nor search, which macOS opens
// no descriptor for and which is judged by its name in the folder above.
func TestOwnFolderRefusesAFolderOthersCanWrite(t *testing.T) {
	root := testRoot(t)
	for name, mode := range map[string]os.FileMode{
		"group":             0o775,
		"other":             0o757,
		"sticky":            0o777 | os.ModeSticky,
		"group, unreadable": 0o320,
		"group, write only": 0o220,
		"group alone":       0o020,
		"other alone":       0o002,
	} {
		folder := filepath.Join(root, name)
		folderAt(t, folder, mode)
		if err := makeOwnFolder("permissions", folder); err == nil {
			t.Errorf("%s: a write took a folder of mode %v", name, mode)
		}
		assertMode(t, folder, mode)
		info, err := os.Lstat(folder)
		if err != nil {
			t.Fatal(err)
		}
		if err = ownFolder("permissions", folder, info); err == nil {
			t.Errorf("%s: a read took a folder of mode %v", name, mode)
		}
		assertMode(t, folder, mode)
	}
}

// Prevent changing a folder that is not Workbench's: a link put where
// Workbench keeps one of its folders must not make it change the mode of the
// folder the link points to, such as one other users share, which 0700 would
// shut them out of.
func TestMakeOwnFolderNeverFollowsALink(t *testing.T) {
	root := testRoot(t)
	elsewhere := filepath.Join(root, "elsewhere")
	folderAt(t, elsewhere, 0o755)
	link := filepath.Join(root, "state")
	if err := os.Symlink(elsewhere, link); err != nil {
		t.Fatal(err)
	}
	if err := makeOwnFolder("permissions", link); err == nil {
		t.Error("a link was taken for a folder")
	}
	assertMode(t, elsewhere, 0o755)
}

// Prevent changing the machine before the person approves anything: doctor,
// a dry run and every other check that only reads must leave a folder of
// Workbench's own files that its owner can read and search as it is, so a
// folder others cannot write in but whose mode is not 0700 is used with its
// mode unchanged.
func TestFolderChecksThatOnlyReadChangeNothing(t *testing.T) {
	root := testRoot(t)
	state := filepath.Join(root, "state")
	guard := filepath.Join(state, "agent", "guard")
	checkpoint := filepath.Join(state, "checkpoints", "scope", "checkpoint")
	folders := []string{state, filepath.Dir(guard), guard, checkpoint}
	for _, folder := range folders {
		folderAt(t, folder, 0o755)
	}
	for _, name := range []string{"CONFIG", "DATA", "CACHE", "BIN"} {
		t.Setenv("WORKBENCH_"+name+"_DIR", filepath.Join(root, name))
	}
	t.Setenv("WORKBENCH_STATE_DIR", state)
	if _, err := runtimePaths(root); err != nil {
		t.Errorf("runtime folders: %v", err)
	}
	if err := CheckPrivateDirectory(state, "agent/guard"); err != nil {
		t.Errorf("CheckPrivateDirectory: %v", err)
	}
	if err := PrivateTree(state, guard, false); err != nil {
		t.Errorf("PrivateTree without create: %v", err)
	}
	if _, err := checkpointStorageBytes(checkpoint); err != nil {
		t.Errorf("checkpoint folders: %v", err)
	}
	for _, folder := range folders {
		assertMode(t, folder, 0o755)
	}
}
