package cli

import (
	"fmt"
	"os/exec"
	"runtime"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

func doctorCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Inventory local tool locations without executing them",
		Long:  "Inventory local tool locations without executing them. This initial diagnostic does not validate versions, profiles, machine answers or readiness to apply configuration.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
			if _, err := fmt.Fprintf(w, "Platform\t%s/%s\nTool\tDiscovery\tLocation (version not checked)\n", runtime.GOOS, runtime.GOARCH); err != nil {
				return err
			}
			missing := false
			// A diagnostic inventory, not a second package installation list.
			for _, name := range []string{"chezmoi", "uv", "python3", "code"} {
				if err := cmd.Context().Err(); err != nil {
					return err
				}
				path, err := exec.LookPath(name)
				if err != nil {
					if _, err := fmt.Fprintf(w, "%s\tnot found safely on PATH\t-\n", name); err != nil {
						return err
					}
					missing = true
					continue
				}
				if _, err := fmt.Fprintf(w, "%s\tfound\t%q\n", name, path); err != nil {
					return err
				}
			}
			if err := w.Flush(); err != nil {
				return err
			}
			cmd.Println("No tools were executed. Tool compatibility and editor host/profile remain unchecked.")
			cmd.Println("Installer and provisioning are unavailable; this is not a readiness check.")
			if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
				return fmt.Errorf("native %s provisioning is outside the planned platform scope", runtime.GOOS)
			}
			if missing {
				return errIncomplete
			}
			return nil
		},
	}
}

func statusCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show development status; release tracking is not implemented",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cmd.Printf("CLI: %s\n", version())
			cmd.Println("Applied configuration: unknown (release tracking not implemented)")
			cmd.Println("Staged candidate: unknown (release staging not implemented)")
			cmd.Println("Drift and partial operations: not inspected")
			cmd.Println("No machine configuration or private state was read or written.")
			return nil
		},
	}
}
