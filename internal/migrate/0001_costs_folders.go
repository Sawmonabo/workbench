package migrate

import (
	"context"

	"github.com/Sawmonabo/workbench/internal/costs"
)

// costsFolders moves the costs files out of the claude-costs folders. The
// move belongs to internal/costs, which alone opens, checks and copies the
// ledger; the import worker runs the same move by itself (costs.Ingest), without
// asking, because usage left unread could be lost.
var costsFolders = Migration{
	ID:    "0001-costs-folders",
	Title: "Move the costs files into Workbench's folders",
	Doing: "moving the costs files into Workbench's folders",
	What: "Moves the costs ledger and its price caches from the claude-costs data folder " +
		"into Workbench's data folder (costs/), and your price overrides into its config " +
		"folder, then deletes the old import log and lock and removes each emptied " +
		"claude-costs folder. A file whose old and new paths both hold different data " +
		"stays where it is, and every other file still moves. A file it doesn't know " +
		"stays, with its folder.",
	Recovery: "moves keep every ledger row and every other file's bytes, so moving the " +
		"files back by hand restores the old layout",
	Check: func(l Locations) (Status, string) {
		check := costs.CheckFolders(l.OldCosts, l.Costs)
		switch check.State {
		case costs.FoldersPending:
			return Pending, check.Text
		case costs.FoldersUnfinished:
			return Unfinished, check.Text
		case costs.FoldersConflict:
			return Conflict, check.Text
		default:
			return Done, keeps(check.Kept)
		}
	},
	Run: func(ctx context.Context, l Locations) ([]string, error) {
		return costs.MoveFolders(ctx, l.OldCosts, l.Costs)
	},
}
