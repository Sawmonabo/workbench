package machine

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/Sawmonabo/workbench/internal/operation"
	"github.com/pelletier/go-toml/v2"
)

var toolVersion = regexp.MustCompile(`[0-9]+\.[0-9]+\.[0-9]+`)

// ResolveDependencies never installs or repairs. Recorded compatible owners win;
// incompatible borrowed tools are retained and reported, never replaced here.
func ResolveDependencies(
	ctx context.Context,
	c operation.Context,
	requirements Requirements,
	recorded []operation.Dependency,
	searchPath string,
) ([]operation.Dependency, []operation.Component) {
	var dependencies []operation.Dependency
	var results []operation.Component
	for _, name := range []string{"chezmoi", "python3", "uv"} {
		var candidates []operation.Dependency
		for _, prior := range recorded {
			if prior.Name == name {
				candidates = append(candidates, prior)
			}
		}
		path, err := operation.FindExecutable(
			name,
			searchPath,
			[]string{c.Native.Source, projectRoot(c)},
		)
		if err == nil {
			owner := "user"
			if strings.Contains(path, "/Cellar/") {
				owner = "homebrew"
			}
			if strings.HasPrefix(path, "/usr/bin/") {
				owner = "system"
			}
			candidates = append(
				candidates,
				operation.Dependency{Name: name, Path: path, Owner: owner},
			)
		}
		component := operation.Component{
			Name:    name,
			Status:  operation.StatusBlocked,
			Message: "Missing qualified tool; approved setup is required",
		}
		for _, candidate := range candidates {
			version, err := probeVersion(ctx, c, name, candidate.Path)
			if err != nil {
				component.Message = err.Error()
				continue
			}
			qualified, capabilities := qualify(name, version, requirements)
			candidate.Capabilities = capabilities
			candidate.Version = version
			if !qualified {
				component.Message = fmt.Sprintf(
					"%s %s at %s is not the qualified %s; it is left alone, and approved setup installs a private copy",
					name,
					version,
					candidate.Path,
					qualifiedVersions(name, requirements),
				)
				continue
			}
			dependencies = append(dependencies, candidate)
			component.Status = operation.StatusComplete
			component.Message = version + " (" + candidate.Owner + ") at " + candidate.Path
			break
		}
		results = append(results, component)
	}
	return dependencies, results
}

// probeVersion runs a bounded version probe of the tool at path and returns
// the version it reports. chezmoi runs against an empty private scratch
// context so it never reads the user's configuration.
func probeVersion(ctx context.Context, c operation.Context, name, path string) (string, error) {
	args := []string{"--version"}
	environment := []string{"PATH=/usr/bin:/bin", "PYTHONDONTWRITEBYTECODE=1", "HOME=" + c.Home}
	switch name {
	case "chezmoi":
		scratch, err := os.MkdirTemp("", "workbench-version-")
		if err != nil {
			return "", operation.Fail(
				operation.ExitFailed,
				"dependency",
				"Cannot create private version-probe scratch",
			)
		}
		defer func() { _ = os.RemoveAll(scratch) }()
		config := filepath.Join(scratch, "config.toml")
		if err = os.WriteFile(config, nil, 0o600); err != nil {
			return "", operation.Fail(
				operation.ExitFailed,
				"dependency",
				"Cannot create private version-probe scratch",
			)
		}
		native := operation.NativeContext{
			Source:          scratch,
			Config:          config,
			Destination:     scratch,
			PersistentState: filepath.Join(scratch, "state.boltdb"),
			Cache:           filepath.Join(scratch, "cache"),
		}
		args, _ = native.Args()
		args = append(args, "--version")
		environment = []string{"PATH=/usr/bin:/bin", "HOME=" + scratch}
	case "python3":
		args = []string{
			"-I",
			"-c",
			"import sys,tomllib; print('.'.join(map(str,sys.version_info[:3])))",
		}
	}
	output, err := operation.Run(ctx, c, nil, operation.Process{
		Executable:  path,
		Args:        args,
		Directory:   "/",
		Environment: environment,
		Timeout:     10 * time.Second,
		OutputLimit: 4096,
	})
	if err != nil {
		return "", operation.Fail(
			operation.ExitFailed,
			"dependency",
			"Version/capability probe failed; existing tool retained",
		)
	}
	return toolVersion.FindString(output.Stdout), nil
}

