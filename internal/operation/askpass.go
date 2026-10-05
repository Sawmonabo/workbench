package operation

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	// askpassDeadline bounds one exchange with the helper's socket; the person's
	// typing happens between exchanges, never inside one.
	askpassDeadline = 10 * time.Second
	// askpassLine bounds a request, far above any password.
	askpassLine = 4096
	// socketPathLimit is macOS's 104-byte Unix socket path, less its final NUL.
	socketPathLimit = 103
	// socketPathMax is the longest path the folder and socket names can have.
	socketPathMax = len("/wb-askpass-4294967295/s")
)

// askpassService is, for one apply, a private folder holding the SUDO_ASKPASS
// helper and the Unix socket it asks Workbench on. The helper is a two-line
// script that runs this executable's hidden askpass command; it holds no
// password. The socket answers only while the apply runs, and only processes the
// apply started: the peer's ancestry is checked (see peerDescendsFrom), so
// Workbench itself and any other process under the account get nothing.
type askpassService struct {
	dir, helper string
	listener    *net.UnixListener
	m           *Mutation
	stopped     chan struct{}
	handlers    sync.WaitGroup

	mu       sync.Mutex
	password string
	// handedOut says a helper got the password for sudo, which then keeps a
	// sudo approval for this terminal; the apply drops it when it ends.
	handedOut bool
}

// startAskpass makes the folder, helper and socket, and starts answering.
func startAskpass(m *Mutation) (*askpassService, error) {
	service, err := newAskpass(m)
	if err != nil {
		return nil, Fail(
			ExitBlocked,
			"privilege",
			"Workbench could not set up its Mac password helper, so nothing was changed ("+err.Error()+")",
		)
	}
	go service.serve()
	return service, nil
}

func newAskpass(m *Mutation) (service *askpassService, err error) {
	executable, err := os.Executable()
	if err != nil {
		return nil, err
	}
	base := os.TempDir()
	if len(base)+socketPathMax > socketPathLimit {
		base = "/tmp"
	}
	dir, err := os.MkdirTemp(base, "wb-askpass-")
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			_ = os.RemoveAll(dir)
		}
	}()
	socket := filepath.Join(dir, "s")
	helper := filepath.Join(dir, "askpass")
	script := "#!/bin/sh\nexec " + shellQuote(
		executable,
	) + " askpass -- " + shellQuote(
		socket,
	) + " \"$@\"\n"
	if err = os.WriteFile(helper, []byte(script), 0o700); err != nil {
		return nil, err
	}
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: socket, Net: "unix"})
	if err != nil {
		return nil, err
	}
	if err = os.Chmod(socket, 0o600); err != nil {
		_ = listener.Close()
		return nil, err
	}
	return &askpassService{
		dir:      dir,
		helper:   helper,
		listener: listener,
		m:        m,
		stopped:  make(chan struct{}),
	}, nil
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func (s *askpassService) serve() {
	defer close(s.stopped)
	for {
		conn, err := s.listener.AcceptUnix()
		if err != nil {
			return
		}
		s.handlers.Go(func() { s.answer(conn) })
	}
}

// answer serves one request, "get" or "set PASSWORD", from a process the apply
// started; anyone else is dropped without a word.
func (s *askpassService) answer(conn *net.UnixConn) {
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(askpassDeadline))
	if !peerDescendsFrom(conn, os.Getpid()) {
		return
	}
	request, err := bufio.NewReader(io.LimitReader(conn, askpassLine)).ReadString('\n')
	if err != nil {
		return
	}
	switch password, isSet := strings.CutPrefix(request, "set "); {
	case isSet:
		s.remember(strings.TrimSuffix(password, "\n"))
		s.mu.Lock()
		s.handedOut = true
		s.mu.Unlock()
	case request == "get\n":
		s.mu.Lock()
		reply := "none\n"
		if s.password != "" {
			reply = "password " + s.password + "\n"
			s.handedOut = true
		}
		s.mu.Unlock()
		_, _ = io.WriteString(conn, reply)
	}
}

// remember keeps a password sudo accepted, for the helper's later requests, and
// adds it to what the apply redacts.
func (s *askpassService) remember(password string) {
	if password == "" {
		return
	}
	s.mu.Lock()
	s.password = password
	s.mu.Unlock()
	s.m.remember(password)
}

// close stops answering, lets go of the password and removes the folder, the
// helper and the socket. The apply calls it however it ends. It reports whether
// a helper ever got the password.
func (s *askpassService) close() (handedOut bool) {
	_ = s.listener.Close()
	<-s.stopped
	s.handlers.Wait()
	s.mu.Lock()
	s.password = ""
	handedOut = s.handedOut
	s.mu.Unlock()
	_ = os.RemoveAll(s.dir)
	return handedOut
}

// AskPass is the SUDO_ASKPASS helper's work, run by the hidden askpass command
// during an apply's native scripts: it prints on out, the password sudo reads,
// the one Workbench holds. When Workbench holds none yet, it asks on the
// terminal, as a process in the terminal's foreground group can, with sudo
// checking the answer, and hands only a correct one to Workbench to remember.
// Prompts go to the terminal, never to out. Three wrong answers fail this one
// sudo, as sudo's own prompt would.
func AskPass(ctx context.Context, socket string, out io.Writer) error {
	// After a ctrl+c, Workbench takes the terminal back while this process may
	// still be putting echo back; from the background that would stop it
	// (SIGTTOU) instead.
	signal.Ignore(syscall.SIGTTOU)
	password, held, err := askpassGet(socket)
	if err != nil {
		return Fail(
			ExitBlocked,
			"privilege",
			"Workbench is not answering Mac password requests; it does so only while its apply runs",
		)
	}
	if !held {
		terminal, openErr := os.OpenFile("/dev/tty", os.O_RDWR, 0)
		if openErr != nil {
			return Fail(
				ExitBlocked,
				"privilege",
				"There is no terminal to ask for the Mac password",
			)
		}
		defer func() { _ = terminal.Close() }()
		password, err = askVerified(
			ctx,
			terminal,
			"A step of this apply needs your Mac password; Workbench asks for it once.",
		)
		switch {
		case err == nil:
		case ctx.Err() != nil:
			return Fail(
				ExitInterrupted,
				"interrupted",
				"Interrupted while asking for the Mac password",
			)
		default:
			return Fail(ExitBlocked, "privilege", "The Mac password was not accepted")
		}
		// A failure to remember it only means the next request asks again. The
		// server replies to a set with nothing; waiting for its close lets it finish.
		_, _ = askpassTalk(socket, "set "+password+"\n")
	}
	_, err = fmt.Fprintln(out, password)
	return err
}

// askpassTalk sends one request on socket and returns Workbench's one-line
// reply. A close with no reply is an error: it is how Workbench turns away a
// process the apply did not start.
func askpassTalk(socket, request string) (string, error) {
	conn, err := net.DialTimeout("unix", socket, askpassDeadline)
	if err != nil {
		return "", err
	}
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(askpassDeadline))
	if _, err = io.WriteString(conn, request); err != nil {
		return "", err
	}
	return bufio.NewReader(io.LimitReader(conn, askpassLine)).ReadString('\n')
}

// askpassGet asks Workbench for the password it holds, held false when it has none.
func askpassGet(socket string) (password string, held bool, err error) {
	reply, err := askpassTalk(socket, "get\n")
	if err != nil {
		return "", false, err
	}
	if password, held = strings.CutPrefix(reply, "password "); held {
		return strings.TrimSuffix(password, "\n"), true, nil
	}
	if reply != "none\n" {
		return "", false, errors.New("unexpected reply")
	}
	return "", false, nil
}
