package operation

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"syscall"
)

type Paths struct{ Config, Data, State, Cache, Bin string }
type Scope struct {
	Kind string `json:"kind"`
	Root string `json:"root"`
}
type NativeContext struct{ Source, Config, Destination, PersistentState, Cache string }
type Context struct {
	Paths     Paths
	Scope     Scope
	Native    NativeContext
	Home      string
	Languages []string
	ReadOnly  bool
}
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
		return c, Fail(2, "input", "Cannot resolve the current user's home")
	}
	c.Home, err = ExistingDirectory(home)
	if err != nil {
		return c, err
	}
	c.Paths, err = RuntimePaths(c.Home)
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
			return c, Fail(2, "language", "Unknown language; use python, javascript, typescript, rust or go")
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
			return c, Fail(2, "input", "Cannot resolve machine answer file")
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
			return c, Fail(2, "source", "Source must contain a regular .chezmoiroot file")
		}
		if Within(c.Native.Source, c.Native.Destination) || c.Native.Source == c.Native.Destination {
			return c, Fail(2, "scope", "Destination must not alias or lie inside the source")
		}
		for _, path := range []string{c.Paths.Config, c.Paths.Data, c.Paths.State, c.Paths.Cache, c.Paths.Bin} {
			if Within(c.Native.Source, path) || Within(path, c.Native.Source) {
				return c, Fail(2, "scope", "Source and Workbench runtime directories must not overlap")
			}
		}
	}
	return c, nil
}

func ExistingDirectory(path string) (string, error) {
	if path == "" {
		path = "."
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", Fail(2, "path", "Cannot resolve the selected directory")
	}
	canonical, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", Fail(2, "path", "Selected directory must exist and be accessible")
	}
	info, err := os.Stat(canonical)
	if err != nil || !info.IsDir() {
		return "", Fail(2, "path", "Selected path must be an existing directory")
	}
	return canonical, nil
}

// Within includes equality; both paths must be canonical absolute paths.
func Within(root, path string) bool {
	relative, err := filepath.Rel(root, path)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func RuntimePaths(home string) (Paths, error) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		return Paths{}, Fail(3, "platform", "Workbench runtime paths require macOS or Linux/WSL")
	}
	defaults := []string{filepath.Join(home, ".config", "workbench"), filepath.Join(home, ".local", "share", "workbench"), filepath.Join(home, ".local", "state", "workbench"), filepath.Join(home, ".cache", "workbench"), filepath.Join(home, ".local", "bin")}
	if runtime.GOOS == "darwin" {
		base := filepath.Join(home, "Library", "Application Support", "workbench")
		defaults[0], defaults[1], defaults[2], defaults[3] = filepath.Join(base, "config"), filepath.Join(base, "data"), filepath.Join(base, "state"), filepath.Join(home, "Library", "Caches", "workbench")
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
			return Paths{}, Fail(2, "runtime_path", "WORKBENCH directory overrides must be absolute")
		}
		value = filepath.Clean(value)
		if value == "/" || value == home {
			return Paths{}, Fail(2, "runtime_path", "A Workbench directory must not be the filesystem root or home itself")
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
				return Paths{}, Fail(2, "runtime_path", "Workbench runtime path is not a directory")
			}
			if err := owned(info); err != nil {
				return Paths{}, err
			}
			if i < 4 && info.Mode().Perm()&0077 != 0 {
				return Paths{}, Fail(2, "permissions", "Existing Workbench runtime directories must be private (0700)")
			}
		} else if !os.IsNotExist(err) {
			return Paths{}, Fail(2, "runtime_path", "Cannot inspect Workbench runtime directory")
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
				return Fail(2, "path", "Workbench private paths must not traverse symlinks")
			}
			if current != path && !info.IsDir() {
				return Fail(2, "path", "Workbench path parent is not a directory")
			}
			stat, ok := info.Sys().(*syscall.Stat_t)
			if !ok || (stat.Uid != 0 && int(stat.Uid) != os.Geteuid()) || (info.Mode().Perm()&0022 != 0 && info.Mode()&os.ModeSticky == 0) {
				return Fail(2, "permissions", "Workbench path has an unsafe owner or writable parent")
			}
		} else if !os.IsNotExist(err) {
			return Fail(2, "path", "Cannot inspect Workbench path parents")
		}
		if current == filepath.Dir(current) {
			return nil
		}
	}
}

func owned(info os.FileInfo) error {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || int(stat.Uid) != os.Geteuid() {
		return Fail(2, "permissions", "Private Workbench state must belong to the current user")
	}
	return nil
}

func checkPrivateFile(path string) error {
	if err := safeParents(path); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		return Fail(2, "state", "Expected an existing regular private file")
	}
	if err := owned(info); err != nil {
		return err
	}
	stat := info.Sys().(*syscall.Stat_t)
	if info.Mode().Perm()&0077 != 0 || stat.Nlink != 1 {
		return Fail(2, "permissions", "Private files require mode 0600 and exactly one link")
	}
	return nil
}

// Args is the sole native context prefix. Callers must validate tool/answers and
// audit native commands and template execution before invoking the runner.
func (n NativeContext) Args() ([]string, error) {
	for _, path := range []string{n.Source, n.Config, n.Destination, n.PersistentState, n.Cache} {
		if !filepath.IsAbs(path) {
			return nil, Fail(3, "native_context", "Resolve every native context path before running chezmoi")
		}
	}
	return []string{"--source", n.Source, "--config", n.Config, "--destination", n.Destination, "--persistent-state", n.PersistentState, "--cache", n.Cache, "--no-pager", "--use-builtin-diff", "--refresh-externals=never"}, nil
}

func (c Context) ValidateTarget(path string) error {
	if !filepath.IsAbs(path) || !Within(c.Scope.Root, path) || path == c.Scope.Root {
		return Fail(2, "scope", "Target must lie strictly inside the selected scope")
	}
	for _, excluded := range []string{c.Native.Source, c.Paths.Config, c.Paths.Data, c.Paths.State, c.Paths.Cache, c.Paths.Bin} {
		if excluded != "" && (Within(excluded, path) || Within(path, excluded)) {
			return Fail(2, "scope", "Target overlaps source or Workbench runtime state")
		}
	}
	if err := safeParents(path); err != nil {
		return err
	}
	return nil
}

func (s Scope) Validate() error {
	if (s.Kind != "machine" && s.Kind != "project") || !filepath.IsAbs(s.Root) || filepath.Clean(s.Root) != s.Root {
		return Fail(2, "scope", "Invalid scope; resolve an absolute existing directory")
	}
	canonical, err := ExistingDirectory(s.Root)
	if err != nil || canonical != s.Root {
		return Fail(2, "scope", "Scope directory changed or is not canonical")
	}
	return nil
}
