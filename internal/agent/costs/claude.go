package costs

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/Sawmonabo/workbench/internal/platform"
)

// claude is the Claude Code source: JSONL transcripts under
// <config dir>/projects, where the config dir is CLAUDE_CONFIG_DIR, else
// ~/.claude.
type claude struct{}

func (claude) Name() string { return "claude" }

// ClaudeDir is where Claude Code keeps its state and its user settings:
// CLAUDE_CONFIG_DIR as Claude Code resolves it (see claudeConfigDir), else
// ~/.claude.
func ClaudeDir(home string) string {
	dir, _, _ := claudeConfigDir(home)
	return dir
}

// claudeConfigDir is Claude Code's config folder and where CLAUDE_CONFIG_DIR
// was set for it: "the environment", the settings file whose env sets it, or
// "" when nothing does and the folder is ~/.claude. Claude Code takes the
// variable from its environment, its user settings or its managed settings;
// a settings file's env replaces the environment's value, managed settings
// override the user's, and project and local settings cannot set it. A
// settings file's value is copied as written, so it must be an absolute path
// (https://code.claude.com/docs/en/env-vars); one that is not depends on the
// folder Claude Code starts in, so it is left out and returned in relative.
func claudeConfigDir(home string) (dir, source string, relative []RelativeSetting) {
	dir = filepath.Join(home, ".claude")
	if env := os.Getenv("CLAUDE_CONFIG_DIR"); env != "" {
		dir, source = env, "the environment"
	}
	for _, path := range append(
		[]string{filepath.Join(dir, "settings.json")},
		ClaudeManagedSettings()...,
	) {
		var settings struct {
			Env struct {
				Dir string `json:"CLAUDE_CONFIG_DIR"`
			} `json:"env"`
		}
		raw, err := os.ReadFile(path)
		switch {
		case err != nil, !decode(raw, &settings), settings.Env.Dir == "":
		case !filepath.IsAbs(settings.Env.Dir):
			relative = append(relative, RelativeSetting{File: path, Value: settings.Env.Dir})
		default:
			dir, source = settings.Env.Dir, path
		}
	}
	return dir, source, relative
}

// ClaudeManagedSettings are the machine's managed settings files, in the
// order Claude Code merges them, a later one overriding an earlier:
// managed-settings.json, then each managed-settings.d/*.json in name order.
func ClaudeManagedSettings() []string {
	managed := platform.ClaudeManagedFolder
	files := []string{filepath.Join(managed, "managed-settings.json")}
	drops, _ := filepath.Glob(filepath.Join(managed, "managed-settings.d", "*.json"))
	for _, drop := range drops {
		if !strings.HasPrefix(filepath.Base(drop), ".") {
			files = append(files, drop)
		}
	}
	return files
}

// ConfigMove reports CLAUDE_CONFIG_DIR pointing Claude Code away from
// ~/.claude, or set to a relative path in a settings file.
func (claude) ConfigMove(home string) (ConfigMove, bool) {
	dir, source, relative := claudeConfigDir(home)
	move := ConfigMove{
		Tool:     "Claude Code",
		Variable: "CLAUDE_CONFIG_DIR",
		Source:   source,
		Dir:      dir,
		Default:  filepath.Join(home, ".claude"),
		Relative: relative,
	}
	if sameDir(dir, move.Default) {
		move.Source = ""
	}
	return move, move.Source != "" || len(relative) > 0
}

// ClaudeJSON is Claude Code's account, usage and MCP server file: inside
// CLAUDE_CONFIG_DIR when that is set, directly in the home directory otherwise.
func ClaudeJSON(home string) string {
	if dir, source, _ := claudeConfigDir(home); source != "" {
		return filepath.Join(dir, ".claude.json")
	}
	return filepath.Join(home, ".claude.json")
}

// TranscriptRoots is where Claude Code writes its transcripts: projects/ in
// its config folder and, when CLAUDE_CONFIG_DIR moves that, also
// ~/.claude/projects, which may still hold transcripts written before the
// move. A response read from both is stored once, by its request ID.
func (claude) TranscriptRoots(home string) []string {
	roots := []string{filepath.Join(ClaudeDir(home), "projects")}
	if old := filepath.Join(home, ".claude", "projects"); !sameDir(roots[0], old) {
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
	raw, err := os.ReadFile(ClaudeJSON(home))
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

// Hooks reports, for each of hooks, whether Claude Code's settings.json runs
// it on its event synchronously: HookOK, else HookMissing. An async hook does
// not count: Claude Code kills a background hook still running when a session
// ends in headless (-p) mode, and Workbench's hooks return within
// milliseconds anyway. Claude Code has no hook trust or per-hook switch.
func (claude) Hooks(home string, hooks []HookCommand) []string {
	var settings struct {
		Hooks map[string][]struct {
			Hooks []struct {
				Command string `json:"command"`
				Async   bool   `json:"async"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if raw, err := os.ReadFile(filepath.Join(ClaudeDir(home), "settings.json")); err == nil {
		decode(raw, &settings)
	}
	out := make([]string, len(hooks))
	for i, want := range hooks {
		out[i] = HookMissing
		for _, group := range settings.Hooks[want.Event] {
			for _, hook := range group.Hooks {
				if want.Command.MatchString(hook.Command) && !hook.Async {
					out[i] = HookOK
				}
			}
		}
	}
	return out
}
