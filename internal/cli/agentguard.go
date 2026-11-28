package cli

// This file is the `agent guard` command and its hidden hook and watcher
// commands, and the forms of a call's ID, a time taken and a memory size that
// the log lines and the live view write alike.

import (
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/Sawmonabo/workbench/internal/agent/guard"
	"github.com/Sawmonabo/workbench/internal/display"
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

// tookValue is seconds as a guard line shows them (see [display.Duration]);
// empty for 0, which the log never gives a call that ran.
func tookValue(seconds float64) string {
	took := time.Duration(seconds * float64(time.Second))
	if took <= 0 {
		return ""
	}
	return display.Duration(took)
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
