package machine

import (
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
		Title:       "Homebrew packages",
		Summary:     "Homebrew and the tools Workbench lists",
		What:        "Installs Homebrew if it is missing, then every formula Workbench lists that is not installed yet. Nothing is upgraded.",
		Touches:     "Homebrew's folders and the tools it installs",
		RunsAs:      "you; downloads packages, and Homebrew itself may ask for your password",
		Undo:        "Not reverted automatically: remove packages with brew uninstall",
	},
	{
		Name:        "macos-apps-extensions",
		Description: "Missing casks and VS Code extensions; apps installed outside Homebrew are left alone",
		Privilege:   "user; casks may require elevation",
		Recovery:    "external; no package rollback",
		Title:       "Mac apps and editor extensions",
		Summary:     "the apps and VS Code extensions Workbench lists",
		What:        "Installs the apps Workbench lists that are missing and the VS Code extensions you are missing. Apps you installed another way are left alone.",
		Touches:     "/Applications and your VS Code extensions",
		RunsAs:      "you; downloads apps, and some may ask for your password",
		Undo:        "Not reverted automatically: remove an app or extension by hand",
	},
	{
		Name:        "brew-maintenance",
		Description: "Refresh Homebrew's package index, reinstall tap-sourced packages.toml formulae from homebrew/core, untap unused taps, autoremove, remove old versions and the download cache",
		Privilege:   "user; network",
		Recovery:    "external; removed versions and cache are not restored",
		Title:       "Homebrew cleanup",
		Summary:     "refreshes Homebrew, moves tap packages to core and removes leftovers",
		What:        "Refreshes Homebrew's package index, so the next plan sees new versions, reinstalls formulae that came from a third-party tap from homebrew/core, removes taps nothing uses, then removes orphaned packages, old versions and the download cache.",
		Touches:     "Homebrew's package index, packages, taps and download cache",
		RunsAs:      "you; downloads packages",
		Undo:        "Not reverted: removed versions and the cache are not restored",
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
	"brew-maintenance":        {"60-cleanup"},
	"linux-packages":          {"00-packages"},
	"linux-editor-extensions": {"35-vscode-extensions"},
	"work-tools": {
		"00-packages", "50-apps-and-extensions", "35-vscode-extensions",
	},
	"bitwarden-session-cache": {".zshrc", ".bashrc"},
	"windows-files":           {"00-packages-windows", "10-deploy-windows-configs"},
	"wsl-preferences":         {"10-deploy-windows-configs"},
	"sysctl":                  {"20-sysctl"},
	"terminal-adoption":       {"00-packages-windows"},
	"powershell-adoption":     {"00-packages-windows"},
	"font-registry":           {"00-packages-windows"},
	"default-distro":          {"10-deploy-windows-configs"},
	"windows-path":            {"10-deploy-windows-configs"},
}

// activeEffects keeps the effects with an active source; see [effectSources].
func activeEffects(effects []operation.Effect, active map[string]bool) []operation.Effect {
	return slices.DeleteFunc(effects, func(effect operation.Effect) bool {
		sources, ok := effectSources[effect.Name]
		return ok &&
			!slices.ContainsFunc(sources, func(source string) bool { return active[source] })
	})
}

// aiSecurityEffect is the fixed first effect of every plan: the managed AI settings.
var aiSecurityEffect = operation.Effect{
	Name:        "ai-security-settings",
	Description: "Managed AI trust roots, approval/sandbox policy, enabled plugins and work hooks; review policy before apply",
	Privilege:   "user",
	Recovery:    "configuration files only",
	Fixed:       true,
	Title:       "AI safety settings",
	Summary:     "Claude Code and Codex stay on your approved rules",
	What:        "Keeps Claude Code and Codex on the trust, approval and sandbox rules Workbench manages. Always runs.",
	Touches:     "~/.claude/settings.json, ~/.codex/config.toml, ~/.codex/hooks.json",
	RunsAs:      "you",
	Undo:        "Restored from the checkpoint Workbench takes before applying",
}

