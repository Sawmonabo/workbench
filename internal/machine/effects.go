package machine

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/Sawmonabo/workbench/internal/operation"
	"github.com/Sawmonabo/workbench/internal/release"
)

// macOSEffects are the macOS-only script owners of a full apply.
var macOSEffects = []operation.Effect{
	{
		Name:        "macos-packages",
		Description: "Homebrew/formulae and ~/.config/oh-my-posh; already selected chezmoi/uv owners retained",
		Privilege:   "user; installing Homebrew uses sudo",
		Recovery:    "package effects external; created files require checkpoints",
		Title:       "Homebrew packages",
		Summary:     "Homebrew and the tools Workbench lists",
		What:        "Installs Homebrew if it is missing and each listed formula not installed yet, upgrading nothing.",
		Touches:     "Homebrew's folders and the tools it installs",
		RunsAs:      "you, downloading packages, with administrator rights to install a missing Homebrew",
		Undo:        "Not reverted automatically: remove packages with brew uninstall",
	},
	{
		Name:        "macos-apps-extensions",
		Description: "Missing casks and VS Code extensions; apps installed outside Homebrew are left alone",
		Privilege:   "user; casks may require elevation",
		Recovery:    "external; no package rollback",
		Title:       "Mac apps and editor extensions",
		Summary:     "the apps and VS Code extensions Workbench lists",
		What:        "Installs the listed apps and VS Code extensions that are missing, and leaves apps installed another way.",
		Touches:     "/Applications and your VS Code extensions",
		RunsAs:      "you, downloading apps, with administrator rights for some, such as Docker Desktop",
		Undo:        "Not reverted automatically: remove an app or extension by hand",
	},
	{
		Name:        "brew-maintenance",
		Description: "brew update, tap formulae moved to homebrew/core, unused taps untapped, autoremove and cleanup",
		Privilege:   "user; network",
		Recovery:    "external; removed versions and cache are not restored",
		Title:       "Homebrew cleanup",
		Summary:     "refreshes Homebrew, moves tap packages to core and removes leftovers",
		What:        "Refreshes Homebrew, moves tap formulae to homebrew/core and removes what nothing uses.",
		Touches:     "Homebrew's index and taps, orphaned packages, old versions and the download cache",
		RunsAs:      "you; downloads packages",
		Undo:        "Not reverted: removed versions and the cache are not restored",
	},
}

// effectSources names what each effect covers: the scripts that carry it out,
// by name without chezmoi's prefixes and .sh suffix, and the files it manages,
// relative to the destination. A plan lists an effect only when one of them is
// active, or when a script of its own that chezmoi would not run has something
// to do (see [listDue]); an effect without an entry is always listed.
var effectSources = map[string][]string{
	"ai-security-settings":    {".claude/settings.json", ".codex/config.toml", ".codex/hooks.json"},
	"runtime-managers":        {"10-runtime-managers"},
	"runtimes":                {"20-runtimes"},
	"global-tools":            {"30-global-tools"},
	"tmux-plugins":            {"40-tmux-plugins"},
	"agent-costs-ingest":      {"90-agent-costs-ingest"},
	"migrations":              {"05-migrations"},
	"env-files":               {"06-env-files"},
	"claude-mcp":              {"46-claude-mcp"},
	"macos-packages":          {"00-packages"},
	"macos-apps-extensions":   {"50-apps-and-extensions"},
	"brew-maintenance":        {"60-cleanup"},
	"linux-packages":          {"00-packages"},
	"epel":                    {"00-packages"},
	"linux-editor-extensions": {"35-vscode-extensions"},
	"work-tools":              {"50-apps-and-extensions", "35-vscode-extensions"},
	"windows-files":           {"00-packages-windows", "10-deploy-windows-configs"},
	"wsl-preferences":         {"10-deploy-windows-configs"},
	"sysctl":                  {"20-sysctl"},
	"touch-id-sudo":           {"70-touch-id-sudo"},
	"mbx":                     {"32-mbx"},
	"terminal-adoption":       {"00-packages-windows"},
	"powershell-adoption":     {"00-packages-windows"},
	"font-registry":           {"00-packages-windows"},
	"default-distro":          {"10-deploy-windows-configs"},
	"windows-path":            {"10-deploy-windows-configs"},
	// A take-over runs its step's script even with the step itself unticked.
	takeOverPrefix + "terminal-adoption":   {"00-packages-windows"},
	takeOverPrefix + "powershell-adoption": {"00-packages-windows"},
	takeOverPrefix + "wsl-preferences":     {"10-deploy-windows-configs"},
	takeOverPrefix + "default-distro":      {"10-deploy-windows-configs"},
}

