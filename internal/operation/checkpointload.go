package operation

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/Sawmonabo/workbench/internal/display"
)

// LastApplied returns, for each target path in c's scope, the image the
// checkpoint that most recently wrote it left there: its post-image, or its
// pre-image after a revert or failure. A checkpoint with an unknown outcome
// for a target is skipped, so an earlier known image stands.
func LastApplied(c Context) (map[string]Image, error) {
	checkpoints, err := loadCheckpoints(c)
	if err != nil {
		return nil, err
	}
	slices.SortFunc(checkpoints, func(a, b *Checkpoint) int {
		return b.journal.Updated.Compare(a.journal.Updated)
	})
	images := map[string]Image{}
	for _, cp := range checkpoints {
		for i, change := range cp.changes {
			if _, seen := images[change.Path]; seen || cp.journal.Known[i] == outcomeUnknown {
				continue
			}
			images[change.Path] = cp.expectedImage(i)
		}
	}
	return images, nil
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

// decodePrivate strictly decodes the private checkpoint file at path, at
// most limit bytes and named what (see [ReadPrivateInput]), into value.
func decodePrivate(path string, limit int64, what string, value any) error {
	data, err := ReadPrivateInput(path, limit, what)
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
	if errors.Is(err, fs.ErrNotExist) {
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
		if !entry.IsDir() && entry.Name() == ".DS_Store" {
			continue // Finder metadata, never Workbench data.
		}
		if entry.IsDir() && strings.HasPrefix(entry.Name(), ".prepare-") &&
			operationID.MatchString(strings.TrimPrefix(entry.Name(), ".prepare-")) {
			continue
		}
		if !entry.IsDir() || !operationID.MatchString(entry.Name()) {
			return nil, Fail(
				ExitInvalid,
				"checkpoint_format",
				"Unexpected checkpoint entry "+filepath.Join(
					directory,
					entry.Name(),
				)+"; no cleanup attempted",
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
				return Fail(
					ExitInvalid,
					"permissions",
					"Existing "+display.Path(
						path,
					)+" must be a private directory (mode 0700); it was not changed",
				)
			}
			return owned(path, info)
		}
		if entry.Name() == ".DS_Store" && info.Mode().IsRegular() {
			return nil // Finder metadata; nothing reads it.
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
		"a checkpoint's record",
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
	// An interrupted save can tear the slot it was writing. saveJournal syncs
	// the other slot before any target write that depends on it, so the intact
	// slot is the last durably recorded state: the same or more cautious. Only
	// format failures are tolerated; permission and ownership errors mean
	// tampering and fail at once.
	var torn error
	for i := range 2 {
		j, err := readJournal(directory, i, len(cp.changes))
		if err != nil {
			if Category(err) == "checkpoint_format" {
				torn = err
				continue
			}
			return nil, err
		}
		if j.Sequence > cp.journal.Sequence {
			cp.journal = j
		}
	}
	if cp.journal.Sequence == 0 {
		return nil, torn
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
	if err := decodePrivate(path, journalBytes, "a checkpoint's journal", &envelope); err != nil {
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

func readStoredImage(directory, digest string) (Image, error) {
	directory = filepath.Join(directory, "images", digest)
	var manifest imageManifest
	if err := decodePrivate(
		filepath.Join(directory, "image.json"),
		1024,
		"a checkpoint image's manifest",
		&manifest,
	); err != nil {
		return Image{}, err
	}
	if manifest.Size < 1 || manifest.Size > MaxImageBytes*2 ||
		manifest.Chunks != (manifest.Size+(1<<20)-1)/(1<<20) {
		return Image{}, Fail(ExitInvalid, "checkpoint_format", "Invalid image storage bounds")
	}
	data := make([]byte, 0, manifest.Size)
	for i := range manifest.Chunks {
		part, err := ReadPrivateInput(
			filepath.Join(directory, fmt.Sprintf("%02d.part", i)),
			1<<20,
			"a part of a checkpoint image",
		)
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
