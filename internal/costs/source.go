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
	Account(home string) string                 // who was signed in, or "unknown"
	Builtin() RateCard                          // prices shipped with Workbench
	PricingURL() string                         // official price page, "" when none
	ParsePricing(page string) RateCard          // that page's prices
	Observations(
		home string,
	) []Observation // the tool's own (tokens, cost) pairs, for calibration; nil when none
	Hooks(home string) map[string]string // hook event → a Hook* state

	// Session reports whether file belongs to the session whose transcript is
	// transcript (the SessionEnd hook passes it), so that session's rows are
	// tagged "session" and not "sweep". A session may span several files.
	Session(file, transcript string) bool
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
// for the thread's own turns, rather than when it was selected.
type TierChange struct {
	Thread string
	Time   time.Time
	Tier   string
	Turn   bool
}

// SplitTier splits "gpt-5.6-sol@fast" into the model and its service tier:
// the word after the last "@", a lowercase letter then lowercase letters,
// digits, "_" or "-". An id without one is all model, tier "", so a dated
// snapshot such as "claude-sonnet-4@20250514" keeps its date.
func SplitTier(model string) (base, tier string) {
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
