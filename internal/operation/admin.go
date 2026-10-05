package operation

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
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

const (
	// adminRefresh is how often a held ticket is renewed, well inside the five
	// minutes sudo keeps one.
	adminRefresh = time.Minute
	adminPrompt  = "Mac password: "
)

// Admin says what an apply needs the administrator password for.
type Admin struct {
	// Terminal is where a person can type the password, nil when nobody can.
	Terminal *os.File
	// Why finishes the line shown before the prompt: "install Homebrew and apps".
	Why string
	// Refusal is, when nobody can type the password and no ticket exists, the
	// message of the blocked error that ends the apply before any change. Empty
	// carries on without a ticket.
	Refusal string
}

// WithAdmin makes the administrator ticket available while run does its work, so
// the apply asks for the password once, up front, and then runs without being
// asked again. Workbench never reads the password: sudo reads it from the
// terminal itself.
//
// An existing ticket (a recent sudo, or passwordless sudo) is used as it is. With
// none, a person at the terminal is asked once; an account that is not an
// administrator, a refusal or three wrong passwords end the apply as blocked, and
// a ctrl+c as interrupted, before run starts. Without a terminal nobody is asked.
//
// While run works the ticket is renewed about every minute from Workbench's own
// process, which sudo ties to the same terminal as the scripts' sudo, so a long
// apply is not asked again. When run returns, however it ends, the renewing stops
// and a ticket this call created is dropped with sudo -k; one that already
// existed is left as it was.
func WithAdmin(ctx context.Context, c Context, m *Mutation, admin Admin, run func() error) error {
	held, created, err := admin.acquire(ctx, c, m)
	if err != nil {
		return err
	}
	if held {
		stop := keepTicket(ctx, c, m)
		defer func() {
			stop()
			if created {
				// The ticket is dropped even when ctx ended the apply.
				_ = sudo(context.WithoutCancel(ctx), c, m, "-k")
			}
		}()
	}
	return run()
}

// acquire finds or obtains the ticket. held says a ticket exists and created that
// this call made it.
func (a Admin) acquire(
	ctx context.Context,
	c Context,
	m *Mutation,
) (held, created bool, err error) {
	if sudo(ctx, c, m, "-n", "-v") == nil {
		return true, false, nil
	}
	if ctx.Err() != nil {
		return false, false, interruptedBeforeChange()
	}
	if a.Terminal == nil {
		if a.Refusal != "" {
			return false, false, Fail(ExitBlocked, "privilege", a.Refusal)
		}
		return false, false, nil
	}
	if err = a.prompt(ctx, c); err != nil {
		return false, false, err
	}
	return true, true, nil
}

// prompt runs sudo -v on the terminal for the person to type the password, after
// one plain line saying why. It stays in Workbench's own process group, which is
// the terminal's foreground one, so ctrl+c reaches Workbench as well as sudo and
// ends the apply as interrupted; the process-group handoff native runs use would
// deliver it to sudo alone, which cannot be told from a wrong password.
// It is the one run that is not through [Run], like [StartDetached], because it
// must share Workbench's process group.
func (a Admin) prompt(ctx context.Context, c Context) error {
	executable, err := trustedExecutable(sudoPath, nil)
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintf(
		a.Terminal,
		"Workbench needs your Mac password once, to %s. sudo asks for it here; Workbench never sees it.\n",
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

func interruptedBeforeChange() error {
	return Fail(
		ExitInterrupted,
		"interrupted",
		"Interrupted while asking for the Mac password; nothing was changed",
	)
}

// keepTicket renews the ticket every [adminRefresh] until the returned function
// is called, which waits for a renewal still running. A failed renewal is
// ignored: the scripts' own sudo then asks, as it always did.
func keepTicket(ctx context.Context, c Context, m *Mutation) (stop func()) {
	ctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(adminRefresh)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				_ = sudo(ctx, c, m, "-n", "-v")
			}
		}
	}()
	return func() {
		cancel()
		<-done
	}
}

// sudo runs sudo with args and no terminal: -n never asks, so these runs only
// check, renew or drop the ticket.
func sudo(ctx context.Context, c Context, m *Mutation, args ...string) error {
	_, err := Run(ctx, c, m, Process{
		Executable:  sudoPath,
		Args:        args,
		Directory:   "/",
		Environment: sudoEnvironment,
		Timeout:     30 * time.Second,
		Mutates:     true,
	})
	return err
}
