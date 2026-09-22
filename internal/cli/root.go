// Package cli owns command presentation, not provisioning or project policy.
package cli

import (
	"errors"
	"fmt"
	"runtime/debug"

	"github.com/spf13/cobra"
)

// New returns an independent command tree; there is no global command state.
func New() *cobra.Command {
	root := &cobra.Command{
		Use:           "workbench",
		Short:         "Inspect developer machines and existing projects",
		Long:          "Workbench is in development. Read-only inventories are available; installation and configuration changes are not yet implemented.",
		Version:       version(),
		SilenceErrors: true,
		SilenceUsage:  true,
		Args:          cobra.NoArgs,
	}
	root.SetVersionTemplate("workbench {{.Version}}\n")
	root.AddCommand(doctorCommand(), statusCommand(), projectCommand())
	for _, spec := range []struct{ use, short string }{
		{"pull [version]", "Stage a release (not implemented)"},
		{"plan", "Preview machine changes (not implemented)"},
		{"apply", "Apply machine configuration (not implemented)"},
		{"update", "Pull and apply a release (not implemented)"},
		{"revert [version]", "Recover configuration (not implemented)"},
	} {
		cmd := unavailable(spec.use, spec.short)
		if cmd.Name() == "apply" {
			cmd.Flags().Bool("dry-run", false, "Preview only; the planner is not implemented")
			cmd.Flags().Bool("config-only", false, "Exclude provisioning; application is not implemented")
		}
		root.AddCommand(cmd)
	}
	return root
}

func unavailable(use, short string) *cobra.Command {
	return &cobra.Command{
		Use:   use,
		Short: short,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return fmt.Errorf("%s is not implemented; no changes were made", cmd.CommandPath())
		},
	}
}

func version() string {
	info, ok := debug.ReadBuildInfo()
	if !ok || info.Main.Version == "" || info.Main.Version == "(devel)" {
		return "dev"
	}
	return info.Main.Version
}

var errIncomplete = errors.New("inventory incomplete; review the reported limitations")
