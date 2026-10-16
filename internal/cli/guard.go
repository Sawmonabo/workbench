package cli

import (
	"github.com/spf13/cobra"

	"github.com/Sawmonabo/workbench/internal/guard"
)

// guardCommand groups the agent command guard's entries. Claude Code and
// Codex run pre and post as hooks, and pre starts watch with fixed arguments.
// Like askpass they are plain commands, not actions, and print no result
// envelope. pre and post take any arguments, flags included (flag parsing is
// off, so even the root's --json is just an argument), so nothing an agent
// passes makes them exit non-zero, and print only what the hook protocol
// reads.
func guardCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:    "guard",
		Short:  "Agent command memory guard (hooks and watcher)",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE:   showHelp,
	}
	cmd.AddCommand(
		&cobra.Command{
			Use:                "pre",
			DisableFlagParsing: true,
			Args:               cobra.ArbitraryArgs,
			RunE: func(cmd *cobra.Command, _ []string) error {
				guard.Pre(stdinFile(cmd))
				return nil
			},
		},
		&cobra.Command{
			Use:                "post",
			DisableFlagParsing: true,
			Args:               cobra.ArbitraryArgs,
			RunE: func(cmd *cobra.Command, _ []string) error {
				guard.Post(stdinFile(cmd), cmd.OutOrStdout())
				return nil
			},
		},
		&cobra.Command{
			Use:  "watch",
			Args: cobra.NoArgs,
			RunE: func(cmd *cobra.Command, _ []string) error {
				guard.Watch(cmd.Context())
				return nil
			},
		},
	)
	return cmd
}