// checkMbx is doctor's line for the optional Rust build cache step: why it is
// not offered where mbx publishes no build for this target, else the mbx in
// ~/.local/bin against the pin. It reads the version mbx reports and nothing
// else; mbx setup --status, which checks the shim, is left to the step.
func checkMbx(
	ctx context.Context,
	c operation.Context,
	pin MbxPin,
	selected []string,
) operation.Component {
	line := operation.Component{Name: "mbx", Status: operation.StatusSkipped}
	if pin.SHA256[release.Target()] == "" {
		line.Message = "the Rust build cache step is not offered"
		build := "mbx publishes no build for " + release.Target()
		if release.Target() == "darwin-amd64" {
			build = "mbx publishes no Intel Mac build"
		}
		line.Body = operation.Explain(operation.Cause(build))
		return line
	}
	ticked := slices.Contains(selected, "mbx")
	path := filepath.Join(c.Home, ".local", "bin", "mbx")
	if _, err := os.Stat(path); err != nil {
		line.Message = "not installed"
		line.Body = operation.Explain(
			operation.Cause("the optional Rust build cache step installs it, once ticked"),
			operation.Fix("workbench", "apply", "--choose"),
		)
		if ticked {
			line.Status = operation.StatusAbsent
			line.Body = operation.Explain(
				operation.Cause("Rust build cache is ticked, and the next apply installs it"),
				operation.Fix("workbench", "apply"),
			)
		}
		return line
	}
	have, _ := probeVersion(ctx, c, "mbx", path)
	line.Status = operation.StatusComplete
	switch {
	case have == "":
		line.Message = "in ~/.local/bin; its version cannot be read"
	case ticked && newerDotted(pin.Version, have):
		line.Status = operation.StatusPending
		line.Message = "mbx " + have + " in ~/.local/bin (pinned " + pin.Version + ")"
		line.Body = operation.Explain(operation.Fix("workbench", "apply"))
	default:
		line.Message = "mbx " + have + " in ~/.local/bin (pinned " + pin.Version + ")"
	}
	return line
}

// activeEffects keeps the effects with an active source, and the effects
// carried by a script chezmoi would not run (a run-once script that ran, or an
// on-change script whose content is unchanged), which it names in p.dormant:
// the plan probes those scripts too, and [listDue] keeps such an effect only
// when one of its scripts has something to do. See [effectSources].
func (p *preparation) activeEffects(effects []operation.Effect) []operation.Effect {
	p.dormant = map[string]bool{}
	return slices.DeleteFunc(effects, func(effect operation.Effect) bool {
		sources, ok := effectSources[effect.Name]
		if !ok ||
			slices.ContainsFunc(sources, func(source string) bool { return p.active[source] }) {
			return false
		}
		if slices.ContainsFunc(sources, isScript) {
			p.dormant[effect.Name] = true
			return false
		}
		return true
	})
}

// isScript reports that an [effectSources] entry names a script, not a file.
func isScript(source string) bool { return !strings.HasPrefix(source, ".") }

// listDue drops each dormant effect (see [activeEffects]) none of whose
// scripts has something to do by the probe findings, which are the effects of
// the plan that probed. A script that has something to do keeps every effect
// it carries, so each one's switch reaches it and an unticked one stays off.
func listDue(
	effects []operation.Effect,
	dormant map[string]bool,
	findings []operation.Effect,
) []operation.Effect {
	due := map[string]bool{}
	for _, finding := range findings {
		if hasWork(finding) {
			for _, source := range effectSources[finding.Name] {
				due[source] = true
			}
		}
	}
	return slices.DeleteFunc(effects, func(effect operation.Effect) bool {
		return dormant[effect.Name] &&
			!slices.ContainsFunc(effectSources[effect.Name], func(source string) bool {
				return due[source]
			})
	})
}

// hasWork reports that every script carrying the effect answered its probe
// and at least one named something to do.
func hasWork(effect operation.Effect) bool {
	return effect.Probe == "ok" && !effect.NoChange && effect.Delta != ""
}

