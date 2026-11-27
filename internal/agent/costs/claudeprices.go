package costs

import (
	"os"
	"strings"
)

// PricingURL is the official price page; CLAUDE_COSTS_PRICING_URL replaces it
// for checks that must not reach the network.
func (claude) PricingURL() string {
	if url := os.Getenv("CLAUDE_COSTS_PRICING_URL"); url != "" {
		return url
	}
	return "https://platform.claude.com/docs/en/about-claude/pricing.md"
}

// builtin is the list prices read from the pricing page on 2026-09-30:
// input, output, 5m write, 1h write, cache read per million tokens. Longest
// prefix wins, so a family row is the fallback for ids no specific row
// covers. The official card and calibration override these at runtime. A
// "@fast" row, read from the page on 2026-10-04, is the model's fast-mode
// price (the page's fast mode table, with the caching multipliers applied on
// top of it); only the models the page lists for fast mode have one.
var builtin = map[string][5]float64{
	"claude-fable-5-1":  {10, 50, 12.5, 20, 0.25},
	"claude-fable-5":    {10, 50, 12.5, 20, 1},
	"claude-fable":      {10, 50, 12.5, 20, 1},
	"claude-mythos-5-1": {10, 50, 12.5, 20, 0.25},
	"claude-mythos-5":   {10, 50, 12.5, 20, 1},
	"claude-opus-5-5":   {4, 20, 5, 8, 0.2},
	"claude-opus-5":     {5, 25, 6.25, 10, 0.5},
	"claude-opus-4-8":   {5, 25, 6.25, 10, 0.5},
	"claude-opus-4-7":   {5, 25, 6.25, 10, 0.5},
	"claude-opus-4-6":   {5, 25, 6.25, 10, 0.5},
	"claude-opus-4-5":   {5, 25, 6.25, 10, 0.5},
	"claude-opus-4-1":   {15, 75, 18.75, 30, 1.5},
	"claude-opus-4":     {15, 75, 18.75, 30, 1.5},
	"claude-opus":       {5, 25, 6.25, 10, 0.5},
	"claude-sonnet-5-5": {2, 10, 2.5, 4, 0.2},
	"claude-sonnet-5":   {2, 10, 2.5, 4, 0.2},
	"claude-sonnet-4-6": {3, 15, 3.75, 6, 0.3},
	"claude-sonnet-4-5": {3, 15, 3.75, 6, 0.3},
	"claude-sonnet-4":   {3, 15, 3.75, 6, 0.3},
	"claude-sonnet":     {3, 15, 3.75, 6, 0.3},
	"claude-haiku-4-5":  {1, 5, 1.25, 2, 0.1},
	"claude-haiku-3-5":  {0.8, 4, 1, 1.6, 0.08},
	"claude-haiku":      {1, 5, 1.25, 2, 0.1},

	"claude-opus-5-5@fast": {8, 40, 10, 16, 0.4},
	"claude-opus-5@fast":   {10, 50, 12.5, 20, 1},
	"claude-opus-4-8@fast": {10, 50, 12.5, 20, 1},
}

func (claude) Builtin() RateCard {
	card := RateCard{}
	for prefix, v := range builtin {
		card[prefix] = Rate{v[0], v[1], v[2], v[3], v[4], "builtin"}
	}
	return card
}

// normalizeModel turns 'Claude Opus 5.5' into 'claude-opus-5-5'; an id is
// returned as is. A cell that names several models ('/', ',', ' and ') or none
// gives "".
func normalizeModel(name string) string {
	s := strings.ToLower(
		strings.TrimSpace(
			notePattern.ReplaceAllString(markupPattern.ReplaceAllString(name, ""), ""),
		),
	)
	if !strings.HasPrefix(s, "claude") || strings.ContainsAny(s, "/,") ||
		strings.Contains(s, " and ") {
		return ""
	}
	if !strings.Contains(s, " ") {
		if strings.HasPrefix(s, "claude-") {
			return s
		}
		return ""
	}
	s = strings.Trim(spacePattern.ReplaceAllString(strings.TrimSpace(s[len("claude"):]), "-"), "-")
	if s == "" {
		return ""
	}
	return "claude-" + s
}

func findColumn(headers []string, keys []string, exclude ...string) int {
	for j, h := range headers {
		if containsAny(h, keys) && !containsAny(h, exclude) {
			return j
		}
	}
	return -1
}

