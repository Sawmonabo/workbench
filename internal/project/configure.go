package project

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/Sawmonabo/workbench/internal/operation"
	pythonpolicy "github.com/Sawmonabo/workbench/project/python"
)

type ConfigureOptions struct {
	Release             string
	Extensions          bool
	Ignore              bool
	ResolveDependencies bool
	AllowBuildHooks     bool
	CI                  bool
}
type Proposal struct {
	Plan      operation.Plan           `json:"plan"`
	Inventory *Inventory               `json:"inventory"`
	Warnings  []string                 `json:"warnings,omitempty"`
	Changes   []operation.TargetChange `json:"-"`
	native    []nativeRequest
}
type nativeRequest struct {
	Owner   string
	Missing []string
}

func Plan(
	ctx context.Context,
	c operation.Context,
	options ConfigureOptions,
) (proposal *Proposal, planErr error) {
	p := &Proposal{
		Plan: operation.Plan{
			Scope:    c.Scope,
			Complete: true,
			Source: operation.SourceIdentity{
				Release: options.Release,
				ContentDigest: hash(
					[]byte(
						pythonpolicy.Policy + string(
							pythonpolicy.Extensions,
						) + pythonpolicy.Ignore + adapter,
					),
				),
			},
		},
	}
	defer func() {
		if planErr != nil {
			p.Plan.Complete = false
		}
	}()
	for _, language := range c.Languages {
		if language != "python" {
			return p, operation.Fail(
				3,
				"unsupported_language",
				"Explicitly requested language is discovery-only; only Python configuration is supported",
			)
		}
	}
	inventory, err := Inspect(ctx, c.Scope.Root)
	p.Inventory = inventory
	if err != nil {
		return p, err
	}
	var selected []Project
	for _, project := range inventory.Projects {
		if project.Language != "python" {
			p.Warnings = append(
				p.Warnings,
				"Discovery-only component left untouched: "+project.Root,
			)
			continue
		}
		if !project.Supported {
			if project.Manager != "uv" {
				p.Warnings = append(
					p.Warnings,
					"Discovery-only Python component left untouched: "+unsupported(project),
				)
				continue
			}
			p.Plan.Prerequisites = append(p.Plan.Prerequisites, unsupported(project))
			continue
		}
		selected = append(selected, project)
	}
	p.Plan.Inputs = inventory.inputsList()
	if len(selected) == 0 {
		p.Plan.Prerequisites = append(
			p.Plan.Prerequisites,
			"Select an existing supported uv project or workspace with a shared lockfile",
		)
	}
	if len(p.Plan.Prerequisites) > 0 {
		p.Plan.Complete = false
		return p, operation.Fail(
			3,
			"project_scope",
			"Project ownership or requested integration needs review; no files changed",
		)
	}
	python, library, version, deps, err := adapterDependency(ctx, c)
	p.Plan.Dependencies = deps
	if err != nil {
		p.Plan.Complete = false
		p.Plan.Prerequisites = append(
			p.Plan.Prerequisites,
			"Run separately approved Workbench setup for private Python/TOML Kit; preview does not install dependencies",
		)
		return p, err
	}
	p.Plan.Inputs = append(
		p.Plan.Inputs,
		operation.Input{Name: "tomlkit", Digest: hash([]byte(library + version))},
	)
	optionsData, _ := json.Marshal(options)
	p.Plan.Inputs = append(
		p.Plan.Inputs,
		operation.Input{Name: "project-options", Digest: hash(optionsData)},
	)
	var documents []string
	for _, project := range selected {
		documents = append(
			documents,
			string(inventory.inputs[filepath.Join(project.Root, "pyproject.toml")]),
		)
	}
	rendered, err := transform(ctx, c, python, library, version, options.Release, documents)
	if err != nil {
		return p, err
	}
	owners := map[string]bool{}
	for i, project := range selected {
		if err = p.add(
			c,
			filepath.Join(project.Root, "pyproject.toml"),
			[]byte(rendered[i]),
			"Merge missing Python policy while preserving existing rule choices and application metadata",
		); err != nil {
			return p, err
		}
		owners[project.Owner] = true
	}
	roots := make([]string, 0, len(owners))
	for root := range owners {
		roots = append(roots, root)
	}
	slices.Sort(roots)
	for _, owner := range roots {
		ownerManifest := filepath.Join(owner, "pyproject.toml")
		data, err := inventory.read(ownerManifest)
		if err != nil {
			return p, err
		}
		doc, err := decodeTOML(data)
		if err != nil {
			return p, err
		}
		missing := missingTools(doc)
		if len(missing) > 0 {
			if !options.ResolveDependencies {
				p.Plan.Prerequisites = append(
					p.Plan.Prerequisites,
					"Missing development checks at "+owner+": "+strings.Join(
						missing,
						", ",
					)+"; select --resolve-dependencies to approve native resolution",
				)
			} else {
				if unsupportedUVSettings(inventory, owner) {
					p.Plan.Prerequisites = append(
						p.Plan.Prerequisites,
						"Custom uv resolution settings at "+owner+" need a native integration review; they will not be ignored or converted",
					)
				}
				if !options.AllowBuildHooks && unsafeResolution(inventory, owner) {
					p.Plan.Prerequisites = append(
						p.Plan.Prerequisites,
						"Dynamic metadata, local sources or nonstandard uv resolution requires explicit --allow-build-hooks and review; metadata-only staging may not support this project",
					)
				}
				p.native = append(p.native, nativeRequest{Owner: owner, Missing: missing})
				// Native resolution may change only these two declared files. Bind their
				// current images even when the policy transform itself is unchanged.
				for _, path := range []string{ownerManifest, filepath.Join(owner, "uv.lock")} {
					present := false
					for _, change := range p.Changes {
						present = present || change.Path == path
					}
					if !present {
						image, err := operation.ReadImage(c, path)
						if err != nil {
							return p, err
						}
						p.Changes = append(
							p.Changes,
							operation.TargetChange{Path: path, Before: image, After: image},
						)
					}
					p.Plan.Edits = append(
						p.Plan.Edits,
						operation.Edit{
							Path:        path,
							Action:      "native-resolution",
							Description: "Native uv may update this exact manifest/lockfile after approved dependency resolution",
						},
					)
				}
				p.Plan.Effects = append(
					p.Plan.Effects,
					operation.Effect{
						Name:        "uv-dependency-resolution",
						Description: "Resolve missing development tools with native uv in private metadata staging; may access indexes and download metadata. --no-sync avoids environment synchronization; --no-build is not a sandbox. Project/third-party build execution is " + map[bool]string{true: "explicitly selected", false: "not selected; only static registry/workspace metadata is supported"}[options.AllowBuildHooks],
						Privilege:   "user",
						Recovery:    "Project manifests and lockfiles have exact file recovery; network, package caches and any approved code execution do not",
					},
				)
			}
		}
		if options.Extensions {
			path := filepath.Join(owner, ".vscode", "extensions.json")
			before, err := readMetadata(path)
			if err != nil {
				return p, err
			}
			merged, err := mergeExtensions(before)
			if err != nil {
				return p, err
			}
			if err = p.add(
				c,
				path,
				merged,
				"Merge reviewed unique extension recommendations; does not install extensions",
			); err != nil {
				return p, err
			}
		}
		if options.Ignore {
			path := filepath.Join(owner, ".gitignore")
			before, err := readMetadata(path)
			if err != nil {
				return p, err
			}
			merged, err := mergeIgnore(before)
			if err != nil {
				return p, err
			}
			if err = p.add(
				c,
				path,
				merged,
				"Append relevant Python environment/cache ignore entries preserving order",
			); err != nil {
				return p, err
			}
		}
	}
	if options.CI {
		if err = p.addCI(c, roots); err != nil {
			return p, err
		}
	}
	p.Plan.Inputs = append(
		p.Plan.Inputs,
		operation.Input{Name: "checkpoint-images", Digest: operation.ChangesDigest(p.Changes)},
	)
	for _, change := range p.Changes {
		p.Plan.Inputs = append(
			p.Plan.Inputs,
			operation.Input{Name: change.Path, Digest: operation.ImageDigest(change.Before)},
		)
	}
	p.Plan.RecoveryLimits = []string{
		"Exact project files only; dependency caches, downloads and external execution are not reverted",
	}
	if len(p.Changes) > 0 || len(p.native) > 0 {
		retention, retentionErr := operation.RetentionEffect(c)
		if retentionErr != nil {
			return p, retentionErr
		}
		if retention != nil {
			p.Plan.Effects = append(p.Plan.Effects, *retention)
		}
	}
	if len(p.native) > 0 {
		found := false
		for _, dep := range deps {
			found = found || dep.Name == "uv"
		}
		if !found {
			p.Plan.Prerequisites = append(
				p.Plan.Prerequisites,
				"Qualified uv is required for native dependency resolution",
			)
		}
	}
	if len(p.Plan.Prerequisites) > 0 {
		p.Plan.Complete = false
		return p, operation.Fail(
			3,
			"prerequisites",
			"Review project prerequisites and produce a complete plan before approval",
		)
	}
	return p, nil
}

