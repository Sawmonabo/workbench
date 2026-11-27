package toolconfig

import (
	"encoding/json"
	"os"
	"path/filepath"
)

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
		case err != nil, json.Unmarshal(raw, &settings) != nil, settings.Env.Dir == "":
		case !filepath.IsAbs(settings.Env.Dir):
			relative = append(relative, RelativeSetting{File: path, Value: settings.Env.Dir})
		default:
			dir, source = settings.Env.Dir, path
		}
	}
	return dir, source, relative
}

// ClaudeJSON is Claude Code's account, usage and MCP server file: inside
// CLAUDE_CONFIG_DIR when that is set, directly in the home directory otherwise.
func ClaudeJSON(home string) string {
	if dir, source, _ := claudeConfigDir(home); source != "" {
		return filepath.Join(dir, ".claude.json")
	}
	return filepath.Join(home, ".claude.json")
}

// CodexDir is the Codex home as Codex resolves it (codex-rs utils/home-dir
// find_codex_home): CODEX_HOME made absolute with its symlinks resolved, else
// ~/.codex as is. The hooks trust keys Codex writes start with this path.
func CodexDir(home string) string {
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
