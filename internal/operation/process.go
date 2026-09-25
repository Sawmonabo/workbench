package operation

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// FindExecutable ignores empty/relative PATH entries and project-local shims.
// This discovers a location only; callers must verify version/capabilities.
func FindExecutable(name, searchPath string, excluded []string) (string, error) {
	if filepath.Base(name) != name || name == "." || name == "" {
		return "", Fail(ExitInvalid, "tool", "Tool discovery requires a command basename")
	}
	for _, directory := range filepath.SplitList(searchPath) {
		if !filepath.IsAbs(directory) {
			continue
		}
		path, err := trustedExecutable(filepath.Join(directory, name), excluded)
		if err == nil {
			return path, nil
		}
	}
	return "", Fail(
		ExitBlocked,
		"dependency",
		"Required executable was not found outside project-controlled PATH entries",
	)
}

func trustedExecutable(path string, excluded []string) (string, error) {
	if !filepath.IsAbs(path) {
		return "", Fail(ExitInvalid, "tool", "Subprocess executable must be absolute")
	}
	canonical, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", Fail(ExitBlocked, "dependency", "Selected executable is unavailable")
	}
	for _, candidate := range []string{path, canonical} {
		if err := outsideProjects(filepath.Dir(candidate), excluded); err != nil {
			return "", err
		}
	}
	info, err := os.Stat(canonical)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 ||
		info.Mode().Perm()&0o022 != 0 {
		return "", Fail(
			ExitBlocked,
			"tool",
			"Selected executable must be a regular executable without group/other write access",
		)
	}
	return canonical, nil
}

func outsideProjects(directory string, excluded []string) error {
	for _, root := range excluded {
		if root != "" && Within(root, directory) {
			return Fail(
				ExitBlocked,
				"tool",
				"Project-controlled management executable lookup is forbidden",
			)
		}
	}
	for parent := directory; ; parent = filepath.Dir(parent) {
		for _, marker := range projectMarkers {
			if _, err := os.Lstat(filepath.Join(parent, marker)); err == nil {
				if marker == ".git" && homebrewRepository(parent, directory) {
					continue
				}
				return Fail(
					ExitBlocked,
					"tool",
					"Project-controlled management executable lookup is forbidden",
				)
			} else if !os.IsNotExist(err) {
				return Fail(
					ExitBlocked,
					"tool",
					"Cannot establish ownership of a management executable directory",
				)
			}
		}
		if parent == filepath.Dir(parent) {
			return nil
		}
	}
}

// Homebrew itself is a Git checkout at its standard installation locations.
// Recognize its installed-tool directories, not arbitrary repositories with a
// Homebrew-shaped name. Explicit project exclusions and nested markers still win.
func homebrewRepository(repository, directory string) bool {
	known := runtime.GOOS == "darwin" &&
		(repository == "/opt/homebrew" || repository == "/usr/local/Homebrew") ||
		runtime.GOOS == "linux" && repository == "/home/linuxbrew/.linuxbrew/Homebrew"
	if !known {
		return false
	}
	installed := false
	for _, name := range []string{"bin", "sbin", "Cellar", "opt"} {
		if Within(filepath.Join(repository, name), directory) {
			installed = true
			break
		}
	}
	if !installed {
		return false
	}
	for _, name := range []string{"bin/brew", "Library/Homebrew/brew.sh"} {
		info, err := os.Lstat(filepath.Join(repository, name))
		if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o022 != 0 {
			return false
		}
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !ok || (stat.Uid != 0 && int(stat.Uid) != os.Geteuid()) {
			return false
		}
	}
	return true
}

// Process is one subprocess request for [Run].
type Process struct {
	Executable string
	Args       []string
	Directory  string
	// Environment is the entire environment, not additions to os.Environ.
	// Callers must use only audited variables and a trusted absolute PATH.
	Environment []string
	Input       []byte
	Secrets     []string
	// Timeout bounds the run. Zero means one minute for captured runs and no
	// deadline for terminal or progress runs, which the operator interrupts.
	Timeout     time.Duration
	OutputLimit int
	Mutates     bool
	// PrivateOutput returns stdout unredacted, as exact private target images;
	// never attach it to public results. A failure still reports the redacted
	// stderr tail, and overflow withholds all output.
	PrivateOutput bool
	// Terminal is only for approved interactive native setup, never preview.
	// The child owns it as the foreground process group, so sudo can prompt.
	Terminal *os.File
	// Progress receives redacted output lines as they arrive instead of
	// capturing them, for unattended native runs without a terminal.
	Progress io.Writer
}

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
	attributes.Foreground = true
	attributes.Ctty = fd
	return func() {
		signal.Ignore(syscall.SIGTTOU)
		defer signal.Reset(syscall.SIGTTOU)
		_ = unix.IoctlSetPointerInt(fd, unix.TIOCSPGRP, foreground)
	}, nil
}

