package machine

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/Sawmonabo/workbench/internal/operation"
	"github.com/Sawmonabo/workbench/internal/release"
)

// SetupPlan is offline. Downloads and native initialization are disclosed setup
// effects; their consent never approves target configuration or provisioning.
func SetupPlan(ctx context.Context, c operation.Context) (operation.Plan, error) {
	plan := operation.Plan{
		Scope:    c.Scope,
		Complete: true,
		RecoveryLimits: []string{
			"Private tool acquisition and native answer initialization are separate from configuration apply; borrowed installations are retained",
		},
	}
	_, identity, err := SourceSnapshot(c.Native.Source)
	plan.Source = identity
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
	plan.Dependencies, _ = ResolveDependencies(ctx, c, recorded, os.Getenv("PATH"))
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
	if _, err = TomlkitPath(c); err != nil {
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
	plan.Effects = append(
		plan.Effects,
		operation.Effect{
			Name:        "native-initialization",
			Description: "Copy verified source into a private application context and run the canonical native questionnaire; save private answers only",
			Privilege:   "user",
			Recovery:    "Previously saved answers are retained until successful validation",
		},
	)
	if raw, readErr := operation.ReadPrivateInput(c.Native.Config, 1<<20); readErr == nil {
		if _, err = parseAnswers(raw); err != nil {
			return plan, err
		}
		plan.Inputs = append(plan.Inputs, operation.Input{Name: "answers", Digest: digest(raw)})
	} else if !os.IsNotExist(func() error { _, e := os.Lstat(c.Native.Config); return e }()) {
		return plan, readErr
	}
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
		return nil, operation.Fail(3, "root", "Do not initialize Workbench as root")
	}
	state, err := operation.ReadState(c.Paths)
	if err != nil {
		return nil, err
	}
	if state == nil {
		state = &operation.State{SchemaVersion: 1}
	}
	selected, _ := ResolveDependencies(ctx, c, state.Dependencies, os.Getenv("PATH"))
	requirements := ManagementRequirements()
	for _, name := range []string{"chezmoi", "uv"} {
		if dependency(selected, name) != "" {
			continue
		}
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
			return nil, operation.Fail(
				3,
				"dependency_trust",
				"No reviewed management artifact digest for this target",
			)
		}
		data, downloadErr := release.Download(ctx, location, release.MaxDownload)
		if downloadErr != nil {
			return nil, downloadErr
		}
		if digest(data) != hash {
			return nil, operation.Fail(
				2,
				"dependency_trust",
				"Management archive checksum mismatch; executable was not used",
			)
		}
		binary, extractErr := toolArchive(data, name)
		if extractErr != nil {
			return nil, extractErr
		}
		directory := filepath.Join(c.Paths.Data, "tools", name, version)
		if err = release.PrivateDirectory(c.Paths.Data, directory, true); err != nil {
			return nil, err
		}
		executable := filepath.Join(directory, name)
		if err = writeNewOrIdentical(executable, binary, 0o700); err != nil {
			return nil, err
		}
		selected = append(
			selected,
			operation.Dependency{
				Name:    name,
				Path:    executable,
				Version: version,
				Owner:   "workbench",
			},
		)
	}
	if dependency(selected, "python3") == "" {
		install := filepath.Join(c.Paths.Data, "tools", "python", requirements.Python)
		cache := filepath.Join(c.Paths.Cache, "uv")
		if err = release.PrivateDirectory(c.Paths.Cache, cache, true); err != nil {
			return nil, err
		}
		if err = release.PrivateDirectory(c.Paths.Data, install, true); err != nil {
			return nil, err
		}
		_, err = operation.Run(
			ctx,
			c,
			m,
			operation.Process{
				Executable: dependency(selected, "uv"),
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
			},
		)
		if err != nil {
			return nil, err
		}
		// uv also creates a minor-version alias; select the exact acquired patch
		// directory rather than counting that alias as a second interpreter.
		matches, globErr := filepath.Glob(
			filepath.Join(install, "cpython-"+requirements.Python+"-*", "bin", "python3"),
		)
		if globErr != nil || len(matches) != 1 {
			return nil, operation.Fail(
				3,
				"python",
				"Private Python installation did not produce one interpreter",
			)
		}
		selected = append(
			selected,
			operation.Dependency{
				Name:    "python3",
				Path:    matches[0],
				Version: requirements.Python,
				Owner:   "workbench",
			},
		)
	}
	if _, err = TomlkitPath(c); err != nil {
		wheel, downloadErr := release.Download(ctx, requirements.TomlkitURL, 16<<20)
		if downloadErr != nil {
			return nil, downloadErr
		}
		if digest(wheel) != requirements.TomlkitSHA256 {
			return nil, operation.Fail(2, "dependency_trust", "TOML Kit wheel checksum mismatch")
		}
		directory := filepath.Join(c.Paths.Data, "tools", "tomlkit", requirements.Tomlkit)
		if err = release.PrivateDirectory(
			c.Paths.Data,
			filepath.Join(directory, "lib"),
			true,
		); err != nil {
			return nil, err
		}
		files, extractErr := wheelFiles(wheel)
		if extractErr != nil {
			return nil, extractErr
		}
		for name, data := range files {
			target := filepath.Join(directory, "lib", name)
			if err = release.PrivateDirectory(
				c.Paths.Data,
				filepath.Dir(target),
				true,
			); err != nil {
				return nil, err
			}
			if err = writeNewOrIdentical(target, data, 0o600); err != nil {
				return nil, err
			}
		}
		if err = writeNewOrIdentical(
			filepath.Join(directory, "distribution.whl"),
			wheel,
			0o600,
		); err != nil {
			return nil, err
		}
	}
	qualified, results := ResolveDependencies(ctx, c, selected, "")
	for _, result := range results {
		if result.Status != "complete" {
			return nil, operation.Fail(
				3,
				"dependency",
				"Acquired management dependencies did not pass their capability checks",
			)
		}
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
				4,
				"dependency_conflict",
				"Existing private tool has unsupported file type",
			)
		}
		existing, readErr := os.ReadFile(target)
		if readErr != nil || !bytes.Equal(existing, data) {
			return operation.Fail(
				4,
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

func toolArchive(data []byte, name string) ([]byte, error) {
	fail := func() ([]byte, error) {
		return nil, operation.Fail(2, "dependency_archive", "Unsafe management archive")
	}
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
		if readErr == io.EOF {
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
			return fail()
		}
		seen[member] = true
		if header.Typeflag == tar.TypeDir {
			if header.Size != 0 {
				return fail()
			}
			continue
		}
		if header.Typeflag != tar.TypeReg || header.Size < 0 || header.Size > release.MaxDownload {
			return fail()
		}
		total += header.Size
		if total > 256<<20 {
			return fail()
		}
		content, readErr := io.ReadAll(io.LimitReader(reader, header.Size+1))
		if readErr != nil {
			return nil, readErr
		}
		if path.Base(member) == name {
			if binary != nil {
				return fail()
			}
			binary = content
		}
	}
	if len(binary) == 0 {
		return fail()
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
			return nil, operation.Fail(2, "dependency_archive", "Unsafe wheel member")
		}
		if _, exists := files[name]; exists {
			return nil, operation.Fail(2, "dependency_archive", "Duplicate wheel member")
		}
		total += int64(file.UncompressedSize64)
		if total > 16<<20 {
			return nil, operation.Fail(2, "dependency_archive", "Wheel exceeds extraction bound")
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
func TomlkitPath(c operation.Context) (string, error) {
	requirements := ManagementRequirements()
	directory := filepath.Join(c.Paths.Data, "tools", "tomlkit", requirements.Tomlkit)
	if err := release.PrivateDirectory(c.Paths.Data, directory, false); err != nil {
		return "", err
	}
	info, err := os.Lstat(filepath.Join(directory, "distribution.whl"))
	if err != nil || !info.Mode().IsRegular() || info.Size() > 16<<20 {
		return "", operation.Fail(
			3,
			"tomlkit",
			"Private TOML Kit is missing; approve setup before project editing",
		)
	}
	data, err := os.ReadFile(filepath.Join(directory, "distribution.whl"))
	if err != nil || digest(data) != requirements.TomlkitSHA256 {
		return "", operation.Fail(
			3,
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
				3,
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
			return "", operation.Fail(3, "tomlkit", "Private TOML Kit files were modified")
		}
		actual, readErr := os.ReadFile(file)
		if readErr != nil || !bytes.Equal(expected, actual) {
			return "", operation.Fail(3, "tomlkit", "Private TOML Kit content was modified")
		}
	}
	return library, nil
}

// Setup calls native initialization exactly once, preserving the canonical
// questionnaire. A terminal enables genuine native prompts, never simulations.
func Setup(
	ctx context.Context,
	c operation.Context,
	m *operation.Mutation,
	terminal *os.File,
) (operation.Context, error) {
	if _, err := setupDependencies(ctx, c, m); err != nil {
		return c, err
	}
	files, identity, err := SourceSnapshot(c.Native.Source)
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
			return c, operation.Fail(2, "release", "Invalid native context release metadata")
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
	if _, err = initialize(ctx, c, m, terminal); err != nil {
		return c, err
	}
	c.Native.Config = filepath.Join(c.Paths.Config, "machine.toml")
	return c, nil
}
