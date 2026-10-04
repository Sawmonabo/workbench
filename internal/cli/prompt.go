package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/huh/v2"
	"charm.land/lipgloss/v2"
	"github.com/spf13/cobra"

	"github.com/Sawmonabo/workbench/internal/machine"
	"github.com/Sawmonabo/workbench/internal/operation"
	pythonpolicy "github.com/Sawmonabo/workbench/project/python"
)

// interactive reports whether this run may prompt at all.
func (o *options) interactive() bool { return !o.nonInteractive && !o.json }

// shownAtPrompt drops a plan component's details after an interactive
// approval, which already showed the plan; JSON and unattended runs keep them.
func shownAtPrompt(o *options, component *operation.Component) {
	if o.interactive() {
		component.Details = nil
	}
}

// agentRefusal is why a coding agent's run never prompts or applies unasked.
const agentRefusal = "An agent runs this; use --dry-run --json then --approve-plan"

// openTerminal opens the controlling terminal for a prompt. A run without one,
// or by an agent, never approves or chooses anything.
func openTerminal() (*os.File, error) {
	if operation.AgentSession() {
		return nil, operation.Fail(operation.ExitBlocked, "consent", agentRefusal)
	}
	terminal, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return nil, operation.Fail(
			operation.ExitBlocked,
			"consent",
			"No terminal is available; supply complete inputs and --approve-plan",
		)
	}
	return terminal, nil
}

// hasTerminal reports whether a person's controlling terminal can be opened, as
// [openTerminal] would, without keeping it.
func hasTerminal() bool {
	terminal, err := openTerminal()
	if err != nil {
		return false
	}
	_ = terminal.Close()
	return true
}

// ask runs one huh field on the terminal and reports whether it was answered;
// esc or ctrl+c leaves it unanswered.
func ask(terminal *os.File, field huh.Field) (bool, error) {
	keys := huh.NewDefaultKeyMap()
	keys.Quit = key.NewBinding(key.WithKeys("esc", "ctrl+c"), key.WithHelp("esc", "cancel"))
	err := huh.NewForm(huh.NewGroup(field)).
		WithKeyMap(keys).
		WithInput(terminal).
		WithOutput(terminal).
		Run()
	if errors.Is(err, huh.ErrUserAborted) {
		return false, nil
	}
	return err == nil, err
}

// confirmPlan shows the plan on the controlling terminal and asks Yes or No;
// No is the default, so Enter alone approves nothing.
func confirmPlan(plan operation.Plan, digest string) (bool, error) {
	terminal, err := openTerminal()
	if err != nil {
		return false, err
	}
	defer func() { _ = terminal.Close() }()
	if err := writePlan(terminal, plan); err != nil {
		return false, err
	}
	if _, err := fmt.Fprintf(terminal, "\nPlan digest: %s\n\n", digest); err != nil {
		return false, err
	}
	approved := false
	answered, err := ask(terminal, huh.NewConfirm().
		Title("Approve this exact plan?").
		Affirmative("Yes").
		Negative("No").
		Value(&approved))
	if err != nil {
		return false, operation.Fail(
			operation.ExitBlocked,
			"consent",
			"No complete approval was received",
		)
	}
	approved = answered && approved
	// huh clears the prompt once answered; leave the answer on screen.
	answer := "No"
	if approved {
		answer = "Yes"
	}
	_, _ = fmt.Fprintln(terminal, "Approve this exact plan? "+answer)
	return approved, nil
}

// choosePlan shows the live plan on the controlling terminal: the files and
// the effects with the plan's checks as defaults. `a` approves exactly that
// selection; q, esc or ctrl+c approves nothing. The list is dismissed by
// leaving the alternate screen, and when approved its final frame is printed
// once, with nothing blank below it. selection is the one the plan was made from: it holds
// each Windows setup part's own choice, which the list keeps while the parent
// includes the part. The names that come back are the effects the owner left
// checked, a part by its own choice; the caller re-gates the plan from them.
func choosePlan(
	plan operation.Plan,
	selection machine.Selection,
	verbose bool,
) ([]string, bool, error) {
	terminal, err := openTerminal()
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = terminal.Close() }()
	// main owns SIGINT; ctrl+c arrives as a key.
	final, err := tea.NewProgram(
		newChecklist(plan, selection, verbose),
		tea.WithInput(terminal),
		tea.WithOutput(terminal),
		tea.WithoutSignalHandler(),
	).Run()
	if err != nil {
		return nil, false, operation.Fail(
			operation.ExitBlocked,
			"consent",
			"No complete approval was received",
		)
	}
	list, ok := final.(*checklistModel)
	if !ok {
		return nil, false, nil
	}
	if !list.approved {
		// Nothing was chosen: no frame of toggled boxes that reads as a plan.
		return nil, false, nil
	}
	if err := printPlanView(
		terminal,
		list.plan,
		planView{Interactive: true, Done: true},
	); err != nil {
		return nil, false, err
	}
	return list.checkedNames(), true, nil
}

