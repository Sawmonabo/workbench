package guard

// This file keeps the ancestry store: where each process of a tracked
// command's or unit's tree was seen, and which recorded process it came from,
// so that work such a tree leaves behind can be traced back to it, and so
// that whatever is found not to be that tree's work takes with it everything
// it started (see ancestry.drop).

import (
	"cmp"
	"slices"
	"time"
)

const (
	// versionsKept is how long a process's version is kept after it is last
	// seen: a child the init process adopts is found within a tick or two, so
	// a minute covers a loaded machine without keeping versions without end.
	versionsKept = time.Minute
	// versionsLimit is the most versions kept, the oldest dropped first: at
	// about 200 bytes each, 16,384 of them take about 3 MiB of the watcher's
	// 32 MiB limit, and at 1,400 processes recorded each tick they still hold
	// several seconds of history, more than an adoption needs.
	versionsLimit = 1 << 14
)

// origin is what the store keeps of one recorded process: the call or unit
// whose tree it was last seen in (owner), the recorded process that was its
// parent when it was first recorded (parent, set once, zero for none), the
// entries that name it as their parent (kids), and, where the kernel keeps
// versions, every version recorded for it.
type origin struct {
	owner    owner
	parent   procKey
	kids     []procKey
	versions []uint32
}

// seenVersion is a recorded version's process and when it was last seen.
type seenVersion struct {
	key procKey
	at  time.Time
}

// ancestry is the store. An entry stays while its process runs; once it is
// gone, while one of its versions is kept (versionsKept, versionsLimit), or,
// where the kernel keeps no versions, not at all. An entry removed for age or
// bound hands its kids to its own parent, so a process that exits never
// breaks the chain from what it started to what started it. Use newAncestry.
type ancestry struct {
	keepsVersions bool
	entries       map[procKey]*origin
	versions      map[uint32]seenVersion
}

// newAncestry is an empty store; keepsVersions is whether the kernel keeps
// each process's version (platform.KnowsOriginalParent).
func newAncestry(keepsVersions bool) *ancestry {
	return &ancestry{
		keepsVersions: keepsVersions,
		entries:       map[procKey]*origin{},
		versions:      map[uint32]seenVersion{},
	}
}

// record records key as seen now in the tree of o: its owner becomes o, and
// version, unless 0, is kept as one of its versions, last seen now. A process
// recorded for the first time takes parent as its parent when parent is
// recorded; zero, or a parent never recorded, gives it none, as the shell of
// a noted command has, so no chain leads from a call into its agent. A
// process already recorded keeps the parent it was first given. It takes one
// process at a time, so a caller may record a process outside a tick.
func (a *ancestry) record(key procKey, o owner, parent procKey, version uint32, now time.Time) {
	e := a.entries[key]
	if e == nil {
		e = &origin{}
		if p := a.entries[parent]; p != nil && parent != key {
			e.parent = parent
			p.kids = append(p.kids, key)
		}
		a.entries[key] = e
	}
	e.owner = o
	if version == 0 || !a.keepsVersions {
		return
	}
	if seen, ok := a.versions[version]; !ok || seen.key != key {
		// A version names one process image for good, so another entry can
		// hold it only by a misread; the latest reading wins.
		a.forgetVersion(version)
		e.versions = append(e.versions, version)
	}
	a.versions[version] = seenVersion{key: key, at: now}
}

// ownerOf is the owner key was recorded under, when it is recorded.
func (a *ancestry) ownerOf(key procKey) (owner, bool) {
	if e := a.entries[key]; e != nil {
		return e.owner, true
	}
	return owner{}, false
}

// forker is the recorded process whose version is version, with its owner:
// the process that forked a child whose original parent's version this is.
func (a *ancestry) forker(version uint32) (procKey, owner, bool) {
	seen, ok := a.versions[version]
	if !ok {
		return procKey{}, owner{}, false
	}
	e := a.entries[seen.key]
	if e == nil {
		return procKey{}, owner{}, false
	}
	return seen.key, e.owner, true
}

// prune forgets, at now, the versions last seen more than versionsKept ago,
// then the oldest beyond versionsLimit, then every entry whose process is
// gone (runs reports false) and keeps no version.
func (a *ancestry) prune(runs func(procKey) bool, now time.Time) {
	for v, seen := range a.versions {
		if now.Sub(seen.at) > versionsKept {
			a.forgetVersion(v)
		}
	}
	if extra := len(a.versions) - versionsLimit; extra > 0 {
		type kept struct {
			v  uint32
			at time.Time
		}
		all := make([]kept, 0, len(a.versions))
		for v, seen := range a.versions {
			all = append(all, kept{v, seen.at})
		}
		slices.SortFunc(
			all,
			func(x, y kept) int { return cmp.Or(x.at.Compare(y.at), cmp.Compare(x.v, y.v)) },
		)
		for _, old := range all[:extra] {
			a.forgetVersion(old.v)
		}
	}
	for key, e := range a.entries {
		if len(e.versions) == 0 && !runs(key) {
			a.remove(key)
		}
	}
}

// forgetVersion forgets version v.
func (a *ancestry) forgetVersion(v uint32) {
	seen, ok := a.versions[v]
	if !ok {
		return
	}
	delete(a.versions, v)
	if e := a.entries[seen.key]; e != nil {
		e.versions = slices.DeleteFunc(e.versions, func(x uint32) bool { return x == v })
	}
}

// remove forgets the entry key alone, with its versions: its kids take its
// parent, or none when it had none.
func (a *ancestry) remove(key procKey) {
	e := a.entries[key]
	if e == nil {
		return
	}
	parent := a.entries[e.parent]
	if parent != nil {
		parent.kids = slices.DeleteFunc(parent.kids, func(k procKey) bool { return k == key })
	}
	for _, kid := range e.kids {
		if k := a.entries[kid]; k != nil {
			k.parent = procKey{}
			if parent != nil {
				k.parent = e.parent
				parent.kids = append(parent.kids, kid)
			}
		}
	}
	for _, v := range e.versions {
		delete(a.versions, v)
	}
	delete(a.entries, key)
}

// drop forgets key's entry, every entry whose chain of parents reaches it and
// their versions, and gives the processes it forgot, key's first when it is
// recorded. Nothing recorded is then tied to key or what it started.
func (a *ancestry) drop(key procKey) []procKey {
	e := a.entries[key]
	if e == nil {
		return nil
	}
	if parent := a.entries[e.parent]; parent != nil {
		parent.kids = slices.DeleteFunc(parent.kids, func(k procKey) bool { return k == key })
	}
	var dropped []procKey
	for next := []procKey{key}; len(next) > 0; {
		k := next[0]
		next = next[1:]
		d := a.entries[k]
		if d == nil {
			continue
		}
		next = append(next, d.kids...)
		for _, v := range d.versions {
			delete(a.versions, v)
		}
		delete(a.entries, k)
		dropped = append(dropped, k)
	}
	return dropped
}
