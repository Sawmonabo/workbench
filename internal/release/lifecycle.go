package release

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/Sawmonabo/workbench/internal/operation"
)

// Download fetches a bounded public HTTPS resource; callers verify its content.
func Download(ctx context.Context, location string, limit int64) ([]byte, error) {
	u, err := url.Parse(location)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Fragment != "" || limit < 1 ||
		limit > MaxDownload {
		return nil, operation.Fail(
			2,
			"download",
			"Downloads require HTTPS, no URL credentials, and a bounded size",
		)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, location, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", "application/octet-stream")
	client := &http.Client{
		Timeout: 2 * time.Minute,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 || req.URL.Scheme != "https" {
				return operation.Fail(2, "download", "Unsafe download redirect")
			}
			return nil
		},
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, operation.Fail(1, "download", "HTTPS download failed: "+err.Error())
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK || response.ContentLength > limit {
		return nil, operation.Fail(
			1,
			"download",
			"Release asset unavailable or exceeds the download bound",
		)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil || int64(len(data)) > limit {
		return nil, operation.Fail(
			1,
			"download",
			"Download exceeds its size bound or is incomplete",
		)
	}
	return data, nil
}

// ReadBundle verifies a release archive from a local file or HTTPS URL. A
// non-empty version must match the archive's recorded release.
func ReadBundle(ctx context.Context, location, version string) (Bundle, error) {
	if strings.HasPrefix(location, "https://") {
		data, err := Download(ctx, location, MaxDownload)
		if err != nil {
			return Bundle{}, err
		}
		return Verify(bytes.NewReader(data), version, Target())
	}
	info, err := os.Lstat(location)
	if err != nil || !info.Mode().IsRegular() || info.Size() > MaxDownload {
		return Bundle{}, operation.Fail(
			2,
			"release",
			"Bundle must be an existing bounded regular file or HTTPS URL",
		)
	}
	file, err := os.Open(location)
	if err != nil {
		return Bundle{}, err
	}
	defer func() { _ = file.Close() }()
	return Verify(file, version, Target())
}

// StagePlan previews staging b, bound to its archive digest and current state.
func StagePlan(c operation.Context, b Bundle) (operation.Plan, error) {
	plan := operation.Plan{
		Scope:    c.Scope,
		Source:   b.Identity(),
		Complete: true,
		Edits: []operation.Edit{
			{
				Path:        b.Directory(c),
				Action:      "stage",
				Description: "Verify and stage immutable CLI, machine sources and project policies; no activation or dependency acquisition",
			},
		},
		Inputs: []operation.Input{{Name: "archive", Digest: b.ArchiveDigest}},
	}
	state, err := operation.ReadState(c.Paths)
	if err != nil {
		return plan, err
	}
	raw, _ := json.Marshal(state)
	plan.Inputs = append(
		plan.Inputs,
		operation.Input{Name: "state", Digest: operation.SHA256Hex(raw)},
	)
	return plan, nil
}

// Stage extracts b into its release directory, or rechecks an existing one,
// records it as the candidate and returns the directory.
func Stage(c operation.Context, m *operation.Mutation, b Bundle) (string, error) {
	if err := m.Check(); err != nil {
		return "", err
	}
	directory := b.Directory(c)
	if err := PrivateDirectory(c.Paths.Data, filepath.Dir(directory), true); err != nil {
		return "", err
	}
	if _, err := os.Lstat(directory); err == nil {
		if err = b.CheckDirectory(directory); err != nil {
			return "", err
		}
		return directory, writeCandidate(c, m, b, directory)
	} else if !os.IsNotExist(err) {
		return "", err
	}
	parent := filepath.Dir(directory)
	if err := PrivateDirectory(c.Paths.Data, parent, true); err != nil {
		return "", err
	}
	id, err := operation.NewID()
	if err != nil {
		return "", err
	}
	temporary := filepath.Join(parent, ".stage-"+id)
	defer func() { _ = os.RemoveAll(temporary) }()
	if err = b.Extract(temporary); err != nil {
		return "", err
	}
	if err = os.Rename(temporary, directory); err != nil {
		return "", err
	}
	if err = syncDirectory(parent); err != nil {
		return "", err
	}
	return directory, writeCandidate(c, m, b, directory)
}

func writeCandidate(c operation.Context, m *operation.Mutation, b Bundle, directory string) error {
	record, _ := json.Marshal(
		candidateRecord{
			Directory:      directory,
			SHA256:         b.ArchiveDigest,
			Version:        b.Metadata.Release,
			MetadataSHA256: operation.SHA256Hex(b.Files["release.json"]),
		},
	)
	return m.WritePrivate(filepath.Join(c.Paths.State, "candidate.json"), record)
}

// Candidate is an offline selection; only pull writes this pointer. Active
// runtime selection remains in state.json and applied configuration is separate.
func Candidate(c operation.Context) (string, error) {
	record, _, err := readCandidate(c)
	if record == nil || err != nil {
		return "", err
	}
	return record.Directory, nil
}

// readCandidate strictly decodes and validates candidate.json, returning nil
// when no candidate is staged, plus the raw bytes that consent binds.
func readCandidate(c operation.Context) (*candidateRecord, []byte, error) {
	path := filepath.Join(c.Paths.State, "candidate.json")
	if _, err := os.Lstat(path); os.IsNotExist(err) {
		return nil, nil, nil
	}
	raw, err := operation.ReadPrivateInput(path, 1<<20)
	if err != nil {
		return nil, nil, err
	}
	invalid := operation.Fail(2, "candidate", "Invalid staged candidate record")
	var record candidateRecord
	if operation.DecodeStrict(raw, &record) != nil {
		return nil, nil, invalid
	}
	valid := operation.ValidDigest(record.SHA256) && operation.ValidDigest(record.MetadataSHA256) &&
		identifier.MatchString(record.Version)
	if !valid {
		return nil, nil, invalid
	}
	staged := filepath.Join(c.Paths.Data, "releases", record.Version+"-"+record.SHA256[:16])
	if record.Directory != staged {
		return nil, nil, invalid
	}
	return &record, raw, nil
}

// Inspect rechecks a staged release directory against its manifest and
// returns its metadata.
func Inspect(directory string) (Metadata, error) {
	var metadata Metadata
	info, err := os.Lstat(filepath.Join(directory, "release.json"))
	if err != nil || !info.Mode().IsRegular() || info.Size() > 1<<20 {
		return metadata, operation.Fail(2, "release", "Missing bounded release metadata")
	}
	raw, err := os.ReadFile(filepath.Join(directory, "release.json"))
	if err != nil {
		return metadata, err
	}
	unsupported := operation.Fail(2, "release", "Unsupported release metadata")
	if operation.DecodeStrict(raw, &metadata) != nil {
		return metadata, unsupported
	}
	if metadata.SchemaVersion != 1 || metadata.StateVersion != 1 || metadata.Target != Target() ||
		!identifier.MatchString(
			metadata.Release,
		) || !operation.ValidDigest(metadata.SourceDigest) ||
		len(metadata.Files) > maxFiles {
		return metadata, unsupported
	}
	var total int64
	for name, entry := range metadata.Files {
		if !member(name) || !allowed(name) || !operation.ValidDigest(entry.SHA256) ||
			entry.Size < 0 ||
			entry.Size > MaxDownload {
			return metadata, operation.Fail(2, "release", "Invalid release file manifest")
		}
		total += entry.Size
		if total > maxExpanded {
			return metadata, operation.Fail(2, "release", "Release exceeds expanded size bound")
		}
		file := filepath.Join(directory, name)
		if err = PrivateDirectory(directory, filepath.Dir(file), false); err != nil {
			return metadata, err
		}
		info, err = os.Lstat(file)
		if err != nil || !info.Mode().IsRegular() || info.Size() != entry.Size {
			return metadata, operation.Fail(4, "release_conflict", "Release file changed")
		}
		data, readErr := os.ReadFile(file)
		if readErr != nil || operation.SHA256Hex(data) != entry.SHA256 {
			return metadata, operation.Fail(4, "release_conflict", "Release content changed")
		}
	}
	for _, name := range []string{"bin/workbench", ".chezmoiroot", "home/.chezmoi.toml.tmpl", "licenses/NOTICE"} {
		if entry, ok := metadata.Files[name]; !ok || entry.Size == 0 {
			return metadata, operation.Fail(2, "release", "Required release payload missing")
		}
	}
	count := 0
	err = filepath.Walk(directory, func(full string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if info.IsDir() {
			return nil
		}
		name, relErr := filepath.Rel(directory, full)
		if relErr != nil {
			return relErr
		}
		if !info.Mode().IsRegular() {
			return operation.Fail(
				4,
				"release_conflict",
				"Release contains an unexpected link or special file",
			)
		}
		if name != "release.json" {
			if _, ok := metadata.Files[name]; !ok {
				return operation.Fail(4, "release_conflict", "Release contains an unexpected file")
			}
		}
		count++
		if count > maxFiles {
			return operation.Fail(2, "release", "Release file-count bound exceeded")
		}
		return nil
	})
	if err != nil {
		return metadata, err
	}
	return metadata, nil
}

type activationJournal struct {
	SchemaVersion int                      `json:"schema_version"`
	Status        string                   `json:"status"`
	Previous      *operation.ReleaseRecord `json:"previous"`
	Candidate     operation.ReleaseRecord  `json:"candidate"`
}

func readActivation(c operation.Context) (*activationJournal, error) {
	path := filepath.Join(c.Paths.State, "activation.json")
	if _, err := os.Lstat(path); os.IsNotExist(err) {
		return nil, nil
	}
	raw, err := operation.ReadPrivateInput(path, 1<<20)
	if err != nil {
		return nil, err
	}
	var journal activationJournal
	if err = operation.DecodeStrict(raw, &journal); err != nil || journal.SchemaVersion != 1 ||
		(journal.Status != "running" && journal.Status != "complete" && journal.Status != "failed") {
		return nil, operation.Fail(
			2,
			"activation",
			"Invalid current activation journal; left unchanged",
		)
	}
	if err = (operation.State{SchemaVersion: 1, ActiveRelease: &journal.Candidate}).Validate(); err != nil {
		return nil, err
	}
	if journal.Previous != nil {
		if err = (operation.State{SchemaVersion: 1, ActiveRelease: journal.Previous}).Validate(); err != nil {
			return nil, err
		}
	}
	return &journal, nil
}

// ValidateSelection fails closed after an interrupted selector update. The same
// install operation resumes it with explicit plan consent; no repair engine.
func ValidateSelection(c operation.Context) error {
	journal, err := readActivation(c)
	if err != nil {
		return err
	}
	if journal != nil && journal.Status == "running" {
		return operation.Fail(
			4,
			"activation_incomplete",
			"Runtime activation was interrupted; resume install with the same verified bundle and explicit consent",
		)
	}
	state, err := operation.ReadState(c.Paths)
	if err != nil || state == nil || state.ActiveRelease == nil {
		return err
	}
	entry, err := os.Readlink(filepath.Join(c.Paths.Bin, "workbench"))
	if err != nil || entry != state.ActiveRelease.Executable {
		return operation.Fail(
			4,
			"activation_incomplete",
			"Active runtime and command entry point differ; resume install with the verified bundle and explicit consent",
		)
	}
	if state.ActiveRelease.Executable != filepath.Join(
		state.ActiveRelease.Source,
		"bin",
		"workbench",
	) {
		return operation.Fail(
			2,
			"activation",
			"Active executable and source are not one matched release",
		)
	}
	return nil
}

// Activate only records a verified matched runtime. It never changes the last
// successfully applied configuration identity or removes a previous release.
func Activate(
	ctx context.Context,
	c operation.Context,
	m *operation.Mutation,
	b Bundle,
	directory string,
) error {
	if err := m.Check(); err != nil {
		return err
	}
	if err := b.CheckDirectory(directory); err != nil {
		return err
	}
	state, err := operation.ReadState(c.Paths)
	if err != nil {
		return err
	}
	if state == nil {
		state = &operation.State{SchemaVersion: 1}
	}
	executable := filepath.Join(directory, "bin", "workbench")
	// The child validates its compiled trust against the actual staged sources.
	if err = checkRuntime(ctx, c, executable, directory); err != nil {
		return err
	}
	entry := filepath.Join(c.Paths.Bin, "workbench")
	journal, err := readActivation(c)
	if err != nil {
		return err
	}
	previous, err := replacedRuntime(state, journal, executable, b.Identity(), entry)
	if err != nil {
		return err
	}
	if err = os.MkdirAll(c.Paths.Bin, 0o755); err != nil {
		return err
	}
	next := operation.ReleaseRecord{
		Identity:   b.Identity(),
		Executable: executable,
		Source:     directory,
	}
	journal = &activationJournal{
		SchemaVersion: 1,
		Status:        "running",
		Previous:      previous,
		Candidate:     next,
	}
	writeJournal := func() error {
		raw, _ := json.Marshal(journal)
		return m.WritePrivate(filepath.Join(c.Paths.State, "activation.json"), raw)
	}
	if err = writeJournal(); err != nil {
		return err
	}
	// Retain the old pointer until the replacement link is ready. The state is
	// written first; a failed link swap restores the previous state below.
	id, err := operation.NewID()
	if err != nil {
		return err
	}
	pending := filepath.Join(c.Paths.Bin, ".workbench-"+id)
	if err = os.Symlink(executable, pending); err != nil {
		return err
	}
	defer func() { _ = os.Remove(pending) }()
	// restore puts the previous runtime back in state after a failed step; if
	// even that fails, the journal still requires resuming this install.
	restore := func(cause error, unrestored string) error {
		state.ActiveRelease = previous
		if m.WriteState(*state) != nil {
			return operation.Fail(5, "activation_incomplete", unrestored)
		}
		journal.Status = "failed"
		_ = writeJournal()
		return cause
	}
	state.ActiveRelease = &next
	if err = m.WriteState(*state); err != nil {
		return restore(
			err,
			"Activation state could not be made durable or restored; resume the same install",
		)
	}
	if err = os.Rename(pending, entry); err != nil {
		return restore(
			err,
			"Entry-point activation failed and state restoration is incomplete; resume install",
		)
	}
	if err = syncDirectory(c.Paths.Bin); err != nil {
		return operation.Fail(
			5,
			"activation_incomplete",
			"Runtime selector changed but durability could not be confirmed; resume install",
		)
	}
	journal.Status = "complete"
	if err = writeJournal(); err != nil {
		return operation.Fail(
			5,
			"activation_incomplete",
			"Runtime selectors changed but completion journal could not be made durable; resume the same install",
		)
	}
	return ValidateSelection(c)
}

// replacedRuntime returns the runtime this activation replaces: the active one,
// or the one an interrupted activation of the same candidate recorded. It also
// checks that the entry point may be replaced.
func replacedRuntime(
	state *operation.State,
	journal *activationJournal,
	executable string,
	identity operation.SourceIdentity,
	entry string,
) (*operation.ReleaseRecord, error) {
	previous := state.ActiveRelease
	if journal != nil && journal.Status == "running" {
		if journal.Candidate.Executable != executable ||
			journal.Candidate.Identity != identity {
			return nil, operation.Fail(
				4,
				"activation_incomplete",
				"Resume the interrupted install with its original verified candidate",
			)
		}
		previous = journal.Previous
	}
	// Only an entry point recorded by this state may be replaced.
	known := []*operation.ReleaseRecord{state.ActiveRelease}
	if journal != nil && journal.Status == "running" {
		known = append(known, &journal.Candidate, previous)
	}
	return previous, checkEntryPoint(entry, known)
}

// checkEntryPoint allows a missing entry point, or a symlink to one of the
// known runtimes' executables; anything else is a command collision.
func checkEntryPoint(entry string, known []*operation.ReleaseRecord) error {
	info, err := os.Lstat(entry)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink == 0 {
		return operation.Fail(
			4,
			"command_collision",
			"An unrelated workbench command already occupies the installation path",
		)
	}
	link, err := os.Readlink(entry)
	recorded := slices.ContainsFunc(known, func(record *operation.ReleaseRecord) bool {
		return record != nil && link == record.Executable
	})
	if err != nil || !recorded {
		return operation.Fail(
			4,
			"command_collision",
			"Existing workbench entry point is not the recorded runtime",
		)
	}
	return nil
}

func runtimeEnvironment(c operation.Context) []string {
	return []string{
		"HOME=" + c.Home,
		"PATH=/usr/bin:/bin",
		"WORKBENCH_CONFIG_DIR=" + c.Paths.Config,
		"WORKBENCH_DATA_DIR=" + c.Paths.Data,
		"WORKBENCH_STATE_DIR=" + c.Paths.State,
		"WORKBENCH_CACHE_DIR=" + c.Paths.Cache,
		"WORKBENCH_BIN_DIR=" + c.Paths.Bin,
	}
}
