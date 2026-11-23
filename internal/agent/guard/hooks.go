package guard

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/Sawmonabo/workbench/internal/agent/costs"
	"github.com/Sawmonabo/workbench/internal/operation"
	"github.com/Sawmonabo/workbench/internal/platform"
)

// guardCommand matches a guard hook's command as Workbench installs it: the
// workbench executable, by name, by path or as a single-quoted path, then
// "agent guard" and the part, then "|| true", so a failing hook never blocks
// the agent.
func guardCommand(part string) *regexp.Regexp {
	return regexp.MustCompile(
		`(^|/)workbench'? agent guard ` + regexp.QuoteMeta(part) + ` \|\| true$`,
	)
}

// Hooks are the guard hooks Workbench installs in the agent named tool
// ("claude" or "codex", the costs tool names): the session hook that starts
// the watcher, the pre hook before a Bash call and the post hook after it,
// which Claude Code also runs when a call fails.
func Hooks(tool string) []costs.HookCommand {
	hooks := []costs.HookCommand{
		{Event: "SessionStart", Command: guardCommand("session " + tool)},
		{Event: "PreToolUse", Command: guardCommand("pre")},
		{Event: "PostToolUse", Command: guardCommand("post")},
	}
	if tool == "claude" {
		hooks = append(
			hooks,
			costs.HookCommand{Event: "PostToolUseFailure", Command: guardCommand("post")},
		)
	}
	return hooks
}

// hookInput is the part of a Claude Code or Codex hook's stdin the guard
// reads. Codex's carries turn_id; Claude Code's tool_use_id starts "toolu_".
// Both carry cwd. Claude Code sets agent_id and agent_type only for a call a
// subagent makes.
type hookInput struct {
	Event     string  `json:"hook_event_name"`
	Tool      string  `json:"tool_name"`
	ToolUseID string  `json:"tool_use_id"`
	SessionID string  `json:"session_id"`
	TurnID    *string `json:"turn_id"`
	Cwd       string  `json:"cwd"`
	AgentType string  `json:"agent_type"`
	AgentID   string  `json:"agent_id"`
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
		p, err := platform.ProcessInfo(pid)
		if err != nil {
			return proc{}, err
		}
		if !shells[p.Name] && p.Name != "fish" {
			if p.PID == 1 && initNames[p.Name] {
				break
			}
			return proc{Process: p}, nil
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
			f.event(slog.LevelError, EventError, "panic", "error", fmt.Sprint(p))
		}
	}()
	mode := os.Getenv(modeVariable)
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
	if f, err = hookFolders("pre"); err != nil {
		return
	}
	agent, err := findAgent(os.Getppid())
	if err != nil {
		f.event(slog.LevelError, EventError, "cannot find the agent; the call is not guarded",
			"call", input.ToolUseID, "error", err)
		return
	}
	self, err := platform.ProcessInfo(os.Getpid())
	if err != nil {
		f.event(
			slog.LevelError,
			EventError,
			"cannot read the hook's process; the call is not guarded",
			"call",
			input.ToolUseID,
			"error",
			err,
		)
		return
	}
	n := note{
		AgentPID: agent.PID, AgentStart: agent.Start, Kind: agentType,
		SessionID: input.SessionID, ToolUseID: input.ToolUseID, Command: input.Input.Command,
		NotedStart: self.Start, NotedAt: time.Now(), DryRun: mode == "dry-run",
		Project: projectName(input.Input.Command, input.Cwd),
	}
	n.Cwd, n.Folder = callFolders(input)
	if input.AgentID != "" {
		n.Subagent = input.AgentType
	}
	wrote := false
	work := func() error {
		if !wrote {
			if err := writeJSON(f.note(n.ToolUseID), n); err != nil {
				return err
			}
			wrote = true
		}
		return startWatcher(f)
	}
	if err = work(); err != nil && f.again() {
		err = work()
	}
	switch {
	case !wrote:
		f.event(slog.LevelError, EventError, "cannot write the note; the call is not guarded",
			"call", n.ToolUseID, "error", err)
	case err != nil:
		f.event(slog.LevelError, EventError, "cannot start the watcher", "error", err)
	}
}

// hookFolders opens the guard folder for a hook's part: it first moves the
// folder from its old path when no watcher holds it (see Move), and logs a
// move that failed.
func hookFolders(part string) (folders, error) {
	state, err := stateFolder()
	if err != nil {
		return folders{}, err
	}
	moved, moveErr := Move(state)
	f, err := openFolders(state, part)
	if err != nil {
		return f, err
	}
	if moved {
		f.event(slog.LevelInfo, EventMoved, "moved the guard folder to "+f.root)
	}
	if moveErr != nil {
		f.event(slog.LevelError, EventError, "cannot move the guard folder", "error", moveErr)
	}
	return f, nil
}

