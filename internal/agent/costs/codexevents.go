package costs

import (
	"cmp"
	"encoding/json"
	"time"

	"github.com/Sawmonabo/workbench/internal/operation"
)

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
		state.Model = cmp.Or(state.Model, event.Settings.Model)
		state.Cwd = cmp.Or(state.Cwd, event.Settings.Cwd)
		state.Next, state.NextSet = codexTier(event.Settings.Tier), true
		state.save(file)
		when, _ := time.Parse(time.RFC3339Nano, ts)
		file.Tiers = append(file.Tiers, TierChange{
			Thread: cmp.Or(event.ThreadID, state.Thread), Time: when, Tier: state.Next,
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
// (rate_limits differ) matches no row of the parent's, so its row is marked a
// copy of the parent's (CopyOf): ingest drops it once the parent's rollout is
// read, and keeps it as the only trace of that response while it is not.
// Later copies follow it as in the parent and land on the parent's rows. A
// fork that copied nothing carries its parent's total into its own first
// token_count, which is its own response.
func codexTokenCount(ts string, event codexTokenInfo, state *codexState, file *FileState) []Usage {
	state.readPlan(event.RateLimits, file) // every token_count names the plan, counted or not
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
	var last codexCounts
	if !decode(event.Info.Last, &last) || (last.Input == 0 && last.Output == 0) {
		return nil // a synthetic estimate after compaction
	}
	sum := operation.SHA256Hex([]byte(total + "\n" + canonicalJSON(event.Info.Last) + "\n" +
		canonicalJSON(event.RateLimits)))
	u := state.row(ts, "tc:"+sum)
	if first && state.Copies == 1 && total != canonicalJSON(event.Info.Last) {
		u.CopyOf = state.Fork + "@" + state.Spawn // the parent's, copied into the fork
	}
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
		Account   string          `json:"creator_account_id"`
		Source    json.RawMessage `json:"source"`
	}
	if !decode(payload, &meta) || meta.ID == "" {
		return
	}
	state.Thread, state.Cwd = meta.ID, cmp.Or(meta.Cwd, state.Cwd)
	state.Fork, state.Version = meta.Forked, codexVersion(meta.Version)
	state.Account = meta.Account
	state.Root = codexRoot(meta.ID, meta.SessionID, meta.Source)
	state.Parent = codexParent(meta.Source)
	state.Spawned = state.Parent != ""
	if state.Spawned {
		file.Parents = append(file.Parents, ThreadParent{Thread: meta.ID, Parent: state.Parent})
	}
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
			From: turnTierFrom(cmp.Or(state.Parent, state.Root), state.Spawn),
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
// differs from the thread id, else the spawning parent, which for a deeper
// subagent of an older Codex is not yet the root (the ledger walks the
// parents' links, see ThreadParent); "" for a root thread.
func codexRoot(id, sessionID string, source json.RawMessage) string {
	if sessionID != "" && sessionID != id {
		return sessionID
	}
	return codexParent(source)
}
