package costs

import (
	"regexp"
	"strconv"
	"strings"
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

// markdownTable is one Markdown table of a price page: the last heading above
// it, in lower case without its #s, its header cells in lower case, and the
// cells of its rows.
type markdownTable struct {
	heading string
	headers []string
	rows    [][]string
}

// markdownTables is every table of page, in order: a header row, a separator
// row of |, - and : only, and every line after them that starts with |.
func markdownTables(page string) []markdownTable {
	var tables []markdownTable
	lines := strings.Split(page, "\n")
	heading := ""
	for i := 0; i < len(lines)-1; {
		head, sep := strings.TrimSpace(lines[i]), strings.TrimSpace(lines[i+1])
		if strings.HasPrefix(head, "#") {
			heading = strings.ToLower(strings.TrimSpace(strings.TrimLeft(head, "#")))
		}
		if !strings.HasPrefix(head, "|") || !strings.HasPrefix(sep, "|") ||
			strings.Trim(sep, "|-: ") != "" {
			i++
			continue
		}
		table := markdownTable{heading: heading, headers: cellsOf(head)}
		for j, header := range table.headers {
			table.headers[j] = strings.ToLower(header)
		}
		for i += 2; i < len(lines) && strings.HasPrefix(strings.TrimSpace(lines[i]), "|"); i++ {
			table.rows = append(table.rows, cellsOf(lines[i]))
		}
		tables = append(tables, table)
	}
	return tables
}

// cellsOf is the cells of a table row, each trimmed.
func cellsOf(line string) []string {
	var cells []string
	for cell := range strings.SplitSeq(strings.Trim(strings.TrimSpace(line), "|"), "|") {
		cells = append(cells, strings.TrimSpace(cell))
	}
	return cells
}
