package cli

import (
	"encoding/csv"
	"fmt"
	"io"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/spf13/cobra"
	"golang.org/x/sync/errgroup"

	"github.com/Sawmonabo/workbench/internal/agent/costs"
	"github.com/Sawmonabo/workbench/internal/display"
	"github.com/Sawmonabo/workbench/internal/machine"
	"github.com/Sawmonabo/workbench/internal/operation"
	"github.com/Sawmonabo/workbench/internal/platform"
)

// costsFlags are the report flags of `workbench agent costs`.
type costsFlags struct {
	by, tool, since, until, sort string
	top                          int
	all, detail, tokens          bool
	noRollup, csv                bool
	from, to                     time.Time // since and until as read by validate
	roots                        []string  // the role folders, read once the ledger is open
}

// agentCostsCommand reports what AI coding tools cost from the durable ledger, and
// owns the ledger's ingest hook.
func agentCostsCommand(o *options) *cobra.Command {
	f := &costsFlags{}
	cmd := &cobra.Command{
		Use:   "costs",
		Short: "Report what Claude Code and Codex cost, from a ledger kept on this machine",
		Long: "Show per-project, per-model spend from the ledger the SessionStart and SessionEnd " +
			"hooks keep, so it survives the tools deleting old transcripts. At a terminal the " +
			"report opens in one tab per tool (Tab and Shift+Tab switch, q quits); otherwise " +
			"--tool picks the tool to print. Figures are list-price equivalents, not subscription charges.",
		Example: "  workbench agent costs\n" +
			"  workbench agent costs --by model --since 2026-11-01\n" +
			"  workbench agent costs --tool codex --by month --csv\n" +
			"  workbench agent costs status",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return o.runPrinted(
				cmd,
				func(result *operation.Result) error { return f.report(cmd, o, result) },
			)
		},
	}
	flags := cmd.Flags()
	flags.StringVar(&f.by, "by", "project", "Group rows by project, model, account or month")
	flags.StringVar(
		&f.tool,
		"tool",
		"claude",
		"The tab to open on, or the tool to print: claude or codex",
	)
	flags.StringVar(
		&f.since,
		"since",
		"",
		"First time to include: "+timeFlagForms,
	)
	flags.StringVar(
		&f.until,
		"until",
		"",
		"Last time to include, the same forms; a day counts to its end",
	)
	flags.BoolVar(&f.all, "all", false, "Include projects outside your role folders")
	flags.IntVar(&f.top, "top", 0, "Show the first N rows; totals still cover all")
	flags.StringVar(&f.sort, "sort", "cost", "Row order: cost, name or calls")
	flags.BoolVar(&f.detail, "detail", false, "With --by project, a model table per project")
	flags.BoolVar(
		&f.tokens,
		"tokens",
		false,
		"Show the five token columns instead of share, tokens and cached",
	)
	flags.BoolVar(&f.noRollup, "no-rollup", false, "Keep every working directory separate")
	flags.BoolVar(&f.csv, "csv", false, "Write the rows as CSV")
	cmd.AddCommand(
		agentCostsIngestCommand(o),
		agentCostsRatesCommand(o),
		agentCostsStatusCommand(o),
	)
	return cmd
}

func invalidCostsFlag(format string, args ...any) error {
	return operation.Fail(operation.ExitInvalid, "invocation", fmt.Sprintf(format, args...))
}

// The values --by and --sort take.
var (
	costsGroupings = []string{"project", "model", "account", "month"}
	costsOrders    = []string{"cost", "name", "calls"}
)

// validate checks the flags and returns the tool they name.
func (f *costsFlags) validate() (costs.Tool, error) {
	var none costs.Tool
	if !slices.Contains(costsGroupings, f.by) {
		return none, invalidCostsFlag("--by must be project, model, account or month")
	}
	if !slices.Contains(costsOrders, f.sort) {
		return none, invalidCostsFlag("--sort must be cost, name or calls")
	}
	now := time.Now()
	var err error
	if f.from, _, err = parseTimeFlag("--since", f.since, now); err != nil {
		return none, err
	}
	var day bool
	if f.to, day, err = parseTimeFlag("--until", f.until, now); err != nil {
		return none, err
	}
	if day {
		f.to = f.to.AddDate(0, 0, 1)
	}
	if f.top < 0 {
		return none, invalidCostsFlag("--top needs a whole number")
	}
	tool, ok := costs.Lookup(f.tool)
	if !ok {
		var names []string
		for _, t := range costs.Tools {
			names = append(names, t.Name)
		}
		return none, invalidCostsFlag("--tool must be one of: %s", strings.Join(names, ", "))
	}
	return tool, nil
}

func (f *costsFlags) options(tool costs.Tool) costs.ReportOptions {
	return costs.ReportOptions{
		Tool:     tool.Name,
		By:       f.by,
		Since:    f.from,
		Until:    f.to,
		All:      f.all,
		Roots:    f.roots,
		Top:      f.top,
		Sort:     f.sort,
		Detail:   f.detail,
		NoRollup: f.noRollup,
		Tokens:   f.tokens,
	}
}

