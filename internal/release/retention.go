package release

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"

	"github.com/Sawmonabo/workbench/internal/operation"
)

// StaleReleases lists the staged releases that activating bundle leaves
// unneeded as removal edits, and returns the identities of the kept ones,
// whose application contexts stay. Activation keeps bundle, the release it
// replaces (the journal's previous one while an interrupted activation is
// resumed) and a staged candidate, so storage stays bounded while rollback
// remains possible. An unreadable candidate record returns no kept
// identities: nothing may be removed while an unknown directory could be it.
func StaleReleases(
	c operation.Context,
	bundle Bundle,
) ([]operation.Edit, []operation.SourceIdentity, error) {
	candidate, _, err := readCandidate(c)
	if err != nil {
		return nil, nil, nil
	}
	journal, err := readActivation(c)
	if err != nil {
		return nil, nil, err
	}
	state, err := operation.ReadState(c.Paths)
	if err != nil {
		return nil, nil, err
	}
	keep := []string{bundle.Directory(c)}
	kept := []operation.SourceIdentity{bundle.Identity()}
	var replaced []*operation.ReleaseRecord
	if state != nil {
		replaced = append(replaced, state.ActiveRelease)
	}
	if journal != nil && journal.Status == activationRunning {
		replaced = append(replaced, journal.Previous)
	}
	for _, record := range replaced {
		if record != nil {
			keep = append(keep, record.Source)
			kept = append(kept, record.Identity)
		}
	}
	if candidate != nil {
		keep = append(keep, candidate.Directory)
		if metadata, err := readMetadata(candidate.Directory); err == nil {
			kept = append(kept, operation.SourceIdentity{
				Release:       metadata.Release,
				ContentDigest: metadata.SourceDigest,
			})
		}
	}
	stale, err := staleDirectories(c, keep)
	return stale, kept, err
}

func staleDirectories(c operation.Context, keep []string) ([]operation.Edit, error) {
	root := filepath.Join(c.Paths.Data, "releases")
	entries, err := os.ReadDir(root)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if err = PrivateDirectory(c.Paths.Data, root, false); err != nil {
		return nil, err
	}
	var stale []operation.Edit
	for _, entry := range entries {
		directory := filepath.Join(root, entry.Name())
		if !slices.Contains(keep, directory) {
			stale = append(stale, operation.Edit{
				Path:        directory,
				Action:      "remove",
				Description: "Remove an older staged release; the new, replaced and candidate releases stay",
			})
		}
	}
	return stale, nil
}
