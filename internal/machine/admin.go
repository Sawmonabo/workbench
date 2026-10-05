package machine

import "github.com/Sawmonabo/workbench/internal/operation"

// adminReason finishes "asks for your Mac password once, to ...". The one thing
// an apply asks it for up front is Homebrew's installer, which cannot ask for
// itself because it runs non-interactively. Homebrew drops earlier sudo
// approvals each time it runs, so an app that needs the password is asked for it
// by Homebrew itself while it installs, and is not covered here.
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
