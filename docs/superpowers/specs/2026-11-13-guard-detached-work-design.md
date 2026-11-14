# Guard: detached work design

Status: design, Nov 13, 2026. The guard tracks a command by following parent
links down from the shell its agent started. A program that detaches from
that shell is no longer under it, so the guard neither measures nor stops it,
and a command whose real work runs detached shows as a few megabytes. This
design makes the guard recognize that work by what it inherits from the
command and by what the kernel records about where it came from. It also
gives the agent and the helpers it starts their own rows, adds a line for
where the machine's memory is, and clears a Monitor command's note. It builds
on the [agent command guard design](2026-10-14-agent-command-guard-design.md)
and the [platform and guard view design](2026-10-17-platform-and-guard-view-design.md),
and replaces the "processes that leave the command's tree" line of the guard
design's section 12.

Of the approaches in section 3 the owner chose A. Section 11 holds the one
part that waits for a check before it is designed further.

## 1. Goal and boundaries

- A program that an agent's command started and that then detached (a double
  fork, `setsid`, `nohup … &`, a background job left behind when its call
  returns) is its own row in `workbench guard memory`, is measured each tick,
  and is a candidate when the guard must stop the largest agent command.
- The rule names no script, tool or project. A process is detached work when
  it was forked by another process and not started by the init process, is no
  longer under its agent, and either carries a stamp of an agent the guard
  knows or was forked by a process the guard saw in a command's tree.
- A row always names its agent. It names its subagent and call only when the
  guard can prove which call started it, and is blank otherwise. Nothing is
  guessed.
- Each agent, and each long-lived helper it starts itself, is a row too. The
  guard never chooses these for a stop.
- One line shows where the memory in use is: commands, detached work, agents,
  helpers and everything else.
- The only change to how a command runs is one more variable in its
  environment (section 4.2).
- Not in scope: anything that needs root or an entitlement from Apple;
  attributing work a service did on a command's behalf (a container engine, a
  launchd or systemd job); stopping a process that carries no stamp and that
  no process the guard saw forked.
- Anti-goal: stopping a process that no agent command forked. A process the
  guard misses is a gap. A process it stops wrongly is a fault.

## 2. Facts this design rests on

Each fact says how it is known. Sources are in section 15.

### 2.1 Measured

On macOS 27 with System Integrity Protection on, Claude Code 2.1.325, Codex
0.169.0 and zsh as the agent's shell unless a fact says otherwise, in
throwaway sessions and with throwaway processes:

1. A process started from a Bash tool command through a double fork and
   `setsid` becomes a child of launchd in its own session and still carries
   `CLAUDE_PID`, the agent's process ID, and `CLAUDE_CODE_SESSION_ID`.
2. A subagent's Bash command runs in a shell that is a direct child of the
   same agent process and carries the same `CLAUDE_PID`. No variable in its
   environment names the subagent or the call.
3. A line `export NAME="$CLAUDE_PID:$$"` that a SessionStart hook appends to
   `$CLAUDE_ENV_FILE` gives every later Bash command a `NAME` holding its
   agent's process ID and its own shell's, a different shell for each
   command. A line added to the file in the middle of a session did not reach
   that session's later commands.
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
   on `Bash` fires for it with an input the guard accepts as a Bash call, so
   the guard notes and tracks it. No post hook fires, so its note stays on
   disk after the command ends.
9. A Bash command that ends within one watcher tick is never tracked, so the
   guard never learns its shell's process ID.
10. A language server that Claude Code started itself is a direct child of
    the agent and carries neither `CLAUDE_PID` nor `CLAUDECODE`.
11. A process a hook starts carries `CLAUDE_PID` and `CLAUDECODE` but none of
    the variables the env file exports: the watcher, which the pre hook
    starts, is one. A session's env file gains the same lines again each time
    a SessionStart hook that appends to it runs.
12. A Claude Code session started with such a variable already in its
    environment, as one a command starts would be, passes it unchanged to
    what its hooks start: a daemon one of its hooks left carried that
    session's own `CLAUDE_PID` beside the inherited value, which named
    another agent.
13. With bash 3.2 as the agent's shell the env file line gives each command
    the same stamp as under zsh.
14. An MCP server Claude Code starts never carries its own agent's
    `CLAUDE_PID`: the variable is unset when a person started the session,
    and is the outer agent's when an agent's command started it. It carries
    `CLAUDECODE` and no call stamp.
