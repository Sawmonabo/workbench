package cli

import "github.com/spf13/cobra"

// agentCommand groups what Workbench does for AI coding agents: their costs
// and the memory guard that watches the commands they run.
func agentCommand(o *options) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "agent",
		Short: "AI coding agents (Claude Code, Codex): what they cost and the memory usage guard",
		Long: "Claude Code and Codex run Workbench's hooks by themselves: one keeps a ledger of " +
			"what each session cost, the other guards the machine's memory against the commands " +
			"the agents start. These commands show what the hooks recorded.",
		Example: "  workbench agent costs\n" +
			"  workbench agent costs status\n" +
			"  workbench agent guard\n" +
			"  workbench agent guard logs --follow",
		Args: cobra.NoArgs,
		RunE: showHelp,
	}
	cmd.AddCommand(agentCostsCommand(o), agentGuardCommand(o))
	return cmd
}
