package guard

import (
	"slices"
	"testing"
	"time"
)

// A nested session is recorded in the outer call's tree until it is known,
// with what it started. Once it is known, that work is the session's: if
// anything recorded of it survived the drop, a process it left behind would
// be tied by parent to the outer call, and the guard could stop it as that
// call's work, which is unauthorized execution against an agent session. The
// tie runs through processes that exit (the middle of a double fork) and
// through versions recorded before a process exec'd, so the drop must follow
// both. A nested session's own calls are its own: dropping them with it
// would leave its work tied to nothing, so they must stay.
func TestDropTakesWhatAProcessStarted(t *testing.T) {
	t0 := time.Now()
	agent := procKey{100, start(-3600)}
	nested := procKey{150, start(-1000)}
	byOuter := owner{ID: "toolu_outer", Agent: agent, Kind: claude}
	byNested := owner{ID: "toolu_nested", Agent: nested, Kind: claude}
	shell := procKey{200, start(1)}
	session := procKey{210, start(2)}
	middle := procKey{220, start(3)}
	sleeper := procKey{230, start(4)}
	helper := procKey{240, start(5)}
	nestedShell := procKey{300, start(6)}
	nestedChild := procKey{310, start(7)}
	for _, keepsVersions := range []bool{true, false} {
		a := newAncestry(keepsVersions)
		a.record(shell, byOuter, procKey{}, 1, t0)
		a.record(session, byOuter, shell, 2, t0)
		// The middle of a double fork, recorded once, then gone.
		a.record(middle, byOuter, session, 3, t0)
		a.record(sleeper, byOuter, middle, 4, t0)
		// A helper recorded before and after it exec'd.
		a.record(helper, byOuter, session, 10, t0.Add(50*time.Second))
		a.record(helper, byOuter, session, 11, t0.Add(55*time.Second))
		// The session's own call: its shell has no parent entry.
		a.record(nestedShell, byNested, procKey{}, 20, t0.Add(55*time.Second))
		a.record(nestedChild, byNested, nestedShell, 21, t0.Add(55*time.Second))
		// The middle is gone and its version too old, so it leaves the store.
		a.prune(func(k procKey) bool { return k != middle }, t0.Add(61*time.Second))
		if _, ok := a.ownerOf(middle); ok {
			t.Fatalf("keepsVersions %v: the gone middle is still recorded", keepsVersions)
		}
		dropped := a.drop(session)
		for _, key := range []procKey{session, sleeper, helper} {
			if !slices.Contains(dropped, key) {
				t.Errorf("keepsVersions %v: drop kept %v (dropped %v)", keepsVersions, key, dropped)
			}
			if _, ok := a.ownerOf(key); ok {
				t.Errorf("keepsVersions %v: %v is still recorded", keepsVersions, key)
			}
		}
		for _, v := range []uint32{2, 4, 10, 11} {
			if _, _, ok := a.forker(v); ok {
				t.Errorf("keepsVersions %v: version %d still ties a child", keepsVersions, v)
			}
		}
		for _, key := range []procKey{shell, nestedShell, nestedChild} {
			if _, ok := a.ownerOf(key); !ok {
				t.Errorf("keepsVersions %v: drop took %v", keepsVersions, key)
			}
		}
		if _, o, ok := a.forker(21); keepsVersions && (!ok || o != byNested) {
			t.Errorf("the nested call's child no longer ties to its call: %v %v", o, ok)
		}
	}
}