// ProcessOutput is a captured run's redacted output.
type ProcessOutput struct{ Stdout, Stderr string }

// Run is the only subprocess owner. Read-only requests are reviewed native probes,
// not a sandbox for arbitrary tools; never label a modifying command read-only.
func Run(
	ctx context.Context,
	c Context,
	mutation *Mutation,
	request Process,
) (ProcessOutput, error) {
	var output ProcessOutput
	executable, directory, err := request.admit(c, mutation)
	if err != nil {
		return output, err
	}
	if request.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, request.Timeout)
		defer cancel()
	}
	cmd := exec.CommandContext(ctx, executable, request.Args...)
	cmd.Dir = directory
	cmd.Env = append([]string{}, request.Environment...)
	cmd.Stdin = bytes.NewReader(request.Input)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if request.Terminal != nil {
		restore, err := lendTerminal(cmd.SysProcAttr, request.Terminal)
		if err != nil {
			return output, err
		}
		defer restore()
	}
	cmd.Cancel = func() error {
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		return err
	}
	cmd.WaitDelay = time.Second
	stdout := &boundedBuffer{limit: request.OutputLimit}
	stderr := &boundedBuffer{limit: request.OutputLimit}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	var progress *redactingWriter
	switch {
	case request.Terminal != nil:
		cmd.Stdin, cmd.Stdout, cmd.Stderr = request.Terminal, request.Terminal, request.Terminal
	case request.Progress != nil:
		progress = &redactingWriter{out: request.Progress, secrets: request.Secrets}
		cmd.Stdout, cmd.Stderr = progress, progress
	}
	err = cmd.Run()
	// A tool may exit while a child retains its pipes. WaitDelay bounds the
	// wait, and the process group cleanup prevents retained children lingering.
	if errors.Is(err, exec.ErrWaitDelay) && cmd.Process != nil {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	// An interrupted stream may end halfway through a secret; do not expose it.
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		limit := request.Timeout.String()
		message := filepath.Base(executable) + " exceeded its " + limit + " limit and was stopped"
		return output, Fail(ExitFailed, "timeout", message)
	}
	if ctx.Err() != nil {
		return output, ctx.Err()
	}
	if progress != nil {
		progress.flush()
	}
	// Suppress overflowing output entirely, including partial secret suffixes.
	if stdout.overflow || stderr.overflow {
		return output, Fail(
			ExitFailed,
			"output_limit",
			"Subprocess output exceeded its limit; output withheld",
		)
	}
	if err != nil {
		return output, failure(
			filepath.Base(executable),
			err,
			redact(stderr.String(), request.Secrets),
		)
	}
	output.Stdout = redact(stdout.String(), request.Secrets)
	output.Stderr = redact(stderr.String(), request.Secrets)
	if request.PrivateOutput {
		output.Stdout = stdout.String()
	}
	if len(output.Stdout) > request.OutputLimit || len(output.Stderr) > request.OutputLimit {
		return ProcessOutput{}, Fail(
			ExitFailed,
			"output_limit",
			"Redacted subprocess output exceeded its limit; output withheld",
		)
	}
	return output, nil
}

// admit checks the request's authority, executable, directory and bounds, and
// fills in the default timeout and output limit. It returns the canonical
// executable and working directory.
func (request *Process) admit(c Context, mutation *Mutation) (string, string, error) {
	if request.Terminal != nil &&
		(!request.Mutates || len(request.Input) != 0 || !isTerminal(request.Terminal)) {
		return "", "", Fail(
			ExitBlocked,
			"terminal",
			"Terminal execution requires approved mutation, a controlling terminal and no piped input",
		)
	}
	if request.Mutates {
		if err := mutation.Check(); err != nil {
			return "", "", err
		}
		if c.ReadOnly || mutation.context.Scope != c.Scope {
			return "", "", Fail(
				ExitBlocked,
				"read_only",
				"Subprocess mutation is outside the approved context",
			)
		}
	}
	excluded := []string{c.Native.Source}
	if c.Scope.Kind == "project" {
		excluded = append(excluded, c.Scope.Root)
	}
	executable, err := trustedExecutable(request.Executable, excluded)
	if err != nil {
		return "", "", err
	}
	directory, err := ExistingDirectory(request.Directory)
	if err != nil {
		return "", "", err
	}
	if !filepath.IsAbs(request.Directory) {
		return "", "", Fail(
			ExitInvalid,
			"process",
			"Subprocess working directory must be explicit and absolute",
		)
	}
	if !request.Mutates && c.Scope.Kind == "project" && Within(c.Scope.Root, directory) {
		return "", "", Fail(
			ExitBlocked,
			"process",
			"Read-only management probes must run outside the selected project",
		)
	}
	// Captured runs are bounded probes and helpers. Streamed native provisioning
	// depends on network and package sizes, so it runs until done or interrupted.
	streamed := request.Terminal != nil || request.Progress != nil
	if request.Timeout == 0 && !streamed {
		request.Timeout = time.Minute
	}
	if request.Timeout < 0 || request.Timeout > 30*time.Minute {
		return "", "", Fail(
			ExitInvalid,
			"process",
			"Subprocess timeout must be non-negative and at most 30 minutes",
		)
	}
	if request.OutputLimit == 0 {
		request.OutputLimit = 1 << 20
	}
	if request.OutputLimit < 1 || request.OutputLimit > 16<<20 || len(request.Input) > 16<<20 {
		return "", "", Fail(
			ExitInvalid,
			"process",
			"Subprocess input/output exceeds the bounded execution policy",
		)
	}
	return executable, directory, checkEnvironment(request.Environment, excluded)
}

