# AlmaLinux 10 Work Distribution Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Workbench runs on AlmaLinux 10. Each role has a folder the owner
picks. Credentials live in per-folder env files. The work MCP servers start
only inside the work folder for both Claude Code and Codex. Two WSL
distributions share the Windows side safely. Bitwarden is gone.

**Architecture:** The Go CLI gains hidden subcommands (`mcp-gate`,
`env-files`, `claude-mcp`) that run as apply steps the way `migrate run`
does. The chezmoi templates read the new folder answers and the config
folder. Migration 0005 moves the saved tokens. The Windows scripts read and
write a record of which distribution writes each shared Windows file.

**Tech Stack:** Go 1.x (Cobra, go-toml/v2), chezmoi templates, bash/zsh,
Python (Codex config modify script), PowerShell via `cmd.exe`/`wsl.exe`.

**Spec:** `docs/superpowers/specs/2026-10-21-almalinux-work-distro-design.md`.
Read it in full before any task. Section numbers below (§4.11 and so on)
point into it.

## Global Constraints

- **Project rules.** `AGENTS.md` governs. Greenfield: no compatibility
  readers except the ones the spec names (retired token keys,
  migration 0005).
- **Tests.** Keep them near zero: only the safeguard tests named in tasks
  5, 6 and 7, each with its risk written beside it. No TDD mandate, no
  per-feature suites.
- **Gates** after every task: `golangci-lint fmt`,
  `golangci-lint config verify`, `golangci-lint run ./...`,
  `go build ./...`, `go test ./...`. For tasks that touch `home/`, also
  `scripts/render-check.sh ROLE MODE` and `scripts/render-check.sh ROLE MODE wsl`
  for ROLE in `personal work both` and MODE in `pinned latest`.
- **Platform tests.** Only in `internal/platform/` (ADR 0012; `forbidigo`
  bans `runtime.GOOS` elsewhere).
- **No credential value** in a plan, `--json`, log, rendered file, agent
  config, commit or the repository. Smoke checks use synthetic credentials
  and disposable homes only. Never provision the developer's live machine.
- **No personal data** in tracked files or commit messages: no real
  repository names, home paths, emails or spend. The two company addresses
  already in `home/.chezmoitemplates/codex-config.toml.tmpl` move into
  migration 0005; they are not copied anywhere else.
- **Commit subjects** are conventional and become the changelog. End each
  commit message with
  `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`.
- **Work on a branch** (`feat/almalinux-work-distro`), never on `main`.
- **Folder answers** are home-relative text (`~/repos`). Each segment uses
  only letters, digits, `.`, `_` and `-`, with no `.` or `..` segment, and
  the answer is not `~` itself. On `both`, the two answers differ and
  neither contains the other.
- **Defaults** are `work_folder = "~/repos"` and `personal_folder = "~/dev"`.
- **`work.env` names**, in this order: `JIRA_BASE_URL`, `JIRA_EMAIL`,
  `JIRA_API_TOKEN`, `CONFLUENCE_BASE_URL`, `CONFLUENCE_EMAIL`,
  `CONFLUENCE_API_TOKEN`, `TEMPO_API_TOKEN`, `GITLAB_URL`, `GITLAB_TOKEN`.
  `personal.env` starts with no names.
- **Env file format** is `NAME=value` lines, `#` comments and blank lines.
  Files are mode 0600, owned by the user, with no group or other bits.
- **Retired answer keys** are `jira_api_token` and `gitlab_token`.

## Review Focus

1. **Existing machine, first apply after upgrade, at a terminal.** The new
   folder questions trigger a re-ask before migration 0005 runs. The tokens
   must survive it. (Task 5, safeguard test
   `TestRetiredTokensSurviveEveryAnswerWrite`.)
2. **A folder answer typed with a trailing slash** (`~/repos/`). A person
   expects it accepted, not refused. The questionnaire drops one trailing
   slash before validating and saving. (Task 1, step 1.)
3. **A symlinked work folder** (`~/repos` pointing at another disk). The gate
   and the shell hook both resolve symlinks before comparing. (Task 7,
   safeguard test cases; Task 6, smoke step.)
4. **An owner-edited env file with `export NAME=value` or quotes**, which is
   habit from other tools. Expected: a clear plan line naming the line
   number, and that line skipped. Never a half-loaded file, never a crash.
   (Task 6, step 3.)
5. **A session started in the work folder itself**, which is not a
   repository (`~/repos`), or through a symlinked path. Expected: inside.
   (Task 7, safeguard test cases.)

---

### Task 1: Folder answers

**Files:**
- Modify: `home/.chezmoi.toml.tmpl`
- Modify: `internal/machine/answers.go`
- Modify: `internal/machine/plan.go:330` (native answers)
- Create: `internal/operation/folders.go`
- Modify: `scripts/scratch-init.sh`

**Interfaces:**
- Produces:
  - Answer keys `work_folder` (when `has_work`) and `personal_folder` (when
    `has_personal`).
  - Native answer `workbench_config` (absolute `c.Paths.Config`), beside
    `workbench_bin`.
  - `func (a Answers) folder(home, key string) string` in `answers.go`:
    the absolute path, or `""` when the key is absent.
  - `func RoleFolders(config, home string) []string` in
    `internal/operation/folders.go`: the saved folders as absolute paths,
    personal first, then work. It reads only `[data].personal_folder` and
    `[data].work_folder` from `<config>/machine.toml`, and returns
    `[home/dev, home/repos]` when the file or both keys are missing.

