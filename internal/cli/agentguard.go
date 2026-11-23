package cli

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"

	"charm.land/lipgloss/v2"
	"github.com/spf13/cobra"

	"github.com/Sawmonabo/workbench/internal/agent/guard"
	"github.com/Sawmonabo/workbench/internal/operation"
)

// agentGuardCommand is `agent guard`: run bare, it opens the live view of what the
// guard tracks (agentguardview.go), and logs prints what the guard did. Claude Code
// and Codex run pre, post and session as hooks, and pre starts watch with
// fixed arguments; those four are hidden. Like askpass they are plain
// commands, not actions, and print no result envelope. pre, post and session
// take any arguments, flags included (flag parsing is off, so even the root's
// --json is just an argument), so nothing an agent passes makes them exit
// non-zero, and print only what the hook protocol reads. The view and logs are
// for people.
func agentGuardCommand(o *options) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "guard",
		Short: "Live view of the agent commands the memory usage guard tracks, where you can stop one",
		Long: "The guard stops the largest shell command Claude Code or Codex started, or the " +
			"largest program such a command left running detached, when the machine is about " +
			"to run out of memory, and tells the agent why. Its hooks and watcher run by " +
			"themselves.\n\n" +
			"Run bare, it opens a live, full-screen view of the commands and detached programs " +
			"the watcher tracks, with their memory, project and agent, and below them the " +
			"commands that finished, for as long as workbench agent guard history keeps them. " +
			"↑ and ↓ move, pgup, pgdn, home and end move by a page or to an end, space tags a " +
			"row, enter opens its details, / finds the rows that hold a text, s sorts the " +
			"finished rows by their peak, k stops the tagged or selected rows, and q quits. " +
			"The view needs a terminal and refuses --json, --non-interactive and coding " +
			"agents; workbench agent guard logs --follow prints the same events. docs/usage.md " +
			"describes every row, key and kind of stop.",
		Example: "  workbench agent guard\n" +
			"  workbench agent guard logs --since 2h\n" +
			"  workbench agent guard history --keep 3d",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return o.runPrinted(cmd, func(*operation.Result) error { return runGuardView(cmd, o) })
		},
	}
	cmd.AddCommand(
		agentGuardLogsCommand(o),
		agentGuardHistoryCommand(o),
		&cobra.Command{
			Use:                "pre",
			Short:              "Hook: Claude Code and Codex run this before each Bash call",
			Hidden:             true,
			DisableFlagParsing: true,
			Args:               cobra.ArbitraryArgs,
			RunE: func(cmd *cobra.Command, _ []string) error {
				guard.Pre(stdinFile(cmd))
				return nil
			},
		},
		&cobra.Command{
			Use:                "post",
			Short:              "Hook: Claude Code and Codex run this after each Bash call",
			Hidden:             true,
			DisableFlagParsing: true,
			Args:               cobra.ArbitraryArgs,
			RunE: func(cmd *cobra.Command, _ []string) error {
				guard.Post(stdinFile(cmd), cmd.OutOrStdout())
				return nil
			},
		},
		&cobra.Command{
			Use:                "session AGENT",
			Short:              "Hook: Claude Code and Codex run this when a session starts",
			Hidden:             true,
			DisableFlagParsing: true,
			Args:               cobra.ArbitraryArgs,
			RunE: func(cmd *cobra.Command, args []string) error {
				agent := ""
				if len(args) > 0 {
					agent = args[0]
				}
				guard.Session(stdinFile(cmd), agent)
				return nil
			},
		},
		&cobra.Command{
			Use:    "watch",
			Short:  "The guard's watcher, which the pre hook starts detached",
			Hidden: true,
			Args:   cobra.NoArgs,
			RunE: func(cmd *cobra.Command, _ []string) error {
				guard.Watch(cmd.Context(), version())
				return nil
			},
		},
	)
	return cmd
}

