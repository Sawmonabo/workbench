package machine

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/Sawmonabo/workbench/internal/operation"
	"github.com/Sawmonabo/workbench/internal/release"
)

// SetupPlan is offline. It lists the tools setup would install and the native
// questionnaire; neither approves target configuration or provisioning.
func SetupPlan(ctx context.Context, c operation.Context) (operation.Plan, error) {
	return setupPlan(ctx, c, true)
}

// ToolsPlan is [SetupPlan] without the questionnaire: only the pinned tools
// Workbench runs that are missing.
func ToolsPlan(ctx context.Context, c operation.Context) (operation.Plan, error) {
	return setupPlan(ctx, c, false)
}

func setupPlan(
	ctx context.Context,
	c operation.Context,
	questions bool,
) (_ operation.Plan, err error) {
	defer operation.Annotate(&err, "plan management setup")
	plan := operation.Plan{
		Scope:    c.Scope,
		Complete: true,
		RecoveryLimits: []string{
			"Private tool acquisition and native answer initialization are separate from configuration apply; borrowed installations are retained",
		},
	}
	files, identity, err := SourceSnapshot(c.Native.Source, false)
	plan.Source = identity
	if err != nil {
		return plan, err
	}
	requirements, err := SourceRequirements(files)
	if err != nil {
		return plan, err
	}
	state, err := operation.ReadState(c.Paths)
	if err != nil {
		return plan, err
	}
	var recorded []operation.Dependency
	if state != nil {
		recorded = state.Dependencies
	}
	plan.Dependencies, _ = ResolveDependencies(ctx, c, requirements, recorded, os.Getenv("PATH"))
	for _, name := range []string{"chezmoi", "uv", "python3"} {
		if dependency(plan.Dependencies, name) == "" {
			plan.Effects = append(
				plan.Effects,
				operation.Effect{
					Name:        "private-" + name,
					Description: "Acquire a pinned private management dependency; existing unqualified installations are retained",
					Privilege:   "user",
					Recovery:    "Private installation retained; no global tool replacement",
				},
			)
		}
	}
	if _, err = TomlkitPath(c, requirements); err != nil {
		plan.Effects = append(
			plan.Effects,
			operation.Effect{
				Name:        "private-tomlkit",
				Description: "Acquire the pinned, SHA-256 verified TOML Kit wheel in Workbench's private library directory",
				Privilege:   "user",
				Recovery:    "Private library retained",
			},
		)
	}
	if !questions {
		return plan, nil
	}
	plan.Effects = append(
		plan.Effects,
		operation.Effect{
			Name:        "native-initialization",
			Description: "Copy verified source into a private application context and run the canonical native questionnaire; save private answers only",
			Privilege:   "user",
			Recovery:    "Previously saved answers are retained until successful validation",
		},
	)
	if _, err = os.Lstat(c.Native.Config); errors.Is(err, fs.ErrNotExist) {
		return plan, nil
	}
	raw, err := operation.ReadPrivateInput(c.Native.Config, 1<<20)
	if err != nil {
		return plan, err
	}
	answers, err := readAnswers(raw)
	if err != nil {
		return plan, err
	}
	input := operation.Input{Name: "answers", Digest: operation.SHA256Hex(raw)}
	if validateAnswers(answers) != nil {
		// The questionnaire asks for what this release needs and they lack.
		input.Name = "incomplete-answers"
	}
	plan.Inputs = append(plan.Inputs, input)
	return plan, nil
}

