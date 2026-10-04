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
// unneeded as removal edits, and returns the metadata of the kept ones, whose
// setup contexts and pinned tools stay. Activation keeps bundle and the
// release it replaces (the journal's previous one while an interrupted
// activation is resumed), so storage stays bounded while going back to the
// previous release stays possible. If a kept release's records cannot be
// read, it returns no kept releases: nothing may be removed that an unknown
// release could need. Reinstalling the active release removes nothing.
func StaleReleases(c operation.Context, bundle Bundle) ([]operation.Edit, []Metadata, error) {
	journal, err := readActivation(c)
	if err != nil {
		return nil, nil, err
	}
	state, err := operation.ReadState(c.Paths)
	if err != nil {
		return nil, nil, err
	}
	if state != nil && state.ActiveRelease != nil &&
		state.ActiveRelease.Source == bundle.Directory(c) &&
		(journal == nil || journal.Status != activationRunning) {
		// Reinstalling the active release repairs it in place and replaces
		// nothing, so the release before it stays.
		return nil, nil, nil
	}
	keep := []string{bundle.Directory(c)}
	if state != nil && state.ActiveRelease != nil {
		keep = append(keep, state.ActiveRelease.Source)
	}
	if journal != nil && journal.Status == activationRunning && journal.Previous != nil {
		keep = append(keep, journal.Previous.Source)
	}
	// The bundle may not be staged yet; its verified metadata is in memory.
	kept := []Metadata{bundle.Metadata}
	for _, directory := range keep[1:] {
		if directory == keep[0] {
			continue
		}
		metadata, err := readMetadata(directory)
		if err != nil {
			return nil, nil, nil
		}
		kept = append(kept, metadata)
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
				Description: "Remove an older staged release; the new and replaced releases stay",
			})
		}
	}
	return stale, nil
}
