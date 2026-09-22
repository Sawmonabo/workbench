package project

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/Sawmonabo/workbench/internal/operation"
	pythonpolicy "github.com/Sawmonabo/workbench/project/python"
)

// ConfigureOptions selects what project configure changes.
type ConfigureOptions struct {
	Extensions          bool
	Ignore              bool
	ResolveDependencies bool
	AllowBuildHooks     bool
	CI                  bool
}

// Proposal is a project configure plan with the exact changes it approves.
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

// Plan previews configuring the supported uv projects in c's scope. The
// proposal is returned with any error so callers can show what blocked it.
func Plan(
	ctx context.Context,
	c operation.Context,
	options ConfigureOptions,
) (proposal *Proposal, planErr error) {
	p := newProposal(c)
	defer func() {
		if planErr != nil {
			p.Plan.Complete = false
		}
	}()
	if slices.ContainsFunc(
		c.Languages,
		func(language string) bool { return language != "python" },
	) {
		return p, operation.Fail(
			operation.ExitBlocked,
			"unsupported_language",
			"Explicitly requested language is discovery-only; only Python configuration is supported",
		)
	}
	inventory, err := Inspect(ctx, c.Scope.Root)
	p.Inventory = inventory
	if err != nil {
		return p, err
	}
	selected := p.selectProjects()
	p.Plan.Inputs = inventory.inputsList()
	if err = p.blocked(
		"project_scope",
		"Project ownership or requested integration needs review; no files changed",
	); err != nil {
		return p, err
	}
	if err = p.addPolicy(ctx, c, options, selected); err != nil {
		return p, err
	}
	owners := ownersOf(selected)
	for _, owner := range owners {
		if err = p.addOwner(c, owner, options); err != nil {
			return p, err
		}
	}
	if options.CI {
		if err = p.addCI(c, owners); err != nil {
			return p, err
		}
	}
	return p, p.finish(c)
}

// newProposal identifies the source by policy rather than CLI release, so an
// upgrade that keeps the policy proposes no project edits.
func newProposal(c operation.Context) *Proposal {
	policy := pythonpolicy.Policy + string(pythonpolicy.Extensions) + pythonpolicy.Ignore + adapter
	return &Proposal{
		Plan: operation.Plan{
			Scope:    c.Scope,
			Complete: true,
			Source: operation.SourceIdentity{
				Release:       pythonpolicy.ID,
				ContentDigest: operation.SHA256Hex([]byte(policy)),
			},
		},
	}
}

// selectProjects returns the supported Python projects. Other components are
// left untouched with a warning; unsupported uv projects block the plan.
func (p *Proposal) selectProjects() []Project {
	var selected []Project
	for _, project := range p.Inventory.Projects {
		switch {
		case project.Language != "python":
			p.Warnings = append(
				p.Warnings,
				"Discovery-only component left untouched: "+project.Root,
			)
		case !project.Supported && project.Manager != "uv":
			p.Warnings = append(
				p.Warnings,
				"Discovery-only Python component left untouched: "+unsupported(project),
			)
		case !project.Supported:
			p.Plan.Prerequisites = append(p.Plan.Prerequisites, unsupported(project))
		default:
			selected = append(selected, project)
		}
	}
	if len(selected) == 0 {
		p.Plan.Prerequisites = append(
			p.Plan.Prerequisites,
			"Select an existing supported uv project or workspace with a shared lockfile",
		)
	}
	return selected
}

// blocked fails the plan while prerequisites remain.
func (p *Proposal) blocked(category, message string) error {
	if len(p.Plan.Prerequisites) == 0 {
		return nil
	}
	return operation.Fail(operation.ExitBlocked, category, message)
}

// addPolicy merges the Python policy into each selected manifest through the
// private TOML Kit adapter.
func (p *Proposal) addPolicy(
	ctx context.Context,
	c operation.Context,
	options ConfigureOptions,
	selected []Project,
) error {
	python, library, version, deps, err := adapterDependency(ctx, c)
	p.Plan.Dependencies = deps
	if err != nil {
		p.Plan.Prerequisites = append(
			p.Plan.Prerequisites,
			"Run separately approved Workbench setup for private Python/TOML Kit; preview does not install dependencies",
		)
		return err
	}
	optionsData, _ := json.Marshal(options)
	p.Plan.Inputs = append(
		p.Plan.Inputs,
		operation.Input{Name: "tomlkit", Digest: operation.SHA256Hex([]byte(library + version))},
		operation.Input{Name: "project-options", Digest: operation.SHA256Hex(optionsData)},
	)
	documents := make([]string, len(selected))
	for i, project := range selected {
		documents[i] = string(p.Inventory.inputs[filepath.Join(project.Root, "pyproject.toml")])
	}
	rendered, err := transform(ctx, c, python, library, version, documents)
	if err != nil {
		return err
	}
	for i, project := range selected {
		if err = p.add(
			c,
			filepath.Join(project.Root, "pyproject.toml"),
			[]byte(rendered[i]),
			"Merge missing Python policy while preserving existing rule choices and application metadata",
		); err != nil {
			return err
		}
	}
	return nil
}

