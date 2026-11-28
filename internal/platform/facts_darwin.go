package platform

import "path/filepath"

// system is macOS's Go name.
const system = "darwin"

// wsl is false: Workbench never runs inside WSL on macOS.
func wsl() bool { return false }

// packageManager is empty: the Linux scripts do not run on macOS.
func packageManager() string { return "" }

// runtimeBase is Workbench's folder in Application Support.
func runtimeBase(home string) string {
	return filepath.Join(home, "Library", "Application Support", "workbench")
}

// runtimeFolders puts the config, data and state folders in runtimeBase and
// the cache under Caches.
func runtimeFolders(home string) [5]string {
	base := runtimeBase(home)
	return [5]string{
		filepath.Join(base, "config"),
		filepath.Join(base, "data"),
		filepath.Join(base, "state"),
		filepath.Join(home, "Library", "Caches", "workbench"),
		filepath.Join(home, ".local", "bin"),
	}
}

// homebrewPrefixes are Homebrew's default prefixes, Apple silicon's first.
var homebrewPrefixes = []string{"/opt/homebrew", "/usr/local"}

// homebrewRepositories are the Git checkouts Homebrew installs itself as at
// its default prefixes.
var homebrewRepositories = []string{"/opt/homebrew", "/usr/local/Homebrew"}

// hasCasks is true: Homebrew casks and the Mac apps they install exist here.
const hasCasks = true

// goBunFromHomebrew is true: Go and bun come from Homebrew here.
const goBunFromHomebrew = true

// toolFolders are bun's globals, nvm (the bin folder of nvm's default Node,
// left out when empty), Cargo's bin folder, ~/.local/bin, Go's bin folders,
// then Homebrew's and the system's folders.
func toolFolders(home, nvm string) []string {
	folders := []string{filepath.Join(home, ".bun", "bin")}
	if nvm != "" {
		folders = append(folders, nvm)
	}
	return append(
		folders,
		filepath.Join(home, ".cargo", "bin"),
		filepath.Join(home, ".local", "bin"),
		"/usr/local/go/bin",
		filepath.Join(home, "go", "bin"),
		"/opt/homebrew/bin",
		"/usr/local/bin",
		"/usr/bin",
		"/bin",
	)
}

// vsCodeUserFolder is VS Code's user settings folder under Application
// Support.
func vsCodeUserFolder(home string) string {
	return filepath.Join(home, "Library", "Application Support", "Code", "User")
}

// claudeManagedFolder is the folder that holds Claude Code's managed settings.
const claudeManagedFolder = "/Library/Application Support/ClaudeCode"

// groupFromFolder is true: on macOS a new file always takes its folder's
// group.
const groupFromFolder = true

// folderTakesSetgid is false: a folder made in a set-group-ID folder is not
// set-group-ID, as mkdir in one shows.
const folderTakesSetgid = false

// aclsAreAttributes is false: macOS keeps a file's ACL apart from its
// extended attributes, and FileSecurity reads it.
const aclsAreAttributes = false

// accessACLAttribute is empty: macOS keeps ACLs apart from the attributes.
const accessACLAttribute = ""

// defaultACLAttribute is empty: macOS marks an ACL entry inherited instead.
const defaultACLAttribute = ""

// folderAttributeInert is false: a macOS folder's attributes are not read
// for its ACL.
func folderAttributeInert(string) bool { return false }

// labelAttribute is empty: no macOS attribute holds a security label.
const labelAttribute = ""

// capabilityAttribute is empty: macOS has no file capabilities.
const capabilityAttribute = ""

// touchIDPossible is true: sudo can be set up to ask for Touch ID here.
const touchIDPossible = true

// readsPasswords is true: readPassword reads a password at a terminal with
// echo off.
const readsPasswords = true

// knowsOriginalParent is true: macOS's kernel records the process that forked
// one even after that parent exited (see processLineage).
const knowsOriginalParent = true

// followsForks is true: macOS's kqueue can wake a Wait when any process the
// caller can see forks or execs.
const followsForks = true

// assetTriple is macOS's part of a Rust-style release asset name.
const assetTriple = "apple-darwin"
