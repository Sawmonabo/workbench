package operation

import (
	"context"
	"errors"
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
// Workbench itself or any other process, and nothing once the apply ends, when
// the sudo approval its answer left is dropped too. A stand-in script plays sudo
// and records what it was given.
func TestWithAdminKeepsThePasswordToTheApply(t *testing.T) {
	const wrong, right = "wrong-password", "right-password"
	directory := t.TempDir()
	log, stdin := filepath.Join(directory, "log"), filepath.Join(directory, "stdin")
	script := "#!/bin/sh\n" +
		`{ echo "args: $*"; env; } >> ` + log + "\n" +
		`[ "$1" = -n ] && exit 1` + "\n" +
		`[ "$*" = -k ] && exit 0` + "\n" +
		`IFS= read -r typed; echo "$typed" >> ` + stdin + "\n" +
		`[ "$typed" = ` + right + " ]\n"
	stand := filepath.Join(directory, "sudo")
	if err := os.WriteFile(stand, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	typed := []string{wrong, right}
	// The password path is for a Mac where sudo does not use Touch ID, whatever
	// this one does.
	previousSudo, previousRead, previousLocal, previousSystem :=
		sudoPath, readSecret, pamSudoLocal, pamSudo
	sudoPath = stand
	pamSudoLocal, pamSudo = filepath.Join(directory, "none"), filepath.Join(directory, "none")
	readSecret = func(context.Context, *os.File) (string, error) {
		next := typed[0]
		typed = typed[1:]
		return next, nil
	}
	t.Cleanup(func() {
		sudoPath, readSecret, pamSudoLocal, pamSudo =
			previousSudo, previousRead, previousLocal, previousSystem
	})
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
	if !strings.Contains(string(recorded), "args: -k\n") {
		t.Errorf("the sudo approval the helper's answer left was not dropped; log:\n%s", recorded)
	}
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

// Prevent credential exposure: where sudo already asks for Touch ID (or for its
// own password where Touch ID cannot be used), sudo does the asking, so
// Workbench must not read the Mac password or make the SUDO_ASKPASS helper. The
// helper would hold a password nobody needed to give Workbench and hand it to
// every process the apply starts, which is root for whoever reads it. sudo -v is
// run on the terminal with no -S, no password on standard input and no
// SUDO_ASKPASS, and the approval it leaves is dropped when the apply ends. A
// stand-in script plays sudo.
func TestWithAdminAsksNothingItselfWhereSudoUsesTouchID(t *testing.T) {
	directory := t.TempDir()
	temp := filepath.Join(directory, "tmp")
	if err := os.Mkdir(temp, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMPDIR", temp)
	pam := filepath.Join(directory, "sudo_local")
	if err := os.WriteFile(
		pam,
		[]byte("auth       sufficient     pam_tid.so\n"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	log := filepath.Join(directory, "log")
	script := "#!/bin/sh\n" +
		`{ echo "args: $*"; env; } >> ` + log + "\n" +
		`[ "$1" = -n ] && exit 1` + "\n" +
		"exit 0\n"
	stand := filepath.Join(directory, "sudo")
	if err := os.WriteFile(stand, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	previousSudo, previousRead, previousLocal, previousSystem :=
		sudoPath, readSecret, pamSudoLocal, pamSudo
	sudoPath, pamSudoLocal, pamSudo = stand, pam, filepath.Join(directory, "missing")
	readSecret = func(context.Context, *os.File) (string, error) {
		t.Error("Workbench read the Mac password where sudo asks for Touch ID")
		return "", errors.New("no password")
	}
	t.Cleanup(func() {
		sudoPath, readSecret, pamSudoLocal, pamSudo =
			previousSudo, previousRead, previousLocal, previousSystem
	})
	terminal, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = terminal.Close() })
	c := Context{Scope: Scope{Kind: "machine", Root: directory}}
	m := &Mutation{context: c, active: true}

	ran := false
	err = WithAdmin(
		context.Background(),
		c,
		m,
		Admin{Terminal: terminal, Why: "install Homebrew"},
		func(helper string) error {
			ran = true
			if helper != "" {
				t.Errorf("a SUDO_ASKPASS helper was made where sudo asks for Touch ID: %q", helper)
			}
			return nil
		},
	)
	if err != nil || !ran {
		t.Fatalf("the apply did not run (ran %v): %v", ran, err)
	}
	if folders, _ := filepath.Glob(filepath.Join(temp, "wb-askpass-*")); len(folders) > 0 {
		t.Errorf("a helper folder was made: %v", folders)
	}
	recorded, _ := os.ReadFile(log)
	for _, want := range []string{"args: -n -v\n", "args: -v -p ", "args: -k\n"} {
		if !strings.Contains(string(recorded), want) {
			t.Errorf("sudo was not run with %q; log:\n%s", want, recorded)
		}
	}
	if strings.Contains(string(recorded), "args: -k -S") ||
		strings.Contains(string(recorded), "SUDO_ASKPASS") {
		t.Errorf("sudo was given a password channel; log:\n%s", recorded)
	}
}
