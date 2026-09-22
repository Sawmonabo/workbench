package operation

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
)

type SourceIdentity struct {
	Release       string `json:"release"`
	ContentDigest string `json:"content_digest"`
}
type Dependency struct {
	Name         string   `json:"name"`
	Path         string   `json:"path"`
	Version      string   `json:"version"`
	Owner        string   `json:"owner"`
	Capabilities []string `json:"capabilities"`
}
type Input struct {
	Name   string `json:"name"`
	Digest string `json:"digest"`
}
type Edit struct {
	Path        string `json:"path"`
	Action      string `json:"action"`
	Description string `json:"description"`
}
type Effect struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Privilege   string `json:"privilege"`
	Recovery    string `json:"recovery"`
}

// Inputs includes native state, answers and target image hashes. It is private;
// public output exposes only the aggregate digest, never individual secret hashes.
// Descriptions contain reviewed redacted text, never raw diffs or answers.
type Plan struct {
	Source         SourceIdentity `json:"source"`
	Scope          Scope          `json:"scope"`
	Dependencies   []Dependency   `json:"dependencies"`
	Inputs         []Input        `json:"-"`
	Edits          []Edit         `json:"edits"`
	Prerequisites  []string       `json:"prerequisites"`
	Effects        []Effect       `json:"effects"`
	RecoveryLimits []string       `json:"recovery_limits"`
	Complete       bool           `json:"complete"`
}

func (p Plan) Digest() (string, error) {
	// Include the private inputs in the digest without serializing them publicly.
	data, err := json.Marshal(struct {
		Plan   Plan
		Inputs []Input
	}{p, p.Inputs})
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:]), nil
}

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

func (m *Mutation) Check() error {
	if m == nil || !m.active || m.context.ReadOnly {
		return Fail(3, "read_only", "Mutation requires an approved current plan and held operation locks")
	}
	return nil
}

// WithMutation binds consent to the displayed plan, re-plans under both locks,
// and refuses changes if any bound input changed. The planner is always read-only.
func WithMutation(ctx context.Context, c Context, displayed Plan, consent Consent, planner func(context.Context, Context) (Plan, error), apply func(*Mutation) error) error {
	if c.ReadOnly {
		return Fail(3, "read_only", "This operation is read-only")
	}
	if err := c.Scope.Validate(); err != nil {
		return err
	}
	if !displayed.Complete || !consent.CompleteInputs || len(displayed.Prerequisites) != 0 {
		return Fail(3, "prerequisites", "Complete the disclosed setup stage and inputs before approving target changes")
	}
	if displayed.Scope != c.Scope {
		return Fail(4, "scope", "Plan does not match the selected scope")
	}
	digest, err := displayed.Digest()
	if err != nil {
		return err
	}
	if consent.ApprovedDigest != "" {
		if consent.ApprovedDigest != digest {
			return Fail(4, "plan", "Approval digest does not match the current plan; review a new plan")
		}
	} else {
		if consent.NonInteractive || consent.Confirm == nil {
			return Fail(3, "consent", "Mutation requires --approve-plan with the displayed digest, or terminal approval")
		}
		accepted, err := consent.Confirm(displayed, digest)
		if err != nil {
			return err
		}
		if !accepted {
			return Fail(3, "consent", "Plan was not approved; no target changes made")
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
	currentDigest, err := current.Digest()
	if err != nil {
		return err
	}
	if currentDigest != digest {
		return Fail(4, "plan", "Inputs changed after approval; review a new plan before writing")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	m := &Mutation{context: c, active: true}
	defer func() { m.active = false }()
	return apply(m)
}

func NewID() (string, error) {
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return "", err
	}
	id[6] = (id[6] & 0x0f) | 0x40
	id[8] = (id[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", id[0:4], id[4:6], id[6:8], id[8:10], id[10:16]), nil
}
