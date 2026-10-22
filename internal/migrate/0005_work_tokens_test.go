package migrate

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/pelletier/go-toml/v2"

	"github.com/Sawmonabo/workbench/internal/operation"
)

// TestWorkTokensNeverLost guards the owner's work tokens while 0005 moves
// them out of machine.toml: the risk is credential loss (the keys deleted
// before work.env holds them, or a work.env the owner wrote replaced) and a
// token exposed in the migration's output.
func TestWorkTokensNeverLost(t *testing.T) {
	const machine = `[data]
email = 'user@example.invalid'
gitlab_token = 'synthetic-gitlab-token'
gitlab_url = 'https://gitlab.example.invalid/group'
jira_api_token = 'synthetic-jira-token'
jira_url = 'https://jira.example.invalid'
machine_role = 'both'
work_email = 'work@example.invalid'

[effects]
decided = ['migrations']
`

	t.Run("both role without work.env", func(t *testing.T) {
		l := tokenLocations(t, machine)
		said := runAll(t, l)
		info, err := os.Lstat(workEnvPath(l))
		if err != nil || info.Mode().Perm() != 0o600 {
			t.Fatalf("work.env is not a 0600 file: %v", err)
		}
		values := envValues(t, l)
		for _, name := range WorkEnvNames {
			if _, ok := values[name]; !ok {
				t.Errorf("work.env lacks %s", name)
			}
		}
		want := map[string]string{
			"JIRA_API_TOKEN": "synthetic-jira-token",
			"GITLAB_TOKEN":   "synthetic-gitlab-token",
			"JIRA_EMAIL":     "work@example.invalid",
			"JIRA_BASE_URL":  "https://jira.example.invalid",
			"GITLAB_URL":     "https://gitlab.example.invalid/group",
		}
		for name, value := range want {
			if values[name] != value {
				t.Errorf("work.env's %s is not the saved value", name)
			}
		}
		assertKeysRemoved(t, l, machine)
		assertNoToken(t, said, l)
	})

	t.Run("stopped after the first write", func(t *testing.T) {
		l := tokenLocations(t, machine)
		found := findWorkTokens(l)
		if err := operation.CreatePrivateFile(workEnvPath(l), workEnv(found.values)); err != nil {
			t.Fatal(err)
		}
		env := read(t, workEnvPath(l))
		if string(read(t, machinePath(l))) != machine {
			t.Fatal("machine.toml changed before the run")
		}
		if status, _ := WorkTokens.Check(l); status != Unfinished {
			t.Fatalf("Check = %v, want unfinished", status)
		}
		said := runAll(t, l)
		if string(read(t, workEnvPath(l))) != string(env) {
			t.Error("work.env changed")
		}
		assertKeysRemoved(t, l, machine)
		assertNoToken(t, said, l)
	})

	t.Run("work.env holds something else", func(t *testing.T) {
		l := tokenLocations(t, machine)
		other := "JIRA_API_TOKEN=synthetic-other-token\n"
		if err := operation.CreatePrivateFile(workEnvPath(l), []byte(other)); err != nil {
			t.Fatal(err)
		}
		assertUntouched(t, l, machine, other)
	})

	t.Run("a control character in a token", func(t *testing.T) {
		l := tokenLocations(t, strings.Replace(machine,
			`jira_api_token = 'synthetic-jira-token'`,
			`jira_api_token = "synthetic-jira-token\nGITLAB_URL=x"`, 1))
		assertUntouched(t, l, string(read(t, machinePath(l))), "")
	})

	t.Run("personal role", func(t *testing.T) {
		personal := strings.Replace(
			machine,
			`machine_role = 'both'`,
			`machine_role = 'personal'`,
			1,
		)
		l := tokenLocations(t, personal)
		status, text := WorkTokens.Check(l)
		if status != Done || !strings.Contains(text, "delete them") {
			t.Fatalf("Check = %v %q, want done naming the keys to delete", status, text)
		}
		probe := Probe(l, nil)
		if !slices.Contains(probe, "migrations: = "+WorkTokens.ID+": "+text) {
			t.Errorf("the plan lines %q don't name the keys to delete", probe)
		}
		said := append(runAll(t, l), probe...)
		if _, err := os.Lstat(workEnvPath(l)); err == nil {
			t.Error("work.env was written without the work role")
		}
		if string(read(t, machinePath(l))) != personal {
			t.Error("machine.toml changed without the work role")
		}
		assertNoToken(t, said, l)
	})
}

