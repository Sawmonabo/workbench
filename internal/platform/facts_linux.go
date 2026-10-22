package platform

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
)

// System is this system's Go name, used in messages and release asset names.
const System = "linux"

// WSL reports whether Workbench runs inside Windows Subsystem for Linux: the
// kernel release names Microsoft. The file is read once, at the first call, and
// an unreadable one means not WSL.
var WSL = sync.OnceValue(func() bool {
	data, _ := os.ReadFile("/proc/sys/kernel/osrelease")
	return strings.Contains(strings.ToLower(string(data)), "microsoft")
})

// PackageManager is the system package manager the Linux scripts use: "apt"
// when apt-get is on PATH, "dnf" when dnf is and apt-get is not, otherwise
// empty. It is looked up once, at the first call.
var PackageManager = sync.OnceValue(func() string {
	if _, err := exec.LookPath("apt-get"); err == nil {
		return "apt"
	}
	if _, err := exec.LookPath("dnf"); err == nil {
		return "dnf"
	}
	return ""
})

// RuntimeFolders are the default config, data, state, cache and bin folders,
// in that order, before any WORKBENCH_*_DIR or XDG_*_HOME override: the XDG
// base directory defaults.
func RuntimeFolders(home string) [5]string {
	return [5]string{
		filepath.Join(home, ".config", "workbench"),
		filepath.Join(home, ".local", "share", "workbench"),
		filepath.Join(home, ".local", "state", "workbench"),
		filepath.Join(home, ".cache", "workbench"),
		filepath.Join(home, ".local", "bin"),
	}
}

// HomebrewPrefixes are Homebrew's default prefixes on Linux.
var HomebrewPrefixes = []string{"/home/linuxbrew/.linuxbrew"}

// HomebrewRepositories are the Git checkouts Homebrew installs itself as at
// its default prefixes.
var HomebrewRepositories = []string{"/home/linuxbrew/.linuxbrew/Homebrew"}

// HasCasks reports whether Homebrew casks, and the Mac apps they install,
// exist here.
const HasCasks = false

// ToolFolders are the folders searched for an agent's launcher, in the order
// the managed shell startup files put them on PATH: ~/bin, bun's globals, nvm
// (the bin folder of nvm's default Node, left out when empty), Cargo's and
// Go's bin folders, ~/.local/bin, then Homebrew's and the system's folders.
// Each call returns a new slice.
func ToolFolders(home, nvm string) []string {
	folders := []string{filepath.Join(home, "bin"), filepath.Join(home, ".bun", "bin")}
	if nvm != "" {
		folders = append(folders, nvm)
	}
	return append(
		folders,
		filepath.Join(home, ".cargo", "bin"),
		"/usr/local/go/bin",
		filepath.Join(home, "go", "bin"),
		filepath.Join(home, ".local", "bin"),
		"/home/linuxbrew/.linuxbrew/bin",
		"/usr/local/bin",
		"/usr/bin",
		"/bin",
	)
}

// VSCodeUserFolder is VS Code's user settings folder.
func VSCodeUserFolder(home string) string {
	return filepath.Join(home, ".config", "Code", "User")
}

// ClaudeManagedFolder is the folder that holds Claude Code's managed settings.
const ClaudeManagedFolder = "/etc/claude-code"

// GroupFromFolder reports whether a new file always takes its folder's group,
// as on macOS, rather than the creator's effective group unless the folder is
// set-group-ID.
const GroupFromFolder = false

// TouchIDPossible reports whether sudo can be set up to ask for Touch ID here.
const TouchIDPossible = false

// AssetTriple is the operating-system part of a Rust-style release asset name,
// such as uv's.
const AssetTriple = "unknown-linux-gnu"
