// Package project inspects existing projects without evaluating their code.
package project

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

const (
	maxEntries = 50000
	maxDepth   = 32
	maxItems   = 1000
)

// Item is a filename-based candidate, not a validated project or policy owner.
type Item struct {
	Path      string `json:"path"`
	Kind      string `json:"kind"`
	Ecosystem string `json:"ecosystem"`
}

type Inventory struct {
	Directory string    `json:"directory"`
	Items     []Item    `json:"items"`
	Entries   int       `json:"entries"`
	Excluded  int       `json:"excluded"`
	Skipped   int       `json:"skipped"`
	Projects  []Project `json:"projects"`
	Warnings  []string  `json:"warnings,omitempty"`
	inputs    map[string][]byte
}

// Inspect reads bounded native metadata without commands or directory creation.
// The returned inventory can be partial when err is non-nil.
func Inspect(ctx context.Context, path string) (*Inventory, error) {
	directory, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolve directory: %w", err)
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return nil, fmt.Errorf("open existing directory: %w", err)
	}
	defer func() { _ = root.Close() }() // Read-only descriptor; no buffered writes to lose.
	result := &Inventory{Directory: directory}
	err = result.walk(ctx, root, ".", 0)
	slices.SortFunc(result.Items, func(a, b Item) int { return strings.Compare(a.Path, b.Path) })
	if err == nil {
		err = result.resolve(ctx)
	}
	return result, err
}

func (r *Inventory) walk(ctx context.Context, root *os.Root, path string, depth int) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if depth > maxDepth {
		return fmt.Errorf("directory depth exceeds %d; choose a narrower scope", maxDepth)
	}
	if path != "." {
		if _, err := root.Lstat(".git"); err == nil {
			r.Excluded++
			return r.add(
				Item{
					Path:      filepath.ToSlash(path),
					Kind:      "nested repository (not scanned)",
					Ecosystem: "unknown",
				},
			)
		} else if !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("inspect repository boundary %q: %w", path, err)
		}
	}
	dir, err := root.Open(".")
	if err != nil {
		return fmt.Errorf("open %q: %w", path, err)
	}
	defer func() { _ = dir.Close() }()
	for {
		entries, readErr := dir.ReadDir(128)
		for _, entry := range entries {
			if err := ctx.Err(); err != nil {
				return err
			}
			r.Entries++
			if r.Entries > maxEntries {
				return fmt.Errorf("entry limit of %d exceeded; choose a narrower scope", maxEntries)
			}
			name := entry.Name()
			relative := filepath.Join(path, name)
			if entry.Type()&os.ModeSymlink != 0 {
				r.Skipped++
				continue
			}
			if entry.IsDir() {
				if excludedDirectory(name) {
					r.Excluded++
					continue
				}
				child, err := root.OpenRoot(name)
				if err != nil {
					return fmt.Errorf("open directory %q: %w", relative, err)
				}
				err = r.walk(ctx, child, relative, depth+1)
				closeErr := child.Close()
				if err != nil {
					return err
				}
				if closeErr != nil {
					return closeErr
				}
				continue
			}
			if !entry.Type().IsRegular() {
				r.Skipped++
				continue
			}
			if item, ok := candidate(name); ok {
				item.Path = filepath.ToSlash(relative)
				if err := r.add(item); err != nil {
					return err
				}
			}
		}
		if errors.Is(readErr, io.EOF) {
			return nil
		}
		if readErr != nil {
			return fmt.Errorf("read directory %q: %w", path, readErr)
		}
	}
}

func (r *Inventory) add(item Item) error {
	if len(r.Items) >= maxItems {
		return fmt.Errorf("candidate limit of %d exceeded; choose a narrower scope", maxItems)
	}
	r.Items = append(r.Items, item)
	return nil
}

func excludedDirectory(name string) bool {
	// Hidden directories include Git metadata, private agent state and environments.
	if strings.HasPrefix(name, ".") {
		return true
	}
	switch name {
	case "node_modules",
		"vendor",
		"target",
		"dist",
		"build",
		"venv",
		"__pycache__",
		"coverage",
		"htmlcov":
		return true
	}
	return false
}

func candidate(name string) (Item, bool) {
	item := Item{}
	switch name {
	case "pyproject.toml":
		item.Kind, item.Ecosystem = "manifest", "python"
	case "package.json":
		item.Kind, item.Ecosystem = "manifest", "javascript/typescript"
	case "Cargo.toml":
		item.Kind, item.Ecosystem = "manifest", "rust"
	case "go.mod":
		item.Kind, item.Ecosystem = "manifest", "go"
	case "pnpm-workspace.yaml":
		item.Kind, item.Ecosystem = "workspace", "javascript/typescript"
	case "go.work":
		item.Kind, item.Ecosystem = "workspace", "go"
	case "uv.lock", "poetry.lock", "pdm.lock", "Pipfile.lock":
		item.Kind, item.Ecosystem = "lockfile", "python"
	case "package-lock.json", "pnpm-lock.yaml", "yarn.lock", "bun.lock", "bun.lockb":
		item.Kind, item.Ecosystem = "lockfile", "javascript/typescript"
	case "Cargo.lock":
		item.Kind, item.Ecosystem = "lockfile", "rust"
	case "go.sum", "go.work.sum":
		item.Kind, item.Ecosystem = "checksums", "go"
	case "ruff.toml",
		".ruff.toml",
		"ty.toml",
		"pyrightconfig.json",
		"uv.toml",
		"requirements.txt",
		"Pipfile":
		item.Kind, item.Ecosystem = "configuration", "python"
	default:
		return Item{}, false
	}
	return item, true
}
