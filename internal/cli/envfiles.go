package cli

import (
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Sawmonabo/workbench/internal/machine"
	"github.com/Sawmonabo/workbench/internal/operation"
)

// envFilesCommand is only the apply's env-files step: --probe prints what it
// would do and run creates the roles' missing env files
// (internal/machine/envfiles.go).
func envFilesCommand(o *options) *cobra.Command {
	cmd := &cobra.Command{Use: "env-files", Hidden: true, Args: cobra.NoArgs}
	applyStep(
		o,
		cmd,
		"env-files",
		"env-files run is the apply's env-files step; run workbench apply",
		func(out io.Writer) error {
			c, answers, err := stepAnswers()
			if err != nil {
				return err
			}
			_, err = fmt.Fprintln(out, strings.Join(machine.EnvFilesProbe(c, answers), "\n"))
			return err
		},
		func(cmd *cobra.Command) error {
			c, answers, err := stepAnswers()
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			return machine.EnvFilesRun(cmd.Context(), c, answers, func(line string) {
				_, _ = fmt.Fprintln(out, "==> env-files: "+line)
			})
		},
	)
	return cmd
}

// stepAnswers resolves Workbench's paths and reads the saved answers for an
// apply step, which runs inside the apply that holds the locks: it reads
// paths only.
func stepAnswers() (operation.Context, machine.Answers, error) {
	c, err := operation.Resolve(operation.Options{ReadOnly: true})
	if err != nil {
		return c, nil, err
	}
	answers, err := machine.ReadAnswers(c)
	return c, answers, err
}
