package guard

// This file is how long the history keeps finished commands, and the trim
// that deletes the older ones and those over its limits.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

// keepSetting is the settings row that holds the time kept.
const keepSetting = "keep"

// Keep is how long finished commands are kept: the settings row, or
// historyKeepDefault while it is missing. A row that is not a positive number
// of seconds fails, and is left as it is.
func (h *History) Keep(ctx context.Context) (time.Duration, error) {
	var value string
	err := h.db.QueryRowContext(
		ctx, "SELECT value FROM settings WHERE key = ?", keepSetting,
	).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return historyKeepDefault, nil
	}
	if err != nil {
		return 0, h.fail(err)
	}
	seconds, err := strconv.ParseInt(value, 10, 64)
	if err != nil || seconds < 1 || seconds > int64(math.MaxInt64/time.Second) {
		return 0, fmt.Errorf("%s: the time kept, %q, is not a number of seconds", h.path, value)
	}
	return time.Duration(seconds) * time.Second, nil
}

// SetKeep sets how long finished commands are kept, in whole seconds; the
// next trim deletes those that ended longer ago.
func (h *History) SetKeep(ctx context.Context, keep time.Duration) error {
	seconds := int64(keep / time.Second)
	if seconds < 1 {
		return fmt.Errorf(
			"the time finished commands are kept must be a second or more, not %v",
			keep,
		)
	}
	_, err := h.db.ExecContext(ctx, `INSERT INTO settings(key, value) VALUES (?, ?)
ON CONFLICT(key) DO UPDATE SET value = excluded.value`, keepSetting, strconv.FormatInt(seconds, 10))
	return h.fail(err)
}

