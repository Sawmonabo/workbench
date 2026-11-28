package machine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/Sawmonabo/workbench/internal/operation"
)

// readTargets binds the current image of every managed target. Protected
// ancestor directories are returned as containers and bound by type and mode.
func (p *preparation) readTargets(
	ctx context.Context,
	c operation.Context,
) ([]string, map[string]bool, error) {
	managed, err := p.run(
		ctx,
		c,
		"managed",
		"--exclude=scripts",
		"--nul-path-separator",
		"--path-style=absolute",
	)
	if err != nil {
		return nil, nil, err
	}
	targets := strings.Split(strings.TrimSuffix(managed, "\x00"), "\x00")
	slices.Sort(targets)
	containers := map[string]bool{}
	for _, target := range targets {
		if err = c.ValidateTarget(target); err != nil {
			if containerErr := c.ValidateContainer(target); containerErr != nil {
				return nil, nil, err
			}
			containers[target] = true
		}
		image, readErr := operation.ReadImage(c, target)
		if containers[target] {
			info, statErr := os.Lstat(target)
			readErr = statErr
			if statErr == nil {
				image = operation.Image{
					Kind: operation.ImageDirectory,
					Mode: uint32(info.Mode().Perm()),
				}
			}
		}
		if readErr != nil {
			return nil, nil, readErr
		}
		p.Plan.Inputs = append(
			p.Plan.Inputs,
			operation.Input{Name: target, Digest: operation.ImageDigest(image)},
		)
	}
	return targets, containers, nil
}

// planEdits turns native status into plan edits, refusing changes to protected
// ancestors and binding any target native reports outside the managed list.
func (p *preparation) planEdits(
	ctx context.Context,
	c operation.Context,
	targets []string,
	containers map[string]bool,
	label string,
) error {
	status, err := p.run(ctx, c, "status", "--exclude=scripts", "--path-style=absolute")
	if err != nil {
		return err
	}
	for line := range strings.SplitSeq(status, "\n") {
		if line == "" {
			continue
		}
		if len(line) < 4 || line[2] != ' ' {
			return operation.Fail(operation.ExitFailed, "native", "Unexpected native status output")
		}
		target := line[3:]
		if containers[target] && line[1] != ' ' {
			if err = p.folderMode(c, target, line[1]); err != nil {
				return err
			}
			continue
		}
		if !containers[target] {
			if err = c.ValidateTarget(target); err != nil {
				return err
			}
		}
		if !slices.Contains(targets, target) {
			image, readErr := operation.ReadImage(c, target)
			if readErr != nil {
				return readErr
			}
			p.Plan.Inputs = append(
				p.Plan.Inputs,
				operation.Input{Name: target, Digest: operation.ImageDigest(image)},
			)
		}
		actions := map[byte]string{'A': "create", 'M': "modify", 'D': "remove", ' ': "unchanged"}
		action := actions[line[1]]
		if action == "" {
			return operation.Fail(
				operation.ExitFailed,
				"native",
				"Unsupported native target action",
			)
		}
		if action != "unchanged" {
			p.Plan.Edits = append(
				p.Plan.Edits,
				operation.Edit{
					Path:        target,
					Action:      action,
					Description: "Native chezmoi configuration change (" + label + ")",
				},
			)
		}
	}
	p.countUnchanged(c, targets, containers)
	return nil
}

// countUnchanged counts the managed files and links the plan leaves as they
// are. Native status lists only entries that differ, so this counts them from
// the managed list instead of from status lines.
func (p *preparation) countUnchanged(
	c operation.Context,
	targets []string,
	containers map[string]bool,
) {
	changing := map[string]bool{}
	for _, edit := range p.Plan.Edits {
		changing[edit.Path] = true
	}
	for _, target := range targets {
		relative, err := filepath.Rel(c.Native.Destination, target)
		if err != nil || changing[target] || containers[target] {
			continue
		}
		if kind := p.desired[relative].Type; kind == "file" || kind == "symlink" {
			p.Plan.UnchangedTargets++
		}
	}
}

