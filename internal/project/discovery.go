package project

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/Sawmonabo/workbench/internal/operation"
	"github.com/pelletier/go-toml/v2"
)

// Project is one discovered project root and who owns its tooling.
type Project struct {
	Root      string `json:"root"`
	Language  string `json:"language"`
	Manager   string `json:"manager"`
	Owner     string `json:"configuration_owner"`
	Lockfile  string `json:"lockfile,omitempty"`
	Supported bool   `json:"supported"`
	Reason    string `json:"reason,omitempty"`
	metadata  map[string]any
}

// readMetadata rejects symlinks and bounds every read. Ancestors are read only
// to discover ownership; they never expand the selected mutation scope.
func readMetadata(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > 8<<20 {
		return nil, operation.Fail(
			operation.ExitBlocked,
			"manifest",
			"Metadata must be a bounded regular file: "+filepath.Base(path),
		)
	}
	root, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		return nil, err
	}
	defer func() { _ = root.Close() }()
	file, err := root.Open(filepath.Base(path))
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	data, err := io.ReadAll(io.LimitReader(file, (8<<20)+1))
	if len(data) > 8<<20 {
		return nil, operation.Fail(
			operation.ExitBlocked,
			"manifest",
			"Metadata exceeds the input bound",
		)
	}
	return data, err
}

func (r *Inventory) read(path string) ([]byte, error) {
	if data, ok := r.inputs[path]; ok {
		return data, nil
	}
	data, err := readMetadata(path)
	if err == nil {
		total := len(data)
		for _, input := range r.inputs {
			total += len(input)
		}
		if total > 16<<20 {
			return nil, operation.Fail(
				operation.ExitBlocked,
				"manifest",
				"Combined metadata exceeds 16 MiB; select a narrower scope",
			)
		}
		r.inputs[path] = data
	}
	return data, err
}

func table(value any) map[string]any { result, _ := value.(map[string]any); return result }

func nested(doc map[string]any, keys ...string) map[string]any {
	for _, key := range keys {
		doc = table(doc[key])
	}
	return doc
}

func stringsOf(value any) []string {
	var result []string
	switch list := value.(type) {
	case []any:
		for _, item := range list {
			if s, ok := item.(string); ok {
				result = append(result, s)
			}
		}
	case []string:
		result = list
	}
	return result
}

func decodeTOML(data []byte) (map[string]any, error) {
	var doc map[string]any
	if err := toml.Unmarshal(data, &doc); err != nil {
		return nil, operation.Fail(
			operation.ExitInvalid,
			"toml",
			"Invalid TOML metadata; correct it before planning",
		)
	}
	return doc, nil
}

// resolve turns the inspected manifests into projects and records every
// metadata file read as a plan input.
func (r *Inventory) resolve(ctx context.Context) error {
	r.inputs = make(map[string][]byte)
	for _, item := range r.Items {
		if err := ctx.Err(); err != nil {
			return err
		}
		if item.Kind == "nested repository (not scanned)" {
			r.Warnings = append(r.Warnings, "Nested repository excluded: "+item.Path)
			continue
		}
		// Only manifests are read here. Lockfiles and standalone configuration are
		// read, bounded and bound as plan inputs on demand by the owner checks, so
		// an unrelated lockfile of another language neither blocks nor pins a plan.
		if item.Kind != "manifest" {
			continue
		}
		path := filepath.Join(r.Directory, filepath.FromSlash(item.Path))
		data, err := r.read(path)
		if err != nil {
			return err
		}
		project, err := r.manifestProject(path, item.Ecosystem, data)
		if err != nil {
			return err
		}
		r.Projects = append(r.Projects, project)
	}
	if len(r.Projects) == 0 {
		return r.enclosingProject()
	}
	return nil
}