- [ ] **Step 1: Ask the folders in the questionnaire.** In
  `home/.chezmoi.toml.tmpl`, after the identity questions:
  - `promptStringOnce . "work_folder" "Work folder (under your home)" "~/repos"`
    when `$hasWork`.
  - `promptStringOnce . "personal_folder" "Personal folder (under your home)" "~/dev"`
    when `$hasPersonal`.

  Drop one trailing `/` (the saved value never ends in `/`), then `fail`
  unless the value matches
  `^~(/[A-Za-z0-9._-]+)+$` with no `.` or `..` segment. On `both`, `fail`
  when the two are equal or one is a prefix of the other plus `/`.
  - Change the `both` email prompts to `Git personal email (repos in your personal folder)`
    and `Git work email (repos in your work folder)`.
  - Rewrite the header comment so it names no fixed folder.
- [ ] **Step 2: Validate in Go.** In `answers.go`:
  - Add both keys to `answerKeys`.
  - In `invalidKey`, after identity, require each role's folder with the
    same pattern (a saved value with a trailing `/` is invalid, since the
    questionnaire never saves one), and the not-equal, not-nested rule on
    `both`.
  - Add `folder(home, key)`.
- [ ] **Step 3: Pass the config folder to templates.** In `plan.go`, set
  `nativeAnswers["workbench_config"] = c.Paths.Config` next to
  `workbench_bin`, with a comment in the same style.
- [ ] **Step 4: Add `operation.RoleFolders`** as specified in Interfaces.
  It uses go-toml/v2, reads at most 1 MiB, and never fails: an unreadable
  file gives the defaults.
- [ ] **Step 5: Update `scripts/scratch-init.sh`.**
  - Add `--promptString "Work folder (under your home)=~/repos"` and
    `--promptString "Personal folder (under your home)=~/dev"`.
  - Replace the two email prompts with the new text.
  - Keep the token prompts until Task 5.
- [ ] **Step 6: Run the gates** (Global Constraints), including every
  render-check. Expected: no output change outside the new answers.
- [ ] **Step 7: Commit**

```bash
git add home/.chezmoi.toml.tmpl internal/machine/answers.go internal/machine/plan.go internal/operation/folders.go scripts/scratch-init.sh
git commit -m "feat(answers): ask a folder for each role"
```

### Task 2: Every folder consumer reads the answers

**Files:**
- Modify: `home/dot_gitconfig.tmpl`, `home/dot_gitconfig-work.tmpl`, `home/dot_gitconfig-personal.tmpl`
- Modify: `home/.chezmoitemplates/codex-config.toml.tmpl:33-45`
- Modify: `home/private_dot_codex/modify_private_config.toml.tmpl:47-56` (`INACTIVE_TABLES`)
- Modify: `home/private_dot_codex/AGENTS.md.tmpl:29-40`
- Modify: `home/.chezmoiscripts/wsl/run_before_00-packages-windows.sh.tmpl:115-117,140,167`
- Modify: `home/.chezmoiscripts/linux/run_once_before_00-packages.sh.tmpl:65-66`
- Modify: `internal/costs/report.go:356-386`, `internal/costs/focus.go:93`, `internal/cli/costs.go:77,743-751`
- Modify: `internal/machine/effects.go` (`linuxPackagesEffect` text)

**Interfaces:**
- Consumes: the `work_folder` and `personal_folder` template data and
  `operation.RoleFolders` (Task 1).
- Produces:
  - `costs.ScopeRoots(p Paths) []string`, replacing `ScopeRoots(home string)`.
  - `projectRoots(p Paths) []string`, which returns
    `operation.RoleFolders(p.Runtime.Config, p.Home)`.

- [ ] **Step 1: git.** `includeIf "gitdir:<answer>/"` uses each answer
  (git expands `~`). Update the header comment and the two include
  comments.
- [ ] **Step 2: Codex.**
  - The trust tables in `codex-config.toml.tmpl` use the expanded answer
    (`joinPath .chezmoi.homeDir (trimPrefix "~/" .work_folder)`), each only
    for its role.
  - `INACTIVE_TABLES` names the expanded answer of each folder the current
    role does not use, when that answer is saved. Otherwise it names the
    default, as today.
  - In `AGENTS.md.tmpl` the path policy names the expanded answers.
- [ ] **Step 3: Windows start folder.** In
  `run_before_00-packages-windows.sh.tmpl`, the start folder is the work
  answer when `.has_work`, otherwise the personal answer. Apply it to:
  - `$wslRepos`: `\\wsl.localhost\<distro>\home\<user>\<rest>`, with the
    answer's `/` turned into `\`.
  - The `--cd` argument.
  - The `startingDirectory` fallback.

  Rename `$wslRepos` to `$wslStart`.
- [ ] **Step 4: Folder creation.** Replace line 66 with `mkdir -p` of the
  expanded work answer under `{{ if .has_work }}` and of the personal
  answer under `{{ if .has_personal }}`. Update the comment above it.
- [ ] **Step 5: Costs.**
  - Change `projectRoots` and `ScopeRoots` as in Interfaces.
  - `rollup(project string, roots []string)` takes the roots.
  - Update the callers in `report.go`, `focus.go` and `internal/cli/costs.go`.
  - `scopeNames`'s comment names "the role folders".
- [ ] **Step 6: Effect text.** `linuxPackagesEffect` Description, What and
  Touches say "your work and personal folders" instead of `~/dev` and
  `~/repos`.
