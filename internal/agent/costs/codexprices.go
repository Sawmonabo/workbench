package costs

import (
	"os"
	"regexp"
	"slices"
	"strings"
)

// PricingURL is OpenAI's price page in Markdown; CODEX_COSTS_PRICING_URL
// replaces it for checks that must not reach the network.
func (codex) PricingURL() string {
	if url := os.Getenv("CODEX_COSTS_PRICING_URL"); url != "" {
		return url
	}
	return "https://developers.openai.com/api/docs/pricing.md"
}

// codexBuiltin is the OpenAI price page read on 2026-10-01, short-context
// columns: input, output, cache write, cache read per million tokens; a "-"
// price was filled with the input price. A tier row is "<model>@<tier>". The
// official card overrides these at runtime.
var codexBuiltin = map[string][4]float64{
	"gpt-6-astra": {10, 50, 12.5, 1}, "gpt-6.1-sol": {2, 10, 2.5, 0.1},
	"gpt-6-luna": {0.1, 0.5, 0.125, 0.01}, "gpt-6-sol": {2, 10, 2.5, 0.2},
	"gpt-5.6-sol": {4, 20, 5, 0.4}, "gpt-5.6-terra": {2, 12, 2.5, 0.2},
	"gpt-5.6-luna": {0.2, 1.2, 0.25, 0.02}, "gpt-5.5": {5, 30, 5, 0.5},
	"gpt-5.5-pro": {30, 180, 30, 30}, "gpt-5.4": {2.5, 15, 2.5, 0.25},
	"gpt-5.4-mini": {0.75, 4.5, 0.75, 0.075}, "gpt-5.4-nano": {0.2, 1.25, 0.2, 0.02},
	"gpt-5.4-pro": {30, 180, 30, 30}, "gpt-5.2": {1.75, 14, 1.75, 0.175},
	"gpt-5.2-pro": {21, 168, 21, 21}, "gpt-5.1": {1.25, 10, 1.25, 0.125},
	"gpt-5": {1.25, 10, 1.25, 0.125}, "gpt-5-mini": {0.25, 2, 0.25, 0.025},
	"gpt-5-nano": {0.05, 0.4, 0.05, 0.005}, "gpt-5-pro": {15, 120, 15, 15},
	"gpt-6-astra@flex": {5, 25, 6.25, 0.5}, "gpt-6.1-sol@flex": {1, 5, 1.25, 0.05},
	"gpt-6-luna@flex": {0.05, 0.25, 0.0625, 0.005}, "gpt-6-sol@flex": {1, 5, 1.25, 0.1},
	"gpt-5.6-sol@flex": {2, 10, 2.5, 0.2}, "gpt-5.6-terra@flex": {1, 6, 1.25, 0.1},
	"gpt-5.6-luna@flex": {0.1, 0.6, 0.125, 0.01}, "gpt-5.5@flex": {2.5, 15, 2.5, 0.25},
	"gpt-5.5-pro@flex": {15, 90, 15, 15}, "gpt-5.4@flex": {1.25, 7.5, 1.25, 0.13},
	"gpt-5.4-mini@flex": {0.375, 2.25, 0.375, 0.0375}, "gpt-5.4-nano@flex": {0.1, 0.625, 0.1, 0.01},
	"gpt-5.4-pro@flex": {15, 90, 15, 15}, "gpt-5.2@flex": {0.875, 7, 0.875, 0.0875},
	"gpt-5.1@flex": {0.625, 5, 0.625, 0.0625}, "gpt-5@flex": {0.625, 5, 0.625, 0.0625},
	"gpt-5-mini@flex": {0.125, 1, 0.125, 0.0125}, "gpt-5-nano@flex": {0.025, 0.2, 0.025, 0.0025},
	"gpt-6-astra@fast": {20, 100, 25, 2}, "gpt-6.1-sol@fast": {4, 20, 5, 0.2},
	"gpt-6-luna@fast": {0.2, 1, 0.25, 0.02}, "gpt-6-sol@fast": {4, 20, 5, 0.4},
	"gpt-5.6-sol@fast": {8, 40, 10, 0.8}, "gpt-5.6-terra@fast": {4, 24, 5, 0.4},
	"gpt-5.6-luna@fast": {0.4, 2.4, 0.5, 0.04}, "gpt-5.5@fast": {12.5, 75, 12.5, 1.25},
	"gpt-5.4@fast": {5, 30, 5, 0.5}, "gpt-5.4-mini@fast": {1.5, 9, 1.5, 0.15},
	"gpt-5.2@fast": {3.5, 28, 3.5, 0.35}, "gpt-5.1@fast": {2.5, 20, 2.5, 0.25},
	"gpt-5@fast": {2.5, 20, 2.5, 0.25}, "gpt-5-mini@fast": {0.45, 3.6, 0.45, 0.045},
	"gpt-6-astra@ultrafast": {60, 300, 75, 6},
}

