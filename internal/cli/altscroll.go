package cli

import (
	"io"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// Alternate scroll mode, xterm's DECSET 1007
// (https://invisible-island.net/xterm/ctlseqs/ctlseqs.html, "Wheel mice"):
// while a program shows the alternate screen and has not taken the mouse,
// the terminal sends a wheel step as ↑ or ↓ instead of scrolling its own
// buffer, so a view that leaves the mouse to the terminal for text selection
// still scrolls under the wheel. In xterm, as ctlseqs describes, mouse
// reporting takes precedence while the mode is on, so a view can take the
// mouse on one screen and leave it on another. The mode's initial state is
// the terminal's own setting (xterm's alternateScroll resource), off in some
// terminals, so a view that needs it turns it on itself and, when it ends,
// leaves the terminal as it found it where the terminal reports the mode
// (DECRQM) or saves it (XTSAVE). Bubble Tea has no named API for the mode;
// the sequences go through tea.Raw.
const (
	altScrollMode = ansi.DECMode(1007)
	// XTSAVE and XTRESTORE of the mode (ctlseqs, "CSI ? Pm s" and "CSI ? Pm
	// r"), which x/ansi does not build.
	saveAltScroll    = "\x1b[?1007s"
	restoreAltScroll = "\x1b[?1007r"
)

// altScroll is a view's hold on alternate scroll mode: whether its arming
// write was queued, and whether the terminal reported the mode already on
// before the view set it.
type altScroll struct {
	armed bool
	wasOn bool
}

// armCmd is the arming write, as one payload because separate commands in a
// batch have no order: the query (DECRQM) first, so its answer is the state
// before the set; then the save, before the set for the same reason; then
// the set.
var armCmd = ansi.RequestMode(altScrollMode) + saveAltScroll + ansi.SetMode(altScrollMode)

// arm asks the terminal for the mode's state, saves it and turns the mode on.
func (a *altScroll) arm() tea.Cmd {
	return tea.Raw(armCmd)
}

// note reads the messages that concern the mode: the raw write itself, which
// Bubble Tea passes to Update once it has queued the bytes, and the
// terminal's report of the mode's state from before the set.
func (a *altScroll) note(msg tea.Msg) {
	switch msg := msg.(type) {
	case tea.RawMsg:
		if msg.Msg == armCmd {
			a.armed = true
		}
	case tea.ModeReportMsg:
		if msg.Mode == altScrollMode && msg.Value.IsSet() {
			a.wasOn = true
		}
	}
}

// disarm, after the program has returned, leaves the terminal as the view
// found it. Nothing is written when the view never armed the mode, or when
// the terminal reported it already on. Otherwise the mode is turned off and
// the saved state restored: the reset first, for a terminal without XTSAVE
// and XTRESTORE, which ignores the restore; where the terminal has them, the
// restore then puts back what it held before the view.
func (a *altScroll) disarm(w io.Writer) {
	if !a.armed || a.wasOn {
		return
	}
	_, _ = io.WriteString(w, ansi.ResetMode(altScrollMode)+restoreAltScroll)
}
