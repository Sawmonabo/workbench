package operation

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/Sawmonabo/workbench/internal/display"
	"golang.org/x/sys/unix"
)

const journalBytes = 64 << 10

// MaxScopeCheckpointBytes bounds all retained checkpoint storage in one scope.
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

// JournalStatus is where a checkpoint's operation stands.
type JournalStatus string

// Journal statuses.
const (
	JournalPrepared JournalStatus = "prepared" // Images are saved; no target is touched yet.
	JournalRunning  JournalStatus = "running"  // Targets are being written.
	JournalComplete JournalStatus = "complete" // Every target reached its image.
	JournalFailed   JournalStatus = "failed"   // Failed before changing any target.
	JournalPartial  JournalStatus = "partial"  // Some targets changed before a failure.
	JournalUnknown  JournalStatus = "unknown"  // A target's outcome could not be observed.
)

// targetOutcome is which image a target was last observed to hold.
type targetOutcome string

const (
	outcomeBefore  targetOutcome = "before"
	outcomeAfter   targetOutcome = "after"
	outcomeUnknown targetOutcome = "unknown"
)

type checkpointJournal struct {
	SchemaVersion      int                 `json:"schema_version"`
	Sequence           uint64              `json:"sequence"`
	OperationID        string              `json:"operation_id"`
	Direction          string              `json:"direction"`
	Status             JournalStatus       `json:"status"`
	Known              []targetOutcome     `json:"known"`
	RecoveryCreated    bool                `json:"recovery_created"`
	PostAttributes     []map[string][]byte `json:"post_attributes"`
	PairPost           []targetOutcome     `json:"pair_post"`
	ObservedAttributes []map[string][]byte `json:"observed_attributes"`
	// Updated is when the journal was last saved, so when its checkpoint last
	// wrote targets, forward or in recovery.
	Updated time.Time `json:"updated"`
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
	// regroup holds the targets preflight confirmed as what native wrote
	// before the pass after it, with what it observed (see confirm).
	regroup map[int]Image
}

// CheckpointSummary is the public view of one selectable checkpoint or its
// recovery pair.
type CheckpointSummary struct {
	ID       string          `json:"id"`
	PairedID string          `json:"paired_id"`
	Before   *SourceIdentity `json:"before"`
	Applied  *SourceIdentity `json:"applied"`
	Created  time.Time       `json:"created"`
	Status   JournalStatus   `json:"status"`
	Recovery bool            `json:"recovery"`
}

// ChangesDigest returns the SHA-256 of changes, which a plan binds as its
// checkpoint-images input.
func ChangesDigest(changes []TargetChange) string {
	data, _ := json.Marshal(changes)
	return SHA256Hex(data)
}

// CheckpointImages is the checkpoint-images input a plan binds for changes,
// once they pass the checks BeginCheckpoint makes of them, so planning
// refuses what applying the plan would refuse.
func CheckpointImages(c Context, changes []TargetChange) (Input, error) {
	if err := checkChanges(c, changes); err != nil {
		return Input{}, err
	}
	return Input{Name: "checkpoint-images", Digest: ChangesDigest(changes)}, nil
}

// checkChanges is validateChanges for changes not yet checkpointed, which
// also have to leave room in the journal (journalRoom).
func checkChanges(c Context, changes []TargetChange) error {
	if err := validateChanges(c, changes); err != nil {
		return err
	}
	return journalRoom(changes)
}

