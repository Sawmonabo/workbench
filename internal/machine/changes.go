package machine

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"

	"github.com/Sawmonabo/workbench/internal/operation"
	"github.com/Sawmonabo/workbench/internal/platform"
)

// buildChanges records each edit's exact before image and the after image
// native renders, refusing metadata the checkpoint owner cannot preserve, and
// binds them as the plan's checkpoint-images input (bindImages). It also gives
// each edit what the plan view shows: its plain title, whether it is merged
// into or owned, whether an owned file was edited outside Workbench, and the
// capped, redacted unified diff with its line counts.
func (p *preparation) buildChanges(ctx context.Context, c operation.Context) error {
	desired := p.desired
	last, err := operation.LastApplied(c)
	if err != nil {
		return err
	}
	written, err := p.nativeWritten(ctx, c)
	if err != nil {
		return err
	}
	// A link native makes gets the mode a link Workbench makes gets, since
	// native inherits Workbench's umask; it is measured once, for the first.
	linkMode, measured := uint32(0), false
	var candidates []string
	for _, edit := range p.Plan.Edits {
		if edit.Action != "remove" {
			candidates = append(candidates, edit.Path)
		}
	}
	merged, err := p.mergedTargets(ctx, c, candidates)
	if err != nil {
		return err
	}
	for i, edit := range p.Plan.Edits {
		before, err := operation.ReadImage(c, edit.Path)
		if err != nil {
			return err
		}
		after := operation.Image{Kind: operation.ImageAbsent}
		relative, err := filepath.Rel(c.Native.Destination, edit.Path)
		if err != nil {
			return err
		}
		if edit.Action != "remove" {
			entry, ok := desired[relative]
			if !ok {
				return operation.Fail(
					operation.ExitFailed,
					"native",
					"Native target missing from image enumeration",
				)
			}
			if entry.Type == "symlink" && !measured {
				if linkMode, err = platform.NewLinkMode(p.scratch); err != nil {
					return err
				}
				measured = true
			}
			if after, err = entry.image(linkMode); err != nil {
				return err
			}
		}
		after, err = operation.ImageWithGroup(c, edit.Path, after)
		if err != nil {
			return err
		}
		if err = operation.CheckPreservable(c, edit, before, after, p.scratch); err != nil {
			return err
		}
		var previous *operation.Image
		if image, ok := last[edit.Path]; ok {
			previous = &image
		} else if before.Kind == operation.ImageFile &&
			written[edit.Path] == operation.SHA256Hex(before.Data) {
			// Retention may have removed the checkpoint that wrote this file.
			// Native's own state still says what it last wrote, and the file
			// still is that, so it was not edited outside Workbench.
			previous = &before
		}
		edited := operation.EditedOutside(before, previous)
		if merged[edit.Path] {
			// Native merges into this file and keeps the owner's own keys, so
			// the owner's edits are expected, not replaced.
			previous, edited = &before, false
		}
		planned := &p.Plan.Edits[i]
		planned.Summary = operation.ChangeSummary(before, after, previous)
		planned.Merged, planned.EditedOutside = merged[edit.Path], edited
		planned.Folder = before.Kind == operation.ImageDirectory ||
			after.Kind == operation.ImageDirectory
		planned.Title = fileTitle(relative)
		if added, removed, lines, ok := operation.FileDiff(before, after); ok {
			planned.Added, planned.Removed = added, removed
			planned.Diff, planned.DiffTruncated = capDiff(redactDiff(lines, p.secrets))
			p.describeSettings(planned, before, after)
		}
		p.Changes = append(
			p.Changes,
			operation.TargetChange{Path: edit.Path, Before: before, After: after},
		)
	}
	return p.bindImages(c)
}

// bindImages binds the changes as the plan's checkpoint-images input once
// they pass the checks the checkpoint makes of them.
func (p *preparation) bindImages(c operation.Context) error {
	images, err := operation.CheckpointImages(c, p.Changes)
	if err != nil {
		return err
	}
	p.Plan.Inputs = append(p.Plan.Inputs, images)
	return nil
}

// nativeWritten returns the SHA-256 of the contents native chezmoi last wrote
// to each file target, by absolute path. A target native never wrote has no
// entry.
func (p *preparation) nativeWritten(
	ctx context.Context,
	c operation.Context,
) (map[string]string, error) {
	dump, err := p.run(ctx, c, "state", "dump", "--format=json")
	if err != nil {
		return nil, err
	}
	var state struct {
		EntryState map[string]struct {
			Type           string `json:"type"`
			ContentsSHA256 string `json:"contentsSHA256"`
		} `json:"entryState"`
	}
	if err = json.Unmarshal([]byte(dump), &state); err != nil {
		return nil, operation.Fail(operation.ExitFailed, "native", "Unexpected native state output")
	}
	written := map[string]string{}
	for target, entry := range state.EntryState {
		if entry.Type == "file" && entry.ContentsSHA256 != "" {
			written[target] = entry.ContentsSHA256
		}
	}
	return written, nil
}

// describeSettings replaces the line diff of a merged JSON or TOML file with
// the list of settings that change, when both sides parse: the merge
// re-serializes the file, so its line diff is mostly reordering. Any other file
// keeps its line diff.
func (p *preparation) describeSettings(
	planned *operation.Edit,
	before, after operation.Image,
) {
	format := strings.TrimPrefix(filepath.Ext(planned.Path), ".")
	if !planned.Merged || before.Kind != operation.ImageFile ||
		after.Kind != operation.ImageFile || (format != "json" && format != "toml") {
		return
	}
	changes, rewritten, ok := operation.SettingsDiff(format, before.Data, after.Data)
	if !ok {
		return
	}
	planned.Settings, planned.Semantic, planned.Rewritten = len(changes), true, rewritten
	planned.Diff, planned.DiffTruncated = capDiff(settingLines(changes, p.secrets))
}
