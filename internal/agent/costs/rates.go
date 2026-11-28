package costs

import (
	"context"
	"maps"
	"math"
	"slices"
	"sort"
	"strings"
	"time"
)

// Rate is USD per million tokens for each token kind.
type Rate struct {
	Input        float64 `json:"input"`
	Output       float64 `json:"output"`
	CacheWrite5m float64 `json:"cache_write_5m"`
	CacheWrite1h float64 `json:"cache_write_1h"`
	CacheRead    float64 `json:"cache_read"`
	Source       string  `json:"source"` // "override", "official", "calibrated" or "builtin"
}

// RateCard maps a model prefix to its rate.
type RateCard map[string]Rate

// sourceOrder ranks where a rate came from, for rates that are equally
// specific; see Lookup.
var sourceOrder = []string{"official", "calibrated", "builtin"}

// rateFields are the keys of a rate in an official price list (see
// officialFile), in the order of the ledger's token columns.
var rateFields = []string{"input", "output", "cache_write_5m", "cache_write_1h", "cache_read"}

func (r Rate) field(name string) float64 {
	return [...]float64{r.Input, r.Output, r.CacheWrite5m, r.CacheWrite1h, r.CacheRead}[slices.Index(rateFields, name)]
}

func (r *Rate) set(name string, v float64) {
	*[...]*float64{&r.Input, &r.Output, &r.CacheWrite5m, &r.CacheWrite1h, &r.CacheRead}[slices.Index(rateFields, name)] = v
}

// matchesRate reports whether the rate keyed prefix prices model: both carry
// the same service tier (none for standard), and the prefix is the whole model
// id or is followed by "-" or "@" and a digit (a version within a family, or a
// dated snapshot). So "claude-opus" prices "claude-opus-5-5" and "gpt-5"
// prices "gpt-5-2025-08-07", but "gpt-5" prices neither "gpt-5-mini" nor
// "gpt-5.3-codex-spark", and no standard rate prices a "@fast" model.
func matchesRate(model, prefix string, tiers bool) bool {
	modelBase, modelTier := model, ""
	prefixBase, prefixTier := prefix, ""
	if tiers {
		modelBase, modelTier = splitTier(model)
		prefixBase, prefixTier = splitTier(prefix)
	}
	if modelTier != prefixTier || !strings.HasPrefix(modelBase, prefixBase) {
		return false
	}
	rest := modelBase[len(prefixBase):]
	return rest == "" ||
		(len(rest) >= 2 && (rest[0] == '-' || rest[0] == '@') && rest[1] >= '0' && rest[1] <= '9')
}

// Lookup returns the rate that prices model, a model id of a tool whose ids
// carry a service tier when tiers is set. The row that names the model most
// specifically wins, wherever it comes from, so a page that drops
// "claude-opus-4-5" and keeps the family row "claude-opus-4" does not reprice
// the built-in "claude-opus-4-5" row away. Rows equally specific fall to the
// source order.
func (c RateCard) Lookup(model string, tiers bool) (Rate, bool) {
	var best Rate
	var bestRank [3]int
	found := false
	for prefix, rate := range c {
		if !matchesRate(model, prefix, tiers) {
			continue
		}
		if rank := rateRank(prefix, rate); !found || slices.Compare(rank[:], bestRank[:]) < 0 {
			best, bestRank, found = rate, rank, true
		}
	}
	return best, found
}

// rateRank orders the rates that match a model, best first: the most specific
// prefix, then the earlier source, then the longer prefix (so the order never
// depends on map iteration).
func rateRank(prefix string, rate Rate) [3]int {
	return [3]int{-specificity(prefix), slices.Index(sourceOrder, rate.Source), -len(prefix)}
}

// specificity is how much of a model id a prefix names: its length without a
// dated snapshot suffix ("-20251101" or "@20251101") and without a tier. Claude
// Code's own cost records, which calibration reads, are keyed by the dated id
// ("claude-opus-4-5-20251101"); that names the same model as the page's
// "claude-opus-4-5", so the two tie and the source order, official first,
// decides, as it always has.
func specificity(prefix string) int {
	base, _ := splitTier(prefix)
	n := len(base)
	if n > 9 && (base[n-9] == '-' || base[n-9] == '@') && isDigits(base[n-8:]) {
		n -= 9
	}
	return n
}

func isDigits(s string) bool {
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return s != ""
}

// Cost is the list-price cost of the tokens at this rate in picodollars
// (1e-12 USD). Each rate, in USD per million tokens, counts to the nearest
// millionth of a dollar, which is a whole number of picodollars a token, so
// the cost is exact and any sum of costs is the same in any order or
// grouping. An int64 holds up to about 9.2 million dollars.
func (r Rate) Cost(t Tokens) int64 {
	per := func(tokens int64, rate float64) int64 { return tokens * int64(math.Round(rate*1e6)) }
	return per(t.Input, r.Input) + per(t.Output, r.Output) +
		per(t.CacheWrite5m, r.CacheWrite5m) + per(t.CacheWrite1h, r.CacheWrite1h) +
		per(t.CacheRead, r.CacheRead)
}

// Card is the merged rate card of every recorded tool.
type Card struct {
	Rows       RateCard
	Official   map[string]RateCard  // per tool: the official rows the ledger keeps
	Calibrated RateCard             // per model, from the tools' own cost records
	Fetched    map[string]time.Time // per tool: when the official list was downloaded
	Ledger     bool                 // whether there was a ledger to read the official rows from
	Notes      []string             // outcomes of the refresh this call made
}

