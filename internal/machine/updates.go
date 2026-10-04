package machine

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/pelletier/go-toml/v2"

	"github.com/Sawmonabo/workbench/internal/operation"
)

// updateEffectPrefix starts the name of every update effect: update-<package>.
// updatesScript is the script that carries them out, together with the apps and
// extensions; see selectScripts.
const (
	updateEffectPrefix = "update-"
	updatesScript      = "50-apps-and-extensions"
)

// brewUpdate is a listed Homebrew package older than Homebrew's current
// version: an app (cask) or a command-line tool (formula).
type brewUpdate struct {
	name, installed, latest string
	cask                    bool
}

// brewExtra is another formula Homebrew installs or updates along with a
// planned tool update: a new or outdated dependency, or an outdated dependent.
type brewExtra struct {
	name, versions string
	install        bool
}

// planUpdates adds one effect per listed package Homebrew can update, so
// approving the plan approves those updates, and records their names for the
// apply script. Asking Homebrew without names keeps its own check, which
// compares the installed app's version for self-updating casks and so never
// proposes a downgrade; pinned packages are held. A tool update's effect also
// names what Homebrew's dry run says comes with it, the list Homebrew would
// otherwise ask about, so the apply script tells Homebrew not to ask. Formulae
// that provide the chezmoi, uv or Python Workbench runs stay at the version it
// qualified, and so does any tool whose update would change them. A failed
// check only warns.
func (p *preparation) planUpdates(
	ctx context.Context,
	c operation.Context,
	files map[string][]byte,
) {
	if runtime.GOOS != "darwin" {
		return
	}
	c.Step("asking Homebrew for updates")
	updates, held, pinned, err := outdatedPackages(ctx, c, files)
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
	var kept, carried, unread []string
	for _, update := range updates {
		effect := operation.Effect{
			Name: updateEffectPrefix + update.name,
			Description: "Update " + update.name + " " + update.installed + " → " +
				update.latest + " through Homebrew",
			Delta:     update.installed + " → " + update.latest,
			Privilege: "user; network",
			Recovery:  "external; the previous version is not kept",
			Title:     "Update " + update.name,
			Summary:   update.installed + " → " + update.latest,
			What:      "Updates " + update.name + " from " + update.installed + " to " + update.latest + " through Homebrew.",
			Touches:   "Homebrew's copy of " + update.name,
			RunsAs:    "you; downloads the update",
			Undo:      "Not reverted: Homebrew does not keep the previous version",
		}
		switch {
		case update.cask:
			effect.Description += "; a running app may be quit"
			effect.Privilege += "; some apps prompt for sudo"
			effect.What += " A running copy of the app may be quit."
			effect.RunsAs += "; some apps ask for your password"
			p.appUpdates = append(p.appUpdates, update.name)
			p.Plan.Effects = append(p.Plan.Effects, effect)
			continue
		case slices.Contains(running, update.name):
			kept = append(kept, update.name)
			continue
		}
		c.Step("checking what updating " + update.name + " brings along")
		extras, err := upgradeExtras(ctx, c, update.name, pinned)
		if err != nil {
			unread = append(unread, update.name)
			continue
		}
		var installs, changes, touched []string
		for _, extra := range extras {
			switch {
			case slices.Contains(running, extra.name):
				touched = append(touched, extra.name)
			case extra.install:
				installs = append(installs, extra.name+" "+extra.versions)
			default:
				changes = append(changes, extra.name+" "+extra.versions)
			}
		}
		if len(touched) > 0 {
			carried = append(carried, update.name+" ("+strings.Join(touched, ", ")+")")
			continue
		}
		if len(installs) > 0 {
			effect.Description += "; also installs " + strings.Join(installs, ", ")
			effect.What += " Also installs " + strings.Join(installs, ", ") + "."
		}
		if len(changes) > 0 {
			effect.Description += "; also updates " + strings.Join(changes, ", ")
			effect.What += " Also updates " + strings.Join(changes, ", ") + "."
		}
		p.toolUpdates = append(p.toolUpdates, update.name)
		p.Plan.Effects = append(p.Plan.Effects, effect)
	}
	for _, warning := range []struct {
		text  string
		names []string
	}{
		{"Kept at the version Workbench runs, not updated: ", kept},
		{"Not updated, since Homebrew would also update what Workbench runs: ", carried},
		{"Not updated, Homebrew's dry run could not be read: ", unread},
	} {
		if len(warning.names) > 0 {
			p.Plan.Warnings = append(
				p.Plan.Warnings,
				warning.text+strings.Join(warning.names, ", "),
			)
		}
	}
}

// upgradeExtras returns the other formulae Homebrew installs or updates when
// it upgrades name. Homebrew's dry run lists new and outdated dependencies and
// outdated dependents, but not the dependencies a dependent's own upgrade
// brings, so the dry run is repeated with those dependents requested too
// until the list stops growing. Pinned formulae, which Homebrew names on a
// line of their own but never upgrades, are left out. Output that never names
// the upgrade itself is unreadable.
func upgradeExtras(
	ctx context.Context,
	c operation.Context,
	name string,
	pinned []string,
) ([]brewExtra, error) {
	requested := []string{name}
	for {
		args := append([]string{"upgrade", "--formula", "--dry-run"}, requested...)
		output, err := brewOutput(ctx, c, homebrew(), args...)
		if err != nil {
			return nil, err
		}
		extras, found := dryRunExtras(output, name)
		if !found {
			return nil, errors.New("unreadable Homebrew dry run")
		}
		extras = slices.DeleteFunc(extras, func(extra brewExtra) bool {
			return slices.Contains(pinned, extra.name)
		})
		grown := false
		for _, extra := range extras {
			if !extra.install && !slices.Contains(requested, extra.name) {
				requested = append(requested, extra.name)
				grown = true
			}
		}
		if !grown {
			return extras, nil
		}
	}
}

