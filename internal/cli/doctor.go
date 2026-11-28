package cli

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/Sawmonabo/workbench/internal/agent/costs"
	"github.com/Sawmonabo/workbench/internal/agent/guard"
	"github.com/Sawmonabo/workbench/internal/agent/toolconfig"
	"github.com/Sawmonabo/workbench/internal/display"
	"github.com/Sawmonabo/workbench/internal/machine"
	"github.com/Sawmonabo/workbench/internal/migrate"
	"github.com/Sawmonabo/workbench/internal/operation"
	"github.com/spf13/cobra"
)

// doctorCommand checks this machine without repairing it: Workbench's own
// lines (see machine.Doctor), then the agent hooks, the guard's watcher and
// any agent folder still at its old path.
func doctorCommand(o *options) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Check Workbench, your runtimes, and the agent costs and agent guard hooks, without repair",
		Long: "Show what Workbench has installed and applied, the steps you skipped or " +
			"selected, its tools and the host; then Node, Python, Rust, Go and bun against " +
			"their pins, the agent costs and agent guard hooks in Claude Code and Codex, " +
			"whether the agent guard's watcher runs, and an agent costs or guard folder still at " +
			"its old path. It reads only this machine and repairs nothing. A missing " +
			"prerequisite returns nonzero; a missing runtime or hook does not, and workbench " +
			"apply fixes it.",
		Example: "  workbench doctor\n" +
			"  workbench doctor --json",
		Args: cobra.NoArgs,
		RunE: o.action(
			machineAction,
			func(cmd *cobra.Command, c operation.Context) (operation.Result, error) {
				result := operation.NewResult(cmd.CommandPath())
				components, err := machine.Doctor(cmd.Context(), c)
				result.Results = append(
					[]operation.Component{{
						Name:    "workbench",
						Status:  operation.StatusComplete,
						Message: shownVersion(),
					}},
					components...,
				)
				result.Results = append(result.Results, agentHooks(c.Home)...)
				result.Results = append(result.Results, guardWatcher(cmd.Context()))
				result.Results = append(result.Results, oldAgentFolders(c)...)
				// A check that found nothing wrong reads ok, not the complete
				// or unchanged of a step that ran.
				for i, line := range result.Results {
					if line.Status == operation.StatusComplete ||
						line.Status == operation.StatusUnchanged {
						result.Results[i].Status = operation.StatusOK
					}
				}
				if err == nil {
					result.Summary = doctorSummary(result.Results)
				}
				return result, err
			},
		),
	}
	o.localBuildFlag(cmd)
	return cmd
}

// doctorSummary is "Every check passed", or, when lines that block nothing
// still need a look, names them.
func doctorSummary(components []operation.Component) string {
	var look []string
	for _, component := range components {
		switch component.Status {
		case operation.StatusOK, operation.StatusSkipped:
		default:
			look = append(look, component.Name)
		}
	}
	if len(look) == 0 {
		return brandTag + " Every check passed"
	}
	return brandTag + " Nothing blocks Workbench; look at " + strings.Join(look, ", ")
}

// agentHooks is one doctor line per agent: whether Claude Code and Codex run
// the agent costs and agent guard hooks Workbench installs, read the way
// workbench agent costs status reads them. A missing hook is absent, and one
// the agent does not run (Codex's untrusted or disabled) is a conflict;
// neither blocks Workbench, and workbench apply installs them and, in Codex,
// trusts them. Each group with a hook that does not run has a line of its
// own, labeled with the group's command.
func agentHooks(home string) []operation.Component {
	var lines []operation.Component
	for _, tool := range costs.Tools {
		if tool.Source == nil {
			continue
		}
		groups := []struct {
			command string
			hooks   []toolconfig.HookCommand
		}{
			{"agent costs", costs.IngestHooks},
			{"agent guard", guard.Hooks(tool.Name)},
		}
		line := operation.Component{
			Name:    "agent-hooks-" + tool.Name,
			Status:  operation.StatusComplete,
			Message: "agent costs and agent guard hooks are installed",
		}
		var states []string
		var topics []operation.Part
		none := true
		for _, group := range groups {
			var missing []string
			idle := map[string][]string{}
			var order []string
			for i, state := range tool.Hooks(home, group.hooks) {
				states = append(states, state)
				event := group.hooks[i].Event
				switch state {
				case toolconfig.HookOK:
				case toolconfig.HookMissing:
					missing = append(missing, event)
				default:
					if _, ok := idle[state]; !ok {
						order = append(order, state)
					}
					idle[state] = append(idle[state], event)
				}
			}
			none = none && len(missing) == len(group.hooks)
			var said []string
			if len(missing) > 0 {
				line.Status = operation.StatusAbsent
				said = append(said, fmt.Sprintf("%d of %d hooks missing (%s)",
					len(missing), len(group.hooks), strings.Join(missing, ", ")))
			}
			for _, state := range order {
				if line.Status == operation.StatusComplete {
					line.Status = operation.StatusConflict
				}
				said = append(said, fmt.Sprintf("%d %s (%s)",
					len(idle[state]), state, strings.Join(idle[state], ", ")))
			}
			if len(said) > 0 {
				topics = append(topics, operation.Line{
					Label: group.command,
					Text:  strings.Join(said, ", "),
				})
			}
		}
		if line.Status != operation.StatusComplete {
			line.Message = ""
		}
		if none {
			line.Message, topics = tool.Title+" has no agent costs or agent guard hooks", nil
		}
		line.Body = operation.Explain(append(topics, hookFixes(home, tool, states)...)...)
		lines = append(lines, line)
	}
	return lines
}

