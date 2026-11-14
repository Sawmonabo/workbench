package guard

import (
	"testing"
	"time"

	"github.com/Sawmonabo/workbench/internal/platform"
)

// A detached unit is a candidate the guard stops when memory runs out, and
// nothing ties it to an agent but what unitRootWhy reads. Taking for a unit a
// program no agent command forked (an app opened from the Dock, a daemon a
// hook started, a service the init process runs, another user's program, a
// stamp naming a dead, unknown or younger process, a nested session's
// inherited stamp) or an agent itself, or Workbench, or a protected process,
// would let the guard kill the user's work or session: the one catastrophic
// outcome this feature can have. Each row that must give no unit starts from
// a setup that is a unit and changes only the one thing it is about, so that
// thing alone is what keeps it out. Each such row also says whether its no is
// final (finalWhy): a final no is never judged again, so a reason that rests
// on evidence placed on the other side would let a process proven not this
// agent's command's work become a unit after its next exec.

// unitWorld is the snapshot and facts of one row, built fresh for each.
type unitWorld struct {
	procs     map[int]proc
	root      int
	lineage   bool
	lineages  map[int]platform.Lineage
	stamps    map[int]stamp
	known     map[procKey]kind
	protected map[int]uint64
	versions  map[uint32]owner
	seenUnder map[procKey]owner
	notes     []note
}

func (w *unitWorld) facts() (snapshot, proc, facts) {
	s := snapshot(w.procs)
	recs := &records{}
	for _, n := range w.notes {
		recs.add(n)
	}
	// The store holds each process seen in a tracked tree, and each recorded
	// version as that of a forker of its own, PID 900 and up.
	origins := newAncestry(true)
	for key, o := range w.seenUnder {
		origins.record(key, o, procKey{}, 0, time.Now())
	}
	forker := 900
	for v, o := range w.versions {
		origins.record(procKey{forker, start(-60)}, o, procKey{}, v, time.Now())
		forker++
	}
	return s, s[w.root], facts{
		self:    0,
		lineage: w.lineage,
		read: func(pid int) (platform.Lineage, bool) {
			l, ok := w.lineages[pid]
			return l, ok
		},
		stamp:     func(pid int) stamp { return w.stamps[pid] },
		args:      func(pid int) []string { return s[pid].Args },
		known:     w.known,
		protected: w.protected,
		origins:   origins,
		records:   recs,
		below:     s.below(),
	}
}

