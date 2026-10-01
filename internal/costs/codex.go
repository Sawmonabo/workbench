package costs

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/pelletier/go-toml/v2"
)

// codex is the Codex source: the rollout JSONL files under
// $CODEX_HOME/sessions/YYYY/MM/DD and $CODEX_HOME/archived_sessions, where
// CODEX_HOME defaults to ~/.codex. Each line is {timestamp, type, payload}.
// Codex 0.153 and later write a token_usage_record per response; older files
// have only token_count events, and a legacy fork copies its parent's lines,
// usage included, into its own file.
type codex struct{}

func (codex) Name() string { return "codex" }

// codexDir is the Codex home as Codex resolves it (codex-rs utils/home-dir
// find_codex_home): CODEX_HOME made absolute with its symlinks resolved, else
// ~/.codex as is. The hooks trust keys Codex writes start with this path.
func codexDir(home string) string {
	dir := os.Getenv("CODEX_HOME")
	if dir == "" {
		return filepath.Join(home, ".codex")
	}
	if abs, err := filepath.Abs(dir); err == nil {
		dir = abs
	}
	if real, err := filepath.EvalSymlinks(dir); err == nil {
		dir = real
	}
	return dir
}

// Transcripts lists every *.jsonl under sessions/ and archived_sessions/.
func (codex) Transcripts(home string) ([]string, error) {
	var files []string
	for _, root := range []string{"sessions", "archived_sessions"} {
		found, err := codexWalk(filepath.Join(codexDir(home), root), ".jsonl")
		if err != nil {
			return files, err
		}
		files = append(files, found...)
	}
	sort.Strings(files)
	return files, nil
}

// Skipped counts the compressed rollouts (.jsonl.zst) Workbench does not read.
func (codex) Skipped(home string) int {
	n := 0
	for _, root := range []string{"sessions", "archived_sessions"} {
		found, _ := codexWalk(filepath.Join(codexDir(home), root), ".zst")
		n += len(found)
	}
	return n
}

func codexWalk(root, ext string) ([]string, error) {
	if info, err := os.Stat(root); err != nil || !info.IsDir() {
		return nil, nil
	}
	var files []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		switch {
		case err != nil:
			if entry != nil && entry.IsDir() {
				return fs.SkipDir
			}
			return nil
		case filepath.Ext(path) != ext:
		case entry.Type().IsRegular():
			files = append(files, path)
		case entry.Type()&fs.ModeSymlink != 0:
			if info, statErr := os.Stat(path); statErr == nil && info.Mode().IsRegular() {
				files = append(files, path)
			}
		}
		return nil
	})
	return files, err
}

// Session reports whether file is the rollout the hook named. Codex's
// SessionEnd fires for root threads only, so a subagent's rows are sweep rows.
func (codex) Session(file, transcript string) bool {
	return transcript != "" && file == transcript
}

// Account is the email claim of the ID token Codex keeps in auth.json. The
// token is only decoded, never verified; the access and refresh tokens are
// never read. API-key and keyring sign-ins have no email there.
func (codex) Account(home string) string {
	var auth struct {
		Tokens struct {
			IDToken string `json:"id_token"`
		} `json:"tokens"`
	}
	raw, err := os.ReadFile(filepath.Join(codexDir(home), "auth.json"))
	if err != nil || !decode(raw, &auth) {
		return "unknown"
	}
	parts := strings.Split(auth.Tokens.IDToken, ".")
	if len(parts) != 3 {
		return "unknown"
	}
	payload, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(parts[1], "="))
	if err != nil {
		return "unknown"
	}
	var claims struct {
		Email   string `json:"email"`
		Profile struct {
			Email string `json:"email"`
		} `json:"https://api.openai.com/profile"`
	}
	if !decode(payload, &claims) {
		return "unknown"
	}
	return firstNonEmpty(claims.Email, claims.Profile.Email, "unknown")
}

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
		Time:      when,
	}
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

