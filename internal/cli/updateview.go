package cli

import (
	"fmt"
	"os"
	"strings"
	"sync"

	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/Sawmonabo/workbench/internal/operation"
)

// updateView draws `workbench update` at a terminal as one live view: a
// header the command prints once, then one working line rewritten in place
// (a spinner beside what the update is doing, then a mark beside the outcome)
// and an indented row for each of Workbench's tools as the update reaches it
// (docs/superpowers/specs/2026-10-20-update-view-design.md, ADR 18).
//
// One inline Bubble Tea program draws the working line and the rows while the
// update works. It ends on an empty frame, and the outcome, its reason and
// the rows are then printed as plain lines: the renderer never writes the
// first lines of a frame taller than the window, which would lose the outcome
// and the start of a long reason. Like the
// status line it reads no input and sends the terminal no query, so nothing
// reaches the next prompt. The release that starts an update hands the
// terminal to the one it activated: before that it clears the working line,
// leaving the cursor on it, and the activated release draws its own view
// from that line on.
type updateView struct {
	output  *os.File
	mu      sync.Mutex
	program *tea.Program
	done    chan struct{}
	// drew is set, before done closes, when the program ran to its end and
	// took the last frame it was sent, and final is then its last state.
	drew  bool
	final updateModel
	// handedOff is set once the view has ended with its working line cleared
	// for the activated release. That release replaces this process unless it
	// cannot start, and then fail writes its line where the cursor was left.
	handedOff bool
	// owned is set once the view has shown the outcome or the failure, which
	// then stands for the command's result lines.
	owned bool
}

func newUpdateView(output *os.File) *updateView { return &updateView{output: output} }

// Start, Step and Stop are the generic progress, which the update view
// replaces with its phases and rows; Stop is not the end of the view, since
// setup stops its progress before the update has finished.
func (*updateView) Start(string) {}
func (*updateView) Step(string)  {}
func (*updateView) Stop()        {}

// Tool shows name's row.
func (v *updateView) Tool(name string, state operation.ToolState, version, previous string) {
	v.send(toolMsg{name: name, state: state, version: version, previous: previous})
}

// Transfer moves the bar of name's download.
func (v *updateView) Transfer(name string, done, total int64) {
	v.send(transferMsg{name: name, done: done, total: total})
}

// header prints the brand, Update and the release the update starts from, or
// Install on a machine with no release: the line the view stays under.
func (v *updateView) header(release string) {
	line := brand.Bold(true).Render("[WorkBench]") + " " + bold.Render("Install")
	if release != "" {
		line = brand.Bold(true).Render("[WorkBench]") + " " + bold.Render("Update") +
			faint.Render(forTerm(" · ")+release)
	}
	_, _ = lipgloss.Fprint(v.output, line+"\n")
}

// phase sets the working line, starting the view if it has not started.
func (v *updateView) phase(phase updatePhase) {
	if v != nil {
		v.send(phaseMsg(phase))
	}
}

// handoff clears the working line and ends the view, leaving the cursor where
// the working line was, for the release taking over the terminal.
func (v *updateView) handoff() {
	v.end(endMsg{clear: true})
	v.mu.Lock()
	v.handedOff = true
	v.mu.Unlock()
}

// finish shows text as the outcome and ends the view. changed says that the
// update installed something.
func (v *updateView) finish(text string, changed bool) {
	if v != nil {
		v.end(endMsg{text: text, unchanged: !changed})
	}
}

// fail shows the failure with its reason, or the stop after an interruption,
// and ends the view. After a handoff that did not take place (the activated
// release could not start) the program has ended, so it writes those lines
// once, where the cursor was left. It does nothing when the view never
// started.
func (v *updateView) fail(interrupted bool, reason string) {
	v.mu.Lock()
	if v.handedOff {
		if !v.owned {
			line := red.Render(glyphs.Cross + " Update failed")
			if interrupted {
				line = faint.Render(forTerm("· Update stopped"))
			}
			_, _ = lipgloss.Fprint(v.output, line+"\n  "+reason+"\n")
			v.owned = true
		}
		v.mu.Unlock()
		return
	}
	v.mu.Unlock()
	v.end(endMsg{text: reason, failed: !interrupted, stopped: interrupted})
}

// shown reports whether the view showed the outcome.
func (v *updateView) shown() bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.owned
}

// close ends a view that is still running, keeping what it shows.
func (v *updateView) close() { v.end(endMsg{keep: true}) }

func (v *updateView) send(msg tea.Msg) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.program == nil {
		if v.done != nil {
			return // ended: a late event changes nothing
		}
		v.start()
	}
	v.program.Send(msg)
}

// start runs the program; the caller holds mu.
func (v *updateView) start() {
	v.program = tea.NewProgram(
		updateModel{spinner: spinner.New(
			spinner.WithSpinner(updateSpinner()),
			spinner.WithStyle(brand),
		)},
		tea.WithInput(nil),
		tea.WithOutput(v.output),
		// main owns SIGINT and SIGTERM through the command's context.
		tea.WithoutSignalHandler(),
	)
	v.done = make(chan struct{})
	go func(program *tea.Program, done chan struct{}) {
		defer close(done)
		model, err := program.Run()
		final, ok := model.(updateModel)
		v.drew, v.final = err == nil && ok && final.end != nil, final
	}(v.program, v.done)
}