15. With the hooks matched on `Bash|Monitor`, a Monitor call's pre and post
    hooks name the tool `Monitor`, carry the same `tool_use_id` and the
    command, and the post hook fires when the monitor starts, not when it
    ends.
16. A command a person types with `!` runs in the same shell form as a tool
    command and carries the same variables, the call stamp included, agreeing
    with its agent. No pre or post hook fires for it, so it leaves no note.
17. A process a Codex command detached carries `CODEX_TOOL_CALL_ID` equal to
    the `tool_use_id` its hooks received. A Codex session an agent's command
    started also carries that agent's `CLAUDE_PID`.
18. A line `export NAME="$CLAUDE_PID:$$:${EPOCHSECONDS:-$(date +%s)}"` in the
    env file gives each command the second its own shell started as the third
    part, under zsh and under bash 3.2: a command that waited two seconds
    before reading the variable still read its shell's start second.
19. A status line command carries `CLAUDE_PID` and no variable the env file
    exports.
20. A SessionStart hook runs as a child of the Claude Code process, carries
    that process's ID in `CLAUDE_PID`, and its input holds `session_id`,
    `cwd`, `source`, `transcript_path` and `hook_event_name`.

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
   (`NOTE_TRACK` is "no longer supported as of 10.5"). The version comes
   from one counter for the whole system (`nextpidversion`), raised at each
   fork and exec, so a version names one program image of one process.
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
14. Codex starts a hook command from the environment its session saved at
    its start, plus the variables the hook's own configuration lists, and
    adds the call's ID only to the commands it launches. This is read from
    Codex's source, not measured on an installed version.
15. systemd's service manager offers `SetEnvironment()`, which alters "the
    environment block that is passed to all spawned processes": a command
    that calls it, as session start scripts do, puts its own variables into
    every service the manager starts afterwards.

### 2.3 Not yet verified

1. Everything in 2.1 on Linux and WSL: which process adopts an orphan, that
   its environment is readable, how the memory cap's cgroup looks from
   outside, and whether a command run in a sandbox sees its shell's process
   ID as the watcher does or one of its own namespace.

It is proved in a Linux container before the release, and what cannot be run
there is recorded as unqualified. A part whose check fails is brought back to
the owner, not built on a guess. Nothing is built on these: until they are
known, a stamp ties nothing on Linux and WSL (5.2).

## 3. Approaches considered

- **A. Inherited stamps, checked against the kernel's record of the original
  parent (chosen).** It is the method the Actions runner uses where a tool
  cannot control how processes are grouped, it needs no privileges, and it
  costs one read per new process tree.
- **B. Guess by start time and working folder.** Rejected: it would stop
  programs a person started themselves in the same folder.
- **C. Kernel process events.** Exact, but Endpoint Security needs root and
  an entitlement Apple grants, `eslogger` is not to be depended on, and the
  kernel's event queue does not name a fork's child. Rejected.
- **D. Start each command inside a kernel group.** On Linux a cgroup keeps
  every descendant, but the command must be placed in it when it starts,
  which a hook cannot do; macOS has no equivalent. Section 11 uses Claude
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
else of any environment. A stamp only ever exposes the process that carries
it and what is under it: no process can make the guard stop another by what
it puts in its own environment.

### 4.1 Agent stamp

`CLAUDE_PID` names the Claude Code process. It counts only when it names an
agent the guard knows (5.4): alive, with the start time the guard holds for
it, and started before the stamped process.
Sessions of a Claude Code older than v2.1.214 have no agent stamp.

Claude Code gives `CLAUDE_PID` to hook commands too (2.1 fact 11), so on its
own it does not show that a command forked a process. A Claude Code process
is tied by its stamps only when it carries the call stamp as well, and the
two agree.

### 4.2 Call stamp

A new hidden command, run as a SessionStart hook beside the costs hook,
appends one line to `$CLAUDE_ENV_FILE` unless it is already there:

```sh
export WORKBENCH_GUARD_CALL="$CLAUDE_PID:$$:${EPOCHSECONDS:-$(date +%s)}"
```

Every Bash and Monitor command of the session then carries its agent's
process ID, and the process ID and start second of its own shell, which is
the process the guard tracks as the command's root (2.1 fact 18). The guard
names every process by its ID and start time, because an ID alone is reused;
the stamp names the shell the same way. A shell without `EPOCHSECONDS` runs
`date` once per command. The stamp counts only when its agent part
equals the `CLAUDE_PID` of the process that carries it. A nested session a
command starts, and everything that session's hooks start, inherit the outer
command's stamp beside their own `CLAUDE_PID`; the two disagree, so the stamp
does not count (2.1 fact 12).
A process a hook starts never carries the call stamp (2.1 fact 11), which is
what keeps a daemon some hook started from being taken for a command's work.
A session already running when this ships has no call stamp until it
restarts; until then its detached work is found only through its original
parent (5.3).

