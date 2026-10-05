package operation

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"slices"
	"strings"
	"syscall"
	"time"
)

// sudoPath is the one sudo Workbench runs. It is always this absolute path, never
// a PATH lookup, so a sudo planted earlier on PATH cannot capture the password.
// It is a variable only so the safeguard test can stand in for it.
var sudoPath = "/usr/bin/sudo"

// sudoEnvironment is the whole environment of a sudo run; in particular it has
// no SUDO_ASKPASS, which would divert the prompt to another program.
var sudoEnvironment = []string{"PATH=/usr/bin:/bin:/usr/sbin:/sbin"}

// pamSudoLocal and pamSudo are the two files whose active auth lines say how sudo
// asks on a Mac: the local one macOS keeps through system updates, which sudo
// reads first, and sudo's own. They are variables only so a check can point at
// scratch copies.
var (
	pamSudoLocal = "/etc/pam.d/sudo_local"
	pamSudo      = "/etc/pam.d/sudo"
)

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

// TouchIDForSudo reports whether sudo on this Mac asks for Touch ID: an active
// (uncommented) auth line of /etc/pam.d/sudo_local or /etc/pam.d/sudo names
// pam_tid.so. A file that is missing or unreadable says nothing. Where Touch ID
// cannot be used (over SSH, in tmux, with the lid closed, with no sensor) sudo
// still falls back to the password, so this says what sudo tries first.
func TouchIDForSudo() bool {
	if runtime.GOOS != "darwin" {
		return false
	}
	for _, path := range []string{pamSudoLocal, pamSudo} {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		for line := range strings.SplitSeq(string(data), "\n") {
			line, _, _ = strings.Cut(line, "#")
			fields := strings.Fields(line)
			if len(fields) > 0 && fields[0] == "auth" &&
				slices.Contains(fields[1:], "pam_tid.so") {
				return true
			}
		}
	}
	return false
}

// WithAdmin lets an apply at a terminal on macOS ask for the Mac password once.
// run is handed the path of the SUDO_ASKPASS helper for its scripts' Homebrew
// commands and the Touch ID step's one sudo command, or "" where there is none,
// and the apply's scripts give it to nothing else.
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
// Where sudo already asks for Touch ID (see [TouchIDForSudo]) Workbench reads no
// password and makes no helper; see [Admin.withTouchID].
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
	if TouchIDForSudo() {
		return admin.withTouchID(ctx, c, m, run)
	}
	service, err := startAskpass(m)
	if err != nil {
		return err
	}
	defer func() {
		if service.close() {
			// sudo keeps an approval for this terminal after the helper answered
			// it; nothing of the apply's access is left behind.
			dropTicket(context.WithoutCancel(ctx), c, m)
		}
	}()
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

// withTouchID is WithAdmin where sudo asks for Touch ID, or for its own password
// where Touch ID cannot be used: Workbench never reads the password and makes no
// helper, so run gets none. When the plan installs Homebrew (Why) and no valid
// ticket exists, sudo -v asks at the terminal before any change; Homebrew's
// installer then uses that approval, and Homebrew's own sudo asks for itself
// each time it needs it. A ticket this call created is dropped with sudo -k when
// run returns, however it ends; one that already existed is left as it was.
func (a Admin) withTouchID(
	ctx context.Context,
	c Context,
	m *Mutation,
	run func(askpass string) error,
) error {
	if a.Why != "" && !hasTicket(ctx, c, m) {
		if ctx.Err() != nil {
			return interruptedBeforeChange()
		}
		if err := a.prompt(ctx, c); err != nil {
			return err
		}
		defer dropTicket(context.WithoutCancel(ctx), c, m)
	}
	return run("")
}

// prompt runs sudo -v on the terminal, after one plain line saying why. sudo asks
// by Touch ID, or for the password when Touch ID cannot be used; either way
// Workbench never sees it. The run stays in Workbench's own process group, which
// is the terminal's foreground one, so ctrl+c reaches Workbench as well as sudo
// and ends the apply as interrupted; the process-group handoff native runs use
// would deliver it to sudo alone, which cannot be told from a wrong password.
// It is the one run that is not through [Run], like [StartDetached], because it
// must share Workbench's process group. A refusal, a non-administrator or three
// wrong passwords end the apply as blocked, a ctrl+c as interrupted, with
// nothing changed.
func (a Admin) prompt(ctx context.Context, c Context) error {
	executable, err := trustedExecutable(sudoPath, nil)
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintf(
		a.Terminal,
		"Workbench needs administrator rights once, to %s. sudo asks for Touch ID here, "+
			"or for your Mac password where Touch ID cannot be used; Workbench never sees it.\n",
		a.Why,
	)
	cmd := exec.CommandContext(
		ctx,
		executable,
		"-v",
		"-p",
		strings.ReplaceAll(adminPrompt, "%", "%%"),
	)
	cmd.Dir = "/"
	cmd.Env = append([]string{}, sudoEnvironment...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = a.Terminal, a.Terminal, a.Terminal
	// A signal lets sudo restore the terminal's echo; a kill would not.
	cmd.Cancel = func() error { return cmd.Process.Signal(syscall.SIGTERM) }
	cmd.WaitDelay = 2 * time.Second
	err = cmd.Run()
	if err == nil {
		return nil
	}
	exit, isExit := errors.AsType[*exec.ExitError](err)
	if ctx.Err() != nil || (isExit && killedByInterrupt(exit)) {
		return interruptedBeforeChange()
	}
	return Fail(
		ExitBlocked,
		"privilege",
		"Workbench could not get administrator rights, so nothing was changed. "+
			"Check that this account is an administrator, then run "+c.WorkbenchCommand()+" apply again",
	)
}

// killedByInterrupt reports that the process ended on SIGINT, which is what
// ctrl+c does to sudo at its prompt.
func killedByInterrupt(exit *exec.ExitError) bool {
	status, ok := exit.Sys().(syscall.WaitStatus)
	return ok && status.Signaled() && status.Signal() == syscall.SIGINT
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
		"Interrupted while asking for administrator rights; nothing was changed",
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

// dropTicket ends this terminal's sudo approval with sudo -k, which never asks.
func dropTicket(ctx context.Context, c Context, m *Mutation) {
	_, _ = Run(ctx, c, m, Process{
		Executable:  sudoPath,
		Args:        []string{"-k"},
		Directory:   "/",
		Environment: sudoEnvironment,
		Timeout:     30 * time.Second,
		Mutates:     true,
	})
}
