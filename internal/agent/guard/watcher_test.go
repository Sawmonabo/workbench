package guard

import (
	"bytes"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/Sawmonabo/workbench/internal/platform"
)

// The guard's own stop takes a detached unit only when what tied it to a
// call still stands and its root is settled. These rules live in the
// watcher, not in a pure function, so each row runs them on a small
// watcher: its log goes to a buffer and its state folder is a temporary
// one. The processes are made up, at PIDs no system hands out, so the
// platform reads the code makes itself find nothing; what the rules read
// beyond that comes from the snapshot and the facts given. Each row is a
// path to unauthorized execution: the guard stopping, as a call's work,
// what that call did not start, what turned the guard off, or a program it
// has not judged.
func TestWatcherKeepsWhatNoCallStartedOutOfItsOwnStop(t *testing.T) {
	const fake = 1 << 30 // beyond any PID a system hands out
	now := time.Now()
	agent := procKey{fake + 1, start(-3600)}
	call := "toolu_1"
	byCall := owner{ID: call, Agent: agent, Kind: claude}
	key := func(p proc) procKey { return procKey{p.PID, p.Start} }
	// orphan is a child of the init process, the watcher's user's.
	orphan := func(pid int, seconds float64, name string) proc {
		return process(fake+pid, 1, fake+pid, seconds, name)
	}
	world := func(procs ...proc) snapshot {
		return snapshotOf(append([]proc{
			process(1, 0, 1, -99999, "launchd"),
			process(agent.pid, fake, agent.pid, -3600, "claude"),
		}, procs...)...)
	}
	newWatcher := func(t *testing.T) (*watcher, *bytes.Buffer) { return fixtureWatcher(t, agent) }
	unit := func(w *watcher, rs ...proc) string { return fixtureUnit(w, agent, call, now, rs...) }
	factsOf := func(s snapshot, w *watcher, stamps map[int]stamp) facts {
		return facts{
			self:      0,
			lineage:   false, // ties by the process's own entry, on every system
			stamp:     func(pid int) stamp { return stamps[pid] },
			args:      func(int) []string { return nil },
			known:     w.known,
			protected: map[int]uint64{agent.pid: agent.start},
			origins:   w.ancestry,
			records:   w.records,
			below:     s.below(),
		}
	}
	ended := func(log *bytes.Buffer, why string) bool {
		return strings.Contains(log.String(), `"event":"ended"`) &&
			strings.Contains(log.String(), why)
	}

	// A program that turned the guard off is out for good, and so is all it
	// started: left recorded, a child it left behind would be tied to the call
	// by parent and stopped as the call's work.
	t.Run("aFinalNoAtFirstSightDropsWhatItStarted", func(t *testing.T) {
		w, log := newWatcher(t)
		off, middle, left := orphan(
			10,
			10,
			"python3",
		), orphan(
			11,
			11,
			"sh",
		), orphan(
			12,
			12,
			"sleep",
		)
		s := world(off, left)
		w.ancestry.record(key(off), byCall, procKey{}, 0, now)
		w.ancestry.record(key(middle), byCall, key(off), 0, now) // exited since
		id := unit(w, left)
		w.ancestry.record(key(left), owner{ID: id, Agent: agent, Kind: claude}, key(middle), 0, now)
		w.judgeUnits(s, factsOf(s, w, map[int]stamp{
			off.PID:  {Readable: true, Agent: agent.pid, Mode: "off"},
			left.PID: {Readable: true, Agent: agent.pid},
		}))
		if j := w.classified[key(off)]; !j.fixed {
			t.Fatalf("the program that turned the guard off is judged %+v, want a final no", j)
		}
		for _, p := range []proc{off, middle, left} {
			if _, ok := w.ancestry.ownerOf(key(p)); ok {
				t.Errorf("%d is still recorded", p.PID)
			}
		}
		if _, ok := w.tracked[id]; ok || !ended(log, whyTiedDropped) {
			t.Fatalf(
				"the unit it started is still tracked (%v) or ended without %q",
				ok,
				whyTiedDropped,
			)
		}
	})

	// A nested session recorded in the call's tree before it was known: once
	// protected, what it started is its own, not the call's.
	t.Run("aRecordedProcessThatBecomesProtectedTakesItsTree", func(t *testing.T) {
		w, log := newWatcher(t)
		shell := process(fake+20, agent.pid, fake+20, 1, "zsh")
		session := process(fake+21, shell.PID, fake+21, 2, "claude")
		left := orphan(23, 4, "sleep")
		s := world(shell, session, left)
		w.tracked[call] = command{
			Note: note{ToolUseID: call, AgentPID: agent.pid, AgentStart: agent.start, Kind: claude},
			Root: shell,
		}
		w.ancestry.record(key(shell), byCall, procKey{}, 0, now)
		w.ancestry.record(key(session), byCall, key(shell), 0, now)
		middle := procKey{fake + 22, start(3)} // exited since
		w.ancestry.record(middle, byCall, key(session), 0, now)
		id := unit(w, left)
		w.ancestry.record(key(left), owner{ID: id, Agent: agent, Kind: claude}, middle, 0, now)
		protected := map[int]uint64{agent.pid: agent.start, session.PID: session.Start}
		w.recordLineage(s, s.below(), protected)
		if _, ok := w.ancestry.ownerOf(key(session)); ok {
			t.Fatal("the protected session is still recorded")
		}
		if _, ok := w.tracked[id]; ok || !ended(log, whyTiedDropped) {
			t.Fatalf(
				"the unit it left is still tracked (%v) or ended without %q",
				ok,
				whyTiedDropped,
			)
		}
	})

	// Judging a root again decides what the guard may do with it: a verdict
	// that is lost leaves it unsettled for good, and a dry run that is lost
	// lets the guard's own stop signal work that asked to be left alone.
	t.Run("whatJudgingAgainDecidesIsWrittenBack", func(t *testing.T) {
		w, _ := newWatcher(t)
		root := orphan(30, 10, "node")
		s := world(root)
		id := unit(w, root)
		w.tracked[id].Detached.Roots[0].Pending = true
		w.ancestry.record(key(root), owner{ID: id, Agent: agent, Kind: claude}, procKey{}, 0, now)
		w.rejudgeRoots(s, factsOf(s, w, map[int]stamp{
			root.PID: {Readable: true, Agent: agent.pid, Mode: "dry-run"},
		}), true)
		u := w.tracked[id]
		if u.Detached.Roots[0].Pending || !u.Note.DryRun {
			t.Fatalf("Pending %v, dry run %v; want settled and a dry run",
				u.Detached.Roots[0].Pending, u.Note.DryRun)
		}
	})

	// A root that runs another program may be a session that has not said it
	// is an agent yet, or one that turned the guard off: it is not settled,
	// and its ten seconds start again.
	t.Run("aRootThatRunsAnotherProgramIsPendingAndYoungAgain", func(t *testing.T) {
		self := os.Getpid()
		p := proc{Process: platform.Process{PID: self, PPID: 1, Start: 1, Name: "test"}}
		r := detachedRoot{procRef: procRef{self, 1}, Since: now.Add(-time.Minute), Name: "test"}
		if platform.KnowsOriginalParent {
			l, ok := platform.ProcessLineage(self)
			if !ok {
				t.Fatal("cannot read this process's version")
			}
			r.Version = l.Version + 1
		} else {
			r.Name = "another"
		}
		followProgram(p, &r)
		if !r.Pending || rootHeld(r, time.Now()) != heldYoung {
			t.Fatalf(
				"Pending %v, held %q; want Pending and young",
				r.Pending,
				rootHeld(r, time.Now()),
			)
		}
	})

	// A root tied by parent whose setting cannot be read may have turned the
	// guard off: it joins its unit, or makes one, unsettled.
	t.Run("aRootWithNothingReadableIsPending", func(t *testing.T) {
		w, _ := newWatcher(t)
		first, joining := orphan(40, 10, "sleep"), orphan(41, 11, "sleep")
		s := world(first, joining)
		hidden := tie{Agent: agent, Kind: claude, Owner: call, Hidden: true}
		w.addRoot(s, first, hidden)
		id := unitID(procRef{first.PID, first.Start})
		hidden.Owner = id
		w.addRoot(s, joining, hidden)
		for i, r := range w.tracked[id].Detached.Roots {
			if !r.Pending {
				t.Errorf("root %d of the unit is settled, want Pending", i)
			}
		}
		if n := len(w.tracked[id].Detached.Roots); n != 2 {
			t.Fatalf("%d roots, want 2", n)
		}
	})

	// A session protected only by a stamp that names it must stay protected
	// across a handover from the new watcher's first tick: its first
	// protected set is what a person's stop at that tick leaves out.
	t.Run("aStampNamedAgentIsProtectedFromTheFirstTickAfterAHandover", func(t *testing.T) {
		w, _ := newWatcher(t)
		session := process(fake+60, agent.pid, fake+60, 5, "claude")
		s := world(session)
		w.inherited = &carried{StampAgents: []procRef{{session.PID, session.Start}}}
		w.adopt(s, nil)
		if _, protected := w.agents(s, nil); protected[session.PID] != session.Start {
			t.Fatal("the stamp-named session is not protected at the new watcher's first tick")
		}
	})

	// While the kernel does not answer where processes come from, no root can
	// be judged in what it runs now: the guard's own stop takes no unit.
	t.Run("withoutLineageTheGuardChoosesNoUnit", func(t *testing.T) {
		w, _ := newWatcher(t)
		// The root is this test's own process, so its program reads as the
		// one it was judged in: only the missing lineage may hold it.
		self := os.Getpid()
		root := proc{Process: platform.Process{PID: self, PPID: 1, Start: start(10), Name: "test"}}
		s := world(root)
		id := unit(w, root)
		r := &w.tracked[id].Detached.Roots[0]
		r.Version = programOf(root).version
		r.Args = programOf(root).args
		w.rejudgeRoots(
			s,
			facts{known: w.known, protected: map[int]uint64{agent.pid: agent.start}},
			false,
		)
		own, age := ownStop(w.tracked[id], now, func(detachedRoot) bool { return true })
		if chosen, _, ok := choose([]candidate{{c: own, size: 1 << 30, tracked: age}}); ok {
			t.Fatalf("chose %d while lineage cannot be read", chosen.c.Root.PID)
		}
	})
}

