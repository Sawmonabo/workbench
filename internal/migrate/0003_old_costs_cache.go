package migrate

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/Sawmonabo/workbench/internal/operation"
)

// oldCostsCache deletes the parse cache the claude-costs script kept before
// Workbench, which Workbench never reads: derived data only. Any other file in
// its folder stays and is reported.
var oldCostsCache = Migration{
	ID:    "0003-old-costs-cache",
	Title: "Delete the old claude-costs script's cache",
	Doing: "deleting the old claude-costs script's cache",
	What: "Deletes the cached transcript summaries the claude-costs script kept in " +
		"~/.cache/claude-costs (CLAUDE_COSTS_CACHE), which Workbench never reads, and " +
		"removes the folder once it is empty. Any other file there stays, and is named.",
	Recovery: "nothing to undo: the deleted files are derived data Workbench never reads",
	Check: func(l Locations) (Status, string) {
		ours, other, err := oldCacheFiles(l)
		folder := operation.ShowPath(l.Home, l.OldScriptCache)
		if err != nil {
			return Pending, "can't read " + folder
		}
		if info, err := os.Lstat(l.OldScriptCache); err == nil && info.IsDir() &&
			len(ours) == 0 && len(other) == 0 {
			return Pending, "removes the empty folder " + folder
		}
		var text []string
		if len(ours) > 0 {
			text = append(text, fmt.Sprintf("deletes %d cached file(s) from %s", len(ours), folder))
		}
		if len(other) > 0 {
			text = append(text, keeps(other))
		}
		if len(ours) == 0 {
			return Done, strings.Join(text, " and ")
		}
		return Pending, strings.Join(text, " and ")
	},
	Run: func(_ context.Context, l Locations) ([]string, error) {
		ours, other, err := oldCacheFiles(l)
		if err != nil {
			return nil, err
		}
		for _, name := range ours {
			if err = removeIfThere(filepath.Join(l.OldScriptCache, name)); err != nil {
				return other, err
			}
		}
		if len(other) > 0 {
			return other, nil
		}
		return nil, removeIfThere(l.OldScriptCache)
	},
}

// oldCacheName is the old script's naming: a 32-hex digest, .json.gz, or the
// .json.tmp of a write that stopped.
var oldCacheName = regexp.MustCompile(`^[0-9a-f]{32}\.json\.(gz|tmp)$`)

// oldCacheFiles lists the old script's cache folder: ours by name, other
// files home-relative. A folder that isn't a real one, such as a link, is
// never read through: it is the one other file. Reading the folder is the
// only read; a folder that can't be read is the error.
func oldCacheFiles(l Locations) (ours, other []string, err error) {
	if l.OldScriptCache == "" {
		return nil, nil, nil
	}
	info, err := os.Lstat(l.OldScriptCache)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil, nil, nil
	case err != nil:
		return nil, nil, err
	case !info.IsDir():
		return nil, []string{operation.ShowPath(l.Home, l.OldScriptCache)}, nil
	}
	entries, err := os.ReadDir(l.OldScriptCache)
	if err != nil {
		return nil, nil, err
	}
	for _, entry := range entries {
		if entry.Type().IsRegular() && oldCacheName.MatchString(entry.Name()) {
			ours = append(ours, entry.Name())
		} else {
			other = append(
				other,
				operation.ShowPath(l.Home, filepath.Join(l.OldScriptCache, entry.Name())),
			)
		}
	}
	return ours, other, nil
}
