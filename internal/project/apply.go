package project

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Sawmonabo/workbench/internal/operation"
)

// Apply writes an approved proposal's changes under a project checkpoint,
// resolving native dependencies first when selected, and returns the
// checkpoint ID.
func Apply(
	ctx context.Context,
	c operation.Context,
	p *Proposal,
	options ConfigureOptions,
	consent operation.Consent,
) (_ string, err error) {
	defer operation.Annotate(&err, "configure project %s", c.Scope.Root)
	var checkpointID string
	err = operation.WithMutation(
		ctx,
		c,
		p.Plan,
		consent,
		func(ctx context.Context, preview operation.Context) (operation.Plan, error) {
			current, err := Plan(ctx, preview, options)
			if err == nil {
				p = current
			}
			return current.Plan, err
		},
		func(m *operation.Mutation) error {
			if len(p.native) > 0 {
				approvedDigest := p.Plan.Digest()
				if err := p.resolveNative(ctx, c, m); err != nil {
					return err
				}
				preview := c
				preview.ReadOnly = true
				current, err := Plan(ctx, preview, options)
				if err != nil {
					return operation.Fail(
						operation.ExitConflict,
						"project_conflict",
						"Project inputs changed during native staging; project files remain unchanged",
					)
				}
				currentDigest := current.Plan.Digest()
				if currentDigest != approvedDigest {
					return operation.Fail(
						operation.ExitConflict,
						"project_conflict",
						"Project inputs changed during native staging; review a fresh plan",
					)
				}
			}
			if len(p.Changes) == 0 {
				return nil
			}
			checkpoint, err := operation.BeginCheckpoint(m, p.Plan, nil, p.Changes)
			if err != nil {
				return err
			}
			checkpointID = checkpoint.ID
			return checkpoint.Apply(ctx)
		},
	)
	return checkpointID, err
}

// resolveNative runs native uv in a fresh private metadata-only workspace per
// owner. No application files or executable local backends are copied into
// staging. The resolved images replace the approved postimages, and they
// become durable in the shared checkpoint before any project write.
func (p *Proposal) resolveNative(
	ctx context.Context,
	c operation.Context,
	m *operation.Mutation,
) error {
	scratch, err := os.MkdirTemp("", "workbench-project-resolution-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(scratch) }()
	for _, request := range p.native {
		relative, err := filepath.Rel(c.Scope.Root, request.Owner)
		if err != nil {
			return err
		}
		root := filepath.Join(scratch, "projects", relative)
		if err = p.stageMetadata(root, request.Owner); err != nil {
			return err
		}
		if err = p.runResolution(ctx, c, m, scratch, root, request.Missing); err != nil {
			return err
		}
		if err = p.collectResolution(root, request.Owner); err != nil {
			return err
		}
	}
	// Source, target preimages, scope and external effects were approved before
	// resolution. Only native staged postimages are refined; no new targets or
	// broader execution authority may appear here.
	for i := range p.Plan.Inputs {
		if p.Plan.Inputs[i].Name == "checkpoint-images" {
			p.Plan.Inputs[i].Digest = operation.ChangesDigest(p.Changes)
		}
	}
	return nil
}

// stageMetadata copies owner's proposed manifests and its lockfile into root.
func (p *Proposal) stageMetadata(root, owner string) error {
	for _, project := range p.Inventory.Projects {
		if project.Language != "python" || project.Owner != owner {
			continue
		}
		name := filepath.Join(project.Root, "pyproject.toml")
		data := p.Inventory.inputs[name]
		if change := p.change(name); change != nil {
			data = change.After.Data
		}
		relative, err := filepath.Rel(owner, name)
		if err != nil {
			return err
		}
		target := filepath.Join(root, relative)
		if err = os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			return err
		}
		if err = os.WriteFile(target, data, 0o600); err != nil {
			return err
		}
	}
	lock := p.Inventory.inputs[filepath.Join(owner, "uv.lock")]
	return os.WriteFile(filepath.Join(root, "uv.lock"), lock, 0o600)
}

// runResolution adds the missing checks to the dev group of the staged project
// at root with native uv.
func (p *Proposal) runResolution(
	ctx context.Context,
	c operation.Context,
	m *operation.Mutation,
	scratch, root string,
	missing []string,
) error {
	var uv, python operation.Dependency
	for _, dep := range p.Plan.Dependencies {
		switch dep.Name {
		case "uv":
			uv = dep
		case "python3":
			python = dep
		}
	}
	// With --no-config, user/system uv configuration and credentials are not
	// inherited. Workspace definitions still come from the project manifest.
	args := []string{
		"add",
		"--dev",
		"--no-sync",
		"--no-build",
		"--no-config",
		"--no-python-downloads",
		"--python",
		python.Path,
		"--cache-dir",
		filepath.Join(scratch, "cache"),
		"--keyring-provider",
		"disabled",
	}
	_, err := operation.Run(
		ctx,
		c,
		m,
		operation.Process{
			Executable: uv.Path,
			Args:       append(args, missing...),
			Directory:  root,
			Environment: []string{
				"PATH=" + filepath.Dir(python.Path) + ":/usr/bin:/bin",
				"HOME=" + scratch,
				"XDG_CONFIG_HOME=" + scratch,
				"UV_NO_PROGRESS=1",
			},
			Timeout:     5 * time.Minute,
			OutputLimit: 1 << 20,
			Mutates:     true,
		},
	)
	if err != nil && !errors.Is(err, context.Canceled) {
		err = operation.Fail(
			operation.ExitFailed,
			"project_resolution",
			"Native uv staging failed; project files remain unchanged. "+err.Error(),
		)
	}
	return err
}

// collectResolution replaces owner's approved manifest and lockfile postimages
// with the resolved files from root.
func (p *Proposal) collectResolution(root, owner string) error {
	for _, name := range []string{"pyproject.toml", "uv.lock"} {
		data, err := readMetadata(filepath.Join(root, name))
		if err != nil {
			return err
		}
		if data == nil {
			return operation.Fail(
				operation.ExitFailed,
				"project_resolution",
				"Native resolution did not produce its required manifest and lockfile",
			)
		}
		if _, err = decodeTOML(data); err != nil {
			return err
		}
		change := p.change(filepath.Join(owner, name))
		if change == nil {
			return operation.Fail(
				operation.ExitConflict,
				"project_resolution",
				"Native output was not an explicitly approved target; no project files changed",
			)
		}
		change.After.Data = data
	}
	return nil
}

// RecoveryInstructions tells the user how to revert checkpoint id.
func RecoveryInstructions(id string) string {
	if id == "" {
		return "No project files changed"
	}
	return "Project checkpoint " + id + "; use project revert with the same selected scope and --checkpoint " + strings.TrimSpace(
		id,
	)
}
