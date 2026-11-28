package toolconfig

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"

	"github.com/pelletier/go-toml/v2"

	"github.com/Sawmonabo/workbench/internal/jsonc"
	"github.com/Sawmonabo/workbench/internal/operation"
)

// The states of a hook Workbench installs in a tool, as ClaudeHooks and
// CodexHooks report them.
const (
	HookOK        = "ok"        // installed, and the tool runs it
	HookMissing   = "missing"   // not installed as Workbench installs it
	HookUntrusted = "untrusted" // installed, but the tool has not recorded it as trusted
	HookDisabled  = "disabled"  // installed and trusted, but turned off in the tool
	HookSkipped   = "skipped"   // the tool skips the settings file that would hold it
)

// HookCommand is one hook Workbench installs in a tool: the event it runs on and a
// match for its command.
type HookCommand struct {
	Event   string
	Command *regexp.Regexp
}

// ClaudeHooks reports, for each of hooks, whether Claude Code's settings.json runs
// it on its event synchronously: HookOK, else HookMissing, or HookSkipped for
// every hook when the file is there but Claude Code skips it (see
// readClaudeSettings), so it runs none of the hooks the file holds. An async
// hook does not count: Claude Code kills a background hook still running when
// a session ends in headless (-p) mode, and Workbench's hooks return within
// milliseconds anyway. Claude Code has no hook trust or per-hook switch.
func ClaudeHooks(home string, hooks []HookCommand) []string {
	var settings struct {
		Hooks map[string][]struct {
			Hooks []struct {
				Command string `json:"command"`
				Async   bool   `json:"async"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	path := ClaudeUserSettings(home)
	out := make([]string, len(hooks))
	if raw, err := os.ReadFile(path); err == nil {
		if _, read := readClaudeSettings(path); !read {
			for i := range out {
				out[i] = HookSkipped
			}
			return out
		}
		_ = json.Unmarshal(raw, &settings)
	}
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
	encoded, _ := jsonc.Marshal(identity) // strings, numbers and bools always encode
	return "sha256:" + operation.SHA256Hex(encoded)
}

// CodexHooks reports, for each of hooks, whether hooks.json runs it on its event
// and Codex runs it: HookOK when config.toml trusts that exact hook and does
// not disable it, HookDisabled when it sets enabled = false, HookUntrusted
// when it records no trusted_hash or another one (Codex ignores the hook then),
// and HookMissing without the hook. Of several matching hooks for an event the
// best state counts.
func CodexHooks(home string, hooks []HookCommand) []string {
	dir := CodexDir(home)
	path := filepath.Join(dir, "hooks.json")
	var file struct {
		Hooks map[string][]codexGroup `json:"hooks"`
	}
	if raw, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(raw, &file)
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