The same hook tells the guard that its agent runs (5.4), under Claude Code
and under Codex. Without `CLAUDE_ENV_FILE`, as under Codex, it writes no
stamp and still says its agent runs. With `WORKBENCH_GUARD=off` it writes
nothing. It prints nothing and never fails.

### 4.3 Codex

A Codex command's processes carry `CODEX_TOOL_CALL_ID`, which is the note's
own `tool_use_id`. It is both the agent stamp and the call stamp: it resolves
to the note or to the record kept of it (5.5), which names the agent. A
Codex session that a Claude Code command started also carries that outer
agent's stamps (2.1 fact 17): a Codex stamp that resolves wins, because the
Codex agent is the nearer one. When it resolves to neither, the outer stamps
decide as for any other process. A Codex hook's command does not carry the
call's ID (2.2 fact 14), so a daemon a hook starts is not taken for a
command's work.

### 4.4 The guard's own setting

`WORKBENCH_GUARD` is read from the same process: `off` makes it no unit,
`dry-run` makes its stops log only, as for a call's note. A unit whose
environment is hidden takes the setting of the call that started it.

## 5. Detached units

### 5.1 Finding them

Each tick the watcher already lists every process. It classifies, once per
process ID and start time, each process of its own user that is a child of
the init process and outside every protected process's tree. A process whose
environment the kernel hides is looked at again only through the new
processes under it.

### 5.2 What a unit is

A process R is the root of a unit when all of these hold:

1. R's parent is the init process (PID 1, or an init the guard knows by
   name).
2. R was forked by another process, not started by the init process. On
   macOS its original parent (`p_orig_ppidversion`) is not the init
   process's version; when the kernel does not answer, R is no unit. This is
   what keeps an app started with `open` out (2.1 fact 5). Linux and WSL
   keep no such record, so there R counts only when the guard itself saw it
   in a tracked tree before the init process adopted it (5.3).
3. R is tied to an agent the guard knows, by a stamp or by its original
   parent (5.3).
   - By a stamp, on macOS only: R carries the call stamp and the agent
     stamp, agreeing, or Codex's one stamp. When R's environment is hidden,
     they are read from the first readable processes below it, reached
     through hidden ones only: at least one carries them and none carries
     another agent's. On Linux and WSL a stamp ties nothing, because a
     service the init process starts can carry a command's variables (2.2
     fact 15) and nothing there tells it from a process the command forked.
   - By its original parent: 5.3.
4. R started after that agent, and after the call's shell when the call
   resolves (5.5), by the kernel's start times.
5. R is not a Workbench process and not a protected process.
6. R is not itself an agent the guard knows (5.4). A session a command
   started and left detached is an agent, with a row of its own, not a unit.

By either tie, R is no unit of a Claude Code agent when the `CLAUDE_PID`
read for it names another process, or when a Codex stamp read for it
resolves to another agent: it is that agent's, to be judged by that agent's
stamps.

A unit one of whose roots turns out to be an agent drops that root, and is
forgotten when it has no other.

The unit is R and everything below it, with protected processes and what is
under them left out, as for a command's tree.

### 5.3 The original parent

Each tick the guard records the `p_idversion` of every process in each
tracked command's tree and each unit, and keeps a process's versions for a
short while after it is last seen. A new child of the init process whose
`p_orig_ppidversion` is one of those was forked by that process, so it is
tied to that call's agent, whatever its environment holds. A restricted
program, a wiped environment, a background job left behind when its call
returns and a fork made in the same tick as the parent's exit all resolve
this way (2.1 fact 6, measured for a single fork and for a fork after an
exec).

A process the guard itself saw in a tracked command's tree or in a unit, and
that the init process later adopts, is tied the same way on every system:
the guard saw where it came from and needs no kernel record for it.

A new child of init forked by a process of an existing unit joins that unit.

It does not see through a double fork whose middle process lived and died
between two ticks: that process was never recorded, and the kernel keeps
nothing of a process that has exited. Such a unit is found by its stamp or
not at all (section 10).

