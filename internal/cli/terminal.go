package cli

import (
	"fmt"
	"io"

	"github.com/Sawmonabo/workbench/internal/machine"
	"github.com/Sawmonabo/workbench/internal/operation"
	"github.com/spf13/cobra"
)

// terminalDefaultCommand is the filter the WSL default-distro and
// terminal-adoption steps run on a copy of Windows Terminal's settings.json:
//
//	workbench terminal-default GUID [NAME]  standard input -> output
//	workbench terminal-default --current    standard input -> its defaultProfile
//
// With GUID, standard output is the exact new file, with only defaultProfile
// changed, and the same bytes when it already opens that profile, by its guid
// or by NAME, the profile's name, which a fragment may hold alone. With
// --current it is the defaultProfile as JSON text, or nothing when there is
// none. Like vscode-settings it is a plain filter: a refusal is a non-zero
// exit with one line on standard error, and the script leaves the file as it
// was.
func terminalDefaultCommand(o *options) *cobra.Command {
	var current bool
	cmd := &cobra.Command{
		Use:    "terminal-default [GUID [NAME]]",
		Hidden: true,
		Args:   cobra.MaximumNArgs(2),
	}
	cmd.Flags().BoolVar(&current, "current", false, "Print the defaultProfile instead")
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		if current == (len(args) > 0) {
			return o.filterFailure(cmd, operation.Fail(
				operation.ExitInvalid, "terminal_settings", "give either a GUID or --current",
			), "")
		}
		existing, err := io.ReadAll(io.LimitReader(cmd.InOrStdin(), maxVSCodeSettings+1))
		if err != nil {
			return o.filterFailure(cmd, err, "")
		}
		const lead = "Windows Terminal settings.json was left unchanged: "
		if len(existing) > maxVSCodeSettings {
			return o.filterFailure(cmd, operation.Fail(
				operation.ExitBlocked, "terminal_settings", "it is larger than 16 MiB",
			), lead)
		}
		if current {
			value, err := machine.TerminalDefault(existing)
			if err != nil {
				return o.filterFailure(cmd, err, lead)
			}
			if value != "" {
				_, err = fmt.Fprintln(cmd.OutOrStdout(), value)
			}
			return err
		}
		name := ""
		if len(args) == 2 {
			name = args[1]
		}
		edited, err := machine.SetTerminalDefault(existing, args[0], name)
		if err != nil {
			return o.filterFailure(cmd, err, lead)
		}
		if _, err = cmd.OutOrStdout().Write(edited); err != nil {
			return o.filterFailure(cmd, err, "")
		}
		return nil
	}
	return cmd
}