// sharedEffects are the script owners every platform lists.
var sharedEffects = []operation.Effect{
	{
		Name:        "runtime-managers",
		Description: "Native nvm/rustup and platform-owned uv/bun/Go; user runtime directories and default links",
		Privilege:   "user; network and installer execution",
		Recovery:    "external; no package rollback",
		Title:       "Runtime managers",
		Summary:     "nvm, rustup, uv, bun and Go are in place",
		What:        "Makes sure nvm, rustup, uv, bun and Go are installed, adding whatever is missing. Go is updated to the version Workbench is set to use.",
		Touches:     "~/.nvm, ~/.cargo, ~/.bun, ~/go and ~/.local/bin",
		RunsAs:      "you; downloads the official installers",
		Undo:        "Not reverted automatically: delete the folders by hand",
	},
	{
		Name:        "runtimes",
		Description: "Native Node/Python/Rust installs and default Node alias",
		Privilege:   "user; network",
		Recovery:    "external; no runtime rollback",
		Title:       "Language runtimes",
		Summary:     "Node, Python and Rust at the versions you chose",
		What:        "Installs Node and makes it the default, installs Python through uv and Rust through rustup, at the versions Workbench is set to use.",
		Touches:     "the Node, Python and Rust installs under your home folder",
		RunsAs:      "you; downloads from the official sites",
		Undo:        "Not reverted automatically: remove a version with nvm, uv or rustup",
	},
	{
		Name:        "global-tools",
		Description: "Native npm/Corepack, uv tools, cargo and Go tools",
		Privilege:   "user; network and package build hooks",
		Recovery:    "external; no tool rollback",
		Title:       "Command-line tools",
		Summary:     "Claude Code, Codex and the tools Workbench lists",
		What:        "Installs the global npm, uv, cargo and Go tools Workbench lists, including Claude Code and Codex, and podman on Linux. Their install steps run.",
		Touches:     "the global tool folders under your home folder",
		RunsAs:      "you; downloads packages and runs their install steps; podman uses sudo",
		Undo:        "Not reverted automatically: uninstall a tool with npm, uv, cargo or go",
	},
	{
		Name:        "tmux-plugins",
		Description: "TPM and tmux plugin installation under ~/.tmux/plugins",
		Privilege:   "user; network and executable plugin hooks",
		Recovery:    "external; no plugin rollback",
		Title:       "tmux plugins",
		Summary:     "the tmux plugin manager and its plugins",
		What:        "Installs the tmux plugin manager if it is missing, then the plugins your tmux.conf lists.",
		Touches:     "~/.tmux/plugins",
		RunsAs:      "you; downloads plugins and runs their install steps",
		Undo:        "Delete ~/.tmux/plugins",
	},
	{
		Name:        "costs-ingest",
		Description: "The installed workbench reads new Claude Code and Codex usage into the costs ledger, as its hooks do",
		Privilege:   "user; network for the official price pages",
		Recovery:    "external; ledger rows are derived from the transcripts and not reverted",
		Title:       "Record AI usage",
		Summary:     "new usage shows up in workbench costs",
		What:        "Reads your latest Claude Code and Codex sessions into the costs ledger, the same thing the session hooks do.",
		Touches:     "~/.local/share/claude-costs/ledger.sqlite",
		RunsAs:      "you; downloads the official price pages",
		Undo:        "Nothing to undo: the ledger is rebuilt from the session files",
	},
}