// Session is the SessionStart hook of Claude Code (agent "claude") and Codex
// ("codex"). It leaves a file saying its agent runs, so the guard knows the
// agent from its start, and removes the files of agents that are gone, so
// they never pile up while no watcher runs. Under Claude Code it also adds
// stampLine to $CLAUDE_ENV_FILE, once, so every later Bash command of the
// session carries its call stamp. Each of the three goes ahead when another
// fails: a later note makes the agent known too, and a stamp of an agent the
// guard does not know counts for nothing. It starts no watcher of its own,
// only the one a move of the guard folder starts (see Move), prints nothing
// and never fails. WORKBENCH_GUARD=off turns it off.
func Session(stdin *os.File, agent string) {
	var f folders
	defer func() {
		if p := recover(); p != nil && f.root != "" {
			f.event(slog.LevelError, EventError, "panic", "error", fmt.Sprint(p))
		}
	}()
	agentType := kind(agent)
	var input hookInput
	if os.Getenv(modeVariable) == "off" || agentType != claude && agentType != codex ||
		json.Unmarshal(operation.ReadHookInput(stdin), &input) != nil ||
		input.Event != "SessionStart" {
		return
	}
	var err error
	if f, err = hookFolders("session"); err != nil {
		return
	}
	found, err := findAgent(os.Getppid())
	if err != nil {
		f.event(
			slog.LevelError,
			EventError,
			"cannot find the agent; it is known from its notes only",
			"error",
			err,
		)
	}
	wrote, listed := err != nil, false
	work := func() error {
		var writeErr, listErr error
		if !wrote {
			writeErr = writeJSON(f.agent(found.PID, found.Start), agentFile{
				PID: found.PID, Start: found.Start, Kind: agentType, At: time.Now(),
			})
			wrote = writeErr == nil
		}
		if !listed {
			listErr = f.removeGoneAgents()
			listed = listErr == nil
		}
		return errors.Join(writeErr, listErr)
	}
	if err = work(); err != nil && f.again() {
		err = work()
	}
	if !wrote {
		f.event(
			slog.LevelError,
			EventError,
			"cannot write the agent file; it is known from its notes only",
			"error",
			err,
		)
	}
	if path := os.Getenv("CLAUDE_ENV_FILE"); agentType == claude && path != "" {
		if err = appendStampLine(path); err != nil {
			f.event(
				slog.LevelError,
				EventError,
				"cannot add the call stamp to the session's env file",
				"error",
				err,
			)
		}
	}
}

// removeGoneAgents removes each agent file whose process has exited or is
// another process reusing its PID, and each file whose name does not parse as
// <pid>-<start>.json, which no agent file ever is. A file whose process cannot
// be read for any other reason stays. It gives the error listing the folder.
func (f folders) removeGoneAgents() error {
	entries, err := listFolder(f.agents)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		path := filepath.Join(f.agents, entry.Name())
		pid, start, ok := agentFileName(entry.Name())
		if !ok {
			_ = os.Remove(path)
			continue
		}
		p, err := platform.ProcessInfo(pid)
		if err != nil && platform.Exited(err) || err == nil && (p.Start != start || p.Zombie) {
			_ = os.Remove(path)
		}
	}
	return nil
}

// agentFileName is the PID and start time an agent file's name holds, false
// when the name is not one folders.agent gives.
func agentFileName(name string) (int, uint64, bool) {
	base, ok := strings.CutSuffix(name, ".json")
	pidText, startText, cut := strings.Cut(base, "-")
	pid, pidErr := strconv.Atoi(pidText)
	start, startErr := strconv.ParseUint(startText, 10, 64)
	if !ok || !cut || pidErr != nil || startErr != nil ||
		name != fmt.Sprintf("%d-%d.json", pid, start) {
		return 0, 0, false
	}
	return pid, start, true
}

// appendStampLine appends stampLine to the env file at path, created private
// when missing, unless a line of the file already is stampLine: the file
// gains the hook's lines again each time the session resumes, is cleared or
// compacted, and the line is to run once. It starts a new line first when the
// file does not end in one, so it never runs on with another hook's line.
func appendStampLine(path string) error {
	data, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	for line := range strings.SplitSeq(string(data), "\n") {
		if line == stampLine {
			return nil
		}
	}
	text := stampLine + "\n"
	if len(data) > 0 && data[len(data)-1] != '\n' {
		text = "\n" + text
	}
	file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	_, err = file.WriteString(text)
	return errors.Join(err, file.Close())
}

