package operation

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"os"
	"slices"
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
	// Merged marks a file native merges into (a modify template: Claude Code
	// settings, Codex config, VS Code settings), which keeps the owner's own
	// keys; every other file is owned, written whole by Workbench.
	Merged bool `json:"merged,omitempty"`
	// Folder marks a folder. A folder Workbench merely creates is a consequence
	// of the files in it, so the plan view leaves it out; see [Edit.Listed].
	Folder bool `json:"folder,omitempty"`
	// Title is the file's plain name for the plan view ("Claude Code
	// settings"); empty falls back to the path.
	Title string `json:"title,omitempty"`
	// Added and Removed are the diff's line counts, so the view colours them
	// without parsing Summary.
	Added   int `json:"added,omitempty"`
	Removed int `json:"removed,omitempty"`
	// Settings is, for a merged JSON or TOML file, how many settings (leaf key
	// paths) the merge changes. Such a file's Diff lists those settings, one
	// per line, instead of lines, because the merge re-serializes the whole
	// file and a line diff of it is mostly reordering; Semantic marks that, and
	// Rewritten says the lines differ by more than those settings. Both are
	// display only, like Diff.
	Settings  int  `json:"settings,omitempty"`
	Semantic  bool `json:"-"`
	Rewritten bool `json:"-"`
	// Diff is the unified diff of the change, capped, with secrets redacted;
	// DiffTruncated says lines were cut. It is the one place a plan carries
	// file content, so it is display only: never in JSON output (which is
	// logged) and never in the digest, which covers the file images instead.
	Diff          string `json:"-"`
	DiffTruncated bool   `json:"-"`
	// EditedOutside marks an existing owned file that is not what Workbench
	// last wrote, so applying replaces someone's edit. It is set from the
	// recorded last-applied image, never from Summary's text, and is never
	// set on a merged file.
	EditedOutside bool `json:"edited_outside,omitempty"`
}

// Listed reports whether the plan view shows the edit as a row of its own: every
// edit but a new folder, which is only where its files go.
func (e Edit) Listed() bool { return !e.Folder || e.Action != "create" }

// Files is how many edits the plan view lists; see [Edit.Listed].
func (p Plan) Files() int {
	files := 0
	for _, edit := range p.Edits {
		if edit.Listed() {
			files++
		}
	}
	return files
}

// AlreadySet reports that the effect has nothing to do: its probe found nothing
// to change and the owner has not turned it off. The plan view folds such an
// effect into one faint line and it cannot be chosen.
func (p Plan) AlreadySet(effect Effect) bool {
	return effect.NoChange && !effect.SavedSkip && !effect.Fixed
}

// Effect is a planned change outside checkpointed files, with the privilege
// it needs and what recovery can and cannot undo. Checked says whether this
// apply runs it; the plan digest covers that, so consent is for exactly the
// shown selection.
type Effect struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Privilege   string `json:"privilege"`
	Recovery    string `json:"recovery"`
	// Delta is the probed one-line change on this machine, "" when the
	// effect was not probed.
	Delta string `json:"delta,omitempty"`
	// Probe is "ok", "failed", "timeout" or "unanswered" (the script ran but
	// could not check something, such as a Windows call that did not answer)
	// once a probe ran, "" otherwise. It is for --json; the plan view shows
	// ProbeNote, never this word.
	Probe string `json:"probe,omitempty"`
	// ProbeNote says in plain words what could not be checked and that apply
	// checks again when it runs, for example "Windows didn't answer in time;
	// checked again when applied". It is empty when the probe answered.
	ProbeNote string `json:"probe_note,omitempty"`
	// NoChange is set when the probe reported nothing to do ("NAME: = TEXT")
	// for every line of every script that carries the effect; Delta then holds
	// what is already in place.
	NoChange bool `json:"no_change,omitempty"`
	// New marks an effect the owner has not yet decided on: not in the saved
	// [effects] decided list. It is display only.
	New bool `json:"new,omitempty"`
	// Optional marks an optional host effect: unchecked unless selected.
	Optional bool `json:"optional,omitempty"`
	// Title and Summary are the plan view's plain name and one faint line.
	// What, Touches, RunsAs and Undo fill the detail panel (what it does, what
	// it changes, who it runs as, how to undo it) beside Delta, which says what
	// it would do on this machine now. All are plain words; Name, Description,
	// Privilege and Recovery stay the technical record for --json and the
	// project plans.
	Title   string `json:"title,omitempty"`
	Summary string `json:"summary,omitempty"`
	What    string `json:"what,omitempty"`
	Touches string `json:"touches,omitempty"`
	RunsAs  string `json:"runs_as,omitempty"`
	Undo    string `json:"undo,omitempty"`
	// Fixed effects always run with the plan and cannot be unchecked: the
	// file-backed policy marker and checkpoint retention.
	Fixed bool `json:"fixed,omitempty"`
	// Checked effects run; unchecked ones are left out of this apply.
	Checked bool `json:"checked"`
	// SavedSkip marks an unchecked effect whose skip came from machine.toml.
	SavedSkip bool `json:"saved_skip,omitempty"`
}