// agentGuardLogsCommand prints the guard log. The global --json passes its records
// through as they are, one per line, so only an error goes through the result
// envelope.
func agentGuardLogsCommand(o *options) *cobra.Command {
	var follow bool
	var since string
	cmd := &cobra.Command{
		Use:   "logs",
		Short: "Show the commands the guard stopped, memory warnings and watcher errors",
		Long: "Show the guard log, oldest first, one line per event: the time in this machine's " +
			"time zone, the level and the event, then key=value fields. With --follow it keeps " +
			"printing new events, and a command still running 5 seconds after the watcher " +
			"found it gets one running line.\n\n" +
			"The global --json prints the log's own records unchanged, one JSON object per line " +
			"(JSON Lines) rather than one result object, with the full call IDs, stop reasons " +
			"and each tracked command, and any other line as a raw record. The log is watch.log " +
			"in the guard folder of Workbench's state folder, and the watcher's own output " +
			"watch.err beside it; docs/usage.md lists every event and field.",
		Example: "  workbench agent guard logs\n" +
			"  workbench agent guard logs --since 2h\n" +
			"  workbench agent guard logs --since \"2026-10-16 21:00\"\n" +
			"  workbench agent guard logs --follow\n" +
			"  workbench agent guard logs --json --since today",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			err := showGuardLogs(cmd, o, follow, since)
			if err == nil {
				return nil
			}
			return o.runPrinted(cmd, func(*operation.Result) error { return err })
		},
	}
	flags := cmd.Flags()
	flags.BoolVarP(&follow, "follow", "f", false, "Keep printing new events until interrupted")
	flags.StringVar(
		&since,
		"since",
		"",
		"Show events from then on: "+timeFlagForms,
	)
	return cmd
}

func showGuardLogs(cmd *cobra.Command, o *options, follow bool, sinceText string) error {
	since, _, err := parseTimeFlag("--since", sinceText, time.Now())
	if err != nil {
		return err
	}
	path, err := guard.LogPath()
	if err != nil {
		return err
	}
	out := cmd.OutOrStdout()
	paint := newPainter(out)
	shown := 0
	calls := runningCalls{tracked: map[string]guard.Event{}, told: map[string]bool{}}
	show := func(raw []byte, e guard.Event) error {
		if o.json {
			shown++
			_, err := fmt.Fprintf(out, "%s\n", raw)
			return err
		}
		calls.see(e)
		if e.Event == guard.EventTracking {
			return nil
		}
		shown++
		_, err := io.WriteString(out, guardLine(paint, e, false)+"\n")
		return err
	}
	var idle func() error
	if follow && !o.json {
		idle = func() error {
			for _, e := range calls.due(time.Now()) {
				if _, err := io.WriteString(out, guardLine(paint, e, true)+"\n"); err != nil {
					return err
				}
			}
			return nil
		}
	}
	// A raw line has no time. One of the watcher's output file was written
	// while its watcher ran, so --since keeps it unless the next watcher
	// started before then. One of the log was written after the record
	// before it, so --since keeps it when it keeps that record; lines before
	// the first record wait for it and are kept when it is.
	type line struct {
		raw []byte
		e   guard.Event
	}
	var waiting []line
	keep, seenRecord := since.IsZero(), false
	err = guard.ReadLog(cmd.Context(), path, follow, func(raw []byte, e guard.Event) error {
		switch {
		case e.Output && (e.Until.IsZero() || !e.Until.Before(since)):
			return show(raw, e)
		case e.Output:
			return nil
		case e.Event == guard.EventRaw && keep:
			return show(raw, e)
		case e.Event == guard.EventRaw && !seenRecord:
			waiting = append(waiting, line{raw, e})
			return nil
		case e.Event == guard.EventRaw:
			return nil
		}
		seenRecord, keep = true, !e.Time.Before(since)
		held := waiting
		waiting = nil
		if !keep {
			return nil
		}
		for _, l := range held {
			if err := show(l.raw, l.e); err != nil {
				return err
			}
		}
		return show(raw, e)
	}, idle)
	// The empty-state line is a message, not output, so it goes to stderr
	// (clig.dev) and leaves --json's stdout empty.
	if err != nil || shown > 0 || follow {
		return err
	}
	text := "No guard events yet"
	if !since.IsZero() {
		text = "No guard events since " + since.Local().Format("Jan 2 3:04 PM")
	}
	_, err = fmt.Fprintln(cmd.ErrOrStderr(), text+".")
	return err
}

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
			"to 30d. A shorter time deletes the older commands without asking: the view " +
			"hides them at once and the watcher deletes them within a minute. A longer time " +
			"brings none back. At most 20,000 finished commands and 256 MiB are kept, the " +
			"oldest leaving first.\n\n" +
			"The global --json prints the same facts as fields: path, keep_seconds, commands " +
			"and bytes, or with --keep path, keep_seconds and was_seconds.",
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
// it: the history's file and the time kept now and before, in seconds.
type guardHistoryKept struct {
	Path        string `json:"path"`
	KeepSeconds int64  `json:"keep_seconds"`
	WasSeconds  int64  `json:"was_seconds"`
}

