package cli

import (
	"context"
	"fmt"
	"text/tabwriter"
	"time"

	"github.com/Sawmonabo/workbench/internal/project"
	"github.com/spf13/cobra"
)

func projectCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "project",
		Short: "Inspect or configure an existing project; never generate one",
	}
	inspect := &cobra.Command{
		Use:   "inspect [PATH]",
		Short: "Inventory manifest, lockfile and configuration candidates",
		Long:  "Inventory candidates under an existing directory (default: current directory). No manifest contents or project code are executed. Workspace membership and configuration ownership are not yet resolved.",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			path := "."
			if len(args) != 0 {
				path = args[0]
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), 5*time.Second)
			defer cancel()
			result, scanErr := project.Inspect(ctx, path)
			if result == nil {
				return scanErr
			}
			cmd.Printf("Scope: %q\n", result.Directory)
			w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
			if _, err := fmt.Fprintln(w, "Kind\tEcosystem\tPath"); err != nil {
				return err
			}
			for _, item := range result.Items {
				if _, err := fmt.Fprintf(w, "%s\t%s\t%q\n", item.Kind, item.Ecosystem, item.Path); err != nil {
					return err
				}
			}
			if err := w.Flush(); err != nil {
				return err
			}
			cmd.Printf("Entries inspected: %d; excluded directories: %d; skipped links/special files: %d\n", result.Entries, result.Excluded, result.Skipped)
			cmd.Println("Candidate inventory only: contents, workspace membership, parent ownership and Git ignore rules are not evaluated.")
			cmd.Println("Project configuration is not implemented for any language. No files were changed.")
			if scanErr != nil {
				return fmt.Errorf("inventory incomplete: %w", scanErr)
			}
			return nil
		},
	}
	configure := unavailable("configure [PATH]", "Configure project tooling (not implemented)")
	configure.Args = cobra.MaximumNArgs(1)
	configure.Flags().StringArray("language", nil, "Narrow configuration by language; repeat to select multiple")
	configure.Flags().Bool("dry-run", false, "Preview only; project configuration is not implemented")
	cmd.AddCommand(inspect, configure)
	return cmd
}