// linuxPackagesEffect is the apt bootstrap of Linux and WSL.
var linuxPackagesEffect = operation.Effect{
	Name:        "linux-packages",
	Description: "apt base prerequisites/bat/fd/tmux/podman, ~/.local/bin, themes, fonts and dev/repos directories",
	Privilege:   "sudo; network and font-cache effects",
	Recovery:    "packages external; written files/links require checkpoints",
	Title:       "System packages",
	Summary:     "apt tools, fonts and your dev folders",
	What:        "Installs a C toolchain, bat, fd and tmux with apt, the GitHub CLI, the oh-my-posh prompt and its themes and the Nerd Font, and creates your ~/dev and ~/repos folders.",
	Touches:     "system packages, ~/.local/bin, ~/.local/share/fonts, ~/dev and ~/repos",
	RunsAs:      "you, with sudo; downloads packages",
	Undo:        "Not reverted automatically: remove packages with apt",
}

// linuxEditorEffect installs the editor extensions on a Linux desktop. WSL does not
// plan it: its editor is the Windows desktop, which no step here installs into,
// so the effect could never do anything.
var linuxEditorEffect = operation.Effect{
	Name:        "linux-editor-extensions",
	Description: "Canonical extensions installed only into a verified local VS Code desktop default profile; unavailable or ambiguous editor hosts block this effect",
	Privilege:   "user; marketplace network and extension code",
	Recovery:    "external; extensions are not reverted",
	Title:       "VS Code extensions",
	Summary:     "your extension set in the local editor",
	What:        "Installs the VS Code extensions Workbench lists into the desktop VS Code on this computer, only when it is a normal local install.",
	Touches:     "~/.vscode/extensions",
	RunsAs:      "you; downloads from the marketplace and runs extension code",
	Undo:        "Not reverted automatically: uninstall an extension in VS Code",
}

// workToolsEffect is the role-gated work tooling. It carries only what its
// scripts can switch off: the shell setup for Bitwarden is a file, which always
// applies, and has its own fixed effect.
var workToolsEffect = operation.Effect{
	Name:        "work-tools",
	Description: "Bitwarden CLI and work editor extension",
	Privilege:   "user; network",
	Recovery:    "external; no package rollback",
	Title:       "Work tools",
	Summary:     "Bitwarden CLI and the work editor extension",
	What:        "Installs the Bitwarden command-line tool and the work VS Code extension.",
	Touches:     "the Bitwarden CLI and your VS Code extensions",
	RunsAs:      "you; downloads from the vendor",
	Undo:        "Not reverted automatically: uninstall the tools by hand",
}

// bitwardenSessionEffect is the fixed, role-gated disclosure of the Bitwarden
// shell policy. The policy is text in the shell startup files, which apply with
// every other file and cannot be switched off by a step, so it is listed like
// the AI safety settings: always, with no box to untick.
var bitwardenSessionEffect = operation.Effect{
	Name:        "bitwarden-session-cache",
	Description: "Shell startup files define a bw wrapper that caches the Bitwarden session key in a 0600 file every new shell reads; review policy before apply",
	Privilege:   "user",
	Recovery:    "configuration files only",
	Fixed:       true,
	Title:       "Bitwarden session caching",
	Summary:     "bw unlock keeps your vault open for new shells",
	What:        "Keeps a bw command in your shell startup files that, after bw unlock, saves the Bitwarden session key in a private file (mode 0600) every new shell reads, until bw lock removes it. While it is saved, anyone using your account can read the vault. Always runs.",
	Touches:     "~/.zshrc, ~/.bashrc, ~/.config/Bitwarden CLI/session",
	RunsAs:      "you",
	Undo:        "Restored from the checkpoint Workbench takes before applying",
}