// qualify reports whether version is qualified for tool name and returns the
// capabilities Workbench relies on from it.
func qualify(name, version string, requirements Requirements) (bool, []string) {
	switch name {
	case "chezmoi":
		return version == requirements.Chezmoi, []string{
			"builtin-git",
			"builtin-diff",
			"native-targets",
		}
	case "uv":
		qualified := version == requirements.UV ||
			slices.Contains(requirements.UVAdditional, version)
		return qualified, []string{"private-python"}
	case "python3":
		parts := strings.Split(version, ".")
		if len(parts) != 3 {
			return false, []string{"tomllib"}
		}
		minor, _ := strconv.Atoi(parts[1])
		qualified := parts[0] == "3" && minor >= requirements.PythonMinMinor &&
			minor <= requirements.PythonMaxMinor
		return qualified, []string{"tomllib"}
	}
	return false, nil
}

// qualifiedVersions describes the versions qualify accepts for tool name.
func qualifiedVersions(name string, requirements Requirements) string {
	switch name {
	case "uv":
		versions := []string{requirements.UV}
		for _, version := range requirements.UVAdditional {
			if !slices.Contains(versions, version) {
				versions = append(versions, version)
			}
		}
		return strings.Join(versions, " or ")
	case "python3":
		return fmt.Sprintf("3.%d–3.%d", requirements.PythonMinMinor, requirements.PythonMaxMinor)
	}
	return requirements.Chezmoi
}

func projectRoot(c operation.Context) string {
	if c.Scope.Kind == "project" {
		return c.Scope.Root
	}
	return ""
}

func dependency(dependencies []operation.Dependency, name string) string {
	for _, dependency := range dependencies {
		if dependency.Name == name {
			return dependency.Path
		}
	}
	return ""
}

// scriptEnvironment keeps privately selected tools ahead of package-manager PATH
// edits. Only the apply owner may add individually approved effect selectors.
func scriptEnvironment(c operation.Context, dependencies []operation.Dependency) []string {
	directories := []string{}
	environment := []string{"HOME=" + c.Home, "LANG=C.UTF-8", "PYTHONDONTWRITEBYTECODE=1"}
	for _, selected := range dependencies {
		directory := filepath.Dir(selected.Path)
		if !slices.Contains(directories, directory) {
			directories = append(directories, directory)
		}
		if selected.Name == "chezmoi" || selected.Name == "uv" {
			environment = append(
				environment,
				"WORKBENCH_"+strings.ToUpper(selected.Name)+"="+selected.Path,
			)
		}
	}
	path := strings.Join(directories, string(os.PathListSeparator))
	system := []string{"/usr/bin", "/bin", "/usr/sbin", "/sbin"}
	if isWSL() {
		// Windows host steps run cmd.exe, wsl.exe and powershell.exe through
		// interop and need the name of the distribution they run in.
		for _, name := range []string{"WSL_DISTRO_NAME", "WSL_INTEROP"} {
			if value := os.Getenv(name); value != "" {
				environment = append(environment, name+"="+value)
			}
		}
		system = append(system, windowsSystemDirectories(os.Getenv("PATH"))...)
	}
	// The scripts download packages and tools, so they need the user's proxy
	// and CA settings; nothing else of the caller's environment reaches them.
	environment = append(environment, operation.NetworkEnvironment()...)
	return append(
		environment,
		"WORKBENCH_TOOL_PATH="+path,
		"PATH="+path+":"+strings.Join(system, ":"),
	)
}

