package cli

import (
	"context"
	"os"

	tea "charm.land/bubbletea/v2"
)

// runFullScreen runs model, a full-screen view, reading input and drawing on
// output until it quits or ctx ends, and returns its last model. main owns
// the signals and ends ctx on SIGINT, SIGTERM and SIGHUP, so the program
// installs no handler of its own, and ctrl+c arrives as a key.
func runFullScreen(
	ctx context.Context,
	model tea.Model,
	input, output *os.File,
) (tea.Model, error) {
	return tea.NewProgram(
		model,
		tea.WithInput(input),
		tea.WithOutput(output),
		tea.WithContext(ctx),
		tea.WithoutSignalHandler(),
	).Run()
}
