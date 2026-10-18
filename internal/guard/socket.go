package guard

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"os"
	"slices"
	"sync"
	"time"

	"github.com/Sawmonabo/workbench/internal/platform"
)

const (
	// clientBuffer is how many messages wait for a client before it counts as
	// too slow and is dropped.
	clientBuffer = 4
	// stopsQueued is how many stop requests may wait for the next tick before
	// another is answered busy.
	stopsQueued = 8
	// helloRead bounds the wait for a new client's hello.
	helloRead = 5 * time.Second
	// writeWait bounds one write to a client that does not read.
	writeWait = 5 * time.Second
	// requestLimit is the longest line a client may send.
	requestLimit = 64 << 10
)

// server is the watcher's socket. Its goroutines accept clients, read their
// messages and write to them; they never touch the watcher's fields: a stop
// goes to the tick as a stopRequest on stops, and snapshots come from the
// tick through broadcast. mu guards clients. release is what hello answers,
// and closed is closed with the server, so a request no tick will take is
// not waited for.
type server struct {
	listener *net.UnixListener
	mu       sync.Mutex
	clients  map[*client]struct{}
	stops    chan stopRequest
	release  string
	closed   chan struct{}
}

// client is one connection that said hello: the messages waiting for it,
// which its own goroutine writes.
type client struct {
	conn *net.UnixConn
	send chan message
}

// stopRequest is a client's stop for the tick: the IDs, whether to kill at
// once, the peer that sent it, and where the tick answers, buffered so a
// tick never waits on a client that left.
type stopRequest struct {
	ids   []string
	force bool
	peer  procKey
	reply chan []StopResult
}

// socketMode is the socket file's mode: only its owner may connect. The
// folder is already private and each peer's user is checked at accept; the
// mode guards the file itself as well, should the folder ever be opened.
const socketMode = 0o600

// listen serves the socket in the guard folder: the listener the old
// watcher handed over, or a new one in place of a socket left behind, which
// holding the lock makes this watcher's to replace. A new socket is made
// owner-only before anything is served; a handed-over one keeps its file, and
// is made owner-only too in case a release that left it to the umask made it.
func (w *watcher) listen() error {
	path := w.f.socket()
	listener := w.inheritedListener
	w.inheritedListener = nil
	if listener != nil {
		_ = os.Chmod(path, socketMode)
	} else {
		if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		var err error
		listener, err = net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
		if err != nil {
			return err
		}
		if err := os.Chmod(path, socketMode); err != nil {
			_ = listener.Close()
			return err
		}
	}
	w.server = &server{
		listener: listener,
		clients:  map[*client]struct{}{},
		stops:    make(chan stopRequest, stopsQueued),
		release:  w.version,
		closed:   make(chan struct{}),
	}
	go w.server.serve()
	return nil
}

// closeServer stops serving, closing every client. With unlink it removes
// the socket file, which only the lock holder may do: a watcher that lost
// its lock, or handed the listener over, leaves the file to the one serving
// it now. Go removes the file of a listener it made, and not of one it took
// from a descriptor, unless told otherwise, so each close says which.
func (w *watcher) closeServer(unlink bool) {
	s := w.server
	if s == nil {
		return
	}
	w.server = nil
	s.listener.SetUnlinkOnClose(unlink)
	_ = s.listener.Close()
	close(s.closed)
	s.mu.Lock()
	defer s.mu.Unlock()
	for c := range s.clients {
		s.drop(c)
	}
}

// serve accepts clients until the listener closes.
func (s *server) serve() {
	for {
		conn, err := s.listener.AcceptUnix()
		if errors.Is(err, net.ErrClosed) {
			return
		}
		if err != nil {
			// Out of descriptors, say: the client is lost, the socket is not.
			time.Sleep(100 * time.Millisecond)
			continue
		}
		go s.handle(conn)
	}
}

// handle serves one connection: only a process of this user, named by its
// PID and start time, since a PID alone may be reused; it must say hello with
// a version this watcher speaks. Then each stop it sends goes to the tick.
func (s *server) handle(conn *net.UnixConn) {
	peer, err := platform.Peer(conn)
	if err != nil || peer.UID != os.Getuid() {
		_ = conn.Close()
		return
	}
	info, err := platform.ProcessInfo(peer.PID)
	if err != nil {
		_ = conn.Close()
		return
	}
	key := procKey{peer.PID, info.Start}
	lines := bufio.NewScanner(conn)
	lines.Buffer(make([]byte, 0, 4<<10), requestLimit)
	_ = conn.SetDeadline(time.Now().Add(helloRead))
	var hello message
	if !lines.Scan() || json.Unmarshal(lines.Bytes(), &hello) != nil || hello.Type != messageHello {
		_ = conn.Close()
		return
	}
	answer := message{Type: messageHello, Version: ProtocolVersion, Release: s.release}
	if !slices.Contains(hello.Versions, ProtocolVersion) {
		answer = message{
			Type: messageError,
			Error: fmt.Sprintf("no protocol version in common; the watcher speaks %d",
				ProtocolVersion),
		}
	}
	if writeMessage(conn, answer) != nil || answer.Type == messageError {
		_ = conn.Close()
		return
	}
	_ = conn.SetDeadline(time.Time{})
	c := &client{conn: conn, send: make(chan message, clientBuffer)}
	if !s.add(c) {
		_ = conn.Close()
		return
	}
	go c.write(s)
	s.read(c, lines, key)
}