// report prints one tool's report, or opens the tabs at a terminal.
func (f *costsFlags) report(cmd *cobra.Command, o *options, result *operation.Result) error {
	tool, err := f.validate()
	if err != nil {
		return err
	}
	out := cmd.OutOrStdout()
	width := terminalWidth(out)
	tabs := o.interactive() && !f.csv && out == os.Stdout && platform.IsTerminal(os.Stdout.Fd()) &&
		platform.IsTerminal(os.Stdin.Fd())
	if tool.Source == nil && !tabs {
		switch {
		case o.json:
			result.Results = append(result.Results, operation.Component{
				Name:    "costs",
				Status:  operation.StatusComplete,
				Details: map[string]any{"tool": tool.Name, "implemented": false},
			})
		case f.csv:
			return writeCostsCSV(out, costs.Statement{By: f.by})
		default:
			return printCosts(out, costsReport(tool, costs.Statement{}, width))
		}
		return nil
	}
	ledger, paths, err := openIngested(cmd, o)
	if err != nil {
		return err
	}
	defer func() { _ = ledger.Close() }()
	f.roots, err = machine.RoleFolders(paths.Home, paths.Runtime.MachineConfig())
	if err != nil {
		// The report still runs, with the folders it uses when none is saved.
		_, _ = fmt.Fprintln(cmd.ErrOrStderr(), glyphs.text.Replace("→ "+display.PathFrom(
			paths.Home,
			paths.Runtime.MachineConfig(),
		)+" can't be read, so the report groups projects under ~/dev and ~/repos"))
	}
	// The tabs need every tool's report; each reads its own rows, so they are
	// read side by side.
	var group errgroup.Group
	statements := make([]costs.Statement, len(costs.Tools))
	for i, t := range costs.Tools {
		if t.Source == nil || (!tabs && t.Name != tool.Name) {
			continue
		}
		group.Go(func() (err error) {
			statements[i], err = costs.Report(cmd.Context(), ledger, f.options(t))
			return err
		})
	}
	if err := group.Wait(); err != nil {
		return err
	}
	reports := map[string]costs.Statement{}
	for i, t := range costs.Tools {
		if t.Source != nil && (tabs || t.Name == tool.Name) {
			reports[t.Name] = statements[i]
		}
	}
	switch {
	case o.json:
		result.Results = append(result.Results, operation.Component{
			Name:    "costs",
			Status:  operation.StatusComplete,
			Details: reports[tool.Name],
		})
	case f.csv:
		return writeCostsCSV(out, reports[tool.Name])
	case tabs:
		return runCostsView(
			cmd,
			tool,
			reports,
			func(t costs.Tool, kind, name string) (costs.Focus, error) {
				return costs.FocusOn(cmd.Context(), ledger, f.options(t), kind, name)
			},
		)
	default:
		return printCosts(out, costsReport(tool, reports[tool.Name], width))
	}
	return nil
}

// printCosts writes styled text, which lipgloss downsamples to the writer's
// color profile.
func printCosts(out io.Writer, text string) error {
	_, err := lipgloss.Fprint(out, text)
	return err
}

func writeCostsCSV(out io.Writer, report costs.Statement) error {
	w := csv.NewWriter(out)
	head := []string{
		report.By,
		"cost",
		"calls",
		"input",
		"output",
		"cache_write_5m",
		"cache_write_1h",
		"cache_read",
		"priced",
	}
	if report.By == "account" {
		head = append(head, "subscription", "subscription_label")
	}
	_ = w.Write(head)
	for _, r := range report.Rows {
		line := []string{
			r.Name, strconv.FormatFloat(r.Cost, 'f', -1, 64), strconv.FormatInt(r.Calls, 10),
			strconv.FormatInt(r.Input, 10), strconv.FormatInt(r.Output, 10),
			strconv.FormatInt(r.CacheWrite5m, 10), strconv.FormatInt(r.CacheWrite1h, 10),
			strconv.FormatInt(r.CacheRead, 10), strconv.FormatBool(r.Priced),
		}
		if report.By == "account" {
			line = append(line, r.Subscription, r.SubscriptionLabel)
		}
		_ = w.Write(line)
	}
	w.Flush()
	return w.Error()
}

// runCostsView shows the tabs and, once they quit, prints the active tab's
// report, since the alternate screen leaves nothing in the scrollback. It runs
// after every progress line has stopped: one Bubble Tea program owns the
// terminal at a time.
func runCostsView(
	cmd *cobra.Command,
	start costs.Tool,
	reports map[string]costs.Statement,
	load focusLoader,
) error {
	active := slices.IndexFunc(costs.Tools, func(t costs.Tool) bool { return t.Name == start.Name })
	model := newCostsView(costs.Tools, reports, active, load)
	if _, err := runFullScreen(cmd.Context(), model, os.Stdin, os.Stdout); err != nil {
		return err
	}
	tool := costs.Tools[model.active]
	width := terminalWidth(os.Stdout)
	if model.focus != nil {
		var b strings.Builder
		writeStyled(&b, width, 0, brandTag+" "+tool.Title+" costs", bold)
		b.WriteString(focusBody(tool, *model.focus, reports[tool.Name], width))
		return printCosts(os.Stdout, b.String())
	}
	return printCosts(os.Stdout, costsReport(tool, reports[tool.Name], width))
}

