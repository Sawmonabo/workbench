package operation

import (
	"os"
	"os/signal"
	"syscall"

	"github.com/Sawmonabo/workbench/internal/platform"
	"github.com/charmbracelet/x/term"
	"golang.org/x/sys/unix"
)

// StandardTerminal duplicates the first standard stream that is a terminal
// open for reading and writing, for native programs that prompt, or returns
// nil. It never opens /dev/tty: on macOS, kqueue cannot poll /dev/tty, and Go
// programs select their input reader by file name, so chezmoi given /dev/tty
// as its stdin stops reading after the first question of its questionnaire.
// Without such a stream, native runs are unattended; sudo still prompts
// because it opens /dev/tty itself.
func StandardTerminal() *os.File {
	for _, standard := range []*os.File{os.Stdin, os.Stdout, os.Stderr} {
		fd := standard.Fd()
		flags, err := unix.FcntlInt(fd, unix.F_GETFL, 0)
		if err != nil || flags&unix.O_ACCMODE != unix.O_RDWR || !platform.IsTerminal(fd) {
			continue
		}
		if duplicate, err := unix.FcntlInt(fd, unix.F_DUPFD_CLOEXEC, 0); err == nil {
			return os.NewFile(uintptr(duplicate), "terminal")
		}
	}
	return nil
}

// OpenTerminal opens the controlling terminal, /dev/tty, to read from and
// write to a person at it, whatever stdin and stdout are. It fails when the
// process has none.
func OpenTerminal() (*os.File, error) { return os.OpenFile("/dev/tty", os.O_RDWR, 0) }

// lendTerminal makes the child's process group the terminal's foreground, so
// sudo and installers can prompt, and returns the function that takes it back.
// That call comes from a background process group, which the terminal answers
// with SIGTTOU (termios(4)); by default that stops Workbench under a
// job-control shell, so the signal is ignored meanwhile.
func lendTerminal(attributes *syscall.SysProcAttr, terminal *os.File) (func(), error) {
	fd := int(terminal.Fd())
	foreground, err := unix.IoctlGetInt(fd, unix.TIOCGPGRP)
	if err != nil {
		return nil, Fail(ExitBlocked, "terminal", "Cannot inspect native setup terminal")
	}
	lent, err := term.GetState(uintptr(fd))
	if err != nil {
		return nil, Fail(ExitBlocked, "terminal", "Cannot inspect native setup terminal")
	}
	attributes.Foreground = true
	attributes.Ctty = fd
	return func() {
		signal.Ignore(syscall.SIGTTOU)
		defer signal.Reset(syscall.SIGTTOU)
		_ = unix.IoctlSetPointerInt(fd, unix.TIOCSPGRP, foreground)
		// The terminal comes back as it was lent: a child that ended mid-prompt,
		// such as the password helper on ctrl+c, may have left echo off.
		_ = term.Restore(uintptr(fd), lent)
	}, nil
}