func TestUnitRootOnlyForWhatAnAgentCommandForked(t *testing.T) {
	agent := procKey{100, start(-3600)}
	nested := procKey{150, start(-1000)}
	codexAgent := procKey{200, start(-1800)}
	rootKey := procKey{500, start(10)}
	byCall := owner{ID: "toolu_1", Agent: agent, Kind: claude}

	// The base is a unit by stamp on macOS: node, forked by agent 100's
	// command (its original parent's version 4000 is no longer recorded), now
	// a child of launchd, carrying the call stamp of agent 100's shell 300.
	base := func() *unitWorld {
		return &unitWorld{
			procs: map[int]proc{
				1:   process(1, 0, 1, -99999, "launchd"),
				100: process(100, 1, 100, -3600, "claude"),
				150: process(150, 100, 150, -1000, "claude"),
				200: process(200, 1, 200, -1800, "codex"),
				500: process(500, 1, 500, 10, "node", "server.js"),
				700: process(700, 100, 700, -10, "tini"),
				800: process(800, 1, 800, -5, "bash"),
			},
			root:    500,
			lineage: true,
			lineages: map[int]platform.Lineage{
				1:   {Version: 1, ParentVersion: 0},
				500: {Version: 5000, ParentVersion: 4000},
				700: {Version: 7000, ParentVersion: 6000},
				800: {Version: 8000, ParentVersion: 1},
			},
			stamps: map[int]stamp{
				500: {
					Readable:   true,
					Agent:      100,
					CallAgent:  100,
					CallShell:  300,
					CallSecond: 1_000_005,
					Mode:       "dry-run",
				},
			},
			known:     map[procKey]kind{agent: claude, nested: claude, codexAgent: codex},
			protected: map[int]uint64{100: agent.start, 150: nested.start, 200: codexAgent.start},
			versions:  map[uint32]owner{},
			seenUnder: map[procKey]owner{},
		}
	}
	// byParent makes the base a unit by its recorded original parent alone:
	// its environment holds no stamp.
	byParent := func(w *unitWorld) {
		w.stamps[500] = stamp{Readable: true}
		w.versions[4000] = byCall
	}

	byStamp := tie{
		Agent:   agent,
		Kind:    claude,
		Version: 5000,
		Shell:   300,
		Second:  1_000_005,
		Mode:    "dry-run",
	}
	// The forker the base's one recorded version is given (see facts).
	forker := procKey{900, start(-60)}
	for _, tc := range []struct {
		name    string
		change  func(*unitWorld)
		want    tie
		verdict verdict
		final   bool // a no unit that is final for the life of the process (finalWhy)
	}{
		{"byStamp", func(*unitWorld) {}, byStamp, isUnit, false},
		{"byRecordedParentVersion", byParent, tie{
			Agent: agent, Kind: claude, Owner: "toolu_1", Parent: forker, Version: 5000,
		}, isUnit, false},
		// Tied by parent, but its environment and all below it are hidden, so
		// it may have turned the guard off: Hidden keeps it Pending, which the
		// guard's own stop never takes (TestGuardChoosesOnlyWhatACallProvablyStarted).
		{"byRecordedParentWithNothingReadable", func(w *unitWorld) {
			byParent(w)
			w.stamps[500] = stamp{}
		}, tie{
			Agent: agent, Kind: claude, Owner: "toolu_1", Parent: forker, Version: 5000, Hidden: true,
		}, isUnit, false},
		{"bySeenUnderWithoutLineage", func(w *unitWorld) {
			w.lineage = false
			w.lineages = nil
			w.stamps[500] = stamp{Readable: true}
			w.seenUnder[rootKey] = byCall
		}, tie{Agent: agent, Kind: claude, Owner: "toolu_1"}, isUnit, false},
		// A Codex session a Claude Code command started: its command's
		// processes carry the outer agent's stamps and the Codex call's ID,
		// and the nearer agent, Codex, owns the unit.
		{"byCodexIDUnderAnOuterAgent", func(w *unitWorld) {
			w.notes = []note{{ToolUseID: "call_abc", AgentPID: 200, AgentStart: codexAgent.start, Kind: codex}}
			st := w.stamps[500]
			st.Codex = "call_abc"
			w.stamps[500] = st
		}, tie{Agent: codexAgent, Kind: codex, Version: 5000, Codex: "call_abc", Mode: "dry-run"}, isUnit, false},
		// A root whose environment is hidden is read through the first
		// readable process below it.
		{"byStampBelowAnUnreadableRoot", func(w *unitWorld) {
			w.procs[501] = process(501, 500, 500, 11, "sudo")
			w.procs[502] = process(502, 501, 500, 12, "node", "worker.js")
			w.stamps[502] = w.stamps[500]
			w.stamps[500] = stamp{}
		}, byStamp, isUnit, false},

		{"noStampAndNoRecordedParent", func(w *unitWorld) {
			w.stamps[500] = stamp{Readable: true}
		}, tie{}, notUnit, false},
		{"stampNamesADeadProcess", func(w *unitWorld) {
			delete(w.procs, 100)
		}, tie{}, notUnit, false},
		{"stampNamesAnUnknownProcess", func(w *unitWorld) {
			delete(w.known, agent)
		}, tie{}, notUnit, false},
		{"stampNamesAProcessYoungerThanTheRoot", func(w *unitWorld) {
			young := procKey{100, start(20)}
			w.procs[100] = process(100, 1, 100, 20, "claude")
			delete(w.known, agent)
			w.known[young] = claude
			w.protected[100] = young.start
		}, tie{}, notUnit, false},
		// An app opened with `open` is started by launchd: its original
		// parent's version is launchd's own, whatever it carries.
		{"initStartedCarryingAStamp", func(w *unitWorld) {
			w.lineages[500] = platform.Lineage{Version: 5000, ParentVersion: 1}
		}, tie{}, notUnit, true},
		// A daemon a hook started carries CLAUDE_PID but no call stamp.
		{"agentStampWithoutTheCallStamp", func(w *unitWorld) {
			w.stamps[500] = stamp{Readable: true, Agent: 100, Mode: "dry-run"}
		}, tie{}, notUnit, false},
		// A nested session's processes carry their own CLAUDE_PID beside the
		// outer command's call stamp.
		{"callStampAgentDiffersFromCLAUDE_PID", func(w *unitWorld) {
			st := w.stamps[500]
			st.Agent = 150
			w.stamps[500] = st
		}, tie{}, notUnit, false},
		// On Linux a service the init process starts can carry a command's
		// variables, so a stamp ties nothing there.
		{"validStampWithoutLineageNorSeenUnder", func(w *unitWorld) {
			w.lineage = false
		}, tie{}, notUnit, false},
		{"rootIsAKnownAgentTiedByStamp", func(w *unitWorld) {
			w.known[rootKey] = claude
		}, tie{}, notUnit, true},
		{"rootIsAKnownAgentTiedByParent", func(w *unitWorld) {
			byParent(w)
			w.known[rootKey] = claude
		}, tie{}, notUnit, true},
		{"aWorkbenchProcess", func(w *unitWorld) {
			w.procs[500] = process(500, 1, 500, 10, "/opt/workbench/bin/workbench", "guard", "watch")
		}, tie{}, notUnit, true},
		{"aProtectedProcess", func(w *unitWorld) {
			w.protected[500] = rootKey.start
		}, tie{}, notUnit, true},
		// tini, an init by name, runs under the protected agent 100.
		{"aProcessUnderAProtectedOne", func(w *unitWorld) {
			r := w.procs[500]
			r.PPID = 700
			w.procs[500] = r
		}, tie{}, notUnit, true},
		{"aZombieRoot", func(w *unitWorld) {
			w.procs[500] = zombie(w.procs[500])
		}, tie{}, notUnit, false},
		// A child of a running bash is still under its parent, not detached.
		{"aParentThatIsNotAnInit", func(w *unitWorld) {
			r := w.procs[500]
			r.PPID = 800
			w.procs[500] = r
		}, tie{}, notUnit, false},
		{"tieByParentToADeadAgent", func(w *unitWorld) {
			byParent(w)
			delete(w.procs, 100)
		}, tie{}, notUnit, false},
		{"tieByParentToAnUnknownAgent", func(w *unitWorld) {
			byParent(w)
			delete(w.known, agent)
		}, tie{}, notUnit, false},
		{"lineageKnownButUnread", func(w *unitWorld) {
			w.lineages = nil
		}, tie{}, notUnit, false},
		{"parentsLineageUnread", func(w *unitWorld) {
			delete(w.lineages, 1)
		}, tie{}, notUnit, false},
		// A daemon a hook started carries CLAUDE_PID without the call stamp;
		// a Claude Code call's ID in CODEX_TOOL_CALL_ID must not stand in for
		// that call stamp.
		{"codexStampResolvingToAClaudeCall", func(w *unitWorld) {
			w.notes = []note{{ToolUseID: "toolu_1", AgentPID: 100, AgentStart: agent.start, Kind: claude}}
			w.stamps[500] = stamp{Readable: true, Agent: 100, Codex: "toolu_1"}
		}, tie{}, notUnit, false},
		{"anotherUsersProcess", func(w *unitWorld) {
			r := w.procs[500]
			r.UID = 501
			w.procs[500] = r
		}, tie{}, notUnit, true},
		{"guardOff", func(w *unitWorld) {
			st := w.stamps[500]
			st.Mode = "off"
			w.stamps[500] = st
		}, tie{}, notUnit, true},
		// Forked by agent 100's command but carrying the nested agent 150's
		// CLAUDE_PID: it is that agent's, judged by that agent's stamps.
		{"tieByParentWhoseStampNamesAnotherAgent", func(w *unitWorld) {
			byParent(w)
			w.stamps[500] = stamp{Readable: true, Agent: 150}
		}, tie{}, notUnit, true},
		// Forked by agent 100's command but carrying a call ID of the Codex
		// agent 200: it is that agent's.
		{"tieByParentWhoseCodexIDNamesAnotherAgent", func(w *unitWorld) {
			byParent(w)
			w.notes = []note{{ToolUseID: "call_abc", AgentPID: 200, AgentStart: codexAgent.start, Kind: codex}}
			w.stamps[500] = stamp{Readable: true, Codex: "call_abc"}
		}, tie{}, notUnit, true},

		{"unreadableWithNothingReadableBelow", func(w *unitWorld) {
			w.stamps[500] = stamp{}
		}, tie{}, unread, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := base()
			tc.change(w)
			s, r, f := w.facts()
			got, v, why := unitRootWhy(s, r, f)
			if v != tc.verdict {
				t.Fatalf("verdict %v, want %v (tie %+v)", v, tc.verdict, got)
			}
			if v != isUnit && finalWhy(why) != tc.final {
				t.Fatalf("final %v for %q, want %v", finalWhy(why), why, tc.final)
			}
			if v == isUnit && got != tc.want {
				t.Fatalf("tie %+v, want %+v", got, tc.want)
			}
		})
	}
}

