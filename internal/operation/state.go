package operation

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"syscall"
	"time"
)

// State describes current selections only. Checkpoint/image validation belongs
// to the recovery owner; unknown state fields fail rather than being discarded.
type State struct {
	SchemaVersion        int               `json:"schema_version"`
	Dependencies         []Dependency      `json:"dependencies,omitempty"`
	ActiveRelease        *ReleaseRecord    `json:"active_release"`
	AppliedConfiguration *SourceIdentity   `json:"applied_configuration"`
	AppliedAt            *time.Time        `json:"applied_at,omitempty"`
	PartialOperation     *PartialOperation `json:"partial_operation"`
}

// ReleaseRecord is an activated runtime: its identity, executable and the
// matched source directory.
type ReleaseRecord struct {
	Identity   SourceIdentity `json:"identity"`
	Executable string         `json:"executable"`
	Source     string         `json:"source"`
}

// PartialOperation records an operation that started changing targets and has
// not finished, so later operations stop for reconciliation.
type PartialOperation struct {
	ID    string `json:"id"`
	Scope Scope  `json:"scope"`
}

var (
	identifier  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)
	operationID = regexp.MustCompile(
		`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`,
	)
)

// ReadPrivateInput shares private-file validation with state/answer consumers.
// It never creates missing state and withholds unsafe or oversized input.
func ReadPrivateInput(path string, limit int64) ([]byte, error) {
	if limit < 1 || limit > 16<<20 {
		return nil, Fail(ExitInvalid, "input", "Invalid private input bound")
	}
	if err := checkPrivateFile(path); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return nil, Fail(ExitInvalid, "input", "Cannot read private input")
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil || int64(len(data)) > limit {
		return nil, Fail(ExitInvalid, "input", "Private input exceeds its read bound")
	}
	return data, nil
}

// Validate checks the schema version and every recorded identity, dependency
// and partial operation.
func (s State) Validate() error {
	if s.SchemaVersion != 1 {
		return Fail(
			ExitInvalid,
			"state_format",
			"Unsupported Workbench state schema; state was left unchanged",
		)
	}
	seen := map[string]bool{}
	for _, dependency := range s.Dependencies {
		if seen[dependency.Name] ||
			!slices.Contains([]string{"chezmoi", "python3", "uv"}, dependency.Name) ||
			!filepath.IsAbs(dependency.Path) ||
			dependency.Version == "" ||
			!slices.Contains(
				[]string{"user", "system", "homebrew", "workbench"},
				dependency.Owner,
			) {
			return Fail(ExitInvalid, "state_format", "Invalid recorded management dependency")
		}
		seen[dependency.Name] = true
	}
	for _, identity := range []*SourceIdentity{s.AppliedConfiguration, releaseIdentity(s.ActiveRelease)} {
		if identity == nil {
			continue
		}
		if !identifier.MatchString(identity.Release) || !ValidDigest(identity.ContentDigest) {
			return Fail(ExitInvalid, "state_format", "Malformed release identity in current state")
		}
	}
	if s.ActiveRelease != nil {
		if !filepath.IsAbs(s.ActiveRelease.Executable) || !filepath.IsAbs(s.ActiveRelease.Source) {
			return Fail(
				ExitInvalid,
				"state_format",
				"Active release requires absolute executable and source paths",
			)
		}
	}
	if s.PartialOperation != nil {
		if !operationID.MatchString(s.PartialOperation.ID) {
			return Fail(ExitInvalid, "state_format", "Malformed partial operation identity")
		}
		if err := s.PartialOperation.Scope.Validate(); err != nil {
			return err
		}
	}
	return nil
}

func releaseIdentity(record *ReleaseRecord) *SourceIdentity {
	if record == nil {
		return nil
	}
	return &record.Identity
}

// ReadState never creates files. Absence is distinct from invalid current state.
func ReadState(paths Paths) (*State, error) {
	path := filepath.Join(paths.State, "state.json")
	if _, err := os.Lstat(path); os.IsNotExist(err) {
		return nil, nil
	}
	if err := checkPrivateFile(path); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return nil, Fail(ExitInvalid, "state", "Cannot read private Workbench state")
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.LimitReader(f, 1024*1024+1))
	if err != nil || len(data) > 1024*1024 {
		return nil, Fail(
			ExitInvalid,
			"state",
			"Current state is unreadable or exceeds its metadata size limit",
		)
	}
	var state State
	if err := DecodeStrict(data, &state); err != nil {
		return nil, Fail(
			ExitInvalid,
			"state_format",
			"Malformed current Workbench state ("+err.Error()+"); no conversion or deletion attempted",
		)
	}
	if err := state.Validate(); err != nil {
		return nil, err
	}
	return &state, nil
}

