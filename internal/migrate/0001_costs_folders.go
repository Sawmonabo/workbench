package migrate

import (
	"context"

	"github.com/Sawmonabo/workbench/internal/agent/costs"
)

// costsFolders moves the costs files out of the claude-costs folders. The
// move belongs to internal/agent/costs, which alone opens, checks and copies the
// ledger; the import worker runs the same move by itself (costs.Ingest), without
// asking, because usage left unread could be lost.
var costsFolders = Migration{
	ID:    "0001-costs-folders",
	Title: "Move the costs files into Workbench's folders",
	Doing: "moving the costs files into Workbench's folders",
	What: "Moves the downloaded price lists and then the costs ledger from the claude-costs " +
		"data folder into Workbench's data folder (agent/costs/), so the ledger's first " +
		"open there takes their prices, then deletes the old import log and lock and " +
		"removes each emptied claude-costs folder. A ledger whose old and new paths both " +
		"hold different data stays where it is, and every other file still moves. A file " +
		"it doesn't know stays, with its folder.",
	Recovery: "moving the files back by hand restores the old layout",
	Check: func(l Locations) Found {
		check := costs.CheckFolders(l.OldCosts, l.Costs)
		if check.State != costs.FoldersDone {
			// What it found is read in the costs folders, so a folder
			// refused there is said instead, and what they hold is read
			// again once a folder its owner could not read or search has
			// 0700.
			if err := costs.CheckOwnFolders(l.Costs); err != nil {
				return Found{Status: Pending, Refusal: err}
			}
			check = costs.CheckFolders(l.OldCosts, l.Costs)
		}
		headline, body := check.Said()
		found := Found{Headline: headline, Body: body}
		switch check.State {
		case costs.FoldersPending:
			found.Status = Pending
		case costs.FoldersUnfinished:
			found.Status = Unfinished
		case costs.FoldersConflict:
			found.Status = Conflict
		default:
			return Found{Status: Done, Headline: keeps(check.Kept)}
		}
		return found
	},
	Run: func(ctx context.Context, l Locations) ([]string, error) {
		return costs.MoveFolders(ctx, l.OldCosts, l.Costs)
	},
}
