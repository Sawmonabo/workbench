package cli

// This file is `agent guard history`: what it says of the history of
// finished commands, the --keep value it parses, and the trim it runs when
// it sets that time.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/Sawmonabo/workbench/internal/agent/guard"
	"github.com/Sawmonabo/workbench/internal/display"
	"github.com/Sawmonabo/workbench/internal/operation"
)

// agentGuardHistoryCommand is `agent guard history`: it says how long the
// guard keeps the commands that finished, how many it keeps and where, and
// --keep sets that time.
func agentGuardHistoryCommand(o *options) *cobra.Command {
	var keep string
	cmd := &cobra.Command{
		Use:   "history",
		Short: "Show or set how long the guard keeps the commands that finished",
		Long: "Show how long the guard keeps the commands that finished, how many it keeps, " +
			"the size of the history and its file. Finished commands are kept 24 hours " +
			"until --keep sets another time, in hours, days or weeks (12h, 3d, 1w) from 1h " +
			"to 30d. A shorter time deletes the older commands at once, without asking, " +
			"whether a watcher runs or not, and says how many, and the guard log's records " +
			"older than the time kept go with them (within a minute, by the watcher, while " +
			"one runs). A longer time " +
			"brings none back. At most 20,000 finished commands and 256 MB are kept, the " +
			"oldest leaving first. Sizes are in KB, MB and GB of 1,024, as the guard shows " +
			"memory.\n\n" +
			"A history the guard could not use is kept as history.damaged.db beside it, and " +
			"the one damaged before as history.damaged.2.db; at a third damage within the time " +
			"kept, history.damaged.2.db, the oldest, stays and the new copy takes the place of " +
			"history.damaged.db, so the history always starts again. Each copy is deleted once " +
			"it is older than the time kept; while one is kept, a last line names it.\n\n" +
			"The global --json prints the same facts as fields: path, keep_seconds, commands " +
			"and bytes, or with --keep path, keep_seconds, was_seconds and deleted.",
		Example: "  workbench agent guard history\n" +
			"  workbench agent guard history --keep 3d\n" +
			"  workbench agent guard history --json",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return o.runPrinted(cmd, func(result *operation.Result) error {
				if cmd.Flags().Changed("keep") {
					return setGuardHistoryKeep(cmd, o, result, keep)
				}
				return showGuardHistory(cmd, o, result)
			})
		},
	}
	cmd.Flags().StringVar(
		&keep,
		"keep",
		"",
		"Keep finished commands this long: hours, days or weeks from 1h to 30d (12h, 3d, 1w)",
	)
	return cmd
}

// guardHistoryFacts are what `agent guard history` says, as --json gives
// them: the history's file, how long finished commands are kept in seconds,
// how many it holds and its size in bytes.
type guardHistoryFacts struct {
	Path        string `json:"path"`
	KeepSeconds int64  `json:"keep_seconds"`
	Commands    int    `json:"commands"`
	Bytes       int64  `json:"bytes"`
}

// guardHistoryKept is what `agent guard history --keep` did, as --json gives
// it: the history's file, the time kept set and the one before, in seconds,
// and how many finished commands it deleted.
type guardHistoryKept struct {
	Path        string `json:"path"`
	KeepSeconds int64  `json:"keep_seconds"`
	WasSeconds  int64  `json:"was_seconds"`
	Deleted     int    `json:"deleted"`
}

