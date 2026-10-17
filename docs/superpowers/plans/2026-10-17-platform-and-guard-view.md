# Platform Package, Guard Log Format and Live Memory View Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Gather every operating-system call and platform fact into `internal/platform`, then print the guard log one event per line and add `workbench guard memory`, a live view of the commands the guard tracks from which a person can stop them.

**Architecture:** `internal/platform` has one `_darwin.go` and one `_linux.go` file per subject, each exporting the same names, and imports only the standard library and `golang.org/x/sys`; the policy around each call stays with its owner, chosen by file suffix where it differs. The watcher measures each tracked command every tick, serves snapshots and takes stop requests over a Unix socket in the guard's private folder, checking each peer's credentials; a person's stop is signalled at once and followed across ticks, so the tick never waits on it. The view is a Bubble Tea program in `internal/cli` that reuses the costs view's full-screen pattern.

**Tech Stack:** Go 1.26, `golang.org/x/sys/unix`, Bubble Tea v2.0.10, Lip Gloss v2, Bubbles v2, `log/slog` JSON Lines, golangci-lint v2.14 (`forbidigo`).

**Spec:** `docs/superpowers/specs/2026-10-17-platform-and-guard-view-design.md` (the "guard design" it cites is `docs/superpowers/specs/2026-10-14-agent-command-guard-design.md`). Where this plan rules against the spec's text, the ruling is marked **Ruling** and Task 9 amends the spec.

## Global Constraints

