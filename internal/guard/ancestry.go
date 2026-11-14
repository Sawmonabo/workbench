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
// process at a time, so a caller may record a process outside a tick. It
// reports whether key was recorded before.
func (a *ancestry) record(
	key procKey,
	o owner,
	parent procKey,
	version uint32,
	now time.Time,
) bool {
	e := a.entries[key]
	before := e != nil
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
		return before
	}
	if seen, ok := a.versions[version]; !ok || seen.key != key {
		// A version names one process image for good, so another entry can
		// hold it only by a misread; the latest reading wins.
		a.forgetVersion(version)
		e.versions = append(e.versions, version)
	}
	a.versions[version] = seenVersion{key: key, at: now}
	return before
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

// carriedOrigin is one entry of the store as a handover carries it to the
// next watcher (see carried): its process, owner and parent, and each of its
// versions with how long ago it was last seen, not when, so a clock set
// between the two watchers neither keeps a version longer nor drops it sooner.
type carriedOrigin struct {
	Process  procRef          `json:"process"`
	Owner    owner            `json:"owner"`
	Parent   *procRef         `json:"parent,omitempty"`
	Versions []carriedVersion `json:"versions,omitempty"`
}

// carriedVersion is a version and the milliseconds since it was last seen.
type carriedVersion struct {
	Version uint32 `json:"v"`
	AgeMS   int64  `json:"age_ms"`
}

// carry is every entry of the store as a handover carries it at now: no more
// than the store holds, which versionsLimit and the processes that run bound.
func (a *ancestry) carry(now time.Time) []carriedOrigin {
	out := make([]carriedOrigin, 0, len(a.entries))
	for key, e := range a.entries {
		c := carriedOrigin{Process: procRef{PID: key.pid, Start: key.start}, Owner: e.owner}
		if _, ok := a.entries[e.parent]; ok {
			c.Parent = &procRef{PID: e.parent.pid, Start: e.parent.start}
		}
		for _, v := range e.versions {
			age := now.Sub(a.versions[v].at).Milliseconds()
			c.Versions = append(c.Versions, carriedVersion{Version: v, AgeMS: age})
		}
		out = append(out, c)
	}
	return out
}

// adopt merges what a watcher handing over carried into the store at now. An
// entry the store does not hold is added with its owner; one it holds keeps
// its own. A parent is set only on an entry that has none, so a parent, once
// set, never changes, and never where it would close a loop. A version the
// store does not hold is added as last seen as long ago as was carried, one
// it holds for the same process keeps the later sighting, and one older than
// versionsKept is left out. A watcher that carries no store carries nothing,
// which changes nothing.
func (a *ancestry) adopt(carried []carriedOrigin, now time.Time) {
	for _, c := range carried {
		key := procKey{c.Process.PID, c.Process.Start}
		if _, ok := a.entries[key]; !ok {
			a.entries[key] = &origin{owner: c.Owner}
		}
	}
	for _, c := range carried {
		key := procKey{c.Process.PID, c.Process.Start}
		e := a.entries[key]
		if c.Parent != nil && e.parent == (procKey{}) {
			parent := procKey{c.Parent.PID, c.Parent.Start}
			if p := a.entries[parent]; p != nil && !a.reaches(parent, key) {
				e.parent = parent
				p.kids = append(p.kids, key)
			}
		}
		if !a.keepsVersions {
			continue
		}
		for _, v := range c.Versions {
			at := now.Add(-time.Duration(v.AgeMS) * time.Millisecond)
			if v.Version == 0 || now.Sub(at) > versionsKept {
				continue
			}
			switch seen, ok := a.versions[v.Version]; {
			case !ok:
				e.versions = append(e.versions, v.Version)
				a.versions[v.Version] = seenVersion{key: key, at: at}
			case seen.key == key && at.After(seen.at):
				a.versions[v.Version] = seenVersion{key: key, at: at}
			}
		}
	}
}

// reaches reports whether from, or a process on its chain of parents, is to.
func (a *ancestry) reaches(from, to procKey) bool {
	for seen := map[procKey]bool{}; from != (procKey{}) && !seen[from]; {
		if from == to {
			return true
		}
		seen[from] = true
		e := a.entries[from]
		if e == nil {
			return false
		}
		from = e.parent
	}
	return false
}