// showGuardHistory prints the time kept, the count and size, and the file,
// and, while the guard folder keeps copies of damaged histories, the
// sentence the view says of them:
//
//	Finished commands are kept 24 hours · 2,832 kept · 3 MB
//	~/.local/state/workbench/agent/guard/history.db
//	Guard history was damaged and started again at 3:04 PM; the old file is ~/…/history.damaged.db
//
// A history not made yet is empty and kept for the default time. A history
// that cannot be opened because the guard folder moved meanwhile is opened
// where the folder is now (see guard.HistoryFolderMoved); once open, it is
// read wherever the folder goes.
func showGuardHistory(cmd *cobra.Command, o *options, result *operation.Result) error {
	root, err := guard.HistoryRoot()
	if err != nil {
		return err
	}
	var history *guard.History
	root, found, err := inHistoryFolder(root, guard.HistoryRoot, func(root string) (err error) {
		history, err = guard.OpenHistory(cmd.Context(), root, false)
		return err
	})
	if !found {
		return err
	}
	facts := guardHistoryFacts{
		Path:        guard.HistoryFile(root),
		KeepSeconds: int64(guard.HistoryKeepDefault / time.Second),
	}
	held := "none kept yet"
	switch {
	case errors.Is(err, fs.ErrNotExist):
	case err != nil:
		return guardHistoryUnreadable(err, root)
	default:
		defer func() { _ = history.Close() }()
		held = ""
		keep, err := history.Keep(cmd.Context())
		if err != nil {
			return guardHistoryUnreadable(err, root)
		}
		facts.KeepSeconds = int64(keep / time.Second)
		if facts.Commands, facts.Bytes, err = history.Count(cmd.Context()); err != nil {
			return guardHistoryUnreadable(err, root)
		}
	}
	if o.json {
		result.Results = append(result.Results, operation.Component{
			Name: "guard-history", Status: operation.StatusComplete, Details: facts,
		})
		return nil
	}
	if held == "" {
		held = display.Number(int64(facts.Commands)) + " kept · " + display.DataSize(facts.Bytes)
	}
	_, err = fmt.Fprintf(cmd.OutOrStdout(), "%s\n%s\n",
		forTerm("Finished commands are kept "+guard.KeepText(time.Duration(facts.KeepSeconds)*
			time.Second)+" · "+held),
		display.Path(facts.Path))
	if text := guard.HistoryDamaged(guard.HistoryDamagedCopies(root), time.Now()); err == nil &&
		text != "" {
		_, err = fmt.Fprintln(cmd.OutOrStdout(), forTerm(text))
	}
	return err
}

// setGuardHistoryKeep sets the time kept to text, making the guard folder and
// the history as the watcher would when they are missing, trims the history
// itself, as the watcher's writer does, whether a watcher runs or not, and
// prints the one line that says how many it deleted:
//
//	Finished commands are kept 12 hours (was 24 hours); 1,204 older ones were deleted.
//
// The history is in WAL mode and each trim step is short, so a watcher's
// writer and this command both write meanwhile, each waiting for the other's
// step within the busy timeout (see guard.History.Trim). A history it cannot
// use fails as showGuardHistory does, and sets nothing. Opening the history
// and cutting the log, which find their files by path, are done again where
// the guard folder is now when they fail because it moved meanwhile (see
// guard.HistoryFolderMoved); the history once open is written wherever the
// folder goes.
func setGuardHistoryKeep(
	cmd *cobra.Command,
	o *options,
	result *operation.Result,
	text string,
) error {
	keep, err := parseKeep(text)
	if err != nil {
		return err
	}
	root, err := guard.MakeHistoryRoot()
	if err != nil {
		return err
	}
	var history *guard.History
	root, found, err := inHistoryFolder(root, guard.MakeHistoryRoot, func(root string) (err error) {
		history, err = guard.OpenHistory(cmd.Context(), root, true)
		return err
	})
	if !found {
		return err
	}
	if err != nil {
		return guardHistoryUnreadable(err, root)
	}
	defer func() { _ = history.Close() }()
	was, err := history.Keep(cmd.Context())
	if err != nil {
		return guardHistoryUnreadable(err, root)
	}
	if err := history.SetKeep(cmd.Context(), keep); err != nil {
		return operation.Fail(operation.ExitFailed, "guard-history",
			"Cannot set how long finished commands are kept",
			operation.Cause(guard.ShowPaths(err.Error(), root)))
	}
	deleted, err := trimGuardHistory(cmd.Context(), history)
	if err != nil {
		return operation.Fail(operation.ExitFailed, "guard-history",
			"Finished commands older than "+guard.KeepText(keep)+" cannot be deleted",
			operation.Cause(guard.ShowPaths(err.Error(), root)),
			operation.Changed("finished commands are kept "+guard.KeepText(keep)))
	}
	line := "Finished commands are kept " + guard.KeepText(keep)
	var said operation.Body
	if keep == was {
		line += ", as before"
	} else {
		said.Lines = append(said.Lines, operation.Line{Label: "was", Text: guard.KeepText(was)})
	}
	if keep > was {
		said.Lines = append(
			said.Lines,
			operation.Line{Label: "note", Text: "those already deleted do not come back"},
		)
	}
	if keep < was || deleted > 0 {
		said.Lines = append(
			said.Lines,
			operation.Line{Label: "history", Text: deletedText(deleted)},
		)
	}
	since := time.Now().Add(-keep)
	root, _, err = inHistoryFolder(root, guard.HistoryRoot, func(root string) error {
		_, err := guard.CutLogUnwatched(root, since)
		return err
	})
	if err != nil {
		return operation.Fail(operation.ExitFailed, "guard-history",
			"The guard log's older records cannot be deleted",
			operation.Cause(guard.ShowPaths(err.Error(), root)),
			operation.Changed(strings.ToLower(line[:1])+line[1:]))
	}
	if o.json {
		result.Results = append(result.Results, operation.Component{
			Name: "guard-history", Status: operation.StatusComplete,
			Details: guardHistoryKept{
				Path:        guard.HistoryFile(root),
				KeepSeconds: int64(keep / time.Second),
				WasSeconds:  int64(was / time.Second),
				Deleted:     deleted,
			},
		})
		return nil
	}
	var b strings.Builder
	out := cmd.OutOrStdout()
	writeHeadline(&b, lineWidth(out), newPainter(out), "", line, said)
	_, err = io.WriteString(out, b.String())
	return err
}