// aiSecurityEffect is the fixed first effect of every plan: the managed AI settings.
var aiSecurityEffect = operation.Effect{
	Name:        "ai-security-settings",
	Description: "Managed AI trust roots, approval and sandbox policy, enabled plugins and work hooks",
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
		Name:        "migrations",
		Description: "The installed workbench migrates the files earlier releases wrote, never other tools' files",
		Privilege:   "user",
		Recovery:    "external; a moved file or token can be moved back by hand",
		Title:       "Move Workbench's files",
		Summary:     "moves files earlier releases left, and saved work tokens into work.env",
		What:        "Moves files earlier releases left into Workbench's folders, and saved work tokens into work.env.",
		Touches:     "the old costs folders, the old caches it deletes, the guard log, machine.toml and work.env",
		RunsAs:      "you",
		Undo:        "Not reverted automatically: moves can be undone by hand, deleted caches and log lines cannot",
	},
	{
		Name:        "env-files",
		Description: "The installed workbench creates each missing role env file, mode 0600, and never writes one that is there",
		Privilege:   "user",
		Recovery:    "external; a created file is not checkpointed",
		Title:       "Credential files",
		Summary:     "work.env and personal.env, where your credentials go",
		What:        "Creates work.env and personal.env for your roles when missing, readable by you alone, for you to fill in.",
		Touches:     "work.env and personal.env in Workbench's config folder",
		RunsAs:      "you",
		Undo:        "Delete the file by hand; it is not checkpointed",
	},
	{
		Name:        "runtime-managers",
		Description: "Native nvm/rustup and platform-owned uv/bun/Go; user runtime directories and default links",
		Privilege:   "user; network and installer execution",
		Recovery:    "external; no package rollback",
		Title:       "Runtime managers",
		Summary:     "nvm, rustup, uv, bun and Go are in place",
		What:        "Installs whichever of nvm, rustup, uv, bun and Go is missing, and updates Go to the version Workbench uses.",
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
		What:        "Installs Node (the default), Python through uv and Rust through rustup, at the versions Workbench uses.",
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
		What:        "Installs the global npm, uv, cargo and Go tools Workbench lists, and Claude Code and Codex when missing.",
		Touches:     "the global tool folders under your home folder, at the versions your mode sets",
		RunsAs:      "you; downloads packages and runs their install steps; podman, on Linux, uses sudo",
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
		Name:        "claude-mcp",
		Description: "claude mcp registers the work dispatch servers at user scope, or removes them without the work role",
		Privilege:   "user",
		Recovery:    "external; Claude Code's entries are not checkpointed",
		Title:       "Claude Code work servers",
		Summary:     "the work MCP servers, started only inside your work folder",
		What:        "Registers credential-free work servers for each Claude Code session, idle at 19 MB outside your work folder.",
		Touches:     "the user-scope MCP servers in ~/.claude.json, and ~/.claude/settings.json at Claude Code's first run",
		RunsAs:      "you",
		Undo:        "Not reverted automatically: claude mcp remove --scope user NAME, for dispatch-atlassian and dispatch-gitlab",
	},
	{
		Name:        "agent-costs-ingest",
		Description: "The installed workbench reads new Claude Code and Codex usage into the agent costs ledger, as its hooks do",
		Privilege:   "user; network for the official price pages",
		Recovery:    "external; ledger rows are derived from the transcripts and not reverted",
		Title:       "Record AI usage",
		Summary:     "new usage shows up in workbench agent costs",
		What:        "Reads your latest Claude Code and Codex sessions into the agent costs ledger, as the session hooks do.",
		RunsAs:      "you; downloads the official price pages",
		Undo:        "Nothing to undo: rows already recorded stay, even after the session files they came from are deleted",
	},
}

// linuxPackagesEffect is the apt or dnf bootstrap of Linux and WSL.
var linuxPackagesEffect = operation.Effect{
	Name:        "linux-packages",
	Description: "apt or dnf base tools, release binaries in ~/.local/bin, themes, fonts and your work and personal folders",
	Privilege:   "sudo; network and font-cache effects",
	Recovery:    "packages external; written files/links require checkpoints",
	Title:       "System packages",
	Summary:     "apt or dnf tools, fonts and your work and personal folders",
	What:        "Installs the base tools with apt or dnf, release binaries, the prompt and its font, and makes your folders.",
	Touches:     "system packages, ~/.local/bin, ~/.local/share/fonts, your work and personal folders",
	RunsAs:      "you, with sudo; downloads packages",
	Undo:        "Not reverted automatically: remove packages with apt or dnf",
}

