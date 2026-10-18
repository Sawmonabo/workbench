package cli

import (
	"io"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// The two terminal modes that make the mouse wheel scroll a full-screen view
// that does not report the mouse. Left to the terminal, the wheel scrolls
// the terminal's own window; these make it send a wheel step on the
// alternate screen as ↑ or ↓ instead, so a plain drag still selects text and
// the wheel still moves the view.
//
//   - Application cursor keys (DECCKM, DECSET 1): Terminal.app sends the
//     wheel as arrow keys on the alternate screen only while this mode is on
//     (observed with a byte probe; the name of its hidden preference
//     ScrollAlternateScreenWithoutApplicationCursorMode suggests the rule),
//     which is why less and vim, which send it by default, scroll there and
//     a program that leaves the mode off does not. The mode changes the arrow
//     keys' encoding to SS3 (ESC O A), which Bubble Tea decodes as the same
//     keys. Programs that read keys through terminfo set it on entry and
//     reset it on exit (the DECCKM half of smkx and rmkx), as the view does.
//   - Alternate scroll (DECSET 1007, xterm's "Wheel mice" in
//     https://invisible-island.net/xterm/ctlseqs/ctlseqs.html): in xterm the
//     wheel is sent as ↑ or ↓ on the alternate screen only while this mode is
//     on; it is off there by default and on in most other terminals that
//     have it. The view saves the terminal's state (XTSAVE), sets the mode and
//     restores the state at exit (XTRESTORE), a restore alone so that a
//     terminal without XTSAVE, Windows Terminal among them, keeps its own
//     setting. Terminal.app does not know this mode.
//
// Bubble Tea has no named API for either; the sequences go through tea.Raw.
const (
	altScrollMode = ansi.DECMode(1007)
	// XTSAVE and XTRESTORE of the alternate scroll mode (ctlseqs, "CSI ? Pm
	// s" and "CSI ? Pm r"), which x/ansi does not build.
	saveAltScroll    = "\x1b[?1007s"
	restoreAltScroll = "\x1b[?1007r"
)

// wheelModes is a view's hold on the two modes: whether its arming write was
// queued.
type wheelModes struct {
	armed bool
}

// armSeq is the arming sequence, written as one payload because separate
// commands in a batch have no order: the save first, so it holds the
// alternate scroll state before the set; then the two sets, one sequence
// each.
var armSeq = saveAltScroll + ansi.SetMode(altScrollMode) + ansi.SetModeCursorKeys

// arm saves the terminal's alternate scroll state and turns both modes on.
func (m *wheelModes) arm() tea.Cmd {
	return tea.Raw(armSeq)
}

// note reads the raw write itself, which Bubble Tea passes to Update once it
// has queued the bytes, so disarm knows the arming write was queued.
func (m *wheelModes) note(msg tea.Msg) {
	if msg, ok := msg.(tea.RawMsg); ok && msg.Msg == armSeq {
		m.armed = true
	}
}

// disarm, after the program has returned, turns application cursor keys off
// and restores the alternate scroll state, when the view armed them.
func (m *wheelModes) disarm(w io.Writer) {
	if !m.armed {
		return
	}
	_, _ = io.WriteString(w, ansi.ResetModeCursorKeys+restoreAltScroll)
}
