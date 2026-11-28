package machine

import (
	"github.com/Sawmonabo/workbench/internal/operation"
	"github.com/Sawmonabo/workbench/internal/platform"
)

// wslEditorEffect is linuxEditorEffect on WSL: the same script installs the
// extensions into this distribution's VS Code server, which VS Code on Windows
// runs workspace extensions from when it opens a folder here. Nothing on
// Windows is touched or run.
var wslEditorEffect = operation.Effect{
	Name:        linuxEditorEffect.Name,
	Description: "Listed extensions, except Windows-side ones, installed into this distribution's VS Code server",
	Privilege:   linuxEditorEffect.Privilege,
	Recovery:    linuxEditorEffect.Recovery,
	Title:       "VS Code extensions",
	Summary:     "your extension set in VS Code's server in this distribution",
	What:        "Installs the listed VS Code extensions into this distribution's VS Code server, once there is one.",
	Touches:     "~/.vscode-server/extensions",
	RunsAs:      linuxEditorEffect.RunsAs,
	Undo:        "Not reverted automatically: uninstall an extension in VS Code while it has a folder here open",
}

// platformEffects are the Linux script owners of a full apply: EPEL where the
// package manager is dnf, the apt or dnf bootstrap, and the editor extensions,
// on WSL into this distribution's VS Code server.
func platformEffects(answers Answers) []operation.Effect {
	var effects []operation.Effect
	if platform.PackageManager() == "dnf" {
		effects = append(effects, epelEffect)
	}
	effects = append(effects, linuxPackagesEffect)
	if answers["is_wsl"] == true {
		effects = append(effects, wslEditorEffect)
	} else {
		effects = append(effects, linuxEditorEffect)
	}
	return effects
}

// platformOptionalEffects are the Windows host's optional effects on WSL, and
// none on other Linux.
func platformOptionalEffects() []operation.Effect {
	if platform.WSL() {
		return optionalEffects
	}
	return nil
}
