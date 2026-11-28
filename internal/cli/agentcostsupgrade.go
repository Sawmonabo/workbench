package cli

import (
	"io"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/Sawmonabo/workbench/internal/agent/costs"
	"github.com/Sawmonabo/workbench/internal/display"
	"github.com/Sawmonabo/workbench/internal/operation"
)

// upgradeLine shows at a terminal an open's upgrade of the costs ledger (see
// costs.UpgradeSteps): at its first step a header and the status line, which
// says that the open waits for another Workbench's upgrade, or upgrades the
// ledger itself, and ends as a mark beside the outcome. Where newProgress
// gives no live line (in --json, a pipe or a log) it shows nothing.
type upgradeLine struct {
	o           *options
	diagnostics io.Writer
	line        *statusLine
	started     time.Time
	title       string // what the status line shows, "" before it starts
	off         bool   // newProgress gave no live line
}

// The titles of the status line.
const (
	upgradingTitle = "Upgrading the ledger"
	waitingTitle   = "Waiting for another Workbench to upgrade the ledger"
)

// step is the costs.UpgradeSteps an open is given.
func (u *upgradeLine) step(step string) {
	if u.line == nil && !u.off && step != "" {
		progress, _ := newProgress(u.o, u.diagnostics, false)
		line, live := progress.(*statusLine)
		if u.off = !live; u.off {
			return
		}
		header := brandHead(brand) + " " + bold.Render("Costs ledger upgrade")
		_, _ = lipgloss.Fprint(line.output, header+"\n")
		u.line, u.started = line, time.Now()
	}
	switch {
	case u.line == nil:
	case step == "":
		u.end(green.Render(glyphs.Check + " Ledger upgraded"))
	case step == costs.UpgradeWaiting:
		u.show(waitingTitle)
	default:
		u.show(upgradingTitle)
		u.line.Step(step)
	}
}

// show starts the status line with title, unless it shows that already.
func (u *upgradeLine) show(title string) {
	if u.title != title {
		u.line.Start(title)
		u.title = title
	}
}

// close ends a line the open left running, as err, the open's error, says. A
// failure ends it as failed, and the result prints the error after it. An
// interrupt ends it as one faint line, the error's own message, which says
// what stopped and what that leaves (see costs.Paths.Open); the result then
// leaves the error out, as it does a stop the update view showed.
func (u *upgradeLine) close(err error) {
	if u.line == nil {
		return
	}
	if operation.ExitCode(err) != operation.ExitInterrupted {
		u.end(red.Render(glyphs.Cross + " Ledger upgrade failed"))
		return
	}
	var stopped operation.Result
	stopped.SetError(err)
	u.end(faint.Render(forTerm("· " + stopped.Errors[0].Message)))
	u.o.stopShown = true
}

func (u *upgradeLine) end(mark string) {
	if u.line == nil {
		return
	}
	u.line.Stop()
	took := faint.Render(" (" + display.Duration(time.Since(u.started)) + ")")
	_, _ = lipgloss.Fprint(u.line.output, mark+took+"\n")
	u.line, u.title = nil, ""
}
