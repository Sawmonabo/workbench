package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/Sawmonabo/workbench/internal/operation"
	"github.com/Sawmonabo/workbench/internal/project"
)

// writePlan prints a plan for review: where it comes from, the files it
// changes grouped by description, and its effects grouped by the privilege
// they need and what recovery can undo, then prerequisites, warnings and
// recovery limits. --json prints the complete plan instead.
func writePlan(w io.Writer, plan operation.Plan) error {
	var b strings.Builder
	width := terminalWidth(w)
	title := "[WorkBench] Plan for " + plan.Scope.Kind + " " + homePath(plan.Scope.Root)
	if plan.Source.Release != "" {
		title += " from " + sourceName(&plan.Source)
	}
	writeText(&b, width, 0, title)
	if len(plan.Dependencies) > 0 {
		tools := make([]string, 0, len(plan.Dependencies))
		for _, dependency := range plan.Dependencies {
			tools = append(
				tools,
				fmt.Sprintf("%s %s (%s)", dependency.Name, dependency.Version, dependency.Owner),
			)
		}
		writeText(&b, width, 0, "Uses "+strings.Join(tools, ", "))
	}

	b.WriteString("\n")
	writeText(&b, width, 0, fmt.Sprintf("Files (%d):", len(plan.Edits)))
	if len(plan.Edits) == 0 {
		writeText(&b, width, 2, "none")
	}
	for _, group := range groupBy(plan.Edits, func(edit operation.Edit) string { return edit.Description }) {
		writeText(&b, width, 2, group.key+":")
		rows := make([][]string, 0, len(group.items))
		for _, edit := range group.items {
			rows = append(rows, []string{edit.Action, homePath(edit.Path), edit.Summary})
		}
		writeTable(&b, width, 4, tableSpec{Cols: []column{{}, {}, {Clip: true}}, Rows: rows})
	}

	b.WriteString("\n")
	writeText(&b, width, 0, fmt.Sprintf("Effects (%d):", len(plan.Effects)))
	if len(plan.Effects) == 0 {
		writeText(&b, width, 2, "none")
	}
	for _, group := range groupBy(plan.Effects, func(effect operation.Effect) string {
		return "Privilege: " + effect.Privilege + ". Recovery: " + effect.Recovery + "."
	}) {
		writeText(&b, width, 2, group.key)
		rows := make([][]string, 0, len(group.items))
		for _, effect := range group.items {
			rows = append(rows, []string{effect.Name, effect.Description})
		}
		writeTable(&b, width, 4, tableSpec{Cols: []column{{}, {Clip: true}}, Rows: rows})
	}

	for _, section := range []struct {
		title string
		lines []string
	}{
		{"Prerequisites", plan.Prerequisites},
		{"Warnings", plan.Warnings},
		{"Recovery limits", plan.RecoveryLimits},
	} {
		if len(section.lines) > 0 {
			b.WriteString("\n")
			writeText(&b, width, 0, section.title+":")
			for _, line := range section.lines {
				writeText(&b, width, 2, line)
			}
		}
	}
	if !plan.Complete {
		b.WriteString("\n")
		writeText(&b, width, 0, "This plan is incomplete and cannot be applied as shown.")
	}
	_, err := lipgloss.Fprint(w, b.String())
	return err
}

// rowKind says which list a planRow indexes.
type rowKind int

const (
	rowNone   rowKind = iota // no row: no cursor
	rowFile                  // Index is into plan.Edits
	rowEffect                // Index is into plan.Effects
)

// planRow is one selectable row of the plan view: a file or an effect.
type planRow struct {
	Kind  rowKind
	Index int
}

// planView says how renderPlan draws a plan.
type planView struct {
	// Cursor is the row under the cursor, the zero value for none.
	Cursor planRow
	// Interactive draws the live list; false draws the static view that
	// --dry-run and a no-prompt apply print.
	Interactive bool
	// Done is set once the live list is dismissed: the final frame is the list
	// as chosen, with no cursor, panel or key line.
	Done bool
	// Verbose adds the detail panel under every listed row and the recovery
	// limits to the static view.
	Verbose bool
	// Applying words the static header as an apply that uses the saved choices.
	Applying bool
	// Height is the screen rows the live frame fills, keys on the last; 0 draws
	// the frame as tall as it is.
	Height int
	// Top is the list line the live frame started at last time. It moves only
	// when the cursor row would leave the screen.
	Top int
}

// planFrame is a drawn frame and where things are in it, for the cursor and
// the mouse.
type planFrame struct {
	Lines      []string
	CursorLine int       // the first line of the cursor row, -1 for none
	Top        int       // the list line the window starts at
	Hits       []planRow // per line, the row that line belongs to
	ListWidth  int       // columns from the left that belong to the list
}

// sideBySide is the width from which the detail panel stands beside the list
// and not under it.
const sideBySide = 100

// writePlanView prints the plan view once, at the writer's width: --dry-run, the
// machine-plan result component and, with applying, an apply that uses the
// saved choices without asking.
func writePlanView(w io.Writer, plan operation.Plan, verbose, applying bool) error {
	return printPlanView(w, plan, planView{Verbose: verbose, Applying: applying})
}

