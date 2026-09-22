package release

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"

	"github.com/Sawmonabo/workbench/internal/operation"
)

// StaleReleases lists staged releases other than keep as removal edits and
// returns the identities of the kept ones, whose application contexts stay.
// Activation keeps the new release, the one it replaces and a staged
// candidate, so storage stays bounded while rollback remains possible.
func StaleReleases(
	c operation.Context,
	keep ...string,
) ([]operation.Edit, []operation.SourceIdentity, error) {
	root := filepath.Join(c.Paths.Data, "releases")
	entries, err := os.ReadDir(root)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	if err = PrivateDirectory(c.Paths.Data, root, false); err != nil {
		return nil, nil, err
	}
	var stale []operation.Edit
	var kept []operation.SourceIdentity
	for _, entry := range entries {
		directory := filepath.Join(root, entry.Name())
		if !slices.Contains(keep, directory) {
			stale = append(stale, operation.Edit{
				Path:        directory,
				Action:      "remove",
				Description: "Remove an older staged release; the new, replaced and candidate releases stay",
			})
			continue
		}
		if metadata, err := readMetadata(directory); err == nil {
			kept = append(kept, operation.SourceIdentity{
				Release:       metadata.Release,
				ContentDigest: metadata.SourceDigest,
			})
		}
	}
	return stale, kept, nil
}
