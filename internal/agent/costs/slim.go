package costs

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"maps"
	"strconv"
	"strings"
	"time"

	"github.com/Sawmonabo/workbench/internal/operation"
)

// slim converts responses and tier_pending of version 6, in the upgrade's
// transaction, to the layout of responseSchema: names from every distinct
// value, each row under its rowid with its names' ids, its packed key, its
// evidence ranks and its time in milliseconds, and each waiting row with its
// packed key and its TierFrom's id. An evidence word other than the five, or
// a time that is neither "" nor exactly in timeLayout, refuses the upgrade,
// naming the file and the value. Before the old tables are dropped every
// converted row is read back and compared with its old one (see checkSlim),
// which step is told first.
func slim(ctx context.Context, tx *sql.Tx, path string, step func(string)) error {
	if err := execAll(ctx, tx,
		"ALTER TABLE responses RENAME TO responses_6",
		"ALTER TABLE tier_pending RENAME TO tier_pending_6",
		responseSchema,
	); err != nil {
		return err
	}
	names, err := newSlimNames(ctx, tx)
	if err != nil {
		return err
	}
	defer names.close()
	insert, err := tx.PrepareContext(ctx, insertRows(slimBatch))
	if err != nil {
		return err
	}
	defer func() { _ = insert.Close() }()
	rows, err := tx.QueryContext(ctx, `SELECT rowid, `+textColumns+` FROM responses_6`)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	var r textRow
	dest := r.fields()
	batch := make([]any, 0, slimBatch*len(dest))
	for rows.Next() {
		if err := rows.Scan(dest...); err != nil {
			return err
		}
		accountRank, ok := storedRank(r.text[colAccountSource])
		if !ok {
			return slimRefusal(path, r, "the account evidence", colAccountSource)
		}
		subscriptionRank, ok := storedRank(r.text[colSubscriptionSource])
		if !ok {
			return slimRefusal(path, r, "the plan evidence", colSubscriptionSource)
		}
		var when any
		if ts := r.text[colTime]; len(ts) > 0 {
			ms, ok := parseStoredTime(string(ts))
			if !ok {
				return slimRefusal(path, r, "the time", colTime)
			}
			when = ms
		}
		batch = append(batch, r.rowid, packID(string(r.text[colID])))
		for col := colTool; col <= colSubscription; col++ {
			id, err := names.id(nameKinds[col], r.text[col])
			if err != nil {
				return err
			}
			batch = append(batch, id)
		}
		batch = append(batch, accountRank, subscriptionRank, when,
			r.tokens[0], r.tokens[1], r.tokens[2], r.tokens[3], r.tokens[4])
		if len(batch) == cap(batch) {
			if _, err := insert.ExecContext(ctx, batch...); err != nil {
				return err
			}
			batch = batch[:0]
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if len(batch) > 0 {
		if _, err := tx.ExecContext(ctx, insertRows(len(batch)/len(dest)), batch...); err != nil {
			return err
		}
	}
	if err := slimPending(ctx, tx, names); err != nil {
		return err
	}
	step("checking every row")
	if err := checkSlim(ctx, tx); err != nil {
		return err
	}
	return execAll(ctx, tx, "DROP TABLE responses_6", "DROP TABLE tier_pending_6", responseIndexes)
}

// slimPending converts tier_pending (see slim).
func slimPending(ctx context.Context, tx *sql.Tx, names *slimNames) error {
	insert, err := tx.PrepareContext(
		ctx,
		"INSERT INTO tier_pending (request_id, root) VALUES (?, ?)",
	)
	if err != nil {
		return err
	}
	defer func() { _ = insert.Close() }()
	rows, err := tx.QueryContext(ctx, "SELECT request_id, root FROM tier_pending_6")
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var id string
		var root sql.RawBytes
		if err := rows.Scan(&id, &root); err != nil {
			return err
		}
		rootID, err := names.id(nameSession, root)
		if err != nil {
			return err
		}
		if _, err := insert.ExecContext(ctx, packID(id), rootID); err != nil {
			return err
		}
	}
	return rows.Err()
}

// slimBatch is how many rows slim inserts in one statement.
const slimBatch = 100

// insertRows is the statement that inserts n converted responses, each with
// its id and packedColumns.
func insertRows(n int) string {
	return `INSERT INTO responses (id, ` + packedColumns + `) VALUES ` +
		strings.TrimSuffix(strings.Repeat("(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?),", n), ",")
}

// slimRefusal is the error of a value slim cannot convert: what, in the
// column col of the response r.
func slimRefusal(path string, r textRow, what string, col int) error {
	return ledgerProblem(operation.ExitInvalid, path, fmt.Sprintf(
		"the response %q holds %s %q, which this release cannot convert to schema version %s; "+
			"the ledger was left as it was",
		r.text[colID], what, r.text[col], schemaVersion))
}

// slimNames are the names slim stores, numbered from 1 as it meets them.
type slimNames struct {
	ctx    context.Context
	insert *sql.Stmt
	ids    [nameSubscription + 1]map[string]int64
	next   int64
}

func newSlimNames(ctx context.Context, tx *sql.Tx) (*slimNames, error) {
	insert, err := tx.PrepareContext(ctx, "INSERT INTO names (id, kind, name) VALUES (?, ?, ?)")
	if err != nil {
		return nil, err
	}
	n := &slimNames{ctx: ctx, insert: insert, next: 1}
	for kind := range n.ids {
		n.ids[kind] = map[string]int64{}
	}
	return n, nil
}

func (n *slimNames) close() { _ = n.insert.Close() }

// id is the id of name, a name of kind, which it stores when it meets it
// first.
func (n *slimNames) id(kind int, name []byte) (int64, error) {
	if id, ok := n.ids[kind][string(name)]; ok {
		return id, nil
	}
	id := n.next
	if _, err := n.insert.ExecContext(n.ctx, id, kind, string(name)); err != nil {
		return 0, err
	}
	n.ids[kind][string(name)], n.next = id, id+1
	return id, nil
}

// textRow is a response as version 6 stored it, read for slim and checkSlim:
// its rowid, its text columns in the order of textColumns, valid until the
// next row is read, and its token counts.
type textRow struct {
	rowid  int64
	text   [colTime + 1]sql.RawBytes
	tokens [5]int64
}

// The text columns of a response, in the order of textColumns and
// packedColumns; nameKinds gives the kind of name of each name column.
const (
	colID = iota
	colTool
	colModel
	colProject
	colSession
	colRoot
	colAccount
	colSubscription
	colAccountSource
	colSubscriptionSource
	colTime
)

var nameKinds = [...]int{
	colTool: nameTool, colModel: nameModel, colProject: nameProject, colSession: nameSession,
	colRoot: nameSession, colAccount: nameAccount, colSubscription: nameSubscription,
}

// textColumns are the columns of a version 6 response, in the order of
// textRow.
const textColumns = `request_id, tool, model, project, session_id, root, account,
  subscription, account_source, subscription_source, ts,
  input, output, cache_write_5m, cache_write_1h, cache_read`

// packedColumns are the same columns of a response as responseSchema stores
// it.
const packedColumns = `request_id, tool, model, project, session, root, account,
  subscription, account_source, subscription_source, ts,
  input, output, cache_write_5m, cache_write_1h, cache_read`

func (r *textRow) fields() []any {
	out := []any{&r.rowid}
	for i := range r.text {
		out = append(out, &r.text[i])
	}
	for i := range r.tokens {
		out = append(out, &r.tokens[i])
	}
	return out
}

// checkSlim compares what slim wrote with the version 6 tables it read, before
// they are dropped: as many rows in each table, the same sum of each token
// column per tool, and every converted row read back through names, decoded,
// equal to its old row field for field. The first difference is the error,
// which rolls the upgrade back.
func checkSlim(ctx context.Context, tx *sql.Tx) error {
	if err := checkSlimTotals(ctx, tx); err != nil {
		return err
	}
	names, err := loadNames(ctx, tx)
	if err != nil {
		return err
	}
	// Both tables in the order of their rowids, which slim kept: the rows
	// pair up one by one.
	olds, err := tx.QueryContext(
		ctx,
		`SELECT rowid, `+textColumns+` FROM responses_6 ORDER BY rowid`,
	)
	if err != nil {
		return err
	}
	defer func() { _ = olds.Close() }()
	news, err := tx.QueryContext(ctx, `SELECT id, `+packedColumns+` FROM responses ORDER BY id`)
	if err != nil {
		return err
	}
	defer func() { _ = news.Close() }()
	var old textRow
	var p packedRow
	oldDest, newDest := old.fields(), p.fields()
	var buf []byte
	for olds.Next() {
		if err := olds.Scan(oldDest...); err != nil {
			return err
		}
		p = packedRow{}
		if news.Next() {
			if err := news.Scan(newDest...); err != nil {
				return err
			}
		}
		var same bool
		if same, buf, err = p.decodesTo(old, names, buf); !same || err != nil {
			return fmt.Errorf("the conversion to schema version %s changed the response %q "+
				"(row %d) to %s (%v)", schemaVersion, old.text[colID], old.rowid, p, err)
		}
	}
	if err := olds.Err(); err != nil {
		return err
	}
	if err := news.Err(); err != nil {
		return err
	}
	return checkSlimPending(ctx, tx, names)
}

// checkSlimTotals checks that the converted tables hold as many rows as the
// version 6 ones and the same sum of each token column per tool.
func checkSlimTotals(ctx context.Context, tx *sql.Tx) error {
	for _, query := range []string{
		`SELECT (SELECT COUNT(*) FROM responses_6), (SELECT COUNT(*) FROM responses)`,
		`SELECT (SELECT COUNT(*) FROM tier_pending_6), (SELECT COUNT(*) FROM tier_pending)`,
	} {
		var before, after int64
		if err := tx.QueryRowContext(ctx, query).Scan(&before, &after); err != nil {
			return err
		}
		if before != after {
			return fmt.Errorf("the conversion to schema version %s kept %d of %d rows",
				schemaVersion, after, before)
		}
	}
	sums := func(query string) (map[string][5]int64, error) {
		rows, err := tx.QueryContext(ctx, query)
		if err != nil {
			return nil, err
		}
		defer func() { _ = rows.Close() }()
		out := map[string][5]int64{}
		for rows.Next() {
			var tool string
			var s [5]int64
			if err := rows.Scan(&tool, &s[0], &s[1], &s[2], &s[3], &s[4]); err != nil {
				return nil, err
			}
			out[tool] = s
		}
		return out, rows.Err()
	}
	const totals = `SUM(input), SUM(output), SUM(cache_write_5m), SUM(cache_write_1h), SUM(cache_read)`
	before, err := sums(`SELECT tool, ` + totals + ` FROM responses_6 GROUP BY tool`)
	if err != nil {
		return err
	}
	after, err := sums(`SELECT n.name, ` + totals + ` FROM responses
	  JOIN names n ON n.id = responses.tool GROUP BY n.name`)
	if err != nil {
		return err
	}
	if !maps.Equal(before, after) {
		return fmt.Errorf("the conversion to schema version %s changed the token sums per tool "+
			"from %v to %v", schemaVersion, before, after)
	}
	return nil
}

// checkSlimPending compares every converted tier_pending row, decoded, with
// its old one.
func checkSlimPending(ctx context.Context, tx *sql.Tx, names []nameKey) error {
	rows, err := tx.QueryContext(ctx, "SELECT request_id, root FROM tier_pending")
	if err != nil {
		return err
	}
	converted := map[string]string{}
	for rows.Next() {
		var packed []byte
		var root int64
		if err := rows.Scan(&packed, &root); err != nil {
			_ = rows.Close()
			return err
		}
		id, err := unpackID(packed)
		if err != nil || root < 1 || root >= int64(len(names)) || names[root].kind != nameSession {
			_ = rows.Close()
			return fmt.Errorf("the conversion to schema version %s wrote a waiting row "+
				"it cannot read (%x, %d)", schemaVersion, packed, root)
		}
		converted[id] = names[root].name
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if err := rows.Err(); err != nil {
		return err
	}
	rows, err = tx.QueryContext(ctx, "SELECT request_id, root FROM tier_pending_6")
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var id, root string
		if err := rows.Scan(&id, &root); err != nil {
			return err
		}
		if got, ok := converted[id]; !ok || got != root {
			return fmt.Errorf("the conversion to schema version %s changed the waiting row %q "+
				"from %q to %q", schemaVersion, id, root, got)
		}
	}
	return rows.Err()
}

// packedRow is a response as responseSchema stores it, as checkSlim reads it
// beside its old row: its id, its packed key, and the columns after it in the
// order of packedColumns (the text columns one place before their col
// constant, then the five token counts), each NULL when there is no such row.
type packedRow struct {
	rowid sql.NullInt64
	id    sql.RawBytes
	cols  [colTime + 5]sql.NullInt64
}

func (p *packedRow) fields() []any {
	out := []any{&p.rowid, &p.id}
	for i := range p.cols {
		out = append(out, &p.cols[i])
	}
	return out
}

func (p packedRow) String() string {
	cols := make([]string, len(p.cols))
	for i, c := range p.cols {
		cols[i] = "NULL"
		if c.Valid {
			cols[i] = strconv.FormatInt(c.Int64, 10)
		}
	}
	return fmt.Sprintf("%d %x %s", p.rowid.Int64, []byte(p.id), strings.Join(cols, " "))
}

// decodesTo reports whether the row, read through names, is old field for
// field. buf is room for a time's text, returned for the next call.
func (p packedRow) decodesTo(old textRow, names []nameKey, buf []byte) (bool, []byte, error) {
	if !p.rowid.Valid || p.rowid.Int64 != old.rowid {
		return false, buf, nil
	}
	id, err := unpackID(p.id)
	if err != nil || id != string(old.text[colID]) {
		return false, buf, err
	}
	for col := colTool; col <= colSubscription; col++ {
		v := p.cols[col-1]
		if !v.Valid || v.Int64 < 1 || v.Int64 >= int64(len(names)) {
			return false, buf, nil
		}
		if name := names[v.Int64]; name.kind != nameKinds[col] ||
			name.name != string(old.text[col]) {
			return false, buf, nil
		}
	}
	for _, col := range []int{colAccountSource, colSubscriptionSource} {
		v := p.cols[col-1]
		if !v.Valid || v.Int64 < 0 || v.Int64 >= int64(len(evidenceLevels)) ||
			evidenceLevels[v.Int64] != string(old.text[col]) {
			return false, buf, nil
		}
	}
	switch ts := p.cols[colTime-1]; {
	case !ts.Valid:
		if len(old.text[colTime]) > 0 {
			return false, buf, nil
		}
	default:
		buf = time.UnixMilli(ts.Int64).UTC().AppendFormat(buf[:0], timeLayout)
		if !bytes.Equal(buf, old.text[colTime]) {
			return false, buf, nil
		}
	}
	for i, n := range old.tokens {
		if v := p.cols[colTime+i]; !v.Valid || v.Int64 != n {
			return false, buf, nil
		}
	}
	return true, buf, nil
}