func (r *Inventory) manifestProject(path, ecosystem string, data []byte) (Project, error) {
	project := Project{
		Root:     filepath.Dir(path),
		Language: ecosystem,
		Owner:    filepath.Dir(path),
		Reason:   "Recognized; configuration is not supported for this language",
	}
	var err error
	switch filepath.Base(path) {
	case "pyproject.toml":
		project.metadata, err = decodeTOML(data)
		if err != nil {
			return project, err
		}
		project.Language = "python"
		project.Manager = "unknown"
		project.Reason = "Existing uv.lock or explicit uv workspace ownership required; no project initialization"
		err = r.pythonOwner(&project)
	case "package.json":
		var doc map[string]any
		if json.Unmarshal(data, &doc) != nil {
			return project, operation.Fail(
				operation.ExitInvalid,
				"manifest",
				"Invalid package.json",
			)
		}
		project.Manager = "javascript package manager"
		if manager, ok := doc["packageManager"].(string); ok {
			project.Manager = manager
		}
	case "Cargo.toml":
		project.Manager = "cargo"
		_, err = decodeTOML(data)
	case "go.mod":
		project.Manager = "go"
	}
	return project, err
}

// enclosingProject recognizes the Python manifest above a selected package
// subdirectory. It is reported but cannot authorize writes above selection.
// The search stops at the repository root, like every other ownership walk, so
// a selected repository never reports a project outside itself.
func (r *Inventory) enclosingProject() error {
	if _, err := os.Lstat(filepath.Join(r.Directory, ".git")); err == nil {
		return nil // The selection is a repository root; nothing encloses it.
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return ancestors(filepath.Dir(r.Directory), func(parent string) (bool, error) {
		data, err := r.read(filepath.Join(parent, "pyproject.toml"))
		if err != nil || data == nil {
			return false, err
		}
		doc, err := decodeTOML(data)
		if err != nil {
			return false, err
		}
		p := Project{
			Root:     parent,
			Owner:    parent,
			Language: "python",
			Manager:  "unknown",
			metadata: doc,
		}
		if err = r.pythonOwner(&p); err != nil {
			return false, err
		}
		p.Supported = false
		p.Reason = "Select enclosing project root explicitly: " + p.Owner
		r.Projects = append(r.Projects, p)
		return true, nil
	})
}

// pythonOwner finds who owns p's tooling: the uv workspace that lists it as a
// member, or p itself. It sets Supported only when that owner has a shared
// uv.lock, lies inside the selection and no inherited policy competes.
func (r *Inventory) pythonOwner(p *Project) error {
	if nested(p.metadata, "tool", "poetry") != nil || nested(p.metadata, "tool", "pdm") != nil {
		p.Manager = "poetry/pdm"
		p.Reason = "Poetry/PDM configuration is discovery-only"
		return nil
	}
	owner, decided, err := r.workspaceOwner(p)
	if err != nil || decided {
		return err
	}
	p.Owner = owner
	p.Lockfile = filepath.Join(owner, "uv.lock")
	lock, err := r.read(p.Lockfile)
	if err != nil {
		return err
	}
	if lock == nil {
		p.Reason = "Shared uv.lock is missing; establish native uv ownership before configuring"
		return nil
	}
	if _, err = decodeTOML(lock); err != nil {
		return err
	}
	p.Manager = "uv"
	if _, inherits := nested(p.metadata, "tool", "ruff")["extend"]; inherits {
		manifest := filepath.Join(p.Root, "pyproject.toml")
		p.Reason = "Ruff extend configuration requires inherited-policy ownership review: " + manifest
		return nil
	}
	p.Supported = true
	p.Reason = ""
	if !operation.Within(r.Directory, owner) {
		p.Supported = false
		p.Reason = "Select shared workspace root explicitly: " + owner
	}
	return r.checkStandalone(p)
}

// workspaceOwner walks up from p's root to the repository boundary for the uv
// workspace that owns p. decided reports that p's manager and reason are
// already final: an ancestor's tool policy or workspace pattern needs review.
func (r *Inventory) workspaceOwner(p *Project) (owner string, decided bool, err error) {
	owner = p.Root
	err = ancestors(p.Root, func(parent string) (bool, error) {
		manifest := filepath.Join(parent, "pyproject.toml")
		data, err := r.read(manifest)
		if err != nil || data == nil {
			return false, err
		}
		doc, err := decodeTOML(data)
		if err != nil {
			return false, err
		}
		if parent != p.Root && inheritsToolPolicy(p.metadata, doc) {
			p.Manager = "uv"
			p.Owner = parent
			p.Reason = "Inherited tool policy requires explicit ownership review: " + manifest
			decided = true
			return true, nil
		}
		workspace := nested(doc, "tool", "uv", "workspace")
		if workspace == nil {
			return false, nil
		}
		p.Manager = "uv"
		member, reason, err := workspaceMember(workspace, parent, p.Root)
		switch {
		case err != nil:
			return false, err
		case reason != "":
			p.Reason = reason
			decided = true
			return true, nil
		case member:
			owner = parent
			return true, nil
		}
		// A workspace that excludes its own root keeps looking further up.
		return parent != p.Root, nil
	})
	return owner, decided, err
}

// inheritsToolPolicy reports an ancestor configuring a check that the project
// leaves to inheritance.
func inheritsToolPolicy(project, ancestor map[string]any) bool {
	for _, tool := range []string{"ruff", "ty", "basedpyright"} {
		if nested(project, "tool", tool) == nil && nested(ancestor, "tool", tool) != nil {
			return true
		}
	}
	return false
}

// workspaceMember reports whether root is a member of the uv workspace declared
// at parent. A pattern that cannot be matched exactly returns a review reason.
func workspaceMember(
	workspace map[string]any,
	parent, root string,
) (member bool, reason string, err error) {
	relative, err := filepath.Rel(parent, root)
	if err != nil {
		return false, "", err
	}
	relative = filepath.ToSlash(relative)
	member = parent == root
	for _, pattern := range stringsOf(workspace["members"]) {
		if !exactPattern(pattern) {
			return false, "Workspace pattern needs explicit ownership review", nil
		}
		matched, err := filepath.Match(pattern, relative)
		if err != nil {
			return false, "", operation.Fail(
				operation.ExitInvalid,
				"workspace",
				"Invalid uv workspace member pattern",
			)
		}
		member = member || matched
	}
	for _, pattern := range stringsOf(workspace["exclude"]) {
		if !exactPattern(pattern) {
			return false, "Workspace exclusion pattern needs explicit ownership review", nil
		}
		matched, err := filepath.Match(pattern, relative)
		if err != nil {
			return false, "", operation.Fail(
				operation.ExitInvalid,
				"workspace",
				"Invalid uv workspace exclude pattern",
			)
		}
		member = member && !matched
	}
	return member, "", nil
}

// exactPattern rejects recursive, absolute and parent-relative globs, which
// filepath.Match cannot evaluate the way uv does.
func exactPattern(pattern string) bool {
	return !strings.Contains(pattern, "**") && !strings.HasPrefix(pattern, "/") &&
		!strings.Contains(pattern, "..")
}

// checkStandalone refuses competing standalone tool configuration. Native
// tools may inherit it from any ancestor, so every candidate is recorded.
func (r *Inventory) checkStandalone(p *Project) error {
	return ancestors(p.Root, func(parent string) (bool, error) {
		for _, name := range []string{"ruff.toml", ".ruff.toml", "ty.toml", "pyrightconfig.json", "uv.toml"} {
			data, err := r.read(filepath.Join(parent, name))
			if err != nil {
				return false, err
			}
			if data != nil {
				p.Supported = false
				p.Reason = "Standalone configuration requires reviewed integration: " +
					filepath.Join(parent, name)
			}
		}
		return false, nil
	})
}

// ancestors calls visit for dir and each parent until visit stops the walk or
// the repository root (the directory holding .git) or filesystem root is done.
func ancestors(dir string, visit func(parent string) (stop bool, err error)) error {
	for parent := dir; ; parent = filepath.Dir(parent) {
		if stop, err := visit(parent); stop || err != nil {
			return err
		}
		if _, err := os.Lstat(filepath.Join(parent, ".git")); err == nil {
			return nil
		} else if !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		if parent == filepath.Dir(parent) {
			return nil
		}
	}
}

func (r *Inventory) inputsList() []operation.Input {
	names := make([]string, 0, len(r.inputs))
	for name := range r.inputs {
		names = append(names, name)
	}
	slices.Sort(names)
	result := make([]operation.Input, 0, len(names))
	for _, name := range names {
		value := r.inputs[name]
		digest := "absent"
		if value != nil {
			digest = operation.SHA256Hex(value)
		}
		result = append(result, operation.Input{Name: name, Digest: digest})
	}
	return result
}

func unsupported(p Project) string { return fmt.Sprintf("%s: %s", p.Root, p.Reason) }
