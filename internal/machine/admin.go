package machine

import "github.com/Sawmonabo/workbench/internal/operation"

// adminReason finishes "asks for ... to ...". The one thing an apply asks for up
// front is Homebrew's installer, which cannot ask for itself because it runs
// non-interactively. An app that needs administrator rights later, such as Docker
// Desktop, is covered too: the same password answers Homebrew's request through
// the apply's SUDO_ASKPASS helper, asked at that point only when nothing was
// asked up front. Where sudo asks for Touch ID, there is no helper and Homebrew's
// own sudo asks each time.
const adminReason = "install Homebrew"

// installsHomebrew reports whether a checked effect has to install Homebrew,
// which markHomebrewInstall sets.
func installsHomebrew(effects []operation.Effect) bool {
	for _, effect := range effects {
		if effect.Checked && effect.NeedsAdmin && effect.Name == "macos-packages" {
			return true
		}
	}
	return false
}

// AdminLine says in plain words what applying plan asks for before it starts:
// the Mac password once, or Touch ID where sudo uses it (with the password where
// Touch ID cannot be used), to install Homebrew. It is "" when it asks for
// nothing. Only an apply at a terminal asks; see [operation.WithAdmin].
func AdminLine(plan operation.Plan) string {
	switch {
	case !installsHomebrew(plan.Effects):
		return ""
	case operation.TouchIDForSudo():
		return "Applying at a terminal asks for Touch ID (your Mac password where Touch ID " +
			"cannot be used) before it starts, to " + adminReason + "."
	}
	return "Applying at a terminal asks for your Mac password once, before it starts, to " +
		adminReason + "."
}

// markHomebrewInstall flags macos-packages as using sudo when Homebrew is not
// installed: its installer then needs the password, and it cannot ask for it
// itself because it runs non-interactively.
func markHomebrewInstall(effects []operation.Effect) {
	if homebrew() != "" {
		return
	}
	for i := range effects {
		if effects[i].Name == "macos-packages" {
			effects[i].NeedsAdmin = true
		}
	}
}
