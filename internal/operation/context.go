package operation

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/Sawmonabo/workbench/internal/display"
	"github.com/Sawmonabo/workbench/internal/platform"
)

// Paths are Workbench's private runtime directories and the directory that
// holds the workbench command.
type Paths struct{ Config, Data, State, Cache, Bin string }

// All are the five folders, in the order Config, Data, State, Cache, Bin.
func (p Paths) All() []string { return []string{p.Config, p.Data, p.State, p.Cache, p.Bin} }

// RuntimeVariables hand the four runtime folders to a Workbench process this
// one starts, as NAME=value entries in the order Config, Data, State, Cache:
// WORKBENCH_CONFIG_DIR, WORKBENCH_DATA_DIR, WORKBENCH_STATE_DIR and
// WORKBENCH_CACHE_DIR, which [Resolve] reads before XDG and the platform's
// defaults. The bin folder's WORKBENCH_BIN_DIR is not among them: each
// environment that carries it lists it in its own place.
func (p Paths) RuntimeVariables() []string {
	return []string{
		"WORKBENCH_CONFIG_DIR=" + p.Config,
		"WORKBENCH_DATA_DIR=" + p.Data,
		"WORKBENCH_STATE_DIR=" + p.State,
		"WORKBENCH_CACHE_DIR=" + p.Cache,
	}
}

// Entrypoint is the workbench command in the bin folder, the link that
// activating a release points at its executable.
func (p Paths) Entrypoint() string { return filepath.Join(p.Bin, "workbench") }

// Locks is the folder in the state folder that holds the operation locks
// and the migrations' lock. [Mutation.WritePrivate] refuses to replace a file
// in it: flock(2) stays on the file it opened.
func (p Paths) Locks() string { return filepath.Join(p.State, "locks") }

// MigrateLock is the lock one run of Workbench's migrations holds while it
// moves files, whether `workbench migrate` or the costs import worker runs it
// (see internal/migrate).
func (p Paths) MigrateLock() string { return filepath.Join(p.Locks(), "migrate.lock") }

// MachineConfig is machine.toml, where the machine's answers and the apply
// choices are saved.
func (p Paths) MachineConfig() string { return filepath.Join(p.Config, "machine.toml") }

// MachineConfigLimit is the most of machine.toml Workbench reads.
const MachineConfigLimit = 1 << 20

// Scope is what an operation may change: the machine destination or one
// canonical project root.
type Scope struct {
	Kind string `json:"kind"`
	Root string `json:"root"`
}

// RecordsHome reports whether this scope is the real home: the only
// destination whose applied configuration the machine state records. An
// isolated destination (apply --destination) is a disposable copy, so applying
// or reverting there leaves the machine's record alone.
func (c Context) RecordsHome() bool { return c.Scope.Root == c.Home }

// NativeContext is the source, answers, destination, state and cache that
// every native chezmoi call receives. Developer marks a source selected with
// --local-build, which may be a checkout bound by its content instead of a release.
type NativeContext struct {
	Source, Config, Destination, PersistentState, Cache string
	Developer                                           bool
}

// Context is one resolved operation: its paths, scope and native selection.
// ReadOnly contexts may plan but never mutate.
type Context struct {
	Paths     Paths
	Scope     Scope
	Native    NativeContext
	Home      string
	Languages []string
	ReadOnly  bool
	// Progress shows what a long operation is doing; nil shows nothing.
	Progress Progress
}

// Options are the command-line selections [Resolve] turns into a [Context].
type Options struct {
	Project                   bool
	Path, Source, Destination string
	Languages                 []string
	ReadOnly                  bool
}

