package operation

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/charmbracelet/x/term"
)

// sudoPath is the one sudo Workbench runs. It is always this absolute path, never
// a PATH lookup, so a sudo planted earlier on PATH cannot capture the password.
// It is a variable only so the safeguard test can stand in for it.
var sudoPath = "/usr/bin/sudo"

// sudoEnvironment is the whole environment of a sudo run; in particular it has
// no SUDO_ASKPASS, which would divert the prompt to another program.
var sudoEnvironment = []string{"PATH=/usr/bin:/bin:/usr/sbin:/sbin"}

// readSecret reads what a person types at terminal, without echo. It is a
// variable only so the safeguard test can type without a terminal.
var readSecret = readPassword

const (
	adminPrompt = "Mac password: "
	// adminTries is how many wrong passwords end the asking, as sudo's own.
	adminTries = 3
)

// Admin says what an apply needs the administrator password for.
type Admin struct {
	// Terminal is where a person can type the password, nil when nobody can.
	Terminal *os.File
	// Why finishes the line shown before the up-front prompt: "install
	// Homebrew". Empty when the plan needs no password before its scripts run.
	Why string
	// Refusal is, when nobody can type the password and no ticket exists, the
	// message of the blocked error that ends the apply before any change. Empty
	// carries on without a ticket.
	Refusal string
}

// WithAdmin lets an apply at a terminal on macOS ask for the Mac password once.
// run is handed the path of the SUDO_ASKPASS helper for its scripts' Homebrew
// commands, or "" where there is none, and the apply's scripts give it to
// nothing else.
//
// At a terminal Workbench reads the password itself, without echo, and checks it
// with sudo -k -S -v, which leaves no sudo approval behind. It does so up front,
// after approval and before run starts, when Why says the plan needs it and no
// valid ticket exists (a recent sudo, or passwordless sudo, asks nothing);
// otherwise the helper asks the first time Homebrew's sudo does (see [AskPass]).
// An account that is not an administrator, a refusal or three wrong passwords end
// the apply as blocked, and a ctrl+c as interrupted, before run starts.
//
// The password lives only in this process's memory until WithAdmin returns,
// however it ends. It is added to the secrets the mutation redacts, never put in
// an argument, the environment or a file, and the helper gets it from Workbench
// over a socket that answers only the apply's own processes (see askpassService).
// Homebrew drops earlier sudo approvals each time brew runs, on purpose, so what
// Workbench holds is the password, not an approval; the approved plan stands in
// for Homebrew's per-command approval for as long as the apply runs.
//
// Without a terminal, or off macOS, nothing is asked and no helper is made: a
// plan that must install Homebrew finds a valid ticket or ends as Refusal says.
func WithAdmin(
	ctx context.Context,
	c Context,
	m *Mutation,
	admin Admin,
	run func(askpass string) error,
) error {
	if admin.Terminal == nil || !askpassSupported {
		if admin.Why != "" {
			if err := admin.requireTicket(ctx, c, m); err != nil {
				return err
			}
		}
		return run("")
	}
	service, err := startAskpass(m)
	if err != nil {
		return err
	}
	defer service.close()
	if admin.Why != "" && !hasTicket(ctx, c, m) {
		if ctx.Err() != nil {
			return interruptedBeforeChange()
		}
		password, askErr := admin.ask(ctx, c)
		if askErr != nil {
			return askErr
		}
		service.remember(password)
	}
	return run(service.helper)
}

// requireTicket is for a run nobody can type a password into: it needs a valid
// ticket, or ends as Refusal says.
func (a Admin) requireTicket(ctx context.Context, c Context, m *Mutation) error {
	if hasTicket(ctx, c, m) {
		return nil
	}
	if ctx.Err() != nil {
		return interruptedBeforeChange()
	}
	if a.Refusal != "" {
		return Fail(ExitBlocked, "privilege", a.Refusal)
	}
	return nil
}

