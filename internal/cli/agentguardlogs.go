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

	"github.com/Sawmonabo/workbench/internal/agent/guard"
	"github.com/Sawmonabo/workbench/internal/display"
	"github.com/Sawmonabo/workbench/internal/operation"
)

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
		text = "No guard events since " + since.Local().Format(display.DayTime)
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
	guard.EventError: true, guard.EventRaw: true, guard.EventMoved: true,
}

// guardLine is one event on one line, as logfmt readers parse it after its
// first four fields: `Oct 17  2:41:05 PM INFO  ended     id=4fQ8nX2a
// project=api took=53s peak=2.41GB cmd="cargo test"`. The time is local and
// the same width on every line (see [display.Stamp]), the level is padded to
// 5 and the event to 9, and a raw line, which has no time, leaves the time
// blank. The time and the keys are faint, the level label
// alone is colored and the event is plain. Fields follow in a fixed order, cmd
// last as the longest, each left out when empty; a field the order does not
// name stays in --json only. running prints a tracking record as a running
// line, without took and peak.
func guardLine(paint painter, e guard.Event, running bool) string {
	name := e.Event
	if running {
		name = "running"
	}
	when := strings.Repeat(" ", display.StampWidth)
	if !e.Time.IsZero() {
		when = display.Stamp(e.Time)
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
