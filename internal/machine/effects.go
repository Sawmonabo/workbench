package machine

import (
	"cmp"
	"os"
	"runtime"
	"slices"
	"strings"

	"github.com/Sawmonabo/workbench/internal/operation"
)

// macOSEffects are the macOS-only script owners of a full apply.
var macOSEffects = []operation.Effect{
	{
		Name:        "macos-packages",
		Description: "Homebrew/formulae and ~/.config/oh-my-posh; already selected chezmoi/uv owners retained",
		Privilege:   "user; initial Homebrew/CLT may require elevation",
		Recovery:    "package effects external; created files require checkpoints",
	},
	{
		Name:        "macos-apps-extensions",
		Description: "Missing casks and VS Code extensions; apps installed outside Homebrew are left alone",
		Privilege:   "user; casks may require elevation",
		Recovery:    "external; no package rollback",
	},
	{
		Name:        "terminal-font",
		Description: "Native managed ~/.terminal-font-setup.sh provides instructions; Terminal font selection is a separate manual step",
		Privilege:   "none during provisioning",
		Recovery:    "helper checkpointed; manual UI external",
	},
	{
		Name:        "brew-maintenance",
		Description: "Reinstall tap-sourced packages.toml formulae from homebrew/core, untap unused taps, autoremove, remove old versions and the download cache",
		Privilege:   "user; network",
		Recovery:    "external; removed versions and cache are not restored",
	},
}

// effectSources names what each effect covers: the scripts that carry it out,
// by name without chezmoi's prefixes and .sh suffix, and the files it manages,
// relative to the destination. A plan lists an effect only when one of them is
// active; an effect without an entry is always listed.
var effectSources = map[string][]string{
	"ai-security-settings":    {".claude/settings.json", ".codex/config.toml", ".codex/hooks.json"},
	"runtime-managers":        {"10-runtime-managers"},
	"runtimes":                {"20-runtimes"},
	"global-tools":            {"30-global-tools"},
	"tmux-plugins":            {"40-tmux-plugins"},
	"costs-ingest":            {"90-costs-ingest"},
	"macos-packages":          {"00-packages"},
	"macos-apps-extensions":   {"50-apps-and-extensions"},
	"terminal-font":           {".terminal-font-setup.sh"},
	"brew-maintenance":        {"60-cleanup"},
	"linux-packages":          {"00-packages"},
	"linux-editor-extensions": {"35-vscode-extensions"},
	"work-tools": {
		"00-packages", "50-apps-and-extensions", "35-vscode-extensions", ".zshrc", ".bashrc",
	},
	"windows-files":       {"00-packages-windows", "10-deploy-windows-configs"},
	"wsl-preferences":     {"10-deploy-windows-configs"},
	"sysctl":              {"20-sysctl"},
	"terminal-adoption":   {"00-packages-windows"},
	"powershell-adoption": {"00-packages-windows"},
	"font-registry":       {"00-packages-windows"},
	"default-distro":      {"10-deploy-windows-configs"},
	"windows-path":        {"10-deploy-windows-configs"},
}

// activeEffects keeps the effects with an active source; see [effectSources].
func activeEffects(effects []operation.Effect, active map[string]bool) []operation.Effect {
	return slices.DeleteFunc(effects, func(effect operation.Effect) bool {
		sources, ok := effectSources[effect.Name]
		return ok &&
			!slices.ContainsFunc(sources, func(source string) bool { return active[source] })
	})
}

// provisioningEffects describes canonical script owners, not simulated provider
// results. Optional effects are deliberately absent until separately selected.
func provisioningEffects(answers Answers) []operation.Effect {
	effects := []operation.Effect{
		{
			Name:        "runtime-managers",
			Description: "Native nvm/rustup and platform-owned uv/bun/Go; user runtime directories and default links",
			Privilege:   "user; network and installer execution",
			Recovery:    "external; no package rollback",
		},
		{
			Name:        "runtimes",
			Description: "Native Node/Python/Rust installs and default Node alias",
			Privilege:   "user; network",
			Recovery:    "external; no runtime rollback",
		},
		{
			Name:        "global-tools",
			Description: "Native npm/Corepack, uv tools, cargo and Go tools",
			Privilege:   "user; network and package build hooks",
			Recovery:    "external; no tool rollback",
		},
		{
			Name:        "tmux-plugins",
			Description: "TPM and tmux plugin installation under ~/.tmux/plugins",
			Privilege:   "user; network and executable plugin hooks",
			Recovery:    "external; no plugin rollback",
		},
		{
			Name:        "costs-ingest",
			Description: "The installed workbench reads new Claude Code and Codex usage into the costs ledger, as its hooks do",
			Privilege:   "user; network for the official price pages",
			Recovery:    "external; ledger rows are derived from the transcripts and not reverted",
		},
	}
	if runtime.GOOS == "darwin" {
		effects = append(effects, macOSEffects...)
	} else {
		effects = append(
			effects,
			operation.Effect{
				Name:        "linux-packages",
				Description: "apt base prerequisites/bat/fd/tmux/podman, ~/.local/bin, themes, fonts and dev/repos directories",
				Privilege:   "sudo; network and font-cache effects",
				Recovery:    "packages external; written files/links require checkpoints",
			},
		)
		effects = append(
			effects,
			operation.Effect{
				Name:        "linux-editor-extensions",
				Description: "Canonical extensions installed only into a verified local VS Code desktop default profile; unavailable or ambiguous editor hosts block this effect",
				Privilege:   "user; marketplace network and extension code",
				Recovery:    "external; extensions are not reverted",
			},
		)
	}
	if answers["has_work"] == true {
		effects = append(
			effects,
			operation.Effect{
				Name:        "work-tools",
				Description: "Bitwarden CLI and work editor extension; existing Bitwarden session-caching shell policy",
				Privilege:   "user; network",
				Recovery:    "packages external; shell configuration checkpointed",
			},
		)
	}
	if answers["is_wsl"] == true {
		effects = append(
			effects,
			operation.Effect{
				Name:        "windows-files",
				Description: "Discovered Windows home/AppData: oh-my-posh binary/themes, fonts, Terminal settings, actual PowerShell profile, .wslconfig, RestartWSL helpers, bin/rg.exe, VS Code User settings and Notepad++ themes; ~/.vscode-server/data/Machine/settings.json",
				Privilege:   "Windows user; not yet qualified on a real Windows host",
				Recovery:    "script writes are not checkpointed; acquired executables are external",
			},
			operation.Effect{
				Name:        "wsl-preferences",
				Description: ".wslconfig affects VM sizing/networking at future startup; restart helpers are installed but never executed",
				Privilege:   "Windows user",
				Recovery:    "configuration only; running VM state is external",
			},
		)
	}
	return effects
}