// journalRoom refuses changes whose journal would not fit in the journalBytes
// each of its two slots holds at its largest: every outcome unknown, a
// recovery pair recorded, and each target's attributes, recorded as observed
// and as written, those of its two images together, each value the longer.
func journalRoom(changes []TargetChange) error {
	largest := checkpointJournal{
		SchemaVersion:      1,
		Sequence:           math.MaxUint64,
		OperationID:        strings.Repeat("0", 36),
		Direction:          "forward",
		Status:             JournalComplete,
		Known:              make([]targetOutcome, len(changes)),
		RecoveryCreated:    true,
		PostAttributes:     make([]map[string][]byte, len(changes)),
		PairPost:           make([]targetOutcome, len(changes)),
		ObservedAttributes: make([]map[string][]byte, len(changes)),
		Updated:            time.Date(9999, 12, 31, 23, 59, 59, 999999999, time.UTC),
	}
	for i, change := range changes {
		attributes := maps.Clone(change.Before.Attributes)
		for name, value := range change.After.Attributes {
			if len(value) >= len(attributes[name]) {
				if attributes == nil {
					attributes = map[string][]byte{}
				}
				attributes[name] = value
			}
		}
		largest.Known[i], largest.PairPost[i] = outcomeUnknown, outcomeBefore
		largest.PostAttributes[i], largest.ObservedAttributes[i] = attributes, attributes
	}
	if envelope, err := encodeJournal(largest); err != nil || len(envelope) > journalBytes {
		return Fail(
			ExitBlocked,
			"checkpoint_limit",
			"Extended attributes exceed the "+display.Limit(journalBytes)+" recovery journal",
		)
	}
	return nil
}

// encodeJournal is j in its envelope, as a journal slot holds it.
func encodeJournal(j checkpointJournal) ([]byte, error) {
	data, err := json.Marshal(j)
	if err != nil {
		return nil, err
	}
	return json.Marshal(journalEnvelope{Digest: SHA256Hex(data), Journal: j})
}

func checkpointScope(c Context) string { return filepath.Join(c.Paths.State, checkpointFolder(c)) }

// checkpointFolder is the folder of the scope's checkpoints, named from the
// state folder.
func checkpointFolder(c Context) string { return filepath.Join("checkpoints", scopeDigest(c)) }

// scopeDigest names a scope's checkpoint directory and lock file.
func scopeDigest(c Context) string {
	return SHA256Hex([]byte(c.Scope.Kind + ":" + c.Scope.Root))
}

// ownFolderRefusal refuses a change to path, a folder that holds
// Workbench's own files, whose image is not a folder other users cannot
// write in: the image on disk, or the planned one when planned.
func ownFolderRefusal(path string, image Image, planned bool) error {
	shown := display.Path(path) + " holds Workbench's own files"
	var message string
	switch {
	case planned && image.Kind != ImageDirectory:
		message = shown + ", and the plan would replace or remove it, which would take those files with it"
	case planned:
		message = fmt.Sprintf(
			"%s, and the plan would give it mode %04o, which lets other users write in it",
			shown,
			image.Mode,
		)
	case image.Kind == ImageAbsent:
		message = display.Path(path) + ", where Workbench's own files go, does not exist"
	case image.Kind != ImageDirectory:
		message = shown + " but is " + imageKindName(image.Kind) + ", not a folder"
	default:
		message = fmt.Sprintf(
			"%s, and its mode %04o lets other users write in it",
			shown,
			image.Mode,
		)
		message += retry([]string{"chmod", "go-w"}, path)
	}
	return Fail(ExitBlocked, "checkpoint", message)
}

func validateChanges(c Context, changes []TargetChange) error {
	if len(changes) > MaxCheckpointTargets {
		return Fail(ExitBlocked, "checkpoint", "Checkpoint permits at most 256 explicit targets")
	}
	seen := map[string]bool{}
	total, attributes := 0, 0
	for _, change := range changes {
		if seen[change.Path] {
			return Fail(ExitInvalid, "checkpoint", "Duplicate checkpoint target")
		}
		seen[change.Path] = true
		if err := c.ValidateTarget(change.Path); err != nil {
			if c.ValidateContainer(change.Path) != nil {
				return err
			}
			// A directory holding Workbench's own files may change only its
			// mode, never to one other users can write: replacing or removing
			// it would take Workbench's state and the user's files with it.
			for i, image := range []Image{change.Before, change.After} {
				if image.Kind != ImageDirectory || image.Mode&0o022 != 0 {
					return ownFolderRefusal(change.Path, image, i == 1)
				}
			}
		}
		for _, image := range []Image{change.Before, change.After} {
			if err := image.validate(c, change.Path); err != nil {
				return err
			}
			if err := validateImageGroup(c, change.Path, image); err != nil {
				return err
			}
			total += len(image.Data) + len(image.Link) + len(image.ACL)
			for _, value := range image.Attributes {
				attributes += len(value)
			}
		}
		if change.Before.Kind == ImageDirectory && change.After.Kind != ImageAbsent &&
			change.After.Kind != ImageDirectory {
			shown := display.Path(change.Path)
			return Fail(
				ExitBlocked,
				"checkpoint",
				shown+" is a folder, and Workbench does not replace a folder with "+
					imageKindName(change.After.Kind)+"; move it aside and retry",
			)
		}
		if change.After.Kind == ImageDirectory && change.Before.Kind != ImageAbsent &&
			change.Before.Kind != ImageDirectory {
			return Fail(
				ExitBlocked,
				"checkpoint",
				display.Path(change.Path)+" is "+imageKindName(change.Before.Kind)+
					", and Workbench does not replace it with a folder; move it aside and retry",
			)
		}
	}
	if attributes > 16<<10 {
		return Fail(
			ExitBlocked,
			"checkpoint_limit",
			"Extended attributes exceed the reserved 16 KiB pair limit",
		)
	}
	if total > MaxCheckpointImageBytes {
		return Fail(
			ExitBlocked,
			"checkpoint_limit",
			"Pre/post images exceed the 32 MiB checkpoint limit",
		)
	}
	return nil
}

