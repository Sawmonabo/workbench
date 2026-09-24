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

// brewUpdate is a listed Homebrew package older than Homebrew's current
// version: an app (cask) or a command-line tool (formula).
type brewUpdate struct {
	name, installed, latest string
	cask                    bool
}

// planUpdates adds one effect per listed package Homebrew can update, so
// approving the plan approves those updates, and records their names for the
// apply script. Asking Homebrew without names keeps its own check, which
// compares the installed app's version for self-updating casks and so never
// proposes a downgrade; pinned packages are held. Formulae that provide the
// chezmoi, uv or Python Workbench runs stay at the version it qualified. A
// failed check only warns.
func (p *preparation) planUpdates(
	ctx context.Context,
	c operation.Context,
	files map[string][]byte,
) {
	if runtime.GOOS != "darwin" {
		return
	}
	updates, held, err := outdatedPackages(ctx, c, files)
	if err != nil {
		p.Plan.Warnings = append(
			p.Plan.Warnings,
			"Updates were not checked; Homebrew's outdated check failed",
		)
		return
	}
	if len(held) > 0 {
		p.Plan.Warnings = append(
			p.Plan.Warnings,
			"Held by brew pin, not updated: "+strings.Join(held, ", "),
		)
	}
	running := workbenchFormulae(p.Plan.Dependencies)
	var kept []string
	for _, update := range updates {
		effect := operation.Effect{
			Name: "update-" + update.name,
			Description: "Update " + update.name + " " + update.installed + " → " +
				update.latest + " through Homebrew",
			Privilege: "user; network",
			Recovery:  "external; the previous version is not kept",
		}
		switch {
		case update.cask:
			effect.Description += "; a running app may be quit"
			effect.Privilege += "; some apps prompt for sudo"
			p.appUpdates = append(p.appUpdates, update.name)
		case slices.Contains(running, update.name):
			kept = append(kept, update.name)
			continue
		default:
			p.toolUpdates = append(p.toolUpdates, update.name)
		}
		p.Plan.Effects = append(p.Plan.Effects, effect)
	}
	if len(kept) > 0 {
		p.Plan.Warnings = append(
			p.Plan.Warnings,
			"Kept at the version Workbench runs, not updated: "+strings.Join(kept, ", "),
		)
	}
}

// workbenchFormulae names the Homebrew formulae whose executables Workbench
// selected as its own chezmoi, uv or Python.
func workbenchFormulae(dependencies []operation.Dependency) []string {
	var names []string
	for _, dependency := range dependencies {
		_, rest, found := strings.Cut(dependency.Path, "/Cellar/")
		if name, _, _ := strings.Cut(rest, "/"); found && name != "" {
			names = append(names, name)
		}
	}
	return names
}

// outdatedPackages returns the formulae and casks in packages.toml that
// Homebrew reports as outdated, and those held by a pin. Without Homebrew there
// is nothing to update.
func outdatedPackages(
	ctx context.Context,
	c operation.Context,
	files map[string][]byte,
) ([]brewUpdate, []string, error) {
	var packages struct {
		Packages struct {
			Darwin struct {
				Brew []string `toml:"brew"`
				Cask []string `toml:"cask"`
			} `toml:"darwin"`
		} `toml:"packages"`
	}
	if err := toml.Unmarshal(files["home/.chezmoidata/packages.toml"], &packages); err != nil {
		return nil, nil, err
	}
	listed := packages.Packages.Darwin
	brew := homebrew()
	if brew == "" || len(listed.Brew)+len(listed.Cask) == 0 {
		return nil, nil, nil
	}
	type entry struct {
		Name              string   `json:"name"`
		InstalledVersions []string `json:"installed_versions"`
		CurrentVersion    string   `json:"current_version"`
		Pinned            bool     `json:"pinned"`
	}
	var outdated struct {
		Formulae []entry `json:"formulae"`
		Casks    []entry `json:"casks"`
	}
	if err := runBrew(ctx, c, brew, &outdated, "outdated", "--json=v2"); err != nil {
		return nil, nil, err
	}
	formulae := listed.Brew
	if len(outdated.Formulae) > 0 && len(formulae) > 0 {
		var err error
		if formulae, err = formulaNames(ctx, c, brew, formulae); err != nil {
			return nil, nil, err
		}
	}
	var updates []brewUpdate
	var held []string
	collect := func(entries []entry, names []string, cask bool) {
		for _, outdated := range entries {
			switch {
			case !slices.Contains(names, outdated.Name):
			case outdated.Pinned:
				held = append(held, outdated.Name)
			default:
				update := brewUpdate{
					name:   outdated.Name,
					latest: versionOf(outdated.CurrentVersion),
					cask:   cask,
				}
				if n := len(outdated.InstalledVersions); n > 0 {
					update.installed = versionOf(outdated.InstalledVersions[n-1])
				}
				updates = append(updates, update)
			}
		}
	}
	collect(outdated.Formulae, formulae, false)
	collect(outdated.Casks, listed.Cask, true)
	return updates, held, installedVersions(ctx, c, brew, updates)
}

// formulaNames returns Homebrew's own names for the listed formulae, which
// outdated reports: an alias such as postgresql becomes postgresql@18.
func formulaNames(
	ctx context.Context,
	c operation.Context,
	brew string,
	listed []string,
) ([]string, error) {
	var info struct {
		Formulae []struct {
			Name string `json:"name"`
		} `json:"formulae"`
	}
	args := append([]string{"info", "--formula", "--json=v2"}, listed...)
	if err := runBrew(ctx, c, brew, &info, args...); err != nil {
		return nil, err
	}
	names := make([]string, 0, len(info.Formulae))
	for _, formula := range info.Formulae {
		names = append(names, formula.Name)
	}
	return names, nil
}

// installedVersions replaces each app update's recorded version with the
// version of the app actually installed, which self-updating apps change.
func installedVersions(
	ctx context.Context,
	c operation.Context,
	brew string,
	updates []brewUpdate,
) error {
	args := []string{"info", "--cask", "--json=v2"}
	for _, update := range updates {
		if update.cask {
			args = append(args, update.name)
		}
	}
	if len(args) == 3 {
		return nil
	}
	var info struct {
		Casks []struct {
			Token              string `json:"token"`
			BundleShortVersion string `json:"bundle_short_version"`
		} `json:"casks"`
	}
	if err := runBrew(ctx, c, brew, &info, args...); err != nil {
		return err
	}
	for _, cask := range info.Casks {
		for i := range updates {
			if updates[i].cask && updates[i].name == cask.Token && cask.BundleShortVersion != "" {
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
