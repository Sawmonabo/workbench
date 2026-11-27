package costs

import (
	"regexp"
	"strconv"
)

var (
	moneyPattern  = regexp.MustCompile(`\$\s*([0-9]+(?:\.[0-9]+)?)`)
	markupPattern = regexp.MustCompile(
		"[`*_]|<[^>]*>|\\[|\\]\\([^)]*\\)",
	) // emphasis, HTML tags, link targets
	notePattern = regexp.MustCompile(
		`\s*\(.*$`,
	) // a trailing note such as "(retired)"
	spacePattern = regexp.MustCompile(`[.\s]+`)
)

// money reads the dollar amount of cell j.
func money(cells []string, j int) (float64, bool) {
	if j < 0 || j >= len(cells) {
		return 0, false
	}
	m := moneyPattern.FindStringSubmatch(cells[j])
	if m == nil {
		return 0, false
	}
	v, err := strconv.ParseFloat(m[1], 64)
	return v, err == nil
}
