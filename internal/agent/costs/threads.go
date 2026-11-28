package costs

import (
	"cmp"
	"context"
	"database/sql"
	"errors"
	"maps"
	"slices"
)

// ThreadParent is a thread the file names as spawned by another: its
// immediate parent, which is not always the root. The ledger walks these links
// to the root thread that a hook binds a session under.
type ThreadParent struct{ Thread, Parent string }

// AddThreadParent records that tool's thread was spawned by parent, as the
// thread's own file names it. It changes no row: ResolveAccounts, which
// every run ends with, moves the rows stored under a parent to the root.
func (t *Tx) AddThreadParent(tool string, link ThreadParent) error {
	if link.Thread == "" || link.Parent == "" || link.Thread == link.Parent {
		return nil
	}
	_, err := t.tx.ExecContext(t.ctx,
		`INSERT INTO thread_parents (tool, thread, parent) VALUES (?, ?, ?)
		 ON CONFLICT(tool, thread) DO UPDATE SET parent = excluded.parent`,
		tool, link.Thread, link.Parent)
	return err
}

// maxThreadDepth bounds a walk up thread_parents, so a cycle that damaged
// data could hold ends. Codex's own depth limit is far below it.
const maxThreadDepth = 64

// moveRowsToRoots gives each row stored under a thread that has a parent
// the root of that thread's chain, and returns the rootKeys of the roots that
// gained rows. A parser knows only the spawning parent of an old subagent, so
// a depth-2 subagent's rows arrive under its parent; the parent's own link
// may be read before or after them, in this run or another, so the walk is
// done here, over every link, whatever order the files were read in. A row
// already under its root is not touched, so it is idempotent.
func moveRowsToRoots(ctx context.Context, tx *sql.Tx, names *nameSet) ([]string, error) {
	rows, err := tx.QueryContext(ctx, "SELECT tool, thread, parent FROM thread_parents")
	if err != nil {
		return nil, err
	}
	parents := map[bindingKey]string{}
	for rows.Next() {
		var key bindingKey
		var parent string
		if err := rows.Scan(&key.tool, &key.session, &parent); err != nil {
			_ = rows.Close()
			return nil, err
		}
		parents[key] = parent
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	var moved []string
	for _, key := range slices.SortedFunc(maps.Keys(parents), func(a, b bindingKey) int {
		return cmp.Or(cmp.Compare(a.tool, b.tool), cmp.Compare(a.session, b.session))
	}) {
		root := parents[key]
		for range maxThreadDepth {
			next, ok := parents[bindingKey{key.tool, root}]
			if !ok {
				break
			}
			root = next
		}
		if root == key.session {
			continue // a cycle in damaged data: no root to move to
		}
		tool, found, err := names.lookup(nameTool, key.tool)
		if err != nil || !found {
			if err != nil {
				return nil, err
			}
			continue // no rows of the tool
		}
		under, found, err := names.lookup(nameSession, key.session)
		if err != nil || !found {
			if err != nil {
				return nil, err
			}
			continue // no rows under the thread
		}
		var one int
		switch err := tx.QueryRowContext(ctx,
			"SELECT 1 FROM responses WHERE tool = ? AND root = ? LIMIT 1", tool, under,
		).Scan(&one); {
		case errors.Is(err, sql.ErrNoRows):
			continue
		case err != nil:
			return nil, err
		}
		rootID, err := names.id(nameSession, root)
		if err != nil {
			return nil, err
		}
		result, err := tx.ExecContext(
			ctx,
			"UPDATE responses SET root = ? WHERE tool = ? AND root = ?",
			rootID,
			tool,
			under,
		)
		if err != nil {
			return nil, err
		}
		if n, err := result.RowsAffected(); err != nil {
			return nil, err
		} else if n > 0 && !slices.Contains(moved, rootKey(key.tool, root)) {
			moved = append(moved, rootKey(key.tool, root))
		}
	}
	return moved, nil
}
