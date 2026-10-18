# 13. A live view stops guarded commands through the watcher's socket

- Status: Accepted, October 17, 2026

## Context

The agent command guard stops the largest command a coding agent started
when the machine is about to run out of memory. A person watching an agent
work could see what it ran only after the fact, in `workbench guard logs`,
and could stop a runaway command only with `kill` or Activity Monitor, by a
process number they had to find first, with nothing logged and nothing told
to the agent. `workbench guard memory` shows, live, each command the guard
tracks, with its memory, project, agent and subagent, and stops the ones a
person chooses.

The watcher is the one process that knows which processes belong to which
call and the one that signals them: it checks start times so a reused
process number is never hit, protects the agents themselves, escalates to
`SIGKILL` after 5 seconds, writes the reason the agent is told, and logs the
stop. A view that read `/proc` or signalled processes itself would repeat all
of that and could disagree with it.

Two identical commands started together in two worktrees of one repository
paired with their notes by command text alone, and could pair crosswise, so
a row could show the wrong project and a stop could hit the other call.

## Options considered

- **A. The watcher serves a Unix socket; the view reads its snapshots and
  sends it stops.** Chosen.
- **B. The view reads the notes and the processes itself and signals by
  process number.** It repeats the matching, start-time checks, protections
  and escalation, and its stops would bypass the log and the agent's reason.
- **C. The view writes a stop request file the watcher picks up.** No
  answer comes back, files need their own cleanup, and the view would still
  need the live sizes, which only the watcher measures.
- **D. A TCP port on localhost.** Any local user can connect, and there is no
  way to learn who connected.

## Decision

- **A socket, as `docker` and `tailscale` do.** The watcher serves
  `guard.sock` in the guard's private (0700) state folder. The `docker` CLI
  talks to its daemon over a Unix socket guarded by Unix permissions, and the
  `tailscale` CLI does the same and authenticates each connection by its
  peer's credentials. Messages are JSON Lines, a format suited to "passing
  messages between cooperating processes": `hello` with the protocol
  versions the view speaks, answered with the highest both share and the
  watcher's release, as Docker's client and daemon negotiate "the highest
  version of the API supported by both"; then a `snapshot` after each tick,
  and `stop` and `stopped`.
- **Peer checks, not only the folder's mode.** unix(7) says pathname sockets
  honor their folder's permissions but "portable programs should not rely on
  this feature for security". At accept the watcher reads the peer's user
  and process ID (`SO_PEERCRED` on Linux, `LOCAL_PEERCRED` and
  `LOCAL_PEERPID` on macOS, through `platform.Peer`) and closes a
  connection from another user. It refuses a `stop` from a process that
  descends from an agent with a note, so a command one agent runs cannot stop
  another agent's work; when a link in that chain cannot be read, it refuses.
  The view itself refuses to run without a terminal or under a coding agent,
  by the detection `apply` uses.
- **The tick owns the state; broadcasts never block.** Every field of the
  watcher stays with its one tick goroutine. The socket's goroutines hand it
  stops over a channel and get each answer on a channel of their own, the
  stateful-goroutine pattern. Each snapshot goes to every client without
  blocking: a client whose small buffer is full, or whose write takes over
  5 seconds, is disconnected, as Tendermint's pub/sub ends a subscription
  that cannot keep up and NATS closes a slow consumer, so a stuck view can
  never slow the decision to stop a command. A queue of waiting stops that
  is full is answered `busy`.
- **A person's stop is the guard's stop, asynchronous.** A `stop` names call
  IDs, never process numbers, and the watcher stops each the way it stops for
  memory: the same targets, start-time checks, pidfd on Linux, `SIGTERM`
  then `SIGKILL` after 5 seconds (or `SIGKILL` at once when forced, the two
  choices htop's signal menu and Kubernetes' grace period give). It answers
  once the first signal is sent and follows the stop across later ticks, so
  the tick never waits on it; the view's row shows `stopping` and then
  `stopped`. It is logged `by=you`, the agent is told `Stopped by you
  (workbench guard memory).`, and it starts no cooldown, so the guard's own
  stops for memory go on. Under dry-run it is only logged.
- **The folder is a preference, never a rule.** Among the processes that
  claim a note, the watcher prefers one whose working folder is the note's
  leading `cd` folder, then one in the note's `cwd`, then one whose folder
  is unknown or neither, the oldest within a rank. A folder that cannot be
  read leaves the choice as it was, so no command goes unguarded because its
  folder is unknown.
- **The socket survives a handover.** The listening socket rides the
  handover with the lock, as descriptor 6, and the old watcher closes its
  copy without removing the file, so a view sees one disconnect and
  reconnects. The view retries every second while no watcher answers; the
  watcher starts with the next agent command.

## Consequences

- One process signals agent commands, whoever asked, and every stop is
  logged and told to the agent the same way.
- A view costs the watcher a JSON encoding and a non-blocking send per tick;
  a view that stops reading is cut off instead of slowing it.
- A view of a newer release meets an older watcher that took the socket over
  without serving it: its hello times out and the view says to run
  `workbench update`.
- On Linux with `hidepid=2`, a person whose process chain passes through a
  process they cannot read gets `not allowed` for every stop: the check
  fails closed.
- A handover carries the finished rows, who stopped each command and the
  largest peak with the tracked commands, so the view shows the same rows
  after it reconnects.

## Sources

- [Docker security: the daemon's Unix socket](https://docs.docker.com/engine/security/)
- [Docker Engine API: version negotiation](https://docs.docker.com/reference/api/engine/)
- [Tailscale `safesocket`: peer credentials on its socket](https://pkg.go.dev/tailscale.com@v1.90.6/safesocket)
- [unix(7): socket permissions and `SO_PEERCRED`](https://man7.org/linux/man-pages/man7/unix.7.html)
- [JSON Lines](https://jsonlines.org/)
- [htop `SignalsPanel.c`: SIGTERM selected by default](https://github.com/htop-dev/htop/blob/main/SignalsPanel.c)
  and [htop(1)](https://www.man7.org/linux/man-pages/man1/htop.1.html)
- [Kubernetes Pod termination: TERM, grace period, KILL](https://kubernetes.io/docs/concepts/workloads/pods/pod-lifecycle/)
- [Go by Example: stateful goroutines](https://gobyexample.com/stateful-goroutines)
- [Tendermint ADR 033: pub/sub, slow subscribers](https://github.com/chainpoint/tendermint/blob/develop/docs/architecture/adr-033-pubsub.md)
- [NATS slow consumers](https://docs.nats.io/learn/resilient-clients/slow-consumers)
- [Bubble Tea realtime example: waiting on a channel](https://github.com/charmbracelet/bubbletea/blob/main/examples/realtime/main.go)
- The [platform and guard view design](../superpowers/specs/2026-10-17-platform-and-guard-view-design.md)
  and the [agent command guard design](../superpowers/specs/2026-10-14-agent-command-guard-design.md)