- [ ] **Step 7: Run the gates.** Then render `both pinned` with a scratch
  config edited to `work_folder = "~/work"` and check by eye:
  - the `gitdir` lines;
  - Codex trust;
  - `AGENTS.md`;
  - the Windows `--cd`;
  - the `mkdir` line.

  Every one names `~/work`.
- [ ] **Step 8: Commit**

```bash
git commit -am "feat: read the role folders everywhere instead of ~/repos and ~/dev"
```

### Task 3: AlmaLinux 10, dnf and the EPEL step

**Files:**
- Modify: `internal/machine/host_linux.go:18-37`
- Modify: `internal/platform/facts_linux.go`
- Modify: `internal/machine/effects.go` (new `epelEffect`, `effectSources`, `linuxPackagesEffect` text)
- Modify: `internal/machine/effects_linux.go`
- Modify: `internal/machine/effects_test.go:46-51`
- Modify: `home/.chezmoiscripts/linux/run_once_before_00-packages.sh.tmpl`
- Modify: `home/.chezmoiscripts/linux/run_onchange_after_30-global-tools.sh.tmpl`
- Modify: `home/private_dot_bashrc.tmpl`
- Modify: `scripts/render-check.sh:97`

**Interfaces:**
- Produces:
  - `platform.PackageManager` (`sync.OnceValue`) returning `"apt"` when
    `apt-get` is found on `PATH`, `"dnf"` when `dnf` is found and `apt-get`
    is not, and `""` otherwise.
  - `epelEffect operation.Effect` with `Name: "epel"`, on by default, not
    `Optional`.
  - Script switch `WORKBENCH_EFFECT_EPEL`.

- [ ] **Step 1: Platform check.** `hostVersion` also sets
  `StatusComplete` when `ID == "almalinux"` and the part of `VERSION_ID`
  before the first `.` is `"10"`. Update the comment.
- [ ] **Step 2: Package-manager fact.** Add `PackageManager` to
  `facts_linux.go` with a comment in the file's style.
- [ ] **Step 3: The `epel` effect.**
  - Text: Title `EPEL`. Summary `turns on Fedora's extra packages for
    AlmaLinux (bat, fd)`. Privilege `sudo; network`. Undo `Not reverted
    automatically: sudo dnf remove epel-release`.
  - Add `"epel": {"00-packages"}` to `effectSources`.
  - In `platformEffects`, list it first when
    `platform.PackageManager() == "dnf"`.
  - Add `epelEffect` to the `linux` list in
    `TestSharedScriptsGateEverySectionOnItsOwnEffect`.
  - `linuxPackagesEffect` text names apt on Ubuntu and dnf on AlmaLinux.
    Its Undo is `remove packages with apt or dnf`.
- [ ] **Step 4: Package script.** In `run_once_before_00-packages.sh.tmpl`:
  - **EPEL probe.** In the probe block, when `command -v dnf` and not
    `command -v apt-get`: print `epel: = EPEL is on` when
    `rpm -q epel-release` succeeds, otherwise
    `epel: turn on EPEL (uses sudo)`.
  - **EPEL part.** Before the base part, under
    `WORKBENCH_EFFECT_EPEL:-1`: `part epel "dnf could not install
    epel-release" install_epel`, which runs
    `sudo dnf install -y epel-release` only when `rpm -q` fails.
  - **Base part.** A `dnf` branch installs, from `gcc gcc-c++ make curl
    unzip git ca-certificates fontconfig`, only the packages `rpm -q` does
    not find.
  - **bat and fd.** A `dnf` branch runs `sudo dnf install -y bat` and
    `fd-find`, and makes no `~/.local/bin` link. Before installing, when
    `dnf repolist --enabled` does not list `epel`, fail the part with
    `why="EPEL is off; turn on the epel step or enable EPEL"`.
  - **dnf lock.** Set it per the builder's check (spec §4.3): the same
    ten-minute wait as apt, or fail with the reason.
  - **Header comment.** Name dnf.
- [ ] **Step 5: podman.** In `30-global-tools`, add a `dnf` branch:
  `sudo dnf install -y podman`.
- [ ] **Step 6: `/etc/bashrc`.** In `private_dot_bashrc.tmpl`, right after
  the interactive guard: `[ -f /etc/bashrc ] && . /etc/bashrc`, with a
  comment naming AlmaLinux and `/etc/profile.d` in non-login shells.
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
- Modify: `home/.chezmoiscripts/linux/run_once_before_00-packages.sh.tmpl` (lines 3-5, 9-26, 30, 235-256)
- Modify: `home/.chezmoiscripts/darwin/run_once_before_00-packages.sh.tmpl` (lines 15, 50-57, 120-133)
- Modify: `internal/machine/effects.go` (`effectSources` lines 69-72, `workToolsEffect`, delete `bitwardenSessionEffect`, `provisioningEffects`)
- Modify: `internal/machine/effects_test.go:40`, `home/.chezmoidata/packages.toml:6`, `scripts/render-check.sh:103`

- [ ] **Step 1:** Delete the wrapper, both includes, and "bw login" from the
  `.bashrc` comment.
- [ ] **Step 2:** Delete the Bitwarden part, its probe lines and the
  `work-tools` names from both package scripts. On Linux,
  `user_bin_refused` lists only `linux-packages`.