// checkEnvironment requires explicit key/value entries and a PATH made only of
// existing absolute directories outside project control.
func checkEnvironment(environment, excluded []string) error {
	for _, variable := range environment {
		key, value, ok := strings.Cut(variable, "=")
		if !ok || key == "" {
			return Fail(
				ExitInvalid,
				"process",
				"Subprocess environment requires explicit key/value entries",
			)
		}
		if key != "PATH" {
			continue
		}
		for _, entry := range filepath.SplitList(value) {
			if !filepath.IsAbs(entry) {
				return Fail(ExitInvalid, "process", "Subprocess PATH entries must be absolute")
			}
			canonical, err := ExistingDirectory(entry)
			if err != nil {
				return err
			}
			if err := outsideProjects(entry, excluded); err != nil {
				return err
			}
			if err := outsideProjects(canonical, excluded); err != nil {
				return err
			}
		}
	}
	return nil
}

type boundedBuffer struct {
	buffer   bytes.Buffer
	limit    int
	overflow bool
}

func (b *boundedBuffer) Write(data []byte) (int, error) {
	length := len(data)
	remaining := b.limit - b.buffer.Len()
	if length > remaining {
		b.overflow = true
		data = data[:remaining]
	}
	_, _ = b.buffer.Write(data)
	return length, nil
}

func (b *boundedBuffer) String() string { return b.buffer.String() }

// failure reports a failed tool with its own diagnostics. Once the tool has
// exited its captured stderr is complete, so the redacted tail is safe to show.
// Terminal and progress runs showed their output live and capture none.
func failure(name string, err error, stderr string) error {
	var exit *exec.ExitError
	if !errors.As(err, &exit) {
		return Fail(ExitFailed, "subprocess", name+" could not run: "+err.Error())
	}
	message := name + " failed (" + exit.String() + ")"
	if len(stderr) > 4096 {
		stderr = stderr[len(stderr)-4096:]
		if _, rest, ok := strings.Cut(stderr, "\n"); ok {
			stderr = rest
		}
	}
	if stderr = strings.TrimSpace(stderr); stderr != "" {
		message += ": " + stderr
	}
	return Fail(ExitFailed, "subprocess", message)
}

// redactingWriter forwards whole lines after replacing known secret values;
// no secret spans a line. Only a stream that ended normally is flushed.
type redactingWriter struct {
	out     io.Writer
	secrets []string
	pending []byte
}

// Write never fails the child: progress is diagnostic, not part of the result.
func (w *redactingWriter) Write(data []byte) (int, error) {
	w.pending = append(w.pending, data...)
	if end := bytes.LastIndexAny(w.pending, "\r\n"); end >= 0 {
		_, _ = io.WriteString(w.out, redact(string(w.pending[:end+1]), w.secrets))
		w.pending = append(w.pending[:0], w.pending[end+1:]...)
	}
	return len(data), nil
}

func (w *redactingWriter) flush() {
	if len(w.pending) > 0 {
		_, _ = io.WriteString(w.out, redact(string(w.pending), w.secrets)+"\n")
		w.pending = nil
	}
}

func redact(value string, secrets []string) string {
	ordered := append([]string{}, secrets...)
	sort.Slice(ordered, func(i, j int) bool { return len(ordered[i]) > len(ordered[j]) })
	for _, secret := range ordered {
		if secret != "" {
			value = strings.ReplaceAll(value, secret, "[REDACTED]")
		}
	}
	return value
}
