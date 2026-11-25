package cli

import (
	"cmp"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
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

// listReleases is update --list: the published full releases, newest first,
// marking the installed one. The action has already read the state, so a
// malformed one fails before the network round trip.
func listReleases(cmd *cobra.Command, c operation.Context) (operation.Result, error) {
	result := operation.NewResult(cmd.CommandPath())
	if len(cmd.Flags().Args()) > 0 {
		return result, operation.Fail(
			operation.ExitInvalid,
			"input",
			"--list shows the releases; drop VERSION",
		)
	}
	for _, name := range []string{"dry-run", "bundle", "runtime-ready", "local-build", "destination"} {
		if cmd.Flags().Changed(name) {
			return result, operation.Fail(
				operation.ExitInvalid,
				"input",
				"--list shows the releases; drop --"+name,
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

// updateRelease downloads (or reads --bundle), verifies, stages and activates
// a release, then hands off to it to install the tools it pins. With
// --local-build it installs the checkout's tools instead.
func updateRelease(
	cmd *cobra.Command,
	c operation.Context,
	o *options,
) (result operation.Result, resultErr error) {
	result = operation.NewResult(cmd.CommandPath())
	defer partialUpdate(cmd.CommandPath(), &result, &resultErr)
	// Input errors come before the view, which would call them a failed update.
	ready, _ := cmd.Flags().GetString("runtime-ready")
	tag, err := "", localBuildInput(cmd, c.Native.Developer)
	if err == nil && !c.Native.Developer && ready == "" {
		tag, err = releaseTag(cmd)
	}
	if location, _ := cmd.Flags().GetString("bundle"); err == nil && cmd.Flags().Changed("bundle") {
		err = release.CheckBundleLocation(location) // refuses an empty one too
	}
	if err == nil && ready == "" {
		err = startUpdateView(c)
	}
	if err != nil {
		return result, err
	}
	if c.Native.Developer {
		return installCheckoutTools(cmd, c, o, result)
	}
	c.ReadOnly = false
	if ready != "" {
		return continueInstall(cmd, c, o, result, ready == "unchanged", ready == "first")
	}
	// Read before the install: afterwards the state names the new release.
	previous, _ := installedRelease(c)
	bundle, replacing, err := readRelease(cmd, c, tag)
	if err != nil {
		return result, err
	}
	if bundle == nil {
		// Already installed: still install the tools, so rerunning update
		// finishes a setup that was declined or failed.
		return continueInstalled(cmd, c, o, result)
	}
	planner := releasePlanner(c, *bundle)
	plan, err := planner(cmd.Context(), c)
	if err != nil {
		return result, err
	}
	result.PlanDigest = plan.Digest()
	result.Results = append(
		result.Results,
		operation.Component{
			Name:    "release-plan",
			Status:  operation.StatusComplete,
			Message: replacing,
			Details: plan,
		},
	)
	if dryRun, _ := cmd.Flags().GetBool("dry-run"); dryRun {
		return result, nil
	}
	// Running update is the go-ahead to install Workbench itself: it changes
	// only Workbench's own files and keeps the release it replaces. The
	// recheck under the locks still refuses a plan that changed.
	updateViewOf(c).phase(phaseInstalling)
	stop := c.ShowProgress("Installing Workbench " + bundle.Metadata.Release)
	// A removal warning prints once the view has ended: from the handoff when
	// it goes ahead, else with the result's other warnings.
	var warnings []string
	defer func() { result.Warnings = append(result.Warnings, warnings...) }()
	err = operation.WithMutation(
		cmd.Context(),
		c,
		plan,
		operation.Consent{ApprovedDigest: result.PlanDigest, CompleteInputs: true},
		planner,
		stageRelease(cmd, c, *bundle, plan, &warnings),
	)
	stop()
	if err != nil {
		return result, err
	}
	result.Results = append(
		result.Results,
		operation.Component{
			Name:    "release",
			Status:  operation.StatusComplete,
			Message: bundle.String(),
		},
	)
	// No operation locks or transient extraction files survive this boundary.
	if _, err = release.Inspect(bundle.Directory(c)); err != nil {
		return result, err
	}
	state, err := operation.ReadState(c.Paths)
	if err != nil {
		return result, err
	}
	announce := announceHandoff(
		cmd,
		c,
		cmp.Or(replacing, "Installed "+bundle.Metadata.Release),
		&warnings,
	)
	install := "installed"
	if previous == "" {
		install = "first"
	}
	if cmd.Context().Err() != nil {
		return result, stoppedBeforeTools(c, bundle.Metadata.Release)
	}
	return result, operation.Handoff(c, *state.ActiveRelease, handoffArgs(o, install), announce)
}

// stoppedBeforeTools is an interruption that arrived between activating
// release and handing over to it: the update stops there, since the activated
// release would start with no memory of the interruption, and says that the
// release is active.
func stoppedBeforeTools(c operation.Context, release string) error {
	return operation.Fail(
		operation.ExitInterrupted,
		"interrupted",
		"Stopped after installing "+release+", before its tools: rerun "+
			c.WorkbenchCommand()+" update to finish",
	)
}

// installCheckoutTools is update --local-build: it puts the tools the checkout's
// versions.toml pins in place, without asking, and stops. No release is
// downloaded, activated or handed off to, and the machine is not applied. As
// for a release, running it is the go-ahead: the tools go only into Workbench's
// own directory, each checked against the digest the checkout pins.
func installCheckoutTools(
	cmd *cobra.Command,
	c operation.Context,
	o *options,
	result operation.Result,
) (operation.Result, error) {
	if dryRun, _ := cmd.Flags().GetBool("dry-run"); dryRun {
		plan, err := machine.ToolsPlan(cmd.Context(), c)
		if err != nil {
			return result, err
		}
		result.PlanDigest = plan.Digest()
		result.Results = append(
			result.Results,
			operation.Component{
				Name:    "tools-plan",
				Status:  operation.StatusComplete,
				Details: plan,
			},
		)
		return result, nil
	}
	c.ReadOnly = false
	if err := setUpTools(cmd, c, o, &result); err != nil {
		return result, err
	}
	result.Summary = "[WorkBench] This checkout's tools are in place; run " +
		c.WorkbenchCommand() + " apply --local-build"
	outcome := "Up to date: this checkout's tools"
	installed := slices.ContainsFunc(result.Results, func(component operation.Component) bool {
		return component.Name == "setup" && component.Status == operation.StatusComplete
	})
	if installed {
		result.Summary = "[WorkBench] Installed this checkout's tools; run " +
			c.WorkbenchCommand() + " apply --local-build"
		outcome = "Installed this checkout's tools"
	}
	updateViewOf(c).finish(outcome, installed)
	return result, nil
}

// continueInstalled hands off to the already active release to install its
// tools, then stops: update never applies the machine.
func continueInstalled(
	cmd *cobra.Command,
	c operation.Context,
	o *options,
	result operation.Result,
) (operation.Result, error) {
	state, err := operation.ReadState(c.Paths)
	if err != nil {
		return result, err
	}
	active := *state.ActiveRelease
	result.Results = append(result.Results, operation.Component{
		Name:    "release",
		Status:  operation.StatusUnchanged,
		Message: active.Identity.Release + " is already installed",
	})
	if dryRun, _ := cmd.Flags().GetBool("dry-run"); dryRun {
		return result, nil
	}
	if err := cmd.Context().Err(); err != nil {
		return result, err // as above
	}
	return result, operation.Handoff(
		c,
		active,
		handoffArgs(o, "unchanged"),
		announceHandoff(cmd, c, active.Identity.Release+" is already installed", nil),
	)
}

// readRelease reads --bundle, or finds and downloads release tag (the latest
// when empty) for this machine. It returns no bundle when that release is
// already the active one, unless an interrupted activation still needs
// finishing, and when tag is empty and the active release, not replaced as a
// local build, is newer than the latest. Its second value, when tag is empty
// and the latest published release replaces the active local build, says so.
func readRelease(
	cmd *cobra.Command,
	c operation.Context,
	tag string,
) (*release.Bundle, string, error) {
	installed, err := installedRelease(c)
	if err != nil {
		return nil, "", err
	}
	current := func(found string) bool {
		return found == installed && release.ValidateSelection(c) == nil
	}
	if location, _ := cmd.Flags().GetString("bundle"); cmd.Flags().Changed("bundle") {
		if strings.HasPrefix(location, "https://") {
			updateViewOf(c).phase(phaseDownloading)
		}
		bundle, err := release.ReadBundle(cmd.Context(), location, tag, c.Transfer("workbench"))
		if err != nil || current(bundle.Metadata.Release) {
			return nil, "", err
		}
		return &bundle, "", nil
	}
	stop := c.ShowProgress("Getting Workbench " + cmp.Or(tag, "latest"))
	defer stop()
	c.Step("asking GitHub for the release")
	remote, err := release.Find(cmd.Context(), c, tag)
	if err != nil || current(remote.Tag) {
		return nil, "", err
	}
	// Without a VERSION, update only moves forward, so a lagging latest never
	// takes a release back. An active local build is not on the release
	// channel: the latest published release replaces it, whatever their
	// versions. Only that build itself, run as the active release, can tell.
	replacing := ""
	switch {
	case tag != "" || installed == "" || installed == remote.Tag:
	case buildPublished != "true" && runsActive(c):
		replacing = "Replacing local build " + installed + " with " + remote.Tag +
			", the latest published release"
	case release.Newer(installed, remote.Tag):
		return nil, "", nil
	}
	c.Step("downloading " + remote.Tag)
	updateViewOf(c).phase(phaseDownloading)
	bundle, err := remote.Bundle(cmd.Context(), c.Transfer("workbench"))
	if err != nil {
		return nil, "", err
	}
	return &bundle, replacing, nil
}

// runsActive reports whether this program is the active release's executable.
func runsActive(c operation.Context) bool {
	state, err := operation.ReadState(c.Paths)
	if err != nil || state == nil || state.ActiveRelease == nil {
		return false
	}
	running, err := os.Executable()
	if err != nil {
		return false
	}
	running, err = filepath.EvalSymlinks(running)
	active, activeErr := filepath.EvalSymlinks(state.ActiveRelease.Executable)
	return err == nil && activeErr == nil && running == active
}

// stageRelease stages and activates bundle, then removes what the approved
// plan lists as no longer needed, adding a warning for each removal that
// failed to warnings.
func stageRelease(
	cmd *cobra.Command,
	c operation.Context,
	bundle release.Bundle,
	plan operation.Plan,
	warnings *[]string,
) func(*operation.Mutation) error {
	return func(m *operation.Mutation) error {
		directory, err := release.Stage(c, m, bundle)
		if err != nil {
			return err
		}
		if err = release.Activate(cmd.Context(), c, m, bundle, directory); err != nil {
			return err
		}
		*warnings = removeStale(m, plan)
		return nil
	}
}

// releasePlanner previews staging bundle and activating it as the workbench
// command.
func releasePlanner(
	c operation.Context,
	bundle release.Bundle,
) func(context.Context, operation.Context) (operation.Plan, error) {
	return func(_ context.Context, current operation.Context) (operation.Plan, error) {
		plan, planErr := release.StagePlan(current, bundle)
		plan.Edits = append(
			plan.Edits,
			operation.Edit{
				Path:        filepath.Join(c.Paths.Bin, "workbench"),
				Action:      "activate",
				Description: "Activate this verified CLI/source pair; retain previous runtime and applied-configuration identity",
			},
		)
		plan.RecoveryLimits = append(
			plan.RecoveryLimits,
			"Activation spans a journal, state record and entry point; interruption fails closed and requires rerunning this update",
		)
		if planErr == nil {
			var removals []operation.Edit
			removals, planErr = retentionEdits(current, bundle)
			plan.Edits = append(plan.Edits, removals...)
		}
		return plan, planErr
	}
}

// retentionEdits lists what activating bundle makes unnecessary: the releases
// it does not keep, plus the setup contexts and private tool versions that no
// kept release uses.
func retentionEdits(c operation.Context, bundle release.Bundle) ([]operation.Edit, error) {
	releases, kept, err := release.StaleReleases(c, bundle)
	if len(kept) == 0 || err != nil {
		return nil, err
	}
	setup, err := machine.StaleSetup(c, kept)
	return append(releases, setup...), err
}

// removeStale deletes the approved removals after activation and returns a
// warning for each that failed: the directory stays and the next activation
// lists it again.
func removeStale(m *operation.Mutation, plan operation.Plan) []string {
	var warnings []string
	for _, edit := range plan.Edits {
		if edit.Action != "remove" {
			continue
		}
		if err := m.RemovePrivateDirectory(edit.Path); err != nil {
			warnings = append(warnings, fmt.Sprintf("could not remove %s: %v", edit.Path, err))
		}
	}
	return warnings
}

// continueInstall runs in the activated runtime after the handoff. The parent
// verified, staged and activated this release, or found it already active
// (unchanged), so nothing is re-read from the original bundle location.
func continueInstall(
	cmd *cobra.Command,
	c operation.Context,
	o *options,
	result operation.Result,
	unchanged, first bool,
) (operation.Result, error) {
	// The view starts on the line the starting release cleared, before any
	// check that can fail; with the release unchanged it is still checking
	// until a tool is fetched.
	phase := phaseInstalling
	if unchanged {
		phase = phaseChecking
	}
	updateViewOf(c).phase(phase)
	state, stateErr := operation.ReadState(c.Paths)
	actual, executableErr := os.Executable()
	if stateErr != nil || executableErr != nil || state == nil || state.ActiveRelease == nil ||
		actual != state.ActiveRelease.Executable {
		return result, operation.Fail(
			operation.ExitConflict,
			"handoff",
			"Runtime continuation must execute the activated runtime",
		)
	}
	if err := release.ValidateSelection(c); err != nil {
		return result, err
	}
	metadata, err := release.Inspect(state.ActiveRelease.Source)
	if err != nil {
		return result, err
	}
	installed := operation.Component{
		Name:    "release",
		Status:  operation.StatusComplete,
		Message: metadata.Release + " (" + metadata.Target + ")",
	}
	if unchanged {
		// Nothing was installed, so a later refusal or failure is not partial.
		installed.Status = operation.StatusUnchanged
		installed.Message = metadata.Release + " is already installed"
	}
	result.Results = append(result.Results, installed)
	c.Native.Source, c.Native.Developer = state.ActiveRelease.Source, false
	return installTools(cmd, c, o, result, metadata.Release, unchanged, first)
}

// handoffArgs repeats the selections for the activated runtime, which
// continues this install with --runtime-ready and whether the release was
// installed now or unchanged.
func handoffArgs(o *options, install string) []string {
	args := []string{"update", "--runtime-ready", install}
	if o.nonInteractive {
		args = append(args, "--non-interactive")
	}
	if o.json {
		args = append(args, "--json")
	}
	if o.verbose {
		args = append(args, "--verbose")
	}
	if o.resolve.Destination != "" {
		args = append(args, "--destination", o.resolve.Destination)
	}
	return args
}

// installTools runs in the activated runtime: it puts the pinned tools in
// place, without asking, and stops. Applying the machine is apply's job.
func installTools(
	cmd *cobra.Command,
	c operation.Context,
	o *options,
	result operation.Result,
	version string,
	unchanged, first bool,
) (operation.Result, error) {
	if err := setUpTools(cmd, c, o, &result); err != nil {
		return result, err
	}
	// The summary, printed where the view is not drawn, says outright that
	// nothing was applied.
	next := "; nothing was applied: run " + c.WorkbenchCommand() + " apply"
	result.Summary = "[WorkBench] Installed " + version + " and its tools" + next
	outcome := "Updated to " + version
	if first {
		outcome = "Installed " + version
	}
	if unchanged {
		result.Summary = "[WorkBench] " + version + " is already installed" + next
		outcome = "Up to date"
	}
	updateViewOf(c).finish(outcome, !unchanged)
	return result, nil
}

// setUp installs the pinned tools Workbench runs, when missing, and, with
// questions, asks the machine questions that the saved answers lack or `answers ask`
// names. Running apply or update is the go-ahead: the tools go into
// Workbench's own directory, and a question asks only once.
func setUp(
	cmd *cobra.Command,
	c operation.Context,
	o *options,
	terminal *os.File,
	result *operation.Result,
	questions bool,
	ask []string,
) (operation.Context, error) {
	planner := machine.ToolsPlan
	if questions {
		planner = machine.SetupPlan
	}
	if questions && terminal == nil {
		if len(ask) > 0 {
			return c, operation.Fail(
				operation.ExitBlocked,
				"terminal",
				"answers ask needs a terminal to ask at",
			)
		}
		saved, err := machine.ReadMachineConfig(c.Native.Config)
		if err != nil {
			return c, operation.Fail(
				operation.ExitBlocked,
				"answers",
				"Without a terminal, setup needs saved answers; save them with "+
					c.WorkbenchCommand()+" answers import FILE",
			)
		}
		// Native init cannot ask here, and a question it reaches fails with
		// a bare EOF, so a missing answer is refused now, by name.
		if err = machine.Unasked(c, saved); err != nil {
			return c, err
		}
	}
	setup, err := planner(cmd.Context(), c)
	if err != nil {
		return c, err
	}
	err = operation.WithMutation(
		cmd.Context(),
		c,
		setup,
		operation.Consent{ApprovedDigest: setup.Digest(), CompleteInputs: true},
		planner,
		func(m *operation.Mutation) error {
			if !questions {
				return machine.InstallTools(cmd.Context(), c, m)
			}
			var setupErr error
			c, setupErr = machine.Setup(cmd.Context(), c, m, terminal, ask)
			return setupErr
		},
	)
	if err != nil {
		return c, err
	}
	tools := map[string]string{
		"private-chezmoi": "chezmoi",
		"private-uv":      "uv",
		"private-python3": "Python",
		"private-tomlkit": "TOML Kit",
	}
	var installed []string
	for _, effect := range setup.Effects {
		if name, ok := tools[effect.Name]; ok {
			installed = append(installed, name)
		}
	}
	// Setup plans the answers as an input only when they were saved complete,
	// so without it, or with `answers ask`, the questionnaire saved new ones.
	answered := !questions
	for _, input := range setup.Inputs {
		answered = answered || input.Name == "answers" && len(ask) == 0
	}
	component := operation.Component{
		Name:    "setup",
		Status:  operation.StatusUnchanged,
		Message: "Tools already in place",
	}
	switch {
	case len(installed) > 0:
		component.Status = operation.StatusComplete
		component.Message = "Installed " + strings.Join(installed, ", ")
	case !answered:
		component.Status = operation.StatusComplete
		component.Message = "Saved your machine answers"
	}
	if !o.interactive() {
		component.Details = setup
	}
	result.Results = append(result.Results, component)
	return c, nil
}

// setUpTools is the setup step of an update, which installs the pinned tools
// and applies nothing: its plan stays in --json and out of the text output,
// whose setup line already names what was installed.
func setUpTools(
	cmd *cobra.Command,
	c operation.Context,
	o *options,
	result *operation.Result,
) error {
	_, err := setUp(cmd, c, o, nil, result, false, nil)
	if last := len(result.Results) - 1; err == nil && !o.json && last >= 0 {
		result.Results[last].Details = nil
	}
	return err
}

// partialUpdate rewrites a failure after this release was installed as a
// partial one, saying so; an interruption keeps its own exit.
func partialUpdate(command string, result *operation.Result, resultErr *error) {
	if *resultErr == nil || operation.ExitCode(*resultErr) == operation.ExitInterrupted {
		return
	}
	for _, component := range result.Results {
		if component.Name == "release" && component.Status == operation.StatusComplete {
			redacted := operation.NewResult(command)
			redacted.SetError(*resultErr)
			problem := redacted.Errors[0]
			*resultErr = operation.Fail(
				operation.ExitPartial,
				problem.Category,
				"Workbench is installed, but the rest of the update did not finish: "+problem.Message,
			)
			return
		}
	}
}

// localBuildInput refuses what --local-build does not take: VERSION,
// --bundle and --runtime-ready. It accepts anything without --local-build.
func localBuildInput(cmd *cobra.Command, developer bool) error {
	if !developer {
		return nil
	}
	if len(cmd.Flags().Args()) > 0 {
		return operation.Fail(
			operation.ExitInvalid,
			"input",
			"--local-build installs the checkout's tools; drop VERSION",
		)
	}
	for _, name := range []string{"bundle", "runtime-ready"} {
		if cmd.Flags().Changed(name) {
			return operation.Fail(
				operation.ExitInvalid,
				"input",
				"--local-build installs the checkout's tools; drop --"+name,
			)
		}
	}
	return nil
}

// releaseTag is update's VERSION, "" for the latest. Release tags start with
// v; 0.2.0 is accepted for v0.2.0.
func releaseTag(cmd *cobra.Command) (string, error) {
	args := cmd.Flags().Args()
	if len(args) == 0 {
		return "", nil
	}
	tag := args[0]
	if tag != "" && tag[0] >= '0' && tag[0] <= '9' {
		tag = "v" + tag
	}
	if !operation.ValidReleaseName(tag) {
		return "", operation.Fail(
			operation.ExitInvalid,
			"input",
			"VERSION must be a release tag such as v0.2.0",
		)
	}
	return tag, nil
}

// announceHandoff is what runs just before the activated release takes the
// terminal, once every check has passed: it clears the update view's working
// line, where the activated release draws on, then prints the warnings, or
// without the view prints them and says in a line what happens next, so a
// log stays readable. The warnings wait until the view has ended, which owns
// the lines it draws. It empties warnings once printed.
func announceHandoff(
	cmd *cobra.Command,
	c operation.Context,
	done string,
	warnings *[]string,
) func() {
	return func() {
		view := updateViewOf(c)
		if view != nil {
			view.handoff()
		}
		if warnings != nil {
			printWarnings(cmd, *warnings)
			*warnings = nil
		}
		if view == nil {
			_, _ = fmt.Fprintln(
				cmd.ErrOrStderr(),
				"[WorkBench] "+done+"; continuing with its tools",
			)
		}
	}
}

// printWarnings prints each warning on its own line of stderr.
func printWarnings(cmd *cobra.Command, warnings []string) {
	warn := newPainter(cmd.ErrOrStderr())
	for _, warning := range warnings {
		_, _ = fmt.Fprintln(cmd.ErrOrStderr(), warn.style(yellow, "Warning:"), warning)
	}
}

// startUpdateView heads the update view, if c's progress draws one, with the
// release the update starts from (or the checkout, with --local-build), and
// starts it checking.
func startUpdateView(c operation.Context) error {
	view := updateViewOf(c)
	if view == nil {
		return nil
	}
	installed, err := installedRelease(c)
	if err != nil {
		return err
	}
	if c.Native.Developer {
		installed = "this checkout" // --local-build installs no release
	}
	view.header(installed)
	view.phase(phaseChecking)
	return nil
}

// updateViewOf is the update view c's progress draws, or nil when it draws
// none (not a terminal, --json, --non-interactive or --dry-run). Its phase
// and finish do nothing on nil.
func updateViewOf(c operation.Context) *updateView {
	view, _ := c.Progress.(*updateView)
	return view
}