// windowsHostEffects are the Windows-side owners a WSL host lists. Windows
// setup is optional, off until ticked: it installs programs and fonts and edits
// settings on the Windows side, outside the checkpoint and with no copy kept,
// so a first apply, or --yes on a machine that never decided, must not do that
// unasked. WSL networking stays on: it keeps the other .wslconfig settings and
// saves a copy of the file first.
var windowsHostEffects = []operation.Effect{
	{
		Name:        "windows-files",
		Optional:    true,
		Description: "Discovered Windows home/AppData: oh-my-posh binary/themes, fonts, .wslconfig, RestartWSL helpers, bin/rg.exe, VS Code User settings and Notepad++ themes; ~/.vscode-server/data/Machine/settings.json",
		Privilege:   "Windows user; not yet qualified on a real Windows host",
		Recovery:    "script writes are not checkpointed; acquired executables are external",
		Title:       "Windows setup",
		Summary:     "theme, fonts and VS Code settings on the Windows side",
		What:        "Puts the Windows side of your setup in place: the oh-my-posh theme, JetBrains Mono fonts, ripgrep, VS Code settings, Notepad++ themes and the RestartWSL helpers. Your Windows Terminal settings, PowerShell profile and font registration are separate optional steps; each stays off until you turn it on.",
		Touches:     "your Windows home and AppData folders, and ~/.vscode-server's machine settings",
		RunsAs:      "you, on Windows",
		Undo:        "Not checkpointed: Windows files are outside Workbench's restore",
	},
	{
		Name:        "wsl-preferences",
		Description: ".wslconfig affects VM sizing/networking at future startup; restart helpers are installed but never executed",
		Privilege:   "Windows user",
		Recovery:    "configuration only; running VM state is external",
		Title:       "WSL networking",
		Summary:     "Windows and WSL share localhost (mirrored)",
		What:        "Sets WSL networking to mirrored, so a server started in WSL is reachable from Windows at localhost and the other way round, and applies your memory, swap and processor sizing. Your other .wslconfig settings are kept and a copy of the file is saved first. Takes effect the next time WSL starts.",
		Touches:     "%USERPROFILE%\\.wslconfig",
		RunsAs:      "you, on Windows",
		Undo:        "Restore the copy next to .wslconfig; nothing restarts on its own",
	},
}

// provisioningEffects describes canonical script owners, not simulated provider
// results. Optional effects are deliberately absent until separately selected.
func provisioningEffects(answers Answers) []operation.Effect {
	effects := slices.Clone(sharedEffects)
	if runtime.GOOS == "darwin" {
		effects = append(effects, macOSEffects...)
	} else {
		effects = append(effects, linuxPackagesEffect)
		if answers["is_wsl"] != true {
			effects = append(effects, linuxEditorEffect)
		}
	}
	if answers["has_work"] == true {
		effects = append(effects, workToolsEffect, bitwardenSessionEffect)
	}
	if answers["is_wsl"] == true {
		effects = append(effects, windowsHostEffects...)
	}
	return effects
}

