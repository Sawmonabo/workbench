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

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/spf13/cobra"

	"github.com/Sawmonabo/workbench/internal/costs"
	"github.com/Sawmonabo/workbench/internal/operation"
)

// costsFlags are the report flags of `workbench costs`.
type costsFlags struct {
	by, tool, since, until, sort string
	top                          int
	all, detail, tokens          bool
	noRollup, csv                bool
}

// costsCommand reports what AI coding tools cost from the durable ledger, and
// owns the ledger's ingest hook.
func costsCommand(o *options) *cobra.Command {
	f := &costsFlags{}
	cmd := &cobra.Command{
		Use:   "costs",
		Short: "Report what AI coding tools cost, from a durable local ledger",
		Long: "Show per-project, per-model spend from the ledger the SessionStart and SessionEnd " +
			"hooks keep, so it survives the tools deleting old transcripts. At a terminal the " +
			"report opens in one tab per tool (Tab and Shift+Tab switch, q quits); otherwise " +
			"--tool picks the tool to print. Figures are list-price equivalents, not subscription charges.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return o.costsRun(
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
	flags.StringVar(&f.since, "since", "", "First day to include, YYYY-MM-DD")
	flags.StringVar(&f.until, "until", "", "Last day to include, YYYY-MM-DD")
	flags.BoolVar(&f.all, "all", false, "Include projects outside ~/dev and ~/repos")
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
	cmd.AddCommand(costsIngestCommand(o), costsRatesCommand(o), costsStatusCommand(o))
	return cmd
}

// costsRun runs a costs command that prints its own text: only errors and
// --json go through the result envelope.
func (o *options) costsRun(cmd *cobra.Command, run func(*operation.Result) error) error {
	result := operation.NewResult(cmd.CommandPath())
	err := run(&result)
	result.SetError(err)
	o.rendered = true
	if renderErr := render(
		cmd.OutOrStdout(),
		cmd.ErrOrStderr(),
		o.json,
		o.verbose,
		result,
	); renderErr != nil {
		return renderErr
	}
	return err
}

func invalidCostsFlag(format string, args ...any) error {
	return operation.Fail(operation.ExitInvalid, "invocation", fmt.Sprintf(format, args...))
}

// validate checks the flags and returns the tool they name.
func (f *costsFlags) validate() (costs.Tool, error) {
	var none costs.Tool
	if !slices.Contains([]string{"project", "model", "account", "month"}, f.by) {
		return none, invalidCostsFlag("--by must be project, model, account or month")
	}
	if !slices.Contains([]string{"cost", "name", "calls"}, f.sort) {
		return none, invalidCostsFlag("--sort must be cost, name or calls")
	}
	for flag, day := range map[string]string{"--since": f.since, "--until": f.until} {
		if _, err := time.Parse("2006-01-02", day); day != "" && err != nil {
			return none, invalidCostsFlag("%s needs a YYYY-MM-DD date", flag)
		}
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
		Tool: tool.Name, By: f.by, Since: f.since, Until: f.until, All: f.all, Top: f.top,
		Sort: f.sort, Detail: f.detail, NoRollup: f.noRollup, Tokens: f.tokens,
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
	tabs := o.interactive() && !f.csv && out == os.Stdout && operation.IsTerminal(os.Stdout) &&
		operation.IsTerminal(os.Stdin)
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
	if err := ensureIngested(cmd, o); err != nil {
		return err
	}
	paths, err := costs.Locations()
	if err != nil {
		return err
	}
	ledger, err := costs.OpenLedger(paths.Ledger, true)
	if err != nil {
		return err
	}
	defer func() { _ = ledger.Close() }()
	reports := map[string]costs.Statement{}
	for _, t := range costs.Tools {
		if t.Source == nil || (!tabs && t.Name != tool.Name) {
			continue
		}
		if reports[t.Name], err = costs.Report(cmd.Context(), ledger, f.options(t)); err != nil {
			return err
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
		return runCostsView(cmd, tool, reports)
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
	_ = w.Write([]string{
		report.By,
		"cost",
		"calls",
		"input",
		"output",
		"cache_write_5m",
		"cache_write_1h",
		"cache_read",
		"priced",
	})
	for _, r := range report.Rows {
		_ = w.Write([]string{
			r.Name, strconv.FormatFloat(r.Cost, 'f', -1, 64), strconv.FormatInt(r.Calls, 10),
			strconv.FormatInt(r.Input, 10), strconv.FormatInt(r.Output, 10),
			strconv.FormatInt(r.CacheWrite5m, 10), strconv.FormatInt(r.CacheWrite1h, 10),
			strconv.FormatInt(r.CacheRead, 10), strconv.FormatBool(r.Priced),
		})
	}
	w.Flush()
	return w.Error()
}

// runCostsView shows the tabs and, once they quit, prints the active tab's
// report, since the alternate screen leaves nothing in the scrollback. It runs
// after every progress line has stopped: one Bubble Tea program owns the
// terminal at a time.
func runCostsView(cmd *cobra.Command, start costs.Tool, reports map[string]costs.Statement) error {
	active := slices.IndexFunc(costs.Tools, func(t costs.Tool) bool { return t.Name == start.Name })
	model := newCostsView(costs.Tools, reports, active)
	program := tea.NewProgram(
		model,
		tea.WithInput(os.Stdin),
		tea.WithOutput(os.Stdout),
		tea.WithContext(cmd.Context()),
		// main owns SIGINT and SIGTERM through the command's context.
		tea.WithoutSignalHandler(),
	)
	if _, err := program.Run(); err != nil {
		return err
	}
	tool := costs.Tools[model.active]
	return printCosts(os.Stdout, costsReport(tool, reports[tool.Name], terminalWidth(os.Stdout)))
}

// ensureIngested fills an empty ledger inline before the first report, so the
// command works before the hooks are applied.
func ensureIngested(cmd *cobra.Command, o *options) error {
	paths, err := costs.Locations()
	if err != nil {
		return err
	}
	ledger, err := costs.OpenLedger(paths.Ledger, true)
	if err != nil {
		return err
	}
	empty, err := ledger.Empty()
	_ = ledger.Close()
	if err != nil || !empty {
		return err
	}
	_, _ = fmt.Fprintln(
		cmd.ErrOrStderr(),
		glyphs.text.Replace("→ ledger is empty; ingesting transcripts once inline"),
	)
	progress, stop := ingestProgress(o, cmd.ErrOrStderr())
	defer stop()
	_, err = costs.Ingest(cmd.Context(), costs.IngestOptions{Progress: progress})
	return err
}

// ingestProgress is the one live status line at a terminal, and nothing
// otherwise: a transcript per line would drown a pipe or a log.
func ingestProgress(o *options, diagnostics io.Writer) (operation.Progress, func()) {
	progress, stop := newProgress(o, diagnostics)
	if _, live := progress.(*statusLine); !live {
		return nil, func() {}
	}
	progress.Start("Ingesting transcripts")
	return progress, stop
}

// costsIngestCommand is the hook entry, and with --worker the ingest itself.
func costsIngestCommand(o *options) *cobra.Command {
	var worker, quiet bool
	cmd := &cobra.Command{
		Use:   "ingest [EVENT [TRANSCRIPT]]",
		Short: "Hook entry: start a detached worker that copies new usage into the ledger",
		Long: "Without --worker this is the hook command: it reads the hook's JSON from stdin, starts " +
			"`workbench costs ingest --worker` detached and returns at once, printing nothing whatever " +
			"happens. --worker runs the ingest in the foreground.",
		Args: cobra.MaximumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if !worker {
				if stdin, ok := cmd.InOrStdin().(*os.File); ok {
					costs.Hook(stdin)
				} else {
					costs.Hook(nil)
				}
				return nil
			}
			return o.costsRun(cmd, func(result *operation.Result) error {
				opts := costs.IngestOptions{}
				if len(args) > 0 {
					opts.Event = args[0]
				}
				if len(args) > 1 {
					opts.Transcript = args[1]
				}
				if !quiet {
					var stop func()
					opts.Progress, stop = ingestProgress(o, cmd.ErrOrStderr())
					defer stop()
				}
				summary, err := costs.Ingest(cmd.Context(), opts)
				if err == nil && !quiet {
					result.Summary = "[WorkBench] " + summary
				}
				return err
			})
		},
	}
	cmd.Flags().
		BoolVar(&worker, "worker", false, "Run the ingest here instead of starting a detached one")
	cmd.Flags().BoolVarP(&quiet, "quiet", "q", false, "Show no progress and no result")
	return cmd
}

// ratesView is the merged card as --json prints it.
type ratesView struct {
	Official map[string]string `json:"official_fetched"`
	Rows     []rateRow         `json:"rows"`
}

type rateRow struct {
	Model        string  `json:"model"`
	Input        float64 `json:"input"`
	Output       float64 `json:"output"`
	CacheWrite5m float64 `json:"cache_write_5m"`
	CacheWrite1h float64 `json:"cache_write_1h"`
	CacheRead    float64 `json:"cache_read"`
	Source       string  `json:"source"`
	Disagrees    bool    `json:"calibrated_disagrees,omitempty"`
}

func costsRatesCommand(o *options) *cobra.Command {
	var refresh bool
	cmd := &cobra.Command{
		Use:   "rates",
		Short: "Show the merged rate card and where each rate comes from",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return o.costsRun(cmd, func(result *operation.Result) error {
				card, err := costs.Rates(cmd.Context(), refresh)
				for _, note := range card.Notes {
					_, _ = fmt.Fprintln(cmd.ErrOrStderr(), glyphs.text.Replace("→ "+note))
				}
				if err != nil {
					return err
				}
				view := ratesCard(card)
				if o.json {
					result.Results = append(result.Results, operation.Component{
						Name: "costs-rates", Status: operation.StatusComplete, Details: view,
					})
					return nil
				}
				out := cmd.OutOrStdout()
				return printCosts(out, costsRates(card, view, terminalWidth(out)))
			})
		},
	}
	cmd.Flags().BoolVar(&refresh, "refresh", false, "Fetch the official price pages again first")
	return cmd
}

func ratesCard(card costs.Card) ratesView {
	view := ratesView{Official: map[string]string{}}
	for tool, fetched := range card.Fetched {
		view.Official[tool] = fetched.UTC().Format("2006-01-02")
	}
	disagree := card.Disagreements()
	for _, model := range slices.Sorted(mapKeys(card.Rows)) {
		r := card.Rows[model]
		view.Rows = append(view.Rows, rateRow{
			model,
			r.Input,
			r.Output,
			r.CacheWrite5m,
			r.CacheWrite1h,
			r.CacheRead,
			r.Source,
			disagree[model],
		})
	}
	return view
}

func mapKeys[V any](m map[string]V) func(func(string) bool) {
	return func(yield func(string) bool) {
		for k := range m {
			if !yield(k) {
				return
			}
		}
	}
}

func costsRates(card costs.Card, view ratesView, width int) string {
	var b strings.Builder
	writeBold(&b, width, 0, "[WorkBench] Costs rates")
	fetched := "never"
	for _, tool := range costs.Tools {
		if date, ok := view.Official[tool.Name]; ok && tool.Source != nil {
			fetched = date
			break
		}
	}
	writeFaint(&b, width, 0, "USD per 1M tokens; official card fetched: "+fetched)
	b.WriteString("\n")
	spec := tableSpec{
		Header: true,
		Cols: []column{
			{Head: "model prefix", Clip: true, Keep: 24},
			{Head: "input", Right: true},
			{Head: "output", Right: true},
			{Head: "cache 5m", Right: true, Drop: 3},
			{Head: "cache 1h", Right: true, Drop: 4},
			{Head: "cache read", Right: true, Drop: 2},
			{Head: "source", Drop: 1},
			{Head: "", Drop: 5},
		},
	}
	for _, r := range view.Rows {
		flag := ""
		if r.Disagrees {
			flag = yellow.Render("calibrated disagrees >5%")
		}
		spec.Rows = append(spec.Rows, []string{
			r.Model,
			fmt.Sprintf("%.2f", r.Input), fmt.Sprintf("%.2f", r.Output),
			fmt.Sprintf("%.2f", r.CacheWrite5m), fmt.Sprintf("%.2f", r.CacheWrite1h),
			fmt.Sprintf("%.3f", r.CacheRead), r.Source, flag,
		})
	}
	writeTable(&b, width, 2, spec)
	b.WriteString("\n")
	writeFaint(&b, width, 2, fmt.Sprintf(
		"Sources in order: override, official, calibrated, built-in; longest prefix within a source. "+
			"Overrides: %s. Refresh: workbench costs rates --refresh",
		shortPath(card.Overrides),
	))
	return b.String()
}

func costsStatusCommand(o *options) *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show ledger coverage, the last ingest, hooks and the rate card age",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return o.costsRun(cmd, func(result *operation.Result) error {
				if err := ensureIngested(cmd, o); err != nil {
					return err
				}
				info, err := costs.Status(cmd.Context())
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
				return printCosts(out, costsStatus(info, terminalWidth(out)))
			})
		},
	}
}

