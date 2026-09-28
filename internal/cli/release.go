package cli

import (
	"cmp"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/Sawmonabo/workbench/internal/machine"
	"github.com/Sawmonabo/workbench/internal/operation"
	"github.com/Sawmonabo/workbench/internal/release"
	"github.com/spf13/cobra"
)

var buildReleaseVersion = "dev"

// updateCommand installs the latest release, or VERSION, then applies it.
func updateCommand(o *options) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "update [VERSION]",
		Short: "Install the latest Workbench release, or VERSION, then apply it",
		Long: "Install the latest Workbench release, or VERSION (an older one goes back), " +
			"set up its tools, then show the plan for this machine and ask before applying it. " +
			"Installing Workbench and its own tools changes only Workbench's files and keeps " +
			"the previous release, so running update is the go-ahead for them. " +
			"See the releases with workbench version --list.",
		Args: cobra.MaximumNArgs(1),
		RunE: o.action(
			releaseAction,
			func(cmd *cobra.Command, c operation.Context) (operation.Result, error) {
				return updateRelease(cmd, c, o)
			},
		),
	}
	cmd.Flags().Bool("dry-run", false, "Show the install plan without installing")
	cmd.Flags().
		Bool("install-only", false, "Install Workbench without setting up tools or applying")
	cmd.Flags().
		Bool("config-only", false, "Apply configuration without provisioning; missing tools stay blocked")
	addEffectFlag(cmd)
	o.machineConfigFlag(cmd)
	o.destinationFlag(cmd)
	o.approveFlag(cmd)
	cmd.Flags().String("bundle", "", "Install this release archive, a local file or HTTPS URL")
	_ = cmd.Flags().MarkHidden("bundle") // install.sh and offline installs
	cmd.Flags().String(
		"runtime-ready",
		"",
		"Continue the verified runtime handoff after an install that is new or unchanged",
	)
	_ = cmd.Flags().MarkHidden("runtime-ready")
	return cmd
}

// versionCommand prints this version, or with --list the published releases.
func versionCommand(o *options) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "version",
		Short: "Show this Workbench version; --list shows every release",
		Args:  cobra.NoArgs,
		RunE: o.action(
			releaseAction,
			func(cmd *cobra.Command, c operation.Context) (operation.Result, error) {
				result := operation.NewResult(cmd.CommandPath())
				if list, _ := cmd.Flags().GetBool("list"); !list {
					if !o.json {
						// The same line as --version.
						_, err := fmt.Fprintln(cmd.OutOrStdout(), cmd.Root().Name()+" "+version())
						return result, err
					}
					result.Results = append(result.Results, operation.Component{
						Name:    "workbench",
						Status:  operation.StatusComplete,
						Message: version(),
					})
					return result, nil
				}
				stop := c.ShowProgress("Asking GitHub for releases")
				published, err := release.Releases(cmd.Context(), c)
				stop()
				if err != nil {
					return result, err
				}
				installed, err := installedRelease(c)
				if err != nil {
					return result, err
				}
				result.Results = append(result.Results, operation.Component{
					Name:    "releases",
					Status:  operation.StatusComplete,
					Details: releaseList{Releases: published, Installed: installed},
				})
				return result, nil
			},
		),
	}
	cmd.Flags().Bool("list", false, "List the published releases, marking the installed and latest")
	return cmd
}

// releaseList is the published releases, newest first, and the installed tag.
type releaseList struct {
	Releases  []release.Published `json:"releases"`
	Installed string              `json:"installed"`
}

