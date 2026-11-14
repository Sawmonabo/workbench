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

Two facts shape the answer. On macOS the kernel hides the environment of
many programs from other processes (a shell, `sleep`, any restricted
program), but answers, without privileges, which program image of which
process forked a process, and keeps that record after the parent exits.
Linux and WSL keep no such record. And a variable an agent's command
carries is carried as well by a command a person types with `!`, by what
the agent's hooks start, and, on Linux, by a service a session start script
hands its environment to, so a variable alone does not prove an agent's call
forked a process.

## Options considered

- **A. Inherited stamps, checked against the kernel's record of the original
  parent.** Chosen. It is the method GitHub's Actions runner uses where a
  tool cannot control how processes are grouped, it needs no privileges, and
  it costs one read per new process tree.
- **B. Guess by start time and working folder.** It would stop programs a
  person started themselves in the same folder.
- **C. Kernel process events.** Exact, but Endpoint Security needs root and
  an entitlement Apple grants, `eslogger` is not to be depended on, and the
  kernel's event queue does not name a fork's child.
- **D. Start each command inside a kernel group.** On Linux a cgroup keeps
  every descendant, but the command must be placed in it when it starts,
  which a hook cannot do; macOS has no equivalent.
- **E. Rewrite the command through the hook to add a stamp.** Permission
  rules would then be matched against the rewritten command.
- **F. An open file every descendant inherits.** It survives a wiped
  environment, but it adds an open file to every process an agent starts,
  which programs and test suites that check for leaked descriptors would
  see.

## Decision

- **A stamp in every command.** A SessionStart hook, `workbench guard
  session`, for Claude Code and Codex, leaves a small file that says its
  agent runs, so the guard knows the agent from its start, and under Claude
  Code adds one line to the session's env file. Every later command then
  carries a variable naming its agent and its own shell, by process ID and
  start second. It is the only change to how a command runs. The hook never
  starts the watcher, prints nothing and never fails. A Codex command
  already carries its call's ID.
- **Detached work is what a command forked.** A child of the init process is
  detached work when another process forked it (not the init process, which
  keeps out an app opened with `open`), it is tied to an agent the guard
  knows, it started after that agent, and it is not an agent itself. It is
  tied on every system when the guard saw it in a command's tree before it
  detached, and on macOS also when the kernel's record names a process the
  guard saw in a command's tree as its original parent, or when it carries a
  stamp that agrees with its own agent variable. A stamp only ever exposes the process that carries it and
  what is under it. Detached work is its own `detached` row, measured each
  tick like a command.
- **The guard stops only what a call provably started.** Detached work joins
  the commands in the choice of the largest only when the call that started
  it resolves. Work found by its stamp alone, from a command a person typed
  with `!` or a call that ended within a tick, is shown and can be stopped by
  a person, never chosen. Each detached program gets its own first ten
  seconds before the guard's own stop may size or signal it, the time a
  session a command started needs to say it is an agent; a person's stop
  covers every live program of the unit. A program is judged again once it
  starts its own program (its name or command line changes), on fresh stamps,
  so one that turns the guard off leaves its unit and one that says
  `dry-run` makes its unit a dry run. The log, the reason and the kill record
  name detached work by the process name the kernel keeps, never its
  arguments, which may hold a token the shell expanded; the full command
  line is in the view only.
- **Agents and helpers are rows the guard never stops.** Each agent the
  guard knows is a row with its own size, and each long-lived helper it
  starts itself, never a shell, a row with its tree's size; a running
  command no note matched, whose call stamp names that agent and that
  command's shell, is an `unmatched` row. A person may stop a helper or an
  unmatched command from the view, which only logs the stop when
  `WORKBENCH_GUARD` is `dry-run` or `off` for it, and the row then leaves
  the view; a stop of an agent row is refused. One line under
  the counts splits the memory in use into commands, detached work, agents,
  helpers and other.
- **A Monitor command's note is cleared.** Claude Code's post hooks also
  match `Monitor`, whose post hook runs when the monitor starts, so its note
  no longer lingers until the guard abandons it.

## Consequences

- On macOS, detached work is unseen only when it left through a middle
  process that lived less than a tick and all its programs hide or wiped
  their environment, until a readable program carrying the stamp appears in
  it.
- On Linux and WSL, detached work is found only when the guard saw it under
  the command for at least one tick before it detached: a program that
  detaches at once, through a double fork, is unseen. Whether more can be
  found safely, through Claude Code's own cgroup where its memory cap is on,
  waits for a check in a Linux container.
- Work handed to a service (a container engine, a launchd or systemd job, a
  build server a person started) was never the command's descendant. Its
  memory is in `other`.
- Detached work from a fast call or from a `!` command shows no subagent and
  is never chosen by the guard.
- Another user's process can be neither sized nor signalled.
- A subshell that never starts another program, left by a command that
  returned within about a second under a shell whose environment is
  readable, carries the agent stamp without the call stamp and stays unseen.
  Reading stamps below a readable process would find it, but a job server a
  person started that forwards its caller's environment would then count as
  an agent's work.
- A root is judged again only when its process name or command line
  changes: a program started under the same name and command line with only
  a new environment, or a change that reverts within a tick, goes unnoticed.
- An agent session started without Workbench's hooks, or still in its first
  ten seconds, is a program like any other: the guard may stop it, as it
  stops such a session inside a command's tree.
- On Linux an editor or browser a command forked and left running is
  detached work; on macOS apps go through `open` and are left out.
- The original-parent record uses a kernel call that is not in Apple's public
  SDK. If the kernel stops answering it, the guard makes no detached rows,
  is back to tracking commands alone, and logs that once.
- A session already running when this ships has no stamp until it restarts;
  until then its detached work is found only through its original parent.

## Sources

- [Claude Code environment variables](https://code.claude.com/docs/en/env-vars)
  and [hooks](https://code.claude.com/docs/en/hooks)
- [Codex exec server, child environment](https://github.com/openai/codex/blob/main/codex-rs/exec-server/README.md)
- [Apple kernel, original parent kept at fork](https://github.com/apple-oss-distributions/xnu/blob/main/bsd/kern/kern_fork.c)
  and [`proc_pidinfo` flavors](https://github.com/apple-oss-distributions/xnu/blob/main/bsd/kern/proc_info.c)
- [GitHub Actions runner, orphan process cleanup](https://github.com/actions/runner/blob/main/src/Runner.Worker/JobExtension.cs)
- [Endpoint Security entitlement](https://developer.apple.com/documentation/bundleresources/entitlements/com.apple.developer.endpoint-security.client)
- [Linux control groups v2](https://docs.kernel.org/admin-guide/cgroup-v2.html)
- The [detached work design](../superpowers/specs/2026-11-13-guard-detached-work-design.md),
  the [agent command guard design](../superpowers/specs/2026-10-14-agent-command-guard-design.md)
  and the [platform and guard view design](../superpowers/specs/2026-10-17-platform-and-guard-view-design.md)
