package machine

import (
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/Sawmonabo/workbench/internal/migrate"
	"github.com/Sawmonabo/workbench/internal/operation"
	"github.com/Sawmonabo/workbench/internal/platform"
)

// scriptEnvironment keeps privately selected tools ahead of package-manager PATH
// edits. A tool in a system folder (python3 in /usr/bin) is found there at the
// end of PATH and puts no system folder ahead: the scripts put the user's tool
// folders (~/.local/bin, ~/.cargo/bin) ahead of the system ones, and a system
// go, cargo or uv must not hide the one an earlier step installed. Only the
// apply owner may add individually approved effect selectors.
// Native and the scripts keep their temporary files in scratch, the run's
// private folder, which is also where native stages its atomic writes (see
// operation.CheckPreservable). bin, when not "", comes first on PATH: the
// plan's own folder of the qualified python3 and this workbench, which the
// modify scripts run (see stageNative).
func scriptEnvironment(
	c operation.Context,
	dependencies []operation.Dependency,
	scratch, bin string,
) []string {
	directories := []string{}
	environment := []string{
		"HOME=" + c.Home,
		"LANG=C.UTF-8",
		"PYTHONDONTWRITEBYTECODE=1",
		"TMPDIR=" + scratch,
	}
	system := []string{"/usr/bin", "/bin", "/usr/sbin", "/sbin"}
	for _, selected := range dependencies {
		directory := filepath.Dir(selected.Path)
		if !slices.Contains(directories, directory) && !slices.Contains(system, directory) {
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
	if platform.WSL() {
		environment = append(environment, wslInteropEnvironment()...)
		system = append(system, windowsSystemDirectories(os.Getenv("PATH"))...)
	}
	// The scripts download packages and tools, so they need the user's proxy
	// and CA settings; nothing else of the caller's environment reaches them.
	environment = append(environment, operation.NetworkEnvironment()...)
	// The migrations and agent-costs-ingest scripts run the installed workbench in
	// the bin folder this apply resolved, which must find the runtime folders
	// it resolved and, to find the files earlier releases left, the owner's
	// old-location variables.
	// Those reach the scripts as WORKBENCH_OLD_<NAME>, so no installer sees
	// them, with their names in WORKBENCH_OLD_NAMES; home/.chezmoitemplates/
	// old-locations.sh hands them back.
	environment = append(
		environment,
		"WORKBENCH_BIN_DIR="+c.Paths.Bin,
		"WORKBENCH_CONFIG_DIR="+c.Paths.Config,
		"WORKBENCH_DATA_DIR="+c.Paths.Data,
		"WORKBENCH_STATE_DIR="+c.Paths.State,
		"WORKBENCH_CACHE_DIR="+c.Paths.Cache,
		"WORKBENCH_MIGRATIONS="+strings.Join(migrate.IDs(), " "),
		"WORKBENCH_OLD_NAMES="+strings.Join(migrate.OldLocationVariables, " "),
	)
	for _, name := range migrate.OldLocationVariables {
		if value := os.Getenv(name); value != "" {
			environment = append(environment, "WORKBENCH_OLD_"+name+"="+value)
		}
	}
	return append(
		environment,
		"WORKBENCH_TOOL_PATH="+path,
		// An empty part would make PATH name the current folder.
		"PATH="+strings.Join(
			slices.DeleteFunc(
				[]string{bin, path, strings.Join(system, ":")},
				func(part string) bool { return part == "" },
			),
			":",
		),
	)
}

// wslInteropEnvironment is NAME=value for each WSL variable that is set and
// that a Windows call through interop needs: cmd.exe, wsl.exe and
// powershell.exe reach Windows through WSL_INTEROP and need the name of the
// distribution they run in.
func wslInteropEnvironment() []string {
	var environment []string
	for _, name := range []string{"WSL_DISTRO_NAME", "WSL_INTEROP"} {
		if value := os.Getenv(name); value != "" {
			environment = append(environment, name+"="+value)
		}
	}
	return environment
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
