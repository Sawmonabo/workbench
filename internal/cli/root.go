// Package cli owns command presentation, not provisioning or project policy.
package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"runtime/debug"

	"github.com/Sawmonabo/workbench/internal/operation"
	"github.com/Sawmonabo/workbench/internal/release"
	"github.com/spf13/cobra"
)

type options struct {
	resolve              operation.Options
	json, nonInteractive bool
	approvePlan          string
	rendered             bool
	invocation           []string
}

// Execute owns one output envelope even when Cobra rejects flags/arguments.
func Execute(ctx context.Context, args []string, in io.Reader, out, diagnostics io.Writer) int {
	o := &options{invocation: append([]string(nil), args...)}
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
			2,
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
	return operation.ExitCode(err)
}

func newRoot(o *options) *cobra.Command {
	root := &cobra.Command{
		Use:           "workbench",
		Short:         "Inspect developer machines and existing projects",
		Long:          "Workbench previews and coordinates explicit machine provisioning, recoverable configuration changes and existing-project tooling.",
		Version:       version(),
		SilenceErrors: true,
		SilenceUsage:  true,
		Args:          cobra.NoArgs,
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
	flags.StringVar(
		&o.approvePlan,
		"approve-plan",
		"",
		"Approve exactly the displayed SHA-256 plan digest",
	)
	flags.StringVar(&o.resolve.Source, "source", "", "Select an existing developer source tree")
	flags.StringVar(
		&o.resolve.MachineConfig,
		"machine-config",
		"",
		"Select an existing private native answer file",
	)
	flags.StringVar(
		&o.resolve.Destination,
		"destination",
		"",
		"Select an existing configuration destination (default: home)",
	)
	root.AddCommand(doctorCommand(o), statusCommand(o), initCommand(o), projectCommand(o))
	root.AddCommand(recoveryCommand(o))
	root.AddCommand(machineCommands(o)...)
	root.AddCommand(releaseCommands(o)...)
	return root
}

type handler func(*cobra.Command, operation.Context) (operation.Result, error)

// actionKind is what a command operates on, which decides how action prepares
// its context.
type actionKind int

const (
	// machineAction runs against the current release selection.
	machineAction actionKind = iota
	// nativeAction also selects the machine source: --source, else the staged
	// candidate, else the active release. It hands off to the candidate's own
	// runtime when that is not this executable. plan and apply use it.
	nativeAction
	// projectAction selects the project at PATH, narrowed by --language.
	projectAction
	// releaseAction stages, installs or checks a release. It skips validating
	// the current selection, which an interrupted install resumes to repair.
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
			err = o.selectSource(cmd.Context(), &resolved)
		}
		if err == nil {
			result, err = run(cmd, resolved)
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

// selectSource fills in the machine source for a [nativeAction] and hands off
// to a staged candidate's runtime.
func (o *options) selectSource(ctx context.Context, c *operation.Context) error {
	if c.Native.Source == "" {
		source, err := release.Candidate(*c)
		if err != nil {
			return err
		}
		if source == "" {
			state, err := operation.ReadState(c.Paths)
			if err != nil {
				return err
			}
			if state != nil && state.ActiveRelease != nil {
				source = state.ActiveRelease.Source
			}
		}
		c.Native.Source = source
	}
	return release.CandidateHandoff(ctx, *c, o.invocation)
}

func render(out, diagnostics io.Writer, asJSON bool, result operation.Result) error {
	if asJSON {
		return json.NewEncoder(out).Encode(result)
	}
	for _, component := range result.Results {
		if _, err := fmt.Fprintf(out, "%s: %s", component.Name, component.Status); err != nil {
			return err
		}
		if component.Message != "" {
			if _, err := fmt.Fprintf(out, " — %s", component.Message); err != nil {
				return err
			}
		}
		if _, err := fmt.Fprintln(out); err != nil {
			return err
		}
		if component.Details != nil {
			data, err := json.MarshalIndent(component.Details, "", "  ")
			if err != nil {
				return err
			}
			if _, err := fmt.Fprintln(out, string(data)); err != nil {
				return err
			}
		}
	}
	for _, warning := range result.Warnings {
		if _, err := fmt.Fprintln(diagnostics, "Warning:", warning); err != nil {
			return err
		}
	}
	for _, problem := range result.Errors {
		if _, err := fmt.Fprintln(diagnostics, "Error:", problem.Message); err != nil {
			return err
		}
	}
	return nil
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
