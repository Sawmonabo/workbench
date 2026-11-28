package machine

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/Sawmonabo/workbench/internal/machine/envfile"
	"github.com/Sawmonabo/workbench/internal/migrate"
	"github.com/Sawmonabo/workbench/internal/operation"
)

// envRoles are the roles' env files, <role>.env in Workbench's config folder,
// each with the names it starts with. The shell hook loads one only inside
// its role's folder, and the work dispatch servers' gate reads work.env.
var envRoles = []struct {
	role  string
	names []string
}{{"work", envfile.WorkNames}, {"personal", nil}}

// EnvFilesProbe is the env-files step's probe lines. The step only creates a
// missing file for a role the machine has, so every other line has nothing to
// do: a file that is there names its listed names that are empty and its
// skipped lines, and a file without its role says to delete it. Values are
// read only to tell empty from not.
func EnvFilesProbe(c operation.Context, a Answers) []string {
	var lines []string
	for _, env := range envRoles {
		path := envfile.Path(c.Paths.Config, env.role)
		file := filepath.Base(path)
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
	data, err := operation.ReadPrivateInput(path, 1<<20, file)
	if err != nil {
		// Its reason holds "; ", which the plan view joins lines with.
		return []string{
			"= Workbench can't read " + file + " as a private file (mode 600, one link, yours)",
		}
	}
	pairs, bad := envfile.Parse(data)
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
	for _, line := range bad {
		lines = append(
			lines,
			fmt.Sprintf("= %s line %d %s and is skipped", file, line.Number, line.Reason),
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
	status, _ := migrate.WorkTokens.Check(l)
	return status != migrate.Done
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
	values, _ := envfile.WorkValues(a)
	said := false
	for _, env := range envRoles {
		path := envfile.Path(c.Paths.Config, env.role)
		file := filepath.Base(path)
		if _, err := os.Lstat(path); a["has_"+env.role] != true || err == nil {
			continue
		}
		if env.role == "work" && workTokensWaiting() {
			say("waiting for migration 0005 to create work.env")
			said = true
			continue
		}
		// work.env starts as migration 0005 writes it; personal.env, which
		// lists no names, starts empty.
		content := []byte{}
		if env.role == "work" {
			content = envfile.WorkFile(values)
		}
		if err := operation.CreatePrivateFile(path, content); err != nil {
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