func ownersOf(projects []Project) []string {
	owners := make([]string, 0, len(projects))
	for _, project := range projects {
		owners = append(owners, project.Owner)
	}
	slices.Sort(owners)
	return slices.Compact(owners)
}

// addOwner plans what belongs to one policy owner: resolving its missing
// checks and the optional extension and ignore merges.
func (p *Proposal) addOwner(c operation.Context, owner string, options ConfigureOptions) error {
	data, err := p.Inventory.read(filepath.Join(owner, "pyproject.toml"))
	if err != nil {
		return err
	}
	doc, err := decodeTOML(data)
	if err != nil {
		return err
	}
	if missing := missingTools(doc); len(missing) > 0 {
		if err = p.addResolution(c, owner, missing, options); err != nil {
			return err
		}
	}
	if options.Extensions {
		if err = p.merge(
			c,
			filepath.Join(owner, ".vscode", "extensions.json"),
			mergeExtensions,
			"Merge reviewed unique extension recommendations; does not install extensions",
		); err != nil {
			return err
		}
	}
	if options.Ignore {
		return p.merge(
			c,
			filepath.Join(owner, ".gitignore"),
			mergeIgnore,
			"Append relevant Python environment/cache ignore entries preserving order",
		)
	}
	return nil
}

// addResolution plans native uv resolution of the checks missing at owner, or
// asks for --resolve-dependencies when it was not selected.
func (p *Proposal) addResolution(
	c operation.Context,
	owner string,
	missing []string,
	options ConfigureOptions,
) error {
	if !options.ResolveDependencies {
		p.Plan.Prerequisites = append(
			p.Plan.Prerequisites,
			"Missing development checks at "+owner+": "+strings.Join(missing, ", ")+
				"; select --resolve-dependencies to approve native resolution",
		)
		return nil
	}
	if p.Inventory.owned(owner, customUVSettings) {
		p.Plan.Prerequisites = append(
			p.Plan.Prerequisites,
			"Custom uv resolution settings at "+owner+" need a native integration review; they will not be ignored or converted",
		)
	}
	if !options.AllowBuildHooks && p.Inventory.owned(owner, unsafeResolution) {
		p.Plan.Prerequisites = append(
			p.Plan.Prerequisites,
			"Dynamic metadata, local sources or nonstandard uv resolution requires explicit --allow-build-hooks and review; metadata-only staging may not support this project",
		)
	}
	p.native = append(p.native, nativeRequest{Owner: owner, Missing: missing})
	// Native resolution may change only these two declared files. Bind their
	// current images even when the policy transform itself is unchanged.
	for _, path := range []string{filepath.Join(owner, "pyproject.toml"), filepath.Join(owner, "uv.lock")} {
		if err := p.bind(c, path); err != nil {
			return err
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
	build := "not selected; only static registry/workspace metadata is supported"
	if options.AllowBuildHooks {
		build = "explicitly selected"
	}
	p.Plan.Effects = append(
		p.Plan.Effects,
		operation.Effect{
			Name:        "uv-dependency-resolution",
			Description: "Resolve missing development tools with native uv in private metadata staging; may access indexes and download metadata. --no-sync avoids environment synchronization; --no-build is not a sandbox. Project/third-party build execution is " + build,
			Privilege:   "user",
			Recovery:    "Project manifests and lockfiles have exact file recovery; network, package caches and any approved code execution do not",
		},
	)
	return nil
}

// finish binds the proposed images and recovery terms into the plan and fails
// while prerequisites remain.
func (p *Proposal) finish(c operation.Context) error {
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
		retention, err := operation.RetentionEffect(c)
		if err != nil {
			return err
		}
		if retention != nil {
			p.Plan.Effects = append(p.Plan.Effects, *retention)
		}
	}
	hasUV := slices.ContainsFunc(
		p.Plan.Dependencies,
		func(dep operation.Dependency) bool { return dep.Name == "uv" },
	)
	if len(p.native) > 0 && !hasUV {
		p.Plan.Prerequisites = append(
			p.Plan.Prerequisites,
			"Qualified uv is required for native dependency resolution",
		)
	}
	return p.blocked(
		"prerequisites",
		"Review project prerequisites and produce a complete plan before approval",
	)
}

// change returns the proposed change to path, or nil.
func (p *Proposal) change(path string) *operation.TargetChange {
	i := slices.IndexFunc(
		p.Changes,
		func(change operation.TargetChange) bool { return change.Path == path },
	)
	if i < 0 {
		return nil
	}
	return &p.Changes[i]
}

// bind records path's current image as an unchanged target unless a change
// already covers it.
func (p *Proposal) bind(c operation.Context, path string) error {
	if p.change(path) != nil {
		return nil
	}
	image, err := operation.ReadImage(c, path)
	if err != nil {
		return err
	}
	p.Changes = append(p.Changes, operation.TargetChange{Path: path, Before: image, After: image})
	return nil
}

// merge proposes merging path's current content with its policy.
func (p *Proposal) merge(
	c operation.Context,
	path string,
	merge func([]byte) ([]byte, error),
	description string,
) error {
	before, err := readMetadata(path)
	if err != nil {
		return err
	}
	merged, err := merge(before)
	if err != nil {
		return err
	}
	return p.add(c, path, merged, description)
}

// add proposes data as path's new content. Proposals for the same path must
// agree; unchanged content adds nothing.
func (p *Proposal) add(c operation.Context, path string, data []byte, description string) error {
	if existing := p.change(path); existing != nil {
		if bytes.Equal(existing.After.Data, data) {
			return nil
		}
		return operation.Fail(
			operation.ExitConflict,
			"project_conflict",
			"Selected policies propose conflicting changes to a shared file",
		)
	}
	before, err := operation.ReadImage(c, path)
	if err != nil {
		return err
	}
	if before.Kind != operation.ImageAbsent && before.Kind != operation.ImageFile {
		return operation.Fail(
			operation.ExitBlocked,
			"project_target",
			"Project edits require regular files",
		)
	}
	if before.Kind == operation.ImageFile && bytes.Equal(before.Data, data) {
		return nil
	}
	if err = p.addParent(c, path); err != nil {
		return err
	}
	mode := uint32(0o644)
	if before.Kind == operation.ImageFile {
		mode = before.Mode
	}
	after, err := operation.ImageWithGroup(
		c,
		path,
		operation.Image{
			Kind:       operation.ImageFile,
			Mode:       mode,
			Data:       data,
			Attributes: before.Attributes,
		},
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

// addParent proposes creating path's missing parent directory inside the
// selected root.
func (p *Proposal) addParent(c operation.Context, path string) error {
	parent := filepath.Dir(path)
	if parent == c.Scope.Root || p.change(parent) != nil {
		return nil
	}
	if _, err := os.Lstat(parent); !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if !operation.Within(c.Scope.Root, filepath.Dir(parent)) {
		return operation.Fail(
			operation.ExitBlocked,
			"project_scope",
			"Unsupported parent directory creation",
		)
	}
	before, err := operation.ReadImage(c, parent)
	if err != nil {
		return err
	}
	after, err := operation.ImageWithGroup(
		c,
		parent,
		operation.Image{Kind: operation.ImageDirectory, Mode: 0o755},
	)
	if err != nil {
		return err
	}
	p.Changes = append(
		p.Changes,
		operation.TargetChange{Path: parent, Before: before, After: after},
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

// nameSeparators matches the runs PEP 503 normalization collapses to "-".
var nameSeparators = regexp.MustCompile(`[-_.]+`)

// requirementName returns the normalized project name of a PEP 508 requirement.
func requirementName(value string) string {
	end := strings.IndexAny(value, " <>=!~;[@")
	if end >= 0 {
		value = value[:end]
	}
	return nameSeparators.ReplaceAllString(strings.ToLower(value), "-")
}

// owned reports whether check holds for any Python project that owner owns.
func (r *Inventory) owned(owner string, check func(doc map[string]any) bool) bool {
	return slices.ContainsFunc(r.Projects, func(project Project) bool {
		return project.Owner == owner && project.Language == "python" && check(project.metadata)
	})
}

// customUVSettings reports [tool.uv] settings beyond workspace membership and
// sources, which native resolution would apply without review.
func customUVSettings(doc map[string]any) bool {
	for key := range nested(doc, "tool", "uv") {
		switch key {
		case "workspace", "sources", "dev-dependencies", "managed", "package":
		default:
			return true
		}
	}
	return false
}

// unsafeResolution reports metadata that resolution may only handle by building
// or fetching project code: dynamic fields, custom uv settings, non-workspace
// sources and direct URL requirements.
func unsafeResolution(doc map[string]any) bool {
	project := nested(doc, "project")
	if len(stringsOf(project["dynamic"])) > 0 || customUVSettings(doc) {
		return true
	}
	for _, source := range nested(doc, "tool", "uv", "sources") {
		if item := table(source); len(item) != 1 || item["workspace"] != true {
			return true
		}
	}
	requirements := stringsOf(project["dependencies"])
	for _, groups := range []map[string]any{table(project["optional-dependencies"]), table(doc["dependency-groups"])} {
		for _, group := range groups {
			requirements = append(requirements, stringsOf(group)...)
		}
	}
	return slices.ContainsFunc(
		requirements,
		func(requirement string) bool { return strings.Contains(requirement, "@") },
	)
}
