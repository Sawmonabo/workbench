# Guard: detached work design

Status: design, Nov 13, 2026. The guard tracks a command by following parent
links down from the shell its agent started. A program that detaches from
that shell is no longer under it, so the guard neither measures nor stops it,
and a command whose real work runs detached shows as a few megabytes. This
design makes the guard recognize that work by what it inherits from the
command and by what the kernel records about where it came from. It builds on
the [agent command guard design](2026-10-14-agent-command-guard-design.md) and
the [platform and guard view design](2026-10-17-platform-and-guard-view-design.md),
and replaces the "processes that leave the command's tree" line of the guard
design's section 12.

Sections 1 to 8 are the chosen approach. Section 9 holds proposals for the
gaps that approach leaves; each is the owner's to accept or drop, and none is
part of the design until accepted.

## 1. Goal and boundaries

- A program that an agent's command started and that then detached (a double
  fork, `setsid`, `nohup … &`, a background job left behind when its call
  returns) is its own row in `workbench guard memory`, is measured each tick,
  and is a candidate when the guard must stop the largest agent command.
- The rule names no script, tool or project. A process is detached work when
  it carries a stamp of an agent the guard protects, was forked by another
  process and not started by the init process, and is no longer under that
  agent.
- A row always names its agent. It names its subagent and call only when the
  guard can prove which call started it, and is blank otherwise. Nothing is
  guessed.
- The only change to how a command runs is one more variable in its
  environment (section 4.2).
- Not in scope: anything that needs root or an entitlement from Apple;
  attributing work a service did on a command's behalf (a container engine, a
  launchd or systemd job); stopping a process that carries no stamp and that
  the guard never saw under a command.
- Anti-goal: stopping a process that no agent command forked. A process the
  guard misses is a gap. A process it stops wrongly is a fault.

## 2. Facts this design rests on

Each fact says how it is known. Sources are in section 13.

### 2.1 Measured

On macOS 27 with System Integrity Protection on, Claude Code 2.1.325 and zsh
as the agent's shell, in throwaway sessions and with throwaway processes:

1. A process started from a Bash tool command through a double fork and
   `setsid` becomes a child of launchd in its own session and still carries
   `CLAUDE_PID`, the agent's process ID, and `CLAUDE_CODE_SESSION_ID`.
2. A subagent's Bash command runs in a shell that is a direct child of the
   same agent process and carries the same `CLAUDE_PID`. No variable in its
   environment names the subagent or the call.
3. A line `export NAME="$$"` that a SessionStart hook appends to
   `$CLAUDE_ENV_FILE` gives every later Bash command a `NAME` equal to its own
   shell's process ID, a different one for each command. A line added to the
   file in the middle of a session did not reach that session's later
   commands.
4. Reading another process's environment (`kern.procargs2`) returns none for
   `/bin/zsh`, `/bin/bash`, `/bin/sleep` and `/usr/bin/perl`, and returns it
   for Homebrew's Python, Xcode's Python and clang, cargo and rustc.
5. An app started with `open` from a Bash command is a child of launchd,
   carries `CLAUDE_PID`, and its original parent (fact 6) is launchd.
6. `proc_pidinfo` with the flavor `PROC_PIDUNIQIDENTIFIERINFO` answers
   without privileges for another process, a restricted program included. It
   reports `p_idversion`, which changes at each exec, and
   `p_orig_ppidversion`, the version the parent had when it forked the
   process. After the parent exits and launchd adopts the process,
   `p_orig_ppidversion` still holds that parent's version, while
   `p_puniqueid` becomes launchd's. A child forked after its parent's exec
   records the version the parent got at that exec.
7. That flavor and its structure are not in the public SDK's
   `sys/proc_info.h` or `libproc.h`.
8. A Monitor tool command runs in the same shell form as a Bash command, as a
   direct child of the agent, with the env file's lines. The pre hook matched
   on `Bash` fires for it, so the guard notes and tracks it. No post hook
   fires, so its note stays on disk after the command ends.
9. A Bash command that ends within one watcher tick is never tracked, so the
   guard never learns its shell's process ID.

### 2.2 Documented

1. Claude Code sets `CLAUDE_PID` "to its own process ID in the subprocesses it
   spawns: Bash and PowerShell tool commands and hook commands", from
   v2.1.214, and says to "read it from your own scripts to identify or signal
   the parent Claude Code process deliberately".
