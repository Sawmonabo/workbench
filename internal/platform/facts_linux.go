package platform

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// system is Linux's Go name.
const system = "linux"

// wsl reports whether the kernel release names Microsoft. An unreadable
// /proc/sys/kernel/osrelease means not WSL.
func wsl() bool {
	data, _ := os.ReadFile("/proc/sys/kernel/osrelease")
	return strings.Contains(strings.ToLower(string(data)), "microsoft")
}

// packageManager looks for apt-get, then dnf, on PATH.
func packageManager() string {
	if _, err := exec.LookPath("apt-get"); err == nil {
		return "apt"
	}
	if _, err := exec.LookPath("dnf"); err == nil {
		return "dnf"
	}
	return ""
}

// runtimeFolders are the XDG base directory defaults.
func runtimeFolders(home string) [5]string {
	return [5]string{
		filepath.Join(home, ".config", "workbench"),
		filepath.Join(home, ".local", "share", "workbench"),
		filepath.Join(home, ".local", "state", "workbench"),
		filepath.Join(home, ".cache", "workbench"),
		filepath.Join(home, ".local", "bin"),
	}
}

// homebrewPrefixes are Homebrew's default prefixes on Linux.
var homebrewPrefixes = []string{"/home/linuxbrew/.linuxbrew"}

// homebrewRepositories are the Git checkouts Homebrew installs itself as at
// its default prefixes.
var homebrewRepositories = []string{"/home/linuxbrew/.linuxbrew/Homebrew"}

// hasCasks is false: Homebrew casks and Mac apps do not exist on Linux.
const hasCasks = false

// goBunFromHomebrew is false: Go and bun do not come from Homebrew here.
const goBunFromHomebrew = false

// toolFolders are ~/bin, bun's globals, nvm (the bin folder of nvm's default
// Node, left out when empty), Cargo's bin folder, ~/.local/bin, Go's bin
// folders, then Homebrew's and the system's folders.
func toolFolders(home, nvm string) []string {
	folders := []string{filepath.Join(home, "bin"), filepath.Join(home, ".bun", "bin")}
	if nvm != "" {
		folders = append(folders, nvm)
	}
	return append(
		folders,
		filepath.Join(home, ".cargo", "bin"),
		filepath.Join(home, ".local", "bin"),
		"/usr/local/go/bin",
		filepath.Join(home, "go", "bin"),
		"/home/linuxbrew/.linuxbrew/bin",
		"/usr/local/bin",
		"/usr/bin",
		"/bin",
	)
}

// vsCodeUserFolder is VS Code's user settings folder under ~/.config.
func vsCodeUserFolder(home string) string {
	return filepath.Join(home, ".config", "Code", "User")
}

// claudeManagedFolder is the folder that holds Claude Code's managed settings.
const claudeManagedFolder = "/etc/claude-code"

// groupFromFolder is false: on Linux a new file takes the creator's effective
// group unless the folder is set-group-ID.
const groupFromFolder = false

// aclsAreAttributes is true: Linux keeps POSIX ACLs as the extended
// attributes system.posix_acl_access and system.posix_acl_default.
const aclsAreAttributes = true

// labelAttribute is SELinux's: a file's context is its security.selinux
// attribute.
const labelAttribute = "security.selinux"

// touchIDPossible is false: sudo cannot be set up to ask for Touch ID here.
const touchIDPossible = false

// readsPasswords is false: Workbench asks for no password on Linux or WSL,
// so readPassword is not implemented here.
const readsPasswords = false

// knowsOriginalParent is false: Linux keeps no record of the process that
// forked one once that parent exits and another adopts the child.
const knowsOriginalParent = false

// followsForks is false: Linux has no unprivileged way to learn of another
// process's forks that works in every namespace.
const followsForks = false

// assetTriple is Linux's part of a Rust-style release asset name.
const assetTriple = "unknown-linux-gnu"
