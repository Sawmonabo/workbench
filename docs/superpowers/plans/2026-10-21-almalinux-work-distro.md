# AlmaLinux 10 Work Distribution Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Workbench runs on AlmaLinux 10. Each role has a folder the owner
picks. Credentials live in per-folder env files. The work MCP servers start
only inside the work folder for both Claude Code and Codex. Two WSL
distributions share the Windows side safely. Bitwarden is gone.

**Architecture:** One shared helper gives each apply-step command
(`migrate`, and the hidden `env-files` and `claude-mcp`) its hidden
`--probe` flag and `run` child, and `mcp-gate` is a hidden command of its
own. Go owns the dispatch server definition and passes it to the
templates as a native answer. The templates read the new folder answers.
Migration 0005 moves the saved tokens. The WSL scripts keep a record of
which distribution writes each shared Windows file.

**Tech Stack:** Go per `go.mod` (Cobra, go-toml/v2), chezmoi templates,
bash/zsh, Python (the Codex config modify script), and `cmd.exe`, `wsl.exe`
and `wslpath` reached from WSL.

**Spec:** `docs/superpowers/specs/2026-10-21-almalinux-work-distro-design.md`.
Read it in full before any task. Section numbers below (§4.11 and so on)
point into it.

## Global Constraints

- **`AGENTS.md` governs.** The work is greenfield: there are no
  compatibility readers except the ones the spec names (the retired token
  keys and migration 0005).
- **Tests stay near zero.** The only tests are the safeguard tests in tasks
  6 and 8, each with its risk written beside it. There is no TDD mandate
  and no per-feature suite.
