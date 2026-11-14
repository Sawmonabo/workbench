# 16. The guard tracks work that detached from an agent's command, and shows each agent and its helpers

- Status: Accepted, November 13, 2026

## Context

The agent command guard tracks a command by following parent links down
from the shell its agent started. A program that leaves that shell (a double
fork, `setsid`, `nohup … &`, a background job left running when its call
returns) is no longer under it, so the guard neither measured nor stopped
it, and a command whose real work ran detached showed as a few megabytes.
The memory view showed only the commands the guard tracked, so the memory an
agent and its own helpers (a language server, an MCP server) held was
nowhere in it.

Facts shape the answer, all measured or read on macOS:
- The kernel hides the environment of every restricted program from other
  processes (`/bin/zsh`, `/bin/sleep`, `/usr/bin/perl` and the other system
  programs), and nothing without privileges reads it. It does answer,
  without privileges, which program image of which process forked a
  process, and keeps that record after the parent exits. Each exec gives a
  process a new version, and nothing else does.
- A variable an agent's command carries is carried as well by a command a
  person types with `!`, by what the agent's hooks start, by a nested
  agent session's own subprocesses, and, on Linux, by a service a session
  start script hands its environment to. A variable alone does not prove an
  agent's call forked a process.
- A command often ends within one watcher tick. Claude Code starts its shell
  with `posix_spawn`, and the kernel's event queue can wake a process of the
  same user at another's fork, without naming the child.
- A nested session becomes known when its SessionStart hook runs: Claude
  Code 0.3 to 0.7 s after it starts, Codex 2.3 to 3.5 s, and Codex with a
  slow required MCP server 17.1 s.

Linux and WSL keep no record of which process forked another, and nothing
on them was run for this decision.

## Options considered

How to tie work to a call:
- **A. Inherited stamps, checked against the kernel's record of the original
  parent.** Chosen. GitHub's Actions runner uses the same method where a
  tool cannot control how processes are grouped; it needs no privileges and
  costs one version read per tracked process per tick and one environment
  read per program.
- **B. Guess by start time and working folder.** It would stop programs a
  person started themselves in the same folder.
- **C. Kernel process events to know a fork's child.** Endpoint Security
  needs root and an entitlement Apple grants, `eslogger` is not to be
  depended on, and the event queue does not name the child.
- **D. Start each command inside a kernel group.** On Linux a cgroup keeps
  every descendant, but the command must be placed in it when it starts,
  which a hook cannot do; macOS has no equivalent.
- **E. Rewrite the command through the hook to add a stamp.** Permission
  rules would then be matched against the rewritten command.
- **F. An open file every descendant inherits.** It survives a wiped
  environment, but it adds an open file to every process an agent starts,
  which programs and test suites that check for leaked descriptors would
  see.

How to treat a setting the guard cannot read:
- **Unknown means spared.** Chosen. A detached program whose setting the
  guard could not read is never stopped by the guard itself; a person may
  stop it. The Actions runner and Jenkins kill only on a positive read of
  their variable, so a process whose environment cannot be read is spared.
  It exempts only trees made entirely of system programs; every common
  developer program measured shows its environment.
- **A registered opt-out**, a command that records a program as opted out
  before it runs it. Not built: it adds a second opt-out beside the
  variable, does nothing for the variable people already use, and is the
  only way to close one case, a hidden program that removed the variable
  from what it runs (see Consequences).
- **Take the setting of the call that started it.** Not chosen: the guard
  could then stop a hidden program that set `off` itself.

How to keep a nested session's work from the outer call:
- **Ownership traced back through a store of parents.** Chosen. Each
  recorded process keeps the process it came from and every version the
  kernel gave it, so when a process becomes an agent, everything that came
  from it is forgotten, even through a process that exited or exec'd since.
- **A call that started an agent proves nothing by stamp alone.** Chosen.
  A nested session's own processes carry the outer call's stamps, so a
  stamp that resolves to such a call proves nothing.
- **Agent stamps read inside tracked trees.** Chosen, on macOS. A session
  started without the hooks is protected from the first readable program
  that carries its own `CLAUDE_PID`, though not known: it gets no agent row.
  It lets a command keep the guard off its own tree, which only makes the
  guard miss. On Linux and WSL no stamp is read inside a tracked tree.
- **A longer or adaptive wait.** Not chosen: no wait changes who the work
  belongs to, so it only delays a wrong stop, and nothing tells a session
  from another program before it registers.