// end sends the last message and waits for the program to finish. A kept
// view leaves the cursor below what it shows; otherwise the program leaves
// the cursor where the working line was, and an outcome, a failure or a stop
// is printed from there.
func (v *updateView) end(msg endMsg) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.program == nil {
		return
	}
	v.program.Send(msg)
	<-v.done
	v.program = nil
	if v.drew && !msg.clear && !msg.keep {
		_, _ = lipgloss.Fprint(v.output, strings.Join(v.final.lines(), "\n")+"\n")
		v.owned = true
	}
}

// updateSpinner is the braille dots where the terminal's locale is UTF-8 and
// a turning line elsewhere.
func updateSpinner() spinner.Spinner {
	if glyphs.Check == "✓" {
		return spinner.MiniDot
	}
	return spinner.Line
}

type updatePhase int

const (
	phaseChecking updatePhase = iota
	phaseDownloading
	phaseInstalling
)

type (
	phaseMsg updatePhase
	toolMsg  struct {
		name, version, previous string
		state                   operation.ToolState
	}
	transferMsg struct {
		name        string
		done, total int64
	}
	// endMsg is the last frame: clear shows nothing, keep what is shown;
	// otherwise text is the outcome, or the reason under a failure or a stop.
	// unchanged is an outcome that installed nothing.
	endMsg struct {
		text                                    string
		clear, keep, failed, stopped, unchanged bool
	}
)

// updateRow is one tool's row.
type updateRow struct {
	name, version, previous string
	state                   operation.ToolState
	done, total             int64
	failed, stopped         bool
	// touched is set once this update fetched or installed the tool.
	touched bool
}

type updateModel struct {
	phase       updatePhase
	done, total int64 // the release download
	rows        []updateRow
	end         *endMsg
	width       int
	height      int
	spinner     spinner.Model
}

func (m updateModel) Init() tea.Cmd { return m.spinner.Tick }

func (m updateModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case phaseMsg:
		m.phase = updatePhase(msg)
	case toolMsg:
		m.tool(msg)
	case transferMsg:
		if msg.name == "workbench" {
			m.done, m.total = msg.done, msg.total
			break
		}
		for i := range m.rows {
			if m.rows[i].name == msg.name {
				m.rows[i].done, m.rows[i].total = msg.done, msg.total
			}
		}
	case endMsg:
		// A row still working when the update ends failed, or stopped with it.
		for i := range m.rows {
			if working := m.rows[i].state != operation.ToolReady; working && msg.failed {
				m.rows[i].failed = true
			} else if working && msg.stopped {
				m.rows[i].stopped = true
			}
		}
		m.end = &msg
		return m, tea.Quit
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd
	}
	return m, nil
}

// tool adds or updates a row. A tool being fetched means the update is
// installing, also when the release was already in place; a tool that
// failed its check keeps its version and shows failed.
func (m *updateModel) tool(msg toolMsg) {
	if msg.state == operation.ToolDownloading || msg.state == operation.ToolInstalling {
		m.phase = phaseInstalling
	}
	touched := msg.state == operation.ToolDownloading || msg.state == operation.ToolInstalling
	for i := range m.rows {
		if m.rows[i].name == msg.name {
			m.rows[i].touched = m.rows[i].touched || touched
			if msg.state == operation.ToolFailed {
				m.rows[i].state, m.rows[i].failed = msg.state, true
				return
			}
			m.rows[i].state, m.rows[i].version, m.rows[i].previous = msg.state, msg.version, msg.previous
			if msg.state != operation.ToolDownloading {
				m.rows[i].done, m.rows[i].total = 0, 0
			}
			return
		}
	}
	m.rows = append(m.rows, updateRow{
		name:     msg.name,
		version:  msg.version,
		previous: msg.previous,
		state:    msg.state,
		total:    -1,
		failed:   msg.state == operation.ToolFailed,
		touched:  touched,
	})
}

// toolNames are the names the rows show.
var toolNames = map[string]string{"python3": "Python", "tomlkit": "TOML Kit"}

func toolName(name string) string {
	if shown, ok := toolNames[name]; ok {
		return shown
	}
	return name
}

func (m updateModel) View() tea.View {
	var view tea.View
	view.DisableBracketedPasteMode = true // nothing is read
	if m.end != nil && !m.end.keep {
		return view // the outcome is printed once the program has ended
	}
	// A frame taller than the window would scroll its first lines away, so
	// a short window keeps the working line and the newest rows.
	lines := m.lines()
	if room := m.height - 1; m.height > 0 && len(lines) > room {
		lines = append(lines[:1], lines[len(lines)-max(0, room-1):]...)
	}
	// The renderer clears from its last line down when it ends, so the
	// content ends in an empty line, where the cursor is left.
	view.SetContent(strings.Join(lines, "\n") + "\n")
	return view
}

