package project

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Sawmonabo/workbench/internal/operation"
)

func Apply(ctx context.Context, c operation.Context, p *Proposal, options ConfigureOptions, consent operation.Consent) (string, error) {
	var checkpointID string
	err := operation.WithMutation(ctx, c, p.Plan, consent, func(ctx context.Context, preview operation.Context) (operation.Plan, error) {
		current, err := Plan(ctx, preview, options)
		if err == nil {
			p = current
		}
		return current.Plan, err
	}, func(m *operation.Mutation) error {
		if len(p.native) > 0 {
			approvedDigest, _ := p.Plan.Digest()
			if err := p.resolveNative(ctx, c, m); err != nil {
				return err
			}
			preview := c
			preview.ReadOnly = true
			current, err := Plan(ctx, preview, options)
			if err != nil {
				return operation.Fail(4, "project_conflict", "Project inputs changed during native staging; project files remain unchanged")
			}
			currentDigest, _ := current.Plan.Digest()
			if currentDigest != approvedDigest {
				return operation.Fail(4, "project_conflict", "Project inputs changed during native staging; review a fresh plan")
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
	})
	return checkpointID, err
}

// Native mutation runs in a fresh private metadata-only workspace. Exact
// resolved images become durable in the shared checkpoint before project writes.
// No application files or executable local backends are copied into staging.
func (p *Proposal) resolveNative(ctx context.Context, c operation.Context, m *operation.Mutation) error {
	var uv, python operation.Dependency
	for _, dep := range p.Plan.Dependencies {
		if dep.Name == "uv" {
			uv = dep
		}
		if dep.Name == "python3" {
			python = dep
		}
	}
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
		for _, project := range p.Inventory.Projects {
			if project.Language != "python" || project.Owner != request.Owner {
				continue
			}
			name := filepath.Join(project.Root, "pyproject.toml")
			data := p.Inventory.inputs[name]
			for _, change := range p.Changes {
				if change.Path == name {
					data = change.After.Data
				}
			}
			rel, err := filepath.Rel(request.Owner, name)
			if err != nil {
				return err
			}
			target := filepath.Join(root, rel)
			if err = os.MkdirAll(filepath.Dir(target), 0700); err != nil {
				return err
			}
			if err = os.WriteFile(target, data, 0600); err != nil {
				return err
			}
		}
		lock := filepath.Join(request.Owner, "uv.lock")
		if err = os.WriteFile(filepath.Join(root, "uv.lock"), p.Inventory.inputs[lock], 0600); err != nil {
			return err
		}
		// With --no-config, user/system uv configuration and credentials are not
		// inherited. Workspace definitions still come from the project manifest.
		args := []string{"add", "--dev", "--no-sync", "--no-build", "--no-config", "--no-python-downloads", "--python", python.Path, "--cache-dir", filepath.Join(scratch, "cache"), "--keyring-provider", "disabled"}
		args = append(args, request.Missing...)
		_, err = operation.Run(ctx, c, m, operation.Process{Executable: uv.Path, Args: args, Directory: root, Environment: []string{"PATH=" + filepath.Dir(python.Path) + ":/usr/bin:/bin", "HOME=" + scratch, "XDG_CONFIG_HOME=" + scratch, "UV_NO_PROGRESS=1"}, Timeout: 5 * time.Minute, OutputLimit: 1 << 20, Mutates: true})
		if err != nil {
			return operation.Fail(1, "project_resolution", "Native uv metadata staging failed; project files remain unchanged. Local/dynamic sources may require a separately reviewed native operation")
		}
		for _, name := range []string{"pyproject.toml", "uv.lock"} {
			data, err := readMetadata(filepath.Join(root, name))
			if err != nil {
				return err
			}
			if data == nil {
				return operation.Fail(1, "project_resolution", "Native resolution did not produce its required manifest and lockfile")
			}
			if _, err = decodeTOML(data); err != nil {
				return err
			}
			path := filepath.Join(request.Owner, name)
			replaced := false
			for i := range p.Changes {
				if p.Changes[i].Path == path {
					p.Changes[i].After.Data = data
					replaced = true
				}
			}
			if !replaced {
				return operation.Fail(4, "project_resolution", "Native output was not an explicitly approved target; no project files changed")
			}
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

func RecoveryInstructions(id string) string {
	if id == "" {
		return "No project files changed"
	}
	return "Project checkpoint " + id + "; use project revert with the same selected scope and --checkpoint " + strings.TrimSpace(id)
}
