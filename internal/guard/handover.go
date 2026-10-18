package guard

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/unix"

	"github.com/Sawmonabo/workbench/internal/operation"
	"github.com/Sawmonabo/workbench/internal/platform"
)

// handoverVariable is set, to the old watcher's PID, in the environment of
// the watcher it starts from the installed command (see handOver): that one
// was passed the lock as descriptor 3, the pipe it says it is ready on as 4
// and the pipe it is told to start on as 5. As with systemd's LISTEN_PID, a
// process that inherited the variable from its parent without being that
// watcher's child ignores it.
const handoverVariable = "WORKBENCH_GUARD_HANDOVER"

// listenerVariable is set, to 6, beside handoverVariable when the old
// watcher also passes its listening socket, as descriptor 6, so clients
// connected meanwhile wait in its queue and none is refused. A release that
// does not know it leaves the descriptor unused, and clients then get no
// hello (see [Client.Hello]).
const (
	listenerVariable = "WORKBENCH_GUARD_LISTENER"
	listenerFD       = 6
)

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
// each logged once, and, in a new watcher, what the old one carried and the socket it passed.
type handoff struct {
	version      string
	entry        string
	self         os.FileInfo
	pending      *handover
	failedTarget os.FileInfo
	entryErr     string
	inherited    *carried
	// inheritedListener is the socket the old watcher passed, until listen
	// serves it.
	inheritedListener *net.UnixListener
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
// the new one reads itself: the tracked commands' roots, notes and memory,
// the calls whose command ended or that are passed over, the largest peak,
// the commands that finished within finishedShown and who stopped each
// tracked command that was signalled, which live only in memory. The notes
// let the new watcher end a command whose note the post hook removed while
// the watch passed.
type carried struct {
	Tracked     map[string]procRef `json:"tracked"`
	Notes       map[string]note    `json:"notes"`
	Ended       []string           `json:"ended"`
	Passed      []string           `json:"passed"`
	Usage       map[string]usage   `json:"usage"`
	LargestPeak uint64             `json:"largest_peak"`
	Finished    []CommandView      `json:"finished"`
	StoppedBy   map[string]string  `json:"stopped_by"`
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
	// A person's stop still owed its SIGKILL is not carried, so it holds the
	// handover as the guard's own does.
	quiet := w.calm && w.stopped == nil && len(w.manual) == 0
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
	// The new watcher serves the socket now, so its file stays.
	w.closeServer(false)
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
	env := []string{handoverVariable + "=" + strconv.Itoa(os.Getpid())}
	files := []*os.File{w.lockFile, readyWrite, startRead}
	listener := w.listenerFile()
	if listener != nil {
		env = append(env, listenerVariable+"="+strconv.Itoa(listenerFD))
		files = append(files, listener)
	}
	process, err := operation.StartDetached(operation.Detached{
		Entry:  w.entry,
		Args:   []string{"guard", "watch"},
		Output: w.f.output(),
		Env:    env,
		Files:  files,
	})
	_, _ = readyWrite.Close(), startRead.Close()
	if listener != nil {
		_ = listener.Close()
	}
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

// listenerFile is a copy of the socket's listening descriptor to pass on, nil
// without a socket or when it cannot be copied: the new watcher then listens
// anew. The copy is made with F_DUPFD_CLOEXEC and wrapped by [os.NewFile],
// not taken from [net.UnixListener.File]: a starting process takes each
// file's descriptor, and that of a File from net is put in blocking mode
// ([os.File.Fd]), a mode the socket's descriptors share, so this watcher's
// accept could block a thread and its close at the handover wait for a
// client that may never come. One from NewFile keeps its mode. The copy is
// close-on-exec, like every descriptor Go opens, so no other child gets it;
// the child it is passed to gets it as descriptor 6 by dup2, which clears
// that flag.
func (w *watcher) listenerFile() *os.File {
	if w.server == nil {
		return nil
	}
	raw, err := w.server.listener.SyscallConn()
	if err != nil {
		return nil
	}
	duplicate, dupErr := -1, error(nil)
	err = raw.Control(func(fd uintptr) {
		duplicate, dupErr = unix.FcntlInt(fd, unix.F_DUPFD_CLOEXEC, 0)
	})
	if err != nil || dupErr != nil || duplicate < 0 {
		return nil
	}
	return os.NewFile(uintptr(duplicate), "guard.sock")
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
	c := carried{
		Tracked:     map[string]procRef{},
		Notes:       map[string]note{},
		Usage:       map[string]usage{},
		LargestPeak: w.largestPeak,
		Finished:    w.finished,
		StoppedBy:   map[string]string{},
	}
	for id, cmd := range w.tracked {
		c.Tracked[id] = procRef{cmd.Root.PID, cmd.Root.Start}
		c.Notes[id] = cmd.Note
		if u := w.usage[id]; u != nil {
			c.Usage[id] = *u
		}
		if by := w.stoppedBy[id]; by != "" {
			c.StoppedBy[id] = by
		}
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
	listener := os.Getenv(listenerVariable)
	_, _ = os.Unsetenv(handoverVariable), os.Unsetenv(listenerVariable)
	if parent == "" || parent != strconv.Itoa(os.Getppid()) {
		return w.lock()
	}
	return w.inherit(listener == strconv.Itoa(listenerFD))
}

// inherit takes over from the watcher that started this one (see handOver).
// It takes on the lock passed as descriptor 3, which must be the guard's lock
// file with no other process's lock on it, says it is ready, with its
// release, on descriptor 4, and waits on descriptor 5 for what the old
// watcher carried, the word to start. With listener, it takes on the
// listening socket passed as descriptor 6 (see listenerVariable). It reports
// false, having acted on nothing, when the lock is not the guard's or the old
// watcher closes the pipe without a word.
func (w *watcher) inherit(listener bool) bool {
	last := 5
	if listener {
		last = listenerFD
	}
	for fd := 3; fd <= last; fd++ {
		unix.CloseOnExec(fd)
	}
	lock := os.NewFile(3, "watch.lock")
	ready := os.NewFile(4, "ready")
	start := os.NewFile(5, "start")
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
	if listener {
		w.inheritedListener = takeListener()
	}
	return true
}

// takeListener takes the listening socket passed as descriptor 6. Go's
// copy is its own descriptor, so the passed one is closed. It gives nil when
// the descriptor is not a Unix listener; the watcher then listens anew.
func takeListener() *net.UnixListener {
	f := os.NewFile(listenerFD, "guard.sock")
	defer func() { _ = f.Close() }()
	ln, err := net.FileListener(f)
	if err != nil {
		return nil
	}
	unixListener, ok := ln.(*net.UnixListener)
	if !ok {
		_ = ln.Close()
		return nil
	}
	return unixListener
}

// adopt takes on what the old watcher carried: the largest peak, the
// finished commands, which buildView drops finishedShown after they ended as
// it does its own, who stopped each tracked command, and each tracked command
// with its note, the one on disk or else the one carried. A command whose
// root still runs, with its start time, as the note's agent's child is
// tracked again with its memory, whether it is passed over, and logged. One
// whose root is gone is tracked without a log, so forget ends it as it ends
// any command, once its root has been absent for two rounds: it logs the end
// once and keeps it among the finished commands with who stopped it, and the
// note on disk is never matched again. A tracked command carried without
// memory starts it anew. Other notes are matched as a new watcher matches
// them (see match).
func (w *watcher) adopt(s snapshot, notes []note) {
	c := w.inherited
	w.largestPeak = max(w.largestPeak, c.LargestPeak)
	w.finished = append(w.finished, c.Finished...)
	maps.Copy(w.stoppedBy, c.StoppedBy)
	onDisk := map[string]note{}
	for _, n := range notes {
		onDisk[n.ToolUseID] = n
		if slices.Contains(c.Ended, n.ToolUseID) {
			w.ended[n.ToolUseID] = true
		}
	}
	for id, root := range c.Tracked {
		n, noted := onDisk[id]
		if !noted {
			n, noted = c.Notes[id]
		}
		p, running := s[root.PID]
		running = running && s.alive(root.PID, root.Start)
		if !noted || (running && p.PPID != n.AgentPID) {
			continue
		}
		if !running {
			p = proc{Process: platform.Process{PID: root.PID, PPID: n.AgentPID, Start: root.Start}}
		}
		cmd := command{Note: n, Root: p}
		w.tracked[id] = cmd
		u, kept := c.Usage[id]
		if !kept {
			u = usage{Since: time.Now()}
		}
		w.usage[id] = &u
		w.passed[id] = slices.Contains(c.Passed, id)
		if seen, ok := w.seen[id]; ok {
			seen.matched = true
			w.seen[id] = seen
		}
		if running {
			w.f.event(slog.LevelInfo, EventTracking, "tracking", callAttrs(cmd)...)
		}
	}
	// A mark is shown only with its tracked command.
	for id := range w.stoppedBy {
		if _, ok := w.tracked[id]; !ok {
			delete(w.stoppedBy, id)
		}
	}
}
