package guard

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/unix"

	"github.com/Sawmonabo/workbench/internal/operation"
)

// handoverVariable is set, to the old watcher's PID, in the environment of
// the watcher it starts from the installed command (see handOver): that one
// was passed the lock as descriptor 3, the pipe it says it is ready on as 4
// and the pipe it is told to start on as 5. As with systemd's LISTEN_PID, a
// process that inherited the variable from its parent without being that
// watcher's child ignores it.
const handoverVariable = "WORKBENCH_GUARD_HANDOVER"

const (
	// readyWait bounds how long the old watcher waits for the new one to say
	// it is ready; it keeps watching meanwhile.
	readyWait = 10 * time.Second
	// handoverPoll is the tick while a handover is under way.
	handoverPoll = 100 * time.Millisecond
)

// handoff is the watcher's part in a handover: the release it runs
// (version), the command it was started as (entry) and the file it runs
// (self), read once at its start, the handover under way, the installed file
// a handover to failed (failedTarget) and the last error reading the entry,
// each logged once, and, in a new watcher, what the old one carried.
type handoff struct {
	version      string
	entry        string
	self         os.FileInfo
	pending      *handover
	failedTarget os.FileInfo
	entryErr     string
	inherited    *carried
}

// handover is a handover under way: the new watcher, the installed file it
// was started from, its release once it said it is ready (ready gives "" when
// it did not), and the pipe that tells it to start.
type handover struct {
	process *os.Process
	target  os.FileInfo
	ready   chan string
	release string
	start   *os.File
}

// carried is what the old watcher hands over beside the notes on disk, which
// the new one reads itself: the tracked commands' roots, and the calls whose
// command ended or that are passed over, which live only in memory.
type carried struct {
	Tracked map[string]procRef `json:"tracked"`
	Ended   []string           `json:"ended"`
	Passed  []string           `json:"passed"`
}

type procRef struct {
	PID   int    `json:"pid"`
	Start uint64 `json:"start"`
}

// identify reads, once at the start, the command this watcher was started as
// and the file it runs: after an update the command resolves to the new
// release, and the release this one runs may be deleted while it runs.
// Without both it never hands over.
func (w *watcher) identify() {
	entry, err := operation.Invoked()
	var self string
	if err == nil {
		self, err = os.Executable()
	}
	if err == nil {
		self, err = filepath.EvalSymlinks(self)
	}
	if err == nil {
		w.self, err = os.Stat(self)
	}
	if err != nil {
		w.f.event(slog.LevelError, EventError,
			"cannot tell which workbench this watcher runs; it will not hand over", "error", err)
		return
	}
	w.entry = entry
}

// handOver hands the watch to the release the installed command now runs,
// when that is another file (device and inode) than the one this watcher
// runs. Called after each tick, it starts that release's watcher with this
// one's lock, so the lock is never free and no hook starts a third, and keeps
// watching until the new watcher says it is ready. Then, at a calm moment
// with no stop cooling down, whose memory the new watcher would not have, it
// logs the handover, sends what it carries, which tells the new watcher to
// start, and reports true: the caller exits without another tick, so two
// watchers never act at once. A stop under way finishes first, because it
// runs within a tick. A command that cannot be read, a file that does not
// start, or a watcher that is not ready within readyWait, such as one of a
// release without handover, which finds the lock held and exits, leaves this
// watcher running, logged once; it tries again only once the command resolves
// to another file.
func (w *watcher) handOver() bool {
	h := w.pending
	quiet := w.calm && w.stopped == nil
	if h == nil {
		if quiet && w.entry != "" {
			w.pending = w.startHandover()
		}
		return false
	}
	if h.release == "" {
		select {
		case h.release = <-h.ready:
			if h.release == "" {
				w.cancelHandover(errors.New("its watcher did not get ready to take over"))
				return false
			}
		default:
			return false
		}
	}
	if !quiet {
		return false
	}
	w.f.event(slog.LevelInfo, EventHandover,
		fmt.Sprintf("watcher handing over from %s to %s", w.version, h.release),
		"old_release", w.version, "new_release", h.release, "new_pid", h.process.Pid)
	data, err := json.Marshal(w.carried())
	if err == nil {
		_, err = h.start.Write(append(data, '\n'))
	}
	if err != nil {
		w.cancelHandover(err)
		return false
	}
	_ = h.start.Close()
	w.pending = nil
	return true
}