// The guard stops what choose gives without asking anyone. A unit whose
// stamp resolved to no call may be work a person started with `!`, which
// carries the same stamp as an agent's command; a unit tracked for only a
// few seconds may be a session that has not yet said it is an agent; a unit
// whose root is not settled (Pending) may run a program that turned the guard
// off, either because its new program was not judged yet or because its
// setting cannot be read at all. The guard choosing any of them on its own
// would be unauthorized execution: it would stop a person's program, an agent
// session or work that opted out. A person may still stop them.
func TestGuardChoosesOnlyWhatACallProvablyStarted(t *testing.T) {
	const gb = uint64(1) << 30
	now := time.Now()
	// A unit's candidate is made as stopLargest makes it, through ownStop.
	unit := func(pid int, call string, size uint64, tracked time.Duration, pending bool) candidate {
		c, age := ownStop(command{
			Root: process(pid, 1, pid, float64(pid), "node"),
			Detached: &detached{Roots: []detachedRoot{
				{
					procRef: procRef{
						pid,
						start(float64(pid)),
					},
					Since:   now.Add(-tracked),
					Pending: pending,
				},
			}, Call: call},
		}, now)
		return candidate{c: c, size: size, tracked: age}
	}
	noted := candidate{
		c:       command{Root: process(10, 100, 10, 10, "zsh")},
		size:    gb,
		tracked: time.Second,
	}
	withCall := unit(20, "toolu_1", 2*gb, time.Minute, false)
	withoutCall := unit(30, "", 8*gb, time.Minute, false)
	young := unit(40, "toolu_2", 9*gb, 3*time.Second, false)
	unsettled := unit(50, "toolu_3", 10*gb, time.Minute, true)

	chosen, total, ok := choose([]candidate{noted, withCall, withoutCall, young, unsettled})
	if !ok || chosen.c.Root.PID != 20 || total != 3*gb {
		t.Fatalf("chose %d of %d bytes (ok %v), want the 2 GB unit of 3 GB",
			chosen.c.Root.PID, total, ok)
	}
	if chosen, _, ok := choose([]candidate{withoutCall, young, unsettled}); ok {
		t.Fatalf("chose %d, want nothing", chosen.c.Root.PID)
	}
}