- [ ] **Step 3:** In `effects.go`:
  - Delete `bitwardenSessionEffect` and its `effectSources` entry.
  - `work-tools` sources become `{"50-apps-and-extensions", "35-vscode-extensions"}`.
  - Text: Description `Work editor extension`. Summary
    `the work editor extension`. What
    `Installs the work VS Code extension.` Touches
    `your VS Code extensions`. Undo
    `Not reverted automatically: uninstall the extension in VS Code. A Bitwarden CLI installed earlier stays; run bw lock (or delete ~/.config/Bitwarden CLI/session) and remove bw by hand`.
  - `provisioningEffects` appends only `workToolsEffect`.
  - Drop the comment above `workToolsEffect` that names Bitwarden.
- [ ] **Step 4:** Remove Bitwarden from the comment in `effects_test.go:40`,
  the comment in `packages.toml`, and the word `bitwarden` from the leak
  pattern in `render-check.sh:103`.
- [ ] **Step 5:** Run the gates. Then check that
  `rg -n -i "bitwarden|bw-wrapper" home internal scripts` prints nothing.
- [ ] **Step 6: Commit.** The subject carries the owner notice (spec
  §4.12).

```bash
git commit -am "feat!: remove Bitwarden; run bw lock first to clear its cached session"
```

### Task 5: Retired token keys and migration 0005

**Files:**
- Modify: `internal/machine/answers.go` (`answerKeys`, `invalidKey`, `secrets`, `Askable`, `AdoptionPlan`)
- Modify: `internal/machine/init.go` (`saveAnswers`)
- Create: `internal/migrate/0005_work_tokens.go`
- Modify: `internal/migrate/migrate.go` (package comment, `Registry`, `Plan` recovery limits)
- Modify: `internal/machine/effects.go` (`migrations` effect text)
- Modify: `home/.chezmoi.toml.tmpl:40-43`, `scripts/scratch-init.sh:41-42`
- Modify: `home/.chezmoitemplates/codex-config.toml.tmpl:107-111,123-126` (remove the literals)
- Test: `internal/machine/answers_test.go`, `internal/migrate/0005_work_tokens_test.go`

**Interfaces:**
- Produces:
  - `var retiredAnswers = []string{"jira_api_token", "gitlab_token"}`.
  - `func carryRetired(answers, saved Answers)`, which copies each retired
    key from `saved` into `answers` when `answers` lacks it.
  - `var workTokens = Migration{ID: "0005-work-tokens", ...}`.
  - `func workEnvBody(values map[string]string) []byte` in `0005_work_tokens.go`:
    the nine names in order, one `NAME=value` per line.
  - `var WorkEnvNames` (the nine names) exported from `internal/migrate`,
    so `internal/machine` (Task 6) uses the same list.

- [ ] **Step 1: Write the safeguard test
  `TestRetiredTokensSurviveEveryAnswerWrite`** in
  `internal/machine/answers_test.go`.
  - Comment above it: the risk is unrecoverable loss of the owner's saved
    work tokens before migration 0005 moves them.
  - Setup: a `machine.toml` holding `jira_api_token = "synthetic-jira"`
    and `gitlab_token = "synthetic-gitlab"`.
  - Case one: `saveAnswers` with answers lacking both keys.
  - Case two: `AdoptionPlan` with a FILE lacking both keys.
  - Assert, for each case: the written file still holds both values, and
    `validateAnswers` accepts it.
- [ ] **Step 2: Run it.** `go test ./internal/machine -run TestRetiredTokensSurviveEveryAnswerWrite`.
  Expected: FAIL (values dropped).
- [ ] **Step 3: Implement the retired keys.**
  - `answerKeys` keeps both keys.
  - `invalidKey` no longer requires them.
  - `secrets()` iterates `retiredAnswers`.
  - `Askable` skips them.
  - `saveAnswers` and `AdoptionPlan` call `carryRetired` with the saved
    answers before encoding.
- [ ] **Step 4: Run it again.** Expected: PASS.
- [ ] **Step 5: Drop the token prompts.** Remove them from
  `.chezmoi.toml.tmpl` and `scratch-init.sh`. Remove the
  `JIRA_BASE_URL`/`JIRA_EMAIL`/`JIRA_API_TOKEN`/`GITLAB_URL`/`GITLAB_TOKEN`
  lines from the Codex template (the gate and env work in Tasks 7–8
  replaces them).
- [ ] **Step 6: Write the safeguard test `TestWorkTokensNeverLost`** in
  `0005_work_tokens_test.go`.
  - Comment: the risk is credential loss, and exposure of a token in
    output.
  - Run it in a temp home with synthetic values, covering four cases:
    - **(a) Role `both`, no `work.env`.** After `Run`, `work.env` is 0600
      and holds all nine names, with the tokens, `JIRA_EMAIL` =
      `work_email`, and both addresses. `machine.toml` has neither key.
    - **(b) A crash after the first write** (`work.env` written,
      `machine.toml` unchanged). `Run` only removes the keys.
    - **(c) `work.env` already holds something else.** `Check` returns
      `Conflict`, and both files are byte-for-byte unchanged after `Run`.
    - **(d) Role `personal`.** Nothing changes.
  - For every case: no synthetic value appears in any `say` line or in
    `Check`'s text.