// checkpointChoice returns --checkpoint or, at a terminal, the checkpoint the
// user picks. Otherwise it lists the checkpoints under name and asks for
// --checkpoint.
func (o *options) checkpointChoice(
	cmd *cobra.Command,
	c operation.Context,
	result *operation.Result,
	name string,
) (string, error) {
	if id, _ := cmd.Flags().GetString("checkpoint"); id != "" {
		return id, nil
	}
	checkpoints, err := operation.ListCheckpoints(c)
	if err != nil {
		return "", err
	}
	if len(checkpoints) == 0 {
		return "", operation.Fail(
			operation.ExitBlocked,
			"selection",
			"No checkpoints are saved here",
		)
	}
	if o.interactive() {
		if terminal, openErr := openTerminal(); openErr == nil {
			defer func() { _ = terminal.Close() }()
			return chooseCheckpoint(terminal, checkpoints)
		}
	}
	result.Results = append(result.Results, operation.Component{
		Name:    name,
		Status:  operation.StatusComplete,
		Details: checkpoints,
	})
	return "", operation.Fail(
		operation.ExitBlocked,
		"selection",
		"Choose a checkpoint ID from this list with --checkpoint",
	)
}

// chooseCheckpoint asks which checkpoint to restore, newest first.
func chooseCheckpoint(
	terminal *os.File,
	checkpoints []operation.CheckpointSummary,
) (string, error) {
	options := make([]huh.Option[string], 0, len(checkpoints))
	for _, checkpoint := range slices.Backward(checkpoints) {
		options = append(options, huh.NewOption(checkpointLabel(checkpoint), checkpoint.ID))
	}
	var id string
	answered, err := ask(terminal, huh.NewSelect[string]().
		Title("Restore which checkpoint?").
		Options(options...).
		Value(&id))
	if err != nil || !answered {
		return "", operation.Fail(
			operation.ExitBlocked,
			"selection",
			"No checkpoint was chosen; nothing restored",
		)
	}
	return id, nil
}

// writeCheckpoints prints one checkpoint per line, newest first, with the ID
// that --checkpoint takes.
func writeCheckpoints(w io.Writer, checkpoints []operation.CheckpointSummary) error {
	rows := make([][]string, 0, len(checkpoints))
	for _, checkpoint := range slices.Backward(checkpoints) {
		rows = append(rows, []string{checkpoint.ID, checkpointLabel(checkpoint)})
	}
	var b strings.Builder
	writeTable(&b, terminalWidth(w), 4, tableSpec{Cols: []column{{}, {Clip: true}}, Rows: rows})
	_, err := lipgloss.Fprint(w, b.String())
	return err
}

// checkpointLabel names when a checkpoint was saved and what restoring it
// does: a forward checkpoint undoes its apply, its recovery pair redoes it.
func checkpointLabel(checkpoint operation.CheckpointSummary) string {
	label := checkpoint.Created.Local().Format("Jan 2 3:04 PM") + "  "
	if checkpoint.Recovery {
		label += "redo the apply of " + sourceName(checkpoint.Before)
	} else {
		label += "undo the apply of " + sourceName(checkpoint.Applied)
	}
	if checkpoint.Status == operation.JournalRunning ||
		checkpoint.Status == operation.JournalPartial {
		label += " (did not finish)"
	}
	return label
}

// sourceName names a configuration source for people.
func sourceName(source *operation.SourceIdentity) string {
	switch {
	case source == nil:
		return "nothing"
	case source.Release == "developer":
		return "developer checkout " + shortDigest(source.ContentDigest)
	case source.Release == pythonpolicy.ID:
		return "project policy " + source.Release
	default:
		return "release " + source.Release
	}
}