- **Safe check environment.** On this WSL host, never run a check with the
  normal `PATH`: render-check and every dry run call the real `cmd.exe`,
  `powershell.exe`, `reg.exe` and `wsl.exe` in their probes, and IT
  monitors them. Define once per session:

  ```bash
  SCRATCH=<session scratch folder>/wbcheck
  (umask 077; mkdir -p "$SCRATCH"/{tmp,home,config,data,state,cache,bin,tools})
  SAFE_PATH="$SCRATCH/tools:$(tr ':' '\n' <<<"$PATH" | grep -v '^/mnt/' | paste -sd:)"
  gate()     { env PATH="$SAFE_PATH" TMPDIR="$SCRATCH/tmp" "$@"; }
  isolated() { env PATH="$SAFE_PATH" TMPDIR="$SCRATCH/tmp" HOME="$SCRATCH/home" \
                   XDG_CONFIG_HOME="$SCRATCH/config" XDG_DATA_HOME="$SCRATCH/data" \
                   XDG_STATE_HOME="$SCRATCH/state" XDG_CACHE_HOME="$SCRATCH/cache" \
                   WORKBENCH_BIN_DIR="$SCRATCH/bin" "$@"; }
  WB="$SCRATCH/bin/workbench"
  ```

  - With `/mnt/` taken out of `PATH`, the WSL scripts take their "no
    Windows interop" path, so no Windows program can run. `isolated` keeps
    the live home, the costs ledger and the agents' files out of reach.
    Never rely on `--destination` alone for isolation.
  - **Checks run a build of the checkout, never the installed release.**
    From the checkout, run `gate go build -o "$WB" ./cmd/workbench`
    (`./cmd/workbench` is the main package, as in README's build), again
    after every Go change. Every check
    calls `"$WB"` by path, never `workbench`. `isolated` sets
    `WORKBENCH_BIN_DIR` to `$SCRATCH/bin`, so the apply's scripts
    (`installed-workbench.sh`) run the same build. `update --local-build`
    installs only tools and links nothing into the bin folder, so the build
    stays.
  - **Saved answers first.** A check that needs saved answers writes a
    synthetic answers file, mode 600 under `$SCRATCH` (`init` reads it with
    `ReadPrivateInput`). It holds `[data]` with `machine_role`, `editor`,
    `versions_mode`, `name`, `email` (plus `personal_email`/`work_email` on
    `both`), the derived `has_work`, `has_personal` and `is_wsl = true`,
    the folder answers, and, because this host is WSL, `wsl_memory`,
    `wsl_swap`, `wsl_processors` and `restart_wsl_path` (`invalidKey`,
    `internal/machine/answers.go:187-230`). From the checkout, run
    `isolated "$WB" init --answers-from FILE --dry-run --json`, then the
    same with `--approve-plan DIGEST` (`init` takes no `--local-build`).
    Then run `isolated "$WB" update --local-build`, which puts the pinned
    tools under `$SCRATCH/data`. Dry runs are then
    `isolated "$WB" apply --local-build --dry-run`.
  - **Never apply on the host.** No `apply` without `--dry-run` runs on
    this host, `isolated` or not. With `HOME=$SCRATCH/home` the destination
    is the home, and the apply then runs the provisioning scripts (`sudo
    apt` and the rest) against the live machine: `plan.go:967-971` adds
    `--exclude=scripts` only when the destination differs from the home.
    Interactive shell checks (`bash -i`, `zsh -i`) need an applied home, so
    they run only in Task 13's throwaway containers.
  - **`golangci-lint`.** Use the version in `.golangci-lint-version`. When
    it is not on `PATH`, install that version into `$SCRATCH/tools` with
    `gate env GOBIN="$SCRATCH/tools" go install
    github.com/golangci/golangci-lint/v2/cmd/golangci-lint@<version>`.
  - **One scratch folder per session.** A session removes its `$SCRATCH`
    when its checks are done and says so in its report. Task 13 shows the
    proof for its own.
- **The gates.** After every task, run `gate golangci-lint fmt`,
  `gate golangci-lint config verify`, `gate golangci-lint run ./...`,
  `gate go build ./...` and `gate go test ./...`. Tasks that touch `home/`
  also run `gate scripts/render-check.sh ROLE MODE` and
  `gate scripts/render-check.sh ROLE MODE wsl` for ROLE in
  `personal work both` and MODE in `pinned latest`.
- **Platform code.** Platform facts and OS calls live only in
  `internal/platform/`, with the same exported names in its `_linux.go` and
  `_darwin.go` files (ADR 0012, `internal/platform/doc.go`).
  - Processes run only through `operation.Run`, apart from its documented
    exceptions. `mcp-gate`'s `exec` becomes one of them, and it lives in
    `internal/operation` beside `Handoff`'s `syscall.Exec`, not in
    `internal/cli`.
  - There are no Go interfaces only for tests, no `utils` and no `pkg`.
- **Credentials and personal data.**
  - No credential value appears in a plan, `--json`, log, rendered file,
    agent config, commit or the repository.
  - Checks use synthetic values and the `isolated` environment or
    containers. Never provision the developer's live machine.
  - No tracked file or commit message holds personal data: no real
    repository names, home paths or emails. Test fixtures use neutral names
    (`app`, `site`).
  - The two company addresses and the checkout name already in
    `home/.chezmoitemplates/codex-config.toml.tmpl` move into Go (Tasks 6
    and 9) and are never copied into a second file, tests included. (Fix
    pass 2 replaced them with the `jira_url`, `gitlab_url` and
    `dispatch_folder` work answers.)
- **Commits.**
  - Subjects are conventional; they become the changelog.
  - Each message ends with
    `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`.
  - New files are added with `git add <path>` before `git commit -am`.
  - All work happens on the branch `feat/almalinux-work-distro`, never on
    `main`.
- **Folder answers.**
  - They are saved as home-relative text, such as `~/repos`.
  - Each segment uses only letters, digits, `.`, `_` and `-`. A `.` or `..`
    segment, a trailing `/` and the answer `~` itself are refused.
  - On `both`, the two folders differ and neither contains the other.
  - Defaults: `work_folder = "~/repos"` and `personal_folder = "~/dev"`.
- **`work.env` names**, in order: `JIRA_BASE_URL`, `JIRA_EMAIL`,
  `JIRA_API_TOKEN`, `CONFLUENCE_BASE_URL`, `CONFLUENCE_EMAIL`,
  `CONFLUENCE_API_TOKEN`, `TEMPO_API_TOKEN`, `GITLAB_URL`, `GITLAB_TOKEN`.
  `personal.env` starts with no names.
- **Env file format.** Lines are `NAME=value`, `#` comments or blank, where
  NAME matches `^[A-Za-z_][A-Za-z0-9_]*$`. Any other line is skipped and
  reported. A `NAME=` line with an empty value sets nothing. The file is never sourced or executed. It must pass
  `operation.ReadPrivateInput`'s checks.
- **Probe lines.** No probe line contains `; `, which the plan view uses to
  join lines.
- **Release path (owner's rule).** This change ships first as a
  pre-release, the next free `-rc.N` (`v0.1.28-rc.3` for the first,
  unless the owner picks another). The owner
  installs it by name, checks it and signs off (Task 14). Fixes go out as
  the next `-rc.N`. After sign-off the owner merges to `main` and tags the
  release. Only the owner pushes tags or merges.
- **Windows calls look like ordinary user activity.** The work laptop has
  no PowerShell admin, and IT monitors PowerShell.
  - New code adds no PowerShell call. It uses only `cmd.exe /c echo
    %LOCALAPPDATA%` (the call the scripts already make) and plain file
    reads and writes through the `/mnt/c` path.
  - Nothing runs elevated. There is no `-ExecutionPolicy Bypass`, no
    `-EncodedCommand`, no `Invoke-Expression`, no download-and-run, no
    scheduled task, no `HKLM` write, no security-tool setting and no hidden
    window.
  - No check in this plan runs a Windows program (see "Safe check
    environment").
- **Clean up everything throwaway.** Checks work only under `$SCRATCH`, in
  `$SCRATCH/tmp` as `TMPDIR`, or in containers labelled
  `workbench-check=almalinux-work`. Task 13 removes them all and shows the
  proof. Removing the branch or worktree waits for the owner's explicit
  go-ahead (`AGENTS.md`).

## Review Focus

1. **The first apply after upgrading, at a terminal, on an existing
   machine.** The new folder questions re-ask before migration 0005 runs,
   and the tokens must survive (Task 6, `TestRetiredTokensSurviveEveryAnswerWrite`).
2. **A folder answer typed with a trailing slash** (`~/repos/`). The
   questionnaire drops it before validating and saving (Task 1, step 1).
3. **A symlinked work folder.** The gate and the shell hook both resolve
   symlinks before comparing (Task 8 test; Task 13 step 4, "Shell hook").
4. **An owner-edited env file with `export NAME=value` or quotes.** The plan
   line names the line number, and the line is skipped. It is never
   half-loaded and never crashes anything (Task 7 step 5 for the plan line;
   Task 8 step 5 for the gate; Task 13 step 4, "Shell hook", for the hook).
5. **A session started in the work folder itself** (`~/repos`, not a
   repository) **or through a symlinked path.** It counts as inside (Task 8
   test).
6. **Another dnf holds the package lock.** AlmaLinux 10 ships dnf 4, which
   waits for the lock by itself and says so; the scripts add no limit
   (Task 3 step 4; Task 13 step 3, "Lock").

---

### Task 1: Folder answers

**Files:**
- Modify: `home/.chezmoi.toml.tmpl:3-7,25-28` (header, email prompts, new questions)
- Create: `home/.chezmoitemplates/workbench-config`
- Modify: `internal/machine/answers.go` (`answerKeys`, `invalidKey`, new `folder`, new `RoleFolders`)
- Modify: `internal/machine/plan.go:326-330` (native answers)
- Modify: `scripts/scratch-init.sh:39-40`

**Interfaces:**
- Produces:
  - Answer keys `work_folder` (when `has_work`) and `personal_folder` (when
    `has_personal`).
  - Native answer `workbench_config = c.Paths.Config`.
  - Template `workbench-config`, which prints `get . "workbench_config"`,
    defaulting to `~/Library/Application Support/workbench/config` on
    darwin and `~/.config/workbench` on Linux, expanded under
    `.chezmoi.homeDir`.
  - `func (a Answers) folder(home, key string) string`: the absolute path,
    or `""` when the key is absent.
  - `func RoleFolders(home, config string) []string`: the saved folders
    as absolute paths, personal first. It reads `<config>/machine.toml`
    with `operation.ReadPrivateInput` and `readAnswers`, so it needs no
    `operation.Context`. It returns nil when none are saved or the file
    cannot be read. It has no default list: the one fallback stays in
    `internal/costs` (Task 2).

- [ ] **Step 1: The questionnaire.** In `.chezmoi.toml.tmpl`, after the
  identity questions:
  - When `$hasWork`:
    `promptStringOnce . "work_folder" "Work folder (under your home)" "~/repos"`.
  - When `$hasPersonal`:
    `promptStringOnce . "personal_folder" "Personal folder (under your home)" "~/dev"`.
  - Drop one trailing `/` from each, then `fail` unless the value matches
    `^~(/[A-Za-z0-9._-]+)+$` with no `.` or `..` segment.
  - On `both`, `fail` when the two are equal, or when one starts with the
    other followed by `/`.
  - The `both` email prompts become
    `Git personal email (repos in your personal folder)` and
    `Git work email (repos in your work folder)`.
  - The header comment names no fixed folder.
- [ ] **Step 2: Validate in Go.** In `answers.go`:
  - Add both keys to `answerKeys`.
  - In `invalidKey`, after identity, require each role's folder with the
    same pattern (a trailing `/` is invalid here), and the
    not-equal-not-nested rule on `both`.
  - Add `folder` and `RoleFolders`.
- [ ] **Step 3: The config folder.** In `plan.go`, set
  `nativeAnswers["workbench_config"] = c.Paths.Config` beside
  `workbench_bin`, with a comment in the same style. Then write
  `.chezmoitemplates/workbench-config`, modelled on `workbench-command`'s
  `get … | default` form.
- [ ] **Step 4: `scratch-init.sh`.**
  - Add `--promptString "Work folder (under your home)=~/repos"` and
    `--promptString "Personal folder (under your home)=~/dev"`.
  - Replace the two email prompts with the new text. The token prompts stay
    until Task 6.
- [ ] **Step 5: Run the gates.**
- [ ] **Step 6: Commit**

```bash
git add home/.chezmoitemplates/workbench-config
git commit -am "feat(answers): ask a folder for each role"
```

### Task 2: Every folder consumer reads the answers

**Files:**
- Modify: `home/dot_gitconfig.tmpl:14-19`, `home/dot_gitconfig-work.tmpl`, `home/dot_gitconfig-personal.tmpl`
- Modify: `home/.chezmoitemplates/codex-config.toml.tmpl:33-45`
- Modify: `home/private_dot_codex/modify_private_config.toml.tmpl:47-56`
- Modify: `home/private_dot_codex/AGENTS.md.tmpl:29-40`
- Modify: `home/.chezmoiscripts/wsl/run_before_00-packages-windows.sh.tmpl:115-117,140,167`
- Modify: `home/.chezmoiscripts/linux/run_once_before_00-packages.sh.tmpl:65-66`
- Modify: `internal/costs/report.go:224-234` (`ReportOptions.Roots`), `internal/costs/report.go:296,356-386,456-459`, `internal/costs/focus.go:93,96`
- Modify: `internal/cli/costs.go:72-77,158-163,190,318-327,743-751`
- Modify: `internal/machine/effects.go:183-194` (`linuxPackagesEffect` text)

**Interfaces:**
- Consumes: the folder template data and `machine.RoleFolders` (Task 1).
- Produces:
  - A field `Roots []string` on `costs.ReportOptions`: the role folders.
    `Report` and `FocusOn` resolve their own `Locations()`
    (`report.go:276`, `focus.go:46`), so the roots reach them through the
    options the `cli` builds, not through `costs.Paths`. `internal/costs`
    cannot call `internal/machine`, which imports it.
  - `projectRoots(home string, roots []string) []string`: `roots`, or,
    when it is empty, `[home/dev, home/repos]`. This is the one default
    list.
  - `ScopeRoots(home string, roots []string) []string`: the project roots
    that exist on disk, all of them when none exists.
  - `rollup(project string, roots []string) string`, which takes every
    project root.

- [ ] **Step 1: git.** The `includeIf "gitdir:<answer>/"` lines use each
  answer (git expands `~`). Update the header and include comments to
  match.
- [ ] **Step 2: Codex.**
  - Each role's trust table uses its expanded answer,
    `joinPath .chezmoi.homeDir (trimPrefix "~/" .work_folder)`.
  - `INACTIVE_TABLES` keeps naming the default folder of the role the
    machine lacks, since that answer is never saved. A changed folder leaves
    its old trust entry behind, which spec §4.13 accepts.
  - The path policy in `AGENTS.md.tmpl` names the expanded answers.
- [ ] **Step 3: Windows start folder.** The start folder is the work answer
  when `.has_work`, otherwise the personal one.
  - Rename `$wslRepos` to `$wslStart`. It becomes
    `\\wsl.localhost\<distro>\home\<user>\` plus the answer with `/`
    turned into `\`.
  - Use the same folder for `--cd` and the `startingDirectory` fallback.
- [ ] **Step 4: Folders.** Line 66 creates the expanded work answer under
  `{{ if .has_work }}` and the personal answer under
  `{{ if .has_personal }}`. Update the comment above it.
- [ ] **Step 5: Costs.**
  - Add `ReportOptions.Roots`; `projectRoots`, `ScopeRoots` and `rollup`
    change as described in Interfaces.
  - Update the callers with `opts.Roots`: `report.go:296` and `:456-459`
    (`loadGroups` already has `home` and `opts`), and `focus.go:93,96`.
  - **The `cli` fills it.** `openIngested` (`cli/costs.go:318-327`) also
    returns the `costs.Paths` it resolves. `(*costsFlags).report` (line
    190) then sets `f.roots = machine.RoleFolders(paths.Home,
    paths.Runtime.Config)`, and `f.options` (lines 158-163) copies it into
    `Roots`.
  - The `--all` flag help in `cli/costs.go:77` becomes
    `Include projects outside your role folders`, so it needs no lookup at
    construction, and the `os.UserHomeDir` call above it goes.
  - `scopeNames`' comment names the role folders.
- [ ] **Step 6: Effect text.** `linuxPackagesEffect`'s Description, What
  and Touches say "your work and personal folders".
- [ ] **Step 7: Run the gates.** Then:
  - Render `both pinned` with a scratch config set to
    `work_folder = "~/work"`. The `gitdir` lines, Codex trust, `AGENTS.md`,
    the Windows `--cd` and the `mkdir` line all name `~/work`.
  - Run
    `rg --hidden -n '~/repos|~/dev|/repos\b|"dev"|%s/dev|\}\}/dev' home internal`.
    It catches `printf "%s/dev"` and `{{ .chezmoi.homeDir }}/dev/` too.
    Every remaining hit must be one of these:
    - a default (the questionnaire's, the header comment's or the
      `INACTIVE_TABLES` entry for the role the machine lacks);
    - the costs fallback in `projectRoots`;
    - a GitHub API URL (`api.github.com/repos/...`);
    - an unrelated `"dev"`: the version string in `internal/cli/root.go`
      and `internal/cli/release.go`, and `internal/project/configure.go`.

    `codex-config.toml.tmpl`'s trust tables and `AGENTS.md.tmpl`'s path
    policy must not appear.
- [ ] **Step 8: Commit**

```bash
git commit -am "feat: read the role folders everywhere instead of ~/repos and ~/dev"
```

### Task 3: AlmaLinux 10, dnf and the EPEL step

**Files:**
- Modify: `internal/machine/host_linux.go:14-37`
- Modify: `internal/platform/facts_linux.go`, `internal/platform/facts_darwin.go`
- Modify: `internal/machine/effects.go` (new `epelEffect`, `effectSources`, `linuxPackagesEffect`)
- Modify: `internal/machine/effects_linux.go:11-17`, `internal/machine/effects_test.go:46-51`
- Modify: `home/.chezmoiscripts/linux/run_once_before_00-packages.sh.tmpl`, `home/.chezmoiscripts/linux/run_onchange_after_30-global-tools.sh.tmpl`
- Modify: `home/private_dot_bashrc.tmpl:3-8`, `scripts/render-check.sh:97`

**Interfaces:**
- Produces:
  - `platform.PackageManager`, a `sync.OnceValue` returning `"apt"` when
    `apt-get` is on `PATH`, `"dnf"` when `dnf` is and `apt-get` is not, and
    otherwise `""`. Its darwin twin returns `""`.
  - Effect `epel`, on by default, with the switch `WORKBENCH_EFFECT_EPEL`.

- [ ] **Step 1: Platform check.** `hostVersion` also sets `StatusComplete`
  when `ID == "almalinux"` and the part of `VERSION_ID` before the first
  `.` is `"10"`. Update its comment.
- [ ] **Step 2: Package-manager fact.** Add `PackageManager` to both facts
  files, with the same comment.
- [ ] **Step 3: The `epel` effect.**
  - Fill all nine fields, the way `migrations` does. Title is `EPEL`.
    Summary is `Fedora's extra packages for AlmaLinux (bat, fd)`. Privilege
    is `sudo; network`. Undo is `Not reverted automatically: sudo dnf
    remove epel-release`.
  - Add `"epel": {"00-packages"}` to `effectSources`.
  - `platformEffects` lists it first when `platform.PackageManager() ==
    "dnf"`.
  - Add `epelEffect` to the linux list in
    `TestSharedScriptsGateEverySectionOnItsOwnEffect`.
  - `linuxPackagesEffect` names apt on Ubuntu and dnf on AlmaLinux. Its
    Undo becomes `remove packages with apt or dnf`.
- [ ] **Step 4: Package script.**
  - **EPEL probe.** On a dnf host (dnf present, `apt-get` absent), print
    `epel: = EPEL is on` when `rpm -q epel-release` succeeds, otherwise
    `epel: turn on EPEL (uses sudo)`.
  - **EPEL part.** Before the base part, under `WORKBENCH_EFFECT_EPEL:-1`:
    `part epel "dnf could not install epel-release" install_epel`.
    `install_epel` runs `sudo dnf install -y epel-release` only when `rpm -q`
    fails.
  - **Base part.** A dnf branch collects each of
    `gcc gcc-c++ make curl unzip git ca-certificates fontconfig` that
    `rpm -q --whatprovides "$pkg"` does not find, then installs only those.
    This way `curl-minimal` satisfies `curl`.
  - **bat and fd.** A dnf branch first checks that `dnf repolist --enabled`
    lists `epel`, otherwise
    `why="EPEL is off: turn on the epel step or enable EPEL"`. It then
    installs `bat` and `fd-find` and makes no link.
  - **The lock.** Add no `timeout`: it would put `sudo dnf` in its own
    process group, so sudo could not ask for its password at the terminal,
    and it would kill a slow download or install as if the lock had been
    held. AlmaLinux 10 ships dnf 4 (`dnf-4.20.0-*.el10`, Red Hat advisory
    RHBA-2025:20106; RHEL 10 left dnf5 out). When another dnf holds the
    RPMDB lock, dnf 4 waits by itself: `ProcessLock.__enter__` checks
    every second and logs `Waiting for process with pid N to finish.` once
    per holder
    ([dnf/lock.py](https://github.com/rpm-software-management/dnf/blob/master/dnf/lock.py)).
    It fails at once only when the owner sets `exit_on_lock=True`, whose
    default is False
    ([dnf.conf reference](https://dnf.readthedocs.io/en/latest/conf_ref.html)).
    So the apply's output shows dnf's own waiting line, and the part goes
    on when the other dnf ends. A failed `sudo dnf`, the `exit_on_lock`
    case included, ends its function with `|| return 1`, as the apt lines
    do, so `part` reports that part's message, with dnf's own message
    printed above it.
  - **Header comment.** It names dnf, and says dnf waits for its lock by
    itself.
- [ ] **Step 5: podman.** `30-global-tools` gets a dnf branch:
  `sudo dnf install -y podman`.
- [ ] **Step 6: `/etc/bashrc`.** Right after the interactive guard in
  `private_dot_bashrc.tmpl`, add `[ -f /etc/bashrc ] && . /etc/bashrc`,
  with a comment naming AlmaLinux and non-login shells.
- [ ] **Step 7: render-check.** Add `dnf install` to the writer pattern on
  line 97.
- [ ] **Step 8: Run the gates.**
- [ ] **Step 9: Commit**

```bash
git commit -am "feat(linux): support AlmaLinux 10 with dnf and an EPEL step"
```

### Task 4: Remove Bitwarden

**Files:**
- Delete: `home/.chezmoitemplates/bw-wrapper.sh`
- Modify: `home/private_dot_bashrc.tmpl:147,160-163`, `home/dot_zshrc.tmpl:133`
- Modify: `home/.chezmoiscripts/linux/run_once_before_00-packages.sh.tmpl:3-5,9-26,30,235-256`
- Modify: `home/.chezmoiscripts/darwin/run_once_before_00-packages.sh.tmpl:15,50-57,120-133`
- Modify: `internal/machine/effects.go:69-72,212-244,293`, `internal/machine/effects_test.go:40`, `home/.chezmoidata/packages.toml:6-7`, `scripts/render-check.sh:103`

- [ ] **Step 1: The wrapper.** Delete it and both includes, and drop
  "bw login" from the `.bashrc` comment.
- [ ] **Step 2: The package scripts.** Delete the Bitwarden part, its probe
  lines and the `work-tools` names from both scripts. On Linux,
  `user_bin_refused` lists only `linux-packages`.
- [ ] **Step 3: `effects.go`.**
  - Delete `bitwardenSessionEffect` and its `effectSources` entry.
  - Set `work-tools` sources to
    `{"50-apps-and-extensions", "35-vscode-extensions"}`.
  - Rewrite every `work-tools` field for the extension only. Its Undo is:
    `Not reverted automatically: uninstall the extension in VS Code. A
    Bitwarden CLI installed earlier stays: run bw lock (or delete
    ~/.config/Bitwarden CLI/session) and remove bw by hand`.
  - `provisioningEffects` appends only `workToolsEffect`.
  - Drop the Bitwarden comment above it.
- [ ] **Step 4: The rest.** Drop Bitwarden from the test comment and the
  `packages.toml` comment, and the word `bitwarden` from the leak pattern.
- [ ] **Step 5: Run the gates.** Then run
  `rg --hidden -n -i "bitwarden|bw-wrapper|bw login" home internal scripts`.
  Expected: exactly one hit, the `work-tools` Undo text. `rg -n` counts
  lines, so keep that Undo string literal on one source line.
- [ ] **Step 6: Commit.** The subject carries the owner notice (§4.12).

```bash
git commit -am "feat!: remove Bitwarden; run bw lock first to clear its cached session"
```

### Task 5: Shared apply-step helpers

**Files:**
- Create: `internal/cli/applystep.go`
- Modify: `internal/cli/migrate.go:17-125`
- Modify: `internal/machine/effects.go:554-558` (export `effectVariable`), `internal/machine/plan.go:1026`, `internal/machine/effects_test.go:81,88`
- Modify: `home/.chezmoitemplates/installed-workbench.sh`, `home/.chezmoiscripts/shared/run_after_05-migrations.sh.tmpl:10-19`
- Modify: `internal/operation/state.go` (new `CreatePrivateFile` beside `ReplacePrivateFile`)

**Interfaces:**
- Produces:
  - `func applyStep(o *options, cmd *cobra.Command, effect, refusal string, probe func(io.Writer) error, run func(*cobra.Command) error)`.
    It adds two things to `cmd`, a command the caller builds:
    - **A hidden `--probe` flag.** `applyStep` wraps `cmd.RunE`: with
      `--probe` it runs `probe(cmd.OutOrStdout())` through `o.runPrinted`
      and is never refused. Without it, it calls the `RunE` that `cmd`
      already had. When `cmd` had none, it fails with `ExitInvalid`
      (`invocation`): `<name> is an apply step; it takes --probe or run`.
    - **A hidden `run` child.** It runs `run(cmd)` through `o.runPrinted`.
      It is refused with `operation.Fail(operation.ExitBlocked, "consent",
      refusal)` unless `os.Getenv(machine.EffectVariable(effect)) == "1"`
      and `!operation.AgentSession()`.

    Each step passes its own `refusal`. `migrate` keeps today's text:
    `migrate run is the apply's migrations step; run workbench migrate
    --apply`. `env-files` and `claude-mcp` use `<name> run is the apply's
    <name> step; run workbench apply`.
  - `func EffectVariable(name string) string` in `internal/machine`: today's
    `effectVariable` (`effects.go:556`), exported and used by its callers
    in `plan.go:1026` and `effects_test.go:81,88`, so `cli` does not derive the variable name a second time.
  - Shell function `knows_command NAME`, which succeeds when
    `"$workbench" NAME --help` prints `workbench NAME [`.
  - `func CreatePrivateFile(path string, data []byte) error`, in a private
    folder that already exists.
    - **Shape.** It is `ReplacePrivateFile`'s shape: a `.write-<id>` temp
      file created 0600 with `O_EXCL`, written, fsynced, then put in place
      with `RenameNoReplace(temp, path)` (`state.go:488`) instead of
      `Rename`, then the folder fsynced. A `path` that exists fails it with
      `fs.ErrExist`, and the deferred remove deletes the temp file.
    - **No crash window.** On the native filesystems
      `ensurePrivateDirectory` requires (`privateFilesystem`),
      `RenameNoReplace` is one `renameat2(RENAME_NOREPLACE)` (Linux) or
      `renameatx_np(RENAME_EXCL)` (macOS) call
      (`internal/platform/file_*.go`). `path` is never a second hard link
      of the temp file, so `checkPrivateFile`'s one-link rule never trips.
    - **Recovery.** Only where the no-replace flag is unsupported does
      `RenameNoReplace` fall back to link and unlink. A crash between those
      two leaves the `.write-` temp as a second link.
      `operation.RemoveStaleWrites(folder)` deletes such a leftover once
      it is a minute old, and 0005's `Run` and `env-files run` call it on
      the config folder before they check.
    - **Tokens.** They are never lost: 0005 deletes the keys from
      `machine.toml` only after `CreatePrivateFile` has returned nil.

- [ ] **Step 1:** Write `applyStep`.
  - Rebuild `migrate` on it. It keeps its visible command, its
    `--apply`/`--dry-run`/list `RunE`, and every message. It drops its own
    `probe` flag and its `case probe:`.
  - It calls `applyStep(o, cmd, "migrations", <today's refusal>,
    migrateProbe, <migrateRunCommand's body>)`.
  - `migrateRunCommand` goes; its doc comment moves to the `run` child.
  - Export `EffectVariable`.
- [ ] **Step 2:** Move `knows_migrations` into `installed-workbench.sh` as
  `knows_command`, and call `knows_command migrate` in `05-migrations`.
- [ ] **Step 3:** Add `CreatePrivateFile`, with a comment in the file's
  style.
- [ ] **Step 4: Run the gates.** Then build `"$WB"` and run
  `isolated "$WB" migrate`, `isolated "$WB" migrate --probe` and
  `isolated "$WB" migrate run`.
  - The first two behave as before.
  - `migrate run`, with no `WORKBENCH_EFFECT_MIGRATIONS`, exits 3 with
    today's message.
- [ ] **Step 5: Commit**

```bash
git add internal/cli/applystep.go
git commit -am "refactor: share the apply-step command, script check and private create"
```

### Task 6: Retired token keys and migration 0005

**Files:**
- Modify: `internal/machine/answers.go` (`answerKeys`, `invalidKey`, `secrets`, `Askable`, `AdoptionPlan`), `internal/machine/init.go:150-169` (`saveAnswers`)
- Create: `internal/migrate/0005_work_tokens.go`, `internal/migrate/0005_work_tokens_test.go`
- Create: `internal/machine/answers_test.go`
- Modify: `internal/migrate/migrate.go:1-8,78,322-339`, `internal/machine/effects.go:109-121` (`migrations` text)
- Modify: `home/.chezmoi.toml.tmpl:38-41`, `scripts/scratch-init.sh:41-42`, `home/.chezmoitemplates/codex-config.toml.tmpl:109-111,125-126`
- Modify: `AGENTS.md` (the migration exception for 0005)

**Interfaces:**
- Consumes: `operation.CreatePrivateFile` (Task 5), and the existing
  `operation.ReplacePrivateFile` and `operation.RemoveStaleWrites`.
- Produces:
  - `var retiredAnswers = []string{"jira_api_token", "gitlab_token"}`.
  - `func carryRetired(answers, saved Answers)`, which copies each retired
    key from `saved` that `answers` lacks.
  - `var WorkEnvNames []string` (the nine names) in `internal/migrate`.
  - Constants `jiraBaseURL` and `gitlabURL` in `0005_work_tokens.go`,
    holding the literals removed from the Codex template.
  - `var workTokens = Migration{ID: "0005-work-tokens", ...}`.

- [ ] **Step 1: Write the safeguard test
  `TestRetiredTokensSurviveEveryAnswerWrite`.**
  - Comment: the risk is unrecoverable loss of the owner's saved work
    tokens before 0005 moves them.
  - Saved answers hold `jira_api_token = "synthetic-jira"` and
    `gitlab_token = "synthetic-gitlab"`. New answers lack both.
  - Run `carryRetired` then `encodeMachineConfig(answers, Selection{})`
    (`selection.go:92`), the two calls `saveAnswers` and `AdoptionPlan`
    both make.
  - Assert: the decoded `[data]` holds both values, and `invalidKey`
    returns `""` for it.
- [ ] **Step 2:** Run `gate go test ./internal/machine -run TestRetiredTokensSurviveEveryAnswerWrite`.
  Expected: FAIL (`carryRetired` is undefined).
- [ ] **Step 3: The retired keys.**
  - `invalidKey` no longer requires the keys, and `answerKeys` keeps them.
  - `secrets()` iterates `retiredAnswers`.
  - `Askable` skips them.
  - `saveAnswers` and `AdoptionPlan` call `carryRetired` with the saved
    answers before encoding.
- [ ] **Step 4:** Run the test. Expected: PASS.
- [ ] **Step 5: Remove the old copies.** Drop the token prompts from the
  questionnaire and `scratch-init.sh`, and the `JIRA_BASE_URL`,
  `JIRA_EMAIL`, `JIRA_API_TOKEN`, `GITLAB_URL` and `GITLAB_TOKEN` lines from
  the Codex template.
- [ ] **Step 6: Write the safeguard test `TestWorkTokensNeverLost`.**
  - Comment: the risk is credential loss, and a token exposed in output.
  - Set up a temp `Locations` with synthetic values, then cover:
    - **(a) Role `both`, no `work.env`.** After `Run`, `work.env` is 0600
      and holds every `WorkEnvNames` entry with the tokens.
      `JIRA_EMAIL == work_email`, and the address lines equal
      `jiraBaseURL` and `gitlabURL` (compared by constant, never literal).
      `machine.toml` has neither key and keeps its `[effects]`.
    - **(b) Stopped after the first write.** `work.env` is written and
      `machine.toml` is unchanged. `Run` removes only the keys.
    - **(c) Something else in `work.env`.** `Check` returns `Conflict` and
      both files stay byte-for-byte.
    - **(d) A control character in a token.** `Conflict`, and nothing
      written.
    - **(e) Role `personal`.** Nothing changes.
  - In every case, no synthetic value appears in a `say` line or in
    `Check`'s text.
- [ ] **Step 7:** Run it. Expected: FAIL (undefined).
- [ ] **Step 8: Implement `workTokens`** (§4.11).
  - **Reading.** Decode `<Runtime.Config>/machine.toml` into
    `map[string]any` with go-toml/v2; `internal/migrate` may not import
    `internal/machine`.
  - **Status.**
    - `Pending`: a retired key is present, the role has work, and there is
      no `work.env`.
    - `Unfinished`: `work.env` holds the same two tokens.
    - `Conflict`: `work.env` holds something else, or a value has a
      control character.
    - `Done`: no retired key is present.
    - On a role without work it is also `Done`, with text saying the keys
      should be removed.
  - **Run.** First `operation.RemoveStaleWrites` on the config folder
    (Task 5, "Recovery"). Create `work.env` with `CreatePrivateFile`. Only
    after it returns nil, delete the two keys from `data`, `toml.Marshal`
    the whole map, and write it with `ReplacePrivateFile`.
  - **Registry.** Add `workTokens` to `Registry`.
- [ ] **Step 9:** Run it. Expected: PASS.
- [ ] **Step 10: Text.** Update the package comment, the recovery limits
  of `Plan`, every field of the `migrations` effect, and `AGENTS.md`'s
  migration exception (§4.9), all in this commit.
- [ ] **Step 11: Run the gates.**
- [ ] **Step 12: Commit**

```bash
git add internal/migrate/0005_work_tokens.go internal/migrate/0005_work_tokens_test.go internal/machine/answers_test.go
git commit -am "feat(migrate): move the saved work tokens into work.env"
```

### Task 7: Env files, the shell hook and agent guidance

**Files:**
- Create: `internal/machine/envfiles.go`, `internal/cli/envfiles.go`, `home/.chezmoiscripts/shared/run_after_06-env-files.sh.tmpl`
- Modify: `internal/cli/root.go:100-110`, `internal/machine/effects.go` (`sharedEffects`, `effectSources`), `internal/machine/answers.go` (new `ReadAnswers`)
- Modify: `home/private_dot_bashrc.tmpl`, `home/dot_zshrc.tmpl`
- Modify: `home/private_dot_claude/modify_private_settings.json`, `home/private_dot_claude/CLAUDE.md.tmpl`, `home/private_dot_codex/AGENTS.md.tmpl`

**Interfaces:**
- Consumes: `applyStep` and `knows_command` (Task 5); `migrate.WorkEnvNames`
  and `migrate.Check` (Task 6); `workbench-config` and the folder answers
  (Task 1).
- Produces:
  - `func ParseEnvFile(data []byte) (pairs [][2]string, bad []int)`.
  - `func EnvFilesProbe(c operation.Context, a Answers) []string`.
  - `func EnvFilesRun(ctx context.Context, c operation.Context, a Answers, say func(string)) error`.
  - `func ReadAnswers(c operation.Context) (Answers, error)` in
    `internal/machine`: `c.Native.Config` through `ReadPrivateInput` and
    `parseAnswers`, the read `WriteSelection` and setup already make with
    the unexported `readAnswers`. The `env-files` and `claude-mcp` probe
    and run actions get `c` from
    `operation.Resolve(operation.Options{ReadOnly: true})`, as
    `costs.Locations` does (`Resolve` sets `c.Native.Config` to
    `<config>/machine.toml`, `context.go:115`), then `a` from
    `ReadAnswers(c)`.
  - Effect `env-files`, with the switch `WORKBENCH_EFFECT_ENV_FILES`.
  - Shell function `__workbench_folder_env`.

- [ ] **Step 1: `envfiles.go`.**
  - **Reading.** Files are read with `operation.ReadPrivateInput(path, 1<<20)`.
  - **Probe lines.** Every line but `create` starts with `=`:
    - `env-files: create work.env`;
    - `env-files: = work.env is there, empty: JIRA_API_TOKEN, TEMPO_API_TOKEN`;
    - `env-files: = waiting for migration 0005 to create work.env`;
    - `env-files: = work.env holds work credentials without the work role: delete it`;
    - `env-files: = work.env line 4 is not NAME=value and is skipped`.
  - **Run.** First `operation.RemoveStaleWrites` on the config folder
    (Task 5, "Recovery"). Then create each missing file for the role with
    `CreatePrivateFile`. Skip `work.env` while `0005-work-tokens` is not
    `Done`.
- [ ] **Step 2: CLI, script and effect.**
  - `workbench env-files` is a hidden `&cobra.Command{Use: "env-files",
    Hidden: true, Args: cobra.NoArgs}` with no `RunE` of its own, passed to
    `applyStep(o, cmd, "env-files", "env-files run is the apply's
    env-files step; run workbench apply", <probe>, <run>)`. Its probe
    prints `EnvFilesProbe`'s lines. Its run calls `EnvFilesRun` with a
    `say` that prints `==> env-files: <line>`. Register it in `root.go`.
  - The script copies `05-migrations`' shape, using `knows_command
    env-files`.
  - `envFilesEffect` joins `sharedEffects` with every field filled; its
    Title is `Credential files`. Add `"env-files": {"06-env-files"}` to
    `effectSources`.
- [ ] **Step 3: Shell hook in bash.** Render the expanded folder answers
  and `workbench-config` into `__workbench_folder_env`.
  - **Interactive only.** It returns at once unless `$-` contains `i`.
  - **Cache.** It keeps the last `pwd -P` and each file's
    `stat -c '%Y %a'` (mtime and mode), and does nothing when none has
    changed. `chmod` changes no mtime, so the mode is in the key: a file
    made group-readable is unloaded at the next prompt.
  - **Unload.** It unsets (`unset -v`) every name the hook loaded, kept as a
    names-only list, then every name listed in the file of a role folder the
    shell is not in.
  - **Load.** It exports the file of the folder it is in only when the
    file has no group or other permission bits, the rule
    `checkPrivateFile` applies (`mode&0o077 == 0`,
    `internal/operation/context.go:337`): the last two digits of
    `stat -c %a` are `00`, so `600` and `400` both load. It reads lines
    with `while IFS='=' read -r name value || [ -n "$name" ]`, so a last
    line without a newline is not lost, and keeps only valid names. It
    never sources the file.
  - **Hooks.** Wrap `cd`, `pushd` and `popd`, and append the hook to
    `PROMPT_COMMAND` after the oh-my-posh line:
    `PROMPT_COMMAND="${PROMPT_COMMAND:+$PROMPT_COMMAND;}__workbench_folder_env"`.
  - **Wrappers.** `claude()` runs the hook before `command claude`. Add
    `codex() { __workbench_folder_env; command codex "$@"; }`, and a
    `code()` that runs `env -u NAME… code "$@"` for every name in both
    files.
- [ ] **Step 4: Shell hook in zsh.** The same function in `dot_zshrc.tmpl`,
  with these differences:
  - mtime comes from `zmodload zsh/stat; zstat +mtime`, and the mode from
    `zstat -o +mode`, both in the cache key as in bash, which prints it in octal with a leading zero
    (`0100600`); the same rule applies, so its last two digits must be
    `00`;
  - the guard is `[[ -o interactive ]]`;
  - registration is `autoload -Uz add-zsh-hook`, `add-zsh-hook chpwd` and
    `add-zsh-hook precmd`.

  It keeps the same three wrappers.
- [ ] **Step 5: Check Review Focus 4 (the plan line).** Build `"$WB"`,
  save role `both` answers and install the tools as "Safe check
  environment" says. Then write `$SCRATCH/config/workbench/work.env`, mode
  600, holding `export JIRA_EMAIL=x` on line 2. Run
  `isolated "$WB" apply --local-build --dry-run` from the checkout. It
  shows `env-files: = work.env line 2 is not NAME=value and is skipped`.
  The hook's half of this check runs in Task 13 step 4 ("Shell hook").
- [ ] **Step 6: Claude Code deny rules.** In `modify_private_settings.json`,
  assign `{{- $config := includeTemplate "workbench-config" . -}}` beside
  the existing `$workbench` line. Append `printf "Read(/%s/work.env)"
  $config` when `.has_work`, and the same for `personal.env` when
  `.has_personal`, to `$enforced.permissions.deny`. `$config` is absolute,
  so each rule starts with `Read(//`.
- [ ] **Step 7: Agent text.** In `CLAUDE.md.tmpl` and Codex's
  `AGENTS.md.tmpl`, for roles with an env file, add:
  "Credentials are in environment variables loaded from Workbench's env
  files. Use them by name; never open, print or echo the files or their
  values."
- [ ] **Step 8: Run the gates.** Then render the bash hook without applying
  anything:
  - `config=$(gate scripts/scratch-init.sh both pinned)`;
  - `gate chezmoi --config "$config" --source . execute-template <
    home/private_dot_bashrc.tmpl > "$SCRATCH/bashrc"`;
  - `gate bash -n "$SCRATCH/bashrc"`;
  - `rm -rf "$(dirname "$config")"`.

  The hook's behaviour (§4.10's shell checks) needs an applied home, so it
  runs in Task 13 step 4 ("Shell hook"), never on this host.
- [ ] **Step 9: Commit**

```bash
git add internal/machine/envfiles.go internal/cli/envfiles.go home/.chezmoiscripts/shared/run_after_06-env-files.sh.tmpl
git commit -am "feat: per-folder credential files with a shell hook and deny rules"
```

### Task 8: The `mcp-gate` command

**Files:**
- Create: `internal/cli/mcpgate.go`, `internal/cli/mcpgate_test.go`
- Modify: `internal/cli/root.go:100-110`
- Modify: `internal/operation/process.go:389-395` (the documented exceptions), `internal/operation/handoff.go` (new `Exec`)

**Interfaces:**
- Consumes: `machine.ParseEnvFile` (Task 7) and `operation.ReadPrivateInput`.
- Produces:
  - `workbench mcp-gate --folder DIR --env-file FILE -- COMMAND ARGS...`,
    hidden.
  - `func Exec(command string, args, environment []string) error` in
    `internal/operation/handoff.go`, beside `Handoff`'s `syscall.Exec`
    (line 71). It replaces the process with `command`, so `internal/cli`
    makes no system call itself.
  - `func gateInside(folder, projectDir, cwd string) bool`.
    - It uses `projectDir` when that is not empty, otherwise `cwd`.
    - It resolves symlinks in both paths with `filepath.EvalSymlinks`; a
      path that cannot be resolved counts as outside.
    - It returns true when the path equals the folder, or starts with the
      folder followed by `/`.

- [ ] **Step 1: Write the safeguard test
  `TestGateNeverCountsOutsideFolderAsInside`.**
  - Comment: the risk is work credentials and work tools reaching a
    personal session.
  - The temp tree holds `repos`, `repos-old`, `repos/app`, `dev`,
    `dev/link -> ../repos/app` and `alias -> repos`.
  - True for:
    - `repos`;
    - `repos/app`;
    - `dev/link`;
    - folder `alias` with cwd `repos/app`;
    - projectDir `repos/app` with cwd `dev`.
  - False for:
    - `repos-old`;
    - `dev`;
    - a missing path;
    - projectDir `dev` with cwd `repos/app`.
- [ ] **Step 2:** Run `gate go test ./internal/cli -run TestGateNeverCountsOutsideFolderAsInside`.
  Expected: FAIL (undefined).
- [ ] **Step 3:** Implement `gateInside`. Run the test. Expected: PASS.
- [ ] **Step 4: The command.** It calls
  `gateInside(folder, os.Getenv("CLAUDE_PROJECT_DIR"), cwd)`; when
  `os.Getwd` fails, it counts as outside.
  - **Inside.** Read the file with `ReadPrivateInput`, then `ParseEnvFile`.
    Each skipped line is named on stderr as `mcp-gate: <file> line N is
    not NAME=value and is skipped`, never with its text. Set each pair with
    a value over the inherited environment, then `operation.Exec` the
    command. When the file is missing or unsafe, exit 1 with
    `mcp-gate: <file> is missing or readable by others` on stderr.
  - **Outside.** Run a newline-delimited JSON-RPC loop on stdin and stdout
    that reads no file:
    - `initialize` → `{"protocolVersion": <client's>, "capabilities": {}, "serverInfo": {"name": "workbench-mcp-gate"}}`;
    - `ping` → `{}`;
    - `tools/list`, `prompts/list`, `resources/list` and
      `resources/templates/list` → an empty `tools`, `prompts`,
      `resources` or `resourceTemplates` list;
    - notifications → no reply;
    - anything else → error `-32601`.

    It exits 0 at end of input.
  - **Exceptions.** Add `operation.Exec` to `Run`'s exceptions comment.
- [ ] **Step 5: Smoke the gate** with no agent involved. Build `"$WB"`.
  Create `$SCRATCH/home/work` and, with `mkdir -m 700`,
  `$SCRATCH/config/workbench`. Write
  `$SCRATCH/config/workbench/work.env`, mode 600, with synthetic values
  for `JIRA_BASE_URL` and `GITLAB_TOKEN` and the bad line
  `export JIRA_EMAIL=x` on line 2. Pipe the `initialize`, `tools/list`,
  `prompts/list`, `resources/list` and `ping` lines into
  `isolated "$WB" mcp-gate --folder "$SCRATCH/home/work" --env-file "$SCRATCH/config/workbench/work.env" -- /usr/bin/env`,
  with `CLAUDE_PROJECT_DIR` both set and unset.
  - From a cwd outside the folder, it gives empty-list replies and exits 0.
  - From inside, `env` prints the two synthetic names and no
    `JIRA_EMAIL`. stderr says `line 2 is not NAME=value and is skipped`,
    without the line's text (Review Focus 4).
  - With the file at mode 644, from inside, it exits 1 with the
    `missing or readable by others` line.
- [ ] **Step 6: Run the gates.**
- [ ] **Step 7: Commit**

```bash
git add internal/cli/mcpgate.go internal/cli/mcpgate_test.go
git commit -am "feat(cli): mcp-gate starts a server only inside the work folder"
```

### Task 9: One definition of the dispatch servers; Codex through the gate

**Files:**
- Create: `internal/machine/dispatch.go`
- Modify: `internal/machine/plan.go:326-330` (native answer)
- Modify: `home/.chezmoitemplates/codex-config.toml.tmpl:97-128`
- Modify: `home/private_dot_codex/modify_private_config.toml.tmpl:4-13,244-250` (`RETIRED_KEYS`)
- Modify: `scripts/render-check.sh:39-40,246-289` (a sample `dispatch_servers`)

**Interfaces:**
- Consumes: `Answers.folder` (Task 1).
- Produces:
  - `type dispatchServer struct { Name, Command string; Args []string; Env map[string]string; Approve []string }`.
  - `func dispatchServers(a Answers, bin, config, home string) []dispatchServer`.
    It returns the two servers when `has_work`, otherwise none.
    - `bin` is `filepath.Join(c.Paths.Bin, "workbench")`.
    - Args are `["mcp-gate", "--folder", <work>, "--env-file", <config>/work.env, "--", <home>/.nvm/nvm-exec, "node", <work>/<checkout>/<server>/dist/index.js]`.
    - Env is `{"NODE_VERSION": "default"}`.
    - `Approve` holds today's approved tool names.
    - The checkout name lives only here.
  - Native answer `dispatch_servers`, a list of maps with keys `name`,
    `command`, `args`, `env` and `approve`.

- [ ] **Step 1:** Write `dispatch.go`. `(*preparation).stageNative`
  (`plan.go:294`) sets `nativeAnswers["dispatch_servers"]` beside
  `workbench_bin` (line 330).
- [ ] **Step 2: The template.** It replaces the two hand-written entries
  with a `range (get . "dispatch_servers" | default list)` loop. Each entry
  emits:
  - `[mcp_servers.<name>]`, `enabled = true`, and `command` and `args`
    through `toToml`;
  - `[mcp_servers.<name>.env]`;
  - one `approval_mode = "approve"` table per `approve` entry.

  A comment says the gate starts them only inside the work folder.
- [ ] **Step 3: `RETIRED_KEYS`.**
  - In the modify script, add
    `RETIRED_KEYS = [("dispatch-atlassian", ("JIRA_BASE_URL", "JIRA_EMAIL", "JIRA_API_TOKEN")), ("dispatch-gitlab", ("GITLAB_URL", "GITLAB_TOKEN"))]`.
  - After the `INACTIVE_TABLES` loop, pop those keys from each
    `document["mcp_servers"][name]["env"]`.
  - Name it in the docstring.
  - The literal names sit only in this modify script, as
    `INACTIVE_TABLES`'s already do.
- [ ] **Step 4: render-check renders the loop.** Today it sets only
  `workbench_bin`, by `awk` under `[data]` (`render-check.sh:298`), so the
  work and both renders would hold no dispatch tables.
  - Right after `scratch-init.sh` (line 39), when the role is not
    `personal`, the same `awk` form adds one sample entry to `$config`'s
    `[data]`:
    `dispatch_servers = [{name = "sample-server", command = "<tmp>/wb bin's/workbench", args = ["mcp-gate", "--folder", "<tmp>/work", "--", "node", "index.js"], env = {NODE_VERSION = "default"}, approve = ["sample_tool"]}]`.
  - The names are neutral, so no work-only string is copied, and the
    command has a space and a quote, like the bin check's.
  - The python block of the hooks check (lines 246-289) also loads the
    rendered `.codex/config.toml` with `tomllib`. It asserts that
    `mcp_servers.sample-server` has that `command` and `args`,
    `env.NODE_VERSION`, and that
    `mcp_servers.sample-server.tools.sample_tool.approval_mode` is
    `approve`.
- [ ] **Step 5: Run the gates.** Then pipe a synthetic existing config,
  with literal tokens, through the rendered modify script:
  `isolated python3 -I <rendered> < synthetic.toml`. The output has no
  `TOKEN =` line under the dispatch `env` tables, and `NODE_VERSION`
  stays.
- [ ] **Step 6: Commit**

```bash
git add internal/machine/dispatch.go
git commit -am "feat(codex): start the dispatch servers through mcp-gate"
```

### Task 10: The `claude-mcp` step

**Files:**
- Create: `internal/machine/claudemcp.go`, `internal/cli/claudemcp.go`, `home/.chezmoiscripts/shared/run_after_46-claude-mcp.sh.tmpl`
- Modify: `internal/cli/root.go:100-110`, `internal/machine/effects.go` (`sharedEffects`, `effectSources`)

**Interfaces:**
- Consumes:
  - `dispatchServers` (Task 9), `applyStep` (Task 5) and `ReadAnswers`
    (Task 7);
  - `findAgent(home, nvm string, agent agent) (agentInstall, bool)` with
    `nvmDefaultBin(home)` (`internal/machine/agents.go:345,435`), for the
    Claude Code entry of `agents` (`agents.go:42-51`, command `claude`).
- Produces:
  - `func ClaudeMCPProbe(c operation.Context, a Answers) []string`.
  - `func ClaudeMCPCommands(c operation.Context, a Answers) (claude, nodeBin string, commands [][]string)`:
    the resolved `claude` path (`""` when Claude Code is not installed),
    `nvmDefaultBin(home)` (`""` when nvm has no default Node), and the
    argument lists to run, in order.
  - `workbench claude-mcp`, hidden, built with `applyStep` (Task 5) and no
    `RunE` of its own:
    - **`claude-mcp --probe`** prints `ClaudeMCPProbe`'s lines.
    - **`claude-mcp run`** is the step's run action. It prints what the
      script runs and runs nothing itself. Like every `run`, it is refused
      unless `WORKBENCH_EFFECT_CLAUDE_MCP=1` and outside an agent session,
      with `claude-mcp run is the apply's claude-mcp step; run workbench
      apply`.
    - **Output.** Every field is NUL-terminated. Field 1 is the `claude`
      path, field 2 is the `nodeBin` folder, either of them possibly
      empty. Then come each command's arguments, with an empty field
      ending each command.
  - Effect `claude-mcp`, with the switch `WORKBENCH_EFFECT_CLAUDE_MCP`.

- [ ] **Step 1: Probe.** Read `~/.claude.json` as JSON: `mcpServers` (user
  scope) and `projects[*].mcpServers` (local scope).
  - **Registered.** An entry counts as registered only when its `command`
    and `args` match and its `env` key set is exactly `{NODE_VERSION}`.
    Never print a value.
  - **Lines:**
    - `claude-mcp: register dispatch-atlassian, dispatch-gitlab`;
    - `claude-mcp: = registered`;
    - `claude-mcp: remove dispatch-atlassian, dispatch-gitlab`;
    - `claude-mcp: = nothing to do`.
  - **Warnings:**
    - `claude-mcp: = a local entry for dispatch-gitlab in <project> outranks this one: run claude mcp remove --scope local dispatch-gitlab`;
    - when 0005 found no tokens to move and a replaced entry's `env` has a
      `TOKEN` name: `claude-mcp: = the old entry's credentials are not carried into work.env`.
- [ ] **Step 2: Commands.** `ClaudeMCPCommands` lists, for each server,
  `mcp remove --scope user NAME`, then
  `mcp add-json --scope user NAME JSON`, where JSON is
  `{"type":"stdio","command":…,"args":…,"env":{"NODE_VERSION":"default"}}`.
  Without the work role, it lists only the removes. Go runs none of them;
  the script does (step 3).
- [ ] **Step 3: Script.**
  - **Shape.** It follows `05-migrations`, using `knows_command
    claude-mcp`.
  - **Run.** It reads `"$workbench" claude-mcp run` with
    `while IFS= read -r -d ''`. Fields 1 and 2 give `claude` and `nodeBin`.
    It runs each command as `"$claude" "${args[@]}"`, with
    `PATH="$(dirname "$claude")${nodeBin:+:$nodeBin}:$PATH"`, so the
    `claude` launcher finds `node`. When `claude-mcp run` itself fails, the
    script calls `part_blocked claude-mcp` with its reason.
  - **Exit status.** A `remove` exit status is ignored. A failed `add-json`
    calls `part_blocked claude-mcp`. An empty field 1 calls
    `part_blocked claude-mcp "Claude Code is not installed"`.
  - **No names in the script.** The script itself names no server; the
    names come only from Go at run time, so the personal leak check
    passes.
- [ ] **Step 4: CLI and effect.**
  - Build the command as Interfaces says and register it in `root.go`.
  - `claudeMCPEffect` joins `sharedEffects` with every field filled.
    - Title: `Claude Code work servers`.
    - What: names the idle `mcp-gate` process outside the work folder.
    - Undo: `claude mcp remove --scope user dispatch-atlassian dispatch-gitlab`.
  - Add `"claude-mcp": {"46-claude-mcp"}` to `effectSources`.
- [ ] **Step 5: Run the gates**, the personal leak check included.
- [ ] **Step 6: Commit**

```bash
git add internal/machine/claudemcp.go internal/cli/claudemcp.go home/.chezmoiscripts/shared/run_after_46-claude-mcp.sh.tmpl
git commit -am "feat: register the Claude Code dispatch servers through mcp-gate"
```

### Task 11: The Windows writer record

**Files:**
- Create: `internal/machine/writers.go`, `home/.chezmoitemplates/windows-writers.sh`
- Modify: `home/.chezmoiscripts/wsl/run_before_00-packages-windows.sh.tmpl:62-80`, `home/.chezmoiscripts/wsl/run_after_10-deploy-windows-configs.sh.tmpl:54-58`
- Modify: `internal/machine/plan.go` (the input, the take-over effects, `recoveryLimits()` at `:221`), `internal/machine/effects.go` (`selectionToSave` at `:505`, `effectSources`, the four steps' Undo)

**Interfaces:**
- Produces:
  - `func windowsWriters(ctx context.Context, c operation.Context) operation.Input`.
    - Only when `platform.WSL()`, and not for an isolated destination
      (`c.Native.Destination != c.Home`).
    - It finds `cmd.exe` in the Windows system folders
      `windowsSystemDirectories` keeps (`dependencies.go:289`), and runs
      `cmd.exe /c echo %LOCALAPPDATA%`, then `wslpath -u` on the answer,
      through `operation.Run`: read-only, no mutation, 60 seconds.
    - **The `cmd.exe` call's `Process`.** `Environment` holds
      `PATH=/usr/bin:/bin:<those Windows folders>` and `WSL_INTEROP` and
      `WSL_DISTRO_NAME` from `os.Getenv`, when set. That is how
      `scriptEnvironment` forwards them to the Windows steps
      (`dependencies.go:244-252`); `Run` passes only what it is given
      (`process.go:414`). `Directory` is the absolute folder `cmd.exe` was
      found in, as `Run` requires (`process.go:530-537`), so `cmd.exe`
      starts in a Windows folder. `Run` also refuses an executable with
      group or other write access (`trustedExecutable`, `process.go:46`);
      `cmd.exe` on this host's drvfs mount is mode 555, and a refusal
      elsewhere gives `"unknown"` and the warning below.
    - It returns `{Name: "windows-writers", Digest: <sha256 of <LocalAppData>/Workbench/writers.json, or "absent">}`.
    - On any failure the Digest is `"unknown"`. `Plan` adds the warning
      `Couldn't read the Windows writer record` when it sees that Digest.
  - The record, `{"terminal-adoption": "<distro>", …}`, keyed by the four
    step names.
  - Shell function `writer_line STEP RECORD_FILE INSTALLED_LIST
    THIS_DISTRO`, which prints the probe line for the four rules (§4.7).
    The scripts pass `"$WSL_DISTRO_NAME"` as `THIS_DISTRO`. Also
    `writer_record STEP RECORD_FILE THIS_DISTRO`. Both read and write JSON
    with `python3 -I -c`, and `writer_record` writes a new file then
    renames it.
  - `windows-writers.sh` is plain bash with no template directive (no
    `{{`), unlike `windows-calls.sh:86`. The scripts include it with
    `{{ template "windows-writers.sh" }}`, and a check can `source` it.
  - Optional plan effects `take-over-<step>`, listed only when that step's
    probe line starts `= written by`.
    - **Switch.** Each has an `effectSources` entry naming its step's
      script, so the script runs when only the take-over is ticked. The
      effect loop in `scriptsEnvironment` (`plan.go:1018-1027`) already
      sets `WORKBENCH_EFFECT_TAKE_OVER_<STEP>=1` for a checked one. The
      scripts read that variable; no new variable is needed.
    - **Saving.** In `selectionToSave` (`effects.go:505`), a `take-over-*`
      name is dropped from `Select`, both the one `SelectionOf` adds and
      any saved one. It stays in `Decided`, where the `!AlreadySet`
      branch puts it. So a declined take-over is not `New` again
      (`applySelection`, `effects.go:432-438`), and "apply decides once"
      holds. A tick takes over once. After that the record names this
      distribution, so the effect is no longer listed. A later take-over
      is `apply --choose`, as §4.7 rule 4 says.

- [ ] **Step 1: `windows-writers.sh`.** Both WSL scripts include it.
  - Both scripts ask `%LOCALAPPDATA%`; the deploy script adds it to its
    line-54 echo.
  - Both run `win_start distros wsl.exe --list --quiet` in probe mode and
    run mode alike, through `win_start`/`win_wait`.
- [ ] **Step 2: Apply the rules** in each of the four steps.
  - **Probe.** Print `writer_line`.
  - **Run.** Skip the step when another installed distribution is
    recorded, unless `WORKBENCH_EFFECT_TAKE_OVER_<STEP>` is `1`. A ticked
    take-over runs its step in this apply. After writing, call
    `writer_record`.
- [ ] **Step 3: Go side.**
  - Add `windowsWriters` to the plan inputs.
  - Add the `take-over-<step>` effects with every field filled (Title:
    `Take over <step title> from <distro>`) and their `effectSources`
    entries.
  - Add the `Select` filter in `selectionToSave`.
- [ ] **Step 4: Text.** The four steps' Undo, and a line that
  `recoveryLimits()` (`plan.go:221`) adds when `platform.WSL()`, name the
  record and say that deleting it is safe. `revert` prints `plan.RecoveryLimits`, which
  `recoveryLimits()` fills (`revert.go` only copies them).
- [ ] **Step 5: Run the gates.** Then check the four rules without any
  Windows program: `source` `windows-writers.sh` in `gate bash` and call
  `writer_line` with a scratch record, an installed list and
  `THIS_DISTRO` set to `Distro-A` for each case:
  - nobody recorded;
  - this distribution recorded;
  - another installed distribution recorded;
  - a recorded distribution that is no longer installed.

  Each prints §4.7's line.
- [ ] **Step 6: Commit**

```bash
git add internal/machine/writers.go home/.chezmoitemplates/windows-writers.sh
git commit -am "feat(wsl): record which distribution writes each shared Windows file"
```

### Task 12: Decision record and docs

**Files:**
- Create: `docs/adr/0015-almalinux-and-role-folders.md`
- Modify: `AGENTS.md`, `README.md`, `docs/linux.md`, `docs/wsl.md`, `docs/usage.md`, `docs/superpowers/specs/workbench-contracts.md`, `internal/operation/process.go` (comment only, if not done in Task 8)

- [ ] **Step 1: ADR 0015.** Follow ADR 0014's format. It records §4.9's
  seven reasons and names the Ubuntu-only decision as replaced.
- [ ] **Step 2: `AGENTS.md`.**
  - Ownership rows for the env files, `env-files`, `claude-mcp`,
    `mcp-gate`, `applyStep` and `CreatePrivateFile`.
  - `epel` named as on by default.
  - The 0005 exception already landed in Task 6.
- [ ] **Step 3: Contracts doc.**
  - Lines 39, 80, 135 and 142–143.
  - The targets table and the prerequisites paragraph.
  - `operation.Exec`, `mcp-gate`'s exec, among the process exceptions.
- [ ] **Step 4: `docs/usage.md`.**
  - Lines 139, 366, 368 and 468.
  - The `epel`, `env-files`, `claude-mcp` and take-over steps, and the
    folder questions.
  - The Bitwarden notice.
- [ ] **Step 5: The other docs.**
  - `docs/linux.md`: targets, dnf, EPEL, the bash note and the Bitwarden
    notice.
  - `docs/wsl.md`: the §4.7 rules.
  - `README.md`: the targets line.
- [ ] **Step 6: Run the gates.** Then run
  `rg -n "~/repos|~/dev" docs/usage.md docs/linux.md README.md`. Every hit
  should read as a default.
- [ ] **Step 7: Commit**

```bash
git add docs/adr/0015-almalinux-and-role-folders.md
git commit -am "docs: AlmaLinux 10, role folders, env files and the MCP gate"
```

### Task 13: Throwaway checks, cleanup and the acceptance entry

**Files:**
- Modify: `docs/acceptance.md` (one new entry)

- [ ] **Step 1: Starting state.** Save
  `podman images --format '{{.Repository}}:{{.Tag}}'`,
  `podman ps -a --format '{{.Names}}'` and `podman volume ls -q` to
  `$SCRATCH/before-images`, `$SCRATCH/before-containers` and
  `$SCRATCH/before-volumes`. Use `podman` only; `docker` here is Docker
  Desktop's Windows shim.
- [ ] **Step 2: Bundle.** Build a `-dev.N` bundle into `$SCRATCH` with
  `gate scripts/package-release.py`.
- [ ] **Step 3: AlmaLinux container.** Start
  `podman run --label workbench-check=almalinux-work docker.io/library/almalinux:10`
  with a new non-root user and passwordless sudo.
  - **Install.** Copy the bundle in, extract `workbench`, and run
    `./workbench update <ver>-dev.N --bundle /tmp/bundle.tar.gz`.
  - **Answers.** The container sees the WSL kernel, so Workbench treats it
    as WSL. Run `init --answers-from FILE` with role `work`, the folder
    answers, `is_wsl = true` and the four `wsl_*` answers. Then append
    `[effects]\nskip = ["wsl-preferences"]` to its `machine.toml`.
  - **Lock.** As root, hold dnf's own RPMDB lock in the background for 90
    seconds:
    `python3 -c 'import dnf.lock, time; dnf.lock.build_rpmdb_lock("/var/lib/dnf", False).__enter__(); time.sleep(90)' &`.
    This is the call dnf makes before a transaction (`dnf/lock.py`; as
    root the lock is `/var/lib/dnf/rpmdb_lock.pid`).
  - **Apply.** `apply --dry-run --json`. Start the lock holder, then at
    once `apply --approve-plan DIGEST`. If the apply reached its first
    `sudo dnf` after the holder had exited, repeat with a longer hold.
  - **Expected.**
    - The first `sudo dnf` prints `Waiting for process with pid N to
      finish.` and goes on once the holder exits. No part reports a lock
      failure.
    - EPEL, the base packages, bat, fd, tmux, podman and the font
      (`fc-cache`) are installed. `rpm -q --whatprovides curl` names the
      package the image shipped, and the base part did not try to install
      `curl` over `curl-minimal`.
    - `dnf --version` reports dnf 4, the premise of the lock rule.
    - **tmux theme.** Run `tmux -L chk new-session -d -s chk`, wait two
      seconds, then `tmux -L chk show -gv status-right`. It names
      `plugins/tmux/scripts/`: the Dracula script appends
      `#($current_dir/<plugin>.sh)` entries to `status-right`, with
      `current_dir` its own folder (`scripts/dracula.sh` in
      `dracula/tmux`). So TPM ran and the theme loaded.
      `~/.tmux/plugins/tpm` and `~/.tmux/plugins/tmux` exist. Then run
      `tmux -L chk kill-server`.
    - Every chosen non-Windows step is complete.
    - The Windows steps report no interop.
- [ ] **Step 4: Workbench-side checks** in a second labelled AlmaLinux
  container on role `both`, set up as step 3 (bundle, passwordless sudo,
  `is_wsl = true`, the four `wsl_*` answers and
  `skip = ["wsl-preferences"]`), with non-default folders and synthetic
  values:
  - **Migration.** A `machine.toml` holding synthetic tokens ends with
    `work.env` holding all nine names at 0600, and the keys gone.
  - **Old Codex tokens.** A `~/.codex/config.toml` holding synthetic
    literal tokens loses them. The plan diff shows each removed line as
    `-JIRA_API_TOKEN = [REDACTED]` (the marker `redactDiff` writes,
    `internal/machine/files.go:195-216`), and no synthetic value appears
    anywhere in the dry run's output or `--json`.
  - **Env files.** The `env-files` lines appear as specified.
  - **Shell hook** (§4.10; Review Focus 3 and 4). The work folder is a
    symlink to another folder. Synthetic values are in both files, and
    `work.env` also holds `export JIRA_EMAIL=x` on line 2. In `bash -i`:
    - in the work folder (through the symlink too), the synthetic work
      names are exported and `JIRA_EMAIL` is not;
    - `cd <personal>/x && env` from the work folder shows no work name;
    - `chmod 640 work.env` stops it loading, and `chmod 400` loads it;
    - a last line without a newline is loaded.
  - **zsh syntax.** Install `zsh` in this container, render
    `home/dot_zshrc.tmpl` with the chezmoi Workbench installed there and
    this machine's answers, and run `zsh -n` on it. The zsh hook's
    behaviour needs a Mac and is recorded as not run.
  - **Claude Code entries.**
    - Install Claude Code there with npm and run `apply` again. User-scope
      entries hold the gate command, and a local-scope one is reported.
    - If `claude mcp` needs a login, record "not run" and leave it to Task
      14.
  - **Role switch.** Switching to `personal` removes both agents' entries
    and warns about `work.env`.
  - **Folders.** Non-default folders reach git, Codex trust and the costs
    report.
- [ ] **Step 5: Ubuntu container.** A labelled
  `docker.io/library/ubuntu:24.04` on role `personal`, set up as step 3:
  bundle, passwordless sudo, `is_wsl = true`, the four `wsl_*` answers and
  `skip = ["wsl-preferences"]`. The apt path is unchanged, and there is no
  `work.env`, Claude Code entry or dispatch entry.
- [ ] **Step 6: Clean up, compare, then delete the scratch folder.**
  1. `podman rm -f -v` every container labelled
     `workbench-check=almalinux-work`.
  2. `podman rmi` every image that is not in `$SCRATCH/before-images`,
     and `podman volume rm` every volume not in `$SCRATCH/before-volumes`.
  3. Compare, while the baseline still exists:
     `podman ps -a --filter label=workbench-check=almalinux-work` prints
     nothing, and the image, container and volume lists `diff` equal
     against `$SCRATCH/before-*`.
  4. Only then `rm -rf "$SCRATCH"`.
- [ ] **Step 7: Prove it.** Paste step 6.3's three empty `diff` outputs
  and the empty `podman ps` into the task report, then `ls "$SCRATCH"`,
  which reports no such folder.
- [ ] **Step 8: Acceptance entry.** List exactly what ran and what did not:
  - the containers ran under a WSL kernel;
  - arm64 did not run;
  - real agent behaviour, the real Windows writes and the WSL apply path on
    AlmaLinux wait for Task 14.
- [ ] **Step 9: Commit**

```bash
git commit -am "docs(acceptance): record the AlmaLinux 10 and role-folder checks"
```

### Task 14: Pre-release, owner checks, sign-off (owner)

No agent runs anything in this task. The agent prepares this checklist and
fixes what the owner reports.

- [ ] **Step 1: Tag.** The owner tags the next free `-rc.N` (check
  `git tag --list 'v0.1.28-rc.*'`; `v0.1.28-rc.3` for the first, unless
  the owner chooses another) and pushes it. Call it `<rc>` below.
- [ ] **Step 2: AlmaLinux.**
  1. `workbench update <rc>`.
  2. `workbench init --ask machine_role`, choosing `work`.
  3. `workbench apply --dry-run`, and read the plan.
  4. `workbench apply`.

  Expected:
  - 0005 moves the tokens.
  - `epel` sits under Already set.
- [ ] **Step 3: Ubuntu.** Switch the role to `personal` first, with the
  installed release, then update, dry-run and apply. Expected: no dispatch
  entries in either agent and no `work.env`.
- [ ] **Step 4: Agent behaviour,** with the owner's real agents. Claude Code
  and Codex in `~/repos/<repo>`:
  - list the dispatch tools;
  - deny `cat` of `work.env` (Claude Code);
  - show the work names in a Bash command.

  In `~` and `~/dev/<repo>`:
  - they list none;
  - they show no error or warning;
  - they show no work name.

  Also note:
  - the gate's idle memory (`ps -o rss`);
  - Codex's startup time;
  - whether Codex sub-agents start their own copies.

  These numbers go into `claudeMCPEffect.What` in the next `-rc`.
- [ ] **Step 5: Windows Terminal (optional).**
  1. In AlmaLinux, `apply --choose`, tick **Replace Windows Terminal
     settings**, and apply. A copy of the old file is kept.
  2. In Ubuntu, `apply --dry-run` shows
     `terminal-adoption: = written by AlmaLinux-10`, and a real apply
     leaves the file alone.

  If anything is wrong, restore the copy and report the plan text.
- [ ] **Step 6: Fixes.** Each reported problem is fixed on the branch.
  Repeat from step 1, which tags the next free `-rc.N` as the new `<rc>`.
- [ ] **Step 7: Sign-off.** The owner merges `feat/almalinux-work-distro`
  into `main`, then tags the release without `-rc` on `main`. The release
  workflow publishes it and commits the regenerated `CHANGELOG.md`.

## Found while planning (follow-ups, not in this change)

- **The RestartWSL helpers.** They are part of the ticked Windows setup
  step on both distributions. They include a `.bat` running
  `powershell -ExecutionPolicy Bypass -File restart-wsl.ps1`, and a
  `#Requires -RunAsAdministrator` script that registers a scheduled task
  restarting `vmcompute`
  (`home/.chezmoiscripts/wsl/run_after_10-deploy-windows-configs.sh.tmpl:142-171`).
  - Workbench writes them but never runs them.
  - Without admin they cannot work, and a monitored laptop may flag them.
  - Suggestion: make them their own optional step, off by default.
- **PowerShell probes on every plan.** Every WSL plan already runs
  `powershell.exe -NoProfile -Command` and `reg.exe query` probes: the
  profile path, the user PATH and the font registry
  (`run_before_00-packages-windows.sh.tmpl:63-67`,
  `run_after_10-deploy-windows-configs.sh.tmpl:58`). A monitored laptop
  logs them on each `apply --dry-run`. Suggestion: run them only when their
  step is ticked.
- **VS Code on Windows.** Its settings and extensions are not managed from
  WSL. Workbench's `~/.config/Code/User/settings.json` is not read by
  Windows VS Code. A Windows-side step would need the same one-writer
  record.