func containsAny(s string, parts []string) bool {
	for _, p := range parts {
		if strings.Contains(s, p) {
			return true
		}
	}
	return false
}

// pricedModels are the model ids the first cell of a price table row prices.
// The fast mode table names two models that share a price in one cell ("Claude
// Opus 5 / Claude Opus 4.8"), so there each part counts; anywhere else a cell
// that names several models prices none. A cell with a part that names no
// model prices none either.
func pricedModels(cell string, shared bool) []string {
	if !shared {
		if model := normalizeModel(cell); model != "" {
			return []string{model}
		}
		return nil
	}
	var models []string
	for part := range strings.SplitSeq(markupPattern.ReplaceAllString(cell, ""), "/") {
		model := normalizeModel(part)
		if model == "" {
			return nil
		}
		models = append(models, model)
	}
	return models
}

// ParsePricing reads every Markdown table with an input and an output column.
// The first table that prices a model wins. A table under a batch heading is
// skipped: the page repeats model names there at discounted rates. A table
// under a fast-mode heading prices "<model>@fast", at the page's input and
// output rates and the caching multipliers of the model's standard row applied
// on top, as the page says they are (Opus 5.5 reads its cache at 0.05x, the
// others at 0.1x).
func (claude) ParsePricing(page string) RateCard {
	card := RateCard{}
	for _, table := range markdownTables(page) {
		columns := priceColumnsOf(table.headers)
		if columns.input < 0 || columns.output < 0 || strings.Contains(table.heading, "batch") {
			continue
		}
		tier := ""
		if strings.Contains(table.heading, fastSpeed) {
			tier = "@" + fastSpeed
		}
		for _, cells := range table.rows {
			addPriceRow(card, cells, columns, tier)
		}
	}
	return card
}

// priceColumns are the indexes of a price table's columns, -1 for a missing one.
type priceColumns struct{ input, output, write5m, write1h, read int }

// priceColumnsOf maps a table's header cells, in lower case, to its columns
// by their text.
func priceColumnsOf(headers []string) priceColumns {
	columns := priceColumns{
		input:   findColumn(headers, []string{"input"}, "cache", "batch", "additional"),
		output:  findColumn(headers, []string{"output"}, "batch"),
		write5m: findColumn(headers, []string{"5m", "5-minute"}, "1h"),
		write1h: findColumn(headers, []string{"1h", "1-hour"}),
		read:    findColumn(headers, []string{"hit", "read", "refresh"}),
	}
	if columns.write5m < 0 {
		columns.write5m = findColumn(headers, []string{"write"}, "1h")
	}
	return columns
}

// addPriceRow adds the models a table row prices to card, at tier ("" for the
// standard table, "@fast" for the fast mode table). The first row that prices a
// model wins.
func addPriceRow(card RateCard, cells []string, columns priceColumns, tier string) {
	in, okIn := money(cells, columns.input)
	out, okOut := money(cells, columns.output)
	if !okIn || !okOut {
		return
	}
	write5m, ok5m := money(cells, columns.write5m)
	write1h, ok1h := money(cells, columns.write1h)
	read, okRead := money(cells, columns.read)
	for _, model := range pricedModels(cells[0], tier != "") {
		if _, seen := card[model+tier]; seen {
			continue
		}
		// A column the table lacks is the input rate times the model's
		// multiplier: the page's 1.25x, 2x and 0.1x, or, for a fast price,
		// those of the standard row it applies on top of.
		multiplier := [3]float64{1.25, 2, 0.1}
		if standard, ok := card[model]; tier != "" && ok && standard.Input > 0 {
			multiplier = [3]float64{
				standard.CacheWrite5m / standard.Input,
				standard.CacheWrite1h / standard.Input,
				standard.CacheRead / standard.Input,
			}
		}
		rate := Rate{
			Input:        in,
			Output:       out,
			CacheWrite5m: in * multiplier[0],
			CacheWrite1h: in * multiplier[1],
			CacheRead:    in * multiplier[2],
		}
		if ok5m {
			rate.CacheWrite5m = write5m
		}
		if ok1h {
			rate.CacheWrite1h = write1h
		}
		if okRead {
			rate.CacheRead = read
		}
		card[model+tier] = rate
	}
}