// printPlanView prints one frame with no blank line after it.
func printPlanView(w io.Writer, plan operation.Plan, view planView) error {
	frame := buildPlanFrame(plan, terminalWidth(w), view)
	_, err := lipgloss.Fprint(w, strings.Join(frame.Lines, "\n")+"\n")
	return err
}

// buildPlanFrame is the one renderer of the machine plan, for the live list,
// its final frame and the static view, at width columns. It draws the header,
// then the list and, in the live list, the detail panel of the cursor row beside
// it (under it on a narrow terminal) and the key line.
func buildPlanFrame(plan operation.Plan, width int, view planView) planFrame {
	head := append(planHeader(plan, width, view), "")
	if view.Interactive && !view.Done {
		return liveFrame(plan, width, view, head)
	}
	frame := planFrame{
		CursorLine: -1,
		ListWidth:  width,
		Lines:      head,
		Hits:       make([]planRow, len(head)),
	}
	for _, line := range listLines(plan, width, view) {
		frame.Lines = append(frame.Lines, trimEnd(line.Text))
		frame.Hits = append(frame.Hits, line.Row)
	}
	return frame
}

// liveFrame is the frame of the live list: head, a window of the list with the
// cursor row's panel beside or under it, and the rule and keys at the bottom.
func liveFrame(plan operation.Plan, width int, view planView, head []string) planFrame {
	wide := width >= sideBySide
	listWidth, panelWidth := width, width
	if wide {
		listWidth = (width - 3) * 52 / 100
		panelWidth = width - 3 - listWidth
	}
	list := listLines(plan, listWidth, view)
	panel := renderDetail(plan, view.Cursor, panelWidth)
	bounded := view.Height > 0
	listHeight, panelHeight, bodyHeight := len(list), len(panel), 0
	if bounded {
		bodyHeight = max(view.Height-len(head)-keyLines, 4)
		listHeight, panelHeight = bodyHeight, bodyHeight
		if !wide {
			panelHeight = min(len(panel), bodyHeight/2)
			listHeight = bodyHeight - panelHeight
			if panelHeight > 0 {
				listHeight--
			}
		}
	}
	frame := planFrame{
		CursorLine: -1,
		ListWidth:  width,
		Lines:      head,
		Hits:       make([]planRow, len(head)),
	}
	frame.Top = windowTop(list, view.Cursor, view.Top, listHeight, bounded)
	shown := list[frame.Top:min(frame.Top+listHeight, len(list))]
	panel = clipPanel(panel, panelHeight, bounded)
	rule := scrollRule(width, bounded && frame.Top > 0, bounded && frame.Top+listHeight < len(list))
	add := func(text string, row planRow) {
		if row == view.Cursor && row != (planRow{}) && frame.CursorLine < 0 {
			frame.CursorLine = len(frame.Lines)
		}
		frame.Lines = append(frame.Lines, trimEnd(text))
		frame.Hits = append(frame.Hits, row)
	}
	if wide {
		frame.ListWidth = listWidth
		for i := range max(len(shown), len(panel), bodyHeight) {
			var left listLine
			if i < len(shown) {
				left = shown[i]
			}
			right := ""
			if i < len(panel) {
				right = panel[i]
			}
			add(
				padTo(left.Text, listWidth)+" "+faint.Render(glyphs.border.Left)+" "+right,
				left.Row,
			)
		}
	} else {
		for _, line := range shown {
			add(line.Text, line.Row)
		}
		if len(panel) > 0 {
			add(rule, planRow{})
		}
		for _, line := range panel {
			add(line, planRow{})
		}
	}
	for bounded && len(frame.Lines) < view.Height-keyLines {
		add("", planRow{})
	}
	if wide {
		add(rule, planRow{})
	} else {
		add(faint.Render(strings.Repeat(glyphs.Rule, width)), planRow{})
	}
	add(keyLine(view, width), planRow{})
	return frame
}

// keyLines is the lines under the list: the rule and the key line.
const keyLines = 2

// scrollRule is a faint rule, ending in a note when the list has lines out of
// sight above or below the window.
func scrollRule(width int, above, below bool) string {
	note := ""
	switch {
	case above && below:
		note = " more above and below"
	case above:
		note = " more above"
	case below:
		note = " more below"
	}
	return faint.Render(strings.Repeat(glyphs.Rule, max(width-ansi.StringWidth(note), 0)) + note)
}

// padTo pads s with spaces to width display cells.
func padTo(s string, width int) string {
	return s + strings.Repeat(" ", max(width-ansi.StringWidth(s), 0))
}

// windowTop is the first list line a window of height lines shows: top, moved
// the least that keeps the cursor row on screen (and the group header above a
// group's first row), or 0 when the whole list fits or there is no bound.
func windowTop(list []listLine, cursor planRow, top, height int, bounded bool) int {
	if !bounded || height <= 0 || len(list) <= height {
		return 0
	}
	first, last := -1, -1
	for i, line := range list {
		if cursor != (planRow{}) && line.Row == cursor {
			if first < 0 {
				first = i
			}
			last = i
		}
	}
	if first >= 0 {
		if first < top {
			top = first
			for top > 0 && list[top-1].Row == (planRow{}) && top > first-2 {
				top--
			}
		}
		if last >= top+height {
			top = last - height + 1
		}
	}
	return max(min(top, len(list)-height), 0)
}

