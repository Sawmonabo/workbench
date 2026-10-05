package cli

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"

	"github.com/Sawmonabo/workbench/internal/machine"
	"github.com/Sawmonabo/workbench/internal/operation"
	"github.com/spf13/cobra"
)

func doctorCommand(o *options) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Check what is installed and applied, tool versions and the host, without repair",
		Args:  cobra.NoArgs,
		RunE: o.action(
			machineAction,
			func(cmd *cobra.Command, c operation.Context) (operation.Result, error) {
				result := operation.NewResult(cmd.CommandPath())
				components, err := machine.Doctor(cmd.Context(), c)
				result.Results = append(
					[]operation.Component{{
						Name:    "workbench",
						Status:  operation.StatusComplete,
						Message: version(),
					}},
					components...,
				)
				if err == nil {
					result.Summary = "[WorkBench] Every check passed"
				}
				return result, err
			},
		),
	}
	o.localBuildFlag(cmd)
	return cmd
}

// initCommand adopts machine answers from an existing chezmoi config once, so a
// dotfiles machine switches without re-answering the questionnaire, or asks
// saved answers again.
func initCommand(o *options) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Adopt answers from an existing chezmoi config, or ask a saved answer again",
		Args:  cobra.NoArgs,
	}
	o.approveFlag(cmd)
	cmd.Flags().
		String("answers-from", "", "Existing chezmoi config, for example ~/.config/chezmoi/chezmoi.toml")
	cmd.Flags().StringSlice(
		"ask",
		nil,
		"Ask these saved machine answers again, for example --ask machine_role",
	)
	cmd.Flags().Bool("dry-run", false, "Show the plan without saving answers")
	cmd.RunE = o.action(
		machineAction,
		func(cmd *cobra.Command, c operation.Context) (operation.Result, error) {
			result := operation.NewResult(cmd.CommandPath())
			ask, _ := cmd.Flags().GetStringSlice("ask")
			from, _ := cmd.Flags().GetString("answers-from")
			if (from == "") == (len(ask) == 0) {
				return result, operation.Fail(
					operation.ExitInvalid,
					"init",
					"init takes --answers-from FILE or --ask KEY",
				)
			}
			if len(ask) > 0 {
				return askAnswers(cmd, c, o, result, ask)
			}
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
					return m.WritePrivate(filepath.Join(c.Paths.Config, "machine.toml"), encoded)
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
				result.Summary = "[WorkBench] Saved your machine answers; run " + c.WorkbenchCommand() + " apply"
			}
			return result, err
		},
	)
	return cmd
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
	if dryRun, _ := cmd.Flags().GetBool("dry-run"); dryRun {
		return result, operation.Fail(
			operation.ExitInvalid,
			"ask",
			"--ask saves the answers it asks; drop --dry-run",
		)
	}
	if !o.interactive() {
		return result, operation.Fail(
			operation.ExitInvalid,
			"ask",
			"--ask asks at a terminal; drop --json and --non-interactive",
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
	result.Summary = "[WorkBench] Saved your machine answers; run " + c.WorkbenchCommand() + " apply"
	return result, nil
}

// applyCommand makes this machine match the installed release, or the local
// checkout, after a checklist of what would change.
func applyCommand(o *options) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "apply",
		Short: "Show what would change on this machine as a checklist, then apply what is checked",
		Long: "Install Workbench's tools when missing and ask the machine questions your saved " +
			"answers lack, then plan every file change and provisioning effect for this machine. " +
			"The first apply, and any apply that finds something new to decide or a file Workbench " +
			"owns edited outside it, shows the checklist: space unchecks a step, a applies. Your " +
			"choices are remembered, so a later apply with nothing new prints the plan and applies " +
			"them without asking. --choose shows the checklist anyway; --reset forgets the " +
			"remembered choices and asks again with the defaults; --yes never asks, taking the " +
			"remembered choice or the default for what is new (it cannot be combined with --choose " +
			"or --reset, and a coding agent is refused it); --dry-run only shows the plan. When a " +
			"coding agent runs it (Claude Code, Codex), apply never applies unasked: pass " +
			"--approve-plan DIGEST from a --dry-run --json plan. Without a terminal it applies " +
			"only with --approve-plan DIGEST or --yes. On a Mac, an apply that is not given " +
			"--approve-plan DIGEST first refreshes Homebrew's package list, so the updates it " +
			"plans are current; --dry-run and --approve-plan leave the list as it is.",
		Args: cobra.NoArgs,
		RunE: o.action(
			nativeAction,
			func(cmd *cobra.Command, c operation.Context) (operation.Result, error) {
				if dryRun, _ := cmd.Flags().GetBool("dry-run"); dryRun {
					return machinePlan(cmd, c, o)
				}
				if err := checkApply(o); err != nil {
					return operation.NewResult(cmd.CommandPath()), err
				}
				c.ReadOnly = false
				result, plan, err := applyMachine(cmd, c, o)
				if err == nil {
					result.Summary = appliedSummary(result, plan)
					// This shell started before the apply wrote the shell setup that
					// finds workbench and the tools; only a new terminal reads it.
					if c.RecordsHome() && !c.BinOnPath() {
						result.Summary += "; open a new terminal so your shell finds workbench and the other tools"
					}
				}
				return result, err
			},
		),
	}
	cmd.Flags().Bool("dry-run", false, "Show the checklist without installing, asking or applying")
	cmd.Flags().BoolVarP(
		&o.yes,
		"yes",
		"y",
		false,
		"Apply the saved selection without asking (refused for coding agents)",
	)
	cmd.Flags().
		BoolVar(&o.choose, "choose", false, "Show the checklist with your saved choices, even when nothing is new")
	cmd.Flags().
		BoolVar(&o.reset, "reset", false, "Forget your saved choices and ask again with the defaults")
	o.localBuildFlag(cmd)
	o.destinationFlag(cmd)
	o.approveFlag(cmd)
	return cmd
}

