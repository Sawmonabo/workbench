package operation

import (
	"context"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Prevent credential exposure: an apply at a terminal holds the Mac password so
// Homebrew's sudo can ask for it through the SUDO_ASKPASS helper, and whoever
// reads it can run anything as root. So it must never reach a file in the
// helper's folder (any process under the account can list /tmp), or the
// arguments or environment of sudo (visible to ps); sudo gets it on standard
// input only. The helper must answer only processes the apply started, not
// Workbench itself or any other process, and nothing once the apply ends. A
// stand-in script plays sudo and records what it was given.
func TestWithAdminKeepsThePasswordToTheApply(t *testing.T) {
	const wrong, right = "wrong-password", "right-password"
	directory := t.TempDir()
	log, stdin := filepath.Join(directory, "log"), filepath.Join(directory, "stdin")
	script := "#!/bin/sh\n" +
		`{ echo "args: $*"; env; } >> ` + log + "\n" +
		`[ "$1" = -n ] && exit 1` + "\n" +
		`IFS= read -r typed; echo "$typed" >> ` + stdin + "\n" +
		`[ "$typed" = ` + right + " ]\n"
	stand := filepath.Join(directory, "sudo")
	if err := os.WriteFile(stand, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	typed := []string{wrong, right}
	previousSudo, previousRead := sudoPath, readSecret
	sudoPath = stand
	readSecret = func(context.Context, *os.File) (string, error) {
		next := typed[0]
		typed = typed[1:]
		return next, nil
	}
	t.Cleanup(func() { sudoPath, readSecret = previousSudo, previousRead })
	terminal, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = terminal.Close() })
	c := Context{Scope: Scope{Kind: "machine", Root: directory}}
	m := &Mutation{context: c, active: true}

	var folder, socket string
	err = WithAdmin(
		context.Background(),
		c,
		m,
		Admin{Terminal: terminal, Why: "install Homebrew"},
		func(helper string) error {
			folder = filepath.Dir(helper)
			socket = filepath.Join(folder, "s")
			files := 0
			_ = filepath.WalkDir(folder, func(path string, entry fs.DirEntry, walkErr error) error {
				if walkErr == nil && entry.Type().IsRegular() {
					data, _ := os.ReadFile(path)
					files++
					if strings.Contains(string(data), right) ||
						strings.Contains(string(data), wrong) {
						t.Errorf("a password is in %s", path)
					}
				}
				return nil
			})
			if files == 0 {
				t.Error("the helper's folder holds no file, so the check above proved nothing")
			}
			// This process is Workbench itself, not one the apply started.
			if reply, talkErr := askpassTalk(socket, "get\n"); talkErr == nil {
				t.Errorf("the helper answered Workbench itself: %q", reply)
			}
			// A process the apply started is answered, so the check above is not
			// just a helper that answers nobody.
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			out, _ := exec.CommandContext(
				ctx, "/bin/sh", "-c", `printf 'get\n' | /usr/bin/nc -U "$1"`, "sh", socket,
			).Output()
			if string(out) != "password "+right+"\n" {
				t.Errorf("the helper did not answer a process the apply started: %q", out)
			}
			return nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	recorded, _ := os.ReadFile(log)
	if !strings.Contains(string(recorded), "args: -k -S -v") {
		t.Errorf("sudo was not run as sudo -k -S -v; log:\n%s", recorded)
	}
	if strings.Contains(string(recorded), wrong) || strings.Contains(string(recorded), right) ||
		strings.Contains(string(recorded), "SUDO_ASKPASS") {
		t.Errorf(
			"a password or SUDO_ASKPASS reached sudo's arguments or environment; log:\n%s",
			recorded,
		)
	}
	if got, _ := os.ReadFile(stdin); string(got) != wrong+"\n"+right+"\n" {
		t.Errorf("sudo was not given the passwords on standard input: %q", got)
	}
	if strings.Contains(redact("sudo said "+right, m.redactions(nil)), right) {
		t.Error("the password is not among the secrets the apply redacts")
	}
	if _, statErr := os.Stat(folder); !os.IsNotExist(statErr) {
		t.Errorf("the helper's folder is left after the apply: %v", statErr)
	}
	if reply, talkErr := askpassTalk(socket, "get\n"); talkErr == nil {
		t.Errorf("the helper still answers after the apply: %q", reply)
	}
}
