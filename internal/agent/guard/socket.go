package guard

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"slices"
	"sync"
	"time"

	"github.com/Sawmonabo/workbench/internal/operation"
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

// client is one connection that said hello: the lines waiting for it, which
// its own goroutine writes.
type client struct {
	conn *net.UnixConn
	send chan *outgoing
}

// outgoing is one message as the JSON line a client is sent, encoded once, by
// the first client's goroutine that writes it, and then shared by every
// client it was offered to, so a snapshot, with its finished commands, is
// encoded once a tick however many views watch, and never on the tick.
type outgoing struct {
	m    message
	once sync.Once
	data []byte
	err  error
}

// bytes is o's JSON line, newline included, encoded the first time it is
// asked for. Nothing appends to it: it is shared.
func (o *outgoing) bytes() ([]byte, error) {
	o.once.Do(func() { o.data, o.err = encodeLine(o.m) })
	return o.data, o.err
}

// encodeLine is m as one JSON line: json.Marshal's bytes, which escape HTML
// as it does, and a newline, encoded in one buffer, so the newline never
// copies them.
func encodeLine(m message) ([]byte, error) {
	var buffer bytes.Buffer
	if err := json.NewEncoder(&buffer).Encode(m); err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
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
		if err := operation.RemoveIfThere(path); err != nil {
			return err
		}
		var err error
		if listener, err = platform.ListenPrivate(path); err != nil {
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
	w.history.ended("socket")
	return nil
}

// closeServer stops serving, closing every client. With unlink it removes
// the socket file, which only the lock holder may do: a watcher that lost
// its lock, or handed the listener over, leaves the file to the one serving
// it now. It removes the file itself, at the guard folder's current path:
// Go would remove a listener's file by the name it was bound with, which is
// the old path once the folder has moved (see watcher.moveFolder), so it is
// told to remove none.
func (w *watcher) closeServer(unlink bool) {
	s := w.server
	if s == nil {
		return
	}
	w.server = nil
	s.listener.SetUnlinkOnClose(false)
	if unlink {
		_ = os.Remove(w.f.socket())
	}
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
	answer := message{Type: messageHello, Version: protocolVersion, Release: s.release}
	if !slices.Contains(hello.Versions, protocolVersion) {
		answer = message{
			Type: messageError,
			Error: fmt.Sprintf("no protocol version in common; the watcher speaks %d",
				protocolVersion),
		}
	}
	if writeMessage(conn, answer) != nil || answer.Type == messageError {
		_ = conn.Close()
		return
	}
	_ = conn.SetDeadline(time.Time{})
	c := &client{conn: conn, send: make(chan *outgoing, clientBuffer)}
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

// write sends c's lines until its buffer is closed or a write fails.
func (c *client) write(s *server) {
	for o := range c.send {
		data, err := o.bytes()
		if err == nil {
			_ = c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			_, err = c.conn.Write(data)
		}
		if err != nil {
			s.remove(c)
			return
		}
	}
}

// broadcast sends snap to every client without waiting: a client whose
// buffer is full is too slow and is dropped, so no client ever holds up a
// tick. Every client shares the one encoded line (see outgoing).
func (s *server) broadcast(snap Snapshot) {
	o := &outgoing{m: message{Type: messageSnapshot, Snapshot: &snap}}
	s.mu.Lock()
	defer s.mu.Unlock()
	for c := range s.clients {
		s.offer(c, o)
	}
}

// send gives c one message, as broadcast does.
func (s *server) send(c *client, m message) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.offer(c, &outgoing{m: m})
}

// offer puts o in c's buffer, or drops c when it is full. The caller holds
// mu.
func (s *server) offer(c *client, o *outgoing) {
	if _, ok := s.clients[c]; !ok {
		return
	}
	select {
	case c.send <- o:
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

// writeMessage writes m to conn as one JSON line (see encodeLine).
func writeMessage(conn *net.UnixConn, m message) error {
	data, err := encodeLine(m)
	if err != nil {
		return err
	}
	_, err = conn.Write(data)
	return err
}

// drainStops answers each stop waiting for this tick (see manualStop).
func (w *watcher) drainStops(s snapshot, notes []note, protected map[int]uint64) {
	if w.server == nil {
		return
	}
	for {
		select {
		case req := <-w.server.stops:
			req.reply <- w.manualStop(req, s, notes, protected)
		default:
			return
		}
	}
}

// stopAllowed reports whether peer may stop an agent's command: it must run,
// with its start time, and neither it nor any process above it may be
// protected (see watcher.agents: every agent the guard knows, from its notes
// or its agent file, every process a stamp names as its agent, and the
// watcher), so a process still under an agent cannot stop an agent's work.
// The walk goes up to PID 1 included, which may itself be an agent in a
// container started without an init, and ends at its parent, 0. A parent
// missing from s cannot be checked, so it refuses.
func stopAllowed(peer procKey, s snapshot, protected map[int]uint64) bool {
	if !s.alive(peer.pid, peer.start) {
		return false
	}
	for pid, seen := peer.pid, map[int]bool{}; pid >= 1 && !seen[pid]; {
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
// once, and answers each ID: unknown when not tracked, which an agent or any
// other process the guard does not track always is, already finished when it
// no longer runs in s (see live), otherwise OK, which the stop itself may
// still change.
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
		case !live(s, c):
			results = append(results, StopResult{ID: id, Error: "already finished"})
		default:
			stop = append(stop, c)
			results = append(results, StopResult{ID: id, OK: true})
		}
	}
	return stop, results
}

// listenFailed logs that the socket could not be served, a condition that
// lasts until a listen succeeds, so its record stays in the guard log
// meanwhile (see historyWriter.lasts).
func (w *watcher) listenFailed(err error) {
	w.history.lasts("socket", slog.LevelError, EventError,
		"cannot serve the guard socket; workbench agent guard will not connect", "error", err)
}
