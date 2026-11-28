package platform

import "sync"

// System is this system's Go name, "darwin" or "linux", used in messages and
// release asset names.
const System = system

// WSL reports whether Workbench runs inside Windows Subsystem for Linux,
// which it never does on macOS. It is asked once, at the first call, and
// answered from memory after.
var WSL = sync.OnceValue(wsl)

// PackageManager is the system package manager the Linux scripts use: "apt"
// when apt-get is on PATH, "dnf" when dnf is and apt-get is not, otherwise
// empty, and always empty on macOS. It is looked up once, at the first call.
var PackageManager = sync.OnceValue(packageManager)

// RuntimeFolders are the default config, data, state, cache and bin folders,
// in that order, before any WORKBENCH_*_DIR or XDG_*_HOME override. On macOS
// the first three sit under Application Support and the cache under Caches;
// on Linux they are the XDG base directory defaults. The bin folder is
// ~/.local/bin on both.
func RuntimeFolders(home string) [5]string { return runtimeFolders(home) }

// HomebrewPrefixes are Homebrew's default prefixes: /opt/homebrew, Apple
// silicon's, then /usr/local on macOS, and /home/linuxbrew/.linuxbrew on
// Linux.
var HomebrewPrefixes = homebrewPrefixes

// HomebrewRepositories are the Git checkouts Homebrew installs itself as at
// its default prefixes: /opt/homebrew and /usr/local/Homebrew on macOS, and
// /home/linuxbrew/.linuxbrew/Homebrew on Linux.
var HomebrewRepositories = homebrewRepositories

// HasCasks reports whether Homebrew casks, and the Mac apps they install,
// exist here: true on macOS, false on Linux.
const HasCasks = hasCasks

// GoBunFromHomebrew reports whether Go and bun come from Homebrew here, which
// installs only its current version, so the go and bun pins do not apply:
// true on macOS, false on Linux.
const GoBunFromHomebrew = goBunFromHomebrew

// ToolFolders are the folders searched for an agent's launcher, in the order
// the managed shell startup files put them on PATH: on Linux ~/bin, then on
// both bun's globals, nvm (the bin folder of nvm's default Node, left out
// when empty), Cargo's bin folder, ~/.local/bin, Go's bin folders, then
// Homebrew's and the system's folders. Each call returns a new slice.
func ToolFolders(home, nvm string) []string { return toolFolders(home, nvm) }

// VSCodeUserFolder is VS Code's user settings folder:
// ~/Library/Application Support/Code/User on macOS and ~/.config/Code/User
// on Linux.
func VSCodeUserFolder(home string) string { return vsCodeUserFolder(home) }

// ClaudeManagedFolder is the folder that holds Claude Code's managed
// settings: /Library/Application Support/ClaudeCode on macOS and
// /etc/claude-code on Linux.
const ClaudeManagedFolder = claudeManagedFolder

// GroupFromFolder reports whether a new file always takes its folder's group,
// as on macOS, rather than the creator's effective group unless the folder is
// set-group-ID, as on Linux.
const GroupFromFolder = groupFromFolder

// ACLsAreAttributes reports whether a file's ACL is kept among its extended
// attributes, where FileSecurity does not read it: true on Linux, whose POSIX
// ACLs are the system.posix_acl_access and system.posix_acl_default
// attributes, and false on macOS, whose ACLs FileSecurity reads.
const ACLsAreAttributes = aclsAreAttributes

// AccessACLAttribute is the extended attribute that holds a file's ACL where
// ACLs are extended attributes: system.posix_acl_access on Linux, setting
// which also sets the mode's permission bits from the ACL
// (posix_acl_update_mode in fs/posix_acl.c), and empty on macOS, whose ACLs
// FileSecurity reads.
const AccessACLAttribute = accessACLAttribute

// DefaultACLAttribute is the extended attribute that holds a folder's default
// ACL, which each file and folder made in it inherits:
// system.posix_acl_default on Linux, and empty on macOS, whose inherited ACL
// entries FileSecurity reads.
const DefaultACLAttribute = defaultACLAttribute

// LabelAttribute is the extended attribute that holds a file's security
// label, which a new file gets from the system's policy and a rename keeps:
// security.selinux on Linux, where SELinux labels each file it sees and the
// label stays on disk while SELinux is off, and empty on macOS, whose files
// carry no such attribute.
const LabelAttribute = labelAttribute

// CapabilityAttribute is the extended attribute that holds a program's file
// capabilities, the privileges it gets when it runs, which only a process
// with CAP_SETFCAP may set (cap_inode_setxattr in security/commoncap.c), so
// a user could not set it back: security.capability on Linux, and empty on
// macOS, which has none.
const CapabilityAttribute = capabilityAttribute

// TouchIDPossible reports whether sudo can be set up to ask for Touch ID
// here: true on macOS, false on Linux.
const TouchIDPossible = touchIDPossible

// ReadsPasswords reports whether ReadPassword reads a password at a terminal
// here: true on macOS, and false on Linux, where ReadPassword always fails.
const ReadsPasswords = readsPasswords

// KnowsOriginalParent reports whether ProcessLineage names the process that
// forked one even after that parent exited, as macOS's kernel records it and
// Linux's does not.
const KnowsOriginalParent = knowsOriginalParent

// FollowsForks reports whether Waiter.Follow wakes a Wait when a process
// forks or execs: true on macOS, for any process the caller can see; on
// Linux, where it is false, Follow is always Unsupported.
const FollowsForks = followsForks

// AssetTriple is the operating-system part of a Rust-style release asset
// name, such as uv's: "apple-darwin" on macOS and "unknown-linux-gnu" on
// Linux.
const AssetTriple = assetTriple
