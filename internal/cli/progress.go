package cli

import (
	"fmt"
	"io"
	"os"
	"time"

	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/term"

	"github.com/Sawmonabo/workbench/internal/display"
	"github.com/Sawmonabo/workbench/internal/operation"
	"github.com/Sawmonabo/workbench/internal/platform"
)

// newProgress shows progress on diagnostics: when it is a terminal that
// reports its size and the run is interactive, the update view for an update
// and one live status line otherwise, else plain lines. The returned function
// ends the live line or view.
func newProgress(o *options, diagnostics io.Writer, update bool) (operation.Progress, func()) {
	// A terminal with no size gets plain lines: there the status line and the
	// update view would draw nothing.
	if file, ok := diagnostics.(*os.File); ok && o.interactive() && isTerminal(file) &&
		reportsSize(file) {
		if update {
			view := newUpdateView(file)
			return view, view.close
		}
		status := &statusLine{output: file}
		return status, status.Stop
	}
	return progressLines{diagnostics}, func() {}
}

// isTerminal reports whether w is a terminal.
func isTerminal(w io.Writer) bool {
	file, ok := w.(*os.File)
	return ok && platform.IsTerminal(file.Fd())
}

// reportsSize reports whether the terminal file gives a width and a height.
// A pseudo-terminal opened with no size (ssh -tt from a script, script
// without a terminal of its own) reports 0.
func reportsSize(file *os.File) bool {
	width, height, err := term.GetSize(file.Fd())
	return err == nil && width > 0 && height > 0
}

// progressLines prints each title and step on its own line.
type progressLines struct{ w io.Writer }

func (p progressLines) Start(title string) { _, _ = fmt.Fprintln(p.w, "==> "+title) }
func (p progressLines) Step(step string)   { _, _ = fmt.Fprintln(p.w, "    "+step) }
func (p progressLines) Stop()              {}

// statusLine draws a spinner, the title, the current step and the time so far
// on one line, the inline pattern of Bubble Tea's package-manager example.
// Each Start runs a Bubble Tea program that Stop quits, clearing the line, so
// prompts and programs given the terminal never share it. The program reads
// no input: the terminal stays in its normal mode, so ctrl+c and ctrl+z act
// as usual, and no terminal query is sent whose late reply a following prompt
// could read.
type statusLine struct {
	output  *os.File
	program *tea.Program
	done    chan struct{}
}

func (s *statusLine) Start(title string) {
	s.Stop()
	s.program = tea.NewProgram(
		statusModel{
			title:   title,
			started: time.Now(),
			spinner: spinner.New(
				spinner.WithSpinner(spinner.MiniDot),
				spinner.WithStyle(lipgloss.NewStyle().Foreground(lipgloss.Cyan)),
			),
		},
		tea.WithInput(nil),
		tea.WithOutput(s.output),
		// main owns SIGINT and SIGTERM through the command's context.
		tea.WithoutSignalHandler(),
	)
	s.done = make(chan struct{})
	go func(program *tea.Program, done chan struct{}) {
		defer close(done)
		_, _ = program.Run()
	}(s.program, s.done)
}

func (s *statusLine) Step(step string) {
	if s.program != nil {
		s.program.Send(stepMsg(step))
	}
}

func (s *statusLine) Stop() {
	if s.program == nil {
		return
	}
	s.program.Send(stopMsg{})
	<-s.done
	s.program = nil
}

type (
	stepMsg string
	stopMsg struct{}
)

type statusModel struct {
	title, step string
	started     time.Time
	width       int
	stopped     bool
	spinner     spinner.Model
}

func (m statusModel) Init() tea.Cmd { return m.spinner.Tick }

func (m statusModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case stepMsg:
		m.step = string(msg)
	case stopMsg:
		// An empty last frame leaves no line behind.
		m.stopped = true
		return m, tea.Quit
	case tea.WindowSizeMsg:
		m.width = msg.Width
	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m statusModel) View() tea.View {
	var view tea.View
	view.DisableBracketedPasteMode = true // nothing is read
	if m.stopped {
		return view
	}
	line := m.spinner.View() + " " + m.title
	if m.step != "" {
		line += ": " + m.step
	}
	elapsed := display.Duration(time.Since(m.started))
	line += lipgloss.NewStyle().Faint(true).Render(" (" + elapsed + ")")
	if m.width > 0 {
		line = lipgloss.NewStyle().MaxWidth(m.width).Render(line)
	}
	view.SetContent(line)
	return view
}
