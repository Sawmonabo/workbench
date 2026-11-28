package operation

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

// Prevent unauthorized system mutation if a preview accidentally reaches the
// real subprocess owner. The command would create a directory if it ran.
func TestPreviewCannotExecuteMutation(t *testing.T) {
	directory := t.TempDir()
	target := filepath.Join(directory, "unauthorized")
	c := Context{ReadOnly: true, Scope: Scope{Kind: "project", Root: directory}}
	_, err := Run(context.Background(), c, nil, Process{
		Executable: "/bin/mkdir", Args: []string{target}, Directory: directory, Mutates: true,
	})
	if ExitCode(err) != 3 {
		t.Fatalf("expected blocked mutation, got %v", err)
	}
	if _, err := os.Stat(target); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("preview executed mutation: %v", err)
	}
}
