# AlmaLinux 10 Work Distribution Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Workbench runs on AlmaLinux 10. Each role has a folder the owner
picks. Credentials live in per-folder env files. The work MCP servers start
only inside the work folder for both Claude Code and Codex. Two WSL
distributions share the Windows side safely. Bitwarden is gone.

**Architecture:** One shared helper makes hidden apply-step subcommands
(`migrate`, `env-files`, `claude-mcp`), and `mcp-gate` is a hidden command
of its own. Go owns the dispatch server definition and passes it to the
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
  mkdir -p "$SCRATCH"/{tmp,home,config,data,state,cache,bin}
  SAFE_PATH=$(tr ':' '\n' <<<"$PATH" | grep -v '^/mnt/' | paste -sd:)
  gate()     { env PATH="$SAFE_PATH" TMPDIR="$SCRATCH/tmp" "$@"; }
  isolated() { env PATH="$SAFE_PATH" TMPDIR="$SCRATCH/tmp" HOME="$SCRATCH/home" \
                   XDG_CONFIG_HOME="$SCRATCH/config" XDG_DATA_HOME="$SCRATCH/data" \
                   XDG_STATE_HOME="$SCRATCH/state" XDG_CACHE_HOME="$SCRATCH/cache" \
                   WORKBENCH_BIN_DIR="$SCRATCH/bin" "$@"; }
  ```

  With `/mnt/` taken out of `PATH`, the WSL scripts take their "no Windows
  interop" path, so no Windows program can run. `isolated` keeps the live
  home, the costs ledger and the agents' files out of reach. Never rely on
  `--destination` alone for isolation.
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
    exceptions. `mcp-gate`'s `exec` becomes one of them.
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
    and 9) and are never copied into a second file, tests included.
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
  reported. The file is never sourced or executed. It must pass
  `operation.ReadPrivateInput`'s checks.
- **Probe lines.** No probe line contains `; `, which the plan view uses to
  join lines.
- **Release path (owner's rule).** This change ships first as a
  pre-release, `v0.1.28-rc.3` unless the owner picks another. The owner
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
   symlinks before comparing (Task 8 test; Task 13 step 5).
4. **An owner-edited env file with `export NAME=value` or quotes.** The plan
   line names the line number, and the line is skipped. It is never
   half-loaded and never crashes anything (Task 7, step 4).
5. **A session started in the work folder itself** (`~/repos`, not a
   repository) **or through a symlinked path.** It counts as inside (Task 8
   test).

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
  - `func RoleFolders(c operation.Context) []string`: the saved folders as
    absolute paths, personal first, read through `readAnswers`. When none
    are saved it returns `[home/dev, home/repos]`.

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
- Modify: `internal/costs/ledger.go:29-35` (`Paths.Roots`), `internal/costs/report.go:292,296,356-386,456-459`, `internal/costs/focus.go:93`
- Modify: `internal/cli/costs.go:72-77,743-751`
- Modify: `internal/machine/effects.go:183-194` (`linuxPackagesEffect` text)

**Interfaces:**
- Consumes: the folder template data and `machine.RoleFolders` (Task 1).
- Produces:
  - A field `Roots []string` on `costs.Paths`. The `cli` costs commands fill
    it from `machine.RoleFolders`; when it is empty, `costs` uses
    `[home/dev, home/repos]`.
  - `ScopeRoots(p Paths) []string`: the roots that exist on disk.
  - `rollup(project string, roots []string) string`, which takes every
    root.

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
  - Add `Roots` and fill it as described in Interfaces.
  - `projectRoots(p Paths)` returns `p.Roots`, or the fallback.
  - `ScopeRoots(p Paths)` and `rollup` change as described.
  - Update the callers: `report.go:292` (`loadGroups` takes `paths`
    instead of `paths.Home`), `report.go:296` and `:456-459`, and
    `focus.go:93`.
  - The `--all` flag help in `cli/costs.go:77` becomes
    `Include projects outside your role folders`, so it needs no lookup at
    construction.
  - `scopeNames`' comment names the role folders.
- [ ] **Step 6: Effect text.** `linuxPackagesEffect`'s Description, What
  and Touches say "your work and personal folders".
- [ ] **Step 7: Run the gates.** Then:
  - Render `both pinned` with a scratch config set to
    `work_folder = "~/work"`. The `gitdir` lines, Codex trust, `AGENTS.md`,
    the Windows `--cd` and the `mkdir` line all name `~/work`.
  - Run `rg --hidden -n '~/repos|~/dev|/repos\b|"dev"' home internal`. It
    shows only defaults and the costs fallback.
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
  - **The lock.** dnf 4 waits on a held lock without limit, so wrap every
    `sudo dnf` in `timeout 600`. On exit 124, set
    `why="another dnf held the package lock for ten minutes"`.
  - **Header comment.** It names dnf.
- [ ] **Step 5: podman.** `30-global-tools` gets a dnf branch:
  `timeout 600 sudo dnf install -y podman`.
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
  Expected: exactly one hit, the `work-tools` Undo text.
- [ ] **Step 6: Commit.** The subject carries the owner notice (§4.12).

```bash
git commit -am "feat!: remove Bitwarden; run bw lock first to clear its cached session"
```

### Task 5: Shared apply-step helpers

**Files:**
- Create: `internal/cli/applystep.go`
- Modify: `internal/cli/migrate.go:17-125`
- Modify: `home/.chezmoitemplates/installed-workbench.sh`, `home/.chezmoiscripts/shared/run_after_05-migrations.sh.tmpl:10-19`
- Modify: `internal/operation/state.go` (new `CreatePrivateFile` beside `ReplacePrivateFile`)

**Interfaces:**
- Produces:
  - `func applyStep(o *options, effect string, probe func(io.Writer) error, run func(context.Context, func(string)) error) []*cobra.Command`.
    It returns the hidden `--probe` behaviour, which is never refused, and
    the hidden `run`, which is refused unless
    `WORKBENCH_EFFECT_<EFFECT>=1` (upper case, `-` to `_`) and
    `!operation.AgentSession()`, with the message `migrate run`'s error
    has today.
  - Shell function `knows_command NAME`, which succeeds when
    `"$workbench" NAME --help` prints `workbench NAME [`.
  - `func CreatePrivateFile(path string, data []byte) error`. It writes a
    0600 temp file in the same folder and fsyncs it, then
    `os.Link(temp, path)`, which fails when `path` exists. Then it removes
    the temp file and fsyncs the folder.

- [ ] **Step 1:** Write `applyStep`, and rewrite `migrateRunCommand` and
  `migrate --probe` on it. `migrate`'s behaviour and messages stay as they
  are.
- [ ] **Step 2:** Move `knows_migrations` into `installed-workbench.sh` as
  `knows_command`, and call `knows_command migrate` in `05-migrations`.
- [ ] **Step 3:** Add `CreatePrivateFile`, with a comment in the file's
  style.
- [ ] **Step 4: Run the gates.** Then, in the `isolated` environment, run
  `workbench migrate` and `migrate --probe`. Both behave as before.
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
- Consumes: `operation.CreatePrivateFile` and `operation.ReplacePrivateFile`
  (Task 5).
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
  - Run `carryRetired` then `encodeMachineConfig`, the two calls
    `saveAnswers` and `AdoptionPlan` both make.
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
  - **Run.** Create `work.env` with `CreatePrivateFile`. Then delete the
    two keys from `data`, `toml.Marshal` the whole map, and write it with
    `ReplacePrivateFile`.
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
- Modify: `internal/cli/root.go:100-110`, `internal/machine/effects.go` (`sharedEffects`, `effectSources`)
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
  - **Run.** Create each missing file for the role with
    `CreatePrivateFile`. Skip `work.env` while `0005-work-tokens` is not
    `Done`.
- [ ] **Step 2: CLI, script and effect.**
  - `workbench env-files` is hidden and built with `applyStep`. Register it
    in `root.go`.
  - The script copies `05-migrations`' shape, using `knows_command
    env-files`.
  - `envFilesEffect` joins `sharedEffects` with every field filled; its
    Title is `Credential files`. Add `"env-files": {"06-env-files"}` to
    `effectSources`.
- [ ] **Step 3: Shell hook in bash.** Render the expanded folder answers
  and `workbench-config` into `__workbench_folder_env`.
  - **Interactive only.** It returns at once unless `$-` contains `i`.
  - **Cache.** It keeps the last `pwd -P` and each file's `stat -c %Y`, and
    does nothing when neither has changed.
  - **Unload.** It unsets every name listed in the file of a role folder the
    shell is not in.
  - **Load.** When the file is mode `600` by `stat -c %a`, it exports the
    file of the folder it is in, reading lines with
    `while IFS='=' read -r name value` and keeping only valid names. It
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
  - mtime and mode come from `zmodload zsh/stat; zstat +mtime` and
    `+mode`;
  - the guard is `[[ -o interactive ]]`;
  - registration is `autoload -Uz add-zsh-hook`, `add-zsh-hook chpwd` and
    `add-zsh-hook precmd`.

  It keeps the same three wrappers.
- [ ] **Step 5: Check Review Focus 4.** In the `isolated` environment, a
  `work.env` holding `export JIRA_EMAIL=x`:
  - shows `line N is not NAME=value` in `isolated workbench apply
    --local-build --dry-run`;
  - is not exported by the hook in `isolated bash -i`.
- [ ] **Step 6: Claude Code deny rules.** In `modify_private_settings.json`,
  append `printf "Read(/%s/work.env)" $config` when `.has_work`, and the
  same for `personal.env` when `.has_personal`, to
  `$enforced.permissions.deny`. `$config` is absolute, so each rule starts
  with `Read(//`.
- [ ] **Step 7: Agent text.** In `CLAUDE.md.tmpl` and Codex's
  `AGENTS.md.tmpl`, for roles with an env file, add:
  "Credentials are in environment variables loaded from Workbench's env
  files. Use them by name; never open, print or echo the files or their
  values."
- [ ] **Step 8: Run the gates.** Then, in `isolated bash -i` on `both` with
  synthetic values and a symlinked work folder, the shell-hook checks in
  §4.10 pass, including `cd <personal>/x && env` from the work folder.
- [ ] **Step 9: Commit**

```bash
git add internal/machine/envfiles.go internal/cli/envfiles.go home/.chezmoiscripts/shared/run_after_06-env-files.sh.tmpl
git commit -am "feat: per-folder credential files with a shell hook and deny rules"
```

### Task 8: The `mcp-gate` command

**Files:**
- Create: `internal/cli/mcpgate.go`, `internal/cli/mcpgate_test.go`
- Modify: `internal/cli/root.go:100-110`
- Modify: `internal/operation/process.go:389-395` (the documented exceptions)

**Interfaces:**
- Consumes: `machine.ParseEnvFile` (Task 7) and `operation.ReadPrivateInput`.
- Produces:
  - `workbench mcp-gate --folder DIR --env-file FILE -- COMMAND ARGS...`,
    hidden.
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
    Set each pair with a value over the inherited environment, then
    `syscall.Exec` the command. When the file is missing or unsafe, exit 1
    with `mcp-gate: <file> is missing or readable by others` on stderr.
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
  - **Exceptions.** Add the gate's `exec` to `Run`'s exceptions comment.
- [ ] **Step 5: Smoke the gate** with no agent involved. Pipe the
  `initialize`, `tools/list`, `prompts/list`, `resources/list` and `ping`
  lines into
  `isolated workbench mcp-gate --folder $SCRATCH/home/work --env-file $SCRATCH/config/workbench/work.env -- /usr/bin/env`,
  with `CLAUDE_PROJECT_DIR` both set and unset.
  - From a cwd outside the folder, it gives empty-list replies and exits 0.
  - From inside, `env` prints the synthetic names.
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

- [ ] **Step 1:** Write `dispatch.go`. `prepare` sets
  `nativeAnswers["dispatch_servers"]` beside `workbench_bin`.
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
- [ ] **Step 4: Run the gates.** Then pipe a synthetic existing config, with
  literal tokens, through the rendered modify script:
  `isolated python3 -I <rendered> < synthetic.toml`. The output has no
  `TOKEN =` line under the dispatch `env` tables, and `NODE_VERSION`
  stays.
- [ ] **Step 5: Commit**

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
  - `dispatchServers` (Task 9) and `applyStep` (Task 5);
  - `findAgent(home, nvm string, agent agent) (agentInstall, bool)` with
    `nvmDefaultBin(home)` (`internal/machine/agents.go:345,435`).
- Produces:
  - `func ClaudeMCPProbe(c operation.Context, a Answers) []string`.
  - `func ClaudeMCPCommands(c operation.Context, a Answers) (claude string, commands [][]string)`:
    the resolved `claude` path, and the argument lists to run, in order.
  - `workbench claude-mcp --probe` and `workbench claude-mcp --commands`
    (hidden). `--commands` prints the `claude` path, then each command's
    arguments, every field NUL-terminated, with an empty field ending each
    command.
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
- [ ] **Step 2: Commands.** For each server, run
  `mcp remove --scope user NAME`, then
  `mcp add-json --scope user NAME JSON`, where JSON is
  `{"type":"stdio","command":…,"args":…,"env":{"NODE_VERSION":"default"}}`.
  Without the work role, only the removes run.
- [ ] **Step 3: Script.**
  - **Shape.** It follows `05-migrations`, using `knows_command
    claude-mcp`.
  - **Run.** It reads `--commands` with `while IFS= read -r -d ''` and runs
    each as `"$claude" "${args[@]}"`, with `PATH` set to the `claude`
    folder plus `nvmDefaultBin`'s folder (Go prints both).
  - **Exit status.** A `remove` exit status is ignored. A failed `add-json`
    calls `part_blocked claude-mcp`. No `claude` path calls
    `part_blocked claude-mcp "Claude Code is not installed"`.
  - **No names in the script.** The script itself names no server; the
    names come only from Go at run time, so the personal leak check
    passes.
- [ ] **Step 4: CLI and effect.**
  - The command is built with `applyStep`; add a `--commands` flag.
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
- Modify: `internal/machine/plan.go` (input, take-over effects, `scriptsEnvironment` at `:967-975`), `internal/machine/selection.go` (never save `take-over-*`), `internal/machine/revert.go`, `internal/machine/effects.go` (the four steps' Undo)

**Interfaces:**
- Produces:
  - `func windowsWriters(ctx context.Context, c operation.Context) operation.Input`.
    - On WSL only, and not for an isolated destination.
    - It finds `cmd.exe` and `wslpath` the way `windowsSystemDirectories`
      does, and runs `cmd.exe /c echo %LOCALAPPDATA%` then `wslpath -u`
      through `operation.Run` (read-only, no mutation, 60 seconds).
    - It returns `{Name: "windows-writers", Digest: <sha256 of <LocalAppData>/Workbench/writers.json, or "absent">}`.
    - On any failure it returns `"unknown"` and the plan adds a warning.
  - The record, `{"terminal-adoption": "<distro>", …}`, keyed by the four
    step names.
  - Shell function `writer_line STEP RECORD_FILE INSTALLED_LIST`, which
    prints the probe line for the four rules (§4.7), and `writer_record
    STEP RECORD_FILE`. Both read and write JSON with `python3 -I -c`, and
    `writer_record` writes a new file then renames it.
  - Optional plan effects `take-over-<step>`, listed only when that step's
    probe line starts `= written by`. Checking one adds the step to
    `WORKBENCH_WINDOWS_TAKE_OVER` in `scriptsEnvironment`. `selectionToSave`
    and `decided` never save `take-over-*`.

- [ ] **Step 1: `windows-writers.sh`.** Both WSL scripts include it.
  - Both scripts ask `%LOCALAPPDATA%`; the deploy script adds it to its
    line-54 echo.
  - Both run `win_start distros wsl.exe --list --quiet` in probe mode and
    run mode alike, through `win_start`/`win_wait`.
- [ ] **Step 2: Apply the rules** in each of the four steps.
  - **Probe.** Print `writer_line`.
  - **Run.** Skip the step when another installed distribution is
    recorded, unless the step is in `WORKBENCH_WINDOWS_TAKE_OVER`. After
    writing, call `writer_record`.
- [ ] **Step 3: Go side.** Add `windowsWriters` to the plan inputs, the
  `take-over-<step>` effects with every field filled (Title:
  `Take over <step title> from <distro>`), and `scriptsEnvironment`. Make
  `selection.go` skip them.
- [ ] **Step 4: Text.** The four steps' Undo and `revert`'s output name
  the record and say that deleting it is safe.
- [ ] **Step 5: Run the gates.** Then check the four rules without any
  Windows program: `source` `windows-writers.sh` in `gate bash` and call
  `writer_line` with a scratch record and installed list for each case:
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
    `mcp-gate` and `applyStep`.
  - `epel` named as on by default.
  - The 0005 exception already landed in Task 6.
- [ ] **Step 3: Contracts doc.**
  - Lines 39, 80, 135 and 142–143.
  - The targets table and the prerequisites paragraph.
  - `mcp-gate`'s `exec` among the process exceptions.
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
  `$SCRATCH`. Use `podman` only; `docker` here is Docker Desktop's Windows
  shim.
- [ ] **Step 2: Bundle.** Build a `-dev.N` bundle into `$SCRATCH` with
  `gate scripts/package-release.py`.
- [ ] **Step 3: AlmaLinux container.** Start
  `podman run --label workbench-check=almalinux-work docker.io/library/almalinux:10`
  with a new non-root user and sudo.
  - **Install.** Copy the bundle in, extract `workbench`, and run
    `./workbench update <ver>-dev.N --bundle /tmp/bundle.tar.gz`.
  - **Answers.** The container sees the WSL kernel, so Workbench treats it
    as WSL. Run `init --answers-from FILE` with role `work`, the folder
    answers and the `wsl_*` answers. Then append
    `[effects]\nskip = ["wsl-preferences"]` to its `machine.toml`.
  - **Apply.** `apply --dry-run --json`, then `apply --approve-plan DIGEST`.
  - **Expected.**
    - EPEL, the base packages, bat, fd, tmux, podman and the font
      (`fc-cache`) are installed.
    - `tmux -c 'tmux show -g @dracula-plugins'` loads.
    - Every chosen non-Windows step is complete.
    - The Windows steps report no interop.
- [ ] **Step 4: Workbench-side checks** in a second labelled AlmaLinux
  container on role `both`, with non-default folders and synthetic values:
  - **Migration.** A `machine.toml` holding synthetic tokens ends with
    `work.env` holding all nine names at 0600, and the keys gone.
  - **Old Codex tokens.** A `~/.codex/config.toml` holding synthetic
    literal tokens loses them, and the plan diff shows
    `TOKEN = <redacted>`.
  - **Env files.** The `env-files` lines appear as specified.
  - **Claude Code entries.**
    - Install Claude Code there with npm and run `apply` again. User-scope
      entries hold the gate command, and a local-scope one is reported.
    - If `claude mcp` needs a login, record "not run" and leave it to Task
      14.
  - **Role switch.** Switching to `personal` removes both agents' entries
    and warns about `work.env`.
  - **Folders.** Non-default folders reach git, Codex trust and the costs
    report.
- [ ] **Step 5: Ubuntu container.** In a labelled
  `docker.io/library/ubuntu:24.04` on role `personal`, the apt path is
  unchanged, and there is no `work.env`, Claude Code entry or dispatch
  entry.
- [ ] **Step 6: Clean up.**
  - `podman rm -f -v` every container labelled
    `workbench-check=almalinux-work`.
  - `podman rmi` every image that is not in step 1's list.
  - `rm -rf "$SCRATCH"`.
- [ ] **Step 7: Prove it.** Paste these into the task report:
  - `podman ps -a --filter label=workbench-check=almalinux-work`, which
    prints nothing;
  - the image list and `podman volume ls -q`, each equal to step 1's;
  - `ls "$SCRATCH"`, which reports no such folder.
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

- [ ] **Step 1: Tag.** The owner tags the pre-release (`v0.1.28-rc.3`
  unless the owner chooses another) and pushes it.
- [ ] **Step 2: AlmaLinux.**
  1. `workbench update v0.1.28-rc.3`.
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
- [ ] **Step 6: Fixes.** Each reported problem is fixed on the branch, and
  the owner tags the next `-rc.N`. Repeat from step 2.
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
