package costs

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"

	"github.com/Sawmonabo/workbench/internal/agent/toolconfig"
	"github.com/Sawmonabo/workbench/internal/operation"
)

// ingestCommand matches the ingest hook's command as Workbench installs it:
// the workbench executable, by name, by path or as a single-quoted path, then
// "agent costs ingest", then "|| true", so a workbench that is missing or does
// not know the command exits 0, which Claude Code and Codex report as no
// failure (see [Hook]).
var ingestCommand = regexp.MustCompile(`(^|/)workbench'? agent costs ingest \|\| true$`)

// IngestHooks are the ingest hook on each session event, the hooks that keep
// the ledger.
var IngestHooks = []toolconfig.HookCommand{
	{Event: "SessionStart", Command: ingestCommand},
	{Event: "SessionEnd", Command: ingestCommand},
}

// Hook is the hook entry. Claude Code and Codex pipe a JSON object
// (hook_event_name, transcript_path, session_id) on stdin; Hook starts
// `workbench agent costs ingest --worker` detached with those three values and
// returns. The hooks run synchronously (Claude Code stops a background hook
// that is still running when a headless session ends), which costs a session
// the few milliseconds of one process spawn. Hook moves the costs folders under
// agent/ when it may (see [MoveAgentFolders]) and writes only the import log,
// which the costs move never compares, so it needs no gate: the worker runs it.
// Hook prints nothing on stdout and never fails: a terminal, a closed
// or empty stdin, or anything unreadable just means no event, and the worker
// then sweeps everything. Reading stdin gives up after two seconds. When
// it can't start the worker, for example because a runtime folder is not
// private, it says why in one line on stderr and records nothing. Claude Code
// and Codex report a hook that exits 0 as succeeded whatever its stderr, and
// report any other exit as a failed hook, so the hook's command ends in
// `|| true` ([IngestHooks]): a workbench that is missing, or an older one that
// does not know the command, fails no session either.
func Hook(stdin *os.File) {
	defer func() { _ = recover() }()
	event, transcript, session := readHookInput(stdin)
	if err := startWorker(event, transcript, session); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "workbench agent costs: nothing recorded: "+err.Error())
	}
}

// startWorker starts the detached worker with the hook's values, its output
// appended to the import log in the private costs state folder. It first
// moves the costs folders under agent/ when it may (see [MoveAgentFolders]),
// and starts the worker where they are then; a move that failed leaves them
// where they are, and the worker logs why. When the start fails because
// another process moved them in between (see [Paths.moved]), it starts the
// worker where they are now.
func startWorker(event, transcript, session string) error {
	paths, err := Locations()
	if err != nil {
		return err
	}
	// Claude Code with CLAUDE_CODE_SUBPROCESS_ENV_SCRUB set runs its hooks
	// without CLAUDE_CONFIG_DIR (https://code.claude.com/docs/en/env-vars),
	// and the worker would read ~/.claude instead of the folder the session
	// writes in. Its transcript, <config folder>/projects/<project>/<id>.jsonl,
	// names that folder, which the worker is given back.
	projects := filepath.Dir(filepath.Dir(transcript))
	if os.Getenv("CLAUDE_CONFIG_DIR") == "" && filepath.IsAbs(transcript) &&
		filepath.Base(projects) == "projects" && filepath.Ext(transcript) == ".jsonl" {
		if _, owned := toolOfTranscript(paths.Home, transcript); !owned {
			_ = os.Setenv("CLAUDE_CONFIG_DIR", filepath.Dir(projects))
		}
	}
	self, err := os.Executable()
	if err != nil {
		return err
	}
	// "--" keeps a value that starts with a dash from being read as a flag.
	worker := operation.Detached{
		Entry: self,
		Args: []string{
			"agent",
			"costs",
			"ingest",
			"--worker",
			"--quiet",
			"--",
			event,
			transcript,
			session,
		},
	}
	used, err := spawn(paths, worker)
	if err != nil && used.moved() {
		_, err = spawn(paths, worker)
	}
	return err
}

// spawn is one try of [startWorker], which returns the paths it used.
func spawn(paths Paths, worker operation.Detached) (Paths, error) {
	paths, _ = locate(paths.Home, paths.Runtime)
	if _, err := paths.stateFolder(); err != nil {
		return paths, err
	}
	worker.Output = paths.output()
	_, err := operation.StartDetached(worker)
	return paths, err
}

func readHookInput(stdin *os.File) (event, transcript, session string) {
	var input struct {
		Event      string `json:"hook_event_name"`
		Transcript string `json:"transcript_path"`
		Session    string `json:"session_id"`
	}
	data := operation.ReadHookInput(stdin)
	if json.Unmarshal(data, &input) != nil {
		return "", "", ""
	}
	return input.Event, input.Transcript, input.Session
}
