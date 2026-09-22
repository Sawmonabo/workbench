package operation

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
)

// State describes current selections only. Checkpoint/image validation belongs
// to the recovery owner; unknown state fields fail rather than being discarded.
type State struct {
	SchemaVersion        int               `json:"schema_version"`
	Dependencies         []Dependency      `json:"dependencies,omitempty"`
	ActiveRelease        *ReleaseRecord    `json:"active_release"`
	AppliedConfiguration *SourceIdentity   `json:"applied_configuration"`
	PartialOperation     *PartialOperation `json:"partial_operation"`
}
type ReleaseRecord struct {
	Identity   SourceIdentity `json:"identity"`
	Executable string         `json:"executable"`
	Source     string         `json:"source"`
}
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
		return nil, Fail(2, "input", "Invalid private input bound")
	}
	if err := checkPrivateFile(path); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return nil, Fail(2, "input", "Cannot read private input")
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil || int64(len(data)) > limit {
		return nil, Fail(2, "input", "Private input exceeds its read bound")
	}
	return data, nil
}

func (s State) Validate() error {
	if s.SchemaVersion != 1 {
		return Fail(
			2,
			"state_format",
			"Unsupported Workbench state schema; state was left unchanged",
		)
	}
	seen := map[string]bool{}
	for _, dependency := range s.Dependencies {
		if seen[dependency.Name] ||
			(dependency.Name != "chezmoi" && dependency.Name != "python3" && dependency.Name != "uv") ||
			!filepath.IsAbs(dependency.Path) ||
			dependency.Version == "" ||
			(dependency.Owner != "user" && dependency.Owner != "system" && dependency.Owner != "homebrew" && dependency.Owner != "workbench") {
			return Fail(2, "state_format", "Invalid recorded management dependency")
		}
		seen[dependency.Name] = true
	}
	for _, identity := range []*SourceIdentity{s.AppliedConfiguration, releaseIdentity(s.ActiveRelease)} {
		if identity == nil {
			continue
		}
		digest, err := hex.DecodeString(identity.ContentDigest)
		if !identifier.MatchString(identity.Release) || err != nil || len(digest) != sha256.Size {
			return Fail(2, "state_format", "Malformed release identity in current state")
		}
	}
	if s.ActiveRelease != nil {
		if !filepath.IsAbs(s.ActiveRelease.Executable) || !filepath.IsAbs(s.ActiveRelease.Source) {
			return Fail(
				2,
				"state_format",
				"Active release requires absolute executable and source paths",
			)
		}
	}
	if s.PartialOperation != nil {
		if !operationID.MatchString(s.PartialOperation.ID) {
			return Fail(2, "state_format", "Malformed partial operation identity")
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
		return nil, Fail(2, "state", "Cannot read private Workbench state")
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.LimitReader(f, 1024*1024+1))
	if err != nil || len(data) > 1024*1024 {
		return nil, Fail(
			2,
			"state",
			"Current state is unreadable or exceeds its metadata size limit",
		)
	}
	var state State
	if err := validateStateJSON(json.NewDecoder(bytes.NewReader(data))); err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&state); err != nil {
		return nil, Fail(
			2,
			"state_format",
			"Malformed current Workbench state; no conversion or deletion attempted",
		)
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return nil, Fail(2, "state_format", "Current state must contain exactly one JSON object")
	}
	if err := state.Validate(); err != nil {
		return nil, err
	}
	return &state, nil
}

