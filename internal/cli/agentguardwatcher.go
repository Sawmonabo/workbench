package cli

import (
	"context"
	"errors"
	"os"
	"slices"
	"strings"
	"syscall"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/Sawmonabo/workbench/internal/agent/guard"
)

// snapshotMsg is a snapshot the watcher sent.
type snapshotMsg guard.Snapshot

// disconnectedMsg says the watcher's connection ended.
type disconnectedMsg struct{}

// stoppedMsg is the watcher's answer to a stop, one result per call ID; a
// stop that got no answer gives each ID the error.
type stoppedMsg []guard.StopResult

// connectedMsg is a connection attempt's outcome: a client that said hello,
// or why no watcher answered.
type connectedMsg struct {
	client  *guard.Client
	problem string
}

// The texts the view shows when no watcher answers.
const (
	watcherAbsent = "The guard's watcher is not running; it starts with the next agent " +
		"command."
	watcherOlder = "The watcher runs an older Workbench release that does not serve this " +
		"view; run workbench update."
	watcherOther = "The watcher runs a Workbench release that speaks another protocol " +
		"version; run workbench update."
)

// watcherSilent is what the view says while the watcher's lock is held and
// nothing answers on its socket: doctor's words (see guard.WatcherSilent), as
// a sentence.
var watcherSilent = strings.ToUpper(guard.WatcherSilent[:1]) + guard.WatcherSilent[1:] + "."

// waitSnapshot waits for the watcher's next snapshot, or for the end of the
// connection, as Bubble Tea's realtime example waits on a channel.
func waitSnapshot(ch <-chan guard.Snapshot) tea.Cmd {
	return func() tea.Msg {
		snap, ok := <-ch
		if !ok {
			return disconnectedMsg{}
		}
		return snapshotMsg(snap)
	}
}

// dialWatcher dials the watcher's socket. absent reports a missing or
// refused socket, where nothing serves.
func dialWatcher(ctx context.Context) (client *guard.Client, absent bool, err error) {
	client, err = guard.Dial(ctx)
	return client, errors.Is(err, syscall.ENOENT) || errors.Is(err, syscall.ECONNREFUSED), err
}

// connectWatcher dials the watcher's socket and says hello. A missing or
// refused socket is told by the watcher's lock, as doctor and every entry
// tell whether a watcher runs (see guard.WatcherRuns): with the lock free no
// watcher runs, and with it held one is starting or cannot serve (see
// guard.WatcherSilent). A hello that times out means a watcher of an older
// release took the socket over without serving it; a hello without a
// protocol version in common means another release; any other failed hello
// is shown as it is.
func connectWatcher() tea.Msg {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	client, absent, err := dialWatcher(ctx)
	switch {
	case absent:
		runs, lockErr := guard.WatcherRuns()
		switch {
		case lockErr != nil:
			return connectedMsg{problem: "Cannot tell whether the guard's watcher runs: " +
				lockErr.Error()}
		case runs:
			return connectedMsg{problem: watcherSilent}
		}
		return connectedMsg{problem: watcherAbsent}
	case err != nil:
		return connectedMsg{problem: "Cannot reach the guard's watcher: " + err.Error()}
	}
	if err = client.Hello(); err == nil {
		return connectedMsg{client: client}
	}
	_ = client.Close()
	switch {
	case errors.Is(err, os.ErrDeadlineExceeded):
		return connectedMsg{problem: watcherOlder}
	case errors.Is(err, guard.ErrProtocolVersion):
		return connectedMsg{problem: watcherOther}
	}
	// A watcher answered the dial, so one runs: it may be exiting or handing
	// over, which the next attempt finds out.
	return connectedMsg{problem: "The watcher did not finish the hello (" + err.Error() +
		")."}
}

// take shows a new snapshot: a finished command that left its pending list
// stays as a carried row until the history has it, the rows are made anew
// (see rebuild), tags stay only on commands still running, and the stop menu
// closes once all it names have finished.
func (m *guardView) take(snap guard.Snapshot) {
	m.snaps++
	still := make(map[string]bool, len(snap.Pending))
	for _, f := range snap.Pending {
		still[f.ID] = true
	}
	// A snapshot's slices are never changed once taken, so its commands can
	// be pointed at after the next one replaces it.
	for i := range m.snap.Pending {
		if f := &m.snap.Pending[i]; !still[f.ID] && m.hist.byID[f.ID] == nil {
			m.carried = append(m.carried, carriedRow{Finished: f, at: m.snaps})
		}
	}
	m.snap = snap
	m.rebuild()
	for id := range m.tagged {
		if !slices.Contains(m.running([]string{id}), id) {
			delete(m.tagged, id)
		}
	}
	if m.mode == menuMode && len(m.running(m.targets)) == 0 {
		m.mode = m.menuFrom
		m.say(m.shortIDs(m.targets) + " already finished; nothing was stopped")
	}
}

// answered shows the watcher's answer to a stop: "stopped 4fQ8nX2a",
// "logged 4fQ8nX2a (dry run)" for a dry-run command, which goes on running,
// or the ID and why not ("4fQ8nX2a already finished").
func (m *guardView) answered(results []guard.StopResult) {
	parts := make([]string, 0, len(results))
	for _, r := range results {
		dry := slices.ContainsFunc(m.snap.Commands, func(c guard.CommandView) bool {
			return c.ID == r.ID && c.DryRun
		})
		switch {
		case r.OK && dry:
			parts = append(parts, "logged "+m.label(r.ID)+" (dry run)")
		case r.OK:
			parts = append(parts, "stopped "+m.label(r.ID))
		default:
			parts = append(parts, m.label(r.ID)+" "+r.Error)
		}
	}
	m.say(strings.Join(parts, "; "))
}

// send asks the watcher to stop ids, politely or by force, without waiting:
// its answer comes back as a stoppedMsg, and the rows follow the stop.
func (m *guardView) send(ids []string, force bool) tea.Cmd {
	client := m.client
	if len(ids) == 0 || client == nil {
		m.notice = ""
		return nil
	}
	m.say("asking the watcher to stop " + m.shortIDs(ids))
	clear(m.tagged)
	return func() tea.Msg {
		results, err := client.Stop(ids, force)
		if err != nil {
			results = make([]guard.StopResult, len(ids))
			for i, id := range ids {
				results[i] = guard.StopResult{ID: id, Error: err.Error()}
			}
		}
		return stoppedMsg(results)
	}
}
