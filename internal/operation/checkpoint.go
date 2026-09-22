package operation

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

const journalBytes = 64 << 10
const MaxScopeCheckpointBytes = 1 << 30

type imageReference struct {
	Path   string `json:"path"`
	Before string `json:"before"`
	After  string `json:"after"`
}
type checkpointRecord struct {
	SchemaVersion int              `json:"schema_version"`
	ID            string           `json:"id"`
	RecoveryID    string           `json:"recovery_id"`
	Scope         Scope            `json:"scope"`
	Created       time.Time        `json:"created"`
	Before        *SourceIdentity  `json:"before"`
	Applied       SourceIdentity   `json:"applied"`
	Targets       []imageReference `json:"targets"`
	Effects       []Effect         `json:"effects"`
}
type checkpointJournal struct {
	SchemaVersion      int                 `json:"schema_version"`
	Sequence           uint64              `json:"sequence"`
	OperationID        string              `json:"operation_id"`
	Direction          string              `json:"direction"`
	Status             string              `json:"status"`
	Known              []string            `json:"known"`
	RecoveryCreated    bool                `json:"recovery_created"`
	PostAttributes     []map[string][]byte `json:"post_attributes"`
	PairPost           []string            `json:"pair_post"`
	ObservedAttributes []map[string][]byte `json:"observed_attributes"`
}
type journalEnvelope struct {
	Digest  string            `json:"digest"`
	Journal checkpointJournal `json:"journal"`
}

// Checkpoint is a held-lock capability; only summaries are safe for public output.
type Checkpoint struct {
	ID        string
	mutation  *Mutation
	record    checkpointRecord
	journal   checkpointJournal
	changes   []TargetChange
	directory string
}
type CheckpointSummary struct {
	ID       string          `json:"id"`
	PairedID string          `json:"paired_id"`
	Before   *SourceIdentity `json:"before"`
	Applied  *SourceIdentity `json:"applied"`
	Created  time.Time       `json:"created"`
	Status   string          `json:"status"`
	Recovery bool            `json:"recovery"`
}

