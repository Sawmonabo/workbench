package toolconfig

import (
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
// override the user's, and project and local settings cannot set it. The
// user settings that can set it are those in the folder the environment
// names, read with the managed settings as claudeSettings reads them. A
// settings file's value is copied as written, so it must be an absolute path
// (https://code.claude.com/docs/en/env-vars); one that is not depends on the
// folder Claude Code starts in, so it is left out and returned in relative.
func claudeConfigDir(home string) (dir, source string, relative []RelativeSetting) {
	dir = filepath.Join(home, ".claude")
	if env := os.Getenv("CLAUDE_CONFIG_DIR"); env != "" {
		dir, source = env, "the environment"
	}
	for _, settings := range claudeSettings(dir) {
		switch {
		case settings.ConfigDir == "":
		case !filepath.IsAbs(settings.ConfigDir):
			relative = append(
				relative,
				RelativeSetting{File: settings.Path, Value: settings.ConfigDir},
			)
		default:
			dir, source = settings.ConfigDir, settings.Path
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

// CodexConfig is Codex's config.toml, in [CodexDir], which holds the trust of
// the hooks Workbench installs.
func CodexConfig(home string) string { return filepath.Join(CodexDir(home), "config.toml") }
