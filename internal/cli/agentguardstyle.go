package cli

import (
	"image/color"

	"charm.land/lipgloss/v2"

	"github.com/Sawmonabo/workbench/internal/agent/guard"
	"github.com/Sawmonabo/workbench/internal/display"
)

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

// The sizes at which a command's memory is colored: gold from the size
// [display.Memory] prints as 1.00 GB, red from the one it prints as 4.00 GB,
// so the color and the text agree.
const (
	guardSizeNoted = display.MemoryMBLimit
	// guardSizeLarge is half a hundredth of a GB under 4 GB, where two
	// decimals round up to 4.00.
	guardSizeLarge = 4<<30 - (1<<30)/200
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

// sizeStep is the step of [sizeStyle] a memory size falls on: 0 below a
// gigabyte, 1 from there, 2 from four.
func sizeStep(bytes uint64) int {
	switch {
	case bytes >= guardSizeLarge:
		return 2
	case bytes >= guardSizeNoted:
		return 1
	}
	return 0
}

// sizeStyle is the style of a memory size: nil below a gigabyte, gold bold
// from there, red bold from four.
func sizeStyle(bytes uint64) *lipgloss.Style {
	switch sizeStep(bytes) {
	case 2:
		s := guardRed.Bold(true)
		return &s
	case 1:
		s := guardGold.Bold(true)
		return &s
	}
	return nil
}

// The selected row's band and accent in the live view, for a dark terminal
// and for a light one. The band is a slate a few steps lighter than a dark
// background, the way lazygit's selectedLineBgColor marks its selected line;
// the accent, a light steel, marks the row's ▸ and its ID, bold.
var (
	guardBandDark    = lipgloss.Color("#3a4252")
	guardBandLight   = lipgloss.Color("#d6dde8")
	guardAccentDark  = lipgloss.Color("#9ec1dc")
	guardAccentLight = lipgloss.Color("#2f5c7e")
)

// guardAccent is the accent, bold, on a light background or a dark one: the
// selected row's ▸ and ID, the chosen `… more` row of the details and the
// side panel's title.
func guardAccent(light bool) lipgloss.Style {
	return lipgloss.NewStyle().Bold(true).
		Foreground(lipgloss.LightDark(!light)(guardAccentLight, guardAccentDark))
}

// guardOnBand are the colors drawn on the selected row in place of the
// pinned ones, for the dark band (false) and the light one (true): each
// pinned color with under 3:1 contrast against the band, or little more,
// becomes the same hue lighter on the dark band and darker on the light one,
// to about 3.2:1 or more. 3:1 is WCAG 2's floor for large text (success
// criterion 1.4.3) and for parts of a view a person must tell apart (1.4.11,
// https://www.w3.org/WAI/WCAG22/Understanding/non-text-contrast.html). Gold
// stays brighter than the dim gold. A color not listed has 3:1 or more as it
// is, and the pinned values themselves stay as they are off the band.
var guardOnBand = map[bool]map[color.Color]color.Color{
	// The dark band, #3a4252.
	false: {
		guardGreen.GetForeground():   lipgloss.Color("#6e9c72"), // 2.54:1 to 3.20:1
		guardGold.GetForeground():    lipgloss.Color("#b9942e"), // 2.94:1 to 3.53:1
		guardRed.GetForeground():     lipgloss.Color("#e5737c"), // 1.61:1 to 3.40:1
		guardDimGold.GetForeground(): lipgloss.Color("#a39145"), // 2.37:1 to 3.22:1
		guardCodex.GetForeground():   lipgloss.Color("#7297aa"), // 3.11:1 to 3.23:1
	},
	// The light band, #d6dde8; the red (4.58:1) and the dim gold (3.12:1)
	// keep their pinned values.
	true: {
		guardGreen.GetForeground():  lipgloss.Color("#59825d"), // 2.91:1 to 3.22:1
		guardGold.GetForeground():   lipgloss.Color("#927525"), // 2.52:1 to 3.21:1
		guardClaude.GetForeground(): lipgloss.Color("#a26d3e"), // 2.19:1 to 3.21:1
		guardCodex.GetForeground():  lipgloss.Color("#587d92"), // 2.37:1 to 3.23:1
	},
}

// sizeStyles are the styles of a command's memory now (while it runs; a
// finished one shows none) and of its peak, each nil below a gigabyte.
func sizeStyles(c guard.CommandView) (now, peak *lipgloss.Style) {
	if active(c) {
		now = sizeStyle(c.Memory)
	}
	return now, sizeStyle(c.Peak)
}

// statusStyle colors a state: running green, stopping gold and bold,
// stopped red, unmatched a dim gold, done plain.
func statusStyle(state string) lipgloss.Style {
	switch state {
	case "running":
		return guardGreen
	case "stopping":
		return guardGold.Bold(true)
	case "stopped":
		return guardRed
	case "unmatched":
		return guardDimGold
	}
	return lipgloss.NewStyle()
}

// onBand is s as the selected row draws it on the light band or the dark
// one: its foreground swapped for the one [guardOnBand] lists, so it keeps at
// least 3:1 contrast with the band, and the rest of s as it is.
func onBand(s lipgloss.Style, light bool) lipgloss.Style {
	if lifted, ok := guardOnBand[light][s.GetForeground()]; ok {
		return s.Foreground(lifted)
	}
	return s
}

// accent is the style of the selected row's ▸ and ID, of the chosen `… more`
// row and of the side panel's title: bold in the accent, or bold alone
// without color.
func (m *guardView) accent() lipgloss.Style {
	if m.noColor {
		return bold
	}
	return guardAccent(m.light)
}
