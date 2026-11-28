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
// trusts them.
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
		var absent, idling []string
		none, untrusted, skipped := true, false, false
		for _, group := range groups {
			var missing, idle []string
			for i, state := range tool.Hooks(home, group.hooks) {
				switch state {
				case toolconfig.HookOK:
				case toolconfig.HookMissing:
					missing = append(missing, group.hooks[i].Event)
				default:
					untrusted = untrusted || state == toolconfig.HookUntrusted
					skipped = skipped || state == toolconfig.HookSkipped
					idle = append(idle, group.hooks[i].Event+" "+state)
				}
			}
			none = none && len(missing) == len(group.hooks)
			if len(missing) > 0 {
				absent = append(absent, fmt.Sprintf(
					"%d of %d %s hooks (%s)",
					len(missing), len(group.hooks), group.command, strings.Join(missing, ", "),
				))
			}
			if len(idle) > 0 {
				idling = append(idling, fmt.Sprintf(
					"%d of %d %s hooks (%s)",
					len(idle), len(group.hooks), group.command, strings.Join(idle, ", "),
				))
			}
		}
		line := operation.Component{
			Name:    "agent-hooks-" + tool.Name,
			Status:  operation.StatusComplete,
			Message: "agent costs and agent guard hooks are installed",
		}
		var problems []string
		if len(absent) > 0 {
			line.Status = operation.StatusAbsent
			problems = append(problems, "is missing "+strings.Join(absent, " and "))
		}
		if len(idling) > 0 {
			if line.Status == operation.StatusComplete {
				line.Status = operation.StatusConflict
			}
			problems = append(problems, "does not run "+strings.Join(idling, " and "))
		}
		switch {
		case none:
			line.Message = tool.Title + " has no agent costs or agent guard hooks"
		case len(problems) > 0:
			line.Message = tool.Title + " " + strings.Join(problems, " and ")
		}
		line.Message += hookFixes(home, tool, len(absent) > 0, untrusted, skipped)
		lines = append(lines, line)
	}
	return lines
}

// hookFixes is what the doctor line of tool's hooks adds on how to fix them:
// the apply installs absent ones and, for Codex, trusts them, except in a
// folder a variable moved, and a settings file Claude Code skips is named.
func hookFixes(home string, tool costs.Tool, absent, untrusted, skipped bool) string {
	var hint string
	// Codex runs a hook only once it is recorded as trusted, which the
	// apply does for the hooks it installs.
	var fixes []string
	if absent {
		fixes = append(fixes, "installs")
	}
	if untrusted || (absent && tool.Name == "codex") {
		fixes = append(fixes, "trusts")
	}
	// Workbench installs nothing in a folder a variable moved, which
	// doctor's own line for the move explains.
	moved := slices.ContainsFunc(toolconfig.Moves(home), func(m toolconfig.Move) bool {
		return m.Tool == tool.Title && m.Source != ""
	})
	if len(fixes) > 0 && !moved {
		hint += "; workbench apply " + strings.Join(fixes, " and ") + " them"
	}
	// Claude Code skips a settings file with a setting it refuses, hooks
	// and all, which no apply can fix: the apply keeps the user's settings.
	if skipped {
		hint += "; Claude Code skips " + display.Path(toolconfig.ClaudeUserSettings(home)) +
			" for a setting it refuses, so fix that setting"
	}
	return hint
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
		line.Message = "cannot tell whether the watcher runs: " + err.Error()
		return line
	case !runs:
		line.Status = operation.StatusSkipped
		line.Message = "not running; the next agent session starts it, and it exits when idle"
		return line
	}
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	client, err := guard.Dial(ctx)
	if err != nil {
		line.Status = operation.StatusConflict
		line.Message = guard.WatcherSilent
		if !errors.Is(err, syscall.ENOENT) && !errors.Is(err, syscall.ECONNREFUSED) {
			line.Message += " (" + err.Error() + ")"
		}
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
	if status == operation.StatusPending {
		text += "; workbench migrate, or the next apply, does it, or says why a folder stays"
	}
	return []operation.Component{{Name: "agent-folders", Status: status, Message: text}}
}
