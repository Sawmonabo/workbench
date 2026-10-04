package cli

import (
	"cmp"
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
	flags.StringVar(
		&f.since,
		"since",
		"",
		"First day to include, YYYY-MM-DD in this machine's time zone",
	)
	flags.StringVar(
		&f.until,
		"until",
		"",
		"Last day to include, YYYY-MM-DD in this machine's time zone",
	)
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
	width := terminalWidth(os.Stdout)
	if model.focus != nil {
		var b strings.Builder
		writeStyled(&b, width, 0, "[WorkBench] "+tool.Title+" costs", bold)
		b.WriteString(focusBody(tool, *model.focus, reports[tool.Name], width))
		return printCosts(os.Stdout, b.String())
	}
	return printCosts(os.Stdout, costsReport(tool, reports[tool.Name], width))
}

// ensureIngested reads a tool's transcripts inline before its first report, so
// the command works before the hooks are applied.
func ensureIngested(cmd *cobra.Command, o *options) error {
	paths, err := costs.Locations()
	if err != nil {
		return err
	}
	ledger, err := costs.OpenLedger(paths.Ledger, true)
	if err != nil {
		return err
	}
	// A tool with transcripts but no rows has never been read: the ledger is
	// new, or Workbench learned the tool after its hooks were installed.
	var missing []string
	for _, tool := range costs.Tools {
		if tool.Source == nil {
			continue
		}
		empty, err := ledger.Empty(tool.Name)
		if err != nil {
			_ = ledger.Close()
			return err
		}
		if transcripts, _ := tool.Source.Transcripts(paths.Home); empty && len(transcripts) > 0 {
			missing = append(missing, tool.Title)
		}
	}
	_ = ledger.Close()
	if len(missing) == 0 {
		return nil
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
		Use:   "ingest [EVENT [TRANSCRIPT [SESSION]]]",
		Short: "Hook entry: start a detached worker that copies new usage into the ledger",
		Long: "Without --worker this is the hook command: it reads the hook's JSON from stdin " +
			"(hook_event_name, transcript_path, session_id), starts " +
			"`workbench costs ingest --worker` detached with them and returns at once, printing nothing whatever " +
			"happens. --worker runs the ingest in the foreground.",
		Args: cobra.MaximumNArgs(3),
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
				if len(args) > 2 {
					opts.SessionID = args[2]
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
		view.Official[tool] = fetched.Local().Format(time.DateOnly)
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
	var rows [][2]string
	add := func(label, value string) { rows = append(rows, [2]string{label, value}) }
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
		add("sign-in"+suffix, signedIn(tool.SignIn))
		add("coverage"+suffix, fmt.Sprintf(
			"%s → %s, %s responses", c.First, c.Last, commas(c.Responses),
		))
		add("email evidence"+suffix, attribution(c.AccountEvidence))
		add("plan evidence"+suffix, attribution(c.SubscriptionEvidence))
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
			fixable := false
			for _, event := range []string{"SessionStart", "SessionEnd"} {
				state := cmp.Or(tool.Hooks[event], costs.HookMissing)
				fixable = fixable || state == costs.HookMissing || state == costs.HookUntrusted
				hooks = append(hooks, event+" "+state)
			}
			line := strings.Join(hooks, ", ")
			if fixable {
				line += " (workbench apply)"
			}
			add("hooks"+suffix, line)
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
		var value strings.Builder
		writeText(&value, width, labelWidth, row[1])
		first, rest, _ := strings.Cut(value.String(), "\n")
		b.WriteString(bold.Render(fmt.Sprintf("%-*s", labelWidth, row[0])) +
			strings.TrimLeft(first, " ") + "\n" + rest)
	}
	return b.String()
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
	for _, level := range []struct {
		n    int64
		name string
	}{
		{e.Transcript, "transcript"},
		{e.Session, "session"},
		{e.Observed, "observed"},
		{e.Unknown, "unknown"},
	} {
		if level.n > 0 {
			parts = append(parts, commas(level.n)+" "+level.name)
		}
	}
	if len(parts) == 0 {
		return "no rows"
	}
	return strings.Join(parts, ", ")
}

// rowName is how a report row of tool is named: a path under the home
// directory as ~/..., and a model at a service tier as "gpt-5.6-sol (fast)".
func rowName(tool costs.Tool, name string) string {
	if base, tier := tool.SplitModel(name); tier != "" {
		return base + " (" + tier + ")"
	}
	return shortPath(name)
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

// palette is one tool's colors. lipgloss downsamples them to what the
// terminal shows and drops them for a pipe, TERM=dumb and NO_COLOR.
type palette struct {
	accent lipgloss.Style // the headline, column heads, share bars and brand
	tab    lipgloss.Style // the active tab: dark text on the accent
}

// toolColors are the tools' own brand colors.
var toolColors = map[string]string{"claude": "#D97757", "codex": "#10A37F"}

func toolPalette(name string) palette {
	hex, ok := toolColors[name]
	if !ok {
		hex = "#7AA2F7"
	}
	accent := lipgloss.Color(hex)
	return palette{
		accent: lipgloss.NewStyle().Foreground(accent),
		tab: lipgloss.NewStyle().Bold(true).Padding(0, 1).
			Foreground(lipgloss.Color("#1E1E1E")).Background(accent),
	}
}

// costStyle colors every dollar figure.
var costStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#8FC37E"))

// minBar is the narrowest share bar; a terminal with less spare width gets
// the percentages alone.
const minBar = 8

// costsReport is one tool's whole report at width: the [WorkBench] line, then
// costsBody. It is what is printed to a pipe and, after the tabs close, to the
// scrollback.
func costsReport(tool costs.Tool, report costs.Statement, width int) string {
	var b strings.Builder
	switch {
	case tool.Source == nil:
		writeText(&b, width, 0, "[WorkBench] "+tool.Title+" costs are not implemented yet")
	case report.Empty():
		writeText(&b, width, 0, "[WorkBench] "+emptyReport(report))
	default:
		writeStyled(&b, width, 0, "[WorkBench] "+tool.Title+" costs", bold)
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

// target is a row the tab view can open: a project or a model.
type target struct{ kind, name string }

// costsBody is the report under the brand line, which the tabs replace with
// their tab bar: the total and its period, then the tables, then only the
// notes that report something. A tool without a source, or without rows,
// says so.
func costsBody(tool costs.Tool, report costs.Statement, width int) string {
	body, _ := costsPage(tool, report, width, -1)
	return body
}

// costsPage is costsBody for the tab view when selected >= 0: the project and
// model rows it can open get a marker column, the selected one marked, and
// targets lists them in order.
func costsPage(
	tool costs.Tool,
	report costs.Statement,
	width, selected int,
) (string, []target) {
	var b strings.Builder
	switch {
	case tool.Source == nil:
		writeFaint(&b, width, 0, tool.Title+" costs are not implemented yet.")
		return b.String(), nil
	case report.Empty():
		writeFaint(&b, width, 0, emptyReport(report))
		return b.String(), nil
	}
	pal := toolPalette(tool.Name)
	opts := report.Options
	c := report.Coverage
	period := fmt.Sprintf("%s responses · %s → %s", commas(c.Responses), day(c.First), day(c.Last))
	writeText(
		&b,
		width,
		0,
		pal.accent.Bold(true).Render(money(report.Total.Cost))+"  "+faint.Render(period),
	)
	writeNotes(&b, width, 0, []string{
		tool.PriceNote,
		"updated " + localTime(c.LastIngestAt),
	})
	grand := report.GrandTotal
	label := fmt.Sprintf("total · %d %s%s", report.RowCount, report.By, plural(report.RowCount))
	total := report.Total
	mark := noMarks
	if selected >= 0 {
		mark = unmarked
	}
	base := reportSpec{
		grand: grand, tokens: opts.Tokens, cacheHeads: tool.CacheWrites, tool: tool,
		width: width, pal: pal, mark: mark,
	}
	var sections []reportSpec
	var targets []target
	add := func(heading, head, kind string, rows []costs.Row, total *costs.Row, label string) {
		section := base
		section.heading, section.head, section.rows, section.total, section.label = heading, head, rows, total, label
		if selected >= 0 && kind != "" {
			section.kind = kind
			if offset := selected - len(targets); offset >= 0 && offset < len(rows) {
				section.mark = offset
			}
			for _, row := range rows {
				targets = append(targets, target{kind, row.Name})
			}
		}
		sections = append(sections, section)
	}
	if opts.Detail && report.By == "project" {
		for _, block := range report.Detail {
			share := "-"
			if grand > 0 {
				share = fmt.Sprintf("%.1f%%", block.Project.Cost/grand*100)
			}
			add(bold.Render(shortPath(block.Project.Name))+"  "+
				costStyle.Render(money(block.Project.Cost))+"  "+
				faint.Render(fmt.Sprintf("%s · %s calls", share, commas(block.Project.Calls))),
				"model", "", block.Models, nil, "")
		}
		add("", "model (all projects)", "model", report.Models, &total, label)
	} else {
		kind := ""
		if report.By == "project" || report.By == "model" {
			kind = report.By
		}
		add("", report.By, kind, report.Rows, &total, label)
		if report.By != "model" {
			add("", "model", "model", report.Models, nil, "")
		}
	}
	if report.By != "account" && len(report.Accounts) > 1 {
		add("", "account", "", report.Accounts, nil, "")
	}
	writeSections(&b, width, sections)
	writeFooter(&b, tool, width, report)
	return b.String(), targets
}

// writeSections writes report tables with one bar width for all of them, so
// bars compare across tables: all the width the widest table leaves.
func writeSections(b *strings.Builder, width int, sections []reportSpec) {
	bar := width
	for _, section := range sections {
		spec := reportTable(section)
		need := tableWidth(2, spec) + 1 // the bar and the space after it
		if !section.tokens {
			// addShareBars pads every percentage to 6 cells, wider than a share
			// column whose widest cell is "share" or "68.5%".
			need += max(6-naturalWidths(spec.plain())[2], 0)
		}
		bar = min(bar, width-need)
	}
	for _, section := range sections {
		b.WriteString("\n")
		if section.heading != "" {
			writeText(b, width, 0, section.heading)
		}
		if bar >= minBar {
			section.bar = bar
		}
		writeTable(b, width, 2, reportTable(section))
	}
}

// focusBody is the page of one project or model: the way back, its total and
// share, then its cost by model (or project), by day and by session.
func focusBody(tool costs.Tool, focus costs.Focus, report costs.Statement, width int) string {
	var b strings.Builder
	pal := toolPalette(tool.Name)
	back := "projects"
	if report.By != "project" || focus.Kind == "model" {
		back = "report"
	}
	writeText(&b, width, 0, faint.Render("‹ "+back)+"   "+bold.Render(rowName(tool, focus.Name)))
	b.WriteString("\n")
	var notes []string
	if report.GrandTotal > 0 {
		notes = append(
			notes,
			fmt.Sprintf("%.1f%% of the total", focus.Total.Cost/report.GrandTotal*100),
		)
	}
	notes = append(notes, commas(focus.Total.Calls)+" calls")
	dated := slices.DeleteFunc(
		slices.Clone(focus.Days),
		func(d costs.Row) bool { return d.Name == "unknown" },
	)
	if n := len(dated); n > 0 {
		notes = append(notes, day(dated[n-1].Name)+" → "+day(dated[0].Name))
	}
	writeText(&b, width, 0, pal.accent.Bold(true).Render(money(focus.Total.Cost)))
	writeNotes(&b, width, 0, notes)
	parts := "model"
	if focus.Kind == "model" {
		parts = "project"
	}
	days := make([]costs.Row, len(focus.Days))
	for i, d := range focus.Days {
		d.Name = day(d.Name)
		days[i] = d
	}
	base := reportSpec{
		grand:      focus.Total.Cost,
		tokens:     report.Options.Tokens,
		cacheHeads: tool.CacheWrites,
		tool:       tool,
		width:      width,
		pal:        pal,
		mark:       noMarks,
	}
	partsSection, daysSection := base, base
	partsSection.head, partsSection.rows = parts, focus.Parts
	daysSection.head, daysSection.rows = "day", days
	writeSections(&b, width, []reportSpec{partsSection, daysSection})
	b.WriteString("\n")
	writeTable(&b, width, 2, sessionTable(tool, focus.Sessions, pal))
	return b.String()
}

// sessionTable lists a focus's sessions, costliest first.
func sessionTable(tool costs.Tool, sessions []costs.Session, pal palette) tableSpec {
	spec := tableSpec{Header: true, Cols: []column{
		{Head: "session"},
		{Head: "started", Clip: true},
		{Head: "length", Right: true, Drop: 2},
		{Head: "model", Drop: 3},
		{Head: "cost", Right: true},
		{Head: "calls", Right: true, Drop: 1},
	}}
	for i := range spec.Cols {
		spec.Cols[i].Head = pal.accent.Render(spec.Cols[i].Head)
	}
	for _, session := range sessions {
		id := session.ID
		if len(id) > 8 {
			id = id[:8]
		}
		started, length := "-", "-"
		if !session.Start.IsZero() {
			started = session.Start.Local().Format("Jan 2 3:04 PM")
			length = duration(session.End.Sub(session.Start))
		}
		spec.Rows = append(spec.Rows, []string{
			faint.Render(id), started, faint.Render(length), rowName(tool, session.Model),
			costStyle.Render(money(session.Cost)), faint.Render(commas(session.Calls)),
		})
	}
	return spec
}

// duration is a session length as "5d 7h", "3h 10m", "52m" or "<1m". A
// resumed session spans from its first response to its last.
func duration(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "<1m"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh %dm", int(d.Hours()), int(d.Minutes())%60)
	}
	return fmt.Sprintf("%dd %dh", int(d.Hours())/24, int(d.Hours())%24)
}

// writeNotes writes faint notes on one line, joined by a dot, when they fit,
// and one per line when they do not.
func writeNotes(b *strings.Builder, width, indent int, notes []string) {
	if joined := strings.Join(notes, " · "); len(notes) > 1 &&
		indent+lipgloss.Width(glyphs.text.Replace(joined)) <= width {
		notes = []string{joined}
	}
	for _, note := range notes {
		writeFaint(b, width, indent, note)
	}
}

// day is a ledger date as "Sep 30", with the year when it is not this year.
func day(date string) string {
	when, err := time.Parse(time.DateOnly, date)
	if err != nil {
		return date
	}
	if when.Year() != time.Now().Year() {
		return when.Format("Jan 2, 2006")
	}
	return when.Format("Jan 2")
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// reportSpec is what one table of a report is made from. total, when set, is
// the bold row under a rule, named label; heading, when set, prints above the
// table; bar, when set, is the width of the share bars.
type reportSpec struct {
	heading string
	bar     int
	kind    string // "project" or "model" when the tab view can open the rows
	mark    int    // the marked row, or unmarked or noMarks
	head    string
	rows    []costs.Row
	grand   float64
	tokens  bool
	// cacheHeads are the tool's cache-write column heads under --tokens.
	cacheHeads []string
	tool       costs.Tool // whose rows: how a model id names its tier
	total      *costs.Row
	label      string
	width      int
	pal        palette
}

// The mark of a reportSpec: noMarks prints no marker column (a pipe, or a
// table the tab view cannot open), unmarked a column with no row marked.
const (
	noMarks  = -2
	unmarked = -1
)

// reportTable lays one table out: the name, cost and number columns, and,
// where the terminal has room, a share bar that uses the spare width.
func reportTable(r reportSpec) tableSpec {
	// A path keeps its tail; an account keeps its email and, longer, its
	// subscription, which is all that tells one email's rows apart.
	name := column{Head: r.head, Clip: true, ClipLeft: true, Keep: 24}
	if r.head == "account" {
		name.ClipLeft, name.Keep = false, 40
	}
	spec := tableSpec{
		Header: true,
		Cols: []column{
			name,
			{Head: "cost", Right: true},
		},
	}
	if r.tokens {
		spec.Cols = append(spec.Cols,
			column{Head: "calls", Right: true, Drop: 1},
			column{Head: "input", Right: true},
			column{Head: "output", Right: true},
		)
		for i, head := range r.cacheHeads {
			spec.Cols = append(spec.Cols, column{Head: head, Right: true, Drop: 3 + i})
		}
		spec.Cols = append(spec.Cols, column{Head: "cache read", Right: true, Drop: 2})
	} else {
		spec.Cols = append(spec.Cols,
			column{Head: "share", Right: true, Drop: 2},
			column{Head: "calls", Right: true, Drop: 1},
			column{Head: "tokens", Right: true, Drop: 3},
			column{Head: "cached", Right: true, Drop: 4},
		)
	}
	// Only a model row is flagged: a project, account or total that includes
	// an unpriced model is otherwise priced, and the warning under the report
	// names the model.
	flagged := false
	for _, row := range r.rows {
		flagged = flagged || r.head == "model" && !row.Priced
	}
	if flagged {
		spec.Cols = append(spec.Cols, column{Drop: 5}) // the unpriced flag
	}
	for i, row := range r.rows {
		name := rowName(r.tool, row.Name)
		if row.Subscription != "" {
			name = row.Display()
		}
		if r.kind != "" && i == r.mark {
			name = r.pal.accent.Bold(true).Render(name)
		}
		spec.Rows = append(spec.Rows, reportCells(r, name, row, flagged, false))
	}
	if r.total != nil {
		total := *r.total
		total.Priced = true
		spec.Total = reportCells(r, r.label, total, flagged, true)
	}
	for i := range spec.Cols {
		spec.Cols[i].Head = r.pal.accent.Render(spec.Cols[i].Head)
	}
	if !r.tokens && r.bar > 0 && r.grand > 0 {
		addShareBars(&spec, r)
	}
	if r.mark != noMarks && r.kind != "" {
		addMarker(&spec, r)
	}
	return spec
}

// reportCells is one row's cells: name, cost, then the number columns.
func reportCells(r reportSpec, name string, row costs.Row, flagged, total bool) []string {
	cost := costStyle.Render(money(row.Cost))
	if total {
		cost = r.pal.accent.Render(money(row.Cost))
	}
	out := []string{name, cost}
	if r.tokens {
		out = append(out, commas(row.Calls), human(float64(row.Input)), human(float64(row.Output)))
		switch len(r.cacheHeads) {
		case 0: // a tool without cache-write columns
		case 1:
			out = append(out, human(float64(row.CacheWrite5m+row.CacheWrite1h)))
		default:
			out = append(out, human(float64(row.CacheWrite5m)), human(float64(row.CacheWrite1h)))
		}
		out = append(out, human(float64(row.CacheRead)))
	} else {
		share, cached := "-", "-"
		if r.grand > 0 {
			share = fmt.Sprintf("%.1f%%", row.Cost/r.grand*100)
		}
		if v, ok := row.CachedShare(); ok {
			cached = fmt.Sprintf("%.1f%%", v*100)
		}
		out = append(out, share, faint.Render(commas(row.Calls)),
			faint.Render(human(float64(row.Total()))), faint.Render(cached))
	}
	switch {
	case !flagged:
		return out
	case row.Priced:
		return append(out, "")
	}
	return append(out, yellow.Render("unpriced"))
}

// addShareBars puts a bar of each row's share of the total in front of its
// percentage, r.bar cells wide: the share column then uses the spare width.
func addShareBars(spec *tableSpec, r reportSpec) {
	share := 2 // name, cost, share
	for i, row := range spec.Rows {
		filled := min(int(r.rows[i].Cost/r.grand*float64(r.bar)+0.5), r.bar)
		spec.Rows[i][share] = r.pal.accent.Render(strings.Repeat("█", filled)) +
			faint.Render(strings.Repeat("░", r.bar-filled)) + " " + fmt.Sprintf("%6s", row[share])
	}
	// Every cell is now as wide as the bar and its percentage, so the head sits
	// over the bar and the total's percentage under the others.
	spec.Cols[share].Right = false
	if spec.Total != nil {
		spec.Total[share] = strings.Repeat(" ", r.bar+1) + fmt.Sprintf("%6s", spec.Total[share])
	}
}

// addMarker puts a column in front for the tab view's cursor: the selected
// row's marker, a space elsewhere, so stacked rows line up too.
func addMarker(spec *tableSpec, r reportSpec) {
	spec.Cols = slices.Insert(spec.Cols, 0, column{})
	for i, row := range spec.Rows {
		mark := " "
		if i == r.mark {
			mark = r.pal.accent.Bold(true).Render("›")
		}
		spec.Rows[i] = slices.Insert(row, 0, mark)
	}
	if spec.Total != nil {
		spec.Total = slices.Insert(spec.Total, 0, " ")
	}
}

// writeFooter writes only the notes that report something: the --tokens key,
// the --top cut and the projects the scope hid, on one line when they fit,
// then a warning for models without a rate.
func writeFooter(b *strings.Builder, tool costs.Tool, width int, report costs.Statement) {
	var notes []string
	if report.Options.Tokens {
		read := "cache read: prompt tokens served from it"
		note := "cache 5m / 1h: prompt tokens written to the cache with that lifetime; " + read
		switch len(tool.CacheWrites) {
		case 0: // a tool without cache-write columns
			note = "cache read: prompt tokens served from the cache"
		case 1:
			note = "cache write: prompt tokens written to the cache; " + read
		}
		notes = append(notes, note)
	}
	if report.RowCount > len(report.Rows) {
		notes = append(notes, fmt.Sprintf(
			"%d of %d %ss shown; totals cover all", len(report.Rows), report.RowCount, report.By))
	}
	if report.Hidden > 0 {
		notes = append(notes, fmt.Sprintf(
			"%d project%s outside ~/dev and ~/repos hidden (--all shows them)",
			report.Hidden,
			plural(report.Hidden),
		))
	}
	notes = append(notes, unrecordedNotes(tool, report)...)
	if len(notes) > 0 {
		b.WriteString("\n")
		writeNotes(b, width, 2, notes)
	}
	if len(report.Unpriced) > 0 {
		b.WriteString("\n")
		writeText(b, width, 0, yellow.Render("warning:")+
			" no rate for these models; their tokens count, their cost shows as 0:")
		for _, u := range report.Unpriced {
			writeText(b, width, 2, rowName(tool, u.Model)+" — "+u.Reason)
		}
		writeText(b, width, 0, fmt.Sprintf(
			"Add a rate for each to the rates overrides file (%s).", shortPath(report.Overrides),
		))
	}
}

// unrecordedNotes say, for an account table that shows "not recorded", since
// when the tool's transcripts record the account and the plan: no evidence for
// an earlier row exists anywhere on disk, so the report names none.
func unrecordedNotes(tool costs.Tool, report costs.Statement) []string {
	if report.By != "account" && len(report.Accounts) < 2 {
		return nil
	}
	var account, plan bool
	for _, row := range report.Accounts {
		account = account || row.Name == "" || row.Name == "unknown"
		plan = plan || row.Subscription == "unknown"
	}
	since := func(part, at string, missing int64) string {
		switch {
		case at == "":
			return tool.Title + "'s transcripts record no " + part
		case costs.LocalDate(at) <= report.Coverage.First:
			// Named from the first row on: the rows without one are threads
			// whose transcript never named it, not an earlier era.
			come := "responses come"
			if missing == 1 {
				come = "response comes"
			}
			return fmt.Sprintf("%s %s from %s conversations that never named the %s",
				commas(missing), come, tool.Title, part)
		}
		return fmt.Sprintf("%s records the %s only since %s; earlier usage has no record of it",
			tool.Title, part, localTime(at))
	}
	var notes []string
	c := report.Coverage
	if account {
		notes = append(notes, since("account", c.AccountNamedSince, c.AccountEvidence.Unknown))
	}
	if plan {
		notes = append(
			notes,
			since("plan", c.SubscriptionNamedSince, c.SubscriptionEvidence.Unknown),
		)
	}
	return notes
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