// Sources are the distinct sources the card's rows come from, sorted.
func (c Card) Sources() []string {
	seen := map[string]bool{}
	for _, rate := range c.Rows {
		seen[rate.Source] = true
	}
	out := make([]string, 0, len(seen))
	for source := range seen {
		out = append(out, source)
	}
	sort.Strings(out)
	return out
}

// Disagreements are the models whose calibrated and official input or output
// rates differ by more than five percent.
func (c Card) Disagreements() map[string]bool {
	out := map[string]bool{}
	for model, calibrated := range c.Calibrated {
		for name, official := range c.Official {
			tool, _ := Lookup(name)
			if o, ok := longestPrefix(model, official, tool.Tiers); ok &&
				(math.Abs(calibrated.Input-o.Input) > 0.05*math.Max(o.Input, 1e-9) ||
					math.Abs(calibrated.Output-o.Output) > 0.05*math.Max(o.Output, 1e-9)) {
				out[model] = true
			}
		}
	}
	return out
}

func longestPrefix(model string, card RateCard, tiers bool) (Rate, bool) {
	best, found := "", false
	for prefix := range card {
		if matchesRate(model, prefix, tiers) && (!found || len(prefix) > len(best)) {
			best, found = prefix, true
		}
	}
	return card[best], found
}

// Rates returns the merged card: built-in, then calibration, then the official
// price lists the ledger keeps. It opens the ledger as a report does (see
// [Paths.Open], which steps is as it takes) but never makes one (see
// [Paths.OpenExisting]): without a ledger the card holds the built-in and
// calibrated rows only, and refresh downloads nothing. With one, refresh
// downloads every official list first and reports each outcome in Notes.
func Rates(ctx context.Context, refresh bool, steps UpgradeSteps) (Card, error) {
	paths, err := Locations()
	if err != nil {
		return Card{}, err
	}
	ledger, err := paths.openAgain(ctx, false, steps)
	if err != nil {
		return Card{}, err
	}
	var notes []string
	switch {
	case ledger != nil:
		defer func() { _ = ledger.Close() }()
	case refresh:
		notes = append(notes, "rates: there is no ledger yet, so nothing was downloaded")
	}
	for _, tool := range Tools {
		if refresh && ledger != nil && tool.Source != nil && tool.Source.PricingURL() != "" {
			notes = append(notes, refreshOfficial(ctx, ledger, tool, true))
		}
	}
	card, err := loadCard(ctx, paths.Home, ledger)
	card.Notes = notes
	return card, err
}

// loadCard builds the merged card from the built-in rows, the rows calibrated
// from the tools' own cost records in home, and the official rows ledger
// keeps, none when ledger is nil.
func loadCard(ctx context.Context, home string, ledger *Ledger) (Card, error) {
	card := Card{
		Rows:     RateCard{},
		Official: map[string]RateCard{},
		Fetched:  map[string]time.Time{},
		Ledger:   ledger != nil,
	}
	for _, tool := range Tools {
		if tool.Source == nil {
			continue
		}
		maps.Copy(card.Rows, tool.Source.Builtin())
	}
	card.Calibrated = calibrated(home)
	maps.Copy(card.Rows, card.Calibrated)
	if ledger == nil {
		return card, nil
	}
	for _, tool := range Tools {
		if tool.Source == nil {
			continue
		}
		official, err := ledger.officialRates(ctx, tool.Name)
		if err != nil {
			return card, err
		}
		card.Official[tool.Name] = official.rates()
		if official.FetchedAt > 0 {
			card.Fetched[tool.Name] = fromEpoch(official.FetchedAt)
		}
		maps.Copy(card.Rows, card.Official[tool.Name])
	}
	return card, nil
}

// Unpriced is a model no rate prices, and why. Its tokens count; its cost
// counts as 0 until an official price list prices it.
type Unpriced struct {
	Model  string `json:"model"`
	Reason string `json:"reason"`
}

// unpriced is why no rate prices model, one of tool's: the transcript named
// no model, the price page has the model but not at its tier, or the page
// does not have it at all.
func (c Card) unpriced(tool Tool, model string) Unpriced {
	base, tier := tool.SplitModel(model)
	reason := "not on " + tool.PricePage
	switch _, standard := c.Rows.Lookup(base, tool.Tiers); {
	case base == "unknown":
		reason = "the transcript names no model"
	case tier != "" && standard:
		reason = "no " + tier + " price on " + tool.PricePage
	}
	return Unpriced{Model: model, Reason: reason}
}

// unpricedModels are the tool's ledger models no source prices. A read of the
// ledger that fails is its error, never a shorter list.
func unpricedModels(ctx context.Context, ledger *Ledger, card Card, tool Tool) ([]Unpriced, error) {
	// The tool's rows through the index of (tool, root), which read faster
	// than the whole table for one tool.
	rows, err := ledger.db.QueryContext(ctx, `SELECT n.name FROM
	  (SELECT DISTINCT model FROM responses
	    WHERE tool = (SELECT id FROM names WHERE kind = ?1 AND name = ?2)) m
	  JOIN names n ON n.id = m.model`,
		nameTool, tool.Name)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []Unpriced
	for rows.Next() {
		var model string
		if err := rows.Scan(&model); err != nil {
			return nil, err
		}
		if _, ok := card.Rows.Lookup(model, tool.Tiers); !ok {
			out = append(out, card.unpriced(tool, model))
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Model < out[j].Model })
	return out, nil
}