// setupDependencies is the sole management acquisition owner. It is called only
// inside an approved setup mutation, never from preview or config-only apply.
func setupDependencies(
	ctx context.Context,
	c operation.Context,
	m *operation.Mutation,
) ([]operation.Dependency, error) {
	if err := m.Check(); err != nil {
		return nil, err
	}
	if os.Geteuid() == 0 {
		return nil, operation.Fail(
			operation.ExitBlocked,
			"root",
			"Do not initialize Workbench as root",
		)
	}
	state, err := operation.ReadState(c.Paths)
	if err != nil {
		return nil, err
	}
	if state == nil {
		state = &operation.State{SchemaVersion: 1}
	}
	requirements, err := ManagementRequirements(c)
	if err != nil {
		return nil, err
	}
	selected, _ := ResolveDependencies(ctx, c, requirements, state.Dependencies, os.Getenv("PATH"))
	for _, name := range []string{"chezmoi", "uv"} {
		if dependency(selected, name) != "" {
			continue
		}
		c.Step("downloading " + name)
		acquired, acquireErr := acquireTool(ctx, c, name, requirements)
		if acquireErr != nil {
			return nil, acquireErr
		}
		selected = append(selected, acquired)
	}
	if dependency(selected, "python3") == "" {
		c.Step("installing Python " + requirements.Python)
		python, pythonErr := acquirePython(ctx, c, m, dependency(selected, "uv"), requirements)
		if pythonErr != nil {
			return nil, pythonErr
		}
		selected = append(selected, python)
	}
	if _, err = TomlkitPath(c, requirements); err != nil {
		c.Step("downloading TOML Kit")
		if err = acquireTomlkit(ctx, c, requirements); err != nil {
			return nil, err
		}
	}
	c.Step("checking the installed tools")
	qualified, results := ResolveDependencies(ctx, c, requirements, selected, "")
	var failed []string
	for _, result := range results {
		if result.Status != operation.StatusComplete {
			failed = append(failed, result.Name+": "+result.Message)
		}
	}
	if len(failed) > 0 {
		return nil, operation.Fail(
			operation.ExitBlocked,
			"dependency",
			"Acquired tools did not pass their checks; "+strings.Join(failed, "; "),
		)
	}
	state.Dependencies = qualified
	if err = m.WriteState(*state); err != nil {
		return nil, err
	}
	return qualified, nil
}