- [ ] **Step 7: Run it.** Expected: FAIL (`workTokens` undefined).
- [ ] **Step 8: Implement `workTokens`** per spec §4.11.
  - It reads `<Runtime.Config>/machine.toml` with go-toml/v2 (it may not
    import `internal/machine`).
  - Statuses:
    - `Pending` when a retired key is present, the role has work, and
      `work.env` is missing.
    - `Unfinished` when `work.env` holds the same two token values.
    - `Conflict` when `work.env` holds anything else.
    - `Done` when no retired key remains.
    - On a role without work, `Done`, with text saying the keys are there
      and should be removed.
  - Writes go to a new file then a rename: `work.env` first, then
    `machine.toml`, at mode 0600.
  - `JIRA_BASE_URL` and `GITLAB_URL` take the two literals removed from
    the Codex template in step 5.
  - Add it to `Registry`.
- [ ] **Step 9: Run it again.** Expected: PASS.
- [ ] **Step 10: Text.** Update the `internal/migrate` package comment, the
  `Plan` recovery limits and the `migrations` effect's Description, What
  and Undo to say 0005 moves the two saved tokens into a new `work.env`,
  and never merges.
- [ ] **Step 11: Run the gates.**
- [ ] **Step 12: Commit**

```bash
git commit -am "feat(migrate): move the saved work tokens into work.env"
```

### Task 6: Env files, the shell hook and agent guidance

**Files:**
- Create: `internal/machine/envfiles.go`
- Create: `internal/cli/envfiles.go` (hidden `env-files` command, registered in `root.go`)
- Create: `home/.chezmoiscripts/shared/run_after_06-env-files.sh.tmpl`
- Modify: `internal/machine/effects.go` (`envFilesEffect` in `sharedEffects`, `effectSources`)
- Modify: `home/private_dot_bashrc.tmpl`, `home/dot_zshrc.tmpl`
- Modify: `home/private_dot_claude/modify_private_settings.json`, `home/private_dot_claude/CLAUDE.md.tmpl`, `home/private_dot_codex/AGENTS.md.tmpl`

**Interfaces:**
- Consumes: `migrate.WorkEnvNames`; the `workTokens` status (Task 5); the
  folder answers and `workbench_config` (Task 1).
- Produces:
  - `func ParseEnvFile(data []byte) (pairs [][2]string, bad []int)`: the
    `NAME=value` pairs in order, and the 1-based numbers of the lines that
    are not a comment, blank or `NAME=value` (with NAME matching
    `^[A-Za-z_][A-Za-z0-9_]*$`).
  - `func SafeEnvFile(path string) ([]byte, error)`: reads a regular file
    owned by the user with no group or other bits, and fails otherwise.
  - `func EnvFilesProbe(c operation.Context, a Answers) []string` and
    `func EnvFilesRun(c operation.Context, a Answers, say func(string)) error`.
  - Effect `env-files`, switch `WORKBENCH_EFFECT_ENV_FILES`.
  - Shell function `__workbench_folder_env`.

- [ ] **Step 1: `envfiles.go`.** Probe lines per spec §4.11. Every line but
  `create` starts with `=`.
  - `env-files: create work.env` (or `personal.env`).
  - `env-files: = work.env is there; empty: …`.
  - `env-files: = waiting for migration 0005 to create work.env`.
  - `env-files: = work.env holds work credentials on a machine without the work role; delete it`.
  - `env-files: = work.env line 4 is not NAME=value and is skipped`.

  Run creates each missing file for the role, at 0600 through a new file
  then a rename. It skips `work.env` while `migrate.Check` reports
  `0005-work-tokens` as anything but `Done`, and never writes an existing
  file.
- [ ] **Step 2: CLI and script.**
  - `workbench env-files --probe` and `workbench env-files run` are hidden,
    and follow `migrateRunCommand`: refused unless
    `WORKBENCH_EFFECT_ENV_FILES=1` and not an agent session.
  - The script copies `run_after_05-migrations.sh.tmpl`'s structure (with
    an installed-binary check that it knows `env-files`).
  - Add `envFilesEffect` to `sharedEffects`: Title `Credential files`,
    Undo `Delete the file by hand; it is not checkpointed`. Add
    `"env-files": {"06-env-files"}` to `effectSources`.
- [ ] **Step 3: Check the Review Focus 4 case.** Smoke it in a disposable
  home. A `work.env` with a line `export JIRA_EMAIL=x` shows the "line N is
  not NAME=value" line in `apply --dry-run`, and the hook and the gate
  skip that line.
- [ ] **Step 4: Shell hook in bash.** In `private_dot_bashrc.tmpl`, render
  the expanded folder answers and `workbench_config` (default
  `~/.config/workbench`) into `__workbench_folder_env`.
  - **What it does.** It resolves `$PWD` with `pwd -P`, then unsets every
    name listed in the env file of each role folder the shell is not in,
    then exports the pairs of the file it is inside, when that file has no
    group or other bits. It returns at once unless `$-` contains `i`.
  - **Where it runs.** Wrap `cd`, `pushd` and `popd`, and append
    `__workbench_folder_env` to `PROMPT_COMMAND` after the oh-my-posh line
    (`PROMPT_COMMAND="${PROMPT_COMMAND:+$PROMPT_COMMAND;}__workbench_folder_env"`).
  - **Wrappers.** `claude()` runs the hook before `command claude`. Add
    `codex() { __workbench_folder_env; command codex "$@"; }` and
    `code()`, which runs `env -u NAME… code "$@"` for every name in both
    files.
