package cli

import (
	"io"
	"os"
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"
	"charm.land/lipgloss/v2/table"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/term"
)

// column describes one table column. When the terminal is too narrow the one
// Clip column first shortens to Keep cells (when Keep > 0), then columns with
// Drop > 0 are removed, highest Drop first, then the Clip column shortens
// further. It
// clips with an ellipsis: from the left for paths (ClipLeft), so the part that
// tells them apart survives, otherwise from the right. Faint columns are
// secondary detail and print dimmed.
type column struct {
	Head     string
	Right    bool
	Drop     int
	Clip     bool
	ClipLeft bool
	Keep     int
	Faint    bool
}

// tableSpec is one table: its columns, its rows, whether a bold, underlined
// head line comes first, and an optional bold total row set off by a rule.
type tableSpec struct {
	Cols   []column
	Rows   [][]string
	Header bool
	Total  []string
}

// glyphSet is what output draws with: Unicode when the locale is UTF-8,
// ASCII otherwise, so a terminal that cannot draw ─ … → · − ✓ ✗ never shows
// mojibake.
type glyphSet struct {
	Rule, Ellipsis, Check, Cross string
	border                       lipgloss.Border
	text                         *strings.Replacer // applied to every cell and prose line
}

// glyphs is chosen once, from the first of LC_ALL, LC_CTYPE and LANG that is
// set, the order the C library uses.
var glyphs = chooseGlyphs(os.Getenv)

func chooseGlyphs(getenv func(string) string) glyphSet {
	locale := ""
	for _, name := range []string{"LC_ALL", "LC_CTYPE", "LANG"} {
		if locale = getenv(name); locale != "" {
			break
		}
	}
	locale = strings.ToLower(locale)
	if strings.Contains(locale, "utf-8") || strings.Contains(locale, "utf8") {
		return glyphSet{
			Rule:     "─",
			Ellipsis: "…",
			Check:    "✓",
			Cross:    "✗",
			border:   lipgloss.NormalBorder(),
			text:     strings.NewReplacer(),
		}
	}
	return glyphSet{
		Rule:     "-",
		Ellipsis: "...",
		Check:    "+",
		Cross:    "x",
		border:   lipgloss.ASCIIBorder(),
		text:     asciiText,
	}
}

// asciiText swaps each non-ASCII character Workbench and its probes print for
// an ASCII stand-in.
var asciiText = strings.NewReplacer(
	"→", "->",
	"—", "-",
	"·", "-",
	"−", "-",
	"…", "...",
	"─", "-",
	"✓", "+",
	"✗", "x",
	"█", "#",
	"←", "<-",
	"↑", "up",
	"↓", "down",
	"░", "-",
)

// plain rewrites every cell for the terminal's glyphs before anything is
// measured, since an ASCII stand-in can be wider than what it replaces.
func (t tableSpec) plain() tableSpec {
	convert := func(row []string) []string {
		if row == nil {
			return nil
		}
		out := make([]string, len(row))
		for i, cell := range row {
			out[i] = glyphs.text.Replace(cell)
		}
		return out
	}
	rows := make([][]string, len(t.Rows))
	for i, row := range t.Rows {
		rows[i] = convert(row)
	}
	t.Rows, t.Total = rows, convert(t.Total)
	return t
}

// minClip is the narrowest a clipped column gets before rows stack.
const minClip = 12

// gap is the space between columns.
const gap = 2

// terminalWidth is the width output must fit: the terminal's, else COLUMNS,
// else 100 when the output is not a terminal.
func terminalWidth(w io.Writer) int {
	if file, ok := w.(*os.File); ok {
		if width, _, err := term.GetSize(file.Fd()); err == nil && width > 0 {
			return width
		}
	}
	if columns, err := strconv.Atoi(os.Getenv("COLUMNS")); err == nil && columns > 0 {
		return columns
	}
	return 100
}

// fitRows lays t out so that no line, indent included, is wider than width.
// lipgloss renders whatever fits; below minClip each row is a stacked block.
func fitRows(width, indent int, t tableSpec) []string {
	t = t.plain()
	active, widths, stack := layout(width, indent, t)
	if stack {
		return stacked(width, indent, t, active, false)
	}
	return renderTable(indent, t, active, widths)
}

