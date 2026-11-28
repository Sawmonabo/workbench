package cli

import (
	"fmt"
	"io"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/Sawmonabo/workbench/internal/display"
	"github.com/Sawmonabo/workbench/internal/machine"
	"github.com/Sawmonabo/workbench/internal/operation"
	"github.com/Sawmonabo/workbench/internal/release"
	"github.com/spf13/cobra"
)

var buildReleaseVersion = "dev"

// buildPublished is "true" in a build the release workflow publishes
// (scripts/package-release.py --published). Every other build is local: not on
// the release channel, so update without a VERSION, run by it as the active
// release, replaces it.
var buildPublished = ""

// updateCommand installs the latest release, or VERSION, and the tools it
// pins, then stops. Applying the machine is apply's job. The hidden
// --local-build installs the tools a checkout pins instead, with no release.
func updateCommand(o *options) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "update [VERSION]",
		Short: "Install the latest Workbench release, or VERSION, and its tools",
		Long: "Install the latest Workbench release, or VERSION (an older one goes back), and " +
			"the chezmoi, uv, Python and TOML Kit it pins. It changes only Workbench's own " +
			"files and keeps the previous release, so running it is the go-ahead. It never " +
			"applies the machine; run workbench apply next. A pre-release is installed only " +
			"by naming it.\n\n" +
			"--list shows the published full releases, marking the latest and the installed " +
			"one, and installs nothing.",
		Example: "  workbench update\n" +
			"  workbench update --list\n" +
			"  workbench update v0.2.0\n" +
			"  workbench update --dry-run",
		Args: cobra.MaximumNArgs(1),
		RunE: o.action(
			updateAction,
			func(cmd *cobra.Command, c operation.Context) (operation.Result, error) {
				if list, _ := cmd.Flags().GetBool("list"); list {
					return listReleases(cmd, c)
				}
				return updateRelease(cmd, c, o)
			},
		),
	}
	cmd.Flags().
		Bool("dry-run", false, "Show the install step only; apply --dry-run shows the machine plan")
	cmd.Flags().
		Bool("list", false, "List the published full releases, marking the installed and latest")
	o.destinationFlag(cmd)
	cmd.Flags().String("bundle", "", "Install this release archive, a local file or HTTPS URL")
	_ = cmd.Flags().MarkHidden("bundle") // install.sh and offline installs
	cmd.Flags().String(
		"runtime-ready",
		"",
		"Continue the verified runtime handoff after an install that is first, new or unchanged",
	)
	_ = cmd.Flags().MarkHidden("runtime-ready")
	o.localBuildFlag(cmd)
	return cmd
}

// versionCommand prints this version.
func versionCommand(o *options) *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Show this Workbench version",
		Long: "Print this Workbench's version, marked (local build) when the release workflow " +
			"did not publish it. It reads no state, so it answers even when Workbench's state " +
			"is damaged. workbench update --list shows the published releases.",
		Example: "  workbench version\n" +
			"  workbench version --json",
		Args: cobra.NoArgs,
		RunE: o.action(
			versionAction,
			func(cmd *cobra.Command, _ operation.Context) (operation.Result, error) {
				result := operation.NewResult(cmd.CommandPath())
				if !o.json {
					// The same line as --version.
					_, err := fmt.Fprintln(
						cmd.OutOrStdout(),
						cmd.Root().Name()+" "+shownVersion(),
					)
					return result, err
				}
				result.Results = append(result.Results, operation.Component{
					Name:    "workbench",
					Status:  operation.StatusComplete,
					Message: shownVersion(),
				})
				return result, nil
			},
		),
	}
}

// listDrops are the flags update --list does not take: it shows the
// releases, and its refusal's fix is the run without them and VERSION.
var listDrops = []string{"dry-run", "bundle", "runtime-ready", "local-build", "destination"}

