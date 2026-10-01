package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"charm.land/lipgloss/v2"

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

// planView says how renderPlan draws a plan.
type planView struct {
	// Cursor is the index in plan.Effects of the row under the cursor, -1 for
	// none. Fixed effects are never under it.
	Cursor int
	// Interactive draws the live list; false draws the static view that
	// --dry-run and a no-prompt apply print.
	Interactive bool
	// Done is set once the live list is dismissed: the final frame has no
	// cursor and keeps its height.
	Done bool
	// Verbose adds every effect's recovery text and the recovery limits.
	Verbose bool
}

// writePlanView prints the plan view once, at the writer's width: --dry-run,
// the machine-plan result component and an apply that does not prompt.
func writePlanView(w io.Writer, plan operation.Plan, verbose bool) error {
	lines, _ := renderPlan(plan, terminalWidth(w), planView{Cursor: -1, Verbose: verbose})
	_, err := lipgloss.Fprint(w, strings.Join(lines, "\n")+"\n")
	return err
}

// renderPlan is the one renderer of the machine plan, for the interactive list
// and for the static view, at width columns. It returns the lines and the
// index of the line the cursor row starts on, -1 when there is no cursor row.
func renderPlan(plan operation.Plan, width int, view planView) (lines []string, cursorLine int) {
	if !view.Interactive {
		text := machinePlanText(plan, width, view.Verbose, true)
		return strings.Split(strings.TrimSuffix(text, "\n"), "\n"), -1
	}
	// The files above the list are still printed by choosePlan.
	spec := tableSpec{Cols: effectColumns}
	cursorBlock := -1
	for i, effect := range plan.Effects {
		if effect.Fixed {
			continue
		}
		row := append([]string{" "}, effectRow(effect)...)
		if !view.Done && i == view.Cursor {
			row[0], row[2] = ">", bold.Render(row[2])
			cursorBlock = len(spec.Rows)
		}
		spec.Rows = append(spec.Rows, row)
	}
	cursorLine = -1
	for i, block := range fitBlocks(width, 2, spec) {
		if i == cursorBlock {
			cursorLine = len(lines)
		}
		lines = append(lines, block...)
	}
	return lines, cursorLine
}

// machinePlanText is the checklist view as text: changed files with their line
// counts, then one line per effect with its probed delta, a privilege tag and
// whether a skip was saved. Recovery text and limits are verbose.
func machinePlanText(plan operation.Plan, width int, verbose, effects bool) string {
	var b strings.Builder
	title := "[WorkBench] Plan for this machine"
	if plan.Source.Release != "" {
		title += " (" + sourceName(&plan.Source) + ")"
	}
	writeText(&b, width, 0, title)
	b.WriteString("\n")
	writeText(
		&b,
		width,
		0,
		fmt.Sprintf("Files (%d changed, %d unchanged)", len(plan.Edits), plan.UnchangedTargets),
	)
	if len(plan.Edits) == 0 {
		writeText(&b, width, 2, "none")
	}
	rows := make([][]string, 0, len(plan.Edits))
	for _, edit := range plan.Edits {
		rows = append(rows, []string{homePath(edit.Path), editSummary(edit)})
	}
	writeTable(
		&b,
		width,
		4,
		tableSpec{Cols: []column{{Clip: true, ClipLeft: true}, {}}, Rows: rows},
	)
	if effects {
		writeEffects(&b, width, plan.Effects, verbose)
	}
	if verbose && len(plan.Effects) > 0 {
		b.WriteString("\n")
		writeText(&b, width, 0, "Recovery")
		for _, effect := range plan.Effects {
			writeText(&b, width, 2, effect.Name+": "+effect.Recovery)
		}
	}
	sections := []struct {
		title string
		lines []string
	}{{"Prerequisites", plan.Prerequisites}, {"Warnings", plan.Warnings}}
	if verbose {
		sections = append(sections, struct {
			title string
			lines []string
		}{"Recovery limits", plan.RecoveryLimits})
	}
	for _, section := range sections {
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
	return b.String()
}

// writeEffects writes the Effects section of the plan: every effect's box,
// name, delta, privilege and saved note, in full under verbose.
func writeEffects(b *strings.Builder, width int, effects []operation.Effect, verbose bool) {
	b.WriteString("\n")
	writeText(b, width, 0, "Effects")
	rows := make([][]string, 0, len(effects))
	for _, effect := range effects {
		rows = append(rows, effectRow(effect))
	}
	if len(rows) == 0 {
		writeText(b, width, 2, "none")
	}
	spec := tableSpec{Cols: effectColumns[1:], Rows: rows}
	if verbose {
		writeFull(b, width, 4, spec)
	} else {
		writeTable(b, width, 4, spec)
	}
}

// editSummary keeps the counts and mode of an edit's summary and drops the
// "not previously written by Workbench" note, which the first apply adds to
// every line.
func editSummary(edit operation.Edit) string {
	summary := strings.TrimSuffix(edit.Summary, "; not previously written by Workbench")
	if edit.Action == "create" && summary == "" {
		return "new"
	}
	if edit.Action == "remove" {
		return "removed"
	}
	return summary
}

// effectColumns are the checklist's columns: the cursor, which only the
// interactive list fills, then box, name, delta, privilege tag and saved note.
// The privilege tag and the note are secondary, and drop first on a narrow
// terminal.
var effectColumns = []column{
	{},
	{},
	{},
	{Clip: true},
	{Drop: 2, Faint: true},
	{Drop: 1, Faint: true},
}

// effectRow is one checklist line: box, name, delta, privilege tag, saved mark.
func effectRow(effect operation.Effect) []string {
	box := "[x]"
	if !effect.Checked {
		box = "[ ]"
	}
	if effect.Fixed {
		box = "   "
	}
	delta := effect.Delta
	switch {
	case delta == "" && effect.Probe != "":
		delta = "unprobed (" + effect.Probe + ")"
	case delta == "":
		delta = effect.Description
	}
	note := ""
	if effect.SavedSkip {
		note = "skipped (saved)"
	}
	return []string{box, effect.Name, delta, privilegeTag(effect), note}
}

// privilegeTag shortens "sudo; network and font-cache effects" to "sudo, network".
func privilegeTag(effect operation.Effect) string {
	tag := strings.TrimSpace(strings.Split(effect.Privilege, ";")[0])
	tag = strings.TrimSuffix(tag, " user")
	if strings.Contains(effect.Privilege, "network") {
		tag += ", network"
	}
	return tag
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