func (p *Proposal) add(c operation.Context, path string, data []byte, description string) error {
	for _, change := range p.Changes {
		if change.Path == path {
			if bytes.Equal(change.After.Data, data) {
				return nil
			}
			return operation.Fail(
				4,
				"project_conflict",
				"Selected policies propose conflicting changes to a shared file",
			)
		}
	}
	before, err := operation.ReadImage(c, path)
	if err != nil {
		return err
	}
	if before.Kind != "absent" && before.Kind != "file" {
		return operation.Fail(3, "project_target", "Project edits require regular files")
	}
	if before.Kind == "file" && bytes.Equal(before.Data, data) {
		return nil
	}
	parent := filepath.Dir(path)
	if parent != c.Scope.Root {
		if _, err = os.Lstat(parent); os.IsNotExist(err) {
			if filepath.Dir(parent) != c.Scope.Root &&
				!operation.Within(c.Scope.Root, filepath.Dir(parent)) {
				return operation.Fail(3, "project_scope", "Unsupported parent directory creation")
			}
			image, err := operation.ReadImage(c, parent)
			if err != nil {
				return err
			}
			exists := false
			for _, change := range p.Changes {
				exists = exists || change.Path == parent
			}
			if !exists {
				after, err := operation.ImageWithGroup(
					c,
					parent,
					operation.Image{Kind: "directory", Mode: 0o755},
				)
				if err != nil {
					return err
				}
				p.Changes = append(
					p.Changes,
					operation.TargetChange{Path: parent, Before: image, After: after},
				)
			}
		} else if err != nil {
			return err
		}
	}
	mode := uint32(0o644)
	if before.Kind == "file" {
		mode = before.Mode
	}
	after, err := operation.ImageWithGroup(
		c,
		path,
		operation.Image{Kind: "file", Mode: mode, Data: data, Attributes: before.Attributes},
	)
	if err != nil {
		return err
	}
	p.Changes = append(p.Changes, operation.TargetChange{Path: path, Before: before, After: after})
	p.Plan.Edits = append(
		p.Plan.Edits,
		operation.Edit{Path: path, Action: "merge", Description: description},
	)
	return nil
}