// read takes c's messages until it disconnects. A stop waits for the tick's
// answer, so a client has one stop in flight at a time.
func (s *server) read(c *client, lines *bufio.Scanner, peer procKey) {
	defer s.remove(c)
	for lines.Scan() {
		var m message
		if json.Unmarshal(lines.Bytes(), &m) != nil || m.Type != messageStop {
			s.send(c, message{Type: messageError, Error: "expected a stop"})
			continue
		}
		req := stopRequest{
			ids:   m.IDs,
			force: m.Force,
			peer:  peer,
			reply: make(chan []StopResult, 1),
		}
		select {
		case s.stops <- req:
		default:
			s.send(c, message{Type: messageError, Error: "busy"})
			continue
		}
		select {
		case results := <-req.reply:
			s.send(c, message{Type: messageStopped, Results: results})
		case <-s.closed:
			return
		}
	}
}

// write sends c's messages until its buffer is closed or a write fails.
func (c *client) write(s *server) {
	for m := range c.send {
		_ = c.conn.SetWriteDeadline(time.Now().Add(writeWait))
		if writeMessage(c.conn, m) != nil {
			s.remove(c)
			return
		}
	}
}

// broadcast sends snap to every client without waiting: a client whose
// buffer is full is too slow and is dropped, so no client ever holds up a
// tick.
func (s *server) broadcast(snap Snapshot) {
	m := message{Type: messageSnapshot, Snapshot: &snap}
	s.mu.Lock()
	defer s.mu.Unlock()
	for c := range s.clients {
		s.offer(c, m)
	}
}

// send gives c one message, as broadcast does.
func (s *server) send(c *client, m message) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.offer(c, m)
}

// offer puts m in c's buffer, or drops c when it is full. The caller holds
// mu.
func (s *server) offer(c *client, m message) {
	if _, ok := s.clients[c]; !ok {
		return
	}
	select {
	case c.send <- m:
	default:
		s.drop(c)
	}
}

// add registers c, unless the server has closed.
func (s *server) add(c *client) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	select {
	case <-s.closed:
		return false
	default:
	}
	s.clients[c] = struct{}{}
	return true
}

// remove drops c if it is still registered.
func (s *server) remove(c *client) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.clients[c]; ok {
		s.drop(c)
	}
}

// drop unregisters c, ends its writer and closes its connection, which ends
// its reader. The caller holds mu and has checked c is registered, so its
// buffer is closed once.
func (s *server) drop(c *client) {
	delete(s.clients, c)
	close(c.send)
	_ = c.conn.Close()
}

// writeMessage writes m to conn as one JSON line.
func writeMessage(conn *net.UnixConn, m message) error {
	data, err := json.Marshal(m)
	if err != nil {
		return err
	}
	_, err = conn.Write(append(data, '\n'))
	return err
}

// drainStops answers each stop waiting for this tick (see manualStop).
func (w *watcher) drainStops(s snapshot, protected map[int]uint64) {
	if w.server == nil {
		return
	}
	for {
		select {
		case req := <-w.server.stops:
			req.reply <- w.manualStop(req, s, protected)
		default:
			return
		}
	}
}

// stopAllowed reports whether peer may stop an agent's command: it must run,
// with its start time, and neither it nor any process above it may be
// protected (an agent that left a note or has a tracked command, the
// watcher), so an agent's own command cannot stop another's work. A parent
// missing from s cannot be checked, so it refuses.
func stopAllowed(peer procKey, s snapshot, protected map[int]uint64) bool {
	if !s.alive(peer.pid, peer.start) {
		return false
	}
	for pid, seen := peer.pid, map[int]bool{}; pid > 1 && !seen[pid]; {
		seen[pid] = true
		p, ok := s[pid]
		if !ok {
			return false
		}
		if start, ok := protected[pid]; ok && start == p.Start {
			return false
		}
		pid = p.PPID
	}
	return true
}

// chooseStops picks the tracked commands to stop, in the order asked, each
// once, and answers each ID: unknown when not tracked, already finished when
// its root is gone or its PID names another process, otherwise OK, which the
// stop itself may still change.
func chooseStops(
	ids []string,
	tracked map[string]command,
	s snapshot,
) (stop []command, results []StopResult) {
	seen := map[string]bool{}
	for _, id := range ids {
		if seen[id] {
			continue
		}
		seen[id] = true
		c, ok := tracked[id]
		switch {
		case !ok:
			results = append(results, StopResult{ID: id, Error: "unknown"})
		case !s.alive(c.Root.PID, c.Root.Start):
			results = append(results, StopResult{ID: id, Error: "already finished"})
		default:
			stop = append(stop, c)
			results = append(results, StopResult{ID: id, OK: true})
		}
	}
	return stop, results
}

// listenFailed logs that the socket could not be served.
func (w *watcher) listenFailed(err error) {
	w.f.event(slog.LevelError, EventError,
		"cannot serve the guard socket; guard memory will not connect", "error", err)
}