// BeginCheckpoint durably stores the exact before/after images of approved
// changes, with reserved journal and recovery space, before any target write.
// before is the configuration identity the checkpoint restores.
func BeginCheckpoint(
	m *Mutation,
	plan Plan,
	before *SourceIdentity,
	changes []TargetChange,
) (_ *Checkpoint, err error) {
	if err := m.Check(); err != nil {
		return nil, err
	}
	c := m.context
	if err := checkApproved(c, plan, before, changes); err != nil {
		return nil, err
	}
	if err := makeRoom(c, plan); err != nil {
		return nil, err
	}
	if _, err := PrivateDirectory(c.Paths.State, checkpointFolder(c)); err != nil {
		return nil, err
	}
	id, err := NewID()
	if err != nil {
		return nil, err
	}
	pair, err := NewID()
	if err != nil {
		return nil, err
	}
	cp := &Checkpoint{
		ID:        id,
		mutation:  m,
		directory: filepath.Join(checkpointScope(c), ".prepare-"+id),
		changes:   changes,
	}
	// A failed reservation holds no recovery data: no target was written
	// against it. Discard it so it cannot block later applies.
	defer func() {
		if err != nil {
			_ = discardPrepared(c, cp.directory)
		}
	}()
	cp.record = checkpointRecord{
		SchemaVersion: 1,
		ID:            id,
		RecoveryID:    pair,
		Scope:         c.Scope,
		Created:       time.Now().UTC(),
		Before:        before,
		Applied:       plan.Source,
		Effects:       plan.Effects,
	}
	cp.journal = checkpointJournal{
		SchemaVersion:      1,
		OperationID:        id,
		Direction:          "forward",
		Status:             JournalPrepared,
		Known:              make([]targetOutcome, len(changes)),
		PostAttributes:     make([]map[string][]byte, len(changes)),
		ObservedAttributes: make([]map[string][]byte, len(changes)),
	}
	for i, change := range changes {
		cp.journal.Known[i] = outcomeBefore
		cp.journal.PostAttributes[i] = change.After.Attributes
		cp.journal.ObservedAttributes[i] = change.Before.Attributes
		ref := imageReference{
			Path:   change.Path,
			Before: ImageDigest(change.Before),
			After:  ImageDigest(change.After),
		}
		cp.record.Targets = append(cp.record.Targets, ref)
		for _, image := range []Image{change.Before, change.After} {
			if err = cp.storeImage(image); err != nil {
				return nil, err
			}
		}
	}
	if err = cp.publish(); err != nil {
		return nil, err
	}
	return cp, nil
}

// discardPrepared removes a reservation directory that never published.
func discardPrepared(c Context, directory string) error {
	base := filepath.Base(directory)
	if !strings.HasPrefix(base, ".prepare-") {
		return nil
	}
	if _, err := os.Lstat(directory); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}
	return removeDirectory(
		directory,
		filepath.Dir(checkpointScope(c)),
		".removed-"+strings.TrimPrefix(base, ".prepare-"),
	)
}

