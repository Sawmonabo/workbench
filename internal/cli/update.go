package cli

import (
	"cmp"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/Sawmonabo/workbench/internal/machine"
	"github.com/Sawmonabo/workbench/internal/operation"
	"github.com/Sawmonabo/workbench/internal/release"
	"github.com/spf13/cobra"
)

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
	var warnings []operation.Warning
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
	result.Summary = brandTag + " This checkout's tools are in place; run " +
		c.WorkbenchCommand() + " apply --local-build"
	outcome := "Up to date: this checkout's tools"
	installed := slices.ContainsFunc(result.Results, func(component operation.Component) bool {
		return component.Name == "setup" && component.Status == operation.StatusComplete
	})
	if installed {
		result.Summary = brandTag + " Installed this checkout's tools; run " +
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
	warnings *[]operation.Warning,
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
				Path:        c.Paths.Entrypoint(),
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
func removeStale(m *operation.Mutation, plan operation.Plan) []operation.Warning {
	var warnings []operation.Warning
	for _, edit := range plan.Edits {
		if edit.Action != "remove" {
			continue
		}
		if err := m.RemovePrivateDirectory(edit.Path); err != nil {
			warnings = append(
				warnings,
				operation.Warn(fmt.Sprintf("could not remove %s: %v", edit.Path, err)),
			)
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
	result.Summary = brandTag + " Installed " + version + " and its tools" + next
	outcome := "Updated to " + version
	if first {
		outcome = "Installed " + version
	}
	if unchanged {
		result.Summary = brandTag + " " + version + " is already installed" + next
		outcome = "Up to date"
	}
	updateViewOf(c).finish(outcome, !unchanged)
	return result, nil
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
			*resultErr = operation.FailWith(
				operation.ExitPartial,
				problem.Category,
				"Workbench is installed, but the rest of the update did not finish",
				operation.Explain(operation.Cause(problem.Message)).With(problem.Body),
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
			"--local-build installs the checkout's tools",
			operation.FixText("drop VERSION"),
		)
	}
	for _, name := range []string{"bundle", "runtime-ready"} {
		if cmd.Flags().Changed(name) {
			return operation.Fail(
				operation.ExitInvalid,
				"input",
				"--local-build installs the checkout's tools",
				operation.FixText("drop --"+name),
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
