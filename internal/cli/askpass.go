package cli

import (
	"github.com/Sawmonabo/workbench/internal/operation"
	"github.com/spf13/cobra"
)

// askpassCommand is the program sudo runs as SUDO_ASKPASS during an apply at a
// terminal on a Mac; the helper script in the apply's private folder runs it as
//
//	workbench askpass -- SOCKET PROMPT
//
// sudo's prompt text is ignored. Like vscode-settings it is a plain filter, not
// an action: it resolves no context and prints no result envelope. Standard
// output is the password, which sudo reads; a refusal is a non-zero exit with
// one line on standard error. Nothing is printed unless Workbench's apply, whose
// process started this one, answers (see [operation.AskPass]).
func askpassCommand(o *options) *cobra.Command {
	return &cobra.Command{
		Use:    "askpass SOCKET",
		Hidden: true,
		Args:   cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := operation.AskPass(cmd.Context(), args[0], cmd.OutOrStdout()); err != nil {
				return o.filterFailure(cmd, err, "")
			}
			return nil
		},
	}
}
