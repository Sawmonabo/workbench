package cli

import (
	"io"
	"os"

	"github.com/spf13/cobra"

	"github.com/Sawmonabo/workbench/internal/machine"
	"github.com/Sawmonabo/workbench/internal/operation"
)

// applyStep makes cmd the command of the apply's effect step. The hidden
// --probe prints the step's probe lines and is never refused; without it, cmd
// does what its own RunE does, and a cmd without one is only the step. Both
// run inside an apply that holds the locks, so they resolve paths only and
// print their own lines.
func applyStep(
	o *options,
	cmd *cobra.Command,
	effect, refusal string,
	probe func(io.Writer) error,
	run func(*cobra.Command) error,
) {
	var probing bool
	own := cmd.RunE
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		switch {
		case probing:
			return o.runPrinted(
				cmd,
				func(*operation.Result) error { return probe(cmd.OutOrStdout()) },
			)
		case own == nil:
			return o.runPrinted(cmd, func(*operation.Result) error {
				return operation.Fail(
					operation.ExitInvalid,
					"invocation",
					cmd.Name()+" is an apply step; it takes --probe or run",
				)
			})
		}
		return own(cmd, args)
	}
	cmd.Flags().BoolVar(&probing, "probe", false, "Print the apply's probe lines")
	_ = cmd.Flags().MarkHidden("probe")
	// run carries the step out and prints what happens. Ticking the step is the
	// approval, so it runs only as that step: with the WORKBENCH_EFFECT_<NAME>=1
	// the apply gives its scripts, and never in a coding agent's session, whose
	// variables the apply doesn't pass on. That stops a run by mistake, not a
	// caller that sets the variable itself.
	cmd.AddCommand(&cobra.Command{
		Use:    "run",
		Short:  "Apply step: carry out the " + effect + " step",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return o.runPrinted(cmd, func(*operation.Result) error {
				if os.Getenv(machine.EffectVariable(effect)) != "1" || operation.AgentSession() {
					return operation.Fail(operation.ExitBlocked, "consent", refusal)
				}
				return run(cmd)
			})
		},
	})
}