// callFolders are the call's working folder and its command's leading cd
// target, a relative one taken against the working folder, each as a real
// path, or empty when unknown or unresolvable. They are read here, once, so
// the watcher's matching reads no files for them.
func callFolders(input hookInput) (cwd, folder string) {
	cwd = realPath(input.Cwd)
	if target, _, ok := commandFolder(input.Input.Command); ok {
		if !filepath.IsAbs(target) && cwd != "" {
			target = filepath.Join(cwd, target)
		}
		folder = realPath(target)
	}
	return cwd, folder
}

// startWatcher starts `workbench agent guard watch` detached when the lock in
// the guard folder f is free, from the command this process was run as, so
// the watcher knows the installed command it is to follow to a new release
// (see watcher.handOver). The watcher takes the lock itself; one that loses a
// race exits at once. It makes no folder, and gives the error trying the
// lock or starting the watcher.
func startWatcher(f folders) error {
	lock, held, err := operation.TryLockIn(f.lock())
	if err != nil || held {
		return err
	}
	_ = lock.Close()
	entry, err := operation.Invoked()
	if err == nil {
		_, err = operation.StartDetached(operation.Detached{
			Entry: entry, Args: []string{"agent", "guard", "watch"}, Output: f.output(),
		})
	}
	return err
}

// Post is Codex's PostToolUse hook on Bash, and Claude Code's PostToolUse and
// PostToolUseFailure hooks on Bash and Monitor. It first takes the call's kill
// record, when the watcher left one, reading and deleting it, and then marks
// the call's note ended, or, for a call whose command runs on
// after this hook, posted: one sent to the background (run_in_background),
// and a Monitor call, whose post hook runs when the monitor starts (the pre
// hook on Bash noted it as a Bash call). The record comes first because the
// watcher deletes a kill record once its call's note is no longer on disk
// (see watcher.expireKills), which it may be from the moment the note is
// marked. It deletes no note: the watcher deletes it once it has kept the
// call's record (see watcher.notes), so every call leaves one. It takes no
// lock to ask whether a watcher runs, only the one a move of the guard
// folder takes (see Move): an ended note waits for the next watcher, which
// the next pre hook starts. When the watcher stopped this call, it prints the
// reason as additionalContext, which the agent sees beside the tool result.
// Otherwise it prints nothing. It never fails.
func Post(stdin *os.File, stdout io.Writer) {
	var f folders
	defer func() {
		if p := recover(); p != nil && f.root != "" {
			f.event(slog.LevelError, EventError, "panic", "error", fmt.Sprint(p))
		}
	}()
	input, ok := readInput(stdin)
	if !ok {
		return
	}
	var err error
	if f, err = hookFolders("post"); err != nil {
		return
	}
	mark := "ended"
	if input.Input.Background || input.Tool == "Monitor" {
		mark = "posted"
	}
	var markErr error
	marked, told, removed := false, false, false
	var record kill
	// A record read at the folder's old path just before a move is deleted at
	// its new one: once read, the reason is told whatever the deletion does.
	work := func() error {
		var recordErr, removeErr, readErr error
		if !told {
			recordErr = readJSON(f.record(input.ToolUseID), &record)
			told = recordErr == nil
		}
		if told && !removed {
			removeErr = os.Remove(f.record(input.ToolUseID))
			removed = removeErr == nil || errors.Is(removeErr, fs.ErrNotExist)
		}
		if !marked {
			marked, markErr, readErr = markNote(f.note(input.ToolUseID), mark)
		}
		return errors.Join(recordErr, removeErr, readErr, markErr)
	}
	if work() != nil && f.again() {
		_ = work()
	}
	if markErr != nil {
		f.event(slog.LevelError, EventError, "cannot mark the note "+mark,
			"call", input.ToolUseID, "error", markErr)
	}
	if !told {
		return
	}
	_ = json.NewEncoder(stdout).Encode(map[string]any{
		"hookSpecificOutput": map[string]string{
			"hookEventName":     input.Event,
			"additionalContext": record.Reason,
		},
	})
}

// markNote marks the note at path ended, or posted with mark "posted". It
// reports whether it marked it, the error writing it and the error reading
// it, such as a call with no note.
func markNote(path, mark string) (marked bool, writeErr, readErr error) {
	var n note
	if readErr = readJSON(path, &n); readErr != nil {
		return false, nil, readErr
	}
	if mark == "posted" {
		n.Posted = true
	} else {
		n.Ended = true
	}
	if writeErr = writeJSON(path, n); writeErr != nil {
		return false, writeErr, nil
	}
	return true, nil, nil
}
