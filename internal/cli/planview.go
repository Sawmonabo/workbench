package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/Sawmonabo/workbench/internal/operation"
	"github.com/Sawmonabo/workbench/internal/project"
)

// writePlan prints a plan for review: where it comes from, the files it
// changes grouped by description, and its effects grouped by the privilege
// they need and what recovery can undo, then prerequisites, warnings and
// recovery limits. --json prints the complete plan instead.
func writePlan(w io.Writer, plan operation.Plan) error {
	var b strings.Builder
	title := "Plan for " + plan.Scope.Kind + " " + homePath(plan.Scope.Root)
	if plan.Source.Release != "" {
		title += " from " + sourceName(&plan.Source)
	}
	b.WriteString(title + "\n")
	if len(plan.Dependencies) > 0 {
		tools := make([]string, 0, len(plan.Dependencies))
		for _, dependency := range plan.Dependencies {
			tools = append(
				tools,
				fmt.Sprintf("%s %s (%s)", dependency.Name, dependency.Version, dependency.Owner),
			)
		}
		b.WriteString("Uses " + strings.Join(tools, ", ") + "\n")
	}

	fmt.Fprintf(&b, "\nFiles (%d):\n", len(plan.Edits))
	if len(plan.Edits) == 0 {
		b.WriteString("  none\n")
	}
	for _, group := range groupBy(plan.Edits, func(edit operation.Edit) string { return edit.Description }) {
		b.WriteString("  " + group.key + ":\n")
		rows := make([][]string, 0, len(group.items))
		for _, edit := range group.items {
			rows = append(rows, []string{edit.Action, homePath(edit.Path), edit.Summary})
		}
		writeColumns(&b, rows)
	}

	fmt.Fprintf(&b, "\nEffects (%d):\n", len(plan.Effects))
	if len(plan.Effects) == 0 {
		b.WriteString("  none\n")
	}
	for _, group := range groupBy(plan.Effects, func(effect operation.Effect) string {
		return "Privilege: " + effect.Privilege + ". Recovery: " + effect.Recovery + "."
	}) {
		b.WriteString("  " + group.key + "\n")
		rows := make([][]string, 0, len(group.items))
		for _, effect := range group.items {
			rows = append(rows, []string{effect.Name, effect.Description})
		}
		writeColumns(&b, rows)
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
			b.WriteString("\n" + section.title + ":\n")
			for _, line := range section.lines {
				b.WriteString("  " + line + "\n")
			}
		}
	}
	if !plan.Complete {
		b.WriteString("\nThis plan is incomplete and cannot be applied as shown.\n")
	}
	_, err := io.WriteString(w, b.String())
	return err
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
	counts := []string{fmt.Sprintf("%d entries", inventory.Entries)}
	if inventory.Excluded > 0 {
		counts = append(counts, fmt.Sprintf("%d excluded", inventory.Excluded))
	}
	if inventory.Skipped > 0 {
		counts = append(counts, fmt.Sprintf("%d skipped", inventory.Skipped))
	}
	fmt.Fprintf(
		&b,
		"Found in %s (%s):\n",
		homePath(inventory.Directory),
		strings.Join(counts, ", "),
	)
	if len(inventory.Items) == 0 {
		b.WriteString("  nothing\n")
	}
	rows := make([][]string, 0, len(inventory.Items))
	for _, item := range inventory.Items {
		rows = append(rows, []string{item.Path, item.Kind, item.Ecosystem})
	}
	writeColumns(&b, rows)
	if len(inventory.Projects) > 0 {
		b.WriteString("Projects:\n")
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
		writeColumns(&b, rows)
	}
	if len(inventory.Warnings) > 0 {
		b.WriteString("Warnings:\n")
		for _, warning := range inventory.Warnings {
			b.WriteString("  " + warning + "\n")
		}
	}
	_, err := io.WriteString(w, b.String())
	return err
}

// writeColumns writes indented rows with every column but the last padded to
// its widest cell, and no trailing spaces.
func writeColumns(b *strings.Builder, rows [][]string) {
	var widths []int
	for _, row := range rows {
		for i, cell := range row {
			if i == len(widths) {
				widths = append(widths, 0)
			}
			widths[i] = max(widths[i], utf8.RuneCountInString(cell))
		}
	}
	var line strings.Builder
	for _, row := range rows {
		line.Reset()
		line.WriteString("   ")
		for i, cell := range row {
			line.WriteString(" " + cell)
			if i < len(row)-1 {
				line.WriteString(strings.Repeat(" ", widths[i]-utf8.RuneCountInString(cell)+1))
			}
		}
		b.WriteString(strings.TrimRight(line.String(), " ") + "\n")
	}
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
