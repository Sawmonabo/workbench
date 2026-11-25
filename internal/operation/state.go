package operation

import (
	"encoding/json"
	"path/filepath"
	"regexp"
	"slices"
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

// Unfinished returns the blocked error when the state records an unfinished
// operation that this scope may not replace: one for another scope or, when own
// is given, one whose ID is not in own. Callers check it before overwriting
// PartialOperation, which holds a single record.
func (s *State) Unfinished(scope Scope, own ...string) error {
	if s == nil || s.PartialOperation == nil {
		return nil
	}
	partial := s.PartialOperation
	if partial.Scope == scope && (len(own) == 0 || slices.Contains(own, partial.ID)) {
		return nil
	}
	return Fail(
		ExitBlocked,
		"recovery",
		"Apply "+partial.ID+" to "+partial.Scope.Root+
			" did not finish; rerun apply for that destination first",
	)
}

var (
	identifier  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)
	operationID = regexp.MustCompile(
		`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`,
	)
)

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
	data, found, err := ReadPrivateRecord(filepath.Join(paths.State, "state.json"), 1<<20)
	if !found || err != nil {
		return nil, err
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
