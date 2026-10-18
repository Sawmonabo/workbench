package cli

import (
	"io"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// Alternate scroll mode, xterm's DECSET 1007
// (https://invisible-island.net/xterm/ctlseqs/ctlseqs.html, "Wheel mice"):
// while a program shows the alternate screen and does not report the mouse,
// the terminal sends a wheel step as ↑ or ↓ instead of scrolling its own
// window, so a view that leaves the mouse to the terminal, for text
// selection, still scrolls under the wheel. The mode's initial state is the
// terminal's own setting (xterm's alternateScroll resource), off in xterm
// itself, so a view that needs it turns it on and puts the terminal's state
// back when it ends. Terminal.app does not know the mode and decides this
// with a setting of its own, so there the sequences are ignored. Bubble Tea
// has no named API for the mode; the sequences go through tea.Raw.
const (
	altScrollMode = ansi.DECMode(1007)
	// XTSAVE and XTRESTORE of the mode (ctlseqs, "CSI ? Pm s" and "CSI ? Pm
	// r"), which x/ansi does not build.
	saveAltScroll    = "\x1b[?1007s"
	restoreAltScroll = "\x1b[?1007r"
)

// altScroll is a view's hold on alternate scroll mode: whether its arming
// write was queued.
type altScroll struct {
	armed bool
}

// armSeq is the arming sequence, written as one payload because separate
// commands in a batch have no order: the save first, so it holds the state
// before the set.
var armSeq = saveAltScroll + ansi.SetMode(altScrollMode)

// arm saves the terminal's alternate scroll state and turns the mode on.
func (a *altScroll) arm() tea.Cmd {
	return tea.Raw(armSeq)
}

// note reads the raw write itself, which Bubble Tea passes to Update once it
// has queued the bytes, so disarm knows the mode was set.
func (a *altScroll) note(msg tea.Msg) {
	if msg, ok := msg.(tea.RawMsg); ok && msg.Msg == armSeq {
		a.armed = true
	}
}

// disarm, after the program has returned, turns the mode off and restores
// the saved state, when the view set it: the reset first, for a terminal
// without XTSAVE and XTRESTORE, which ignores the restore; where the
// terminal has them, the restore then puts back what it held before the
// view.
func (a *altScroll) disarm(w io.Writer) {
	if !a.armed {
		return
	}
	_, _ = io.WriteString(w, ansi.ResetMode(altScrollMode)+restoreAltScroll)
}