func writeNewOrIdentical(target string, data []byte, mode os.FileMode) error {
	if info, err := os.Lstat(target); err == nil {
		if !info.Mode().IsRegular() {
			return operation.Fail(
				operation.ExitConflict,
				"dependency_conflict",
				"Existing private tool has unsupported file type",
			)
		}
		existing, readErr := os.ReadFile(target)
		if readErr != nil || !bytes.Equal(existing, data) {
			return operation.Fail(
				operation.ExitConflict,
				"dependency_conflict",
				"Existing private tool differs; it was retained",
			)
		}
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	file, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	_, err = file.Write(data)
	if err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err != nil {
		return err
	}
	return closeErr
}

// acquireTool downloads the pinned chezmoi or uv release for this target,
// checks its reviewed digest and installs the binary privately.
func acquireTool(
	ctx context.Context,
	c operation.Context,
	name string,
	requirements Requirements,
) (operation.Dependency, error) {
	version := requirements.Chezmoi
	hash := requirements.ChezmoiSHA256[release.Target()]
	asset := "chezmoi_" + version + "_" + runtime.GOOS + "_" + runtime.GOARCH + ".tar.gz"
	location := "https://github.com/twpayne/chezmoi/releases/download/v" + version + "/" + asset
	if name == "uv" {
		version = requirements.UV
		hash = requirements.UVSHA256[release.Target()]
		cpu := map[string]string{"amd64": "x86_64", "arm64": "aarch64"}[runtime.GOARCH]
		platform := map[string]string{"darwin": "apple-darwin", "linux": "unknown-linux-gnu"}[runtime.GOOS]
		asset = "uv-" + cpu + "-" + platform + ".tar.gz"
		location = "https://github.com/astral-sh/uv/releases/download/" + version + "/" + asset
	}
	if len(hash) != 64 {
		return operation.Dependency{}, operation.Fail(
			operation.ExitBlocked,
			"dependency_trust",
			"No reviewed management artifact digest for this target",
		)
	}
	data, err := release.Download(ctx, location, release.MaxDownload)
	if err != nil {
		return operation.Dependency{}, err
	}
	if operation.SHA256Hex(data) != hash {
		return operation.Dependency{}, operation.Fail(
			operation.ExitInvalid,
			"dependency_trust",
			"Management archive checksum mismatch; executable was not used",
		)
	}
	binary, err := toolArchive(data, name)
	if err != nil {
		return operation.Dependency{}, err
	}
	directory := filepath.Join(c.Paths.Data, "tools", name, version)
	if err = release.PrivateDirectory(c.Paths.Data, directory, true); err != nil {
		return operation.Dependency{}, err
	}
	executable := filepath.Join(directory, name)
	if err = writeNewOrIdentical(executable, binary, 0o700); err != nil {
		return operation.Dependency{}, err
	}
	return operation.Dependency{
		Name:    name,
		Path:    executable,
		Version: version,
		Owner:   "workbench",
	}, nil
}

// acquirePython installs the pinned CPython privately with uv.
func acquirePython(
	ctx context.Context,
	c operation.Context,
	m *operation.Mutation,
	uv string,
	requirements Requirements,
) (operation.Dependency, error) {
	install := filepath.Join(c.Paths.Data, "tools", "python", requirements.Python)
	cache := filepath.Join(c.Paths.Cache, "uv")
	if err := release.PrivateDirectory(c.Paths.Cache, cache, true); err != nil {
		return operation.Dependency{}, err
	}
	if err := release.PrivateDirectory(c.Paths.Data, install, true); err != nil {
		return operation.Dependency{}, err
	}
	_, err := operation.Run(ctx, c, m, operation.Process{
		Executable: uv,
		Args: []string{
			"--cache-dir",
			cache,
			"python",
			"install",
			"--install-dir",
			install,
			"--no-bin",
			requirements.Python,
		},
		Directory:   "/",
		Environment: []string{"HOME=" + c.Home, "PATH=/usr/bin:/bin", "UV_NO_PROGRESS=1"},
		Mutates:     true,
		Timeout:     10 * time.Minute,
	})
	if err != nil {
		return operation.Dependency{}, err
	}
	// uv also creates a minor-version alias; select the exact acquired patch
	// directory rather than counting that alias as a second interpreter.
	pattern := filepath.Join(install, "cpython-"+requirements.Python+"-*", "bin", "python3")
	matches, err := filepath.Glob(pattern)
	if err != nil || len(matches) != 1 {
		return operation.Dependency{}, operation.Fail(
			operation.ExitBlocked,
			"python",
			"Private Python installation did not produce one interpreter",
		)
	}
	return operation.Dependency{
		Name:    "python3",
		Path:    matches[0],
		Version: requirements.Python,
		Owner:   "workbench",
	}, nil
}

// acquireTomlkit downloads the pinned TOML Kit wheel, checks its digest and
// unpacks it privately for the project editor adapter.
func acquireTomlkit(ctx context.Context, c operation.Context, requirements Requirements) error {
	wheel, err := release.Download(ctx, requirements.TomlkitURL, 16<<20)
	if err != nil {
		return err
	}
	if operation.SHA256Hex(wheel) != requirements.TomlkitSHA256 {
		return operation.Fail(
			operation.ExitInvalid,
			"dependency_trust",
			"TOML Kit wheel checksum mismatch",
		)
	}
	directory := filepath.Join(c.Paths.Data, "tools", "tomlkit", requirements.Tomlkit)
	lib := filepath.Join(directory, "lib")
	if err = release.PrivateDirectory(c.Paths.Data, lib, true); err != nil {
		return err
	}
	files, err := wheelFiles(wheel)
	if err != nil {
		return err
	}
	for name, data := range files {
		target := filepath.Join(lib, name)
		if err = release.PrivateDirectory(c.Paths.Data, filepath.Dir(target), true); err != nil {
			return err
		}
		if err = writeNewOrIdentical(target, data, 0o600); err != nil {
			return err
		}
	}
	return writeNewOrIdentical(filepath.Join(directory, "distribution.whl"), wheel, 0o600)
}

func toolArchive(data []byte, name string) ([]byte, error) {
	unsafeArchive := operation.Fail(
		operation.ExitInvalid,
		"dependency_archive",
		"Unsafe management archive",
	)
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	defer func() { _ = gz.Close() }()
	reader := tar.NewReader(gz)
	seen := map[string]bool{}
	var binary []byte
	var total int64
	for {
		header, readErr := reader.Next()
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return nil, readErr
		}
		member := strings.TrimPrefix(strings.TrimSuffix(header.Name, "/"), "./")
		if member == "" && header.Typeflag == tar.TypeDir {
			continue
		}
		if path.Clean(member) != member || path.IsAbs(member) || strings.HasPrefix(member, "../") ||
			strings.Contains(member, "\\") ||
			seen[member] ||
			len(seen) > 4096 {
			return nil, unsafeArchive
		}
		seen[member] = true
		if header.Typeflag == tar.TypeDir {
			if header.Size != 0 {
				return nil, unsafeArchive
			}
			continue
		}
		if header.Typeflag != tar.TypeReg || header.Size < 0 || header.Size > release.MaxDownload {
			return nil, unsafeArchive
		}
		total += header.Size
		if total > 256<<20 {
			return nil, unsafeArchive
		}
		content, readErr := io.ReadAll(io.LimitReader(reader, header.Size+1))
		if readErr != nil {
			return nil, readErr
		}
		if path.Base(member) == name {
			if binary != nil {
				return nil, unsafeArchive
			}
			binary = content
		}
	}
	if len(binary) == 0 {
		return nil, unsafeArchive
	}
	return binary, nil
}

