package operation

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"syscall"
)

// Paths are Workbench's private runtime directories and the directory that
// holds the workbench command.
type Paths struct{ Config, Data, State, Cache, Bin string }

// Scope is what an operation may change: the machine destination or one
// canonical project root.
type Scope struct {
	Kind string `json:"kind"`
	Root string `json:"root"`
}

// NativeContext is the source, answers, destination, state and cache that
// every native chezmoi call receives.
type NativeContext struct{ Source, Config, Destination, PersistentState, Cache string }

// Context is one resolved operation: its paths, scope and native selection.
// ReadOnly contexts may plan but never mutate.
type Context struct {
	Paths     Paths
	Scope     Scope
	Native    NativeContext
	Home      string
	Languages []string
	ReadOnly  bool
}

// Options are the command-line selections [Resolve] turns into a [Context].
type Options struct {
	Project                                  bool
	Path, Source, MachineConfig, Destination string
	Languages                                []string
	ReadOnly                                 bool
}

// Resolve reads paths only: it never creates runtime directories or answers.
func Resolve(options Options) (Context, error) {
	c := Context{ReadOnly: options.ReadOnly}
	home, err := os.UserHomeDir()
	if err != nil {
		return c, Fail(ExitInvalid, "input", "Cannot resolve the current user's home")
	}
	c.Home, err = ExistingDirectory(home)
	if err != nil {
		return c, err
	}
	c.Paths, err = runtimePaths(c.Home)
	if err != nil {
		return c, err
	}
	destination := options.Destination
	if destination == "" {
		destination = c.Home
	}
	c.Native.Destination, err = ExistingDirectory(destination)
	if err != nil {
		return c, err
	}
	c.Scope = Scope{Kind: "machine", Root: c.Native.Destination}
	if options.Project {
		c.Scope.Kind = "project"
		c.Scope.Root, err = ExistingDirectory(options.Path)
		if err != nil {
			return c, err
		}
	}
	for _, language := range options.Languages {
		switch language {
		case "python", "javascript", "typescript", "rust", "go":
		default:
			return c, Fail(
				ExitInvalid,
				"language",
				"Unknown language; use python, javascript, typescript, rust or go",
			)
		}
		if !slices.Contains(c.Languages, language) {
			c.Languages = append(c.Languages, language)
		}
	}
	slices.Sort(c.Languages)
	c.Native.Config = filepath.Join(c.Paths.Config, "machine.toml")
	if options.MachineConfig != "" {
		c.Native.Config, err = filepath.Abs(options.MachineConfig)
		if err != nil {
			return c, Fail(ExitInvalid, "input", "Cannot resolve machine answer file")
		}
		if err = checkPrivateFile(c.Native.Config); err != nil {
			return c, err
		}
	}
	c.Native.PersistentState = filepath.Join(c.Paths.State, "chezmoi", "chezmoi.boltdb")
	c.Native.Cache = c.Paths.Cache
	if options.Source != "" {
		c.Native.Source, err = ExistingDirectory(options.Source)
		if err != nil {
			return c, err
		}
		info, markerErr := os.Lstat(filepath.Join(c.Native.Source, ".chezmoiroot"))
		if markerErr != nil || !info.Mode().IsRegular() {
			return c, Fail(ExitInvalid, "source", "Source must contain a regular .chezmoiroot file")
		}
		if Within(c.Native.Source, c.Native.Destination) ||
			c.Native.Source == c.Native.Destination {
			return c, Fail(
				ExitInvalid,
				"scope",
				"Destination must not alias or lie inside the source",
			)
		}
		for _, path := range []string{c.Paths.Config, c.Paths.Data, c.Paths.State, c.Paths.Cache, c.Paths.Bin} {
			if Within(c.Native.Source, path) || Within(path, c.Native.Source) {
				return c, Fail(
					ExitInvalid,
					"scope",
					"Source and Workbench runtime directories must not overlap",
				)
			}
		}
	}
	return c, nil
}

// ExistingDirectory returns the canonical absolute path of an existing
// directory; an empty path means the working directory.
func ExistingDirectory(path string) (string, error) {
	if path == "" {
		path = "."
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", Fail(ExitInvalid, "path", "Cannot resolve the selected directory")
	}
	canonical, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", Fail(ExitInvalid, "path", "Selected directory must exist and be accessible")
	}
	info, err := os.Stat(canonical)
	if err != nil || !info.IsDir() {
		return "", Fail(ExitInvalid, "path", "Selected path must be an existing directory")
	}
	return canonical, nil
}

