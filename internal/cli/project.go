package cli

import (
	"context"
	"errors"
	"time"

	"github.com/Sawmonabo/workbench/internal/operation"
	"github.com/Sawmonabo/workbench/internal/project"
	"github.com/spf13/cobra"
)

func projectCommand(o *options) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "project",
		Short: "Inspect an existing project or apply Workbench's settings to it; never generate one",
		Long: "Workbench works on a project you already have: inspect lists what it finds, " +
			"apply previews and applies Workbench's settings for its language, and revert " +
			"restores the files an apply changed. PATH defaults to the current folder.",
		Example: "  workbench project inspect .\n" +
			"  workbench project apply ./apps/api --language python --dry-run\n" +
			"  workbench project revert ./apps/api",
		Args: cobra.NoArgs,
		RunE: showHelp,
	}
	cmd.AddCommand(projectInspectCommand(o), projectApplyCommand(o), projectRevertCommand(o))
	return cmd
}

func projectInspectCommand(o *options) *cobra.Command {
	return &cobra.Command{
		Use:   "inspect [PATH]",
		Short: "List a project's manifests, lockfiles and configuration files",
		Long: "List the manifests, lockfiles and configuration files of the project at PATH, " +
			"read-only and offline: no project code runs and nothing changes.",
		Example: "  workbench project inspect\n" +
			"  workbench project inspect ./apps/api --json",
		Args: cobra.MaximumNArgs(1),
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
					// Keep a user interrupt (the parent context is canceled) and an
					// actionable operation error. The five-second timeout cancels only
					// the child context, so it still gets the narrowing message.
					_, actionable := errors.AsType[*operation.Error](err)
					if cmd.Context().Err() == nil && !actionable {
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

func projectApplyCommand(o *options) *cobra.Command {
	command := &cobra.Command{
		Use:   "apply [PATH]",
		Short: "Preview and apply Workbench's settings to an existing uv Python project",
		Long: "Plan Workbench's Ruff, ty and basedpyright settings for the uv Python project at " +
			"PATH, show the plan and apply it once approved, saving a checkpoint that project " +
			"revert restores. --extensions, --gitignore and --ci add optional changes. " +
			"--resolve-dependencies lets uv resolve missing check tools, an external effect, and " +
			"--allow-build-hooks (only with it) lets that run project build code. --dry-run " +
			"previews offline without changing anything. Without a terminal, or for a coding " +
			"agent, pass --approve-plan DIGEST from a --dry-run --json plan.",
		Example: "  workbench project apply ./apps/api --language python --dry-run\n" +
			"  workbench project apply . --language python --extensions --gitignore\n" +
			"  workbench project apply . --language python --resolve-dependencies",
		Args: cobra.MaximumNArgs(1),
	}
	command.Flags().
		StringArray("language", nil, "Narrow configuration by language; repeat to select multiple")
	command.Flags().
		Bool("dry-run", false, "Preview offline without resolving dependencies or changing project files")
	command.Flags().
		Bool("extensions", false, "Merge optional portable VS Code extension recommendations")
	command.Flags().
		Bool("gitignore", false, "Append relevant Python environment and cache ignore entries")
	command.Flags().
		Bool("resolve-dependencies", false, "Approve native uv dependency resolution as an explicit external effect")
	command.Flags().
		Bool("allow-build-hooks", false, "Explicitly select potential native build-code execution during dependency resolution")
	command.Flags().
		Bool("ci", false, "Request CI integration; unsupported ownership receives a manual proposal")
	o.localBuildFlag(command)
	o.approveFlag(command)
	command.RunE = o.action(
		projectAction,
		func(cmd *cobra.Command, c operation.Context) (operation.Result, error) {
			result := operation.NewResult(cmd.CommandPath())
			var options project.ApplyOptions
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
				result.Summary = "Configured the project at " + c.ShowPath(c.Scope.Root)
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
	return command
}

func projectRevertCommand(o *options) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "revert [PATH]",
		Short: "Undo a project apply's file changes from a saved checkpoint",
		Long: "Restore the files an earlier project apply changed in the project at PATH, from " +
			"the checkpoint it saved. Without --checkpoint it lists the checkpoints to choose " +
			"from at a terminal. It restores files only: it does not undo dependency " +
			"resolution or CI runs.",
		Example: "  workbench project revert ./apps/api --dry-run\n" +
			"  workbench project revert ./apps/api --checkpoint ID",
		Args: cobra.MaximumNArgs(1),
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