// showGuardHistory prints the time kept, the count and size, and the file:
//
//	Finished commands are kept 24 hours · 2,832 kept · 3.2 MB
//	~/.local/state/workbench/agent/guard/history.db
//
// A history not made yet is empty and kept for the default time.
func showGuardHistory(cmd *cobra.Command, o *options, result *operation.Result) error {
	root, err := guard.HistoryRoot()
	if err != nil {
		return err
	}
	facts := guardHistoryFacts{
		Path:        guard.HistoryFile(root),
		KeepSeconds: int64(guard.HistoryKeepDefault / time.Second),
	}
	held := "none kept yet"
	history, err := guard.OpenHistory(root, false)
	switch {
	case errors.Is(err, fs.ErrNotExist):
	case err != nil:
		return guardHistoryUnreadable(err, facts.Path)
	default:
		defer func() { _ = history.Close() }()
		held = ""
		keep, err := history.Keep(cmd.Context())
		if err != nil {
			return guardHistoryUnreadable(err, facts.Path)
		}
		facts.KeepSeconds = int64(keep / time.Second)
		if facts.Commands, facts.Bytes, err = history.Count(cmd.Context()); err != nil {
			return guardHistoryUnreadable(err, facts.Path)
		}
	}
	if o.json {
		result.Results = append(result.Results, operation.Component{
			Name: "guard-history", Status: operation.StatusComplete, Details: facts,
		})
		return nil
	}
	if held == "" {
		held = commas(int64(facts.Commands)) + " kept · " + transferSize(facts.Bytes, facts.Bytes)
	}
	_, err = fmt.Fprintf(cmd.OutOrStdout(), "%s\n%s\n",
		forTerm("Finished commands are kept "+keptText(time.Duration(facts.KeepSeconds)*
			time.Second)+" · "+held),
		showPath(facts.Path))
	return err
}

// setGuardHistoryKeep sets the time kept to text, making the guard folder and
// the history as the watcher would when they are missing, and prints the one
// line that says so:
//
//	Finished commands are kept 12 hours (was 24 hours); older ones are deleted within a minute.
//
// A history it cannot use fails as showGuardHistory does, and sets nothing.
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
	path := guard.HistoryFile(root)
	history, err := guard.OpenHistory(root, true)
	if err != nil {
		return guardHistoryUnreadable(err, path)
	}
	defer func() { _ = history.Close() }()
	was, err := history.Keep(cmd.Context())
	if err != nil {
		return guardHistoryUnreadable(err, path)
	}
	if err := history.SetKeep(cmd.Context(), keep); err != nil {
		return operation.Fail(operation.ExitFailed, "guard-history",
			"Cannot set how long finished commands are kept: "+shownError(err, path))
	}
	if o.json {
		result.Results = append(result.Results, operation.Component{
			Name: "guard-history", Status: operation.StatusComplete,
			Details: guardHistoryKept{
				Path:        path,
				KeepSeconds: int64(keep / time.Second),
				WasSeconds:  int64(was / time.Second),
			},
		})
		return nil
	}
	line := "Finished commands are kept " + keptText(keep)
	switch {
	case keep < was:
		line += " (was " + keptText(was) + "); older ones are deleted within a minute."
	case keep > was:
		line += " (was " + keptText(was) + "); those already deleted do not come back."
	default:
		line += ", as before."
	}
	_, err = fmt.Fprintln(cmd.OutOrStdout(), line)
	return err
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

