package costs

import (
	"os"
	"path/filepath"

	"github.com/Sawmonabo/workbench/internal/agent/toolconfig"
)

// claude is the Claude Code source: JSONL transcripts under
// <config dir>/projects, where the config dir is CLAUDE_CONFIG_DIR, else
// ~/.claude.
type claude struct{}

func (claude) Name() string { return "claude" }

// TranscriptRoots is where Claude Code writes its transcripts: projects/ in
// its config folder and, when CLAUDE_CONFIG_DIR moves that, also
// ~/.claude/projects, which may still hold transcripts written before the
// move. A response read from both is stored once, by its request ID.
func (claude) TranscriptRoots(home string) []string {
	roots := []string{filepath.Join(toolconfig.ClaudeDir(home), "projects")}
	if old := filepath.Join(home, ".claude", "projects"); !toolconfig.SameDir(roots[0], old) {
		roots = append(roots, old)
	}
	return roots
}

// Transcripts lists every *.jsonl under each transcript root, subagent
// transcripts included.
func (claude) Transcripts(home string) ([]string, error) {
	return transcriptFiles((claude{}).TranscriptRoots(home), ".jsonl")
}

// Observations are the (tokens, costUSD) pairs Claude Code keeps per project
// and model in .claude.json (lastModelUsage), for calibration. Those records do
// not separate fast mode, so a model's pairs can mix standard and fast cost.
// That cannot misprice a response: mixes in differing proportions miss the
// five percent residual and calibrate nothing, and a model used only in fast
// mode calibrates a standard rate that no standard response of it uses, while
// its "@fast" responses take the fast rows.
func (claude) Observations(home string) []Observation {
	var data struct {
		Projects map[string]struct {
			LastModelUsage map[string]struct {
				Input         float64 `json:"inputTokens"`
				Output        float64 `json:"outputTokens"`
				CacheCreation float64 `json:"cacheCreationInputTokens"`
				CacheRead     float64 `json:"cacheReadInputTokens"`
				Cost          float64 `json:"costUSD"`
			} `json:"lastModelUsage"`
		} `json:"projects"`
	}
	raw, err := os.ReadFile(toolconfig.ClaudeJSON(home))
	if err != nil || !decode(raw, &data) {
		return nil
	}
	var out []Observation
	for _, project := range data.Projects {
		for model, u := range project.LastModelUsage {
			x := [4]float64{u.Input / 1e6, u.Output / 1e6, u.CacheCreation / 1e6, u.CacheRead / 1e6}
			if u.Cost > 0 && x != [4]float64{} {
				out = append(out, Observation{Model: model, Tokens: x, Cost: u.Cost})
			}
		}
	}
	return out
}
