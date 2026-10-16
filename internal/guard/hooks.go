package guard

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
	"time"

	"github.com/Sawmonabo/workbench/internal/operation"
)

// hookInput is the part of a Claude Code or Codex hook's stdin the guard
// reads. Codex's carries turn_id; Claude Code's tool_use_id starts "toolu_".
type hookInput struct {
	Event     string  `json:"hook_event_name"`
	Tool      string  `json:"tool_name"`
	ToolUseID string  `json:"tool_use_id"`
	SessionID string  `json:"session_id"`
	TurnID    *string `json:"turn_id"`
	Input     struct {
		Command    string `json:"command"`
		Background bool   `json:"run_in_background"`
	} `json:"tool_input"`
}

func readInput(stdin *os.File) (hookInput, bool) {
	var input hookInput
	data := operation.ReadHookInput(stdin)
	if json.Unmarshal(data, &input) != nil {
		return input, false
	}
	return input, idPattern.MatchString(input.ToolUseID)
}

// agentKind tells the agent from the hook input; an unknown one is not
// guarded.
func agentKind(input hookInput) (kind, bool) {
	switch {
	case strings.HasPrefix(input.ToolUseID, "toolu_") && input.TurnID == nil:
		return claude, true
	case input.TurnID != nil:
		return codex, true
	}
	return "", false
}

// maxShellAncestors bounds the walk past nested `sh -c`.
const maxShellAncestors = 8

// initNames are the init processes a shell whose agent exited is handed to:
// the machine's, and a container's minimal init (Docker's --init runs
// docker-init, which is tini; Podman's runs catatonit as podman-init).
var initNames = map[string]bool{
	"launchd": true, "systemd": true, "init": true,
	"docker-init": true, "tini": true, "dumb-init": true, "catatonit": true, "podman-init": true,
}

// findAgent is the hook's nearest ancestor that is not a shell, starting at
// pid: the agent that ran the hook through `sh -c`. Codex runs hooks through
// $SHELL, which may be fish. An agent may be PID 1, as in a container started
// without an init; an init (initNames) never is.
func findAgent(pid int) (proc, error) {
	for range maxShellAncestors {
		p, err := processInfo(pid)
		if err != nil {
			return proc{}, err
		}
		if !shells[p.Name] && p.Name != "fish" {
			if p.PID == 1 && initNames[p.Name] {
				break
			}
			return p, nil
		}
		if pid = p.PPID; pid < 1 {
			break
		}
	}
	return proc{}, errors.New("no agent among the hook's ancestors")
}

// Pre is the PreToolUse hook on Bash. It leaves a note for the call and
// starts the watcher when none runs. It prints nothing and never fails:
// whatever goes wrong leaves the call unguarded, never blocked.
// WORKBENCH_GUARD=off turns it off; WORKBENCH_GUARD=dry-run notes the call so
// the watcher only logs what it would stop.
func Pre(stdin *os.File) {
	var f folders
	defer func() {
		if p := recover(); p != nil && f.root != "" {
			f.logf("pre: panic: %v", p)
		}
	}()
	mode := os.Getenv("WORKBENCH_GUARD")
	input, ok := readInput(stdin)
	if mode == "off" || !ok || input.Event != "PreToolUse" || input.Tool != "Bash" ||
		input.Input.Command == "" {
		return
	}
	agentType, known := agentKind(input)
	if !known {
		return
	}
	var err error
	if f, err = openFolders(); err != nil {
		return
	}
	agent, err := findAgent(os.Getppid())
	if err != nil {
		f.logf("pre %s: %v", input.ToolUseID, err)
		return
	}
	self, err := processInfo(os.Getpid())
	if err != nil {
		f.logf("pre %s: %v", input.ToolUseID, err)
		return
	}
	n := note{
		AgentPID: agent.PID, AgentStart: agent.Start, Kind: agentType,
		SessionID: input.SessionID, ToolUseID: input.ToolUseID, Command: input.Input.Command,
		NotedStart: self.Start, NotedAt: time.Now(), DryRun: mode == "dry-run",
	}
	if err = writeJSON(f.note(n.ToolUseID), n); err != nil {
		f.logf("pre %s: %v", n.ToolUseID, err)
		return
	}
	startWatcher(f)
}

// startWatcher starts `workbench guard watch` detached when the lock is free.
// The watcher takes the lock itself; one that loses a race exits at once.
func startWatcher(f folders) {
	release, held, err := operation.TryLock(f.lock())
	if err != nil || held {
		return
	}
	release()
	if err = operation.StartDetached([]string{"guard", "watch"}, f.log()); err != nil {
		f.logf("pre: cannot start the watcher: %v", err)
	}
}

// Post is the PostToolUse hook on Bash, and Claude Code's PostToolUseFailure.
// It drops the call's note, except for a call sent to the background
// (run_in_background), whose command runs on after this hook: it marks that
// note posted and the watcher drops it (see watcher.notes). When the watcher
// stopped this call, it prints the reason as additionalContext, which the
// agent sees beside the tool result. Otherwise it prints nothing. It never
// fails.
func Post(stdin *os.File, stdout io.Writer) {
	var f folders
	defer func() {
		if p := recover(); p != nil && f.root != "" {
			f.logf("post: panic: %v", p)
		}
	}()
	input, ok := readInput(stdin)
	if !ok {
		return
	}
	var err error
	if f, err = openFolders(); err != nil {
		return
	}
	path := f.note(input.ToolUseID)
	var n note
	switch {
	case !input.Input.Background:
		_ = os.Remove(path)
	case readJSON(path, &n) == nil:
		n.Posted = true
		if err = writeJSON(path, n); err != nil {
			f.logf("post %s: %v", input.ToolUseID, err)
		}
	}
	var record kill
	if readJSON(f.record(input.ToolUseID), &record) != nil {
		return
	}
	_ = os.Remove(f.record(input.ToolUseID))
	_ = json.NewEncoder(stdout).Encode(map[string]any{
		"hookSpecificOutput": map[string]string{
			"hookEventName":     input.Event,
			"additionalContext": record.Reason,
		},
	})
}