// epelEffect turns on EPEL where the package manager is dnf: bat, fd, ripgrep,
// htop and ShellCheck come from it on AlmaLinux. It is a step of its own
// because it adds a package source for the whole system.
var epelEffect = operation.Effect{
	Name:        "epel",
	Description: "dnf epel-release from AlmaLinux extras when rpm does not find it; no other repository is changed",
	Privilege:   "sudo; network",
	Recovery:    "external; the package source stays on",
	Title:       "EPEL",
	Summary:     "Fedora's extra packages for AlmaLinux (bat, fd, ripgrep, htop, ShellCheck)",
	What:        "Installs epel-release with dnf when it is missing, turning on the package source System packages uses.",
	Touches:     "the system's dnf repositories (EPEL only)",
	RunsAs:      "you, with sudo; downloads packages",
	Undo:        "Not reverted automatically: sudo dnf remove epel-release",
}

// linuxEditorEffect installs the editor extensions on a Linux desktop. WSL plans
// wslEditorEffect under the same name instead.
var linuxEditorEffect = operation.Effect{
	Name:        "linux-editor-extensions",
	Description: "Listed extensions installed only into a verified local VS Code desktop default profile",
	Privilege:   "user; marketplace network and extension code",
	Recovery:    "external; extensions are not reverted",
	Title:       "VS Code extensions",
	Summary:     "your extension set in the local editor",
	What:        "Installs the listed VS Code extensions into this computer's VS Code, when it is a normal local install.",
	Touches:     "~/.vscode/extensions",
	RunsAs:      "you; downloads from the marketplace and runs extension code",
	Undo:        "Not reverted automatically: uninstall an extension in VS Code",
}

// workToolsEffect is the role-gated work editor extension.
var workToolsEffect = operation.Effect{
	Name:        "work-tools",
	Description: "Work editor extension",
	Privilege:   "user; marketplace network and extension code",
	Recovery:    "external; extensions are not reverted",
	Title:       "Work tools",
	Summary:     "the work editor extension",
	What:        "Installs the work VS Code extension.",
	Touches:     "your VS Code extensions",
	RunsAs:      "you; downloads from the marketplace and runs extension code",
	Undo:        "Not reverted automatically: uninstall the extension in VS Code",
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
		Description: "Windows home and AppData: oh-my-posh and its themes, fonts, .wslconfig, bin/rg.exe and Notepad++ themes",
		Privilege:   "Windows user; not yet checked on a real Windows host",
		Recovery:    "script writes are not checkpointed; acquired executables are external",
		Title:       "Windows setup",
		Summary:     "theme, fonts and ripgrep on the Windows side",
		What:        "Puts the oh-my-posh theme, JetBrains Mono fonts, ripgrep and Notepad++ themes on the Windows side.",
		Touches:     "your Windows home and AppData folders",
		RunsAs:      "you, on Windows",
		Undo:        "Not checkpointed: Windows files are outside Workbench's restore",
	},
	{
		Name:        "wsl-preferences",
		Description: ".wslconfig affects VM sizing/networking at future startup; WSL is never restarted",
		Privilege:   "Windows user",
		Recovery:    "configuration only; running VM state is external",
		Title:       "WSL networking",
		Summary:     "Windows and WSL share localhost (mirrored)",
		What:        "Sets WSL networking to mirrored and applies your memory, swap and CPU sizing, from WSL's next start.",
		Touches:     "%USERPROFILE%\\.wslconfig, a copy saved first and your other settings kept",
		RunsAs:      "you, on Windows",
		Undo:        "Restore the copy saved next to .wslconfig",
	},
}

