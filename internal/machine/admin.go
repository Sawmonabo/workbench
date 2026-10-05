package machine

import "github.com/Sawmonabo/workbench/internal/operation"

// adminReason finishes "asks for your Mac password once, to ...". The one thing
// an apply asks it for up front is Homebrew's installer, which cannot ask for
// itself because it runs non-interactively. An app that needs the password later,
// such as Docker Desktop, is covered too: the same password answers Homebrew's
// request through the apply's SUDO_ASKPASS helper, asked at that point only when
// nothing was asked up front.
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

// AdminReason says in plain words what applying plan asks for the Mac password
// to do, "install Homebrew", or "" when it asks for nothing. Only an apply at a
// terminal asks; see [operation.WithAdmin].
func AdminReason(plan operation.Plan) string {
	if installsHomebrew(plan.Effects) {
		return adminReason
	}
	return ""
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