// encoding/json accepts duplicate object keys. State cannot, since that could
// hide an unsupported schema or contradictory identity from one of its readers.
func validateStateJSON(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return Fail(2, "state_format", "Malformed current state JSON")
	}
	delimiter, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	seen := map[string]bool{}
	for decoder.More() {
		if delimiter == '{' {
			key, err := decoder.Token()
			if err != nil {
				return Fail(2, "state_format", "Malformed current state JSON")
			}
			name, ok := key.(string)
			if !ok || name != strings.ToLower(name) || seen[name] {
				return Fail(2, "state_format", "Duplicate fields in current state are unsupported")
			}
			seen[name] = true
		}
		if err := validateStateJSON(decoder); err != nil {
			return err
		}
	}
	if _, err := decoder.Token(); err != nil {
		return Fail(2, "state_format", "Malformed current state JSON")
	}
	return nil
}

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
		return Fail(2, "scope", "Private metadata targets must be canonical absolute paths")
	}
	// Atomic replacement of a lock file would leave flock on the old inode.
	// Compare filesystem identities: config and state overrides may themselves
	// alias the same directory under different spellings or mount paths.
	lockDirectory, err := os.Stat(filepath.Join(m.context.Paths.State, "locks"))
	if err != nil || !lockDirectory.IsDir() {
		return Fail(
			4,
			"lock",
			"Cannot establish the held operation lock directory; stop before metadata writes",
		)
	}
	for ancestor := path; ; ancestor = filepath.Dir(ancestor) {
		info, err := os.Stat(ancestor)
		if err == nil && os.SameFile(lockDirectory, info) {
			return Fail(
				2,
				"scope",
				"Operation lock files are reserved and cannot be replaced by metadata writes",
			)
		}
		if err != nil && !os.IsNotExist(err) {
			return Fail(2, "scope", "Cannot inspect private metadata target ancestry")
		}
		if ancestor == filepath.Dir(ancestor) {
			break
		}
	}
	if len(data) > 1024*1024 {
		return Fail(2, "state", "Private metadata exceeds its 1 MiB limit")
	}
	allowed := false
	for _, base := range []string{m.context.Paths.Config, m.context.Paths.State} {
		if path != base && Within(base, path) {
			allowed = true
			if err := ensurePrivateDirectory(base); err != nil {
				return err
			}
		}
	}
	if !allowed {
		return Fail(2, "scope", "Private metadata write is outside config/state directories")
	}
	if err := ensurePrivateDirectory(filepath.Dir(path)); err != nil {
		return err
	}
	if _, err := os.Lstat(path); err == nil {
		if err := checkPrivateFile(path); err != nil {
			return err
		}
	} else if !os.IsNotExist(err) {
		return Fail(2, "state", "Cannot inspect private metadata target")
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
			2,
			"permissions",
			"Existing private state directory must have mode 0700; it was not changed",
		)
	}
	return owned(info)
}

type locks struct{ files []*os.File }

// All writes currently hold an exclusive shared lock before their distinct
// machine/project lock. This also serializes overlapping parent/child scopes.
// Lock files are never unlinked: flock releases on close/process death.
func acquireLocks(c Context) (*locks, error) {
	if c.ReadOnly {
		return nil, Fail(3, "read_only", "Read-only operations cannot create locks")
	}
	if _, err := ReadState(c.Paths); err != nil {
		return nil, err
	}
	if err := ensurePrivateDirectory(c.Paths.State); err != nil {
		return nil, err
	}
	directory := filepath.Join(c.Paths.State, "locks")
	if err := ensurePrivateDirectory(directory); err != nil {
		return nil, err
	}
	result := &locks{}
	digest := sha256.Sum256([]byte(c.Scope.Kind + ":" + c.Scope.Root))
	for _, name := range []string{"shared.lock", fmt.Sprintf("%s-%x.lock", c.Scope.Kind, digest)} {
		path := filepath.Join(directory, name)
		f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0o600)
		if err != nil {
			result.close()
			return nil, Fail(3, "lock", "Cannot open private operation lock")
		}
		result.files = append(result.files, f)
		if err := checkPrivateFile(path); err != nil {
			result.close()
			return nil, err
		}
		if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
			result.close()
			return nil, Fail(
				3,
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

func (l *locks) close() {
	for i := len(l.files) - 1; i >= 0; i-- {
		_ = l.files[i].Close()
	}
	l.files = nil
}
