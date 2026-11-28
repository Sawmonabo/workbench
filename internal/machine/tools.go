package machine

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"cmp"
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

	"github.com/Sawmonabo/workbench/internal/download"
	"github.com/Sawmonabo/workbench/internal/operation"
	"github.com/Sawmonabo/workbench/internal/platform"
	"github.com/Sawmonabo/workbench/internal/release"
)

// toolEffect starts the name of the setup effect that installs one of the
// tools Workbench runs, before the tool's own name: private-uv.
const toolEffect = "private-"

// SetupTool is the tool effect installs, when it is a setup effect that
// installs one.
func SetupTool(effect operation.Effect) (string, bool) {
	return strings.CutPrefix(effect.Name, toolEffect)
}

// toolTitles are the tools' names as a person reads them, where they differ
// from the names setup gives them.
var toolTitles = map[string]string{"python3": "Python", "tomlkit": "TOML Kit"}

// ToolTitle is a tool's name as a person reads it: Python for python3.
func ToolTitle(name string) string { return cmp.Or(toolTitles[name], name) }

// setupDependencies is the sole management acquisition owner. It is called only
// inside an approved setup mutation, never from preview.
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
	// A probe cut short by an interruption is not a missing tool.
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	pinned := map[string]string{
		"chezmoi": requirements.Chezmoi,
		"uv":      requirements.UV,
		"python3": requirements.Python,
	}
	// Each tool is reported as setup reaches it: in place, or fetched and then
	// in place, with the previous private copy it replaces.
	for _, name := range []string{"chezmoi", "uv"} {
		if version := dependencyVersion(selected, name); version != "" {
			c.Tool(name, operation.ToolReady, version, "")
			continue
		}
		previous := replacedVersion(state.Dependencies, name, pinned[name])
		c.Tool(name, operation.ToolDownloading, pinned[name], previous)
		c.Step("downloading " + name)
		acquired, acquireErr := acquireTool(ctx, c, name, requirements)
		if acquireErr != nil {
			return nil, acquireErr
		}
		selected = append(selected, acquired)
		c.Tool(name, operation.ToolReady, acquired.Version, previous)
	}
	if version := dependencyVersion(selected, "python3"); version != "" {
		c.Tool("python3", operation.ToolReady, version, "")
	} else {
		previous := replacedVersion(state.Dependencies, "python3", requirements.Python)
		c.Tool("python3", operation.ToolInstalling, requirements.Python, previous)
		c.Step("installing Python " + requirements.Python)
		python, pythonErr := acquirePython(ctx, c, m, dependency(selected, "uv"), requirements)
		if pythonErr != nil {
			return nil, pythonErr
		}
		selected = append(selected, python)
		c.Tool("python3", operation.ToolReady, python.Version, previous)
	}
	if _, err = TomlkitPath(c, requirements); err == nil {
		c.Tool("tomlkit", operation.ToolReady, requirements.Tomlkit, "")
	} else {
		c.Tool("tomlkit", operation.ToolDownloading, requirements.Tomlkit, "")
		c.Step("downloading TOML Kit")
		if err = acquireTomlkit(ctx, c, requirements); err != nil {
			return nil, err
		}
		c.Tool("tomlkit", operation.ToolReady, requirements.Tomlkit, "")
	}
	c.Step("checking the installed tools")
	qualified, results := ResolveDependencies(ctx, c, requirements, selected, "")
	// A check cut short by an interruption is not a failed tool.
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	var failed []operation.Component
	for _, result := range results {
		if result.Status != operation.StatusComplete {
			failed = append(failed, result)
			c.Tool(result.Name, operation.ToolFailed, "", "")
		}
	}
	if len(failed) > 0 {
		return nil, failedTools(failed)
	}
	state.Dependencies = qualified
	if err = m.WriteState(*state); err != nil {
		return nil, err
	}
	return qualified, nil
}

// failedTools is the refusal of the installed tools whose checks failed: one
// tool's own headline and body, led by its name, or, for several, a line
// for each tool, its headline, and every fix line they give.
func failedTools(failed []operation.Component) error {
	if len(failed) == 1 {
		return operation.FailWith(
			operation.ExitBlocked,
			"dependency",
			failed[0].Name+": "+failed[0].Message,
			failed[0].Body,
		)
	}
	var body operation.Body
	for _, tool := range failed {
		body.Lines = append(body.Lines, operation.Line{Label: tool.Name, Text: tool.Message})
		body = body.With(tool.Body)
	}
	return operation.FailWith(
		operation.ExitBlocked,
		"dependency",
		"Installed tools did not pass their checks",
		body,
	)
}