// dryRunExtras reads "brew upgrade --dry-run" output, whose sections look like
// "==> Would install 1 dependency:" followed by "jemalloc 5.4.0", or
// "==> Would upgrade 2 dependents" followed by "cairo 1.18.4 -> 1.18.6 (1.6MB)".
// It returns every package listed except name, and whether name was listed.
func dryRunExtras(output, name string) ([]brewExtra, bool) {
	var extras []brewExtra
	install, listing, found := false, false, false
	for line := range strings.Lines(output) {
		fields := strings.Fields(line)
		if header, ok := strings.CutPrefix(line, "==> "); ok {
			words := strings.Fields(header)
			listing = len(words) > 1 && words[0] == "Would"
			install = listing && words[1] == "install"
			continue
		}
		switch {
		case !listing || len(fields) < 2:
		case fields[0] == name:
			found = true
		case slices.ContainsFunc(extras, func(extra brewExtra) bool { return extra.name == fields[0] }):
		case len(fields) >= 4 && fields[2] == "->":
			extras = append(
				extras,
				brewExtra{name: fields[0], versions: fields[1] + " → " + fields[3]},
			)
		default:
			extras = append(
				extras,
				brewExtra{name: fields[0], versions: fields[1], install: install},
			)
		}
	}
	return extras, found
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
// Homebrew reports as outdated, those held by a pin, and every pinned outdated
// formula. Without Homebrew there is nothing to update.
func outdatedPackages(
	ctx context.Context,
	c operation.Context,
	files map[string][]byte,
) (updates []brewUpdate, held, pinned []string, err error) {
	var packages struct {
		Packages struct {
			Darwin struct {
				Brew []string `toml:"brew"`
				Cask []string `toml:"cask"`
			} `toml:"darwin"`
		} `toml:"packages"`
	}
	if err = toml.Unmarshal(files["home/.chezmoidata/packages.toml"], &packages); err != nil {
		return nil, nil, nil, err
	}
	listed := packages.Packages.Darwin
	brew := homebrew()
	if brew == "" || len(listed.Brew)+len(listed.Cask) == 0 {
		return nil, nil, nil, nil
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
	if err = runBrew(ctx, c, brew, &outdated, "outdated", "--json=v2"); err != nil {
		return nil, nil, nil, err
	}
	formulae := listed.Brew
	if len(outdated.Formulae) > 0 && len(formulae) > 0 {
		if formulae, err = formulaNames(ctx, c, brew, formulae); err != nil {
			return nil, nil, nil, err
		}
	}
	for _, formula := range outdated.Formulae {
		if formula.Pinned {
			pinned = append(pinned, formula.Name)
		}
	}
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
	return updates, held, pinned, installedVersions(ctx, c, brew, updates)
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

// installedVersions replaces an app update's recorded version with the
// installed app's own version when that is newer, because the app updated
// itself after Homebrew installed it. Some apps number themselves differently
// from their Homebrew release, such as Discord 0.0.413 for release 0.0.414, so
// an older or differently shaped app version keeps Homebrew's record, which
// its upgrade then prints.
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
			if updates[i].cask && updates[i].name == cask.Token &&
				newerVersion(cask.BundleShortVersion, updates[i].installed) {
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

// runBrew runs a read-only Homebrew query and decodes its JSON output.
func runBrew(
	ctx context.Context,
	c operation.Context,
	brew string,
	target any,
	args ...string,
) error {
	output, err := brewOutput(ctx, c, brew, args...)
	if err != nil {
		return err
	}
	if err = json.Unmarshal([]byte(output), target); err != nil {
		return errors.New("unreadable Homebrew JSON output")
	}
	return nil
}

// brewOutput runs a read-only Homebrew command and returns its standard output.
// Homebrew refreshes its own metadata first unless the user set
// HOMEBREW_NO_AUTO_UPDATE.
func brewOutput(
	ctx context.Context,
	c operation.Context,
	brew string,
	args ...string,
) (string, error) {
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
		return "", err
	}
	return output.Stdout, nil
}

// newerVersion reports whether dotted numeric version a is newer than b.
// Versions with a different number of parts are not compared, as in Homebrew,
// and parts that are not plain numbers keep Homebrew's record.
func newerVersion(a, b string) bool {
	left, right := strings.Split(a, "."), strings.Split(b, ".")
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		x, errX := strconv.ParseUint(left[i], 10, 64)
		y, errY := strconv.ParseUint(right[i], 10, 64)
		if errX != nil || errY != nil {
			return false
		}
		if x != y {
			return x > y
		}
	}
	return false
}

// versionOf drops the build suffix Homebrew appends after a comma, as in
// "4.92.0,240144".
func versionOf(version string) string {
	release, _, _ := strings.Cut(version, ",")
	return release
}