2. `CLAUDE_ENV_FILE` is the "path to a shell script whose contents Claude Code
   runs before each Bash command in the same shell process". SessionStart,
   Setup, CwdChanged and FileChanged hooks may write to it.
3. No documented variable gives a Bash command its tool call or subagent. The
   hook input carries `agent_id` and `agent_type` for a call a subagent makes.
4. When a PreToolUse hook returns `updatedInput`, "Claude Code evaluates
   permission rules … against the input your hook returns, not the input
   Claude sent".
5. From v2.1.314 a PreToolUse hook matched on `Bash` "also runs on shell
   commands Claude starts with the Monitor tool", a PostToolUse hook matched
   on `Bash` "doesn't run for Monitor", and a matcher may name several tools
   (`Bash|Monitor`).
6. On Linux and WSL, with `CLAUDE_CODE_TOOL_MEMORY_LIMIT` set, "Claude Code
   applies the cap with a memory cgroup" and "counts all of a session's Bash,
   PowerShell, and Monitor commands against the one cap".
7. Codex exposes the call's ID "as `CODEX_TOOL_CALL_ID` in the initial child
   environment", and its shell tools match hooks as `Bash`.
8. Apple's kernel omits the environment from `kern.procargs2` unless the
   target is the caller, is not restricted, SIP is off or the caller holds a
   private entitlement. At fork it keeps "our parent's current pid and
   pidversion"; adopting an orphan changes only its current parent.
   `PROC_PIDUNIQIDENTIFIERINFO` is among the operations that "don't require
   privileges". The kernel's event queue no longer follows forks
   (`NOTE_TRACK` is "no longer supported as of 10.5").
9. On Linux, "when a process forks a child process, the new process is born
   into the cgroup that the forking process belongs to", and
   `/proc/PID/environ` holds "the initial environment that was set when the
   currently executing program was started".
10. GitHub's Actions runner sets `RUNNER_TRACKING_ID` and, when a job ends,
    reads that variable from each process to find and end the ones the job
    left behind. Jenkins's handbook documents changing `BUILD_ID` or
    `JENKINS_NODE_COOKIE` as the way to keep a process a build started.
11. `eslogger` "is NOT API in any sense", and the Endpoint Security
    entitlement must be requested from Apple.
12. `open`: "Opened applications inherit environment variables just as if you
    had launched the application directly through its full path."
13. Build tools may filter what their tasks inherit: Turborepo's default
    strict mode passes only the variables its configuration lists.

### 2.3 Not yet verified

1. Everything in 2.1 on Linux and WSL: which process adopts an orphan, that
   its environment is readable, and how the memory cap's cgroup looks from
   outside.
2. That a detached process started by a Codex command carries
   `CODEX_TOOL_CALL_ID` where the guard can read it.
3. When a PostToolUse hook matched on `Bash|Monitor` fires for a Monitor call
   and what input it carries.
4. The env file line with bash as the agent's shell.

Each is proved in a disposable home or container before the part that needs
it is built.

## 3. Approaches considered

- **A. Inherited stamps, checked against the kernel's record of the original
  parent (chosen).** It is the method the Actions runner and Jenkins use where
  a tool cannot control how processes are grouped, it needs no privileges, and
  it costs one read per new process tree.
- **B. Guess by start time and working folder.** Rejected: it would stop
  programs a person started themselves in the same folder.
- **C. Kernel process events.** Exact, but Endpoint Security needs root and
  an entitlement Apple grants, `eslogger` is not to be depended on, and the
  kernel's event queue does not name a fork's child. Rejected.
- **D. Start each command inside a kernel group.** On Linux a cgroup keeps
  every descendant, but the command must be placed in it when it starts,
  which a hook cannot do; macOS has no equivalent. Section 9.5 uses Claude
  Code's own cgroup where one exists.
- **E. Rewrite the command through the hook to add a stamp.** Rejected:
  permission rules would then be matched against the rewritten command.
- **F. An open file every descendant inherits.** Readable even on restricted
  programs and kept when an environment is wiped, but it adds an open file to
  every process an agent starts, which programs and test suites that check
  for leaked descriptors would see. Rejected.