// Languages are the values --language takes.
var Languages = []string{"python", "javascript", "typescript", "rust", "go"}

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
		if !slices.Contains(Languages, language) {
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
	c.Native.Config = c.Paths.MachineConfig()
	c.Native.PersistentState = filepath.Join(c.Paths.State, "chezmoi", "chezmoi.boltdb")
	c.Native.Cache = c.Paths.Cache
	if options.Source != "" {
		c.Native.Source, err = ExistingDirectory(options.Source)
		if err != nil {
			return c, err
		}
		c.Native.Developer = true
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
		for _, path := range c.Paths.All() {
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

// WithinResolved is [Within] for paths that may name links: both are
// compared with their links resolved, by whole path segments, so a sibling
// such as root-old is outside. A path that cannot be resolved, or does not
// resolve to an absolute one, is outside.
func WithinResolved(root, path string) bool {
	root, rootErr := filepath.EvalSymlinks(root)
	path, pathErr := filepath.EvalSymlinks(path)
	return rootErr == nil && pathErr == nil && filepath.IsAbs(root) && filepath.IsAbs(path) &&
		Within(root, path)
}

func runtimePaths(home string) (Paths, error) {
	defaults := platform.RuntimeFolders(home)
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
		if i < 4 {
			if err := safeOwnFolder("runtime_path", value); err != nil {
				return Paths{}, err
			}
			if err := privateFilesystem(value); err != nil {
				return Paths{}, err
			}
		} else if err := safeParents(value); err != nil {
			return Paths{}, err
		}
		if info, err := os.Lstat(value); err == nil {
			switch {
			case i < 4:
				err = ownFolder("runtime_path", value, info)
			case !info.IsDir():
				err = notFolderRefusal("runtime_path", value, info)
			default:
				err = owned(value, info)
			}
			if err != nil {
				return Paths{}, err
			}
		} else if !errors.Is(err, fs.ErrNotExist) {
			return Paths{}, Fail(
				ExitInvalid,
				"runtime_path",
				"Cannot inspect the Workbench runtime directory "+display.Path(value),
			)
		}
		defaults[i] = value
	}
	return Paths{defaults[0], defaults[1], defaults[2], defaults[3], defaults[4]}, nil
}

// configHome is the XDG configuration folder: $XDG_CONFIG_HOME when it is
// absolute, as for the config directory itself, otherwise ~/.config. It is empty
// when the home cannot be resolved.
func configHome() string {
	if base := os.Getenv("XDG_CONFIG_HOME"); filepath.IsAbs(base) {
		return filepath.Clean(base)
	}
	home := display.Home()
	if home == "" {
		return ""
	}
	return filepath.Join(home, ".config")
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
				} else if !errors.Is(err, fs.ErrNotExist) {
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
	entrypoint := c.Paths.Entrypoint()
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
			return c.entrypointAlias(path)
		}
	} else if !errors.Is(entryErr, fs.ErrNotExist) {
		return Fail(ExitInvalid, "scope", "Cannot inspect protected Workbench entry point")
	}
	// Leaf symlinks are images for the checkpoint owner to qualify. Ancestor
	// symlinks still cannot redirect a managed target outside its scope.
	if err := safeParents(filepath.Dir(path)); err != nil {
		return err
	}
	return nil
}

// entrypointAlias refuses path, which is Workbench's entry point under
// another name: a link to it by validateLinkTarget's rule, and a hard link
// to it as a file Workbench would replace, which moving it aside clears.
func (c Context) entrypointAlias(path string) error {
	if link, err := os.Readlink(path); err == nil {
		if !filepath.IsAbs(link) {
			link = filepath.Join(filepath.Dir(path), link)
		}
		if err = c.validateLinkTarget(path, filepath.Clean(link)); err != nil {
			return err
		}
	}
	shown := display.Path(path)
	return Fail(
		ExitInvalid,
		"scope",
		shown+" is Workbench's entry point under another name, which Workbench does not write"+
			replacedFile.retry(nil, path),
	)
}

// validateLinkTarget refuses the link at path when target, the cleaned path
// it names, or the path that resolves to when it exists, lies in Workbench's
// own files: its source, its config, data, state and cache folders, or its
// entry point, which it also refuses through a hard link. Where else a link
// points is not checked: Workbench writes a link at a target with
// symlinkat(2) and renameat(2) and removes it with unlinkat(2), none of which
// follows it, chezmoi replaces or removes a link the same way, and what reads
// through one, such as a modify_ template's input, changes nothing.
func (c Context) validateLinkTarget(path, target string) error {
	targets := []string{target}
	if resolved, err := filepath.EvalSymlinks(target); err == nil && resolved != target {
		targets = append(targets, resolved)
	}
	entrypoint := c.Paths.Entrypoint()
	protected := []string{
		c.Native.Source,
		c.Paths.Config,
		c.Paths.Data,
		c.Paths.State,
		c.Paths.Cache,
		entrypoint,
	}
	own := false
	for _, candidate := range targets {
		for _, excluded := range protected {
			own = own || excluded != "" && Within(excluded, candidate)
		}
	}
	if !own && entrypoint != "" {
		entryInfo, entryErr := os.Stat(entrypoint)
		targetInfo, targetErr := os.Stat(target)
		own = entryErr == nil && targetErr == nil && os.SameFile(entryInfo, targetInfo)
	}
	if !own {
		return nil
	}
	return Fail(
		ExitInvalid,
		"scope",
		display.Path(path)+" links to "+display.Path(target)+
			", among Workbench's own files, and Workbench does not keep a link into them"+
			replacedLink.retry(nil, path),
	)
}

// ValidateContainer permits an existing directory above protected paths, such
// as ~/.config holding Workbench's config. Checkpoints may change only such a
// directory's mode (see validateChanges); nothing may replace, remove or
// recursively write it.
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
		c.Paths.Entrypoint(),
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

// isContainer reports whether path is a directory that holds Workbench's own
// files rather than a target: ValidateTarget refuses it only because it
// overlaps Workbench's runtime state, and ValidateContainer accepts it.
func (c Context) isContainer(path string) bool {
	return c.ValidateTarget(path) != nil && c.ValidateContainer(path) == nil
}

// validateImagePath is ValidateTarget, plus an existing directory that holds
// Workbench's own files. Image writes to such a directory change only its
// mode, group and attributes: writeImage refuses to replace a directory, and
// removing a non-empty one fails.
func (c Context) validateImagePath(path string) error {
	err := c.ValidateTarget(path)
	if err != nil && c.ValidateContainer(path) == nil {
		return nil
	}
	return err
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
