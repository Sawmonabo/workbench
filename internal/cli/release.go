package cli

import (
	"context"
	"os"
	"path/filepath"

	"github.com/Sawmonabo/workbench/internal/machine"
	"github.com/Sawmonabo/workbench/internal/operation"
	"github.com/Sawmonabo/workbench/internal/release"
	"github.com/spf13/cobra"
)

var buildReleaseVersion = "dev"

func releaseCommands(o *options) []*cobra.Command {
	commands := []*cobra.Command{}
	for _, name := range []string{"pull", "install", "update"} {
		cmd := &cobra.Command{
			Use:   name + " [version]",
			Args:  cobra.MaximumNArgs(1),
			Short: map[string]string{"pull": "Verify and stage an immutable release only", "install": "Install a verified runtime and perform separately approved setup", "update": "Stage, plan and apply a verified release through shared lifecycle operations"}[name],
		}
		cmd.Flags().
			String("bundle", "", "Release archive: a local file or HTTPS URL (install.sh downloads the right one)")
		cmd.Flags().
			Bool("dry-run", false, "Verify the bundle and show only the runtime staging/activation plan")
		cmd.Flags().
			Bool("install-only", false, "Install the CLI and sources without management setup or configuration")
		cmd.Flags().
			Bool("config-only", false, "Apply configuration without provisioning; missing dependencies remain blocked")
		addEffectFlag(cmd)
		cmd.Flags().String("approve-setup", "", "Approve exactly the separate setup plan digest")
		cmd.Flags().
			String("approve-apply", "", "Approve exactly the subsequent native target plan digest")
		cmd.Flags().
			Bool("runtime-ready", false, "Continue the verified same-process runtime handoff")
		_ = cmd.Flags().MarkHidden("runtime-ready")
		cmd.RunE = o.action(
			false,
			func(cmd *cobra.Command, c operation.Context) (operation.Result, error) {
				return releaseLifecycle(cmd, c, o)
			},
		)
		commands = append(commands, cmd)
	}
	check := &cobra.Command{Use: "release-check", Hidden: true, Args: cobra.NoArgs}
	check.Flags().String("bundle-directory", "", "Verified private extracted bundle")
	check.RunE = o.action(
		false,
		func(cmd *cobra.Command, c operation.Context) (operation.Result, error) {
			result := operation.NewResult(cmd.CommandPath())
			directory, _ := cmd.Flags().GetString("bundle-directory")
			metadata, err := release.Inspect(directory)
			if err != nil {
				return result, err
			}
			_, identity, err := machine.SourceSnapshot(directory)
			if err != nil {
				return result, err
			}
			if metadata.SourceDigest != identity.ContentDigest {
				return result, operation.Fail(
					2,
					"release_identity",
					"Candidate executable and source identities differ",
				)
			}
			result.Results = append(
				result.Results,
				operation.Component{
					Name:    "release-check",
					Status:  "complete",
					Details: metadata.Release,
				},
			)
			return result, nil
		},
	)
	return append(commands, check)
}

func releaseConsent(o *options, digest string) operation.Consent {
	consent := operation.Consent{
		ApprovedDigest: digest,
		NonInteractive: o.nonInteractive || o.json,
		CompleteInputs: true,
	}
	if !o.nonInteractive && !o.json {
		consent.Confirm = ConfirmPlan
	}
	return consent
}

