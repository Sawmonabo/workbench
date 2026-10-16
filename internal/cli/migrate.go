package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Sawmonabo/workbench/internal/migrate"
	"github.com/Sawmonabo/workbench/internal/operation"
)

// migrateCommand lists and runs Workbench's migrations (internal/migrate).
// Without flags it lists them and changes nothing. --apply runs the ones with
// something to do, under the approval rules apply uses. The hidden --probe
// and run are the apply's migrations step: they run inside an apply that
// holds the locks, so they resolve paths only and print their own lines.
func migrateCommand(o *options) *cobra.Command {
	var apply, dryRun, probe bool
	cmd := &cobra.Command{
		Use:   "migrate",
		Short: "Move files earlier Workbench releases left outside Workbench's folders",
		Long: "Without flags, list each migration and whether it has anything to do; nothing " +
			"changes. --apply runs every migration with something to do: at a terminal it shows " +
			"the list and asks once; --dry-run --json prints it with a plan_digest that " +
			"--approve-plan DIGEST runs exactly; without a terminal it needs --approve-plan or " +
			"--yes, and a coding agent must use --approve-plan. Every apply runs them too, as its " +
			"migrations step, and the costs import moves the costs files by itself.",
		Args: cobra.NoArgs,
	}
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		switch {
		case probe:
			return o.runPrinted(
				cmd,
				func(*operation.Result) error { return migrateProbe(cmd.OutOrStdout()) },
			)
		case !apply && (dryRun || o.yes || o.approvePlan != ""):
			return o.runPrinted(cmd, func(*operation.Result) error {
				return operation.Fail(
					operation.ExitInvalid,
					"invocation",
					"--dry-run, --yes and --approve-plan go with --apply",
				)
			})
		case !apply:
			return o.action(machineAction, migrateList)(cmd, nil)
		}
		return o.action(
			machineAction,
			func(cmd *cobra.Command, c operation.Context) (operation.Result, error) {
				return o.migrateApply(cmd, c, dryRun)
			},
		)(
			cmd,
			nil,
		)
	}
	flags := cmd.Flags()
	flags.BoolVar(&apply, "apply", false, "Run every migration that has something to do")
	flags.BoolVar(&dryRun, "dry-run", false, "With --apply, show what would run and exit")
	flags.BoolVar(
		&o.yes,
		"yes",
		false,
		"With --apply, run without asking (refused for coding agents)",
	)
	flags.BoolVar(&probe, "probe", false, "Print the apply's probe lines")
	_ = flags.MarkHidden("probe")
	o.approveFlag(cmd)
	cmd.AddCommand(migrateRunCommand(o))
	return cmd
}

// migrateRunCommand is the apply's migrations step: run every migration with
// something to do and print what happens. Ticking the step is the approval, so
// it runs only as that step: with the WORKBENCH_EFFECT_MIGRATIONS=1 the apply
// gives its scripts, and never in a coding agent's session, whose variables
// the apply doesn't pass on. That stops a run by mistake, not a caller that
// sets the variable itself. WORKBENCH_MIGRATIONS names the planning binary's
// migrations, so one this binary doesn't know is named, not passed over.
func migrateRunCommand(o *options) *cobra.Command {
	return &cobra.Command{
		Use:    "run",
		Short:  "Apply step: run every migration that has something to do",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return o.runPrinted(cmd, func(*operation.Result) error {
				if os.Getenv("WORKBENCH_EFFECT_MIGRATIONS") != "1" || operation.AgentSession() {
					return operation.Fail(operation.ExitBlocked, "consent",
						"migrate run is the apply's migrations step; run workbench migrate --apply")
				}
				locations, err := migrate.Locate()
				if err != nil {
					return err
				}
				wanted := strings.Fields(os.Getenv("WORKBENCH_MIGRATIONS"))
				return migrate.Run(
					cmd.Context(),
					locations,
					wanted,
					sayMigration(cmd.OutOrStdout()),
				)
			})
		},
	}
}

// sayMigration prints a migration line in the style of the apply's scripts.
func sayMigration(out io.Writer) func(string) {
	return func(line string) { _, _ = fmt.Fprintln(out, "==> migrations: "+line) }
}

// migrateProbe prints the probe lines; on a failure it prints nothing to
// stdout, so the script says it couldn't check.
func migrateProbe(out io.Writer) error {
	locations, err := migrate.Locate()
	if err != nil {
		return err
	}
	wanted := strings.Fields(os.Getenv("WORKBENCH_MIGRATIONS"))
	_, err = fmt.Fprintln(out, strings.Join(migrate.Probe(locations, wanted), "\n"))
	return err
}

