package costs

import (
	"bytes"
	"encoding/json"
	"strconv"
	"strings"
	"time"
)

// codexState is a rollout's state at the stored offset, saved between runs.
type codexState struct {
	Thread  string `json:"thread,omitempty"`   // this file's thread: the first session_meta id
	Root    string `json:"root,omitempty"`     // the root thread, when this is a subagent's file
	Parent  string `json:"parent,omitempty"`   // the thread that spawned this one, when a subagent's
	Spawn   string `json:"spawn,omitempty"`    // when this thread started: its session_meta time, ledger layout
	Turn    string `json:"turn,omitempty"`     // the running turn: the latest turn_context turn_id
	Cwd     string `json:"cwd,omitempty"`      // the latest working directory
	Model   string `json:"model,omitempty"`    // the latest turn_context model, or the first settings one
	Tier    string `json:"tier,omitempty"`     // the running turn's service tier, stored name
	TierSet bool   `json:"tier_set,omitempty"` // whether a turn of this file has run at a named tier
	Next    string `json:"next,omitempty"`     // the tier the latest snapshot selected, for the next turn
	NextSet bool   `json:"next_set,omitempty"` // whether a snapshot is waiting for the next turn
	Records bool   `json:"records,omitempty"`  // whether this file holds a token_usage_record, its own or a copy
	Version int    `json:"version,omitempty"`  // the session_meta cli_version, as major*1000+minor
	Spawned bool   `json:"spawned,omitempty"`  // whether a spawn_agent tool call started this thread
	Fork    string `json:"fork,omitempty"`     // the thread this one forked from: session_meta forked_from_id
	Copies  int    `json:"copies,omitempty"`   // a fork's history: 0 not known yet, 1 copied into this file, 2 not
	Total   string `json:"total,omitempty"`    // the last token_count running total, canonical JSON
	Plan    string `json:"plan,omitempty"`     // the latest rate_limits plan_type read in this file
	Account string `json:"account,omitempty"`  // the session_meta creator_account_id (the ChatGPT account id)
}

// Codex versions, as codexState.Version holds them.
const (
	// codexRecords is the first Codex that writes a token_usage_record for
	// every response it records usage for (core session
	// record_observed_response_completed, before every token info update in
	// turn.rs and compact.rs), so its token_count lines never add a response.
	codexRecords = 153
	// codexRootTier is the first Codex whose spawned subagents send every
	// request at their root's selected tier (openai/codex dc2ccc6843 "Make
	// subagents follow the root service tier", first tagged in 0.152.0).
	codexRootTier = 152
)

// codexVersion reads "0.152.1" or "0.160.0-alpha.3" as major*1000+minor; 0
// when unreadable.
func codexVersion(text string) int {
	parts := strings.SplitN(text, ".", 3)
	if len(parts) < 2 {
		return 0
	}
	major, err1 := strconv.Atoi(parts[0])
	minor, err2 := strconv.Atoi(strings.SplitN(parts[1], "-", 2)[0])
	if err1 != nil || err2 != nil {
		return 0
	}
	return major*1000 + minor
}

func codexStateOf(file *FileState) *codexState {
	if state, ok := file.cache.(*codexState); ok {
		return state
	}
	state := &codexState{}
	if len(file.Saved) > 0 {
		_ = json.Unmarshal(file.Saved, state) // damaged state reads as empty
	}
	file.cache = state
	return state
}

func (s *codexState) save(file *FileState) {
	file.Saved, _ = json.Marshal(s)
}

