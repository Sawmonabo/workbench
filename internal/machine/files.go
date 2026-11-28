package machine

import (
	"context"
	"path/filepath"
	"strings"

	"github.com/Sawmonabo/workbench/internal/operation"
)

// fileTitles are the plain names of every target the machine source manages,
// by path relative to the destination. A path not listed (a folder, a removed
// old file) shows its base name.
var fileTitles = map[string]string{
	".bashrc":                     "Bash startup file",
	".zshrc":                      "Zsh startup file",
	".gitconfig":                  "Git settings",
	".gitconfig-personal":         "Git settings (personal)",
	".gitconfig-work":             "Git settings (work)",
	".terminal-font-setup.sh":     "Terminal font setup",
	".config/ruff/pyproject.toml": "Ruff settings",
	".config/ty/ty.toml":          "ty settings",
	".config/tmux/tmux.conf":      "tmux settings",
	".config/oh-my-posh/catppuccin_mocha.omp.json":          "Shell prompt theme",
	".local/bin/win-browser":                                "Windows browser opener",
	".claude/settings.json":                                 "Claude Code settings",
	".claude/CLAUDE.md":                                     "Claude Code instructions",
	".claude/scripts/statusline.py":                         "Claude Code status line",
	".codex/config.toml":                                    "Codex settings",
	".codex/hooks.json":                                     "Codex hooks",
	".codex/AGENTS.md":                                      "Codex instructions",
	".config/Code/User/settings.json":                       "VS Code settings",
	"Library/Application Support/Code/User/settings.json":   "VS Code settings",
	".local/bin/claude-costs":                               "Old claude-costs command",
	".local/share/bash-completion/completions/claude-costs": "Old claude-costs completions",
}

// fileTitle is the plain name for a target path relative to the destination.
func fileTitle(relative string) string {
	if title, ok := fileTitles[filepath.ToSlash(relative)]; ok {
		return title
	}
	return filepath.Base(relative)
}

// mergedTargets reports which of the targets native renders from a modify_
// source: native merges into those files and keeps the owner's own keys.
func (p *preparation) mergedTargets(
	ctx context.Context,
	c operation.Context,
	targets []string,
) (map[string]bool, error) {
	merged := map[string]bool{}
	if len(targets) == 0 {
		return merged, nil
	}
	output, err := p.run(ctx, c, append([]string{"source-path"}, targets...)...)
	if err != nil {
		return nil, err
	}
	sources := strings.Split(strings.TrimSuffix(output, "\n"), "\n")
	if len(sources) != len(targets) {
		return nil, operation.Fail(
			operation.ExitFailed,
			"native",
			"Unexpected native source-path output",
		)
	}
	for i, source := range sources {
		merged[targets[i]] = strings.HasPrefix(filepath.Base(source), "modify_")
	}
	return merged, nil
}