// keptText is a time kept in words: whole weeks, else whole days from two
// days, else whole hours ("1 week", "3 days", "24 hours"); any other time,
// which only a settings row written by something else can hold, in Go's form.
func keptText(keep time.Duration) string {
	const day = 24 * time.Hour
	unit := func(n time.Duration, word string) string {
		if n != 1 {
			word += "s"
		}
		return strconv.FormatInt(int64(n), 10) + " " + word
	}
	switch {
	case keep%(7*day) == 0:
		return unit(keep/(7*day), "week")
	case keep%day == 0 && keep >= 2*day:
		return unit(keep/day, "day")
	case keep%time.Hour == 0:
		return unit(keep/time.Hour, "hour")
	}
	return keep.String()
}

// guardHistoryUnreadable is the error of a history at path that cannot be
// read, as the view says it: "Guard history unreadable: REASON". A file that
// is not a guard history is invalid state; any other reason a failure.
func guardHistoryUnreadable(err error, path string) error {
	code := operation.ExitFailed
	if errors.Is(err, guard.ErrHistoryUnusable) {
		code = operation.ExitInvalid
	}
	return operation.Fail(code, "guard-history", "Guard history unreadable: "+shownError(err, path))
}

// shownError is err's text with the history's path as output shows paths.
func shownError(err error, path string) string {
	return strings.ReplaceAll(err.Error(), path, showPath(path))
}

// runningAfter is how long a tracked command runs before a followed log
// prints that it is still running.
const runningAfter = 5 * time.Second

// runningCalls are the calls a followed log saw tracked and not yet end, so
// each still running runningAfter later gets one running line: tracked holds
// their tracking records by call ID, and told the calls that got their line.
// A watcher that starts, goes idle or exits leaves none of the earlier
// watcher's calls tracked; one it adopts in a handover is tracked again.
type runningCalls struct {
	tracked map[string]guard.Event
	told    map[string]bool
}

// see notes a record the log gave.
func (r runningCalls) see(e guard.Event) {
	switch e.Event {
	case guard.EventTracking:
		if !r.told[e.Call] {
			r.tracked[e.Call] = e
		}
	case guard.EventEnded, guard.EventStopping, guard.EventAbandoned:
		delete(r.tracked, e.Call)
	case guard.EventStarted, guard.EventIdle, guard.EventExited:
		clear(r.tracked)
	}
}

// due are the tracking records of the calls tracked at least runningAfter
// before now, oldest first, each timed now and given once.
func (r runningCalls) due(now time.Time) []guard.Event {
	var due []guard.Event
	for call, e := range r.tracked {
		if now.Sub(e.Time) >= runningAfter {
			due = append(due, e)
			delete(r.tracked, call)
			r.told[call] = true
		}
	}
	slices.SortFunc(due, func(a, b guard.Event) int { return a.Time.Compare(b.Time) })
	for i := range due {
		due[i].Time = now
	}
	return due
}

// guardLevelStyles color a line's level label, and the label alone: routine
// green, warnings gold and errors red, both bold. A level not named here is
// faint.
var guardLevelStyles = map[string]lipgloss.Style{
	slog.LevelInfo.String(): guardGreen, slog.LevelWarn.String(): guardGold.Bold(true),
	slog.LevelError.String(): guardRed.Bold(true),
}