- Platform code goes in `_darwin.go` / `_linux.go` files, never behind a run-time test: the Go toolchain builds a file whose name ends in `_GOOS` only for that system ([go/build](https://pkg.go.dev/cmd/go/internal/help@go1.26.4)). A shared file exports the behavior and calls what the suffix files define, the idiom the Go standard library and [Gopher Academy's multi-platform guide](https://blog.gopheracademy.com/advent-2013/day-23-multi-platform-applications/) describe. There is no third file for another system (spec 3.2).
- `internal/platform` imports only the standard library and `golang.org/x/sys` (spec 3.1). Every exported name exists in both suffix files with the same signature; a shared file holds only what is identical on both.
- WSL is detected once with `sync.OnceValue`, which "returns a function that invokes f only once" ([sync](https://pkg.go.dev/sync#OnceValue)), as Tailscale's [distro](https://pkg.go.dev/tailscale.com@v1.98.3/version/distro) package does.
- `runtime.GOOS` appears only in `internal/platform` and `internal/release/archive.go`, enforced by `forbidigo` (spec 3.5).
- macOS libSystem functions that `golang.org/x/sys` does not wrap are called the way x/sys itself calls them: a `//go:cgo_import_dynamic` binding and an assembly trampoline, through the runtime's `syscall.syscall6`, whose `//go:linkname` the Go team keeps on purpose ("Do not remove or change the type signature", `syscall/syscall_darwin.go`). The Go team calls `cgo_import_dynamic` unsupported in user code ([Go issue 61220](https://golang.org/issue/61220)); Workbench already accepts that for two functions, and the alternative, [purego](https://pkg.go.dev/github.com/ebitengine/purego), is beta and a new module. Apple's `libproc.h` says its interfaces are "subject to change in future releases" ([xnu libproc.h](https://git.saurik.com/apple/xnu.git/blob/HEAD:/libsyscall/wrappers/libproc/libproc.h)); every call through it fails safe (an unreadable value leaves behavior as today).
- Doc comments: "Every exported (capitalized) name should have a doc comment" ([Go Doc Comments](https://go.dev/doc/comment)), starting with the name; `revive`'s `exported` and `package-comments` rules are on. Unexported names that carry a decision get one too, in the repository's style.
- Line length: "There is no rigid line length limit in Go code, but avoid uncomfortably long lines" ([Code Review Comments](https://go.dev/wiki/CodeReviewComments)); here `golines` wraps at 100 and `lll` fails at 120. A user-facing string over 50 characters stays whole. `funlen` 100 lines / 60 statements, `gocognit` 40, `nestif` 8, `unparam` (name an unused parameter `_`), `unused` (a declaration a suffix file alone uses must live in that suffix file).
- Package names are short, lowercase, one word; "Avoid uninformative package names like `util`" ([Google Go Style Decisions](https://google.github.io/styleguide/go/decisions)). Supporting packages live under `internal/` ([Organizing a Go module](https://go.dev/doc/modules/layout)).
- Concurrency: the watcher's state is owned by its one tick goroutine; other goroutines send requests over channels and get replies on a channel of their own, the stateful-goroutine pattern ([Go by Example](https://gobyexample.com/stateful-goroutines)). A snapshot is sent to each client without blocking, and a client whose buffer is full is disconnected, as pub/sub systems handle a slow consumer ([Tendermint ADR 033](https://github.com/chainpoint/tendermint/blob/develop/docs/architecture/adr-033-pubsub.md), [NATS slow consumers](https://docs.nats.io/learn/resilient-clients/slow-consumers)).
- Tests: AGENTS.md admits one only for catastrophic data loss, credential exposure or unauthorized execution, explained beside it, and no mock-only interface. New guard tests go in `internal/guard/match_test.go`'s style: pure deciders over fixed snapshots built with its `process`, `snapshotOf`, `claudeShell` and `start` helpers; cases sharing a decider go in one table test with `t.Run` ([TableDrivenTests](https://go.dev/wiki/TableDrivenTests)). No test opens a socket, signals a process or reads `/proc`. The platform move adds none (spec 3.6).
- Reuse first: `operation.AgentSession`, `operation.StartDetached`, `operation.InheritLock`, `guard.ReadLog`, `guard.Gigabytes`, `parseTimeFlag`, `newPainter`, `lineWidth`, `terminalWidth`, `runCostsView`'s program options, `view.AltScreen`, the guard's `targets`, `escalation` and `outside` (spec 7). No new module.
- Gates after every task, from the worktree root: `golangci-lint fmt ./...`, `golangci-lint config verify`, `golangci-lint run --concurrency 2 ./...`, `GOOS=linux golangci-lint run --concurrency 2 ./...`, `go build ./...`, `GOOS=linux go build ./...`, `GOOS=linux go vet ./...`, `go test ./...`. CI lints the darwin target only, so the Linux lint runs here.
- Smokes run in disposable homes with `WORKBENCH_GUARD=dry-run` first and kill only processes the smoke started; never `workbench apply` on the Mac. A smoke home that holds a socket is `mktemp -d /tmp/wb.XXXX`, not a folder under the session scratchpad: a Unix socket path is limited to 104 bytes on macOS (`unix.RawSockaddrUnix.Path`), and the scratchpad path alone is longer. The smoke removes the folder when done.
- Commit subjects are conventional (`refactor(platform): …`, `feat(guard): …`, `docs(guard): …`); each commit ends with `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`. Never edit `CHANGELOG.md`.
- Tracked docs name no person's machine, project, worktree or call ID; examples use neutral names and made-up IDs. Times are 12-hour, America/New_York.

## Review Focus

1. **A slow or stuck view must never slow the watcher.** Snapshots are sent without blocking and a client whose 4-message buffer is full is dropped; a stop request is run by the tick and answered before any wait. Pinned by Task 8's design and its Step 5 smoke, which leaves one connection unread while the watcher keeps ticking.
2. **A person's stop while the guard's own stop is under way.** The guard's stop polls for up to 10 s inside its tick; during that poll the watcher still broadcasts snapshots (the rows show `stopping`), queued stop requests wait for the tick after it, and `Client.Stop` allows 12 s. The person's stop is then signalled and followed across ticks, sets no cooldown and counts toward no refusal, and the guard's own chooser skips a command a person is already stopping. Pinned by Task 8's `followManual` design and the Step 5 smoke (stop a second command while a first is `stopping`).
3. **A watcher handover while a view is connected.** The new watcher takes the listener as descriptor 6 only when the handover said it was passed, listens fresh otherwise, and carries each command's peak; the view reconnects within a second and `Hello` gives up after 3 s against a watcher that ignores the socket. Pinned by Task 8's handover steps and Task 10's smoke.
4. **Hook input without `cwd`.** `project` and `cwd` stay empty, the row shows `—`, pairing is today's. Pinned by Task 5's `noFolderKnown` case, whose note has no `Cwd`.
5. **A `cd` to a quoted, `~` or `$VAR` path.** A quoted plain path strips and labels; `~` and `$VAR` make the folder unknown, so pairing is today's and nothing is rejected. Pinned by Task 5's `quotedFolder` and `tildeIsUnknown` cases.

---

### Task 1: Baseline for the no-behavior-change check

**Files:**
- Create: `$WS/baseline/` (`$WS` is the SDD workspace `scripts/sdd-workspace` prints; git-ignored)

**Interfaces:**
- Produces: `baseline/darwin-doctor.txt`, `baseline/darwin-plan.json`, `baseline/linux-doctor.txt`, `baseline/linux-plan.json`, `baseline/render-check-wsl.txt`, `baseline/commit`. Task 4 repeats the same commands into `$WS/after/` and diffs.

- [ ] **Step 1: Build both systems**

Run: `go build -buildvcs=false -o "$WS/baseline/workbench-darwin" ./cmd/workbench && GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -buildvcs=false -o "$WS/baseline/workbench-linux" ./cmd/workbench`. `-buildvcs=false` matters: `version()` (`internal/cli/root.go:384`) otherwise reads the commit into the version that `doctor` prints, and the before and after builds sit on different commits.

- [ ] **Step 2: Write the answers file**

`$WS/baseline/answers.toml`: a `[data]` table with `machine_role = "personal"` and a fixed value for every key `home/.chezmoi.toml.tmpl` prompts for (read its `prompt*Once` calls). One file for both systems.

- [ ] **Step 3: Capture macOS**

The home is the fixed path `$WS/home`, removed and recreated before each capture, so paths inside the plan are identical between runs (a plan carries absolute paths, so a fresh `mktemp` home would change the digest for the wrong reason):
```bash
rm -rf "$WS/home" && mkdir "$WS/home"
env -i HOME="$WS/home" PATH=/usr/bin:/bin TERM=dumb "$WS/baseline/workbench-darwin" doctor > "$WS/baseline/darwin-doctor.txt" 2>&1; echo "EXIT:$?"
env -i HOME="$WS/home" PATH=/usr/bin:/bin "$WS/baseline/workbench-darwin" init --answers-from "$WS/baseline/answers.toml" --dry-run --json --local-build > "$WS/baseline/darwin-plan.json"; echo "EXIT:$?"
```
Expected: both exit 0 or 3; record the codes in `baseline/exits.txt`.

- [ ] **Step 4: Capture Linux**

Run from the worktree root (`--local-build` is a hidden root flag that needs the checkout, mounted read-only; the home is `/tmp/h`, the container's own):
`docker run --rm --memory 2g -v "$WS/baseline:/b:ro" -v "$PWD:/src:ro" -e HOME=/tmp/h -w /src ubuntu:24.04 sh -c 'mkdir -p /tmp/h && /b/workbench-linux doctor; echo "EXIT:$?"' > "$WS/baseline/linux-doctor.txt" 2>&1`, and the same with `init --answers-from /b/answers.toml --dry-run --json --local-build` into `linux-plan.json`.

- [ ] **Step 5: Capture the WSL simulation and the commit**

Run: `scripts/render-check.sh personal pinned wsl > "$WS/baseline/render-check-wsl.txt" 2>&1; echo "EXIT:$?"` (expect 0) and `git rev-parse HEAD > "$WS/baseline/commit"`. Nothing is committed to git.

---

### Task 2: `internal/platform` processes, memory, signals, libc and the guard move

**Files:**
- Create: `internal/platform/platform.go`, `internal/platform/process.go`, `internal/platform/process_darwin.go`, `internal/platform/process_linux.go`, `internal/platform/memory.go`, `internal/platform/memory_darwin.go`, `internal/platform/memory_linux.go`, `internal/platform/libc_darwin.go`, `internal/platform/libc_darwin.s`
- Modify: `internal/guard/guard.go` (`proc`), `internal/guard/memory.go` (`memory`, `readMemory`), `internal/guard/watch.go` (`snapshot`, `stopLargest`, `signal`, `escalate`, `escalateNow`, `exited`, `waiter`, `loadArgs`), `internal/guard/hooks.go` (`processInfo`), `internal/guard/match.go` (`startSlack`), `internal/guard/match_test.go` (`start`, `process`, line 408), `internal/operation/image_metadata_darwin.go` (`darwinACL`'s `libcCall6` calls)
- Delete: `internal/guard/platform_darwin.go`, `internal/guard/platform_linux.go`, `internal/operation/libc_darwin.go`, `internal/operation/libc_darwin.s`

**Interfaces:**
- Produces, in `package platform`:
  - `platform.go`: only the package doc comment (what it owns, the two-file rule, the import rule, the trampoline note from Global Constraints). Values that differ by system live in the suffix files.
  - `process.go` (shared): `type Process struct { PID, PPID, PGID int; Start uint64; Name string; Zombie bool }` with guard's `proc` doc comment (Start with the PID names the process); `func Exited(err error) bool` (ENOENT or ESRCH; from guard's `exited`).
  - `process_darwin.go` / `process_linux.go`: `const System = "darwin"` / `"linux"` (**Ruling:** the Go name, not spec 3.3's `"macos"`, so `checkPlatform`'s message bytes and the doctor line do not change, as spec 3.6 demands); `const StartSecond = 1_000_000` / `100`, untyped so `match_test.go`'s `start()` keeps multiplying a `float64` by it; `func Processes(unreadable func(pid int, err error)) ([]Process, error)` (from `snapshotNow`; macOS names the parameter `_`), `func ProcessInfo(pid int) (Process, error)`, `func Args(pid int) []string`, `func Environ(pid int, name string) string` (one variable of the process's starting environment; generalizes `readCallID`), `func Footprint(pid int) (uint64, error)` (physical footprint / `smaps_rollup` Pss, as today), `func Resident(pid int) (uint64, error)` (macOS: the physical footprint again, spec 6.4; Linux: `/proc/PID/statm` field 2 × `os.Getpagesize()`, which [proc_pid_statm(5)](https://man7.org/linux/man-pages/man5/proc_pid_statm.5.html) marks approximate: fine for a live display, while the stop decision keeps Pss), `func Signal(p Process, group bool, sig unix.Signal) bool` (from `send`), `type Waiter struct{…}` declared in each suffix file with its doc comment, `func NewWaiter() *Waiter`, `func (w *Waiter) Watch(pid int)`, `func (w *Waiter) Wait(d time.Duration)`, `func (w *Waiter) Close()` (from `waiter`). `Folder` comes in Task 3.
  - `memory.go` (shared): `type Memory struct { Level int; Available, Total, Installed, SwapFree, SwapTotal uint64; FullAvg10 float64 }` with guard's doc comment.
  - `memory_darwin.go` / `memory_linux.go`: `func MachineMemory() (Memory, error)` (macOS: `readMemory`'s body; Linux: `/proc/meminfo` and `/proc/pressure/memory` only), `func CgroupMemory(machine Memory) (Memory, bool)` (Linux: `confined` with `fullAvg10`, `cgroupNumber`, `cgroupRoot`; macOS returns `Memory{}, false`, documented: macOS has no cgroups).
  - `libc_darwin.go` / `libc_darwin.s`: `libcCall6`, `getattrlist`, `fgetattrlist`, `procPidRusage` and `PhysicalFootprint`'s body (now `Footprint`) moved whole, with the existing comments. Export `func LibcCall6(fn, a1, a2, a3, a4, a5, a6 uintptr) (r1, r2 uintptr, err unix.Errno)` and `var Getattrlist, Fgetattrlist uintptr` only until Task 3 moves `darwinACL` in; Task 3 unexports them again. (They move here, not in Task 3, because `Footprint` needs `proc_pid_rusage` and `platform` may not import `operation`.)
- Consumes: nothing new.

- [ ] **Step 1: Write the platform files**

Move the bodies named above out of `internal/guard/platform_*.go` and `internal/operation/libc_darwin.*`, renaming as listed; keep every existing comment, adjusted to the new names. `Processes` returns `[]Process`; the map stays guard's.

- [ ] **Step 2: Make the guard and operation use them**

- `guard.go`: `type proc struct { platform.Process; Args []string; CallID string }`.
- `watch.go`: `snapshot()` calls `platform.Processes` and builds the map; `platform.Footprint`, `platform.Signal(platform.Process{PID: t.PID, Start: t.Start}, t.Group, sig)`, `platform.Exited`, `*platform.Waiter`; `loadArgs` reads `platform.Args` and `platform.Environ(pid, callIDVariable)`.
- `memory.go`: `type memory platform.Memory`; `func readMemory() (memory, error)` reads `platform.MachineMemory`, then `platform.CgroupMemory(machine)` and keeps the cgroup reading under exactly the condition `readMemory` in `platform_linux.go` uses today (`decide` on both, then `availablePercent`): guard policy, shared code.
- `match.go`, `hooks.go`: `platform.StartSecond`, `platform.ProcessInfo`.
- `match_test.go`: `process()` builds `proc{Process: platform.Process{PID: pid, PPID: ppid, PGID: pgid, Start: start(seconds), Name: args[0]}, Args: args}`; line 408 becomes `proc{Process: platform.Process{PID: c.root, Start: c.start}}`.
- `image_metadata_darwin.go`: `darwinACL` calls `platform.LibcCall6(platform.Fgetattrlist, …)` for now.

- [ ] **Step 3: Gates**

Run the gate list. Expected: all pass; `go test ./internal/guard/` passes.

- [ ] **Step 4: Commit**

```bash
git add internal/platform internal/guard internal/operation
git commit -m "refactor(platform): move the guard's process, memory, signal and libc calls into one package"
```

---

### Task 3: Platform file calls, the process folder, peer credentials and the operation move

**Files:**
- Create: `internal/platform/file_darwin.go`, `internal/platform/file_linux.go`, `internal/platform/socket_darwin.go`, `internal/platform/socket_linux.go`
- Modify: `internal/platform/process_darwin.go` (`Folder`), `internal/platform/process_linux.go` (`Folder`), `internal/platform/libc_darwin.go` / `.s` (new trampoline; unexport again), `internal/operation/filesystem_darwin.go`, `internal/operation/filesystem_linux.go`, `internal/operation/image_metadata_darwin.go`, `internal/operation/image_metadata_linux.go`, `internal/operation/image_metadata_darwin_test.go:56`, `internal/operation/askpass.go`, `internal/operation/askpass_darwin.go`, `internal/operation/state.go:340,366,496`, `internal/operation/context.go:239`, `internal/operation/process.go:231,374,505`, `internal/cli/progress.go:21`, `internal/cli/planview.go:164`, `internal/cli/costs.go:172-173`
- Rename: `internal/operation/askpass_other.go` → `askpass_linux.go` (drop its `//go:build !darwin` line)

**Interfaces:**
- Produces:
  - `file_*.go`: `func IsTerminal(fd uintptr) bool`; `func NativeFilesystem(path string) (bool, error)` (walks up to the first existing ancestor as `privateFilesystem` does; true for `apfs`/`hfs` on macOS, for the ext, XFS, Btrfs, tmpfs and overlay magic numbers on Linux; the error is a statfs failure); `func RenameNoReplace(from, to string) error`; `func RenameAt(fd int, from, to string, exclusive bool) error` (from `replaceImage`); `type ACE struct { Flags, Rights uint32 }`; `var ErrUnsupportedACL = errors.New("unsupported ACL layout")` (shared, in `file.go`); `func FileSecurity(fd int, path string) (entries []ACE, present bool, err error)` (macOS: `darwinACL`'s body, `path != ""` selects `getattrlist`, errors are the errno or `ErrUnsupportedACL`; Linux: `nil, false, nil`, documented: Linux keeps POSIX ACLs as extended attributes, which `ExtendedAttributeCount` counts); `func ExtendedAttributeCount(fd int, path string) (int, error)` (`Flistxattr` on `fd`, `Llistxattr` on `path` when `fd < 0`, through the two-call read `attributeNames` does today, on both systems).
  - `process_*.go`: `func Folder(pid int) (string, error)`, the process's working folder as the kernel reports it (a real path). Linux: `os.Readlink("/proc/" + pid + "/cwd")` ([proc(5)](https://man7.org/linux/man-pages/man5/proc_pid_statm.5.html) family). macOS: `proc_pidinfo(pid, PROC_PIDVNODEPATHINFO, 0, &buf, len)` through a new `procPidinfo` trampoline, the call Apple's own [libproc](https://git.saurik.com/apple/xnu.git/blob/HEAD:/libsyscall/wrappers/libproc/proc_listpidspath.c) and [Terminology](https://git.enlightenment.org/enlightenment/terminology/commit/2626037407fe094e3755f1481771d8283c5996de) use for the current directory. Constants, checked on Oct 17, 2026 against the installed SDK's `sys/proc_info.h` with a C `offsetof` program: flavor `9`, `sizeof(struct proc_vnodepathinfo)` = `2352`, `pvi_cdir.vip_path` at byte `152`, `MAXPATHLEN` = `1024`; the prototype is `int proc_pidinfo(int pid, int flavor, uint64_t arg, void *buffer, int buffersize)` (`libproc.h:96`). The call returns the bytes written; anything under 2352 is an error; the path is the NUL-terminated string from byte 152.
  - `socket_*.go`: `type Credentials struct { UID, PID int }`; `func Peer(conn *net.UnixConn) (Credentials, error)` through `conn.SyscallConn().Control`. macOS: `unix.GetsockoptXucred(fd, unix.SOL_LOCAL, unix.LOCAL_PEERCRED)` for the UID (`Xucred` has no PID) and `unix.GetsockoptInt(fd, unix.SOL_LOCAL, unix.LOCAL_PEERPID)` for the PID, as Rust's standard library does ([ucred.rs](https://codebrowser.dev/slint/crates/std/src/os/unix/ucred.rs.html); `LOCAL_PEERPID` exists since OS X 10.8, [libvirt](https://www.redhat.com/archives/libvir-list/2013-October/msg00617.html)). Linux: `unix.GetsockoptUcred(fd, unix.SOL_SOCKET, unix.SO_PEERCRED)` (`Ucred{Pid, Uid, Gid}`).
- Consumes: Task 2's `ProcessInfo`, `LibcCall6`.

- [ ] **Step 1: Write the platform files**

As listed. Add `libc_proc_pidinfo` to `libc_darwin.s` and `.go` the way the three existing trampolines are written. Unexport `LibcCall6`, `Getattrlist`, `Fgetattrlist` again once `FileSecurity` is in.

- [ ] **Step 2: Make operation and cli use them**

- `filesystem_darwin.go` / `_linux.go` stay as the two policy files (their "requires APFS or HFS" / "requires a supported native Linux filesystem" messages differ), each `privateFilesystem` now over `platform.NativeFilesystem`; `IsTerminal` deleted from both, callers use `platform.IsTerminal(file.Fd())`; `renameNoReplace` deleted, callers use `platform.RenameNoReplace`.
- `image_metadata_darwin.go`: `darwinACL` deleted; `metadata`, `parentMetadata`, `linkMetadata` call `platform.FileSecurity(fd, "")` / `(-1, path)` and map an error to `Fail(ExitBlocked, "metadata", "Cannot verify target ACL semantics")`, or for `platform.ErrUnsupportedACL` to `"Unsupported target ACL metadata"`; `aclEntry` → `platform.ACE`; `replaceImage` → `platform.RenameAt`. The test at line 56 calls `platform.FileSecurity(fd, "")`.
- `image_metadata_linux.go`: `attributeNames` deleted; `platform.ExtendedAttributeCount`; `platform.RenameAt`.
- `peerDescendsFrom` moves from `askpass_darwin.go` to `askpass.go`, reading `platform.Peer(conn).PID` and walking `platform.ProcessInfo(pid).PPID`; `askpass_linux.go` keeps `askpassSupported = false` and the `readPassword` stub.

- [ ] **Step 3: Gates**

Run the gate list plus `go test ./internal/operation/` on macOS.

- [ ] **Step 4: Commit**

```bash
git add internal/platform internal/operation internal/cli
git commit -m "refactor(platform): move the file and socket calls and read a process's folder and peer on both systems"
```

---

### Task 4: Platform facts, WSL once, the lint rule and the no-change check

**Files:**
- Create: `internal/platform/facts_darwin.go`, `internal/platform/facts_linux.go`, `internal/machine/effects_darwin.go`, `internal/machine/effects_linux.go`, `internal/machine/host_darwin.go`, `internal/machine/host_linux.go`, `docs/adr/0012-platform-package.md`
- Modify: `internal/machine/dependencies.go:244,301-366,437`, `internal/machine/setup.go:302,308`, `internal/machine/updates.go:58,385-391,465`, `internal/machine/agents.go:169,345-362`, `internal/machine/effects.go:285-312,313-330,414-422`, `internal/machine/answers.go:202,224`, `internal/machine/plan.go:386`, `internal/operation/image.go:392`, `internal/operation/context.go:176-200`, `internal/operation/admin.go:63`, `internal/operation/process.go:162-166`, `internal/costs/claude.go:74`, `.golangci.yml`, `AGENTS.md:108`
- Delete: `machine.isWSL` (`dependencies.go:360-363`)

**Interfaces:**
- Produces in `facts_darwin.go` / `facts_linux.go`, each documented by what it is:
  - `var WSL = sync.OnceValue(func() bool {…})` (Linux reads `/proc/sys/kernel/osrelease` once for `microsoft`; macOS returns false).
  - `func RuntimeFolders(home string) [5]string` (config, data, state, cache, bin defaults from `runtimePaths`).
  - `var HomebrewPrefixes = []string{"/opt/homebrew", "/usr/local"}` / `{"/home/linuxbrew/.linuxbrew"}` and `var HomebrewRepositories = []string{"/opt/homebrew", "/usr/local/Homebrew"}` / `{"/home/linuxbrew/.linuxbrew/Homebrew"}`; `homebrew()` becomes `prefix + "/bin/brew"` over `HomebrewPrefixes` and `homebrewRepository` uses `slices.Contains(platform.HomebrewRepositories, repository)`.
  - `const HasCasks = true` / `false`; `func ToolFolders(home string) []string` (the ordered launcher search list from `findAgent` without the nvm entry; `findAgent` inserts `nvm` after `.bun/bin` with `slices.Insert`); `func VSCodeUserFolder(home string) string`; `const ClaudeManagedFolder = "/Library/Application Support/ClaudeCode"` / `"/etc/claude-code"`; `const GroupFromFolder = true` / `false`; `const TouchIDPossible = true` / `false`; `const AssetTriple = "apple-darwin"` / `"unknown-linux-gnu"`.
- Produces in `machine`: `host_darwin.go` `func hostVersion(ctx context.Context, c operation.Context, result *operation.Component)` (the `sw_vers` branch of `checkPlatform`); `host_linux.go` the os-release branch with `platform.WSL()` for the WSL note (its unused parameters named `_`). `effects_darwin.go`: `var macOSOptionalEffects` moved here (nothing else uses it, so `unused` would fail the Linux lint if it stayed shared), `func platformEffects(Answers) []operation.Effect { return macOSEffects }`, `func platformOptionalEffects() []operation.Effect { return macOSOptionalEffects }`. `effects_linux.go`: `platformEffects` returns `linuxPackagesEffect` plus `linuxEditorEffect` unless `answers["is_wsl"] == true`; `platformOptionalEffects` returns `optionalEffects` when `platform.WSL()`, else nil. **Ruling:** every other effect list stays in `effects.go`: `effects_test.go:49-51,135` names `macOSEffects`, `linuxPackagesEffect`, `linuxEditorEffect`, `windowsHostEffects` and `optionalEffects` together in a test CI runs on darwin. `provisioningEffects` keeps `answers["is_wsl"]` for the editor effect: it is the questionnaire's answer, which the render-check WSL simulation sets.

- [ ] **Step 1: Write the facts and the machine suffix files**

`checkPlatform` keeps the arch check and `Message: platform.System + "/" + runtime.GOARCH`, then calls `hostVersion`.

- [ ] **Step 2: Replace each site**

For each line in the Modify list, replace the test with the platform name spec 3.3 lists; `runtimePaths` drops its refusal (`context.go:177-183`) and reads `platform.RuntimeFolders(home)`; `setup.go:302` uses `platform.System` (same bytes), `:308` `platform.AssetTriple`.

- [ ] **Step 3: Add the lint rule**

In `.golangci.yml`, add `forbidigo` under `linters.enable` and:
```yaml
    forbidigo:
      forbid:
        - pattern: '^runtime\.GOOS$'
          msg: platform tests belong in internal/platform (ADR 0012)
```
(The pattern matches the expression as written in the source; the repository imports `runtime` under its own name everywhere.) Under `linters.exclusions.rules` add `- linters: [forbidigo]` with `path: internal/platform/` and another with `path: internal/release/archive\.go`.

- [ ] **Step 4: Prove the rule fires**

Insert `var _ = runtime.GOOS` in `internal/costs/claude.go`, run `golangci-lint run --concurrency 2 ./internal/costs/`, expect a `forbidigo` finding on that line, remove it. Run `rg -n 'runtime\.GOOS|isWSL' internal`: hits only in `internal/platform/` and `internal/release/archive.go`.

- [ ] **Step 5: Gates and the no-change comparison**

Run the gate list. Repeat Task 1's Steps 1, 3, 4 and 5 into `$WS/after/` (same fixed home path), then `diff -r "$WS/baseline" "$WS/after"` excluding the `workbench-*` binaries and `commit`. Expected: identical doctor lines, `plan_digest` values and render-check output. Any difference is a finding to fix before this task is complete.

- [ ] **Step 6: Docs**

- `AGENTS.md`: the ownership row `| Operating-system calls and platform facts | `internal/platform/` |` above the guard row, and one sentence in the Development contract: platform tests live only there, enforced by `forbidigo`.
- `docs/adr/0012-platform-package.md`, in `0011-release-channels.md`'s layout: Context (the 20 sites and 6 WSL calls before), Decision (spec 3.1-3.5 in prose, with this task's two rulings and `System`'s), Consequences, and the sources from spec section 2's first three bullets plus the trampoline sources from Global Constraints.

- [ ] **Step 7: Commit**

```bash
git add internal .golangci.yml AGENTS.md docs/adr/0012-platform-package.md
git commit -m "refactor(platform): read every platform fact and WSL once from internal/platform"
```

---

### Task 5: Note fields, project names and pairing by folder

**Files:**
- Create: `internal/guard/project.go`
- Modify: `internal/guard/guard.go` (`note`, `proc`), `internal/guard/hooks.go` (`hookInput`, `Pre`), `internal/guard/match.go` (`match`), `internal/guard/watch.go` (`loadArgs`, `seenNote`), `internal/guard/match_test.go`

**Interfaces:**
- Produces:
  - `note` gains `Cwd string \`json:"cwd,omitempty"\`` (the hook's `cwd` after `filepath.EvalSymlinks`, so it compares with the kernel's real path; empty when absent or unresolvable), `Folder string \`json:"folder,omitempty"\`` (the command's leading `cd` target, resolved against `Cwd` and through `EvalSymlinks` in `Pre`; empty when there is none, it is unknown or it does not resolve), `Subagent string \`json:"subagent,omitempty"\``, `Project string \`json:"project,omitempty"\``. All file reads happen in the pre hook, once; the matcher stays pure.
  - `hookInput` gains `Cwd string \`json:"cwd"\``, `AgentType string \`json:"agent_type"\``, `AgentID string \`json:"agent_id"\``; `Pre` sets `Subagent = input.AgentType` only when `input.AgentID != ""`, and `Project = projectName(input.Input.Command, input.Cwd)`.
  - `project.go`: `func commandFolder(command string) (folder, rest string, ok bool)`: a leading `cd PATH &&` or `cd PATH;` where PATH is bare, single- or double-quoted; `ok` is false when PATH starts with `~` or `$` or holds a backquote (the shell would expand it, so the folder is unknown); `rest` is the command after the separator. `func CommandRest(command string) string` (exported: `rest` when `commandFolder` is `ok`, else the command). `func projectName(command, cwd string) string` (spec 6.2, over the `cd` folder when known else `cwd`, resolved with `filepath.Join(cwd, folder)` when relative; empty when nothing is known). `func noteFolders(n note) []string` (not `folders`: `type folders struct` exists in `state.go`): `n.Cwd` and `n.Folder`, `filepath.Clean`ed, duplicates and empties dropped; no I/O.
  - `proc` gains `Folder string` (the kernel's real path, read lazily like `Args`; empty when unread or unreadable).
  - **Ruling (spec 6.3 said "requires"):** the folder prefers, never rejects. In `match`, for each note collect every child `claims` accepts, in age order; take the oldest whose command process's `Folder` is in `noteFolders(n)`; when no candidate's folder is known or matches, take the oldest, as today. A requirement would leave a command untracked, and so unguarded, whenever a folder could not be read or was written through a link, `~` or a variable; a preference only improves the choice. `claims` is unchanged.
  - `loadArgs` reads `platform.Folder` for the processes whose `Args` it reads when any note of that agent has a non-empty `noteFolders(n)`.
  - `seenNote` gains `cwd, project, subagent string` and `at time.Time` (the note's `NotedAt`), for Task 6's `unmatched` record.
- Consumes: Task 3's `platform.Folder`.

- [ ] **Step 1: Write the failing test**

Add `func inFolder(p proc, folder string) proc` beside `zombie`, then:

```go
// Pairing two notes with the same text crosswise would stop the command in
// the other worktree than the one chosen: a SIGTERM nobody asked for.
func TestMatchPrefersTheCommandInTheNotesFolder(t *testing.T) {
	agent := process(100, 1, 100, -3600, "claude")
	login := "/w/.worktrees/feature-login"
	api := "/w/.worktrees/api-server"
	make := claudeShell(`'make test'`)
	// The api-server note is older, its shell is younger: today's rule pairs them crosswise.
	inLogin := inFolder(process(200, 100, 200, 0.4, make...), login)
	inAPI := inFolder(process(201, 100, 201, 0.5, make...), api)
	noteAPI := note{AgentPID: 100, AgentStart: start(-3600), Kind: claude, ToolUseID: "toolu_api",
		Command: "make test", NotedStart: start(0.3), Cwd: api}
	noteLogin := noteAPI
	noteLogin.ToolUseID, noteLogin.Cwd, noteLogin.NotedStart = "toolu_login", login, start(0.35)
	noFolder := noteAPI
	noFolder.ToolUseID, noFolder.Cwd = "toolu_plain", ""
	cdQuoted := noteAPI
	cdQuoted.ToolUseID, cdQuoted.Command, cdQuoted.Cwd, cdQuoted.Folder =
		"toolu_cd", "cd '/w/my repo' && make test", "/w", "/w/my repo"
	cdQuotedShell := claudeShell(`'cd '"'"'/w/my repo'"'"' && make test'`)
	cdTilde := noteAPI
	cdTilde.ToolUseID, cdTilde.Command, cdTilde.Cwd = "toolu_tilde", "cd ~/repo && make test", "/w"
	cdTildeShell := claudeShell(`'cd ~/repo && make test'`)
	for _, tc := range []struct {
		name  string
		notes []note
		procs []proc
		want  map[string]int
	}{
		{"sameTextTwoFolders", []note{noteAPI, noteLogin}, []proc{agent, inLogin, inAPI},
			map[string]int{"toolu_api": 201, "toolu_login": 200}},
		// Without a folder on the note, the oldest shell wins, as today.
		{"noFolderKnown", []note{noFolder}, []proc{agent, inLogin, inAPI},
			map[string]int{"toolu_plain": 200}},
		// A rival whose folder could not be read is not preferred over one that matches.
		{"unreadableRivalLoses", []note{noteAPI},
			[]proc{agent, process(200, 100, 200, 0.4, make...), inAPI},
			map[string]int{"toolu_api": 201}},
		// No candidate matches: the oldest wins, as today, and nothing is rejected.
		{"noMatchFallsBack", []note{noteAPI}, []proc{agent, inLogin},
			map[string]int{"toolu_api": 200}},
		{"quotedFolder", []note{cdQuoted},
			[]proc{agent, inFolder(process(200, 100, 200, 0.4, cdQuotedShell...), "/w"),
				inFolder(process(201, 100, 201, 0.5, cdQuotedShell...), "/w/my repo")},
			map[string]int{"toolu_cd": 201}},
		// The shell sits in cwd until its cd runs, so cwd matches too.
		{"cwdMatchesBeforeTheCd", []note{cdQuoted},
			[]proc{agent, inFolder(process(200, 100, 200, 0.4, cdQuotedShell...), "/elsewhere"),
				inFolder(process(201, 100, 201, 0.5, cdQuotedShell...), "/w")},
			map[string]int{"toolu_cd": 201}},
		// `~` is the shell's to expand: the folder is unknown, cwd still counts.
		{"tildeIsUnknown", []note{cdTilde},
			[]proc{agent, inFolder(process(200, 100, 200, 0.4, cdTildeShell...), "/elsewhere"),
				inFolder(process(201, 100, 201, 0.5, cdTildeShell...), "/w")},
			map[string]int{"toolu_tilde": 201}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := roots(match(snapshotOf(tc.procs...), tc.notes, nil, nil))
			if !maps.Equal(got, tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}
```
No test for `projectName`: the project only labels (spec 6.2), so AGENTS.md admits none.

- [ ] **Step 2: Run it**

Run: `go test ./internal/guard/ -run TestMatchPrefersTheCommandInTheNotesFolder -v`
Expected: FAIL to compile (`Cwd`, `Folder` unknown fields).

- [ ] **Step 3: Implement**

The interfaces above.

- [ ] **Step 4: Tests and gates**

Run: `go test ./internal/guard/` and the gate list. Expected: PASS, including the existing match tests unchanged.

- [ ] **Step 5: Commit**

```bash
git add internal/guard
git commit -m "feat(guard): note each call's folder, project and subagent and prefer the command in the note's folder"
```

---

### Task 6: Memory every tick and the record fields

**Files:**
- Modify: `internal/guard/watch.go` (`watcher` struct, `tick`, `stop`, `callAttrs`, `notes`' `unmatched` event), `internal/guard/handover.go` (`carried`, `adopt`), `internal/guard/logs.go` (`Event`)

**Interfaces:**
- Produces:
  - `type usage struct { Samples []uint64 \`json:"samples"\`; Peak uint64 \`json:"peak"\`; Since time.Time \`json:"since"\` }` with `func (u *usage) add(size uint64)` keeping the last `samplesKept = 120`; `watcher.usage map[string]*usage` by tool use ID, created at `tracking` with `Since: time.Now()`, deleted where `tracked` entries are deleted; `watcher.largestPeak uint64` (since this watcher or its predecessor started; carried).
  - `tick` measures each tracked, alive command's tree (`s.tree(root, outside(protected))`, zombies skipped) with `platform.Resident` after `match` and before `readMemory`; an unreadable process is logged once through `unsized`.
  - `callAttrs(c)` adds `"cwd"`, `"project"`, `"subagent"` when set. `func (w *watcher) endAttrs(c command) []any` appends `"took"`, `time.Since(c.Note.NotedAt).Seconds()` and `"peak"`, the usage's peak; `ended` and the `stopping` record use it; `stopping` also gets `"by", "guard"`. The `unmatched` event takes `took` (`time.Since(seen.at)`), `cwd`, `project`, `subagent` from `seenNote`.
  - `carried` gains `Usage map[string]usage \`json:"usage"\`` and `LargestPeak uint64 \`json:"largest_peak"\``; `adopt` restores both.
  - `logs.go` `Event` gains `Call, Agent, Subagent, Project, Cwd, By, Release, OldRelease, NewRelease string`, `Took float64`, `Peak, Floor uint64`, `NewPID, Process int`, `StillRunning []int`, `Memory struct { Level int \`json:"level"\`; Available, Total uint64 }` tagged `memory`; each tagged with its record key.
- Consumes: Task 2's `platform.Resident`, Task 5's note and `seenNote` fields.

- [ ] **Step 1: Implement**

As listed. `stopLargest` keeps `platform.Footprint` for the decision (spec 6.4).

- [ ] **Step 2: Smoke in dry-run**

Smoke home `H=$(mktemp -d /tmp/wb.XXXX)`, `WORKBENCH_GUARD=dry-run`, every agent variable unset (`env -u CLAUDECODE -u CODEX_CI -u CODEX_THREAD_ID -u CODEX_SANDBOX -u CODEX_SANDBOX_NETWORK_DISABLED`, the list in `internal/operation/plan.go`). The fake agent is a `python3` script (not a shell, so `findAgent` takes it as the agent, and able to start children, which `exec -a claude sleep` is not): it runs `sh -c 'workbench guard pre'` with the PreToolUse JSON on stdin (`hook_event_name`, `tool_name: Bash`, `tool_use_id: toolu_smoke1`, `session_id`, `cwd`, `tool_input.command: "sleep 20"`), then starts `/bin/zsh -c` with Claude Code's command-shell text for `sleep 20` exactly as `claudeShell` in `match_test.go` writes it, since `runs` matches that shape (`match.go:147-160`). Keep this script in the SDD workspace; Tasks 8 to 10 reuse it. Expected: `watch.log`'s `tracking` record carries `cwd` and `project`; the `ended` record carries `took` ≈ 20 and `peak` > 0. Remove `$H` and kill the smoke's processes.

- [ ] **Step 3: Gates and commit**

```bash
git add internal/guard
git commit -m "feat(guard): measure each tracked command every tick and record how long it ran and its peak"
```

---

### Task 7: `workbench guard logs` one line per event

**Files:**
- Modify: `internal/cli/guard.go` (`guardLogsCommand`'s `Long`, `Example`, flags; `showGuardLogs`; `guardEventText` → `guardLine`; `guardEventStyles`; `routineGuardEvents` deleted), `internal/guard/logs.go` (`ReadLog` gains `idle`), `docs/usage.md:212-235`

**Interfaces:**
- Produces: `func guardLine(paint painter, e guard.Event, running bool) string` printing spec section 4: `Jan _2 15:04:05`, the level padded to 5, the event padded to 9, then keys in the order `id agent subagent project by dry_run took size peak floor level available total still_running release old_release new_release new_pid msg error process cmd`. `id` is the first 8 characters of `e.Call` after `toolu_` or `call_`; `cmd` is `guard.CommandRest(e.Command)`. A value is written with `strconv.Quote` when it holds a space, `"`, `=`, `\` or a control character, else bare, the quoting logfmt readers expect ([Log::Fmt grammar](https://metacpan.org/pod/Log::Fmt)); Go's escapes keep a command with a newline on one line. `msg` prints only for `memory`, `refused`, `gave-up`, `error` and `raw`. `took`: one decimal under 10 s, whole seconds under a minute, else Go's `1m12s`. Sizes through `func sizeValue(bytes uint64) string`: `MB` under 1 GB, else `guard.Gigabytes` with its space removed (`2.4GB`), so a value stays one token. Colors through `paint.style`: INFO `faint`, WARN `yellow`, ERROR `red`; the `id` value `lipgloss.Blue`; `took` ≥ 10 s bold; `peak`/`size` ≥ 1 GB `yellow`. `tracking` records are not printed. With `--follow`, a call whose `tracking` is 5 s old with no `ended`/`stopping` since prints one `INFO running` line (`running == true`: no `took`, `peak`).
- `ReadLog(ctx, path, follow, emit, idle func() error)`: `idle` is called once per `followPoll` when following and no line arrived (nil allowed); `showGuardLogs` passes the running-line check, which is how a `running` line can appear without another record arriving.
- The local `--verbose` flag and `routineGuardEvents` go; `Long` and `Example` lose their `--verbose` sentences (the root's global `--verbose` stays accepted and does nothing here); `--since`, `--follow`, `--json` stay. The empty-state message loses its `--verbose` hint.

- [ ] **Step 1: Implement**

- [ ] **Step 2: Check by eye**

In a smoke home with Task 6's `watch.log`: `workbench guard logs`, `NO_COLOR=1 workbench guard logs`, `workbench guard logs --json | head -1`, `workbench guard logs | rg 'id=\w{8}'`, and `workbench guard logs -f` while a 20 s command runs (expect a `running` line after 5 s).

- [ ] **Step 3: Docs**

`docs/usage.md` guard section: the new format, key order, `--verbose` gone, neutral example lines from spec section 4.

- [ ] **Step 4: Gates and commit**

```bash
git add internal/cli/guard.go internal/guard/logs.go docs/usage.md
git commit -m "feat(guard): print the guard log one event per line with an id, project and key=value fields"
```

---

### Task 8: The watcher's socket and a person's stop

**Files:**
- Create: `internal/guard/protocol.go`, `internal/guard/socket.go`, `internal/guard/manual.go`, `internal/guard/client.go`
- Modify: `internal/guard/watch.go` (`watcher` struct, `Watch`, `tick`, `stopLargest`, `signal`, `carryOn`, the exit paths), `internal/guard/handover.go` (`listenerVariable`, `startHandover`, `handOver`'s `quiet`, `inherit`, `carried`), `internal/guard/state.go` (`folders.socket()`), `internal/guard/logs.go` (`SocketPath`), `internal/guard/match_test.go`

**Interfaces:**
- Produces, `protocol.go`:
  - `const ProtocolVersion = 1`; `type message struct { Type string \`json:"type"\`; Versions []int \`json:"versions,omitempty"\`; Version int \`json:"version,omitempty"\`; Release string \`json:"release,omitempty"\`; Error string \`json:"error,omitempty"\`; Snapshot *Snapshot \`json:"snapshot,omitempty"\`; IDs []string \`json:"ids,omitempty"\`; Force bool \`json:"force,omitempty"\`; Results []StopResult \`json:"results,omitempty"\` }`, types `hello`, `error`, `snapshot`, `stop`, `stopped`. **Ruling:** the snapshot's payload nests under `snapshot`, one message type; hello carries no `dry_run`, since dry run is a property of each note, not the watcher.
  - `type Snapshot struct { At time.Time \`json:"at"\`; Memory MemoryView \`json:"memory"\`; LargestPeak uint64 \`json:"largest_peak"\`; Commands []CommandView \`json:"commands"\` }`; `type MemoryView struct { Level string \`json:"level"\`; Installed, Available, Total, SwapFree, SwapTotal uint64 }` with lowercase snake tags (`Level`: `normal`/`warning`/`critical` from macOS's 1/2/4, `normal`/`low` from Linux's `decide`; **Ruling:** the header shows installed memory and swap on macOS and available of total on Linux, the figures the guard already reads, instead of spec 5's "10.5 of 16 GB used", which would need a Mach call the guard has no other use for); `type CommandView struct { ID, Agent, Subagent, Project, Cwd, Command, State, By string; DryRun bool; Started, Ended time.Time; Memory, Peak uint64; History []uint64; Tree []ProcessView }` with tags `id agent subagent project cwd cmd state by dry_run started ended memory peak history tree`; `type ProcessView struct { PID int \`json:"pid"\`; Name string \`json:"name"\`; Memory uint64 \`json:"memory"\` }`; `type StopResult struct { ID string \`json:"id"\`; OK bool \`json:"ok"\`; Error string \`json:"error,omitempty"\` }`. States `running`, `stopping`, `done`, `unmatched`, `stopped`; finished commands stay for `finishedShown = 60 * time.Second`.
- `socket.go`:
  - `type server struct { listener *net.UnixListener; mu sync.Mutex; clients map[*client]struct{}; stops chan stopRequest }`; `type client struct { conn *net.UnixConn; send chan message }` (buffer 4); `type stopRequest struct { ids []string; force bool; peer procKey; reply chan []StopResult }` (`reply` buffered 1, so a tick never blocks on a client that left).
  - `func (w *watcher) listen() error`: when `w.inheritedListener != nil` use it; else remove a stale `guard.sock` and `net.ListenUnix`. `func (s *server) serve()` accepts; per connection: `platform.Peer`; close unless `UID == os.Getuid()`; `platform.ProcessInfo(peer.PID)` for the peer's `procKey` (a PID alone can be reused); read `hello`, answer the highest common version with `Release` or `error` and close; then each `stop` becomes a `stopRequest` pushed without blocking (`error: busy` when `stops` is full). `func (s *server) broadcast(snap Snapshot)`: non-blocking send to each client; a full `send` closes that client. The tick drains `stops` right after `w.agents(s, notes)` (it needs that snapshot, those notes and `protected`) and runs `w.manualStop`; it keeps the last `Snapshot` it built in `w.view` and `Watch` broadcasts it after each tick when `w.server != nil`; no other goroutine touches watcher fields. A request waits at most one tick (`delay(m)` ≤ 1 s), or up to 10 s while the guard's own `signal` polls, during which `signal`'s loop broadcasts a view with the command `stopping`: **Ruling**, no wake descriptor, and `Client.Stop` allows 12 s.
  - Pure, tested: `func stopAllowed(peer procKey, s snapshot, protected map[int]uint64) bool`: the peer must be alive with its start (`s.alive`); walking `peer.PID` and its parents, none may be in `protected` with its start (the agents `w.agents(s, notes)` returns cover notes, tracked commands and the watcher). `func chooseStops(ids []string, tracked map[string]command, s snapshot) (stop []command, results []StopResult)`: an ID not in `tracked` → `{OK: false, Error: "unknown"}`; tracked but root gone or restarted → `"already finished"`; else chosen with `OK: true` (set once signalled).
- `manual.go`:
  - `type manualStop struct { procs []proc; since time.Time; force, forced bool }`; `watcher.manual map[string]*manualStop`.
  - `func (w *watcher) manualStop(req stopRequest, s snapshot, protected map[int]uint64) []StopResult`: refuse all with `"not allowed"` when `!stopAllowed(req.peer, s, protected)`; else for each chosen command, in the order `stop` uses: a dry-run note → log `would stop (dry run)` with `"by", "you"` and answer OK, nothing written or sent; otherwise `targets(s, c.Root, protected)`, write the kill record with `Reason: "Stopped by you (workbench guard memory)."`, log `stopping` with `endAttrs` plus `"by", "you"` and `"size"` from the usage's last sample, send `SIGTERM` (or `SIGKILL` when `force`, logging `forced`) with `platform.Signal` to each target; when no live target was signalled, remove the record (as `stop` does) and answer `OK: false, Error: "already finished"`; else `OK: true` and `w.manual[id] = &manualStop{procs: s.tree(root, outside(protected)), since: now, force: req.force, forced: req.force}`. A person's stop sets no `w.stopped` cooldown and counts toward no `refusals` or `passed`: it was asked for, not chosen. `stopLargest` skips IDs in `w.manual`, so the guard never signals a command a person is already stopping, and `handOver`'s `quiet` also requires `len(w.manual) == 0`, so a stop still owed its SIGKILL is never dropped at a handover.
  - `func (w *watcher) followManual(s snapshot, protected map[int]uint64)`, called each tick after `w.agents`: for each manual stop, `kill, wait := escalation(s, m.procs, protected)` and `m.procs = wait` (the contract: `wait` is what to follow next); `kill` empty → state `stopped`, delete; otherwise when `since` + `killAfter` has passed and `!m.forced`, `SIGKILL` each non-zombie in `kill`, log `forced`, set `m.forced`; when `since` + `giveUp` has passed, log `gave-up` with `still_running` and delete. The state shown is `stopping` while the entry exists.
- Handover: `WORKBENCH_GUARD_HANDOVER` keeps its value, the parent PID (`takeLock`, `handover.go:259-266`, compares the whole value to `os.Getppid()`, in this release and in v0.1.26). A second variable, `const listenerVariable = "WORKBENCH_GUARD_LISTENER"`, set to `6` by `startHandover`, says the listener's file (`listener.File()`, closed in the parent right after `StartDetached`, as `readyWrite` is) rides as descriptor 6; an older release ignores it. `inherit` reads and unsets it: with `6`, `unix.CloseOnExec(6)` (extend the existing loop's bound) and `net.FileListener(os.NewFile(6, "guard.sock"))` ([net.FileListener](https://pkg.go.dev/net#FileListener) returns a copy; close the `os.File` after), kept in `w.inheritedListener`; without it, `listen()` listens fresh. The old watcher, after writing the handover line, calls `SetUnlinkOnClose(false)` and closes its listener: Go's default unlinks the socket file when a listener made by `ListenUnix` closes, which would delete the socket the new watcher serves. Exit paths: idle exit and a signal close the listener (unlink on, the file is this watcher's) **before** `w.release()`, while the lock is still its own; the lost-lock exit (`watch.go:180-183`) closes with unlink off, since another watcher may own the path by then; `carryOn` closes and removes before releasing, clears `w.inheritedListener`, and calls `listen()` again when it re-locks. **Ruling:** a watcher of this release handing over to an older release leaves descriptor 6 open and unused in that process, so `Client.Hello` has a 3 s deadline and the view then says the watcher runs an older release and to run `workbench update`.
- `client.go`: `type Client struct{…}`; `func SocketPath() (string, error)` beside `LogPath` (error when the path exceeds 100 bytes, naming the state folder); `func Dial(ctx context.Context) (*Client, error)`; `func (c *Client) Hello() (release string, err error)` (3 s deadline); `func (c *Client) Snapshots() <-chan Snapshot` (closed on disconnect); `func (c *Client) Stop(ids []string, force bool) ([]StopResult, error)` (12 s deadline); `func (c *Client) Close() error`.
- Consumes: Task 3's `platform.Peer`, `ProcessInfo`; Task 6's `usage`, `endAttrs`; the existing `targets`, `escalation`, `outside`, `kill` record.

- [ ] **Step 1: Write the failing test**

In `match_test.go`:

```go
// A stop nobody with the right to stop it chose, or that an agent's own
// command sends against another agent's work, is an unauthorized SIGTERM.
func TestManualStopsOnlyWhatAPersonMayStop(t *testing.T) {
	agent := process(100, 1, 100, -3600, "claude")
	shell := process(200, 100, 200, 0.4, claudeShell(`'make'`)...)
	s := snapshotOf(agent, shell,
		process(300, 200, 300, 0.5, "workbench", "guard", "memory"), // under the agent's command
		process(400, 1, 400, 0.6, "zsh"), process(401, 400, 401, 0.7, "workbench", "guard", "memory"),
		process(500, 1, 500, -3600, "claude")) // agent-named, left no note
	protected := map[int]uint64{100: start(-3600)}
	for _, tc := range []struct {
		name string
		peer procKey
		want bool
	}{
		{"personAtATerminal", procKey{401, start(0.7)}, true},
		{"underTheAgentsCommand", procKey{300, start(0.5)}, false},
		{"theAgentItself", procKey{100, start(-3600)}, false},
		{"peerExitedPIDReused", procKey{401, start(9)}, false},
		{"unknownPeer", procKey{999, start(0)}, false},
		{"agentWithoutANote", procKey{500, start(-3600)}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := stopAllowed(tc.peer, s, protected); got != tc.want {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
	tracked := map[string]command{
		"toolu_live": {Root: shell},
		"toolu_gone": {Root: process(201, 100, 201, 0.1, "sh")},
	}
	stop, results := chooseStops([]string{"toolu_live", "toolu_gone", "toolu_never"}, tracked, s)
	if len(stop) != 1 || stop[0].Root.PID != 200 {
		t.Fatalf("chosen %v, want only the live command", stop)
	}
	want := []StopResult{{ID: "toolu_live", OK: true}, {ID: "toolu_gone", Error: "already finished"},
		{ID: "toolu_never", Error: "unknown"}}
	if !slices.Equal(results, want) {
		t.Fatalf("results %v, want %v", results, want)
	}
}
```
(The other-user case is closed at accept by the UID check and is not a decider case.)

- [ ] **Step 2: Run it**

Run: `go test ./internal/guard/ -run TestManualStopsOnlyWhatAPersonMayStop -v`
Expected: FAIL to compile.

- [ ] **Step 3: Implement**

As listed. In `Watch`, after `takeLock`: `w.listen()` (an `error` record and `w.server == nil` on failure), then `go w.server.serve()`. After each `tick`, when `w.server != nil`: `w.server.broadcast(w.view)`; `tick` sets `w.view` once it has the snapshot and memory, and leaves the previous view in place on its early error returns.

- [ ] **Step 4: Tests and gates**

Run: `go test ./internal/guard/` and the gate list. Expected: PASS.

- [ ] **Step 5: Smoke**

Smoke home `mktemp -d /tmp/wb.XXXX`, `WORKBENCH_GUARD=dry-run`, Task 6's setup with two 60 s commands. With `nc -U $H/…/guard/guard.sock`: send `{"type":"hello","versions":[1]}`, read the hello and a snapshot; a second connection with `versions:[7]` gets `error`; a third connection sends hello then reads nothing: after four snapshots its buffer is full and the watcher closes it, while `ps` shows the watcher's CPU unchanged and the first connection keeps receiving. Then without dry-run, on a `sleep` the smoke started: `{"type":"stop","ids":["<id>"]}` answers `ok:true` within a second, the next snapshots show `stopping` then `stopped`, and the log has `stopping … by=you`; a stop of the second command while the first is still `stopping` is answered too. Remove the home.

- [ ] **Step 6: Commit**

```bash
git add internal/guard
git commit -m "feat(guard): serve snapshots and take a person's stop over a peer-checked Unix socket"
```

---

### Task 9: `workbench guard memory`

**Files:**
- Create: `internal/cli/guardview.go`
- Modify: `internal/cli/guard.go` (`guardCommand` adds `guardMemoryCommand`), `docs/usage.md`, `docs/superpowers/specs/workbench-contracts.md:137`, `docs/superpowers/specs/2026-10-14-agent-command-guard-design.md` (sections 4, 4.1, 6.1, 6.3, 6.5), `docs/superpowers/specs/2026-10-17-platform-and-guard-view-design.md` (3.3 `System`, 3.4 effect lists, 5 header, 6.3 prefers, 6.5 message shape and handover, 7 `Credentials`), `docs/adr/0013-guard-memory-view.md`

**Interfaces:**
- Produces: `func guardMemoryCommand(o *options) *cobra.Command` (`Use: "memory"`, `Short: "Watch the commands the guard tracks and stop one"`). Before the program: `platform.IsTerminal(os.Stdin.Fd()) && platform.IsTerminal(os.Stdout.Fd())` else `operation.Fail(operation.ExitBlocked, "terminal", "guard memory needs a terminal; use workbench guard logs --follow")`; `operation.AgentSession()` → `operation.Fail(operation.ExitBlocked, "agent", "guard memory is for a person at a terminal")`.
- `guardview.go`: `type guardView struct { client *guard.Client; snap guard.Snapshot; cursor int; tagged map[string]bool; mode viewMode; menu int; notice string; noticeUntil time.Time; width, height int; release string; connected bool; problem string }`; `type viewMode int` (`listMode`, `menuMode`, `detailMode`); messages `snapshotMsg guard.Snapshot`, `disconnectedMsg struct{}`, `stoppedMsg []guard.StopResult`, `tickMsg time.Time` (one per second via `tea.Tick`, for the clock and reconnects); `func waitSnapshot(ch <-chan guard.Snapshot) tea.Cmd` (the Bubble Tea [realtime pattern](https://github.com/charmbracelet/bubbletea/blob/main/examples/realtime/main.go)). Keys, layout, columns, the narrow-window order and the stop menu as spec section 5; the header's memory part per Task 8's ruling, and `dry run: stops are logged, not sent` when any shown command is a dry-run note. The program starts as `runCostsView` does; `view.AltScreen = true`; widths from `terminalWidth`; styles `faint`, `yellow`, `red`, `green` and `glyphs` from `root.go`; the memory graph uses `▁▂▃▄▅▆▇█` over `History` scaled to `Peak`. A `Hello` timeout or `Dial` failure shows spec 5's watcher-not-running text (or the older-release text) and retries on each `tickMsg`.
- Consumes: Task 8's `guard.Dial`, `Client`, `Snapshot`, `CommandView`, `StopResult`.

- [ ] **Step 1: Implement**

- [ ] **Step 2: Smoke on macOS**

Smoke home `mktemp -d /tmp/wb.XXXX`, dry-run first, Task 6's fake agent with two commands in two folders, in a `tmux` session so the window can be resized, every agent variable unset as in Task 6 (this session sets `CLAUDECODE=1`, which the view refuses): rows show project, subagent, memory; Space, `k`, Enter (notice `stopped …`, log `would stop`); Enter on a row for details; Esc; `q`. Then without dry-run, stop a `sleep` the smoke started politely (row goes `stopping` then `stopped`) and another by force. `tmux resize-window -x 50` shows the widen message; `NO_COLOR=1` plain text; `CLAUDECODE=1 workbench guard memory; echo EXIT:$?` → refusal and `3`; `workbench guard memory < /dev/null | cat; echo "EXIT:${pipestatus[1]}"` → terminal refusal and `3`. Record each in the ledger; remove the home.

- [ ] **Step 3: Docs**

- `docs/usage.md`: a `workbench guard memory` paragraph after the `guard logs` block.
- Contracts row 137: the socket (`guard.sock` in the 0700 guard folder, peer UID at accept, a stop refused from under a noted agent, JSON Lines `hello`/`snapshot`/`stop`/`stopped`, a person's stop followed across ticks).
- Guard design: note fields (section 4), record keys and `--verbose` gone (4.1), folder preference (6.1), memory each tick (6.3), the socket and descriptor 6 (6.5).
- This spec: the rulings listed in this task's Files entry, each in place.
- `docs/adr/0013-guard-memory-view.md`: why the watcher serves a socket, the peer checks, the tick-owned state and non-blocking broadcast, the asynchronous manual stop, the folder preference, with spec section 2's Docker, Tailscale, unix(7), jsonlines, htop and Kubernetes sources plus Go by Example, Tendermint ADR 033 and NATS from Global Constraints.

- [ ] **Step 4: Gates and commit**

```bash
git add internal/cli docs
git commit -m "feat(guard): add workbench guard memory, a live view of tracked commands with a stop menu"
```

---

### Task 10: Checks before release

**Files:**
- Modify: `docs/acceptance.md` (the "Agent command guard" section, line 639 on)

- [ ] **Step 1: Linux in Docker**

Build `GOOS=linux` into the workspace; `docker run --rm -it --memory 2g --init -e HOME=/tmp/h -e WORKBENCH_GUARD=dry-run -v "$WS/linux:/b:ro" ubuntu:24.04` under `script` for a terminal; repeat Task 9 Step 2 (dry run, then real stops of the container's own `sleep` processes); then a handover: start the watcher from one build, replace the `workbench` link with a second build, and watch the view reconnect with its rows and peaks kept.

- [ ] **Step 2: Real agents, two worktrees**

Owner-authorized only: the same command in two worktrees from Claude Code (one via a subagent) and from Codex; rows carry the right project and subagent; a stop hits the chosen row. If not authorized, the acceptance text says it was not run.

- [ ] **Step 3: Cost**

Ten tracked `sleep` commands for one minute; sample the watcher's and the view's `ps -o %cpu,rss -p PID` every 10 s.

- [ ] **Step 4: Acceptance and commit**

Replace the guard section of `docs/acceptance.md` with what was observed, impersonally, then:
```bash
git add docs/acceptance.md
git commit -m "docs(acceptance): record the guard memory view and platform package checks"
```

---

## Self-review notes

- Spec coverage: 3.1-3.5 → Tasks 2-4; 3.6 → Tasks 1 and 4; 4 → Task 7; 5 → Task 9; 6.1-6.3 → Task 5; 6.4 → Task 6; 6.5 → Task 8; 7 → each task's Interfaces; 8 → Tasks 5 and 8 (three spec tests merged into two tables); 9 → Task 10; 10 → Tasks 4, 7, 9, 10.
- Rulings against the spec's text, each amended in Task 9: `System` keeps the Go name; effect lists stay shared and only the choice is per suffix; the folder prefers rather than requires; the snapshot message nests its payload and hello carries no `dry_run`; the header shows the memory the guard already reads; a stop request waits one tick; a person's stop is followed across ticks with no cooldown; the handover names what it passed and `Hello` times out.
- Second review (Oct 17, 2026, 18 findings) applied: `noteFolders` avoids the `folders` type; the handover announces the listener in its own variable, since `takeLock` compares the whole handover value to the parent PID; unlink-on-close is turned off before the old watcher closes, and the lost-lock exit never removes the file; builds for the no-change check carry no VCS stamp; a dry-run manual stop writes no record and a refused one removes it; the guard's chooser skips a person's stop and the handover waits for it; `followManual` stores `escalation`'s `wait`; the guard's own 10 s poll broadcasts and `Client.Stop` allows 12 s; the pairing cases each have a rival in the wrong folder and the `cd` folder is resolved in the pre hook; the smoke's fake agent is a python3 script running Claude Code's shell shape with the agent variables unset; sizes print as one token.
- First review (Oct 17, 2026, 22 findings) applied: manual stops no longer go through `stop`'s memory re-check or its 10 s wait; libc moves with `Footprint`; the folder is a preference with links resolved and `~`/`$` unknown; descriptor 6 is passed only when announced and never a live socket's victim; the pairing test now fails on today's code; the peer rides the request with its start time; the socket is removed before the lock is released; smoke homes are short; the filesystem policy files stay two; the baseline home path is fixed; protocol JSON tags are pinned; `seenNote` carries the new fields; `ReadLog` has an idle callback; `macOSOptionalEffects` moves with its only user; the `start()` constant stays untyped and line 408 is updated; no `send` field exists for tests.