// ServedTier is the tier Codex sends a model's request at when tier is
// selected. Codex sends a tier only to a model whose catalog entry lists it,
// flex excepted, and none but flex with the fast_mode feature off
// (openai_models.rs service_tier_for_request, session get_service_tier). It
// keeps its catalog in models_cache.json; a model the cache does not list
// keeps the selected tier, as the cache holds only the catalog Codex fetched
// last. fast_mode is on unless config.toml turns it off.
func (codex) ServedTier(home string) func(model, tier string) string {
	var config struct {
		Features struct {
			FastMode *bool `toml:"fast_mode"`
		} `toml:"features"`
	}
	if raw, err := os.ReadFile(filepath.Join(codexDir(home), "config.toml")); err == nil {
		_ = toml.Unmarshal(raw, &config) // an unreadable config keeps the default
	}
	fastMode := config.Features.FastMode == nil || *config.Features.FastMode
	var cache struct {
		Models []struct {
			Slug  string `json:"slug"`
			Tiers []struct {
				ID string `json:"id"`
			} `json:"service_tiers"`
		} `json:"models"`
	}
	if raw, err := os.ReadFile(filepath.Join(codexDir(home), "models_cache.json")); err == nil {
		decode(raw, &cache)
	}
	supports := map[string]map[string]bool{}
	for _, m := range cache.Models {
		if m.Slug == "" {
			continue
		}
		supports[m.Slug] = map[string]bool{}
		for _, t := range m.Tiers {
			supports[m.Slug][codexTier(t.ID)] = true
		}
	}
	return func(model, tier string) string {
		if tier == "flex" {
			return tier
		}
		if listed, ok := supports[model]; !fastMode || (ok && !listed[tier]) {
			return ""
		}
		return tier
	}
}

