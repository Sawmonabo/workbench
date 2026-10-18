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
// still scrolls under the wheel. Mouse reporting takes precedence while the
// mode is on, so a view can take the mouse on one screen and leave it on
// another. The mode's initial state is the terminal's own setting (xterm's
// alternateScroll resource), off in some terminals, so a view that needs it
// turns it on itself and puts the terminal's state back when it ends.
// Bubble Tea has no named API for the mode; the sequences go through
// tea.Raw.
var altScrollMode = ansi.DECMode(1007)

// XTSAVE and XTRESTORE of the mode (ctlseqs, "CSI ? Pm s" and "CSI ? Pm r"),
// which x/ansi does not build.
const (
	saveAltScroll    = "\x1b[?1007s"
	restoreAltScroll = "\x1b[?1007r"
)

// armAltScroll saves the terminal's alternate scroll state and turns the
// mode on, in one write with the save first: separate commands in a batch
// have no order, and a save after the set would restore the mode on.
func armAltScroll() tea.Cmd {
	return tea.Raw(saveAltScroll + ansi.SetMode(altScrollMode))
}

// disarmAltScroll, after the program has returned, turns the mode off and
// restores the saved state: the reset first, for a terminal without XTSAVE
// and XTRESTORE, which ignores the restore; where the terminal has them, the
// restore then puts back what it had before the view, so a terminal that had
// the mode on keeps it.
func disarmAltScroll(w io.Writer) {
	_, _ = io.WriteString(w, ansi.ResetMode(altScrollMode)+restoreAltScroll)
}