// checkApply is apply's one consent gate. It runs before anything else
// happens, so a run that is certain to be refused downloads no tools and
// touches no machine.toml. Flags that contradict each other are an invalid
// invocation (exit 2). A run nobody approved is refused (exit 3): it needs
// --approve-plan from a plan someone read, --yes from a person who trusts the
// saved selection, or a person at a terminal. A coding agent can type at a
// pseudo-terminal and --yes would apply a plan it never read, so it gets only
// --approve-plan. --reset forgets the saved choices, so --yes would turn every
// step the owner skipped back on and apply it unasked.
func checkApply(o *options) error {
	switch {
	case o.reset && o.yes:
		return operation.Fail(
			operation.ExitInvalid,
			"reset",
			"--reset asks and --yes does not; pick one",
		)
	case o.choose && o.yes:
		return operation.Fail(
			operation.ExitInvalid,
			"choose",
			"--choose asks and --yes does not; pick one",
		)
	case o.choose && o.approvePlan != "":
		return operation.Fail(
			operation.ExitInvalid,
			"choose",
			"--choose asks and --approve-plan does not; pick one",
		)
	case o.choose && !o.interactive():
		return operation.Fail(
			operation.ExitInvalid,
			"choose",
			"--choose asks at a terminal; drop --json and --non-interactive",
		)
	case operation.AgentSession() && (o.yes || o.approvePlan == ""):
		return operation.Fail(operation.ExitBlocked, "consent", agentRefusal)
	case o.yes || o.approvePlan != "":
		return nil
	case o.interactive() && hasTerminal():
		return nil
	case o.choose:
		return operation.Fail(operation.ExitBlocked, "choose", "--choose needs a terminal")
	}
	return operation.Fail(
		operation.ExitBlocked,
		"consent",
		"Nothing approved this plan; read it with workbench apply --dry-run --json, "+
			"then pass --approve-plan DIGEST",
	)
}

// needsChoice reports whether apply must show the checklist rather than apply
// the saved selection: the owner has never decided anything (selection has no
// decided list), a non-fixed effect the owner has not decided yet has something
// to do (one the probe found nothing to change cannot be chosen, so it waits
// until it has something), or a file Workbench owns whole was edited outside
// it. Merged files never count, since Claude Code and Codex rewrite theirs
// constantly.
func needsChoice(plan operation.Plan, selection machine.Selection) bool {
	return selection.NeverDecided() ||
		slices.ContainsFunc(plan.Effects, func(effect operation.Effect) bool {
			return effect.New && !effect.Fixed && !plan.AlreadySet(effect)
		}) || slices.ContainsFunc(plan.Edits, func(edit operation.Edit) bool {
		return edit.EditedOutside && !edit.Merged
	})
}

// machineSelection is the saved selection, or an empty one with --reset.
func machineSelection(c operation.Context, o *options) (machine.Selection, error) {
	if o.reset {
		return machine.Selection{Forget: true}, nil
	}
	return machine.ReadSelection(c.Native.Config)
}

// machinePlan shows the checklist without applying it.
func machinePlan(cmd *cobra.Command, c operation.Context, o *options) (operation.Result, error) {
	result := operation.NewResult(cmd.CommandPath())
	selection, err := machineSelection(c, o)
	if err != nil {
		return result, err
	}
	plan, err := machine.Plan(cmd.Context(), c, selection)
	if operation.ExitCode(err) == operation.ExitInterrupted {
		return result, err // an interrupted plan is incomplete; show only the error
	}
	status := operation.StatusComplete
	if err != nil {
		status = operation.StatusBlocked
	}
	result.Results = append(
		result.Results,
		operation.Component{Name: "machine-plan", Status: status, Details: plan},
	)
	if err != nil {
		return result, err
	}
	result.PlanDigest = plan.Digest()
	return result, nil
}