func ChangesDigest(changes []TargetChange) string {
	data, _ := json.Marshal(changes)
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

func checkpointScope(c Context) string {
	digest := sha256.Sum256([]byte(c.Scope.Kind + ":" + c.Scope.Root))
	return filepath.Join(c.Paths.State, "checkpoints", hex.EncodeToString(digest[:]))
}

func validateChanges(c Context, changes []TargetChange) error {
	if len(changes) > MaxCheckpointTargets {
		return Fail(3, "checkpoint", "Checkpoint permits at most 256 explicit targets")
	}
	seen := map[string]bool{}
	total, attributes := 0, 0
	for _, change := range changes {
		if seen[change.Path] {
			return Fail(2, "checkpoint", "Duplicate checkpoint target")
		}
		seen[change.Path] = true
		if err := c.ValidateTarget(change.Path); err != nil {
			return err
		}
		for _, image := range []Image{change.Before, change.After} {
			if err := image.validate(c, change.Path); err != nil {
				return err
			}
			if err := validateImageGroup(c, change.Path, image); err != nil {
				return err
			}
			total += len(image.Data) + len(image.Link)
			for _, value := range image.Attributes {
				attributes += len(value)
			}
		}
		if change.Before.Kind == "directory" && change.After.Kind != "absent" && change.After.Kind != "directory" {
			return Fail(3, "checkpoint", "Existing directory replacement is unsupported")
		}
		if change.After.Kind == "directory" && change.Before.Kind != "absent" && change.Before.Kind != "directory" {
			return Fail(3, "checkpoint", "Directory replacement is unsupported")
		}
	}
	if attributes > 16<<10 {
		return Fail(3, "checkpoint_limit", "Extended attributes exceed the reserved 16 KiB pair limit")
	}
	if total > MaxCheckpointImageBytes {
		return Fail(3, "checkpoint_limit", "Pre/post images exceed the 32 MiB checkpoint limit")
	}
	return nil
}

func BeginCheckpoint(m *Mutation, plan Plan, before *SourceIdentity, changes []TargetChange) (*Checkpoint, error) {
	if err := m.Check(); err != nil {
		return nil, err
	}
	c := m.context
	if plan.Scope != c.Scope {
		return nil, Fail(4, "scope", "Checkpoint scope does not match held mutation")
	}
	if err := (State{SchemaVersion: 1, AppliedConfiguration: &plan.Source}).Validate(); err != nil {
		return nil, err
	}
	if err := (State{SchemaVersion: 1, AppliedConfiguration: before}).Validate(); err != nil {
		return nil, err
	}
	if err := validateChanges(c, changes); err != nil {
		return nil, err
	}
	bound := false
	for _, input := range plan.Inputs {
		if input.Name == "checkpoint-images" && input.Digest == ChangesDigest(changes) {
			bound = true
		}
	}
	if !bound {
		return nil, Fail(4, "plan", "Exact checkpoint images were not bound to the approved plan")
	}
	for _, change := range changes {
		current, err := ReadImage(c, change.Path)
		if err != nil {
			return nil, err
		}
		if !sameImage(current, change.Before) {
			return nil, Fail(4, "conflict", "Checkpoint target changed after preview")
		}
	}
	if err := preflightDirectories(c, changes, false); err != nil {
		return nil, err
	}
	existing, err := loadCheckpoints(c)
	if err != nil {
		return nil, err
	}
	if len(existing) >= MaxForwardCheckpoints {
		return nil, Fail(3, "checkpoint_limit", "Scope retains 20 forward checkpoints; further application is blocked, recovery remains available")
	}
	entries, readErr := os.ReadDir(checkpointScope(c))
	if readErr != nil && !os.IsNotExist(readErr) {
		return nil, readErr
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".prepare-") {
			return nil, Fail(3, "checkpoint", "A failed checkpoint reservation requires review before further application; retained checkpoints remain recoverable")
		}
	}
	for _, old := range existing {
		if old.journal.Status == "running" || old.journal.Status == "unknown" || old.journal.Status == "partial" {
			return nil, Fail(3, "recovery", "Resolve the retained incomplete checkpoint before further application")
		}
	}
	id, err := NewID()
	if err != nil {
		return nil, err
	}
	pair, err := NewID()
	if err != nil {
		return nil, err
	}
	cp := &Checkpoint{ID: id, mutation: m, directory: filepath.Join(checkpointScope(c), ".prepare-"+id), changes: changes}
	cp.record = checkpointRecord{SchemaVersion: 1, ID: id, RecoveryID: pair, Scope: c.Scope, Created: time.Now().UTC(), Before: before, Applied: plan.Source, Effects: plan.Effects}
	cp.journal = checkpointJournal{SchemaVersion: 1, OperationID: id, Direction: "forward", Status: "prepared", Known: make([]string, len(changes)), PostAttributes: make([]map[string][]byte, len(changes)), ObservedAttributes: make([]map[string][]byte, len(changes))}
	for i, change := range changes {
		cp.journal.Known[i] = "before"
		cp.journal.PostAttributes[i] = change.After.Attributes
		cp.journal.ObservedAttributes[i] = change.Before.Attributes
		ref := imageReference{Path: change.Path, Before: ImageDigest(change.Before), After: ImageDigest(change.After)}
		cp.record.Targets = append(cp.record.Targets, ref)
		for _, image := range []Image{change.Before, change.After} {
			if err = cp.storeImage(image); err != nil {
				return nil, err
			}
		}
	}
	// Allocate bounded writable journal space and two independent metadata
	// replacement reserves before target writes. Never delete these for capacity.
	for _, name := range []string{"journal-0.json", "journal-1.json", "recovery-reserve-0", "recovery-reserve-1"} {
		size := journalBytes
		if name[0] == 'r' {
			size = 1 << 20
		}
		if err = m.WritePrivate(filepath.Join(cp.directory, name), bytes.Repeat([]byte(" "), size)); err != nil {
			return nil, err
		}
	}
	data, err := json.Marshal(cp.record)
	if err != nil {
		return nil, err
	}
	if err = m.WritePrivate(filepath.Join(cp.directory, "checkpoint.json"), data); err != nil {
		return nil, err
	}
	if err = cp.saveJournal(); err != nil {
		return nil, err
	}
	if err = cp.saveJournal(); err != nil {
		return nil, err
	}
	final := filepath.Join(checkpointScope(c), id)
	if _, err = checkpointStorageBytes(cp.directory); err != nil {
		return nil, err
	}
	if err = os.Rename(cp.directory, final); err != nil {
		return nil, err
	}
	cp.directory = final
	directory, err := os.Open(checkpointScope(c))
	if err != nil {
		return nil, err
	}
	defer func() { _ = directory.Close() }()
	if err = directory.Sync(); err != nil {
		return nil, err
	}
	return cp, nil
}