How to see a call that ends within a tick:
- **Fork events as a wake-up.** Chosen. The watcher follows each known
  agent's forks; on one it lists the agent's children and learns the new
  shell at once, then follows that shell only. Measured, it held the
  shell's version 100 of 100 times for commands from no time to 500 ms; as
  built it tied 30 of 30 sub-second calls' work to the call at about 4 ms
  of the watcher's CPU a call.
- **The pre hook tells the watcher, which samples.** Not chosen: it misses
  by timing (85 of 100 at a 10 ms period) and misses most double forks (10
  to 68 of 100), costs about 40 ms of CPU a call, and needs a new message in
  the socket protocol.

## Decision

- **A stamp in every command.** A SessionStart hook, `workbench guard
  session`, for Claude Code and Codex, leaves a small file that says its
  agent runs, so the guard knows the agent from its start, and under Claude
  Code adds one line to the session's env file. Every later command then
  carries a variable naming its agent and its own shell, by process ID and
  start second. It is the only change to how a command runs. The hook never
  starts the watcher, prints nothing and never fails. A Codex command
  already carries its call's ID.
- **One store of where processes were seen.** At every tick the watcher
  records each process a stop of a tracked command or unit would signal,
  with the call or unit it belongs to, the recorded process it came from,
  and on macOS every version the kernel gave it. A child of the init process
  is detached work when the store holds it, or on macOS holds its original
  parent's version, or when it carries a stamp that agrees with its own
  agent variable; it must have been forked rather than started by the init
  process, be tied to an agent the guard knows that started before it, and
  not be an agent itself.
- **What leaves takes its tree.** When a recorded process becomes protected
  (an agent, the watcher, a process a stamp names as an agent), or a child
  of the init process gets a final "no", the store forgets it and
  everything that came from it, and every unit root tied through it leaves
  its unit for good. A "no" that rests on evidence is final for the life of
  the process. `off` is one: the process said so itself, and a program it
  runs may drop the variable without bringing it back. A program that
  rewrites its title keeps it: on macOS `WORKBENCH_GUARD` and `CLAUDE_PID`
  are also read from trailing `NAME=value` strings among its arguments,
  which can only make the guard do less; the stamps that tie are read from
  the environment only.
- **A root is settled, or the guard does not choose it.** The guard's own
  stop takes a root only when its call is proved, it has been tracked for
  ten seconds since it was found or started its present program, and it is
  settled: judged in the program it runs, with its setting read. A root
  whose setting cannot be read stays unsettled until something readable
  appears below it. On macOS "another program" means a new version, so a
  title rewrite changes nothing and an exec of the same program counts.
- **The shell learned at its start.** On macOS the watcher follows every
  known agent's forks, between its calls too, and the forks, execs and exit
  of each shell it matches to a note, and records that shell and its direct
  children at once. It gives a note only where the tick would give the same
  one, and stops following a shell once it is protected. A shell that wakes
  it more than 250 times in a second, by forks and execs, loses its fork
  watch, and an agent more than 100 times is not followed for 5 seconds. The tick still tracks, measures,
  logs and decides every stop.
- **Agents and helpers are rows the guard never stops.** Each agent the
  guard knows is a row with its own size, each long-lived helper it starts
  itself (never a shell or a sandbox launcher) a row with its tree's size,
  and a running command no note matched an `unmatched` row. A person may
  stop a helper or an unmatched command, which only logs the stop when
  `WORKBENCH_GUARD` is `dry-run` or `off` for it; a stop of an agent row is
  refused. One line under the counts splits the memory in use into
  commands, detached work, agents, helpers and other.
- **What a person sees.** The details of a detached row say why the guard
  will not stop it itself (no call proved, younger than ten seconds, setting
  unread). Its ID column shows the PID of its first root that runs; the log
  keeps one label for it. A command line is shown only in a form that never
  holds an environment string, marked when shortened. The log, the reason
  and the kill record name a program by the kernel's process name only,
  that of a root the stop signals and that still runs.
- **A Monitor command's note is cleared.** Claude Code's post hooks also
  match `Monitor`, whose post hook runs when the monitor starts.

## Consequences