// optionalEffects are the WSL host steps, which run only when selected by name.
// The owning script checks WORKBENCH_EFFECT_<NAME>; see [effectVariable].
var optionalEffects = []operation.Effect{
	{
		Name:        "terminal-adoption",
		Description: "Replace an existing Windows Terminal settings.json with the managed one; a dated copy is kept beside it",
		Privilege:   "Windows user",
		Recovery:    "dated copy only; not checkpointed",
	},
	{
		Name:        "powershell-adoption",
		Description: "Replace an existing PowerShell profile with the managed one; a copy is kept beside it",
		Privilege:   "Windows user",
		Recovery:    "copy only; not checkpointed",
	},
	{
		Name:        "font-registry",
		Description: "Register JetBrainsMono Nerd Font files in HKCU Fonts and load them into the session",
		Privilege:   "Windows user registry",
		Recovery:    "external; registry values are not reverted",
	},
	{
		Name:        "default-distro",
		Description: "Make this distribution the default WSL distribution",
		Privilege:   "Windows user",
		Recovery:    "external; previous default is not restored",
	},
	{
		Name:        "windows-path",
		Description: "Append %USERPROFILE%\\bin and %USERPROFILE%\\.local\\bin to the Windows user PATH, keeping existing entries",
		Privilege:   "Windows user environment",
		Recovery:    "external; PATH is not reverted",
	},
	{
		Name:        "sysctl",
		Description: "Write /etc/sysctl.d/99-dev.conf with vm.swappiness=10 and apply it live",
		Privilege:   "sudo",
		Recovery:    "external; kernel setting and file are not reverted",
	},
}

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
	if slices.Contains(selection.Select, "default-distro") && os.Getenv("WSL_DISTRO_NAME") == "" {
		return operation.Fail(
			operation.ExitBlocked,
			"effect",
			"default-distro requires WSL_DISTRO_NAME from a WSL session",
		)
	}
	for _, name := range selection.Select {
		if !slices.Contains(available, name) {
			return operation.Fail(
				operation.ExitInvalid,
				"effect",
				"Unknown or unavailable effect "+name+"; available here: "+cmp.Or(
					AvailableEffects(),
					"none",
				),
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
	// The Windows adoptions and the font registry act on files that
	// windows-files writes; without it they have nothing to do.
	filesChecked := slices.ContainsFunc(effects, func(effect operation.Effect) bool {
		return effect.Name == "windows-files" && effect.Checked
	})
	for i := range effects {
		if !slices.Contains(windowsFileEffects, effects[i].Name) {
			continue
		}
		effects[i].Delta = needsFilesDelta(effects[i].Delta, filesChecked)
		if !filesChecked {
			effects[i].Checked = false
		}
	}
	return effects
}

// needsFilesNote prefixes the probed delta of an effect that cannot run
// without windows-files, and only while windows-files is unchecked.
const needsFilesNote = "needs windows-files"

// needsFilesDelta adds the note to delta when windows-files is unchecked and
// removes it when checked, so a probed delta survives toggling.
func needsFilesDelta(delta string, filesChecked bool) string {
	rest := delta
	if rest == needsFilesNote {
		rest = ""
	} else if after, ok := strings.CutPrefix(rest, needsFilesNote+"; "); ok {
		rest = after
	}
	switch {
	case filesChecked:
		return rest
	case rest == "":
		return needsFilesNote
	}
	return needsFilesNote + "; " + rest
}

// windowsFileEffects are the optional effects that act on windows-files' output.
var windowsFileEffects = []string{"terminal-adoption", "powershell-adoption", "font-registry"}

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

// selectionToSave is the selection an approved apply remembers: what the
// checklist chose, plus the saved skips and selects for effects this plan does
// not list, such as another platform's, which are ignored here and kept.
func selectionToSave(effects []operation.Effect, saved Selection) Selection {
	selection := SelectionOf(effects)
	listed := func(name string) bool {
		return slices.ContainsFunc(effects, func(effect operation.Effect) bool {
			return effect.Name == name
		})
	}
	for _, name := range saved.Skip {
		if !listed(name) {
			selection.Skip = append(selection.Skip, name)
		}
	}
	for _, name := range saved.Select {
		if !listed(name) {
			selection.Select = append(selection.Select, name)
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

// effectVariable is the script switch for an optional effect, for example
// WORKBENCH_EFFECT_DEFAULT_DISTRO for default-distro.
func effectVariable(name string) string {
	return "WORKBENCH_EFFECT_" + strings.ToUpper(strings.ReplaceAll(name, "-", "_"))
}