// activeSources returns what this apply would actually do: the files the plan
// changes, relative to the destination, and for a full apply the scripts native
// chezmoi would run, by name without chezmoi's prefixes and .sh suffix. A
// once-only script that already ran, or an on-change script whose content is
// unchanged, is not among them. Rendering scripts only reads source files.
func (p *preparation) activeSources(
	ctx context.Context,
	c operation.Context,
) (map[string]bool, error) {
	active := map[string]bool{}
	for _, edit := range p.Plan.Edits {
		if relative, err := filepath.Rel(c.Native.Destination, edit.Path); err == nil {
			active[relative] = true
		}
	}
	status, err := p.run(ctx, c, "status", "--include=scripts")
	if err != nil {
		return nil, err
	}
	for line := range strings.SplitSeq(status, "\n") {
		if len(line) > 3 && line[1] == 'R' {
			active[strings.TrimSuffix(filepath.Base(line[3:]), ".sh")] = true
		}
	}
	return active, nil
}

// readDesired reads the image native renders for every target.
func (p *preparation) readDesired(ctx context.Context, c operation.Context) error {
	rendered, err := p.run(ctx, c, "dump", "--exclude=scripts", "--format=json")
	if err != nil {
		return err
	}
	if json.Unmarshal([]byte(rendered), &p.desired) != nil {
		return operation.Fail(
			operation.ExitFailed,
			"native",
			"Unexpected native target-image output",
		)
	}
	return nil
}

// nativeEntry is one target in native chezmoi's dump output, which gives a
// link's target as linkname and no mode for a link.
type nativeEntry struct {
	Type     string `json:"type"`
	Perm     uint32 `json:"perm"`
	Contents string `json:"contents"`
	Linkname string `json:"linkname"`
}

// image is the entry's image; a link gets linkMode, the mode a link native
// makes in this run gets.
func (e nativeEntry) image(linkMode uint32) (operation.Image, error) {
	switch e.Type {
	case "file":
		return operation.Image{
			Kind: operation.ImageFile,
			Mode: e.Perm,
			Data: []byte(e.Contents),
		}, nil
	case "dir":
		return operation.Image{Kind: operation.ImageDirectory, Mode: e.Perm}, nil
	case "symlink":
		return operation.Image{Kind: operation.ImageSymlink, Mode: linkMode, Link: e.Linkname}, nil
	}
	return operation.Image{}, operation.Fail(
		operation.ExitBlocked,
		"native",
		"Native target type is unsupported for recovery",
	)
}

// folderMode allows native to change only the mode of a directory that holds
// Workbench's own files, such as ~/.config on Linux, and only to one without
// group or other write. It is an ordinary checkpointed edit, so revert
// restores the mode; any other change there is refused.
func (p *preparation) folderMode(c operation.Context, target string, action byte) error {
	relative, err := filepath.Rel(c.Native.Destination, target)
	if err != nil {
		return err
	}
	desired, ok := p.desired[relative]
	info, statErr := os.Lstat(target)
	if action != 'M' || !ok || desired.Type != "dir" || desired.Perm&0o022 != 0 ||
		statErr != nil || !info.IsDir() {
		return operation.Fail(
			operation.ExitBlocked,
			"scope",
			"Native plan would replace or loosen "+c.ShowPath(
				target,
			)+", which holds Workbench's own files",
		)
	}
	// Revert must be able to restore the mode, so the checkpoint has to record
	// the folder. It admits what macOS puts on ~/Library/Application Support
	// (the "deny delete" ACL) and on ~/Library above it (the hidden flag, the
	// same ACL); a folder with other flags, ACLs or attributes is refused with
	// the chmod to run.
	if _, err = operation.ReadImage(c, target); err != nil {
		// A refusal names the folder and its cause in its own words; another
		// error is said after the folder.
		shown, cause := c.ShowPath(target), err.Error()
		reason := shown + " holds Workbench's own files, and Workbench cannot record it" +
			" to undo the mode the plan gives it: " + cause
		if problem, ok := errors.AsType[*operation.Error](err); ok {
			reason = problem.Message
		}
		return operation.Fail(
			operation.ExitBlocked,
			"scope",
			fmt.Sprintf(
				"%s. Instead, you can run chmod %04o %s to give it the configuration's mode yourself, then retry",
				strings.TrimSuffix(reason, "."),
				desired.Perm,
				c.ShellPath(target),
			),
		)
	}
	p.Plan.Edits = append(p.Plan.Edits, operation.Edit{
		Path:        target,
		Action:      "modify",
		Description: "Folder that holds Workbench's own files; mode only",
	})
	return nil
}