// ask reads the password up front, after one plain line saying why.
func (a Admin) ask(ctx context.Context, c Context) (string, error) {
	password, err := askVerified(
		ctx,
		a.Terminal,
		"Workbench needs your Mac password once, to "+a.Why+
			". It keeps it in memory until this apply ends and never saves it.",
	)
	switch {
	case err == nil:
		return password, nil
	case ctx.Err() != nil:
		return "", interruptedBeforeChange()
	}
	return "", Fail(
		ExitBlocked,
		"privilege",
		"Workbench could not get administrator rights, so nothing was changed. "+
			"Check that this account is an administrator, then run "+c.WorkbenchCommand()+" apply again",
	)
}

// askVerified shows announce, then asks for the password on terminal until sudo
// accepts one or [adminTries] have been wrong.
func askVerified(ctx context.Context, terminal *os.File, announce string) (string, error) {
	_, _ = fmt.Fprintln(terminal, announce)
	for attempt := 1; attempt <= adminTries; attempt++ {
		_, _ = fmt.Fprint(terminal, adminPrompt)
		password, err := readSecret(ctx, terminal)
		_, _ = fmt.Fprintln(terminal)
		if err != nil {
			return "", err
		}
		accepted, err := checkPassword(ctx, password)
		if err != nil {
			return "", err
		}
		if accepted {
			return password, nil
		}
		if attempt < adminTries {
			_, _ = fmt.Fprintln(terminal, "Sorry, try again.")
		}
	}
	return "", errors.New("the Mac password was not accepted")
}

// readPassword reads a line from terminal without echo. A read that is blocked
// does not return when ctx ends, so on ctrl+c it puts the terminal back as it was
// itself, which the read's own deferred restore would do only after a key.
func readPassword(ctx context.Context, terminal *os.File) (string, error) {
	fd := terminal.Fd()
	saved, err := term.GetState(fd)
	if err != nil {
		return "", err
	}
	type typed struct {
		password []byte
		err      error
	}
	done := make(chan typed, 1)
	go func() {
		password, readErr := term.ReadPassword(fd)
		done <- typed{password, readErr}
	}()
	select {
	case t := <-done:
		return string(t.password), t.err
	case <-ctx.Done():
		_ = term.Restore(fd, saved)
		return "", ctx.Err()
	}
}

// checkPassword reports whether sudo accepts password. It runs sudo -k -S -v with
// the password on its standard input, the one place it belongs: -k ignores and
// updates no ticket, so nothing is left behind, and it is neither in an argument
// nor in the environment. Its output is discarded. It is the one sudo run that is
// not through [Run], because the password helper makes it in its own process,
// which holds no [Mutation].
func checkPassword(ctx context.Context, password string) (bool, error) {
	executable, err := trustedExecutable(sudoPath, nil)
	if err != nil {
		return false, err
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, executable, "-k", "-S", "-v", "-p", "")
	cmd.Dir = "/"
	cmd.Env = append([]string{}, sudoEnvironment...)
	cmd.Stdin = strings.NewReader(password + "\n")
	err = cmd.Run()
	if err == nil {
		return true, nil
	}
	if _, isExit := errors.AsType[*exec.ExitError](err); isExit && ctx.Err() == nil {
		return false, nil
	}
	return false, err
}

func interruptedBeforeChange() error {
	return Fail(
		ExitInterrupted,
		"interrupted",
		"Interrupted while asking for the Mac password; nothing was changed",
	)
}

// hasTicket reports whether a sudo approval is valid, by renewing it with
// sudo -n -v, which never asks.
func hasTicket(ctx context.Context, c Context, m *Mutation) bool {
	_, err := Run(ctx, c, m, Process{
		Executable:  sudoPath,
		Args:        []string{"-n", "-v"},
		Directory:   "/",
		Environment: sudoEnvironment,
		Timeout:     30 * time.Second,
		Mutates:     true,
	})
	return err == nil
}
