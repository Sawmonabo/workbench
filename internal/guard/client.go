package guard

import (
	"bufio"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"path/filepath"
	"sync"
	"time"

	"github.com/Sawmonabo/workbench/internal/operation"
)

const (
	// helloWait bounds the hello: a watcher of an older release that took over
	// the socket without serving it never answers.
	helloWait = 3 * time.Second
	// stopWait bounds a stop: the watcher takes it at its next tick, or after
	// a stop of its own, which may poll for up to giveUp.
	stopWait = 12 * time.Second
	// socketPathLimit is the longest socket path accepted: macOS holds 104
	// bytes, with the closing NUL, in a socket address, Linux 108.
	socketPathLimit = 100
	// lineLimit is the longest message line read.
	lineLimit = 16 << 20
)

// errClosed is what Stop gives once the watcher has gone.
var errClosed = errors.New("the watcher closed the connection")

// ErrProtocolVersion is what Hello wraps when the watcher speaks no protocol
// version this client knows: it runs another release.
var ErrProtocolVersion = errors.New("the watcher speaks no protocol version this client knows")

// socketFile is the watcher's socket in the guard folder root.
func socketFile(root string) string { return filepath.Join(root, "guard.sock") }

// socketPath is the watcher's socket path. It reads nothing and creates
// nothing. A path too long for a socket address is an error naming the state
// folder, which holds it.
func socketPath() (string, error) {
	c, err := operation.Resolve(operation.Options{ReadOnly: true})
	if err != nil {
		return "", err
	}
	path := socketFile(filepath.Join(c.Paths.State, "guard"))
	if len(path) > socketPathLimit {
		return "", fmt.Errorf(
			"the guard socket path %s is over %d bytes; the state folder %s is too deep for a socket",
			path,
			socketPathLimit,
			c.Paths.State,
		)
	}
	return path, nil
}

// Client is a connection to the watcher's socket. Call Hello first; then
// Snapshots gives what the watcher sees and Stop asks it to stop commands.
type Client struct {
	conn    *net.UnixConn
	lines   *bufio.Scanner
	snaps   chan Snapshot
	replies chan message
	// stopping lets one Stop wait for its answer at a time.
	stopping sync.Mutex
}

// Dial connects to the watcher's socket. It fails when no watcher listens.
func Dial(ctx context.Context) (*Client, error) {
	path, err := socketPath()
	if err != nil {
		return nil, err
	}
	var d net.Dialer
	conn, err := d.DialContext(ctx, "unix", path)
	if err != nil {
		return nil, err
	}
	unixConn, ok := conn.(*net.UnixConn)
	if !ok {
		_ = conn.Close()
		return nil, fmt.Errorf("%s is not a Unix socket", path)
	}
	lines := bufio.NewScanner(unixConn)
	lines.Buffer(make([]byte, 0, 64<<10), lineLimit)
	return &Client{
		conn:    unixConn,
		lines:   lines,
		snaps:   make(chan Snapshot, 1),
		replies: make(chan message, 1),
	}, nil
}

// Hello says which protocol versions this client speaks and gives the
// release the watcher runs. It fails after three seconds without an answer:
// a watcher of an older release that took the socket over never answers.
// Once it succeeds the client starts reading snapshots.
func (c *Client) Hello() (release string, err error) {
	if err := c.conn.SetDeadline(time.Now().Add(helloWait)); err != nil {
		return "", err
	}
	if err := c.write(message{Type: messageHello, Versions: []int{protocolVersion}}); err != nil {
		return "", err
	}
	reply, err := c.read()
	if err != nil {
		return "", err
	}
	switch {
	case reply.Type == messageError:
		// The only error a watcher answers a hello with is no common version.
		return "", fmt.Errorf("%w: %s", ErrProtocolVersion, reply.Error)
	case reply.Type != messageHello || reply.Version != protocolVersion:
		return "", fmt.Errorf("%w: the watcher answered %q with version %d",
			ErrProtocolVersion, reply.Type, reply.Version)
	}
	if err := c.conn.SetDeadline(time.Time{}); err != nil {
		return "", err
	}
	go c.receive()
	return reply.Release, nil
}

// Snapshots gives the watcher's snapshots, the latest only when the reader is
// behind. It is closed when the connection ends.
func (c *Client) Snapshots() <-chan Snapshot { return c.snaps }

// Stop asks the watcher to stop the commands with these IDs, politely or,
// with force, with SIGKILL at once, and gives its answer for each. It waits
// up to twelve seconds.
func (c *Client) Stop(ids []string, force bool) ([]StopResult, error) {
	c.stopping.Lock()
	defer c.stopping.Unlock()
	// An answer that came after an earlier Stop gave up is not this one's.
	select {
	case <-c.replies:
	default:
	}
	if err := c.conn.SetWriteDeadline(time.Now().Add(stopWait)); err != nil {
		return nil, err
	}
	if err := c.write(message{Type: messageStop, IDs: ids, Force: force}); err != nil {
		return nil, err
	}
	timer := time.NewTimer(stopWait)
	defer timer.Stop()
	select {
	case reply, ok := <-c.replies:
		switch {
		case !ok:
			return nil, errClosed
		case reply.Type == messageError:
			return nil, errors.New(reply.Error)
		}
		return reply.Results, nil
	case <-timer.C:
		return nil, errors.New("the watcher did not answer the stop")
	}
}

// Close ends the connection.
func (c *Client) Close() error { return c.conn.Close() }

// write sends m to the watcher as one JSON line.
func (c *Client) write(m message) error {
	data, err := json.Marshal(m)
	if err != nil {
		return err
	}
	_, err = c.conn.Write(append(data, '\n'))
	return err
}

// read takes the watcher's next line; a line that does not decode gives
// its JSON error, a closed connection errClosed or the read's error.
func (c *Client) read() (message, error) {
	if !c.lines.Scan() {
		return message{}, cmp.Or(c.lines.Err(), errClosed)
	}
	var m message
	err := json.Unmarshal(c.lines.Bytes(), &m)
	return m, err
}

// receive reads the watcher's messages until the connection ends, then
// closes Snapshots. A snapshot replaces one the reader has not taken yet.
func (c *Client) receive() {
	defer close(c.snaps)
	defer close(c.replies)
	for {
		m, err := c.read()
		if err != nil && !isSyntax(err) {
			return
		}
		switch {
		case m.Type == messageSnapshot && m.Snapshot != nil:
			select {
			case c.snaps <- *m.Snapshot:
			default:
				select {
				case <-c.snaps:
				default:
				}
				c.snaps <- *m.Snapshot
			}
		case m.Type == messageStopped || m.Type == messageError:
			select {
			case c.replies <- m:
			default:
			}
		}
	}
}

// isSyntax reports whether err is a line that does not decode, which is
// skipped.
func isSyntax(err error) bool {
	var syntax *json.SyntaxError
	var typed *json.UnmarshalTypeError
	return errors.As(err, &syntax) || errors.As(err, &typed)
}
