package costs

import (
	"context"
	"database/sql"

	"github.com/Sawmonabo/workbench/internal/operation"
	"github.com/Sawmonabo/workbench/internal/sqlitefile"
)

const (
	schemaVersion = "7"

	// firstTool is the tool of every row written before the tool column
	// existed, and the column's default: its request IDs are stored as they
	// are, every other tool's as <tool>:<id>.
	firstTool = "claude"
)

// responseSchema holds the responses, each name they repeat once in names,
// and the rows waiting for their price tier. A response refers to its tool,
// model, project, session, root, account and subscription by the id of a
// name of that kind (nameTool and the rest; the root is a session), so a
// row reads back as exactly the text it was given, "" and "unknown"
// included. Its request_id is its ledger key packed (packID), account_source
// and subscription_source are the EvidenceRank of each field's evidence, and
// ts is its time in Unix milliseconds, UTC, NULL when it has none.
// tier_pending.root is the row's TierFrom, a name of the session kind.
const responseSchema = `
CREATE TABLE names (
  id   INTEGER PRIMARY KEY,
  kind INTEGER NOT NULL,
  name TEXT NOT NULL,
  UNIQUE (kind, name)
);
CREATE TABLE responses (
  id                  INTEGER PRIMARY KEY,
  request_id          BLOB NOT NULL UNIQUE,
  tool                INTEGER NOT NULL,
  model               INTEGER NOT NULL,
  project             INTEGER NOT NULL,
  session             INTEGER NOT NULL,
  root                INTEGER NOT NULL,
  account             INTEGER NOT NULL,
  subscription        INTEGER NOT NULL,
  account_source      INTEGER NOT NULL,
  subscription_source INTEGER NOT NULL,
  ts                  INTEGER,
  input               INTEGER NOT NULL,
  output              INTEGER NOT NULL,
  cache_write_5m      INTEGER NOT NULL,
  cache_write_1h      INTEGER NOT NULL,
  cache_read          INTEGER NOT NULL
);
CREATE TABLE tier_pending (
  request_id BLOB PRIMARY KEY,
  root       INTEGER NOT NULL
) WITHOUT ROWID;
`

// responseIndexes are the indexes of responses beside its request_id: the rows
// of a thread root, and the rows whose account or plan is still open to
// correction by time, which the account resolution reads (accountsQuery
// states openRows in the same words, or SQLite cannot use the index).
const responseIndexes = `
CREATE INDEX responses_root ON responses (tool, root);
CREATE INDEX responses_open ON responses (tool, ts) WHERE ` + openRows + `;
`

// openRows is true for a row with a field its transcript did not decide.
const openRows = "account_source < 4 OR subscription_source < 4"

// The kinds of names.
const (
	nameTool = iota + 1
	nameModel
	nameProject
	nameSession // a session, a thread root or a TierFrom
	nameAccount
	nameSubscription
)

const schema = responseSchema + responseIndexes + `
CREATE TABLE files (
  path      TEXT PRIMARY KEY,
  offset    INTEGER NOT NULL,
  size      INTEGER NOT NULL,
  mtime_ns  INTEGER NOT NULL,
  last_seen TEXT NOT NULL,
  state     TEXT NOT NULL DEFAULT '',
  head      TEXT NOT NULL DEFAULT ''
);
CREATE TABLE tier_changes (
  thread_id TEXT NOT NULL,
  ts        TEXT NOT NULL,
  tier      TEXT NOT NULL,
  PRIMARY KEY (thread_id, ts, tier)
);
CREATE TABLE meta (key TEXT PRIMARY KEY, value TEXT NOT NULL);
` + subscriptionSchema + markSchema + priceSchema

