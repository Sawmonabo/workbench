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
	Description: "Canonical extensions, except those that run only on the Windows side, installed into this distribution's VS Code server (~/.vscode-server/extensions) with the newest server's own command line that lists its extensions; nothing to do until VS Code (stable) has opened a folder here; a link in the extensions folder's path, or no working server, blocks this effect",
	Privilege:   linuxEditorEffect.Privilege,
	Recovery:    linuxEditorEffect.Recovery,
	Title:       "VS Code extensions",
	Summary:     "your extension set in VS Code's server in this distribution",
	What:        "Installs the VS Code extensions Workbench lists into this distribution's VS Code server, which VS Code on Windows uses when it opens a folder here. Extensions that run only on the Windows side, such as Remote - WSL, are left out. Until VS Code has opened a folder here there is no server, and nothing to do.",
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
