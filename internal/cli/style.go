package cli

import (
	"charm.land/lipgloss/v2"
)

var (
	green  = lipgloss.NewStyle().Foreground(lipgloss.Green)
	red    = lipgloss.NewStyle().Foreground(lipgloss.Red)
	yellow = lipgloss.NewStyle().Foreground(lipgloss.Yellow)
	faint  = lipgloss.NewStyle().Faint(true)
)

var bold = lipgloss.NewStyle().Bold(true)

// The plan view's own styles, beside the ones `workbench agent costs` shares from
// root.go and agentcosts.go: the brand and key color is the costs default accent,
// and a new item wears the dark-on-yellow pill the costs tabs wear in their
// own color.
var (
	brand = toolPalette("workbench").accent
	badge = lipgloss.NewStyle().Bold(true).Padding(0, 1).
		Foreground(lipgloss.Color("#1E1E1E")).Background(lipgloss.Yellow)
)

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
		tab: lipgloss.NewStyle().Bold(true).Padding(0, 1).
			Foreground(lipgloss.Color("#1E1E1E")).Background(accent),
	}
}
