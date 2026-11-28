# 12. One package owns operating-system calls and platform facts

- Status: Accepted, October 17, 2026

## Context

Workbench runs on macOS and on Linux, including WSL. Its low-level calls
already sat in `_darwin.go` and `_linux.go` files spread over `guard` and
`operation`, but 20 `runtime.GOOS` tests in 10 files of `machine`,
`operation` and `costs` decided platform facts where they were used: the
runtime folders' default places, where Homebrew, VS Code and Claude Code keep
their files, whether Homebrew casks exist, which effects a platform offers.
Six `isWSL()` calls in four files of `machine` asked whether Workbench runs
on WSL, and each one read `/proc/sys/kernel/osrelease` again.

A file whose name ends in `_GOOS`, such as `facts_darwin.go`, builds only for
that system, with no build line, and the Go standard library splits its own
system calls this way. A `runtime.GOOS` test is evaluated at run time, and
every branch of it must compile on every system. Tailscale's `version/distro`
package detects the Linux distribution and WSL once and caches the answer for
every caller.

## Options considered

- **A. One package, `internal/platform`, owns every operating-system call and
  every platform fact, split by file suffix, and a lint rule keeps platform
  tests out of every other package.** Chosen.
- **B. Keep the `runtime.GOOS` tests where they are and only cache WSL.**
  Every branch keeps compiling on both systems, each new feature adds its own
  test, and nothing stops the next one.
- **C. Move the facts into each owning package's own suffix files.** The
  tests leave the shared code, but a fact such as the Homebrew prefix is
  needed by two packages and would be written twice.

## Decision

- **What it owns.** `internal/platform` owns every operating-system call (the
  system calls, libc calls and `/proc` and `/etc/os-release` reads that
  differ between macOS and Linux) and every fact that differs by system. The
  logic around a call stays with its owner; only the call moves: which file
  metadata an image keeps, which releases Workbench supports and when the
  apply asks for the Mac password are `operation` and `machine` policy, as
  shared code that reads the facts. It imports only the standard library,
  `golang.org/x/sys` and, on macOS, the netdb package of the SQLite driver's
  C library (`modernc.org/libc`), whose tables `ReleaseServiceTables` lets go
  of, never another Workbench package, so every other package may import it.
- **Layout.** Each subject declares every exported name once, in its file
  without a suffix (`process.go`), with one comment that states the contract
  callers rely on and how the systems differ in what they see, so callers
  compile the same on both. Its `_darwin.go` and `_linux.go` files declare
  only unexported names, with comments on how that system does it: an
  exported function calls the unexported one of the same signature, an
  exported type whose fields differ embeds an unexported per-system type, as
  `os.File` holds `*file`, and a value that differs is set from an unexported
  constant or variable. Code that is the same on both has one body in the
  file without a suffix. The system is fixed at compile time, so no Go
  interface with one implementation per build stands between the two.
  There is no third file for another system: Workbench ships for macOS and
  Linux only, so building for anything else fails to compile, which replaces
  the run-time refusal the runtime folders had.