// fitBlocks is fitRows split by row, for a caller that must know which lines
// belong to which row (the checklist's cursor). t has no Header and no Total.
func fitBlocks(width, indent int, t tableSpec) [][]string {
	t = t.plain()
	active, widths, stack := layout(width, indent, t)
	blocks := make([][]string, len(t.Rows))
	if !stack {
		for i, line := range renderTable(indent, t, active, widths) {
			blocks[i] = []string{line}
		}
		return blocks
	}
	for i, row := range t.Rows {
		one := tableSpec{Cols: t.Cols, Rows: [][]string{row}}
		blocks[i] = stacked(width, indent, one, active, false)
	}
	return blocks
}

// layout decides which columns survive and how wide each is: it shortens the
// Clip column to its Keep width, drops columns by priority, then shortens the
// Clip column further. stack reports that even minClip does not fit.
func layout(width, indent int, t tableSpec) (active, widths []int, stack bool) {
	cols := t.Cols
	active = make([]int, len(cols))
	clip := -1
	for i, c := range cols {
		active[i] = i
		if c.Clip {
			clip = i
		}
	}
	widths = naturalWidths(t)
	total := func() int {
		sum := indent + gap*max(len(active)-1, 0)
		for _, i := range active {
			sum += widths[i]
		}
		return sum
	}
	if over := total() - width; over > 0 && clip >= 0 && cols[clip].Keep > 0 {
		widths[clip] -= min(over, max(widths[clip]-max(cols[clip].Keep, minClip), 0))
	}
	for total() > width {
		drop, highest := -1, 0
		for n, i := range active {
			if cols[i].Drop > highest {
				drop, highest = n, cols[i].Drop
			}
		}
		if drop < 0 {
			break
		}
		active = append(active[:drop], active[drop+1:]...)
	}
	if over := total() - width; over > 0 {
		if clip < 0 || widths[clip]-over < minClip {
			return active, widths, true
		}
		widths[clip] -= over
	}
	return active, widths, false
}

// writeTable writes fitRows' lines to b.
//
// The indent parameter is part of the shared table interface: every caller in
// this package indents 4 today; other callers pick their own.
//
//nolint:unparam // See the comment above.
func writeTable(b *strings.Builder, width, indent int, t tableSpec) {
	for _, line := range fitRows(width, indent, t) {
		b.WriteString(line + "\n")
	}
}

// writeFull writes every row as a stacked block with each value wrapped, not
// clipped, so nothing is lost and nothing is wider than width (--verbose).
func writeFull(b *strings.Builder, width, indent int, t tableSpec) {
	t = t.plain()
	active := make([]int, len(t.Cols))
	for i := range active {
		active[i] = i
	}
	for _, line := range stacked(width, indent, t, active, true) {
		b.WriteString(line + "\n")
	}
}

// writeText writes prose word-wrapped to width under indent; a word longer
// than the line is broken.
func writeText(b *strings.Builder, width, indent int, text string) {
	pad := strings.Repeat(" ", indent)
	text = glyphs.text.Replace(text)
	for line := range strings.SplitSeq(ansi.Wrap(text, max(width-indent, 1), ""), "\n") {
		b.WriteString(strings.TrimRight(pad+line, " ") + "\n")
	}
}

// tableWidth is how wide t prints, indent included, with every column at its
// natural width.
func tableWidth(indent int, t tableSpec) int {
	widths := naturalWidths(t.plain())
	sum := indent + gap*max(len(widths)-1, 0)
	for _, w := range widths {
		sum += w
	}
	return sum
}

// naturalWidths is each column's widest cell in display cells.
func naturalWidths(t tableSpec) []int {
	widths := make([]int, len(t.Cols))
	measure := func(row []string) {
		for i := range t.Cols {
			if i < len(row) {
				widths[i] = max(widths[i], ansi.StringWidth(row[i]))
			}
		}
	}
	if t.Header {
		for i, c := range t.Cols {
			widths[i] = ansi.StringWidth(c.Head)
		}
	}
	for _, row := range t.Rows {
		measure(row)
	}
	measure(t.Total)
	return widths
}

