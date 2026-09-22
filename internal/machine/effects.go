package machine

import (
	"runtime"

	"github.com/Sawmonabo/workbench/internal/operation"
)

// ProvisioningEffects describes canonical script owners, not simulated provider
// results. Optional effects are deliberately absent until separately selected.
func ProvisioningEffects(answers Answers) []operation.Effect {
	effects := []operation.Effect{
		{Name: "runtime-managers", Description: "Native nvm/rustup and platform-owned uv/bun/Go; user runtime directories and default links", Privilege: "user; network and installer execution", Recovery: "external; no package rollback"},
		{Name: "runtimes", Description: "Native Node/Python/Rust installs and default Node alias", Privilege: "user; network", Recovery: "external; no runtime rollback"},
		{Name: "global-tools", Description: "Native npm/Corepack, uv tools, cargo and Go tools", Privilege: "user; network and package build hooks", Recovery: "external; no tool rollback"},
		{Name: "tmux-plugins", Description: "TPM/plugin installation under ~/.tmux/plugins", Privilege: "user; network and executable plugin hooks", Recovery: "external; no plugin rollback"},
	}
	if runtime.GOOS == "darwin" {
		effects = append(effects,
			operation.Effect{Name: "macos-packages", Description: "Homebrew/formulae, ~/.config/oh-my-posh and TPM; already selected chezmoi/uv owners retained", Privilege: "user; initial Homebrew/CLT may require elevation", Recovery: "package effects external; created files require checkpoints"},
			operation.Effect{Name: "macos-apps-extensions", Description: "Missing casks and VS Code extensions; existing unmanaged apps require separate replacement/adoption selection", Privilege: "user; casks may require elevation", Recovery: "external; application snapshots retained separately"},
			operation.Effect{Name: "terminal-font", Description: "Native managed ~/.terminal-font-setup.sh provides instructions; Terminal font selection is a separate manual step", Privilege: "none during provisioning", Recovery: "helper checkpointed; manual UI external"})
	} else {
		effects = append(effects, operation.Effect{Name: "linux-packages", Description: "apt base prerequisites/bat/fd/tmux/podman, ~/.local/bin, themes, fonts, dev/repos directories and TPM", Privilege: "sudo; network and font-cache effects", Recovery: "packages external; written files/links require checkpoints"})
		effects = append(effects, operation.Effect{Name: "linux-editor-extensions", Description: "Canonical extensions installed only into a verified local VS Code desktop default profile; unavailable or ambiguous editor hosts block this effect", Privilege: "user; marketplace network and extension code", Recovery: "external; extensions are not reverted"})
	}
	if answers["has_work"] == true {
		effects = append(effects, operation.Effect{Name: "work-tools", Description: "Bitwarden CLI and work editor extension; existing Bitwarden session-caching shell policy", Privilege: "user; network", Recovery: "packages external; shell configuration checkpointed"})
	}
	if answers["is_wsl"] == true {
		effects = append(effects,
			operation.Effect{Name: "windows-files", Description: "Discovered Windows home/AppData: oh-my-posh binary/themes, fonts, Terminal settings, actual PowerShell profile, .wslconfig, RestartWSL helpers, bin/rg.exe, VS Code User settings and Notepad++ themes; ~/.vscode-server/data/Machine/settings.json", Privilege: "Windows user; native path/ACL and existing-file ownership qualification required", Recovery: "each exact path needs file preflight/checkpoint; acquired executables are external"},
			operation.Effect{Name: "wsl-preferences", Description: ".wslconfig affects VM sizing/networking at future startup; restart helpers are installed but never executed", Privilege: "Windows user", Recovery: "configuration only; running VM state is external"})
	}
	return effects
}