func releaseLifecycle(
	cmd *cobra.Command,
	c operation.Context,
	o *options,
) (result operation.Result, resultErr error) {
	result = operation.NewResult(cmd.CommandPath())
	defer func() {
		if resultErr == nil || operation.ExitCode(resultErr) == 130 {
			return
		}
		for _, component := range result.Results {
			if component.Name == "release" && component.Status == "complete" {
				redacted := operation.NewResult(cmd.CommandPath())
				redacted.SetError(resultErr)
				problem := redacted.Errors[0]
				resultErr = operation.Fail(
					5,
					problem.Category,
					"Runtime installed; requested later stages are incomplete: "+problem.Message,
				)
				return
			}
		}
	}()
	c.ReadOnly = false
	if ready, _ := cmd.Flags().GetBool("runtime-ready"); ready {
		// The activated runtime continues the install that verified, staged and
		// activated it; it re-reads nothing from the original bundle location.
		state, stateErr := operation.ReadState(c.Paths)
		actual, executableErr := os.Executable()
		if stateErr != nil || executableErr != nil || state == nil || state.ActiveRelease == nil ||
			actual != state.ActiveRelease.Executable {
			return result, operation.Fail(
				4,
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
		result.Results = append(
			result.Results,
			operation.Component{
				Name:    "release",
				Status:  "complete",
				Message: metadata.Release + " (" + metadata.Target + ")",
			},
		)
		c.Native.Source = state.ActiveRelease.Source
		return configureMachine(cmd, c, o, result)
	}
	location, _ := cmd.Flags().GetString("bundle")
	if location == "" {
		return result, operation.Fail(
			3,
			"release_unavailable",
			"Supply --bundle with a release archive; the one-line install.sh downloads one",
		)
	}
	version := ""
	if len(cmd.Flags().Args()) > 0 {
		version = cmd.Flags().Args()[0]
	}
	bundle, err := release.ReadBundle(cmd.Context(), location, version)
	if err != nil {
		return result, err
	}
	activate := cmd.Name() != "pull"
	planner := func(_ context.Context, current operation.Context) (operation.Plan, error) {
		plan, planErr := release.StagePlan(current, bundle)
		if activate {
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
				"Activation spans a journal, state record and entry point; interruption fails closed and requires resuming this installer",
			)
		}
		return plan, planErr
	}
	plan, err := planner(cmd.Context(), c)
	if err != nil {
		return result, err
	}
	result.PlanDigest, _ = plan.Digest()
	result.Results = append(
		result.Results,
		operation.Component{Name: "release-plan", Status: "complete", Details: plan},
	)
	if dryRun, _ := cmd.Flags().GetBool("dry-run"); dryRun {
		return result, nil
	}
	err = operation.WithMutation(
		cmd.Context(),
		c,
		plan,
		releaseConsent(o, o.approvePlan),
		planner,
		func(m *operation.Mutation) error {
			directory, stageErr := release.Stage(c, m, bundle)
			if stageErr != nil {
				return stageErr
			}
			if activate {
				return release.Activate(cmd.Context(), c, m, bundle, directory)
			}
			return nil
		},
	)
	if err != nil {
		return result, err
	}
	result.Results = append(
		result.Results,
		operation.Component{Name: "release", Status: "complete", Message: bundle.String()},
	)
	installOnly, _ := cmd.Flags().GetBool("install-only")
	if !activate || installOnly {
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
	args := []string{cmd.Name(), "--runtime-ready"}
	for _, name := range []string{"approve-setup", "approve-apply"} {
		if value, _ := cmd.Flags().GetString(name); value != "" {
			args = append(args, "--"+name, value)
		}
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
	return result, operation.Handoff(c, *state.ActiveRelease, args, "")
}

// configureMachine runs the separately approved setup and apply stages inside
// the activated runtime.
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
		setup, setupErr := machine.SetupPlan(cmd.Context(), c)
		if setupErr != nil {
			return result, setupErr
		}
		setupDigest, _ := setup.Digest()
		result.PlanDigest = setupDigest
		result.Results = append(
			result.Results,
			operation.Component{Name: "setup-plan", Status: "complete", Details: setup},
		)
		approved, _ := cmd.Flags().GetString("approve-setup")
		if o.nonInteractive || o.json {
			if _, readErr := operation.ReadPrivateInput(c.Native.Config, 1<<20); readErr != nil {
				return result, operation.Fail(
					3,
					"answers",
					"Runtime installed; unattended setup requires complete private answers and a separate --approve-setup digest",
				)
			}
		}
		if !o.nonInteractive && !o.json && terminal == nil {
			return result, operation.Fail(
				3,
				"terminal",
				"Runtime installed; native setup requires a terminal or complete unattended inputs",
			)
		}
		err := operation.WithMutation(
			cmd.Context(),
			c,
			setup,
			releaseConsent(o, approved),
			machine.SetupPlan,
			func(m *operation.Mutation) error {
				var setupErr error
				c, setupErr = machine.Setup(cmd.Context(), c, m, terminal)
				return setupErr
			},
		)
		if err != nil {
			return result, err
		}
	}
	selection := machineSelection(cmd)
	applyPlan, err := machine.Plan(cmd.Context(), c, selection)
	if err != nil {
		return result, err
	}
	result.PlanDigest, _ = applyPlan.Digest()
	result.Results = append(
		result.Results,
		operation.Component{Name: "machine-plan", Status: "complete", Details: applyPlan},
	)
	approved, _ := cmd.Flags().GetString("approve-apply")
	applied, err := machine.Apply(
		cmd.Context(),
		c,
		selection,
		applyPlan,
		releaseConsent(o, approved),
		terminal,
		progress,
	)
	result.Results = append(result.Results, applied.Results...)
	result.OperationID = applied.OperationID
	return result, err
}