// row is a usage row of this file at time ts, keyed id. Its tier is the one
// Codex selected for it:
//   - a spawned subagent's row (Codex 0.152 and later) at its root's
//     selected tier at that time: every spawned subagent's request takes the
//     root's current tier (core session capture_step_context_inner,
//     root_service_tier), whatever the subagent's own snapshot says;
//   - any other row of a thread whose turn has started at a named tier at
//     that tier;
//   - another subagent's row at the tier it started with: its parent's
//     running-turn tier when it started (multi_agents
//     apply_spawn_agent_service_tier).
//
// Codex then sends no tier a model does not support; ingest applies that
// (ServedTier).
func (s *codexState) row(ts, id string) Usage {
	when, _ := time.Parse(time.RFC3339Nano, ts)
	u := Usage{
		Tool:      "codex",
		RequestID: id,
		Model:     firstNonEmpty(s.Model, "unknown"),
		Project:   firstNonEmpty(s.Cwd, "unknown"),
		Session:   s.Thread,
		Root:      firstNonEmpty(s.Root, s.Thread),
		Time:      when,
	}
	if s.Plan != "" { // the plan of the latest rate_limits read, at or before this row
		u.Subscription, u.SubscriptionLabel = "codex:"+s.Plan, codexPlanLabel(s.Plan)
		u.SubscriptionEvidence = EvidenceTranscript
	}
	u.AccountKey = s.Account
	subagent := s.Root != "" && s.Root != s.Thread
	switch {
	case subagent && s.Spawned && s.Version >= codexRootTier:
		u.TierFrom = s.Root
	case s.TierSet:
		if s.Tier != "" {
			u.Model += "@" + s.Tier
		}
	case subagent && s.Spawn != "":
		u.TierFrom = TurnTierFrom(firstNonEmpty(s.Parent, s.Root), s.Spawn)
	case subagent:
		u.TierFrom = s.Root
	}
	return u
}

// readPlan keeps the plan_type of a token_count's rate_limits as the file's
// latest. A null, absent or empty plan_type changes nothing.
func (s *codexState) readPlan(rateLimits json.RawMessage, file *FileState) {
	if !bytes.Contains(rateLimits, []byte(`"plan_type"`)) {
		return
	}
	var limits struct {
		Plan string `json:"plan_type"`
	}
	if decode(rateLimits, &limits) && limits.Plan != "" && limits.Plan != s.Plan {
		s.Plan = limits.Plan
		s.save(file)
	}
}

// codexCounts are a response's token counts. Cached and cache-write input are
// part of input_tokens; reasoning is part of output_tokens.
type codexCounts struct {
	Input      int64 `json:"input_tokens"`
	Cached     int64 `json:"cached_input_tokens"`
	CacheWrite int64 `json:"cache_write_input_tokens"`
	Output     int64 `json:"output_tokens"`
}

func (c codexCounts) fill(u *Usage) {
	u.Input = max(c.Input-c.Cached-c.CacheWrite, 0)
	u.CacheRead, u.CacheWrite5m, u.Output = c.Cached, c.CacheWrite, c.Output
}

// codexMarks are the line types Parse reads, a line's own or an event_msg's
// payload's; every other line, almost all of a rollout's bytes, is skipped
// without decoding.
var codexMarks = map[string]bool{
	"session_meta": true, "turn_context": true, "token_usage_record": true,
	"token_count": true, "thread_settings_applied": true,
}

// codexLineType reads a line's type, and an event_msg's payload type, from
// the head Codex writes every line with:
// {"timestamp":"…",["ordinal":N,]"type":"…"[,"payload":{"type":"…"]. ok is
// false for any other head; Parse then decodes the line to learn its type.
func codexLineType(line []byte) (kind, event []byte, ok bool) {
	rest, ok := bytes.CutPrefix(line, []byte(`{"timestamp":`))
	if !ok {
		return nil, nil, false
	}
	if _, rest, ok = codexString(rest); !ok {
		return nil, nil, false
	}
	if after, found := bytes.CutPrefix(rest, []byte(`,"ordinal":`)); found {
		digits := 0
		for digits < len(after) && after[digits] >= '0' && after[digits] <= '9' {
			digits++
		}
		if digits == 0 {
			return nil, nil, false
		}
		rest = after[digits:]
	}
	if rest, ok = bytes.CutPrefix(rest, []byte(`,"type":`)); !ok {
		return nil, nil, false
	}
	if kind, rest, ok = codexString(rest); !ok {
		return nil, nil, false
	}
	if string(kind) != "event_msg" {
		return kind, nil, true
	}
	if rest, ok = bytes.CutPrefix(rest, []byte(`,"payload":{"type":`)); !ok {
		return nil, nil, false
	}
	event, _, ok = codexString(rest)
	return kind, event, ok
}

// codexString splits a JSON string with no escapes off the front of b: its
// text and what follows the closing quote.
func codexString(b []byte) (text, rest []byte, ok bool) {
	if len(b) == 0 || b[0] != '"' {
		return nil, nil, false
	}
	end := bytes.IndexByte(b[1:], '"')
	if end < 0 || bytes.IndexByte(b[1:end+1], '\\') >= 0 {
		return nil, nil, false
	}
	return b[1 : end+1], b[end+2:], true
}