// partialOf reports whether name is the temporary file of one of files, left
// by an interrupted writeNewOrIdentical.
func partialOf(name string, files map[string][]byte) bool {
	for original := range files {
		if partialName(original) == name {
			return true
		}
	}
	return false
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
	asset := "chezmoi_" + version + "_" + platform.System + "_" + runtime.GOARCH + ".tar.gz"
	location := "https://github.com/twpayne/chezmoi/releases/download/v" + version + "/" + asset
	if name == "uv" {
		version = requirements.UV
		hash = requirements.UVSHA256[release.Target()]
		cpu := map[string]string{"amd64": "x86_64", "arm64": "aarch64"}[runtime.GOARCH]
		asset = "uv-" + cpu + "-" + platform.AssetTriple + ".tar.gz"
		location = "https://github.com/astral-sh/uv/releases/download/" + version + "/" + asset
	}
	if len(hash) != 64 {
		return operation.Dependency{}, operation.Fail(
			operation.ExitBlocked,
			"dependency_trust",
			"No reviewed management artifact digest for this target",
		)
	}
	data, err := release.Download(ctx, location, release.MaxDownload, c.Transfer(name))
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
	if err = operation.PrivateTree(c.Paths.Data, directory, true); err != nil {
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
	if err := operation.PrivateTree(c.Paths.Cache, cache, true); err != nil {
		return operation.Dependency{}, err
	}
	if err := operation.PrivateTree(c.Paths.Data, install, true); err != nil {
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
		Directory: "/",
		// uv downloads the interpreter, so it needs the user's proxy and CA settings.
		Environment: append(
			[]string{"HOME=" + c.Home, "PATH=/usr/bin:/bin", "UV_NO_PROGRESS=1"},
			operation.NetworkEnvironment()...,
		),
		Mutates: true,
		Timeout: 10 * time.Minute,
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
// unpacks it privately for the project editor adapter. The wheel comes from
// PyPI's file host, not a release, so a refusal reads as the download's own.
func acquireTomlkit(ctx context.Context, c operation.Context, requirements Requirements) error {
	wheel, _, err := download.Get(
		ctx,
		requirements.TomlkitURL,
		16<<20,
		download.Request{Accept: "application/octet-stream", Progress: c.Transfer("tomlkit")},
	)
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
	if err = operation.PrivateTree(c.Paths.Data, lib, true); err != nil {
		return err
	}
	files, err := wheelFiles(wheel)
	if err != nil {
		return err
	}
	for name, data := range files {
		target := filepath.Join(lib, name)
		if err = operation.PrivateTree(c.Paths.Data, filepath.Dir(target), true); err != nil {
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
		if (member == "" || member == ".") && header.Typeflag == tar.TypeDir {
			continue
		}
		if !release.Member(member) || seen[member] || len(seen) > 4096 {
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
		if !file.Mode().IsRegular() || !release.Member(name) || len(files) > 1024 ||
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

// InstallFix is the fix lines that install Workbench's tools: update, or an
// apply at a terminal, for a release; update --local-build for a checkout,
// which installs the tools its versions.toml pins. update installs only
// Workbench's own checked tool files and never asks, so a coding agent can
// run either update; its apply is refused before it installs anything (see
// the CLI's consent gate), so no line suggests an unattended apply.
func InstallFix(c operation.Context) []operation.Part {
	command := c.WorkbenchCommand()
	if c.Native.Developer {
		return []operation.Part{operation.FixText(command + " update --local-build")}
	}
	return []operation.Part{
		operation.FixText(command + " update"),
		operation.FixText("or " + command + " apply at a terminal"),
	}
}

// TomlkitPath is read-only and rechecks the trusted wheel and every installed
// member before the caller gives that directory to isolated Python.
func TomlkitPath(c operation.Context, requirements Requirements) (string, error) {
	directory := filepath.Join(c.Paths.Data, "tools", "tomlkit", requirements.Tomlkit)
	if err := operation.PrivateTree(c.Paths.Data, directory, false); err != nil {
		return "", err
	}
	data, err := operation.ReadRegularFile(filepath.Join(directory, "distribution.whl"), 16<<20)
	if err != nil {
		return "", operation.Fail(
			operation.ExitBlocked,
			"tomlkit",
			"Workbench's TOML Kit is missing",
			InstallFix(c)...,
		)
	}
	if operation.SHA256Hex(data) != requirements.TomlkitSHA256 {
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
			return operation.PrivateTree(c.Paths.Data, full, false)
		}
		name, relErr := filepath.Rel(library, full)
		if relErr != nil {
			return relErr
		}
		if _, ok := files[name]; !ok || entry.Type()&os.ModeSymlink != 0 {
			if partialOf(name, files) && entry.Type().IsRegular() {
				return operation.Fail(
					operation.ExitBlocked,
					"tomlkit",
					"An interrupted TOML Kit install left "+name,
					InstallFix(c)...,
				)
			}
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
		info, err := os.Lstat(file)
		if err != nil || !info.Mode().IsRegular() || info.Size() != int64(len(expected)) {
			return "", operation.Fail(
				operation.ExitBlocked,
				"tomlkit",
				"Private TOML Kit files were modified",
			)
		}
		actual, readErr := operation.ReadRegularFile(file, int64(len(expected)))
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
		requirements, err := parseRequirements([]byte(metadata.Versions))
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
