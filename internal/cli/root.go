// Package cli owns command presentation, not provisioning or project policy.
package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
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

func New() *cobra.Command { return newRoot(&options{invocation: os.Args[1:]}) }

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
		err = operation.Fail(2, "invocation", "Invalid command, flags or arguments; run workbench --help")
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
		Use: "workbench", Short: "Inspect developer machines and existing projects",
		Long:    "Workbench previews and coordinates explicit machine provisioning, recoverable configuration changes and existing-project tooling.",
		Version: version(), SilenceErrors: true, SilenceUsage: true, Args: cobra.NoArgs,
	}
	root.SetVersionTemplate("workbench {{.Version}}\n")
	flags := root.PersistentFlags()
	flags.BoolVar(&o.json, "json", false, "Emit one structured result object")
	flags.BoolVar(&o.nonInteractive, "non-interactive", false, "Never prompt; mutations require complete inputs and plan approval")
	flags.StringVar(&o.approvePlan, "approve-plan", "", "Approve exactly the displayed SHA-256 plan digest")
	flags.StringVar(&o.resolve.Source, "source", "", "Select an existing developer source tree")
	flags.StringVar(&o.resolve.MachineConfig, "machine-config", "", "Select an existing private native answer file")
	flags.StringVar(&o.resolve.Destination, "destination", "", "Select an existing configuration destination (default: home)")
	root.AddCommand(doctorCommand(o), statusCommand(o), projectCommand(o))
	root.AddCommand(recoveryCommand(o))
	root.AddCommand(releaseCommands(o)...)
	for _, spec := range []struct{ use, short string }{
		{"plan", "Preview native machine changes and prerequisites"},
		{"apply", "Apply approved native machine changes with file checkpoints"},
	} {
		cmd := &cobra.Command{Use: spec.use, Short: spec.short, Args: cobra.NoArgs, RunE: o.action(false, func(cmd *cobra.Command, c operation.Context) (operation.Result, error) { return machinePlan(cmd, c, o) })}
		if cmd.Name() == "plan" {
			cmd.Flags().Bool("config-only", false, "Preview configuration without provisioning effects")
		}
		if cmd.Name() == "apply" {
			cmd.Flags().Bool("dry-run", false, "Preview only through the shared machine planner")
			cmd.Flags().Bool("config-only", false, "Apply native configuration without provisioning scripts")
		}
		root.AddCommand(cmd)
	}
	return root
}

type handler func(*cobra.Command, operation.Context) (operation.Result, error)

func (o *options) action(project bool, run handler) func(*cobra.Command, []string) error {
	return func(cmd *cobra.Command, args []string) error {
		selection := o.resolve
		selection.Project, selection.ReadOnly = project, true
		if project && len(args) != 0 {
			selection.Path = args[0]
		}
		if project && cmd.Flags().Lookup("language") != nil {
			selection.Languages, _ = cmd.Flags().GetStringArray("language")
		}
		result := operation.NewResult(cmd.CommandPath())
		resolved, err := operation.Resolve(selection)
		if err == nil {
			var state *operation.State
			state, err = operation.ReadState(resolved.Paths)
			if err == nil && cmd.Name() != "install" && cmd.Name() != "update" && cmd.Name() != "pull" && cmd.Name() != "release-check" {
				err = release.ValidateSelection(resolved)
			}
			if err == nil && resolved.Native.Source == "" && !project && (cmd.Name() == "plan" || cmd.Name() == "apply") {
				resolved.Native.Source, err = release.Candidate(resolved)
				if err == nil && resolved.Native.Source == "" && state != nil && state.ActiveRelease != nil {
					resolved.Native.Source = state.ActiveRelease.Source
				}
			}
			if err == nil && !project && (cmd.Name() == "plan" || cmd.Name() == "apply") {
				err = release.CandidateHandoff(cmd.Context(), resolved, o.invocation)
			}
			if err == nil {
				result, err = run(cmd, resolved)
			}
		}
		result.SetError(err)
		o.rendered = true
		if renderErr := render(cmd.OutOrStdout(), cmd.ErrOrStderr(), o.json, result); renderErr != nil {
			return renderErr
		}
		return err
	}
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
