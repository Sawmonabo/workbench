// Package costs records what AI coding tools spend in a durable local ledger
// and reports it. Everything that differs per tool lives in a [Source]; the
// ledger, ingest, rates and report code never parse a transcript.
package costs

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/Sawmonabo/workbench/internal/operation"
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
// "claude:<organization uuid>" or "codex:plus", "api-key" for a Codex API key, ""
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

// The evidence the account (email) and the subscription of a row each rest on,
// strongest first; the ledger stores them as account_source and
// subscription_source. A stronger copy of a row replaces a weaker one for that
// field, an equal one keeps the stored value.
const (
	// the transcript named it (Codex: the plan, the creator account; Claude
	// Code: the organization)
	EvidenceTranscript = "transcript"
	EvidenceSession    = "session"  // the sign-in a hook bound to the row's root session
	EvidenceObserved   = "observed" // the sign-in observed on both sides of the row's time
	// the account the tool's records put in the row's login stretch, which no
	// record of the row itself names (accountBasis.inferred)
	EvidenceInferred = "inferred"
	EvidenceUnknown  = "unknown" // no evidence (a row keeps an email an earlier build stored, which no report shows)
)

// EvidenceRank orders the evidence levels: 4 transcript, 3 session, 2
// observed, 1 inferred, 0 unknown or anything else.
func EvidenceRank(evidence string) int {
	switch evidence {
	case EvidenceTranscript:
		return 4
	case EvidenceSession:
		return 3
	case EvidenceObserved:
		return 2
	case EvidenceInferred:
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

// ingestCommand matches the ingest hook's command as Workbench installs it:
// the workbench executable, by name, by path or as a single-quoted path, then
// "costs ingest" and nothing after. "claude-costs ingest", the replaced
// script, is not it.
var ingestCommand = regexp.MustCompile(`(^|/)workbench'? costs ingest$`)

// isIngestHook reports whether command is the ingest hook's command.
func isIngestHook(command string) bool { return ingestCommand.MatchString(command) }

// ConfigMove is a tool's config folder moved by a variable away from the one
// Workbench installs the tool's settings, instructions and hooks in, so the
// tool reads none of them. Two folders side by side, one per account, are the
// variable's documented use, so Workbench names the move and installs nothing
// in the other folder. It also names a settings file that sets the variable
// to a relative path, which Workbench ignores.
type ConfigMove struct {
	Tool     string `json:"tool"`     // "Claude Code", "Codex"
	Variable string `json:"variable"` // CLAUDE_CONFIG_DIR, CODEX_HOME
	// Source is "the environment", the settings file that sets it, or "" when
	// it moves nothing.
	Source   string            `json:"source"`
	Dir      string            `json:"dir"`                // the folder the tool reads
	Default  string            `json:"default"`            // the folder Workbench installs in
	Relative []RelativeSetting `json:"relative,omitempty"` // settings files that set it to a relative path
}

// RelativeSetting is a settings file that sets a folder variable to a path
// that is not absolute.
type RelativeSetting struct {
	File  string `json:"file"`
	Value string `json:"value"`
}

// configMover is a source whose config folder a variable can move.
type configMover interface {
	ConfigMove(home string) (ConfigMove, bool)
}

// ConfigMoves is each tool's config folder that a variable moved (see
// [ConfigMove]).
func ConfigMoves(home string) []ConfigMove {
	var moves []ConfigMove
	for _, tool := range Tools {
		if mover, ok := tool.Source.(configMover); ok {
			if move, moved := mover.ConfigMove(home); moved {
				moves = append(moves, move)
			}
		}
	}
	return moves
}

// Explain says what the move means and how to undo it, the same in the plan,
// doctor and costs status.
func (m ConfigMove) Explain(home string) string {
	var parts []string
	if m.Source != "" {
		where, fix := m.Source, "unset it, or set it to "+operation.ShowPath(home, m.Default)
		if m.Source != "the environment" {
			where, fix = operation.ShowPath(
				home,
				m.Source,
			), "remove it from that file, or set it there to "+m.Default
		}
		parts = append(parts, m.Variable+" in "+where+" points "+m.Tool+" at "+
			operation.ShowPath(home, m.Dir)+", so it does not read the settings, instructions "+
			"and costs and guard hooks Workbench installs in "+operation.ShowPath(home, m.Default)+
			"; "+fix+", for them to apply")
	}
	for _, r := range m.Relative {
		parts = append(parts, fmt.Sprintf(
			"%s in %s is %q, not an absolute path: %s copies it as written and reads it from "+
				"the folder it starts in, so Workbench ignores it and reads %s; write the "+
				"absolute path there",
			m.Variable, operation.ShowPath(home, r.File), r.Value, m.Tool,
			operation.ShowPath(home, m.Dir),
		))
	}
	return strings.Join(parts, "; ")
}

// sameDir reports whether a and b name the same folder: the same file when
// both exist, else the same absolute path.
func sameDir(a, b string) bool {
	aInfo, aErr := os.Stat(a)
	bInfo, bErr := os.Stat(b)
	if aErr == nil && bErr == nil {
		return os.SameFile(aInfo, bInfo)
	}
	absolute := func(path string) string {
		if abs, err := filepath.Abs(path); err == nil {
			return abs
		}
		return filepath.Clean(path)
	}
	return absolute(a) == absolute(b)
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
	// Ingest resolves it to an email through accountDirectory.
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
	// Parents are the spawning links read this run; ingest stores them.
	Parents []ThreadParent
	// Marks are the account marks read this run; ingest stores them.
	Marks []AccountMark

	cache any // the source's decoded Saved during one read
}

// The kinds of AccountMark.
const (
	MarkLogin   = "login"   // a sign-in happened at Time
	MarkAccount = "account" // the tool's own id of the signed-in account was Account at Time
	MarkOrg     = "org"     // the signed-in organization was Org at Time
	MarkPair    = "pair"    // a sign-in on disk holds account id Account in organization Org
)

// AccountMark is a record of who was signed in, for rows whose own records
// name no account: a sign-in that starts a login stretch, an account id or an
// organization seen during one, or a sign-in that pairs an account id with
// its organization. ResolveAccounts infers a stretch's account from its marks
// (accountBasis.inferred). Time is zero for a MarkPair.
type AccountMark struct {
	Kind         string
	Time         time.Time
	Account, Org string
}

// markSource is a source that keeps account marks outside its transcripts
// (Claude Code's prompt history and saved sign-ins); ingest stores them each
// run.
type markSource interface {
	Marks(home string) []AccountMark
}

// finisher is a source that saves its state once a read of a file ends,
// instead of after each line that changes it.
type finisher interface {
	Finish(file *FileState)
}

// ThreadParent is a thread the file names as spawned by another: its
// immediate parent, which is not always the root. The ledger walks these links
// to the root thread that a hook binds a session under.
type ThreadParent struct{ Thread, Parent string }

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