func wheelFiles(data []byte) (map[string][]byte, error) {
	archive, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, err
	}
	files := map[string][]byte{}
	var total int64
	for _, file := range archive.File {
		name := file.Name
		if file.FileInfo().IsDir() {
			continue
		}
		if !file.Mode().IsRegular() || path.Clean(name) != name || path.IsAbs(name) ||
			strings.HasPrefix(name, "../") ||
			strings.Contains(name, "\\") ||
			len(files) > 1024 ||
			file.UncompressedSize64 > 16<<20 {
			return nil, operation.Fail(
				operation.ExitInvalid,
				"dependency_archive",
				"Unsafe wheel member",
			)
		}
		if _, exists := files[name]; exists {
			return nil, operation.Fail(
				operation.ExitInvalid,
				"dependency_archive",
				"Duplicate wheel member",
			)
		}
		total += int64(file.UncompressedSize64)
		if total > 16<<20 {
			return nil, operation.Fail(
				operation.ExitInvalid,
				"dependency_archive",
				"Wheel exceeds extraction bound",
			)
		}
		reader, openErr := file.Open()
		if openErr != nil {
			return nil, openErr
		}
		content, readErr := io.ReadAll(io.LimitReader(reader, 16<<20+1))
		_ = reader.Close()
		if readErr != nil {
			return nil, readErr
		}
		files[name] = content
	}
	return files, nil
}