## 4. Stamps

A stamp is an environment variable a command's processes inherit. The guard
reads only the names below, with `platform.Environ`, and keeps or logs nothing
else of any environment.

### 4.1 Agent stamp

`CLAUDE_PID` names the Claude Code process. It counts only when that process
is alive and started before the stamped process. Sessions of a Claude Code
older than v2.1.214 have no agent stamp and get no detached rows.

### 4.2 Call stamp

A new hidden command, run as a SessionStart hook beside the costs hook,
appends one line to `$CLAUDE_ENV_FILE` unless it is already there:

```sh
export WORKBENCH_GUARD_SHELL="$$"
```

Every Bash and Monitor command of the session then carries the process ID of
its own shell, which is the process the guard tracks as the command's root.
The hook prints nothing and never fails; with `WORKBENCH_GUARD=off` it writes
nothing. A session already running when this ships has no call stamp until it
restarts, and falls back to the agent stamp.

### 4.3 Codex

A Codex command's processes carry `CODEX_TOOL_CALL_ID`, which is the note's
own `tool_use_id`. It is both the agent stamp and the call stamp: it resolves
to the note, and the note names the agent. With the note gone, it resolves to
nothing and no row is made.

### 4.4 The guard's own setting

`WORKBENCH_GUARD` is read from the same process: `off` makes it no unit,
`dry-run` makes its stops log only, as for a call's note.

## 5. Detached units

### 5.1 Finding them

Each tick the watcher already lists every process. It classifies, once per
process ID and start time, each process of its own user that is outside every
protected process's tree and every tracked command's tree. A process whose
environment the kernel hides is looked at again only through the new
processes under it.

### 5.2 What a unit is

A process R is the root of a unit when all of these hold:

1. R's parent is the init process (PID 1, or an init the guard knows by
   name).
2. R was forked by another process. On macOS its original parent
   (`p_orig_ppidversion`) is not the init process's version; when the kernel
   does not answer, R is no unit. This is what keeps an app started with
   `open` out (2.1 fact 5). On Linux no launcher is known to copy a caller's
   environment into a process the init starts, so the condition is met by the
   stamp alone, pending 2.3 item 1.
3. R carries a stamp of an agent the guard protects. When R's environment is
   hidden, the stamp is read from the first readable processes below it,
   reached through hidden ones only: at least one carries the stamp and none
   carries another agent's.
4. R started after that agent, and after the call's shell when the call
   resolves (5.4), by the kernel's start times.
5. R is not a Workbench process and not a protected process.

The unit is R and everything below it, with protected processes and what is
under them left out, as for a command's tree.

A process the guard saw under a tracked command's root that later becomes a
child of the init process is a unit of that call, with or without a readable
stamp, when conditions 1, 2 and 5 hold. This covers a background job left
behind when its call returns.

### 5.3 Protected processes

Besides the agents that left a note and the watcher, every live process that
a stamp names as an agent, and that started before the stamped process, is
protected, with everything under it.

### 5.4 Which call started it

The first of these that applies:

1. A Codex call stamp resolves to its note.
2. A Claude Code call stamp resolves through a record the guard keeps for
   each call it tracked: the shell's process ID and start time, the agent,
   subagent, project and folder. Records outlive their calls while the agent
   runs, the newest few thousand per agent. The stamp resolves to the record
   of that agent with that shell ID whose shell started before R. When more
   than one such record exists, because the ID was reused, it does not
   resolve.
3. Otherwise the row has the agent from the agent stamp, the project from
   R's working folder, and a blank subagent.

A call that ended within one tick was never tracked (2.1 fact 9) and has no
record, so its detached work takes case 3. The records pass to the next
watcher at a handover with the tracked commands. A watcher that starts fresh
has none.

### 5.5 Lifetime

A unit is tracked while any of its processes lives and its agent runs.
Members the init process adopts after the unit's root exits stay in the unit.
The watcher does not exit idle while a unit is tracked. When the agent exits,
the unit is forgotten, not stopped, as a command is today.

## 6. Measuring and stopping

- A unit is measured each tick like a command: resident size for the view,
  footprint for the choice of what to stop.
- Units compete with commands for the largest. The floor, the cooldown, the
  refusal count and the dry run apply to them unchanged.
