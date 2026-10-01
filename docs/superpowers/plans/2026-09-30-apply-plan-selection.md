# Apply Plan Selection Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** `update` only installs, `apply` shows a branded checklist of concrete per-effect deltas, remembers which effects a machine skips, and approves exactly what was shown; `claude-costs` prints one readable line per repository.

**Architecture:** `operation.Effect` gains probed `Delta`, `Checked`, `Fixed` and `SavedSkip` fields so the plan digest covers the selection. The machine planner runs every active provisioning script in a read-only `WORKBENCH_PROBE=1` mode to fill the deltas, and apply removes the script sources of unchecked effects from its private chezmoi copy so chezmoi never runs or records them. The CLI replaces the Yes/No prompt with a `huh` multi-select, saves skips to `machine.toml`'s `[effects]` table, and `update` stops after installing the release and its tools.

**Tech Stack:** Go 1.26 (cobra, charm.land/huh v2, go-toml v2), chezmoi 2.70.3 (`dump --include=scripts`), bash script templates, shellcheck, Python 3 for render checks.

**Spec:** `docs/superpowers/specs/2026-09-30-apply-plan-selection-design.md`; Task 8 implements section 7 of `docs/superpowers/specs/2026-09-30-claude-costs-ledger-design.md`

## Global Constraints

- Greenfield: delete `--config-only`, `--install-only`, `--skip`, `--only`, `--machine-config`, `--effect`, `--source` and the update→apply handoff; no aliases, no compatibility readers.
- `apply --help` shows exactly `--dry-run`, `--yes` (`-y`), `--reset`; `--approve-plan` and `--local-build` are hidden; `--destination` stays hidden for isolated-destination checks.
- Every screen `apply`, `update` and `doctor` print starts with `[WorkBench]`, result lines included.
- Probe: `WORKBENCH_PROBE=1` makes a script print one `<effect-name>: <delta>` line per effect it owns and exit 0 without changing anything; the planner bounds each probe at 5 s; a failed, timed-out or malformed probe shows `unprobed` and the effect stays checked.
- Delta grammar: `<item> <from> → <to>` for a change, `<item> ok` or `present` for no change, `install <names>` for new items, `(asks)` for a step that prompts; items joined with `; `.
- Skips are saved only after an approved apply, never by `--dry-run`; only skips and optional selections are stored; new effects default to checked.
- The plan digest covers files, effects and their `Checked` flags; `--approve-plan` with a changed selection exits 3 with `Approval digest does not match the current plan; review a new plan`.
- Tests stay near zero: one Go test for the consent boundary (digest covers selection); everything else is observed smoke checks recorded in the ledger.
- Gates before every commit touching Go: `go build ./...`, `go vet ./...`, `go test ./...`; before every commit touching `home/`: `scripts/render-check.sh` for `personal pinned`, `work latest`, `work pinned wsl` (on a WSL host run them against a scratch copy with the `microsoft` detection string disabled, as the previous plan did).
- Tracked docs and commit messages carry no personal repo names, project paths, account identifiers or absolute checkout paths.
- claude-costs report lines never exceed the terminal width; `--compact`, `w-5m`, `w-1h` and `cache-r` are gone; footer notes appear only when they report something (hidden projects, `--top`, the `--tokens` legend).

## Review Focus

1. A saved `skip` naming an effect this host does not list (saved on macOS, applied on Linux) must be ignored silently and kept, not fail the plan or be dropped. Pinned in Task 3 step 6.
2. A probe that writes anything (temp file, network download) or prompts would corrupt a dry run; every probe must exit before `mktemp`, `sudo`, `read` and installers. Pinned in Task 2 step 3 (grep gate).
3. A `run_once_` script skipped this apply must still run on a later apply once unskipped; chezmoi must not record it as run. Pinned in Task 3 step 9 (scratch-source removal check).
4. `--approve-plan DIGEST` from a dry run with saved skips, then a hand edit of `machine.toml` changing the skips, must exit 3 (conflict), never apply the old selection. Pinned in Task 1 step 5 (Go test) and Task 4 step 8.
5. `update` with no terminal, `--json`, or run from `install.sh` must never plan or apply the machine; its last line names `workbench apply`. Pinned in Task 5 step 6.

---

### Task 1: Effect model, selection state and the consent-boundary test

