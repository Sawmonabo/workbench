package cli

import (
	"fmt"
	"io"
	"strings"
	"time"

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
// only what the hook protocol reads. logs is for people.
func guardCommand(o *options) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "guard",
		Short: "Show what the agent command memory guard did",
		Long: "The guard stops the largest shell command Claude Code or Codex started when the " +
			"machine is about to run out of memory, and tells the agent why. Its hooks and " +
			"watcher run by themselves; `workbench guard logs` shows what they did.",
		Args: cobra.NoArgs,
		RunE: showHelp,
	}
	cmd.AddCommand(
		guardLogsCommand(o),
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
				guard.Watch(cmd.Context())
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
		Long: "Show the guard log, oldest first, in this machine's time zone: each stop with the " +
			"command, its size and the reason the agent was told, low memory that stopped nothing, " +
			"calls dropped without their post hook, notes no command shape matched, the watcher " +
			"starting and exiting, errors, and any line of the watcher's own output, such as a " +
			"crash's trace, as it is. --verbose adds every command the watcher tracked and ended. " +
			"--json prints the log's own records (JSON Lines) unchanged, and any other line as a " +
			"raw record. The log is watch.log in the guard folder of Workbench's state folder.",
		Example: "  workbench guard logs\n" +
			"  workbench guard logs --since 2h\n" +
			"  workbench guard logs --since \"2026-10-16 21:00\" --verbose\n" +
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
	// These shadow the root's persistent --json and --verbose, setting the
	// same options, so help describes what they do here.
	flags.BoolVar(
		&o.json,
		"json",
		false,
		"Print the log's records as JSON Lines, other lines as raw records",
	)
	flags.BoolVar(&o.verbose, "verbose", false, "Also show every command tracked and ended")
	flags.BoolVarP(&follow, "follow", "f", false, "Keep printing new events until interrupted")
	flags.StringVar(
		&since,
		"since",
		"",
		"Show events from then on: a duration ago (10m, 2h), today, yesterday, or a time in "+
			"this machine's time zone (2026-10-16, \"2026-10-16 21:00\", 21:00) or RFC 3339",
	)
	return cmd
}

func showGuardLogs(cmd *cobra.Command, o *options, follow bool, sinceText string) error {
	since, err := parseSince(sinceText, time.Now())
	if err != nil {
		return err
	}
	path, err := guard.LogPath()
	if err != nil {
		return err
	}
	out := cmd.OutOrStdout()
	paint, width := newPainter(out), lineWidth(out)
	shown, hidden := 0, 0
	show := func(raw []byte, e guard.Event) error {
		if routineGuardEvents[e.Event] && !o.verbose {
			hidden++
			return nil
		}
		shown++
		if o.json {
			_, err := fmt.Fprintf(out, "%s\n", raw)
			return err
		}
		_, err := io.WriteString(out, guardEventText(paint, width, e))
		return err
	}
	// A raw line has no time. It was written after the record before it, so
	// --since keeps it when it keeps that record; lines before the first
	// record wait for it and are kept when it is.
	type line struct {
		raw []byte
		e   guard.Event
	}
	var waiting []line
	keep, seenRecord := since.IsZero(), false
	err = guard.ReadLog(cmd.Context(), path, follow, func(raw []byte, e guard.Event) error {
		switch {
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
	})
	// The empty-state line is a message, not output, so it goes to stderr
	// (clig.dev) and leaves --json's stdout empty.
	if err != nil || shown > 0 || follow {
		return err
	}
	text := "No guard events yet"
	if !since.IsZero() {
		text = "No guard events since " + since.Local().Format("Jan 2 3:04 PM")
	}
	if hidden > 0 {
		text += fmt.Sprintf("; --verbose shows %d more about the commands it tracked", hidden)
	}
	_, err = fmt.Fprintln(cmd.ErrOrStderr(), text+".")
	return err
}

// routineGuardEvents are shown only with --verbose.
var routineGuardEvents = map[string]bool{guard.EventTracking: true, guard.EventEnded: true}

// guardEventStyles color a stop and its outcome, errors and the watcher's raw
// output red, low memory, calls the guard gave up on or could not match and a
// lost lock yellow, and the watcher's routine faint.
var guardEventStyles = map[string]lipgloss.Style{
	guard.EventStopping: red, guard.EventForced: red, guard.EventGaveUp: red,
	guard.EventError: red, guard.EventRaw: red, guard.EventMemory: yellow,
	guard.EventAbandoned: yellow, guard.EventUnmatched: yellow, guard.EventRefused: yellow,
	guard.EventExited: yellow, guard.EventStarted: faint, guard.EventIdle: faint,
	guard.EventTracking: faint, guard.EventEnded: faint,
}

// guardEventText is one event for people: "Oct 16 9:24 PM  stopping: CMD
// (4.2 GB)", with a stop's reason indented on the lines below, or a raw line
// as it is after "watcher output: ".
func guardEventText(paint painter, width int, e guard.Event) string {
	if e.Event == guard.EventRaw {
		return paint.style(red, "watcher output: "+e.Msg) + "\n"
	}
	line := e.Time.Local().Format("Jan 2 3:04 PM") + "  " + e.Msg
	if e.Command != "" {
		line += ": " + e.Command
	}
	if e.Size > 0 {
		line += " (" + guard.Gigabytes(e.Size) + ")"
	}
	if e.Error != "" {
		line += ": " + e.Error
	}
	var wrapped, b strings.Builder
	writeText(&wrapped, width, 0, line)
	style, styled := guardEventStyles[e.Event]
	for text := range strings.Lines(wrapped.String()) {
		if styled {
			text = paint.style(style, strings.TrimSuffix(text, "\n")) + "\n"
		}
		b.WriteString(text)
	}
	if e.Reason != "" {
		writeText(&b, width, 4, e.Reason)
	}
	return b.String()
}

// parseSince reads --since as `docker logs --since` and `journalctl --since`
// do: a Go duration is that long before now (journalctl's "-2h" counts the
// same), today and yesterday are midnight, and a date or time without a zone
// is in this machine's: a date alone is its midnight, a time alone is today.
// Empty means the start of the log.
func parseSince(value string, now time.Time) (time.Time, error) {
	if value == "" {
		return time.Time{}, nil
	}
	if d, err := time.ParseDuration(value); err == nil {
		return now.Add(-d.Abs()), nil
	}
	midnight := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.Local)
	switch value {
	case "today":
		return midnight, nil
	case "yesterday":
		return midnight.AddDate(0, 0, -1), nil
	}
	if t, err := time.Parse(time.RFC3339, value); err == nil {
		return t, nil
	}
	for _, layout := range []string{
		time.DateOnly, "2006-01-02 15:04", time.DateTime, "2006-01-02T15:04", "2006-01-02T15:04:05",
	} {
		if t, err := time.ParseInLocation(layout, value, time.Local); err == nil {
			return t, nil
		}
	}
	for _, layout := range []string{"15:04", time.TimeOnly} {
		if t, err := time.ParseInLocation(layout, value, time.Local); err == nil {
			return time.Date(now.Year(), now.Month(), now.Day(),
				t.Hour(), t.Minute(), t.Second(), 0, time.Local), nil
		}
	}
	return time.Time{}, operation.Fail(
		operation.ExitInvalid,
		"invocation",
		"--since needs a duration (10m, 2h), today, yesterday, a local date or time "+
			"(2026-10-16, \"2026-10-16 21:00\", 21:00) or an RFC 3339 time",
	)
}
