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
// Bare, it runs the ones with something to do under the approval rules apply
// uses; --dry-run lists them and changes nothing. The hidden --probe and run
// are the apply's migrations step: they run inside an apply that holds the
// locks, so they resolve paths only and print their own lines.
func migrateCommand(o *options) *cobra.Command {
	var dryRun bool
	cmd := &cobra.Command{
		Use:   "migrate",
		Short: "Move or clean up files earlier Workbench releases left behind",
		Long: "Run every migration that has something to do: at a terminal it shows what will " +
			"run and asks once. --dry-run lists each migration and whether it has anything to " +
			"do, and changes nothing; with --json it adds the plan_digest that --approve-plan " +
			"DIGEST runs exactly.\n\n" +
			"Without a terminal it runs only with --approve-plan DIGEST or --yes, and a coding " +
			"agent must use --approve-plan (it is refused --yes); --yes and --approve-plan " +
			"cannot be combined. Every apply runs the migrations too, as its migrations step, " +
			"and the costs import moves the costs files by itself.",
		Example: "  workbench migrate --dry-run\n" +
			"  workbench migrate\n" +
			"  workbench migrate --dry-run --json\n" +
			"  workbench migrate --approve-plan DIGEST",
		Args: cobra.NoArgs,
	}
	cmd.RunE = o.action(
		machineAction,
		func(cmd *cobra.Command, c operation.Context) (operation.Result, error) {
			if dryRun {
				return migrateList(cmd, c)
			}
			return o.migrateApply(cmd, c)
		},
	)
	flags := cmd.Flags()
	flags.BoolVar(&dryRun, "dry-run", false, "List each migration and what it would do, and exit")
	flags.BoolVarP(
		&o.yes,
		"yes",
		"y",
		false,
		"Run without asking (refused for coding agents)",
	)
	o.approveFlag(cmd)
	// The step runs every migration with something to do. WORKBENCH_MIGRATIONS
	// names the planning binary's migrations, so one this binary doesn't know
	// is named, not passed over.
	applyStep(
		o,
		cmd,
		"migrations",
		"migrate run is the apply's migrations step; run workbench migrate",
		migrateProbe,
		func(cmd *cobra.Command) error {
			locations, err := migrate.Locate()
			if err != nil {
				return err
			}
			wanted := strings.Fields(os.Getenv("WORKBENCH_MIGRATIONS"))
			return migrate.Run(cmd.Context(), locations, wanted, sayMigration(cmd.OutOrStdout()))
		},
	)
	return cmd
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

// migrateList is migrate --dry-run: each migration and where it stands, and
// the digest of the plan a bare migrate would ask approval for, which
// --approve-plan takes.
func migrateList(cmd *cobra.Command, c operation.Context) (operation.Result, error) {
	result := operation.NewResult(cmd.CommandPath())
	locations, err := migrate.Locate()
	if err != nil {
		return result, err
	}
	findings := migrate.Check(locations)
	result.PlanDigest = migrate.Plan(c.Scope, locations, findings).Digest()
	result.Warnings = append(result.Warnings, operation.WarnAll(locations.Notes...)...)
	for _, f := range findings {
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
			component.Message = "unfinished: " + f.Detail + "; workbench migrate finishes it"
		default:
			component.Status = operation.StatusPending
			component.Message = f.Detail + "; workbench migrate runs it"
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
) (operation.Result, error) {
	result := operation.NewResult(cmd.CommandPath())
	if err := checkConsent(o, "migrate"); err != nil {
		return result, err
	}
	stop := c.ShowProgress("Checking migrations")
	locations, err := migrate.Locate()
	if err != nil {
		stop()
		return result, err
	}
	findings := migrate.Check(locations)
	plan := migrate.Plan(c.Scope, locations, findings)
	stop()
	result.Warnings = append(result.Warnings, operation.WarnAll(locations.Notes...)...)
	result.Warnings = append(result.Warnings, plan.Warnings...)
	result.PlanDigest = plan.Digest()
	if err = operation.CheckApproval(o.approvePlan, result.PlanDigest); err != nil {
		return result, err
	}
	if len(plan.Effects) == 0 {
		return migrateShown(result, plan, findings)
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
			return migrate.Plan(preview.Scope, current, migrate.Check(current)), nil
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

// migrateShown is the result of a run with nothing to move: "Nothing to
// migrate", or the conflicts that need the owner, which the warnings name,
// and the old agent folders that stay and why (a watcher or an import works
// in one, or its file system can't swap two names).
func migrateShown(
	result operation.Result,
	plan operation.Plan,
	findings []migrate.Finding,
) (operation.Result, error) {
	component := operation.Component{
		Name:    "migrations",
		Status:  operation.StatusUnchanged,
		Message: "Nothing to migrate",
	}
	var err error
	if len(plan.Warnings) > 0 {
		component.Status, component.Message = operation.StatusConflict, ""
		err = operation.Fail(
			operation.ExitConflict,
			"migrate",
			"A migration needs you: deal with each file the warnings name as they say",
		)
	}
	result.Results = append(result.Results, component)
	for _, f := range findings {
		if f.ID == migrate.AgentFolders.ID && f.Status == migrate.Done && f.Detail != "" {
			result.Results = append(result.Results, operation.Component{
				Name:    f.ID,
				Title:   f.ID + " " + f.Title,
				Status:  operation.StatusUnchanged,
				Message: f.Detail,
			})
		}
	}
	return result, err
}
