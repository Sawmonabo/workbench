package guard

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/Sawmonabo/workbench/internal/platform"
)

// readAgents has the waiter wake at each agent's exit, and follow its forks
// where it can (see followAgents), and reads what matching needs of each (see
// loadArgs), noting the Codex agents seen setting call IDs of their own while
// they run. It forgets the known agents that are gone (see gone), with their
// calls' records.
func (w *watcher) readAgents(s snapshot, agents []int, notes []note) {
	codexAgents, inFolders := map[int]bool{}, map[int]bool{}
	for _, n := range notes {
		codexAgents[n.AgentPID] = codexAgents[n.AgentPID] || n.Kind == codex
		inFolders[n.AgentPID] = inFolders[n.AgentPID] || len(noteFolders(n)) > 0
	}
	for key := range w.setsCallIDs {
		if !s.alive(key.pid, key.start) {
			delete(w.setsCallIDs, key)
		}
	}
	// By gone, not by one reading: a record dropped is never read again, so
	// one absent reading must not drop it. An agent that exited but is still
	// in the process table, a zombie its parent has not reaped, stays known,
	// so it stays a boundary of every tree until it is gone (see agents).
	stays := func(key procKey) bool {
		return s.present(key.pid, key.start) || !w.gone(s, key.pid, key.start)
	}
	for key := range w.known {
		if !stays(key) {
			delete(w.known, key)
		}
	}
	w.records.prune(stays)
	for _, agent := range agents {
		w.waiter.Watch(agent)
		loadArgs(s, agent, codexAgents[agent], inFolders[agent])
		if !codexAgents[agent] {
			continue
		}
		for _, child := range s.children(agent) {
			if child.CallID != "" && child.CallID != s[agent].CallID {
				w.setsCallIDs[procKey{agent, s[agent].Start}] = true
			}
		}
	}
	w.followAgents(s, agents)
}

// agents are the live agents of the notes and tracked commands, whose
// arguments readAgents reads. protected is them, every other known agent,
// every process a stamp read names as its agent and that started before the
// stamped process (see stampOf), and the watcher, each PID with its start
// time: never signalled, nor anything under them. An agent is protected for
// as long as it is in the process table, a zombie its parent has not reaped
// included (see present), so it stays a boundary of every tree until it is
// gone. It reads the agents folder first (see readAgentFiles), so every path
// that decides what is protected sees a session that registered since the
// last read, and the known agents are filled before anything later in the
// tick reads them.
func (w *watcher) agents(s snapshot, notes []note) ([]int, map[int]uint64) {
	w.readAgentFiles(s)
	protected := map[int]uint64{}
	if self, ok := s[os.Getpid()]; ok {
		protected[self.PID] = self.Start
	}
	var agents []int
	add := func(pid int, start uint64) {
		if _, ok := protected[pid]; ok || !s.present(pid, start) {
			return
		}
		protected[pid] = start
		if s.alive(pid, start) {
			agents = append(agents, pid)
		}
	}
	for _, n := range notes {
		add(n.AgentPID, n.AgentStart)
	}
	for _, c := range w.tracked {
		add(c.Note.AgentPID, c.Note.AgentStart)
	}
	// After the agents above, which add would otherwise leave out of agents.
	for key := range w.known {
		if _, ok := protected[key.pid]; !ok && s.present(key.pid, key.start) {
			protected[key.pid] = key.start
		}
	}
	for key := range w.stampAgents {
		if !s.present(key.pid, key.start) {
			delete(w.stampAgents, key)
		} else if _, ok := protected[key.pid]; !ok {
			protected[key.pid] = key.start
		}
	}
	return agents, protected
}

// readAgentFiles reads the agents folder (see Session), each time agents
// builds the protected set. A file whose agent runs, with that start time,
// makes it known; one whose agent is gone (see gone) is removed, as is one
// that does not parse, does not match its name or names a kind other than
// claude or codex, which never heals. A file that cannot be read is left for
// a later read, and a folder that cannot be read leaves the agents known from
// their notes only.
func (w *watcher) readAgentFiles(s snapshot) {
	entries, err := listFolder(w.f.agents)
	if err != nil {
		return
	}
	for _, entry := range entries {
		path := filepath.Join(w.f.agents, entry.Name())
		data, err := readRecord(path)
		if err != nil {
			continue
		}
		var a agentFile
		pid, start, named := agentFileName(entry.Name())
		if !named || json.Unmarshal(data, &a) != nil || a.PID != pid || a.Start != start ||
			a.Kind != claude && a.Kind != codex || w.gone(s, a.PID, a.Start) {
			_ = os.Remove(path)
			continue
		}
		if s.alive(a.PID, a.Start) {
			w.known[procKey{a.PID, a.Start}] = a.Kind
		}
	}
}

// loadArgs reads the command lines matching looks at: the agent's, its direct
// children's and, below a sandbox launcher, what the launcher started. For a
// Codex agent it also reads the CallID of the agent and its direct children;
// no other part of any environment is kept. With folders, a note of the agent
// knows a folder, so it also reads each one's Folder; one it cannot read stays
// empty. Sizes need none of these, so the rest of the tree is not read.
func loadArgs(s snapshot, agent int, callIDs, folders bool) {
	seen := map[int]bool{}
	var read func(pid int, expand bool)
	read = func(pid int, expand bool) {
		p, ok := s[pid]
		if !ok || seen[pid] {
			return
		}
		seen[pid] = true
		p.Args = platform.Args(pid)
		if callIDs && (pid == agent || p.PPID == agent) {
			p.CallID = platform.Environ(pid, codexStamp)
		}
		if folders {
			p.Folder, _ = platform.Folder(pid)
		}
		s[pid] = p
		if expand || len(p.Args) > 0 && launchers[filepath.Base(p.Args[0])] {
			for _, child := range s.children(pid) {
				read(child.PID, false)
			}
		}
	}
	read(agent, true)
}