func costsStatus(info costs.StatusInfo, width int) string {
	var b strings.Builder
	writeBold(&b, width, 0, "[WorkBench] Costs status")
	add := func(label, value string) {
		writeText(&b, width, 0, bold.Render(fmt.Sprintf("%-12s", label))+value)
	}
	add("ledger", fmt.Sprintf("%s (%sB)", shortPath(info.Ledger), human(float64(info.LedgerBytes))))
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
		add("coverage"+suffix, fmt.Sprintf(
			"%s → %s, %s responses (%s session-tagged, %s sweep-tagged)",
			c.First, c.Last, commas(c.Responses), commas(c.SessionRows), commas(c.SweepRows),
		))
	}
	add("last ingest", localTime(info.LastIngestAt)+"  "+faint.Render(info.LastIngestSummary))
	lastError := info.LastError
	if lastError == "" {
		lastError = "none"
	}
	add("last error", lastError)
	worker := "idle"
	if info.WorkerRunning {
		worker = "running (lock held)"
	}
	add("worker", worker)
	overrides := "none"
	if info.HasOverrides {
		overrides = "present"
	}
	for _, tool := range info.Tools {
		if tool.Implemented {
			suffix := ""
			if implemented > 1 {
				suffix = " " + tool.Name
			}
			add(
				"rates"+suffix,
				fmt.Sprintf(
					"official card fetched %s; overrides %s",
					tool.Coverage.RatesFetched,
					overrides,
				),
			)
			var hooks []string
			for _, event := range []string{"SessionStart", "SessionEnd"} {
				state := "MISSING"
				if tool.Hooks[event] {
					state = "ok"
				}
				hooks = append(hooks, event+" "+state)
			}
			add("hooks"+suffix, strings.Join(hooks, ", "))
		}
	}
	return b.String()
}

