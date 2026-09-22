package machine

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/pelletier/go-toml/v2"

	"github.com/Sawmonabo/workbench/internal/operation"
)

// appUpdate is a listed cask whose installed app is older than Homebrew's
// current version.
type appUpdate struct{ name, installed, latest string }

// planAppUpdates adds one effect per listed app Homebrew can update, so
// approving the plan approves those updates, and records their names for the
// apply script. Asking Homebrew without cask names keeps its own check, which
// compares the installed app's version for self-updating casks and so never
// proposes a downgrade; pinned casks are held. A failed check only warns.
func (p *preparation) planAppUpdates(
	ctx context.Context,
	c operation.Context,
	files map[string][]byte,
) {
	if runtime.GOOS != "darwin" {
		return
	}
	updates, held, err := outdatedApps(ctx, c, files)
	if err != nil {
		p.Plan.Warnings = append(
			p.Plan.Warnings,
			"App updates were not checked; Homebrew's outdated check failed",
		)
		return
	}
	if len(held) > 0 {
		p.Plan.Warnings = append(
			p.Plan.Warnings,
			"Held by brew pin, not updated: "+strings.Join(held, ", "),
		)
	}
	for _, update := range updates {
		p.Plan.Effects = append(p.Plan.Effects, operation.Effect{
			Name: "update-" + update.name,
			Description: "Update " + update.name + " " + update.installed + " → " +
				update.latest + " through Homebrew; a running app may be quit",
			Privilege: "user; network; some apps prompt for sudo",
			Recovery:  "external; the previous version is not kept",
		})
		p.appUpdates = append(p.appUpdates, update.name)
	}
}

// outdatedApps returns the casks in packages.toml that Homebrew reports as
// outdated, and those held by a pin. Without Homebrew there is nothing to
// update.
func outdatedApps(
	ctx context.Context,
	c operation.Context,
	files map[string][]byte,
) ([]appUpdate, []string, error) {
	var packages struct {
		Packages struct {
			Darwin struct {
				Cask []string `toml:"cask"`
			} `toml:"darwin"`
		} `toml:"packages"`
	}
	if err := toml.Unmarshal(files["home/.chezmoidata/packages.toml"], &packages); err != nil {
		return nil, nil, err
	}
	listed := packages.Packages.Darwin.Cask
	brew := homebrew()
	if brew == "" || len(listed) == 0 {
		return nil, nil, nil
	}
	var outdated struct {
		Casks []struct {
			Name              string   `json:"name"`
			InstalledVersions []string `json:"installed_versions"`
			CurrentVersion    string   `json:"current_version"`
			Pinned            bool     `json:"pinned"`
		} `json:"casks"`
	}
	if err := runBrew(ctx, c, brew, &outdated, "outdated", "--cask", "--json=v2"); err != nil {
		return nil, nil, err
	}
	var updates []appUpdate
	var held []string
	for _, cask := range outdated.Casks {
		switch {
		case !slices.Contains(listed, cask.Name):
		case cask.Pinned:
			held = append(held, cask.Name)
		default:
			update := appUpdate{name: cask.Name, latest: versionOf(cask.CurrentVersion)}
			if len(cask.InstalledVersions) > 0 {
				update.installed = versionOf(cask.InstalledVersions[0])
			}
			updates = append(updates, update)
		}
	}
	if len(updates) == 0 {
		return nil, held, nil
	}
	return updates, held, installedVersions(ctx, c, brew, updates)
}

// installedVersions replaces each update's recorded version with the version
// of the app actually installed, which self-updating apps change.
func installedVersions(
	ctx context.Context,
	c operation.Context,
	brew string,
	updates []appUpdate,
) error {
	var info struct {
		Casks []struct {
			Token              string `json:"token"`
			BundleShortVersion string `json:"bundle_short_version"`
		} `json:"casks"`
	}
	args := []string{"info", "--cask", "--json=v2"}
	for _, update := range updates {
		args = append(args, update.name)
	}
	if err := runBrew(ctx, c, brew, &info, args...); err != nil {
		return err
	}
	for _, cask := range info.Casks {
		for i := range updates {
			if updates[i].name == cask.Token && cask.BundleShortVersion != "" {
				updates[i].installed = cask.BundleShortVersion
			}
		}
	}
	return nil
}

// homebrew returns Homebrew's executable at its default prefix, or "".
func homebrew() string {
	for _, path := range []string{"/opt/homebrew/bin/brew", "/usr/local/bin/brew"} {
		if info, err := os.Stat(path); err == nil && info.Mode().IsRegular() {
			return path
		}
	}
	return ""
}

// runBrew runs a read-only Homebrew query and decodes its JSON output. Homebrew
// refreshes its own metadata first unless the user set HOMEBREW_NO_AUTO_UPDATE.
func runBrew(
	ctx context.Context,
	c operation.Context,
	brew string,
	target any,
	args ...string,
) error {
	environment := []string{
		"HOME=" + c.Home,
		"PATH=" + filepath.Dir(brew) + ":/usr/bin:/bin:/usr/sbin:/sbin",
	}
	if value, ok := os.LookupEnv("HOMEBREW_NO_AUTO_UPDATE"); ok {
		environment = append(environment, "HOMEBREW_NO_AUTO_UPDATE="+value)
	}
	output, err := operation.Run(ctx, c, nil, operation.Process{
		Executable:  brew,
		Args:        args,
		Directory:   "/",
		Environment: environment,
		Timeout:     5 * time.Minute,
		OutputLimit: 8 << 20,
	})
	if err != nil {
		return err
	}
	if err = json.Unmarshal([]byte(output.Stdout), target); err != nil {
		return errors.New("unreadable Homebrew JSON output")
	}
	return nil
}

// versionOf drops the build suffix Homebrew appends after a comma, as in
// "4.92.0,240144".
func versionOf(version string) string {
	release, _, _ := strings.Cut(version, ",")
	return release
}