// TomlkitPath is read-only and rechecks the trusted wheel and every installed
// member before the caller gives that directory to isolated Python.
func TomlkitPath(c operation.Context, requirements Requirements) (string, error) {
	directory := filepath.Join(c.Paths.Data, "tools", "tomlkit", requirements.Tomlkit)
	if err := release.PrivateDirectory(c.Paths.Data, directory, false); err != nil {
		return "", err
	}
	info, err := os.Lstat(filepath.Join(directory, "distribution.whl"))
	if err != nil || !info.Mode().IsRegular() || info.Size() > 16<<20 {
		return "", operation.Fail(
			operation.ExitBlocked,
			"tomlkit",
			"Workbench's TOML Kit is missing; workbench apply installs it",
		)
	}
	data, err := os.ReadFile(filepath.Join(directory, "distribution.whl"))
	if err != nil || operation.SHA256Hex(data) != requirements.TomlkitSHA256 {
		return "", operation.Fail(
			operation.ExitBlocked,
			"tomlkit",
			"Private TOML Kit wheel failed integrity verification",
		)
	}
	files, err := wheelFiles(data)
	if err != nil {
		return "", err
	}
	library := filepath.Join(directory, "lib")
	err = filepath.WalkDir(library, func(full string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return release.PrivateDirectory(c.Paths.Data, full, false)
		}
		name, relErr := filepath.Rel(library, full)
		if relErr != nil {
			return relErr
		}
		if _, ok := files[name]; !ok || entry.Type()&os.ModeSymlink != 0 {
			return operation.Fail(
				operation.ExitBlocked,
				"tomlkit",
				"Unexpected private library files or links; refuse Python imports",
			)
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	for name, expected := range files {
		file := filepath.Join(library, name)
		info, err = os.Lstat(file)
		if err != nil || !info.Mode().IsRegular() || info.Size() != int64(len(expected)) {
			return "", operation.Fail(
				operation.ExitBlocked,
				"tomlkit",
				"Private TOML Kit files were modified",
			)
		}
		actual, readErr := os.ReadFile(file)
		if readErr != nil || !bytes.Equal(expected, actual) {
			return "", operation.Fail(
				operation.ExitBlocked,
				"tomlkit",
				"Private TOML Kit content was modified",
			)
		}
	}
	return library, nil
}

// InstallTools installs the pinned tools Workbench runs that are missing.
func InstallTools(ctx context.Context, c operation.Context, m *operation.Mutation) error {
	stop := c.ShowProgress("Setting up chezmoi, uv and Python")
	defer stop()
	_, err := setupDependencies(ctx, c, m)
	return err
}

// Setup calls native initialization exactly once, preserving the canonical
// questionnaire. A terminal enables genuine native prompts, never simulations.
// The saved answers named in ask are asked again.
func Setup(
	ctx context.Context,
	c operation.Context,
	m *operation.Mutation,
	terminal *os.File,
	ask []string,
) (_ operation.Context, err error) {
	defer operation.Annotate(&err, "set up management tools")
	// The questionnaire below prompts; InstallTools stops its progress first.
	if err = InstallTools(ctx, c, m); err != nil {
		return c, err
	}
	files, identity, err := SourceSnapshot(c.Native.Source, false)
	if err != nil {
		return c, err
	}
	contextDirectory := filepath.Join(
		c.Paths.Data,
		"application-contexts",
		identity.Release+"-"+identity.ContentDigest,
	)
	if err = release.PrivateDirectory(c.Paths.Data, contextDirectory, true); err != nil {
		return c, err
	}
	for name, data := range files {
		target := filepath.Join(contextDirectory, name)
		if err = release.PrivateDirectory(c.Paths.Data, filepath.Dir(target), true); err != nil {
			return c, err
		}
		if err = writeNewOrIdentical(target, data, 0o600); err != nil {
			return c, err
		}
	}
	if info, statErr := os.Lstat(filepath.Join(c.Native.Source, "release.json")); statErr == nil {
		if !info.Mode().IsRegular() || info.Size() > 1<<20 {
			return c, operation.Fail(
				operation.ExitInvalid,
				"release",
				"Invalid native context release metadata",
			)
		}
		metadata, readErr := os.ReadFile(filepath.Join(c.Native.Source, "release.json"))
		if readErr != nil {
			return c, readErr
		}
		if err = writeNewOrIdentical(
			filepath.Join(contextDirectory, "release.json"),
			metadata,
			0o600,
		); err != nil {
			return c, err
		}
	} else if !os.IsNotExist(statErr) {
		return c, statErr
	}
	c, err = selectPrivateContext(c, contextDirectory)
	if err != nil {
		return c, err
	}
	if _, err = initialize(ctx, c, m, terminal, ask); err != nil {
		return c, err
	}
	c.Native.Config = filepath.Join(c.Paths.Config, "machine.toml")
	return c, nil
}

// StaleSetup lists setup output that no kept release needs as removal edits:
// application contexts of other sources, and private tool versions that no
// kept release pins and state does not record in use.
func StaleSetup(c operation.Context, kept []release.Metadata) ([]operation.Edit, error) {
	state, err := operation.ReadState(c.Paths)
	if err != nil {
		return nil, err
	}
	var stale []operation.Edit
	contexts := filepath.Join(c.Paths.Data, "application-contexts")
	names, err := privateEntries(c, contexts)
	if err != nil {
		return nil, err
	}
	for _, name := range names {
		used := slices.ContainsFunc(kept, func(metadata release.Metadata) bool {
			return name == metadata.Release+"-"+metadata.SourceDigest
		})
		if !used {
			stale = append(stale, operation.Edit{
				Path:        filepath.Join(contexts, name),
				Action:      "remove",
				Description: "Remove the setup context of a source no kept release uses",
			})
		}
	}
	tools, err := staleTools(c, state, kept)
	return append(stale, tools...), err
}

// staleTools lists private tool versions that no kept release pins and state
// does not record in use. Each release's metadata carries the versions.toml
// built into its executable; if one cannot be read, no tool is removed.
func staleTools(
	c operation.Context,
	state *operation.State,
	kept []release.Metadata,
) ([]operation.Edit, error) {
	pinned := map[string][]string{}
	for _, metadata := range kept {
		requirements, err := ParseRequirements([]byte(metadata.Versions))
		if err != nil {
			return nil, nil
		}
		pinned["chezmoi"] = append(pinned["chezmoi"], requirements.Chezmoi)
		pinned["uv"] = append(pinned["uv"], requirements.UV)
		pinned["python"] = append(pinned["python"], requirements.Python)
		pinned["tomlkit"] = append(pinned["tomlkit"], requirements.Tomlkit)
	}
	var recorded []operation.Dependency
	if state != nil {
		recorded = state.Dependencies
	}
	root := filepath.Join(c.Paths.Data, "tools")
	tools, err := privateEntries(c, root)
	if err != nil {
		return nil, err
	}
	var stale []operation.Edit
	for _, tool := range tools {
		versions, err := privateEntries(c, filepath.Join(root, tool))
		if err != nil {
			return nil, err
		}
		for _, version := range versions {
			directory := filepath.Join(root, tool, version)
			inUse := slices.ContainsFunc(recorded, func(dependency operation.Dependency) bool {
				return operation.Within(directory, dependency.Path)
			})
			if slices.Contains(pinned[tool], version) || inUse {
				continue
			}
			stale = append(stale, operation.Edit{
				Path:        directory,
				Action:      "remove",
				Description: "Remove a private " + tool + " version that no kept release pins or uses",
			})
		}
	}
	return stale, nil
}

// privateEntries returns the names in directory, a private runtime directory
// that may not exist yet.
func privateEntries(c operation.Context, directory string) ([]string, error) {
	entries, err := os.ReadDir(directory)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if err = release.PrivateDirectory(c.Paths.Data, directory, false); err != nil {
		return nil, err
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	return names, nil
}
