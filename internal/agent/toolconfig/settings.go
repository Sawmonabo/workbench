package toolconfig

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/Sawmonabo/workbench/internal/platform"
)

// claudeManagedSettings are the machine's managed settings files, in the
// order Claude Code merges them, a later one overriding an earlier:
// managed-settings.json, then each managed-settings.d/*.json in name order.
func claudeManagedSettings() []string {
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

// ClaudeSettingsFile is what Workbench reads of one Claude Code settings
// file: env.CLAUDE_CONFIG_DIR (ConfigDir), autoUpdatesChannel (Channel) and
// minimumVersion (Minimum), each "" when the file does not set it.
type ClaudeSettingsFile struct {
	Path, ConfigDir, Channel, Minimum string
}

// ClaudeSettings are the Claude Code settings files that apply to the user,
// in the order Claude Code merges them, a later one overriding an earlier:
// settings.json in [ClaudeDir], then claudeManagedSettings. Claude Code
// takes its user settings from the folder CLAUDE_CONFIG_DIR ends at, a
// settings file's env included. Project and local settings depend on the
// folder Claude Code starts in and are not read. A file Claude Code skips is
// left out (see readClaudeSettings).
func ClaudeSettings(home string) []ClaudeSettingsFile {
	return claudeSettings(ClaudeDir(home))
}

// ClaudeUserSettings is the user's settings.json, in [ClaudeDir], which holds
// the hooks Workbench installs.
func ClaudeUserSettings(home string) string {
	return filepath.Join(ClaudeDir(home), "settings.json")
}

// claudeSettings are the user's settings.json in dir, then the managed
// settings files, each read as readClaudeSettings reads it; a missing or
// skipped one is left out.
func claudeSettings(dir string) []ClaudeSettingsFile {
	var files []ClaudeSettingsFile
	for _, path := range append(
		[]string{filepath.Join(dir, "settings.json")},
		claudeManagedSettings()...,
	) {
		if file, ok := readClaudeSettings(path); ok {
			files = append(files, file)
		}
	}
	return files
}

// claudeChannels are the values Claude Code's settings accept for
// autoUpdatesChannel.
var claudeChannels = []string{"latest", "stable", "rc"}

// readClaudeSettings reads one settings file the way Claude Code takes it;
// ok is false for a file it skips. Claude Code skips a file with an error
// entirely, not just the setting in error: one that is not a JSON object, or
// in which a setting holds a value Claude Code refuses, null included, so a
// minimumVersion that is not a string drops the file's env and
// autoUpdatesChannel too. Only the settings read here can be judged; a file
// Claude Code skips for another setting is still read. Keys match exactly,
// as Claude Code matches them. Claude Code accepts an env value of any type
// and takes one that is not a string as its JavaScript text (5 is "5", null
// "null"), which is never an absolute path, so such a CLAUDE_CONFIG_DIR is
// kept as its JSON text, a relative path.
func readClaudeSettings(path string) (ClaudeSettingsFile, bool) {
	file := ClaudeSettingsFile{Path: path}
	raw, err := os.ReadFile(path)
	var settings map[string]json.RawMessage
	if err != nil || json.Unmarshal(raw, &settings) != nil || settings == nil {
		return file, false
	}
	if value, set := settings["env"]; set {
		var env map[string]json.RawMessage
		if json.Unmarshal(value, &env) != nil || env == nil {
			return file, false
		}
		if dir, set := env["CLAUDE_CONFIG_DIR"]; set {
			var text bool
			if file.ConfigDir, text = claudeString(dir); !text {
				file.ConfigDir = string(dir)
			}
		}
	}
	if value, set := settings["autoUpdatesChannel"]; set {
		channel, ok := claudeString(value)
		if !ok || !slices.Contains(claudeChannels, channel) {
			return file, false
		}
		file.Channel = channel
	}
	if value, set := settings["minimumVersion"]; set {
		minimum, ok := claudeString(value)
		if !ok {
			return file, false
		}
		file.Minimum = minimum
	}
	return file, true
}

// claudeString is the string a setting holds; ok is false for any other
// value, null included.
func claudeString(raw json.RawMessage) (string, bool) {
	var value *string
	if json.Unmarshal(raw, &value) != nil || value == nil {
		return "", false
	}
	return *value, true
}