// fixtureWatcher is the small watcher the rows run on: agent is the one
// agent it knows, its log goes to the buffer it gives, and its state folder
// is a temporary one.
func fixtureWatcher(t *testing.T, agent procKey) (*watcher, *bytes.Buffer) {
	dir := t.TempDir()
	log := &bytes.Buffer{}
	return &watcher{
		f: folders{
			root: dir, notes: dir, killed: dir, agents: dir, part: "test",
			logger: slog.New(slog.NewJSONHandler(log, nil)),
		},
		tracked:     map[string]command{},
		usage:       map[string]*usage{},
		passed:      map[string]bool{},
		refusals:    map[string]int{},
		stoppedBy:   map[string]string{},
		records:     &records{},
		known:       map[procKey]kind{agent: claude},
		stamps:      map[procKey]cachedStamp{},
		stampAgents: map[procKey]bool{},
		classified:  map[procKey]judged{},
		ancestry:    newAncestry(true),
	}, log
}

// fixtureUnit tracks in w a unit of agent's call whose roots are rs, all
// settled and tracked for a minute at now, and gives its ID.
func fixtureUnit(w *watcher, agent procKey, call string, now time.Time, rs ...proc) string {
	d := &detached{Call: call}
	for _, r := range rs {
		d.Roots = append(d.Roots, detachedRoot{
			procRef: procRef{r.PID, r.Start}, Since: now.Add(-time.Minute), Name: r.Name,
		})
		w.classified[procKey{r.PID, r.Start}] = judged{v: isUnit}
	}
	id := unitID(d.Roots[0].procRef)
	w.tracked[id] = command{
		Note:     note{ToolUseID: id, AgentPID: agent.pid, AgentStart: agent.start, Kind: claude},
		Root:     rs[0],
		Detached: d,
	}
	w.usage[id] = &usage{Since: now}
	return id
}

