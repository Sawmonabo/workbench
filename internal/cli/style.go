package cli

import (
	"image/color"

	"charm.land/lipgloss/v2"
)

var (
	green  = lipgloss.NewStyle().Foreground(lipgloss.Green)
	red    = lipgloss.NewStyle().Foreground(lipgloss.Red)
	yellow = lipgloss.NewStyle().Foreground(lipgloss.Yellow)
	faint  = lipgloss.NewStyle().Faint(true)
	bold   = lipgloss.NewStyle().Bold(true)
)

// brandTag is Workbench's name as its output heads a line with it.
const brandTag = "[WorkBench]"

// brandHead is brandTag bold in accent, as a header draws it.
func brandHead(accent lipgloss.Style) string { return accent.Bold(true).Render(brandTag) }

// brand is the color of Workbench's own name and keys, the accent the costs
// report gives a tool with no color of its own; badge is the dark-on-yellow
// pill a new item wears, as the costs tabs wear one in their tool's color.
var (
	brand = toolPalette("workbench").accent
	badge = pill(lipgloss.Yellow)
)

// pill is bold dark text on background, a space of it either side: the
// active costs tab and the new-item badge.
func pill(background color.Color) lipgloss.Style {
	return lipgloss.NewStyle().Bold(true).Padding(0, 1).
		Foreground(lipgloss.Color("#1E1E1E")).Background(background)
}

// palette is one tool's colors. lipgloss downsamples them to what the
// terminal shows and drops them for a pipe, TERM=dumb and NO_COLOR.
type palette struct {
	accent lipgloss.Style // the headline, column heads, share bars and brand
	tab    lipgloss.Style // the active tab: dark text on the accent
}

// toolColors are the tools' own brand colors.
var toolColors = map[string]string{"claude": "#D97757", "codex": "#10A37F"}

func toolPalette(name string) palette {
	hex, ok := toolColors[name]
	if !ok {
		hex = "#7AA2F7"
	}
	accent := lipgloss.Color(hex)
	return palette{
		accent: lipgloss.NewStyle().Foreground(accent),
		tab:    pill(accent),
	}
}
