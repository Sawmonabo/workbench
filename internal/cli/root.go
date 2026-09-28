// Package cli owns command presentation, not provisioning or project policy.
package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"runtime/debug"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/Sawmonabo/workbench/internal/operation"
	"github.com/Sawmonabo/workbench/internal/project"
	"github.com/Sawmonabo/workbench/internal/release"
	"github.com/spf13/cobra"
)

type options struct {
	resolve              operation.Options
	json, nonInteractive bool
	approvePlan          string
	rendered             bool
}

// Execute owns one output envelope even when Cobra rejects flags/arguments.
func Execute(ctx context.Context, args []string, in io.Reader, out, diagnostics io.Writer) int {
	o := &options{}
	root := newRoot(o)
	root.SetArgs(args)
	root.SetIn(in)
	root.SetOut(out)
	root.SetErr(diagnostics)
	cmd, err := root.ExecuteContextC(ctx)
	if err != nil && !o.rendered {
		// Cobra stops before parsing flags when the command or a flag is invalid,
		// so o.json is unset here; honor --json from the raw arguments.
		for _, arg := range args {
			if arg == "--" {
				break
			}
			if arg == "--json" || arg == "--json=true" {
				o.json = true
			}
			if arg == "--json=false" {
				o.json = false
			}
		}
		err = operation.Fail(
			operation.ExitInvalid,
			"invocation",
			"Invalid command, flags or arguments; run workbench --help",
		)
		name := "workbench"
		if cmd != nil {
			name = cmd.CommandPath()
		}
		result := operation.NewResult(name)
		result.SetError(err)
		if renderErr := render(out, diagnostics, o.json, result); renderErr != nil {
			return 1
		}
	}
	return int(operation.ExitCode(err))
}

func newRoot(o *options) *cobra.Command {
	root := &cobra.Command{
		Use:           "workbench",
		Short:         "Set up and maintain developer machines and existing projects",
		Long:          "Workbench shows what it will change, asks, then provisions the machine, applies recoverable configuration and configures existing projects.",
		Version:       version(),
		SilenceErrors: true,
		SilenceUsage:  true,
		Args:          cobra.NoArgs,
		RunE:          showHelp,
	}
	root.SetVersionTemplate("workbench {{.Version}}\n")
	flags := root.PersistentFlags()
	flags.BoolVar(&o.json, "json", false, "Emit one structured result object")
	flags.BoolVar(
		&o.nonInteractive,
		"non-interactive",
		false,
		"Never prompt; mutations require complete inputs and plan approval",
	)
	root.AddCommand(
		applyCommand(o),
		updateCommand(o),
		versionCommand(o),
		doctorCommand(o),
		revertCommand(o),
		projectCommand(o),
		initCommand(o),
		releaseCheckCommand(o),
	)
	return root
}

// Each command registers only the selections it uses.

func (o *options) approveFlag(cmd *cobra.Command) {
	cmd.Flags().StringVar(
		&o.approvePlan,
		"approve-plan",
		"",
		"Approve exactly the plan with this SHA-256 digest, for unattended runs",
	)
}

func (o *options) sourceFlag(cmd *cobra.Command) {
	cmd.Flags().StringVar(
		&o.resolve.Source,
		"source",
		"",
		"Use this developer checkout instead of the installed release",
	)
}

func (o *options) machineConfigFlag(cmd *cobra.Command) {
	cmd.Flags().StringVar(
		&o.resolve.MachineConfig,
		"machine-config",
		"",
		"Use this private answer file instead of the saved answers",
	)
}

func (o *options) destinationFlag(cmd *cobra.Command) {
	cmd.Flags().StringVar(
		&o.resolve.Destination,
		"destination",
		"",
		"Configure this existing folder instead of your home",
	)
}

// showHelp makes a command group runnable. Cobra then rejects an unknown
// subcommand through the group's Args, instead of printing help and exiting 0.
func showHelp(cmd *cobra.Command, _ []string) error { return cmd.Help() }

type handler func(*cobra.Command, operation.Context) (operation.Result, error)

// actionKind is what a command operates on, which decides how action prepares
// its context.
type actionKind int

const (
	// machineAction runs against the current release selection.
	machineAction actionKind = iota
	// nativeAction also selects the machine source: --source, else the active
	// release. apply uses it.
	nativeAction
	// projectAction selects the project at PATH, narrowed by --language.
	projectAction
	// releaseAction updates to, lists or checks a release. It skips validating
	// the current selection, which an interrupted update resumes to repair.
	releaseAction
)

