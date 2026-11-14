package platform

import (
	"bufio"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// The guard's view shows a program's command line as ShownArgs gives it. A
// process that rewrites its title leaves its kernel-reported arguments
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

// retitleSource rewrites its title as Perl's $0 does on macOS: the title, a
// NUL, then spaces up to the end of its last argument, so its argument area
// holds two strings whatever its argument count.
const retitleSource = `#include <stdio.h>
#include <string.h>
#include <unistd.h>
int main(int argc, char **argv) {
	char *start = argv[0], *end = argv[argc - 1] + strlen(argv[argc - 1]);
	memcpy(start, "retitled", 8);
	start[8] = '\0';
	memset(start + 9, ' ', (size_t)(end - (start + 9)));
	printf("ready\n"); fflush(stdout); sleep(30); return 0;
}
`

// A program can keep the guard off its work by WORKBENCH_GUARD=off in its
// environment. If a program that rewrites its title loses that opt-out to
// the read, the guard may stop work its owner kept out of reach, and a
// CLAUDE_PID it loses the same way no longer marks it as another agent's.
// Under bash, a command-prefix assignment is the first environment string,
// the one a rewritten title with three or more arguments lets the arguments
// run into. The program here is readable (not a restricted system binary),
// as a Homebrew or nix Perl is.
func TestARetitledProgramKeepsItsOptOut(t *testing.T) {
	cc, err := exec.LookPath("cc")
	if err != nil {
		t.Skip("no cc to build the retitling program")
	}
	dir := t.TempDir()
	source, program := filepath.Join(dir, "retitle.c"), filepath.Join(dir, "retitle")
	if err := os.WriteFile(source, []byte(retitleSource), 0o600); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command(cc, "-o", program, source).CombinedOutput(); err != nil {
		t.Skipf("cc failed: %v: %s", err, out)
	}
	shell := exec.Command(
		"/bin/bash",
		"-c",
		"WORKBENCH_GUARD=off CLAUDE_PID=4242 "+program+" a b c & wait",
	)
	shell.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + dir, "LANG=C"}
	out, err := shell.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := shell.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		for _, child := range Children(shell.Process.Pid) {
			_ = syscall.Kill(child, syscall.SIGKILL)
		}
		_ = shell.Process.Kill()
		_ = shell.Wait()
	})
	if line, _ := bufio.NewReader(out).ReadString('\n'); line != "ready\n" {
		t.Fatalf("the program did not start: %q", line)
	}
	children := Children(shell.Process.Pid)
	if len(children) != 1 {
		t.Fatalf("bash has %d children, want the program alone", len(children))
	}
	env := ReadEnvironment(children[0], "WORKBENCH_GUARD", "CLAUDE_PID")
	if !env.Readable || env.Narrowing("WORKBENCH_GUARD") != "off" ||
		env.Narrowing("CLAUDE_PID") != "4242" {
		t.Fatalf("readable %v, opt-out %q, agent stamp set %v: the opt-out was lost",
			env.Readable, env.Narrowing("WORKBENCH_GUARD"), env.Narrowing("CLAUDE_PID") == "4242")
	}
}