This is macOS only. The call it uses is the one condition 2 already depends
on, which is not in the public SDK (2.1 fact 7). If the kernel stops
answering it, no units are made, the guard is back to what it does today, and
it logs that once.

### 5.4 Agents the guard knows, and protected processes

The guard knows an agent, by process ID and start time and while it runs,
from either of two things: the SessionStart hook (4.2), which leaves a small
file naming its agent, and a note. The hook writes nothing else there, never
starts the watcher and never keeps it running; the file is removed once its
agent is gone, by the watcher or by the next session's hook. So a session is
known from its start, before its first command, whenever a watcher runs.
Only known agents count for a stamp.

Protected, with everything under them: every known agent that runs, whether
or not it has a note at the moment, the watcher, and every live process that
any stamp names as an agent and that started before the stamped process.

### 5.5 Which call started it

The guard keeps a record of each call whose note it read: its ID, the agent,
subagent, project and folder, whether its note was a dry run, and, once the
call was tracked, its shell's process ID and start time. So that every call
leaves one, the post hook marks a call's note ended where it deletes it
today, and the watcher deletes the note once it has the record. An ended note
is never matched to a command, so it cannot claim a later command with the
same text; it is not abandoned and adds no warning the guard does not log
today. The post hook does not ask whether a watcher runs, which it could
only do by taking the watcher's lock for a moment: an ended note waits for
the next watcher, and every pre hook starts one when none runs. Records
outlive their calls while the agent runs, the newest few thousand in all,
and pass to the next watcher at a handover with the tracked commands. A
watcher that starts fresh has none.

The first of these that applies:

1. A unit tied by its original parent belongs to the call whose tree held
   that parent.
2. A Codex call stamp resolves to its note or record.
3. A Claude Code call stamp resolves to the record of its agent whose shell
   has the stamp's process ID and started in the stamp's second, give or
   take the moment a shell needs to start. A later shell that reuses the ID
   started at another time, so a stamp never resolves to another call.
4. Otherwise the row has the agent from the agent stamp, the project from
   R's working folder, and a blank subagent.

A Claude Code call that ended within one tick was never tracked (2.1 fact
9), so its record holds no shell and detached work found only by its stamps
takes case 4. A Codex call's record resolves by its ID whether or not the
call was tracked.

A unit in case 4 is shown, and a person may stop it, but the guard never
chooses it. A command a person types with `!` carries the same stamp as an
agent's command and leaves no note (2.1 fact 16), so a stamp that resolves to
no call does not prove an agent's command forked the process, and the guard
stops only what it can prove.

A unit's call is resolved once, when the unit is found.

### 5.6 Lifetime

A unit is tracked while any of its processes lives and its agent runs.
Members the init process adopts after the unit's root exits stay in the unit.
The watcher does not exit idle while a unit is tracked. When the agent exits,
the unit is forgotten, not stopped, as a command is today.

## 6. Measuring and stopping

- A unit is measured each tick like a command, and its sizes are read the
  same way.
- Units whose call resolved compete with commands for the largest. The
  floor, the cooldown, the refusal count and the dry run apply to them
  unchanged. A unit whose call did not resolve is never chosen (5.5), and no
  unit is chosen in its first ten seconds: a session a command started needs
  that long to reach its SessionStart hook and be known as an agent.
- The same target choice signals a unit: its root usually leads its own
  process group, which is signalled as a group when every member is in the
  unit.
- The reason is told to the call that started the unit when that call
  resolves and its command still runs in the foreground, through that call's
  kill record, in words that name the detached program. Otherwise the stop
  is in the log only, as for a command whose result already returned.
- The log and the reason name a unit by its root program's name, never by
  its arguments: a call's text is logged as the agent wrote it, but a
  running program's arguments hold what the shell put there, a token read
  from a variable included. The full command line is in the view only, as a
  process list would show it.
- A person stops a unit from the view the same way as a command.

## 7. Agent and helper rows

- Each agent the guard knows is a row with its own size, not its tree's.
- A helper is a direct child of such an agent that no note claims, that the
  guard has seen alive for several seconds, that is not a shell running a
  command script, and in whose tree no readable process carries a
  `CLAUDE_PID` naming that agent: a language server, an MCP server (2.1
  facts 10 and 14). It is a row with its tree's size.
- A direct child that no note claims is a command the guard could not match
  when a readable process in its tree carries a call stamp naming that child
  as its shell, and the guard has seen it alive for several seconds. Today
  it is unseen while it runs. It is a row with the status `unmatched` and its
  tree's size while it runs, counted with the commands in the memory line,
  and is logged when it ends as today. A command a person typed with `!` is
  such a row too (2.1 fact 16). A hook or a status line command carries
  `CLAUDE_PID` but no such stamp, so it is neither this nor a helper and has
  no row.
