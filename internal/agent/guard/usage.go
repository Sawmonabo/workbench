package guard

import (
	"cmp"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/Sawmonabo/workbench/internal/platform"
)

// usage is a tracked command's memory, measured each tick over the processes
// a stop would signal, kept for as long as it is tracked and handed over as it
// is: the graph of its whole run in at most graphBars buckets, oldest first,
// each the largest of Per readings (Filled of them in the newest bucket so
// far), the latest reading, the largest it has had, which is always in the
// graph, and when it was first tracked. Processes are the processes it has
// had, by PID and start, each with its latest name and its peak resident size:
// after each cut (see cut) those that run and the largest of those that have
// exited, at most processesKept in all unless more run; Unlisted counts the
// ones cut, each once, and Outside are the ones cut while they still ran,
// only when more than processesCut ran at once, so they are not taken for new
// processes while they run. The zero value, or one with only Since, is a
// command not measured yet.
type usage struct {
	Buckets   []uint64                 `json:"buckets"`
	Per       int                      `json:"per"`
	Filled    int                      `json:"filled"`
	Last      uint64                   `json:"last"`
	Peak      uint64                   `json:"peak"`
	Since     time.Time                `json:"since"`
	Processes map[procKey]*ProcessPeak `json:"processes"`
	Unlisted  int                      `json:"unlisted"`
	Outside   map[procKey]bool         `json:"outside,omitempty"`
}

// add records one reading: in the newest bucket until it holds Per readings,
// else in a new one. When graphBars buckets are full, neighbors merge into
// the larger first (see mergeNeighbors) and Per doubles, so the graph covers
// the whole run.
func (u *usage) add(size uint64) {
	u.Last, u.Peak = size, max(u.Peak, size)
	per := max(u.Per, 1)
	if n := len(u.Buckets); n > 0 && u.Filled < per {
		u.Buckets[n-1] = max(u.Buckets[n-1], size)
		u.Filled++
		return
	}
	if len(u.Buckets) >= graphBars {
		u.Buckets, per = mergeNeighbors(u.Buckets[:0], u.Buckets), per*2
	}
	u.Per, u.Filled = per, 1
	u.Buckets = append(u.Buckets, size)
}

// processesCut is how many processes a command may hold before cut drops the
// smallest of those that have exited: a quarter more than processesKept, so
// the cut, which sorts them, runs once for every few thousand new processes,
// not at every tick.
const processesCut = processesKept + processesKept/4

// saw records the processes of one reading, tree with the resident sizes read
// for it by PID (a zombie, not read, counts 0): each one's peak, and its name
// as it is now, since a process that execs keeps its PID and start. A process
// cut before (see cut) is not taken again while it runs.
func (u *usage) saw(tree []proc, sizes map[int]uint64) {
	if u.Processes == nil {
		u.Processes = map[procKey]*ProcessPeak{}
	}
	var outside map[procKey]bool
	for _, p := range tree {
		key := procKey{p.PID, p.Start}
		size := sizes[p.PID]
		if kept := u.Processes[key]; kept != nil {
			kept.Peak = max(kept.Peak, size)
			if p.Name != "" && p.Name != kept.Name {
				kept.Name = strings.Clone(p.Name)
			}
			continue
		}
		if u.Outside[key] {
			if outside == nil {
				outside = map[procKey]bool{}
			}
			outside[key] = true
			continue
		}
		// A clone, so the name never holds on to the text it was read from.
		u.Processes[key] = &ProcessPeak{PID: p.PID, Name: strings.Clone(p.Name), Peak: size}
	}
	if len(u.Processes) > processesCut {
		outside = u.cut(tree, outside)
	}
	u.Outside = outside
}

// cut drops the processes that have exited, those not in tree, with the
// smallest peaks, so the processes kept are those that run and the largest of
// those that have exited, at most processesKept in all unless more run: a
// process still running may yet grow past every other, so it is never judged
// by the peak it has so far. Only when more than processesCut run at once does
// it keep the processesKept largest of all, and adds those of tree it drops to
// outside, which it gives. Each process dropped counts once in Unlisted.
func (u *usage) cut(tree []proc, outside map[procKey]bool) map[procKey]bool {
	type entry struct {
		key  procKey
		peak uint64
	}
	live := make(map[procKey]bool, len(tree))
	for _, p := range tree {
		live[procKey{p.PID, p.Start}] = true
	}
	crowded := len(tree) > processesCut
	entries := make([]entry, 0, len(u.Processes))
	running := 0
	for key, p := range u.Processes {
		if !crowded && live[key] {
			running++
			continue
		}
		entries = append(entries, entry{key, p.Peak})
	}
	slices.SortFunc(entries, func(a, b entry) int {
		return cmp.Or(cmp.Compare(b.peak, a.peak), cmp.Compare(a.key.pid, b.key.pid),
			cmp.Compare(a.key.start, b.key.start))
	})
	for _, e := range entries[min(max(processesKept-running, 0), len(entries)):] {
		delete(u.Processes, e.key)
		u.Unlisted++
		if live[e.key] {
			if outside == nil {
				outside = map[procKey]bool{}
			}
			outside[e.key] = true
		}
	}
	return outside
}

// peaks are the processes kept, the largest peak first, at most
// processesKept of them, and how many the command had that are not among
// them. Those it leaves out leave memory too: the list holds no more than it
// shows, so a finished command takes what its size counts.
func (u *usage) peaks() ([]ProcessPeak, int) {
	all := make([]ProcessPeak, 0, len(u.Processes))
	for _, p := range u.Processes {
		all = append(all, *p)
	}
	kept, left := keptProcesses(all)
	if left > 0 {
		kept = slices.Clone(kept)
	}
	return kept, u.Unlisted + left
}

// measure adds each tracked command's size this tick to its usage, with the
// peak of each of its processes: the resident sizes over the tree a stop would
// signal, without protected processes, a unit's over the trees of its live
// roots. A command that no longer runs is left for forget. It gives each
// measured command's tree with the sizes it read.
func (w *watcher) measure(
	s snapshot,
	below parents,
	protected map[int]uint64,
) map[string][]ProcessView {
	skip := outside(protected)
	trees := map[string][]ProcessView{}
	for id, c := range w.tracked {
		u := w.usage[id]
		if u == nil || !live(s, c) {
			continue
		}
		tree, sizes := s.commandTreeIn(below, c, skip), map[int]uint64{}
		u.add(w.size(tree, residentInto(sizes)))
		u.saw(tree, sizes)
		w.largestPeak = max(w.largestPeak, u.Peak)
		trees[id] = treeView(tree, sizes)
	}
	return trees
}

// size is the sum of read over the processes, zombies left out. A process
// whose size cannot be read, for a reason other than its exit, counts as 0
// and is logged once, as a warning: the system keeps the size of a program
// that runs as another user (a setuid one) to itself, and the guard goes on
// with the rest of the tree.
func (w *watcher) size(procs []proc, read func(pid int) (uint64, error)) uint64 {
	var total uint64
	for _, p := range procs {
		if p.Zombie {
			continue
		}
		bytes, err := read(p.PID)
		key := procKey{p.PID, p.Start}
		if err != nil && !platform.Exited(err) && !w.unsized[key] {
			w.unsized[key] = true
			w.f.event(slog.LevelWarn, EventError, "cannot read a process's size (counted as 0)",
				"process", p.PID, "error", err)
		}
		total += bytes
	}
	return total
}
