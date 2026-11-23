package guard

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/Sawmonabo/workbench/internal/operation"
	"github.com/Sawmonabo/workbench/internal/platform"
	"github.com/Sawmonabo/workbench/internal/sqlitefile"
)

const logLimit = 1 << 20

// idPattern is a tool_use_id that may name a file; any other call is not
// guarded.
var idPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)

// kill is the record the watcher leaves for the post hook, which reads only
// Reason; ToolUseID and At are for a person reading the record.
type kill struct {
	ToolUseID string    `json:"tool_use_id"`
	Reason    string    `json:"reason"`
	At        time.Time `json:"at"`
}

// agentFile is what the SessionStart hook leaves to say that its agent runs,
// named by its PID and start time so a later process reusing the PID is not
// taken for it. At is only for a person reading the file.
type agentFile struct {
	PID   int       `json:"pid"`
	Start uint64    `json:"start"`
	Kind  kind      `json:"kind"`
	At    time.Time `json:"at"`
}

// folders is the guard folder (see Root) and its notes, killed, agents and
// units folders, with the state folder that holds it, the part (pre, post,
// session or watch) that opened them and its logger. Only the watcher uses
// units (see unitRecord).
type folders struct {
	state, root, notes, killed, agents, units, part string
	logger                                          *slog.Logger
}

// stateFolder is Workbench's state folder. It reads nothing and creates
// nothing.
func stateFolder() (string, error) {
	c, err := operation.Resolve(operation.Options{ReadOnly: true})
	if err != nil {
		return "", err
	}
	return c.Paths.State, nil
}

// Root is where the guard folder is in the state folder state, for every
// guard entry: the folder at its old path, guard, while it is a folder and
// either agent/guard is not one or a watcher holds the old folder's lock;
// else agent/guard. It creates nothing, and tries the old folder's lock only
// when both are folders.
func Root(state string) string {
	old, root := oldRoot(state), newRoot(state)
	if operation.IsFolder(old) &&
		(!operation.IsFolder(root) || operation.IsLockHeld(folders{}.at(old).lock())) {
		return old
	}
	return root
}

// oldRoot is the guard folder's old path, guard in the state folder, for
// Root, the move and the entries that follow a move.
func oldRoot(state string) string { return filepath.Join(state, "guard") }

// newRoot is the guard folder's path, agent/guard in the state folder.
func newRoot(state string) string { return filepath.Join(state, "agent", "guard") }

// Folder is the guard folder for migration 0007: its old path in the state
// folder state, its path under agent/, and the watcher's lock in the old
// folder, which a watcher working there holds. It looks at nothing.
func Folder(state string) (old, root, lock string) {
	old = oldRoot(state)
	return old, newRoot(state), folders{}.at(old).lock()
}

// WatcherRuns reports whether a process holds the watcher's lock in the
// guard folder where Root says, asked as every entry asks (see
// [operation.LockHeld]): a watcher, from its start until it exits, or, for
// an instant, a hook or `workbench migrate` moving the folder (see Move). It
// creates nothing, and its error names the lock from the home folder (see
// [sqlitefile.FileError]).
func WatcherRuns() (bool, error) {
	state, err := stateFolder()
	if err != nil {
		return false, err
	}
	lock := locate(state, "").lock()
	held, err := operation.LockHeld(lock)
	if err != nil {
		return false, sqlitefile.FileError(lock, err)
	}
	return held, nil
}

// WatcherSilent is what is said, by doctor and the view, when a process holds
// the watcher's lock (see [WatcherRuns]) but nothing answers on its socket: a
// watcher that is starting, or one that cannot serve, whose log says why; a
// hook moving the guard folder holds the lock for an instant too.
const WatcherSilent = "the watcher's lock is held, but nothing answers on its socket: a " +
	"watcher is starting, or one runs that cannot serve; workbench agent guard logs says why"

// locate is the guard folder where Root says, for part, with nothing opened
// or made: the watcher takes its lock before it opens anything (see open).
func locate(state, part string) folders {
	return folders{state: state, part: part}.at(Root(state))
}

// at is f with the guard folder at root.
func (f folders) at(root string) folders {
	f.root = root
	f.notes = filepath.Join(root, "notes")
	f.killed = filepath.Join(root, "killed")
	f.agents = filepath.Join(root, "agents")
	f.units = filepath.Join(root, "units")
	return f
}

// openFolders opens the guard folder for part where Root says (see open).
func openFolders(state, part string) (folders, error) {
	f := locate(state, part)
	return f, f.open()
}

