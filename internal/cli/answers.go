package cli

import (
	"context"

	"github.com/Sawmonabo/workbench/internal/machine"
	"github.com/Sawmonabo/workbench/internal/operation"
	"github.com/spf13/cobra"
)

// answersCommand groups the commands that change the saved machine answers
// apply reads, outside the questions apply asks itself.
func answersCommand(o *options) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "answers",
		Short: "Import machine answers from a chezmoi config, or ask a saved answer again",
		Long: "apply asks the machine questions your saved answers lack and saves the answers " +
			"in Workbench's private machine.toml. These commands change them outside apply: " +
			"import adopts the answers of an existing chezmoi config once, so a dotfiles " +
			"machine switches without answering again, and ask asks saved answers again.",
		Example: "  workbench answers import ~/.config/chezmoi/chezmoi.toml --dry-run\n" +
			"  workbench answers ask machine_role",
		Args: cobra.NoArgs,
		RunE: showHelp,
	}
	cmd.AddCommand(answersImportCommand(o), answersAskCommand(o))
	return cmd
}

// answersImportCommand adopts machine answers from an existing chezmoi config
// once, so a dotfiles machine switches without re-answering the questionnaire.
func answersImportCommand(o *options) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "import FILE",
		Short: "Adopt the answers of an existing chezmoi config, once",
		Long: "Read the [data] table of an existing chezmoi config, such as " +
			"~/.config/chezmoi/chezmoi.toml, show the machine answers it would save and save " +
			"them once approved. It refuses a file that lacks an answer or holds one Workbench " +
			"does not accept, naming it. At a terminal it asks; without one, or for a coding " +
			"agent, pass --approve-plan DIGEST from a --dry-run --json plan.",
		Example: "  workbench answers import ~/.config/chezmoi/chezmoi.toml --dry-run\n" +
			"  workbench answers import ~/.config/chezmoi/chezmoi.toml\n" +
			"  workbench answers import FILE --dry-run --json\n" +
			"  workbench answers import FILE --approve-plan DIGEST",
		Args: cobra.ExactArgs(1),
		// FILE is any file: the shell's own completion.
		ValidArgsFunction: func(*cobra.Command, []string, string) ([]cobra.Completion, cobra.ShellCompDirective) {
			return nil, cobra.ShellCompDirectiveDefault
		},
	}
	o.approveFlag(cmd)
	cmd.Flags().Bool("dry-run", false, "Show the plan without saving answers")
	cmd.RunE = o.action(
		machineAction,
		func(cmd *cobra.Command, c operation.Context) (operation.Result, error) {
			result := operation.NewResult(cmd.CommandPath())
			from := cmd.Flags().Arg(0)
			var encoded []byte
			planner := func(_ context.Context, current operation.Context) (operation.Plan, error) {
				plan, data, err := machine.AdoptionPlan(current, from)
				encoded = data
				return plan, err
			}
			plan, err := planner(cmd.Context(), c)
			result.Results = append(
				result.Results,
				operation.Component{
					Name:    "answers-plan",
					Status:  operation.StatusComplete,
					Details: plan,
				},
			)
			if err != nil {
				return result, err
			}
			result.PlanDigest = plan.Digest()
			if dryRun, _ := cmd.Flags().GetBool("dry-run"); dryRun || len(plan.Edits) == 0 {
				return result, nil
			}
			c.ReadOnly = false
			err = operation.WithMutation(
				cmd.Context(),
				c,
				plan,
				consentFor(o, o.approvePlan),
				planner,
				func(m *operation.Mutation) error {
					return m.WritePrivate(c.Paths.MachineConfig(), encoded)
				},
			)
			if err == nil {
				shownAtPrompt(o, &result.Results[0])
				result.Results = append(
					result.Results,
					operation.Component{
						Name:    "answers",
						Status:  operation.StatusComplete,
						Message: "Saved; apply now uses them",
					},
				)
				result.Summary = brandTag + " Saved your machine answers; run " + c.WorkbenchCommand() + " apply"
			}
			return result, err
		},
	)
	return cmd
}

// answersAskCommand asks saved machine answers again.
func answersAskCommand(o *options) *cobra.Command {
	return &cobra.Command{
		Use:   "ask NAME...",
		Short: "Ask saved machine answers again, for example machine_role",
		Long: "Ask the named saved machine answers again at the terminal and save what you " +
			"answer; run workbench apply next to apply the change. It needs a person at a " +
			"terminal: it refuses --json, --non-interactive and coding agents. Tab completes " +
			"the names this machine has saved.",
		Example: "  workbench answers ask machine_role\n" +
			"  workbench answers ask work_folder personal_folder",
		Args:              cobra.MinimumNArgs(1),
		ValidArgsFunction: completeAsk,
		RunE: o.action(
			machineAction,
			func(cmd *cobra.Command, c operation.Context) (operation.Result, error) {
				return askAnswers(
					cmd,
					c,
					o,
					operation.NewResult(cmd.CommandPath()),
					cmd.Flags().Args(),
				)
			},
		),
	}
}

// askAnswers asks the named saved machine answers again, at a terminal that a
// person, not a coding agent, sits at.
func askAnswers(
	cmd *cobra.Command,
	c operation.Context,
	o *options,
	result operation.Result,
	ask []string,
) (operation.Result, error) {
	if !o.interactive() {
		return result, operation.Fail(
			operation.ExitInvalid,
			"ask",
			"answers ask asks at a terminal; drop --json and --non-interactive",
		)
	}
	// An agent cannot answer the owner's machine questions, and what it typed
	// at a pseudo-terminal would be saved as the owner's answers.
	if operation.AgentSession() {
		return result, operation.Fail(operation.ExitBlocked, "consent", agentRefusal)
	}
	if err := selectSource(&c); err != nil {
		return result, err
	}
	if err := machine.CheckAsk(c, ask); err != nil {
		return result, err
	}
	terminal, _, closeConsole := nativeConsole(o, cmd.ErrOrStderr())
	defer closeConsole()
	c.ReadOnly = false
	if _, err := setUp(cmd, c, o, terminal, &result, true, ask); err != nil {
		return result, err
	}
	result.Summary = brandTag + " Saved your machine answers; run " + c.WorkbenchCommand() + " apply"
	return result, nil
}