func missingTools(doc map[string]any) []string {
	found := map[string]bool{}
	groups := table(doc["dependency-groups"])
	// Preserve other groups and existing versions. The default dev group is the
	// only dependency owner this policy adds to; include-group is expanded.
	var collect func(string, map[string]bool)
	collect = func(name string, seen map[string]bool) {
		if seen[name] {
			return
		}
		seen[name] = true
		for _, entry := range array(groups[name]) {
			if value, ok := entry.(string); ok {
				found[requirementName(value)] = true
			} else if include, ok := table(entry)["include-group"].(string); ok {
				collect(include, seen)
			}
		}
	}
	collect("dev", map[string]bool{})
	for _, value := range stringsOf(nested(doc, "tool", "uv")["dev-dependencies"]) {
		found[requirementName(value)] = true
	}
	var missing []string
	for _, name := range []string{"ruff", "basedpyright"} {
		if !found[name] {
			missing = append(missing, name)
		}
	}
	return missing
}
func array(value any) []any { result, _ := value.([]any); return result }
func requirementName(value string) string {
	end := strings.IndexAny(value, " <>=!~;[@")
	if end >= 0 {
		value = value[:end]
	}
	return strings.ReplaceAll(strings.ToLower(value), "_", "-")
}

func unsupportedUVSettings(inventory *Inventory, owner string) bool {
	for _, project := range inventory.Projects {
		if project.Owner != owner || project.Language != "python" {
			continue
		}
		for key := range nested(project.metadata, "tool", "uv") {
			if key != "workspace" && key != "sources" && key != "dev-dependencies" &&
				key != "managed" &&
				key != "package" {
				return true
			}
		}
	}
	return false
}

func unsafeResolution(inventory *Inventory, owner string) bool {
	for _, project := range inventory.Projects {
		if project.Owner != owner || project.Language != "python" {
			continue
		}
		doc := project.metadata
		if len(stringsOf(nested(doc, "project")["dynamic"])) > 0 {
			return true
		}
		uv := nested(doc, "tool", "uv")
		for key := range uv {
			if key != "workspace" && key != "sources" && key != "dev-dependencies" &&
				key != "managed" &&
				key != "package" {
				return true
			}
		}
		for _, source := range table(uv["sources"]) {
			item := table(source)
			if len(item) != 1 || item["workspace"] != true {
				return true
			}
		}
		for _, value := range stringsOf(nested(doc, "project")["dependencies"]) {
			if strings.Contains(value, "@") {
				return true
			}
		}
		for _, group := range []map[string]any{nested(doc, "project", "optional-dependencies"), table(doc["dependency-groups"])} {
			for _, dependencies := range group {
				for _, value := range stringsOf(dependencies) {
					if strings.Contains(value, "@") {
						return true
					}
				}
			}
		}
	}
	return false
}
