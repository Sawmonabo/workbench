package release

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/Sawmonabo/workbench/internal/operation"
)

type candidateRecord struct {
	Directory      string `json:"directory"`
	SHA256         string `json:"sha256"`
	Version        string `json:"version"`
	MetadataSHA256 string `json:"metadata_sha256"`
}

type candidate struct {
	bundle       Bundle
	record       operation.ReleaseRecord
	recordDigest string
}

func selectedCandidate(c operation.Context) (*candidate, error) {
	directory, err := Candidate(c)
	if err != nil {
		return nil, err
	}
	if directory == "" || directory != c.Native.Source {
		return nil, nil
	}
	raw, err := operation.ReadPrivateInput(filepath.Join(c.Paths.State, "candidate.json"), 1<<20)
	if err != nil {
		return nil, err
	}
	var saved candidateRecord
	if err = json.Unmarshal(raw, &saved); err != nil {
		return nil, err
	}
	metadata, err := Inspect(directory)
	if err != nil {
		return nil, err
	}
	manifest, err := os.ReadFile(filepath.Join(directory, "release.json"))
	if err != nil || sum(manifest) != saved.MetadataSHA256 || metadata.Release != saved.Version {
		return nil, operation.Fail(4, "candidate", "Staged manifest differs from its verified candidate record")
	}
	b := Bundle{Metadata: metadata, ArchiveDigest: saved.SHA256, Files: map[string][]byte{"release.json": manifest}}
	return &candidate{bundle: b, record: operation.ReleaseRecord{Identity: b.Identity(), Executable: filepath.Join(directory, "bin", "workbench"), Source: directory}, recordDigest: sum(raw)}, nil
}

// CandidateHandoff validates and executes a staged runtime without activation,
// acquisition or persistent writes. Its own compiled source trust handles the
// selected plan/apply; the installed entry point remains unchanged until apply.
func CandidateHandoff(ctx context.Context, c operation.Context, args []string) error {
	selected, err := selectedCandidate(c)
	if err != nil || selected == nil {
		return err
	}
	actual, err := os.Executable()
	if err != nil {
		return err
	}
	actual, err = filepath.EvalSymlinks(actual)
	if err != nil {
		return err
	}
	if actual == selected.record.Executable {
		return nil
	}
	if err = checkRuntime(ctx, c, selected.record.Executable, selected.record.Source); err != nil {
		return err
	}
	return operation.Handoff(c, selected.record, args, selected.recordDigest)
}

// PlanCandidate binds the complete verified candidate (including CLI-only
// changes) and current active selection into the same native approval digest.
func PlanCandidate(c operation.Context, plan *operation.Plan) error {
	selected, err := selectedCandidate(c)
	if err != nil || selected == nil {
		return err
	}
	state, err := operation.ReadState(c.Paths)
	if err != nil {
		return err
	}
	plan.Inputs = append(plan.Inputs, operation.Input{Name: "verified-candidate", Digest: selected.recordDigest})
	stateRaw, _ := json.Marshal(state)
	plan.Inputs = append(plan.Inputs, operation.Input{Name: "runtime-selection", Digest: sum(stateRaw)})
	if state != nil && state.ActiveRelease != nil && *state.ActiveRelease == selected.record {
		return nil
	}
	plan.Effects = append(plan.Effects, operation.Effect{Name: "activate-candidate", Description: "Activate this verified unpublished evaluation CLI and source together; production qualification remains gated", Privilege: "user", Recovery: "Previous runtime retained; interrupted selector changes require resuming the same approved installation"})
	return nil
}

// ActivateCandidate is called by the native apply owner while holding the
// approved plan's mutation locks, including when no target file edits exist.
func ActivateCandidate(ctx context.Context, c operation.Context, m *operation.Mutation) error {
	selected, err := selectedCandidate(c)
	if err != nil || selected == nil {
		return err
	}
	state, err := operation.ReadState(c.Paths)
	if err != nil {
		return err
	}
	if state != nil && state.ActiveRelease != nil && *state.ActiveRelease == selected.record {
		return nil
	}
	for name := range selected.bundle.Metadata.Files {
		data, readErr := os.ReadFile(filepath.Join(selected.record.Source, name))
		if readErr != nil {
			return readErr
		}
		selected.bundle.Files[name] = data
	}
	return Activate(ctx, c, m, selected.bundle, selected.record.Source)
}

func checkRuntime(ctx context.Context, c operation.Context, executable, directory string) error {
	probe := c
	probe.Native.Source = ""
	output, err := operation.Run(ctx, probe, nil, operation.Process{Executable: executable, Args: []string{"release-check", "--bundle-directory", directory, "--json"}, Directory: "/", Environment: RuntimeEnvironment(c), OutputLimit: 1 << 20})
	var checked operation.Result
	if err != nil || json.Unmarshal([]byte(output.Stdout), &checked) != nil || checked.Status != "complete" || len(checked.Errors) != 0 || len(checked.Results) != 1 || checked.Results[0].Name != "release-check" || checked.Results[0].Status != "complete" {
		return operation.Fail(3, "handoff", "Candidate runtime did not validate its source and current contract; installed runtime retained")
	}
	return nil
}
