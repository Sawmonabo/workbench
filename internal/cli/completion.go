package cli

import (
	"slices"
	"strings"

	"github.com/Sawmonabo/workbench/internal/costs"
	"github.com/Sawmonabo/workbench/internal/machine"
	"github.com/Sawmonabo/workbench/internal/operation"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// registerCompletions says what Tab offers for each argument and flag value;
// Cobra's completion command writes the shell scripts that ask for it. A flag
// or argument that names no file offers nothing rather than the shell's file
// list, and a value Workbench reads from this machine (a saved answer, a
// checkpoint) is read without changing anything.
func registerCompletions(root *cobra.Command) {
	paths := map[string]bool{
		"workbench project inspect":   true,
		"workbench project configure": true,
		"workbench project revert":    true,
	}
	files := []string{"answers-from", "bundle", "bundle-directory"}
	directories := []string{"destination"}
	fixed := map[string][]string{
		"by":       costsGroupings,
		"sort":     costsOrders,
		"language": operation.Languages,
	}
	for _, tool := range costs.Tools {
		fixed["tool"] = append(fixed["tool"], tool.Name)
	}
	var walk func(*cobra.Command)
	walk = func(cmd *cobra.Command) {
		switch {
		case paths[cmd.CommandPath()]:
			cmd.ValidArgsFunction = completeDirectories
		case cmd.ValidArgsFunction == nil && len(cmd.ValidArgs) == 0:
			cmd.ValidArgsFunction = cobra.NoFileCompletions
		}
		cmd.LocalFlags().VisitAll(func(flag *pflag.Flag) {
			name := flag.Name
			if _, set := cmd.GetFlagCompletionFunc(name); set || flag.Value.Type() == "bool" {
				return
			}
			var complete cobra.CompletionFunc
			switch {
			case slices.Contains(files, name):
				return // the shell's own file completion
			case slices.Contains(directories, name):
				complete = completeDirectories
			case name == "ask":
				complete = completeAsk
			case name == "checkpoint":
				complete = completeCheckpoint
			case fixed[name] != nil:
				complete = cobra.FixedCompletions(
					fixedChoices(fixed[name]), cobra.ShellCompDirectiveNoFileComp,
				)
			default:
				complete = cobra.NoFileCompletions
			}
			_ = cmd.RegisterFlagCompletionFunc(name, complete)
		})
		for _, child := range cmd.Commands() {
			walk(child)
		}
	}
	walk(root)
}

func fixedChoices(values []string) []cobra.Completion {
	choices := make([]cobra.Completion, len(values))
	for i, value := range values {
		choices[i] = cobra.Completion(value)
	}
	return choices
}

func completeDirectories(
	_ *cobra.Command, _ []string, _ string,
) ([]cobra.Completion, cobra.ShellCompDirective) {
	return nil, cobra.ShellCompDirectiveFilterDirs
}

// completeAsk offers the saved answers --ask can ask again, skipping those
// already named.
func completeAsk(
	_ *cobra.Command, _ []string, typed string,
) ([]cobra.Completion, cobra.ShellCompDirective) {
	c, err := operation.Resolve(operation.Options{ReadOnly: true})
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	askable, err := machine.Askable(c)
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	// --ask is a list: "machine_role,ed" completes the last name after what
	// is already there.
	before, _ := cutLast(typed, ",")
	named := strings.Split(before, ",")
	var choices []cobra.Completion
	for _, key := range askable {
		if !slices.Contains(named, key) {
			choices = append(choices, cobra.Completion(joinNonEmpty(before, key)))
		}
	}
	return choices, cobra.ShellCompDirectiveNoFileComp | cobra.ShellCompDirectiveNoSpace
}

// completeCheckpoint offers the saved checkpoints of the scope the command
// restores, newest first, each described the way the revert list shows it.
func completeCheckpoint(
	cmd *cobra.Command, args []string, _ string,
) ([]cobra.Completion, cobra.ShellCompDirective) {
	selection := operation.Options{ReadOnly: true}
	if cmd.Parent() != nil && cmd.Parent().Name() == "project" {
		selection.Project, selection.Path = true, "."
		if len(args) != 0 {
			selection.Path = args[0]
		}
	}
	if destination, _ := cmd.Flags().GetString("destination"); destination != "" {
		selection.Destination = destination
	}
	c, err := operation.Resolve(selection)
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	checkpoints, err := operation.ListCheckpoints(c)
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	choices := make([]cobra.Completion, 0, len(checkpoints))
	for _, checkpoint := range slices.Backward(checkpoints) {
		choices = append(
			choices,
			cobra.CompletionWithDesc(checkpoint.ID, checkpointLabel(checkpoint)),
		)
	}
	return choices, cobra.ShellCompDirectiveNoFileComp | cobra.ShellCompDirectiveKeepOrder
}

func cutLast(s, sep string) (before, after string) {
	if i := strings.LastIndex(s, sep); i >= 0 {
		return s[:i], s[i+len(sep):]
	}
	return "", s
}

func joinNonEmpty(before, key string) string {
	if before == "" {
		return key
	}
	return before + "," + key
}
