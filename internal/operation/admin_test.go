package operation

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Prevent unauthorized root access: a sudo ticket Workbench created for an apply
// and left behind would let any later command in that terminal run as root with
// no password for five minutes. So the ticket is dropped with sudo -k whenever
// the apply ends, by success, error, interrupt or panic, and only a ticket
// Workbench created: dropping one the person already had would end their own sudo
// session. A stand-in script plays sudo; its log shows what was run.
func TestWithAdminDropsOnlyTheTicketItCreated(t *testing.T) {
	directory := t.TempDir()
	log, ticket := filepath.Join(directory, "log"), filepath.Join(directory, "ticket")
	script := "#!/bin/sh\n" +
		`echo "$*" >> ` + log + "\n" +
		`case "$1 $2" in` + "\n" +
		`"-n -v") [ -e ` + ticket + ` ];;` + "\n" +
		`"-v -p") : > ` + ticket + `;;` + "\n" +
		`"-k ") rm -f ` + ticket + `;;` + "\n" +
		"esac\n"
	stand := filepath.Join(directory, "sudo")
	if err := os.WriteFile(stand, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	previous := sudoPath
	sudoPath = stand
	t.Cleanup(func() { sudoPath = previous })
	terminal, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = terminal.Close() })
	c := Context{Scope: Scope{Kind: "machine", Root: directory}}
	m := &Mutation{context: c, active: true}

	for name, test := range map[string]struct {
		existing bool
		run      func(cancel func()) error
		wantDrop bool
	}{
		"success":          {run: func(func()) error { return nil }, wantDrop: true},
		"error":            {run: func(func()) error { return errors.New("failed") }, wantDrop: true},
		"panic":            {run: func(func()) error { panic("boom") }, wantDrop: true},
		"interrupt":        {run: func(cancel func()) error { cancel(); return context.Canceled }, wantDrop: true},
		"existing ticket":  {existing: true, run: func(func()) error { return nil }},
		"existing, failed": {existing: true, run: func(func()) error { return errors.New("failed") }},
	} {
		t.Run(name, func(t *testing.T) {
			_ = os.Remove(log)
			_ = os.Remove(ticket)
			if test.existing {
				if err := os.WriteFile(ticket, nil, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			func() {
				defer func() { _ = recover() }()
				_ = WithAdmin(
					ctx,
					c,
					m,
					Admin{Terminal: terminal, Why: "install apps"},
					func() error {
						return test.run(cancel)
					},
				)
			}()
			recorded, _ := os.ReadFile(log)
			want := 0
			if test.wantDrop {
				want = 1
			}
			if dropped := strings.Count(string(recorded), "-k\n"); dropped != want {
				t.Fatalf("sudo -k ran %d times, want %d; log:\n%s", dropped, want, recorded)
			}
			_, statErr := os.Stat(ticket)
			if test.wantDrop == (statErr == nil) {
				t.Fatalf("ticket left in the wrong state after the apply: %v", statErr)
			}
		})
	}
}