// shortPath writes a path under the home directory as ~/...
func shortPath(path string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return path
	}
	if path == home {
		return "~"
	}
	if rest, ok := strings.CutPrefix(path, home+string(os.PathSeparator)); ok {
		return "~/" + rest
	}
	return path
}

// ---------------------------------------------------------------- the report

// costsReport is one tool's whole report at width: the [WorkBench] header
// line, then costsBody. It is what is printed to a pipe and, after the tabs
// close, to the scrollback.
func costsReport(tool costs.Tool, report costs.Statement, width int) string {
	var b strings.Builder
	switch {
	case tool.Source == nil:
		writeText(&b, width, 0, "[WorkBench] "+tool.Title+" costs are not implemented yet")
	case report.Empty():
		writeText(&b, width, 0, "[WorkBench] "+emptyReport(report))
	default:
		c := report.Coverage
		writeBold(&b, width, 0, fmt.Sprintf(
			"[WorkBench] Costs · %s → %s · %s responses", c.First, c.Last, commas(c.Responses),
		))
		b.WriteString(costsBody(tool, report, width))
	}
	return b.String()
}

// emptyReport is the line for a report with no rows.
func emptyReport(report costs.Statement) string {
	if report.Hidden > 0 {
		return fmt.Sprintf(
			"no responses matched in ~/dev and ~/repos; %d projects elsewhere (--all shows them)",
			report.Hidden,
		)
	}
	return "no responses matched (check `workbench costs status`)"
}