// Plan is what an operation will do, shown before consent. Inputs holds native
// state, answers and target image hashes; it is private, and public output
// exposes only the aggregate [Plan.Digest], never individual secret hashes.
// Descriptions contain reviewed redacted text and summaries only counts, modes
// and link targets, never answers; an [Edit]'s capped, redacted Diff is the one
// place file content appears, for display only.
type Plan struct {
	Source        SourceIdentity `json:"source"`
	Scope         Scope          `json:"scope"`
	Dependencies  []Dependency   `json:"dependencies"`
	Inputs        []Input        `json:"-"`
	Edits         []Edit         `json:"edits"`
	Prerequisites []string       `json:"prerequisites"`
	Effects       []Effect       `json:"effects"`
	// UnchangedTargets counts the managed files this plan leaves as they are.
	UnchangedTargets int `json:"unchanged_targets"`
	// Warnings are review notes that change nothing, such as a check that
	// could not run.
	Warnings       []string `json:"warnings,omitempty"`
	RecoveryLimits []string `json:"recovery_limits"`
	Complete       bool     `json:"complete"`
}

// Digest returns the SHA-256 that consent approves: the public plan plus its
// private inputs, with each effect's probed Delta, Probe, ProbeNote and
// NoChange and its New mark blanked, and each edit's display-only Diff left
// out, so what is approved is the files, the effects and which are checked. A plan holds
// only strings, slices and bools, so marshalling cannot fail.
func (p Plan) Digest() string {
	approved := p
	approved.Edits = slices.Clone(p.Edits)
	for i := range approved.Edits {
		approved.Edits[i].Diff, approved.Edits[i].DiffTruncated = "", false
	}
	approved.Effects = slices.Clone(p.Effects)
	for i := range approved.Effects {
		effect := &approved.Effects[i]
		effect.Delta, effect.Probe, effect.ProbeNote = "", "", ""
		effect.NoChange, effect.New = false, false
	}
	data, _ := json.Marshal(struct {
		Plan   Plan
		Inputs []Input
	}{approved, approved.Inputs})
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

// agentVariables are the variables by which a coding agent marks the commands
// it runs: Claude Code sets CLAUDECODE=1, and Codex sets CODEX_CI,
// CODEX_THREAD_ID and, in its sandbox, CODEX_SANDBOX and
// CODEX_SANDBOX_NETWORK_DISABLED. The update handoff carries them, so the
// activated runtime still knows an agent runs it.
var agentVariables = []string{
	"CLAUDECODE",
	"CODEX_CI",
	"CODEX_THREAD_ID",
	"CODEX_SANDBOX",
	"CODEX_SANDBOX_NETWORK_DISABLED",
}

// AgentSession reports whether a coding agent runs this command. An agent can
// type at a pseudo-terminal, so one never counts as a person at a terminal: it
// reads the plan with --dry-run --json and applies exactly that plan with
// --approve-plan. Any value counts, so a marker never fails open.
func AgentSession() bool {
	for _, name := range agentVariables {
		if os.Getenv(name) != "" {
			return true
		}
	}
	return false
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