type imageManifest struct {
	Size   int `json:"size"`
	Chunks int `json:"chunks"`
}

func (cp *Checkpoint) storeImage(image Image) error {
	data, err := json.Marshal(image)
	if err != nil {
		return err
	}
	directory := filepath.Join(cp.directory, "images", ImageDigest(image))
	manifest := imageManifest{Size: len(data), Chunks: (len(data) + (1 << 20) - 1) / (1 << 20)}
	for i := range manifest.Chunks {
		end := min((i+1)*(1<<20), len(data))
		if err = cp.mutation.WritePrivate(filepath.Join(directory, fmt.Sprintf("%02d.part", i)), data[i*(1<<20):end]); err != nil {
			return err
		}
	}
	metadata, _ := json.Marshal(manifest)
	return cp.mutation.WritePrivate(filepath.Join(directory, "image.json"), metadata)
}

func readStoredImage(directory, digest string) (Image, error) {
	directory = filepath.Join(directory, "images", digest)
	var manifest imageManifest
	if err := decodePrivate(filepath.Join(directory, "image.json"), 1024, &manifest); err != nil {
		return Image{}, err
	}
	if manifest.Size < 1 || manifest.Size > MaxImageBytes*2 || manifest.Chunks != (manifest.Size+(1<<20)-1)/(1<<20) {
		return Image{}, Fail(2, "checkpoint_format", "Invalid image storage bounds")
	}
	data := make([]byte, 0, manifest.Size)
	for i := range manifest.Chunks {
		part, err := ReadPrivateInput(filepath.Join(directory, fmt.Sprintf("%02d.part", i)), 1<<20)
		if err != nil {
			return Image{}, err
		}
		if len(part) != min(1<<20, manifest.Size-i*(1<<20)) {
			return Image{}, Fail(2, "checkpoint_format", "Incomplete checkpoint image")
		}
		data = append(data, part...)
	}
	if err := validateStateJSON(json.NewDecoder(bytes.NewReader(data))); err != nil {
		return Image{}, err
	}
	var image Image
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&image); err != nil {
		return Image{}, Fail(2, "checkpoint_format", "Malformed checkpoint image")
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return Image{}, Fail(2, "checkpoint_format", "Malformed checkpoint image tail")
	}
	return image, nil
}

func (cp *Checkpoint) saveJournal() error {
	if err := cp.mutation.Check(); err != nil {
		return err
	}
	cp.journal.Sequence++
	data, _ := json.Marshal(cp.journal)
	digest := sha256.Sum256(data)
	envelope, err := json.Marshal(journalEnvelope{Digest: hex.EncodeToString(digest[:]), Journal: cp.journal})
	if err != nil {
		return err
	}
	if len(envelope) > journalBytes {
		return Fail(2, "checkpoint", "Recovery journal exceeded its reserved capacity")
	}
	path := filepath.Join(cp.directory, fmt.Sprintf("journal-%d.json", cp.journal.Sequence%2))
	if err = checkPrivateFile(path); err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_WRONLY|unix.O_NOFOLLOW, 0)
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()
	envelope = append(envelope, bytes.Repeat([]byte(" "), journalBytes-len(envelope))...)
	if _, err = file.WriteAt(envelope, 0); err != nil {
		return err
	}
	return file.Sync()
}