// costsBody is the report without its header line, which the tabs' tab bar
// replaces. A tool without a source, or without rows, says so.
func costsBody(tool costs.Tool, report costs.Statement, width int) string {
	var b strings.Builder
	switch {
	case tool.Source == nil:
		writeFaint(&b, width, 0, tool.Title+" costs are not implemented yet.")
		return b.String()
	case report.Empty():
		writeFaint(&b, width, 0, emptyReport(report))
		return b.String()
	}
	opts := report.Options
	writeFaint(&b, width, 0, "list-price equivalents, not subscription charges")
	grand := report.GrandTotal
	label := fmt.Sprintf("total · %d %s%s", report.RowCount, report.By, plural(report.RowCount))
	if opts.Detail && report.By == "project" {
		for _, block := range report.Detail {
			share := "-"
			if grand > 0 {
				share = fmt.Sprintf("%.1f%%", block.Project.Cost/grand*100)
			}
			b.WriteString("\n")
			writeText(&b, width, 0, bold.Render(shortPath(block.Project.Name))+"  "+
				green.Render(money(block.Project.Cost))+"  "+
				faint.Render(fmt.Sprintf("%s · %s calls", share, commas(block.Project.Calls))))
			writeTable(
				&b,
				width,
				2,
				reportTable("model", block.Models, grand, opts.Tokens, nil, ""),
			)
		}
		b.WriteString("\n")
		total := report.Total
		writeTable(
			&b,
			width,
			2,
			reportTable("model (all projects)", report.Models, grand, opts.Tokens, &total, label),
		)
	} else {
		b.WriteString("\n")
		total := report.Total
		writeTable(
			&b,
			width,
			2,
			reportTable(report.By, report.Rows, grand, opts.Tokens, &total, label),
		)
		if report.By != "model" {
			b.WriteString("\n")
			writeTable(
				&b,
				width,
				2,
				reportTable("model", report.Models, grand, opts.Tokens, nil, ""),
			)
		}
	}
	if report.By != "account" && len(report.Accounts) > 1 {
		b.WriteString("\n")
		writeTable(
			&b,
			width,
			2,
			reportTable("account", report.Accounts, grand, opts.Tokens, nil, ""),
		)
	}
	b.WriteString("\n")
	writeFooter(&b, width, report)
	if len(report.Unpriced) > 0 {
		b.WriteString("\n")
		writeText(&b, width, 0, yellow.Render("warning:")+fmt.Sprintf(
			" no rate for %s; tokens counted, cost shown as 0. Run `workbench costs rates --refresh` or add them to %s.",
			strings.Join(report.Unpriced, ", "),
			shortPath(overridesPath()),
		))
	}
	return b.String()
}