// renderTable hands the surviving columns, clipped to widths, to lipgloss. Cells
// are clipped first so lipgloss's natural width is the fitted width; the
// table's own Width is never set because it would spread narrow tables.
func renderTable(indent int, t tableSpec, active, widths []int) []string {
	// Faint cells are styled here, not in StyleFunc, so an empty one adds no
	// escape codes and its line still ends without spaces once colors strip.
	// Only data rows clip from the left: a head or total label is not a path.
	cells := func(row []string, head, total bool) []string {
		out := make([]string, len(active))
		for n, i := range active {
			if i < len(row) && row[i] != "" {
				out[n] = fit(row[i], widths[i], t.Cols[i].ClipLeft && !head && !total)
				if t.Cols[i].Faint && !head {
					out[n] = faint.Render(out[n])
				}
			}
		}
		return out
	}
	totalRow := -1
	tbl := table.New().
		Border(glyphs.border).BorderStyle(faint).
		BorderTop(false).BorderBottom(false).BorderLeft(false).BorderRight(false).
		BorderColumn(false).BorderHeader(t.Header).
		StyleFunc(func(row, col int) lipgloss.Style {
			c := t.Cols[active[col]]
			style := lipgloss.NewStyle()
			if col < len(active)-1 {
				style = style.PaddingRight(gap)
			}
			if c.Right {
				style = style.Align(lipgloss.Right)
			}
			if row == table.HeaderRow || row == totalRow {
				style = style.Bold(true)
			}
			return style
		})
	if t.Header {
		heads := make([]string, len(t.Cols))
		for i, c := range t.Cols {
			heads[i] = c.Head
		}
		tbl = tbl.Headers(cells(heads, true, false)...)
	}
	for _, row := range t.Rows {
		tbl = tbl.Row(cells(row, false, false)...)
	}
	if t.Total != nil {
		totalRow = len(t.Rows)
		tbl = tbl.Row(cells(t.Total, false, true)...)
	}
	if !t.Header && len(t.Rows) == 0 && t.Total == nil {
		return nil
	}
	pad := strings.Repeat(" ", indent)
	var lines []string
	for line := range strings.SplitSeq(tbl.Render(), "\n") {
		lines = append(lines, pad+trimEnd(line))
	}
	if t.Total != nil {
		// The rule spans the table and sits above the total row.
		widest := 0
		for _, line := range lines {
			widest = max(widest, ansi.StringWidth(line)-indent)
		}
		rule := pad + faint.Render(strings.Repeat(glyphs.Rule, widest))
		lines = append(lines[:len(lines)-1], rule, lines[len(lines)-1])
	}
	return lines
}

// trimEnd drops the spaces that end a line even when escape codes follow
// them, as they do after an empty bold cell (a head or total row's empty last
// column), so the line still ends without spaces once colors strip.
func trimEnd(line string) string {
	visible := ansi.Strip(line)
	if trimmed := strings.TrimRight(visible, " "); trimmed != visible {
		return ansi.Truncate(line, ansi.StringWidth(trimmed), "")
	}
	return line
}

// stacked prints each row as a block: a first line joining the cells before
// the Clip column (or the Clip cell when it comes first), then one indented
// "head value" line per other cell. Values clip to width, or with wrap they
// wrap onto further lines.
func stacked(width, indent int, t tableSpec, active []int, wrap bool) []string {
	pad := strings.Repeat(" ", indent)
	rows := t.Rows
	if t.Total != nil {
		rows = append(rows[:len(rows):len(rows)], t.Total)
	}
	lead := 1
	for n, i := range active {
		if t.Cols[i].Clip {
			lead = max(n, 1)
			break
		}
	}
	var lines []string
	add := func(prefix, text string, left bool) {
		room := max(width-ansi.StringWidth(prefix), 1)
		if !wrap {
			lines = append(lines, prefix+fit(text, room, left))
			return
		}
		for part := range strings.SplitSeq(ansi.Wrap(text, room, ""), "\n") {
			lines = append(lines, prefix+part)
		}
	}
	for r, row := range rows {
		label := t.Total != nil && r == len(rows)-1 // the total's label is not a path
		cell := func(i int) string {
			if i < len(row) {
				return row[i]
			}
			return ""
		}
		var first []string
		for _, i := range active[:min(lead, len(active))] {
			if text := cell(i); text != "" {
				first = append(first, text)
			}
		}
		add(pad, strings.Join(first, "  "), lead == 1 && t.Cols[active[0]].ClipLeft && !label)
		for _, i := range active[min(lead, len(active)):] {
			text := cell(i)
			if text == "" {
				continue
			}
			if head := t.Cols[i].Head; head != "" {
				text = faint.Render(head) + " " + text
			}
			// A path clips from the left only when no head label precedes it.
			add(pad+"  ", text, t.Cols[i].ClipLeft && t.Cols[i].Head == "" && !label)
		}
	}
	return lines
}

// fit shortens s to width display cells with the ellipsis glyph, from the
// left or the right.
func fit(s string, width int, left bool) string {
	if ansi.StringWidth(s) <= width {
		return s
	}
	if left {
		cut := ansi.StringWidth(s) - width + ansi.StringWidth(glyphs.Ellipsis)
		return ansi.TruncateLeft(s, cut, glyphs.Ellipsis)
	}
	return ansi.Truncate(s, width, glyphs.Ellipsis)
}
