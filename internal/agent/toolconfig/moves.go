package toolconfig

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Sawmonabo/workbench/internal/display"
	"github.com/Sawmonabo/workbench/internal/operation"
)

// Move is a tool's config folder moved by a variable away from the one
// Workbench installs the tool's settings, instructions and hooks in, so the
// tool reads none of them. Two folders side by side, one per account, are the
// variable's documented use, so Workbench names the move and installs nothing
// in the other folder. It also names a settings file that sets the variable
// to a relative path, which Workbench ignores.
type Move struct {
	Tool     string `json:"tool"`     // "Claude Code", "Codex"
	Variable string `json:"variable"` // CLAUDE_CONFIG_DIR, CODEX_HOME
	// Source is "the environment", the settings file that sets it, or "" when
	// it moves nothing.
	Source   string            `json:"source"`
	Dir      string            `json:"dir"`                // the folder the tool reads
	Default  string            `json:"default"`            // the folder Workbench installs in
	Relative []RelativeSetting `json:"relative,omitempty"` // settings files that set it to a relative path
}

// RelativeSetting is a settings file that sets a folder variable to a path
// that is not absolute.
type RelativeSetting struct {
	File  string `json:"file"`
	Value string `json:"value"`
}

// Moves is each tool's config folder that a variable moved (see [Move]),
// Claude Code's before Codex's.
func Moves(home string) []Move {
	var moves []Move
	for _, tool := range []func(string) (Move, bool){ClaudeMove, CodexMove} {
		if move, moved := tool(home); moved {
			moves = append(moves, move)
		}
	}
	return moves
}

// ClaudeMove reports CLAUDE_CONFIG_DIR pointing Claude Code away from
// ~/.claude, or set to a relative path in a settings file.
func ClaudeMove(home string) (Move, bool) {
	dir, source, relative := claudeConfigDir(home)
	move := Move{
		Tool:     "Claude Code",
		Variable: "CLAUDE_CONFIG_DIR",
		Source:   source,
		Dir:      dir,
		Default:  filepath.Join(home, ".claude"),
		Relative: relative,
	}
	if SameDir(dir, move.Default) {
		move.Source = ""
	}
	return move, move.Source != "" || len(relative) > 0
}

// codexTitle is Codex's name as a [Move] names its tool.
const codexTitle = "Codex"

// CodexMove reports CODEX_HOME pointing Codex away from ~/.codex.
func CodexMove(home string) (Move, bool) {
	move := Move{
		Tool:     codexTitle,
		Variable: "CODEX_HOME",
		Source:   "the environment",
		Dir:      CodexDir(home),
		Default:  filepath.Join(home, ".codex"),
	}
	return move, os.Getenv("CODEX_HOME") != "" && !SameDir(move.Dir, move.Default)
}

// Explain says what the move means and how to undo it, the same in the plan,
// doctor and agent costs status: a warning for the variable that moves the
// folder, and one for each settings file that sets it to a relative path.
func (m Move) Explain(home string) []operation.Warning {
	var warnings []operation.Warning
	if m.Source != "" {
		where := m.Source
		fix := operation.Fix("unset", m.Variable)
		if m.Source != "the environment" {
			where = display.PathFrom(home, m.Source)
			fix = operation.FixText("remove it from that file, or set it there to " + m.Default)
		}
		warnings = append(warnings, operation.Warn(
			m.Variable+" in "+where+" points "+m.Tool+" at "+display.PathFrom(home, m.Dir),
			operation.Cause(m.Tool+" does not read the settings, instructions and costs and "+
				"guard hooks Workbench installs in "+display.PathFrom(home, m.Default)),
			fix,
		))
	}
	for _, r := range m.Relative {
		warnings = append(warnings, operation.Warn(
			fmt.Sprintf("%s in %s is %q, not an absolute path",
				m.Variable, display.PathFrom(home, r.File), r.Value),
			operation.Cause(m.Tool+" reads it from the folder it starts in, so Workbench "+
				"ignores it and reads "+display.PathFrom(home, m.Dir)),
			operation.FixText("write the absolute path there"),
		))
	}
	return warnings
}

// Sentence is Explain in one line, for a cell of agent costs status.
func (m Move) Sentence(home string) string {
	var said []string
	for _, warning := range m.Explain(home) {
		said = append(said, warning.Sentence(warning.Message))
	}
	return strings.Join(said, "; ")
}

// SameDir reports whether a and b name the same folder: the same file when
// both exist, else the same absolute path.
func SameDir(a, b string) bool {
	aInfo, aErr := os.Stat(a)
	bInfo, bErr := os.Stat(b)
	if aErr == nil && bErr == nil {
		return os.SameFile(aInfo, bInfo)
	}
	absolute := func(path string) string {
		if abs, err := filepath.Abs(path); err == nil {
			return abs
		}
		return filepath.Clean(path)
	}
	return absolute(a) == absolute(b)
}