// lines is the working line and the rows under it; at the end, the outcome,
// the reason of a failure or a stop, and the rows.
func (m updateModel) lines() []string {
	lines := []string{m.working()}
	// An update that installed nothing lists only the tools it had to fetch.
	quiet := m.end != nil && m.end.unchanged
	width := 0
	for _, row := range m.rows {
		width = max(width, lipgloss.Width(toolName(row.name)))
	}
	for _, row := range m.rows {
		if quiet && !row.touched {
			continue
		}
		name := toolName(row.name)
		lines = append(
			lines,
			"  "+name+strings.Repeat(
				" ",
				width+2-lipgloss.Width(name),
			)+row.detail(
				m.barCells(width+4, row.done, row.total),
			),
		)
	}
	if m.width > 0 {
		clip := lipgloss.NewStyle().MaxWidth(m.width)
		for i, line := range lines {
			lines[i] = clip.Render(line)
		}
	}
	// The reason of a failure or a stop is wrapped, never cut.
	if end := m.end; end != nil && (end.failed || end.stopped) && end.text != "" {
		reason := end.text
		if m.width > 4 {
			reason = ansi.Wrap(reason, m.width-2, "")
		}
		wrapped := strings.Split(reason, "\n")
		for i := range wrapped {
			wrapped[i] = "  " + wrapped[i]
		}
		lines = append(lines[:1], append(wrapped, lines[1:]...)...)
	}
	return lines
}

// working is the working line: the phase, or the outcome at the end.
func (m updateModel) working() string {
	if end := m.end; end != nil && !end.keep {
		switch {
		case end.stopped:
			return faint.Render(forTerm("· Update stopped"))
		case end.failed:
			for _, row := range m.rows {
				if row.failed {
					return red.Render(glyphs.Cross + " Update failed: " + toolName(row.name))
				}
			}
			return red.Render(glyphs.Cross + " Update failed")
		case end.unchanged:
			return faint.Render(forTerm("·")) + " " + end.text
		default:
			return green.Render(glyphs.Check + " " + end.text)
		}
	}
	mark := m.spinner.View() + " "
	switch m.phase {
	case phaseDownloading:
		const label = "Downloading  "
		return mark + label + transferBar(
			m.done,
			m.total,
			m.barCells(2+len(label), m.done, m.total),
		)
	case phaseInstalling:
		return mark + "Installing tools"
	default:
		return mark + "Checking for a newer release"
	}
}

// barCells is how wide a bar that starts at column before may be, so that
// the size of done of total after it fits the window: the full width where
// the window is wide or its width is not known yet.
func (m updateModel) barCells(before int, done, total int64) int {
	if m.width <= 0 {
		return barCells
	}
	size := len("  " + transferSize(done, total))
	return max(4, min(barCells, m.width-before-size))
}

// detail is what a row shows after the name, with a bar of at most cells.
func (row updateRow) detail(cells int) string {
	switch {
	case row.failed:
		return red.Render("failed")
	case row.stopped:
		return faint.Render("stopped")
	case row.state == operation.ToolDownloading:
		return transferBar(row.done, row.total, cells)
	case row.state == operation.ToolInstalling:
		return faint.Render("installing")
	case row.previous != "":
		return faint.Render(row.previous) + forTerm(" → ") + row.version
	case row.touched:
		return row.version
	default:
		return faint.Render(row.version)
	}
}

// barCells is the bar's width in a window wide enough for it.
const barCells = 24

// transferBar is a download's bar and its size, or only the bytes received
// when the size is unknown.
func transferBar(done, total int64, cells int) string {
	line := glyphs.Check == "✓" // a thin line where the locale is UTF-8
	if !line {
		cells = max(1, cells-2) // its brackets
	}
	if done == 0 && total <= 0 {
		if line {
			return faint.Render(strings.Repeat("━", cells)) // no byte yet
		}
		return faint.Render("[" + strings.Repeat(" ", cells) + "]")
	}
	if total <= 0 {
		return faint.Render(transferSize(done, done))
	}
	filled := min(cells, int(float64(cells)*float64(done)/float64(total)))
	if line {
		return brand.Render(strings.Repeat("━", filled)) +
			faint.Render(strings.Repeat("━", cells-filled)+"  "+transferSize(done, total))
	}
	head := ">"
	if filled == cells {
		head = ""
	}
	return faint.Render("[") + brand.Render(strings.Repeat("=", filled)+head) +
		faint.Render(strings.Repeat(" ", max(0, cells-filled-len(head)))+"]") +
		faint.Render("  "+transferSize(done, total))
}

// transferSize is done of total in decimal megabytes with one decimal, or in
// whole kilobytes for a total under a megabyte.
func transferSize(done, total int64) string {
	unit, scale, format := "MB", 1e6, "%.1f"
	if total < 1e6 {
		unit, scale, format = "kB", 1e3, "%.0f"
	}
	amount := func(n int64) string { return fmt.Sprintf(format, float64(n)/scale) }
	if done == total {
		return amount(total) + " " + unit
	}
	return amount(done) + "/" + amount(total) + " " + unit
}