- [ ] **Step 5: Shell hook in zsh.** The same function in `dot_zshrc.tmpl`,
  registered with `autoload -Uz add-zsh-hook; add-zsh-hook chpwd …;
  add-zsh-hook precmd …`, with the same interactive guard (`[[ -o
  interactive ]]`) and the same three wrappers.
- [ ] **Step 6: Claude Code deny rules.** In `modify_private_settings.json`,
  append `printf "Read(/%s/work.env)" .workbench_config` when `.has_work`
  and the same for `personal.env` when `.has_personal` to
  `$enforced.permissions.deny`. `workbench_config` is absolute and starts
  with `/`, so each rule starts with `Read(//`, the absolute form.
- [ ] **Step 7: Agent text.** In `CLAUDE.md.tmpl` and Codex's `AGENTS.md.tmpl`,
  for roles with an env file, add: "Credentials are in environment
  variables loaded from Workbench's env files. Use them by name; never
  open, print or echo the files or their values."
- [ ] **Step 8: Run the gates.** Then smoke in a disposable home with
  synthetic values on `both`, with the work folder as a symlink. The
  shell-hook checks in spec §4.10 all pass in bash, including
  `cd <personal>/x && env` from the work folder.
- [ ] **Step 9: Commit**

```bash
git commit -am "feat: per-folder credential files with a shell hook and deny rules"
```

### Task 7: The `mcp-gate` command

**Files:**
- Create: `internal/cli/mcpgate.go` (registered in `root.go`)
- Test: `internal/cli/mcpgate_test.go`

**Interfaces:**
- Consumes: `machine.ParseEnvFile` and `machine.SafeEnvFile` (Task 6).
- Produces:
  - `workbench mcp-gate --folder DIR --env-file FILE -- COMMAND ARGS...`,
    hidden.
  - `func gateInside(folder, projectDir, cwd string) bool`: it uses
    `projectDir` when it is not empty, otherwise `cwd`. It resolves
    symlinks in both paths (`filepath.EvalSymlinks`; a path that cannot be
    resolved counts as outside), then returns true when the path equals the
    folder or starts with the folder plus `/`.

- [ ] **Step 1: Write the safeguard test
  `TestGateNeverCountsOutsideFolderAsInside`.**
  - Comment: the risk is work credentials and work tools reaching a
    personal session.
  - Run it in a temp folder with `repos`, `repos-old`, `repos/r3`, `dev`,
    a symlink `dev/link -> ../repos/r3`, and a symlink `alias -> repos`.
  - Assert true for: `repos`, `repos/r3`, `dev/link` (it resolves into
    `repos`), folder `alias` with cwd `repos/r3`, and projectDir
    `repos/r3` with cwd `dev`.
  - Assert false for: `repos-old`, `dev`, a missing path, and projectDir
    `dev` with cwd `repos/r3`.
- [ ] **Step 2: Run it.** `go test ./internal/cli -run TestGateNeverCountsOutsideFolderAsInside`.
  Expected: FAIL (undefined).
- [ ] **Step 3: Implement `gateInside`.** Then run the test: PASS.
- [ ] **Step 4: Implement the command.** It calls
  `gateInside(--folder, os.Getenv("CLAUDE_PROJECT_DIR"), os.Getwd())`.
  - **Inside.** `SafeEnvFile`, then `ParseEnvFile`. Set each pair with a
    non-empty value, overriding the inherited value, then `syscall.Exec`
    COMMAND with the result. When the file is missing or unsafe, exit 1
    with `mcp-gate: <file> is missing or readable by others` on stderr.
  - **Outside.** A newline-delimited JSON-RPC loop on stdin and stdout:
    - `initialize` → `{"protocolVersion": <the client's>, "capabilities": {}, "serverInfo": {"name": "workbench-mcp-gate"}}`;
    - `ping` → `{}`;
    - `tools/list`, `prompts/list`, `resources/list` and
      `resources/templates/list` → an empty list under `tools`, `prompts`,
      `resources` and `resourceTemplates`;
    - notifications → no reply;
    - any other request → error `-32601`.

    Exit 0 at end of input. It reads no file.
- [ ] **Step 5: Run the gates.**
- [ ] **Step 6: Commit**

```bash
git commit -am "feat(cli): mcp-gate starts a server only inside the work folder"
```

### Task 8: Codex dispatch entries through the gate

**Files:**
- Modify: `home/.chezmoitemplates/codex-config.toml.tmpl:99-128`
- Modify: `home/private_dot_codex/modify_private_config.toml.tmpl` (new `RETIRED_KEYS`)

**Interfaces:**
- Consumes: `mcp-gate` (Task 7), `workbench_config` and the folder answers
  (Task 1), and the `workbench-command` template.

- [ ] **Step 1: The two entries.** For `dispatch-atlassian` and
  `dispatch-gitlab`:
  - `enabled = true`.
  - `command`: `includeTemplate "workbench-command" (dict "root" . "tilde" false)`
    without the shell quotes (TOML holds it as a string).
  - `args`: `["mcp-gate", "--folder", <abs work folder>, "--env-file", <workbench_config>/work.env, "--", <home>/.nvm/nvm-exec, "node", <abs work folder>/<checkout>/<server>/dist/index.js]`.
    The checkout name stays as the template has it today.
  - The `env` table holds only `NODE_VERSION = "default"`.
  - Update the comment above each entry: the gate starts it only inside the
    work folder.
