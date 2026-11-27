package costs

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/Sawmonabo/workbench/internal/agent/toolconfig"
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

// TranscriptRoots is where this tool's transcripts live.
func (codex) TranscriptRoots(home string) []string {
	return []string{
		filepath.Join(toolconfig.CodexDir(home), "sessions"),
		filepath.Join(toolconfig.CodexDir(home), "archived_sessions"),
	}
}

// Transcripts lists every *.jsonl under sessions/ and archived_sessions/.
func (codex) Transcripts(home string) ([]string, error) {
	return transcriptFiles((codex{}).TranscriptRoots(home), ".jsonl")
}

// Skipped counts the compressed rollouts (.jsonl.zst) Workbench does not read.
func (codex) Skipped(home string) int {
	found, _ := transcriptFiles((codex{}).TranscriptRoots(home), ".zst")
	return len(found)
}

// ServedTier is the tier Codex sends a model's request at when tier is
// selected. Codex sends a tier only to a model whose catalog entry lists it,
// flex excepted, and none but flex with the fast_mode feature off
// (openai_models.rs service_tier_for_request, session get_service_tier). It
// keeps its catalog in models_cache.json; a model the cache does not list
// keeps the selected tier, as the cache holds only the catalog Codex fetched
// last. fast_mode is on unless config.toml turns it off.
func (codex) ServedTier(home string) func(model, tier string) string {
	dir := toolconfig.CodexDir(home)
	var config struct {
		Features struct {
			FastMode *bool `toml:"fast_mode"`
		} `toml:"features"`
	}
	if raw, err := os.ReadFile(filepath.Join(dir, "config.toml")); err == nil {
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
	if raw, err := os.ReadFile(filepath.Join(dir, "models_cache.json")); err == nil {
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