// StartNative persists uncertainty before handing authority to the native engine.
// An abrupt process death cannot make unobserved writes look safely reversible.
func (cp *Checkpoint) StartNative() error {
	cp.journal.Status = "running"
	for i := range cp.journal.Known {
		cp.journal.Known[i] = "unknown"
	}
	return cp.saveJournal()
}

// FinalizeNative restores only the approved group after native atomic creation
// inherited its temporary directory's group. Every target's expected content,
// type and mode is checked first; unknown content never authorizes metadata edits.
// It is called only following successful native execution, not interruption.
func (cp *Checkpoint) FinalizeNative() error {
	if err := cp.mutation.Check(); err != nil {
		return err
	}
	c := cp.mutation.context
	observed := make([]Image, len(cp.changes))
	for i, change := range cp.changes {
		current, err := ReadImage(c, change.Path)
		if err != nil {
			return err
		}
		comparison := current
		comparison.Group = change.After.Group
		if !sameImageContent(comparison, change.After) {
			return Fail(5, "native_image", "Native output differs from the approved images; metadata was not changed")
		}
		if current.Kind == "symlink" && *current.Group != *change.After.Group {
			return Fail(3, "metadata", "Native link group correction is unqualified; retained images require review")
		}
		observed[i] = current
	}
	for i, change := range cp.changes {
		if change.After.Kind == "absent" || *observed[i].Group == *change.After.Group {
			continue
		}
		current, err := ReadImage(c, change.Path)
		if err != nil {
			return err
		}
		if !sameImage(current, observed[i]) {
			return Fail(5, "native_image", "Native target changed before group preservation; stop for review")
		}
		if err = enforceNativeGroup(c, change.Path, observed[i], *change.After.Group); err != nil {
			return err
		}
	}
	return nil
}

func (cp *Checkpoint) Finish(runErr error) error {
	changed, unknown, incomplete := false, false, false
	for i, change := range cp.changes {
		current, err := ReadImage(cp.mutation.context, change.Path)
		if err == nil {
			cp.journal.ObservedAttributes[i] = current.Attributes
		}
		switch {
		case err == nil && sameImageContent(current, change.After):
			cp.journal.Known[i] = "after"
			cp.journal.PostAttributes[i] = current.Attributes
			cp.changes[i].After.Attributes = current.Attributes
			changed = changed || !sameImage(change.Before, current)
		case err == nil && sameImage(current, change.Before):
			cp.journal.Known[i] = "before"
			incomplete = incomplete || !sameImage(change.Before, change.After)
		default:
			cp.journal.Known[i] = "unknown"
			unknown = true
		}
	}
	if err := validateChanges(cp.mutation.context, cp.changes); err != nil {
		unknown = true
	}
	if incomplete && runErr == nil {
		runErr = Fail(1, "incomplete", "Native apply did not produce every approved target image")
	}
	cp.journal.Status = "complete"
	if runErr != nil {
		cp.journal.Status = "failed"
	}
	if changed && runErr != nil {
		cp.journal.Status = "partial"
	}
	if unknown {
		cp.journal.Status = "unknown"
	}
	if err := cp.saveJournal(); err != nil {
		if errors.Is(runErr, context.Canceled) {
			return runErr
		}
		return Fail(5, "checkpoint", "Target outcome could not be recorded; retained images require reviewed reconciliation")
	}
	if errors.Is(runErr, context.Canceled) {
		return runErr
	}
	if unknown {
		return Fail(5, "recovery", "Unrecognized target outcomes require reviewed reconciliation; original images retained")
	}
	if changed && runErr != nil {
		if errors.Is(runErr, context.Canceled) {
			return runErr
		}
		return Fail(5, "partial", "Some target changes completed; recover using the retained checkpoint")
	}
	return runErr
}