- Parent links decide these rows, so they hold on every platform.
- They show while the watcher runs, and the watcher's lifetime does not
  change. It still exits after ten minutes with no note, no tracked command
  and no unit, and the rows go with it until the next command. Keeping the
  watcher running for as long as any known agent does was considered and
  dropped: these rows are display only, and the view design's rule of no
  watcher while nothing is guarded stands.
- The guard never chooses an agent, a helper or an unmatched command for a
  stop. A person may stop a helper or an unmatched command from the view,
  through the watcher like any stop. A stop of an agent row is refused.
- Agent and helper rows are not written to the guard log while they run. A
  person's stop of a helper or an unmatched command is logged with the
  program's name only: a helper's command line is not a call's text and may
  hold what its owner did not mean to keep on disk. The view shows it, as a
  process list would.

This changes a boundary of the view design, which showed only commands the
guard tracks.

## 8. View, log and protocol

Every row uses the view's own columns. A detached unit's ID is its root's
process ID, its status reads `detached`, its time counts from the root's
start, and its command is the root's own command line. An agent's and a
helper's status read `agent` and `helper`, and a running command no note
claims reads `unmatched`, as it does today once it has ended:

```
   ID        STATUS      AGENT   SUBAGENT        TIME   MEMORY     PEAK  PROJECT  COMMAND
   01PnU61k  ● run       claude  implementer   12m58s     6 MB   166 MB  shop     scripts/build.sh api
   p87305    ● detached  claude  implementer    4m36s  4.20 GB  4.20 GB  shop     /bin/zsh scripts/build.sh --worker api
   p81483    ● helper    claude                 44m2s  1.39 GB  1.41 GB  shop     tsc --lsp --stdio
   p12253    ● agent     claude                  2h6m   739 MB   802 MB  shop     claude
```

Under the memory bar, one line splits the memory in use, each part by the
size its rows show:

```
in use 12.05 GB · commands 40 MB · detached 4.20 GB · agents 1.36 GB · helpers 1.39 GB · other 5.06 GB
```

"Other" is the memory in use less the four parts. A container engine's
virtual machine or a launchd job is in it, without the guard claiming an
agent started it.

The details screen of a unit names the call that started it when it
resolved. The log's `tracking`, `ended` and `stopping` events for a unit
carry its kind, root, root program's name and launching call. A snapshot's command gains its kind
and launching call, and a snapshot gains the five sums; all are additions an
older view ignores.

## 9. The Monitor post hook

A Monitor command is tracked, but no post hook clears its note (2.1 fact 8),
so the note lingers until the guard abandons it with a warning, and a stop's
reason cannot reach the agent. Claude Code's post hooks are matched on
`Bash|Monitor`, and the post hook marks a Monitor call's note the way it
marks a background call's: the post hook fires when the monitor starts (2.1
fact 15). The Codex hooks do not change.

## 10. What stays unseen

1. **A fast double fork on macOS with nothing readable.** A unit that
   detached through a middle process that lived less than a tick, and whose
   programs all hide their environment or had it wiped, is unseen until a
   readable program carrying the stamp appears in it.
2. **Wiped environments on Linux and WSL**, where there is no original-parent
   record, until section 11.
3. **Work handed to a service.** A container engine, a launchd or systemd
   job, or a build server a person started was never the command's
   descendant. Its memory is in "other".
4. **Fast calls.** Detached work found only by its stamp, from a Claude Code
   call that ended within a tick or from a command a person typed with `!`,
   shows no subagent and is never chosen by the guard. A person can stop it
   from the view.
5. **Other users' processes.** A root-owned process can be neither sized nor
   signalled.
6. **Linux and WSL find less.** With no original-parent record and no tie by
   stamp, detached work is found there only when the guard saw it in a
   tracked command's tree for at least one tick before it detached. A
   program that detaches at once, through a double fork, is unseen. Whether
   more can be found safely is decided with the checks of 2.3 and section
   11.
7. **A session without Workbench's hooks.** An agent session a command
   starts and leaves detached is known, and so never stopped, from its
   SessionStart hook. One started without the hooks, or still in its first
   moments, is a program like any other: it is a unit, and after ten seconds
   the guard may choose it, as it stops such a session inside a command's
   tree today.
