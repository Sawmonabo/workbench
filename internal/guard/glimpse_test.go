package guard

import (
	"os/exec"
	"syscall"
	"testing"
	"time"

	"github.com/Sawmonabo/workbench/internal/platform"
)

// A call's shell can turn out to be an agent: a stamp names it as one, or it
// exec'd into a session. The tick then leaves it out of the call's tree with
// everything under it and drops what was recorded of it, so that the
// session's work is never the call's. If the glimpse went on recording that
// shell's children under the call, they would have no parent for a later
// drop to reach, and work the nested agent left behind would be tied to the
// outer call, which the guard could then stop as that call's: unauthorized
// execution against an agent session.
func TestGlimpseRecordsNothingUnderAProtectedShell(t *testing.T) {
	shell := exec.Command("/bin/sh", "-c", "/bin/sleep 30 & wait")
	if err := shell.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		for _, child := range platform.Children(shell.Process.Pid) {
			if p, err := platform.ProcessInfo(child); err == nil {
				platform.Signal(p, false, syscall.SIGKILL)
			}
		}
		_ = shell.Process.Kill()
		_ = shell.Wait()
	})
	var children []int
	for deadline := time.Now().Add(5 * time.Second); len(children) == 0; {
		if time.Now().After(deadline) {
			t.Fatal("the shell started no child")
		}
		time.Sleep(20 * time.Millisecond)
		children = platform.Children(shell.Process.Pid)
	}
	info, err := platform.ProcessInfo(shell.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}
	child, err := platform.ProcessInfo(children[0])
	if err != nil {
		t.Fatal(err)
	}
	waiter := platform.NewWaiter()
	t.Cleanup(waiter.Close)
	w := &watcher{
		ancestry:    newAncestry(platform.KnowsOriginalParent),
		known:       map[procKey]kind{},
		stampAgents: map[procKey]bool{},
		records:     &records{},
		waiter:      waiter,
		glimpse:     newGlimpse(),
	}
	shellKey := procKey{info.PID, info.Start}
	sh := &shellWatch{
		key:   shellKey,
		owner: owner{ID: "toolu_C", Agent: procKey{1, 1}, Kind: claude},
	}
	w.recordGlimpsed(shellKey, sh.owner, procKey{})
	w.glimpse.shells[shellKey.pid] = sh

	// The tick finds a stamp naming the shell as an agent: it protects the
	// shell and drops what was recorded of it.
	w.stampAgents[shellKey] = true
	w.ancestry.drop(shellKey)

	w.shellChanged(sh, true, true)
	if o, recorded := w.ancestry.ownerOf(procKey{child.PID, child.Start}); recorded {
		t.Fatalf("the protected shell's child was recorded under %q", o.ID)
	}
	if _, recorded := w.ancestry.ownerOf(shellKey); recorded {
		t.Fatal("the protected shell was recorded again")
	}
	if _, followed := w.glimpse.shells[shellKey.pid]; followed {
		t.Fatal("the protected shell is still followed as the call's")
	}
}
