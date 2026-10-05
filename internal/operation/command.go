package operation

import (
	"os"
	"path/filepath"
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
	if Within(c.Home, entry) {
		if relative, err := filepath.Rel(c.Home, entry); err == nil {
			return "~/" + filepath.ToSlash(relative)
		}
	}
	return entry
}
