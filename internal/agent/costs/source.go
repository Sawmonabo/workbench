// Package costs records what AI coding tools spend in a durable local ledger
// and reports it. Everything that differs per tool lives in a [Source]; the
// ledger, ingest, rates and report code never parse a transcript.
package costs

import (
	"encoding/json"
	"errors"
	"time"

	"github.com/Sawmonabo/workbench/internal/agent/toolconfig"
)

// Source is one AI tool whose transcripts the ledger records. Everything a
// source knows is about its own files and prices; it never writes.
type Source interface {
	Name() string                              // "claude", "codex"
	Transcripts(home string) ([]string, error) // files to ingest
	// TranscriptRoots are the folders the transcripts live in, so a hook's
	// transcript path names the tool it fired for.
	TranscriptRoots(home string) []string
	Parse(line []byte, file *FileState) []Usage // usage rows in one line
	SignIn(
		home string,
	) SignIn // the tool's current sign-in; the zero SignIn when none
	// Accounts maps each id by which the tool's transcripts name an account
	// (Usage.AccountKey) to the email of the sign-in that holds it, for every
	// sign-in the tool keeps, not only the current one. Ingest uses it for
	// rows whose transcript names the account.
	Accounts(home string) map[string]string
	Builtin() RateCard                 // prices shipped with Workbench
	PricingURL() string                // official price page, "" when none
	ParsePricing(page string) RateCard // that page's prices
	Observations(
		home string,
	) []Observation // the tool's own (tokens, cost) pairs, for calibration; nil when none
}

// SignIn is who a tool is signed in as, and what pays for it: the account (an
// email, "unknown" when there is none), the subscription (a stable id such as
// "claude:<organization uuid>" or "codex:plus", "api-key" for a Codex API key, ""
// when unknown) and the label the report shows for that subscription ("Max",
// "Team (Example Org)", "Plus"; "" when the id says it all). The zero SignIn
// means nothing is known.
type SignIn struct {
	Account, Subscription, Label string
}

// signInDirectory is a source that keeps earlier sign-ins on disk: SignIns
// is each of them and the current one, oldest first. ResolveAccounts learns
// from them which email holds a subscription that names an account
// (Tool.OrgAccount), as it does from the sign-ins the ledger recorded.
type signInDirectory interface {
	SignIns(home string) []SignIn
}

// labelDirectory is a source whose transcripts name a subscription by id but
// not its label: Labels maps each id a sign-in on disk holds to the label the
// report shows. Ingest uses it for rows whose transcript names the
// subscription.
type labelDirectory interface {
	Labels(home string) map[string]string
}

// reparser is a source whose parser can learn to read more from lines it has
// already read. When ParseRevision differs from the one the ledger stored,
// ingest forgets where it stopped in the tool's transcripts and reads them
// again from the start, once; a re-read rewrites the same rows, with the
// stronger evidence it now finds.
type reparser interface {
	ParseRevision() string
}

// markSource is a source that keeps account marks outside its transcripts
// (Claude Code's prompt history and saved sign-ins); ingest stores them each
// run. Marks returns what it found, and an error when it couldn't read all of
// where they are kept.
type markSource interface {
	Marks(home string) ([]AccountMark, error)
}

// finisher is a source that saves its state once a read of a file ends,
// instead of after each line that changes it.
type finisher interface {
	Finish(file *FileState)
}

// tierServer is a source whose tool may send a request at another tier than
// the one selected for it: ServedTier says which, for a model and a selected
// tier ("" for standard).
type tierServer interface {
	ServedTier(home string) func(model, tier string) string
}

// skipCounter is a source with transcripts it cannot read (Codex's
// compressed rollouts): Skipped counts them, for the status.
type skipCounter interface {
	Skipped(home string) int
}

// Usage is one billed response, whatever tool produced it.
type Usage struct {
	Tool, RequestID, Model, Project, Session, Account    string
	Time                                                 time.Time
	Input, Output, CacheWrite5m, CacheWrite1h, CacheRead int64

	// TierFrom is where a row whose tier another thread decides takes it
	// from (a Codex subagent's); "" otherwise. A thread id prices it at that
	// thread's selected tier at the row's time; TurnTierFrom prices it at a
	// thread's running-turn tier at another time.
	TierFrom string

	// Subscription is the stable id of what paid for the row, and
	// SubscriptionLabel its display label (see SignIn); a source sets them
	// only when its transcript names the plan (Codex's plan_type, Claude Code's
	// organization), else ingest fills them from a sign-in. Ingest fills an
	// empty label through labelDirectory. A parser's Subscription is final: re-attribution never
	// changes the subscription of a row whose SubscriptionEvidence is EvidenceTranscript.
	Subscription, SubscriptionLabel string

	// Root is the session a hook names for the row: Claude's sessionId (the
	// same for a subagent's file), Codex's root thread, or for an older
	// subagent the thread that spawned it (its file names no more); the ledger
	// walks ThreadParent links from that to the root. "" when unknown; the
	// ledger matches session bindings on the root.
	Root string

	// AccountKey is the tool's own id of the account that answered, when the
	// transcript names it (Codex's creator_account_id, the ChatGPT account id;
	// Claude Code's credential_org, the organization uuid), "" otherwise.
	// Ingest resolves it to an email through Source.Accounts.
	AccountKey string

	// SubscriptionEvidence is "transcript" when the source decided the
	// subscription from the transcript itself, "" otherwise. AccountEvidence is
	// the same for the account, set by ingest when the transcript's AccountKey
	// names a sign-in. Ingest and the ledger assign every other level, each
	// field apart: a transcript can name the plan and not the email.
	SubscriptionEvidence, AccountEvidence string

	// CopyOf, when set, is "<thread>@<time>": the row may repeat a response
	// of that thread written before that time, under another key. It is
	// stored apart and dropped once that thread's transcript is read past
	// that time; until then it is the only trace of the response.
	CopyOf string
}