// windowsSystemDirectories keeps only the Windows system directories from the
// WSL PATH, where cmd.exe, wsl.exe and powershell.exe live.
func windowsSystemDirectories(searchPath string) []string {
	var directories []string
	for _, entry := range filepath.SplitList(searchPath) {
		lower := strings.ToLower(strings.TrimSuffix(entry, "/"))
		system := strings.HasSuffix(lower, "/windows/system32") ||
			strings.HasSuffix(lower, "/windows/system32/windowspowershell/v1.0")
		if strings.HasPrefix(lower, "/mnt/") && system && !slices.Contains(directories, entry) {
			directories = append(directories, entry)
		}
	}
	return directories
}

func checkPlatform(ctx context.Context, c operation.Context) operation.Component {
	result := operation.Component{
		Name:    "platform",
		Status:  operation.StatusBlocked,
		Message: runtime.GOOS + "/" + runtime.GOARCH,
	}
	if runtime.GOARCH != "amd64" && runtime.GOARCH != "arm64" {
		return result
	}
	switch runtime.GOOS {
	case "darwin":
		output, err := operation.Run(
			ctx,
			c,
			nil,
			operation.Process{
				Executable:  "/usr/bin/sw_vers",
				Args:        []string{"-productVersion"},
				Directory:   "/",
				Environment: []string{"PATH=/usr/bin:/bin"},
				Timeout:     10 * time.Second,
			},
		)
		if err != nil {
			return result
		}
		major, _ := strconv.Atoi(strings.Split(strings.TrimSpace(output.Stdout), ".")[0])
		result.Message = fmt.Sprintf(
			"macOS %s/%s; native provisioning qualification still required",
			strings.TrimSpace(output.Stdout),
			runtime.GOARCH,
		)
		if major >= 15 {
			result.Status = operation.StatusComplete
		}
	case "linux":
		data, err := os.ReadFile("/etc/os-release")
		if err != nil {
			return result
		}
		values := map[string]string{}
		for line := range strings.SplitSeq(string(data), "\n") {
			key, value, ok := strings.Cut(line, "=")
			if ok {
				values[key] = strings.Trim(value, `"`)
			}
		}
		if values["ID"] == "ubuntu" &&
			slices.Contains([]string{"22.04", "24.04", "26.04"}, values["VERSION_ID"]) {
			result.Status = operation.StatusComplete
		}
		result.Message = values["ID"] + " " + values["VERSION_ID"] + "/" + runtime.GOARCH
		if isWSL() {
			result.Message += "; WSL: Windows host steps are not yet qualified on a real host"
		}
	}
	return result
}

func isWSL() bool {
	data, _ := os.ReadFile("/proc/sys/kernel/osrelease")
	return runtime.GOOS == "linux" && strings.Contains(strings.ToLower(string(data)), "microsoft")
}

