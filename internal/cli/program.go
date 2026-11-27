package cli

import (
	"context"
	"os"

	"charm.land/bubbles/v2/spinner"
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

// inlineProgram is an inline Bubble Tea program, drawn on the lines below the
// cursor, the pattern of Bubble Tea's package-manager example. It reads no
// input: the terminal stays in its normal mode, so ctrl+c and ctrl+z act as
// usual, and no terminal query is sent whose late reply a following prompt
// could read.
type inlineProgram struct {
	program *tea.Program
	done    chan struct{}
	// final and err are the program's last model and its error, set before
	// done closes.
	final tea.Model
	err   error
}

// startInline runs model inline on output until end.
func startInline(model tea.Model, output *os.File) *inlineProgram {
	p := &inlineProgram{
		program: tea.NewProgram(
			model,
			tea.WithInput(nil),
			tea.WithOutput(output),
			// main owns SIGINT and SIGTERM through the command's context.
			tea.WithoutSignalHandler(),
		),
		done: make(chan struct{}),
	}
	go func() {
		defer close(p.done)
		p.final, p.err = p.program.Run()
	}()
	return p
}

// send hands the program msg.
func (p *inlineProgram) send(msg tea.Msg) { p.program.Send(msg) }

// end sends msg, the last message, which quits the program, waits for the
// program to finish and returns its last model and its error.
func (p *inlineProgram) end(msg tea.Msg) (tea.Model, error) {
	p.program.Send(msg)
	<-p.done
	return p.final, p.err
}

// inlineSpinner is the braille dots where the terminal's locale is UTF-8 and
// a turning line elsewhere.
func inlineSpinner() spinner.Spinner {
	if glyphs.Check == "✓" {
		return spinner.MiniDot
	}
	return spinner.Line
}
