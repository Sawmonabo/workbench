package cli

import (
	"context"
	"time"

	"github.com/Sawmonabo/workbench/internal/operation"
	"github.com/Sawmonabo/workbench/internal/project"
	"github.com/spf13/cobra"
)

func projectCommand(o *options) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "project",
		Short: "Inspect or configure an existing project; never generate one",
		Args:  cobra.NoArgs,
		RunE:  showHelp,
	}
	cmd.AddCommand(projectInspectCommand(o), projectConfigureCommand(o), projectRevertCommand(o))
	return cmd
}

func projectInspectCommand(o *options) *cobra.Command {
	return &cobra.Command{
		Use:   "inspect [PATH]",
		Short: "Inventory manifest, lockfile and configuration candidates",
		Args:  cobra.MaximumNArgs(1),
		RunE: o.action(
			projectAction,
			func(cmd *cobra.Command, c operation.Context) (operation.Result, error) {
				result := operation.NewResult(cmd.CommandPath())
				ctx, cancel := context.WithTimeout(cmd.Context(), 5*time.Second)
				defer cancel()
				inventory, err := project.Inspect(ctx, c.Scope.Root)
				component := operation.Component{
					Name:    "inventory",
					Status:  operation.StatusComplete,
					Details: inventory,
				}
				if err != nil {
					component.Status = operation.StatusFailed
					if ctx.Err() == nil {
						err = operation.Fail(
							operation.ExitFailed,
							"inventory",
							"Inventory incomplete or inaccessible; select a narrower readable scope",
						)
					}
				}
				result.Results = append(result.Results, component)
				result.Warnings = append(
					result.Warnings,
					"Bounded native metadata inspection; dependency/generated directories and symlinks are excluded. Git ignore rules are not evaluated. No project code was executed or files changed.",
				)
				return result, err
			},
		),
	}
}

func projectConfigureCommand(o *options) *cobra.Command {
	configure := &cobra.Command{
		Use:   "configure [PATH]",
		Short: "Preview and configure existing uv Python projects",
		Args:  cobra.MaximumNArgs(1),
	}
	configure.Flags().
		StringArray("language", nil, "Narrow configuration by language; repeat to select multiple")
	configure.Flags().
		Bool("dry-run", false, "Preview offline without resolving dependencies or changing project files")
	configure.Flags().
		Bool("extensions", false, "Merge optional portable VS Code extension recommendations")
	configure.Flags().
		Bool("gitignore", false, "Append relevant Python environment and cache ignore entries")
	configure.Flags().
		Bool("resolve-dependencies", false, "Approve native uv dependency resolution as an explicit external effect")
	configure.Flags().
		Bool("allow-build-hooks", false, "Explicitly select potential native build-code execution during dependency resolution")
	configure.Flags().
		Bool("ci", false, "Request CI integration; unsupported ownership receives a manual proposal")
	o.localBuildFlag(configure)
	o.approveFlag(configure)
	configure.RunE = o.action(
		projectAction,
		func(cmd *cobra.Command, c operation.Context) (operation.Result, error) {
			result := operation.NewResult(cmd.CommandPath())
			var options project.ConfigureOptions
			options.Extensions, _ = cmd.Flags().GetBool("extensions")
			options.Ignore, _ = cmd.Flags().GetBool("gitignore")
			options.ResolveDependencies, _ = cmd.Flags().GetBool("resolve-dependencies")
			options.AllowBuildHooks, _ = cmd.Flags().GetBool("allow-build-hooks")
			options.CI, _ = cmd.Flags().GetBool("ci")
			if options.AllowBuildHooks && !options.ResolveDependencies {
				return result, operation.Fail(
					operation.ExitInvalid,
					"input",
					"--allow-build-hooks requires --resolve-dependencies",
				)
			}
			proposal, err := project.Plan(cmd.Context(), c, options)
			if proposal != nil {
				result.PlanDigest = proposal.Plan.Digest()
				result.Results = append(
					result.Results,
					operation.Component{
						Name:    "project-plan",
						Status:  operation.StatusComplete,
						Details: proposal,
					},
				)
				result.Warnings = append(result.Warnings, proposal.Warnings...)
			}
			if err != nil {
				if len(result.Results) > 0 {
					result.Results[0].Status = operation.StatusOf(err)
				}
				return result, err
			}
			dryRun, _ := cmd.Flags().GetBool("dry-run")
			if dryRun {
				return result, nil
			}
			if len(proposal.Plan.Edits) == 0 && len(proposal.Plan.Effects) == 0 {
				result.Results[0].Status = operation.StatusUnchanged
				return result, nil
			}
			consent := consentFor(o, o.approvePlan)
			consent.CompleteInputs = proposal.Plan.Complete
			c.ReadOnly = false
			checkpoint, err := project.Apply(cmd.Context(), c, proposal, options, consent)
			result.OperationID = checkpoint
			if err == nil {
				shownAtPrompt(o, &result.Results[0])
				result.Summary = "Configured the project at " + homePath(c.Scope.Root)
			}
			status := operation.StatusOf(err)
			result.Results = append(
				result.Results,
				operation.Component{
					Name:    "project-configuration",
					Status:  status,
					Message: project.RecoveryInstructions(checkpoint),
				},
			)
			return result, err
		},
	)
	return configure
}

func projectRevertCommand(o *options) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "revert [PATH]",
		Short: "Undo a project configure's file changes from a saved checkpoint",
		Args:  cobra.MaximumNArgs(1),
	}
	cmd.Flags().
		String("checkpoint", "", "Restore this checkpoint ID instead of choosing from a list")
	cmd.Flags().Bool("dry-run", false, "Show the restore plan without restoring")
	o.approveFlag(cmd)
	cmd.RunE = o.action(
		projectAction,
		func(cmd *cobra.Command, c operation.Context) (operation.Result, error) {
			result := operation.NewResult(cmd.CommandPath())
			id, err := o.checkpointChoice(cmd, c, &result, "project-checkpoints")
			if err != nil {
				return result, err
			}
			selector := operation.RecoverySelector{Checkpoint: id}
			plan, err := operation.RecoveryPlan(c, selector)
			if err != nil {
				return result, err
			}
			result.PlanDigest = plan.Digest()
			result.Results = append(
				result.Results,
				operation.Component{
					Name:    "project-recovery-plan",
					Status:  operation.StatusComplete,
					Details: plan,
				},
			)
			if dryRun, _ := cmd.Flags().GetBool("dry-run"); dryRun {
				return result, nil
			}
			c.ReadOnly = false
			consent := consentFor(o, o.approvePlan)
			result.OperationID, err = operation.Recover(cmd.Context(), c, plan, selector, consent)
			if err == nil {
				shownAtPrompt(o, &result.Results[0])
				result.Summary = "Restored the project's files from the checkpoint"
			}
			return result, err
		},
	)
	return cmd
}