// Apply is the shared exact-write owner for project configuration and recovery.
func (cp *Checkpoint) Apply(ctx context.Context) error { return cp.applyImages(ctx, false) }

func (cp *Checkpoint) applyImages(ctx context.Context, reverse bool) error {
	if err := cp.mutation.Check(); err != nil {
		return err
	}
	if err := cp.preflight(reverse); err != nil {
		return err
	}
	cp.journal.Status = "running"
	if err := cp.saveJournal(); err != nil {
		return err
	}
	indices := make([]int, len(cp.changes))
	for i := range indices {
		indices[i] = i
	}
	// Creation goes parent-first, removal child-first; no recursive deletion.
	slices.SortFunc(indices, func(a, b int) int {
		x, y := cp.changes[a], cp.changes[b]
		xImage, yImage := x.After, y.After
		if reverse {
			xImage, yImage = x.Before, y.Before
		}
		if xImage.Kind == "absent" && yImage.Kind != "absent" {
			return 1
		}
		if xImage.Kind != "absent" && yImage.Kind == "absent" {
			return -1
		}
		if xImage.Kind == "absent" {
			return stringsCompare(y.Path, x.Path)
		}
		return stringsCompare(x.Path, y.Path)
	})
	for _, i := range indices {
		if err := ctx.Err(); err != nil {
			cp.journal.Status = "partial"
			_ = cp.saveJournal()
			return err
		}
		change := cp.changes[i]
		expected := cp.expectedImage(i)
		desired, known := change.After, "after"
		if reverse {
			desired, known = change.Before, "before"
		}
		if sameImage(expected, desired) {
			cp.journal.Known[i] = known
			continue
		}
		cp.journal.Known[i] = "unknown"
		if err := cp.saveJournal(); err != nil {
			return err
		}
		if err := writeImage(cp.mutation.context, change.Path, expected, desired); err != nil {
			cp.journal.Status = "partial"
			_ = cp.saveJournal()
			return Fail(5, "partial", "A target write did not complete; retained checkpoint requires reconciliation before retry")
		}
		observed, err := ReadImage(cp.mutation.context, change.Path)
		if err != nil || !sameImageContent(observed, desired) {
			cp.journal.Status = "unknown"
			_ = cp.saveJournal()
			return Fail(5, "partial", "Target outcome requires reconciliation; retained images were preserved")
		}
		cp.journal.ObservedAttributes[i] = observed.Attributes
		if !reverse && !cp.journal.RecoveryCreated {
			cp.journal.PostAttributes[i] = observed.Attributes
			cp.changes[i].After.Attributes = observed.Attributes
		}
		cp.journal.Known[i] = known
		if err := cp.saveJournal(); err != nil {
			return Fail(5, "checkpoint", "A target changed but journal update failed; recovery images are retained")
		}
	}
	cp.journal.Status = "complete"
	if err := cp.saveJournal(); err != nil {
		return Fail(5, "checkpoint", "Target changes completed but journal finalization failed; retained images remain available")
	}
	return nil
}

func stringsCompare(a, b string) int {
	if a < b {
		return -1
	}
	if a > b {
		return 1
	}
	return 0
}

func decodePrivate(path string, limit int64, value any) error {
	data, err := ReadPrivateInput(path, limit)
	if err != nil {
		return err
	}
	if err = validateStateJSON(json.NewDecoder(bytes.NewReader(data))); err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(value); err != nil {
		return Fail(2, "checkpoint_format", "Malformed checkpoint; no repair or deletion attempted")
	}
	if err = decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return Fail(2, "checkpoint_format", "Checkpoint must contain exactly one JSON value")
	}
	return nil
}