// migrateList shows each migration and where it stands.
func migrateList(cmd *cobra.Command, _ operation.Context) (operation.Result, error) {
	result := operation.NewResult(cmd.CommandPath())
	locations, err := migrate.Locate()
	if err != nil {
		return result, err
	}
	result.Warnings = append(result.Warnings, locations.Notes...)
	for _, f := range migrate.Check(locations) {
		component := operation.Component{Name: f.ID, Title: f.ID + " " + f.Title}
		switch f.Status {
		case migrate.Done:
			component.Status, component.Message = operation.StatusUnchanged, "nothing to do"
			if at := migrate.DoneAt(locations, f.ID); at != "" {
				component.Message = "done " + at
			}
			if f.Detail != "" {
				component.Message += ", " + f.Detail
			}
		case migrate.Conflict:
			component.Status, component.Message = operation.StatusConflict, f.Detail
		case migrate.Unfinished:
			component.Status = operation.StatusPending
			component.Message = "unfinished: " + f.Detail + "; workbench migrate --apply finishes it"
		default:
			component.Status = operation.StatusPending
			component.Message = f.Detail + "; workbench migrate --apply runs it"
		}
		result.Results = append(result.Results, component)
	}
	return result, nil
}

// migrateApply shows "Checking migrations" while it checks, then runs the
// migrations with something to do once the plan of them is approved.
func (o *options) migrateApply(
	cmd *cobra.Command,
	c operation.Context,
	dryRun bool,
) (operation.Result, error) {
	result := operation.NewResult(cmd.CommandPath())
	if !dryRun {
		if err := checkConsent(o, "migrate --apply"); err != nil {
			return result, err
		}
	}
	stop := c.ShowProgress("Checking migrations")
	locations, err := migrate.Locate()
	if err != nil {
		stop()
		return result, err
	}
	plan := migrate.Plan(c.Scope, migrate.Check(locations))
	stop()
	result.Warnings = append(result.Warnings, locations.Notes...)
	result.Warnings = append(result.Warnings, plan.Warnings...)
	result.PlanDigest = plan.Digest()
	if !dryRun {
		if err = operation.CheckApproval(o.approvePlan, result.PlanDigest); err != nil {
			return result, err
		}
	}
	if dryRun || len(plan.Effects) == 0 {
		return migrateShown(result, plan, dryRun)
	}
	consent := consentFor(o, o.approvePlan)
	if o.yes {
		consent.ApprovedDigest = result.PlanDigest
		if !o.json {
			if err = writePlan(cmd.OutOrStdout(), plan); err != nil {
				return result, err
			}
		}
	}
	// --json keeps stdout for the one result object.
	say := sayMigration(cmd.OutOrStdout())
	if o.json {
		say = sayMigration(cmd.ErrOrStderr())
	}
	c.ReadOnly = false
	err = operation.WithMutation(
		cmd.Context(), c, plan, consent,
		func(_ context.Context, preview operation.Context) (operation.Plan, error) {
			current, err := migrate.Locate()
			if err != nil {
				return operation.Plan{}, err
			}
			locations = current
			return migrate.Plan(preview.Scope, migrate.Check(current)), nil
		},
		func(*operation.Mutation) error {
			return migrate.Run(cmd.Context(), locations, nil, say)
		},
	)
	result.Results = append(
		result.Results,
		operation.Component{Name: "migrations", Status: operation.StatusOf(err)},
	)
	return result, err
}

// migrateShown is the result of a dry run, or of a run with nothing to move:
// the plan, "Nothing to migrate", or the conflicts that need the owner, which
// the warnings name.
func migrateShown(
	result operation.Result,
	plan operation.Plan,
	dryRun bool,
) (operation.Result, error) {
	component := operation.Component{
		Name:    "migrations",
		Status:  operation.StatusComplete,
		Details: plan,
	}
	var err error
	switch {
	case dryRun:
	case len(plan.Warnings) > 0:
		component.Status, component.Details = operation.StatusConflict, nil
		err = operation.Fail(
			operation.ExitConflict,
			"migrate",
			"A migration needs you: deal with each file the warnings name as they say",
		)
	default:
		component.Status, component.Message, component.Details = operation.StatusUnchanged, "Nothing to migrate", nil
	}
	result.Results = append(result.Results, component)
	return result, err
}