// provisioningEffects describes canonical script owners, not simulated provider
// results. Optional effects are deliberately absent until separately selected.
// agent-costs-ingest touches the ledger in the data folder this apply
// resolved, named by that folder alone: the plan digest covers it, and the
// ledger's own folder moves under agent/ as soon as a costs entry or the
// migrations step holds the import lock, which may be between a dry run and
// its approval. The mbx step is listed only where its pin has a build for
// this target.
func provisioningEffects(
	c operation.Context,
	answers Answers,
	requirements Requirements,
) []operation.Effect {
	effects := slices.Clone(sharedEffects)
	for i := range effects {
		if effects[i].Name == "agent-costs-ingest" {
			effects[i].Touches = "the agent costs ledger in " + c.ShowPath(c.Paths.Data)
		}
	}
	effects = append(effects, platformEffects(answers)...)
	if answers["has_work"] == true {
		effects = append(effects, workToolsEffect)
	}
	if answers["is_wsl"] == true {
		effects = append(effects, windowsHostEffects...)
	}
	if mbx, ok := mbxEffect(requirements.Mbx); ok {
		effects = append(effects, mbx)
	}
	return effects
}

// mbxEffect is the optional Rust build cache step, off until ticked: it changes
// what plain cargo runs on the whole machine and writes rust-analyzer's user
// file, as touch-id-sudo changes every sudo. Unlike the other optional steps,
// whose scripts run on every apply, its script runs on change, so a ticked
// step with nothing to do is not listed (see [listDue]). It is false where the
// pin has no build for this target, an Intel Mac.
func mbxEffect(pin MbxPin) (operation.Effect, bool) {
	if pin.SHA256[release.Target()] == "" {
		return operation.Effect{}, false
	}
	memory, cpus := strconv.Itoa(pin.MemoryPercent)+"%", strconv.Itoa(pin.ReserveCPUs)
	return operation.Effect{
		Name:     "mbx",
		Optional: true,
		Description: "mbx " + pin.Version + " checked against its SHA-256, mbx setup, and a budget of " + memory +
			" of memory and " + cpus + " cores free",
		Privilege: "user; network",
		Recovery:  "external; revert does not undo it",
		Title:     "Rust build cache",
		Summary:   "plain cargo shares one compile cache and one CPU and memory budget",
		What: "Installs mbx " + pin.Version + ", a shared Rust compile cache, so plain cargo and rust-analyzer " +
			"run through it.",
		Touches: "~/.local/bin/mbx, its Cargo shim, its config file and rust-analyzer.toml",
		RunsAs:  "you; downloads mbx from GitHub",
		Undo: "Not reverted automatically: run mbx setup --uninstall, then delete ~/.local/bin/mbx and the " +
			"shim folder",
	}, true
}

// optionalEffects are the WSL host steps, which run only when selected by name.
// The owning script checks WORKBENCH_EFFECT_<NAME>; see [EffectVariable].
var optionalEffects = []operation.Effect{
	{
		Name:        "terminal-adoption",
		Optional:    true,
		Description: "The managed Windows Terminal settings.json in place of yours, a copy kept, and its font registered",
		Privilege:   "Windows user",
		Recovery:    "copy only; not checkpointed",
		Title:       "Replace Windows Terminal settings",
		Summary:     "replaces yours; a copy is kept",
		What:        "Replaces your Windows Terminal settings.json with the managed one and registers its font, keeping a copy.",
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
		What:        "Replaces your PowerShell profile with the managed one, keeping a copy, and installs oh-my-posh if missing.",
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
		What:        "Registers the JetBrains Mono Nerd Font for your Windows user and loads it at once, downloading it if missing.",
		Touches:     "your Windows font registry",
		RunsAs:      "you, on Windows",
		Undo:        "Not reverted automatically",
	},
	{
		Name:     "default-distro",
		Optional: true,
		Description: "Make this distribution WSL's default and, with several WSL profiles, the one new " +
			"Terminal tabs open",
		Privilege: "Windows user",
		Recovery:  "external; the previous defaults are not restored; a copy of Terminal's settings.json is kept",
		Title:     "Open new terminals in this distribution",
		Summary:   "plain wsl and new Windows Terminal tabs open it",
		What:      "Makes this distribution the one plain `wsl` and new Windows Terminal tabs open.",
		Touches:   "WSL settings and Windows Terminal settings.json (defaultProfile only, a copy kept)",
		RunsAs:    "you, on Windows",
		Undo:      "wsl --set-default <name>, and pick Terminal's default profile or restore its settings.json copy",
	},
	{
		Name:        "windows-path",
		Optional:    true,
		Description: "Append %USERPROFILE%\\bin and %USERPROFILE%\\.local\\bin to the Windows user PATH, keeping existing entries",
		Privilege:   "Windows user environment",
		Recovery:    "external; PATH is not reverted",
		Title:       "Add Windows bin folders to PATH",
		Summary:     "Windows finds the tools Workbench puts there",
		What:        "Appends %USERPROFILE%\\bin and %USERPROFILE%\\.local\\bin to your Windows user PATH.",
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
		What:        "Sets vm.swappiness to 10, kept across restarts, so idle programs stay in memory.",
		Touches:     "/etc/sysctl.d/99-dev.conf",
		RunsAs:      "you, with sudo",
		Undo:        "Delete /etc/sysctl.d/99-dev.conf and restart WSL",
	},
}