func loadCheckpoints(c Context) ([]*Checkpoint, error) {
	if err := c.Scope.Validate(); err != nil {
		return nil, err
	}
	directory := checkpointScope(c)
	if err := safeParents(directory); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(directory)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if len(entries) > MaxForwardCheckpoints+1 {
		return nil, Fail(2, "checkpoint_format", "Scope checkpoint count exceeds its supported bound")
	}
	var result []*Checkpoint
	var total int64
	for _, entry := range entries {
		if entry.IsDir() && strings.HasPrefix(entry.Name(), ".prepare-") && operationID.MatchString(strings.TrimPrefix(entry.Name(), ".prepare-")) {
			continue
		}
		if !entry.IsDir() || !operationID.MatchString(entry.Name()) {
			return nil, Fail(2, "checkpoint_format", "Unexpected checkpoint entry; no cleanup attempted")
		}
		cp, loadErr := loadCheckpoint(c, filepath.Join(directory, entry.Name()))
		if loadErr != nil {
			return nil, loadErr
		}
		result = append(result, cp)
		stored, sizeErr := checkpointStorageBytes(cp.directory)
		if sizeErr != nil {
			return nil, sizeErr
		}
		total += stored
	}
	if len(result) > MaxForwardCheckpoints || total > MaxScopeCheckpointBytes {
		return nil, Fail(2, "checkpoint_limit", "Retained scope storage exceeds its supported count or byte bound")
	}
	return result, nil
}

func checkpointStorageBytes(directory string) (int64, error) {
	var size int64
	entries := 0
	err := filepath.WalkDir(directory, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		entries++
		if entries > 4096 {
			return Fail(2, "checkpoint_limit", "Checkpoint file count exceeds its supported bound")
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if info.Mode().Perm() != 0700 {
				return Fail(2, "permissions", "Checkpoint directories require mode 0700")
			}
			return owned(info)
		}
		if err = checkPrivateFile(path); err != nil {
			return err
		}
		size += info.Size()
		if size > 50<<20 {
			return Fail(2, "checkpoint_limit", "Checkpoint exceeds its 50 MiB serialized storage bound")
		}
		return nil
	})
	return size, err
}