// WriteState validates and atomically replaces the private state record.
func (m *Mutation) WriteState(state State) error {
	if err := m.Check(); err != nil {
		return err
	}
	if err := state.Validate(); err != nil {
		return err
	}
	if _, err := ReadState(m.context.Paths); err != nil {
		return err
	}
	data, err := json.Marshal(state)
	if err != nil {
		return err
	}
	return m.WritePrivate(filepath.Join(m.context.Paths.State, "state.json"), data)
}

// WritePrivate is for bounded private metadata, not target files or checkpoint
// images. It uses same-directory create/fsync/rename/fsync and mode 0600.
func (m *Mutation) WritePrivate(path string, data []byte) error {
	if err := m.Check(); err != nil {
		return err
	}
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return Fail(
			ExitInvalid,
			"scope",
			"Private metadata targets must be canonical absolute paths",
		)
	}
	// Atomic replacement of a lock file would leave flock on the old inode.
	// Compare filesystem identities: config and state overrides may themselves
	// alias the same directory under different spellings or mount paths.
	lockDirectory, err := os.Stat(filepath.Join(m.context.Paths.State, "locks"))
	if err != nil || !lockDirectory.IsDir() {
		return Fail(
			ExitConflict,
			"lock",
			"Cannot establish the held operation lock directory; stop before metadata writes",
		)
	}
	for ancestor := path; ; ancestor = filepath.Dir(ancestor) {
		info, err := os.Stat(ancestor)
		if err == nil && os.SameFile(lockDirectory, info) {
			return Fail(
				ExitInvalid,
				"scope",
				"Operation lock files are reserved and cannot be replaced by metadata writes",
			)
		}
		if err != nil && !os.IsNotExist(err) {
			return Fail(ExitInvalid, "scope", "Cannot inspect private metadata target ancestry")
		}
		if ancestor == filepath.Dir(ancestor) {
			break
		}
	}
	if len(data) > 1024*1024 {
		return Fail(ExitInvalid, "state", "Private metadata exceeds its 1 MiB limit")
	}
	allowed := false
	for _, base := range []string{m.context.Paths.Config, m.context.Paths.State} {
		if path != base && Within(base, path) {
			allowed = true
			if err := ensureRoot(base); err != nil {
				return err
			}
		}
	}
	if !allowed {
		return Fail(
			ExitInvalid,
			"scope",
			"Private metadata write is outside config/state directories",
		)
	}
	if err := ensurePrivateDirectory(filepath.Dir(path)); err != nil {
		return err
	}
	if _, err := os.Lstat(path); err == nil {
		if err := checkPrivateFile(path); err != nil {
			return err
		}
	} else if !os.IsNotExist(err) {
		return Fail(ExitInvalid, "state", "Cannot inspect private metadata target")
	}
	root, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	id, err := NewID()
	if err != nil {
		return err
	}
	temporary := ".write-" + id
	f, err := root.OpenFile(temporary, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	defer func() { _ = root.Remove(temporary) }()
	_, writeErr := f.Write(data)
	if writeErr == nil {
		writeErr = f.Sync()
	}
	closeErr := f.Close()
	if writeErr != nil {
		return writeErr
	}
	if closeErr != nil {
		return closeErr
	}
	if err := root.Rename(temporary, filepath.Base(path)); err != nil {
		return err
	}
	directory, err := root.Open(".")
	if err != nil {
		return err
	}
	defer func() { _ = directory.Close() }()
	return directory.Sync()
}

func ensurePrivateDirectory(path string) error {
	if err := safeParents(path); err != nil {
		return err
	}
	if err := privateFilesystem(path); err != nil {
		return err
	}
	if err := os.MkdirAll(path, 0o700); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode().Perm() != 0o700 {
		return Fail(
			ExitInvalid,
			"permissions",
			"Existing private state directory must have mode 0700; it was not changed",
		)
	}
	return owned(info)
}

// ensureRoot creates a missing runtime root 0700. Its missing parents, such as
// ~/.config or ~/.local on Linux, get the usual 0755 instead: the machine
// configuration manages some of them, and it refuses to change the mode of a
// directory that holds Workbench state.
func ensureRoot(root string) error {
	if err := safeParents(root); err != nil {
		return err
	}
	if err := privateFilesystem(root); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(root), 0o755); err != nil {
		return err
	}
	return ensurePrivateDirectory(root)
}

