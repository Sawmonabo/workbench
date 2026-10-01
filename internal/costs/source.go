// Package costs records what AI coding tools spend in a durable local ledger
// and reports it. Everything that differs per tool lives in a [Source]; the
// ledger, ingest, rates and report code never parse a transcript.
package costs

import (
	"strings"
	"time"
)

// Source is one AI tool whose transcripts the ledger records. Everything a
// source knows is about its own files and prices; it never writes.
type Source interface {
	Name() string                               // "claude", "codex"
	Transcripts(home string) ([]string, error)  // files to ingest
	Parse(line []byte, file *FileState) []Usage // usage rows in one line
	SignIn(
		home string,
	) SignIn // the tool's current sign-in; the zero SignIn when none
	Builtin() RateCard                 // prices shipped with Workbench
	PricingURL() string                // official price page, "" when none
	ParsePricing(page string) RateCard // that page's prices
	Observations(
		home string,
	) []Observation // the tool's own (tokens, cost) pairs, for calibration; nil when none
	Hooks(home string) map[string]string // hook event → a Hook* state
}

// SignIn is who a tool is signed in as, and what pays for it: the account (an
// email, "unknown" when there is none), the subscription (a stable id such as
// "claude:<organization uuid>" or "codex:plus", "api-key" for an API key, ""
// when unknown) and the label the report shows for that subscription ("Max",
// "Team (Example Org)", "Plus"; "" when the id says it all). The zero SignIn
// means nothing is known.
type SignIn struct {
	Account, Subscription, Label string
}

// accountDirectory is a source whose transcripts name an account by the tool's
// own id (Usage.AccountKey): Accounts maps each such id to the email of the
// sign-in that holds it, for every sign-in the tool keeps, not only the
// current one. Ingest uses it for rows whose transcript names the account.
type accountDirectory interface {
	Accounts(home string) map[string]string
}

// The evidence a row's account and subscription rest on, strongest first; the
// ledger stores it as account_source. A stronger copy of a row replaces a
// weaker one, an equal one keeps the stored value.
const (
	EvidenceTranscript = "transcript" // the transcript named them (Codex: plan and creator account)
	EvidenceSession    = "session"    // the sign-in a hook bound to the row's root session
	EvidenceObserved   = "observed"   // the sign-in observed on both sides of the row's time
	EvidenceUnknown    = "unknown"    // no evidence
)

// EvidenceRank orders the evidence levels: 3 transcript, 2 session, 1 observed,
// 0 unknown or anything else.
func EvidenceRank(evidence string) int {
	switch evidence {
	case EvidenceTranscript:
		return 3
	case EvidenceSession:
		return 2
	case EvidenceObserved:
		return 1
	default:
		return 0
	}
}

// The states of a tool's ingest hook for one event, as Hooks reports them.
const (
	HookOK        = "ok"        // installed, and the tool runs it
	HookMissing   = "missing"   // not installed as Workbench installs it
	HookUntrusted = "untrusted" // installed, but the tool has not recorded it as trusted
	HookDisabled  = "disabled"  // installed and trusted, but turned off in the tool
)

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
	// only when its transcript names the plan (Codex), else ingest fills them
	// from a sign-in. A parser's Subscription is final: re-attribution never
	// changes the subscription of a row whose Evidence is EvidenceTranscript.
	Subscription, SubscriptionLabel string

	// Root is the session a hook names for the row: Claude's sessionId (the
	// same for a subagent's file), Codex's root thread. "" when unknown; the
	// ledger matches session bindings on it.
	Root string

	// AccountKey is the tool's own id of the account that answered, when the
	// transcript names it (Codex's creator_account_id, the ChatGPT account id),
	// "" otherwise. Ingest resolves it to an email through accountDirectory.
	AccountKey string

	// Evidence is "transcript" when the source decided the subscription from
	// the transcript itself, "" otherwise. Ingest and the ledger assign every
	// other level.
	Evidence string

	// CopyOf, when set, is "<thread>@<time>": the row may repeat a response
	// of that thread written before that time, under another key. It is
	// stored apart and dropped once that thread's transcript is read past
	// that time; until then it is the only trace of the response.
	CopyOf string
}

// The ledger keeps two series of tier changes per thread: when a selected
// tier was set (keyed by the thread id), and when it took effect for the
// thread's own turns (keyed turnTierKey(thread)).
const turnTierPrefix = "turn:"

func turnTierKey(thread string) string { return turnTierPrefix + thread }

// TurnTierFrom is the TierFrom of a row priced at thread's running-turn tier
// at time at (ledger layout), such as a subagent's tier fixed when it was
// spawned.
func TurnTierFrom(thread, at string) string { return turnTierKey(thread) + "@" + at }

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

	cache any // the source's decoded Saved during one read
}

// TierChange is a thread switching service tier at a time; Tier is the
// stored tier name, "" for standard. Turn marks the time the tier took effect
// for the thread's own turns, rather than when it was selected. From, when
// set, is a TierFrom whose tier the thread takes instead of Tier.
type TierChange struct {
	Thread string
	Time   time.Time
	Tier   string
	Turn   bool
	From   string
}

// splitTier splits "gpt-5.6-sol@fast", an id a Tiers tool's source composed,
// into the model and its service tier:
// the word after the last "@", a lowercase letter then lowercase letters,
// digits, "_" or "-". An id without one is all model, tier "", so a dated
// snapshot such as "claude-sonnet-4@20250514" keeps its date.
func splitTier(model string) (base, tier string) {
	if i := strings.LastIndex(model, "@"); i >= 0 && isTier(model[i+1:]) {
		return model[:i], model[i+1:]
	}
	return model, ""
}

// isTier reports whether name can be a service tier in a model id.
func isTier(name string) bool {
	for i, c := range name {
		switch {
		case c >= 'a' && c <= 'z':
		case i > 0 && (c >= '0' && c <= '9' || c == '_' || c == '-'):
		default:
			return false
		}
	}
	return name != ""
}

// Rate is USD per million tokens for each token kind.
type Rate struct {
	Input, Output, CacheWrite5m, CacheWrite1h, CacheRead float64
	Source                                               string // "override", "official", "calibrated" or "builtin"
}

// RateCard maps a model prefix to its rate.
type RateCard map[string]Rate

// Observation is one (tokens, cost) pair a tool recorded itself, for
// calibration; tokens are in millions: input, output, cache write, cache read.
type Observation struct {
	Model  string
	Tokens [4]float64
	Cost   float64
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
	// Tiers is whether the tool's model ids carry a service tier after "@",
	// which only its source composes; any other tool's ids are read as they
	// are, so a vendor id such as "foo@latest" is never split.
	Tiers bool
}

// SplitModel splits a model id of the tool into its model and service tier;
// the whole id with tier "" when the tool's ids carry no tier.
func (t Tool) SplitModel(model string) (base, tier string) {
	if !t.Tiers {
		return model, ""
	}
	return splitTier(model)
}

// Tools is every tab, in order.
var Tools = []Tool{
	{
		Name: "claude", Title: "Claude Code", Source: claude{},
		CacheWrites: []string{"cache 5m", "cache 1h"},
		PriceNote:   "API list-price equivalent, not a subscription bill",
		PricePage:   "Anthropic's price page",
	},
	{
		Name: "codex", Title: "Codex", Source: codex{},
		CacheWrites: []string{"cache write"},
		PriceNote:   "OpenAI API list-price equivalent, not a ChatGPT plan bill",
		PricePage:   "OpenAI's price page",
		Tiers:       true,
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