func overridesPath() string {
	paths, err := costs.Locations()
	if err != nil {
		return "the overrides file"
	}
	return paths.Overrides
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// reportTable is one table of a report. total, when set, is the bold row under
// a rule, named label.
func reportTable(
	head string,
	rows []costs.Row,
	grand float64,
	tokens bool,
	total *costs.Row,
	label string,
) tableSpec {
	spec := tableSpec{
		Header: true,
		Cols:   []column{{Head: head, Clip: true, ClipLeft: true, Keep: 24}},
	}
	spec.Cols = append(spec.Cols, column{Head: "cost", Right: true})
	if tokens {
		spec.Cols = append(spec.Cols,
			column{Head: "calls", Right: true, Drop: 1},
			column{Head: "input", Right: true},
			column{Head: "output", Right: true},
			column{Head: "cache 5m", Right: true, Drop: 3},
			column{Head: "cache 1h", Right: true, Drop: 4},
			column{Head: "cache read", Right: true, Drop: 2},
		)
	} else {
		spec.Cols = append(spec.Cols,
			column{Head: "share", Right: true, Drop: 2},
			column{Head: "calls", Right: true, Drop: 1},
			column{Head: "tokens", Right: true, Drop: 3},
			column{Head: "cached", Right: true, Drop: 4},
		)
	}
	flagged := total != nil && !total.Priced
	for _, r := range rows {
		flagged = flagged || !r.Priced
	}
	if flagged {
		spec.Cols = append(
			spec.Cols,
			column{Drop: 5},
		) // the unpriced flag, only when some row has one
	}
	cells := func(name string, r costs.Row) []string {
		out := []string{shortPath(name), money(r.Cost)}
		if tokens {
			out = append(
				out,
				commas(r.Calls),
				human(float64(r.Input)),
				human(float64(r.Output)),
				human(
					float64(r.CacheWrite5m),
				),
				human(float64(r.CacheWrite1h)),
				human(float64(r.CacheRead)),
			)
		} else {
			share, cached := "-", "-"
			if grand > 0 {
				share = fmt.Sprintf("%.1f%%", r.Cost/grand*100)
			}
			if v, ok := r.CachedShare(); ok {
				cached = fmt.Sprintf("%.1f%%", v*100)
			}
			out = append(out, share, commas(r.Calls), human(float64(r.Total())), cached)
		}
		switch {
		case !flagged:
			return out
		case r.Priced:
			return append(out, "")
		}
		return append(out, yellow.Render("unpriced"))
	}
	for _, r := range rows {
		spec.Rows = append(spec.Rows, cells(r.Name, r))
	}
	if total != nil {
		spec.Total = cells(label, *total)
	}
	return spec
}

// writeFooter writes the notes under the tables: when the ledger last
// ingested and the rate sources always; the --top, scope and --tokens notes
// only when they report something. They share one line when it fits.
func writeFooter(b *strings.Builder, width int, report costs.Statement) {
	c := report.Coverage
	notes := []string{
		"ingested " + localTime(c.LastIngestAt),
		fmt.Sprintf(
			"rates %s (official card %s)",
			strings.Join(report.Sources, ", "),
			c.RatesFetched,
		),
	}
	if report.Options.Tokens {
		notes = append(notes,
			"cache 5m / 1h: prompt tokens written to the cache with that lifetime; "+
				"cache read: prompt tokens served from it")
	}
	if report.RowCount > len(report.Rows) {
		notes = append(notes, fmt.Sprintf(
			"%d of %d %ss shown; totals cover all", len(report.Rows), report.RowCount, report.By))
	}
	if report.Hidden > 0 {
		notes = append(notes, fmt.Sprintf(
			"%d project%s outside ~/dev and ~/repos hidden (--all)",
			report.Hidden,
			plural(report.Hidden),
		))
	}
	if joined := strings.Join(
		notes,
		" · ",
	); lipgloss.Width(
		glyphs.text.Replace(joined),
	)+2 <= width {
		notes = []string{joined}
	}
	for _, note := range notes {
		writeFaint(b, width, 2, note)
	}
}

// writeBold is writeText in bold.
func writeBold(b *strings.Builder, width, indent int, text string) {
	writeStyled(b, width, indent, text, bold)
}

// writeFaint is writeText for secondary notes.
func writeFaint(b *strings.Builder, width, indent int, text string) {
	writeStyled(b, width, indent, text, faint)
}

// writeStyled wraps text like writeText and styles each line, so a wrapped
// note never carries a style across a line break.
func writeStyled(b *strings.Builder, width, indent int, text string, style lipgloss.Style) {
	var wrapped strings.Builder
	writeText(&wrapped, width, indent, text)
	for line := range strings.SplitSeq(strings.TrimSuffix(wrapped.String(), "\n"), "\n") {
		if line != "" {
			line = style.Render(line)
		}
		b.WriteString(line + "\n")
	}
}

// human abbreviates a token count: 2.1K, 600.0M, 1.0B.
func human(n float64) string {
	for _, unit := range []struct {
		suffix string
		scale  float64
	}{{"B", 1e9}, {"M", 1e6}, {"K", 1e3}} {
		if n >= unit.scale {
			return commasFloat(n/unit.scale, 1) + unit.suffix
		}
	}
	return strconv.FormatInt(int64(n), 10)
}

func money(v float64) string { return "$" + commasFloat(v, 2) }

func commas(n int64) string { return commasFloat(float64(n), 0) }

// commasFloat formats v to decimals places with thousands separators.
func commasFloat(v float64, decimals int) string {
	s := strconv.FormatFloat(v, 'f', decimals, 64)
	sign := ""
	if strings.HasPrefix(s, "-") {
		sign, s = "-", s[1:]
	}
	whole, fraction, _ := strings.Cut(s, ".")
	for i := len(whole) - 3; i > 0; i -= 3 {
		whole = whole[:i] + "," + whole[i:]
	}
	if fraction != "" {
		whole += "." + fraction
	}
	return sign + whole
}

// localTime is an ingest timestamp in the machine's zone: "Sep 30 9:00 PM".
func localTime(stamp string) string {
	when, err := time.Parse(time.RFC3339, stamp)
	if err != nil {
		return stamp
	}
	return when.Local().Format("Jan 2 3:04 PM")
}