// applyMachine installs Workbench's tools when missing and asks the machine
// questions the saved answers lack, refreshes Homebrew's package list unless a
// plan digest was approved, plans, gets the selection approved and applies it.
// A local checkout installs the tools its own versions.toml pins when missing
// and uses the saved answers as they are.
func applyMachine(
	cmd *cobra.Command,
	c operation.Context,
	o *options,
) (operation.Result, operation.Plan, error) {
	result := operation.NewResult(cmd.CommandPath())
	terminal, progress, closeConsole := nativeConsole(o, cmd.ErrOrStderr())
	defer closeConsole()
	var err error
	if c.Native.Developer {
		// A checkout uses the saved answers as they are, but installs the tools
		// its versions.toml pins when they are missing.
		c, err = setUp(cmd, c, o, nil, &result, false, nil)
	} else {
		c, err = setUp(cmd, c, o, terminal, &result, true, nil)
	}
	if err != nil {
		return result, operation.Plan{}, err
	}
	selection, err := machineSelection(c, o)
	if err != nil {
		return result, operation.Plan{}, err
	}
	refreshed, err := refreshHomebrew(cmd.Context(), c, o, &result)
	if err != nil {
		return result, operation.Plan{}, err
	}
	plan, err := machine.Plan(cmd.Context(), c, selection)
	if err != nil {
		return result, plan, err
	}
	consent := consentFor(o, o.approvePlan)
	// checkApply already refused every run that is neither approved by digest,
	// --yes nor at a person's terminal, so the cases below that ask, or apply
	// the saved selection unasked, have a person to read the plan.
	switch {
	case o.approvePlan != "":
	case o.yes:
		// --yes never asks: undecided effects take the saved choice or the
		// default and are recorded as decided by the apply.
		consent.ApprovedDigest = plan.Digest()
		if !o.json {
			// Undecided effects take their defaults: show the whole plan so
			// they are visible, not the compact saved-choices view.
			if err = writePlanView(
				cmd.OutOrStdout(),
				plan,
				o.verbose,
				!needsChoice(plan, selection),
			); err != nil {
				return result, plan, err
			}
		}
	case !o.choose && !o.reset && !needsChoice(plan, selection):
		// Nothing new to decide: show the plan and apply the saved selection.
		if err = writePlanView(cmd.OutOrStdout(), plan, o.verbose, true); err != nil {
			return result, plan, err
		}
		consent.ApprovedDigest = plan.Digest()
	default:
		// Tag saved skips from machine.toml even under --reset: Apply's recheck
		// reads the file the same way, and SavedSkip is part of the digest the
		// approval must equal.
		saved, savedErr := machine.ReadSelection(c.Native.Config)
		if savedErr != nil {
			return result, plan, savedErr
		}
		checked, approved, chooseErr := choosePlan(cmd.Context(), plan, o.verbose)
		if chooseErr != nil {
			return result, plan, chooseErr
		}
		if !approved {
			return result, plan, operation.Fail(
				operation.ExitBlocked,
				"consent",
				"Plan was not approved; no changes made",
			)
		}
		for i := range plan.Effects {
			if !plan.Effects[i].Fixed {
				plan.Effects[i].Checked = slices.Contains(checked, plan.Effects[i].Name)
			}
		}
		selection = machine.SelectionOf(plan.Effects)
		selection.Forget = o.reset
		plan = machine.Reselect(plan, selection, saved)
		consent.ApprovedDigest = plan.Digest()
	}
	result.PlanDigest = plan.Digest()
	applied, err := machine.Apply(
		cmd.Context(), c, selection, plan, consent, terminal, progress, refreshed,
	)
	result.Results = append(result.Results, applied.Results...)
	result.OperationID = applied.OperationID
	return result, plan, err
}

// refreshHomebrew refreshes Homebrew's package list before apply plans, so the
// updates the plan lists are current, and reports whether it did. A run that
// approves a plan digest never refreshes: the digest was computed from the list
// as it was, so a refresh would change the plan under the approval, and the
// brew-maintenance step refreshes at the end of such a run instead. A refresh
// that fails only warns, and apply plans from the list as it is; an interrupt
// stops apply.
func refreshHomebrew(
	ctx context.Context,
	c operation.Context,
	o *options,
	result *operation.Result,
) (bool, error) {
	if o.approvePlan != "" {
		return false, nil
	}
	refreshed, err := machine.RefreshHomebrew(ctx, c)
	if operation.ExitCode(err) == operation.ExitInterrupted {
		return false, err
	}
	if err != nil {
		result.Warnings = append(
			result.Warnings,
			"Homebrew's package list was not refreshed, so the updates listed may be out of date; "+
				"run brew update to see why",
		)
	}
	return refreshed, nil
}

// appliedSummary is the branded result line. Apply's early return, with no
// file to write and no selection to save, reports itself by its message.
func appliedSummary(result operation.Result, plan operation.Plan) string {
	for _, component := range result.Results {
		if component.Message == machine.NothingToApply {
			return "[WorkBench] Nothing to apply; this machine already matches"
		}
	}
	effects, skipped := 0, 0
	for _, effect := range plan.Effects {
		switch {
		case effect.Fixed:
		case effect.Checked:
			effects++
		case plan.AlreadySet(effect):
			// Nothing to change and nothing turned off: not a skipped step.
		default:
			skipped++
		}
	}
	line := fmt.Sprintf(
		"[WorkBench] Applied: %d file%s, %d effect%s",
		plan.Files(), plural(plan.Files()), effects, plural(effects),
	)
	if skipped > 0 {
		line += fmt.Sprintf("; %d skipped", skipped)
	}
	return line
}