- The same target choice signals a unit: its root usually leads its own
  process group, which is signalled as a group when every member is in the
  unit.
- The reason is told to the call that started the unit when that call
  resolves and its command still runs, through that call's kill record, in
  words that name the detached program. Otherwise the stop is in the log
  only, as for a command whose result already returned.
- A person stops a unit from the view the same way as a command.

## 7. View, log and protocol

A unit is a row in the view's own columns. Its ID is its root's process ID,
its status reads `detached`, its time counts from the root's start, and its
command is the root's own command line:

```
   ID        STATUS      AGENT   SUBAGENT        TIME   MEMORY     PEAK  PROJECT   COMMAND
   01PnU61k  ● run       claude  implementer   12m58s     6 MB   166 MB  shop      scripts/build.sh api
   p87305    ● detached  claude  implementer    4m36s  4.20 GB  4.20 GB  shop      /bin/zsh scripts/build.sh --worker api
```

The details screen names the call that started it when it resolved. The log's
`tracking`, `ended` and `stopping` events carry the unit's kind, root and
launching call. A snapshot's command gains its kind and launching call; both
are additions an older view ignores.

## 8. What stays unseen

With sections 4 to 7 alone:

1. **System-only trees on macOS.** A unit whose programs all hide their
   environment is unseen until a readable program appears in it, unless the
   guard saw it under its command first (5.2).
2. **Wiped environments.** A program that clears or filters its environment
   at the moment it detaches carries no stamp.
3. **Work handed to a service.** A container engine, a launchd or systemd
   job, or a build server a person started was never the command's
   descendant.
4. **What the agent starts itself.** Language servers, MCP servers and the
   agent's own memory are not commands.
5. **Fast calls.** Detached work from a call that ended within a tick shows
   no subagent.
6. **Other users' processes.** A root-owned process can be neither sized nor
   signalled.

## 9. Proposals for the gaps

### 9.1 Follow the original parent (gaps 1 and 2, macOS)

Each tick the guard also records the `p_idversion` of every process in each
tracked command's tree and each unit. A new child of the init process whose
`p_orig_ppidversion` is one of those was forked by that process, so it is a
unit of that call, whatever its environment holds: a restricted program, a
wiped environment and a call that ended within the same tick as the fork all
resolve (2.1 fact 6, measured for a single fork and for a fork after an
exec).

It does not see through a double fork whose middle process lived and died
between two ticks: that process was never recorded, and the kernel keeps
nothing of a process that has exited. It costs one more kernel call per
tracked process per tick and one per new child of init. It uses the call
section 5.2 already depends on, which is not in the public SDK (2.1 fact 7):
if the kernel stops answering it, units stop being made and the guard is back
to what it does today, which it logs once.

### 9.2 Rows for the agent and what it starts (gap 4)

One row per agent with its own memory, and one row per long-lived direct
child of an agent that is not a noted command or a hook: language servers and
MCP servers. Parent links make these exact. The guard never chooses them. A
person may stop a helper row from the view, never an agent row.