// startHandover starts the watcher of the file the installed command resolves
// to, when that is not this watcher's own and no handover to it failed, with
// the lock and the two pipes (see handoverVariable), and reads its ready line
// in the background. It gives nil when it started nothing.
func (w *watcher) startHandover() *handover {
	target, err := os.Stat(w.entry)
	if err != nil {
		if err.Error() != w.entryErr {
			w.entryErr = err.Error()
			w.f.event(slog.LevelError, EventError,
				"cannot read the installed workbench; this watcher carries on",
				"entry", w.entry, "error", err)
		}
		return nil
	}
	w.entryErr = ""
	if os.SameFile(target, w.self) ||
		w.failedTarget != nil && os.SameFile(target, w.failedTarget) &&
			target.Mode() == w.failedTarget.Mode() && target.ModTime().Equal(w.failedTarget.ModTime()) {
		return nil
	}
	readyRead, readyWrite, err := os.Pipe()
	if err != nil {
		w.failHandover(target, err)
		return nil
	}
	startRead, startWrite, err := os.Pipe()
	if err != nil {
		_, _ = readyRead.Close(), readyWrite.Close()
		w.failHandover(target, err)
		return nil
	}
	process, err := operation.StartDetached(operation.Detached{
		Entry:  w.entry,
		Args:   []string{"guard", "watch"},
		Output: w.f.output(),
		Env:    []string{handoverVariable + "=" + strconv.Itoa(os.Getpid())},
		Files:  []*os.File{w.lockFile, readyWrite, startRead},
	})
	_, _ = readyWrite.Close(), startRead.Close()
	if err != nil {
		_, _ = readyRead.Close(), startWrite.Close()
		w.failHandover(target, err)
		return nil
	}
	h := &handover{process: process, target: target, ready: make(chan string, 1), start: startWrite}
	_ = readyRead.SetReadDeadline(time.Now().Add(readyWait))
	go func() {
		defer func() { _ = readyRead.Close() }()
		line, err := bufio.NewReader(readyRead).ReadString('\n')
		release, ok := strings.CutPrefix(strings.TrimSuffix(line, "\n"), "ready ")
		if err != nil || !ok {
			release = ""
		}
		h.ready <- release
	}()
	return h
}

// cancelHandover gives up the handover under way: it closes the pipe, which
// tells the new watcher to exit, and kills it in case it hangs, so it never
// keeps the lock. It is this watcher's child and not yet waited for, so its
// PID cannot have passed to another process.
func (w *watcher) cancelHandover(cause error) {
	h := w.dropHandover()
	w.failHandover(h.target, cause)
}

// dropHandover ends the handover under way, if any, and gives it.
func (w *watcher) dropHandover() *handover {
	h := w.pending
	if h == nil {
		return nil
	}
	w.pending = nil
	_ = h.start.Close()
	_ = h.process.Kill()
	go func() { _, _ = h.process.Wait() }()
	return h
}

func (w *watcher) failHandover(target os.FileInfo, cause error) {
	w.failedTarget = target
	w.f.event(slog.LevelError, EventError,
		"cannot hand over to the installed workbench; this watcher carries on",
		"entry", w.entry, "error", cause)
}

// carried is what this watcher hands over.
func (w *watcher) carried() carried {
	c := carried{Tracked: map[string]procRef{}}
	for id, cmd := range w.tracked {
		c.Tracked[id] = procRef{cmd.Root.PID, cmd.Root.Start}
	}
	for id := range w.ended {
		c.Ended = append(c.Ended, id)
	}
	for id := range w.passed {
		c.Passed = append(c.Passed, id)
	}
	return c
}

// takeLock takes the lock: passed on by the watcher that started this one
// (see inherit), or anew.
func (w *watcher) takeLock() bool {
	parent := os.Getenv(handoverVariable)
	_ = os.Unsetenv(handoverVariable)
	if parent == "" || parent != strconv.Itoa(os.Getppid()) {
		return w.lock()
	}
	return w.inherit()
}

// inherit takes over from the watcher that started this one (see handOver).
// It takes on the lock passed as descriptor 3, which must be the guard's lock
// file with no other process's lock on it, says it is ready, with its
// release, on descriptor 4, and waits on descriptor 5 for what the old
// watcher carried, the word to start. It reports false, having acted on
// nothing, when the lock is not the guard's or the old watcher closes the
// pipe without a word.
func (w *watcher) inherit() bool {
	for fd := 3; fd <= 5; fd++ {
		unix.CloseOnExec(fd)
	}
	lock, ready, start := os.NewFile(
		3,
		"watch.lock",
	), os.NewFile(
		4,
		"ready",
	), os.NewFile(
		5,
		"start",
	)
	defer func() { _, _ = ready.Close(), start.Close() }()
	if held, err := operation.InheritLock(lock, w.f.lock()); err != nil || held {
		_ = lock.Close()
		return false
	}
	_, err := fmt.Fprintf(ready, "ready %s\n", w.version)
	_ = ready.Close()
	var line []byte
	if err == nil {
		line, err = bufio.NewReader(start).ReadBytes('\n')
	}
	info, statErr := os.Stat(w.f.lock())
	if err = errors.Join(err, statErr); err != nil {
		_ = lock.Close()
		return false
	}
	// A word that does not parse still says to start; the notes on disk are
	// read either way.
	var c carried
	_ = json.Unmarshal(line, &c)
	w.inherited = &c
	w.lockFile, w.release, w.lockInfo = lock, func() { _ = lock.Close() }, info
	return true
}

// adopt takes on what the old watcher carried for each note still on disk:
// its tracked command when the root still runs, with its start time, as the
// note's agent's child, with whether it is passed over, and whether its
// command ended.
// Other notes are matched as a new watcher matches them (see match).
func (w *watcher) adopt(s snapshot, notes []note) {
	c := w.inherited
	for _, n := range notes {
		id := n.ToolUseID
		if slices.Contains(c.Ended, id) {
			w.ended[id] = true
		}
		root, ok := c.Tracked[id]
		if p := s[root.PID]; ok && s.alive(root.PID, root.Start) && p.PPID == n.AgentPID {
			cmd := command{Note: n, Root: p}
			w.tracked[id] = cmd
			w.passed[id] = slices.Contains(c.Passed, id)
			seen := w.seen[id]
			seen.matched = true
			w.seen[id] = seen
			w.f.event(slog.LevelInfo, EventTracking, "tracking", callAttrs(cmd)...)
		}
	}
}