// tokenLocations is a home of its own holding machine.toml with the given
// text, and its locations.
func tokenLocations(t *testing.T, machine string) Locations {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	for _, name := range []string{"CONFIG", "DATA", "STATE", "CACHE", "BIN"} {
		t.Setenv("WORKBENCH_"+name+"_DIR", "")
	}
	for _, name := range OldLocationVariables {
		t.Setenv(name, "")
	}
	l, err := Locate()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = operation.PrivateDirectory(l.Runtime.Config, ""); err != nil {
		t.Fatal(err)
	}
	if err = operation.CreatePrivateFile(machinePath(l), []byte(machine)); err != nil {
		t.Fatal(err)
	}
	return l
}

// runAll runs every migration as the apply does and returns what 0005's
// Check said first and what the run said.
func runAll(t *testing.T, l Locations) []string {
	t.Helper()
	_, text := WorkTokens.Check(l)
	said := []string{text}
	if err := Run(
		context.Background(),
		l,
		nil,
		func(line string) { said = append(said, line) },
	); err != nil {
		t.Fatalf("Run: %v (said %q)", err, said)
	}
	return said
}

// assertUntouched checks that 0005 is in conflict, that a run fails, and that
// both files keep their bytes; an empty env means there is no work.env.
func assertUntouched(t *testing.T, l Locations, machine, env string) {
	t.Helper()
	status, text := WorkTokens.Check(l)
	if status != Conflict {
		t.Fatalf("Check = %v, want conflict", status)
	}
	var said []string
	if err := Run(
		context.Background(),
		l,
		nil,
		func(line string) { said = append(said, line) },
	); err == nil {
		t.Error("Run succeeded with 0005 in conflict")
	}
	if _, err := WorkTokens.Run(context.Background(), l); err == nil {
		t.Error("0005's Run succeeded in conflict")
	}
	if string(read(t, machinePath(l))) != machine {
		t.Error("machine.toml changed")
	}
	if env == "" {
		if _, err := os.Lstat(workEnvPath(l)); err == nil {
			t.Error("work.env was written")
		}
	} else if string(read(t, workEnvPath(l))) != env {
		t.Error("work.env changed")
	}
	assertNoToken(t, append(said, text), l)
}

// assertKeysRemoved checks that machine.toml is the original without the two
// token keys, its other answers and [effects] kept.
func assertKeysRemoved(t *testing.T, l Locations, original string) {
	t.Helper()
	var want, got map[string]any
	if err := toml.Unmarshal([]byte(original), &want); err != nil {
		t.Fatal(err)
	}
	data, _ := want["data"].(map[string]any)
	delete(data, "jira_api_token")
	delete(data, "gitlab_token")
	if err := toml.Unmarshal(read(t, machinePath(l)), &got); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Error("machine.toml is not the original without the two token keys")
	}
	if status, _ := WorkTokens.Check(l); status != Done {
		t.Errorf("Check after the run = %v, want done", status)
	}
}

// assertNoToken fails when a synthetic token appears in the lines or in what
// Check says.
func assertNoToken(t *testing.T, lines []string, l Locations) {
	t.Helper()
	_, text := WorkTokens.Check(l)
	for _, line := range append(lines, text) {
		if strings.Contains(line, "synthetic") {
			t.Errorf("a token appears in the output: %q", line)
		}
	}
}

func envValues(t *testing.T, l Locations) map[string]string {
	t.Helper()
	values := map[string]string{}
	for line := range strings.Lines(string(read(t, workEnvPath(l)))) {
		if name, value, ok := strings.Cut(strings.TrimSuffix(line, "\n"), "="); ok {
			values[name] = value
		}
	}
	return values
}

func read(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		t.Fatal(err)
	}
	return data
}