This reverses a boundary of the view design ("only commands the guard tracks
are shown"), which is why it is the owner's choice.

### 9.3 A line for where memory is (gap 3 and whatever is left)

Under the memory bar, one line splits the memory in use into commands,
detached work, agents, helpers and everything else, each by footprint. A
container engine's virtual machine or a launchd job then shows as part of
"everything else" without the guard claiming an agent started it. Footprint,
the measure the stop choice already uses, and not resident size, because one
process's resident size was seen to move by over a gigabyte within seconds.

### 9.4 The Monitor post hook (a fault found on the way)

A Monitor command is tracked, but its note is never cleared by a post hook
(2.1 fact 8), so it lingers until the guard abandons it with a warning, and a
stop's reason cannot reach the agent. Matching the post hooks on
`Bash|Monitor` and marking such a note as a background call's is marked fixes
both, once 2.3 item 3 is known.

### 9.5 Claude Code's cgroup on Linux and WSL (gap 2 there)

Where the owner turns on Claude Code's memory cap, every tool command of a
session shares one cgroup and every descendant is born into it (2.2 facts 6
and 9). The guard then reads the cgroup of a tracked root and treats its
members outside the agent's tree as that session's detached work, whatever
their environment. It applies only with the cap on and needs 2.3 item 1
first.

Not proposed: Workbench moving commands into cgroups itself. systemd can do
it for an unprivileged user, but it costs a bus call per command and changes
how a command is accounted.

### 9.6 What stays unseen with all of section 9

- On macOS, a double fork through a middle process that lived less than a
  tick, when the detached tree is system-only or its environment was wiped.
- Work handed to a service, which shows only inside "everything else".
- Other users' processes.

No method open to a tool without root closes these. The ones that would are
in section 3, C and D.

## 10. Left to the builder

The names of the variable, the hidden command and the log attributes; the
bound on call records; the classification cache; how units and records pass
at a handover; a unit's full ID in the socket protocol; the Linux details
once 2.3 item 1 is known; whether the protocol version moves.

## 11. Tests and checks

One test, for unauthorized execution: the unit rule yields no root for a
process without a stamp, for a stamp naming a dead process or one younger
than the stamped process, for a process the init process started that carries
a stamp, for a Workbench process, for a protected process, and when the
kernel does not answer. The risk it guards is the guard stopping a program no
agent command forked.

Before release, in a disposable home on macOS and in a Linux container:

- A command that double-forks a sleeper gives a `detached` row with its
  agent, and with its subagent when a subagent made the call. A stop from the
  view ends it.
- An app started with `open` from a command gives no row.
- A sub-second command that leaves a sleeper gives a row with a blank
  subagent.
- `scripts/render-check.sh` for each role and mode the hook change touches.

## 12. Docs to revise

- The guard design's section 12 line on processes that leave the command's
  tree, and the view design's section 1 line on what is shown, its columns
  and its protocol section.
- A new ADR for recognizing detached work by stamps and the original-parent
  record.
- `docs/usage.md`: the guard section's sentence that detached programs are
  not tracked, and the view's rows.
- `AGENTS.md`: the guard row of the ownership table gains the SessionStart
  hook.
- `docs/acceptance.md` once the checks in section 11 have run.

## 13. Sources

- Claude Code environment variables: <https://code.claude.com/docs/en/env-vars>
- Claude Code hooks: <https://code.claude.com/docs/en/hooks>
- Claude Code tools reference, memory limit on Linux and WSL:
  <https://code.claude.com/docs/en/tools-reference>
- Codex exec server, child environment:
  <https://github.com/openai/codex/blob/main/codex-rs/exec-server/README.md>
- Codex hooks: <https://developers.openai.com/codex/hooks>
- Apple kernel, `kern.procargs2` and its environment rule:
  <https://github.com/apple-oss-distributions/xnu/blob/main/bsd/kern/kern_sysctl.c>
- Apple kernel, original parent kept at fork:
  <https://github.com/apple-oss-distributions/xnu/blob/main/bsd/kern/kern_fork.c>
- Apple kernel, adopting an orphan:
  <https://github.com/apple-oss-distributions/xnu/blob/main/bsd/kern/kern_exit.c>
- Apple kernel, `proc_pidinfo` flavors and their checks:
  <https://github.com/apple-oss-distributions/xnu/blob/main/bsd/kern/proc_info.c>
- Apple kernel, event queue flags:
  <https://github.com/apple-oss-distributions/xnu/blob/main/bsd/sys/event.h>
- Endpoint Security entitlement:
  <https://developer.apple.com/documentation/bundleresources/entitlements/com.apple.developer.endpoint-security.client>
- `eslogger` and `open`: their macOS manual pages (`man eslogger`, `man open`)
- Linux control groups v2:
  <https://docs.kernel.org/admin-guide/cgroup-v2.html>
- `/proc/PID/environ`:
  <https://man7.org/linux/man-pages/man5/proc_pid_environ.5.html>
- systemd, why cgroups track a service's processes:
  <https://0pointer.de/blog/projects/systemd.html>
- GitHub Actions runner, orphan process cleanup:
  <https://github.com/actions/runner/blob/main/src/Runner.Worker/JobExtension.cs>
- Jenkins, spawning processes from a build:
  <https://www.jenkins.io/doc/book/managing/spawning-processes/>
- Turborepo, environment modes:
  <https://github.com/vercel/turborepo/blob/main/apps/docs/content/docs/crafting-your-repository/using-environment-variables.mdx>
