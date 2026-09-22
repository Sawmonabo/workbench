package cli

import (
	"context"
	"time"

	"github.com/Sawmonabo/workbench/internal/operation"
	"github.com/Sawmonabo/workbench/internal/project"
	"github.com/spf13/cobra"
)

func projectCommand(o *options) *cobra.Command {
	cmd := &cobra.Command{Use: "project", Short: "Inspect or configure an existing project; never generate one"}
	inspect := &cobra.Command{
		Use: "inspect [PATH]", Short: "Inventory manifest, lockfile and configuration candidates", Args: cobra.MaximumNArgs(1),
		RunE: o.action(true, func(cmd *cobra.Command, c operation.Context) (operation.Result, error) {
			result := operation.NewResult(cmd.CommandPath())
			ctx, cancel := context.WithTimeout(cmd.Context(), 5*time.Second)
			defer cancel()
			inventory, err := project.Inspect(ctx, c.Scope.Root)
			component := operation.Component{Name: "inventory", Status: "complete", Details: inventory}
			if err != nil {
				component.Status = "failed"
				if ctx.Err() == nil {
					err = operation.Fail(1, "inventory", "Inventory incomplete or inaccessible; select a narrower readable scope")
				}
			}
			result.Results = append(result.Results, component)
			result.Warnings = append(result.Warnings, "Filename candidates only: contents, workspace membership, parent ownership and Git ignore rules are not evaluated. No project code was executed or files changed.")
			return result, err
		}),
	}
	configure := unavailable(o, "configure [PATH]", "Configure project tooling (not implemented)", true)
	configure.Flags().StringArray("language", nil, "Narrow configuration by language; repeat to select multiple")
	configure.Flags().Bool("dry-run", false, "Preview only (project planning not implemented)")
	cmd.AddCommand(inspect, configure)
	return cmd
}