**Files:**
- Modify: `internal/operation/plan.go:36-64` (Effect, Plan)
- Create: `internal/machine/selection.go`
- Modify: `internal/machine/answers.go:27-50` (readAnswers), `:200-266` (AdoptionPlan)
- Modify: `internal/machine/init.go:138-145` (initialize's save)
- Modify: `internal/operation/state.go:18-24` (State)
- Create: `internal/operation/plan_test.go`

**Interfaces:**
- Produces: `operation.Effect{Delta, Probe string; Fixed, Checked, SavedSkip bool}`, `operation.Plan.UnchangedTargets int`, `operation.State.AppliedAt *time.Time`.
- Produces: `machine.Selection{Skip, Select []string}`, `machine.ReadSelection(config string) (Selection, error)`, `machine.WriteSelection(m *operation.Mutation, config string, selection Selection) error`, `machine.encodeMachineConfig(answers Answers, selection Selection) ([]byte, error)`.

- [ ] **Step 1: Extend Effect, Plan and State**

In `internal/operation/plan.go` replace the `Effect` type with:

```go
// Effect is a planned change outside checkpointed files, with the privilege
// it needs and what recovery can and cannot undo. Checked says whether this
// apply runs it; the plan digest covers that, so consent is for exactly the
// shown selection.
type Effect struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Privilege   string `json:"privilege"`
	Recovery    string `json:"recovery"`
	// Delta is the probed one-line change on this machine, "" when the
	// effect was not probed.
	Delta string `json:"delta,omitempty"`
	// Probe is "ok", "failed" or "timeout" once a probe ran, "" otherwise.
	Probe string `json:"probe,omitempty"`
	// Fixed effects always run with the plan and cannot be unchecked: the
	// file-backed policy marker and checkpoint retention.
	Fixed bool `json:"fixed,omitempty"`
	// Checked effects run; unchecked ones are left out of this apply.
	Checked bool `json:"checked"`
	// SavedSkip marks an unchecked effect whose skip came from machine.toml.
	SavedSkip bool `json:"saved_skip,omitempty"`
}
```

In the `Plan` struct add, after `Effects`:

```go
	// UnchangedTargets counts the managed files this plan leaves as they are.
	UnchangedTargets int `json:"unchanged_targets"`
```

In `internal/operation/state.go` add to `State`, after `AppliedConfiguration`:

```go
	AppliedAt            *time.Time        `json:"applied_at,omitempty"`
```

and `"time"` to its imports.

- [ ] **Step 2: Create `internal/machine/selection.go`**

```go
package machine

import (
	"os"
	"slices"

	"github.com/Sawmonabo/workbench/internal/operation"
	"github.com/pelletier/go-toml/v2"
)

// Selection is which effects an apply runs: every default effect not named in
// Skip, and the optional host effects named in Select. An approved apply saves
// it in machine.toml's [effects] table, so a machine that never wants the
// Windows-side steps declines them once.
type Selection struct {
	Skip   []string `toml:"skip,omitempty"`
	Select []string `toml:"select,omitempty"`
}

func (s Selection) empty() bool { return len(s.Skip) == 0 && len(s.Select) == 0 }

// ReadSelection reads the [effects] table of config. A missing file or table
// is an empty selection: everything checked, nothing optional selected.
func ReadSelection(config string) (Selection, error) {
	if _, err := os.Lstat(config); os.IsNotExist(err) {
		return Selection{}, nil
	}
	raw, err := operation.ReadPrivateInput(config, 1<<20)
	if err != nil {
		return Selection{}, err
	}
	return readSelection(raw)
}

func readSelection(raw []byte) (Selection, error) {
	var config struct {
		Effects Selection `toml:"effects"`
	}
	if err := toml.Unmarshal(raw, &config); err != nil {
		return Selection{}, operation.Fail(
			operation.ExitInvalid,
			"answers",
			"Machine answers are not valid TOML; original input retained",
		)
	}
	slices.Sort(config.Effects.Skip)
	slices.Sort(config.Effects.Select)
	return config.Effects, nil
}

// encodeMachineConfig is the one writer of machine.toml's shape: the [data]
// answers and, when any, the [effects] selection.
func encodeMachineConfig(answers Answers, selection Selection) ([]byte, error) {
	config := map[string]any{"data": answers}
	if !selection.empty() {
		config["effects"] = selection
	}
	return toml.Marshal(config)
}

// WriteSelection saves selection beside the answers already in config.
func WriteSelection(m *operation.Mutation, config string, selection Selection) error {
	raw, err := operation.ReadPrivateInput(config, 1<<20)
	if err != nil {
		return err
	}
	answers, err := readAnswers(raw)
	if err != nil {
		return err
	}
	encoded, err := encodeMachineConfig(answers, selection)
	if err != nil {
		return err
	}
	return m.WritePrivate(config, encoded)
}
```

- [ ] **Step 3: Let `readAnswers` accept the `[effects]` table and preserve it on every save**

In `internal/machine/answers.go` replace the `len(config) != 1 || config["data"] == nil` check in `readAnswers` with:

```go
	if config["data"] == nil {
		return nil, operation.Fail(
			operation.ExitInvalid,
			"answers",
			"Machine config must contain a [data] table; adopt answers without native hooks or commands",
		)
	}
	for key := range config {
		if key != "data" && key != "effects" {
			return nil, operation.Fail(
				operation.ExitInvalid,
				"answers",
				"Machine config may contain only [data] and [effects] tables; adopt answers without native hooks or commands",
			)
		}
	}
```

In `AdoptionPlan`, replace `encoded, err := toml.Marshal(map[string]any{"data": answers})` with:

```go
	target := filepath.Join(c.Paths.Config, "machine.toml")
	existing, err := ReadSelection(target)
	if err != nil {
		return plan, nil, err
	}
	encoded, err := encodeMachineConfig(answers, existing)
```

and delete the later duplicate `target := filepath.Join(c.Paths.Config, "machine.toml")` line.

In `internal/machine/init.go` `initialize`, replace `encoded, err := toml.Marshal(map[string]any{"data": answers})` with:

```go
	previous, err := ReadSelection(filepath.Join(c.Paths.Config, "machine.toml"))
	if err != nil {
		return nil, err
	}
	encoded, err := encodeMachineConfig(answers, previous)
```

(`initSeed` keeps marshalling a data-only seed: it is scratch input for native init.)

- [ ] **Step 4: Build**

Run: `cd "$(git rev-parse --show-toplevel)" && go build ./... && go vet ./...`
Expected: no output (the new fields are unused so far; `encodeMachineConfig` and `WriteSelection` compile).

- [ ] **Step 5: Write the consent-boundary test and watch it fail**

Create `internal/operation/plan_test.go`:

```go
package operation

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// Prevent unapproved script execution: consent is for the selection that was
// shown. A plan approved with an effect checked must be refused once that
// effect's Checked flag differs at recheck, which is what a hand edit of
// machine.toml between dry run and --approve-plan produces.
func TestApprovalCoversEffectSelection(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	c := Context{
		Paths: Paths{
			State:  filepath.Join(root, "state"),
			Config: filepath.Join(root, "config"),
			Data:   filepath.Join(root, "data"),
			Cache:  filepath.Join(root, "cache"),
			Bin:    filepath.Join(root, "bin"),
		},
		Scope: Scope{Kind: "project", Root: filepath.Join(root, "project")},
	}
	if err = os.Mkdir(c.Scope.Root, 0o700); err != nil {
		t.Fatal(err)
	}
	shown := Plan{
		Scope:    c.Scope,
		Complete: true,
		Effects:  []Effect{{Name: "runtimes", Checked: true}},
	}
	recheck := shown
	recheck.Effects = []Effect{{Name: "runtimes", Checked: false}}
	if shown.Digest() == recheck.Digest() {
		t.Fatal("digest ignores Checked")
	}
	applied := false
	err = WithMutation(
		context.Background(),
		c,
		shown,
		Consent{ApprovedDigest: shown.Digest(), CompleteInputs: true},
		func(context.Context, Context) (Plan, error) { return recheck, nil },
		func(*Mutation) error { applied = true; return nil },
	)
	if applied || ExitCode(err) != ExitConflict {
		t.Fatalf("selection change applied=%v err=%v", applied, err)
	}
}
```

Run: `go test ./internal/operation/ -run TestApprovalCoversEffectSelection -v`
Expected: the test compiles and PASSES already if `acquireLocks` creates its directories; if it fails on a missing lock directory, read `acquireLocks` in `internal/operation/state.go` and create what `TestRecoveryPreservesUserDataAndEvidence` creates (it is the reference for a writable test Context). The assertion that matters is `ExitConflict`; a `t.Fatal("digest ignores Checked")` would mean Step 1 did not land.

- [ ] **Step 6: Commit**

```bash
git add internal/operation/plan.go internal/operation/state.go internal/operation/plan_test.go internal/machine/selection.go internal/machine/answers.go internal/machine/init.go
git commit -m "feat(machine): effect selection model saved in machine.toml [effects]"
```

---

### Task 2: Script probe mode

**Files:**
- Modify: every `home/.chezmoiscripts/*/*.sh.tmpl` (15 files)
- Modify: `scripts/render-check.sh:60-72` (lint loop)
- Modify: `docs/superpowers/specs/2026-09-30-apply-plan-selection-design.md` section 4 (probe line grammar)

**Interfaces:**
- Produces: with `WORKBENCH_PROBE=1` in the environment, each script prints one `<effect>: <delta>` line per effect it owns to stdout and exits 0 before any write, prompt or installer. Effect names are those in `internal/machine/effects.go`'s `effectSources`.

- [ ] **Step 1: Amend the spec's probe wording**

In the spec's section 4 "Deltas", replace "prints exactly one line describing what a real run would change" with "prints one `<effect-name>: <delta>` line per effect it owns (most scripts own one; `10-deploy-windows-configs` owns `windows-files` and `wsl-preferences`, and the Windows scripts also describe the optional effects they carry)", and in section 8 replace "print exactly one line" with "print only lines of the form `<effect-name>: <text>`". In section 4's rendering block and rules, replace "q quits" with "esc quits" (the prompt library binds esc and ctrl+c, not q).

- [ ] **Step 2: Add the probe block to each script**

Insert each block at the anchor named. Every block ends with `exit 0` inside the `if`, so the script below it never runs in probe mode.

`home/.chezmoiscripts/linux/run_once_before_00-packages.sh.tmpl`, immediately before the line `download_dir=$(mktemp -d)`:

```bash
if [ "${WORKBENCH_PROBE:-}" = 1 ]; then
    missing=()
    for tool in bat fd oh-my-posh gh tmux; do
        command -v "$tool" >/dev/null 2>&1 || missing+=("$tool")
    done
    compgen -G "$HOME/.local/share/fonts/JetBrainsMono*NerdFont*.ttf" >/dev/null || missing+=("JetBrainsMono Nerd Font")
    if [ ${#missing[@]} -eq 0 ]; then
        echo "linux-packages: nothing to install"
    else
        echo "linux-packages: install ${missing[*]}"
    fi
{{- if .has_work }}
    if command -v bw >/dev/null 2>&1 || [ -x "$HOME/.local/bin/bw" ]; then
        echo "work-tools: Bitwarden CLI present"
    else
        echo "work-tools: install Bitwarden CLI"
    fi
{{- end }}
    exit 0
fi
```

`home/.chezmoiscripts/linux/run_once_after_10-runtime-managers.sh.tmpl`, immediately after the line `echo "==> Runtime managers ({{ .versions_mode }} mode)..."` (and after the `{{ template "operation-context.sh" }}` line that follows it, if present in this file):

```bash
if [ "${WORKBENCH_PROBE:-}" = 1 ]; then
    parts=()
    [ -r "${NVM_DIR:-$HOME/.nvm}/nvm.sh" ] && parts+=("nvm present") || parts+=("install nvm")
    for tool in uv rustup bun; do
        if command -v "$tool" >/dev/null 2>&1 || [ -x "$HOME/.cargo/bin/$tool" ] || [ -x "$HOME/.bun/bin/$tool" ]; then
            parts+=("$tool present")
        else
            parts+=("install $tool")
        fi
    done
{{- if eq .versions_mode "pinned" }}
    go_target="{{ .versions.go }}"
{{- else }}
    go_target=$(curl -fsSL --max-time 3 'https://go.dev/VERSION?m=text' 2>/dev/null | head -n1); go_target=${go_target#go}
{{- end }}
    go_have=$(command -v go >/dev/null 2>&1 && go version 2>/dev/null | awk '{sub(/^go/, "", $3); print $3}')
    if [ -z "$go_target" ]; then
        parts+=("Go ${go_have:-absent} (latest unknown, offline)")
    elif [ "$go_have" = "$go_target" ]; then
        parts+=("Go $go_have ok")
    else
        parts+=("Go ${go_have:-absent} → $go_target")
    fi
    IFS='; '; echo "runtime-managers: ${parts[*]}"; unset IFS
    exit 0
fi
```

`home/.chezmoiscripts/linux/run_onchange_after_20-runtimes.sh.tmpl` and `home/.chezmoiscripts/darwin/run_onchange_after_20-runtimes.sh.tmpl`, immediately after the line `failed=()`:

```bash
if [ "${WORKBENCH_PROBE:-}" = 1 ]; then
    parts=()
    if [ -r "$NVM_DIR/nvm.sh" ]; then
        # shellcheck source=/dev/null
        source "$NVM_DIR/nvm.sh" >/dev/null 2>&1 || true
        node_have=$(nvm version default 2>/dev/null); node_have=${node_have#v}
{{- if eq .versions_mode "pinned" }}
        node_want="{{ .versions.node_default }}"
{{- else }}
        node_want=$(nvm version-remote node 2>/dev/null); node_want=${node_want#v}
{{- end }}
        if [ -z "$node_want" ]; then
            parts+=("Node ${node_have:-absent} (latest unknown, offline)")
        elif [ "$node_have" = "$node_want" ]; then
            parts+=("Node $node_have ok")
        else
            parts+=("Node ${node_have:-absent} → $node_want (default alias)")
        fi
    else
        parts+=("Node needs nvm")
    fi
    if command -v uv >/dev/null 2>&1; then
{{- if eq .versions_mode "pinned" }}
        parts+=("Python {{ join ", " .versions.python_pinned }} via uv")
{{- else }}
        parts+=("Python newest via uv")
{{- end }}
    else
        parts+=("Python needs uv")
    fi
    if command -v rustup >/dev/null 2>&1; then
{{- if eq .versions_mode "pinned" }}
        parts+=("Rust {{ .versions.rust }}")
{{- else }}
        parts+=("Rust stable")
{{- end }}
    else
        parts+=("Rust needs rustup")
    fi
    IFS='; '; echo "runtimes: ${parts[*]}"; unset IFS
    exit 0
fi
```

(`uv python install` and `rustup toolchain install` are idempotent and report nothing cheap to compare, so the probe names the policy; a reasonable person reads "Python newest via uv" as "kept current".)

`home/.chezmoiscripts/linux/run_onchange_after_30-global-tools.sh.tmpl` and `home/.chezmoiscripts/darwin/run_onchange_after_30-global-tools.sh.tmpl`, immediately after the line `failed=()`:

```bash
if [ "${WORKBENCH_PROBE:-}" = 1 ]; then
    missing=()
    for tool in codex claude corepack; do command -v "$tool" >/dev/null 2>&1 || missing+=("$tool"); done
{{- range $tool, $ver := .corepack_globals }}
    command -v "{{ $tool }}" >/dev/null 2>&1 || missing+=("{{ $tool }}")
{{- end }}
{{- range $pkg, $ver := .uv_tools }}
    command -v "{{ $pkg }}" >/dev/null 2>&1 || missing+=("{{ $pkg }}")
{{- end }}
{{- range $pkg, $ver := .cargo_tools }}
    [ -x "$HOME/.cargo/bin/{{ $pkg }}" ] || command -v "{{ $pkg }}" >/dev/null 2>&1 || missing+=("{{ $pkg }}")
{{- end }}
{{- range $mod, $ver := .go_tools }}
    [ -x "$HOME/go/bin/$(basename "{{ $mod }}")" ] || command -v "$(basename "{{ $mod }}")" >/dev/null 2>&1 || missing+=("$(basename "{{ $mod }}")")
{{- end }}
    present="npm, uv, cargo and Go tools"
{{- if eq .versions_mode "pinned" }}
    policy="pinned versions"
{{- else }}
    policy="upgraded to latest"
{{- end }}
    if [ ${#missing[@]} -eq 0 ]; then
        echo "global-tools: $present present ($policy)"
    else
        echo "global-tools: install ${missing[*]}; others $policy"
    fi
    exit 0
fi
```

`home/.chezmoiscripts/linux/run_after_35-vscode-extensions.sh.tmpl`, immediately after the line `status=0`:

```bash
if [ "${WORKBENCH_PROBE:-}" = 1 ]; then
    case "$(command -v code 2>/dev/null)" in
        /usr/share/code/bin/code|/snap/code/current/usr/share/code/bin/code) echo "linux-editor-extensions: install missing extensions into the local VS Code" ;;
        "") echo "linux-editor-extensions: blocked, no local VS Code desktop" ;;
        *) echo "linux-editor-extensions: blocked, code on PATH is not a local desktop" ;;
    esac
    exit 0
fi
```

`home/.chezmoiscripts/shared/run_onchange_after_40-tmux-plugins.sh.tmpl`, immediately before the line `if [ ! -d "$HOME/.tmux/plugins/tpm" ]; then`:

```bash
if [ "${WORKBENCH_PROBE:-}" = 1 ]; then
    if [ -d "$HOME/.tmux/plugins/tpm" ]; then
        echo "tmux-plugins: TPM present; plugins refreshed from tmux.conf"
    else
        echo "tmux-plugins: install TPM and plugins"
    fi
    exit 0
fi
```

`home/.chezmoiscripts/darwin/run_once_before_00-packages.sh.tmpl`, immediately after the line `echo "==> macOS bootstrap starting..."`:

```bash
if [ "${WORKBENCH_PROBE:-}" = 1 ]; then
    if [ ! -x /opt/homebrew/bin/brew ] && [ ! -x /usr/local/bin/brew ]; then
        echo "macos-packages: install Homebrew and every packages.toml formula"
    else
        echo "macos-packages: install missing packages.toml formulae (no upgrades)"
    fi
    exit 0
fi
```

`home/.chezmoiscripts/darwin/run_once_after_10-runtime-managers.sh.tmpl`, immediately after the line `{{ template "operation-context.sh" }}`:

```bash
if [ "${WORKBENCH_PROBE:-}" = 1 ]; then
    parts=()
    [ -r "${NVM_DIR:-$HOME/.nvm}/nvm.sh" ] && parts+=("nvm present") || parts+=("install nvm")
    command -v rustup >/dev/null 2>&1 || [ -x "$HOME/.cargo/bin/rustup" ] && parts+=("rustup present") || parts+=("install rustup")
    for tool in uv bun go; do
        command -v "$tool" >/dev/null 2>&1 && parts+=("$tool present") || parts+=("$tool missing, from Homebrew")
    done
    IFS='; '; echo "runtime-managers: ${parts[*]}"; unset IFS
    exit 0
fi
```

`home/.chezmoiscripts/darwin/run_after_50-apps-and-extensions.sh.tmpl`, immediately after the line `echo "==> Apps and VS Code extensions..."`:

```bash
if [ "${WORKBENCH_PROBE:-}" = 1 ]; then
    installed="$(brew list --cask 2>/dev/null || true)"
    missing=()
{{- range .packages.darwin.cask }}
    grep -qx "{{ . }}" <<< "$installed" || missing+=("{{ . }}")
{{- end }}
    if [ ${#missing[@]} -eq 0 ]; then
        echo "macos-apps-extensions: apps present; missing VS Code extensions installed"
    else
        echo "macos-apps-extensions: install ${missing[*]} unless already outside Homebrew; missing VS Code extensions installed"
    fi
    exit 0
fi
```

`home/.chezmoiscripts/darwin/run_after_60-cleanup.sh.tmpl`, immediately after the line `echo "==> Homebrew cleanup..."`:

```bash
if [ "${WORKBENCH_PROBE:-}" = 1 ]; then
    echo "brew-maintenance: autoremove, cleanup old versions and cache"
    exit 0
fi
```

Both WSL scripts call `cmd.exe` before anything else, which fails under `set -euo pipefail` on a host without Windows interop (CI's `wsl` render). So each of them first gets, immediately after its `set -euo pipefail` line:

```bash
if [ "${WORKBENCH_PROBE:-}" = 1 ] && ! command -v cmd.exe >/dev/null 2>&1; then
    echo "windows-files: blocked, no Windows interop on this host"
    exit 0
fi
```

(in `10-deploy-windows-configs` the block also prints `echo "wsl-preferences: blocked, no Windows interop on this host"` before `exit 0`.)

`home/.chezmoiscripts/wsl/run_before_00-packages-windows.sh.tmpl`, immediately before the line `download_dir=$(mktemp -d)` (after `manage_profile` is set):

```bash
if [ "${WORKBENCH_PROBE:-}" = 1 ]; then
    parts=()
    [ -f "$WIN_APPDATA/Programs/oh-my-posh/bin/oh-my-posh.exe" ] && parts+=("oh-my-posh.exe present") || parts+=("install oh-my-posh.exe")
    ls "$WIN_APPDATA/Microsoft/Windows/Fonts"/JetBrainsMono*.ttf >/dev/null 2>&1 && parts+=("fonts present") || parts+=("install JetBrainsMono fonts")
    IFS='; '; echo "windows-files: ${parts[*]}"; unset IFS
    if [ -e "$WT_SETTINGS_DIR/settings.json" ]; then
        echo "terminal-adoption: replace existing Windows Terminal settings.json (dated copy kept)"
    else
        echo "terminal-adoption: no existing settings.json; windows-files writes it"
    fi
    if [ -e "$PS_PROFILE" ]; then
        echo "powershell-adoption: replace existing PowerShell profile (copy kept)"
    else
        echo "powershell-adoption: no existing profile; windows-files writes it"
    fi
    echo "font-registry: register JetBrainsMono fonts in HKCU and load them"
    exit 0
fi
```

Also move the `exit 3` blocks above it so a probe on a non-WSL host still answers: replace the two early `exit 3` guards (`Windows x64 required` and `Windows home, AppData or WSL_DISTRO_NAME unavailable`) with:

```bash
    *) if [ "${WORKBENCH_PROBE:-}" = 1 ]; then echo "windows-files: blocked, Windows x64 required"; exit 0; fi
       echo 'WORKBENCH_EFFECT windows-files blocked: Windows x64 required' >&2; exit 3 ;;
```

and

```bash
if [[ ! -d "$WIN_HOME" || ! -d "$WIN_APPDATA" || -z "${WSL_DISTRO_NAME:-}" ]]; then
    if [ "${WORKBENCH_PROBE:-}" = 1 ]; then echo "windows-files: blocked, Windows home, AppData or WSL_DISTRO_NAME unavailable"; exit 0; fi
    echo 'WORKBENCH_EFFECT windows-files blocked: Windows home, AppData or WSL_DISTRO_NAME unavailable' >&2
    exit 3
fi
```

`home/.chezmoiscripts/wsl/run_after_10-deploy-windows-configs.sh.tmpl`, immediately after the `merge_ini_key()` function's closing `}` and before the `# ---------- .wslconfig ----------` comment:

```bash
if [ "${WORKBENCH_PROBE:-}" = 1 ]; then
    WSLCONFIG="$WIN_HOME/.wslconfig"
    changes=()
    if [[ ! -f "$WSLCONFIG" ]]; then
        changes+=(".wslconfig create")
    else
        probe_tmp=$(mktemp)
        sed 's/\r$//' "$WSLCONFIG" > "$probe_tmp"
        for entry in "wsl2|memory|$WSL_MEMORY" "wsl2|swap|$WSL_SWAP" "wsl2|processors|$WSL_PROCESSORS" \
            "wsl2|networkingMode|mirrored" "wsl2|vmIdleTimeout|300000" \
            "experimental|autoMemoryReclaim|gradual" "experimental|sparseVhd|true"; do
            IFS='|' read -r section key value <<< "$entry"
            current=$(awk -v section="[$section]" -v key="$(tr '[:upper:]' '[:lower:]' <<< "$key")" '
                /^\[/ { insec = (tolower($0) == tolower(section)) }
                insec && tolower($0) ~ "^" key "[[:space:]]*=" { sub(/^[^=]*=[[:space:]]*/, ""); print; exit }
            ' "$probe_tmp")
            [ "$current" = "$value" ] || changes+=("$key ${current:-unset} → $value")
        done
        rm -f "$probe_tmp"
    fi
    if [ ${#changes[@]} -eq 0 ]; then
        echo "wsl-preferences: .wslconfig ok"
    else
        IFS='; '; echo "wsl-preferences: .wslconfig ${changes[*]} (asks)"; unset IFS
    fi
    parts=("RestartWSL helpers rewritten")
    [ -f "$WIN_HOME/bin/rg.exe" ] && parts+=("rg.exe present") || parts+=("install rg.exe")
    parts+=("VS Code todo-tree path merged")
    IFS='; '; echo "windows-files: ${parts[*]}"; unset IFS
    echo "default-distro: make ${WSL_DISTRO_NAME:-this distribution} the default"
    echo "windows-path: append %USERPROFILE%\\bin and %USERPROFILE%\\.local\\bin to the user PATH"
    exit 0
fi
```

(The `mktemp` here is a read-only scratch copy removed before exit; it is the one allowed exception and the grep gate in Step 3 names it.)

Also make the x64 guard answer in probe mode as in the previous script:

```bash
    *) if [ "${WORKBENCH_PROBE:-}" = 1 ]; then echo "windows-files: blocked, Windows x64 required"; exit 0; fi
       echo 'WORKBENCH_EFFECT windows-config blocked: Windows x64 required' >&2; exit 3 ;;
```

and replace `[[ -d "$WIN_HOME" && -d "$WIN_ROAMING" ]] || exit 3` with:

```bash
if [[ ! -d "$WIN_HOME" || ! -d "$WIN_ROAMING" ]]; then
    if [ "${WORKBENCH_PROBE:-}" = 1 ]; then echo "windows-files: blocked, Windows home or AppData unavailable"; exit 0; fi
    exit 3
fi
```

`home/.chezmoiscripts/wsl/run_after_20-sysctl.sh.tmpl`, immediately after `set -euo pipefail` and before the `WORKBENCH_EFFECT_SYSCTL` check:

```bash
if [ "${WORKBENCH_PROBE:-}" = 1 ]; then
    if [[ -f /etc/sysctl.d/99-dev.conf ]] && grep -qx 'vm.swappiness=10' /etc/sysctl.d/99-dev.conf; then
        echo "sysctl: vm.swappiness=10 ok"
    else
        echo "sysctl: write /etc/sysctl.d/99-dev.conf vm.swappiness=10 (sudo)"
    fi
    exit 0
fi
```

- [ ] **Step 3: Add the probe stage and the write-free gate to `render-check.sh`**

In `scripts/render-check.sh`, inside the lint loop after the `shellcheck -S warning "$out"` line, add:

```bash
        # Probe mode must answer with effect lines only, exit 0, and never
        # reach a writer: every write or prompt sits below the probe block.
        probe_out="$tmp/probe.out"
        if WORKBENCH_PROBE=1 HOME="$dest" bash "$out" >"$probe_out" 2>"$tmp/probe.err"; then
            if ! grep -qE '^[a-z0-9-]+: .+$' "$probe_out" || grep -vqE '^[a-z0-9-]+: .+$' "$probe_out"; then
                echo "PROBE FAIL: $script printed something other than effect lines:"; cat "$probe_out"; fail=1
            fi
        else
            echo "PROBE FAIL: $script exited $? in probe mode:"; cat "$tmp/probe.err"; fail=1
        fi
        probe_end=$(grep -n 'WORKBENCH_PROBE' "$out" | tail -1 | cut -d: -f1)
        if [ -n "$probe_end" ] && head -n "$probe_end" "$out" | grep -vE '^[[:space:]]*#' | grep -qE 'sudo |read -r|curl -f[^ ]* -o|brew install|apt-get install|npm install|cargo install|install -m|cp |mv |> "\$(WSLCONFIG|PS_PROFILE)'; then
            echo "PROBE FAIL: $script has a writer or prompt above its probe block"; fail=1
        fi
```

The probe runs the rendered script with `HOME` pointed at the scratch destination, so on CI it reports "install …" lines for everything; on a WSL render (`extra=wsl`) the Windows scripts answer "blocked" because `cmd.exe` is absent, which is the correct probe on a non-Windows host.

- [ ] **Step 4: Render-check all three modes**

Run: `scripts/render-check.sh personal pinned && scripts/render-check.sh work latest && scripts/render-check.sh work pinned wsl`
Expected: each ends `OK [...]`; every script prints at least one `name: text` line in probe mode and the writer gate is quiet. Fix any `PROBE FAIL` by moving the probe block above the writer it names, never by weakening the gate.

- [ ] **Step 5: Probe the real machine once, read-only**

Run (on a WSL host; adjust the path for another platform):

```bash
S=$(mktemp -d); chezmoi --config "$HOME/.config/chezmoi/chezmoi.toml.dotfiles-retired" --source "$(git rev-parse --show-toplevel)" --destination "$S/home" --persistent-state "$S/s.boltdb" --cache "$S/c" dump --include=scripts --format=json > "$S/scripts.json" 2>/dev/null || true
python3 - "$S/scripts.json" "$S" <<'EOF'
import json, os, subprocess, sys
scripts = json.load(open(sys.argv[1]))
for name, entry in scripts.items():
    path = os.path.join(sys.argv[2], os.path.basename(name))
    open(path, "w").write(entry["contents"]); os.chmod(path, 0o700)
    env = dict(os.environ, WORKBENCH_PROBE="1")
    r = subprocess.run(["bash", path], env=env, capture_output=True, text=True, timeout=10)
    print(f"{os.path.basename(name)} exit={r.returncode}\n  " + r.stdout.strip().replace("\n", "\n  "))
EOF
rm -rf "$S"
```

(If no retired dotfiles config exists, write a scratch config with `scripts/scratch-init.sh work latest` and point `--config` at it.)
Expected: every script exits 0 with effect lines; `runtime-managers` names the real Go versions, `runtimes` the real Node default versus newest, `wsl-preferences` the one `.wslconfig` change with `(asks)`.

- [ ] **Step 6: Commit**

```bash
git add home/.chezmoiscripts scripts/render-check.sh docs/superpowers/specs/2026-09-30-apply-plan-selection-design.md
git commit -m "feat(scripts): WORKBENCH_PROBE mode prints each effect's delta without changing anything"
```

---

### Task 3: Planner selection, probes and skip-aware apply

**Files:**
- Modify: `internal/machine/plan.go` (Selection, prepare, checkPrerequisites, activeSources, Apply)
- Create: `internal/machine/probe.go`
- Modify: `internal/machine/effects.go` (selection helpers; optional effects always listed)
- Modify: `internal/machine/apply.go` (no ConfigOnly; save selection; AppliedAt)
- Modify: `internal/machine/updates.go:67-90` (update-* effects carry Delta)
- Modify: `internal/machine/dependencies.go:365-390` (Doctor: effects and applied date)

**Interfaces:**
- Consumes: `operation.Effect` fields, `machine.Selection`, `ReadSelection`, `WriteSelection` (Task 1); probe output grammar (Task 2).
- Produces: `machine.Plan(ctx, c, selection Selection) (operation.Plan, error)`; `machine.Apply(ctx, c, selection Selection, displayed operation.Plan, consent, terminal, progress)`; `machine.Reselect(plan operation.Plan, selection Selection, saved Selection) operation.Plan`; `machine.SelectionOf(effects []operation.Effect) Selection`; `machine.CheckSelection(selection Selection) error`.

- [ ] **Step 1: Replace the Selection type and its validation in `effects.go`**

Delete `Selection` from `plan.go` (it now lives in `selection.go`). In `effects.go` delete `CheckEffects` and `selectedEffects`; add:

```go
// optionalNames lists this host's optional effects.
func optionalNames() []string {
	if !isWSL() {
		return nil
	}
	names := make([]string, 0, len(optionalEffects))
	for _, effect := range optionalEffects {
		names = append(names, effect.Name)
	}
	return names
}

// AvailableEffects lists this host's optional effect names for messages.
func AvailableEffects() string { return strings.Join(optionalNames(), ", ") }

// CheckSelection refuses an optional effect this host does not offer. Skips
// are not validated: a skip saved on another platform is kept and ignored.
func CheckSelection(selection Selection) error {
	available := optionalNames()
	for _, name := range selection.Select {
		if !slices.Contains(available, name) {
			return operation.Fail(
				operation.ExitInvalid,
				"effect",
				"Unknown or unavailable effect "+name+"; available here: "+cmp.Or(AvailableEffects(), "none"),
			)
		}
	}
	return nil
}

// hostOptionalEffects lists every optional effect this host offers, naming
// the distribution for default-distro so consent never covers an implicit
// choice.
func hostOptionalEffects() []operation.Effect {
	if !isWSL() {
		return nil
	}
	effects := slices.Clone(optionalEffects)
	for i := range effects {
		if effects[i].Name == "default-distro" {
			if distribution := os.Getenv("WSL_DISTRO_NAME"); distribution != "" {
				effects[i].Description = "Make " + distribution + " the default WSL distribution"
			}
		}
	}
	return effects
}

// applySelection marks each effect checked or not: fixed effects always,
// optional effects only when selected, every other effect unless skipped.
// SavedSkip tells the checklist which skips came from machine.toml.
func applySelection(effects []operation.Effect, selection, saved Selection) []operation.Effect {
	optional := optionalNames()
	for i := range effects {
		effect := &effects[i]
		switch {
		case effect.Fixed:
			effect.Checked, effect.SavedSkip = true, false
		case slices.Contains(optional, effect.Name):
			effect.Checked, effect.SavedSkip = slices.Contains(selection.Select, effect.Name), false
		default:
			effect.Checked = !slices.Contains(selection.Skip, effect.Name)
			effect.SavedSkip = !effect.Checked && slices.Contains(saved.Skip, effect.Name)
		}
	}
	return effects
}

// Reselect returns plan with its effects checked as selection says; the
// checklist uses it so the approved digest is the one the planner recomputes.
func Reselect(plan operation.Plan, selection, saved Selection) operation.Plan {
	plan.Effects = applySelection(slices.Clone(plan.Effects), selection, saved)
	return plan
}

// SelectionOf is the selection a checklist produced: skipped default
// effects and selected optional ones.
func SelectionOf(effects []operation.Effect) Selection {
	optional := optionalNames()
	var selection Selection
	for _, effect := range effects {
		switch {
		case effect.Fixed:
		case slices.Contains(optional, effect.Name):
			if effect.Checked {
				selection.Select = append(selection.Select, effect.Name)
			}
		case !effect.Checked:
			selection.Skip = append(selection.Skip, effect.Name)
		}
	}
	slices.Sort(selection.Skip)
	slices.Sort(selection.Select)
	return selection
}

// scriptEffects lists the effects a provisioning script carries out, by its
// name without chezmoi's prefixes and .sh suffix.
func scriptEffects(script string) []string {
	var names []string
	for name, sources := range effectSources {
		if slices.Contains(sources, script) {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	return names
}
```

Add to `effectSources`: `"sysctl": {"20-sysctl"}, "terminal-adoption": {"00-packages-windows"}, "powershell-adoption": {"00-packages-windows"}, "font-registry": {"00-packages-windows"}, "default-distro": {"10-deploy-windows-configs"}, "windows-path": {"10-deploy-windows-configs"}`. `activeEffects` keeps working; `effectVariable` and `optionalEffects` stay; delete the old `optionalEffectNames` (renamed above).

- [ ] **Step 2: Create `internal/machine/probe.go`**

```go
package machine

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/Sawmonabo/workbench/internal/operation"
)

// probeTimeout bounds one script's probe; a slow version lookup must not
// hold the plan.
const probeTimeout = 5 * time.Second

var probeLine = regexp.MustCompile(`^([a-z0-9-]+): (.+)$`)

// probeEffects fills each effect's Delta from the active scripts run with
// WORKBENCH_PROBE=1, in parallel. A probe that fails, times out or prints
// anything but effect lines leaves its effects marked failed; the plan never
// blocks on a probe, and an unprobed effect stays checked.
func (p *preparation) probeEffects(ctx context.Context, c operation.Context) {
	scripts, err := p.scriptSources(ctx, c)
	if err != nil {
		p.Plan.Warnings = append(p.Plan.Warnings, "Effects were not probed; native script rendering failed")
		return
	}
	directory := filepath.Join(p.scratch, "probe")
	if err = os.Mkdir(directory, 0o700); err != nil {
		p.Plan.Warnings = append(p.Plan.Warnings, "Effects were not probed; scratch directory unavailable")
		return
	}
	environment := scriptEnvironment(c, p.Plan.Dependencies)
	environment = append(environment, "WORKBENCH_PROBE=1")
	for i, value := range environment {
		if rest, ok := strings.CutPrefix(value, "PATH="); ok {
			environment[i] = "PATH=" + filepath.Join(p.scratch, "bin") + ":" + rest
		}
	}
	var (
		mu      sync.Mutex
		wg      sync.WaitGroup
		deltas  = map[string][]string{}
		outcome = map[string]string{}
	)
	for name, contents := range scripts {
		if !p.active[name] {
			continue
		}
		wg.Add(1)
		go func(name, contents string) {
			defer wg.Done()
			lines, status := p.runProbe(ctx, c, directory, environment, name, contents)
			mu.Lock()
			defer mu.Unlock()
			for effect, text := range lines {
				deltas[effect] = append(deltas[effect], text)
			}
			for _, effect := range scriptEffects(name) {
				if outcome[effect] != "failed" && outcome[effect] != "timeout" {
					outcome[effect] = status
				}
			}
		}(name, contents)
	}
	wg.Wait()
	for i := range p.Plan.Effects {
		effect := &p.Plan.Effects[i]
		status, probed := outcome[effect.Name]
		if !probed {
			continue
		}
		effect.Probe = status
		if lines := deltas[effect.Name]; len(lines) > 0 {
			slices.Sort(lines)
			effect.Delta = strings.Join(lines, "; ")
		}
	}
}

// scriptSources renders every provisioning script native would consider,
// keyed by name without chezmoi's prefixes and .sh suffix.
func (p *preparation) scriptSources(
	ctx context.Context,
	c operation.Context,
) (map[string]string, error) {
	rendered, err := p.run(ctx, c, "dump", "--include=scripts", "--format=json")
	if err != nil {
		return nil, err
	}
	var entries map[string]struct {
		Type     string `json:"type"`
		Contents string `json:"contents"`
	}
	if json.Unmarshal([]byte(rendered), &entries) != nil {
		return nil, operation.Fail(operation.ExitFailed, "native", "Unexpected native script output")
	}
	scripts := map[string]string{}
	for name, entry := range entries {
		if entry.Type == "script" {
			scripts[strings.TrimSuffix(filepath.Base(name), ".sh")] = entry.Contents
		}
	}
	return scripts, nil
}

// runProbe runs one rendered script in probe mode and parses its effect
// lines. The status is "ok" only for exit 0 with at least one effect line and
// nothing else on stdout.
func (p *preparation) runProbe(
	ctx context.Context,
	c operation.Context,
	directory string,
	environment []string,
	name, contents string,
) (map[string]string, string) {
	path := filepath.Join(directory, name+".sh")
	if err := os.WriteFile(path, []byte(contents), 0o700); err != nil {
		return nil, "failed"
	}
	output, err := operation.Run(ctx, c, nil, operation.Process{
		Executable:  "/bin/bash",
		Args:        []string{path},
		Directory:   directory,
		Environment: environment,
		Secrets:     p.secrets,
		Timeout:     probeTimeout,
		OutputLimit: 64 << 10,
	})
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || strings.Contains(err.Error(), "timed out") {
			return nil, "timeout"
		}
		return nil, "failed"
	}
	lines := map[string]string{}
	for line := range strings.SplitSeq(strings.TrimSpace(output.Stdout), "\n") {
		match := probeLine.FindStringSubmatch(line)
		if match == nil {
			return nil, "failed"
		}
		if previous, ok := lines[match[1]]; ok {
			lines[match[1]] = previous + "; " + match[2]
		} else {
			lines[match[1]] = match[2]
		}
	}
	if len(lines) == 0 {
		return nil, "failed"
	}
	return lines, "ok"
}
```

`operation.Run` with a `Timeout` returns its own error on expiry; read `internal/operation/process.go:260-270` and match the exact text in the `strings.Contains` check (the plan's `"timed out"` is the expected wording; correct it to the real one and ledger the ruling).

- [ ] **Step 3: Rework `prepare` in `plan.go`**

Add to `preparation`: `active map[string]bool`. Replace the block from `effects := []operation.Effect{` through `plan.Effects = append(plan.Effects, optional...)` with:

```go
	effects := []operation.Effect{
		{
			Name:        "ai-security-settings",
			Description: "Managed AI trust roots, approval/sandbox policy, enabled plugins and work hooks; review policy before apply",
			Privilege:   "user",
			Recovery:    "configuration files only",
			Fixed:       true,
		},
	}
	effects = append(effects, provisioningEffects(answers)...)
	prepared.active, err = prepared.activeSources(ctx, c)
	if err != nil {
		return prepared, err
	}
	plan.Effects = append(plan.Effects, activeEffects(effects, prepared.active)...)
	plan.Effects = append(plan.Effects, hostOptionalEffects()...)
```

Replace `if !selection.ConfigOnly { prepared.planUpdates(ctx, c, files) }` with `prepared.planUpdates(ctx, c, files)`, and immediately after it add:

```go
	prepared.probeEffects(ctx, c)
	saved, err := ReadSelection(c.Native.Config)
	if err != nil {
		return prepared, err
	}
	plan.Effects = applySelection(plan.Effects, selection, saved)
	if c.Native.Destination != c.Home {
		// An isolated destination gets files only; scripts provision the
		// real home.
		for i := range plan.Effects {
			if !plan.Effects[i].Fixed {
				plan.Effects[i].Checked = false
			}
		}
		plan.Warnings = append(plan.Warnings, "Isolated destination: provisioning effects are unchecked and only files apply")
	}
```

Keep the retention effect append, but set `Fixed: true` on it: after `if retention != nil {`, add `retention.Fixed, retention.Checked = true, true`.

In `checkPrerequisites`: drop the `(result.Name != "uv" || !p.selection.ConfigOnly)` condition to just `result.Status != operation.StatusComplete`; delete the `Full provisioning requires the actual user's home` block; replace `optional, err := selectedEffects(p.selection)` with `if err := CheckSelection(p.selection); err != nil { return nil, nil, err }`, and change the function to return `(Answers, error)` (update its caller: `answers, err := prepared.checkPrerequisites(ctx, c, requirements)`).

In `activeSources`: delete the `if p.selection.ConfigOnly { return active, nil }` lines.

In `planEdits`: count unchanged targets. Where `action != "unchanged"` appends an edit, add an `else { p.Plan.UnchangedTargets++ }`.

- [ ] **Step 4: Make `Apply` honour the selection**

In `preparation.Apply` replace the block from `environment := p.environment` through the `for i, value := range environment` PATH loop with:

```go
	if err := p.removeSkippedScripts(); err != nil {
		return err
	}
	environment := scriptEnvironment(c, p.Plan.Dependencies)
	for _, effect := range p.Plan.Effects {
		if effect.Checked && slices.Contains(optionalNames(), effect.Name) {
			environment = append(environment, effectVariable(effect.Name)+"=1")
		}
	}
	if updates := p.checkedUpdates(p.appUpdates); len(updates) > 0 {
		environment = append(environment, "WORKBENCH_APP_UPDATES="+strings.Join(updates, " "))
	}
	if updates := p.checkedUpdates(p.toolUpdates); len(updates) > 0 {
		environment = append(environment, "WORKBENCH_TOOL_UPDATES="+strings.Join(updates, " "))
	}
	for i, value := range environment {
		if rest, ok := strings.CutPrefix(value, "PATH="); ok {
			environment[i] = "PATH=" + filepath.Join(p.scratch, "bin") + ":" + rest
		}
	}
```

and add these methods to `plan.go`:

```go
// removeSkippedScripts deletes, from the private source copy, every script
// whose effects are all unchecked, so native neither runs it nor records it
// as run; a later apply with the effect checked runs it as if new.
func (p *preparation) removeSkippedScripts() error {
	checked := map[string]bool{}
	for _, effect := range p.Plan.Effects {
		checked[effect.Name] = effect.Checked
	}
	root := filepath.Join(p.native.Source, "home", ".chezmoiscripts")
	return filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		base := entry.Name()
		if !strings.HasPrefix(base, "run_") || !strings.HasSuffix(base, ".sh.tmpl") {
			return nil
		}
		script := strings.TrimSuffix(base[strings.LastIndex(base, "_")+1:], ".sh.tmpl")
		owners := scriptEffects(script)
		if len(owners) == 0 || slices.ContainsFunc(owners, func(name string) bool { return checked[name] }) {
			return nil
		}
		return os.Remove(path)
	})
}

// checkedUpdates keeps the Homebrew updates whose update-<name> effect is
// checked.
func (p *preparation) checkedUpdates(names []string) []string {
	var kept []string
	for _, name := range names {
		for _, effect := range p.Plan.Effects {
			if effect.Name == "update-"+name && effect.Checked {
				kept = append(kept, name)
			}
		}
	}
	return kept
}
```

Script names with a numeric prefix contain underscores only in chezmoi's attribute prefix (`run_once_after_10-runtime-managers.sh.tmpl` → `10-runtime-managers`), which the `LastIndex(base, "_")` cut relies on; `effectSources` keys use exactly those names.

- [ ] **Step 5: Rework `apply.go` for the selection**

Replace every `selection.ConfigOnly` / `a.selection.ConfigOnly` use:

- The early "nothing to approve" check becomes:

```go
	provisioning := slices.ContainsFunc(displayed.Effects, func(effect operation.Effect) bool {
		return effect.Checked && !effect.Fixed
	})
	if displayed.Complete && len(displayed.Edits) == 0 && !provisioning {
		... (same body: unchanged unless a partial operation is pending)
			Message: "Every file already matches and no effect is checked; nothing to apply",
```

- In both `withoutCheckpoint` and `withCheckpoint` start with `plan := a.prepared.Plan` and `provisioning := slices.ContainsFunc(plan.Effects, func(effect operation.Effect) bool { return effect.Checked && !effect.Fixed })`.
- In `withoutCheckpoint`, replace `if !a.selection.ConfigOnly { ... }` around `a.prepared.Apply` with the same `provisioning` test computed from `a.prepared.Plan.Effects`; replace `if !a.selection.ConfigOnly || settled != nil` with `if provisioning || settled != nil`; and replace both `effectResults(...)` appends with `effectResults(checkedEffects(plan.Effects))` where:

```go
func checkedEffects(effects []operation.Effect) []operation.Effect {
	return slices.DeleteFunc(slices.Clone(effects), func(effect operation.Effect) bool { return !effect.Checked })
}
```

- In `withCheckpoint`, `if !a.selection.ConfigOnly && runErr != nil` becomes `if provisioning && runErr != nil`.
- Wherever `state.AppliedConfiguration = ...` is set (both paths), add `now := time.Now().UTC(); state.AppliedAt = &now` (`current.AppliedAt` in the first path).
- After each successful `WriteState`, save the selection:

```go
	if err = WriteSelection(a.m, a.c.Native.Config, SelectionOf(plan.Effects)); err != nil {
		return operation.Fail(operation.ExitPartial, "state", "Applied; saving the effect selection failed")
	}
```

- `effectResults` keeps its shape; its `Message` becomes `effect.Delta` when non-empty, else `effect.Description`.

- [ ] **Step 6: Review Focus 1: a foreign saved skip is ignored and kept**

Run (scratch answers from `scripts/scratch-init.sh work latest`, then append to that file):

```bash
cfg=$(scripts/scratch-init.sh work latest); printf '\n[effects]\nskip = ["macos-packages", "windows-files"]\n' >> "$cfg"
go run ./cmd/workbench apply --dry-run --json --local-build --machine-config "$cfg" 2>/dev/null | python3 -c "import json,sys; r=json.load(sys.stdin); p=[c['details'] for c in r['results'] if isinstance(c.get('details'),dict)][0]; print([ (e['name'], e['checked'], e.get('saved_skip', False)) for e in p['effects']])"
```

(`--machine-config` no longer exists after Task 4; until then it does. Re-run this check in Task 4 step 8 with the saved `machine.toml` instead.)
Expected: no error; `macos-packages` is absent from the list; `windows-files` shows `checked=False, saved_skip=True` on WSL; every other effect `checked=True`.

- [ ] **Step 7: `updates.go` and `Doctor`**

In `updates.go`, where each `update-<name>` effect is built, set `Delta: update.installed + " → " + update.latest` so the checklist shows the versions without the prose.

In `Doctor` (`dependencies.go`), after `results := installed(state)` add:

```go
	selection, selectionErr := ReadSelection(c.Native.Config)
	effects := operation.Component{Name: "effects", Status: operation.StatusUnchanged, Message: "none skipped"}
	switch {
	case selectionErr != nil:
		effects.Status, effects.Message = operation.StatusBlocked, selectionErr.Error()
	case len(selection.Skip) > 0 || len(selection.Select) > 0:
		effects.Status = operation.StatusComplete
		var parts []string
		if len(selection.Skip) > 0 {
			parts = append(parts, "skipped "+strings.Join(selection.Skip, ", ")+" (saved)")
		}
		if len(selection.Select) > 0 {
			parts = append(parts, "selected "+strings.Join(selection.Select, ", ")+" (saved)")
		}
		effects.Message = strings.Join(parts, "; ")
	}
	results = append(results, effects)
```

In `installed`, when `AppliedAt != nil`, append `" on " + source.AppliedAt.Local().Format("Jan 2, 2006")` to the applied message (pass the state, it is already the parameter).

- [ ] **Step 8: Build and test**

Run: `go build ./... && go vet ./... && go test ./...`
Expected: `internal/cli` fails to compile (it still references `ConfigOnly`, `CheckEffects`, `machineSelection`); that is Task 4. `go test ./internal/operation/ ./internal/machine/ ./internal/release/` must pass. Record the exact cli compile errors in the ledger; they are the checklist for Task 4 step 1.

- [ ] **Step 9: Review Focus 3: a skipped once-script is not recorded**

Prove the removal path with the private source copy: add a temporary `t.Log`-free check by running, after Task 4 lands, `apply --dry-run --json` with `skip = ["runtime-managers"]` saved, then inspect the chezmoi state Workbench uses:

```bash
go run ./cmd/workbench apply --yes --local-build --destination "$(mktemp -d)" 2>&1 | tail -2
```

is not a valid check (isolated destinations uncheck every effect). Instead verify statically in this step, and dynamically in Task 7 step 3 on the real machine: `removeSkippedScripts` runs before `chezmoi apply`, and a removed source cannot enter chezmoi's `scriptState` bucket. Ledger the reasoning as a ruling; the dynamic check in Task 7 reads the bucket.

- [ ] **Step 10: Commit**

```bash
git add internal/machine
git commit -m "feat(machine): probe effect deltas, check effects by saved selection and skip their scripts"
```

---

### Task 4: CLI apply checklist, flags and rendering

**Files:**
- Modify: `internal/cli/root.go` (options, flags, action, render, writeDetails)
- Modify: `internal/cli/machine.go` (apply, doctor, init, machinePlan, applyMachine)
- Modify: `internal/cli/planview.go` (writeMachinePlan, brand)
- Modify: `internal/cli/prompt.go` (choosePlan)
- Modify: `internal/cli/consent.go` (unchanged API; keep)
- Create: `internal/cli/checkout.go` (`--local-build`)

**Interfaces:**
- Consumes: `machine.Plan/Apply/Reselect/SelectionOf/ReadSelection/CheckSelection` (Task 3).
- Produces: `applyMachine(cmd, c, o, ask []string) (operation.Result, operation.Plan, error)`, `setUp(cmd, c, o, terminal, result, questions bool, ask []string)`, `machinePlan(cmd, c, o)`, `writeMachinePlan(w, plan, verbose bool)`, `choosePlan(plan) (checked []string, approved bool, err error)`.

- [ ] **Step 1: Options and flags in `root.go`**

Replace the `options` struct and the flag helpers:

```go
type options struct {
	resolve                              operation.Options
	json, nonInteractive, verbose        bool
	yes, reset, localBuild               bool
	approvePlan                          string
	rendered                             bool
}
```

Add `flags.BoolVar(&o.verbose, "verbose", false, "Show unchanged files, recovery text and limits in plans")` beside `--json`. Delete `sourceFlag` and `machineConfigFlag`; `destinationFlag` stays but adds `_ = cmd.Flags().MarkHidden("destination")`. `approveFlag` adds `_ = cmd.Flags().MarkHidden("approve-plan")`. Add:

```go
func (o *options) localBuildFlag(cmd *cobra.Command) {
	cmd.Flags().BoolVar(&o.localBuild, "local-build", false, "Use the Workbench checkout containing the current directory")
	_ = cmd.Flags().MarkHidden("local-build")
}
```

In `action`, before `operation.Resolve(selection)`:

```go
		if o.localBuild {
			checkout, err := localCheckout()
			if err != nil {
				result := operation.NewResult(cmd.CommandPath())
				result.SetError(err)
				o.rendered = true
				_ = render(cmd.OutOrStdout(), cmd.ErrOrStderr(), o.json, result)
				return err
			}
			selection.Source = checkout
		}
```

Create `internal/cli/checkout.go`:

```go
package cli

import (
	"os"
	"path/filepath"

	"github.com/Sawmonabo/workbench/internal/operation"
)

// localCheckout finds the Workbench checkout the current directory is in: the
// nearest ancestor holding .chezmoiroot and go.mod. --local-build takes no
// path, so a typo cannot select an unrelated tree.
func localCheckout() (string, error) {
	directory, err := os.Getwd()
	if err != nil {
		return "", operation.Fail(operation.ExitInvalid, "source", "Cannot resolve the current directory")
	}
	for {
		root, rootErr := os.Stat(filepath.Join(directory, ".chezmoiroot"))
		module, moduleErr := os.Stat(filepath.Join(directory, "go.mod"))
		if rootErr == nil && moduleErr == nil && root.Mode().IsRegular() && module.Mode().IsRegular() {
			return directory, nil
		}
		parent := filepath.Dir(directory)
		if parent == directory {
			return "", operation.Fail(
				operation.ExitInvalid,
				"source",
				"--local-build needs a Workbench checkout; run it inside one",
			)
		}
		directory = parent
	}
}
```

- [ ] **Step 2: Rewrite `applyCommand`, `machinePlan`, `applyMachine` in `machine.go`**

```go
// applyCommand makes this machine match the installed release, or the local
// checkout, after a checklist of what would change.
func applyCommand(o *options) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "apply",
		Short: "Show what would change on this machine as a checklist, then apply what is checked",
		Long: "Install Workbench's tools when missing and ask the machine questions your saved " +
			"answers lack, then show every file change and provisioning effect with what it would " +
			"do here. Space unchecks an effect, enter applies; unchecked effects are remembered for " +
			"this machine until --reset. --yes applies the saved selection without asking; " +
			"--dry-run only shows the checklist.",
		Args: cobra.NoArgs,
		RunE: o.action(
			nativeAction,
			func(cmd *cobra.Command, c operation.Context) (operation.Result, error) {
				if dryRun, _ := cmd.Flags().GetBool("dry-run"); dryRun {
					return machinePlan(cmd, c, o)
				}
				c.ReadOnly = false
				result, plan, err := applyMachine(cmd, c, o, nil)
				if err == nil {
					result.Summary = appliedSummary(plan)
				}
				return result, err
			},
		),
	}
	cmd.Flags().Bool("dry-run", false, "Show the checklist without installing, asking or applying")
	cmd.Flags().BoolVarP(&o.yes, "yes", "y", false, "Apply the saved selection without asking")
	cmd.Flags().BoolVar(&o.reset, "reset", false, "Forget the saved skips; everything is checked again")
	o.localBuildFlag(cmd)
	o.destinationFlag(cmd)
	o.approveFlag(cmd)
	return cmd
}

// machineSelection is the saved selection, or an empty one with --reset.
func machineSelection(c operation.Context, o *options) (machine.Selection, error) {
	if o.reset {
		return machine.Selection{}, nil
	}
	return machine.ReadSelection(c.Native.Config)
}

// machinePlan shows the checklist without applying it.
func machinePlan(cmd *cobra.Command, c operation.Context, o *options) (operation.Result, error) {
	result := operation.NewResult(cmd.CommandPath())
	selection, err := machineSelection(c, o)
	if err != nil {
		return result, err
	}
	plan, err := machine.Plan(cmd.Context(), c, selection)
	if operation.ExitCode(err) == operation.ExitInterrupted {
		return result, err
	}
	status := operation.StatusComplete
	if err != nil {
		status = operation.StatusBlocked
	}
	result.Results = append(result.Results, operation.Component{Name: "machine-plan", Status: status, Details: plan})
	if err != nil {
		return result, err
	}
	result.PlanDigest = plan.Digest()
	return result, nil
}

// applyMachine installs Workbench's tools when missing and asks the machine
// questions the saved answers lack (or ask names), plans, gets the selection
// approved and applies it. A local checkout uses the saved answers and tools
// as they are.
func applyMachine(
	cmd *cobra.Command,
	c operation.Context,
	o *options,
	ask []string,
) (operation.Result, operation.Plan, error) {
	result := operation.NewResult(cmd.CommandPath())
	terminal, progress, closeConsole := nativeConsole(o, cmd.ErrOrStderr())
	defer closeConsole()
	if !c.Native.Developer {
		var err error
		c, err = setUp(cmd, c, o, terminal, &result, true, ask)
		if err != nil {
			return result, operation.Plan{}, err
		}
	}
	selection, err := machineSelection(c, o)
	if err != nil {
		return result, operation.Plan{}, err
	}
	plan, err := machine.Plan(cmd.Context(), c, selection)
	if err != nil {
		return result, plan, err
	}
	consent := consentFor(o, o.approvePlan)
	switch {
	case o.approvePlan != "":
	case o.yes:
		consent.ApprovedDigest = plan.Digest()
	case o.interactive():
		checked, approved, chooseErr := choosePlan(plan, o.verbose)
		if chooseErr != nil {
			return result, plan, chooseErr
		}
		if !approved {
			return result, plan, operation.Fail(operation.ExitBlocked, "consent", "Plan was not approved; no changes made")
		}
		for i := range plan.Effects {
			if !plan.Effects[i].Fixed {
				plan.Effects[i].Checked = slices.Contains(checked, plan.Effects[i].Name)
			}
		}
		selection = machine.SelectionOf(plan.Effects)
		saved, savedErr := machine.ReadSelection(c.Native.Config)
		if savedErr != nil {
			return result, plan, savedErr
		}
		plan = machine.Reselect(plan, selection, saved)
		consent.ApprovedDigest = plan.Digest()
	default:
		// --json or --non-interactive: show the plan; WithMutation then
		// refuses without a digest.
		result.Results = append(result.Results, operation.Component{
			Name:    "machine-plan",
			Status:  operation.StatusComplete,
			Details: plan,
		})
	}
	result.PlanDigest = plan.Digest()
	applied, err := machine.Apply(cmd.Context(), c, selection, plan, consent, terminal, progress)
	result.Results = append(result.Results, applied.Results...)
	result.OperationID = applied.OperationID
	return result, plan, err
}

// appliedSummary is the branded result line.
func appliedSummary(plan operation.Plan) string {
	effects, skipped := 0, 0
	for _, effect := range plan.Effects {
		switch {
		case effect.Fixed:
		case effect.Checked:
			effects++
		default:
			skipped++
		}
	}
	line := fmt.Sprintf("[WorkBench] Applied: %d files, %d effects", len(plan.Edits), effects)
	if skipped > 0 {
		line += fmt.Sprintf("; %d skipped", skipped)
	}
	return line
}
```

Delete `addAskFlag`, `addEffectFlag`, the old `machineSelection`, `matchSummary`. Add `"fmt"` and `"slices"` to the imports.

- [ ] **Step 3: `doctor` and `init`**

`doctorCommand`: replace `o.sourceFlag(cmd)` with `o.localBuildFlag(cmd)`.

`initCommand`: drop `Hidden: true` and `MarkFlagRequired("answers-from")`; add `cmd.Flags().StringSlice("ask", nil, "Ask these saved machine answers again, for example --ask machine_role")`; in `RunE`, before the adoption planner:

```go
			ask, _ := cmd.Flags().GetStringSlice("ask")
			from, _ := cmd.Flags().GetString("answers-from")
			if (from == "") == (len(ask) == 0) {
				return result, operation.Fail(operation.ExitInvalid, "init", "init takes --answers-from FILE or --ask KEY")
			}
			if len(ask) > 0 {
				if !o.interactive() {
					return result, operation.Fail(operation.ExitInvalid, "ask", "--ask needs a terminal")
				}
				if err := selectSource(&c); err != nil {
					return result, err
				}
				if err := machine.CheckAsk(c.Native.Config, ask); err != nil {
					return result, err
				}
				terminal, _, closeConsole := nativeConsole(o, cmd.ErrOrStderr())
				defer closeConsole()
				c.ReadOnly = false
				if _, err := setUp(cmd, c, o, terminal, &result, true, ask); err != nil {
					return result, err
				}
				result.Summary = "[WorkBench] Saved your machine answers; run workbench apply"
				return result, nil
			}
```

and change the adoption success summary to `"[WorkBench] Saved your machine answers; run workbench apply"`. Change `initCommand`'s `Short` to "Adopt answers from an existing chezmoi config, or ask a saved answer again".

- [ ] **Step 4: `writeMachinePlan` and the brand in `planview.go`**

Add:

```go
// writeMachinePlan prints the checklist view: changed files with their line
// counts, then one line per effect with its probed delta, a privilege tag and
// whether a skip was saved. Recovery text and unchanged files are verbose.
func writeMachinePlan(w io.Writer, plan operation.Plan, verbose bool) error {
	var b strings.Builder
	b.WriteString("[WorkBench] Plan for this machine")
	if plan.Source.Release != "" {
		b.WriteString(" (" + sourceName(&plan.Source) + ")")
	}
	b.WriteString("\n")
	fmt.Fprintf(&b, "\nFiles (%d changed, %d unchanged)\n", len(plan.Edits), plan.UnchangedTargets)
	if len(plan.Edits) == 0 {
		b.WriteString("  none\n")
	}
	rows := make([][]string, 0, len(plan.Edits))
	for _, edit := range plan.Edits {
		rows = append(rows, []string{homePath(edit.Path), editSummary(edit)})
	}
	writeColumns(&b, rows)
	b.WriteString("\nEffects\n")
	rows = rows[:0]
	for _, effect := range plan.Effects {
		rows = append(rows, effectRow(effect))
	}
	if len(rows) == 0 {
		b.WriteString("  none\n")
	}
	writeColumns(&b, rows)
	sections := []struct {
		title string
		lines []string
	}{{"Prerequisites", plan.Prerequisites}, {"Warnings", plan.Warnings}}
	if verbose {
		sections = append(sections, struct {
			title string
			lines []string
		}{"Recovery limits", plan.RecoveryLimits})
	}
	for _, section := range sections {
		if len(section.lines) > 0 {
			b.WriteString("\n" + section.title + ":\n")
			for _, line := range section.lines {
				b.WriteString("  " + line + "\n")
			}
		}
	}
	if !plan.Complete {
		b.WriteString("\nThis plan is incomplete and cannot be applied as shown.\n")
	}
	_, err := io.WriteString(w, b.String())
	return err
}

// editSummary keeps the counts and mode of an edit's summary and drops the
// "not previously written by Workbench" note, which the first apply adds to
// every line.
func editSummary(edit operation.Edit) string {
	summary := strings.TrimSuffix(edit.Summary, "; not previously written by Workbench")
	if edit.Action == "create" && summary == "" {
		return "new"
	}
	if edit.Action == "remove" {
		return "removed"
	}
	return summary
}

// effectRow is one checklist line: box, name, delta, privilege tag, saved mark.
func effectRow(effect operation.Effect) []string {
	box := "[x]"
	if !effect.Checked {
		box = "[ ]"
	}
	if effect.Fixed {
		box = "   "
	}
	delta := effect.Delta
	switch {
	case delta == "" && effect.Probe != "":
		delta = "unprobed (" + effect.Probe + ")"
	case delta == "":
		delta = effect.Description
	}
	note := ""
	if effect.SavedSkip {
		note = "skipped (saved)"
	}
	return []string{box, effect.Name, delta, privilegeTag(effect), note}
}

// privilegeTag shortens "sudo; network and font-cache effects" to "sudo, network".
func privilegeTag(effect operation.Effect) string {
	tag := strings.TrimSpace(strings.Split(effect.Privilege, ";")[0])
	tag = strings.TrimSuffix(tag, " user")
	if strings.Contains(effect.Privilege, "network") {
		tag += ", network"
	}
	return tag
}
```

In `writePlan`, change `title := "Plan for " + ...` to `title := "[WorkBench] Plan for " + ...`. In `root.go` `writeDetails`, keep `case operation.Plan: return writePlan(out, details)` for non-machine plans but route machine plans: machine plans are the ones whose component name is `machine-plan`; the simplest routing is in `render`: before `writeDetails`, `if plan, ok := component.Details.(operation.Plan); ok && component.Name == "machine-plan" { err = writeMachinePlan(out, plan, verbose) } else { err = writeDetails(out, component.Details) }`. `render` gains a `verbose bool` parameter passed from `o.verbose` at both call sites.

- [ ] **Step 5: `choosePlan` in `prompt.go`**

Replace `confirmPlan` with:

```go
// choosePlan shows the checklist on the controlling terminal: the files, then
// a multi-select of the effects with the plan's checks as defaults. Enter
// approves exactly that selection; esc or ctrl+c approves nothing.
func choosePlan(plan operation.Plan, verbose bool) ([]string, bool, error) {
	terminal, err := openTerminal()
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = terminal.Close() }()
	if err := writeMachinePlan(terminal, operation.Plan{
		Source: plan.Source, Scope: plan.Scope, Edits: plan.Edits,
		UnchangedTargets: plan.UnchangedTargets, Prerequisites: plan.Prerequisites,
		Warnings: plan.Warnings, RecoveryLimits: plan.RecoveryLimits, Complete: plan.Complete,
	}, verbose); err != nil {
		return nil, false, err
	}
	var options []huh.Option[string]
	var fixed []string
	var checked []string
	for _, effect := range plan.Effects {
		if effect.Fixed {
			fixed = append(fixed, effect.Name)
			continue
		}
		row := effectRow(effect)
		label := fmt.Sprintf("%-24s %s  (%s)", row[1], row[2], row[3])
		if row[4] != "" {
			label += "  " + row[4]
		}
		options = append(options, huh.NewOption(label, effect.Name).Selected(effect.Checked))
		if effect.Checked {
			checked = append(checked, effect.Name)
		}
	}
	if len(options) == 0 {
		approved := false
		answered, askErr := ask(terminal, huh.NewConfirm().
			Title("[WorkBench] Apply these files?").
			Affirmative("Yes").Negative("No").Value(&approved))
		return fixed, answered && approved && askErr == nil, nil
	}
	answered, err := ask(terminal, huh.NewMultiSelect[string]().
		Title("[WorkBench] Effects: space toggles, enter applies, esc quits").
		Description("Unchecked effects are remembered for this machine; apply --reset forgets them.").
		Options(options...).
		Filterable(false).
		Height(len(options)+2).
		Value(&checked))
	if err != nil {
		return nil, false, operation.Fail(operation.ExitBlocked, "consent", "No complete approval was received")
	}
	if !answered {
		return nil, false, nil
	}
	_, _ = fmt.Fprintf(terminal, "[WorkBench] Applying %d of %d effects\n", len(checked), len(options))
	return append(checked, fixed...), true, nil
}
```

In `consent.go`, `consentFor` no longer sets `Confirm` (the checklist already approved); delete the `if o.interactive() { consent.Confirm = confirmPlan }` lines. `revert`'s and `project configure`'s approval still use `Confirm`: grep `consentFor(` callers; for those (`recovery.go`, `project.go`) pass a confirm explicitly: add `func confirmFor(o *options) func(operation.Plan, string) (bool, error)` returning the old Yes/No `confirmPlan` (keep `confirmPlan`, renamed from the deleted body above, printing `writePlan`), and set `consent.Confirm = confirmFor(o)` in those two call sites only.

- [ ] **Step 6: Build**

Run: `go build ./... && go vet ./... && go test ./...`
Expected: `internal/cli/release.go` still fails (it references `checkAsk`, `machineSelection(cmd)`, `install-only`); that is Task 5. If `internal/cli` has other errors, fix them here (they are mechanical: removed helpers).

- [ ] **Step 7: Lint**

Run: `go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.12.2 run ./internal/machine/... ./internal/operation/...` (the pin is `.golangci-lint-version`; CI runs it on everything).
Expected: clean. `funlen` (100 lines/60 statements) is the likely complaint on `applyMachine`; split the interactive branch into `approveInteractively(plan, c, o) (operation.Plan, machine.Selection, string, error)` if it trips.

- [ ] **Step 8: Review Focus 4 and 1, re-run with the real flags (after Task 5 compiles)**

Deferred to Task 7 step 2, which has a compiling binary; keep the commit here.

- [ ] **Step 9: Commit**

```bash
git add internal/cli
git commit -m "feat(cli): branded apply checklist with remembered effect skips, --yes, --reset and --local-build"
```

---

### Task 5: `update` installs only; `install.sh`; drop the old flags

**Files:**
- Modify: `internal/cli/release.go` (updateCommand, updateRelease, continueInstalled, handoffArgs, continueInstall, configureMachine, setUp; delete checkAsk, nextSteps)
- Modify: `install.sh:1-7` (comment) and its last line

**Interfaces:**
- Consumes: `setUp(cmd, c, o, terminal, result, questions, ask)` (Task 4).
- Produces: `update` ends with summary `[WorkBench] Installed vX and its tools; run workbench apply` (or `… is already installed; …`).

- [ ] **Step 1: Flags**

In `updateCommand`, keep only `--dry-run`, hidden `--bundle`, hidden `--runtime-ready` and `o.destinationFlag(cmd)`; set `Short: "Install the latest Workbench release, or VERSION, and its tools"` and `Long: "Install the latest Workbench release, or VERSION (an older one goes back), and the chezmoi, uv, Python and TOML Kit it pins. It changes only Workbench's own files and keeps the previous release, so running it is the go-ahead. It never applies the machine; run workbench apply next."`.

- [ ] **Step 2: Flow**

In `updateRelease` delete the `checkAsk` and `machine.CheckEffects` calls. Replace `nextSteps(cmd)` uses with the literal `"its tools"` and delete `nextSteps`. Replace `handoffArgs` with:

```go
func handoffArgs(o *options, install string) []string {
	args := []string{"update", "--runtime-ready", install}
	if o.nonInteractive {
		args = append(args, "--non-interactive")
	}
	if o.json {
		args = append(args, "--json")
	}
	if o.resolve.Destination != "" {
		args = append(args, "--destination", o.resolve.Destination)
	}
	return args
}
```

(update both callers: `handoffArgs(o, "installed")`, `handoffArgs(o, "unchanged")`). Replace `configureMachine` with:

```go
// installTools runs in the activated runtime: it puts the pinned tools in
// place, without asking, and stops. Applying the machine is apply's job.
func installTools(
	cmd *cobra.Command,
	c operation.Context,
	o *options,
	result operation.Result,
	version string,
	unchanged bool,
) (operation.Result, error) {
	_, err := setUp(cmd, c, o, nil, &result, false, nil)
	if err != nil {
		return result, err
	}
	result.Summary = "[WorkBench] Installed " + version + " and its tools; run workbench apply"
	if unchanged {
		result.Summary = "[WorkBench] " + version + " is already installed; run workbench apply"
	}
	return result, nil
}
```

and in `continueInstall` call `installTools(cmd, c, o, result, metadata.Release, unchanged)`. Delete `checkAsk`. In `setUp`, replace the `ask, _ := cmd.Flags().GetStringSlice("ask")` line with the new `ask []string` parameter (signature `setUp(cmd, c, o, terminal, result, questions bool, ask []string)`).

- [ ] **Step 3: `install.sh`**

Change the header comment lines 5-6 to `# Options: --version vX.Y.Z (default: this release, or the latest). Every other` / `# argument goes to \`workbench update\`, for example --dry-run. Installing never` / `# applies the machine; the CLI's last line says to run \`workbench apply\`.`. The final command line stays `"$tmp/bin/workbench" update "$version" --bundle "$bundle" "$@"`.

- [ ] **Step 4: Build, vet, test, lint**

Run: `go build ./... && go vet ./... && go test ./... && shellcheck -s sh install.sh && go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.12.2 run ./...`
Expected: all clean. `go test` prints `ok` for `internal/operation` (now 4 tests) and `internal/release`.

- [ ] **Step 5: Help surfaces**

Run: `go run ./cmd/workbench apply --help | grep -E '^\s+-'` and `go run ./cmd/workbench update --help | grep -E '^\s+-'`
Expected: apply shows `--destination`? No: it is hidden, so exactly `--dry-run`, `--reset`, `-y, --yes` plus the global `--json`, `--non-interactive`, `--verbose`; update shows `--dry-run` plus the globals. `--approve-plan`, `--local-build`, `--bundle`, `--runtime-ready`, `--destination` do not appear.

- [ ] **Step 6: Review Focus 5: update never plans the machine**

Run: `go run ./cmd/workbench update --dry-run --json 2>/dev/null | python3 -c "import json,sys; r=json.load(sys.stdin); print([c['name'] for c in r['results']], r.get('summary'))"`
Expected: component names contain `release` only (no `machine-plan`), and the summary, when the release is already installed, is `[WorkBench] vX is already installed; run workbench apply` (a `--dry-run` on an installed release returns before the handoff, so the summary may be empty; then run without `--dry-run` and `--json`: the last line must be the `run workbench apply` summary and no checklist appears).

- [ ] **Step 7: Commit**

```bash
git add internal/cli/release.go install.sh
git commit -m "feat(cli): update installs the release and its tools only; apply owns the machine"
```

---

### Task 6: Documentation and the agents' unattended recipe

**Files:**
- Modify: `AGENTS.md` (new "Unattended runs" paragraph under "Development contract")
- Modify: `docs/usage.md`, `README.md`, `docs/acceptance.md`, `docs/wsl.md`, `docs/switch-from-dotfiles.md`, `docs/chezmoi-local-overrides.md`
- Modify: `docs/superpowers/specs/workbench-design.md:135` (update row), `docs/superpowers/specs/workbench-contracts.md` (any `--config-only`/`--source` mention)

- [ ] **Step 1: AGENTS.md**

After the "Routine gates" bullet add:

```markdown
- Unattended runs (agents, scripts): `workbench apply --dry-run --json` prints
  the checklist with `plan_digest`; `workbench apply --approve-plan DIGEST`
  applies exactly that plan and exits 3 if the machine, release or saved
  selection changed since. `workbench init --answers-from FILE --dry-run --json`
  and `--approve-plan` work the same way. `--yes` is for a person who trusts
  the saved selection, not for a caller that did not read the plan. From a
  checkout, add `--local-build` to both calls.
```

- [ ] **Step 2: usage.md**

Rewrite the install options list to: `--dry-run` only, with the sentence "Installing never applies the machine; the CLI's last line says to run `workbench apply`." Rewrite the Commands table rows:

| Command | Behavior |
| --- | --- |
| `apply` | Install Workbench's tools if missing and ask any machine question your answers lack, then show the `[WorkBench]` checklist: changed files with line counts, and each provisioning effect with what it would do here. Space unchecks, enter applies, esc quits. Unchecked effects are remembered. |
| `apply --yes` | Apply the saved selection without asking. |
| `apply --reset` | Forget the saved skips; every effect is checked again, and what you pick is saved. |
| `apply --dry-run` | Print the checklist and exit; installs, asks and changes nothing. |
| `update` | Install the latest release and its tools. Never applies; run `apply` next. |
| `update VERSION` | The same for that release; an older one goes back. |
| `init --answers-from FILE` | One-time adoption of a chezmoi config's `[data]` table. |
| `init --ask KEY` | Ask a saved machine answer again, for example `machine_role`. |

Replace the "Unattended mutation" example with the AGENTS.md recipe, delete every `--config-only`, `--install-only`, `--effect`, `--machine-config`, `--source PATH` sentence, and replace the WSL host-steps paragraph with: "Windows host steps on WSL (`terminal-adoption`, `powershell-adoption`, `font-registry`, `default-distro`, `windows-path`, `sysctl`) appear in the checklist unchecked; checking one is remembered like a skip." Add one paragraph "Probes": "Each effect line is what the script would do, found by running it in a read-only probe mode before the checklist; a probe that fails or exceeds five seconds shows `unprobed` and the effect stays checked." Mention `--verbose` for unchanged files and recovery limits. Replace `--source PATH` with `--local-build` (no value, run inside the checkout) in the developer paragraph.

- [ ] **Step 3: README.md, wsl.md, switch-from-dotfiles.md, chezmoi-local-overrides.md, design, contracts**

- README: `workbench apply --ask machine_role` → `workbench init --ask machine_role`; `./bin/workbench doctor --source .` → `./bin/workbench doctor --local-build`; "or a `--source` checkout" → "or, inside the checkout, `--local-build`"; line 109 likewise.
- wsl.md: replace the `--config-only` and `--effect` sentences with the checklist wording from usage.md.
- switch-from-dotfiles.md: step 1 `--install-only` → plain install (it no longer applies); step 3 `workbench apply --config-only` → `workbench apply`, "uncheck every effect to apply files only, or keep them to provision now"; `--source PATH_TO_CHECKOUT` → `--local-build`; step 2 note about `--machine-config` deleted.
- chezmoi-local-overrides.md: `--machine-config` sentences deleted; `workbench apply --ask machine_role` → `workbench init --ask machine_role`.
- workbench-design.md row for `update`: "… then hand off to the new runtime, which installs its pinned tools and stops. An older VERSION goes back. An already active release is not reinstalled; `apply` is a separate command."
- workbench-contracts.md: replace any `--config-only`/`--source` mention with `--local-build`/"all effects unchecked".
- acceptance.md: in "Releases, install and update" and "Planning and apply", rewrite the sentences that describe `--install-only`, `--config-only`, `--effect`, `--machine-config`, `--source` and the update→apply chain as release targets of this change, each marked "(target; observed checks in docs/superpowers/plans/2026-09-30-apply-plan-selection.md Task 7)" until Task 7 records them, then Task 7 replaces the marker with the observed result.

- [ ] **Step 4: Scan**

Run: `grep -rn -- '--config-only\|--install-only\|--effect \|--machine-config\|--source PATH\|apply --ask' README.md docs AGENTS.md install.sh | grep -v 'superpowers/plans/\|superpowers/specs/2026-09-30-apply' ; echo "exit=$?"`
Expected: `exit=1` (no matches outside this plan and spec).

- [ ] **Step 5: Commit**

```bash
git add AGENTS.md README.md docs install.sh
git commit -m "docs: apply checklist, remembered skips, update/apply split and the unattended recipe"
```

---

### Task 7: Observed checks on this machine and the ledger

**Files:**
- Modify: `docs/acceptance.md` (replace Task 6's markers with observed results)
- Modify: `docs/superpowers/specs/2026-09-30-apply-plan-selection-design.md` (status line)

- [ ] **Step 1: Build the local binary and dry-run from the checkout**

Run: `go build -o bin/workbench ./cmd/workbench && bin/workbench apply --dry-run --local-build`
Expected: a `[WorkBench] Plan for this machine (developer checkout …)` header, `Files (0 changed, 20 unchanged)` on a machine already applied, and one line per effect with real deltas (`runtime-managers` names the installed and target Go, `runtimes` the Node default versus newest, `wsl-preferences` the `.wslconfig` change with `(asks)` on WSL). No `unprobed` lines except `linux-editor-extensions` on WSL, which reports `blocked, code on PATH is not a local desktop`.

- [ ] **Step 2: Review Focus 4: a digest does not survive a selection change**

Run:

```bash
D=$(bin/workbench apply --dry-run --local-build --json 2>/dev/null | python3 -c "import json,sys; print(json.load(sys.stdin)['plan_digest'])")
cp ~/.config/workbench/machine.toml "$(mktemp -d)/machine.toml.bak"   # keep a copy outside the repo
python3 - <<'EOF'
import os, re
p = os.path.expanduser("~/.config/workbench/machine.toml"); s = open(p).read()
s = re.sub(r"\n\[effects\][\s\S]*$", "", s) + '\n[effects]\nskip = ["windows-files"]\n'
open(p, "w").write(s)
EOF
bin/workbench apply --local-build --approve-plan "$D"; echo "exit=$?"
```

Expected: `exit=4` and `Approval digest does not match the current plan; review a new plan`; nothing applied (`workbench doctor` shows the same applied line as before). Then restore `machine.toml` from the copy.

- [ ] **Step 3: Review Focus 3 and the live checklist**

Run `bin/workbench apply --local-build` at a terminal, uncheck `runtime-managers` and every Windows-side effect, enter.
Expected: files unchanged, the checked effects run (Node/tool bumps happen; answer as the scripts ask), `[WorkBench] Applied: 0 files, N effects; M skipped`, and `~/.config/workbench/machine.toml` now ends with `[effects]` listing the skips. Then confirm chezmoi did not record the skipped once-script:

```bash
python3 - <<'EOF'
import os, subprocess
state = os.path.expanduser("~/.local/state/workbench/chezmoi/chezmoi.boltdb")
out = subprocess.run([os.path.expanduser("~/.local/share/workbench/tools/chezmoi/2.70.3/chezmoi"), "state", "dump", "--persistent-state", state], capture_output=True, text=True).stdout
print("10-runtime-managers recorded:", "10-runtime-managers" in out)
EOF
```

Expected: `False`. Run `bin/workbench apply --dry-run --local-build` again: `runtime-managers` shows `[ ]` with `skipped (saved)`.

- [ ] **Step 4: `--yes`, `--reset`, `doctor`, `update`**

- `bin/workbench apply --yes --local-build` → applies the saved selection without a prompt; summary line.
- `bin/workbench apply --dry-run --reset --local-build` → every effect `[x]`, no `skipped (saved)`.
- `bin/workbench doctor --local-build` → `effects: complete — skipped runtime-managers, windows-files, wsl-preferences (saved)` and `applied: … on <today's date>`.
- `bin/workbench update --dry-run` → release component only, no checklist.

- [ ] **Step 5: Record**

Replace each Task 6 marker in `docs/acceptance.md` with the observed sentence (what ran, on which host kind, the result), without dollar figures, account names or absolute paths. Change the spec's status line to `Status: implemented 2026-09-30; observed checks in docs/acceptance.md.`

- [ ] **Step 6: Gates and commit**

Run: `go build ./... && go vet ./... && go test ./... && scripts/render-check.sh personal pinned && scripts/render-check.sh work latest && scripts/render-check.sh work pinned wsl`
Expected: all green.

```bash
git add docs/acceptance.md docs/superpowers/specs/2026-09-30-apply-plan-selection-design.md
git commit -m "docs(acceptance): observed checklist, remembered skips and update/apply split on a WSL host"
```

### Task 8: claude-costs report view

The current report prints one block per working directory, each with a
seven-column model table, a 200-character header line and a footer that
mentions the default scope even when nothing is hidden. On a six-week ledger
that is 150 lines, wider than most terminals, with sub-directories of one
repository listed as separate projects and columns named `w-5m`, `w-1h`,
`cache-r`. This task implements section 7 of
`docs/superpowers/specs/2026-09-30-claude-costs-ledger-design.md` as revised
on 2026-09-30: one line per repository sized to the terminal, plain column
names, the token breakdown behind `--tokens`, the per-model blocks behind
`--detail`, and footer notes only when they say something.

**Files:**
- Modify: `home/dot_local/bin/executable_claude-costs` (scope and rollup, `load_groups`, the report rendering, `cmd_report`, `cmd_rates` headers, `parse_opts`, `cmd_help`)
- Modify: `home/dot_local/share/bash-completion/completions/claude-costs` (flag list)
- Modify: `docs/superpowers/specs/2026-09-30-claude-costs-ledger-design.md` (status line only; section 7 is already the revised design)

**Interfaces:**
- Consumes: nothing from Tasks 1–7; this task is independent and can run first or last.
- Produces: `load_groups(db, opts) -> tuple[list[dict], int]` (groups, hidden project count); `print_table(rows, key, label, opts, grand, total=None)`; `print_footer(cov, card, hidden, shown_n, total_n, opts)`; `print_unpriced(groups)`; `total_row(rows, key, label)`; `cached_share(r)`; `table_columns(opts, grand)`; `clip(s, width)`; `columns()`; `local_time(iso)`; options `detail` and `tokens` replace `compact`.

- [ ] **Step 1: Roll sessions up to their repository and count what the scope hides**

Add `import shutil` after `import select` in the import block. Replace `rollup` and add three helpers after `short`:

```python
def rollup(project: str) -> str:
    """Fold a session's working directory into its repository: the first
    directory below a scope root, else the path before `/.worktrees/`."""
    p = Path(project)
    for root in DEFAULT_ROOTS:
        if root in p.parents:
            return str(root / p.relative_to(root).parts[0])
    return project.split("/.worktrees/")[0]
```

```python
def clip(s: str, width: int) -> str:
    """Keep the end of a path, which is the part that tells projects apart."""
    return s if len(s) <= width else "…" + s[-(width - 1) :]


def columns() -> int:
    return shutil.get_terminal_size((100, 24)).columns if sys.stdout.isatty() else 100


def local_time(iso: str) -> str:
    try:
        return datetime.fromisoformat(iso).astimezone().strftime("%b %-d %-I:%M %p")
    except ValueError:
        return iso
```

Replace `load_groups` so it returns the groups and how many rolled-up projects the default scope left out:

```python
def load_groups(
    db: sqlite3.Connection, opts: SimpleNamespace
) -> tuple[list[dict], int]:
    """One dict per (project, model, account, month) with tokens, calls, cost,
    plus how many projects the default scope left out."""
    where, params = [], []
    if opts.since:
        where.append("substr(ts, 1, 10) >= ?")
        params.append(opts.since)
    if opts.until:
        where.append("substr(ts, 1, 10) <= ?")
        params.append(opts.until)
    sql = (
        "SELECT project, model, account, substr(ts, 1, 7), COUNT(*), "
        + ", ".join(f"SUM({c})" for c in RATE_FIELDS)
        + " FROM responses"
        + (" WHERE " + " AND ".join(where) if where else "")
        + " GROUP BY 1, 2, 3, 4"
    )
    card = load_card()
    groups, hidden = [], set()
    for project, model, account, month, calls, *tokens in db.execute(sql, params):
        project = rollup(project) if opts.rollup else project
        if not in_scope(project, opts):
            hidden.add(project)
            continue
        t = dict(zip(RATE_FIELDS, tokens))
        rate = rate_for(model, card)
        groups.append(
            {
                "project": project,
                "model": model,
                "account": account,
                "month": month or "unknown",
                "calls": calls,
                "cost": cost_of(t, rate) if rate else 0.0,
                "priced": rate is not None,
                **t,
            }
        )
    return groups, len(hidden)
```

- [ ] **Step 2: Replace the rendering**

Delete `print_header`, `print_table` and `print_totals`. In their place, between `coverage` and the `# ---- commands` marker, add:

```python
def total_row(rows: list[dict], key: str, label: str) -> dict:
    t = {key: label, "calls": 0, "cost": 0.0, "priced": True}
    for f in RATE_FIELDS:
        t[f] = 0
    for r in rows:
        t["calls"] += r["calls"]
        t["cost"] += r["cost"]
        t["priced"] = t["priced"] and r["priced"]
        for f in RATE_FIELDS:
            t[f] += r[f]
    return t


def cached_share(r: dict) -> str:
    """Share of prompt tokens served from the cache instead of re-read."""
    prompt = r["input"] + r["cache_write_5m"] + r["cache_write_1h"] + r["cache_read"]
    return f"{r['cache_read'] / prompt * 100:.1f}%" if prompt else "—"


def table_columns(opts: SimpleNamespace, grand: float) -> list[tuple]:
    """(header, width, cell) for every column after the name column."""
    cost = ("cost", 11, lambda r: f"${r['cost']:,.2f}")
    calls = ("calls", 8, lambda r: f"{r['calls']:,}")
    if opts.tokens:
        return [
            cost,
            calls,
            ("input", 9, lambda r: human(r["input"])),
            ("output", 9, lambda r: human(r["output"])),
            ("cache 5m", 9, lambda r: human(r["cache_write_5m"])),
            ("cache 1h", 9, lambda r: human(r["cache_write_1h"])),
            ("cache read", 11, lambda r: human(r["cache_read"])),
        ]
    return [
        cost,
        ("share", 7, lambda r: f"{r['cost'] / grand * 100:.1f}%" if grand else "—"),
        calls,
        ("tokens", 8, lambda r: human(sum(r[f] for f in RATE_FIELDS))),
        ("cached", 7, cached_share),
    ]


def print_table(
    rows: list[dict],
    key: str,
    label: str,
    opts: SimpleNamespace,
    grand: float,
    total: dict | None = None,
) -> None:
    """One line per row, sized to the terminal so nothing wraps; the name
    column takes what the numeric columns leave."""
    cols = table_columns(opts, grand)
    name_w = max(24, columns() - sum(w + 1 for _, w, _ in cols) - 2)

    def line(r: dict, style: str) -> str:
        name = clip(short(str(r[key])), name_w)
        cells = "".join(f" {cell(r):>{w}}" for _, w, cell in cols)
        flag = "" if r["priced"] else f" {YEL}unpriced{OFF}"
        return f"  {style}{name:<{name_w}}{cells}{OFF}{flag}"

    heads = "".join(f" {h:>{w}}" for h, w, _ in cols)
    print(f"\n  {BOLD}{label:<{name_w}}{heads}{OFF}")
    for r in rows:
        print(line(r, ""))
    if total is not None:
        print(f"  {DIM}{'─' * (name_w + len(heads))}{OFF}")
        print(line(total, BOLD))


def print_footer(
    cov: dict,
    card: dict,
    hidden: int,
    shown_n: int,
    total_n: int,
    opts: SimpleNamespace,
) -> None:
    sources = sorted({r["source"] for r in card.values()})
    notes = [
        f"ingested {local_time(cov['last_ingest_at'])}",
        f"rates {', '.join(sources)} (official card {cov['rates_fetched']})",
    ]
    if opts.tokens:
        notes.append(
            "cache 5m / 1h: prompt tokens written to the cache with that lifetime; "
            "cache read: prompt tokens served from it"
        )
    if total_n > shown_n:
        notes.append(f"{shown_n} of {total_n} {opts.by}s shown; totals cover all")
    if hidden:
        notes.append(
            f"{hidden} project{'s' if hidden != 1 else ''} outside ~/dev and ~/repos "
            f"hidden (--all)"
        )
    joined = " · ".join(notes)
    print()
    for n in [joined] if len(joined) + 2 <= columns() else notes:
        print(f"  {DIM}{n}{OFF}")


def print_unpriced(groups: list[dict]) -> None:
    unpriced = sorted({g["model"] for g in groups if not g["priced"]})
    if unpriced:
        print(
            f"\n{YEL}warning:{OFF} no rate for {', '.join(unpriced)}; tokens counted, "
            f"cost shown as 0. Run `claude-costs rates --refresh` or add them to "
            f"{short(str(RATES_FILE))}."
        )
```

- [ ] **Step 3: Replace `cmd_report`**

```python
def cmd_report(opts: SimpleNamespace) -> None:
    db = ensure_ingested(open_ledger(create=True))
    groups, hidden = load_groups(db, opts)
    if not groups:
        print(
            f"no responses matched in ~/dev and ~/repos; {hidden} projects elsewhere "
            "(--all shows them)"
            if hidden
            else "no responses matched (check `claude-costs status`)"
        )
        return
    key = opts.by
    rows = sort_rows(aggregate(groups, key), key, opts)
    shown = rows[: opts.top] if opts.top else rows
    grand = sum(r["cost"] for r in rows)
    if opts.json or opts.csv:
        cols = [key, "cost", "calls", *RATE_FIELDS, "priced"]
        if opts.json:
            json.dump(
                {
                    "by": key,
                    "coverage": coverage(db),
                    "rows": shown,
                    "grand_total": grand,
                    "hidden_projects": hidden,
                },
                sys.stdout,
                indent=1,
            )
            print()
        else:
            w = csv.writer(sys.stdout)
            w.writerow(cols)
            for r in shown:
                w.writerow([r[c] for c in cols])
        return
    cov, card = coverage(db), load_card()
    print(
        f"{BOLD}claude-costs{OFF} · {cov['first']} → {cov['last']} · "
        f"{cov['rows']:,} responses"
    )
    print(f"{DIM}list-price equivalents, not subscription charges{OFF}")
    label = f"total · {len(rows)} {key}{'s' if len(rows) != 1 else ''}"
    by_model = sort_rows(aggregate(groups, "model"), "model", opts)
    by_account = sort_rows(aggregate(groups, "account"), "account", opts)
    if key == "project" and opts.detail:
        for p in shown:
            share = p["cost"] / grand * 100 if grand else 0
            print(
                f"\n{BOLD}{short(p['project'])}{OFF}  {GRN}${p['cost']:,.2f}{OFF}  "
                f"{DIM}{share:.1f}% · {p['calls']:,} calls{OFF}"
            )
            models = sort_rows(
                aggregate([g for g in groups if g["project"] == p["project"]], "model"),
                "model",
                opts,
            )
            print_table(models, "model", "model", opts, grand)
        print_table(
            by_model,
            "model",
            "model (all projects)",
            opts,
            grand,
            total_row(by_model, "model", label),
        )
    else:
        print_table(shown, key, key, opts, grand, total_row(rows, key, label))
        if key != "model":
            print_table(by_model, "model", "model", opts, grand)
    if key != "account" and len(by_account) > 1:
        print_table(by_account, "account", "account", opts, grand)
    print_footer(cov, card, hidden, len(shown), len(rows), opts)
    print_unpriced(groups)
```

- [ ] **Step 4: Column names in `rates`, the two new flags, help and completion**

In `cmd_rates` replace the header and row prints with:

```python
    print(
        f"  {DIM}{'model prefix':<28} {'input':>8} {'output':>8} {'cache 5m':>9} "
        f"{'cache 1h':>9} {'cache read':>10}  source{OFF}"
    )
```

and

```python
        print(
            f"  {m:<28} {r['input']:>8.2f} {r['output']:>8.2f} {r['cache_write_5m']:>9.2f} "
            f"{r['cache_write_1h']:>9.2f} {r['cache_read']:>10.3f}  {r['source']}{flag}"
        )
```

In `parse_opts` replace `compact=False,` with `detail=False,` and `tokens=False,` on two lines, and replace the `--compact` branch with:

```python
        elif a == "--detail":
            o.detail = True
        elif a == "--tokens":
            o.tokens = True
```

In `cmd_help` replace the usage line `[--all] [--top N] [--sort cost|name|calls] [--compact] [--no-rollup]` with:

```
               [--all] [--top N] [--sort cost|name|calls] [--detail] [--tokens]
               [--no-rollup]
```

In `home/dot_local/share/bash-completion/completions/claude-costs` replace `--compact` in `opts=` with `--detail --tokens`.

Run: `rg -n "compact|w-5m|w-1h|cache-r|print_header|print_totals" home/dot_local/bin/executable_claude-costs home/dot_local/share/bash-completion/completions/claude-costs`
Expected: no output.

- [ ] **Step 5: Compile and lint**

Run:
```bash
cd "$(git rev-parse --show-toplevel)"
python3 -m py_compile home/dot_local/bin/executable_claude-costs
uvx ruff check --config home/dot_config/ruff/pyproject.toml home/dot_local/bin/executable_claude-costs
uvx ruff format --check --config home/dot_config/ruff/pyproject.toml home/dot_local/bin/executable_claude-costs
```
Expected: `All checks passed!` and `1 file already formatted`. If `ruff format --check` reports a reformat, run `uvx ruff format --config home/dot_config/ruff/pyproject.toml home/dot_local/bin/executable_claude-costs` and rerun the check.

- [ ] **Step 6: Check every view against a synthetic ledger**

The fixture lives under a scratch directory and the script's own `CLAUDE_COSTS_*` and `CLAUDE_CONFIG_DIR` overrides keep it away from the real ledger. The working directories are under the real `$HOME/dev` so the default scope and the rollup apply; the directories need not exist.

```bash
S=$(mktemp -d); mkdir -p "$S/cfg/projects/-home-u-dev-app" "$S/data" "$S/state" "$S/rates"
echo '{"email":"synthetic@example.test"}' > "$S/cfg/.claude.json"
cat > "$S/cfg/projects/-home-u-dev-app/s1.jsonl" <<EOF
{"type":"assistant","cwd":"$HOME/dev/zz-fixture/sub/dir","sessionId":"s1","requestId":"req-1","timestamp":"2026-09-01T10:00:00Z","message":{"id":"m1","model":"claude-opus-5-5","usage":{"input_tokens":100,"output_tokens":200,"cache_creation":{"ephemeral_5m_input_tokens":300,"ephemeral_1h_input_tokens":0},"cache_read_input_tokens":1000}}}
{"type":"assistant","cwd":"$HOME/dev/zz-fixture/.worktrees/w1","sessionId":"s2","requestId":"req-2","timestamp":"2026-09-02T10:00:00Z","message":{"id":"m2","model":"claude-opus-5-5","usage":{"input_tokens":10,"output_tokens":20,"cache_read_input_tokens":500}}}
{"type":"assistant","cwd":"$HOME/dev/zz-fixture","sessionId":"s3","requestId":"req-3","timestamp":"2026-09-03T10:00:00Z","message":{"id":"m3","model":"claude-fable-5-1","usage":{"input_tokens":1,"output_tokens":2,"cache_read_input_tokens":0}}}
{"type":"assistant","cwd":"/srv/elsewhere/app","sessionId":"s4","requestId":"req-4","timestamp":"2026-09-03T11:00:00Z","message":{"id":"m4","model":"claude-opus-5-5","usage":{"input_tokens":5,"output_tokens":5}}}
EOF
export CLAUDE_CONFIG_DIR="$S/cfg" CLAUDE_COSTS_LEDGER="$S/data/ledger.sqlite" CLAUDE_COSTS_STATE="$S/state" CLAUDE_COSTS_RATES="$S/rates/rates.json" CLAUDE_COSTS_PRICING_URL=http://127.0.0.1:9/none NO_COLOR=1
CC=home/dot_local/bin/executable_claude-costs
python3 $CC ingest --worker --quiet
python3 $CC | sed "s#$HOME#~#g"
python3 $CC --all | sed "s#$HOME#~#g" | sed -n 4,9p
python3 $CC --no-rollup | sed "s#$HOME#~#g" | sed -n 5,7p
python3 $CC --tokens | sed "s#$HOME#~#g" | sed -n 4,5p
python3 $CC --detail | sed "s#$HOME#~#g" | sed -n 4,14p
python3 $CC --since 2030-01-01
python3 $CC --json | python3 -c "import json,sys; d=json.load(sys.stdin); print(d['hidden_projects'], len(d['rows']), round(d['grand_total'], 6))"
python3 $CC --compact; echo "exit=$?"
python3 $CC | awk '{ if (length($0) > m) m = length($0) } END { print "max", m }'
unset CLAUDE_CONFIG_DIR CLAUDE_COSTS_LEDGER CLAUDE_COSTS_STATE CLAUDE_COSTS_RATES CLAUDE_COSTS_PRICING_URL NO_COLOR
```

Expected, in order:

1. The default report is two header lines (`claude-costs · 2026-09-01 → 2026-09-03 · 4 responses`, then `list-price equivalents, not subscription charges`), a `project` table with one row `~/dev/zz-fixture  $0.01  100.0%  3  2.1K  78.5%` (the sub-directory and the worktree folded in), a rule and `total · 1 project` with the same figures, a `model` table with `claude-opus-5-5  $0.01  98.4%  2  2.1K  78.5%` and `claude-fable-5-1  $0.00  1.6%  1  3  0.0%`, then three footer lines: `ingested <local time>`, `rates builtin (official card never)`, `1 project outside ~/dev and ~/repos hidden (--all)`. No `TOTAL BY`, no `scope:` line, no `w-5m`.
2. `--all` adds the row `/srv/elsewhere/app  $0.00  1.7%  1  10  0.0%` and the total reads `total · 2 projects … 4 … 78.3%`.
3. `--no-rollup` lists `~/dev/zz-fixture/sub/dir`, `~/dev/zz-fixture/.worktrees/w1` and `~/dev/zz-fixture` as three rows.
4. `--tokens` prints the header `project  cost  calls  input  output  cache 5m  cache 1h  cache read` and the row `~/dev/zz-fixture  $0.01  3  111  222  300  0  1.5K`.
5. `--detail` prints the block header `~/dev/zz-fixture  $0.01  100.0% · 3 calls`, its two model rows, then a `model (all projects)` table with the same two rows and the total.
6. The empty run prints exactly `no responses matched (check \`claude-costs status\`)` and exits 0.
7. The JSON line prints `1 1 0.00675`.
8. `--compact` prints `claude-costs: unknown flag --compact (see \`claude-costs help\`)` and `exit=1`.
9. `max 100`: no line of the non-terminal report exceeds 100 characters. At a terminal the name column shrinks to the window instead (`columns()`), and in a window narrower than the token columns need, names are clipped from the left with `…`.

- [ ] **Step 7: Render check and commit**

Run: `scripts/render-check.sh personal pinned && scripts/render-check.sh work latest && scripts/render-check.sh work pinned wsl` (on a WSL host against the scratch copy, as the Global Constraints say).
Expected: all pass; the script is deployed verbatim, so only its presence is rendered.

Change the status line of `docs/superpowers/specs/2026-09-30-claude-costs-ledger-design.md` to begin `Status: implemented 2026-09-30; report view of section 7 revised and implemented 2026-09-30.`

```bash
git add home/dot_local/bin/executable_claude-costs home/dot_local/share/bash-completion/completions/claude-costs docs/superpowers/specs/2026-09-30-claude-costs-ledger-design.md
git commit -m "feat(claude-costs): one line per repository, plain column names, --detail and --tokens"
```
