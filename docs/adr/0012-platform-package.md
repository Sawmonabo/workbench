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
  hides a file in Finder, which `FileFlags` reads; `TouchIDPossible` whether
  Touch ID for sudo can exist; `ReadsPasswords` whether `ReadPassword` reads
  a password at a terminal; `HostRelease` the release's name and version;
  `AssetTriple` the `apple-darwin` or `unknown-linux-gnu` part of a release
  asset's name.
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