// publish reserves journal space, writes the record and both
// journal slots, then renames the prepared directory into the scope durably.
func (cp *Checkpoint) publish() error {
	m := cp.mutation
	// Allocate bounded writable journal space before target writes so journal
	// updates are rewritten in place. Never delete these for capacity.
	for _, name := range []string{"journal-0.json", "journal-1.json"} {
		space := bytes.Repeat([]byte(" "), journalBytes)
		if err := m.WritePrivate(filepath.Join(cp.directory, name), space); err != nil {
			return err
		}
	}
	data, err := json.Marshal(cp.record)
	if err != nil {
		return err
	}
	if err = m.WritePrivate(filepath.Join(cp.directory, "checkpoint.json"), data); err != nil {
		return err
	}
	if err = cp.saveJournal(); err != nil {
		return err
	}
	if err = cp.saveJournal(); err != nil {
		return err
	}
	if _, err = checkpointStorageBytes(cp.directory); err != nil {
		return err
	}
	scope := checkpointScope(m.context)
	final := filepath.Join(scope, cp.ID)
	if err = os.Rename(cp.directory, final); err != nil {
		return err
	}
	cp.directory = final
	return SyncDirectory(scope)
}

// checkApproved verifies that changes are exactly the approved plan's images
// and that every target still holds its previewed before image.
func checkApproved(c Context, plan Plan, before *SourceIdentity, changes []TargetChange) error {
	if plan.Scope != c.Scope {
		return Fail(ExitConflict, "scope", "Checkpoint scope does not match held mutation")
	}
	if err := (State{SchemaVersion: 1, AppliedConfiguration: &plan.Source}).Validate(); err != nil {
		return err
	}
	if err := (State{SchemaVersion: 1, AppliedConfiguration: before}).Validate(); err != nil {
		return err
	}
	if err := checkChanges(c, changes); err != nil {
		return err
	}
	bound := slices.ContainsFunc(plan.Inputs, func(input Input) bool {
		return input.Name == "checkpoint-images" && input.Digest == ChangesDigest(changes)
	})
	if !bound {
		return Fail(
			ExitConflict,
			"plan",
			"Exact checkpoint images were not bound to the approved plan",
		)
	}
	for _, change := range changes {
		current, err := ReadImage(c, change.Path)
		if err != nil {
			return err
		}
		if !sameImage(current, change.Before) {
			shown := display.Path(change.Path)
			return Fail(
				ExitConflict,
				"conflict",
				shown+" changed after the plan was made, so nothing was written; plan again",
			)
		}
	}
	return preflightDirectories(c, changes, false)
}

// makeRoom discards leftover failed reservations, then removes the
// plan-approved oldest checkpoint at the limit. Incomplete checkpoints do not
// block: checkApproved already ties the new plan to the targets' current
// images, and the older checkpoint keeps its images and stays revertable.
func makeRoom(c Context, plan Plan) error {
	existing, err := loadCheckpoints(c)
	if err != nil {
		return err
	}
	entries, err := os.ReadDir(checkpointScope(c))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	// The held exclusive lock means no reservation is in progress, and a
	// .prepare- directory never had targets written against it.
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() && strings.HasPrefix(name, ".prepare-") {
			if err = discardPrepared(c, filepath.Join(checkpointScope(c), name)); err != nil {
				return err
			}
		}
	}
	if len(existing) >= MaxForwardCheckpoints {
		return pruneApproved(c, plan)
	}
	return nil
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
		if err = cp.mutation.WritePrivate(
			filepath.Join(directory, fmt.Sprintf("%02d.part", i)),
			data[i*(1<<20):end],
		); err != nil {
			return err
		}
	}
	metadata, _ := json.Marshal(manifest)
	return cp.mutation.WritePrivate(filepath.Join(directory, "image.json"), metadata)
}

func (cp *Checkpoint) saveJournal() error {
	if err := cp.mutation.Check(); err != nil {
		return err
	}
	cp.journal.Sequence++
	cp.journal.Updated = time.Now().UTC()
	envelope, err := encodeJournal(cp.journal)
	if err != nil {
		return err
	}
	if len(envelope) > journalBytes {
		return Fail(ExitInvalid, "checkpoint", "Recovery journal exceeded its reserved capacity")
	}
	if err = makeOwnFolder("permissions", cp.directory); err != nil {
		return err
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