// FileState is what a source keeps between the lines of one transcript, and,
// through Saved, between ingest runs.
type FileState struct {
	Path     string // the transcript
	Fallback string // project decoded from the path, for records without a cwd
	LastCwd  string // Claude: the last cwd seen in this file

	// Saved is the source's own state at the stored offset; ingest stores it
	// with the offset and hands it back when it resumes the file.
	Saved []byte
	// Tiers are the service tier changes read this run; ingest stores them.
	Tiers []TierChange
	// Parents are the spawning links read this run; ingest stores them.
	Parents []ThreadParent
	// Marks are the account marks read this run; ingest stores them.
	Marks []AccountMark

	cache any // the source's decoded Saved during one read
}

// Tool is one tab of the report. A nil Source is a tool Workbench knows of
// but does not record yet: its tab says so, and ingest skips it.
type Tool struct {
	Name        string // "claude", "codex": the ledger's tool column and --tool
	Title       string // "Claude Code", "Codex": the tab label
	Source      Source
	CacheWrites []string // the report's cache-write column heads: one per cache lifetime the tool bills
	PriceNote   string   // what the figures are, under the total
	PricePage   string   // whose price page PricingURL is, for the unpriced note
	// SessionPlan is where a row whose transcript names no subscription takes
	// the one its session's transcript names (fillSessionPlans): "next", the
	// first named at or after it; "earlier", the latest named at or before
	// it; "" nowhere.
	SessionPlan string
	// OrgAccount is whether a subscription the tool's transcript names is an
	// account of its own: Claude Code's credential_org names the organization
	// that answered, a sign-in's own (or a team's), so a row so named takes the
	// email of a sign-in to that organization, never that of whichever sign-in
	// was current when the row was made or read (ResolveAccounts). A Codex plan
	// is shared by every account on it.
	OrgAccount bool
	// Tiers is whether the tool's model ids carry a service tier after "@",
	// which only its source composes; any other tool's ids are read as they
	// are, so a vendor id such as "foo@latest" is never split.
	Tiers bool
	// Hooks is a toolconfig Hook* state for each of hooks, as the tool's own
	// config runs them (toolconfig.ClaudeHooks, toolconfig.CodexHooks).
	Hooks func(home string, hooks []toolconfig.HookCommand) []string
	// Move reports a variable that moves the tool's config folder away from
	// the one Workbench installs in (toolconfig.ClaudeMove,
	// toolconfig.CodexMove); nil when no variable can.
	Move func(home string) (toolconfig.Move, bool)
}

// SplitModel splits a model id of the tool into its model and service tier;
// the whole id with tier "" when the tool's ids carry no tier.
func (t Tool) SplitModel(model string) (base, tier string) {
	if !t.Tiers {
		return model, ""
	}
	return splitTier(model)
}

// The values of Tool.SessionPlan.
const (
	planNext    = "next"
	planEarlier = "earlier"
)

// Tools is every tab, in order.
var Tools = []Tool{
	{
		Name: "claude", Title: "Claude Code", Source: claude{},
		CacheWrites: []string{"cache 5m", "cache 1h"},
		PriceNote:   "API list-price equivalent, not a subscription bill",
		PricePage:   "Anthropic's price page",
		// A subagent runs in its parent's process, under the credential the
		// parent's latest credential_org names, and its own file may name none.
		SessionPlan: planEarlier,
		OrgAccount:  true,
		Tiers:       true, // fast mode: "claude-opus-5@fast"
		Hooks:       toolconfig.ClaudeHooks,
		Move:        toolconfig.ClaudeMove,
	},
	{
		Name: "codex", Title: "Codex", Source: codex{},
		CacheWrites: []string{"cache write"},
		PriceNote:   "OpenAI API list-price equivalent, not a ChatGPT plan bill",
		PricePage:   "OpenAI's price page",
		Tiers:       true,
		// rate_limits, which names the plan, can be null in a thread's first
		// token counts; the thread names its plan a few responses later.
		SessionPlan: planNext,
		Hooks:       toolconfig.CodexHooks,
		Move:        toolconfig.CodexMove,
	},
}

// Lookup returns the tool named name.
func Lookup(name string) (Tool, bool) {
	for _, tool := range Tools {
		if tool.Name == name {
			return tool, true
		}
	}
	return Tool{}, false
}

// decode fills v from raw, tolerating fields of the wrong type (they stay
// zero) but not a syntax error.
func decode(raw []byte, v any) bool {
	err := json.Unmarshal(raw, v)
	var wrong *json.UnmarshalTypeError
	return err == nil || errors.As(err, &wrong)
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
