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
func ResolveDependencies(ctx context.Context, c operation.Context, recorded []operation.Dependency, searchPath string) ([]operation.Dependency, []operation.Component) {
	var dependencies []operation.Dependency
	var results []operation.Component
	requirements := ManagementRequirements()
	for _, name := range []string{"chezmoi", "python3", "uv"} {
		var candidates []operation.Dependency
		for _, prior := range recorded {
			if prior.Name == name {
				candidates = append(candidates, prior)
			}
		}
		path, err := operation.FindExecutable(name, searchPath, []string{c.Native.Source, projectRoot(c)})
		if err == nil {
			owner := "user"
			if strings.Contains(path, "/Cellar/") {
				owner = "homebrew"
			}
			if strings.HasPrefix(path, "/usr/bin/") {
				owner = "system"
			}
			candidates = append(candidates, operation.Dependency{Name: name, Path: path, Owner: owner})
		}
		component := operation.Component{Name: name, Status: "blocked", Message: "Missing qualified tool; approved setup is required"}
		for _, candidate := range candidates {
			args := []string{"--version"}
			environment := []string{"PATH=/usr/bin:/bin", "PYTHONDONTWRITEBYTECODE=1", "HOME=" + c.Home}
			cleanup := func() {}
			if name == "chezmoi" {
				scratch, scratchErr := os.MkdirTemp("", "workbench-version-")
				if scratchErr != nil {
					component.Message = "Cannot create private version-probe scratch"
					continue
				}
				cleanup = func() { _ = os.RemoveAll(scratch) }
				if scratchErr = os.WriteFile(filepath.Join(scratch, "config.toml"), nil, 0600); scratchErr != nil {
					cleanup()
					continue
				}
				native := operation.NativeContext{Source: scratch, Config: filepath.Join(scratch, "config.toml"), Destination: scratch, PersistentState: filepath.Join(scratch, "state.boltdb"), Cache: filepath.Join(scratch, "cache")}
				args, _ = native.Args()
				args = append(args, "--version")
				environment = []string{"PATH=/usr/bin:/bin", "HOME=" + scratch}
			}
			if name == "python3" {
				args = []string{"-I", "-c", "import sys,tomllib; print('.'.join(map(str,sys.version_info[:3])))"}
			}
			output, runErr := operation.Run(ctx, c, nil, operation.Process{Executable: candidate.Path, Args: args, Directory: "/", Environment: environment, Timeout: 10 * time.Second, OutputLimit: 4096})
			cleanup()
			if runErr != nil {
				component.Message = "Version/capability probe failed; existing tool retained"
				continue
			}
			version := toolVersion.FindString(output.Stdout)
			qualified := false
			switch name {
			case "chezmoi":
				qualified = version == requirements.Chezmoi
				candidate.Capabilities = []string{"builtin-git", "builtin-diff", "native-targets"}
			case "uv":
				qualified = version == requirements.UV || slices.Contains(requirements.UVAdditional, version)
				candidate.Capabilities = []string{"private-python"}
			case "python3":
				parts := strings.Split(version, ".")
				if len(parts) == 3 {
					minor, _ := strconv.Atoi(parts[1])
					qualified = parts[0] == "3" && minor >= requirements.PythonMinMinor && minor <= requirements.PythonMaxMinor
				}
				candidate.Capabilities = []string{"tomllib"}
			}
			candidate.Version = version
			if !qualified {
				component.Message = "Installed version is unqualified; retained without replacement"
				continue
			}
			dependencies = append(dependencies, candidate)
			component.Status, component.Message, component.Details = "complete", "Qualified existing installation", candidate
			break
		}
		results = append(results, component)
	}
	return dependencies, results
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

// ScriptEnvironment keeps privately selected tools ahead of package-manager PATH
// edits. Only the apply owner may add individually approved effect selectors.
func ScriptEnvironment(c operation.Context, dependencies []operation.Dependency) []string {
	directories := []string{}
	environment := []string{"HOME=" + c.Home, "LANG=C.UTF-8", "PYTHONDONTWRITEBYTECODE=1"}
	for _, selected := range dependencies {
		directory := filepath.Dir(selected.Path)
		if !slices.Contains(directories, directory) {
			directories = append(directories, directory)
		}
		if selected.Name == "chezmoi" || selected.Name == "uv" {
			environment = append(environment, "WORKBENCH_"+strings.ToUpper(selected.Name)+"="+selected.Path)
		}
	}
	path := strings.Join(directories, string(os.PathListSeparator))
	system := "/usr/bin:/bin:/usr/sbin:/sbin"
	if IsWSL() {
		// Windows host steps run cmd.exe, wsl.exe and powershell.exe through
		// interop and need the name of the distribution they run in.
		for _, name := range []string{"WSL_DISTRO_NAME", "WSL_INTEROP"} {
			if value := os.Getenv(name); value != "" {
				environment = append(environment, name+"="+value)
			}
		}
		for _, directory := range windowsSystemDirectories(os.Getenv("PATH")) {
			system += ":" + directory
		}
	}
	return append(environment, "WORKBENCH_TOOL_PATH="+path, "PATH="+path+":"+system)
}

// windowsSystemDirectories keeps only the Windows system directories from the
// WSL PATH, where cmd.exe, wsl.exe and powershell.exe live.
func windowsSystemDirectories(searchPath string) []string {
	var directories []string
	for _, entry := range filepath.SplitList(searchPath) {
		lower := strings.ToLower(strings.TrimSuffix(entry, "/"))
		system := strings.HasSuffix(lower, "/windows/system32") || strings.HasSuffix(lower, "/windows/system32/windowspowershell/v1.0")
		if strings.HasPrefix(lower, "/mnt/") && system && !slices.Contains(directories, entry) {
			directories = append(directories, entry)
		}
	}
	return directories
}

func Platform(ctx context.Context, c operation.Context) operation.Component {
	result := operation.Component{Name: "platform", Status: "blocked", Message: runtime.GOOS + "/" + runtime.GOARCH}
	if runtime.GOARCH != "amd64" && runtime.GOARCH != "arm64" {
		return result
	}
	switch runtime.GOOS {
	case "darwin":
		output, err := operation.Run(ctx, c, nil, operation.Process{Executable: "/usr/bin/sw_vers", Args: []string{"-productVersion"}, Directory: "/", Environment: []string{"PATH=/usr/bin:/bin"}, Timeout: 10 * time.Second})
		if err != nil {
			return result
		}
		major, _ := strconv.Atoi(strings.Split(strings.TrimSpace(output.Stdout), ".")[0])
		result.Message = fmt.Sprintf("macOS %s/%s; native provisioning qualification still required", strings.TrimSpace(output.Stdout), runtime.GOARCH)
		if major >= 15 {
			result.Status = "complete"
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
		if values["ID"] == "ubuntu" && slices.Contains([]string{"22.04", "24.04", "26.04"}, values["VERSION_ID"]) {
			result.Status = "complete"
		}
		result.Message = values["ID"] + " " + values["VERSION_ID"] + "/" + runtime.GOARCH
		if IsWSL() {
			result.Message += "; WSL: Windows host steps are not yet qualified on a real host"
		}
	}
	return result
}

func IsWSL() bool {
	data, _ := os.ReadFile("/proc/sys/kernel/osrelease")
	return runtime.GOOS == "linux" && strings.Contains(strings.ToLower(string(data)), "microsoft")
}

// Doctor probes management versions but does not launch VS Code's CLI: that CLI
// may create logs/user state. Host/profile discovery reads the actual locations.
func Doctor(ctx context.Context, c operation.Context) ([]operation.Component, error) {
	state, err := operation.ReadState(c.Paths)
	if err != nil {
		return nil, err
	}
	var recorded []operation.Dependency
	if state != nil {
		recorded = state.Dependencies
	}
	_, results := ResolveDependencies(ctx, c, recorded, os.Getenv("PATH"))
	results = append([]operation.Component{Platform(ctx, c)}, results...)
	// Plain chezmoi still pointed at another source, such as dotfiles, would
	// reapply it over the same files Workbench manages.
	if raw, readErr := os.ReadFile(filepath.Join(c.Home, ".config", "chezmoi", "chezmoi.toml")); readErr == nil {
		var native struct {
			SourceDir string `toml:"sourceDir"`
		}
		if toml.Unmarshal(raw, &native) == nil && native.SourceDir != "" {
			results = append(results, operation.Component{Name: "native-chezmoi", Status: "conflict", Message: "~/.config/chezmoi/chezmoi.toml points plain chezmoi at " + native.SourceDir + "; move it aside after switching (docs/switch-from-dotfiles.md)"})
		}
	}
	root := filepath.Join(c.Home, ".config", "Code", "User")
	if runtime.GOOS == "darwin" {
		root = filepath.Join(c.Home, "Library", "Application Support", "Code", "User")
	}
	for _, target := range []struct{ name, path string }{{"editor-local", root}, {"editor-remote-wsl", filepath.Join(c.Home, ".vscode-server", "data", "Machine")}} {
		component := operation.Component{Name: target.name, Status: "skipped", Message: "No existing host settings directory"}
		if info, statErr := os.Lstat(target.path); statErr == nil && info.IsDir() {
			component.Status = "complete"
			component.Message = target.path + " (profile ownership requires apply preflight)"
		}
		results = append(results, component)
	}
	for _, result := range results {
		if result.Status == "blocked" {
			return results, operation.Fail(3, "dependency", "Doctor found missing or unqualified prerequisites; no repair performed")
		}
	}
	return results, nil
}