func (o *options) action(kind actionKind, run handler) func(*cobra.Command, []string) error {
	return func(cmd *cobra.Command, args []string) error {
		selection := o.resolve
		selection.Project, selection.ReadOnly = kind == projectAction, true
		if kind == projectAction && len(args) != 0 {
			selection.Path = args[0]
		}
		if kind == projectAction && cmd.Flags().Lookup("language") != nil {
			selection.Languages, _ = cmd.Flags().GetStringArray("language")
		}
		result := operation.NewResult(cmd.CommandPath())
		resolved, err := operation.Resolve(selection)
		switch {
		case err != nil:
		case kind == releaseAction:
			// Malformed state still stops a release command before any work.
			_, err = operation.ReadState(resolved.Paths)
		default:
			err = release.ValidateSelection(resolved)
		}
		if err == nil && kind == nativeAction {
			err = selectSource(&resolved)
		}
		if err == nil {
			progress, stop := newProgress(o, cmd.ErrOrStderr())
			defer stop() // clears the status line on a panic
			resolved.Progress = progress
			result, err = run(cmd, resolved)
			stop()
		}
		result.SetError(err)
		o.rendered = true
		if renderErr := render(
			cmd.OutOrStdout(),
			cmd.ErrOrStderr(),
			o.json,
			result,
		); renderErr != nil {
			return renderErr
		}
		return err
	}
}

// selectSource fills in the active release as the machine source of a
// [nativeAction] run without --source.
func selectSource(c *operation.Context) error {
	if c.Native.Source != "" {
		return nil
	}
	state, err := operation.ReadState(c.Paths)
	if state != nil && state.ActiveRelease != nil {
		c.Native.Source = state.ActiveRelease.Source
	}
	return err
}

func render(out, diagnostics io.Writer, asJSON bool, result operation.Result) error {
	if asJSON {
		return json.NewEncoder(out).Encode(result)
	}
	paint, warn := newPainter(out), newPainter(diagnostics)
	for _, component := range result.Results {
		line := component.Name + ": " + string(component.Status)
		if component.Message != "" {
			line += " — " + component.Message
		}
		if _, err := fmt.Fprintln(out, paint.mark(component.Status)+line); err != nil {
			return err
		}
		if err := writeDetails(out, component.Details); err != nil {
			return err
		}
	}
	for _, warning := range result.Warnings {
		if _, err := fmt.Fprintln(
			diagnostics,
			warn.style(yellow, "Warning:"),
			warning,
		); err != nil {
			return err
		}
	}
	for _, problem := range result.Errors {
		if _, err := fmt.Fprintln(
			diagnostics,
			warn.style(red, "✗ Error:"),
			problem.Message,
		); err != nil {
			return err
		}
	}
	if result.Summary != "" && len(result.Errors) == 0 {
		if _, err := fmt.Fprintln(
			out,
			paint.style(green.Bold(true), "✓ "+result.Summary),
		); err != nil {
			return err
		}
	}
	return nil
}

var (
	green  = lipgloss.NewStyle().Foreground(lipgloss.Green)
	red    = lipgloss.NewStyle().Foreground(lipgloss.Red)
	yellow = lipgloss.NewStyle().Foreground(lipgloss.Yellow)
	faint  = lipgloss.NewStyle().Faint(true)
)

// painter colors text only for a terminal, and never when NO_COLOR is set
// (no-color.org), so logs and pipes get plain lines.
type painter bool

func newPainter(w io.Writer) painter {
	file, ok := w.(*os.File)
	return painter(ok && operation.IsTerminal(file) && os.Getenv("NO_COLOR") == "")
}

func (p painter) style(style lipgloss.Style, text string) string {
	if !p {
		return strings.TrimPrefix(strings.TrimPrefix(text, "✓ "), "✗ ")
	}
	return style.Render(text)
}

// mark is a colored symbol before a component line at a terminal: ✓ done,
// · nothing to do, ✗ not done.
func (p painter) mark(status operation.Status) string {
	if !p {
		return ""
	}
	switch status {
	case operation.StatusComplete:
		return green.Render("✓") + " "
	case operation.StatusUnchanged, operation.StatusAbsent, operation.StatusSkipped:
		return faint.Render("·") + " "
	default:
		return red.Render("✗") + " "
	}
}

// writeDetails prints a component's details for people: plans, project
// inventories, release and checkpoint lists in their own views, anything else
// as indented JSON.
func writeDetails(out io.Writer, details any) error {
	switch details := details.(type) {
	case nil:
		return nil
	case operation.Plan:
		return writePlan(out, details)
	case releaseList:
		return writeReleases(out, details)
	case []operation.CheckpointSummary:
		return writeCheckpoints(out, details)
	case *project.Inventory:
		return writeInventory(out, details)
	case *project.Proposal:
		return writeProposal(out, details)
	}
	data, err := json.MarshalIndent(details, "", "  ")
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(out, string(data))
	return err
}

func version() string {
	if buildReleaseVersion != "dev" {
		return buildReleaseVersion
	}
	info, ok := debug.ReadBuildInfo()
	if !ok || info.Main.Version == "" || info.Main.Version == "(devel)" {
		return "dev"
	}
	return info.Main.Version
}
