package cli

import (
	"cmp"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Sawmonabo/workbench/internal/agent/costs"
	"github.com/Sawmonabo/workbench/internal/agent/toolconfig"
	"github.com/Sawmonabo/workbench/internal/display"
	"github.com/Sawmonabo/workbench/internal/operation"
)

func agentCostsStatusCommand(o *options) *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show ledger coverage, the last ingest, hooks and the rate card age",
		Long: "Show what the ledger covers for each tool, when usage was last read into it, " +
			"whether each tool runs the session hooks that keep it, and how old the rate card " +
			"is. Run it when a report looks short.",
		Example: "  workbench agent costs status\n" +
			"  workbench agent costs status --json",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return o.runPrinted(cmd, func(result *operation.Result) error {
				ledger, _, err := openIngested(cmd, o)
				if err != nil {
					return err
				}
				defer func() { _ = ledger.Close() }()
				info, err := costs.Status(cmd.Context(), ledger)
				if err != nil {
					return err
				}
				if o.json {
					result.Results = append(result.Results, operation.Component{
						Name: "costs-status", Status: operation.StatusComplete, Details: info,
					})
					return nil
				}
				out := cmd.OutOrStdout()
				return printCosts(out, costsStatus(info, lineWidth(out)))
			})
		},
	}
}

func costsStatus(info costs.StatusInfo, width int) string {
	var b strings.Builder
	writeBold(&b, width, 0, brandTag+" Costs status")
	rows := [][2]string{{
		"ledger",
		display.Path(info.Ledger) + " (" + human(float64(info.LedgerBytes)) + "B)",
	}}
	add := func(label, value string) { rows = append(rows, [2]string{label, value}) }
	implemented := 0
	for _, tool := range info.Tools {
		if tool.Implemented {
			implemented++
		}
	}
	for _, tool := range info.Tools {
		if !tool.Implemented {
			continue
		}
		suffix := ""
		if implemented > 1 {
			suffix = " " + tool.Name
		}
		c := tool.Coverage
		add("sign-in"+suffix, signedIn(tool.SignIn))
		add("coverage"+suffix, fmt.Sprintf(
			"%s → %s, %s responses", c.First, c.Last, display.Number(c.Responses),
		))
		add("email evidence"+suffix, attribution(c.AccountEvidence))
		add("plan evidence"+suffix, attribution(c.SubscriptionEvidence))
	}
	lastIngest := localTime(info.LastIngestAt)
	if info.LastIngestSummary != "" {
		lastIngest += "  " + faint.Render(info.LastIngestSummary)
	}
	add("last ingest", lastIngest)
	add("last error", cmp.Or(info.LastError, "none"))
	worker := "idle"
	if info.WorkerRunning {
		worker = "running (lock held)"
	}
	add("worker", worker)
	for _, tool := range info.Tools {
		if tool.Implemented {
			suffix := ""
			if implemented > 1 {
				suffix = " " + tool.Name
			}
			add("rates"+suffix, "official card fetched "+tool.Coverage.RatesFetched)
			add("hooks"+suffix, hookStates(tool))
			if tool.ConfigMove != nil {
				add("config"+suffix, tool.ConfigMove.Explain(display.Home()))
			}
			if tool.Skipped > 0 {
				add(
					"skipped"+suffix,
					fmt.Sprintf("%d compressed rollouts (.jsonl.zst) not read", tool.Skipped),
				)
			}
		}
	}
	labelWidth := 12
	for _, row := range rows {
		labelWidth = max(labelWidth, len(row[0])+2)
	}
	// A value wraps under itself, not under the labels.
	for _, row := range rows {
		head := bold.Render(fmt.Sprintf("%-*s", labelWidth, row[0]))
		b.WriteString(strings.Join(labelledLines(head, row[1], width, 1), "\n") + "\n")
	}
	return b.String()
}

// hookStates is a tool's ingest hook state for each session event, with the
// command that installs or trusts them when it does something about them
// (toolconfig.ApplyFixes).
func hookStates(tool costs.ToolStatus) string {
	var hooks, states []string
	for _, event := range []string{"SessionStart", "SessionEnd"} {
		state := cmp.Or(tool.Hooks[event], toolconfig.HookMissing)
		states = append(states, state)
		hooks = append(hooks, event+" "+state)
	}
	line := strings.Join(hooks, ", ")
	if len(toolconfig.ApplyFixes(display.Home(), tool.Title, states)) > 0 {
		line += " (workbench apply)"
	}
	return line
}

// signedIn is a tool's current sign-in as an account row names it.
func signedIn(s costs.SignInStatus) string {
	if text := s.Display(); text != "unknown" {
		return text
	}
	return "not signed in"
}

// attribution counts a tool's rows by the evidence that tied each to one
// field (its email or its subscription), strongest first, leaving out the
// levels with none.
func attribution(e costs.Evidence) string {
	var parts []string
	for level, n := range e.Levels() {
		if n > 0 {
			parts = append(parts, display.Number(n)+" "+level)
		}
	}
	if len(parts) == 0 {
		return "no rows"
	}
	return strings.Join(parts, ", ")
}