// Within includes equality; both paths must be canonical absolute paths.
func Within(root, path string) bool {
	relative, err := filepath.Rel(root, path)
	return err == nil && relative != ".." &&
		!strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func runtimePaths(home string) (Paths, error) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		return Paths{}, Fail(
			ExitBlocked,
			"platform",
			"Workbench runtime paths require macOS or Linux/WSL",
		)
	}
	defaults := []string{
		filepath.Join(home, ".config", "workbench"),
		filepath.Join(home, ".local", "share", "workbench"),
		filepath.Join(home, ".local", "state", "workbench"),
		filepath.Join(home, ".cache", "workbench"),
		filepath.Join(home, ".local", "bin"),
	}
	if runtime.GOOS == "darwin" {
		base := filepath.Join(home, "Library", "Application Support", "workbench")
		defaults[0], defaults[1], defaults[2], defaults[3] = filepath.Join(
			base,
			"config",
		), filepath.Join(
			base,
			"data",
		), filepath.Join(
			base,
			"state",
		), filepath.Join(
			home,
			"Library",
			"Caches",
			"workbench",
		)
	}
	names := []string{"CONFIG", "DATA", "STATE", "CACHE", "BIN"}
	for i, name := range names {
		value := os.Getenv("WORKBENCH_" + name + "_DIR")
		if value == "" && i < 4 {
			if base := os.Getenv("XDG_" + name + "_HOME"); filepath.IsAbs(base) {
				value = filepath.Join(base, "workbench")
			}
		}
		if value == "" {
			value = defaults[i]
		}
		if !filepath.IsAbs(value) {
			return Paths{}, Fail(
				ExitInvalid,
				"runtime_path",
				"WORKBENCH directory overrides must be absolute",
			)
		}
		value = filepath.Clean(value)
		if value == "/" || value == home {
			return Paths{}, Fail(
				ExitInvalid,
				"runtime_path",
				"A Workbench directory must not be the filesystem root or home itself",
			)
		}
		if err := safeParents(value); err != nil {
			return Paths{}, err
		}
		if i < 4 {
			if err := privateFilesystem(value); err != nil {
				return Paths{}, err
			}
		}
		if info, err := os.Lstat(value); err == nil {
			if !info.IsDir() {
				return Paths{}, Fail(
					ExitInvalid,
					"runtime_path",
					"Workbench runtime path is not a directory",
				)
			}
			if err := owned(info); err != nil {
				return Paths{}, err
			}
			if i < 4 && info.Mode().Perm()&0o077 != 0 {
				return Paths{}, Fail(
					ExitInvalid,
					"permissions",
					"Existing Workbench runtime directories must be private (0700)",
				)
			}
		} else if !os.IsNotExist(err) {
			return Paths{}, Fail(
				ExitInvalid,
				"runtime_path",
				"Cannot inspect Workbench runtime directory",
			)
		}
		defaults[i] = value
	}
	return Paths{defaults[0], defaults[1], defaults[2], defaults[3], defaults[4]}, nil
}

// Reject symlink parents rather than letting an override redirect private writes.
func safeParents(path string) error {
	for current := filepath.Clean(path); ; current = filepath.Dir(current) {
		info, err := os.Lstat(current)
		if err == nil {
			if info.Mode()&os.ModeSymlink != 0 {
				return Fail(
					ExitInvalid,
					"path",
					"Workbench private paths must not traverse symlinks",
				)
			}
			if current != path && !info.IsDir() {
				return Fail(ExitInvalid, "path", "Workbench path parent is not a directory")
			}
			stat, ok := info.Sys().(*syscall.Stat_t)
			if !ok || (stat.Uid != 0 && int(stat.Uid) != os.Geteuid()) ||
				(info.Mode().Perm()&0o022 != 0 && info.Mode()&os.ModeSticky == 0) {
				return Fail(
					ExitInvalid,
					"permissions",
					"Workbench path has an unsafe owner or writable parent",
				)
			}
		} else if !os.IsNotExist(err) {
			return Fail(ExitInvalid, "path", "Cannot inspect Workbench path parents")
		}
		if current == filepath.Dir(current) {
			return nil
		}
	}
}

func owned(info os.FileInfo) error {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || int(stat.Uid) != os.Geteuid() {
		return Fail(
			ExitInvalid,
			"permissions",
			"Private Workbench state must belong to the current user",
		)
	}
	return nil
}

func checkPrivateFile(path string) error {
	if err := safeParents(path); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		return Fail(ExitInvalid, "state", "Expected an existing regular private file")
	}
	if err := owned(info); err != nil {
		return err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || info.Mode().Perm()&0o077 != 0 || stat.Nlink != 1 {
		return Fail(
			ExitInvalid,
			"permissions",
			"Private files require mode 0600 and exactly one link",
		)
	}
	return nil
}

// Args is the sole native context prefix. Callers must validate tool/answers and
// audit native commands and template execution before invoking the runner.
func (n NativeContext) Args() ([]string, error) {
	for _, path := range []string{n.Source, n.Config, n.Destination, n.PersistentState, n.Cache} {
		if !filepath.IsAbs(path) {
			return nil, Fail(
				ExitBlocked,
				"native_context",
				"Resolve every native context path before running chezmoi",
			)
		}
	}
	return []string{
		"--source",
		n.Source,
		"--config",
		n.Config,
		"--destination",
		n.Destination,
		"--persistent-state",
		n.PersistentState,
		"--cache",
		n.Cache,
		"--no-pager",
		"--use-builtin-diff",
		"--refresh-externals=never",
	}, nil
}