// trimGuardHistory trims history as the watcher's writer does, a step at a
// time until none is due, each after leaving the history to the watcher's
// writer as long as the step before held it (see guard.History.Trim and
// guard.TrimPause), and reports how many finished commands its steps deleted,
// which leaves out those a watcher's own trim deleted meanwhile.
func trimGuardHistory(ctx context.Context, history *guard.History) (deleted int, err error) {
	for again := true; again; {
		start := time.Now()
		var n int
		if n, again, err = history.Trim(ctx, start); err != nil {
			return deleted, err
		}
		deleted += n
		if again {
			time.Sleep(guard.TrimPause(time.Since(start)))
		}
	}
	return deleted, nil
}

// deletedText says how many finished commands were deleted for being older
// than the time kept: "1,204 older ones were deleted", "1 older one was
// deleted", or "none was older".
func deletedText(n int) string {
	switch n {
	case 0:
		return "none was older"
	case 1:
		return "1 older one was deleted"
	}
	return display.Number(int64(n)) + " older ones were deleted"
}

// keepPattern is a --keep value: a whole number of hours, days or weeks.
var keepPattern = regexp.MustCompile(`^([1-9][0-9]{0,3})([hdw])$`)

// The shortest and the longest time --keep takes.
const (
	keepShortest = time.Hour
	keepLongest  = 30 * 24 * time.Hour
)

// parseKeep is a --keep value as a time: 12h, 3d or 1w, from an hour to 30
// days.
func parseKeep(text string) (time.Duration, error) {
	invalid := operation.Fail(operation.ExitInvalid, "invocation",
		fmt.Sprintf("--keep takes hours, days or weeks from 1h to 30d, such as 12h, 3d or 1w, "+
			"not %q", text))
	match := keepPattern.FindStringSubmatch(text)
	if match == nil {
		return 0, invalid
	}
	n, err := strconv.Atoi(match[1])
	if err != nil {
		return 0, invalid
	}
	unit := map[string]time.Duration{"h": time.Hour, "d": 24 * time.Hour, "w": 7 * 24 * time.Hour}
	keep := time.Duration(n) * unit[match[2]]
	if keep < keepShortest || keep > keepLongest {
		return 0, invalid
	}
	return keep, nil
}

// inHistoryFolder runs work in the guard folder root and, when it fails
// because the folder moved meanwhile (see guard.HistoryFolderMoved), once
// more where find (guard.HistoryRoot or guard.MakeHistoryRoot) says the
// folder is now. It gives the folder work last ran in, or, with found false,
// the folder find gave and its error when find failed.
func inHistoryFolder(
	root string,
	find func() (string, error),
	work func(root string) error,
) (_ string, found bool, err error) {
	if err = work(root); err == nil || !guard.HistoryFolderMoved(root) {
		return root, true, err
	}
	if root, err = find(); err != nil {
		return root, false, err
	}
	return root, true, work(root)
}

// guardHistoryUnreadable is the error of a history in the guard folder root
// that cannot be read, as the view says it (see guard.HistoryUnreadable). A
// file that is not a guard history is invalid state; any other reason a
// failure.
func guardHistoryUnreadable(err error, root string) error {
	code := operation.ExitFailed
	if errors.Is(err, guard.ErrHistoryUnusable) {
		code = operation.ExitInvalid
	}
	return operation.Fail(code, "guard-history", guard.HistoryUnreadable(err, root))
}
