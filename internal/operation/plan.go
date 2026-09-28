package operation

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
)

// SourceIdentity names a machine source: its release and the digest of its
// reviewed content.
type SourceIdentity struct {
	Release       string `json:"release"`
	ContentDigest string `json:"content_digest"`
}

// Dependency is a resolved management tool (chezmoi, python3 or uv): where it
// is, its version, who installed it and what it was qualified for.
type Dependency struct {
	Name         string   `json:"name"`
	Path         string   `json:"path"`
	Version      string   `json:"version"`
	Owner        string   `json:"owner"`
	Capabilities []string `json:"capabilities"`
}

// Input is one private value a plan depends on, recorded only by digest.
type Input struct {
	Name   string `json:"name"`
	Digest string `json:"digest"`
}

// Edit is one planned file change: its path, action, reviewed description and
// a content-free summary of what changes.
type Edit struct {
	Path        string `json:"path"`
	Action      string `json:"action"`
	Description string `json:"description"`
	Summary     string `json:"summary,omitempty"`
}

// Effect is a planned change outside checkpointed files, with the privilege
// it needs and what recovery can and cannot undo.
type Effect struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Privilege   string `json:"privilege"`
	Recovery    string `json:"recovery"`
}

// Plan is what an operation will do, shown before consent. Inputs holds native
// state, answers and target image hashes; it is private, and public output
// exposes only the aggregate [Plan.Digest], never individual secret hashes.
// Descriptions contain reviewed redacted text and summaries only counts, modes
// and link targets, never raw diffs or answers.
type Plan struct {
	Source        SourceIdentity `json:"source"`
	Scope         Scope          `json:"scope"`
	Dependencies  []Dependency   `json:"dependencies"`
	Inputs        []Input        `json:"-"`
	Edits         []Edit         `json:"edits"`
	Prerequisites []string       `json:"prerequisites"`
	Effects       []Effect       `json:"effects"`
	// Warnings are review notes that change nothing, such as a check that
	// could not run.
	Warnings       []string `json:"warnings,omitempty"`
	RecoveryLimits []string `json:"recovery_limits"`
	Complete       bool     `json:"complete"`
}

// Digest returns the SHA-256 that consent approves: the public plan plus its
// private inputs. A plan holds only strings, slices and bools, so marshalling
// cannot fail.
func (p Plan) Digest() string {
	data, _ := json.Marshal(struct {
		Plan   Plan
		Inputs []Input
	}{p, p.Inputs})
	return SHA256Hex(data)
}

// Consent is how a mutation is approved: an exact digest, or terminal
// confirmation of the displayed plan.
type Consent struct {
	ApprovedDigest string
	NonInteractive bool
	CompleteInputs bool
	// Confirm must display the same plan and only prompt on an actual terminal.
	// JSON/noninteractive callers leave it nil.
	Confirm func(Plan, string) (bool, error)
}

// Mutation is valid only inside WithMutation, with shared then scope locks held.
// This capability is not a checkpoint: target writers still need recovery preflight.
type Mutation struct {
	context Context
	active  bool
}

// Check reports an error unless m is an active, writable mutation.
func (m *Mutation) Check() error {
	if m == nil || !m.active || m.context.ReadOnly {
		return Fail(
			ExitBlocked,
			"read_only",
			"Mutation requires an approved current plan and held operation locks",
		)
	}
	return nil
}

// WithMutation binds consent to the displayed plan, re-plans under both locks,
// and refuses changes if any bound input changed. The planner is always read-only.
func WithMutation(
	ctx context.Context,
	c Context,
	displayed Plan,
	consent Consent,
	planner func(context.Context, Context) (Plan, error),
	apply func(*Mutation) error,
) error {
	if c.ReadOnly {
		return Fail(ExitBlocked, "read_only", "This operation is read-only")
	}
	if err := c.Scope.Validate(); err != nil {
		return err
	}
	if !displayed.Complete || !consent.CompleteInputs || len(displayed.Prerequisites) != 0 {
		return Fail(
			ExitBlocked,
			"prerequisites",
			"Complete the disclosed setup stage and inputs before approving target changes",
		)
	}
	if displayed.Scope != c.Scope {
		return Fail(ExitConflict, "scope", "Plan does not match the selected scope")
	}
	digest := displayed.Digest()
	if consent.ApprovedDigest != "" {
		if consent.ApprovedDigest != digest {
			return Fail(
				ExitConflict,
				"plan",
				"Approval digest does not match the current plan; review a new plan",
			)
		}
	} else {
		if consent.NonInteractive || consent.Confirm == nil {
			return Fail(
				ExitBlocked,
				"consent",
				"Approve at a terminal, or pass the displayed plan digest to --approve-plan",
			)
		}
		accepted, err := consent.Confirm(displayed, digest)
		if err != nil {
			return err
		}
		if !accepted {
			return Fail(ExitBlocked, "consent", "Plan was not approved; no target changes made")
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	locks, err := acquireLocks(c)
	if err != nil {
		return err
	}
	defer locks.close()
	preview := c
	preview.ReadOnly = true
	current, err := planner(ctx, preview)
	if err != nil {
		return err
	}
	currentDigest := current.Digest()
	if currentDigest != digest {
		return Fail(
			ExitConflict,
			"plan",
			"Inputs changed after approval; review a new plan before writing",
		)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	m := &Mutation{context: c, active: true}
	defer func() { m.active = false }()
	return apply(m)
}

// NewID returns a random version 4 UUID for an operation or checkpoint.
func NewID() (string, error) {
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return "", err
	}
	id[6] = (id[6] & 0x0f) | 0x40
	id[8] = (id[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", id[0:4], id[4:6], id[6:8], id[8:10], id[10:16]), nil
}