- [ ] **Step 2: `RETIRED_KEYS`.** In the modify script, add
  `RETIRED_KEYS = [("dispatch-atlassian", ("JIRA_BASE_URL", "JIRA_EMAIL", "JIRA_API_TOKEN")), ("dispatch-gitlab", ("GITLAB_URL", "GITLAB_TOKEN"))]`.
  - After `INACTIVE_TABLES`, pop those keys from
    `document["mcp_servers"][name]["env"]` when present.
  - Mention `RETIRED_KEYS` in the docstring beside `INACTIVE_TABLES`.
- [ ] **Step 3: Run the gates.** Then check by hand: pipe a synthetic
  existing config with literal tokens through the rendered modify script.
  The output has no `TOKEN =` line under the dispatch `env` tables, and
  `NODE_VERSION` stays.
- [ ] **Step 4: Commit**

```bash
git commit -am "feat(codex): start the dispatch servers through mcp-gate"
```

### Task 9: The `claude-mcp` step

**Files:**
- Create: `internal/machine/claudemcp.go`
- Create: `internal/cli/claudemcp.go` (hidden `claude-mcp`, registered in `root.go`)
- Create: `home/.chezmoiscripts/shared/run_after_46-claude-mcp.sh.tmpl`
- Modify: `internal/machine/effects.go` (`claudeMCPEffect` in `sharedEffects`, `effectSources`)

**Interfaces:**
- Consumes: `findAgent` (`internal/machine/agents.go:345`) for `claude`;
  `Answers.folder` and `workbench_config` (Task 1).
- Produces:
  - `type dispatchServer struct { Name, Command string; Args []string; Env map[string]string }`.
  - `func dispatchServers(a Answers, bin, config, home string) []dispatchServer`:
    the same command, arguments and `NODE_VERSION` as Task 8's template.
  - `func ClaudeMCPProbe(c operation.Context, a Answers) []string` and
    `func ClaudeMCPRun(ctx context.Context, c operation.Context, a Answers, say func(string)) error`.
  - Effect `claude-mcp`, switch `WORKBENCH_EFFECT_CLAUDE_MCP`.
  - Hidden flag `--print-json` on `workbench claude-mcp --probe`: it prints
    `dispatchServers` as JSON, used only by step 5's check.

- [ ] **Step 1: Probe.** It reads `~/.claude.json` as JSON, using only
  `mcpServers` (user scope) and `projects[*].mcpServers` (local scope). It
  compares names, `command` and `args` only, and never prints `env`.
  - **Lines:**
    - `claude-mcp: register dispatch-atlassian, dispatch-gitlab`.
    - `claude-mcp: = registered`.
    - `claude-mcp: remove dispatch-atlassian, dispatch-gitlab` (no work
      role).
    - `claude-mcp: = nothing to do`.
  - **Warning.** A same-named local-scope entry adds `= a local entry for
    dispatch-gitlab in <project> outranks this one; claude mcp remove
    --scope local dispatch-gitlab`.
  - **No saved tokens.** When 0005 has no saved tokens to move and an entry
    with an `env` holding `TOKEN` names is being replaced, add `= the old
    entry's credentials are not carried into work.env`.
- [ ] **Step 2: Run.** For each server it runs
  `claude mcp remove --scope user NAME` (exit status ignored when the name
  is absent), then `claude mcp add-json --scope user NAME JSON`, through
  `operation.Run` with the resolved `claude`. JSON is
  `{"type":"stdio","command":…,"args":…,"env":{"NODE_VERSION":"default"}}`.
  On a machine without the work role it runs only the remove. When
  `claude` is not found, it reports blocked with
  `Claude Code is not installed`.
- [ ] **Step 3: CLI, script and effect.**
  - The CLI follows `env-files` from Task 6 step 2.
  - Add `claudeMCPEffect` to `sharedEffects`: Title
    `Claude Code work servers`, Undo
    `claude mcp remove --scope user dispatch-atlassian dispatch-gitlab`.
    Add `"claude-mcp": {"46-claude-mcp"}` to `effectSources`.
  - The server names appear only in Go, never in the script.
- [ ] **Step 4: Run the gates**, including the personal render-check leak
  check.
