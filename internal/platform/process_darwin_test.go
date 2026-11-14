package platform

import (
	"bufio"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// The guard's view shows a program's full command line, read through
// ShownArgs. A process that rewrites its title leaves its kernel-reported arguments
// running on into its environment, where a token may sit, so a shown command
// line could expose a credential on screen. /usr/bin/perl does it for $0, and
// it is a restricted program whose environment the kernel otherwise hides.
// The synthetic token is the first environment string, the one the rewritten
// arguments run into.
func TestShownArgsNeverHoldTheEnvironmentOfARetitledProcess(t *testing.T) {
	const perl = "/usr/bin/perl"
	if _, err := os.Stat(perl); err != nil {
		t.Skip("no " + perl)
	}
	const token = "synthetic-token-7f3a9c"
	cmd := exec.Command(perl, "-e", `$0 = "retitled"; $| = 1; print "ready\n"; sleep 30`)
	cmd.Env = []string{"WORKBENCH_TEST_TOKEN=" + token, "PATH=/usr/bin:/bin"}
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
	if line, _ := bufio.NewReader(out).ReadString('\n'); line != "ready\n" {
		t.Fatalf("perl did not start: %q", line)
	}
	args, _ := ShownArgs(cmd.Process.Pid)
	if len(args) == 0 || args[0] != "retitled" {
		t.Fatalf("ShownArgs = %d strings, want the new title first", len(args))
	}
	for i, arg := range args {
		if strings.Contains(arg, token) || strings.Contains(arg, "WORKBENCH_TEST_TOKEN") {
			t.Fatalf("ShownArgs[%d] holds the environment string", i)
		}
	}
}