// projectMarkers are the entries that make a directory a project. Machine
// configuration never owns or lies inside one, and management tools are never
// resolved from one.
var projectMarkers = []string{".git", "pyproject.toml", "package.json", "Cargo.toml", "go.mod"}

// ValidateTarget reports whether path may be written in this scope: strictly
// inside it, never a project manifest or inside a project for machine scope,
// and never overlapping the source or Workbench's own state.
func (c Context) ValidateTarget(path string) error {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path || !Within(c.Scope.Root, path) ||
		path == c.Scope.Root {
		return Fail(ExitInvalid, "scope", "Target must lie strictly inside the selected scope")
	}
	if c.Scope.Kind == "machine" {
		// Ruff's native user configuration is named pyproject.toml despite not
		// representing a project; this exact canonical machine owner is allowed.
		ruffConfig := filepath.Join(c.Scope.Root, ".config", "ruff", "pyproject.toml")
		if slices.Contains(projectMarkers, filepath.Base(path)) && path != ruffConfig {
			return Fail(ExitInvalid, "scope", "Machine configuration cannot own project manifests")
		}
		inside := func(directory string) bool {
			return directory != c.Scope.Root && Within(c.Scope.Root, directory)
		}
		for parent := filepath.Dir(path); inside(parent); parent = filepath.Dir(parent) {
			for _, marker := range projectMarkers {
				if filepath.Join(parent, marker) == ruffConfig {
					continue
				}
				if _, err := os.Lstat(filepath.Join(parent, marker)); err == nil {
					return Fail(
						ExitInvalid,
						"scope",
						"Machine configuration cannot write inside a project",
					)
				} else if !os.IsNotExist(err) {
					return Fail(ExitInvalid, "scope", "Cannot establish machine target ownership")
				}
			}
		}
	}
	for _, excluded := range []string{c.Native.Source, c.Paths.Config, c.Paths.Data, c.Paths.State, c.Paths.Cache} {
		if excluded != "" && (Within(excluded, path) || Within(path, excluded)) {
			return Fail(ExitInvalid, "scope", "Target overlaps source or Workbench runtime state")
		}
	}
	entrypoint := filepath.Join(c.Paths.Bin, "workbench")
	if Within(entrypoint, path) {
		return Fail(ExitInvalid, "scope", "Target is the protected Workbench entry point")
	}
	if entryInfo, entryErr := os.Stat(entrypoint); entryErr == nil {
		if Within(path, entrypoint) {
			return Fail(ExitInvalid, "scope", "Target contains the protected Workbench entry point")
		}
		if targetInfo, targetErr := os.Stat(
			path,
		); targetErr == nil &&
			os.SameFile(entryInfo, targetInfo) {
			return Fail(ExitInvalid, "scope", "Target aliases the protected Workbench entry point")
		}
	} else if !os.IsNotExist(entryErr) {
		return Fail(ExitInvalid, "scope", "Cannot inspect protected Workbench entry point")
	}
	// Leaf symlinks are images for the checkpoint owner to qualify. Ancestor
	// symlinks still cannot redirect a managed target outside its scope.
	if err := safeParents(filepath.Dir(path)); err != nil {
		return err
	}
	return nil
}

// ValidateContainer permits inspection of an existing unchanged directory above
// protected paths. This grants no chmod, replacement, removal or recursive-write
// authority; planners must reject any proposed edit to such a container.
func (c Context) ValidateContainer(path string) error {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path || !Within(c.Scope.Root, path) ||
		path == c.Scope.Root {
		return Fail(ExitInvalid, "scope", "Container must lie strictly inside the selected scope")
	}
	protected := []string{
		c.Native.Source,
		c.Paths.Config,
		c.Paths.Data,
		c.Paths.State,
		c.Paths.Cache,
		filepath.Join(c.Paths.Bin, "workbench"),
	}
	for _, excluded := range protected {
		if excluded != "" && Within(excluded, path) {
			return Fail(ExitInvalid, "scope", "Container is protected Workbench state")
		}
	}
	if err := safeParents(path); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil || !info.IsDir() {
		return Fail(
			ExitInvalid,
			"scope",
			"Protected ancestors must be existing unchanged directories",
		)
	}
	return nil
}

// Validate checks that s is a known kind rooted at an existing canonical
// directory.
func (s Scope) Validate() error {
	if (s.Kind != "machine" && s.Kind != "project") || !filepath.IsAbs(s.Root) ||
		filepath.Clean(s.Root) != s.Root {
		return Fail(ExitInvalid, "scope", "Invalid scope; resolve an absolute existing directory")
	}
	canonical, err := ExistingDirectory(s.Root)
	if err != nil || canonical != s.Root {
		return Fail(ExitInvalid, "scope", "Scope directory changed or is not canonical")
	}
	return nil
}
