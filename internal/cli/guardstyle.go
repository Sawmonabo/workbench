package cli

import "charm.land/lipgloss/v2"

// The guard's colors, pinned rather than the terminal's own green, yellow
// and red, which many themes draw bright: three semantic colors a step darker
// than those, which mean the same thing in the log and the live view (green
// routine, gold worth a look, red acted on), and one hue per agent, so the
// eye can tell whose command a line is. lipgloss downsamples them to what the
// terminal shows, and a pipe or NO_COLOR gets none. Faint and bold come from
// the terminal and stay under NO_COLOR, as the costs view's do. The values are
// chosen on a dark background, where they also read on a light one.
var (
	// guardGreen marks an INFO level and a running command.
	guardGreen = lipgloss.NewStyle().Foreground(lipgloss.Color("#5e8a62"))
	// guardGold marks a WARN level, a command stopping, a size of 1 GB or
	// more and low memory; bold where it is a level, a state or a badge.
	guardGold = lipgloss.NewStyle().Foreground(lipgloss.Color("#a8862a"))
	// guardRed marks an ERROR level, a stopped command, a size of 4 GB or
	// more and critical memory; bold where it is a level, a size or a badge.
	guardRed = lipgloss.NewStyle().Foreground(lipgloss.Color("#a33f46"))
	// guardDimGold marks a command that ended unmatched: a warning that is
	// over.
	guardDimGold = lipgloss.NewStyle().Foreground(lipgloss.Color("#8a7a3a"))
	// guardClaude and guardCodex color an agent's name: clay for Claude Code,
	// steel for Codex.
	guardClaude = lipgloss.NewStyle().Foreground(lipgloss.Color("#c08a5a"))
	guardCodex  = lipgloss.NewStyle().Foreground(lipgloss.Color("#6f94a8"))
)

// The sizes at which a command's memory is colored: gold from a gigabyte,
// red from four.
const (
	guardSizeNoted = 1 << 30
	guardSizeLarge = 4 << 30
)

// agentStyle is the style of an agent's name, nil for one with no hue.
func agentStyle(agent string) *lipgloss.Style {
	switch agent {
	case "claude":
		return &guardClaude
	case "codex":
		return &guardCodex
	}
	return nil
}

// sizeStyle is the style of a memory size: nil below a gigabyte, gold bold
// from there, red bold from four.
func sizeStyle(bytes uint64) *lipgloss.Style {
	switch {
	case bytes >= guardSizeLarge:
		s := guardRed.Bold(true)
		return &s
	case bytes >= guardSizeNoted:
		s := guardGold.Bold(true)
		return &s
	}
	return nil
}
