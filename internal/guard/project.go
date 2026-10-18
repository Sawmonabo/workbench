package guard

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// commandFolder reads a command's leading `cd PATH &&` or `cd PATH;`: the
// folder PATH names, bare (plain characters only) or single- or
// double-quoted, and rest, the command after the separator. ok is false when
// there is no such cd or the shell would decide the folder itself: PATH
// starts with ~, $ or - (an option or the previous folder), holds a
// backquote, or, double-quoted, holds a $ or a backslash.
func commandFolder(command string) (folder, rest string, ok bool) {
	text := strings.TrimLeft(command, " \t")
	after, found := strings.CutPrefix(text, "cd")
	if !found || after == "" || after[0] != ' ' && after[0] != '\t' {
		return "", "", false
	}
	if after = strings.TrimLeft(after, " \t"); after == "" {
		return "", "", false
	}
	var end int
	switch quote := after[0]; quote {
	case '\'', '"':
		closing := strings.IndexByte(after[1:], quote)
		if closing < 0 {
			return "", "", false
		}
		folder, end = after[1:1+closing], closing+2
		if quote == '"' && strings.ContainsAny(folder, `$\`) {
			return "", "", false
		}
	default:
		for end < len(after) && plainByte(after[end]) {
			end++
		}
		folder = after[:end]
	}
	if folder == "" || strings.ContainsAny(folder[:1], "~$-") || strings.ContainsRune(folder, '`') {
		return "", "", false
	}
	tail := strings.TrimLeft(after[end:], " \t")
	switch {
	case strings.HasPrefix(tail, "&&"):
		rest = tail[2:]
	case strings.HasPrefix(tail, ";"):
		rest = tail[1:]
	default:
		return "", "", false
	}
	return folder, strings.TrimLeft(rest, " \t"), true
}

// CommandRest is a command without its leading `cd PATH &&` or `cd PATH;`
// when that names a folder the guard can know, else the command as given.
func CommandRest(command string) string {
	if _, rest, ok := commandFolder(command); ok {
		return rest
	}
	return command
}

// projectName labels the folder a command runs in: its leading cd's folder
// when known, else cwd, a relative folder taken against cwd. The label is the
// name after the last .worktrees or worktrees folder in the path when one
// follows it, else the name of the nearest folder at or above the path that
// holds .git (a folder, or a file in a linked worktree), else the path's last
// name. It is empty when no folder is known. It only labels; nothing acts on
// it.
func projectName(command, cwd string) string {
	path := cwd
	if folder, _, ok := commandFolder(command); ok {
		path = folder
		if !filepath.IsAbs(folder) {
			path = ""
			if cwd != "" {
				path = filepath.Join(cwd, folder)
			}
		}
	}
	if path == "" || !filepath.IsAbs(path) {
		return ""
	}
	path = filepath.Clean(path)
	parts := strings.Split(path, string(filepath.Separator))
	for i := len(parts) - 2; i >= 0; i-- {
		if (parts[i] == ".worktrees" || parts[i] == "worktrees") && parts[i+1] != "" {
			return parts[i+1]
		}
	}
	for dir := path; ; dir = filepath.Dir(dir) {
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			if name := filepath.Base(dir); name != string(filepath.Separator) {
				return name
			}
			break
		}
		if filepath.Dir(dir) == dir {
			break
		}
	}
	if path == string(filepath.Separator) {
		return ""
	}
	return filepath.Base(path)
}

// noteFolders are the folders the process running n's command may have as
// its working folder: the agent's cwd, where the shell starts, and the
// leading cd's folder, where it moves before running the rest. Unknown ones
// are left out.
func noteFolders(n note) []string {
	var out []string
	for _, folder := range []string{n.Cwd, n.Folder} {
		if folder == "" {
			continue
		}
		if folder = filepath.Clean(folder); !slices.Contains(out, folder) {
			out = append(out, folder)
		}
	}
	return out
}

// realPath is path with every link resolved, as the kernel reports a
// process's working folder, or empty when path is empty, relative or cannot
// be resolved.
func realPath(path string) string {
	if !filepath.IsAbs(path) {
		return ""
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return ""
	}
	return resolved
}