// hookFixes is what the doctor line of tool's hooks, in states, says on how
// to fix them: workbench apply when the apply fixes them
// (toolconfig.ApplyFixes; nothing in a folder a variable moved, which
// doctor's own line for the move explains), and the settings file the tool
// skips, which no apply fixes: the apply keeps the user's settings.
func hookFixes(home string, tool costs.Tool, states []string) []operation.Part {
	var parts []operation.Part
	// Claude Code skips a settings file with a setting it refuses, hooks
	// and all, and Codex does not start with a config.toml it cannot parse.
	if slices.Contains(states, toolconfig.HookSkipped) {
		if tool.Name == "codex" {
			parts = append(parts,
				operation.Cause("Codex cannot parse "+display.Path(toolconfig.CodexConfig(home))),
				operation.FixText("fix that file"))
		} else {
			parts = append(
				parts,
				operation.Cause(
					"Claude Code skips "+display.Path(toolconfig.ClaudeUserSettings(home))+
						" for a setting it refuses",
				),
				operation.FixText("fix that setting"),
			)
		}
	}
	if fixes := toolconfig.ApplyFixes(home, tool.Title, states); len(fixes) > 0 {
		parts = append(
			parts,
			operation.Line{Label: "apply", Text: strings.Join(fixes, " and ") + " them"},
			operation.Fix("workbench", "apply"),
		)
	}
	return parts
}

// guardWatcher is the doctor line for the guard's watcher: whether one runs,
// told from its lock, as every entry tells it (see guard.WatcherRuns), and,
// when one does, whether it answers on its socket. The watcher starts with
// an agent session and exits when idle, so one that is not running is no
// problem. A lock held with no answer is said as the view says it (see
// guard.WatcherSilent).
func guardWatcher(ctx context.Context) operation.Component {
	line := operation.Component{
		Name:    "agent-guard-watcher",
		Status:  operation.StatusComplete,
		Message: "running",
	}
	runs, err := guard.WatcherRuns()
	switch {
	case err != nil:
		line.Status = operation.StatusConflict
		line.Message = "cannot tell whether the watcher runs"
		line.Body = operation.Explain(operation.Cause(err.Error()))
		return line
	case !runs:
		line.Status = operation.StatusSkipped
		line.Message = "not running"
		line.Body = operation.Explain(
			operation.Cause("the next agent session starts it, and it exits when idle"))
		return line
	}
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	client, err := guard.Dial(ctx)
	if err != nil {
		line.Status = operation.StatusConflict
		cause := guard.WatcherSilentCause
		if !errors.Is(err, syscall.ENOENT) && !errors.Is(err, syscall.ECONNREFUSED) {
			cause += " (" + err.Error() + ")"
		}
		line.Message = guard.WatcherSilentHeadline
		line.Body = operation.Explain(
			operation.Cause(cause),
			operation.Fix(guard.WatcherSilentFix...),
		)
		return line
	}
	_ = client.Close()
	return line
}

// oldAgentFolders is the doctor line for an agent costs or guard folder still
// at its old path, which migration 0007 moves under agent/: it names each and
// why it stays (see [migrate.AgentFoldersLeft]). There is no line once every
// folder has moved.
func oldAgentFolders(c operation.Context) []operation.Component {
	status, text := migrate.AgentFoldersLeft(migrate.Locations{Home: c.Home, Runtime: c.Paths})
	if text == "" {
		return nil
	}
	line := operation.Component{Name: "agent-folders", Status: status, Message: text}
	if status == operation.StatusPending {
		// The next apply's migrations step does it too, or says why a folder
		// stays.
		line.Body = operation.Explain(operation.Fix("workbench", "migrate"))
	}
	return []operation.Component{line}
}
