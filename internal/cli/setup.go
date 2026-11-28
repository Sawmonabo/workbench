package cli

import (
	"os"
	"strings"

	"github.com/Sawmonabo/workbench/internal/machine"
	"github.com/Sawmonabo/workbench/internal/operation"
	"github.com/spf13/cobra"
)

// setUp installs the pinned tools Workbench runs, when missing, and, with
// questions, asks the machine questions that the saved answers lack or `answers ask`
// names. Running apply or update is the go-ahead: the tools go into
// Workbench's own directory, and a question asks only once.
func setUp(
	cmd *cobra.Command,
	c operation.Context,
	o *options,
	terminal *os.File,
	result *operation.Result,
	questions bool,
	ask []string,
) (operation.Context, error) {
	planner := machine.ToolsPlan
	if questions {
		planner = machine.SetupPlan
	}
	if questions && terminal == nil {
		if len(ask) > 0 {
			return c, operation.Fail(
				operation.ExitBlocked,
				"terminal",
				"answers ask needs a terminal to ask at",
			)
		}
		saved, err := machine.ReadMachineConfig(c.Native.Config)
		if err != nil {
			return c, operation.Fail(
				operation.ExitBlocked,
				"answers",
				"Without a terminal, setup needs saved answers",
				operation.FixText(c.WorkbenchCommand()+" answers import FILE"),
			)
		}
		// Native init cannot ask here, and a question it reaches fails with
		// a bare EOF, so a missing answer is refused now, by name.
		if err = machine.Unasked(c, saved); err != nil {
			return c, err
		}
	}
	setup, err := planner(cmd.Context(), c)
	if err != nil {
		return c, err
	}
	err = operation.WithMutation(
		cmd.Context(),
		c,
		setup,
		operation.Consent{ApprovedDigest: setup.Digest(), CompleteInputs: true},
		planner,
		func(m *operation.Mutation) error {
			if !questions {
				return machine.InstallTools(cmd.Context(), c, m)
			}
			var setupErr error
			c, setupErr = machine.Setup(cmd.Context(), c, m, terminal, ask)
			return setupErr
		},
	)
	if err != nil {
		return c, err
	}
	var installed []string
	for _, effect := range setup.Effects {
		if name, ok := machine.SetupTool(effect); ok {
			installed = append(installed, machine.ToolTitle(name))
		}
	}
	// Setup plans the answers as an input only when they were saved complete,
	// so without it, or with `answers ask`, the questionnaire saved new ones.
	answered := !questions
	for _, input := range setup.Inputs {
		answered = answered || input.Name == "answers" && len(ask) == 0
	}
	component := operation.Component{
		Name:    "setup",
		Status:  operation.StatusUnchanged,
		Message: "Tools already in place",
	}
	switch {
	case len(installed) > 0:
		component.Status = operation.StatusComplete
		component.Message = "Installed " + strings.Join(installed, ", ")
	case !answered:
		component.Status = operation.StatusComplete
		component.Message = "Saved your machine answers"
	}
	if !o.interactive() {
		component.Details = setup
	}
	result.Results = append(result.Results, component)
	return c, nil
}
