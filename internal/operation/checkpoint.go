package operation

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

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

func checkpointScope(c Context) string {
	return filepath.Join(c.Paths.State, "checkpoints", scopeDigest(c))
}

// scopeDigest names a scope's checkpoint directory and lock file.
func scopeDigest(c Context) string {
	return SHA256Hex([]byte(c.Scope.Kind + ":" + c.Scope.Root))
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
		if change.Before.Kind == ImageDirectory && change.After.Kind != ImageAbsent &&
			change.After.Kind != ImageDirectory {
			return Fail(ExitBlocked, "checkpoint", "Existing directory replacement is unsupported")
		}
		if change.After.Kind == ImageDirectory && change.Before.Kind != ImageAbsent &&
			change.Before.Kind != ImageDirectory {
			return Fail(ExitBlocked, "checkpoint", "Directory replacement is unsupported")
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
) (*Checkpoint, error) {
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

// publish reserves journal and recovery space, writes the record and both
// journal slots, then renames the prepared directory into the scope durably.
func (cp *Checkpoint) publish() error {
	m := cp.mutation
	// Allocate bounded writable journal space and two independent metadata
	// replacement reserves before target writes. Never delete these for capacity.
	reserves := []string{
		"journal-0.json",
		"journal-1.json",
		"recovery-reserve-0",
		"recovery-reserve-1",
	}
	for _, name := range reserves {
		size := journalBytes
		if name[0] == 'r' {
			size = 1 << 20
		}
		space := bytes.Repeat([]byte(" "), size)
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
	directory, err := os.Open(scope)
	if err != nil {
		return err
	}
	defer func() { _ = directory.Close() }()
	return directory.Sync()
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
	if err := validateChanges(c, changes); err != nil {
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
			return Fail(ExitConflict, "conflict", "Checkpoint target changed after preview")
		}
	}
	return preflightDirectories(c, changes, false)
}

// makeRoom refuses while a reservation or an incomplete checkpoint needs
// review, then removes the plan-approved oldest checkpoint at the limit.
func makeRoom(c Context, plan Plan) error {
	existing, err := loadCheckpoints(c)
	if err != nil {
		return err
	}
	entries, err := os.ReadDir(checkpointScope(c))
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".prepare-") {
			return Fail(
				ExitBlocked,
				"checkpoint",
				"A failed checkpoint reservation requires review before further application; retained checkpoints remain recoverable",
			)
		}
	}
	for _, old := range existing {
		if slices.Contains(
			[]JournalStatus{JournalRunning, JournalUnknown, JournalPartial},
			old.journal.Status,
		) {
			return Fail(
				ExitBlocked,
				"recovery",
				"Resolve the retained incomplete checkpoint before further application",
			)
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

func readStoredImage(directory, digest string) (Image, error) {
	directory = filepath.Join(directory, "images", digest)
	var manifest imageManifest
	if err := decodePrivate(filepath.Join(directory, "image.json"), 1024, &manifest); err != nil {
		return Image{}, err
	}
	if manifest.Size < 1 || manifest.Size > MaxImageBytes*2 ||
		manifest.Chunks != (manifest.Size+(1<<20)-1)/(1<<20) {
		return Image{}, Fail(ExitInvalid, "checkpoint_format", "Invalid image storage bounds")
	}
	data := make([]byte, 0, manifest.Size)
	for i := range manifest.Chunks {
		part, err := ReadPrivateInput(filepath.Join(directory, fmt.Sprintf("%02d.part", i)), 1<<20)
		if err != nil {
			return Image{}, err
		}
		if len(part) != min(1<<20, manifest.Size-i*(1<<20)) {
			return Image{}, Fail(ExitInvalid, "checkpoint_format", "Incomplete checkpoint image")
		}
		data = append(data, part...)
	}
	var image Image
	if err := DecodeStrict(data, &image); err != nil {
		return Image{}, Fail(
			ExitInvalid,
			"checkpoint_format",
			"Malformed checkpoint image ("+err.Error()+")",
		)
	}
	return image, nil
}

func (cp *Checkpoint) saveJournal() error {
	if err := cp.mutation.Check(); err != nil {
		return err
	}
	cp.journal.Sequence++
	data, _ := json.Marshal(cp.journal)
	envelope, err := json.Marshal(
		journalEnvelope{Digest: SHA256Hex(data), Journal: cp.journal},
	)
	if err != nil {
		return err
	}
	if len(envelope) > journalBytes {
		return Fail(ExitInvalid, "checkpoint", "Recovery journal exceeded its reserved capacity")
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
	cp.journal.Status = JournalRunning
	for i := range cp.journal.Known {
		cp.journal.Known[i] = outcomeUnknown
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
			return Fail(
				ExitPartial,
				"native_image",
				"Native output differs from the approved images; metadata was not changed",
			)
		}
		if current.Kind == ImageSymlink && *current.Group != *change.After.Group {
			return Fail(
				ExitBlocked,
				"metadata",
				"Native link group correction is unqualified; retained images require review",
			)
		}
		observed[i] = current
	}
	for i, change := range cp.changes {
		if change.After.Kind == ImageAbsent || *observed[i].Group == *change.After.Group {
			continue
		}
		current, err := ReadImage(c, change.Path)
		if err != nil {
			return err
		}
		if !sameImage(current, observed[i]) {
			return Fail(
				ExitPartial,
				"native_image",
				"Native target changed before group preservation; stop for review",
			)
		}
		if err = enforceNativeGroup(c, change.Path, observed[i], *change.After.Group); err != nil {
			return err
		}
	}
	return nil
}

// Finish records each target's observed outcome after a native run and
// returns runErr, or a partial or unknown-outcome error when targets changed.
func (cp *Checkpoint) Finish(runErr error) error {
	changed, unknown, incomplete := false, false, false
	for i, change := range cp.changes {
		current, err := ReadImage(cp.mutation.context, change.Path)
		if err == nil {
			cp.journal.ObservedAttributes[i] = current.Attributes
		}
		switch {
		case err == nil && sameImageContent(current, change.After):
			cp.journal.Known[i] = outcomeAfter
			cp.journal.PostAttributes[i] = current.Attributes
			cp.changes[i].After.Attributes = current.Attributes
			changed = changed || !sameImage(change.Before, current)
		case err == nil && sameImage(current, change.Before):
			cp.journal.Known[i] = outcomeBefore
			incomplete = incomplete || !sameImage(change.Before, change.After)
		default:
			cp.journal.Known[i] = outcomeUnknown
			unknown = true
		}
	}
	if err := validateChanges(cp.mutation.context, cp.changes); err != nil {
		unknown = true
	}
	if incomplete && runErr == nil {
		runErr = Fail(
			ExitFailed,
			"incomplete",
			"Native apply did not produce every approved target image",
		)
	}
	cp.journal.Status = JournalComplete
	if runErr != nil {
		cp.journal.Status = JournalFailed
	}
	if changed && runErr != nil {
		cp.journal.Status = JournalPartial
	}
	if unknown {
		cp.journal.Status = JournalUnknown
	}
	if err := cp.saveJournal(); err != nil {
		if errors.Is(runErr, context.Canceled) {
			return runErr
		}
		return Fail(
			ExitPartial,
			"checkpoint",
			"Target outcome could not be recorded; retained images require reviewed reconciliation",
		)
	}
	if errors.Is(runErr, context.Canceled) {
		return runErr
	}
	if unknown {
		return Fail(
			ExitPartial,
			"recovery",
			"Unrecognized target outcomes require reviewed reconciliation; original images retained",
		)
	}
	if changed && runErr != nil {
		return Fail(
			ExitPartial,
			"partial",
			"Some target changes completed; recover using the retained checkpoint",
		)
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
	cp.journal.Status = JournalRunning
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
		if xImage.Kind == ImageAbsent && yImage.Kind != ImageAbsent {
			return 1
		}
		if xImage.Kind != ImageAbsent && yImage.Kind == ImageAbsent {
			return -1
		}
		if xImage.Kind == ImageAbsent {
			return strings.Compare(y.Path, x.Path)
		}
		return strings.Compare(x.Path, y.Path)
	})
	for _, i := range indices {
		if err := ctx.Err(); err != nil {
			cp.journal.Status = JournalPartial
			_ = cp.saveJournal()
			return err
		}
		change := cp.changes[i]
		expected := cp.expectedImage(i)
		desired, known := change.After, outcomeAfter
		if reverse {
			desired, known = change.Before, outcomeBefore
		}
		if sameImage(expected, desired) {
			cp.journal.Known[i] = known
			continue
		}
		cp.journal.Known[i] = outcomeUnknown
		if err := cp.saveJournal(); err != nil {
			return err
		}
		if err := writeImage(cp.mutation.context, change.Path, expected, desired); err != nil {
			cp.journal.Status = JournalPartial
			_ = cp.saveJournal()
			return Fail(
				ExitPartial,
				"partial",
				"A target write did not complete; retained checkpoint requires reconciliation before retry",
			)
		}
		observed, err := ReadImage(cp.mutation.context, change.Path)
		if err != nil || !sameImageContent(observed, desired) {
			cp.journal.Status = JournalUnknown
			_ = cp.saveJournal()
			return Fail(
				ExitPartial,
				"partial",
				"Target outcome requires reconciliation; retained images were preserved",
			)
		}
		cp.journal.ObservedAttributes[i] = observed.Attributes
		if !reverse && !cp.journal.RecoveryCreated {
			cp.journal.PostAttributes[i] = observed.Attributes
			cp.changes[i].After.Attributes = observed.Attributes
		}
		cp.journal.Known[i] = known
		if err := cp.saveJournal(); err != nil {
			return Fail(
				ExitPartial,
				"checkpoint",
				"A target changed but journal update failed; recovery images are retained",
			)
		}
	}
	cp.journal.Status = JournalComplete
	if err := cp.saveJournal(); err != nil {
		return Fail(
			ExitPartial,
			"checkpoint",
			"Target changes completed but journal finalization failed; retained images remain available",
		)
	}
	return nil
}

// RetentionEffect names the checkpoint that a new forward checkpoint replaces
// once the scope holds MaxForwardCheckpoints: the oldest settled one. Plans
// list it so consent covers the removal. Running, partial and unknown
// checkpoints and the recorded partial operation are never candidates.
func RetentionEffect(c Context) (*Effect, error) {
	oldest, err := retentionCandidate(c)
	if oldest == nil || err != nil {
		return nil, err
	}
	effect := retentionEffect(oldest)
	return &effect, nil
}

func retentionEffect(cp *Checkpoint) Effect {
	return Effect{
		Name: "checkpoint-retention",
		Description: fmt.Sprintf(
			"Remove checkpoint %s from %s, the oldest settled of %d retained, with its paired recovery record",
			cp.ID,
			cp.record.Created.Format(time.RFC3339),
			MaxForwardCheckpoints,
		),
		Privilege: "user",
		Recovery:  "the removed checkpoint can no longer be restored",
	}
}

func retentionCandidate(c Context) (*Checkpoint, error) {
	checkpoints, err := loadCheckpoints(c)
	if err != nil || len(checkpoints) < MaxForwardCheckpoints {
		return nil, err
	}
	state, err := ReadState(c.Paths)
	if err != nil {
		return nil, err
	}
	var oldest *Checkpoint
	for _, cp := range checkpoints {
		settled := cp.journal.Status == JournalComplete || cp.journal.Status == JournalFailed
		if !settled ||
			state != nil && state.PartialOperation != nil && state.PartialOperation.ID == cp.ID {
			continue
		}
		if oldest == nil || cp.record.Created.Before(oldest.record.Created) {
			oldest = cp
		}
	}
	return oldest, nil
}

// pruneApproved removes only the checkpoint the approved plan names. A rename
// out of the scope makes the removal atomic before its files are deleted.
func pruneApproved(c Context, plan Plan) error {
	oldest, err := retentionCandidate(c)
	if err != nil {
		return err
	}
	if oldest == nil || !slices.Contains(plan.Effects, retentionEffect(oldest)) {
		return Fail(
			ExitBlocked,
			"checkpoint_limit",
			"Scope retains 20 forward checkpoints and the plan approves no removal; resolve incomplete checkpoints and review a new plan",
		)
	}
	// The trash is outside the scope, whose listing admits only checkpoints.
	trash := filepath.Dir(checkpointScope(c))
	return removeDirectory(oldest.directory, trash, ".removed-"+oldest.ID)
}

func decodePrivate(path string, limit int64, value any) error {
	data, err := ReadPrivateInput(path, limit)
	if err != nil {
		return err
	}
	if err = DecodeStrict(data, value); err != nil {
		return Fail(
			ExitInvalid,
			"checkpoint_format",
			"Malformed checkpoint ("+err.Error()+"); no repair or deletion attempted",
		)
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
		return nil, Fail(
			ExitInvalid,
			"checkpoint_format",
			"Scope checkpoint count exceeds its supported bound",
		)
	}
	var result []*Checkpoint
	var total int64
	for _, entry := range entries {
		if entry.IsDir() && strings.HasPrefix(entry.Name(), ".prepare-") &&
			operationID.MatchString(strings.TrimPrefix(entry.Name(), ".prepare-")) {
			continue
		}
		if !entry.IsDir() || !operationID.MatchString(entry.Name()) {
			return nil, Fail(
				ExitInvalid,
				"checkpoint_format",
				"Unexpected checkpoint entry; no cleanup attempted",
			)
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
		return nil, Fail(
			ExitInvalid,
			"checkpoint_limit",
			"Retained scope storage exceeds its supported count or byte bound",
		)
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
			return Fail(
				ExitInvalid,
				"checkpoint_limit",
				"Checkpoint file count exceeds its supported bound",
			)
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if info.Mode().Perm() != 0o700 {
				return Fail(ExitInvalid, "permissions", "Checkpoint directories require mode 0700")
			}
			return owned(info)
		}
		if err = checkPrivateFile(path); err != nil {
			return err
		}
		size += info.Size()
		if size > 50<<20 {
			return Fail(
				ExitInvalid,
				"checkpoint_limit",
				"Checkpoint exceeds its 50 MiB serialized storage bound",
			)
		}
		return nil
	})
	return size, err
}

func loadCheckpoint(c Context, directory string) (*Checkpoint, error) {
	cp := &Checkpoint{directory: directory}
	if err := decodePrivate(
		filepath.Join(directory, "checkpoint.json"),
		1<<20,
		&cp.record,
	); err != nil {
		return nil, err
	}
	r := cp.record
	if r.SchemaVersion != 1 || r.Scope != c.Scope || !operationID.MatchString(r.ID) ||
		r.ID != filepath.Base(directory) ||
		!operationID.MatchString(r.RecoveryID) ||
		r.ID == r.RecoveryID ||
		r.Created.IsZero() {
		return nil, Fail(
			ExitInvalid,
			"checkpoint_format",
			"Unsupported checkpoint identity or scope",
		)
	}
	if err := (State{SchemaVersion: 1, AppliedConfiguration: &r.Applied}).Validate(); err != nil {
		return nil, err
	}
	if err := (State{SchemaVersion: 1, AppliedConfiguration: r.Before}).Validate(); err != nil {
		return nil, err
	}
	if len(r.Targets) > MaxCheckpointTargets {
		return nil, Fail(ExitInvalid, "checkpoint_format", "Invalid checkpoint target count")
	}
	if err := cp.loadImages(); err != nil {
		return nil, err
	}
	if err := validateChanges(c, cp.changes); err != nil {
		return nil, err
	}
	for i := range 2 {
		j, err := readJournal(directory, i, len(cp.changes))
		if err != nil {
			return nil, err
		}
		if j.Sequence > cp.journal.Sequence {
			cp.journal = j
		}
	}
	for i := range cp.changes {
		cp.changes[i].After.Attributes = cp.journal.PostAttributes[i]
		if cp.journal.RecoveryCreated && cp.journal.PairPost[i] == outcomeBefore {
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

// loadImages reads each target's stored before/after images, checking every
// digest and the aggregate read bound.
func (cp *Checkpoint) loadImages() error {
	total := 0
	for _, ref := range cp.record.Targets {
		change := TargetChange{Path: ref.Path}
		for index, digest := range []string{ref.Before, ref.After} {
			if !ValidDigest(digest) {
				return Fail(ExitInvalid, "checkpoint_format", "Invalid checkpoint image reference")
			}
			image, err := readStoredImage(cp.directory, digest)
			if err != nil {
				return err
			}
			if ImageDigest(image) != digest {
				return Fail(ExitInvalid, "checkpoint_format", "Checkpoint image integrity mismatch")
			}
			total += len(image.Data) + len(image.Link)
			if total > MaxCheckpointImageBytes {
				return Fail(
					ExitInvalid,
					"checkpoint_limit",
					"Checkpoint images exceed their aggregate read bound",
				)
			}
			if index == 0 {
				change.Before = image
			} else {
				change.After = image
			}
		}
		cp.changes = append(cp.changes, change)
	}
	return nil
}

// readJournal decodes journal slot i and checks its digest, identity, sizes
// against targets and every recorded outcome.
func readJournal(directory string, i, targets int) (checkpointJournal, error) {
	var envelope journalEnvelope
	path := filepath.Join(directory, fmt.Sprintf("journal-%d.json", i))
	if err := decodePrivate(path, journalBytes, &envelope); err != nil {
		return checkpointJournal{}, err
	}
	data, _ := json.Marshal(envelope.Journal)
	j := envelope.Journal
	statuses := []JournalStatus{
		JournalPrepared,
		JournalRunning,
		JournalComplete,
		JournalFailed,
		JournalPartial,
		JournalUnknown,
	}
	if envelope.Digest != SHA256Hex(data) || j.SchemaVersion != 1 ||
		!operationID.MatchString(j.OperationID) ||
		j.Sequence == 0 ||
		len(j.Known) != targets ||
		len(j.PostAttributes) != targets ||
		len(j.ObservedAttributes) != targets ||
		(j.Direction != "forward" && j.Direction != "reverse") ||
		!slices.Contains(statuses, j.Status) {
		return j, Fail(
			ExitInvalid,
			"checkpoint_format",
			"Invalid recovery journal; retained images require review",
		)
	}
	for _, known := range j.Known {
		if !slices.Contains([]targetOutcome{outcomeBefore, outcomeAfter, outcomeUnknown}, known) {
			return j, Fail(ExitInvalid, "checkpoint_format", "Invalid target outcome")
		}
	}
	if !j.RecoveryCreated {
		if len(j.PairPost) != 0 {
			return j, Fail(
				ExitInvalid,
				"checkpoint_format",
				"Unexpected paired recovery image selection",
			)
		}
		return j, nil
	}
	if len(j.PairPost) != targets {
		return j, Fail(ExitInvalid, "checkpoint_format", "Missing paired recovery image selection")
	}
	for _, known := range j.PairPost {
		if known != outcomeBefore && known != outcomeAfter {
			return j, Fail(
				ExitInvalid,
				"checkpoint_format",
				"Invalid paired recovery image selection",
			)
		}
	}
	return j, nil
}

func (cp *Checkpoint) expectedImage(index int) Image {
	image := cp.changes[index].Before
	if cp.journal.Known[index] == outcomeAfter {
		image = cp.changes[index].After
	}
	image.Attributes = cp.journal.ObservedAttributes[index]
	return image
}

// ListCheckpoints returns the scope's checkpoints and recovery pairs, oldest
// first.
func ListCheckpoints(c Context) ([]CheckpointSummary, error) {
	checkpoints, err := loadCheckpoints(c)
	if err != nil {
		return nil, err
	}
	result := make([]CheckpointSummary, 0, len(checkpoints)*2)
	for _, cp := range checkpoints {
		r := cp.record
		result = append(
			result,
			CheckpointSummary{
				ID:       r.ID,
				PairedID: r.RecoveryID,
				Before:   r.Before,
				Applied:  &r.Applied,
				Created:  r.Created,
				Status:   cp.journal.Status,
			},
		)
		if cp.journal.RecoveryCreated {
			result = append(
				result,
				CheckpointSummary{
					ID:       r.RecoveryID,
					PairedID: r.ID,
					Before:   &r.Applied,
					Applied:  r.Before,
					Created:  r.Created,
					Status:   cp.journal.Status,
					Recovery: true,
				},
			)
		}
	}
	slices.SortFunc(
		result,
		func(a, b CheckpointSummary) int { return a.Created.Compare(b.Created) },
	)
	return result, nil
}
