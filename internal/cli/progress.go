package cli

import (
	"fmt"
	"io"
	"os"
	"time"

	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/Sawmonabo/workbench/internal/operation"
)

// newProgress shows progress on diagnostics: one live status line when it is
// a terminal and the run is interactive, otherwise plain lines. The returned
// function clears the status line.
func newProgress(o *options, diagnostics io.Writer) (operation.Progress, func()) {
	file, ok := diagnostics.(*os.File)
	if ok && !o.nonInteractive && !o.json && operation.IsTerminal(file) {
		status := &statusLine{output: file}
		return status, status.Stop
	}
	return progressLines{diagnostics}, func() {}
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
	elapsed := time.Since(m.started).Round(time.Second)
	line += lipgloss.NewStyle().Faint(true).Render(" (" + elapsed.String() + ")")
	if m.width > 0 {
		line = lipgloss.NewStyle().MaxWidth(m.width).Render(line)
	}
	view.SetContent(line)
	return view
}