// optionalEffects are the WSL host steps, which run only when selected by name.
// The owning script checks WORKBENCH_EFFECT_<NAME>; see [effectVariable].
var optionalEffects = []operation.Effect{
	{
		Name:        "terminal-adoption",
		Optional:    true,
		Description: "Replace an existing Windows Terminal settings.json with the managed one when it differs; a copy is kept beside it; register the font it uses",
		Privilege:   "Windows user",
		Recovery:    "copy only; not checkpointed",
		Title:       "Replace Windows Terminal settings",
		Summary:     "replaces yours; a copy is kept",
		What:        "Replaces your Windows Terminal settings.json with the managed one, when it differs. A copy of yours is kept next to it. Installs and registers the font it uses, if that is not done yet.",
		Touches:     "Windows Terminal settings.json and your Windows font registry",
		RunsAs:      "you, on Windows",
		Undo:        "Restore the copy by hand",
	},
	{
		Name:        "powershell-adoption",
		Optional:    true,
		Description: "Replace an existing PowerShell profile with the managed one; a copy is kept beside it",
		Privilege:   "Windows user",
		Recovery:    "copy only; not checkpointed",
		Title:       "Replace PowerShell profile",
		Summary:     "replaces yours; a copy is kept",
		What:        "Replaces your PowerShell profile with the managed one. A copy of yours is kept next to it. Installs oh-my-posh, which the profile starts, if it is missing.",
		Touches:     "your PowerShell profile",
		RunsAs:      "you, on Windows",
		Undo:        "Restore the copy by hand",
	},
	{
		Name:        "font-registry",
		Optional:    true,
		Description: "Register JetBrainsMono Nerd Font files in HKCU Fonts and load them into the session",
		Privilege:   "Windows user registry",
		Recovery:    "external; registry values are not reverted",
		Title:       "Install fonts for Windows apps",
		Summary:     "every Windows app can use JetBrains Mono",
		What:        "Registers the JetBrains Mono Nerd Font files for your Windows user and loads them now, so apps see them without signing out. Downloads the font files first if they are missing.",
		Touches:     "your Windows font registry",
		RunsAs:      "you, on Windows",
		Undo:        "Not reverted automatically",
	},
	{
		Name:        "default-distro",
		Optional:    true,
		Description: "Make this distribution the default WSL distribution",
		Privilege:   "Windows user",
		Recovery:    "external; previous default is not restored",
		Title:       "Make this distribution your default",
		Summary:     "plain wsl opens this distribution",
		What:        "Makes this distribution the one `wsl` and new Windows Terminal tabs open by default.",
		Touches:     "WSL settings",
		RunsAs:      "you, on Windows",
		Undo:        "wsl --set-default <name>",
	},
	{
		Name:        "windows-path",
		Optional:    true,
		Description: "Append %USERPROFILE%\\bin and %USERPROFILE%\\.local\\bin to the Windows user PATH, keeping existing entries",
		Privilege:   "Windows user environment",
		Recovery:    "external; PATH is not reverted",
		Title:       "Add Windows bin folders to PATH",
		Summary:     "Windows finds the tools Workbench puts there",
		What:        "Adds %USERPROFILE%\\bin and %USERPROFILE%\\.local\\bin to your Windows user PATH, after what is already there.",
		Touches:     "your Windows user PATH",
		RunsAs:      "you, on Windows",
		Undo:        "Remove the two entries in Environment Variables",
	},
	{
		Name:        "sysctl",
		Optional:    true,
		Description: "Write /etc/sysctl.d/99-dev.conf with vm.swappiness=10 and apply it live",
		Privilege:   "sudo",
		Recovery:    "external; kernel setting and file are not reverted",
		Title:       "Lower swapping",
		Summary:     "keeps programs in memory; swaps only under real pressure",
		What:        "Sets vm.swappiness to 10, so idle programs stay in memory on this large-memory WSL machine, and keeps it across restarts.",
		Touches:     "/etc/sysctl.d/99-dev.conf",
		RunsAs:      "you, with sudo",
		Undo:        "Delete /etc/sysctl.d/99-dev.conf and restart WSL",
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

// staleSelectionWarnings names each saved select this host does not offer: a
// choice saved before an optional step was renamed or removed, or on another
// platform. It is ignored, not refused, so one stale name never blocks a plan.
// The warnings follow the saved file, not the selection being planned, so a
// plan and its recheck under the locks read them alike.
func staleSelectionWarnings(saved Selection) []string {
	available := optionalNames()
	var warnings []string
	for _, name := range saved.Select {
		if !slices.Contains(available, name) {
			warnings = append(warnings, "Saved choice "+name+" is not offered here and is "+
				"ignored; workbench apply --reset forgets it")
		}
	}
	return warnings
}

// hostOptionalEffects lists every optional effect this host offers, naming
// the distribution for default-distro so consent never covers an implicit
// choice. Every one is its own choice, off until selected by name: none is
// ticked by another effect.
func hostOptionalEffects() []operation.Effect {
	if !isWSL() {
		return nil
	}
	effects := slices.Clone(optionalEffects)
	for i := range effects {
		if effects[i].Name == "default-distro" {
			if distribution := os.Getenv("WSL_DISTRO_NAME"); distribution != "" {
				effects[i].Description = "Make " + distribution + " the default WSL distribution"
				effects[i].Title = "Make " + distribution + " your default"
			}
		}
	}
	return effects
}

// applySelection marks each effect checked or not, and which ones are new. See
// [gate] for the checks. New marks each non-fixed effect the owner has not yet
// had the chance to decide: not in selection.Decided, skip or select. It is only
// set once a decided list exists, since before that nothing was ever decided and
// "new" would mean everything. New is display only.
func applySelection(effects []operation.Effect, selection, saved Selection) []operation.Effect {
	for i := range effects {
		effect := &effects[i]
		effect.New = !effect.Fixed && !selection.NeverDecided() &&
			!slices.Contains(selection.Decided, effect.Name) &&
			!slices.Contains(selection.Skip, effect.Name) &&
			!slices.Contains(selection.Select, effect.Name)
	}
	return gate(effects, selection, saved)
}

// gate marks each effect checked or not: fixed effects always, optional effects
// only when selected, every other effect unless skipped. No effect is checked
// because another one is: an optional effect that replaces the owner's own
// files, such as the Windows Terminal settings, runs only when selected by name.
// SavedSkip tells the checklist which skips came from machine.toml.
func gate(effects []operation.Effect, selection, saved Selection) []operation.Effect {
	for i := range effects {
		effect := &effects[i]
		switch {
		case effect.Fixed:
			effect.Checked, effect.SavedSkip = true, false
		case effect.Optional:
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
// Each effect keeps its New mark: selection comes from [SelectionOf] and does
// not know what was decided.
func Reselect(plan operation.Plan, selection, saved Selection) operation.Plan {
	plan.Effects = gate(slices.Clone(plan.Effects), selection, saved)
	return plan
}

// SelectionOf is the selection a checklist produced: skipped default
// effects and selected optional ones. It does not carry Decided.
func SelectionOf(effects []operation.Effect) Selection {
	var selection Selection
	for _, effect := range effects {
		switch {
		case effect.Fixed:
		case effect.Optional:
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
// checklist chose, every non-fixed effect the owner could see as decided, plus
// the saved skips, selects and decisions for effects this plan does not list,
// such as another platform's, which are ignored here and kept. An effect with
// nothing to change and no saved skip sits on the Already set line, where it
// cannot be turned off, so it is not recorded as decided unless it already was:
// it is asked about, marked new, the first time it has something to do.
// effects is the plan as applied and chosen the selection that produced it.
// With --reset (chosen.Forget) the earlier decided list is forgotten for the
// effects listed, and so are saved selects for effects this host does not offer,
// which the plan warns about; unlisted skips and decisions are kept. The decided
// list is always non-nil: once saved, the owner has decided.
func selectionToSave(effects []operation.Effect, chosen, saved Selection) Selection {
	selection := SelectionOf(effects)
	plan := operation.Plan{Effects: effects}
	listed := func(name string) bool {
		return slices.ContainsFunc(effects, func(effect operation.Effect) bool {
			return effect.Name == name
		})
	}
	selection.Decided = []string{}
	for _, effect := range effects {
		if !effect.Fixed &&
			((!chosen.Forget && slices.Contains(saved.Decided, effect.Name)) || !plan.AlreadySet(effect)) {
			selection.Decided = append(selection.Decided, effect.Name)
		}
	}
	for _, name := range saved.Skip {
		if !listed(name) {
			selection.Skip = append(selection.Skip, name)
		}
	}
	for _, name := range saved.Select {
		if !listed(name) && !chosen.Forget {
			selection.Select = append(selection.Select, name)
		}
	}
	for _, name := range saved.Decided {
		if !listed(name) {
			selection.Decided = append(selection.Decided, name)
		}
	}
	slices.Sort(selection.Skip)
	slices.Sort(selection.Select)
	slices.Sort(selection.Decided)
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