- The guard misses, and so never stops, on macOS: work double-forked by a
  program below the shell, through a middle that lived less than a tick,
  with nothing readable; a child whose exec and exit both come while a tick
  runs; detached work of a `!` command or of a call that
  started and ended before a watcher followed its agent, which is shown with
  no call and never chosen; work handed to a service; other users'
  processes; a tree whose command named one of its own processes as an
  agent; a sub-second call whose note is left to the tick at a fork
  wake-up; and any detached work while the kernel does not answer where
  processes come from.
- The guard never stops an agent it knows or protects, but it may still
  stop what it must not: work run with the variable removed by a program
  that set `off`; a nested session it does not know yet that never runs a
  readable program carrying its own agent stamp (any session without the
  hooks on Linux and WSL), or has not registered yet, after its first ten
  seconds; and a Codex session whose required MCP servers take longer than
  ten seconds to start, between the tenth second and its registration. The
  design's section 5.4 lists them.
- Claude Code before 2.1.214 sets no `CLAUDE_PID`: its commands carry no
  valid stamp, and its hook-less nested sessions are never protected by
  one.
- On Linux and WSL there is no original-parent record, no stamp tie and no
  fork events: detached work is found only when the guard recorded it under
  the command at a tick before it detached. None of it was run there.
- The store holds between 14,336 and 16,384 versions under a busy build.
  Stamps inside tracked trees are read from a process's second recorded
  tick, so a session without the hooks is protected one tick later.
  Measured against a watcher built from the release before this work, at
  one tick a second, the watcher spends 0.4 to 2.3 ms more CPU a tick
  (inside the run-to-run spread), and under a build starting about 650
  processes a second counts up to 23.1 MiB against its 32 MiB limit where
  the earlier one counted 16.1 to 18.0 MiB, the difference being the store;
  under 1,400 held processes it counts slightly less (15.2 against up to
  17.9 MiB). The limit is unchanged; the collector runs at 50 GC percent.
- The ten seconds start again at a handover, so detached work is not chosen
  for ten seconds after an update; the store (3.66 MB at its bound), the
  call records and the processes stamps named as agents pass to the next
  watcher.
- The original-parent record and the versions come from a kernel call that
  is not in Apple's public SDK. If the kernel stops answering it, the guard
  makes no new detached rows, chooses none, and logs that once.
- A session already running when this ships has no stamp until it restarts;
  until then its detached work is found only through the store.

## Sources

- [Claude Code environment variables](https://code.claude.com/docs/en/env-vars)
  and [hooks](https://code.claude.com/docs/en/hooks)
- [Codex exec server, child environment](https://github.com/openai/codex/blob/main/codex-rs/exec-server/README.md)
  and [SessionStart in the first turn](https://github.com/openai/codex/blob/048431dcbecc510f1879c9421a97220016ecac44/codex-rs/core/src/session/turn.rs)
- Apple kernel at `xnu-12377.121.6`:
  [exec and `P_EXEC`](https://github.com/apple-oss-distributions/xnu/blob/xnu-12377.121.6/bsd/kern/kern_exec.c),
  [`kern.procargs2`](https://github.com/apple-oss-distributions/xnu/blob/xnu-12377.121.6/bsd/kern/kern_sysctl.c),
  [process events](https://github.com/apple-oss-distributions/xnu/blob/xnu-12377.121.6/bsd/kern/kern_event.c),
  [fork](https://github.com/apple-oss-distributions/xnu/blob/xnu-12377.121.6/bsd/kern/kern_fork.c)
  and [`proc_pidinfo` flavors](https://github.com/apple-oss-distributions/xnu/blob/xnu-12377.121.6/bsd/kern/proc_info.c)
- [GitHub Actions runner, orphan process cleanup](https://github.com/actions/runner/blob/v2.338.2/src/Runner.Worker/JobExtension.cs)
  and [Jenkins `ProcessTree`](https://github.com/jenkinsci/jenkins/blob/jenkins-2.590/core/src/main/java/hudson/util/ProcessTree.java)
- [Endpoint Security entitlement](https://developer.apple.com/documentation/bundleresources/entitlements/com.apple.developer.endpoint-security.client)
- [Linux control groups v2](https://docs.kernel.org/admin-guide/cgroup-v2.html)
- The [detached work design](../superpowers/specs/2026-11-13-guard-detached-work-design.md),
  which holds every measurement, the [agent command guard design](../superpowers/specs/2026-10-14-agent-command-guard-design.md)
  and the [platform and guard view design](../superpowers/specs/2026-10-17-platform-and-guard-view-design.md)
