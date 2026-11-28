package operation

import (
	"cmp"
	"errors"
	"fmt"

	"github.com/Sawmonabo/workbench/internal/display"
)

// Settled is the unfinished operation the state recorded in a scope, which
// [SettleUnfinished] settled: its ID and its scope's root, whether it is a
// revert, whether its checkpoint is gone, so nothing of it could be checked,
// how the root's folder is gone ([Unfinished.Removed]), and the paths of the
// files the settling gave what they lacked, apart from a new file that held
// the label a file made in its folder gets.
type Settled struct {
	ID, Root string
	Revert   bool
	Gone     bool
	Removed  string
	Finished []string
}

// SettleUnfinished settles, under m, u, the operation the state records as
// unfinished in m's scope, as [PlanUnfinished] found it for the plan this
// apply rechecked under the same lock, before the apply replaces or clears
// that record; it returns nil for a nil u. It first removes what a stopped
// writer of the scope staged beside its targets (removeStagedFiles); one
// whose folder is gone leaves nothing there to remove or finish. For an
// apply that stopped after chezmoi wrote and before Workbench's pass after
// it, it gives each file u names what it lacks, in place, and leaves its
// content alone (settleTarget), so none of it is lost once the record goes:
// a file chezmoi wrote and one someone changed afterwards alike. It then
// records each target whose outcome the journal does not know as it holds
// after that: written when it holds its approved image, untouched when it
// holds its earlier one, and unknown otherwise, its content someone's
// change, which the plan shows. A file it could not finish does not stop the
// others, and fails the call with the files it finished. A revert writes
// whole images and leaves nothing to finish.
func SettleUnfinished(m *Mutation, u *Unfinished) (*Settled, error) {
	if err := m.Check(); err != nil {
		return nil, err
	}
	if u == nil {
		return nil, nil
	}
	c := m.context
	settled := &Settled{ID: u.ID, Root: u.Root, Revert: u.Revert, Gone: u.Gone, Removed: u.Removed}
	if u.Gone || u.Removed != "" {
		return settled, nil
	}
	if err := removeStagedIn(c); err != nil {
		return nil, err
	}
	if u.Revert {
		return settled, nil
	}
	cp := u.cp
	cp.mutation = m
	var first error
	for _, t := range u.Targets {
		changed, err := cp.settleTarget(t)
		if err != nil {
			first = cmp.Or(first, err)
			continue
		}
		if changed {
			settled.Finished = append(settled.Finished, t.Path)
		}
	}
	if err := cp.observeSettled(u.unknown); err != nil {
		return nil, err
	}
	if first == nil {
		return settled, nil
	}
	problem, ok := errors.AsType[*Error](first)
	if !ok || len(settled.Finished) == 0 {
		return nil, first
	}
	return nil, FailWith(
		problem.Code,
		problem.Category,
		problem.Message,
		problem.With(Explain(Changed(shownPaths(c, settled.Finished)))),
	)
}

// observeSettled records the outcome of each target indexed by targets as it
// holds after the settling, and the checkpoint's status as for a run that was
// stopped, then saves the journal. A target that holds its approved image,
// apart from extended attributes the system gave it, such as macOS's
// provenance, and the label (lacksOf), counts as written, and its attributes
// as what the apply wrote; one that holds its earlier image as untouched;
// any other stays unknown, its content someone's change.
func (cp *Checkpoint) observeSettled(targets []int) error {
	c := cp.mutation.context
	for _, i := range targets {
		change := cp.changes[i]
		current, err := ReadImage(c, change.Path)
		if err != nil {
			return err
		}
		switch {
		case sameImageContent(current, change.After) && lacksOf(change, current).content():
			cp.journal.Known[i] = outcomeAfter
			cp.journal.PostAttributes[i] = current.Attributes
			cp.changes[i].After.Attributes = current.Attributes
		case sameImageContent(current, change.Before):
			cp.journal.Known[i] = outcomeBefore
		default:
			continue
		}
		cp.journal.ObservedAttributes[i] = current.Attributes
	}
	changed, unknown := false, false
	for i, known := range cp.journal.Known {
		unknown = unknown || known == outcomeUnknown
		changed = changed || known == outcomeAfter &&
			!sameImage(cp.changes[i].Before, cp.expectedImage(i))
	}
	if validateChanges(c, cp.changes) != nil {
		unknown = true
	}
	cp.journal.Status = outcomeStatus(true, changed, unknown)
	return cp.saveJournal()
}

// shownPaths names up to three paths as c shows them, then how many more
// there are.
func shownPaths(c Context, paths []string) string {
	const shown = 3
	names := make([]string, 0, shown+1)
	for _, path := range paths[:min(len(paths), shown)] {
		names = append(names, c.ShowPath(path))
	}
	if len(paths) > shown {
		names = append(names, fmt.Sprintf("%d more", len(paths)-shown))
	}
	return display.List(names)
}