// open starts the logger, first, so a failure here can be logged where the
// folder holds its log, then makes the folder and its notes, killed and
// agents folders private (see private), so a folder at either path that is
// not private, or is a link, is refused in the same words. It deletes the
// file a move left at the old path once the system has restarted since (see
// [operation.RemoveClosedPath]).
func (f *folders) open() error {
	f.logger = slog.New(slog.NewJSONHandler(logWriter(f.log()), nil))
	for _, folder := range []string{f.notes, f.killed, f.agents} {
		if err := f.private(folder); err != nil {
			return err
		}
	}
	operation.RemoveClosedPath(oldRoot(f.state))
	return nil
}

// check applies to the guard folder where Root put f, at either path, the
// checks open applies there, making nothing (see
// [operation.CheckPrivateDirectory]), for an entry that only reads the
// folder: the state folder, the guard folder and its notes, killed and agents
// folders, each that exists, must be private, so a reader refuses the folder
// in the words a hook or the watcher does.
func (f folders) check() error {
	for _, folder := range []string{f.notes, f.killed, f.agents} {
		name, err := filepath.Rel(f.state, folder)
		if err != nil {
			return err
		}
		if err := operation.CheckPrivateDirectory(f.state, name); err != nil {
			return err
		}
	}
	return nil
}

// private makes the folder at path, the guard folder or one in it, private
// with the folders above it in the state folder (see
// [operation.PrivateDirectory]) when the guard folder is at its new path. At
// its old path, where nothing is ever made, it applies the same checks and
// makes nothing (see [operation.CheckPrivateDirectory]): a folder the
// release before made there passes them, and a missing one fails where it is
// used.
func (f folders) private(path string) error {
	name, err := filepath.Rel(f.state, path)
	if err != nil {
		return err
	}
	if f.root == oldRoot(f.state) {
		return operation.CheckPrivateDirectory(f.state, name)
	}
	_, err = operation.PrivateDirectory(f.state, name)
	return err
}

// makeUnits makes the units folder private, for the watcher alone, which
// holds the folder's lock, so the folder stays where it is meanwhile, at its
// old path too (see Move).
func (f folders) makeUnits() error {
	name, err := filepath.Rel(f.state, f.units)
	if err == nil {
		_, err = operation.PrivateDirectory(f.state, name)
	}
	return err
}

// makeRoot makes the guard folder private before its lock is taken, or at
// its old path checks it as private (see private).
func (f folders) makeRoot() error { return f.private(f.root) }

// again is for an entry whose read or write in the guard folder failed:
// when f is the folder at its old path and that path is no longer a folder,
// the folder has moved, so f becomes the folder where Root says now and
// again reports true, for the caller to do its work again, once. Otherwise,
// or when the folder cannot be opened, it reports false.
func (f *folders) again() bool {
	old := oldRoot(f.state)
	if f.root != old || operation.IsFolder(old) {
		return false
	}
	g, err := openFolders(f.state, f.part)
	if err != nil || g.root == old {
		return false
	}
	*f = g
	return true
}

// Move moves the guard folder from its old path to agent/guard for an entry
// that is not the watcher (the hooks, and workbench migrate), when no watcher
// holds it: it takes the watcher's lock in the old folder without waiting,
// and when it gets it swaps the folder (see [operation.MoveFolder]), lets
// the lock go, and then starts a watcher in the guard folder when that holds
// a note, since a pre hook that found the lock held in that instant started
// none. When the lock is held a watcher runs in the old folder, and Move
// moves nothing: the notes go where that watcher reads them (see Root). A
// file system that cannot swap moves nothing and is no error: the folder
// stays in use where it is. With no folder at the old path, a folder already
// at the new one, or a file system that [platform.CanSwap] says cannot swap,
// it moves nothing and takes no lock (see [operation.CanMoveFolder]), which
// an entry asking whether a watcher works in the old folder would take for
// one (see Root).
func Move(state string) (moved bool, err error) {
	moved, _, err = move(state, operation.TryLockIn)
	return moved, err
}

// MigrateMove is [Move] for migration 0007, which says why a folder stays:
// when the move's only failure is a file system that cannot swap two names,
// err matches platform.ErrSwapUnsupported and holds nothing else, and the
// folder stays in use where it is. A lock it finds held it tries again for
// about 100 ms before it gives up (see [operation.TakeLockIn]): an entry
// asking whether a watcher runs takes a shared lock on a free one for an
// instant, and a migration that met that instant would otherwise stop as if
// a watcher worked in the folder. The hooks never wait.
func MigrateMove(state string) (moved bool, err error) {
	moved, refused, err := move(state, operation.TakeLockIn)
	if err == nil {
		err = refused
	}
	return moved, err
}