// staleSelectionWarnings names each saved select that is none of the effects
// this host offers, listed or left out for having nothing to do: a choice
// saved before an optional step was renamed or removed, or on another
// platform. It is ignored, not refused, so one stale name never
// blocks a plan. The warnings follow the saved file, not the selection being
// planned, so a plan and its recheck under the locks read them alike.
func staleSelectionWarnings(saved Selection, effects []operation.Effect) []operation.Warning {
	var warnings []operation.Warning
	for _, name := range saved.Select {
		if !slices.ContainsFunc(
			effects,
			func(effect operation.Effect) bool { return effect.Name == name },
		) {
			warnings = append(
				warnings,
				operation.Warn(
					"Saved choice "+name+" is not offered here",
					operation.Outcome("it is ignored"),
					operation.Fix("workbench", "apply", "--reset"),
				),
			)
		}
	}
	return warnings
}

// hostOptionalEffects lists every optional effect this host offers, naming
// the distribution for default-distro so consent never covers an implicit
// choice. Every one is its own choice, off until selected by name: none is
// ticked by another effect.
func hostOptionalEffects() []operation.Effect {
	effects := slices.Clone(platformOptionalEffects())
	for i := range effects {
		if effects[i].Name == "default-distro" {
			if distribution := os.Getenv("WSL_DISTRO_NAME"); distribution != "" {
				effects[i].Description = strings.Replace(
					effects[i].Description, "this distribution", distribution, 1,
				)
				effects[i].Title = "Open new terminals in " + distribution
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
// plan is the plan as applied and chosen the selection that produced it.
// With --reset (chosen.Forget) every saved choice for an effect this machine
// offers is forgotten, listed or left off the plan for having nothing to do
// (plan.Unlisted), so such a step is asked about, marked new, when it has work
// again; so are saved selects for effects this host does not offer, which the
// plan warns about. Skips and decisions for those are kept. The decided list
// is always non-nil: once saved, the owner has decided.
// A take-over is never saved as selected: a tick takes over once, and a later
// one is apply --choose. Decided keeps it, so a declined one is not asked again.
func selectionToSave(plan operation.Plan, chosen, saved Selection) Selection {
	effects := plan.Effects
	selection := SelectionOf(effects)
	listed := func(name string) bool {
		return slices.ContainsFunc(effects, func(effect operation.Effect) bool {
			return effect.Name == name
		})
	}
	// kept reports a saved choice for an effect the plan does not list that
	// this save carries over.
	kept := func(name string) bool {
		return !listed(name) && (!chosen.Forget || !slices.Contains(plan.Unlisted, name))
	}
	takeOver := func(name string) bool { return strings.HasPrefix(name, takeOverPrefix) }
	selection.Select = slices.DeleteFunc(selection.Select, takeOver)
	selection.Decided = []string{}
	for _, effect := range effects {
		if !effect.Fixed &&
			((!chosen.Forget && slices.Contains(saved.Decided, effect.Name)) || !plan.AlreadySet(effect)) {
			selection.Decided = append(selection.Decided, effect.Name)
		}
	}
	for _, name := range saved.Skip {
		if kept(name) {
			selection.Skip = append(selection.Skip, name)
		}
	}
	for _, name := range saved.Select {
		if !listed(name) && !chosen.Forget && !takeOver(name) {
			selection.Select = append(selection.Select, name)
		}
	}
	for _, name := range saved.Decided {
		if kept(name) {
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

// EffectVariable is the script switch for an optional effect, for example
// WORKBENCH_EFFECT_DEFAULT_DISTRO for default-distro.
func EffectVariable(name string) string {
	return "WORKBENCH_EFFECT_" + strings.ToUpper(strings.ReplaceAll(name, "-", "_"))
}
