package cli

import (
	"fmt"
	"os"
	"runtime"

	"github.com/Sawmonabo/workbench/internal/operation"
	"github.com/spf13/cobra"
)

func doctorCommand(o *options) *cobra.Command {
	return &cobra.Command{
		Use: "doctor", Short: "Inventory trusted local tool locations without executing them", Args: cobra.NoArgs,
		RunE: o.action(false, func(cmd *cobra.Command, c operation.Context) (operation.Result, error) {
			result := operation.NewResult(cmd.CommandPath())
			result.Results = append(result.Results, operation.Component{Name: "platform", Status: "complete", Message: runtime.GOOS + "/" + runtime.GOARCH})
			cwd, err := operation.ExistingDirectory(".")
			if err != nil {
				return result, err
			}
			missing := false
			for _, name := range []string{"chezmoi", "uv", "python3", "code"} {
				if err := cmd.Context().Err(); err != nil {
					return result, err
				}
				path, err := operation.FindExecutable(name, os.Getenv("PATH"), []string{cwd, c.Native.Source})
				component := operation.Component{Name: name, Status: "complete", Message: fmt.Sprintf("%q", path)}
				if err != nil {
					component.Status, component.Message, missing = "blocked", "Not found outside project-controlled PATH entries", true
				}
				result.Results = append(result.Results, component)
			}
			result.Warnings = append(result.Warnings, "Location inventory only: versions, machine answers and editor host/profile are unchecked. No tools were executed or repaired.")
			if missing {
				return result, operation.Fail(3, "dependency", "Inventory incomplete; provide missing tools through an approved setup stage")
			}
			return result, nil
		}),
	}
}

func statusCommand(o *options) *cobra.Command {
	return &cobra.Command{
		Use: "status", Short: "Inspect current private state without release or drift probes", Args: cobra.NoArgs,
		RunE: o.action(false, func(cmd *cobra.Command, c operation.Context) (operation.Result, error) {
			result := operation.NewResult(cmd.CommandPath())
			state, err := operation.ReadState(c.Paths)
			if err != nil {
				return result, err
			}
			result.Results = append(result.Results, operation.Component{Name: "cli", Status: "complete", Message: version()})
			message := "No recorded Workbench state"
			if state != nil {
				message = "Current state schema and recorded identities validated"
			}
			result.Results = append(result.Results, operation.Component{Name: "state", Status: "complete", Message: message, Details: state})
			result.Warnings = append(result.Warnings, "Release activation, staged candidates and drift are not inspected. No state was created or changed.")
			return result, nil
		}),
	}
}

// Both plan and apply --dry-run use this owner. No native preview is fabricated.
func machinePlan(cmd *cobra.Command, _ operation.Context) (operation.Result, error) {
	return operation.NewResult(cmd.CommandPath()), operation.Fail(3, "not_implemented", "Native machine planning/application is not implemented; no changes were made")
}
