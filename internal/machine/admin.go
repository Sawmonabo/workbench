package machine

import (
	"strings"

	"github.com/Sawmonabo/workbench/internal/operation"
)

// adminNeed is what the checked effects of a plan use sudo for. It comes from
// the effects' NeedsAdmin marks, which are set where each is known:
// macos-packages when Homebrew is missing, the apps step when its probe finds
// an app to install, and each app update.
type adminNeed struct{ homebrew, install, update bool }

func adminNeeded(effects []operation.Effect) adminNeed {
	var need adminNeed
	for _, effect := range effects {
		if !effect.Checked || !effect.NeedsAdmin {
			continue
		}
		switch {
		case effect.Name == "macos-packages":
			need.homebrew = true
		case effect.Name == "macos-apps-extensions":
			need.install = true
		case strings.HasPrefix(effect.Name, updateEffectPrefix):
			need.update = true
		}
	}
	return need
}

func (n adminNeed) any() bool { return n.homebrew || n.install || n.update }

// reason finishes "asks for your Mac password once, to ...". An app update needs
// Homebrew installed, so Homebrew's own install never comes with one.
func (n adminNeed) reason() string {
	var reason string
	switch {
	case n.homebrew && n.install:
		reason = "install Homebrew and apps"
	case n.homebrew:
		reason = "install Homebrew"
	case n.install && n.update:
		return "install and update apps"
	case n.install:
		reason = "install apps"
	}
	if n.update {
		if reason != "" {
			reason += " and "
		}
		reason += "update apps"
	}
	return reason
}

// AdminReason says in plain words what applying plan asks for the Mac password
// to do, such as "install Homebrew and apps", or "" when it asks for nothing.
// Only an apply at a terminal asks; see [operation.WithAdmin].
func AdminReason(plan operation.Plan) string { return adminNeeded(plan.Effects).reason() }

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
