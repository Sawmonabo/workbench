package cli

import (
	"context"
	"errors"
	"path/filepath"
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
							"Inventory incomplete or inaccessible",
							operation.FixText("select a narrower readable scope"),
						)
					}
				}
				result.Results = append(result.Results, component)
				result.Warnings = append(
					result.Warnings,
					operation.Warn(
						"Bounded native metadata inspection",
						operation.Line{
							Label: "excluded",
							Text:  "dependency and generated folders, and symbolic links",
						},
						operation.Line{Label: "not read", Text: "Git ignore rules"},
						operation.Changed("no file"),
						operation.Outcome("no project code ran"),
					),
				)
				return result, err
			},
		),
	}
}

// projectApplyLong is project apply's help text.
const projectApplyLong = "Plan Workbench's settings for the project at PATH: Ruff, ty and basedpyright for a " +
	"uv Python project; a golangci-lint configuration (only when the module has none) " +
	"and a .workbench.toml record for a Go module; and, for a Cargo package or workspace " +
	"root, missing lints, a dev profile setting and a record in Cargo.toml, member " +
	"opt-ins to new workspace lints, and rustfmt.toml, deny.toml and .mbx.toml where " +
	"the project has none. Show the plan and apply it once " +
	"approved, saving a checkpoint that project revert restores. --extensions, " +
	"--gitignore, --ci, --hooks and --dependabot add optional changes; the last three " +
	"need the projects at the repository root. --hooks never runs lefthook install: " +
	"run it once per clone. --resolve-dependencies lets uv resolve " +
	"missing Python check tools, an external effect, and --allow-build-hooks (only with " +
	"it) lets that run project build code. --dry-run previews offline without changing " +
	"anything. Without a terminal, or for a coding agent, pass --approve-plan DIGEST " +
	"from a --dry-run --json plan."

func projectApplyCommand(o *options) *cobra.Command {
	command := &cobra.Command{
		Use:   "apply [PATH]",
		Short: "Preview and apply Workbench's settings to a uv Python project, Go module or Cargo package",
		Long:  projectApplyLong,
		Example: "  workbench project apply ./apps/api --language python --dry-run\n" +
			"  workbench project apply . --language python --extensions --gitignore\n" +
			"  workbench project apply . --language python --resolve-dependencies\n" +
			"  workbench project apply . --language go --extensions --gitignore --ci\n" +
			"  workbench project apply . --language rust --extensions --gitignore --ci\n" +
			"  workbench project apply . --hooks --dependabot",
		Args: cobra.MaximumNArgs(1),
	}
	command.Flags().
		StringArray("language", nil, "Narrow configuration by language; repeat to select multiple")
	command.Flags().
		Bool("dry-run", false, "Preview offline without resolving dependencies or changing project files")
	command.Flags().
		Bool("extensions", false, "Merge optional portable VS Code extension recommendations")
	command.Flags().
		Bool("gitignore", false, "Append the selected languages' environment, cache and build output ignore entries")
	command.Flags().
		Bool("resolve-dependencies", false, "Approve native uv dependency resolution as an explicit external effect")
	command.Flags().
		Bool("allow-build-hooks", false, "Explicitly select potential native build-code execution during dependency resolution")
	command.Flags().
		Bool("ci", false, "Add Workbench's check job to the existing workflow that runs the tests; other layouts get a manual proposal")
	command.Flags().
		Bool("hooks", false, "Write lefthook.yml, with checks before commit and tests before push, when the repository has none")
	command.Flags().
		Bool("dependabot", false, "GitHub only: write weekly Dependabot updates and auto-merge for their patch and minor pull requests")
	o.localBuildFlag(command)
	o.approveFlag(command)
	command.RunE = o.action(
		projectAction,
		func(cmd *cobra.Command, c operation.Context) (operation.Result, error) {
			result := operation.NewResult(cmd.CommandPath())
			options := projectApplyOptions(cmd)
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
			consent := consentFor(cmd, c, o, o.approvePlan)
			consent.CompleteInputs = proposal.Plan.Complete
			c.ReadOnly = false
			checkpoint, err := project.Apply(cmd.Context(), c, proposal, options, consent)
			result.OperationID = checkpoint
			if err == nil {
				result.Summary = "Configured the project at " + c.ShowPath(c.Scope.Root)
				if hooks := proposal.HooksFile(); hooks != "" {
					result.Body = operation.Explain(
						operation.Line{
							Label: "hooks",
							Text: "lefthook install turns on the Git hooks in " +
								c.ShowPath(hooks) + ", once per clone",
						},
						operation.NextText(
							"cd "+c.ShellPath(filepath.Dir(hooks))+" && lefthook install",
						),
					)
				}
			}
			outcome := operation.Component{
				Name:   "project-configuration",
				Status: operation.StatusOf(err),
			}
			outcome.Message, outcome.Body = project.RecoveryInstructions(c, checkpoint)
			result.Results = append(result.Results, outcome)
			return result, err
		},
	)
	return command
}

// projectApplyOptions reads project apply's optional-change flags.
func projectApplyOptions(cmd *cobra.Command) project.ApplyOptions {
	var options project.ApplyOptions
	options.Extensions, _ = cmd.Flags().GetBool("extensions")
	options.Ignore, _ = cmd.Flags().GetBool("gitignore")
	options.ResolveDependencies, _ = cmd.Flags().GetBool("resolve-dependencies")
	options.AllowBuildHooks, _ = cmd.Flags().GetBool("allow-build-hooks")
	options.CI, _ = cmd.Flags().GetBool("ci")
	options.Hooks, _ = cmd.Flags().GetBool("hooks")
	options.Dependabot, _ = cmd.Flags().GetBool("dependabot")
	return options
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
	o.recoveryCommand(
		cmd,
		projectAction,
		"project-checkpoints",
		"project-recovery-plan",
		func(restore recovery) (operation.Result, error) {
			result := restore.result
			var err error
			result.OperationID, err = operation.Recover(
				restore.cmd.Context(), restore.c, restore.plan, restore.selector, restore.consent,
			)
			if err == nil {
				result.Summary = "Restored the project's files from the checkpoint"
			}
			return result, err
		},
	)
	return cmd
}
