package project

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/Sawmonabo/workbench/internal/operation"
	"github.com/Sawmonabo/workbench/project/gopolicy"
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
	r.settleCargoMembers()
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
		project.metadata, err = decodeTOML(data)
		if err != nil {
			return project, err
		}
		err = r.cargoOwner(&project)
	case "go.mod":
		project.Manager = "go"
		err = r.goOwner(&project)
	}
	return project, err
}

// goOwner decides whether the Go module at p's root can be configured: only
// the module at the selected folder, with no go.work at or above it up to the
// repository root. A module below the selected folder is configured by
// selecting its own folder; ./... at the selected module already stops at it.
func (r *Inventory) goOwner(p *Project) error {
	var workspace string
	err := ancestors(p.Root, func(parent string) (bool, error) {
		path := filepath.Join(parent, "go.work")
		data, err := r.read(path)
		if data != nil {
			workspace = path
		}
		return data != nil, err
	})
	switch {
	case err != nil:
		return err
	case workspace != "":
		// go.work makes the go command treat several modules as one build, and
		// ./... does not cross modules, so checks at one module would cover only
		// part of the code.
		p.Reason = "Go workspaces are discovery-only in " + gopolicy.ID + ": " + workspace
	case p.Root != r.Directory:
		p.Reason = "Go module below the selected folder; select the module folder to configure it"
	default:
		p.Supported = true
		p.Reason = ""
	}
	return nil
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
	manifest := filepath.Join(p.Root, "pyproject.toml")
	if _, inherits := nested(p.metadata, "tool", "ruff")["extend"]; inherits {
		p.Reason = "Ruff extend configuration requires inherited-policy ownership review: " + manifest
		return nil
	}
	// basedpyright refuses a pyproject.toml that holds both tool tables, so the
	// policy's [tool.basedpyright] cannot be added beside an existing one.
	if hasTool(p.metadata, "pyright") {
		p.Reason = "Pyright configuration competes with basedpyright; review it before configuring: " +
			manifest
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
// leaves to inheritance. Pyright and basedpyright share one entry because
// basedpyright reads either table and rejects a file holding both.
func inheritsToolPolicy(project, ancestor map[string]any) bool {
	for _, tools := range [][]string{{"ruff"}, {"ty"}, {"basedpyright", "pyright"}} {
		if !hasTool(project, tools...) && hasTool(ancestor, tools...) {
			return true
		}
	}
	return false
}

// hasTool reports whether doc configures any of the named [tool.*] tables.
func hasTool(doc map[string]any, names ...string) bool {
	return slices.ContainsFunc(names, func(name string) bool {
		return nested(doc, "tool", name) != nil
	})
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
// tools may inherit it from any ancestor, so every candidate is recorded. Ruff
// also applies the nearest ruff.toml to the files below it, so one in a folder
// under p's root silently replaces the merged policy there and is refused too.
// The other tools read only the root's configuration.
func (r *Inventory) checkStandalone(p *Project) error {
	refuse := func(path string) {
		p.Supported = false
		p.Reason = "Standalone configuration requires reviewed integration: " + path
	}
	err := ancestors(p.Root, func(parent string) (bool, error) {
		for _, name := range []string{"ruff.toml", ".ruff.toml", "ty.toml", "pyrightconfig.json", "uv.toml"} {
			data, err := r.read(filepath.Join(parent, name))
			if err != nil {
				return false, err
			}
			if data != nil {
				refuse(filepath.Join(parent, name))
			}
		}
		return false, nil
	})
	if err != nil {
		return err
	}
	for _, item := range r.Items {
		path := filepath.Join(r.Directory, filepath.FromSlash(item.Path))
		name := filepath.Base(path)
		// Files in p.Root itself were read by the walk above.
		if item.Kind != "configuration" || (name != "ruff.toml" && name != ".ruff.toml") ||
			filepath.Dir(path) == p.Root || !operation.Within(p.Root, path) {
			continue
		}
		if _, err = r.read(path); err != nil {
			return err
		}
		refuse(path)
	}
	return nil
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

// cargoOwner decides who owns the Cargo package or workspace at p's root,
// from the manifests' text alone: no cargo command runs, because a
// rust-toolchain.toml can make rustup download a toolchain before Cargo
// starts. A workspace root at the selected folder owns itself and its
// members. A package inside a workspace is configured only through that
// workspace's root, and one below the selected folder through its own.
func (r *Inventory) cargoOwner(p *Project) error {
	workspace, isRoot := p.metadata["workspace"]
	pkg := nested(p.metadata, "package")
	switch {
	case !isRoot && pkg == nil:
		p.Reason = "Cargo.toml has neither [package] nor [workspace]"
		return nil
	case isRoot:
		if reason := cargoPatterns(table(workspace)); reason != "" {
			p.Reason = reason
			return nil
		}
		if p.Root != r.Directory {
			p.Reason = "Cargo workspace below the selected folder; select its folder to configure it"
			return nil
		}
		members, reason, err := r.cargoMembers(p.Root, p.metadata)
		if err != nil || reason != "" {
			p.Reason = reason
			return err
		}
		p.Supported, p.Reason = true, ""
		r.cargo = members
		return nil
	}
	if _, explicit := pkg["workspace"]; explicit {
		p.Reason = "package.workspace names the workspace root; review this layout explicitly"
		return nil
	}
	root, err := r.cargoWorkspace(p.Root)
	switch {
	case err != nil:
		return err
	case root == "" && p.Root != r.Directory:
		p.Reason = "Cargo package below the selected folder; select its folder to configure it"
	case root == "":
		p.Supported, p.Reason = true, ""
	case root != r.Directory:
		// Cargo builds a package inside a workspace only as that workspace's
		// member, so the root's settings are what apply to it.
		p.Owner = root
		p.Reason = "Inside the Cargo workspace at " + root + "; select the workspace root"
	default:
		// Membership at the selected root is settled once every manifest is
		// read (settleCargoMembers).
		p.Owner = root
	}
	return nil
}

// cargoWorkspace returns the nearest folder above root, up to the repository
// root, whose Cargo.toml declares a workspace that does not exclude root, or
// "" when there is none. That is the workspace Cargo puts the package in.
func (r *Inventory) cargoWorkspace(root string) (string, error) {
	var found string
	err := ancestors(root, func(parent string) (bool, error) {
		if parent == root {
			return false, nil
		}
		data, err := r.read(filepath.Join(parent, "Cargo.toml"))
		if err != nil || data == nil {
			return false, err
		}
		doc, err := decodeTOML(data)
		if err != nil {
			return false, err
		}
		workspace, ok := doc["workspace"]
		if !ok {
			return false, nil
		}
		listed, _, err := cargoListed(table(workspace), parent, root)
		if err != nil {
			return false, err
		}
		if !listed && cargoExcluded(table(workspace), parent, root) {
			return false, nil
		}
		found = parent
		return true, nil
	})
	return found, err
}

// cargoPatterns returns why a workspace's members or exclude entries need
// review: a pattern filepath.Match cannot evaluate as Cargo does, or a
// members pattern naming a folder that discovery skips, whose members would
// otherwise be missed silently.
func cargoPatterns(workspace map[string]any) string {
	for _, pattern := range stringsOf(workspace["members"]) {
		if !exactPattern(pattern) {
			return "Workspace pattern needs explicit ownership review: " + pattern
		}
		for segment := range strings.SplitSeq(path.Clean(pattern), "/") {
			if segment != "." && excludedDirectory(segment) {
				return "Workspace member pattern names a folder discovery skips, so its members cannot be found: " +
					pattern
			}
		}
	}
	for _, pattern := range stringsOf(workspace["exclude"]) {
		if !exactPattern(pattern) {
			return "Workspace exclusion pattern needs explicit ownership review: " + pattern
		}
	}
	return ""
}

// cargoListed reports whether a workspace's members patterns at parent name
// root, through the same matching as uv workspaces. Cargo accepts "./a/" for
// "a", so patterns are cleaned first.
func cargoListed(workspace map[string]any, parent, root string) (bool, string, error) {
	var members []any
	for _, pattern := range stringsOf(workspace["members"]) {
		if !exactPattern(pattern) {
			members = append(members, pattern)
			continue
		}
		members = append(members, path.Clean(pattern))
	}
	listed, reason, err := workspaceMember(map[string]any{"members": members}, parent, root)
	return listed && parent != root, reason, err
}

// cargoExcluded reports whether an exclude entry of the workspace at parent
// covers root. Cargo reads each entry as a path prefix, not a glob.
func cargoExcluded(workspace map[string]any, parent, root string) bool {
	for _, entry := range stringsOf(workspace["exclude"]) {
		if operation.Within(filepath.Join(parent, filepath.FromSlash(entry)), root) {
			return true
		}
	}
	return false
}

// cargoMembers returns the member folders of the workspace at root, whose
// manifest is doc: its own package, the packages its members patterns list,
// and every path dependency of a member inside the workspace folder, as
// Cargo adds them. reason names a member layout that needs review.
func (r *Inventory) cargoMembers(root string, doc map[string]any) (map[string]bool, string, error) {
	workspace := table(doc["workspace"])
	members := map[string]bool{}
	var queue []string
	if nested(doc, "package") != nil {
		queue = append(queue, root)
	}
	for _, item := range r.Items {
		dir := filepath.Dir(filepath.Join(r.Directory, filepath.FromSlash(item.Path)))
		if item.Kind != "manifest" || filepath.Base(item.Path) != "Cargo.toml" || dir == root {
			continue
		}
		listed, reason, err := cargoListed(workspace, root, dir)
		if err != nil || reason != "" {
			return nil, reason, err
		}
		if listed {
			queue = append(queue, dir)
		}
	}
	shared := table(workspace["dependencies"])
	for len(queue) > 0 {
		dir := queue[0]
		queue = queue[1:]
		if members[dir] {
			continue
		}
		members[dir] = true
		manifest := doc
		if dir != root {
			data, err := r.read(filepath.Join(dir, "Cargo.toml"))
			if err != nil || data == nil {
				return nil, "", err
			}
			if manifest, err = decodeTOML(data); err != nil {
				return nil, "", err
			}
		}
		for _, dependency := range cargoPathDependencies(manifest, dir, shared, root) {
			if members[dependency] {
				continue
			}
			member, reason, err := r.cargoPathMember(root, workspace, dependency)
			if err != nil || reason != "" {
				return nil, reason, err
			}
			if member {
				queue = append(queue, dependency)
			}
		}
	}
	return members, "", nil
}

// cargoPathMember reports whether a member's path dependency is a member of
// the workspace at root too: a package inside the workspace folder that the
// workspace does not exclude. One in a folder discovery skips needs review.
func (r *Inventory) cargoPathMember(
	root string,
	workspace map[string]any,
	dependency string,
) (member bool, reason string, err error) {
	if !operation.Within(root, dependency) || cargoExcluded(workspace, root, dependency) {
		return false, "", nil
	}
	relative, err := filepath.Rel(root, dependency)
	if err != nil {
		return false, "", err
	}
	if slices.ContainsFunc(strings.Split(filepath.ToSlash(relative), "/"), excludedDirectory) {
		return false, "Path dependency in a folder discovery skips needs review: " + dependency, nil
	}
	data, err := r.read(filepath.Join(dependency, "Cargo.toml"))
	return data != nil, "", err
}

// cargoPathDependencies returns the folders of manifest's path dependencies
// (normal, dev and build, also per target), with an entry inheriting a
// workspace dependency resolved against the workspace root. [patch] paths do
// not make members and are not read.
func cargoPathDependencies(
	manifest map[string]any,
	dir string,
	shared map[string]any,
	root string,
) []string {
	tables := []map[string]any{manifest}
	for _, target := range table(manifest["target"]) {
		tables = append(tables, table(target))
	}
	var result []string
	for _, owner := range tables {
		for _, kind := range []string{"dependencies", "dev-dependencies", "build-dependencies"} {
			for name, entry := range table(owner[kind]) {
				spec := table(entry)
				base := dir
				if spec["workspace"] == true {
					spec, base = table(shared[name]), root
				}
				if location, ok := spec["path"].(string); ok {
					result = append(result, filepath.Join(base, filepath.FromSlash(location)))
				}
			}
		}
	}
	slices.Sort(result)
	return slices.Compact(result)
}

// settleCargoMembers marks each package inside the selected workspace root as
// a supported member, or as untouched when Cargo would not count it as one.
// Members of a root that needs review are not configured either.
func (r *Inventory) settleCargoMembers() {
	var root *Project
	for i := range r.Projects {
		project := &r.Projects[i]
		if project.Manager == "cargo" && project.Root == r.Directory &&
			project.Owner == r.Directory {
			root = project
		}
	}
	for i := range r.Projects {
		project := &r.Projects[i]
		if project.Manager != "cargo" || project.Owner != r.Directory ||
			project.Root == r.Directory {
			continue
		}
		switch {
		case root == nil || !root.Supported:
			project.Reason = "The Cargo workspace root needs review first: " + r.Directory
		case !r.cargo[project.Root]:
			project.Owner = project.Root
			project.Reason = "Not a member of the Cargo workspace at " + r.Directory +
				"; Cargo refuses to build it there, so it is left untouched"
		default:
			project.Supported, project.Reason = true, ""
		}
	}
}