func (codex) Builtin() RateCard {
	card := RateCard{}
	for prefix, v := range codexBuiltin {
		card[prefix] = Rate{
			Input: v[0], Output: v[1], CacheWrite5m: v[2], CacheWrite1h: v[2], CacheRead: v[3],
			Source: "builtin",
		}
	}
	return card
}

var codexModelPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9.\-]*$`)

// codexTable is the tier suffix of the price page table under heading: the
// tier a "<Tier> pricing data" heading names, "" for Standard, and ok false
// for any other heading and for Batch, which Codex does not use.
func codexTable(heading string) (suffix string, ok bool) {
	name, found := strings.CutSuffix(strings.ToLower(heading), " pricing data")
	if !found || name == "batch" || !isTier(name) {
		return "", false
	}
	if tier := codexTier(name); tier != "" {
		return "@" + tier, true
	}
	return "", true
}

// ParsePricing reads the short-context columns of every service tier's table:
// Standard, and each other "<Tier> pricing data" table into "<model>@<tier>"
// rows, such as Flex, Fast and Ultrafast. A "-" cached-input or
// cache-write price bills those tokens at the input price. The first row of a
// model in a table wins.
func (codex) ParsePricing(page string) RateCard {
	card := RateCard{}
	lines := strings.Split(page, "\n")
	suffix, inTable := "", false
	for i := 0; i < len(lines)-1; {
		head, sep := strings.TrimSpace(lines[i]), strings.TrimSpace(lines[i+1])
		if strings.HasPrefix(head, "#") {
			suffix, inTable = codexTable(strings.TrimSpace(strings.TrimLeft(head, "#")))
		}
		if !strings.HasPrefix(head, "|") || !strings.HasPrefix(sep, "|") ||
			strings.Trim(sep, "|-: ") != "" {
			i++
			continue
		}
		var headers []string
		for h := range strings.SplitSeq(strings.Trim(head, "|"), "|") {
			headers = append(headers, strings.ToLower(strings.TrimSpace(h)))
		}
		i += 2
		if !inTable {
			continue
		}
		column := func(name string) int { return slices.Index(headers, "short context "+name) }
		ci, co := column("input"), column("output")
		cc, cw := column("cached input"), column("cache writes")
		if ci < 0 || co < 0 {
			continue
		}
		for ; i < len(lines) && strings.HasPrefix(strings.TrimSpace(lines[i]), "|"); i++ {
			var cells []string
			for c := range strings.SplitSeq(strings.Trim(strings.TrimSpace(lines[i]), "|"), "|") {
				cells = append(cells, strings.TrimSpace(c))
			}
			model := strings.ToLower(strings.TrimSpace(notePattern.ReplaceAllString(cells[0], "")))
			if !codexModelPattern.MatchString(model) {
				continue
			}
			key := model + suffix
			if _, seen := card[key]; seen {
				continue
			}
			in, okIn := money(cells, ci)
			out, okOut := money(cells, co)
			if !okIn || !okOut {
				continue
			}
			rate := Rate{Input: in, Output: out, CacheWrite5m: in, CacheWrite1h: in, CacheRead: in}
			if v, ok := money(cells, cw); ok {
				rate.CacheWrite5m, rate.CacheWrite1h = v, v
			}
			if v, ok := money(cells, cc); ok {
				rate.CacheRead = v
			}
			card[key] = rate
		}
	}
	return card
}