func loadCheckpoint(c Context, directory string) (*Checkpoint, error) {
	cp := &Checkpoint{directory: directory}
	if err := decodePrivate(filepath.Join(directory, "checkpoint.json"), 1<<20, &cp.record); err != nil {
		return nil, err
	}
	r := cp.record
	if r.SchemaVersion != 1 || r.Scope != c.Scope || !operationID.MatchString(r.ID) || r.ID != filepath.Base(directory) || !operationID.MatchString(r.RecoveryID) || r.ID == r.RecoveryID || r.Created.IsZero() {
		return nil, Fail(2, "checkpoint_format", "Unsupported checkpoint identity or scope")
	}
	if err := (State{SchemaVersion: 1, AppliedConfiguration: &r.Applied}).Validate(); err != nil {
		return nil, err
	}
	if err := (State{SchemaVersion: 1, AppliedConfiguration: r.Before}).Validate(); err != nil {
		return nil, err
	}
	if len(r.Targets) > MaxCheckpointTargets {
		return nil, Fail(2, "checkpoint_format", "Invalid checkpoint target count")
	}
	total := 0
	for _, ref := range r.Targets {
		change := TargetChange{Path: ref.Path}
		for index, digest := range []string{ref.Before, ref.After} {
			decoded, err := hex.DecodeString(digest)
			if err != nil || len(decoded) != sha256.Size {
				return nil, Fail(2, "checkpoint_format", "Invalid checkpoint image reference")
			}
			image, err := readStoredImage(directory, digest)
			if err != nil {
				return nil, err
			}
			if ImageDigest(image) != digest {
				return nil, Fail(2, "checkpoint_format", "Checkpoint image integrity mismatch")
			}
			total += len(image.Data) + len(image.Link)
			if total > MaxCheckpointImageBytes {
				return nil, Fail(2, "checkpoint_limit", "Checkpoint images exceed their aggregate read bound")
			}
			if index == 0 {
				change.Before = image
			} else {
				change.After = image
			}
		}
		cp.changes = append(cp.changes, change)
	}
	if err := validateChanges(c, cp.changes); err != nil {
		return nil, err
	}
	for i := range 2 {
		var envelope journalEnvelope
		if err := decodePrivate(filepath.Join(directory, fmt.Sprintf("journal-%d.json", i)), journalBytes, &envelope); err != nil {
			return nil, err
		}
		data, _ := json.Marshal(envelope.Journal)
		digest := sha256.Sum256(data)
		j := envelope.Journal
		if envelope.Digest != hex.EncodeToString(digest[:]) || j.SchemaVersion != 1 || !operationID.MatchString(j.OperationID) || j.Sequence == 0 || len(j.Known) != len(cp.changes) || len(j.PostAttributes) != len(cp.changes) || len(j.ObservedAttributes) != len(cp.changes) || (j.Direction != "forward" && j.Direction != "reverse") || !slices.Contains([]string{"prepared", "running", "complete", "partial", "failed", "unknown"}, j.Status) {
			return nil, Fail(2, "checkpoint_format", "Invalid recovery journal; retained images require review")
		}
		for _, known := range j.Known {
			if !slices.Contains([]string{"before", "after", "unknown"}, known) {
				return nil, Fail(2, "checkpoint_format", "Invalid target outcome")
			}
		}
		if j.RecoveryCreated {
			if len(j.PairPost) != len(cp.changes) {
				return nil, Fail(2, "checkpoint_format", "Missing paired recovery image selection")
			}
			for _, known := range j.PairPost {
				if known != "before" && known != "after" {
					return nil, Fail(2, "checkpoint_format", "Invalid paired recovery image selection")
				}
			}
		} else if len(j.PairPost) != 0 {
			return nil, Fail(2, "checkpoint_format", "Unexpected paired recovery image selection")
		}
		if j.Sequence > cp.journal.Sequence {
			cp.journal = j
		}
	}
	for i := range cp.changes {
		cp.changes[i].After.Attributes = cp.journal.PostAttributes[i]
		if cp.journal.RecoveryCreated && cp.journal.PairPost[i] == "before" {
			cp.changes[i].After = cp.changes[i].Before
		}
	}
	if err := validateChanges(c, cp.changes); err != nil {
		return nil, err
	}
	for i, change := range cp.changes {
		observed := cp.expectedImage(i)
		if err := observed.validate(c, change.Path); err != nil {
			return nil, err
		}
	}
	cp.ID = r.ID
	return cp, nil
}

func (cp *Checkpoint) expectedImage(index int) Image {
	image := cp.changes[index].Before
	if cp.journal.Known[index] == "after" {
		image = cp.changes[index].After
	}
	image.Attributes = cp.journal.ObservedAttributes[index]
	return image
}

func ListCheckpoints(c Context) ([]CheckpointSummary, error) {
	checkpoints, err := loadCheckpoints(c)
	if err != nil {
		return nil, err
	}
	result := make([]CheckpointSummary, 0, len(checkpoints)*2)
	for _, cp := range checkpoints {
		r := cp.record
		result = append(result, CheckpointSummary{ID: r.ID, PairedID: r.RecoveryID, Before: r.Before, Applied: &r.Applied, Created: r.Created, Status: cp.journal.Status})
		if cp.journal.RecoveryCreated {
			result = append(result, CheckpointSummary{ID: r.RecoveryID, PairedID: r.ID, Before: &r.Applied, Applied: r.Before, Created: r.Created, Status: cp.journal.Status, Recovery: true})
		}
	}
	slices.SortFunc(result, func(a, b CheckpointSummary) int { return a.Created.Compare(b.Created) })
	return result, nil
}