// codexTier is the stored name of a Codex service tier: priority (renamed
// Fast mode on 2026-07-30) and fast are "fast", the standard tier is "", and
// any other tier is its lowercase name. A name splitTier cannot split off
// stays part of the model id, so its rows show unpriced, never at standard.
func codexTier(raw string) string {
	switch tier := strings.ToLower(strings.TrimSpace(raw)); tier {
	case "priority", "fast":
		return "fast"
	case "", "default", "auto", "standard":
		return ""
	default:
		return tier
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

func codexEvent(ts string, payload json.RawMessage, state *codexState, file *FileState) []Usage {
	var head struct {
		Type string `json:"type"`
	}
	if !decode(payload, &head) {
		return nil
	}
	switch head.Type {
	case "thread_settings_applied":
		// A snapshot of the thread's settings. Codex omits service_tier when
		// the thread requests none, and then sends none: the standard tier.
		// A copy a fork made of its parent's snapshot names the parent's
		// thread and is skipped; older snapshots name no thread. The settings
		// are for the turns that start after it ("the running turn keeps its
		// own"): a changed model or cwd is named by the next turn_context, so
		// the snapshot's only fill in before the first one, and the tier waits
		// for the next turn to start. The selected tier is recorded now too:
		// a subagent's requests take its root's selected tier at once.
		var event struct {
			ThreadID string `json:"thread_id"`
			Settings struct {
				Model string `json:"model"`
				Cwd   string `json:"cwd"`
				Tier  string `json:"service_tier"`
			} `json:"thread_settings"`
		}
		if !decode(payload, &event) ||
			(event.ThreadID != "" && state.Thread != "" && event.ThreadID != state.Thread) {
			return nil
		}
		state.Model = firstNonEmpty(state.Model, event.Settings.Model)
		state.Cwd = firstNonEmpty(state.Cwd, event.Settings.Cwd)
		state.Next, state.NextSet = codexTier(event.Settings.Tier), true
		state.save(file)
		when, _ := time.Parse(time.RFC3339Nano, ts)
		file.Tiers = append(file.Tiers, TierChange{
			Thread: firstNonEmpty(event.ThreadID, state.Thread), Time: when, Tier: state.Next,
		})
	case "token_count":
		var event codexTokenInfo
		if !decode(payload, &event) {
			return nil
		}
		return codexTokenCount(ts, event, state, file)
	}
	return nil
}

// codexTokenCount is the row of a token_count event, if it counts. A file
// Codex 0.153 or later started records every response it counts, so its
// token_count lines count none (a subagent fork copies its parent's
// token_count lines but not its records: spawn.rs keep_forked_rollout_item).
//
// A fork whose history was copied into its file starts with copies of its
// parent's events, and its first token_count whose total is more than its
// last response is one of them: a response of the parent's history. It may
// copy an event the parent re-emitted for a rate-limit update, whose key
// (rate_limits differ) matches no row of the parent's; so while the parent's
// rollout holds that response (FileState.Holds), the copy only sets the
// running total. Later copies follow it as in the parent and land on the
// parent's rows. A fork that copied nothing carries its parent's total into
// its own first token_count, which counts.
func codexTokenCount(ts string, event codexTokenInfo, state *codexState, file *FileState) []Usage {
	if state.Records || state.Version >= codexRecords || event.Info == nil ||
		len(event.Info.Total) == 0 || len(event.Info.Last) == 0 {
		return nil
	}
	total := canonicalJSON(event.Info.Total)
	if total == state.Total {
		return nil // re-emitted for a rate-limit update
	}
	first := state.Total == ""
	state.Total = total
	state.save(file)
	if first && state.Copies == 1 && total != canonicalJSON(event.Info.Last) &&
		file.Holds != nil && file.Holds(state.Fork, state.Spawn) {
		return nil // the parent's, copied into the fork
	}
	var last codexCounts
	if !decode(event.Info.Last, &last) || (last.Input == 0 && last.Output == 0) {
		return nil // a synthetic estimate after compaction
	}
	sum := sha256.Sum256([]byte(total + "\n" + canonicalJSON(event.Info.Last) + "\n" +
		canonicalJSON(event.RateLimits)))
	u := state.row(ts, "tc:"+hex.EncodeToString(sum[:]))
	last.fill(&u)
	return []Usage{u}
}

// codexMeta reads a session_meta line: the first one names this file's
// thread; later ones are copies a fork made of its parent's.
func codexMeta(ts string, payload json.RawMessage, state *codexState, file *FileState) {
	if state.Thread != "" {
		return // a parent's, copied by a fork
	}
	var meta struct {
		ID        string          `json:"id"`
		SessionID string          `json:"session_id"`
		Forked    string          `json:"forked_from_id"`
		Version   string          `json:"cli_version"`
		Cwd       string          `json:"cwd"`
		Source    json.RawMessage `json:"source"`
	}
	if !decode(payload, &meta) || meta.ID == "" {
		return
	}
	state.Thread, state.Cwd = meta.ID, firstNonEmpty(meta.Cwd, state.Cwd)
	state.Fork, state.Version = meta.Forked, codexVersion(meta.Version)
	state.Root = codexRoot(meta.ID, meta.SessionID, meta.Source)
	state.Parent = codexParent(meta.Source)
	state.Spawned = state.Parent != ""
	when, err := time.Parse(time.RFC3339Nano, ts)
	if err == nil {
		state.Spawn = when.UTC().Format(timeLayout)
	}
	// A subagent that keeps the tier it started with runs at its parent's
	// running-turn tier then, until a turn of its own names one; its own
	// subagents read that from its running-turn series.
	if err == nil && state.Root != "" && state.Root != state.Thread &&
		(!state.Spawned || state.Version < codexRootTier) {
		file.Tiers = append(file.Tiers, TierChange{
			Thread: state.Thread, Time: when, Turn: true,
			From: TurnTierFrom(firstNonEmpty(state.Parent, state.Root), state.Spawn),
		})
	}
	state.save(file)
}

// codexForkCopies decides, at the first line after a fork's own session_meta,
// whether the fork copied its parent's history into its file. Codex writes a
// fork that references its parent's history, or a paginated subagent, with
// its own thread_settings_applied first; a fork that copies starts with the
// copied lines and appends that snapshot after them (core session
// InitialHistory::Forked and ForkPersistence, 0.146 and later; earlier forks
// always copy).
func codexForkCopies(line []byte, event string, state *codexState, file *FileState) {
	if state.Thread == "" {
		return // the fork's own session_meta is not read yet
	}
	state.Copies = 1
	if event == "thread_settings_applied" {
		var rec struct {
			Payload struct {
				ThreadID string `json:"thread_id"`
			} `json:"payload"`
		}
		if decode(line, &rec) &&
			(rec.Payload.ThreadID == "" || rec.Payload.ThreadID == state.Thread) {
			state.Copies = 2
		}
	}
	state.save(file)
}

// codexTurn notes a turn_context: one with a turn id other than the running
// turn's (or none) starts a turn, which takes the tier the latest snapshot
// selected; that is recorded as the thread's running-turn tier from then. A
// turn_context re-emitted in the same turn (after a compaction) changes
// nothing.
func codexTurn(ts, turnID string, state *codexState, file *FileState) {
	if turnID != "" && turnID == state.Turn {
		return
	}
	state.Turn = turnID
	if !state.NextSet {
		return
	}
	state.Tier, state.TierSet, state.NextSet = state.Next, true, false
	when, _ := time.Parse(time.RFC3339Nano, ts)
	file.Tiers = append(file.Tiers, TierChange{
		Thread: state.Thread, Time: when, Tier: state.Tier, Turn: true,
	})
}

// codexParent is the thread that spawned a subagent, from its session_meta
// source; "" for any other thread.
func codexParent(source json.RawMessage) string {
	var spawned struct {
		Subagent struct {
			ThreadSpawn struct {
				Parent string `json:"parent_thread_id"`
			} `json:"thread_spawn"`
		} `json:"subagent"`
	}
	if len(source) > 0 && source[0] == '{' && decode(source, &spawned) {
		return spawned.Subagent.ThreadSpawn.Parent
	}
	return ""
}

// codexRoot is the root thread of a subagent's file: the session id when it
// differs from the thread id, else the spawning parent; "" for a root thread.
func codexRoot(id, sessionID string, source json.RawMessage) string {
	if sessionID != "" && sessionID != id {
		return sessionID
	}
	return codexParent(source)
}

// canonicalJSON re-encodes raw with sorted keys and exact numbers, so the same
// value gives the same text however Codex spaced or ordered it. The text is
// what encoding/json writes for raw decoded with UseNumber, and is part of
// every token_count row's key, so it must never change. A value made only of
// objects, arrays, numbers, literals and plain printable ASCII strings is
// written directly; anything else (escapes, HTML characters, other bytes,
// repeated keys) takes the decode-and-marshal path.
func canonicalJSON(raw json.RawMessage) string {
	if out, rest, ok := appendCanonical(nil, skipSpace(raw), 0); ok && len(skipSpace(rest)) == 0 {
		return string(out)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if decoder.Decode(&value) != nil {
		return string(raw)
	}
	out, err := json.Marshal(value)
	if err != nil {
		return string(raw)
	}
	return string(out)
}

// appendCanonical appends the canonical form of the value at the front of
// src, nested depth deep, to dst and returns what follows it; ok is false when
// the value is malformed or needs the decode-and-marshal path.
func appendCanonical(dst, src []byte, depth int) (out, rest []byte, ok bool) {
	if len(src) == 0 || depth > 32 {
		return dst, src, false
	}
	switch c := src[0]; {
	case c == '{':
		return appendCanonicalObject(dst, src[1:], depth+1)
	case c == '[':
		dst = append(dst, '[')
		src = skipSpace(src[1:])
		if len(src) > 0 && src[0] == ']' {
			return append(dst, ']'), src[1:], true
		}
		for {
			if dst, src, ok = appendCanonical(dst, src, depth+1); !ok {
				return dst, src, false
			}
			src = skipSpace(src)
			if len(src) == 0 {
				return dst, src, false
			}
			switch src[0] {
			case ',':
				dst, src = append(dst, ','), skipSpace(src[1:])
			case ']':
				return append(dst, ']'), src[1:], true
			default:
				return dst, src, false
			}
		}
	case c == '"':
		text, rest, ok := plainString(src)
		return append(append(append(dst, '"'), text...), '"'), rest, ok
	case c == '-' || (c >= '0' && c <= '9'):
		n := numberLength(src)
		return append(dst, src[:n]...), src[n:], n > 0
	}
	for _, literal := range []string{"true", "false", "null"} {
		if bytes.HasPrefix(src, []byte(literal)) {
			return append(dst, literal...), src[len(literal):], true
		}
	}
	return dst, src, false
}

// appendCanonicalObject writes the object whose members start at src with
// its keys in byte order, as encoding/json writes a map.
func appendCanonicalObject(dst, src []byte, depth int) (out, rest []byte, ok bool) {
	type member struct {
		key        []byte
		start, end int // the value's text in values
	}
	var buffer [16]member
	members := buffer[:0]
	var values []byte
	src = skipSpace(src)
	if len(src) > 0 && src[0] == '}' {
		return append(dst, '{', '}'), src[1:], true
	}
	for {
		m := member{start: len(values)}
		if m.key, src, ok = plainString(src); !ok {
			return dst, src, false
		}
		src = skipSpace(src)
		if len(src) == 0 || src[0] != ':' {
			return dst, src, false
		}
		if values, src, ok = appendCanonical(values, skipSpace(src[1:]), depth); !ok {
			return dst, src, false
		}
		m.end = len(values)
		members = append(members, m)
		src = skipSpace(src)
		if len(src) == 0 {
			return dst, src, false
		}
		if src[0] == '}' {
			break
		}
		if src[0] != ',' {
			return dst, src, false
		}
		src = skipSpace(src[1:])
	}
	slices.SortFunc(members, func(a, b member) int { return bytes.Compare(a.key, b.key) })
	dst = append(dst, '{')
	for i, m := range members {
		if i > 0 {
			if bytes.Equal(m.key, members[i-1].key) {
				return dst, src, false // a repeated key: the decoder keeps the last
			}
			dst = append(dst, ',')
		}
		dst = append(append(append(append(dst, '"'), m.key...), '"', ':'), values[m.start:m.end]...)
	}
	return append(dst, '}'), src[1:], true
}

// plainString splits a string of printable ASCII off the front of src, one
// encoding/json writes back unchanged: no escapes and none of <, >, &.
func plainString(src []byte) (text, rest []byte, ok bool) {
	if len(src) == 0 || src[0] != '"' {
		return nil, src, false
	}
	for i := 1; i < len(src); i++ {
		switch c := src[i]; {
		case c == '"':
			return src[1:i], src[i+1:], true
		case c < 0x20 || c > 0x7e || c == '\\' || c == '<' || c == '>' || c == '&':
			return nil, src, false
		}
	}
	return nil, src, false
}

// numberLength is the length of the JSON number at the front of src, 0 when
// there is none.
func numberLength(src []byte) int {
	i := 0
	digits := func() int {
		start := i
		for i < len(src) && src[i] >= '0' && src[i] <= '9' {
			i++
		}
		return i - start
	}
	if i < len(src) && src[i] == '-' {
		i++
	}
	switch n := digits(); {
	case n == 0, n > 1 && src[i-n] == '0':
		return 0
	}
	if i < len(src) && src[i] == '.' {
		i++
		if digits() == 0 {
			return 0
		}
	}
	if i < len(src) && (src[i] == 'e' || src[i] == 'E') {
		i++
		if i < len(src) && (src[i] == '+' || src[i] == '-') {
			i++
		}
		if digits() == 0 {
			return 0
		}
	}
	return i
}

func skipSpace(src []byte) []byte {
	for len(src) > 0 && (src[0] == ' ' || src[0] == '\t' || src[0] == '\n' || src[0] == '\r') {
		src = src[1:]
	}
	return src
}

// Observations is nil: Codex records no cost to calibrate against.
func (codex) Observations(string) []Observation { return nil }

// codexEvents are Codex's hook events and the labels its trust keys use.
var codexEvents = map[string]string{
	"PreToolUse": "pre_tool_use", "PermissionRequest": "permission_request",
	"PostToolUse": "post_tool_use", "PreCompact": "pre_compact", "PostCompact": "post_compact",
	"SessionStart": "session_start", "SessionEnd": "session_end",
	"UserPromptSubmit": "user_prompt_submit", "SubagentStart": "subagent_start",
	"SubagentStop": "subagent_stop", "Stop": "stop", "Interrupt": "interrupt",
}

type codexHook struct {
	Type          string  `json:"type"`
	Command       string  `json:"command"`
	Timeout       *int64  `json:"timeout"`
	Async         bool    `json:"async"`
	StatusMessage *string `json:"statusMessage"`
}

type codexGroup struct {
	Matcher *string     `json:"matcher"`
	Hooks   []codexHook `json:"hooks"`
}

// codexHookHash is the trust hash Codex records for a command hook: the
// SHA-256 of the compact, key-sorted JSON of its normalized identity
// (codex-rs hooks/src/engine/discovery.rs hook_hash and config/src/
// fingerprint.rs version_for_toml). Timeouts are normalized as Codex does,
// and the matcher counts only for events that use one. The same rule is in
// home/private_dot_codex/modify_private_config.toml.tmpl, which writes it.
func codexHookHash(event string, group codexGroup, hook codexHook) string {
	timeout := int64(600)
	if hook.Timeout != nil {
		timeout = *hook.Timeout
	}
	switch event {
	case "SessionEnd", "Interrupt":
		if hook.Timeout == nil {
			timeout = 1
		}
		timeout = min(max(timeout, 1), 3)
	default:
		timeout = max(timeout, 1)
	}
	handler := map[string]any{
		"type": "command", "command": hook.Command, "timeout": timeout, "async": hook.Async,
	}
	if hook.StatusMessage != nil {
		handler["statusMessage"] = *hook.StatusMessage
	}
	identity := map[string]any{"event_name": codexEvents[event], "hooks": []any{handler}}
	if group.Matcher != nil &&
		!slices.Contains([]string{"UserPromptSubmit", "Stop", "Interrupt"}, event) {
		identity["matcher"] = *group.Matcher
	}
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetEscapeHTML(false)
	_ = encoder.Encode(identity)
	sum := sha256.Sum256(bytes.TrimSuffix(buf.Bytes(), []byte("\n")))
	return "sha256:" + hex.EncodeToString(sum[:])
}

// Hooks reports, for each session event, whether hooks.json runs the ingest
// hook and Codex runs it: HookOK when config.toml trusts that exact hook and
// does not disable it, HookDisabled when it sets enabled = false, HookUntrusted
// when it records no trusted_hash or another one (Codex ignores the hook then),
// and HookMissing without the hook. Of several ingest hooks for an event the
// best state counts.
func (codex) Hooks(home string) map[string]string {
	dir := codexDir(home)
	path := filepath.Join(dir, "hooks.json")
	var file struct {
		Hooks map[string][]codexGroup `json:"hooks"`
	}
	if raw, err := os.ReadFile(path); err == nil {
		decode(raw, &file)
	}
	var config struct {
		Hooks struct {
			State map[string]struct {
				Enabled     *bool  `toml:"enabled"`
				TrustedHash string `toml:"trusted_hash"`
			} `toml:"state"`
		} `toml:"hooks"`
	}
	if raw, err := os.ReadFile(filepath.Join(dir, "config.toml")); err == nil {
		_ = toml.Unmarshal(raw, &config) // an unreadable config trusts nothing
	}
	// Better states come first.
	rank := []string{HookOK, HookDisabled, HookUntrusted, HookMissing}
	out := map[string]string{}
	for _, event := range []string{"SessionStart", "SessionEnd"} {
		best := HookMissing
		for gi, group := range file.Hooks[event] {
			for hi, hook := range group.Hooks {
				if hook.Type != "command" || !strings.Contains(hook.Command, "workbench") ||
					!strings.HasSuffix(hook.Command, " costs ingest") {
					continue
				}
				state := config.Hooks.State[fmt.Sprintf("%s:%s:%d:%d", path, codexEvents[event], gi, hi)]
				found := HookOK
				switch {
				case state.Enabled != nil && !*state.Enabled:
					found = HookDisabled
				case state.TrustedHash != codexHookHash(event, group, hook):
					found = HookUntrusted
				}
				if slices.Index(rank, found) < slices.Index(rank, best) {
					best = found
				}
			}
		}
		out[event] = best
	}
	return out
}

// PricingURL is OpenAI's price page in Markdown; CODEX_COSTS_PRICING_URL
// replaces it for checks that must not reach the network.
func (codex) PricingURL() string {
	if url := os.Getenv("CODEX_COSTS_PRICING_URL"); url != "" {
		return url
	}
	return "https://developers.openai.com/api/docs/pricing.md"
}

// codexBuiltin is the OpenAI price page read on 2026-10-01, short-context
// columns: input, output, cache write, cache read per million tokens; a "-"
// price was filled with the input price. A tier row is "<model>@<tier>". The
// official card overrides these at runtime and the overrides file wins.
var codexBuiltin = map[string][4]float64{
	"gpt-6-astra": {10, 50, 12.5, 1}, "gpt-6.1-sol": {2, 10, 2.5, 0.1},
	"gpt-6-luna": {0.1, 0.5, 0.125, 0.01}, "gpt-6-sol": {2, 10, 2.5, 0.2},
	"gpt-5.6-sol": {4, 20, 5, 0.4}, "gpt-5.6-terra": {2, 12, 2.5, 0.2},
	"gpt-5.6-luna": {0.2, 1.2, 0.25, 0.02}, "gpt-5.5": {5, 30, 5, 0.5},
	"gpt-5.5-pro": {30, 180, 30, 30}, "gpt-5.4": {2.5, 15, 2.5, 0.25},
	"gpt-5.4-mini": {0.75, 4.5, 0.75, 0.075}, "gpt-5.4-nano": {0.2, 1.25, 0.2, 0.02},
	"gpt-5.4-pro": {30, 180, 30, 30}, "gpt-5.2": {1.75, 14, 1.75, 0.175},
	"gpt-5.2-pro": {21, 168, 21, 21}, "gpt-5.1": {1.25, 10, 1.25, 0.125},
	"gpt-5": {1.25, 10, 1.25, 0.125}, "gpt-5-mini": {0.25, 2, 0.25, 0.025},
	"gpt-5-nano": {0.05, 0.4, 0.05, 0.005}, "gpt-5-pro": {15, 120, 15, 15},
	"gpt-6-astra@flex": {5, 25, 6.25, 0.5}, "gpt-6.1-sol@flex": {1, 5, 1.25, 0.05},
	"gpt-6-luna@flex": {0.05, 0.25, 0.0625, 0.005}, "gpt-6-sol@flex": {1, 5, 1.25, 0.1},
	"gpt-5.6-sol@flex": {2, 10, 2.5, 0.2}, "gpt-5.6-terra@flex": {1, 6, 1.25, 0.1},
	"gpt-5.6-luna@flex": {0.1, 0.6, 0.125, 0.01}, "gpt-5.5@flex": {2.5, 15, 2.5, 0.25},
	"gpt-5.5-pro@flex": {15, 90, 15, 15}, "gpt-5.4@flex": {1.25, 7.5, 1.25, 0.13},
	"gpt-5.4-mini@flex": {0.375, 2.25, 0.375, 0.0375}, "gpt-5.4-nano@flex": {0.1, 0.625, 0.1, 0.01},
	"gpt-5.4-pro@flex": {15, 90, 15, 15}, "gpt-5.2@flex": {0.875, 7, 0.875, 0.0875},
	"gpt-5.1@flex": {0.625, 5, 0.625, 0.0625}, "gpt-5@flex": {0.625, 5, 0.625, 0.0625},
	"gpt-5-mini@flex": {0.125, 1, 0.125, 0.0125}, "gpt-5-nano@flex": {0.025, 0.2, 0.025, 0.0025},
	"gpt-6-astra@fast": {20, 100, 25, 2}, "gpt-6.1-sol@fast": {4, 20, 5, 0.2},
	"gpt-6-luna@fast": {0.2, 1, 0.25, 0.02}, "gpt-6-sol@fast": {4, 20, 5, 0.4},
	"gpt-5.6-sol@fast": {8, 40, 10, 0.8}, "gpt-5.6-terra@fast": {4, 24, 5, 0.4},
	"gpt-5.6-luna@fast": {0.4, 2.4, 0.5, 0.04}, "gpt-5.5@fast": {12.5, 75, 12.5, 1.25},
	"gpt-5.4@fast": {5, 30, 5, 0.5}, "gpt-5.4-mini@fast": {1.5, 9, 1.5, 0.15},
	"gpt-5.2@fast": {3.5, 28, 3.5, 0.35}, "gpt-5.1@fast": {2.5, 20, 2.5, 0.25},
	"gpt-5@fast": {2.5, 20, 2.5, 0.25}, "gpt-5-mini@fast": {0.45, 3.6, 0.45, 0.045},
	"gpt-6-astra@ultrafast": {60, 300, 75, 6},
}

func (codex) Builtin() RateCard {
	card := RateCard{}
	for prefix, v := range codexBuiltin {
		card[prefix] = Rate{
			Input: v[0], Output: v[1], CacheWrite5m: v[2], CacheWrite1h: v[2], CacheRead: v[3],
			Source: "builtin",
		}
	}
	return card
}

var codexModelPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9.\-]*$`)

// codexTable is the tier suffix of the price page table under heading: the
// tier a "<Tier> pricing data" heading names, "" for Standard, and ok false
// for any other heading and for Batch, which Codex does not use.
func codexTable(heading string) (suffix string, ok bool) {
	name, found := strings.CutSuffix(strings.ToLower(heading), " pricing data")
	if !found || name == "batch" || !isTier(name) {
		return "", false
	}
	if tier := codexTier(name); tier != "" {
		return "@" + tier, true
	}
	return "", true
}

// ParsePricing reads the short-context columns of every service tier's table:
// Standard, and each other "<Tier> pricing data" table into "<model>@<tier>"
// rows, as Flex, Fast and Ultrafast are today. A "-" cached-input or
// cache-write price bills those tokens at the input price. The first row of a
// model in a table wins.
func (codex) ParsePricing(page string) RateCard {
	card := RateCard{}
	lines := strings.Split(page, "\n")
	suffix, inTable := "", false
	for i := 0; i < len(lines)-1; {
		head, sep := strings.TrimSpace(lines[i]), strings.TrimSpace(lines[i+1])
		if strings.HasPrefix(head, "#") {
			suffix, inTable = codexTable(strings.TrimSpace(strings.TrimLeft(head, "#")))
		}
		if !strings.HasPrefix(head, "|") || !strings.HasPrefix(sep, "|") ||
			strings.Trim(sep, "|-: ") != "" {
			i++
			continue
		}
		var headers []string
		for h := range strings.SplitSeq(strings.Trim(head, "|"), "|") {
			headers = append(headers, strings.ToLower(strings.TrimSpace(h)))
		}
		i += 2
		if !inTable {
			continue
		}
		column := func(name string) int { return slices.Index(headers, "short context "+name) }
		ci, co := column("input"), column("output")
		cc, cw := column("cached input"), column("cache writes")
		if ci < 0 || co < 0 {
			continue
		}
		for ; i < len(lines) && strings.HasPrefix(strings.TrimSpace(lines[i]), "|"); i++ {
			var cells []string
			for c := range strings.SplitSeq(strings.Trim(strings.TrimSpace(lines[i]), "|"), "|") {
				cells = append(cells, strings.TrimSpace(c))
			}
			model := strings.ToLower(strings.TrimSpace(notePattern.ReplaceAllString(cells[0], "")))
			if !codexModelPattern.MatchString(model) {
				continue
			}
			key := model + suffix
			if _, seen := card[key]; seen {
				continue
			}
			in, okIn := money(cells, ci)
			out, okOut := money(cells, co)
			if !okIn || !okOut {
				continue
			}
			rate := Rate{Input: in, Output: out, CacheWrite5m: in, CacheWrite1h: in, CacheRead: in}
			if v, ok := money(cells, cw); ok {
				rate.CacheWrite5m, rate.CacheWrite1h = v, v
			}
			if v, ok := money(cells, cc); ok {
				rate.CacheRead = v
			}
			card[key] = rate
		}
	}
	return card
}
