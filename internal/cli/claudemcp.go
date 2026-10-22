package cli

import (
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Sawmonabo/workbench/internal/machine"
)

// claudeMCPCommand is only the apply's claude-mcp step: --probe prints what it
// would do and run prints the claude commands the step's script runs
// (internal/machine/claudemcp.go), so Claude Code writes its own file and the
// script names no server. Every field run prints ends in a NUL: the claude
// path, the nvm Node bin folder, then each command's arguments, each command
// ended by an empty field.
func claudeMCPCommand(o *options) *cobra.Command {
	cmd := &cobra.Command{Use: "claude-mcp", Hidden: true, Args: cobra.NoArgs}
	applyStep(
		o,
		cmd,
		"claude-mcp",
		"claude-mcp run is the apply's claude-mcp step; run workbench apply",
		func(out io.Writer) error {
			c, answers, err := stepAnswers()
			if err != nil {
				return err
			}
			_, err = fmt.Fprintln(out, strings.Join(machine.ClaudeMCPProbe(c, answers), "\n"))
			return err
		},
		func(cmd *cobra.Command) error {
			c, answers, err := stepAnswers()
			if err != nil {
				return err
			}
			claude, nodeBin, commands, err := machine.ClaudeMCPCommands(c, answers)
			if err != nil {
				return err
			}
			var fields strings.Builder
			fields.WriteString(claude + "\x00" + nodeBin + "\x00")
			for _, command := range commands {
				fields.WriteString(strings.Join(command, "\x00") + "\x00\x00")
			}
			_, err = io.WriteString(cmd.OutOrStdout(), fields.String())
			return err
		},
	)
	return cmd
}
