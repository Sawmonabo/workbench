package project

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/Sawmonabo/workbench/internal/operation"
	"github.com/pelletier/go-toml/v2"
)

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

func hash(data []byte) string { h := sha256.Sum256(data); return hex.EncodeToString(h[:]) }

// readMetadata rejects symlinks and bounds every read. Ancestors are read only
// to discover ownership; they never expand the selected mutation scope.
func readMetadata(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > 8<<20 {
		return nil, operation.Fail(3, "manifest", "Metadata must be a bounded regular file: "+filepath.Base(path))
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
		return nil, operation.Fail(3, "manifest", "Metadata exceeds the input bound")
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
			return nil, operation.Fail(3, "manifest", "Combined metadata exceeds 16 MiB; select a narrower scope")
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
		return nil, operation.Fail(2, "toml", "Invalid TOML metadata; correct it before planning")
	}
	return doc, nil
}

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
		path := filepath.Join(r.Directory, filepath.FromSlash(item.Path))
		data, err := r.read(path)
		if err != nil {
			return err
		}
		if item.Kind != "manifest" {
			continue
		}
		project := Project{Root: filepath.Dir(path), Language: item.Ecosystem, Owner: filepath.Dir(path), Reason: "Recognized; configuration is not supported for this language"}
		switch filepath.Base(path) {
		case "pyproject.toml":
			project.metadata, err = decodeTOML(data)
			if err != nil {
				return err
			}
			project.Language = "python"
			project.Manager = "unknown"
			project.Reason = "Existing uv.lock or explicit uv workspace ownership required; no project initialization"
			if err = r.pythonOwner(&project); err != nil {
				return err
			}
		case "package.json":
			var doc map[string]any
			if json.Unmarshal(data, &doc) != nil {
				return operation.Fail(2, "manifest", "Invalid package.json")
			}
			project.Manager = "javascript package manager"
			if s, ok := doc["packageManager"].(string); ok {
				project.Manager = s
			}
		case "Cargo.toml":
			project.Manager = "cargo"
			if _, err = decodeTOML(data); err != nil {
				return err
			}
		case "go.mod":
			project.Manager = "go"
		}
		r.Projects = append(r.Projects, project)
	}
	// Also recognize the enclosing Python manifest when selection is a package
	// subdirectory. It is reported but cannot authorize writes above selection.
	if len(r.Projects) == 0 {
		for parent := filepath.Dir(r.Directory); ; parent = filepath.Dir(parent) {
			data, err := r.read(filepath.Join(parent, "pyproject.toml"))
			if err != nil {
				return err
			}
			if data != nil {
				doc, e := decodeTOML(data)
				if e != nil {
					return e
				}
				p := Project{Root: parent, Owner: parent, Language: "python", Manager: "unknown", metadata: doc}
				if e = r.pythonOwner(&p); e != nil {
					return e
				}
				p.Supported = false
				p.Reason = "Select enclosing project root explicitly: " + p.Owner
				r.Projects = append(r.Projects, p)
				break
			}
			if parent == filepath.Dir(parent) {
				break
			}
		}
	}
	return nil
}

func (r *Inventory) pythonOwner(p *Project) error {
	if nested(p.metadata, "tool", "poetry") != nil || nested(p.metadata, "tool", "pdm") != nil {
		p.Manager = "poetry/pdm"
		p.Reason = "Poetry/PDM configuration is discovery-only"
		return nil
	}
	owner := p.Root
	for parent := p.Root; ; parent = filepath.Dir(parent) {
		data, err := r.read(filepath.Join(parent, "pyproject.toml"))
		if err != nil {
			return err
		}
		if data != nil {
			doc, err := decodeTOML(data)
			if err != nil {
				return err
			}
			if parent != p.Root {
				for _, tool := range []string{"ruff", "ty", "basedpyright"} {
					if nested(p.metadata, "tool", tool) == nil && nested(doc, "tool", tool) != nil {
						p.Manager = "uv"
						p.Owner = parent
						p.Reason = "Inherited tool policy requires explicit ownership review: " + filepath.Join(parent, "pyproject.toml")
						return nil
					}
				}
			}
			workspace := nested(doc, "tool", "uv", "workspace")
			if workspace != nil {
				p.Manager = "uv"
				member := parent == p.Root
				relative, _ := filepath.Rel(parent, p.Root)
				relative = filepath.ToSlash(relative)
				for _, pattern := range stringsOf(workspace["members"]) {
					if strings.Contains(pattern, "**") || strings.HasPrefix(pattern, "/") || strings.Contains(pattern, "..") {
						p.Reason = "Workspace pattern needs explicit ownership review"
						return nil
					}
					matched, e := filepath.Match(pattern, relative)
					if e != nil {
						return operation.Fail(2, "workspace", "Invalid uv workspace member pattern")
					}
					member = member || matched
				}
				for _, pattern := range stringsOf(workspace["exclude"]) {
					if strings.Contains(pattern, "**") || strings.HasPrefix(pattern, "/") || strings.Contains(pattern, "..") {
						p.Reason = "Workspace exclusion pattern needs explicit ownership review"
						return nil
					}
					matched, e := filepath.Match(pattern, relative)
					if e != nil {
						return e
					}
					if matched {
						member = false
					}
				}
				if parent != p.Root && !member {
					break
				}
				if member {
					owner = parent
					break
				}
			}
		}
		if _, err := os.Lstat(filepath.Join(parent, ".git")); err == nil {
			break
		} else if !os.IsNotExist(err) {
			return err
		}
		if parent == filepath.Dir(parent) {
			break
		}
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
		p.Reason = "Ruff extend configuration requires explicit inherited-policy ownership review: " + filepath.Join(p.Root, "pyproject.toml")
		return nil
	}
	p.Supported = true
	p.Reason = ""
	if !operation.Within(r.Directory, owner) {
		p.Supported = false
		p.Reason = "Select shared workspace root explicitly: " + owner
	}
	// Native tools may inherit configuration from any ancestor. Record all
	// candidates and refuse competing standalone ownership during planning.
	for parent := p.Root; ; parent = filepath.Dir(parent) {
		for _, name := range []string{"ruff.toml", ".ruff.toml", "ty.toml", "pyrightconfig.json", "uv.toml"} {
			data, err := r.read(filepath.Join(parent, name))
			if err != nil {
				return err
			}
			if data != nil {
				p.Supported = false
				p.Reason = "Standalone configuration requires reviewed integration: " + filepath.Join(parent, name)
			}
		}
		if _, err := os.Lstat(filepath.Join(parent, ".git")); err == nil {
			break
		} else if !os.IsNotExist(err) {
			return err
		}
		if parent == filepath.Dir(parent) {
			break
		}
	}
	return nil
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
			digest = hash(value)
		}
		result = append(result, operation.Input{Name: name, Digest: digest})
	}
	return result
}

func unsupported(p Project) string { return fmt.Sprintf("%s: %s", p.Root, p.Reason) }