// subscriptionSchema is the part of the schema that attributes rows to a
// subscription, also created by the upgrade of an older ledger. Every time is
// in timeLayout text; a response's time meets them as that text (storedTime).
//
// responses.account_source holds the evidence level of account (the email)
// and responses.subscription_source that of subscription (EvidenceTranscript,
// EvidenceSession, EvidenceObserved, EvidenceInferred or EvidenceUnknown, by
// rank): each is decided on its own, as a transcript can name a plan without
// naming an account. responses.subscription is a subscription id ("unknown"
// when none) and responses.root the session a hook names for the row.
//
//   - session_accounts: the sign-in a hook found when a session started (or
//     resumed) at since, for the root session of a tool; a row of that
//     session takes the binding with the latest since at or before its time.
//   - sign_ins: the sign-in an ingest run observed for a tool at a time.
//   - subscriptions: the display label of each subscription id.
//   - thread_parents: the thread a Codex subagent was spawned by, as its own
//     file names it; ResolveAccounts walks them so a row's root is the root
//     thread however deep the subagent is and in whatever order files are read.
const subscriptionSchema = `
CREATE TABLE session_accounts (
  tool         TEXT NOT NULL,
  session      TEXT NOT NULL,
  since        TEXT NOT NULL,
  account      TEXT NOT NULL,
  subscription TEXT NOT NULL,
  PRIMARY KEY (tool, session, since)
);
CREATE TABLE sign_ins (
  tool         TEXT NOT NULL,
  at           TEXT NOT NULL,
  account      TEXT NOT NULL,
  subscription TEXT NOT NULL,
  PRIMARY KEY (tool, at)
);
CREATE TABLE subscriptions (id TEXT PRIMARY KEY, label TEXT NOT NULL);
CREATE TABLE thread_parents (
  tool   TEXT NOT NULL,
  thread TEXT NOT NULL,
  parent TEXT NOT NULL,
  PRIMARY KEY (tool, thread)
);
`

// markSchema keeps the account marks (AccountMark) ingest reads, which outlive
// the transcripts that hold them: at is in timeLayout, "" for a pair, and an
// unused account or org is "". ResolveAccounts infers from them the account
// of rows nothing else names (accountBasis.inferred).
const markSchema = `
CREATE TABLE account_marks (
  tool    TEXT NOT NULL,
  kind    TEXT NOT NULL,
  at      TEXT NOT NULL,
  account TEXT NOT NULL,
  org     TEXT NOT NULL,
  PRIMARY KEY (tool, kind, at, account, org)
);
`

// priceSchema keeps each tool's official price list, the one a refresh
// downloaded last (see refreshOfficial): prices holds its rows, which a
// report's price list is built from (see loadCard), and price_lists where and
// when it was downloaded, when the page was last asked, and the page's
// validators for the next conditional request. Times are seconds since the
// epoch.
const priceSchema = `
CREATE TABLE prices(
  tool           TEXT NOT NULL,   -- claude, codex
  prefix         TEXT NOT NULL,   -- a model, or a family such as claude-opus
  input          REAL NOT NULL,   -- USD per million tokens, as are the rest
  output         REAL NOT NULL,
  cache_write_5m REAL NOT NULL,
  cache_write_1h REAL NOT NULL,
  cache_read     REAL NOT NULL,
  PRIMARY KEY(tool, prefix)) WITHOUT ROWID;

CREATE TABLE price_lists(
  tool          TEXT PRIMARY KEY,
  url           TEXT NOT NULL,
  fetched_at    REAL NOT NULL,    -- when the list was last downloaded
  checked_at    REAL NOT NULL,    -- when the page was last asked
  etag          TEXT NOT NULL,
  last_modified TEXT NOT NULL) WITHOUT ROWID;
`

// create writes the schema of a ledger that was missing when OpenLedger looked.
// Two processes can both have seen it missing (the first apply's ingest and a
// session hook, say): the immediate transaction makes the second wait for the
// first, and it then finds the ledger made and leaves it as it is. A plain
// CREATE TABLE IF NOT EXISTS would instead accept a foreign database that
// happens to share a table name.
func (l *Ledger) create(ctx context.Context) error {
	return sqlitefile.Transact(ctx, l.db, func(tx *sql.Tx) error {
		var made int
		if err := tx.QueryRowContext(
			ctx,
			"SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'meta'",
		).Scan(&made); err != nil {
			return err
		}
		if made > 0 {
			// Another process created it while this one waited; it must be a
			// ledger of this version, as prepare requires of an existing one.
			var version string
			if err := tx.QueryRowContext(
				ctx, "SELECT value FROM meta WHERE key = 'schema_version'",
			).Scan(&version); err != nil || version != schemaVersion {
				return ledgerProblem(operation.ExitInvalid, l.Path,
					"a database appeared that is not a Workbench costs ledger of schema version "+
						schemaVersion+"; move it aside")
			}
			return nil
		}
		if _, err := tx.ExecContext(ctx, schema); err != nil {
			return err
		}
		return setMeta(ctx, tx, "schema_version", schemaVersion)
	})
}
