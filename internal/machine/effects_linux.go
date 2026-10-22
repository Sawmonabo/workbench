package machine

import (
	"github.com/Sawmonabo/workbench/internal/operation"
	"github.com/Sawmonabo/workbench/internal/platform"
)

// platformEffects are the Linux script owners of a full apply: EPEL where the
// package manager is dnf, the apt or dnf bootstrap, and the editor extensions
// except on WSL, whose editor runs on Windows.
func platformEffects(answers Answers) []operation.Effect {
	var effects []operation.Effect
	if platform.PackageManager() == "dnf" {
		effects = append(effects, epelEffect)
	}
	effects = append(effects, linuxPackagesEffect)
	if answers["is_wsl"] != true {
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
