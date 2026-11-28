package operation

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"

	"github.com/Sawmonabo/workbench/internal/display"
	"github.com/Sawmonabo/workbench/internal/platform"
)

// FindExecutable ignores empty/relative PATH entries and project-local shims.
// This discovers a location only; callers must verify version/capabilities.
func FindExecutable(name, searchPath string, excluded []string) (string, error) {
	if filepath.Base(name) != name || name == "." || name == "" {
		return "", Fail(ExitInvalid, "tool", "Tool discovery requires a command basename")
	}
	for _, directory := range filepath.SplitList(searchPath) {
		if !filepath.IsAbs(directory) {
			continue
		}
		path, err := trustedExecutable(filepath.Join(directory, name), excluded)
		if err == nil {
			return path, nil
		}
	}
	return "", Fail(
		ExitBlocked,
		"dependency",
		"Required executable was not found outside project-controlled PATH entries",
	)
}

func trustedExecutable(path string, excluded []string) (string, error) {
	if !filepath.IsAbs(path) {
		return "", Fail(ExitInvalid, "tool", "Subprocess executable must be absolute")
	}
	canonical, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", Fail(ExitBlocked, "dependency", "Selected executable is unavailable")
	}
	for _, candidate := range []string{path, canonical} {
		if err := outsideProjects(filepath.Dir(candidate), excluded); err != nil {
			return "", err
		}
	}
	info, err := os.Stat(canonical)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 ||
		info.Mode().Perm()&0o022 != 0 {
		return "", Fail(
			ExitBlocked,
			"tool",
			"Selected executable must be a regular executable without group/other write access",
		)
	}
	return canonical, nil
}

func outsideProjects(directory string, excluded []string) error {
	for _, root := range excluded {
		if root != "" && Within(root, directory) {
			return Fail(
				ExitBlocked,
				"tool",
				"Project-controlled management executable lookup is forbidden",
			)
		}
	}
	homes := userHomes()
	for parent := directory; ; parent = filepath.Dir(parent) {
		// A marker in the home directory itself (a stray ~/package.json or a
		// tracked home) does not make Workbench's own directories and the
		// user's home-level tool directories project-controlled.
		homeLevel := slices.Contains(homes, parent) && homeToolDirectory(directory, homes)
		for _, marker := range projectMarkers {
			if homeLevel {
				break
			}
			if _, err := os.Lstat(filepath.Join(parent, marker)); err == nil {
				if marker == ".git" && homebrewRepository(parent, directory) {
					continue
				}
				return Fail(
					ExitBlocked,
					"tool",
					"Project-controlled management executable lookup is forbidden because "+
						display.Path(parent)+" contains "+marker,
				)
			} else if !errors.Is(err, fs.ErrNotExist) {
				return Fail(
					ExitBlocked,
					"tool",
					"Cannot establish ownership of a management executable directory",
				)
			}
		}
		if parent == filepath.Dir(parent) {
			return nil
		}
	}
}

// userHomes returns the home directory as configured and with symlinks resolved.
func userHomes() []string {
	home, err := os.UserHomeDir()
	if err != nil || !filepath.IsAbs(home) {
		return nil
	}
	homes := []string{filepath.Clean(home)}
	if canonical, err := filepath.EvalSymlinks(home); err == nil && canonical != homes[0] {
		homes = append(homes, canonical)
	}
	return homes
}

// homeToolDirectory reports whether directory lies in Workbench's own default
// runtime locations or a conventional per-user tool directory under home.
// Anything else under home (node_modules, a project's bin, a venv) stays
// subject to the marker scan.
func homeToolDirectory(directory string, homes []string) bool {
	for _, name := range []string{"CONFIG", "DATA", "STATE", "CACHE", "BIN"} {
		// Overrides are used verbatim by runtimePaths, so honor them the same way.
		root := os.Getenv("WORKBENCH_" + name + "_DIR")
		if filepath.IsAbs(root) && slices.Contains(homes, filepath.Dir(filepath.Clean(root))) &&
			Within(filepath.Clean(root), directory) {
			return true
		}
	}
	for _, home := range homes {
		for _, root := range []string{
			filepath.Join(home, ".local"),
			filepath.Join(home, ".cache", "workbench"),
			filepath.Join(home, ".config", "workbench"),
			filepath.Join(home, ".cargo", "bin"),
			filepath.Join(home, "go", "bin"),
			filepath.Join(home, "Library", "Application Support", "workbench"),
			filepath.Join(home, "Library", "Caches", "workbench"),
		} {
			if Within(root, directory) {
				return true
			}
		}
	}
	return false
}

// Homebrew itself is a Git checkout at its standard installation locations.
// Recognize its installed-tool directories, not arbitrary repositories with a
// Homebrew-shaped name. Explicit project exclusions and nested markers still win.
func homebrewRepository(repository, directory string) bool {
	if !slices.Contains(platform.HomebrewRepositories, repository) {
		return false
	}
	installed := false
	for _, name := range []string{"bin", "sbin", "Cellar", "opt"} {
		if Within(filepath.Join(repository, name), directory) {
			installed = true
			break
		}
	}
	if !installed {
		return false
	}
	for _, name := range []string{"bin/brew", "Library/Homebrew/brew.sh"} {
		info, err := os.Lstat(filepath.Join(repository, name))
		if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o022 != 0 {
			return false
		}
		if !ownedByUserOrRoot(info) {
			return false
		}
	}
	return true
}