type locks struct{ files []*os.File }

// All writes currently hold an exclusive shared lock before their distinct
// machine/project lock. This also serializes overlapping parent/child scopes.
// Lock files are never unlinked: flock releases on close/process death.
func acquireLocks(c Context) (*locks, error) {
	if c.ReadOnly {
		return nil, Fail(ExitBlocked, "read_only", "Read-only operations cannot create locks")
	}
	if _, err := ReadState(c.Paths); err != nil {
		return nil, err
	}
	if err := ensureRoot(c.Paths.State); err != nil {
		return nil, err
	}
	directory := filepath.Join(c.Paths.State, "locks")
	if err := ensurePrivateDirectory(directory); err != nil {
		return nil, err
	}
	result := &locks{}
	for _, name := range []string{"shared.lock", c.Scope.Kind + "-" + scopeDigest(c) + ".lock"} {
		path := filepath.Join(directory, name)
		f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0o600)
		if err != nil {
			result.close()
			return nil, Fail(ExitBlocked, "lock", "Cannot open private operation lock")
		}
		result.files = append(result.files, f)
		if err := checkPrivateFile(path); err != nil {
			result.close()
			return nil, err
		}
		if held, err := flockNonBlocking(f); held || err != nil {
			result.close()
			if err != nil {
				return nil, Fail(ExitBlocked, "lock", "Cannot lock the private operation lock")
			}
			return nil, Fail(
				ExitBlocked,
				"locked",
				"Another Workbench mutation holds the shared or selected scope lock; retry after it finishes",
			)
		}
		if err := f.Truncate(0); err != nil {
			result.close()
			return nil, err
		}
		if _, err := fmt.Fprintf(
			f,
			"schema_version=1\npid=%d\nscope=%s\n",
			os.Getpid(),
			c.Scope.Kind,
		); err != nil {
			result.close()
			return nil, err
		}
	}
	if _, err := ReadState(c.Paths); err != nil {
		result.close()
		return nil, err
	}
	return result, nil
}

// flockNonBlocking takes an exclusive lock on f without waiting. held is true
// when another process has it, which is not an error.
func flockNonBlocking(f *os.File) (held bool, err error) {
	for {
		err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		switch {
		case err == nil:
			return false, nil
		case errors.Is(err, syscall.EINTR):
		case errors.Is(err, syscall.EWOULDBLOCK):
			return true, nil
		default:
			return false, err
		}
	}
}

// TryLock takes an exclusive lock on the file at path without waiting, for a
// background job that must run once at a time (the costs ingest worker). The
// file and its parent directory are created when missing (the directory 0700)
// and the file is never removed: the lock ends when release is called or the
// process dies. held is true, with no error, when another process has it.
func TryLock(path string) (release func(), held bool, err error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, false, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return nil, false, err
	}
	held, err = flockNonBlocking(f)
	if held || err != nil {
		_ = f.Close()
		return nil, held, err
	}
	return func() { _ = f.Close() }, false, nil
}

func (l *locks) close() {
	for _, v := range slices.Backward(l.files) {
		_ = v.Close()
	}
	l.files = nil
}

// RemovePrivateDirectory deletes directory, a runtime directory below the data
// root that the approved plan lists for removal: an older release, application
// context or private tool version. It is a no-op once directory is gone.
func (m *Mutation) RemovePrivateDirectory(directory string) error {
	if err := m.Check(); err != nil {
		return err
	}
	data := m.context.Paths.Data
	if filepath.Clean(directory) != directory || directory == data || !Within(data, directory) {
		return Fail(
			ExitInvalid,
			"private_path",
			"Only directories below Workbench data are removed",
		)
	}
	info, err := os.Lstat(directory)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return Fail(ExitInvalid, "private_path", "Only private directories are removed")
	}
	id, err := NewID()
	if err != nil {
		return err
	}
	return removeDirectory(directory, filepath.Dir(directory), ".removed-"+id)
}

// removeDirectory moves directory into trash as name and syncs directory's
// parent, so the removal is durable before any file is deleted, then deletes
// the moved tree.
func removeDirectory(directory, trash, name string) error {
	removed := filepath.Join(trash, name)
	if err := os.Rename(directory, removed); err != nil {
		return err
	}
	parent, err := os.Open(filepath.Dir(directory))
	if err != nil {
		return err
	}
	defer func() { _ = parent.Close() }()
	if err = parent.Sync(); err != nil {
		return err
	}
	return os.RemoveAll(removed)
}
