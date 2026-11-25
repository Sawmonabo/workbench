package costs

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

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

// ConfigMove reports CODEX_HOME pointing Codex away from ~/.codex.
func (codex) ConfigMove(home string) (ConfigMove, bool) {
	move := ConfigMove{
		Tool:     "Codex",
		Variable: "CODEX_HOME",
		Source:   "the environment",
		Dir:      codexDir(home),
		Default:  filepath.Join(home, ".codex"),
	}
	return move, os.Getenv("CODEX_HOME") != "" && !sameDir(move.Dir, move.Default)
}

// TranscriptRoots is where this tool's transcripts live.
func (codex) TranscriptRoots(home string) []string {
	return []string{
		filepath.Join(codexDir(home), "sessions"),
		filepath.Join(codexDir(home), "archived_sessions"),
	}
}

// Transcripts lists every *.jsonl under sessions/ and archived_sessions/.
func (codex) Transcripts(home string) ([]string, error) {
	var files []string
	for _, root := range (codex{}).TranscriptRoots(home) {
		found, err := codexWalk(root, ".jsonl")
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

// Hooks reports, for each of hooks, whether hooks.json runs it on its event
// and Codex runs it: HookOK when config.toml trusts that exact hook and does
// not disable it, HookDisabled when it sets enabled = false, HookUntrusted
// when it records no trusted_hash or another one (Codex ignores the hook then),
// and HookMissing without the hook. Of several matching hooks for an event the
// best state counts.
func (codex) Hooks(home string, hooks []HookCommand) []string {
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
	out := make([]string, len(hooks))
	for i, want := range hooks {
		event := want.Event
		best := HookMissing
		for gi, group := range file.Hooks[event] {
			for hi, hook := range group.Hooks {
				if hook.Type != "command" || !want.Command.MatchString(hook.Command) {
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
		out[i] = best
	}
	return out
}
