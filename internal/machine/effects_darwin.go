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
		Description: "Turn on Touch ID for sudo: switch on the pam_tid.so line in /etc/pam.d/sudo_local, created from Apple's template when missing; nothing when it is already on",
		Privilege:   "sudo; writes /etc/pam.d/sudo_local",
		Recovery:    "external; revert does not undo it",
		Title:       "Touch ID for sudo",
		Summary:     "sudo asks for your fingerprint, and for the password where that cannot be used",
		What:        "Turns on Touch ID for sudo in every terminal, not only for Workbench. With no /etc/pam.d/sudo_local it creates the file from Apple's template with the pam_tid.so line switched on (owned by root, read-only, as the template is); with one already there it switches on or adds only that line and every other line stays. It never edits /etc/pam.d/sudo, and does nothing when Touch ID for sudo is already on. A system update keeps the file. Where Touch ID cannot be used, over SSH or in tmux for example, sudo asks for the password as before.",
		Touches:     "/etc/pam.d/sudo_local",
		RunsAs:      "you, with sudo; its one sudo command uses the Mac password an apply at a terminal asks for once, and without a terminal needs a valid sudo ticket",
		Undo:        "Not reverted automatically: with sudo, delete /etc/pam.d/sudo_local, or comment out its pam_tid.so line again",
	},
}
