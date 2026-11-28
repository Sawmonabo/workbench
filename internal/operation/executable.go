package operation

import (
	"errors"
	"fmt"
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
		"No "+shellWord(name)+" on PATH outside project folders"+
			" is a program that other users cannot change",
	)
}

func trustedExecutable(path string, excluded []string) (string, error) {
	if !filepath.IsAbs(path) {
		return "", Fail(ExitInvalid, "tool", "Subprocess executable must be absolute")
	}
	canonical, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", Fail(
			ExitBlocked,
			"dependency",
			"Cannot find the program "+FileError(path, err).Error(),
		)
	}
	for _, candidate := range []string{path, canonical} {
		if err := outsideProjects(filepath.Dir(candidate), excluded); err != nil {
			return "", err
		}
	}
	info, err := os.Stat(canonical)
	if err != nil {
		return "", Fail(
			ExitBlocked,
			"tool",
			"Cannot read the program "+FileError(canonical, err).Error(),
		)
	}
	if err := programRefusal(canonical, info); err != nil {
		return "", err
	}
	return canonical, nil
}

// programRefusal is why Workbench does not run the program at path, which
// info describes, or nil when it does: it runs only a file marked
// executable that no user but its owner can change.
func programRefusal(path string, info os.FileInfo) error {
	mode := info.Mode().Perm()
	sudo := []string{}
	if !ownedByUser(info) {
		sudo = []string{"sudo"}
	}
	switch {
	case !info.Mode().IsRegular():
		return Fail(ExitBlocked, "tool",
			display.Path(path)+" is "+fileKind(info)+", not a program Workbench can run")
	case mode&0o111 == 0:
		return Fail(
			ExitBlocked,
			"tool",
			fmt.Sprintf(
				"%s is not marked executable (mode %04o)",
				display.Path(path),
				uint32(mode),
			),
			FixPath(path, append(sudo, "chmod", "+x")...),
		)
	case mode&0o022 != 0:
		return Fail(
			ExitBlocked,
			"tool",
			fmt.Sprintf(
				"%s can be changed by other users (mode %04o)",
				display.Path(path),
				uint32(mode),
			),
			Cause("Workbench runs only a program no other user can change"),
			FixPath(path, append(sudo, "chmod", "go-w")...),
		)
	}
	return nil
}

func outsideProjects(directory string, excluded []string) error {
	for _, root := range excluded {
		if root != "" && Within(root, directory) {
			return Fail(
				ExitBlocked,
				"tool",
				display.Path(directory)+" is in the project "+display.Path(root)+
					", and Workbench does not run a project's own programs",
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
					display.Path(directory)+" is in a project, since "+display.Path(parent)+
						" holds "+marker+", and Workbench does not run a project's own programs",
				)
			} else if !errors.Is(err, fs.ErrNotExist) {
				return Fail(
					ExitBlocked,
					"tool",
					"Cannot read "+FileError(filepath.Join(parent, marker), err).Error()+
						", so Workbench cannot tell whether "+display.Path(directory)+" is in a project",
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