// guardMessageEvents are the events whose name alone does not say what
// happened, so their line keeps the record's msg.
var guardMessageEvents = map[string]bool{
	guard.EventMemory: true, guard.EventRefused: true, guard.EventGaveUp: true,
	guard.EventError: true, guard.EventRaw: true, guard.EventMoved: true,
}

// guardLine is one event on one line, as logfmt readers parse it after its
// first three fields: `Oct 17 14:41:05 INFO  ended     id=4fQ8nX2a
// project=api took=53s peak=2.41GB cmd="cargo test"`. The time is local, the
// level is padded to 5 and the event to 9, and a raw line, which has no time,
// leaves the time blank. The time and the keys are faint, the level label
// alone is colored and the event is plain. Fields follow in a fixed order, cmd
// last as the longest, each left out when empty; a field the order does not
// name stays in --json only. running prints a tracking record as a running
// line, without took and peak.
func guardLine(paint painter, e guard.Event, running bool) string {
	name := e.Event
	if running {
		name = "running"
	}
	when := strings.Repeat(" ", len(time.Stamp))
	if !e.Time.IsZero() {
		when = e.Time.Local().Format(time.Stamp)
	}
	level, ok := guardLevelStyles[e.Level]
	if !ok {
		level = faint
	}
	fields := guardFields(e, running)
	// The level is padded outside its color, so only the label is painted.
	tail := fmt.Sprintf("%s %-9s", strings.Repeat(" ", max(5-len(e.Level), 0)), name)
	if len(fields) == 0 {
		tail = strings.TrimRight(tail, " ")
	}
	var b strings.Builder
	b.WriteString(paint.style(faint, when) + " " + paint.style(level, e.Level) + tail)
	for _, f := range fields {
		value := logfmtValue(f.value)
		if f.style != nil {
			value = paint.style(*f.style, value)
		}
		b.WriteString(" " + paint.style(faint, f.key+"=") + value)
	}
	return b.String()
}

// guardField is one key=value of a guard line, its value styled when style
// is set.
type guardField struct {
	key, value string
	style      *lipgloss.Style
}

// guardFields are the fields of e in a guard line's order, each without a
// value left out: a call's short id, where, for a program that detached from
// a command its kind and the short id of the call that started it, by whom,
// how long it took and how large it got, the memory reading, the watcher's
// releases and processes, then msg, an ended unit's reason, error, process
// and cmd. A stop's reason, the text the agent is told, stays in --json
// only. Values are plain except the agent in its hue, a stop by you and a
// took of 10 s or more bold, and a size or peak bold in gold from 1 GB and in
// red from 4 GB.
func guardFields(e guard.Event, running bool) []guardField {
	took, peak := e.Took, e.Peak
	if running {
		took, peak = 0, 0
	}
	stillRunning := make([]string, len(e.StillRunning))
	for i, pid := range e.StillRunning {
		stillRunning[i] = strconv.Itoa(pid)
	}
	msg, reason := "", ""
	if guardMessageEvents[e.Event] {
		msg = e.Msg
	}
	if e.Event == guard.EventEnded {
		reason = e.Reason
	}
	fields := []guardField{
		{"id", shortCallID(e.Call), nil},
		{"agent", e.Agent, agentStyle(e.Agent)},
		{"subagent", e.Subagent, nil},
		{"project", e.Project, nil},
		{"kind", e.Kind, nil},
		{"launched_by", shortCallID(e.LaunchedBy), nil},
		{"by", e.By, styleIf(e.By == "you", bold)},
		{"dry_run", whenTrue(e.DryRun), nil},
		{"took", tookValue(took), styleIf(took >= 10, bold)},
		{"size", sizeValue(sizeText(e.Size)), sizeStyle(e.Size)},
		{"peak", sizeValue(sizeText(peak)), sizeStyle(peak)},
		{"floor", sizeValue(capacityText(e.Floor)), nil},
		{"level", nonZero(e.Memory.Level), nil},
		{"available", sizeValue(sizeText(e.Memory.Available)), nil},
		{"total", sizeValue(capacityText(e.Memory.Total)), nil},
		{"still_running", strings.Join(stillRunning, ","), nil},
		{"release", e.Release, nil},
		{"old_release", e.OldRelease, nil},
		{"new_release", e.NewRelease, nil},
		{"new_pid", nonZero(e.NewPID), nil},
		{"msg", msg, nil},
		{"reason", reason, nil},
		{"error", e.Error, nil},
		{"process", nonZero(e.Process), nil},
		{"cmd", guard.CommandRest(e.Command), nil},
	}
	return slices.DeleteFunc(fields, func(f guardField) bool { return f.value == "" })
}

