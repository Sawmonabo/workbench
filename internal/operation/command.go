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
// from ~ under home as one word a shell reads back (see [Context.ShellPath]).
// Where nothing is installed to name, it is workbench.
func (c Context) WorkbenchCommand() string {
	if c.BinOnPath() {
		return "workbench"
	}
	entry := filepath.Join(c.Paths.Bin, "workbench")
	if _, err := os.Lstat(entry); err != nil {
		return "workbench"
	}
	return c.ShellPath(entry)
}

// ShowPath writes path for a person with [ShowPath] from c.Home.
func (c Context) ShowPath(path string) string { return ShowPath(c.Home, path) }

// ShowHomePath writes path for a person with [ShowPath] from the current
// user's home folder, resolved as [Resolve] resolves it, for a message made
// where no [Context] is at hand: a check of Workbench's own folders, a file
// error. Without a home it shortens nothing.
func ShowHomePath(path string) string { return ShowPath(userHome(), path) }

// userHome is the current user's home folder, resolved as [Resolve] resolves
// it, or empty when there is none.
func userHome() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	if canonical, err := ExistingDirectory(home); err == nil {
		home = canonical
	}
	return home
}

// ShellPath writes path as one word a POSIX shell reads back as path, for a
// command a person pastes, from c.Home (see [ShellHomePath]).
func (c Context) ShellPath(path string) string { return shellPath(c.Home, path) }

// ShellHomePath writes path as one word a POSIX shell reads back as path, for
// a command a person pastes: the current user's home folder, as
// [ShowHomePath] finds it, as ~, a path under it as ~/ and the rest, any other
// path as it is, all but the ~ quoted where a shell would read a character
// (see shellWord). It is for a message made where no [Context] is at hand.
func ShellHomePath(path string) string { return shellPath(userHome(), path) }

// shellPath is path as one word a shell reads back, written from home as
// [ShellHomePath] says. An empty home, or /, shortens nothing.
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