// move is [Move], taking the watcher's lock with take, with refused, apart
// from err, the error of a swap the folder's file system refused (see
// platform.ErrSwapUnsupported). A lock that cannot be taken is named from the
// home folder in err (see [sqlitefile.FileError]).
func move(
	state string,
	take func(string) (*os.File, bool, error),
) (moved bool, refused, err error) {
	old := oldRoot(state)
	if can, err := operation.CanMoveFolder(old, newRoot(state)); !can {
		return false, err, nil
	}
	path := folders{}.at(old).lock()
	lock, held, err := take(path)
	if err != nil || held {
		switch {
		case err != nil && !operation.IsFolder(old):
			// Another entry moved it meanwhile.
			err = nil
		case err != nil:
			err = sqlitefile.FileError(path, err)
		}
		return false, nil, err
	}
	moved, err = operation.MoveFolder(old, newRoot(state))
	_ = lock.Close()
	if errors.Is(err, platform.ErrSwapUnsupported) {
		refused, err = err, nil
	}
	f := locate(state, "")
	if ids, _ := f.noteIDs(); len(ids) > 0 {
		err = errors.Join(err, startWatcher(f))
	}
	return moved, refused, err
}

func (f folders) lock() string            { return filepath.Join(f.root, "watch.lock") }
func (f folders) log() string             { return logFile(f.root) }
func (f folders) output() string          { return outputFile(f.log()) }
func (f folders) socket() string          { return socketFile(f.root) }
func (f folders) note(id string) string   { return filepath.Join(f.notes, id+".json") }
func (f folders) record(id string) string { return filepath.Join(f.killed, id+".json") }
func (f folders) unit(id string) string   { return filepath.Join(f.units, id+".json") }

func (f folders) agent(pid int, start uint64) string {
	return filepath.Join(f.agents, fmt.Sprintf("%d-%d.json", pid, start))
}

// noteIDs are the IDs of the notes on disk.
func (f folders) noteIDs() ([]string, error) {
	entries, err := listFolder(f.notes)
	if err != nil {
		return nil, err
	}
	var ids []string
	for _, entry := range entries {
		if id, ok := strings.CutSuffix(entry.Name(), ".json"); ok && idPattern.MatchString(id) {
			ids = append(ids, id)
		}
	}
	return ids, nil
}

// listFolder lists a guard folder without temporary files, which start
// ".write-" (see [operation.ReplacePrivateFile]), after
// [operation.RemoveStaleWrites].
func listFolder(folder string) ([]fs.DirEntry, error) {
	operation.RemoveStaleWrites(folder)
	all, err := os.ReadDir(folder)
	if err != nil {
		return nil, err
	}
	var entries []fs.DirEntry
	for _, entry := range all {
		if !strings.HasPrefix(entry.Name(), ".write-") {
			entries = append(entries, entry)
		}
	}
	return entries, nil
}

// writeJSON replaces the private file at path with value, so a reader never
// sees half a file.
func writeJSON(path string, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return operation.ReplacePrivateFile(path, data)
}

func readJSON(path string, value any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, value)
}

// logWriter appends what it is given to the guard log at its path as one
// line, cut once the log is over 1 MiB (see [operation.AppendLog]). A
// [slog.JSONHandler] makes one Write per record, so each record is one line.
type logWriter string

func (path logWriter) Write(p []byte) (int, error) {
	operation.AppendLog(string(path), string(bytes.TrimSuffix(p, []byte("\n"))), logLimit)
	return len(p), nil
}

// event appends one record to the guard log: event names it (see
// [EventStarted]), part and pid name the process writing it, and args are the
// record's other attributes, as for [slog.Logger.Log]. The message and the
// error attribute name the paths in the state folder from the home folder
// (see ShowPaths), as people read them; a path attribute stays in full.
func (f folders) event(level slog.Level, event, msg string, args ...any) {
	if f.logger == nil {
		return
	}
	args = slices.Clone(args)
	for i := 0; i+1 < len(args); i += 2 {
		if args[i] != "error" {
			continue
		}
		switch value := args[i+1].(type) {
		case error:
			args[i+1] = ShowPaths(value.Error(), f.state)
		case string:
			args[i+1] = ShowPaths(value, f.state)
		}
	}
	f.logger.Log(context.Background(), level, ShowPaths(msg, f.state),
		append([]any{"event", event, "part", f.part, "pid", os.Getpid()}, args...)...)
}
