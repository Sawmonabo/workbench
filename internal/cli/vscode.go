package cli

import (
	"fmt"
	"io"
	"strings"

	"github.com/Sawmonabo/workbench/internal/machine"
	"github.com/Sawmonabo/workbench/internal/operation"
	"github.com/spf13/cobra"
)

// maxVSCodeSettings is the largest settings file the filter reads.
const maxVSCodeSettings = 16 << 20

// vscodeSettingsCommand is the filter the VS Code settings modify_ sources
// run, and the one-setting edit of the WSL script runs on a Windows file:
//
//	workbench vscode-settings MANAGED_JSON            standard input -> output
//	workbench vscode-settings --file F [--check] MANAGED_JSON
//
// It is a plain filter, not an action: native chezmoi runs it inside an apply
// that already holds the locks, so it resolves no context, reads no state and
// prints no result envelope. Standard output is the exact new file; a refusal
// is a non-zero exit with one line on standard error, and native leaves the
// file as it was. With --file the file is edited in place: exit 0 is done or
// nothing to change, 3 refused (the file is untouched), and with --check 0 is
// already set and 1 would change.
func vscodeSettingsCommand(o *options) *cobra.Command {
	cmd := &cobra.Command{
		Use:    "vscode-settings MANAGED_JSON",
		Hidden: true,
		Args:   cobra.ExactArgs(1),
	}
	cmd.Flags().
		String("file", "", "Edit this settings file in place instead of filtering standard input")
	cmd.Flags().Bool("check", false, "With --file, only report whether the file would change")
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		file, _ := cmd.Flags().GetString("file")
		check, _ := cmd.Flags().GetBool("check")
		managed := []byte(args[0])
		if check && file == "" {
			return o.filterFailure(cmd, operation.Fail(
				operation.ExitInvalid, "invocation", "--check needs --file",
			), "")
		}
		if file != "" {
			changed, err := machine.EditVSCodeSettingsFile(file, managed, check)
			switch {
			case err != nil:
				return o.filterFailure(cmd, err, "")
			case check && changed:
				// Nothing to say: the exit status is the answer.
				o.rendered = true
				return operation.Fail(
					operation.ExitFailed,
					"vscode_settings",
					"the file would change",
				)
			}
			return nil
		}
		existing, err := io.ReadAll(io.LimitReader(cmd.InOrStdin(), maxVSCodeSettings+1))
		if err != nil {
			return o.filterFailure(cmd, err, "")
		}
		if len(existing) > maxVSCodeSettings {
			return o.filterFailure(cmd, operation.Fail(
				operation.ExitBlocked, "vscode_settings", "it is larger than 16 MiB",
			), "VS Code settings.json was left unchanged: ")
		}
		merged, err := machine.MergeVSCodeSettings(existing, managed)
		if err != nil {
			return o.filterFailure(cmd, err, "VS Code settings.json was left unchanged: ")
		}
		// An empty output would make native remove the file.
		if len(merged) == 0 {
			return o.filterFailure(cmd, operation.Fail(
				operation.ExitFailed, "vscode_settings", "the merge produced no text",
			), "")
		}
		if _, err = cmd.OutOrStdout().Write(merged); err != nil {
			return o.filterFailure(cmd, err, "")
		}
		return nil
	}
	return cmd
}

// filterFailure says err in one line on standard error, after lead, and hands
// it back as the command's exit status. The envelope every other command
// prints is not for a filter whose output is a file.
func (o *options) filterFailure(cmd *cobra.Command, err error, lead string) error {
	o.rendered = true
	_, _ = fmt.Fprintln(cmd.ErrOrStderr(), lead+strings.Join(strings.Fields(err.Error()), " "))
	return err
}
