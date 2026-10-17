package machine

import (
	"github.com/Sawmonabo/workbench/internal/operation"
	"github.com/Sawmonabo/workbench/internal/platform"
)

// platformEffects are the Linux script owners of a full apply: the apt
// bootstrap, and the editor extensions except on WSL, whose editor runs on
// Windows.
func platformEffects(answers Answers) []operation.Effect {
	effects := []operation.Effect{linuxPackagesEffect}
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
