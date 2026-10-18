package cli

import "charm.land/lipgloss/v2"

// The guard's colors, pinned rather than the terminal's own green, yellow
// and red, which many themes draw bright: three semantic colors a step darker
// than those, which mean the same thing in the log and the live view (green
// routine, gold worth a look, red acted on), and one hue per agent, so the
// eye can tell whose command a line is. lipgloss downsamples them to what the
// terminal shows, and a pipe or NO_COLOR gets none. Faint and bold come from
// the terminal and stay under NO_COLOR, as the costs view's do. The values were
// chosen on a dark background and read on a light one too.
var (
	// guardGreen marks an INFO level and a running command.
	guardGreen = lipgloss.NewStyle().Foreground(lipgloss.Color("#5e8a62"))
	// guardGold marks a WARN level, a command stopping, a tag, a watcher not
	// answering (○ and the status text), a size of 1 GB or more and low
	// memory; bold where it is a level, a state, a badge or a size.
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

// The selected row's band and accent in the live view, for a dark terminal
// and for a light one. The band is a slate a few steps lighter than a dark
// background, the way lazygit's selectedLineBgColor marks its selected line;
// the accent, a light steel, marks the row's ▸ and its ID, bold. On the dark
// band the red and the dim gold lose contrast (1.6:1 and 2.4:1, against 2.7:1
// and 3.9:1 on a #1e1e1e background), more so on a finished row, which is
// faint, so on it they are drawn lighter, to at least the contrast they have
// off the band; the light band keeps them as they are.
var (
	guardBandDark    = lipgloss.Color("#3a4252")
	guardBandLight   = lipgloss.Color("#d6dde8")
	guardAccentDark  = lipgloss.Color("#9ec1dc")
	guardAccentLight = lipgloss.Color("#2f5c7e")
	guardRedOnBand   = lipgloss.Color("#d0606a")
	guardDimGoldBand = lipgloss.Color("#b4a264")
)
