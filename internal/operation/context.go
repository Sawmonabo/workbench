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

// Resolve reads paths only: it never creates runtime directories or answers,
// and changes nothing but the mode of a runtime directory its owner may not
// read or search, which gets 0700 (see [ownFolder]).
func Resolve(options Options) (Context, error) {
	c := Context{ReadOnly: options.ReadOnly}
	home, err := os.UserHomeDir()
	if err != nil {
		return c, Fail(
			ExitInvalid,
			"input",
			"Cannot find your home folder",
			Cause(err.Error()),
			FixText("set HOME to your home folder"),
		)
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
				"Unknown language "+shellWord(language),
				Cause("--language takes "+strings.Join(Languages[:len(Languages)-1], ", ")+
					" or "+Languages[len(Languages)-1]),
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
		if Within(c.Native.Source, c.Native.Destination) {
			where := " is in the source "
			if c.Native.Source == c.Native.Destination {
				where = " is the source "
			}
			return c, Fail(
				ExitInvalid,
				"scope",
				"The destination "+display.Path(c.Native.Destination)+where+
					display.Path(c.Native.Source),
				Cause("chezmoi would write what it applies into the files it reads it from"),
				FixText("pass --destination a folder outside "+shellHomePath(c.Native.Source)),
			)
		}
		for i, path := range c.Paths.All() {
			if Within(c.Native.Source, path) || Within(path, c.Native.Source) {
				name := runtimeNames[i]
				return c, Fail(
					ExitInvalid,
					"scope",
					"Workbench's "+strings.ToLower(name)+" folder "+display.Path(path)+
						overlaps(path, c.Native.Source, "the source "),
					Cause("Workbench keeps its own files apart from the source it applies"),
					FixText("set WORKBENCH_"+name+"_DIR to a folder apart from it"),
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
		return "", Fail(
			ExitInvalid,
			"path",
			"Cannot find the folder "+shellWord(path),
			Cause(err.Error()),
		)
	}
	canonical, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", Fail(
			ExitInvalid,
			"path",
			"Cannot find the folder "+display.Path(absolute),
			Cause(causeAt(absolute, err)),
		)
	}
	info, err := os.Stat(canonical)
	if err != nil {
		return "", Fail(
			ExitInvalid,
			"path",
			"Cannot find the folder "+display.Path(canonical),
			Cause(causeAt(canonical, err)),
		)
	}
	if !info.IsDir() {
		return "", Fail(ExitInvalid, "path", display.Path(canonical)+" is not a folder")
	}
	return canonical, nil
}

// causeAt is err as the cause of a refusal whose headline names path: the
// reason alone when err is of path itself, else the path it is of from home
// and its reason ([FileError]).
func causeAt(path string, err error) string {
	problem, ok := errors.AsType[*fs.PathError](err)
	switch {
	case !ok:
		return err.Error()
	case problem.Path == path:
		return problem.Err.Error()
	}
	return FileError(problem.Path, problem.Err).Error()
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

// runtimeNames name the folders of [Paths.All], in its order, as their
// WORKBENCH_NAME_DIR variables do.
var runtimeNames = []string{"CONFIG", "DATA", "STATE", "CACHE", "BIN"}

// runtimePaths are the five folders of [Paths] for home: each
// WORKBENCH_NAME_DIR, else the XDG folder for the first four, else the
// platform's default. Each must be absolute and not the root or home, and
// the four runtime folders pass the checks of a folder of Workbench's own
// files (see [ownFolder]) where they exist; the folder Workbench makes to
// hold the defaults (see [platform.RuntimeBase]) is one too, judged first
// when a runtime folder in it is used.
func runtimePaths(home string) (Paths, error) {
	paths := platform.RuntimeFolders(home)
	for i, name := range runtimeNames {
		value, err := runtimePath(home, name, paths[i], i < 4)
		if err != nil {
			return Paths{}, err
		}
		paths[i] = value
	}
	if base := platform.RuntimeBase(home); base != "" &&
		slices.ContainsFunc(
			paths[:4],
			func(path string) bool { return filepath.Dir(path) == base },
		) {
		if err := privateDirectory(base, false); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return Paths{}, err
		}
	}
	for i, path := range paths {
		if err := checkRuntimePath(path, i < 4); err != nil {
			return Paths{}, err
		}
	}
	return Paths{paths[0], paths[1], paths[2], paths[3], paths[4]}, nil
}

// runtimePath is the folder named name (CONFIG, DATA, STATE, CACHE or BIN)
// for home: WORKBENCH_NAME_DIR, else, for a runtime folder, the workbench
// folder in XDG_NAME_HOME, else fallback, refused when it is relative, the
// file system root or home.
func runtimePath(home, name, fallback string, runtime bool) (string, error) {
	value := os.Getenv("WORKBENCH_" + name + "_DIR")
	if value == "" && runtime {
		if base := os.Getenv("XDG_" + name + "_HOME"); filepath.IsAbs(base) {
			value = filepath.Join(base, "workbench")
		}
	}
	if value == "" {
		value = fallback
	}
	if !filepath.IsAbs(value) {
		return "", Fail(
			ExitInvalid,
			"runtime_path",
			"WORKBENCH_"+name+"_DIR must be an absolute path",
			FixText("set WORKBENCH_"+name+"_DIR to an absolute path"),
		)
	}
	value = filepath.Clean(value)
	if value == "/" || value == home {
		what := "the file system root"
		if value == home {
			what = "your home folder"
		}
		return "", Fail(
			ExitInvalid,
			"runtime_path",
			display.Path(value)+" cannot be Workbench's "+strings.ToLower(name)+" folder",
			Cause("it is "+what),
			FixText("set WORKBENCH_"+name+"_DIR to a folder of its own"),
		)
	}
	return value, nil
}

// checkRuntimePath refuses value, one of the five folders of [Paths], when
// it is not safe to keep Workbench's files in: a runtime folder must pass
// the checks of a folder of its own files where it exists (see
// [ownFolder]), the folder of the entry point only those of the folders
// above it, and that it is a folder of this user's where it exists.
func checkRuntimePath(value string, runtime bool) error {
	if runtime {
		if err := safeOwnFolder("runtime_path", value); err != nil {
			return err
		}
		if err := privateFilesystem(value); err != nil {
			return err
		}
	} else if err := safeParents(value); err != nil {
		return err
	}
	info, err := os.Lstat(value)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil
	case err != nil:
		return lookupRefusal("runtime_path", value, err)
	case runtime:
		return ownFolder("runtime_path", value, info)
	case !info.IsDir():
		return notFolderRefusal("runtime_path", value, info)
	}
	return owned(value, info)
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
				found := filepath.Join(parent, marker)
				if _, err := os.Lstat(found); err == nil {
					return Fail(
						ExitInvalid,
						"scope",
						display.Path(path)+" is in a project",
						Cause(display.Path(parent)+" holds "+shellWord(marker)+
							", and machine configuration stays out of projects"),
						FixText("move "+shellHomePath(found)+" aside"),
					)
				} else if !errors.Is(err, fs.ErrNotExist) {
					parts := unreachable(found, false, err)
					if parts == nil {
						parts = []Part{Cause(FileError(found, err).Error())}
					}
					return Fail(
						ExitInvalid,
						"scope",
						"Cannot tell whether "+display.Path(path)+" is in a project",
						parts...,
					)
				}
			}
		}
	}
	if err := c.ownFilesRefusal(path); err != nil {
		return err
	}
	// Leaf symlinks are images for the checkpoint owner to qualify. Ancestor
	// symlinks still cannot redirect a managed target outside its scope.
	if err := safeParents(filepath.Dir(path)); err != nil {
		return err
	}
	return nil
}

