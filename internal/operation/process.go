package operation

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"syscall"
	"time"
)

// FindExecutable ignores empty/relative PATH entries and project-local shims.
// This discovers a location only; callers must verify version/capabilities.
func FindExecutable(name, searchPath string, excluded []string) (string, error) {
	if filepath.Base(name) != name || name == "." || name == "" {
		return "", Fail(2, "tool", "Tool discovery requires a command basename")
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
	return "", Fail(3, "dependency", "Required executable was not found outside project-controlled PATH entries")
}

func trustedExecutable(path string, excluded []string) (string, error) {
	if !filepath.IsAbs(path) {
		return "", Fail(2, "tool", "Subprocess executable must be absolute")
	}
	canonical, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", Fail(3, "dependency", "Selected executable is unavailable")
	}
	for _, candidate := range []string{path, canonical} {
		if err := outsideProjects(filepath.Dir(candidate), excluded); err != nil {
			return "", err
		}
	}
	info, err := os.Stat(canonical)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0111 == 0 || info.Mode().Perm()&0022 != 0 {
		return "", Fail(3, "tool", "Selected executable must be a regular executable without group/other write access")
	}
	return canonical, nil
}

func outsideProjects(directory string, excluded []string) error {
	for _, root := range excluded {
		if root != "" && Within(root, directory) {
			return Fail(3, "tool", "Project-controlled management executable lookup is forbidden")
		}
	}
	for parent := directory; ; parent = filepath.Dir(parent) {
		for _, marker := range []string{".git", "pyproject.toml", "package.json", "Cargo.toml", "go.mod"} {
			if _, err := os.Lstat(filepath.Join(parent, marker)); err == nil {
				if marker == ".git" && homebrewRepository(parent, directory) {
					continue
				}
				return Fail(3, "tool", "Project-controlled management executable lookup is forbidden")
			} else if !os.IsNotExist(err) {
				return Fail(3, "tool", "Cannot establish ownership of a management executable directory")
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
	known := runtime.GOOS == "darwin" && (repository == "/opt/homebrew" || repository == "/usr/local/Homebrew") ||
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
		if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0022 != 0 {
			return false
		}
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !ok || (stat.Uid != 0 && int(stat.Uid) != os.Geteuid()) {
			return false
		}
	}
	return true
}

type Process struct {
	Executable string
	Args       []string
	Directory  string
	// Environment is the entire environment, not additions to os.Environ.
	// Callers must use only audited variables and a trusted absolute PATH.
	Environment []string
	Input       []byte
	Secrets     []string
	Timeout     time.Duration
	OutputLimit int
	Mutates     bool
}
type ProcessOutput struct{ Stdout, Stderr string }

// Run is the only subprocess owner. Read-only requests are reviewed native probes,
// not a sandbox for arbitrary tools; never label a modifying command read-only.
func Run(ctx context.Context, c Context, mutation *Mutation, request Process) (ProcessOutput, error) {
	var output ProcessOutput
	if request.Mutates {
		if err := mutation.Check(); err != nil {
			return output, err
		}
		if c.ReadOnly || mutation.context.Scope != c.Scope {
			return output, Fail(3, "read_only", "Subprocess mutation is outside the approved context")
		}
	}
	excluded := []string{c.Native.Source}
	if c.Scope.Kind == "project" {
		excluded = append(excluded, c.Scope.Root)
	}
	executable, err := trustedExecutable(request.Executable, excluded)
	if err != nil {
		return output, err
	}
	directory, err := ExistingDirectory(request.Directory)
	if err != nil {
		return output, err
	}
	if !filepath.IsAbs(request.Directory) {
		return output, Fail(2, "process", "Subprocess working directory must be explicit and absolute")
	}
	if !request.Mutates && c.Scope.Kind == "project" && Within(c.Scope.Root, directory) {
		return output, Fail(3, "process", "Read-only management probes must run outside the selected project")
	}
	if request.Timeout == 0 {
		request.Timeout = time.Minute
	}
	if request.Timeout < 0 || request.Timeout > 30*time.Minute {
		return output, Fail(2, "process", "Subprocess timeout must be positive and at most 30 minutes")
	}
	if request.OutputLimit == 0 {
		request.OutputLimit = 1024 * 1024
	}
	if request.OutputLimit < 1 || request.OutputLimit > 16*1024*1024 || len(request.Input) > 16*1024*1024 {
		return output, Fail(2, "process", "Subprocess input/output exceeds the bounded execution policy")
	}
	for _, variable := range request.Environment {
		key, value, ok := strings.Cut(variable, "=")
		if !ok || key == "" {
			return output, Fail(2, "process", "Subprocess environment requires explicit key/value entries")
		}
		if key == "PATH" {
			for _, entry := range filepath.SplitList(value) {
				if !filepath.IsAbs(entry) {
					return output, Fail(2, "process", "Subprocess PATH entries must be absolute")
				}
				canonical, err := ExistingDirectory(entry)
				if err != nil {
					return output, err
				}
				if err := outsideProjects(entry, excluded); err != nil {
					return output, err
				}
				if err := outsideProjects(canonical, excluded); err != nil {
					return output, err
				}
			}
		}
	}
	ctx, cancel := context.WithTimeout(ctx, request.Timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, executable, request.Args...)
	cmd.Dir = directory
	cmd.Env = append([]string{}, request.Environment...)
	cmd.Stdin = bytes.NewReader(request.Input)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		return err
	}
	cmd.WaitDelay = time.Second
	stdout, stderr := &boundedBuffer{limit: request.OutputLimit}, &boundedBuffer{limit: request.OutputLimit}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	err = cmd.Run()
	// A tool may exit while a child retains its pipes. WaitDelay bounds the
	// wait, and the process group cleanup prevents retained children lingering.
	if errors.Is(err, exec.ErrWaitDelay) && cmd.Process != nil {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	// An interrupted stream may end halfway through a secret; do not expose it.
	if ctx.Err() != nil {
		return output, ctx.Err()
	}
	// Suppress overflowing output entirely, including partial secret suffixes.
	if stdout.overflow || stderr.overflow {
		return output, Fail(1, "output_limit", "Subprocess output exceeded its limit; output withheld")
	}
	if err != nil {
		return output, Fail(1, "subprocess", "Subprocess failed; incomplete output withheld; inspect prerequisites before retrying")
	}
	output.Stdout = Redact(stdout.String(), request.Secrets)
	output.Stderr = Redact(stderr.String(), request.Secrets)
	if len(output.Stdout) > request.OutputLimit || len(output.Stderr) > request.OutputLimit {
		return ProcessOutput{}, Fail(1, "output_limit", "Redacted subprocess output exceeded its limit; output withheld")
	}
	return output, nil
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

func Redact(value string, secrets []string) string {
	ordered := append([]string{}, secrets...)
	sort.Slice(ordered, func(i, j int) bool { return len(ordered[i]) > len(ordered[j]) })
	for _, secret := range ordered {
		if secret != "" {
			value = strings.ReplaceAll(value, secret, "[REDACTED]")
		}
	}
	return value
}