// Parse returns the usage rows of one rollout line. A token_usage_record of
// this file's thread is one row keyed by its response id; a copied record
// (another thread's) is skipped, its original being in the parent's file. Any
// record, copied or its own, turns token_count reading off for the file: the
// token_count lines a fork copies beside a record would otherwise count the
// parent's response again. A file without records is read from its
// token_count events: one row per change of the running total that is not a
// synthetic estimate, keyed by a digest of the event's counts and rate
// limits, so a fork's copy of an event lands on the original's row.
func (codex) Parse(line []byte, file *FileState) []Usage {
	kind, event, ok := codexLineType(line)
	if state := codexStateOf(file); state.Fork != "" && state.Copies == 0 {
		codexForkCopies(line, string(event), state, file)
	}
	switch {
	case !ok: // an unusual head: decode the line if it names a type Parse reads
		named := false
		for mark := range codexMarks {
			named = named || bytes.Contains(line, []byte(mark))
		}
		if !named {
			return nil
		}
	case string(kind) != "event_msg":
		if !codexMarks[string(kind)] {
			return nil
		}
	case string(event) == "token_count":
		if rows, done := codexTokenCountLine(line, file); done {
			return rows
		}
	case !codexMarks[string(event)]:
		return nil
	}
	var rec struct {
		Timestamp string          `json:"timestamp"`
		Type      string          `json:"type"`
		Payload   json.RawMessage `json:"payload"`
	}
	if !decode(line, &rec) || len(rec.Payload) == 0 {
		return nil
	}
	state := codexStateOf(file)
	switch rec.Type {
	case "session_meta":
		codexMeta(rec.Timestamp, rec.Payload, state, file)
	case "turn_context":
		var turn struct {
			TurnID string `json:"turn_id"`
			Model  string `json:"model"`
			Cwd    string `json:"cwd"`
		}
		if decode(rec.Payload, &turn) {
			codexTurn(rec.Timestamp, turn.TurnID, state, file)
			state.Model = firstNonEmpty(turn.Model, state.Model)
			state.Cwd = firstNonEmpty(turn.Cwd, state.Cwd)
			state.save(file)
		}
	case "token_usage_record":
		var record struct {
			ThreadID   string      `json:"thread_id"`
			SessionID  string      `json:"session_id"`
			ResponseID string      `json:"response_id"`
			Usage      codexCounts `json:"usage"`
		}
		if !decode(rec.Payload, &record) || record.ResponseID == "" {
			return nil
		}
		if !state.Records { // a copied record counts: its token_count copies follow
			state.Records = true
			state.save(file)
		}
		if state.Thread != "" && record.ThreadID != state.Thread {
			return nil
		}
		if state.Root == "" && record.SessionID != "" && record.SessionID != record.ThreadID {
			state.Root = record.SessionID
			state.save(file)
		}
		u := state.row(rec.Timestamp, record.ResponseID)
		u.Root = firstNonEmpty(record.SessionID, u.Root) // the root session the record names
		record.Usage.fill(&u)
		return []Usage{u}
	case "event_msg":
		return codexEvent(rec.Timestamp, rec.Payload, state, file)
	}
	return nil
}

// codexTokenInfo is what a token_count event carries that Parse reads.
type codexTokenInfo struct {
	Info *struct {
		Total json.RawMessage `json:"total_token_usage"`
		Last  json.RawMessage `json:"last_token_usage"`
	} `json:"info"`
	RateLimits json.RawMessage `json:"rate_limits"`
}

// codexTokenCountLine reads a line whose head says it is a token_count event
// in one decode, the most common line Parse reads. done is false when the
// decoded line is not one after all; Parse then reads it the general way.
func codexTokenCountLine(line []byte, file *FileState) (rows []Usage, done bool) {
	var rec struct {
		Timestamp string `json:"timestamp"`
		Type      string `json:"type"`
		Payload   *struct {
			Type string `json:"type"`
			codexTokenInfo
		} `json:"payload"`
	}
	if !decode(line, &rec) || rec.Type != "event_msg" || rec.Payload == nil ||
		rec.Payload.Type != "token_count" {
		return nil, false
	}
	state := codexStateOf(file)
	return codexTokenCount(rec.Timestamp, rec.Payload.codexTokenInfo, state, file), true
}