// openIngested opens the ledger for a report or the status, and reads a
// tool's transcripts inline before its first report, so the command works
// before the hooks are applied. It also returns the paths it resolved. The
// caller closes the ledger.
func openIngested(cmd *cobra.Command, o *options) (*costs.Ledger, costs.Paths, error) {
	paths, err := costs.Locations()
	if err != nil {
		return nil, costs.Paths{}, err
	}
	upgrade := &upgradeLine{o: o, diagnostics: cmd.ErrOrStderr()}
	ledger, err := paths.Open(cmd.Context(), upgrade.step)
	upgrade.close()
	if err != nil {
		return nil, costs.Paths{}, err
	}
	// A tool with transcripts but no rows has never been read: the ledger is
	// new, or Workbench learned the tool after its hooks were installed.
	var missing []string
	for _, tool := range costs.Tools {
		if tool.Source == nil {
			continue
		}
		empty, err := ledger.Empty(cmd.Context(), tool.Name)
		if err != nil {
			_ = ledger.Close()
			return nil, costs.Paths{}, err
		}
		if !empty {
			continue
		}
		if transcripts, _ := tool.Source.Transcripts(paths.Home); len(transcripts) > 0 {
			missing = append(missing, tool.Title)
		}
	}
	if len(missing) == 0 {
		return ledger, paths, nil
	}
	_, _ = fmt.Fprintln(
		cmd.ErrOrStderr(),
		glyphs.text.Replace(
			"→ no "+strings.Join(
				missing,
				" or ",
			)+" usage in the ledger yet; ingesting transcripts once inline",
		),
	)
	progress, stop := ingestProgress(o, cmd.ErrOrStderr())
	defer stop()
	// A hook's worker may be reading them already: wait for it rather than
	// report an empty ledger.
	if _, err = costs.Ingest(
		cmd.Context(),
		costs.IngestOptions{Progress: progress, Wait: true},
	); err != nil {
		_ = ledger.Close()
		return nil, costs.Paths{}, err
	}
	return ledger, paths, nil
}

// ingestProgress is the one live status line at a terminal, and nothing
// otherwise: a transcript per line would drown a pipe or a log.
func ingestProgress(o *options, diagnostics io.Writer) (operation.Progress, func()) {
	progress, stop := newProgress(o, diagnostics, false)
	if _, live := progress.(*statusLine); !live {
		return nil, func() {}
	}
	progress.Start("Ingesting transcripts")
	return progress, stop
}

// agentCostsIngestCommand is the hook entry, and with --worker the ingest itself.
func agentCostsIngestCommand(o *options) *cobra.Command {
	var worker, quiet bool
	cmd := &cobra.Command{
		Use:   "ingest [EVENT [TRANSCRIPT [SESSION]]]",
		Short: "Read new usage into the ledger: --worker reads now, bare is what the session hooks run",
		Long: "workbench agent costs ingest --worker reads new Claude Code and Codex usage into " +
			"the ledger here, in the foreground, with progress; that is the form to run " +
			"yourself. The apply's agent-costs-ingest step runs it too.\n\n" +
			"Without --worker it is the command the SessionStart and SessionEnd hooks run: it " +
			"reads the hook's JSON from stdin (hook_event_name, transcript_path, session_id), " +
			"starts the worker detached with them and returns at once, printing nothing on " +
			"stdout whatever happens. When it can't start the worker, for example because a " +
			"Workbench folder is not private, it says why on stderr and records nothing.",
		Example: "  workbench agent costs ingest --worker",
		Args:    cobra.MaximumNArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			if !worker {
				costs.Hook(stdinFile(cmd))
				return nil
			}
			opts := costs.IngestOptions{}
			if len(args) > 0 {
				opts.Event = args[0]
			}
			if len(args) > 1 {
				opts.Transcript = args[1]
			}
			if len(args) > 2 {
				opts.SessionID = args[2]
			}
			if quiet && !o.json {
				// Ingest writes its failures to the import log, which is the
				// detached worker's stderr, once its folder is private; printing
				// them again would double each line there.
				o.rendered = true
				_, err := costs.Ingest(cmd.Context(), opts)
				return err
			}
			return o.runPrinted(cmd, func(result *operation.Result) error {
				if !quiet {
					var stop func()
					opts.Progress, stop = ingestProgress(o, cmd.ErrOrStderr())
					defer stop()
				}
				summary, err := costs.Ingest(cmd.Context(), opts)
				if err == nil && !quiet {
					result.Summary = brandTag + " " + summary
				}
				return err
			})
		},
	}
	cmd.Flags().
		BoolVar(&worker, "worker", false, "Run the ingest here instead of starting a detached one")
	cmd.Flags().
		BoolVarP(&quiet, "quiet", "q", false, "Show no progress, result or error (the import log records failures)")
	return cmd
}