- **Its facts.** `System` is the system's name; `RuntimeFolders(home)` the
  five default runtime folders; `HomebrewPrefixes` and
  `HomebrewRepositories` where Homebrew lives; `HasCasks` whether Homebrew
  casks and Mac apps exist; `ToolFolders(home, nvm)` the folders searched, in
  order, for an agent's launcher; `VSCodeUserFolder(home)` VS Code's user
  settings folder; `ClaudeManagedFolder` Claude Code's managed settings
  folder; `GroupFromFolder` whether a new file takes its folder's group;
  `ACLsAreAttributes` whether a file's ACL is among its extended attributes,
  where `FileSecurity` cannot read it; `HiddenFlag` the file flag that only
  hides a file in Finder, `KeptFlags` the flags a file renamed over another
  has too and `FolderFlags` those a folder may carry while its entries
  change, all among the flags `FileFlags` reads (`chflags` flags on macOS,
  inode flags on Linux); `LabelAttribute` the extended attribute that holds a
  file's security label, among the names `ExtendedAttributeNames` lists, and
  `DefaultACLAttribute` the one that holds the ACL a folder passes to what is
  made in it;
  `FlagNames` a file's flags as the system's listing names them (`ls -lO`,
  `lsattr`), `ClearFlags` the `chflags` or `chattr` command that clears
  them, after `sudo` where only root may, and `LockingFlags` the immutable
  and append-only flags, under which no file replaces a file;
  `AttributeRemoval` what an extended attribute is, where it has a name of
  its own (an ACL, file capabilities), and the command that removes it;
  `ACLRemoval` and `ACLEntryRemoval` the commands that remove a file's ACL
  and one of its entries (`chmod -N`, `chmod -a#` on macOS); `NoFollow` a
  command's form that changes a link itself, not the file it points to
  (`chflags -h`, `xattr -s`, `setfattr -h`), or none; `NewLinkMode` the
  mode a new link gets under the process's umask, read from one made and
  removed again (0777 less the umask on macOS; on Linux 0777, but on XFS
  with its deprecated `irix_symlink_mode`), and `SetLinkMode` the call that
  gives a link a mode on the link itself, never on the file it points to
  (`lchmod` on macOS; Linux changes no link's mode); `TouchIDPossible`
  whether Touch ID for sudo can exist; `ReadsPasswords` whether
  `ReadPassword` reads a password at a terminal; `HostRelease` the release's
  name and version; `AssetTriple` the `apple-darwin` or `unknown-linux-gnu`
  part of a release asset's name.
- **A fact learned from a command takes its runner.** The package starts no
  process, since `operation.Run` owns subprocesses, so `HostRelease` is
  handed a function that runs `/usr/bin/sw_vers -productVersion` on macOS
  through it. It does not read `kern.osproductversion` instead: that sysctl
  holds the same ProductVersion, but gives a process whose environment sets
  `SYSTEM_VERSION_COMPAT=1` the compatibility version 10.16 on a release that
  keeps one, while `sw_vers` runs in an environment that sets only `PATH`.
- **`System` is the Go name.** It is `darwin` or `linux`, not `macos`, so the
  doctor's platform line and the chezmoi asset name keep their exact bytes.
- **WSL once.** `WSL()` is a `sync.OnceValue`: on Linux it reads
  `/proc/sys/kernel/osrelease` at the first call and answers from memory
  after; on macOS it is false.
- **Which steps a platform offers stays in `machine`.** That is Workbench
  policy, not an operating-system fact, so `internal/machine` chooses it by
  file suffix too: `effects_darwin.go` and `effects_linux.go` each say which
  effects and optional effects the platform offers, and WSL's on Linux read
  `platform.WSL()`. The effect lists themselves stay in the shared
  `effects.go`, because a test that runs on macOS checks the Mac, Linux and
  WSL lists together; only the Mac's optional effects move to the macOS file,
  since nothing on Linux uses them. The Linux editor step keeps reading the
  `is_wsl` answer, not `platform.WSL()`: that answer is the questionnaire's,
  and the render check's WSL simulation sets it.
- **`release.Target()` keeps `runtime.GOOS`.** It names the build's own
  bundle, not a difference between systems. `runtime.GOARCH` stays wherever
  it is used.
- **macOS calls that x/sys does not wrap** are made the way x/sys makes them:
  a `//go:cgo_import_dynamic` binding and an assembly trampoline through the
  runtime's `syscall.syscall6`, whose `//go:linkname` the Go team keeps on
  purpose. The Go team calls `cgo_import_dynamic` unsupported in user code;
  Workbench already accepted that for two functions, and the alternative,
  purego, is beta and a new module. Apple marks `libproc.h`'s interfaces as
  subject to change, so every call through it fails safe: an unreadable value
  leaves behavior as it was.
- **Words by `System`, behavior by facts.** Outside `internal/platform`,
  what Workbench does on a system follows a named fact, or for the steps it
  offers `machine`'s two effects files, never the system's name. Only a
  message that names each system's own things takes its words from a table
  keyed by `System`: the refusal of private state off a native file system
  (`operation.foreignFilesystem`) and the platform check's note for an
  unsupported release (`machine.supportNote`). The one rule that reads
  `System` is the support rule that note words, `machine.supportedRelease`:
  a release name means a release only on its own system, because on Linux
  `HostRelease` gives whatever `ID` `/etc/os-release` holds, so the rule
  pairs each name it accepts with its system, and an os-release whose `ID`
  is `macOS` is no supported Mac. Apart from `effects_darwin.go`
  and `effects_linux.go`, no file outside the package has a system suffix,
  and a safeguard test that needs one system's features builds on both and
  skips where a fact says they are missing.
- **Linux file metadata.** A replace writes a new inode and renames it over
  the target, so `operation` keeps what the new inode can carry and refuses
  the rest at planning, before any write. On macOS `KeptFlags` is none,
  `FolderFlags` the hidden flag and `LabelAttribute` empty.
  - *Inode flags* (`chattr`). `FileFlags` reads them with `FS_IOC_GETFLAGS`
    on the file's descriptor. A file system without them answers `ENOTTY`
    (procfs, sysfs, 9P, FUSE, and tmpfs before Linux 6.0), or a FUSE server
    `EOPNOTSUPP`, and reports none; planning refuses FUSE and 9P as foreign
    file systems before it reads flags, so on its path that is tmpfs on an
    older kernel. A link reports none without a call, since it cannot be
    opened for one and `chattr` refuses links. A replaced target may carry only
    `KeptFlags`, which the file system sets on a new file itself: `e`
    (extents), `I` (indexed folder), `N` (inline data), `h` (huge file) and
    `E` (encrypted, which every file made in an encrypted folder takes and
    fscrypt requires of a file renamed into one). `i` (immutable) and `a`
    (append-only) block it, since `may_delete` refuses to rename over or
    unlink such a file. Every other flag blocks it too (`A`, `c`, `C`, `d`,
    `D`, `j`, `m`, `P`, `s`, `S`, `t`, `T`, `u`, `x`, and the `F` and `V` of
    case folding and verity), because it belongs to the old inode: a new
    file takes a flag from its folder only as its file system inherits it
    (ext4 `A`, `d`, `S` and others, btrfs only `c`, `m` and `C`, xfs by its
    own rules), and native stages a file in a folder of its own, from which a
    rename kept no inherited flag in a container. A folder written into, or
    one holding Workbench's files, changes only in place, so `FolderFlags`
    lets it keep every flag but `i`, under which no entry can be made
    (`inode_permission`), and `a`, under which none can be renamed or
    removed, a temporary file included (`may_delete`).
  - *Extended attributes.* `ExtendedAttributeNames` lists a file's names.
    A replacement of the same kind carries every one of up to 4 KiB but
    `CapabilityAttribute`, `security.capability`, privilege a user cannot set
    back, which blocks; the access ACL `system.posix_acl_access` among them,
    rewritten as `ACLForMode` says the new mode leaves it. Only a process with
    `CAP_SYS_ADMIN` is shown `trusted.*` names, so a user's Workbench neither
    sees nor keeps one. The folder a target is written into blocks on a
    default ACL and on every name but the label and those
    `FolderAttributeInert` names, as the table below says.
  - *The SELinux label.* A replacement gets the label of the file it
    replaces, as `sed -i` (`setfscreatecon`), Vim and Emacs (`setfilecon`
    after the write) and `cp --preserve=context` give one, and as a rename
    keeps it. `install` and systemd ask the policy for the path's default
    instead (`selabel_lookup`), which drops a label set with `chcon` and
    needs libselinux's file contexts, which Workbench does not link. The
    label is written only when the system gave the new inode another, so
    the common case needs no relabel permission. Where the new inode has no
    label and the system refuses one (`EPERM`, `EOPNOTSUPP`), SELinux is
    off: no new file gets a label then, setting one takes `CAP_SYS_ADMIN`
    (`cap_inode_setxattr`), and turning SELinux back on relabels the file
    systems, so the file stays unlabeled like every file made meanwhile.
    Any other refusal, such as SELinux's `relabelfrom`, `relabelto` or
    `associate` check, fails the write before its rename. After a native
    run, a file or folder native replaced gets its earlier label back in
    the opened-inode pass that gives it its group, since native stages its
    files elsewhere; a file native creates that holds a label is written
    anew in its own folder, where the system gives it the label a file made
    there gets. Image comparisons leave the label out: a relabel such as
    `restorecon` makes is the system's, not an edit, so it neither blocks a
    revert nor makes a write.
- **What a target and its folders may hold.** `operation` decides each rule
  from what `platform` reads and asks; a refusal names the path, what it holds
  and the command that clears it, never a value or an entry.

  | Holder | Metadata | Rule | Source |
  | --- | --- | --- | --- |
  | File or link replaced by one of its kind | ACL, extended attributes, hidden flag | Carried onto the new file or link itself, by Workbench's write and by the pass after chezmoi, which writes the approved image anew; revert puts each back | `setattrlist(2)`, `chflags(2)`; Linux `__posix_acl_chmod_masq` for the ACL the new mode leaves |
  | File or link, macOS | ACL entry denying delete | Refused only when the system denies this user deleting it (`Allowed` with `DeleteRight`) while the folder lets it remove files: no file can be renamed over it | xnu `access1`, `vnode_authorize_delete`, `vn_authorize_renamex_with_paths`; `_DELETE_OK` in `sys/unistd.h` |
  | Folder a plan removes, macOS | ACL entry denying delete | Refused by the same rule: `rmdir` needs it | xnu `vn_authorize_rmdir` |
  | Folder written into, or holding Workbench's files, macOS | ACL entry denying adding or removing entries (by name or a generic right) | Refused when the system denies this user `ChildRights` there; on a folder that is not this user's, or whose mode denies its owner those rights, any such entry is refused, since the mode alone would deny them | xnu `access1`, `kauth_acl_evaluate`; `KAUTH_VNODE_GENERIC_WRITE_BITS` in `sys/kauth.h` |
  | Same folders, macOS | Inherited ACL entry | Refused: each file made there takes it | `KAUTH_ACE_FILE_INHERIT`, `KAUTH_ACE_DIRECTORY_INHERIT` |
  | Folder written into, Linux | Access ACL, `user.*` | Admitted: named entries are limited by the mask, which the mode shows as its group bits and `safeParents` checks, and a new file takes only a default ACL; `user.*` has no meaning to the kernel | `posix_acl_permission`, `posix_acl_create`, `posix_acl_update_mode`; `xattr_permission`; xattr(7) |
  | Same folder, Linux | Default ACL, `user.overlay.*`, any other name but the label | Refused: a default ACL passes to each new file; overlayfs reads `user.overlay.*` on its layers | `posix_acl_create`; `OVL_XATTR_USER_PREFIX` in `fs/overlayfs/overlayfs.h` |
  | File, Linux | File capabilities | Refused: only `CAP_SETFCAP` sets them | `cap_convert_nscap` and `cap_inode_setxattr` in `security/commoncap.c` |
  | File chezmoi creates, Linux | SELinux label | Written anew in its own folder, as Workbench's own writes are made there, so it gets that folder's label, not that of chezmoi's temporary folder | SELinux's label of a new file (`services.c`, below) |
  | Link at a target | Where it points | Not checked, but into Workbench's own files: Workbench writes with `symlinkat` and `renameat` and removes with `unlinkat`, none of which follows it, and chezmoi replaces or removes it the same way | renameio, chezmoi `realsystem_unix.go` |
  | Folder above a target | A link | Refused, as `safeParents` refuses it: a write would go where it points | `openat(2)` with `O_NOFOLLOW` |

- **Kept that way.** A `forbidigo` rule in `.golangci.yml` forbids
  `runtime.GOOS` outside `internal/platform` and `internal/release/archive.go`,
  so a new platform test anywhere else fails `golangci-lint run`.

## Consequences

- A question about the system is answered in one place, and a new platform
  difference is one exported name with its contract and an unexported name in
  each suffix file, not a test at its use.
- The move must change no behavior: the doctor's lines and the plan digest
  are compared before and after it, on macOS, on Linux and in the WSL
  simulation.
- CI lints the linux and the darwin build, each by name with `GOOS` set,
  so the linters and the `runtime.GOOS` rule see both systems' suffix files;
  run `GOOS=linux golangci-lint run` and `GOOS=darwin golangci-lint run`
  locally before a change to either lands.
- Building for a system other than macOS or Linux fails at compile time
  instead of refusing at run time.

## Sources

- [go/build: build constraints and `_GOOS` file names](https://pkg.go.dev/cmd/go/internal/help@go1.26.4)
- [sync.OnceValue](https://pkg.go.dev/sync#OnceValue)
- [Tailscale `version/distro`: distribution and WSL detected once](https://pkg.go.dev/tailscale.com@v1.98.3/version/distro)
- [Go issue 61220: `cgo_import_dynamic` in user code](https://golang.org/issue/61220)
- [purego](https://pkg.go.dev/github.com/ebitengine/purego)
- [xnu `libproc.h`](https://git.saurik.com/apple/xnu.git/blob/HEAD:/libsyscall/wrappers/libproc/libproc.h)
- Go source, `syscall/syscall_darwin.go`: the `syscall6` link name, "Do not
  remove or change the type signature"
- [xnu `bsd/kern/kern_sysctl.c`: `kern.osproductversion`, "The ProductVersion
  from SystemVersion.plist", answers a task with system version compat from
  `kern.osproductversioncompat`](https://github.com/apple-oss-distributions/xnu/blob/xnu-11215.81.4/bsd/kern/kern_sysctl.c)
- [xnu `libsyscall/wrappers/_libkernel_init.c`: the compat shim sets that
  task flag through `kern.system_version_compat`](https://github.com/apple-oss-distributions/xnu/blob/xnu-11215.81.4/libsyscall/wrappers/_libkernel_init.c)
- Inode flags: [`include/uapi/linux/fs.h`, the flag values](https://github.com/torvalds/linux/blob/v6.12/include/uapi/linux/fs.h#L223-L298);
  [`fs/namei.c` `may_delete`](https://github.com/torvalds/linux/blob/v6.12/fs/namei.c#L3037-L3097),
  [`inode_permission`](https://github.com/torvalds/linux/blob/v6.12/fs/namei.c#L510-L545) and
  [`vfs_rename`](https://github.com/torvalds/linux/blob/v6.12/fs/namei.c#L4892-L4927);
  [`fs/attr.c` `may_setattr`](https://github.com/torvalds/linux/blob/v6.12/fs/attr.c#L322-L345);
  [ext4's flags and the ones a new inode inherits](https://github.com/torvalds/linux/blob/v6.12/fs/ext4/ext4.h#L507-L568),
  [ext4 `ialloc.c`](https://github.com/torvalds/linux/blob/v6.12/fs/ext4/ialloc.c#L1257-L1260),
  [btrfs `inode.c`](https://github.com/torvalds/linux/blob/v6.12/fs/btrfs/inode.c#L6197-L6226),
  [xfs `xfs_inode_util.c`](https://github.com/torvalds/linux/blob/v6.12/fs/xfs/libxfs/xfs_inode_util.c#L146-L235)
  and [xfs.rst](https://github.com/torvalds/linux/blob/v6.12/Documentation/admin-guide/xfs.rst#L328-L351),
  [tmpfs `shmem_fs.h`](https://github.com/torvalds/linux/blob/v6.12/include/linux/shmem_fs.h#L45-L48),
  tmpfs `mm/shmem.c` without [`fileattr_get` in 5.19](https://github.com/torvalds/linux/blob/v5.19/mm/shmem.c)
  and with it [in 6.0](https://github.com/torvalds/linux/blob/v6.0/mm/shmem.c#L3182-L3190),
  [fscrypt.rst](https://github.com/torvalds/linux/blob/v6.12/Documentation/filesystems/fscrypt.rst),
  [fsverity.rst](https://github.com/torvalds/linux/blob/v6.12/Documentation/filesystems/fsverity.rst);
  [FS_IOC_SETFLAGS(2const)](https://man7.org/linux/man-pages/man2/FS_IOC_SETFLAGS.2const.html),
  [ioctl(2): `ENOTTY`](https://man7.org/linux/man-pages/man2/ioctl.2.html),
  [chattr(1)](https://man7.org/linux/man-pages/man1/chattr.1.html),
  [e2fsprogs `fgetflags.c`](https://github.com/tytso/e2fsprogs/blob/v1.47.4/lib/e2p/fgetflags.c#L49-L112)
- Extended attributes and labels in the kernel:
  [`fs/xattr.c`, the `trusted.*` rule](https://github.com/torvalds/linux/blob/v7.3/fs/xattr.c#L126-L145),
  [`cap_inode_setxattr`: a `security.*` name takes `CAP_SYS_ADMIN`](https://github.com/torvalds/linux/blob/v7.3/security/commoncap.c#L1009-L1045),
  [`security.c`](https://github.com/torvalds/linux/blob/v7.3/security/security.c#L1924-L1942),
  [SELinux's setxattr checks](https://github.com/torvalds/linux/blob/v7.3/security/selinux/hooks.c#L3383-L3465),
  [a new file's label](https://github.com/torvalds/linux/blob/v7.3/security/selinux/ss/services.c#L1876-L1899);
  [SELinux Notebook: `relabelfrom`, `relabelto`](https://github.com/SELinuxProject/selinux-notebook/blob/7cd56dc9beb67babed079dbbf57ffb9c472d5080/src/object_classes_permissions.md#L158-L166);
  libselinux 3.11 [getfscreatecon(3)](https://github.com/SELinuxProject/selinux/blob/3.11/libselinux/man/man3/getfscreatecon.3)
  and [setfilecon(3)](https://github.com/SELinuxProject/selinux/blob/3.11/libselinux/man/man3/setfilecon.3)
- ACLs and deletion on macOS, xnu-12377.121.6:
  [`access1` and `faccessat`](https://github.com/apple-oss-distributions/xnu/blob/xnu-12377.121.6/bsd/vfs/vfs_syscalls.c#L6902-L6952),
  [`vn_authorize_renamex_with_paths`](https://github.com/apple-oss-distributions/xnu/blob/xnu-12377.121.6/bsd/vfs/vfs_subr.c#L8554-L8789),
  [`vn_authorize_rmdir`](https://github.com/apple-oss-distributions/xnu/blob/xnu-12377.121.6/bsd/vfs/vfs_subr.c#L8833-L8867),
  [`vnode_authorize_delete`](https://github.com/apple-oss-distributions/xnu/blob/xnu-12377.121.6/bsd/vfs/vfs_subr.c#L9501-L9666),
  [`kauth_acl_evaluate`](https://github.com/apple-oss-distributions/xnu/blob/xnu-12377.121.6/bsd/kern/kern_authorization.c#L608-L782),
  [`_DELETE_OK` and `_RMFILE_OK`](https://github.com/apple-oss-distributions/xnu/blob/xnu-12377.121.6/bsd/sys/unistd.h#L103-L118),
  [entry flags](https://github.com/apple-oss-distributions/xnu/blob/xnu-12377.121.6/bsd/sys/kauth.h#L588-L610),
  [the rights](https://github.com/apple-oss-distributions/xnu/blob/xnu-12377.121.6/bsd/sys/kauth.h#L886-L898)
  and [the generic write rights](https://github.com/apple-oss-distributions/xnu/blob/xnu-12377.121.6/bsd/sys/kauth.h#L975-L981)
- POSIX ACLs and attributes on Linux, v7.3:
  [`posix_acl_permission`](https://github.com/torvalds/linux/blob/v7.3/fs/posix_acl.c#L381-L449),
  [`__posix_acl_chmod_masq`](https://github.com/torvalds/linux/blob/v7.3/fs/posix_acl.c#L516-L559),
  [`posix_acl_create`](https://github.com/torvalds/linux/blob/v7.3/fs/posix_acl.c#L640-L687),
  [`posix_acl_update_mode`](https://github.com/torvalds/linux/blob/v7.3/fs/posix_acl.c#L712-L729),
  [`acl_permission_check`](https://github.com/torvalds/linux/blob/v7.3/fs/namei.c#L438-L497),
  [`xattr_permission`](https://github.com/torvalds/linux/blob/v7.3/fs/xattr.c#L125-L179),
  [overlayfs's attribute prefixes](https://github.com/torvalds/linux/blob/v7.3/fs/overlayfs/overlayfs.h#L29-L39),
  [xattr(7)](https://man7.org/linux/man-pages/man7/xattr.7.html),
  [`cap_convert_nscap`: setting file capabilities takes `CAP_SETFCAP`](https://github.com/torvalds/linux/blob/v7.3/security/commoncap.c#L569-L620)
- What other tools do: [sed `execute.c`](https://github.com/mirror/sed/blob/v4.9/sed/execute.c#L598-L630);
  coreutils [`install.c`](https://github.com/coreutils/coreutils/blob/v9.7/src/install.c#L318-L353),
  [`copy.c`](https://github.com/coreutils/coreutils/blob/v9.7/src/copy.c#L1021-L1067),
  [`mv.c`](https://github.com/coreutils/coreutils/blob/v9.7/src/mv.c#L396-L406),
  [cp](https://www.gnu.org/software/coreutils/manual/html_node/cp-invocation.html)
  and [mv](https://www.gnu.org/software/coreutils/manual/html_node/mv-invocation.html) manuals;
  [rsync `--xattrs`](https://github.com/RsyncProject/rsync/blob/v3.4.1/rsync.1.md#L1460-L1468);
  systemd [`fileio.c`](https://github.com/systemd/systemd/blob/v262.2/src/basic/fileio.c#L217-L265)
  and [`selinux-util.c`](https://github.com/systemd/systemd/blob/v262.2/src/shared/selinux-util.c#L742-L770);
  [Git `tempfile.c`](https://github.com/git/git/blob/v2.52.0/tempfile.c#L336-L358), which sets no label;
  [Vim `mch_copy_sec`](https://github.com/vim/vim/blob/v9.2.1417/src/os_unix.c#L2997-L3048);
  Emacs [`files.el`](https://github.com/emacs-mirror/emacs/blob/emacs-31.1/lisp/files.el#L6283-L6302)
  and [`fileio.c`](https://github.com/emacs-mirror/emacs/blob/emacs-31.1/src/fileio.c#L3446-L3530);
  [renameio's temporary folder](https://github.com/google/renameio/blob/v2.0.2/tempfile.go#L86-L135)
  and [chezmoi's writes through it](https://github.com/twpayne/chezmoi/blob/v2.70.5/internal/chezmoi/realsystem_unix.go#L67-L105)
- Red Hat, Using SELinux: [RHEL 9](http://web.archive.org/web/20260204224549/https://docs.redhat.com/en/documentation/red_hat_enterprise_linux/9/html-single/using_selinux/index)
  (files made while SELinux is disabled are not labeled, and the file
  systems are relabeled when it is turned back on; `mv` and `restorecon`),
  [RHEL 10](http://web.archive.org/web/20260513082426/https://docs.redhat.com/en/documentation/red_hat_enterprise_linux/10/html-single/using_selinux/index),
  [RHEL 7 guide](http://web.archive.org/web/20260424224852/https://docs.redhat.com/en/documentation/red_hat_enterprise_linux/7/html-single/selinux_users_and_administrators_guide/index)
  (a new file takes its folder's type; `cp` and `mv`)