// clipPanel cuts a panel to height lines, saying how many it left out.
func clipPanel(panel []string, height int, bounded bool) []string {
	if !bounded || len(panel) <= height {
		return panel
	}
	if height < 2 {
		return panel[:max(height, 0)]
	}
	rest := len(panel) - (height - 1)
	return append(
		slices.Clone(panel[:height-1]),
		faint.Render(forTerm("… "+strconv.Itoa(rest)+" more lines, enter opens them")),
	)
}

// keyLine names the keys of the list: each in the accent color, what it does
// faint.
func keyLine(view planView, width int) string {
	enter := "open"
	switch view.Cursor.Kind {
	case rowFile:
		enter = "open diff"
	case rowEffect:
		enter = "details"
	case rowNone:
	}
	return hintLine(
		[][2]string{
			{"↑/↓", "move"},
			{"space", "on/off"},
			{"enter", enter},
			{"a", "apply"},
			{"q", "quit"},
		},
		[][2]string{{"↑/↓", "move"}, {"space", "toggle"}, {"a", "apply"}, {"q", "quit"}},
		width,
	)
}

// hintLine is a key line: full when it fits, else short, clipped to width.
func hintLine(full, short [][2]string, width int) string {
	line := func(hints [][2]string) string {
		parts := make([]string, len(hints))
		for i, hint := range hints {
			parts[i] = brand.Render(forTerm(hint[0])) + " " + faint.Render(hint[1])
		}
		return strings.Join(parts, faint.Render(forTerm("  ·  ")))
	}
	if keys := line(full); lipgloss.Width(keys) <= width {
		return keys
	}
	return fit(line(short), width, false)
}

// writeProposal prints a project plan, then what inspection found. The
// proposal's warnings print with the result's.
func writeProposal(w io.Writer, proposal *project.Proposal) error {
	if err := writePlan(w, proposal.Plan); err != nil {
		return err
	}
	if proposal.Inventory == nil {
		return nil
	}
	if _, err := fmt.Fprintln(w); err != nil {
		return err
	}
	return writeInventory(w, proposal.Inventory)
}

// writeInventory prints the candidate files inspection found and the projects
// they make, each with its language and manager or why it is unsupported.
func writeInventory(w io.Writer, inventory *project.Inventory) error {
	var b strings.Builder
	width := terminalWidth(w)
	counts := []string{fmt.Sprintf("%d entries", inventory.Entries)}
	if inventory.Excluded > 0 {
		counts = append(counts, fmt.Sprintf("%d excluded", inventory.Excluded))
	}
	if inventory.Skipped > 0 {
		counts = append(counts, fmt.Sprintf("%d skipped", inventory.Skipped))
	}
	writeText(
		&b,
		width,
		0,
		fmt.Sprintf(
			"Found in %s (%s):",
			homePath(inventory.Directory),
			strings.Join(counts, ", "),
		),
	)
	if len(inventory.Items) == 0 {
		writeText(&b, width, 2, "nothing")
	}
	rows := make([][]string, 0, len(inventory.Items))
	for _, item := range inventory.Items {
		rows = append(rows, []string{item.Path, item.Kind, item.Ecosystem})
	}
	writeTable(&b, width, 4, tableSpec{Cols: []column{{}, {}, {Clip: true}}, Rows: rows})
	if len(inventory.Projects) > 0 {
		writeText(&b, width, 0, "Projects:")
		rows = rows[:0]
		for _, found := range inventory.Projects {
			root, err := filepath.Rel(inventory.Directory, found.Root)
			if err != nil {
				root = found.Root
			}
			about := found.Language + " with " + found.Manager
			if !found.Supported {
				about = found.Language + ": " + found.Reason
			}
			rows = append(rows, []string{root, about})
		}
		writeTable(&b, width, 4, tableSpec{Cols: []column{{}, {Clip: true}}, Rows: rows})
	}
	if len(inventory.Warnings) > 0 {
		writeText(&b, width, 0, "Warnings:")
		for _, warning := range inventory.Warnings {
			writeText(&b, width, 2, warning)
		}
	}
	_, err := lipgloss.Fprint(w, b.String())
	return err
}

type group[T any] struct {
	key   string
	items []T
}

// groupBy groups items by key in the order each key first appears.
func groupBy[T any](items []T, key func(T) string) []group[T] {
	var groups []group[T]
	for _, item := range items {
		k := key(item)
		i := slices.IndexFunc(groups, func(g group[T]) bool { return g.key == k })
		if i < 0 {
			groups = append(groups, group[T]{key: k})
			i = len(groups) - 1
		}
		groups[i].items = append(groups[i].items, item)
	}
	return groups
}

// homePath shows a path under the home folder as ~/…
func homePath(path string) string {
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

func shortDigest(digest string) string {
	if len(digest) > 12 {
		return digest[:12]
	}
	return digest
}