- [ ] **Step 5: Check the two definitions match.** In a disposable home on
  `work`, the dry run's Codex config diff and `workbench claude-mcp
  --probe --print-json` show the same command, arguments and
  `NODE_VERSION`.
- [ ] **Step 6: Commit**

```bash
git commit -am "feat: register the Claude Code dispatch servers through mcp-gate"
```

### Task 10: The Windows writer record

**Files:**
- Create: `internal/platform/wsl_linux.go`
- Create: `home/.chezmoitemplates/windows-writers.sh`
- Modify: `home/.chezmoiscripts/wsl/run_before_00-packages-windows.sh.tmpl`, `home/.chezmoiscripts/wsl/run_after_10-deploy-windows-configs.sh.tmpl`
- Modify: `internal/machine/plan.go` (plan input), `internal/machine/selection.go` or the `--choose` path (take-over), `internal/machine/effects.go` (Undo text of the four steps)

**Interfaces:**
- Produces:
  - `platform.WindowsLocalAppData(ctx context.Context) (string, error)`:
    runs `cmd.exe /c echo %LOCALAPPDATA%` and `wslpath -u`, capped at 60
    seconds.
  - Record path `<LocalAppData>/Workbench/writers.json`, holding
    `{"terminal-adoption": "<distro>", ...}` with keys from
    `wsl-preferences`, `terminal-adoption`, `powershell-adoption` and
    `default-distro`.
  - Plan input `operation.Input{Name: "windows-writers", Digest: <sha256 of the file, or "absent">}`
    on WSL.
  - Script variable `WORKBENCH_WINDOWS_TAKE_OVER` (space-separated step
    names).
  - Shell functions `writer_of STEP` (prints the recorded distribution, or
    nothing) and `writer_record STEP` (writes a new file, then renames it).

- [ ] **Step 1: Platform call.** Add `WindowsLocalAppData` with a comment in
  the package's style.
- [ ] **Step 2: Shell template.** Add `windows-writers.sh`. Both WSL scripts
  include it, and both ask `wsl.exe --list --quiet` (the first one now
  too).
- [ ] **Step 3: Apply the four rules** (spec §4.7) in each of the four
  steps' probe and run sections:
  - **Probe.** When another installed distribution is recorded, print
    `STEP: = written by <distro>`.
  - **Run.** When another installed distribution is recorded, skip the
    step unless STEP is in `WORKBENCH_WINDOWS_TAKE_OVER`. After a write,
    call `writer_record STEP`.
- [ ] **Step 4: Plan input.** On WSL, `plan.go` adds the `windows-writers`
  input, so `--approve-plan` exits 4 when the writer changed.
- [ ] **Step 5: Take-over.** Under `apply --choose`, a step whose probe
  says `= written by X` is offered as
  `STEP: take over from X and replace <file>`. Ticking it adds STEP to
  `WORKBENCH_WINDOWS_TAKE_OVER` for that apply only. It is never saved
  and never set without `--choose`, so agents (refused `--choose`) cannot
  move it.
- [ ] **Step 6: Undo text.** The four steps' Undo text names the record
  and says deleting it is safe.
- [ ] **Step 7: Run the gates**, including the `wsl` render-checks. Then
  smoke the probe: place a record naming another distribution in a
  disposable LocalAppData folder, and check that the plan line reads
  `= written by …`.
- [ ] **Step 8: Commit**

```bash
git commit -am "feat(wsl): record which distribution writes each shared Windows file"
```

### Task 11: Decision record and docs

**Files:**
- Create: `docs/adr/0015-almalinux-and-role-folders.md`
- Modify: `AGENTS.md`, `README.md`, `docs/linux.md`, `docs/wsl.md`, `docs/usage.md`, `docs/superpowers/specs/workbench-contracts.md`

- [ ] **Step 1: ADR 0015.** It holds the seven reasons listed in spec
  §4.9, and names the Ubuntu-only decision in `docs/linux.md` as
  replaced. Follow ADR 0014's format.
- [ ] **Step 2: AGENTS.md**, per spec §4.9:
  - The migration exception for 0005.
  - Ownership rows for the env files, `env-files`, `claude-mcp` and
    `mcp-gate`.
  - `epel` named as on by default in the optional-steps paragraph.
- [ ] **Step 3: Contracts doc.** Lines 39, 80, 135 and 142–143, the
  targets table, and the prerequisites paragraph.
- [ ] **Step 4: `docs/usage.md`.**
  - Lines 139, 366, 368 and 468.
  - The `epel`, `env-files` and `claude-mcp` steps, the folder questions,
    and the take-over line.
  - The Bitwarden notice.
- [ ] **Step 5: The other docs.**
  - `docs/linux.md`: targets, dnf, EPEL, the bash note and the Bitwarden
    notice.
  - `docs/wsl.md`: the §4.7 rules.
  - `README.md`: the targets line.
- [ ] **Step 6:** Run the gates, then check that
  `rg -n "~/repos|~/dev" docs/usage.md docs/linux.md README.md` shows each
  remaining hit as a default, not a fixed rule.
- [ ] **Step 7: Commit**

```bash
git commit -am "docs: AlmaLinux 10, role folders, env files and the MCP gate"
```

### Task 12: Disposable checks and acceptance entry

**Files:**
- Modify: `docs/acceptance.md` (a new entry only)

- [ ] **Step 1: Build a local bundle** per `AGENTS.md`
  (`scripts/package-release.py`, `-dev.N` name).
- [ ] **Step 2: AlmaLinux container.** In a disposable `almalinux:10`
  container, with a new non-root user and sudo, run:
  1. `init --answers-from FILE` (role `work`, folder answers);
  2. `apply --local-build --dry-run --json`;
  3. `apply --local-build --approve-plan DIGEST`.

  Expected: EPEL, the base packages, bat, fd, tmux, podman and the font
  (`fc-cache`) are installed, and every chosen step is complete.
- [ ] **Step 3: Ubuntu container.** The same in `ubuntu:24.04` with role
  `personal`. Expected: the apt path is unchanged, and there is no
  `work.env`, `claude-mcp` registration or dispatch entry.
- [ ] **Step 4: Both agents.** In a disposable home on role `both`, with
  non-default folders, synthetic credentials and stub stdio servers in
  place of the dispatch checkouts, run every bullet of spec §4.10's
  disposable-home list for Claude Code and Codex. Record each pass or
  fail.
- [ ] **Step 5: WSL path.** A second AlmaLinux 10 WSL distribution under
  another name with a disposable home, if available. Otherwise record that
  it was not run.
- [ ] **Step 6: Acceptance entry.** Add a new `docs/acceptance.md` entry
  listing exactly what ran and what did not (arm64 and the real Windows
  writer record, unless run).
- [ ] **Step 7: Commit**

```bash
git commit -am "docs(acceptance): record the AlmaLinux 10 and role-folder checks"
```
