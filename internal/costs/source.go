// Package costs records what AI coding tools spend in a durable local ledger
// and reports it. Everything that differs per tool lives in a [Source]; the
// ledger, ingest, rates and report code never parse a transcript.
package costs

import (
	"slices"
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
	Hooks(home string) map[string]bool // hook event → whether the ingest hook is installed

	// Session reports whether file belongs to the session whose transcript is
	// transcript (the SessionEnd hook passes it), so that session's rows are
	// tagged "session" and not "sweep". A session may span several files.
	Session(file, transcript string) bool
}

// Usage is one billed response, whatever tool produced it.
type Usage struct {
	Tool, RequestID, Model, Project, Session, Account    string
	Time                                                 time.Time
	Input, Output, CacheWrite5m, CacheWrite1h, CacheRead int64

	// TierFrom is the root thread whose service tier prices this row when
	// the row's own transcript names none (a Codex subagent's); "" otherwise.
	TierFrom string
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

	cache any // the source's decoded Saved during one read
}

// TierChange is a thread switching service tier at a time; Tier is the
// stored tier name, "" for standard.
type TierChange struct {
	Thread string
	Time   time.Time
	Tier   string
}

// tiers are the service tiers a model id can carry after "@"; each has its
// own price table.
var tiers = []string{"fast", "flex", "ultrafast"}

// SplitTier splits "gpt-5.6-sol@fast" into the model and its service tier.
// An id without a known tier suffix is all model, tier "".
func SplitTier(model string) (base, tier string) {
	if i := strings.LastIndex(model, "@"); i >= 0 && slices.Contains(tiers, model[i+1:]) {
		return model[:i], model[i+1:]
	}
	return model, ""
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
	Name   string // "claude", "codex": the ledger's tool column and --tool
	Title  string // "Claude Code", "Codex": the tab label
	Source Source
}

// Tools is every tab, in order.
var Tools = []Tool{
	{Name: "claude", Title: "Claude Code", Source: claude{}},
	{Name: "codex", Title: "Codex", Source: codex{}},
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