// Trim is one step of the trim, for the writer and for the command that sets
// the time kept: it deletes, in one transaction, first the commands that
// ended before now less the time kept, then those with the lowest seq beyond
// historyRows, then those with the lowest seq while the pages in use take
// more than historyBytes, at most trimStep commands, trimProcesses process
// rows and trimText bytes of command text after the first (see trimBudget),
// and then gives the pages they freed back to the disk. deleted is how many
// commands it deleted; again reports that it stopped at those bounds with
// more to delete, for another step after other writes. A step is short, so
// the two writers, each waiting up to the busy timeout for the other, both
// write while one trims. It also deletes each copy [MoveDamaged] made once
// its files were all last written before now less the time kept, when all it
// holds would have gone anyway.
func (h *History) Trim(ctx context.Context, now time.Time) (deleted int, again bool, err error) {
	if !h.write {
		return 0, false, fmt.Errorf("%s: a history opened for reading is never trimmed", h.path)
	}
	keep, err := h.Keep(ctx)
	if err != nil {
		return 0, false, err
	}
	since := now.Add(-keep)
	removeDamagedBefore(h.root, since)
	cutoff := since.UnixMilli()
	budget := &trimBudget{}
	err = h.transact(ctx, func(tx *sql.Tx) error {
		if _, err := deleteFirst(
			ctx, tx, budget, trimStep, "WHERE ended < ? ORDER BY ended", cutoff,
		); err != nil || budget.spent() {
			return err
		}
		var rows int
		if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM commands").Scan(&rows); err != nil {
			return err
		}
		if over := rows - historyRows; over > 0 {
			if _, err := deleteFirst(
				ctx, tx, budget, min(over, budget.left()), "ORDER BY seq",
			); err != nil || budget.spent() {
				return err
			}
		}
		return deleteForSize(ctx, tx, budget)
	})
	if err != nil {
		return 0, false, err
	}
	deleted = budget.commands
	if deleted == 0 {
		return 0, false, nil
	}
	if _, err := h.db.ExecContext(ctx, "PRAGMA incremental_vacuum"); err != nil {
		return deleted, false, h.fail(err)
	}
	if !budget.spent() {
		// Each part deleted all it had to.
		return deleted, false, nil
	}
	err = h.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM commands WHERE ended < ?1)
  OR (SELECT count(*) FROM commands) > ?2 OR (`+pagesInUse+`) > ?3`,
		cutoff, historyRows, historyBytes,
	).Scan(&again)
	return deleted, again, h.fail(err)
}

// TrimPause is how long a writer that trims leaves the history to the other
// writer (the watcher's, or `agent guard history --keep`) after a step that
// took took, before its next step: as long as the step held the file, and a
// millisecond at least. SQLite's busy wait tries the lock again at intervals
// and is not fair, so a writer whose steps followed each other at once would
// keep the other out past its busy timeout.
func TrimPause(took time.Duration) time.Duration { return max(took, time.Millisecond) }

// pagesInUse is a query of the bytes the pages in use take: those of the file
// less those on its free list.
const pagesInUse = `SELECT (page_count - freelist_count) * page_size
  FROM pragma_page_count, pragma_freelist_count, pragma_page_size`

// trimBudget is what one Trim step has deleted: commands, their process rows
// and their bytes of command text. full is set once a command was left for
// the next step because it did not fit.
type trimBudget struct {
	commands        int
	processes, text int64
	full            bool
}

// take reports whether a command of processes rows and text bytes fits in
// the step, and counts it when it does. The step's first command always fits,
// so every step deletes one: a command holds at most processesKept rows and
// commandKept bytes of text.
func (b *trimBudget) take(processes, text int64) bool {
	if b.commands > 0 && (b.commands >= trimStep || b.processes+processes > trimProcesses ||
		b.text+text > trimText) {
		b.full = true
		return false
	}
	b.commands++
	b.processes += processes
	b.text += text
	return true
}

// spent reports whether the step can delete no more.
func (b *trimBudget) spent() bool { return b.full || b.commands >= trimStep }

// left is how many more commands the step may delete.
func (b *trimBudget) left() int { return trimStep - b.commands }

// deleteFirst deletes the first commands that order lists, order taking args,
// at most limit and as many as fit in budget, with their graphs and
// processes. It weighs each by its process rows and the bytes of its text,
// which octet_length reads from the row's header without reading the text.
// It reports how many it deleted.
func deleteFirst(
	ctx context.Context,
	tx *sql.Tx,
	budget *trimBudget,
	limit int,
	order string,
	args ...any,
) (int, error) {
	rows, err := tx.QueryContext(ctx,
		"SELECT seq, processes - unlisted, octet_length(command) FROM commands "+order+" LIMIT ?",
		append(args, limit)...)
	if err != nil {
		return 0, err
	}
	var seqs []any
	for rows.Next() {
		var seq, processes, text int64
		if err := rows.Scan(&seq, &processes, &text); err != nil {
			_ = rows.Close()
			return 0, err
		}
		if !budget.take(processes, text) {
			break
		}
		seqs = append(seqs, seq)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil || len(seqs) == 0 {
		return 0, err
	}
	result, err := tx.ExecContext(ctx,
		"DELETE FROM commands WHERE seq IN (?"+strings.Repeat(", ?", len(seqs)-1)+")", seqs...)
	if err != nil {
		return 0, err
	}
	n, err := result.RowsAffected()
	return int(n), err
}

// deleteForSize deletes the commands with the lowest seq, as many as fit in
// budget, while the pages in use take more than historyBytes, each time as
// many as the bytes over the limit take at the history's average size of a
// command.
func deleteForSize(ctx context.Context, tx *sql.Tx, budget *trimBudget) error {
	for !budget.spent() {
		var rows int
		var used int64
		if err := tx.QueryRowContext(
			ctx, "SELECT (SELECT count(*) FROM commands), ("+pagesInUse+")",
		).Scan(&rows, &used); err != nil {
			return err
		}
		if used <= historyBytes || rows == 0 {
			return nil
		}
		over := (used - historyBytes) * int64(rows)
		want := int(min((over+used-1)/used, int64(budget.left())))
		n, err := deleteFirst(ctx, tx, budget, max(want, 1), "ORDER BY seq")
		if err != nil || n == 0 {
			return err
		}
	}
	return nil
}
