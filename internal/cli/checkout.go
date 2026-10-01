package cli

import (
	"os"
	"path/filepath"

	"github.com/Sawmonabo/workbench/internal/operation"
)

// localCheckout finds the Workbench checkout the current directory is in: the
// nearest ancestor holding .chezmoiroot and go.mod. --local-build takes no
// path, so a typo cannot select an unrelated tree.
func localCheckout() (string, error) {
	directory, err := os.Getwd()
	if err != nil {
		return "", operation.Fail(
			operation.ExitInvalid,
			"source",
			"Cannot resolve the current directory",
		)
	}
	for {
		root, rootErr := os.Stat(filepath.Join(directory, ".chezmoiroot"))
		module, moduleErr := os.Stat(filepath.Join(directory, "go.mod"))
		if rootErr == nil && moduleErr == nil && root.Mode().IsRegular() &&
			module.Mode().IsRegular() {
			return directory, nil
		}
		parent := filepath.Dir(directory)
		if parent == directory {
			return "", operation.Fail(
				operation.ExitInvalid,
				"source",
				"--local-build needs a Workbench checkout; run it inside one",
			)
		}
		directory = parent
	}
}
