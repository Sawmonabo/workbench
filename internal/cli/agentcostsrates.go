package cli

import (
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/Sawmonabo/workbench/internal/agent/costs"
	"github.com/Sawmonabo/workbench/internal/operation"
)

// ratesView is the merged card as --json prints it. Ledger is whether there
// was a ledger to read the official prices from.
type ratesView struct {
	Ledger   bool              `json:"ledger"`
	Official map[string]string `json:"official_fetched"`
	Rows     []rateRow         `json:"rows"`
}

// rateRow is one model prefix's rate as --json prints it, with whether the
// rate calibrated from the tools' own cost records disagrees with it.
type rateRow struct {
	Model string `json:"model"`
	costs.Rate
	Disagrees bool `json:"calibrated_disagrees,omitempty"`
}

func agentCostsRatesCommand(o *options) *cobra.Command {
	var refresh bool
	cmd := &cobra.Command{
		Use:   "rates",
		Short: "Show the merged rate card and where each rate comes from",
		Long: "Show the USD price per million tokens of each model prefix and which source it " +
			"comes from: the official price pages, whose prices the ledger keeps, the rates " +
			"calibrated from the tools' own cost records, or the built-in card. --refresh fetches " +
			"the official pages again first. Without a ledger it shows the built-in and calibrated " +
			"rates, and --refresh fetches nothing.",
		Example: "  workbench agent costs rates\n" +
			"  workbench agent costs rates --refresh",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return o.runPrinted(cmd, func(result *operation.Result) error {
				upgrade := &upgradeLine{o: o, diagnostics: cmd.ErrOrStderr()}
				card, err := costs.Rates(cmd.Context(), refresh, upgrade.step)
				upgrade.close()
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
				return printCosts(out, costsRates(card, view, lineWidth(out)))
			})
		},
	}
	cmd.Flags().BoolVar(&refresh, "refresh", false, "Fetch the official price pages again first")
	return cmd
}

func ratesCard(card costs.Card) ratesView {
	view := ratesView{Ledger: card.Ledger, Official: map[string]string{}}
	for tool, fetched := range card.Fetched {
		view.Official[tool] = fetched.Local().Format(time.DateOnly)
	}
	disagree := card.Disagreements()
	for _, model := range slices.Sorted(maps.Keys(card.Rows)) {
		view.Rows = append(view.Rows, rateRow{model, card.Rows[model], disagree[model]})
	}
	return view
}

func costsRates(card costs.Card, view ratesView, width int) string {
	var b strings.Builder
	writeBold(&b, width, 0, brandTag+" Costs rates")
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
	note := "The longest matching prefix wins, then this source order: official, calibrated, built-in."
	if card.Ledger {
		note += " Refresh: workbench agent costs rates --refresh"
	} else {
		note = "There is no ledger yet, which keeps the official prices, so --refresh fetches " +
			"nothing until the first import makes it. " + note
	}
	writeFaint(&b, width, 2, note)
	return b.String()
}