// A root that execs a program of the same name may set WORKBENCH_GUARD=off in
// it, and so may a readable process below a root whose own environment is
// hidden: judged on the stamp kept for the program before, the root settles
// again and the guard may stop work that turned it off. Where the kernel
// keeps no versions the scan's stamp cache tells programs apart by name
// alone, so every stamp a tracked root's judgment reads is told apart by name
// and command line, and a root's own is read again whenever it runs another
// program. The processes that must read are this test's own and a copy of
// it, started with WORKBENCH_GUARD=off, that only waits (see childWaits).
func TestWatcherReadsTheStampOfARootThatRunsAnotherProgram(t *testing.T) {
	if os.Getenv(childWaits) != "" {
		time.Sleep(time.Minute)
		return
	}
	now := time.Now()
	agent := procKey{1<<30 + 1, start(-3600)}
	launchd := process(1, 0, 1, -99999, "launchd")
	agentProc := process(agent.pid, 1<<30, agent.pid, -3600, "claude")
	judge := func(w *watcher, s snapshot) {
		w.rejudgeRoots(s, facts{
			stamp:     func(pid int) stamp { return w.stampOf(s, pid, nil) },
			rootStamp: func(pid int) stamp { return w.rootStampOf(s, pid, nil) },
			args:      func(int) []string { return nil },
			known:     w.known,
			protected: map[int]uint64{agent.pid: agent.start},
			origins:   w.ancestry,
			records:   w.records,
			below:     s.below(),
		}, true)
	}
	// stale is a stamp kept for p's earlier program of the same name, under
	// the program the scan's cache would name it by.
	stale := func(w *watcher, p proc) stamp {
		before := program{name: p.Name}
		if platform.KnowsOriginalParent {
			before = program{version: programOf(p).version + 1}
		}
		st := stamp{Readable: true, Agent: agent.pid, CallShell: 424242}
		w.stamps[procKey{p.PID, p.Start}] = cachedStamp{st, before}
		return st
	}

	t.Run("itsOwnStamp", func(t *testing.T) {
		w, _ := fixtureWatcher(t, agent)
		root := proc{
			Process: platform.Process{PID: os.Getpid(), PPID: 1, Start: start(10), Name: "test"},
		}
		s := snapshotOf(launchd, agentProc, root)
		id := fixtureUnit(w, agent, "toolu_1", now, root)
		r := &w.tracked[id].Detached.Roots[0]
		// It was judged in an earlier program of the same name.
		r.Args = "test --before"
		if platform.KnowsOriginalParent {
			r.Version = programOf(root).version + 1
		}
		old := stale(w, root)
		w.ancestry.record(
			procKey{root.PID, root.Start},
			owner{ID: id, Agent: agent, Kind: claude},
			procKey{},
			0,
			now,
		)
		judge(w, s)
		if w.stamps[procKey{root.PID, root.Start}].stamp == old {
			t.Fatal("the root was judged in its new program on the stamp kept for the old one")
		}
	})

	// The root's environment is hidden (a made-up process, which no read
	// finds), so it is judged by its readable child's stamp, kept from the
	// child's earlier program; the child now runs one of the same name that
	// turned the guard off. The root is Pending, as a root is once it has
	// run another program (or, as addRoot leaves it, when nothing readable
	// was found for it): a made-up process gives no command line to tell its
	// programs apart by, so the row sets that state rather than relying on a
	// read that fails. Judged again, the root must leave.
	t.Run("theStampsBelowIt", func(t *testing.T) {
		w, log := fixtureWatcher(t, agent)
		root := process(1<<30+50, 1, 1<<30+50, 10, "sh")
		cmd := exec.Command(
			os.Args[0],
			"-test.run=^TestWatcherReadsTheStampOfARootThatRunsAnotherProgram$",
		)
		cmd.Env = []string{childWaits + "=1", modeVariable + "=off"}
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
		child := proc{Process: platform.Process{
			PID: cmd.Process.Pid, PPID: root.PID, Start: start(20), Name: "guard.test",
		}}
		s := snapshotOf(launchd, agentProc, root, child)
		id := fixtureUnit(w, agent, "toolu_1", now, root)
		w.tracked[id].Detached.Roots[0].Pending = true
		stale(w, child)
		w.ancestry.record(
			procKey{root.PID, root.Start},
			owner{ID: id, Agent: agent, Kind: claude},
			procKey{},
			0,
			now,
		)
		// The copy's environment must be readable before it is judged.
		for deadline := time.Now().Add(5 * time.Second); !readStamp(child.PID).Readable; {
			if time.Now().After(deadline) {
				t.Fatal("the waiting copy's environment never read")
			}
			time.Sleep(10 * time.Millisecond)
		}
		judge(w, s)
		if u, ok := w.tracked[id]; ok || !strings.Contains(log.String(), whyOff) {
			pending := ok && len(u.Detached.Roots) > 0 && u.Detached.Roots[0].Pending
			kept := w.stamps[procKey{child.PID, child.Start}]
			t.Fatalf("the root stayed in its unit, Pending %v (false: judged again and settled; "+
				"true: not judged again, or nothing readable found); "+
				"the child's stamp is kept for %+v, it runs %+v now (by programOf), its mode %q",
				pending, kept.program, programOf(child), kept.Mode)
		}
	})
}

// childWaits, set in a copy of the test binary's environment, makes that copy
// only wait, as a readable process whose environment the test chooses.
const childWaits = "WORKBENCH_GUARD_TEST_CHILD_WAITS"
