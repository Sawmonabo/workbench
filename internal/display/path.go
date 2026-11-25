// Package display writes the forms a person reads in Workbench's output and
// messages: paths named from the home folder. Each form has one rule here, so
// every command, view and message writes it alike. It imports nothing from
// Workbench.
package display

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"
)

// Home is the current user's home folder, resolved as operation.Resolve
// resolves it, so the paths it gives every runtime folder start with it, or
// empty when there is none.
func Home() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	if canonical, ok := existingFolder(home); ok {
		home = canonical
	}
	return home
}

// existingFolder is path made absolute with its links resolved, when that is
// an existing folder.
func existingFolder(path string) (string, bool) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", false
	}
	canonical, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", false
	}
	info, err := os.Stat(canonical)
	return canonical, err == nil && info.IsDir()
}

// Path writes path for a person with [PathFrom] from the current user's home
// folder as [Home] finds it, for a message made where no operation context
// is at hand: a check of Workbench's own folders, a file error. Without a home
// it shortens nothing.
func Path(path string) string { return PathFrom(Home(), path) }

// PathFrom writes path for a person: home itself as ~, a path under home as
// ~/..., any other path as it is. A path holding "; " or a control character
// is shown quoted, with "; " as ";\x20", so it stays on one line and a plan's
// join of a step's probe lines never splits it. An empty home, or /, shortens
// nothing: every absolute path is under /.
func PathFrom(home, path string) string {
	if home != "" && filepath.Clean(home) != "/" {
		if relative, err := filepath.Rel(home, path); err == nil && filepath.IsLocal(relative) {
			path = "~"
			if relative != "." {
				path += "/" + filepath.ToSlash(relative)
			}
		}
	}
	if strings.Contains(path, "; ") || strings.ContainsFunc(path, unicode.IsControl) {
		return strings.ReplaceAll(strconv.Quote(path), "; ", `;\x20`)
	}
	return path
}
