package machine

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/Sawmonabo/workbench/internal/migrate"
	"github.com/Sawmonabo/workbench/internal/operation"
)

// envRoles are the roles' env files, <role>.env in Workbench's config folder,
// each with the names it starts with. The shell hook loads one only inside
// its role's folder, and the work dispatch servers' gate reads work.env.
var envRoles = []struct {
	role  string
	names []string
}{{"work", migrate.WorkEnvNames}, {"personal", nil}}

// envName is a name an env file may set, and envReserved the names it may
// not: the shell's PATH and HOME, and the shell hooks' own variables.
var (
	envName     = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	envReserved = regexp.MustCompile(`^(PATH|HOME|__workbench_.*|_wb_.*)$`)
)

// ParseEnvFile reads an env file's NAME=value lines with a value, in file
// order: the names to set. Blank lines, # comments and a NAME= line with an
// empty value are skipped, so a name the owner has not filled in never hides
// a tool's own setting. bad holds the number, from 1, of every other line
// that is not NAME=value with a valid name that is not reserved, and a value
// that is unquoted, has no space or tab at either end and no carriage return
// at its end: the file is never sourced, so `export NAME=value`, a quoted or
// padded value or a Windows line ending would load wrong, and a reserved name
// would break the shell or the hook. A value is everything after the first =,
// as written. The shell hooks in home/private_dot_bashrc.tmpl and
// home/dot_zshrc.tmpl read the same way.
func ParseEnvFile(data []byte) (pairs [][2]string, bad []int) {
	number := 0
	for line := range strings.Lines(string(data)) {
		number++
		line = strings.TrimSuffix(line, "\n")
		if trimmed := strings.TrimSpace(line); trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		name, value, ok := strings.Cut(line, "=")
		if !ok || !envName.MatchString(name) || envReserved.MatchString(name) ||
			strings.HasPrefix(value, `"`) || strings.HasPrefix(value, "'") ||
			strings.HasSuffix(value, "\r") || strings.Trim(value, " \t") != value {
			bad = append(bad, number)
			continue
		}
		if value != "" {
			pairs = append(pairs, [2]string{name, value})
		}
	}
	return pairs, bad
}

// EnvFilesProbe is the env-files step's probe lines. The step only creates a
// missing file for a role the machine has, so every other line has nothing to
// do: a file that is there names its listed names that are empty and its
// skipped lines, and a file without its role says to delete it. Values are
// read only to tell empty from not.
func EnvFilesProbe(c operation.Context, a Answers) []string {
	var lines []string
	for _, env := range envRoles {
		file := env.role + ".env"
		path := filepath.Join(c.Paths.Config, file)
		_, err := os.Lstat(path)
		missing, has := errors.Is(err, fs.ErrNotExist), a["has_"+env.role] == true
		switch {
		case missing && !has:
		case missing && env.role == "work" && workTokensWaiting():
			lines = append(lines, "= waiting for migration 0005 to create work.env")
		case missing:
			lines = append(lines, "create "+file)
		case !has:
			lines = append(
				lines,
				"= "+file+" holds "+env.role+" credentials without the "+env.role+" role: delete it",
			)
		default:
			lines = append(lines, envFileLines(path, file, env.names)...)
		}
	}
	for i, line := range lines {
		lines[i] = "env-files: " + line
	}
	return lines
}

// envFileLines describes an env file that is there: the listed names it
// leaves empty or lacks, and each line it skips.
func envFileLines(path, file string, names []string) []string {
	data, err := operation.ReadPrivateInput(path, 1<<20)
	if err != nil {
		// Its reason holds "; ", which the plan view joins lines with.
		return []string{
			"= Workbench can't read " + file + " as a private file (mode 600, one link, yours)",
		}
	}
	pairs, bad := ParseEnvFile(data)
	set := map[string]bool{}
	for _, pair := range pairs {
		set[pair[0]] = true
	}
	line := "= " + file + " is there"
	empty := slices.DeleteFunc(slices.Clone(names), func(name string) bool { return set[name] })
	if len(empty) > 0 {
		line += ", empty: " + strings.Join(empty, ", ")
	}
	lines := []string{line}
	for _, number := range bad {
		lines = append(
			lines,
			fmt.Sprintf("= %s line %d is not NAME=value and is skipped", file, number),
		)
	}
	return lines
}

// workTokensWaiting reports whether migration 0005, which creates work.env
// with the work tokens machine.toml holds, still has something to do: until it
// is done, the step leaves work.env to it.
func workTokensWaiting() bool {
	l, err := migrate.Locate()
	if err != nil {
		return true
	}
	for _, f := range migrate.Check(l) {
		if f.ID == "0005-work-tokens" {
			return f.Status != migrate.Done
		}
	}
	return false
}

// EnvFilesRun creates each env file the machine's roles use that is missing,
// mode 600, with its listed names, and says what it did through say. A name
// the answers give (work.env's addresses and JIRA_EMAIL, as migration 0005
// writes them) holds that value, and every other name is empty. It never
// writes a file that is there: after it is created it is the owner's.
func EnvFilesRun(_ context.Context, c operation.Context, a Answers, say func(string)) error {
	operation.RemoveStaleWrites(c.Paths.Config)
	// The answers are checked, so each is one line of text, and they hold no
	// token once 0005 has nothing left to move.
	values, _ := migrate.WorkValues(a)
	said := false
	for _, env := range envRoles {
		file := env.role + ".env"
		path := filepath.Join(c.Paths.Config, file)
		if _, err := os.Lstat(path); a["has_"+env.role] != true || err == nil {
			continue
		}
		if env.role == "work" && workTokensWaiting() {
			say("waiting for migration 0005 to create work.env")
			said = true
			continue
		}
		var content strings.Builder
		for _, name := range env.names {
			content.WriteString(name + "=" + values[name] + "\n")
		}
		if err := operation.CreatePrivateFile(path, []byte(content.String())); err != nil {
			return err
		}
		say("created " + file)
		said = true
	}
	if !said {
		say("nothing to create")
	}
	return nil
}