// writeReleases prints one release per line, newest first, marking the
// latest and the installed one with *.
func writeReleases(w io.Writer, list releaseList) error {
	if len(list.Releases) == 0 {
		_, err := fmt.Fprintln(w, "  No releases published yet")
		return err
	}
	rows := make([][]string, 0, len(list.Releases))
	for i, published := range list.Releases {
		mark, notes := " ", []string{}
		if i == 0 {
			notes = append(notes, "latest")
		}
		if published.Tag == list.Installed {
			mark = "*"
			notes = append(notes, "installed")
		}
		rows = append(rows, []string{
			mark + " " + published.Tag,
			published.Published.Local().Format("Jan 2, 2006"),
			strings.Join(notes, ", "),
		})
	}
	var b strings.Builder
	writeColumns(&b, rows)
	_, err := io.WriteString(w, b.String())
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
	check := &cobra.Command{Use: "release-check", Hidden: true, Args: cobra.NoArgs}
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
// a release, then hands off to it to set up tools and apply.
func updateRelease(
	cmd *cobra.Command,
	c operation.Context,
	o *options,
) (result operation.Result, resultErr error) {
	result = operation.NewResult(cmd.CommandPath())
	defer func() {
		if resultErr == nil || operation.ExitCode(resultErr) == operation.ExitInterrupted {
			return
		}
		for _, component := range result.Results {
			if component.Name == "release" && component.Status == operation.StatusComplete {
				redacted := operation.NewResult(cmd.CommandPath())
				redacted.SetError(resultErr)
				problem := redacted.Errors[0]
				resultErr = operation.Fail(
					operation.ExitPartial,
					problem.Category,
					"Workbench is installed, but the rest of the update did not finish: "+problem.Message,
				)
				return
			}
		}
	}()
	c.ReadOnly = false
	if ready, _ := cmd.Flags().GetString("runtime-ready"); ready != "" {
		return continueInstall(cmd, c, o, result, ready == "unchanged")
	}
	tag := ""
	if args := cmd.Flags().Args(); len(args) > 0 {
		tag = args[0]
		// Release tags start with v; accept 0.2.0 for v0.2.0.
		if tag[0] >= '0' && tag[0] <= '9' {
			tag = "v" + tag
		}
	}
	bundle, err := readRelease(cmd, c, tag)
	if err != nil {
		return result, err
	}
	if bundle == nil {
		// Already installed: still set up and apply, so rerunning update
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
		operation.Component{Name: "release-plan", Status: operation.StatusComplete, Details: plan},
	)
	if dryRun, _ := cmd.Flags().GetBool("dry-run"); dryRun {
		return result, nil
	}
	// Running update is the go-ahead to install Workbench itself: it changes
	// only Workbench's own files and keeps the release it replaces. The
	// recheck under the locks still refuses a plan that changed.
	stop := c.ShowProgress("Installing Workbench " + bundle.Metadata.Release)
	err = operation.WithMutation(
		cmd.Context(),
		c,
		plan,
		operation.Consent{ApprovedDigest: result.PlanDigest, CompleteInputs: true},
		planner,
		stageRelease(cmd, c, *bundle, plan),
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
	if installOnly, _ := cmd.Flags().GetBool("install-only"); installOnly {
		result.Summary = "Installed Workbench " + bundle.Metadata.Release
		return result, nil
	}
	// No operation locks or transient extraction files survive this boundary.
	if _, err = release.Inspect(bundle.Directory(c)); err != nil {
		return result, err
	}
	state, err := operation.ReadState(c.Paths)
	if err != nil {
		return result, err
	}
	_, _ = fmt.Fprintf(
		cmd.ErrOrStderr(),
		"Installed Workbench %s; continuing with setup and apply\n",
		bundle.Metadata.Release,
	)
	return result, operation.Handoff(c, *state.ActiveRelease, handoffArgs(cmd, o, "installed"))
}

// continueInstalled hands off to the already active release to set up its tools
// and apply it.
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
	dryRun, _ := cmd.Flags().GetBool("dry-run")
	installOnly, _ := cmd.Flags().GetBool("install-only")
	if dryRun || installOnly {
		if installOnly {
			result.Summary = "Workbench " + active.Identity.Release + " is already installed"
		}
		return result, nil
	}
	_, _ = fmt.Fprintf(
		cmd.ErrOrStderr(),
		"Workbench %s is already installed; continuing with setup and apply\n",
		active.Identity.Release,
	)
	return result, operation.Handoff(c, active, handoffArgs(cmd, o, "unchanged"))
}

// readRelease reads --bundle, or finds and downloads release tag (the latest
// when empty) for this machine. It returns nil when that release is already
// the active one, unless an interrupted activation still needs finishing.
func readRelease(cmd *cobra.Command, c operation.Context, tag string) (*release.Bundle, error) {
	installed, err := installedRelease(c)
	if err != nil {
		return nil, err
	}
	current := func(found string) bool {
		return found == installed && release.ValidateSelection(c) == nil
	}
	if location, _ := cmd.Flags().GetString("bundle"); location != "" {
		bundle, err := release.ReadBundle(cmd.Context(), location, tag)
		if err != nil || current(bundle.Metadata.Release) {
			return nil, err
		}
		return &bundle, nil
	}
	stop := c.ShowProgress("Getting Workbench " + cmp.Or(tag, "latest"))
	defer stop()
	c.Step("asking GitHub for the release")
	remote, err := release.Find(cmd.Context(), c, tag)
	if err != nil || current(remote.Tag) {
		return nil, err
	}
	c.Step("downloading " + remote.Tag)
	bundle, err := remote.Bundle(cmd.Context())
	if err != nil {
		return nil, err
	}
	return &bundle, nil
}

// stageRelease stages and activates bundle, then removes what the approved
// plan lists as no longer needed.
func stageRelease(
	cmd *cobra.Command,
	c operation.Context,
	bundle release.Bundle,
	plan operation.Plan,
) func(*operation.Mutation) error {
	return func(m *operation.Mutation) error {
		directory, err := release.Stage(c, m, bundle)
		if err != nil {
			return err
		}
		if err = release.Activate(cmd.Context(), c, m, bundle, directory); err != nil {
			return err
		}
		removeStale(m, plan, cmd.ErrOrStderr())
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

// removeStale deletes the approved removals after activation. A failure only
// warns: the directory stays and the next activation lists it again.
func removeStale(m *operation.Mutation, plan operation.Plan, diagnostics io.Writer) {
	for _, edit := range plan.Edits {
		if edit.Action != "remove" {
			continue
		}
		if err := m.RemovePrivateDirectory(edit.Path); err != nil {
			_, _ = fmt.Fprintf(diagnostics, "Warning: could not remove %s: %v\n", edit.Path, err)
		}
	}
}

// continueInstall runs in the activated runtime after the handoff. The parent
// verified, staged and activated this release, or found it already active
// (unchanged), so nothing is re-read from the original bundle location.
func continueInstall(
	cmd *cobra.Command,
	c operation.Context,
	o *options,
	result operation.Result,
	unchanged bool,
) (operation.Result, error) {
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
	return configureMachine(cmd, c, o, result)
}

// handoffArgs repeats the setup and apply selections for the activated
// runtime, which continues this install with --runtime-ready and whether the
// release was installed now or unchanged.
func handoffArgs(cmd *cobra.Command, o *options, install string) []string {
	args := []string{cmd.Name(), "--runtime-ready", install}
	if o.approvePlan != "" {
		args = append(args, "--approve-plan", o.approvePlan)
	}
	if o.nonInteractive {
		args = append(args, "--non-interactive")
	}
	if o.json {
		args = append(args, "--json")
	}
	if o.resolve.MachineConfig != "" {
		args = append(args, "--machine-config", o.resolve.MachineConfig)
	}
	if o.resolve.Destination != "" {
		args = append(args, "--destination", o.resolve.Destination)
	}
	if configOnly, _ := cmd.Flags().GetBool("config-only"); configOnly {
		args = append(args, "--config-only")
	}
	effects, _ := cmd.Flags().GetStringArray("effect")
	for _, effect := range effects {
		args = append(args, "--effect", effect)
	}
	return args
}

// configureMachine sets up Workbench's own tools, then shows the machine plan
// and asks before applying it, inside the activated runtime.
func configureMachine(
	cmd *cobra.Command,
	c operation.Context,
	o *options,
	result operation.Result,
) (operation.Result, error) {
	configOnly, _ := cmd.Flags().GetBool("config-only")
	terminal, progress, closeConsole := nativeConsole(o, cmd.ErrOrStderr())
	defer closeConsole()
	if !configOnly {
		var err error
		c, err = setUp(cmd, c, o, terminal, &result)
		if err != nil {
			return result, err
		}
	}
	selection := machineSelection(cmd)
	applyPlan, err := machine.Plan(cmd.Context(), c, selection)
	if err != nil {
		return result, err
	}
	result.PlanDigest = applyPlan.Digest()
	if !o.interactive() {
		// At a terminal the approval prompt shows the plan instead.
		result.Results = append(result.Results, operation.Component{
			Name:    "machine-plan",
			Status:  operation.StatusComplete,
			Details: applyPlan,
		})
	}
	applied, err := machine.Apply(
		cmd.Context(),
		c,
		selection,
		applyPlan,
		consentFor(o, o.approvePlan),
		terminal,
		progress,
	)
	result.Results = append(result.Results, applied.Results...)
	result.OperationID = applied.OperationID
	if err == nil {
		matches := "your machine matches it"
		if selection.ConfigOnly {
			matches = "your configuration files match it"
		}
		// The first component is the release this run installed or found.
		result.Summary = "Updated to Workbench " + applyPlan.Source.Release + ", and " + matches
		if result.Results[0].Status == operation.StatusUnchanged {
			result.Summary = "Workbench " + applyPlan.Source.Release + " is current, and " + matches
		}
	}
	return result, err
}

// setUp installs the pinned tools Workbench runs, when missing, and asks the
// machine questions that the saved answers lack. Running update is the
// go-ahead: the tools go into Workbench's own directory, and a question asks
// only once.
func setUp(
	cmd *cobra.Command,
	c operation.Context,
	o *options,
	terminal *os.File,
	result *operation.Result,
) (operation.Context, error) {
	setup, err := machine.SetupPlan(cmd.Context(), c)
	if err != nil {
		return c, err
	}
	if !o.interactive() {
		if _, readErr := operation.ReadPrivateInput(c.Native.Config, 1<<20); readErr != nil {
			return c, operation.Fail(
				operation.ExitBlocked,
				"answers",
				"Unattended setup requires saved private answers or --machine-config",
			)
		}
	} else if terminal == nil {
		return c, operation.Fail(
			operation.ExitBlocked,
			"terminal",
			"Setup requires a terminal or complete unattended inputs",
		)
	}
	err = operation.WithMutation(
		cmd.Context(),
		c,
		setup,
		operation.Consent{ApprovedDigest: setup.Digest(), CompleteInputs: true},
		machine.SetupPlan,
		func(m *operation.Mutation) error {
			var setupErr error
			c, setupErr = machine.Setup(cmd.Context(), c, m, terminal)
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
	component := operation.Component{
		Name:    "setup",
		Status:  operation.StatusComplete,
		Message: "Tools already in place",
	}
	if !o.interactive() {
		component.Details = setup
	}
	if len(installed) > 0 {
		component.Message = "Installed " + strings.Join(installed, ", ")
	}
	result.Results = append(result.Results, component)
	return c, nil
}