// styleIf is style when on, else nil.
func styleIf(on bool, style lipgloss.Style) *lipgloss.Style {
	if !on {
		return nil
	}
	return &style
}

// whenTrue is "true" when set, else empty, so a false flag is left out.
func whenTrue(set bool) string {
	if !set {
		return ""
	}
	return "true"
}

// nonZero is n in decimal, or empty for 0.
func nonZero(n int) string {
	if n == 0 {
		return ""
	}
	return strconv.Itoa(n)
}

// shortCallID is the first 8 characters of a tool call ID after its toolu_
// (Claude Code) or call_ (Codex) prefix and any provider segment after it,
// enough to tell calls apart on screen. Claude on Amazon Bedrock and Google
// Vertex AI adds a short lowercase segment ("toolu_bdrk_01…",
// "toolu_vrtx_01…"), the same in every ID, so it is left out too. A detached
// program's ID, made by the watcher for a process, not a call (detached-,
// then the process ID and its start), is p and that process ID ("p87305").
func shortCallID(call string) string {
	if rest, ok := strings.CutPrefix(call, "detached-"); ok {
		pid, _, _ := strings.Cut(rest, "-")
		return "p" + pid
	}
	call = strings.TrimPrefix(strings.TrimPrefix(call, "toolu_"), "call_")
	if segment, rest, ok := strings.Cut(call, "_"); ok && len(segment) >= 2 &&
		len(segment) <= 8 && strings.Trim(segment, "abcdefghijklmnopqrstuvwxyz") == "" {
		call = rest
	}
	return call[:min(8, len(call))]
}

// tookValue is seconds as a guard line shows them: one decimal under 10 s,
// whole seconds under a minute, else Go's duration form ("1m12s"); empty for
// 0, which the log never gives a call that ran.
func tookValue(seconds float64) string {
	took := time.Duration(seconds * float64(time.Second))
	switch {
	case took <= 0:
		return ""
	case took.Round(100*time.Millisecond) < 10*time.Second:
		return fmt.Sprintf("%.1fs", took.Seconds())
	case took.Round(time.Second) < time.Minute:
		return fmt.Sprintf("%.0fs", took.Seconds())
	}
	return took.Round(time.Second).String()
}

// sizeText is a measurement, [guard.Size] ("840 MB", "2.41 GB"); empty for
// 0.
func sizeText(bytes uint64) string {
	if bytes == 0 {
		return ""
	}
	return guard.Size(bytes)
}

// capacityText is a capacity, [guard.Capacity] ("16 GB", "1.86 GB"); empty
// for 0.
func capacityText(bytes uint64) string {
	if bytes == 0 {
		return ""
	}
	return guard.Capacity(bytes)
}

// sizeValue is a size text as one token, without its space ("2.41GB").
func sizeValue(text string) string { return strings.ReplaceAll(text, " ", "") }

// logfmtValue is a value as logfmt writes it: bare when it is one token, else
// in Go's double quotes. A space, a double quote, an equals sign, a backslash
// or a control character ends a bare token, and Go's escapes keep a value
// with a control character on one line.
func logfmtValue(value string) string {
	if strings.ContainsAny(value, " \"=\\") || strings.ContainsFunc(value, unicode.IsControl) {
		return strconv.Quote(value)
	}
	return value
}
