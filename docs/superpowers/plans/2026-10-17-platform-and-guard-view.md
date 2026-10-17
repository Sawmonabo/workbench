# Platform Package, Guard Log Format and Live Memory View Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Gather every operating-system call and platform fact into `internal/platform`, then print the guard log one event per line and add `workbench guard memory`, a live view of the commands the guard tracks from which a person can stop them.

**Architecture:** `internal/platform` has one `_darwin.go` and one `_linux.go` file per subject, each exporting the same names, and imports only the standard library and `golang.org/x/sys`; the policy around each call stays with its owner. The watcher measures each tracked command every tick, serves snapshots and takes stop requests over a Unix socket in the guard's private folder, checking each peer's credentials; the view is a Bubble Tea program in `internal/cli` that reuses the costs view's full-screen pattern.

**Tech Stack:** Go 1.26, `golang.org/x/sys/unix`, Bubble Tea v2.0.10, Lip Gloss v2, Bubbles v2, `log/slog` JSON Lines, golangci-lint v2.14 (`forbidigo`).

**Spec:** `docs/superpowers/specs/2026-10-17-platform-and-guard-view-design.md` (the "guard design" it cites is `docs/superpowers/specs/2026-10-14-agent-command-guard-design.md`).

## Global Constraints

- Platform code goes in `_darwin.go` / `_linux.go` files, never behind a run-time test: the Go toolchain builds a file whose name ends in `_GOOS` only for that system ([go/build](https://pkg.go.dev/cmd/go/internal/help@go1.26.4)). There is no third file for another system (spec 3.2).
- `internal/platform` imports only the standard library and `golang.org/x/sys` (spec 3.1). Every exported name exists in both platform files with the same signature.
- WSL is detected once, with `sync.OnceValue`, which "returns a function that invokes f only once" ([sync](https://pkg.go.dev/sync#OnceValue)); the pattern is Tailscale's [distro](https://pkg.go.dev/tailscale.com@v1.98.3/version/distro) package.
- `runtime.GOOS` appears only in `internal/platform` and `internal/release/archive.go`, enforced by `forbidigo` (spec 3.5). `runtime.GOARCH` is not restricted.
- Doc comments: "Every exported (capitalized) name should have a doc comment" ([Go Doc Comments](https://go.dev/doc/comment)), starting with the name; `revive`'s `exported` and `package-comments` rules are on in `.golangci.yml`. Unexported names that carry a decision get one too, in the repository's style.
- Line length: "There is no rigid line length limit in Go code, but avoid uncomfortably long lines" ([Code Review Comments](https://go.dev/wiki/CodeReviewComments)); here `golines` wraps at 100 and `lll` fails at 120 (`.golangci.yml`). A user-facing string over 50 characters stays whole.
- Package names are short, lowercase, one word; "Avoid uninformative package names like `util`" ([Google Go Style Decisions](https://google.github.io/styleguide/go/decisions)). Supporting packages live under `internal/` so nothing outside the module imports them ([Organizing a Go module](https://go.dev/doc/modules/layout)).
- Tests: AGENTS.md admits one only for catastrophic data loss, credential exposure or unauthorized execution, explained beside it. The platform move adds none (spec 3.6). The guard's new tests go in the existing `internal/guard/match_test.go` style: pure deciders over fixed snapshots built with its `process`, `snapshotOf`, `claudeShell` and `start` helpers; cases that share a decider go in one table-driven test with `t.Run` ([TableDrivenTests](https://go.dev/wiki/TableDrivenTests)). No test opens a real socket or signals a real process.
- Reuse first: `operation.AgentSession`, `operation.StartDetached`, `operation.InheritLock`, `guard.ReadLog`, `guard.Gigabytes`, `parseTimeFlag`, `newPainter`, `lineWidth`, `terminalWidth`, `costsView`'s program options, `view.AltScreen` (spec 7). No new module.
- Gates after every task, from the worktree root: `golangci-lint fmt ./...`, `golangci-lint config verify`, `golangci-lint run --concurrency 2 ./...`, `GOOS=linux golangci-lint run --concurrency 2 ./...`, `go build ./...`, `GOOS=linux go build ./...`, `GOOS=linux go vet ./...` (compiles the Linux test files), `go test ./...`. CI lints the darwin target only, so the Linux lint runs here.
- Smokes only in disposable homes (`env -i HOME=<folder under the scratchpad>`) or in a memory-capped Docker container with no home or credential mounts, with `WORKBENCH_GUARD=dry-run` first; kill only processes the smoke started; never `workbench apply` on the Mac.
- Commit subjects are conventional (`refactor(platform): …`, `feat(guard): …`, `docs(guard): …`); each commit ends with `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`. Never edit `CHANGELOG.md`.
- Tracked docs name no person's machine, project, worktree or call ID; examples use neutral names (`feature-login`, `api-server`) and made-up IDs.
- Times in docs and acceptance notes are 12-hour, America/New_York.

## Review Focus

1. **A slow or stuck view must never slow the watcher.** A client that stops reading snapshots is disconnected, and a stop request is run by the tick, never by the connection's goroutine. Pinned by Task 8's `broadcast` design (non-blocking send, a full channel closes the client) and its Step 5 smoke, which leaves one connection unread for a minute while the watcher keeps ticking.
2. **A manual stop during a memory stop's 10 s wait.** The second stop waits for the tick; it is not lost and does not run concurrently. Pinned by Task 8's design (stops are a channel the tick drains) and its smoke step.
3. **A watcher handover while a view is connected.** The view sees one disconnect, reconnects within a second, and the rows keep their peak. Pinned by Task 8's carried `peak` and `since` and Task 10's smoke.
4. **Hook input without `cwd`.** An older agent gives no `cwd`: `project` is empty, the row shows `—`, and pairing falls back to today's rule. Pinned by Task 5's test case `noFolderKnown`.
5. **A `cd` to a quoted path.** `cd '/w/my repo' && make` strips the `cd` and labels the project `my repo`. Pinned by Task 5's test case `quotedFolder`.

---

### Task 1: Baseline for the no-behavior-change check

**Files:**
- Create: `.superpowers/sdd/<workspace>/baseline/` (git-ignored scratch; the SDD workspace printed by `scripts/sdd-workspace`)

**Interfaces:**
- Produces: `baseline/darwin-doctor.txt`, `baseline/darwin-plan.json`, `baseline/linux-doctor.txt`, `baseline/linux-plan.json`, `baseline/render-check-wsl.txt`, each the output of the commands below on the commit before Task 2. Task 4 compares against them.

- [ ] **Step 1: Build the current code for both systems**

Run: `go build -o "$WS/baseline/workbench-darwin" ./cmd/workbench && GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -o "$WS/baseline/workbench-linux" ./cmd/workbench`
Expected: both files exist (`$WS` is the workspace folder).

- [ ] **Step 2: Capture macOS in a disposable home**

Run, with `H=$(mktemp -d "$WS/home.XXXX")`:
```bash
env -i HOME="$H" PATH=/usr/bin:/bin TERM=dumb "$WS/baseline/workbench-darwin" doctor > "$WS/baseline/darwin-doctor.txt" 2>&1; echo "EXIT:$?"
env -i HOME="$H" PATH=/usr/bin:/bin "$WS/baseline/workbench-darwin" init --answers-from "$WS/baseline/answers.toml" --dry-run --json --local-build > "$WS/baseline/darwin-plan.json"; echo "EXIT:$?"
```
`answers.toml` is a `[data]` table with `machine_role = "personal"` and the keys `home/.chezmoi.toml.tmpl` prompts for (read the template's `promptChoiceOnce`/`promptStringOnce` calls and give each a fixed value). Keep the same file for Linux. Expected: both commands exit 0 or 3 and the files are non-empty; the exit codes are recorded beside them.

- [ ] **Step 3: Capture Linux in Docker**

Run: `docker run --rm --memory 2g -v "$WS/baseline:/b:ro" -v "$PWD:/src:ro" -e HOME=/tmp/h -w /src ubuntu:24.04 sh -c 'mkdir -p /tmp/h && /b/workbench-linux doctor; echo "EXIT:$?"' > "$WS/baseline/linux-doctor.txt" 2>&1` and the same with `init --answers-from /b/answers.toml --dry-run --json --local-build` into `linux-plan.json` (`--local-build` is a hidden root flag that needs the checkout, hence `-w /src`; the mount is read-only and the home is the container's own).
Expected: files written; the plan JSON holds a `plan_digest`.

- [ ] **Step 4: Capture the WSL simulation**

Run: `scripts/render-check.sh personal pinned wsl > "$WS/baseline/render-check-wsl.txt" 2>&1; echo "EXIT:$?"`
Expected: `EXIT:0`.

- [ ] **Step 5: Record the base commit**

Run: `git rev-parse HEAD > "$WS/baseline/commit"`. Nothing is committed to git in this task.

---

### Task 2: `internal/platform` processes, memory, signals and the guard move

**Files:**
- Create: `internal/platform/platform.go`, `internal/platform/process.go`, `internal/platform/memory.go`, `internal/platform/process_darwin.go`, `internal/platform/process_linux.go`, `internal/platform/memory_darwin.go`, `internal/platform/memory_linux.go`
- Modify: `internal/guard/guard.go` (`proc`), `internal/guard/memory.go` (`memory`, new `readMemory`), `internal/guard/watch.go` (`readMemory`, `footprint`, `send`, `exited`, `waiter` calls), `internal/guard/hooks.go` (`processInfo`), `internal/guard/match.go` (`startSlack`), `internal/guard/match_test.go` (`start`, `process`)
- Delete: `internal/guard/platform_darwin.go`, `internal/guard/platform_linux.go`

**Interfaces:**
- Produces, in `package platform`:
  - `platform.go`: the package doc comment (what it owns, the two-file rule, that it imports only the standard library and x/sys), `const System string` (`"darwin"` / `"linux"`, the Go name, so `checkPlatform`'s message bytes do not change; spec 3.3 said `"macos"`, ruled against for spec 3.6), `const StartSecond uint64` (one second in `Process.Start`'s units: `1_000_000` on macOS, `100` on Linux; replaces guard's `startSlack`).
  - `process.go` (shared): `type Process struct { PID, PPID, PGID int; Start uint64; Name string; Zombie bool }` with the doc comment from guard's `proc` (Start names the process with the PID); `func Exited(err error) bool` (ENOENT or ESRCH: the process is gone; moved from guard's `exited`); `type Waiter` doc comment.
  - `process_darwin.go` / `process_linux.go`: `func Processes(unreadable func(pid int, err error)) ([]Process, error)` (from `snapshotNow`; macOS ignores `unreadable`), `func ProcessInfo(pid int) (Process, error)`, `func Args(pid int) []string`, `func Environ(pid int, name string) string` (the value of one variable in the environment the process started with; generalizes `readCallID`), `func Footprint(pid int) (uint64, error)` (physical footprint / `smaps_rollup` Pss, as today), `func Resident(pid int) (uint64, error)` (macOS: the physical footprint again, as spec 6.4 says; Linux: `/proc/PID/statm` field 2 × `os.Getpagesize()`), `func Signal(p Process, group bool, sig unix.Signal) bool` (from `send`; `p` carries PID and Start), `type Waiter struct` with `func NewWaiter() *Waiter`, `func (w *Waiter) Watch(pid int)`, `func (w *Waiter) Wait(d time.Duration)`, `func (w *Waiter) Close()` (from `waiter`).
  - `memory_darwin.go` / `memory_linux.go`: `type Memory struct { Level int; Available, Total, Installed, SwapFree, SwapTotal uint64; FullAvg10 float64 }` declared once in the shared `memory.go` with guard's doc comment; `func MachineMemory() (Memory, error)` (macOS: `readMemory`'s body; Linux: `/proc/meminfo` and `/proc/pressure/memory` only, without the cgroup choice); `func CgroupMemory(machine Memory) (Memory, bool)` (Linux: `confined`; macOS: `return Memory{}, false`, documented: macOS has no cgroups).
- Consumes: nothing new.

- [ ] **Step 1: Write the platform files**

Move the bodies named above out of `internal/guard/platform_darwin.go` and `platform_linux.go`, renaming as listed; keep every existing comment, adjusted to the new names. `Folder` comes in Task 3, where the macOS trampoline it needs is written. `Processes` returns `[]Process`; the map is guard's.

- [ ] **Step 2: Make the guard use them**

- `guard.go`: `type proc struct { platform.Process; Args []string; CallID string }`; `snapshot` unchanged. Add `func fromProcess(p platform.Process) proc { return proc{Process: p} }` only if a one-liner is clearer than the literal at the call sites; otherwise write the literal.
- `watch.go`: `func (w *watcher) snapshot() (snapshot, error)` calls `platform.Processes` and builds the map; `readMemory()` moves to `memory.go` (below); `footprint` → `platform.Footprint`; `send(t, sig)` → `platform.Signal(platform.Process{PID: t.PID, Start: t.Start}, t.Group, sig)`; `exited` → `platform.Exited`; `waiter` → `*platform.Waiter`; `loadArgs` reads `platform.Args` and `platform.Environ(pid, callIDVariable)`.
- `memory.go`: `type memory platform.Memory`; `func readMemory() (memory, error)` reads `platform.MachineMemory`, then `platform.CgroupMemory(machine)` and keeps the cgroup reading under the exact condition `readMemory` in `platform_linux.go` uses today (`decide` compared on both, then `availablePercent`). The condition is guard policy and runs on both systems; on macOS `CgroupMemory` says no.
- `match.go`, `match_test.go`, `hooks.go`: `startSlack` → `platform.StartSecond`; `processInfo` → `platform.ProcessInfo`; `process()` in the test builds `proc{Process: platform.Process{…}, Args: args}`.

- [ ] **Step 3: Run the gates**

Run the Global Constraints gate list. Expected: all pass; `go test ./internal/guard/` passes unchanged.

- [ ] **Step 4: Commit**

```bash
git add internal/platform internal/guard
git commit -m "refactor(platform): move the guard's process, memory and signal calls into one package"
```

---

### Task 3: Platform file calls, libc trampolines, peer credentials and the operation move

**Files:**
- Create: `internal/platform/file_darwin.go`, `internal/platform/file_linux.go`, `internal/platform/socket_darwin.go`, `internal/platform/socket_linux.go`, `internal/platform/libc_darwin.go`, `internal/platform/libc_darwin.s`
- Modify: `internal/platform/process_darwin.go` (`Folder`), `internal/platform/process_linux.go` (`Folder`), `internal/operation/filesystem_darwin.go`, `internal/operation/filesystem_linux.go`, `internal/operation/image_metadata_darwin.go`, `internal/operation/image_metadata_linux.go`, `internal/operation/image_metadata_darwin_test.go:56`, `internal/operation/askpass_darwin.go`, `internal/operation/state.go:340,366,496`, `internal/operation/context.go:239`, `internal/operation/process.go:231,374,505`, `internal/cli/progress.go:21`, `internal/cli/planview.go:164`, `internal/cli/costs.go:172-173`
- Delete: `internal/operation/libc_darwin.go`, `internal/operation/libc_darwin.s`, `internal/operation/askpass_other.go` (rename to `askpass_linux.go`)

**Interfaces:**
- Produces:
  - `file_*.go`: `func IsTerminal(fd uintptr) bool`; `func NativeFilesystem(path string) (bool, error)` (walks up to the first existing ancestor as `privateFilesystem` does; true for `apfs`/`hfs` on macOS and ext, XFS, Btrfs, tmpfs, overlay magic numbers on Linux; the error is the statfs failure); `func RenameNoReplace(from, to string) error`; `func RenameAt(fd int, from, to string, exclusive bool) error` (from `replaceImage`); `type ACE struct { Flags, Rights uint32 }`; `func FileSecurity(fd int, path string) (entries []ACE, present bool, err error)` (macOS: `darwinACL`, `path != ""` selects `getattrlist`, plain errors `errUnsupportedACL` / the errno; Linux: `nil, false, nil`, documented: Linux keeps POSIX ACLs as extended attributes, which `ExtendedAttributeCount` counts); `func ExtendedAttributeCount(fd int, path string) (int, error)` (`Flistxattr` on `fd`, `Llistxattr` on `path` when `fd < 0`, through `attributeNames`'s two-call read on both systems).
  - `libc_darwin.go` / `.s`: `libcCall6`, `getattrlist`, `fgetattrlist`, `procPidRusage` moved whole, plus a new `procPidinfo` trampoline for `proc_pidinfo`.
  - `process_*.go`: `func Folder(pid int) (string, error)`. macOS: `proc_pidinfo(pid, PROC_PIDVNODEPATHINFO, 0, &buf, len)`; `go doc golang.org/x/sys/unix | rg -i pidinfo` shows no wrapper (checked Oct 17), so it goes through `libcCall6`. Read `sys/proc_info.h` under `$(xcrun --show-sdk-path)/usr/include` for the flavor's value and the byte offset of `pvi_cdir.vip_path` in `struct proc_vnodepathinfo`, and write both as named constants with the header line quoted in the comment; never guess them.
  - `socket_*.go`: `type Credentials struct { UID, PID int }`; `func Peer(conn *net.UnixConn) (Credentials, error)` (macOS: `unix.GetsockoptXucred(fd, unix.SOL_LOCAL, unix.LOCAL_PEERCRED)` for the UID, which `Xucred` holds without a PID, and `unix.GetsockoptInt(fd, unix.SOL_LOCAL, unix.LOCAL_PEERPID)` for the PID; Linux: `unix.GetsockoptUcred(fd, unix.SOL_SOCKET, unix.SO_PEERCRED)`, `Ucred{Pid, Uid, Gid}`), through `conn.SyscallConn().Control`.
- Consumes: Task 2's `ProcessInfo` for `peerDescendsFrom`.

- [ ] **Step 1: Write the platform files**

Move and rename as listed. `FileSecurity`'s unsupported-layout error is `var ErrUnsupportedACL = errors.New("unsupported ACL layout")` so operation can test it.

- [ ] **Step 2: Make operation and cli use them**

- `filesystem_darwin.go` / `_linux.go`: keep `privateFilesystem(path) error` as the policy (the two `Fail(ExitBlocked, "filesystem", …)` messages) over `platform.NativeFilesystem`; both files now hold the same body, so merge them into `filesystem.go` (no suffix) and delete the pair. `renameNoReplace` callers call `platform.RenameNoReplace`; `IsTerminal(file)` callers call `platform.IsTerminal(file.Fd())` and `operation.IsTerminal` is deleted.
- `image_metadata_darwin.go`: `darwinACL` deleted; `metadata`, `parentMetadata`, `linkMetadata` call `platform.FileSecurity(fd, "")` / `(-1, path)` and map an error to `Fail(ExitBlocked, "metadata", "Cannot verify target ACL semantics")` or, for `platform.ErrUnsupportedACL`, `"Unsupported target ACL metadata"`; `aclEntry` → `platform.ACE`, fields `Flags`/`Rights`; `replaceImage` → `platform.RenameAt`. The test at line 56 calls `platform.FileSecurity(fd, "")`.
- `image_metadata_linux.go`: `attributeNames` deleted; `fileMetadata`/`linkMetadata` call `platform.ExtendedAttributeCount(fd, "")` / `(-1, path)`; `replaceImage` → `platform.RenameAt`.
- `askpass_darwin.go`: `peerDescendsFrom` reads `platform.Peer(conn)` and walks `platform.ProcessInfo(pid).PPID`; it moves to `askpass.go` (shared) since nothing in it is macOS-only now. `askpass_other.go` becomes `askpass_linux.go` holding `askpassSupported = false` and `readPassword`'s stub, and the `//go:build !darwin` line goes.

- [ ] **Step 3: Run the gates**

Run the Global Constraints gate list, plus `go test ./internal/operation/` on macOS (the ACL test). Expected: all pass.

- [ ] **Step 4: Commit**

```bash
git add internal/platform internal/operation internal/cli
git commit -m "refactor(platform): move the file, libc and socket calls and read peer credentials on both systems"
```

---

### Task 4: Platform facts, WSL once, the lint rule and the no-change check

**Files:**
- Create: `internal/platform/facts_darwin.go`, `internal/platform/facts_linux.go`, `internal/machine/effects_darwin.go`, `internal/machine/effects_linux.go`, `internal/machine/host_darwin.go`, `internal/machine/host_linux.go`, `docs/adr/0012-platform-package.md`
- Modify: `internal/machine/dependencies.go:244,301-366,437`, `internal/machine/setup.go:302,308`, `internal/machine/updates.go:58,465`, `internal/machine/agents.go:169,345-362`, `internal/machine/effects.go:285-312,414-422`, `internal/machine/answers.go:202,224`, `internal/machine/plan.go:386`, `internal/operation/image.go:392`, `internal/operation/context.go:176-200`, `internal/operation/admin.go:63`, `internal/operation/process.go:162-166`, `internal/costs/claude.go:74`, `.golangci.yml`, `AGENTS.md:108`
- Delete: `machine.isWSL` (`dependencies.go:360-363`)

**Interfaces:**
- Produces, in `facts_darwin.go` / `facts_linux.go` (each a constant or a function of `home`, documented by what it is, not by which system):
  - `var WSL = sync.OnceValue(func() bool {…})`: Linux reads `/proc/sys/kernel/osrelease` once for `microsoft`; macOS `func() bool { return false }`.
  - `func RuntimeFolders(home string) [5]string` (config, data, state, cache, bin defaults; the bodies from `runtimePaths`).
  - `var HomebrewRepositories = []string{…}` (`/opt/homebrew`, `/usr/local/Homebrew` / `/home/linuxbrew/.linuxbrew/Homebrew`) and `var HomebrewPrefixes = []string{…}` (`/opt/homebrew`, `/usr/local` / `/home/linuxbrew/.linuxbrew`), used by `homebrew()` (`updates.go:385`, `prefix + "/bin/brew"`) and `homebrewRepository`.
  - `const HasCasks = true/false`.
  - `func ToolFolders(home string) []string`: the whole ordered launcher search list from `findAgent` without the nvm entry (macOS: `.bun/bin`, `.cargo/bin`, `/usr/local/go/bin`, `go/bin`, `.local/bin`, `/opt/homebrew/bin`, `/usr/local/bin`, `/usr/bin`, `/bin`; Linux: `bin` first, then the same with `/home/linuxbrew/.linuxbrew/bin` for `/opt/homebrew/bin`). `findAgent` inserts `nvm` after `.bun/bin` with `slices.Insert`.
  - `func VSCodeUserFolder(home string) string`.
  - `const ClaudeManagedFolder string`.
  - `const GroupFromFolder = true/false`, `const TouchIDPossible = true/false`, `const AssetTriple = "apple-darwin"/"unknown-linux-gnu"`.
- Produces in `machine`: `host_darwin.go` `func hostVersion(ctx context.Context, c operation.Context, result *operation.Component)` (the `sw_vers` branch of `checkPlatform`) and `host_linux.go` (the os-release branch, with `platform.WSL()` for the WSL note); `effects_darwin.go` `func platformEffects(answers Answers) []operation.Effect { return macOSEffects }` and `func platformOptionalEffects() []operation.Effect { return macOSOptionalEffects }`; `effects_linux.go` returning `linuxPackagesEffect` plus `linuxEditorEffect` unless `answers["is_wsl"] == true`, and `optionalEffects` when `platform.WSL()`, else nil. **Ruling:** the effect lists themselves stay in `effects.go`: `effects_test.go` lines 49-51 and 135 name `macOSEffects`, `linuxPackagesEffect` and `optionalEffects` together in one test that CI runs on darwin, so a list hidden behind a suffix would not compile there; spec 3.4's "each define the platform's effect list" becomes "each choose from the shared lists", noted in ADR 0012. `provisioningEffects` keeps branching on `answers["is_wsl"]` for the editor effect: that is the questionnaire's answer, which the render-check WSL simulation sets, not a host fact.

- [ ] **Step 1: Write the facts files and the machine suffix files**

As listed. `checkPlatform` keeps the arch check and `Message: platform.System + "/" + runtime.GOARCH`, then calls `hostVersion`.

- [ ] **Step 2: Replace each site**

For each line in the Modify list, replace the `runtime.GOOS` or `isWSL()` test with the platform name listed in spec 3.3; `runtimePaths` drops its refusal (`context.go:177-183`) and reads `platform.RuntimeFolders(home)`; `setup.go:302` uses `platform.System` (same bytes) and `:308` `platform.AssetTriple`.

- [ ] **Step 3: Add the lint rule**

In `.golangci.yml`, add `forbidigo` under `linters.enable`, and:
```yaml
    forbidigo:
      forbid:
        - pattern: '^runtime\.GOOS$'
          msg: platform tests belong in internal/platform (ADR 0012)
```
(`analyze-types` defaults to true, so the qualified name matches.) Under `linters.exclusions.rules` add `- linters: [forbidigo]` with `path: internal/platform/` and another with `path: internal/release/archive\.go`.

- [ ] **Step 4: Prove the rule fires**

Insert `var _ = runtime.GOOS` (with the import) in `internal/costs/claude.go`, run `golangci-lint run --concurrency 2 ./internal/costs/`, expect a `forbidigo` finding naming the line, then remove it. Run `rg -n 'runtime\.GOOS|isWSL' internal` and expect hits only in `internal/platform/` and `internal/release/archive.go`.

- [ ] **Step 5: Run the gates and the no-change comparison**

Run the Global Constraints gate list. Then repeat Task 1's Steps 1-4 into `$WS/after/` and run `diff -r "$WS/baseline" "$WS/after"` excluding the `workbench-*` binaries and `commit`. Expected: no difference in the doctor lines, the `plan_digest` values and the render-check output (a timestamp line, if any, is the only allowed difference; name it in the ledger).

- [ ] **Step 6: Docs**

- `AGENTS.md`: add the ownership row `| Operating-system calls and platform facts | `internal/platform/` |` above the guard row, and in the Development contract add one sentence: platform tests live only there, enforced by `forbidigo`.
- `docs/adr/0012-platform-package.md`: Status, Context (the 20 sites and 6 WSL calls before), Decision (spec 3.1-3.5 in prose, with the two rulings above), Consequences, and the sources from spec section 2's first three bullets. Follow `docs/adr/0011-release-channels.md`'s layout.

- [ ] **Step 7: Commit**

```bash
git add internal .golangci.yml AGENTS.md docs/adr/0012-platform-package.md
git commit -m "refactor(platform): read every platform fact and WSL once from internal/platform"
```

---

### Task 5: Note fields, project names and pairing by folder

**Files:**
- Modify: `internal/guard/guard.go` (`note`, `proc`), `internal/guard/hooks.go` (`hookInput`, `Pre`), `internal/guard/match.go` (`claims`, new `project.go` helpers may live here or in a new `internal/guard/project.go`), `internal/guard/watch.go` (`loadArgs`), `internal/guard/match_test.go`
- Create: `internal/guard/project.go`

**Interfaces:**
- Produces:
  - `note` gains `Cwd string \`json:"cwd,omitempty"\``, `Subagent string \`json:"subagent,omitempty"\``, `Project string \`json:"project,omitempty"\``.
  - `hookInput` gains `Cwd string \`json:"cwd"\``, `AgentType string \`json:"agent_type"\``, `AgentID string \`json:"agent_id"\``; `Pre` sets `Subagent = input.AgentType` only when `input.AgentID != ""` (a subagent's call), and `Project = projectName(input.Input.Command, input.Cwd)`.
  - `project.go`: `func commandFolder(command string) (folder, rest string, ok bool)` (a leading `cd PATH &&` or `cd PATH;`, PATH bare, single- or double-quoted, `~` kept as written; `rest` is the command without it); `func projectName(command, cwd string) string` (spec 6.2: the folder after a `.worktrees/` or `worktrees/` component, else the nearest folder at or above holding `.git` (file or folder), else the last folder name; resolved with `filepath.Join(cwd, folder)` when relative; empty when both are empty); `func folderOf(n note) string` (the `cd` folder resolved against `Cwd`, else `Cwd`); `func CommandRest(command string) string` (exported: the command without its leading `cd PATH &&`, for the log line and the view).
  - `proc` gains `Folder string` (read lazily like `Args`, empty when unread or unreadable).
  - `claims(s snapshot, n note, agent, child proc, earliest uint64, setsCallIDs bool) bool` adds, after the shape check: when `folderOf(n) != ""` and the command process's `Folder != ""`, both must be equal after `filepath.Clean`.
  - `loadArgs` reads `platform.Folder` for the agent's direct children and the first process below a sandbox launcher (the same set whose `Args` it reads) when any note of that agent has a folder.
- Consumes: Task 3's `platform.Folder`.

- [ ] **Step 1: Write the failing test**

Add to `match_test.go`, beside `zombie`, `func inFolder(p proc, folder string) proc` setting `Folder`. Add one table test:

```go
func TestMatchPairsTheSameCommandByFolder(t *testing.T) {
	agent := process(100, 1, 100, -3600, "claude")
	a := inFolder(process(200, 100, 200, 0.4, claudeShell(`'make test'`)...), "/w/.worktrees/feature-login")
	b := inFolder(process(201, 100, 201, 0.5, claudeShell(`'make test'`)...), "/w/.worktrees/api-server")
	noteA := note{AgentPID: 100, AgentStart: start(-3600), Kind: claude, ToolUseID: "toolu_a",
		Command: "make test", NotedStart: start(0.3), Cwd: "/w/.worktrees/feature-login"}
	noteB := noteA
	noteB.ToolUseID, noteB.Cwd, noteB.NotedStart = "toolu_b", "/w/.worktrees/api-server", start(0.35)
	for _, tc := range []struct {
		name  string
		notes []note
		procs []proc
		want  map[string]int
	}{
		{"sameTextTwoFolders", []note{noteA, noteB}, []proc{agent, a, b},
			map[string]int{"toolu_a": 200, "toolu_b": 201}},
		{"noteBFirstStillPairsByFolder", []note{noteB, noteA}, []proc{agent, a, b},
			map[string]int{"toolu_a": 200, "toolu_b": 201}},
		{"noFolderKnown", []note{noteA}, []proc{agent, process(200, 100, 200, 0.4, claudeShell(`'make test'`)...)},
			map[string]int{"toolu_a": 200}},
		{"quotedFolder", []note{{AgentPID: 100, AgentStart: start(-3600), Kind: claude, ToolUseID: "toolu_q",
			Command: "cd '/w/my repo' && make test", NotedStart: start(0.3), Cwd: "/w"}},
			[]proc{agent, inFolder(process(200, 100, 200, 0.4, claudeShell(`'cd '"'"'/w/my repo'"'"' && make test'`)...), "/w/my repo")},
			map[string]int{"toolu_q": 200}},
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
The comment above it says why it is admitted: pairing crosswise would stop a command in another worktree than the one chosen, an unauthorized execution of a stop.

Add a second small table in the same file for `projectName` and `commandFolder`: `("make", "/w/.worktrees/feature-login") → "feature-login"`, `("cd api && make", "/w") → "api"` when `/w/api/.git` does not exist (use `t.TempDir()` for a case with a `.git` file), `("cd '/w/my repo' && make", "/w") → "my repo"`, `("make", "") → ""`.

- [ ] **Step 2: Run it**

Run: `go test ./internal/guard/ -run 'TestMatchPairsTheSameCommandByFolder|TestProjectName' -v`
Expected: FAIL (`Cwd` undefined / `inFolder` undefined).

- [ ] **Step 3: Implement**

The interfaces above. In `Pre`, the shape check still matches the whole command text as today; only `project` and `cwd` are new.

- [ ] **Step 4: Run the tests and gates**

Run: `go test ./internal/guard/` then the gate list. Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/guard
git commit -m "feat(guard): note the folder, project and subagent of each call and pair same-text commands by folder"
```

---

### Task 6: Memory every tick and the record fields

**Files:**
- Modify: `internal/guard/watch.go` (`watcher` struct, `tick`, `stopLargest`, `stop`, `callAttrs`, the `ended`/`unmatched` events), `internal/guard/handover.go` (`carried`, `adopt`), `internal/guard/logs.go` (`Event`)

**Interfaces:**
- Produces:
  - `type usage struct { Samples []uint64; Peak uint64; Since time.Time }` in `watch.go` with `func (u *usage) add(size uint64)` keeping the last 120 (`const samplesKept = 120`); `watcher.usage map[string]*usage` keyed by tool use ID, created at `tracking` with `Since: time.Now()`, deleted when the call is forgotten.
  - `tick` measures each tracked, alive command's tree (`s.tree(root, outside(protected))`, zombies skipped) with `platform.Resident` after `match` and before `readMemory`; an unreadable process is logged once through `unsized` as today.
  - `func callAttrs(c command) []any` adds `"cwd"`, `"project"`, `"subagent"` when set; `func (w *watcher) endAttrs(c command) []any` appends `"took"`, `time.Since(c.Note.NotedAt).Seconds()` and `"peak"`, `usage.Peak`; `ended` and `stopping` use `endAttrs`, `unmatched` adds `took`, `cwd`, `project`, `subagent` from the note.
  - `type stopCause struct { By string; Force bool }` with `var byGuard = stopCause{By: "guard"}`; `stop(c command, size uint64, children int, cause stopCause)`; the `stopping` record adds `"by", cause.By`. `signal` takes `force bool` and, when true, escalates at once (`SIGKILL` through `escalate`) instead of after `killAfter`. Reason text: the guard's `reason(...)` for `byGuard`; Task 8 supplies the manual text.
  - `carried` gains `Usage map[string]usage \`json:"usage"\``; `adopt` restores it.
  - `logs.go` `Event` gains `Call, Agent, Subagent, Project, Cwd, By, Release, OldRelease, NewRelease string`, `Took float64`, `Peak, Floor uint64`, `NewPID, Process int`, `StillRunning []int`, `Memory struct { Level int \`json:"level"\`; Available, Total uint64 }` with json tag `memory`, each tagged with its record key.
- Consumes: Task 2's `platform.Resident`, Task 5's note fields.

- [ ] **Step 1: Implement**

As listed. Keep `stopLargest`'s `platform.Footprint` for the stop decision (spec 6.4).

- [ ] **Step 2: Smoke in dry-run**

In a disposable home with `WORKBENCH_GUARD=dry-run`, start a fake agent (a `sleep 30` named through `exec -a claude sh -c 'sleep 30'` is not an agent; instead run the real pre hook as the guard design's acceptance did: `workbench guard pre` fed a PreToolUse JSON with `cwd` and `agent_type`, under a parent `sh` whose parent is a long-lived `claude`-named process started by the smoke). Expected: `watch.log`'s `tracking` record carries `cwd` and `project`; the `ended` record carries `took` and `peak` > 0.

- [ ] **Step 3: Gates and commit**

Run the gate list, then:
```bash
git add internal/guard
git commit -m "feat(guard): measure each tracked command every tick and record how long it ran and its peak"
```

---

### Task 7: `workbench guard logs` one line per event

**Files:**
- Modify: `internal/cli/guard.go` (`guardLogsCommand`, `showGuardLogs`, `guardEventText` → `guardLine`, `guardEventStyles`, `routineGuardEvents` deleted), `docs/usage.md:212-235`

**Interfaces:**
- Produces: `func guardLine(paint painter, e guard.Event, running bool) string` printing spec section 4: `Jan _2 15:04:05`, the level padded to 5, the event padded to 9, then keys in the order `id agent subagent project by dry_run took size peak floor level available total still_running release old_release new_release new_pid msg error process cmd`. `id` is the first 8 characters of `e.Call` after `toolu_` or `call_`. `cmd` is `e.Command` with `commandFolder`'s prefix removed (export it as `guard.CommandRest(command string) string`). A value is quoted with `strconv.Quote` when it holds a space, `"`, `=` or a control character, so a command with a newline stays one line; `msg` is printed only for `memory`, `refused`, `gave-up`, `error` and `raw`. `took`: one decimal under 10 s, whole seconds under a minute, else Go's `1m12s`. Sizes: `MB` under 1 GB, else `guard.Gigabytes`. Colors through `paint.style`: INFO `faint`, WARN `yellow`, ERROR `red`; the `id` value `lipgloss.Blue`; `took` ≥ 10 s bold; `peak`/`size` ≥ 1 GB `yellow`. `tracking` records are not printed; with `--follow`, a call whose `tracking` is 5 s old without an `ended`/`stopping` prints one `INFO running` line (`running == true`: no `took`, no `peak`).
- The `--verbose` flag and `routineGuardEvents` are removed; `--since`, `--follow`, `--json` stay. The empty-state message loses its `--verbose` hint.

- [ ] **Step 1: Implement**

Replace `guardEventText` with `guardLine`; keep `showGuardLogs`'s `--since` and raw-line handling. Keep `--follow`'s running line with a `map[string]time.Time` of tracking times, checked on each `followPoll`-driven emit.

- [ ] **Step 2: Check the output by eye**

In a disposable home, copy a `watch.log` the Task 6 smoke produced and run `workbench guard logs`, `NO_COLOR=1 workbench guard logs`, `workbench guard logs --json | head -1`, and `workbench guard logs | rg 'id=\w{8}'`. Expected: one line per event, keys in order, `--json` unchanged, the 8-character ID present.

- [ ] **Step 3: Docs**

`docs/usage.md` guard section: replace the `guard logs` paragraph and the example block with the new format (one line per event, the key order, `--verbose` gone); use neutral example lines from spec section 4.

- [ ] **Step 4: Gates and commit**

```bash
git add internal/cli/guard.go docs/usage.md
git commit -m "feat(guard): print the guard log one event per line with an id, project and key=value fields"
```

---

### Task 8: The watcher's socket

**Files:**
- Create: `internal/guard/protocol.go`, `internal/guard/socket.go`, `internal/guard/client.go`
- Modify: `internal/guard/watch.go` (`watcher` struct, `Watch`, `tick`), `internal/guard/handover.go` (`startHandover` `Files`, `inherit`), `internal/guard/state.go` (`folders.socket()`), `internal/guard/match_test.go`

**Interfaces:**
- Produces, `protocol.go` (exported, used by the client and the view):
  - `const ProtocolVersion = 1`; `type message struct { Type string \`json:"type"\`; Versions []int \`json:"versions,omitempty"\`; Version int \`json:"version,omitempty"\`; Release string \`json:"release,omitempty"\`; DryRun bool \`json:"dry_run,omitempty"\`; Error string \`json:"error,omitempty"\`; Snapshot *Snapshot \`json:"snapshot,omitempty"\`; IDs []string \`json:"ids,omitempty"\`; Force bool \`json:"force,omitempty"\`; Results []StopResult \`json:"results,omitempty"\` }` with the type names `hello`, `error`, `snapshot`, `stop`, `stopped`. (Spec 6.5 shows `at`, `memory` and `commands` at the top level of a snapshot; nesting them under `snapshot` keeps one message type. Rule it so and amend spec 6.5's example in Task 9's docs step.)
  - `type Snapshot struct { At time.Time; Memory MemoryView; Commands []CommandView }`; `type MemoryView struct { Level string; Used, Total uint64 }` (`Level` is `normal`, `warning`, `critical` on macOS from `Level` 1/2/4, `normal`/`low` on Linux from `decide`); `type CommandView struct { ID, Agent, Subagent, Project, Cwd, Command, State, By string; Started, Ended time.Time; Memory, Peak uint64; History []uint64; Tree []ProcessView }`; `type ProcessView struct { PID int; Name string; Memory uint64 }`; `type StopResult struct { ID string; OK bool; Error string }`. States: `running`, `stopping`, `done`, `unmatched`, `stopped`; finished commands stay in the snapshot for `const finishedShown = 60 * time.Second`.
  - `socket.go`: `type server struct { listener *net.UnixListener; mu sync.Mutex; clients map[*client]struct{}; stops chan stopRequest; self int }`; `type client struct { conn *net.UnixConn; send chan message }` (buffer 4); `type stopRequest struct { ids []string; force bool; reply chan<- []StopResult }`. `func (w *watcher) listen() error` creates `guard.sock` in `f.root` (remove a stale file first; the folder is 0700, and the peer check does the gating); `func (s *server) serve()` accepts, reads the hello, answers with the highest common version or `error` and closes, then reads `stop` messages and pushes `stopRequest`s on `stops` (non-blocking; a full channel answers `error: busy`). `func (s *server) broadcast(snap Snapshot)` sends to each client without blocking; a full `send` closes that client. The tick, at its start, drains `stops` and runs each through `w.manualStop(req)`, which replies on `req.reply`; the accept and client goroutines never touch watcher fields. Latency: a request waits at most one tick (`delay(m)` caps at 1 s), ruled acceptable; no wake descriptor.
  - Authorization, pure, in `socket.go`: `func stopAllowed(peer platform.Credentials, self int, s snapshot, notes []note) bool`: false when `peer.UID != self`, or when walking `peer.PID`'s parents in `s` reaches a note's `AgentPID` with its `AgentStart` (the agent itself included). The server checks `Peer` at accept (UID; closes otherwise) and `stopAllowed` per `stop` with the tick's snapshot and notes (passed in the request).
  - `func (w *watcher) manualStop(req stopRequest) []StopResult`: for each ID, `w.tracked[id]` must exist and be alive, else `{OK: false, Error: "already finished"}` (`"unknown"` when never tracked); otherwise `w.stop(c, size, children, stopCause{By: "you", Force: req.force})` with the reason text `Stopped by you (workbench guard memory).` and `OK: true` once SIGTERM (or SIGKILL) was sent; the view marks the row `stopping` on `OK`. Dry run logs `would stop` and answers `OK: true`.
  - Handover: `startHandover` passes the listener's file as descriptor 6 (`Files: []*os.File{w.lockFile, readyWrite, startRead, socketFile}`); `inherit` takes it with `net.FileListener(os.NewFile(6, "guard.sock"))`. The old watcher's clients see a close and reconnect.
  - `client.go`: `type Client struct {…}`; `func Dial(ctx context.Context) (*Client, error)` (path from `LogPath`'s folder: add `func SocketPath() (string, error)` beside `LogPath`); `func (c *Client) Hello() (release string, dryRun bool, err error)`; `func (c *Client) Snapshots() <-chan Snapshot` (closed on disconnect); `func (c *Client) Stop(ids []string, force bool) ([]StopResult, error)`; `func (c *Client) Close() error`.
- Consumes: Task 3's `platform.Peer`, Task 6's `stopCause` and `usage`.

- [ ] **Step 1: Write the failing test**

In `match_test.go` (the deciders are pure, as its header says), one table test:

```go
func TestStopAllowedOnlyForThisUserOutsideAnAgent(t *testing.T) {
	agent := process(100, 1, 100, -3600, "claude")
	notes := []note{{AgentPID: 100, AgentStart: start(-3600), Kind: claude, ToolUseID: "toolu_a", Command: "make"}}
	s := snapshotOf(agent,
		process(200, 100, 200, 0.4, claudeShell(`'make'`)...), // the agent's command shell
		process(300, 200, 300, 0.5, "workbench", "guard", "memory"), // started by that command
		process(400, 1, 400, 0.6, "zsh"), process(401, 400, 401, 0.7, "workbench", "guard", "memory"),
		process(500, 1, 500, -3600, "claude")) // an agent-named process that left no note
	for _, tc := range []struct {
		name string
		peer platform.Credentials
		want bool
	}{
		{"personAtATerminal", platform.Credentials{UID: 501, PID: 401}, true},
		{"otherUser", platform.Credentials{UID: 502, PID: 401}, false},
		{"underTheAgentsCommand", platform.Credentials{UID: 501, PID: 300}, false},
		{"theAgentItself", platform.Credentials{UID: 501, PID: 100}, false},
		{"unknownPeer", platform.Credentials{UID: 501, PID: 999}, false},
		{"agentWithoutANote", platform.Credentials{UID: 501, PID: 500}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := stopAllowed(tc.peer, 501, s, notes); got != tc.want {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}
```
Beside it, `TestManualStopSignalsNothingForAnUnknownOrFinishedCall`: a watcher built with `tracked` holding one command whose root is absent from the snapshot, and `send` replaced by a func that appends to a slice; `manualStop(stopRequest{ids: []string{"toolu_gone", "toolu_never"}})` returns `[{toolu_gone false "already finished"} {toolu_never false "unknown"}]` and the slice stays empty. For that, the watcher gets a field `send func(platform.Process, bool, unix.Signal) bool`, set to `platform.Signal` in `Watch`, and `signal`/`escalate` call `w.send`. The comment above the test: a stop a person did not choose, or chosen by an agent for another agent's work, is an unauthorized execution of `SIGTERM`.

- [ ] **Step 2: Run it**

Run: `go test ./internal/guard/ -run 'TestStopAllowed|TestManualStop' -v`
Expected: FAIL (undefined).

- [ ] **Step 3: Implement**

As listed. In `Watch`, call `w.listen()` after `takeLock` (log an `error` record and carry on without a socket if it fails) and `go w.server.serve()`; `defer` closing the listener and removing the file (only when this watcher is not handing over). After each `tick`, `w.server.broadcast(w.snapshotView(...))`.

- [ ] **Step 4: Run the tests and gates**

Run: `go test ./internal/guard/` and the gate list. Expected: PASS.

- [ ] **Step 5: Smoke**

In a disposable home with `WORKBENCH_GUARD=dry-run` (Task 6's smoke setup), connect with `nc -U "$H/…/guard/guard.sock"` (or `socat`), send `{"type":"hello","versions":[1]}` and read the hello and one snapshot; send `{"type":"hello","versions":[7]}` on a second connection and read the `error`. Expected: as spec 6.5.

- [ ] **Step 6: Commit**

```bash
git add internal/guard
git commit -m "feat(guard): serve snapshots and take stop requests over a peer-checked Unix socket"
```

---

### Task 9: `workbench guard memory`

**Files:**
- Create: `internal/cli/guardview.go`
- Modify: `internal/cli/guard.go` (`guardCommand` adds `guardMemoryCommand`), `docs/usage.md` (guard section), `docs/superpowers/specs/workbench-contracts.md:137` (the guard row: the socket and its messages), `docs/superpowers/specs/2026-10-14-agent-command-guard-design.md` (sections 4, 4.1, 6.1, 6.5 as spec section 10 lists), `docs/superpowers/specs/2026-10-17-platform-and-guard-view-design.md:6.5` (the nested snapshot message), `docs/adr/0013-guard-memory-view.md`

**Interfaces:**
- Produces: `func guardMemoryCommand(o *options) *cobra.Command` (`Use: "memory"`, `Short: "Watch the commands the guard tracks and stop one"`). Before the program starts: `platform.IsTerminal(os.Stdin.Fd()) && platform.IsTerminal(os.Stdout.Fd())` else `operation.Fail(operation.ExitBlocked, "terminal", "guard memory needs a terminal; use workbench guard logs --follow")`; `operation.AgentSession()` → `operation.Fail(operation.ExitBlocked, "agent", "guard memory is for a person at a terminal")`.
- `guardview.go`: `type guardView struct { client *guard.Client; snap guard.Snapshot; rows []guard.CommandView; cursor int; tagged map[string]bool; mode viewMode; menu int; notice string; noticeUntil time.Time; width, height int; release string; dryRun, connected bool }`; `type viewMode int` (`listMode`, `menuMode`, `detailMode`); messages `snapshotMsg guard.Snapshot`, `disconnectedMsg struct{}`, `stoppedMsg []guard.StopResult`, `tickMsg time.Time` (one per second for the clock and reconnects, through `tea.Tick`); `func waitSnapshot(ch <-chan guard.Snapshot) tea.Cmd` (the Bubble Tea realtime pattern). Keys as spec section 5; layout, columns and the narrow-window order as spec section 5; the program is started as `runCostsView` does (`tea.WithInput(os.Stdin)`, `tea.WithOutput(os.Stdout)`, `tea.WithContext`, `tea.WithoutSignalHandler()`), `view.AltScreen = true`. Widths from `terminalWidth`; sizes `guard.Gigabytes` and MB; styles `faint`, `yellow`, `red`, `green` from `root.go`; glyphs from `glyphs`. The one-line memory graph uses the eight block characters `▁▂▃▄▅▆▇█` over `History` scaled to `Peak`.
- Consumes: Task 8's `guard.Dial`, `Client`, `Snapshot`, `CommandView`, `StopResult`.

- [ ] **Step 1: Implement**

As listed. The watcher-not-running screen: `Dial` fails → show the message from spec section 5 and retry on each `tickMsg`.

- [ ] **Step 2: Smoke on macOS**

Disposable home, `WORKBENCH_GUARD=dry-run`, Task 6's fake agent with two commands in two folders: open `workbench guard memory`, check the rows (project, subagent, memory), press Space, `k`, Enter (dry run: the notice says stopped and the log says `would stop`), Enter on a row for details, Esc, `q`. Then without dry-run, stop a `sleep` the smoke started, politely and by force; `COLUMNS=50` shows the widen message; `NO_COLOR=1` shows plain text; `CLAUDECODE=1 workbench guard memory; echo EXIT:$?` prints the refusal and `EXIT:3`; `workbench guard memory < /dev/null | cat; echo EXIT:$?` prints the terminal refusal and `3`. Record each result in the ledger.

- [ ] **Step 3: Docs**

- `docs/usage.md`: a `workbench guard memory` paragraph (what it shows, keys, the stop menu, refusals) after the `guard logs` block.
- Contracts row 137: add the socket (`guard.sock`, 0700 folder, peer UID check, a stop refused from under a noted agent, JSON Lines hello/snapshot/stop/stopped).
- Guard design: note fields in section 4's `notes/` line, the new record keys in 4.1's prose and `--verbose` removed from its `guard logs` sentence, pairing by folder in 6.1, memory each tick in 6.3 and the socket in 6.5.
- Spec 6.5: the snapshot message is `{"type":"snapshot","snapshot":{"at":…,"memory":…,"commands":[…]}}`.
- `docs/adr/0013-guard-memory-view.md`: why the watcher serves a socket (one process signals), the peer checks, the agent lock, the tick-run stops and the 1 s latency ruling, with spec section 2's Docker, Tailscale, unix(7), jsonlines, htop and Kubernetes sources.

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

Build `GOOS=linux` into the workspace; run `docker run --rm -it --memory 2g --init -e HOME=/tmp/h -e WORKBENCH_GUARD=dry-run -v "$WS/linux:/b:ro" ubuntu:24.04` with `script`/`tmux` for a terminal, repeat Task 9 Step 2's walk (dry run, then real stops of the container's own `sleep` processes), and a handover: start the watcher from one build, install a second build over the `workbench` link in the container, and watch the view reconnect and keep its rows.

- [ ] **Step 2: Real agents on macOS, two worktrees**

Owner-authorized only, in the owner's session: run the same command in two worktrees from Claude Code (one through a subagent) and from Codex; check the rows' `project` and `subagent` and that a stop hits the chosen row. If not authorized, record it as not run and say so in the acceptance text.

- [ ] **Step 3: Cost**

With ten tracked `sleep` commands in the disposable home for one minute, sample the watcher's and the view's CPU and memory with `ps -o %cpu,rss -p PID` every 10 s. Record the numbers in `docs/acceptance.md` impersonally ("in a disposable home on macOS", "in an Ubuntu 24.04 container").

- [ ] **Step 4: Acceptance and commit**

Update the guard section of `docs/acceptance.md` with what was observed (replace, do not append history), then:
```bash
git add docs/acceptance.md
git commit -m "docs(acceptance): record the guard memory view and platform package checks"
```

---

## Self-review notes

- Spec coverage: 3.1-3.5 → Tasks 2-4; 3.6 → Tasks 1 and 4; 4 → Task 7; 5 → Task 9; 6.1-6.3 → Task 5; 6.4 → Task 6; 6.5 → Task 8; 7 → every task's Interfaces; 8 → Tasks 5 and 8 (three spec tests merged into two tables); 9 → Task 10; 10 → Tasks 4, 7, 9, 10.
- Rulings that deviate from the spec text, each to be noted in the ledger and the ADRs: `System` keeps the Go name; effect lists stay shared and only the choice is per suffix; `provisioningEffects` keeps the `is_wsl` answer; the snapshot message nests its payload; stop latency is one tick.