// overlaps is what a headline that names path says of other, the noun
// naming it: that path is it, or, when they differ, that path overlaps it,
// named by its path too.
func overlaps(path, other, noun string) string {
	if path == other {
		return " is " + strings.TrimSuffix(noun, " ")
	}
	return " overlaps " + noun + display.Path(other)
}

// ownFilesRefusal refuses the target path when it overlaps what Workbench
// keeps for itself: its source, its config, data, state and cache folders,
// and its entry point, also under another name. Either fix moves the
// source, a checkout, so that the target lies apart from it: one that holds
// the source, to its name in the folder that holds the target; one that is
// the source or lies in it, to a name beside it. A shell's working folder
// moves with it, so the same command typed again there finds the target
// apart from the source.
func (c Context) ownFilesRefusal(path string) error {
	if source := c.Native.Source; source != "" && (Within(source, path) || Within(path, source)) {
		fix := moveFix(source, filepath.Join(filepath.Dir(path), filepath.Base(source)))
		if Within(source, path) {
			fix = moveAsideFix(source)
		}
		return Fail(
			ExitInvalid,
			"scope",
			display.Path(path)+overlaps(path, source, "the source "),
			Cause("Workbench does not write the source it applies"),
			fix,
		)
	}
	for i, own := range c.Paths.All()[:4] {
		if Within(own, path) || Within(path, own) {
			name := runtimeNames[i]
			return Fail(
				ExitInvalid,
				"scope",
				display.Path(path)+
					overlaps(path, own, "Workbench's "+strings.ToLower(name)+" folder "),
				Cause("Workbench does not write its own files as a target"),
				FixText("set WORKBENCH_"+name+"_DIR to a folder apart from it"),
			)
		}
	}
	entrypoint := c.Paths.Entrypoint()
	ownEntry := func(headline string) error {
		return Fail(
			ExitInvalid,
			"scope",
			headline,
			Cause("Workbench does not write its own entry point"),
			FixText("set WORKBENCH_BIN_DIR to a folder apart from it"),
		)
	}
	if Within(entrypoint, path) {
		return ownEntry(display.Path(path) + " is Workbench's entry point")
	}
	if entryInfo, entryErr := os.Stat(entrypoint); entryErr == nil {
		if Within(path, entrypoint) {
			return ownEntry(display.Path(path) + " holds Workbench's entry point " +
				display.Path(entrypoint))
		}
		if targetInfo, targetErr := os.Stat(
			path,
		); targetErr == nil &&
			os.SameFile(entryInfo, targetInfo) {
			return c.entrypointAlias(path)
		}
	} else if !errors.Is(entryErr, fs.ErrNotExist) {
		parts := unreachable(entrypoint, false, entryErr)
		if parts == nil {
			parts = []Part{Cause(causeAt(entrypoint, entryErr))}
		}
		return Fail(
			ExitInvalid,
			"scope",
			"Cannot read Workbench's entry point "+display.Path(entrypoint),
			parts...,
		)
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
		shown+" is Workbench's entry point under another name",
		Cause("Workbench does not write its own entry point"),
		replacedFile.fix(nil, path),
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
		display.Path(path)+" links to "+display.Path(target)+", among Workbench's own files",
		Cause("Workbench does not keep a link into them"),
		replacedLink.fix(nil, path),
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
		return Fail(
			ExitInvalid,
			"scope",
			"Invalid scope",
			Cause("a scope is a machine or project folder given by its absolute path"),
		)
	}
	canonical, err := ExistingDirectory(s.Root)
	if err != nil || canonical != s.Root {
		return Fail(ExitInvalid, "scope", "Scope directory changed or is not canonical")
	}
	return nil
}
