package operation

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"
)

// BinOnPath reports whether the directory that holds the workbench command is
// on this process's PATH. A fresh machine's shell starts without it: the first
// apply writes the shell setup that adds it, and only a new terminal reads that.
func (c Context) BinOnPath() bool {
	bin := filepath.Clean(c.Paths.Bin)
	resolved, resolveErr := filepath.EvalSymlinks(bin)
	for _, entry := range filepath.SplitList(os.Getenv("PATH")) {
		if !filepath.IsAbs(entry) {
			continue
		}
		entry = filepath.Clean(entry)
		if entry == bin {
			return true
		}
		// A PATH entry may reach the directory through a symlink (Handoff
		// passes its canonical form on, while Paths.Bin is only cleaned).
		if canonical, err := filepath.EvalSymlinks(entry); resolveErr == nil && err == nil &&
			canonical == resolved {
			return true
		}
	}
	return false
}

// WorkbenchCommand is what to type to run the installed workbench, for a hint
// such as "run <command> apply" to a person whose shell may not find it yet:
// workbench when its directory is on PATH, otherwise its full path, written
// from ~ under home. Where nothing is installed to name, it is workbench.
func (c Context) WorkbenchCommand() string {
	if c.BinOnPath() {
		return "workbench"
	}
	entry := filepath.Join(c.Paths.Bin, "workbench")
	if _, err := os.Lstat(entry); err != nil {
		return "workbench"
	}
	return c.ShowPath(entry)
}

// ShowPath writes path for a person with [ShowPath] from c.Home.
func (c Context) ShowPath(path string) string { return ShowPath(c.Home, path) }

// ShowPath writes path for a person: home itself as ~, a path under home as
// ~/..., any other path as it is. A path holding "; " or a control character
// is shown quoted, with "; " as ";\x20", so it stays on one line and a plan's
// join of a step's probe lines never splits it. An empty home, or /, shortens
// nothing: every absolute path is under /.
func ShowPath(home, path string) string {
	if home != "" && filepath.Clean(home) != "/" && Within(home, path) {
		if relative, err := filepath.Rel(home, path); err == nil {
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