8. **A program the agent opened for a person.** An editor or a browser a
   command starts is that command's work wherever the system does not hand
   it to the init process to start. On macOS apps go through `open` and are
   left out (5.2); on Linux an app a command forked and left running is a
   unit like any other.

No method open to a tool without root closes the first three. The ones that
would are in section 3, C and D.

## 11. Waiting for a check: Claude Code's cgroup on Linux and WSL

Where the owner turns on Claude Code's memory cap, every tool command of a
session shares one cgroup and every descendant is born into it (2.2 facts 6
and 9). The guard could then read the cgroup of a tracked root and treat its
members outside the agent's tree as that session's detached work, whatever
their environment. This is designed only after 2.3 item 1 is checked in a
Linux container, and applies only with the cap on.

Not proposed: Workbench moving commands into cgroups itself. systemd can do
it for an unprivileged user, but it costs a bus call per command and changes
how a command is accounted.

## 12. Left to the builder

The names of the variable, the hidden command, the statuses and the log
attributes; the bound on call records and how long a process's versions are
kept; the classification cache; how long a helper must be seen before it is a
row; how units and records pass at a handover; a row's full ID in the socket
protocol; the Linux details once 2.3 item 1 is known; whether the protocol
version moves.

## 13. Tests and checks

Tests for one risk, unauthorized execution: the guard stopping a program no
agent command forked, or an agent. The unit rule yields no root for a
process without a stamp and without a recorded original parent, for a stamp
naming a dead process, an unknown one or one younger than the stamped
process, for a process the init process started that carries a stamp, for a
process that carries the agent stamp without the call stamp as a hook's child
does, for one whose call stamp names another agent than its own `CLAUDE_PID`
as a nested session's does, for a stamp alone where the system keeps no
original parent, for a known agent, for a Workbench process, for a protected
process, and when the kernel does not answer. The guard's choice of what to
stop never falls on a unit whose call did not resolve or that is younger
than ten seconds, and a known agent inside a unit is left out of what a stop
signals.

Before release, in a disposable home on macOS and in a Linux container:

- A command that double-forks a sleeper gives a `detached` row with its
  agent, and with its subagent when a subagent made the call. A stop from the
  view ends it.
- A command that leaves a background job of a system program behind gives a
  `detached` row with its call.
- An app started with `open` from a command gives no row.
- A sub-second Claude Code command that leaves a sleeper gives a row with a
  blank subagent that the guard does not choose; the same under Codex gives
  a row with its call.
- A daemon a hook starts gives no row.
- A session a command starts and leaves detached is an agent row, not a
  unit, and no stop reaches it.
- An agent with a language server shows an agent row and a helper row, and
  the five sums add up to the memory in use.
- A Monitor command's note is gone once it ends, with no `abandoned` warning.
- `scripts/render-check.sh` for each role and mode the hook changes touch.

## 14. Docs to revise

- The guard design's section 12 line on processes that leave the command's
  tree, and the view design's section 1 line on what is shown, its columns
  and its protocol section.
- A new ADR for recognizing detached work by stamps and the original-parent
  record, and for the agent and helper rows.
- `docs/usage.md`: the guard section's sentence that detached programs are
  not tracked, its sentence that the guard never stops MCP servers or
  anything else of the agent's, which now holds for the guard's own stops
  and not for a helper a person stops from the view, and the view's rows and
  memory line.
- The package comment of `internal/guard`, which says the same.
- `AGENTS.md`: the guard row of the ownership table gains the SessionStart
  hook, which both agents' hook files now carry.
- `docs/acceptance.md` once the checks in section 13 have run.

## 15. Sources

- Claude Code environment variables: <https://code.claude.com/docs/en/env-vars>
- Claude Code hooks: <https://code.claude.com/docs/en/hooks>
- Claude Code tools reference, memory limit on Linux and WSL:
  <https://code.claude.com/docs/en/tools-reference>
- Codex exec server, child environment:
  <https://github.com/openai/codex/blob/main/codex-rs/exec-server/README.md>
- Codex hooks: <https://developers.openai.com/codex/hooks>
- Codex, a hook command's environment:
  <https://github.com/openai/codex/blob/main/codex-rs/hooks/src/engine/command_runner.rs>
  and
  <https://github.com/openai/codex/blob/main/codex-rs/protocol/src/shell_environment.rs>
- systemd's manager, `SetEnvironment()`:
  <https://www.freedesktop.org/software/systemd/man/latest/org.freedesktop.systemd1.html>
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