// listReleases is update --list: the published full releases, newest first,
// marking the installed one. The action has already read the state, so a
// malformed one fails before the network round trip.
func listReleases(cmd *cobra.Command, c operation.Context) (operation.Result, error) {
	result := operation.NewResult(cmd.CommandPath())
	if len(cmd.Flags().Args()) > 0 {
		return result, operation.Fail(
			operation.ExitInvalid,
			"input",
			"--list shows the releases",
			runAgain(cmd, c, nil, listDrops...),
		)
	}
	for _, name := range listDrops {
		if cmd.Flags().Changed(name) {
			return result, operation.Fail(
				operation.ExitInvalid,
				"input",
				"--list shows the releases",
				runAgain(cmd, c, nil, listDrops...),
			)
		}
	}
	installed, err := installedRelease(c)
	if err != nil {
		return result, err
	}
	stop := c.ShowProgress("Asking GitHub for releases")
	published, err := release.Releases(cmd.Context(), c)
	stop()
	if err != nil {
		return result, err
	}
	result.Results = append(result.Results, operation.Component{
		Name:    "releases",
		Status:  operation.StatusComplete,
		Details: releaseList{Releases: published, Installed: installed},
	})
	return result, nil
}

// releaseList is the published full releases, newest first, and the installed
// tag.
type releaseList struct {
	Releases  []release.Published `json:"releases"`
	Installed string              `json:"installed"`
}

// writeReleases prints one release per line, newest first, marking the
// latest and the installed one with *. An installed release the list lacks,
// a pre-release or local build, follows on its own line.
func writeReleases(w io.Writer, list releaseList) error {
	if len(list.Releases) == 0 && list.Installed == "" {
		_, err := fmt.Fprintln(w, "  No releases published yet")
		return err
	}
	rows := make([][]string, 0, len(list.Releases)+1)
	listed := false
	for i, published := range list.Releases {
		mark, notes := " ", []string{}
		if i == 0 {
			notes = append(notes, "latest")
		}
		if published.Tag == list.Installed {
			mark, listed = "*", true
			notes = append(notes, "installed")
		}
		rows = append(rows, []string{
			mark + " " + published.Tag,
			published.Published.Local().Format(display.Date),
			strings.Join(notes, ", "),
		})
	}
	if list.Installed != "" && !listed {
		rows = append(rows, []string{"* " + list.Installed, "", "installed"})
	}
	var b strings.Builder
	writeTable(&b, lineWidth(w), 4, tableSpec{Cols: []column{{}, {}, {Clip: true}}, Rows: rows})
	_, err := lipgloss.Fprint(w, b.String())
	return err
}

// installedRelease returns the active release's tag, or "" without one.
func installedRelease(c operation.Context) (string, error) {
	state, err := operation.ReadState(c.Paths)
	if err != nil || state == nil || state.ActiveRelease == nil {
		return "", err
	}
	return state.ActiveRelease.Identity.Release, nil
}

// releaseCheckCommand is how a newly staged executable proves, before
// activation, that it accepts its own source.
func releaseCheckCommand(o *options) *cobra.Command {
	check := &cobra.Command{
		Use:    "release-check",
		Short:  "update runs this in a newly staged release to prove it accepts its own source",
		Hidden: true,
		Args:   cobra.NoArgs,
	}
	check.Flags().String("bundle-directory", "", "Verified private extracted bundle")
	check.RunE = o.action(
		releaseAction,
		func(cmd *cobra.Command, c operation.Context) (operation.Result, error) {
			result := operation.NewResult(cmd.CommandPath())
			directory, _ := cmd.Flags().GetString("bundle-directory")
			metadata, err := release.Inspect(directory)
			if err != nil {
				return result, err
			}
			_, identity, err := machine.SourceSnapshot(directory, false)
			if err != nil {
				return result, err
			}
			if metadata.SourceDigest != identity.ContentDigest {
				return result, operation.Fail(
					operation.ExitInvalid,
					"release_identity",
					"Staged executable and source identities differ",
				)
			}
			result.Results = append(
				result.Results,
				operation.Component{
					Name:    "release-check",
					Status:  operation.StatusComplete,
					Details: metadata.Release,
				},
			)
			return result, nil
		},
	)
	return check
}
