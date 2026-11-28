package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Sawmonabo/workbench/internal/operation"
)

// Unauthorized execution: with --yes, or as a bare apply, a coding agent could
// approve a plan it never read, and the plan's sudo, Homebrew and installer
// steps would then run unasked. checkApply is the gate that runs before any
// setup, so both must be consent refusals (exit 3). --approve-plan, with the
// digest of a plan the agent read, is the agent's route.
func TestAgentApplyIsRefusedBeforeSetup(t *testing.T) {
	t.Setenv("CLAUDECODE", "1")
	for name, o := range map[string]*options{
		"--yes":      {yes: true},
		"bare apply": {},
	} {
		if got := operation.ExitCode(checkApply(o)); got != operation.ExitBlocked {
			t.Errorf("agent running apply %s exited %d, want %d", name, got, operation.ExitBlocked)
		}
	}
}

// Unauthorized execution: an apply given a digest that is not the plan
// apply --dry-run shows has nothing approved. Were setup to run before the
// digest is compared, it would download, install and run the pinned tools
// (and take the locks and set folder modes) for a plan nobody read, and only
// then refuse. With the tools missing, the dry run's own refusal must come
// first, with nothing written in Workbench's folders. The download is sent
// to a closed port, so a build that sets up first fails here at once.
func TestStaleApprovalSetsUpNothing(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	home, destination := filepath.Join(root, "home"), filepath.Join(root, "home", "dest")
	if err = os.MkdirAll(destination, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("PATH", root)
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:1")
	folders := map[string]string{}
	for _, name := range []string{"CONFIG", "DATA", "STATE", "CACHE", "BIN"} {
		folders[name] = filepath.Join(root, strings.ToLower(name))
		t.Setenv("WORKBENCH_"+name+"_DIR", folders[name])
	}
	var out, diagnostics strings.Builder
	code := Execute(t.Context(), []string{
		"apply", "--local-build", "--destination", destination, "--json",
		"--approve-plan", strings.Repeat("0", 64),
	}, strings.NewReader(""), &out, &diagnostics)
	if code != int(operation.ExitBlocked) || !strings.Contains(out.String(), `"prerequisites"`) {
		t.Errorf("exit %d, want the dry run's refusal (exit %d, prerequisites):\n%s%s",
			code, operation.ExitBlocked, out.String(), diagnostics.String())
	}
	for _, folder := range folders {
		if _, err := os.Lstat(folder); err == nil {
			t.Errorf("the refused apply made %s", folder)
		}
	}
}
