package machine

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/Sawmonabo/workbench/internal/agent/toolconfig"
	"github.com/Sawmonabo/workbench/internal/display"
	"github.com/Sawmonabo/workbench/internal/operation"
	"github.com/Sawmonabo/workbench/internal/platform"
	"github.com/pelletier/go-toml/v2"
)

// Doctor reports what Workbench has installed and applied, then probes
// management versions, the language runtimes against their pins (see
// checkRuntimes) and the host. It does not launch VS Code's CLI: that CLI may
// create logs/user state. Host/profile discovery reads the actual locations.
// Only a blocked line, a missing prerequisite Workbench itself needs, makes it
// fail.
func Doctor(ctx context.Context, c operation.Context) ([]operation.Component, error) {
	state, err := operation.ReadState(c.Paths)
	if err != nil {
		return nil, err
	}
	if state == nil {
		state = &operation.State{}
	}
	recorded := state.Dependencies
	results := installed(c, state)
	selection, selectionErr := ReadSelection(c.Native.Config)
	effects := operation.Component{
		Name:    "saved-choices",
		Status:  operation.StatusUnchanged,
		Message: "no step skipped or selected",
	}
	switch {
	case selectionErr != nil:
		effects.Status = operation.StatusBlocked
		effects.Message, effects.Body = operation.Explained(selectionErr)
	case len(selection.Skip) > 0 || len(selection.Select) > 0:
		effects.Status = operation.StatusComplete
		var parts []string
		if len(selection.Skip) > 0 {
			parts = append(parts, "skipped "+strings.Join(selection.Skip, ", "))
		}
		if len(selection.Select) > 0 {
			parts = append(parts, "selected "+strings.Join(selection.Select, ", "))
		}
		effects.Message = strings.Join(parts, "; ")
	}
	results = append(results, effects)
	results = append(results, checkPlatform(ctx, c))
	files, sourceErr := SelectedSource(c)
	var requirements Requirements
	if sourceErr == nil {
		requirements, sourceErr = SourceRequirements(files)
	}
	if sourceErr != nil {
		line := operation.Component{Name: "tool-versions", Status: operation.StatusBlocked}
		line.Message, line.Body = operation.Explained(sourceErr)
		results = append(results, line)
	} else {
		_, resolved := ResolveDependencies(ctx, c, requirements, recorded, os.Getenv("PATH"))
		// Workbench's own tools read tool-, apart from the runtime- lines of
		// the versions the machine's projects run on.
		for i := range resolved {
			resolved[i].Name = "tool-" + resolved[i].Name
		}
		results = append(results, resolved...)
		results = append(results, checkRuntimes(ctx, c, files, selection.Skip)...)
		results = append(results, checkMbx(ctx, c, requirements.Mbx, selection.Select))
	}
	// Plain chezmoi still pointed at another source, such as dotfiles, would
	// reapply it over the same files Workbench manages.
	if raw, readErr := os.ReadFile(
		filepath.Join(c.Home, ".config", "chezmoi", "chezmoi.toml"),
	); readErr == nil {
		var native struct {
			SourceDir string `toml:"sourceDir"`
		}
		if toml.Unmarshal(raw, &native) == nil && native.SourceDir != "" {
			results = append(
				results,
				operation.Component{
					Name:   "native-chezmoi",
					Status: operation.StatusConflict,
					Message: "~/.config/chezmoi/chezmoi.toml points plain chezmoi at " + c.ShowPath(
						native.SourceDir,
					),
					Body: operation.Explain(
						operation.Cause(
							"plain chezmoi would apply that source over the files Workbench manages",
						),
						operation.FixText(
							"move it aside after switching (docs/switch-from-dotfiles.md)",
						),
					),
				},
			)
		}
	}
	for _, move := range toolconfig.Moves(c.Home) {
		for _, warning := range move.Explain(c.Home) {
			results = append(results, operation.Component{
				Name:    strings.ToLower(strings.ReplaceAll(move.Variable, "_", "-")),
				Status:  operation.StatusConflict,
				Message: warning.Message,
				Body:    warning.Body,
			})
		}
	}
	results = append(results, editorFolders(c)...)
	for _, result := range results {
		if result.Status == operation.StatusBlocked {
			return results, operation.Fail(
				operation.ExitBlocked,
				"dependency",
				"Doctor found something Workbench cannot run without",
				operation.Cause("each line marked blocked names it; doctor repaired nothing"),
			)
		}
	}
	return results, nil
}

// editorFolders is a doctor line for each VS Code settings folder Workbench
// can manage: the local one, and the VS Code Server one of a remote host.
func editorFolders(c operation.Context) []operation.Component {
	var lines []operation.Component
	root := platform.VSCodeUserFolder(c.Home)
	editors := []struct{ name, path, what string }{
		{"editor-local", root, "VS Code"},
		{
			"editor-remote-wsl",
			filepath.Join(c.Home, ".vscode-server", "data", "Machine"),
			"VS Code Server",
		},
	}
	for _, target := range editors {
		component := operation.Component{
			Name:    target.name,
			Status:  operation.StatusSkipped,
			Message: "no " + target.what + " settings folder on this machine",
		}
		if operation.IsFolder(target.path) {
			component.Status = operation.StatusComplete
			component.Message = target.what + " settings in " + c.ShowPath(target.path)
		}
		lines = append(lines, component)
	}
	return lines
}

// installed reports the active release, the last applied configuration and
// any apply that did not finish, from recorded state without probing.
func installed(c operation.Context, state *operation.State) []operation.Component {
	release := operation.Component{
		Name:    "release",
		Status:  operation.StatusAbsent,
		Message: "No release installed",
		Body:    operation.Explain(operation.FixText("install one with install.sh")),
	}
	if state.ActiveRelease != nil {
		release.Status = operation.StatusComplete
		release.Message = state.ActiveRelease.Identity.Release + " is active"
	}
	applied := operation.Component{
		Name:    "applied",
		Status:  operation.StatusAbsent,
		Message: "Nothing applied yet",
	}
	if source := state.AppliedConfiguration; source != nil {
		applied.Status = operation.StatusComplete
		applied.Message = "Last applied from release " + source.Release
		if source.Release == "developer" {
			applied.Message = "Last applied from a developer checkout (" + source.ContentDigest[:12] + ")"
		}
		if state.AppliedAt != nil {
			applied.Message += " on " + state.AppliedAt.Local().Format(display.Date)
		}
	}
	results := []operation.Component{release, applied}
	if partial := state.PartialOperation; partial != nil {
		results = append(results, operation.Component{
			Name:   "unfinished-apply",
			Status: operation.StatusPartial,
			Message: "Apply " + partial.ID + " to " + c.ShowPath(
				partial.Scope.Root,
			) + " did not finish",
			Body: operation.Explain(operation.FixText(c.WorkbenchCommand() + " apply")),
		})
	}
	return results
}

func checkPlatform(ctx context.Context, c operation.Context) operation.Component {
	result := operation.Component{
		Name:    "platform",
		Status:  operation.StatusBlocked,
		Message: platform.System + " on " + runtime.GOARCH,
	}
	if runtime.GOARCH != "amd64" && runtime.GOARCH != "arm64" {
		result.Body = operation.Explain(operation.Cause("Workbench supports amd64 and arm64"))
		return result
	}
	hostVersion(ctx, c, &result)
	return result
}
