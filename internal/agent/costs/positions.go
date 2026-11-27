package costs

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"time"

	"github.com/Sawmonabo/workbench/internal/sqlitefile"
)

// FileRow is where ingest stopped in one transcript: the offset, the size and
// time it had then, the source's saved state there, and a digest of its
// first line, which tells a file rewritten in place from one that grew.
type FileRow struct {
	Offset, Size, ModTimeNanos int64
	State                      []byte
	Head                       string
}

// File is the stored position of a transcript.
func (l *Ledger) File(path string) (row FileRow, found bool, err error) {
	var state string
	err = l.db.QueryRow(
		"SELECT offset, size, mtime_ns, state, head FROM files WHERE path = ?", path,
	).Scan(&row.Offset, &row.Size, &row.ModTimeNanos, &state, &row.Head)
	if errors.Is(err, sql.ErrNoRows) {
		return row, false, nil
	}
	if state != "" {
		row.State = []byte(state)
	}
	return row, err == nil, err
}

// readSince reports whether the ledger read a transcript whose path contains
// text when that transcript had been written at or after since (ledger
// layout).
func (l *Ledger) readSince(text, since string) bool {
	when, err := time.Parse(timeLayout, since)
	if err != nil {
		return false
	}
	var one int
	return l.db.QueryRow(
		"SELECT 1 FROM files WHERE instr(path, ?) > 0 AND mtime_ns >= ? LIMIT 1",
		text, when.UnixNano(),
	).Scan(&one) == nil
}

// parseRevisionKey is the meta key of the parser revision tool's
// transcripts were last read with (reparser).
func parseRevisionKey(tool string) string { return "parse_revision:" + tool }

// ForgetFiles makes ingest read again from the start every transcript under
// roots, once: when revision is not the one stored for tool it forgets where
// ingest stopped in those files and stores revision, in one transaction, so a
// run that stops half-way does not start the re-read over. The rows stay; a
// re-read rewrites them under the same keys.
func (l *Ledger) ForgetFiles(ctx context.Context, tool, revision string, roots []string) error {
	return sqlitefile.Transact(ctx, l.db, func(tx *sql.Tx) error {
		var stored string
		err := tx.QueryRowContext(ctx, "SELECT value FROM meta WHERE key = ?", parseRevisionKey(tool)).
			Scan(&stored)
		switch {
		case errors.Is(err, sql.ErrNoRows):
		case err != nil:
			return err
		case stored == revision:
			return nil
		}
		for _, root := range roots {
			root = filepath.Clean(root)
			if _, err := tx.ExecContext(
				ctx,
				"DELETE FROM files WHERE instr(path, ?) = 1",
				root+string(filepath.Separator),
			); err != nil {
				return err
			}
		}
		return setMeta(ctx, tx, parseRevisionKey(tool), revision)
	})
}

// DeleteFile forgets a transcript that no longer exists.
func (l *Ledger) DeleteFile(path string) error {
	_, err := l.db.Exec("DELETE FROM files WHERE path = ?", path)
	return err
}

// SetFile stores where ingest stopped in a transcript.
func (t *Tx) SetFile(path string, row FileRow) error {
	_, err := t.tx.ExecContext(
		t.ctx,
		`INSERT INTO files (path, offset, size, mtime_ns, last_seen, state, head)
		 VALUES (?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(path) DO UPDATE SET offset = excluded.offset, size = excluded.size,
		   mtime_ns = excluded.mtime_ns, last_seen = excluded.last_seen,
		   state = excluded.state, head = excluded.head`,
		path,
		row.Offset,
		row.Size,
		row.ModTimeNanos,
		stamp(time.Now()),
		string(row.State),
		row.Head,
	)
	return err
}
