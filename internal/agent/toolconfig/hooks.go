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
	HookSkipped   = "skipped"   // the tool cannot use the file that holds it or its trust
)

// ApplyFixes is what workbench apply does about a tool's hooks in states, as
// doctor and agent costs status say it: "installs" when one is HookMissing,
// and "trusts" when one is HookUntrusted or, for Codex, which runs a hook only
// once it is recorded as trusted, when one is missing. It is nothing while a
// variable moves the tool, named by its [Move] title, away from the folder
// Workbench installs in (see [Moves]), as the apply installs nothing there.
func ApplyFixes(home, title string, states []string) []string {
	absent := slices.Contains(states, HookMissing)
	var fixes []string
	if absent {
		fixes = append(fixes, "installs")
	}
	if slices.Contains(states, HookUntrusted) || absent && title == codexTitle {
		fixes = append(fixes, "trusts")
	}
	if len(fixes) == 0 || slices.ContainsFunc(Moves(home), func(m Move) bool {
		return m.Tool == title && m.Source != ""
	}) {
		return nil
	}
	return fixes
}

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

// CodexHooksFiles are the spellings of the hooks.json Workbench installs in
// home's .codex folder that Codex keys the trust of its hooks by, as
// [codexHooksKeyFile] does: ~/.codex/hooks.json with ~ as $HOME spells it,
// which Codex uses while CODEX_HOME is unset, then that file with its
// symlinks resolved, which a CODEX_HOME naming the same folder gives, when it
// differs. Workbench reads home with its symlinks resolved, so the two differ
// when $HOME or ~/.codex is a symlink or crosses one. The apply trusts each
// of Workbench's hooks under both, and both name the one file it installs.
func CodexHooksFiles(home string) []string {
	files := []string{filepath.Join(homeAsSpelled(home), ".codex", "hooks.json")}
	dir := filepath.Join(home, ".codex")
	if real, err := filepath.EvalSymlinks(dir); err == nil {
		dir = real
	} else if real, err = filepath.EvalSymlinks(home); err == nil {
		dir = filepath.Join(real, ".codex") // the folder the apply creates
	}
	if real := filepath.Join(dir, "hooks.json"); real != files[0] {
		files = append(files, real)
	}
	return files
}

// codexHooksKeyFile is hooks.json as Codex spells it in the trust keys of its
// hooks (codex-rs utils/home-dir find_codex_home and hooks/src/engine/
// discovery.rs load_hooks_json): in CODEX_HOME with its symlinks resolved
// when that is set ([CodexDir]), else in ~/.codex with ~ as $HOME spells it,
// cleaned as Codex cleans a path (utils/absolute-path), symlinks kept.
func codexHooksKeyFile(home string) string {
	if os.Getenv("CODEX_HOME") != "" {
		return filepath.Join(CodexDir(home), "hooks.json")
	}
	return filepath.Join(homeAsSpelled(home), ".codex", "hooks.json")
}

// homeAsSpelled is $HOME as it is set, cleaned, when it is absolute and names
// the folder home, which Workbench holds with its symlinks resolved; home
// otherwise, such as for an isolated destination.
func homeAsSpelled(home string) string {
	if env := os.Getenv("HOME"); filepath.IsAbs(env) && SameDir(env, home) {
		return filepath.Clean(env)
	}
	return home
}

// CodexHooks reports, for each of hooks, whether hooks.json runs it on its event
// and Codex runs it: HookOK when config.toml trusts that exact hook, under the
// key Codex reads ([codexHooksKeyFile]), and does not disable it, HookDisabled
// when it sets enabled = false, HookUntrusted when it records no trusted_hash
// or another one (Codex ignores the hook then), and HookMissing without the
// hook; HookSkipped for every hook when config.toml ([CodexConfig]) is there
// but cannot be parsed, which Codex does not start with and no apply
// replaces. Of several matching hooks for an event the best state counts.
func CodexHooks(home string, hooks []HookCommand) []string {
	dir := CodexDir(home)
	path := filepath.Join(dir, "hooks.json")
	key := codexHooksKeyFile(home)
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
	out := make([]string, len(hooks))
	if raw, err := os.ReadFile(CodexConfig(home)); err == nil &&
		toml.Unmarshal(raw, &config) != nil {
		for i := range out {
			out[i] = HookSkipped
		}
		return out
	}
	// Better states come first.
	rank := []string{HookOK, HookDisabled, HookUntrusted, HookMissing}
	for i, want := range hooks {
		event := want.Event
		best := HookMissing
		for gi, group := range file.Hooks[event] {
			for hi, hook := range group.Hooks {
				if hook.Type != "command" || !want.Command.MatchString(hook.Command) {
					continue
				}
				state := config.Hooks.State[fmt.Sprintf("%s:%s:%d:%d", key, codexEvents[event], gi, hi)]
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
