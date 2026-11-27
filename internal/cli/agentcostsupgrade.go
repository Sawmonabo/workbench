package cli

import (
	"io"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/Sawmonabo/workbench/internal/display"
)

// upgradeLine shows at a terminal an open's upgrade of the costs ledger (see
// costs.UpgradeSteps): at its first step a header and the status line, which
// ends as a mark beside the outcome. Where newProgress gives no live line (in
// --json, a pipe or a log) it shows nothing.
type upgradeLine struct {
	o           *options
	diagnostics io.Writer
	line        *statusLine
	started     time.Time
	off         bool // newProgress gave no live line
}

// step is the costs.UpgradeSteps an open is given.
func (u *upgradeLine) step(step string) {
	if u.line == nil && !u.off && step != "" {
		progress, _ := newProgress(u.o, u.diagnostics, false)
		line, live := progress.(*statusLine)
		if u.off = !live; u.off {
			return
		}
		header := brand.Bold(true).Render("[WorkBench]") + " " + bold.Render("Costs ledger upgrade")
		_, _ = lipgloss.Fprint(line.output, header+"\n")
		u.line, u.started = line, time.Now()
		line.Start("Upgrading the ledger")
	}
	switch {
	case u.line == nil:
	case step == "":
		u.end(green.Render(glyphs.Check + " Ledger upgraded"))
	default:
		u.line.Step(step)
	}
}

// close ends a line the open left running: its upgrade failed.
func (u *upgradeLine) close() { u.end(red.Render(glyphs.Cross + " Ledger upgrade failed")) }

func (u *upgradeLine) end(mark string) {
	if u.line == nil {
		return
	}
	u.line.Stop()
	took := faint.Render(" (" + display.Duration(time.Since(u.started)) + ")")
	_, _ = lipgloss.Fprint(u.line.output, mark+took+"\n")
	u.line = nil
}
