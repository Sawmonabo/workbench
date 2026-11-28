package operation

import (
	"os"
	"path/filepath"

	"github.com/Sawmonabo/workbench/internal/display"
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
// from ~ under home as one word a shell reads back (see [Context.ShellPath]).
// Where nothing is installed to name, it is workbench.
func (c Context) WorkbenchCommand() string {
	if c.BinOnPath() {
		return "workbench"
	}
	entry := c.Paths.Entrypoint()
	if _, err := os.Lstat(entry); err != nil {
		return "workbench"
	}
	return c.ShellPath(entry)
}

// ApplyCommand is what to type to apply the machine configuration to root:
// [Context.WorkbenchCommand]'s apply, with --destination for a root other
// than c.Home and --local-build where c's source is a developer checkout.
func (c Context) ApplyCommand(root string) string {
	command := c.WorkbenchCommand() + " apply"
	if root != c.Home {
		command += " --destination " + c.ShellPath(root)
	}
	if c.Native.Developer {
		command += " --local-build"
	}
	return command
}

// ScopeApplyCommand is what to type to plan and apply c's scope again: the
// machine's [Context.ApplyCommand] of its destination, or project apply of
// the project, with --local-build where c's source is a developer checkout.
func (c Context) ScopeApplyCommand() string {
	if c.Scope.Kind == "project" {
		command := c.WorkbenchCommand() + " project apply " + c.ShellPath(c.Scope.Root)
		if c.Native.Developer {
			command += " --local-build"
		}
		return command
	}
	return c.ApplyCommand(c.Scope.Root)
}

// RevertCommand is what to type to revert checkpoint id in c's scope: revert
// with --destination for a root other than c.Home, or project revert of the
// project.
func (c Context) RevertCommand(id string) string {
	switch {
	case c.Scope.Kind == "project":
		return c.WorkbenchCommand() + " project revert " + c.ShellPath(c.Scope.Root) +
			" --checkpoint " + id
	case c.Scope.Root != c.Home:
		return c.WorkbenchCommand() + " revert --checkpoint " + id +
			" --destination " + c.ShellPath(c.Scope.Root)
	}
	return c.WorkbenchCommand() + " revert --checkpoint " + id
}

// RevertListCommand is what to type to choose a checkpoint of c's scope to
// revert: [Context.RevertCommand]'s revert without a checkpoint, with
// --dry-run, which lists them, or asks which one at a terminal.
func (c Context) RevertListCommand() string {
	switch {
	case c.Scope.Kind == "project":
		return c.WorkbenchCommand() + " project revert " + c.ShellPath(c.Scope.Root) + " --dry-run"
	case c.Scope.Root != c.Home:
		return c.WorkbenchCommand() + " revert --dry-run --destination " + c.ShellPath(c.Scope.Root)
	}
	return c.WorkbenchCommand() + " revert --dry-run"
}

// ShowPath writes path for a person with [display.PathFrom] from c.Home.
func (c Context) ShowPath(path string) string { return display.PathFrom(c.Home, path) }

// ShellPath writes path as one word a POSIX shell reads back as path, for a
// command a person pastes: c.Home as ~, a path under it as ~/ and the rest,
// any other path as it is, all but the ~ quoted where a shell would read a
// character (see shellWord).
func (c Context) ShellPath(path string) string { return shellPath(c.Home, path) }