// Doctor reports what Workbench has installed and applied, then probes
// management versions and the host. It does not launch VS Code's CLI: that CLI
// may create logs/user state. Host/profile discovery reads the actual locations.
func Doctor(ctx context.Context, c operation.Context) ([]operation.Component, error) {
	state, err := operation.ReadState(c.Paths)
	if err != nil {
		return nil, err
	}
	if state == nil {
		state = &operation.State{}
	}
	recorded := state.Dependencies
	results := installed(state)
	selection, selectionErr := ReadSelection(c.Native.Config)
	effects := operation.Component{
		Name:    "effects",
		Status:  operation.StatusUnchanged,
		Message: "none skipped",
	}
	switch {
	case selectionErr != nil:
		effects.Status, effects.Message = operation.StatusBlocked, selectionErr.Error()
	case len(selection.Skip) > 0 || len(selection.Select) > 0:
		effects.Status = operation.StatusComplete
		var parts []string
		if len(selection.Skip) > 0 {
			parts = append(parts, "skipped "+strings.Join(selection.Skip, ", ")+" (saved)")
		}
		if len(selection.Select) > 0 {
			parts = append(parts, "selected "+strings.Join(selection.Select, ", ")+" (saved)")
		}
		effects.Message = strings.Join(parts, "; ")
	}
	results = append(results, effects)
	results = append(results, checkPlatform(ctx, c))
	if requirements, requirementsErr := ManagementRequirements(c); requirementsErr != nil {
		results = append(results, operation.Component{
			Name:    "tool-versions",
			Status:  operation.StatusBlocked,
			Message: requirementsErr.Error(),
		})
	} else {
		_, resolved := ResolveDependencies(ctx, c, requirements, recorded, os.Getenv("PATH"))
		results = append(results, resolved...)
	}
	// Plain chezmoi still pointed at another source, such as dotfiles, would
	// reapply it over the same files Workbench manages.
	if raw, readErr := os.ReadFile(
		filepath.Join(c.Home, ".config", "chezmoi", "chezmoi.toml"),
	); readErr == nil {
		var native struct {
			SourceDir string `toml:"sourceDir"`
		}
		if toml.Unmarshal(raw, &native) == nil && native.SourceDir != "" {
			results = append(
				results,
				operation.Component{
					Name:    "native-chezmoi",
					Status:  operation.StatusConflict,
					Message: "~/.config/chezmoi/chezmoi.toml points plain chezmoi at " + native.SourceDir + "; move it aside after switching (docs/switch-from-dotfiles.md)",
				},
			)
		}
	}
	root := filepath.Join(c.Home, ".config", "Code", "User")
	if runtime.GOOS == "darwin" {
		root = filepath.Join(c.Home, "Library", "Application Support", "Code", "User")
	}
	editors := []struct{ name, path string }{
		{"editor-local", root},
		{"editor-remote-wsl", filepath.Join(c.Home, ".vscode-server", "data", "Machine")},
	}
	for _, target := range editors {
		component := operation.Component{
			Name:    target.name,
			Status:  operation.StatusSkipped,
			Message: "No existing host settings directory",
		}
		if info, statErr := os.Lstat(target.path); statErr == nil && info.IsDir() {
			component.Status = operation.StatusComplete
			component.Message = target.path + " (profile ownership requires apply preflight)"
		}
		results = append(results, component)
	}
	for _, result := range results {
		if result.Status == operation.StatusBlocked {
			return results, operation.Fail(
				operation.ExitBlocked,
				"dependency",
				"Doctor found missing or unqualified prerequisites; no repair performed",
			)
		}
	}
	return results, nil
}

// installed reports the active release, the last applied configuration and
// any apply that did not finish, from recorded state without probing.
func installed(state *operation.State) []operation.Component {
	release := operation.Component{
		Name:    "release",
		Status:  operation.StatusAbsent,
		Message: "No release installed; install.sh installs one",
	}
	if state.ActiveRelease != nil {
		release.Status = operation.StatusComplete
		release.Message = state.ActiveRelease.Identity.Release + " is active"
	}
	applied := operation.Component{
		Name:    "applied",
		Status:  operation.StatusAbsent,
		Message: "Nothing applied yet",
	}
	if source := state.AppliedConfiguration; source != nil {
		applied.Status = operation.StatusComplete
		applied.Message = "Last applied from release " + source.Release
		if source.Release == "developer" {
			applied.Message = "Last applied from a developer checkout (" + source.ContentDigest[:12] + ")"
		}
		if state.AppliedAt != nil {
			applied.Message += " on " + state.AppliedAt.Local().Format("Jan 2, 2006")
		}
	}
	results := []operation.Component{release, applied}
	if partial := state.PartialOperation; partial != nil {
		results = append(results, operation.Component{
			Name:    "unfinished-apply",
			Status:  operation.StatusPartial,
			Message: "Apply " + partial.ID + " to " + partial.Scope.Root + " did not finish; rerun workbench apply",
		})
	}
	return results
}
