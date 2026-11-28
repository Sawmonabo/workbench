package operation

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"

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

// ShowPath writes path for a person with [display.PathFrom] from c.Home.
func (c Context) ShowPath(path string) string { return display.PathFrom(c.Home, path) }

// ShellPath writes path as one word a POSIX shell reads back as path, for a
// command a person pastes: c.Home as ~, a path under it as ~/ and the rest,
// any other path as it is, all but the ~ quoted where a shell would read a
// character (see shellWord).
func (c Context) ShellPath(path string) string { return shellPath(c.Home, path) }

// shellHomePath is [Context.ShellPath] from the current user's home folder,
// as [display.Home] finds it, for a message made where no [Context] is at
// hand.
func shellHomePath(path string) string { return shellPath(display.Home(), path) }

// shellPath is path as one word a shell reads back, written from home as
// [Context.ShellPath] says. An empty home, or /, shortens nothing.
func shellPath(home, path string) string {
	if home != "" && filepath.Clean(home) != "/" && Within(home, path) {
		if relative, err := filepath.Rel(home, path); err == nil {
			if relative == "." {
				return "~"
			}
			return "~/" + shellWord(filepath.ToSlash(relative))
		}
	}
	return shellWord(path)
}

// shellSafe are the characters a POSIX shell reads as themselves anywhere in
// a word that does not start with them.
const shellSafe = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_./,+@:"

// shellWord is s as one word a shell reads back as s: as it is when it holds
// only shellSafe characters, otherwise in single quotes (see shellQuote), and
// one holding a control character as $'...' with every byte outside printable
// ASCII written \xHH, so it stays on one line and prints no control
// character, which bash, zsh and ksh read.
func shellWord(s string) string {
	switch {
	case s != "" && strings.Trim(s, shellSafe) == "":
		return s
	case !strings.ContainsFunc(s, unicode.IsControl):
		return shellQuote(s)
	}
	var b strings.Builder
	b.WriteString("$'")
	for i := range len(s) {
		switch c := s[i]; {
		case c == '\\' || c == '\'':
			b.WriteByte('\\')
			b.WriteByte(c)
		case c < ' ' || c >= 0x7f:
			b.WriteString(`\x` + strconv.FormatUint(uint64(c)|0x100, 16)[1:])
		default:
			b.WriteByte(c)
		}
	}
	b.WriteByte('\'')
	return b.String()
}
