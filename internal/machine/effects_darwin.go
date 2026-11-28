package machine

import "github.com/Sawmonabo/workbench/internal/operation"

// platformEffects are the macOS script owners of a full apply.
func platformEffects(Answers) []operation.Effect { return macOSEffects }

// platformOptionalEffects are the Mac's optional effects.
func platformOptionalEffects() []operation.Effect { return macOSOptionalEffects }

// macOSOptionalEffects are the Mac steps that run only when selected by name,
// like [optionalEffects] on WSL. The owning script checks WORKBENCH_EFFECT_<NAME>;
// see [EffectVariable]. Touch ID for sudo changes how every sudo on the Mac asks,
// and writes a system file outside the checkpoint, so it is never on by default.
var macOSOptionalEffects = []operation.Effect{
	{
		Name:        "touch-id-sudo",
		Optional:    true,
		Description: "Turn on Touch ID for sudo through /etc/pam.d/sudo_local, from Apple's template when it is missing",
		Privilege:   "sudo; writes /etc/pam.d/sudo_local",
		Recovery:    "external; revert does not undo it",
		Title:       "Touch ID for sudo",
		Summary:     "sudo asks for your fingerprint, and for the password where that cannot be used",
		What:        "Turns on Touch ID for sudo in every terminal, through /etc/pam.d/sudo_local.",
		Touches:     "/etc/pam.d/sudo_local",
		RunsAs:      "you, with one sudo command",
		Undo:        "Not reverted automatically: with sudo, delete /etc/pam.d/sudo_local, or comment out its pam_tid.so line again",
	},
}
