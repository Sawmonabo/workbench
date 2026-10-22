package cli

import (
	"os"
	"path/filepath"
	"testing"
)

// TestGateNeverCountsOutsideFolderAsInside guards the work folder's edge: a
// session the gate counts as inside gets work.env's credentials and the work
// servers' tools, so a personal session counted as inside would hand both to
// it. A sibling sharing the folder's name as a prefix, a path that cannot be
// resolved and a project folder outside it all count as outside; a symlink
// counts as where it points.
func TestGateNeverCountsOutsideFolderAsInside(t *testing.T) {
	root := t.TempDir()
	for _, folder := range []string{"repos/app", "repos-old", "dev"} {
		if err := os.MkdirAll(filepath.Join(root, folder), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink("../repos/app", filepath.Join(root, "dev", "link")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("repos", filepath.Join(root, "alias")); err != nil {
		t.Fatal(err)
	}
	at := func(path string) string {
		if path == "" {
			return ""
		}
		return filepath.Join(root, path)
	}
	for _, check := range []struct {
		folder, projectDir, cwd string
		inside                  bool
	}{
		{"repos", "", "repos", true},
		{"repos", "", "repos/app", true},
		{"repos", "", "dev/link", true},
		{"alias", "", "repos/app", true},
		{"repos", "repos/app", "dev", true},
		{"repos", "", "repos-old", false},
		{"repos", "", "dev", false},
		{"repos", "", "missing", false},
		{"repos", "dev", "repos/app", false},
	} {
		if got := gateInside(
			at(check.folder),
			at(check.projectDir),
			at(check.cwd),
		); got != check.inside {
			t.Errorf(
				"folder %s, project folder %q, cwd %s: inside is %v, want %v",
				check.folder, check.projectDir, check.cwd, got, check.inside,
			)
		}
	}
}
