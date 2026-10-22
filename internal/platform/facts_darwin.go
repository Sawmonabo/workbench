package platform

import (
	"path/filepath"
	"sync"
)

// System is this system's Go name, used in messages and release asset names.
const System = "darwin"

// WSL reports whether Workbench runs inside Windows Subsystem for Linux, which
// it never does on macOS.
var WSL = sync.OnceValue(func() bool { return false })

// PackageManager is the system package manager the Linux scripts use: "apt"
// when apt-get is on PATH, "dnf" when dnf is and apt-get is not, otherwise
// empty. It is looked up once, at the first call.
var PackageManager = sync.OnceValue(func() string { return "" })

// RuntimeFolders are the default config, data, state, cache and bin folders,
// in that order, before any WORKBENCH_*_DIR or XDG_*_HOME override. On macOS
// the first three sit under Application Support and the cache under Caches.
func RuntimeFolders(home string) [5]string {
	base := filepath.Join(home, "Library", "Application Support", "workbench")
	return [5]string{
		filepath.Join(base, "config"),
		filepath.Join(base, "data"),
		filepath.Join(base, "state"),
		filepath.Join(home, "Library", "Caches", "workbench"),
		filepath.Join(home, ".local", "bin"),
	}
}

// HomebrewPrefixes are Homebrew's default prefixes, Apple silicon's first.
var HomebrewPrefixes = []string{"/opt/homebrew", "/usr/local"}

// HomebrewRepositories are the Git checkouts Homebrew installs itself as at
// its default prefixes.
var HomebrewRepositories = []string{"/opt/homebrew", "/usr/local/Homebrew"}

// HasCasks reports whether Homebrew casks, and the Mac apps they install,
// exist here.
const HasCasks = true

// ToolFolders are the folders searched for an agent's launcher, in the order
// the managed shell startup files put them on PATH: bun's globals, nvm (the
// bin folder of nvm's default Node, left out when empty), Cargo's and Go's bin
// folders, ~/.local/bin, then Homebrew's and the system's folders. Each call
// returns a new slice.
func ToolFolders(home, nvm string) []string {
	folders := []string{filepath.Join(home, ".bun", "bin")}
	if nvm != "" {
		folders = append(folders, nvm)
	}
	return append(
		folders,
		filepath.Join(home, ".cargo", "bin"),
		"/usr/local/go/bin",
		filepath.Join(home, "go", "bin"),
		filepath.Join(home, ".local", "bin"),
		"/opt/homebrew/bin",
		"/usr/local/bin",
		"/usr/bin",
		"/bin",
	)
}

// VSCodeUserFolder is VS Code's user settings folder.
func VSCodeUserFolder(home string) string {
	return filepath.Join(home, "Library", "Application Support", "Code", "User")
}

// ClaudeManagedFolder is the folder that holds Claude Code's managed settings.
const ClaudeManagedFolder = "/Library/Application Support/ClaudeCode"

// GroupFromFolder reports whether a new file always takes its folder's group,
// as on macOS, rather than the creator's effective group unless the folder is
// set-group-ID.
const GroupFromFolder = true

// TouchIDPossible reports whether sudo can be set up to ask for Touch ID here.
const TouchIDPossible = true

// AssetTriple is the operating-system part of a Rust-style release asset name,
// such as uv's.
const AssetTriple = "apple-darwin"
