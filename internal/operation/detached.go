package operation

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
)

// Detached is a Workbench process [StartDetached] starts.
type Detached struct {
	// Entry is the Workbench command to start: the program its links resolve
	// to runs, with Entry as its argv[0], so the child knows both the path it
	// was started as (see [Invoked]) and the file it runs ([os.Executable]).
	Entry string
	Args  []string
	// Output is the file stdout and stderr are appended to.
	Output string
	// Env is added to the caller's environment, which the child inherits.
	Env []string
	// Files are passed on as the child's descriptors 3, 4 and so on.
	Files []*os.File
}

// StartDetached starts d in a new session and returns at once: stdin is the
// null device, stdout and stderr are appended to d.Output (made 0600, never
// through a link) and the working folder is /, so the child never holds the
// caller's folder in use; it outlives the caller. The caller may wait for the
// process it returns, and otherwise leaves it to the init process once it
// exits itself. The output file is the child's alone, apart from its log,
// because a write to it takes no lock and a cut of the log (see [RewriteLog])
// could lose it; it is only ever renamed (see [RotateOutput]), as a supervisor
// keeps a service's own output apart from its structured log. The costs hook
// starts its ingest worker with it, the guard's pre hook its watcher, and a
// watcher the newer release's. Tools go through [Run].
func StartDetached(d Detached) (*os.Process, error) {
	program, err := filepath.EvalSymlinks(d.Entry)
	if err != nil {
		return nil, err
	}
	output, err := openLogFile(d.Output, os.O_APPEND|os.O_CREATE|os.O_WRONLY)
	if err != nil {
		return nil, err
	}
	defer func() { _ = output.Close() }()
	null, err := os.Open(os.DevNull)
	if err != nil {
		return nil, err
	}
	defer func() { _ = null.Close() }()
	cmd := &exec.Cmd{
		Path:        program,
		Args:        append([]string{d.Entry}, d.Args...),
		Env:         append(os.Environ(), d.Env...),
		Stdin:       null,
		Stdout:      output,
		Stderr:      output,
		ExtraFiles:  d.Files,
		Dir:         "/",
		SysProcAttr: &syscall.SysProcAttr{Setsid: true},
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return cmd.Process, nil
}

// Invoked is the path this process was started as, its argv[0], made
// absolute: a name without a slash, as a shell finds it, is looked up on
// PATH. Unlike [os.Executable], which on Linux reads /proc/self/exe with its
// links resolved, it keeps a link such as the bin folder's workbench, so it
// names the installed command, not the release it pointed to at the start.
func Invoked() (string, error) {
	if !strings.Contains(os.Args[0], "/") {
		return exec.LookPath(os.Args[0])
	}
	return filepath.Abs(os.Args[0])
}
