package cli

import (
	"fmt"
	"io"
	"log/slog"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"

	"charm.land/lipgloss/v2"
	"github.com/spf13/cobra"

	"github.com/Sawmonabo/workbench/internal/guard"
	"github.com/Sawmonabo/workbench/internal/operation"
)

// guardCommand groups the agent command guard's entries. Claude Code and
// Codex run pre and post as hooks, and pre starts watch with fixed arguments;
// those three are hidden. Like askpass they are plain commands, not actions,
// and print no result envelope. pre and post take any arguments, flags
// included (flag parsing is off, so even the root's --json is just an
// argument), so nothing an agent passes makes them exit non-zero, and print
// only what the hook protocol reads. logs and memory are for people.
func guardCommand(o *options) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "guard",
		Short: "Show what the agent command memory guard did, and watch what it tracks",
		Long: "The guard stops the largest shell command Claude Code or Codex started when the " +
			"machine is about to run out of memory, and tells the agent why. Its hooks and " +
			"watcher run by themselves. `workbench guard logs` shows what they did, one line " +
			"per event. `workbench guard memory` shows, live, the commands the guard tracks " +
			"and stops the ones you choose.",
		Example: "  workbench guard logs --since 2h\n" +
			"  workbench guard memory",
		Args: cobra.NoArgs,
		RunE: showHelp,
	}
	cmd.AddCommand(
		guardLogsCommand(o),
		guardMemoryCommand(o),
		&cobra.Command{
			Use:                "pre",
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
			Hidden:             true,
			DisableFlagParsing: true,
			Args:               cobra.ArbitraryArgs,
			RunE: func(cmd *cobra.Command, _ []string) error {
				guard.Post(stdinFile(cmd), cmd.OutOrStdout())
				return nil
			},
		},
		&cobra.Command{
			Use:    "watch",
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

// guardLogsCommand prints the guard log. --json passes its records through as
// they are, one per line, so only an error goes through the result envelope.
func guardLogsCommand(o *options) *cobra.Command {
	var follow bool
	var since string
	cmd := &cobra.Command{
		Use:   "logs",
		Short: "Show the commands the guard stopped, memory warnings and watcher errors",
		Long: "Show the guard log, oldest first, one line per event: the time in this machine's " +
			"time zone, the level and the event, then key=value fields in a fixed order (id, " +
			"agent, subagent, project, by, dry_run, took, size, peak, floor, level, available, " +
			"total, still_running, release, old_release, new_release, new_pid, msg, error, " +
			"process, cmd), a value with a space, quote, equals sign, backslash or control " +
			"character in Go quotes. " +
			"The events are each command that ended, matched no known shape or was stopped, low " +
			"memory that stopped nothing, calls dropped without their post hook, the watcher " +
			"starting, handing over and exiting, errors, and any line of the watcher's own " +
			"output, such as a crash's trace, as a raw event placed at the start of the watcher " +
			"that wrote it. With --follow, a command still running 5 seconds after the watcher " +
			"found it gets one running line. --json prints the log's own records (JSON Lines) " +
			"unchanged, the full call IDs, stop reasons and each tracked command included, and " +
			"any other line as a raw record. The log is watch.log in the guard folder of " +
			"Workbench's state folder, and the watcher's own output watch.err beside it.",
		Example: "  workbench guard logs\n" +
			"  workbench guard logs --since 2h\n" +
			"  workbench guard logs --since \"2026-10-16 21:00\"\n" +
			"  workbench guard logs --follow\n" +
			"  workbench guard logs --json --since today",
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
	// This shadows the root's persistent --json, setting the same option, so
	// help describes what it does here.
	flags.BoolVar(
		&o.json,
		"json",
		false,
		"Print the log's records as JSON Lines, other lines as raw records",
	)
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
	guard.EventError: true, guard.EventRaw: true,
}

// guardLine is one event on one line, as logfmt readers parse it after its
// first three fields: `Oct 17 14:41:05 INFO  ended     id=4fQ8nX2a
// project=api took=53s peak=2.4GB cmd="cargo test"`. The time is local, the
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
// value left out: a call's short id, where and by whom, how long it took and
// how large it got, the memory reading, the watcher's releases and processes,
// then msg, error, process and cmd. Values are plain except the agent in its
// hue, a stop by you and a took of 10 s or more bold, and a size or peak bold
// in gold from 1 GB and in red from 4 GB.
func guardFields(e guard.Event, running bool) []guardField {
	took, peak := e.Took, e.Peak
	if running {
		took, peak = 0, 0
	}
	stillRunning := make([]string, len(e.StillRunning))
	for i, pid := range e.StillRunning {
		stillRunning[i] = strconv.Itoa(pid)
	}
	msg := ""
	if guardMessageEvents[e.Event] {
		msg = e.Msg
	}
	fields := []guardField{
		{"id", shortCallID(e.Call), nil},
		{"agent", e.Agent, agentStyle(e.Agent)},
		{"subagent", e.Subagent, nil},
		{"project", e.Project, nil},
		{"by", e.By, styleIf(e.By == "you", bold)},
		{"dry_run", whenTrue(e.DryRun), nil},
		{"took", tookValue(took), styleIf(took >= 10, bold)},
		{"size", sizeValue(e.Size), sizeStyle(e.Size)},
		{"peak", sizeValue(peak), sizeStyle(peak)},
		{"floor", sizeValue(e.Floor), nil},
		{"level", nonZero(e.Memory.Level), nil},
		{"available", sizeValue(e.Memory.Available), nil},
		{"total", sizeValue(e.Memory.Total), nil},
		{"still_running", strings.Join(stillRunning, ","), nil},
		{"release", e.Release, nil},
		{"old_release", e.OldRelease, nil},
		{"new_release", e.NewRelease, nil},
		{"new_pid", nonZero(e.NewPID), nil},
		{"msg", msg, nil},
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
// "toolu_vrtx_01…"), the same in every ID, so it is left out too.
func shortCallID(call string) string {
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

// sizeText is a size in bytes as people read it: whole MB under 1 GB, else
// [guard.Gigabytes] ("840 MB", "2.4 GB"); empty for 0.
func sizeText(bytes uint64) string {
	switch {
	case bytes == 0:
		return ""
	case bytes < 1<<30:
		return fmt.Sprintf("%.0f MB", float64(bytes)/(1<<20))
	}
	return guard.Gigabytes(bytes)
}

// sizeValue is [sizeText] as one token, without its space ("2.4GB").
func sizeValue(bytes uint64) string { return strings.ReplaceAll(sizeText(bytes), " ", "") }

// logfmtValue is a value as logfmt writes it: bare when it is one token, else
// in Go's double quotes. A space, a double quote, an equals sign, a backslash
// or a control character ends a bare token, and Go's escapes keep a newline
// in a command on the one line.
func logfmtValue(value string) string {
	if strings.ContainsAny(value, " \"=\\") || strings.ContainsFunc(value, unicode.IsControl) {
		return strconv.Quote(value)
	}
	return value
}
