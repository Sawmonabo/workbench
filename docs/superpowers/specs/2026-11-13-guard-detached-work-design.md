# Guard: detached work design

Status: design, Nov 13, 2026. The guard tracks a command by following parent
links down from the shell its agent started. A program that detaches from
that shell is no longer under it, so the guard neither measures nor stops it,
and a command whose real work runs detached shows as a few megabytes. This
design makes the guard recognize that work by what it inherits from the
command and by what the kernel records about where it came from. It also
clears a Monitor command's note. It builds
on the [agent command guard design](2026-10-14-agent-command-guard-design.md)
and the [platform and guard view design](2026-10-17-platform-and-guard-view-design.md),
and replaces the "processes that leave the command's tree" line of the guard
design's section 12.

Approach A of section 3 is the one built. Section 11 holds the one part that
waits for a check before it is designed further.

## 1. Goal and boundaries

- A program that an agent's command started and that then detached (a double
  fork, `setsid`, `nohup … &`, a background job left behind when its call
  returns) is its own row in `workbench guard memory`, under the command that
  started it, is measured each tick,
  and is a candidate when the guard must stop the largest agent command.
- The rule names no script, tool or project. A process is detached work when
  it was forked by another process and not started by the init process, is no
  longer under its agent, and was itself seen in a command's tree before it
  detached, was forked by a process the guard saw in a command's tree, or
  carries a stamp of an agent the guard knows.
- A row always names its agent. It names its subagent and call only when the
  guard can prove which call started it, and is blank otherwise. Nothing is
  guessed. The guard's own stop takes detached work only when its call is
  proved and its setting was read; anything else is shown and left to a
  person.
- The guard never chooses an agent it knows, or a helper the agent starts
  itself, for a stop, and the view does not show them. An agent session
  the guard does not know yet is a program like any other; 5.4 says which
  sessions those are and for how long.
- The only change to how a command runs is one more variable in its
  environment (section 4.2).
- Not in scope: anything that needs root or an entitlement from Apple;
  attributing work a service did on a command's behalf (a container engine, a
  launchd or systemd job); stopping a process that carries no stamp and that
  no process the guard saw forked.
- Anti-goal: stopping a process that no agent command forked, or one that
  opted out. A process the guard misses is a gap. A process it stops wrongly
  is a fault. Every doubt is a no.

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
9. A Bash command that ends within one watcher tick is never tracked by the
   tick, so the tick alone never learns its shell's process ID. In one
   session the shell that ran a short command lived about 22 ms (which of
   the session's shells ran the command is inferred from their timing).
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

The facts below were measured on macOS 27.2 (kernel `xnu-13432.41.4`), SIP
on, with Claude Code 2.1.325 and Codex 0.169.0 where an agent took part.

21. A process's version (`p_idversion`, fact 6) changed at every exec
    measured, `execve` and `posix_spawn` with `POSIX_SPAWN_SETEXEC`, including
    an exec of the same path with the same arguments, which kept the process
    name and the command line. It did not change when a program rewrote its
    title: a C program overwriting its argument area, Ruby's `$0=` (rbenv
    and `/usr/bin/ruby`), Node's `process.title=` and Perl's `$0=`
    (`/usr/bin/perl`). Each of those changed the command line and kept the
    process name.
22. The kernel's `P_EXEC` flag, which the process listing the watcher reads
    already carries (`kinfo_proc`'s `p_flag`), was clear on every forked
    child that had not exec'd (100 of 100 in every series, a Perl daemon's
    child included) and set on every `posix_spawn`ed shell. The watcher uses
    it to tell a forked child that has not exec'd yet (5.3).
23. A process of the same user can attach `NOTE_FORK`, `NOTE_EXEC` and
    `NOTE_EXIT` (`EVFILT_PROC`) to a process that is not its child, without
    privileges; `NOTE_TRACK` is refused. A fork event carries no child PID
    (its data is 0): it is a wake-up, after which the watcher lists the
    process's children.
24. Claude Code starts a command's shell with `posix_spawn`: in one session
    every `zsh` it started was already exec'd when first seen, and no exec
    followed on it. Every child such a shell forked recorded, as its original
    parent's version, the version read for the shell at the agent's fork
    event. Hooks run as `sh -c`, `posix_spawn`ed, then exec once.
25. Woken by an agent's fork event and listing its children at once, a
    watcher held the shell's version that the leftover's original parent
    named in 100 of 100 runs for a command of no time, 5, 20, 100 and 500 ms
    (Homebrew bash 5.3 as the shell, a never-exec'ing subshell as the
    leftover), with a median of about 0.4 ms from spawn to the version; also
    100 of 100 through `Children` and `Follow` as built, and when the spawner
    forked then exec'd, by the exec event. A double fork made by the shell
    itself was caught 100 of 100 by listing the shell's children on its fork
    events. A double fork made by a program below the shell (a Perl one-liner
    that forks twice) was caught 0 of 100: nothing below the shell is
    followed. A shell forking 500 times a second cost such a follower about
    4.3 % of a core (measured for 0.6 s; about 6.8 % in a 10 s run of
    research code), both logging every read; a build below the shell cost nothing, since its forks
    come from processes not followed.
26. Sampling instead of waking, listing an agent's children every 2, 5 or
    10 ms, caught a single fork 100 of 100 at 2 and 5 ms and 85 of 100 at
    10 ms for the shortest commands, and a double fork's middle process 10 to
    68 of 100 even when it also listed the children's children; at 2 ms it
    cost about 40 ms of CPU per call against 1 to 2 ms for the wake-up.
27. A Claude Code session started by an agent's command: its own
    subprocesses (first seen 322 and 431 ms after it started) and its shell
    snapshot's programs (`bash`, `node`, `uv`, from 2.8 to 5.2 s) carried
    the outer agent's `CLAUDE_PID` and a call stamp. That the stamp's agent
    part equals that `CLAUDE_PID` is inferred from inheritance, not read.
    Only its hooks and the commands its Bash calls ran carried its own
    `CLAUDE_PID`.
28. When a session becomes known (its agent file is written), counted from
    its process's start: Claude Code 2.1.325, 0.31 to 0.69 s (four runs,
    three of them started by an agent's command); Codex 0.169.0 `codex exec`, 2.3 to 3.5 s
    (two runs); Codex with one required MCP server that answered after 15 s,
    17.1 s (one run).
29. The kernel hid the environment of exactly the processes flagged
    `CS_RESTRICT` among those tested: `/bin/sleep`, `/bin/zsh`, `/bin/bash`,
    `/bin/sh`, `/usr/bin/perl`, `/usr/bin/ruby`, `/usr/bin/tail`,
    `/usr/bin/awk`, `/usr/bin/osascript` and `/usr/bin/xcrun` until it
    execs. Readable: the programs `/usr/bin/python3`, `make`, `git` and
    `clang` exec under the same PID, Homebrew and rbenv interpreters, nvm
    Node, Go and the programs it builds, bun, Xcode's clang and rustup's
    rustc. No reader without privileges or a private entitlement was found:
    `task_for_pid` was refused, and `ps -E`, `launchctl procinfo`, DTrace and
    `eslogger` read nothing more or need root.
30. After `/usr/bin/perl` rewrote `$0`, its command line read through
    `kern.procargs2` held an environment string's name among its arguments.
    A C program that rewrites its title, started by Homebrew bash, `/bin/bash`
    or `/bin/zsh` as `WORKBENCH_GUARD=off CLAUDE_PID=… program ARGS`, read
    with its setting empty or its agent stamp missing in some of those
    shapes when only the environment proper was read, and with both in every
    shape when the trailing `NAME=value` strings among its arguments were
    read too (4.4).
31. One read of a process's version (flavor 17 of `proc_pidinfo`) took 0.27
    to 0.95 µs; one read of its environment (`kern.procargs2`) 4.8 to
    10.2 µs on average, 110 µs at worst; `proc_listchildpids` 6.2 µs;
    `kern.proc.all` 107 µs with 543 processes.
32. A Go map model of the versions kept took 13.0 MiB of heap at 65,536
    versions, about 208 bytes each; at the bound of 16,384 that is about
    3.3 MiB (computed, not measured). The watcher's soft memory limit is
    32 MiB of what the Go runtime counts (`Sys` less `HeapReleased`), not of
    its resident size. The watcher as built was measured against one built from the
    release before this design, on macOS, ticking once a second, under two
    loads: a command holding 1,400 `sleep` processes, and a command running
    650 shell loops of `/bin/sleep 1` (about 650 new processes a second) for
    75 s. Memory was read every 5 s over a 60-second window and counted as
    the limit counts it (`Sys` less `HeapReleased`); CPU is the watcher's CPU
    time over the window. CPU a tick was 0.4 to 2.3 ms higher (7.9 to
    10.4 ms against 6.1 to 9.0 ms), higher in all six pairs of runs; one
    build varies by about 2 ms from run to run. The collector used 0.03 %
    of the CPU or less. On the same loads, with the counted memory read
    every 5 s and the resident size once a second over the whole run, the
    watcher as released peaked at 22.8 MiB counted and 32.1 MiB resident
    under the churn, and at 15.3 MiB counted and 27.5 MiB resident under
    the held processes; the earlier release peaked at 18.8 and 26.1 MiB
    under the churn and 17.3 and 31.7 MiB under the held processes. The
    difference under the churn is the store, 5 to 7 MiB of live heap at its
    bound (inferred from the churn rate; the watcher does not report the
    store's size). The cost at the 100 ms pressure tick is not measured.
33. At a handover with the store full (16,012 entries), the old watcher
    wrote 4,054,865 bytes to the next one in 9.1 ms, and the next one's read
    ended 1 ms after the write.
34. Woken at each agent's fork, a watcher tied the work of sub-second calls
    that left a never-exec'ing subshell under Homebrew bash to the call, with
    its subagent, in 30 of 30 runs (no time, 20 and 200 ms), and in a real
    Claude Code 2.1.325 session tied `( sleep 300 & ) ; true` to its call. A
    call's shell forking 300 times and an agent forking 300 times were capped
    after 251 and 101 wakes in their second. Following cost about 4 ms of
    the watcher's CPU per call, each call making three children of the
    agent (the pre hook, the shell, the post hook). A research follower
    attached to a real Claude Code 2.1.325 session's process from its start,
    following the session and each new child as the watcher does, was woken
    85 times over the whole of one throwaway session, with 61 listings of
    children and 18 ms of CPU.

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

Items 16 to 21 are read in Apple's kernel at tag `xnu-12377.121.6`, the
newest published; the running kernel's source is not published, and the
measurements in 2.1 agree with it.

16. Every exec gives the process a new version. `exec_handle_sugid`, which
    `exec_mach_imgact` calls for `execve` and `posix_spawn` alike: "Update
    the process' identity version … ensure we always see a modified identity
    version", looping `OSIncrementAtomic(&nextpidversion)` until the new
    version differs from the old one.
17. `sysctl_procargsx`, which serves `kern.procargs2`, omits the environment
    unless the target is the caller, is not `cs_restricted`, SIP is off or
    the caller holds `com.apple.private.read-environment-variables`; a
    process can also mark itself restricted (`CS_OPS_MARKRESTRICT`, in
    `kern_proc.c`). It
    finds the end of the arguments only by counting `p_argc` strings, and
    copies the area as the process holds it now, so a program that merged
    its argument strings when it rewrote its title moves environment strings
    into what reads as its arguments.
18. `filt_procattach` checks permission only for `NOTE_EXIT|NOTE_EXITSTATUS`
    and refuses `NOTE_TRACK`; `filt_procevent` fills the event's data only
    for `NOTE_EXIT`. The SDK's `sys/event.h`: the child's PID "does not get
    passed through in the actual kevent". A fork posts `NOTE_FORK` on the
    parent, and `posix_spawn` posts it on the parent after the image is
    loaded.
19. `P_EXEC` ("Process called exec") is set in `exec_mach_imgact`; a forked
    child starts with it clear, since fork copies only a fixed list of other
    flags; `proc_pidinfo` reports it as `PROC_FLAG_EXEC`.
20. PIDs are handed out one after another and wrap at `PID_MAX` (99,999)
    back to 100 (the wrap in `kern_fork.c`, `PID_MAX` in
    `bsd/sys/proc_internal.h`), so the PID of a process that exited is
    handed out again in time.
21. The version read through flavor 17 cannot be read wrong for a reparented
    process: `p_orig_ppidversion` "doesn't change if reparented"
    (`proc_info_private.h`). The flavor fails only for a process that does
    not exist or by a MAC policy (`proc_info.c`).
22. `man 2 execve`: the caller passes the new program's environment, so a
    program may run another without a variable it was given.
23. Codex (`openai/codex` at commit `048431dc`) installs its MCP servers at
    session start, queues SessionStart, and runs it inside the first turn
    (`run_turn` in `codex-rs/core/src/session/turn.rs`) after preparing the
    turn's required MCP servers, whose startup timeout defaults to 30 s
    (`DEFAULT_STARTUP_TIMEOUT` in `codex-rs/codex-mcp/src/rmcp_client.rs`).
    An idle Codex session started without a prompt never runs it.
24. Claude Code's hooks reference: SessionStart hooks "run in parallel" with
    the event's other hooks and fire "before the session's MCP servers are
    available"; a `command` hook is cancelled at its `timeout` (Workbench
    sets 5 s on its session hook). A PreToolUse or PermissionRequest hook
    matched on `Bash` also runs for Monitor commands from Claude Code
    2.1.314.
25. GitHub's Actions runner (tag `v2.338.2`, `JobExtension.cs`) reads the
    tracking variable of each process left after a job, ignores a failed
    read, and kills a process only when the value matches; on macOS it reads
    it with `ps e`, which reads `kern.procargs2`, so a restricted process is
    never matched. Jenkins (tag `jenkins-2.590`, `ProcessTree.java`) kills
    only processes whose environment matches every variable of its model, and
    a failed read gives an empty one, which never matches.
26. Linux, read at `v6.12`: `PF_FORKNOEXEC` ("Forked but didn't exec",
    `include/linux/sched.h`) is set for a new process in `copy_process`
    (`kernel/fork.c`) and cleared in `begin_new_exec` (`fs/exec.c`), and
    `/proc/PID/stat` gives the flags word as field 9 (`proc_pid_stat(5)`).
    `environ_read` (`fs/proc/base.c`) copies the environment area as the
    process holds it now, and Perl uses that area for a longer `$0` (`perl.c`,
    `perl_parse`: "Can we grab env area too to be used as the area for $0?"),
    so a retitled program's variables can be gone from it.

### 2.3 Not yet verified

1. Everything in 2.1 on Linux and WSL: which process adopts an orphan, that
   its environment is readable, how the memory cap's cgroup looks from
   outside, and whether a command run in a sandbox sees its shell's process
   ID as the watcher does or one of its own namespace.

2. Everything new in 2.1 (facts 21 to 34) on Linux and WSL, and the cost of
   the rules of section 5 there, where a process's program is told by its
   name and command line, read from `/proc`.

Nothing on Linux, WSL or Windows was run for this design; those systems are
unqualified for it, and `docs/acceptance.md` lists what was observed there.
Little is built on these facts: a stamp ties nothing on Linux and WSL (5.2),
no stamp is read inside a tracked tree there (5.4), and fork events are not
followed there (5.3). Stamps read for a detached root, and below a hidden
one, still protect a process they name and carry the opt-out there, which can
only make the guard stop less.

## 3. Approaches considered

- **A. Inherited stamps, checked against the kernel's record of the original
  parent (chosen).** It is the method the Actions runner uses where a tool
  cannot control how processes are grouped, and it needs no privileges. It
  costs one version read per tracked process per tick and one environment
  read per program (2.1 facts 31 and 32).
- **B. Guess by start time and working folder.** Rejected: it would stop
  programs a person started themselves in the same folder.
- **C. Kernel process events as the way to know a fork's child.** Exact,
  but Endpoint Security needs root and an entitlement Apple grants,
  `eslogger` is not to be depended on, and the kernel's event queue does not
  name a fork's child (2.2 item 18). Rejected as a way to know which process
  a fork made. The event queue's fork events are used as a wake-up only
  (5.3): they need no privileges for a process of the same user (2.1 fact
  23), and on a wake the watcher lists the children itself. Sampling the
  agent's children from the pre hook instead was weighed and not chosen: it
  misses by timing and misses most double forks, at about ten times the CPU
  of the wake-up as built (2.1 facts 26 and 34).
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
reads only the names below, with `platform.ReadEnvironment`, and keeps or logs
nothing else of any environment. A stamp only ever exposes the process that carries
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
`dry-run` makes its stops log only, as for a call's note. Where a root's
environment is hidden, it is read from the first readable processes below
it, reached through hidden ones only. When nothing readable is found, the
setting is not known: the root is shown and measured, a person may stop it,
and the guard's own stop never takes it (5.2, section 6), as the Actions
runner and Jenkins stop only what a positive read matched (2.2 item 25).
Its unit is a dry run when its call's record is one, or when its agent's
own environment says `dry-run` or `off`.

`off` is final for the life of the process: the process said so itself. A
program it starts may run without the variable (2.2 item 22), and the
process still stays out, with everything recorded below it (5.3). What it
starts is judged by its own stamps: a program it runs with the variable
removed (`env -u`, a build tool that filters its tasks' variables, 2.2 item
13) reads as not set. A program with an empty environment (`env -i`) reads
as unreadable, like a restricted one, unless it sets a variable of its own.
A hidden root whose first
readable programs read so is judged by them, and a program that leaves an
opted-out root and later detaches is tied by its own stamps like any other
(section 10). No registered opt-out exists beside the variable.

On macOS a program that rewrote its title keeps its setting and its agent
stamp. A title written past the arguments can push environment strings into
what reads as the arguments (2.1 fact 30, 2.2 item 17), so `WORKBENCH_GUARD`
and `CLAUDE_PID` are also read from the trailing `NAME=value` strings among
the arguments. Either can only make the guard do less: a `WORKBENCH_GUARD`
read there keeps the program out, so a real argument such as `make
WORKBENCH_GUARD=off` does too, and a `CLAUDE_PID` read there protects the
process it names and never ties. A tie by stamp needs the call stamp and
`CLAUDE_PID` in the environment proper, and Codex's call ID is read from the
environment only.

On Linux a title written over the environment area wipes the variables
there (2.2 item 26), so an environment area holding a string that is not a
variable reads as unreadable, and such a process is treated as one whose
environment is hidden: it does not keep its setting. When it was recorded
in a tracked command's tree before it detached, it is tracked, held
`unread`, never chosen by the guard itself, and a person can still stop it
(5.2, section 10 item 12).

## 5. Detached units

### 5.1 Finding them

Each tick the watcher lists every process. It judges each child of the init
process that is its own user's and not a zombie: once per process ID and
start time, again at each tick while nothing readable was found for it, and
again when a "no" that rests only on the absence of evidence is followed by
another program (5.2). A final "no" is never judged again. The init process
is PID 1, or a process named as an init the guard knows, such as a
container's; a PID 1 that is protected (an agent run as a container's init)
is no init, so its commands' shells are never taken for detached work. No
environment is read for a root that the rules of 5.2 reject before the
stamp rules (another user's, under a protected process, an agent, Workbench,
or started by the init process).

### 5.2 What a unit is

A process R is the root of a unit when all of these hold:

1. R's parent is the init process (5.1).
2. R was forked by another process, not started by the init process. On
   macOS its original parent (`p_orig_ppidversion`) is not the init
   process's version; when the kernel does not answer, R is no unit. This is
   what keeps an app started with `open` out (2.1 fact 5). Linux and WSL
   keep no such record, so there R counts only when the guard itself saw it
   in a tracked tree before the init process adopted it (5.3).
3. R is tied to an agent the guard knows, in this order:
   - by its own entry in the store: the guard saw R in a tracked command's
     or unit's tree before the init process adopted it (5.3), on every
     system;
   - by its original parent, on macOS: the version R's original parent had
     is recorded in the store, for a process of a tracked tree (5.3);
   - by a stamp, on macOS only: R carries the call stamp and the agent
     stamp, agreeing, or Codex's one stamp. When R's environment is hidden,
     they are read from the first readable processes below it, reached
     through hidden ones only. On Linux and WSL a stamp ties nothing,
     because a service the init process starts can carry a command's
     variables (2.2 fact 15) and nothing there tells it from a process the
     command forked.
4. R started after that agent, by the kernel's start times.
5. R is not a Workbench process, not protected and not under a protected
   process.
6. R is not itself an agent the guard knows (5.4). A session a command
   started and left detached is an agent, with a row of its own, not a unit.

R is also no unit when a stamp read for it says `WORKBENCH_GUARD=off` (4.4),
when its Codex stamp resolves to another agent, when, for a tie a Claude Code
agent owns, its `CLAUDE_PID` names another process, and, for a tie by stamp,
when its call stamps name two different agents.

**Final and not final.** A "no" that rests on evidence is final for the life
of the process (its PID and start time): another user's, started by the init
process, protected, a known agent, Workbench, another agent's, two agents',
off, or tied through what was found not to be this call's work (5.3). It
never becomes a unit, whatever it runs later. A "no" that rests only on the
absence of evidence (not yet a child of the init process, its lineage could
not be read, nothing ties it yet, its agent is not known yet) is judged again
once the process runs another program.

**Another program.** On macOS a process runs another program exactly when
its version changed: the kernel raises it at every exec and at nothing else
(2.1 fact 21, 2.2 item 16). A program that rewrites its title keeps its
version, and one that execs the same path with the same arguments gets a new
one. On Linux and WSL, which keep no version, a process runs another program
when its process name or command line changed. That rule holds for a
tracked root and for every stamp a tracked root's judgment reads (the
root's own, those of the processes below it, and its agent's own setting).
Everything else is told apart by process name alone, so no command line is
read for it at every tick: a child of the init process that is not yet a
root, with the stamps read when it is first judged; and an agent's
own stamp read outside a root's judgment. A failed or empty read never
counts as a change. The same rule decides when a root is judged again, when
a child of the init process whose "no" was not final is judged again, and
when a process's stamp is read again (5.4); a root's stamp is read again
whenever the root runs another program. A root's name, which the log
shows, and the command line the view shows (section 8) follow the process
whenever they differ; that alone resets nothing. On macOS a root's whole
command line is not kept: only the shown form is, read again at every tick;
on Linux the shown form is read again only when the command line changed.

**Settled roots.** A unit's root carries the program it was judged in, when
that program was first seen, and whether it is settled. It is not settled
(pending) while it runs another program than the one it was judged in, while
its program cannot be read, and while nothing readable was found for its
setting: it is tied by its entry or its original parent, its environment is
hidden, and so is that of every process below it reached through hidden ones
(4.4). A pending root is judged again by every rule above at each tick at
which the kernel answers and its unit's own agent runs. Once that agent has
exited (gone, or a zombie not yet reaped) the root is not judged again: it
stays pending, so the guard's own stop never takes it, and its unit ends
with its agent (5.6). One that is still a unit is settled once something
readable was found for it; one that stays unreadable stays pending for as
long as nothing readable appears below it. The guard's own stop takes only
settled roots (section 6).

**Leaving.** A root leaves its unit for good, and is never a unit again, when
it becomes an agent the guard knows, when it comes under a protected process
(a stamp names it as its agent), when, judged again, it is no unit for any
reason but a lineage that could not be read, and when what tied it is
dropped from the store (5.3). It takes with it what was recorded of it and of
everything below it (5.3). A unit left with no root ends with one `ended`
record giving the reason its last root left. A root that is still a unit in
its new program starts its ten seconds again (section 6), since the new
program may be a session yet to reach its SessionStart hook; one whose
stamps now say `dry-run` makes its unit a dry run.

**While the kernel does not answer** where processes come from, on macOS, no
new unit is made, no root is judged again, and every root of every unit is
pending, so the guard's own stop takes no detached work (section 6). A root
still leaves when it becomes a known agent, when it comes under a protected
process, and when what tied it is dropped from the store (5.3), since none
of these needs a kernel record. When the kernel answers again, each root is
judged again. The guard logs the loss once, and once more when the kernel
answers again.

The unit is R and everything below it, with protected processes and what is
under them left out, as for a command's tree.

### 5.3 The store of where processes were seen

At every tick, the watcher records in one store each process of every tracked
command's and unit's tree that a stop would signal: protected processes and
what is under them are left out, and zombies are skipped. An entry holds:

- its owner: the call or unit whose tree it was last seen in, with the agent;
- its parent entry: the recorded process that was its parent when it was
  first recorded, set once and never changed, so adoption by the init
  process does not change it. The shell of a noted command has none, so no
  chain ever leads from a call into its agent. A unit's root has the process
  that tied it: the recorded process that forked it, for a tie by its
  original parent; the parent it was first recorded with, for a tie by its
  own entry; none, for a tie by stamp;
- on macOS, every version the kernel gave it, each with when it was last
  seen.

A new child of the init process whose original parent's version is recorded
was forked by that process, so it is tied to that entry's owner, whatever
its environment holds. A restricted program, a wiped environment, a
background job left behind when its call returns and a fork made in the same
tick as the parent's exit all resolve this way (2.1 fact 6). A process the
guard itself recorded, and that the init process later adopts, is tied by
its own entry on every system. A new child of the init process forked by a
process of an existing unit joins that unit.

An entry stays while its process runs. Once the process is gone, its entry
stays while one of its versions is kept: 60 seconds after the version was
last seen, at most 16,384 versions in all: past that bound the oldest are
dropped down to 14,336 (2.1 fact 32). On Linux, which keeps no versions, the entry of a process that is
gone is removed at once. When an entry is removed for age or for the bound,
the entries that named it as their parent take its parent instead, so a
process that exits never breaks the chain from what it started to what
started it.

**What leaves takes its tree.** When a recorded process is protected at a
tick (a known agent, the watcher, a process a stamp names as its agent), and
when a child of the init process gets a final "no" (5.2), the store forgets
its entry, every entry whose chain of parents reaches it, and all their
versions. Every root of a tracked unit whose entry was forgotten this way
leaves its unit (5.2): the one that caused it with its own reason, any other
with "what started its root is not this call's work". So work a nested
session left detached before the session was known or protected is no
longer the outer call's once it is, even when it was tied through a process that
exited or exec'd before the session registered; and a program that opted
out takes with it what it started. A nested agent's own calls stay tied to
it: their shells have no parent entry, so forgetting the nested agent never
reaches them.

**The shell learned at its start.** On macOS the watcher follows the forks,
execs and exit of every known agent that runs, between its calls too,
through the kernel's event queue (2.1 fact 23). On an agent's fork it lists
the agent's children, oldest first; it follows each new child that is not
protected first, then reads it, so an exec is never missed, and matches it
against that agent's notes that have no tracked command, are not ended and
have not yet been given a shell, by the same matching rules as a tick. It
gives a matched note to the child only where the tick would give the same
one: the note's text is the only one of the agent's untracked notes with
that text, or the child runs in the best folder the note allows and no older
child of the agent is still waiting for its exec. Otherwise the call is left
to the tick. So each note goes to one shell, and two subagents starting the
same command at once each keep their own work.

A matched shell's call record gets the shell (its PID and start time), and
the store records the shell, with no parent, and its direct children that
are not protected, with the call as owner, each with its version read then;
no stamp is read. The shell is then followed for its forks (whose children
are recorded), its execs (whose version is recorded) and its exit, and
nothing deeper. Before each of these, the watcher checks that the shell is
still the call's: once it is protected (a stamp names it, or it exec'd into
a session) or the store no longer holds it under the call, it is no longer
followed and nothing more is recorded under it. A child that has forked and
not exec'd, which the kernel's own flag tells (2.1 fact 22, 2.2 items 19 and
26), waits, followed, for its exec; any other child that matched no note is
no longer followed. What the watcher keeps for this is forgotten at each
tick for processes that have ended. These records only make ties earlier,
never a new kind: the tick stays the one place that tracks, measures, logs
and decides stops, and a wake neither hurries nor delays it.

Two bounds keep the cost down, and neither is logged. A call's shell that
wakes the watcher more than 250 times in one second, by its forks and execs
together, loses its fork watch for the rest of its life (2.1 fact 25); its
exit stays watched and the tick still tracks it. An agent that wakes it more
than 100 times in one second is not followed for its forks for 5 seconds,
and a later tick follows it again; its calls are found at the ticks
meanwhile (2.1 fact 34). When the kernel refuses to follow an agent that
still runs, or cannot follow forks at all on a system that should, the guard
logs that once ("cannot follow an agent's forks") and finds that agent's
calls at the ticks. An agent that exits just as the watcher looks at it is
no refusal and gives no such record: macOS no longer hands out a process
that has begun to exit, though its process list still shows it running, and
the watcher keeps nothing for it. The exit of an agent the watcher watches
ends its wait as before, even while the process list still shows that agent
running. Where the kernel cannot follow
forks (Linux and WSL), the tick alone records, and nothing is logged. So work a sub-second call leaves detached is tied to the call by
its original parent (5.5), as it is for a longer call. The watcher handles
wakes between ticks only: a waiting child whose exec and exit both arrive
while a tick runs is dropped (section 10).

It does not see through a double fork made by a program below the shell
whose middle process lived and died between two ticks: nothing below the
shell is followed, that middle process was never recorded, and the kernel
keeps nothing of a process that has exited. Such work is found by its stamp
or not at all (section 10).

The store passes to the next watcher at a handover: each recorded process
with its owner, the recorded process it came from, and each version with
how long ago it was last seen, not a wall time, so a clock change between
the two watchers neither stretches nor shortens the minute a version is
kept. The next watcher merges it into its own store before its first tick
records the trees: an entry it already holds keeps its own owner and parent,
a version keeps its later sighting, a version older than a minute is left
out, and the bound applies as before. So a process found after a handover
not to be a call's work still takes with it everything it started before,
also through processes that exited before the handover. A watcher of an
earlier release carries no store, and one that does not know it ignores it;
either way the handover works as before. The follow state does not pass:
the next watcher follows the known agents itself.

The kernel call the store's versions come from is the one condition 2 of
5.2 already depends on, which is not in the public SDK (2.1 fact 7).

### 5.4 Agents the guard knows, and protected processes

The guard knows an agent, by process ID and start time and while it runs,
from either of two things: the SessionStart hook (4.2), which leaves a small
file naming its agent, and a note. The hook writes nothing else there, never
starts the watcher and never keeps it running; the file is removed once its
agent is gone, by the watcher or by the next session's hook. So a session is
known from its start, before its first command, whenever a watcher runs.
Only known agents count for a stamp.

Protected, with everything under them: every known agent, whether or not it
has a note at the moment, the watcher, and every process that any stamp
names as an agent and that started before the stamped process. Those
stamp-named processes pass to the next watcher at a handover, so they are
protected from its first tick. A protected process stays protected, and a known agent stays known, for as long as it is
in the process table, a zombie its parent has not yet reaped included, so an
exiting session is never shown inside the tree of the command that started
it.

**Stamps inside tracked trees.** On macOS, while it records a tracked tree,
the watcher reads each member's stamp from the second tick at which it
records it, once per program (5.2); its versions are recorded from the
first. A live process a member names as its agent, older than that member,
is protected from that tick, with everything under it, and forgotten from
the store with what was recorded below it (5.3), as it already is for a
detached root. So a Claude Code session started without the guard's hooks is
protected from the first readable program that carries its own
`CLAUDE_PID`, one tick after that program is first recorded (one second at
the normal pace, 100 ms under pressure), with no name hard-coded; work it
leaves detached in that tick is tied to the outer call only through the
store, which drops it once the session is protected (5.3). It is protected,
not known: its forks are not followed. One whose
programs are all hidden is never protected this way (section 10). Stamps are
read once per program a process runs, a hidden result included (2.1 fact
31). On Linux and WSL no stamp is read inside a tracked tree (2.3).

**Sessions the guard does not know yet.** The guard never stops an agent it
knows or protects. A session a command started is a program like any other
until then, and if it is left detached it is a unit the guard may choose
after its ten seconds (section 6):
- a Claude Code session with the hooks, until its SessionStart hook runs:
  0.31 to 0.69 s measured (2.1 fact 28);
- a Codex session with the hooks, until its SessionStart hook runs in its
  first turn: 2.3 to 3.5 s measured, 17.1 s with a required MCP server that
  answered after 15 s, and never for an idle session started without a
  prompt (2.2 item 23);
- a Claude Code session without the hooks, on macOS, until a readable
  program carrying its own `CLAUDE_PID` has been recorded at two ticks; one
  whose programs are all hidden, never; on Linux and WSL, never;
- a Codex session without the hooks, which sets no `CLAUDE_PID`: never;
- a Claude Code session before 2.1.214, which sets no `CLAUDE_PID`, when it
  runs without the hooks: never.
Section 10 items 7 to 9 give these as limits. Meanwhile such a session, left
detached by a call that returned at once, can be in the log as detached
work: a `tracking` record and then, once it is known, an `ended` one with
"its root is an agent". It is measured as such only until a tick finds it
known, which was within about a second in a real Claude Code session; the
guard could not choose it meanwhile (held `young`, and with no proved call).

### 5.5 Which call started it

The guard keeps a record of each call whose note it read: its ID, the agent,
subagent, project and folder, whether its note was a dry run, once the call
was tracked or its shell was learned at its start (5.3) its shell's process
ID and start time, and whether its command started an agent session. So that
every call leaves one, the post hook marks a call's note ended rather than
deleting it, and the watcher deletes the note once it has the record. An
ended note is never matched to a command, so it cannot claim a later command
with the same text; it is never abandoned and adds no warning. The post hook does not ask whether a watcher runs,
which it could only do by taking the watcher's lock for a moment: an ended
note waits for the next watcher, and every pre hook starts one when none
runs. Records outlive their calls while the agent runs, the newest 4,096 in
all, and pass to the next watcher at a handover with the tracked commands. A
watcher that starts fresh has none.

The first of these that applies:

1. A unit tied by its own entry or its original parent belongs to the call
   that owns that entry. A tie to a unit no longer tracked resolves to no
   call.
2. A Codex call stamp resolves to its note or record.
3. A Claude Code call stamp resolves to the record of its agent whose shell
   has the stamp's process ID and started in the stamp's second, or no more
   than 30 seconds before it. A later shell that reuses the ID started at
   another time, so a stamp never resolves to another call.
4. Otherwise the row has the agent from the agent stamp, the project from
   R's working folder, and a blank subagent.

A call resolved by a stamp is no call when its record belongs to another
agent, or when its shell started after R.

**A call that started an agent proves nothing by stamp alone.** A nested
Claude Code session's own subprocesses and shell setup carry the outer
agent's stamps (2.1 fact 27), so a stamp that resolves to the call that
started a session does not prove that call's command forked the process. A
call's record notes, for good, that its command started an agent session
when, at a tick, a protected process other than the watcher is the shell of
its tracked command or lies below it, or is or lies below a root of one of
its units, or when a known agent's own environment holds a call stamp or a
Codex call ID that resolves to the call. The note passes at a handover with
the records. A unit tied by stamp alone gets no call when its call started
an agent session, and one already made loses its call and its subagent: it
stays shown, a person may stop it, and the guard never chooses it; the dry
run its call's record gives still holds. A unit tied by stamp alone whose
call's record is no longer kept loses its call too, since it can no longer
be shown that the call did not start an agent. Ties by entry or original
parent are not affected: 5.3 handles them.

A call whose shell was never learned, because its command ended within a
tick and its shell started while no watcher followed its agent (before any
watcher ran, on Linux and WSL, or when the kernel refused), has no shell in
its record, so detached work found only by its stamps takes case 4. A Codex
call's record resolves by its ID whether or not its shell was learned.

A unit in case 4 is shown, and a person may stop it, but the guard never
chooses it. A command a person types with `!` carries the same stamp as an
agent's command and leaves no note (2.1 fact 16), so a stamp that resolves to
no call does not prove an agent's command forked the process, and the guard
stops only what it can prove.

A unit's call is resolved once, when the unit is found; it can only be lost
afterwards, never gained.

### 5.6 Lifetime and handover

A unit is tracked while any of its processes lives and its agent runs.
Members the init process adopts after the unit's root exits join it as
roots. The watcher does not exit idle while a unit is tracked. When the agent
exits, every one of its units is forgotten, not stopped, as a command is,
whatever its roots can show: at the same tick, with an `ended` record that
gives no reason, and a finished `done` line in the view (`stopped` when a
person had stopped it). After a failed tick or a handover that tick can come
one or two ticks late, and a person's stop of the unit in that window, which
a tick handles before it judges roots, can reach a pending root whose newest
program was not judged, since a root is no longer judged once its agent has
exited (5.2); the guard's own stop never takes a pending root.

At a handover the next watcher takes each unit with its roots (each with the
program it was judged in, whether it is settled, its name and the command
line it shows, and on Linux the command line it was judged in),
its call and whether it was tied by stamp alone; the call records, with
whether each started an agent; the final verdicts; the processes stamps
named as agents (5.4); and the store (5.3). Known agents and stamp-named ones
are taken on while they are in the process table, a zombie not yet reaped
included, so they are protected from the new watcher's first tick. No root's
time passes: a wall clock that steps would make a root look older
than it is, so each root's ten seconds start again at the handover. The old
watcher gives what it carries 5 seconds to be read; a successor that does
not read it in time is cancelled and the old watcher carries on.

## 6. Measuring and stopping

- A unit is measured each tick like a command, and its sizes are read the
  same way.
- Units whose call resolved compete with commands for the largest. The
  floor, the cooldown, the refusal count and the dry run apply to them
  unchanged. A unit whose call did not resolve is never chosen (5.5).
- The guard's own stop takes only a unit's settled roots (5.2) that have
  been tracked for ten seconds, and sizes and signals only those; a unit
  with none is not chosen. The ten seconds count per root, from when it was
  found or started its present program, and start again at a handover
  (5.6): a session a command started usually needs less than that to reach
  its SessionStart hook and be known as an agent, and it may join a unit as
  a later root; 5.4 lists the sessions that can take longer. One predicate
  decides this for the stop and for the view's `held` (section 8). While
  the kernel does not answer where processes come from, every root is
  pending, so the guard's own stop takes no detached work (5.2).
- A person's stop covers every live root. A root that joins a unit with
  `dry-run` in its stamps makes the unit a dry run, and so does a root whose
  setting cannot be read while its agent's own environment says `dry-run`
  or `off` (4.4); a person's stop of a dry-run unit is only logged.
- The same target choice signals a unit: its root usually leads its own
  process group, which is signalled as a group when every member is in the
  unit.
- The reason is told to the call that started the unit when that call
  resolves and its command still runs in the foreground, through that call's
  kill record, in words that name the detached program. Otherwise the stop
  is in the log only, as for a command whose result already returned.
- The log, the reason and the kill record of the guard's own stop name a
  unit by the process name, as the kernel keeps it, of the first root it
  signals that still runs, never a root it left out or one that has exited,
  and never by its arguments: a call's text is
  logged as the agent wrote it, but a running program's arguments hold what
  the shell put there, a token read from a variable included, and its
  launcher or the program itself may rewrite them. The command line is in
  the view only, in the shown form of section 8.
- A person stops a unit from the view the same way as a command.
- What the Go runtime counts of the watcher's memory stays under its 32 MiB
  limit with room to spare (2.1 fact 32: 22.8 MiB at most); its resident
  size is not bounded by that limit and was measured above it, at 32.1 MiB,
  under a churning build. Its collector runs at 50 GC percent, so the heap grows to
  one and a half times its live heap before each collection instead of
  twice: Go's GC guide gives the heap target as "Live heap + (Live heap + GC
  roots) * GOGC / 100" and, for a program close to its memory limit, says
  that to "decrease GOGC … provides a much cleaner trade-off than
  thrashing-mitigation does". It keeps its process list, snapshot and parent
  index from one tick to the next and builds each tick's in them, cleared
  first, so a busy build does not make them anew at every tick. On macOS it
  lets go, at start, of the tables of network services and protocols that
  the SQLite driver's C library builds in every Workbench process; the
  watcher never opens a database.

## 7. Agents, helpers and unmatched commands

- The guard never chooses, for a stop, an agent it knows, a helper the agent
  starts itself (a language server, an MCP server; 2.1 facts 10 and 14) or a
  command it could not match (one whose shape no note claims, or one a
  person typed with `!`; 2.1 fact 16).
- None of them has a row. The view shows what the guard tracks: commands
  and detached work, and for a minute what ended, an `unmatched` call among
  them. A row for each agent, for each helper seen alive five seconds and
  for a running unmatched command, with a line splitting the memory in use
  by the kind of row, was built and dropped: the rows the guard never stops
  crowded the list, and a status column naming a row's kind no longer said
  its state.
- The watcher's lifetime does not change. It still exits after ten minutes
  with no note, no tracked command and no unit.

The view keeps the boundary of the view design: it shows only what the guard
tracks.

## 8. View, log and protocol

Every row uses the view's own columns, and its status says only its state.
A detached unit's time counts from the start of the program that detached
first, and its command is the command line of its first root that still
runs. Its ID column shows `p` and the PID of that root, since a number in a process list is read as a live PID and a
dead root's PID is handed out again (2.2 item 20): the label changes when
that root exits. The unit's ID in the log, in `guard logs` (`p` and the PID
of the program that detached first) and in the snapshot does not change, and
the details screen shows that log label on an "In the log" line, so a row
can be matched to its log records. A finished `unmatched` row, and the log's `unmatched` record,
stand only for a call the guard never found: no tick tracked it and no fork
wake-up learned its shell. A call whose shell was learned and that ended
within a tick gets no row or record of its own; what it left is a row of
detached work naming it.

A unit whose call resolved is drawn directly under that call's command,
joined to it by a tree mark before its ID (`├`, and `└` for the last under a
command; `|-` and `` `- `` where the terminal shows no such characters). The
view builds that order from each unit's launching call: a command and the
units under it move as one block, the blocks of which any row runs first,
the newest command first, then the ones that ended. The watcher keeps a
finished command in its snapshots, past the minute a finished row is shown,
for as long as a unit its call started is tracked or was finished within
that minute, so such a unit always has its command above it; it carries the
finished commands at a handover. A unit whose call did not resolve, or whose
command is not in the snapshot, is a row among the commands:

```
   ID        STATUS     AGENT   SUBAGENT        TIME   MEMORY     PEAK  PROJECT  COMMAND
   01Hx2Lm9  ✓ done     claude  reviewer        3.0s        —    22 MB  shop     npm run dev > dev.log 2>&1 &
   └ p90112  ● running  claude  reviewer       9m10s  1.36 GB  1.36 GB  shop     node server.js
   01PnU61k  ● running  claude  implementer   12m58s   500 MB   500 MB  shop     scripts/build.sh api
   ├ p87352  ● running  claude  implementer   12m13s  1.27 GB  1.27 GB  shop     node scripts/watch.js
   └ p87305  ● running  claude  implementer   12m18s  2.54 GB  2.54 GB  shop     /bin/zsh scripts/build.sh --worker api
   p55120    ● running  claude                 31m0s   640 MB   655 MB  shop     node .next/standalone/server.js
```

Each row shows its own memory, and a stop reaches only its own row: a
command and a unit under it are stopped one by one, or together when both
are tagged.

A command line read from a running process is shown only in a form that
never holds what may be an environment string (2.1 fact 30, 2.2 item 17):
on macOS, trailing strings shaped like `NAME=value` are dropped, and when any
were, only the program is kept; on Linux the read stops at the end of the
argument area where the kernel shows it, and a line it had to cut is marked
the same way. A line shortened this way ends with ` …`, so `make CC=clang`
shows as `make …`, and one with nothing left shows the process name and
` …`. Matching and judging read the whole command line; only the shown form
is kept for the screen, sent in the snapshot and carried at a handover.

The counts under the head line (`N running · M finished`) and the dry-run
line count every row, commands and detached work alike.

The details screen of a unit names the call that started it when it
resolved ("Started by") and, while the guard itself would not choose it, why,
on a "Note" line: "The guard will not stop this itself: … You can stop it
here.", with one of three reasons, the first that applies: no call is proved
to have started it; it was found, or started its program, less than ten
seconds ago; its setting cannot be read (no root that ran ten seconds is
settled, 5.2). The snapshot carries the same as `held` on a running unit
(`no call`, `young`, `unread`, empty when the guard may choose it), computed
by the predicate the stop uses (section 6). The table gains no column.

The log's `tracking`, `ended` and `stopping` events for a unit carry its
kind, root, root program's name and launching call, and an `ended` record of
a unit whose last root left carries the reason, which the text view of
`guard logs` shows after the message. A snapshot's command gains its kind,
launching call and `held`; all are additions an older view ignores.

## 9. The Monitor post hook

A Monitor command is tracked, but no post hook clears its note (2.1 fact 8),
so the note lingers until the guard abandons it with a warning, and a stop's
reason cannot reach the agent. Claude Code's post hooks are matched on
`Bash|Monitor`, and the post hook marks a Monitor call's note the way it
marks a background call's: the post hook fires when the monitor starts (2.1
fact 15). The Codex hooks do not change.

## 10. What stays unseen

Each item says which way it fails: the guard misses work (a gap), or it may
stop what it must not (a fault).

1. **A fast double fork below the shell, with nothing readable.** Work that
   a program below the command's shell double-forks, through a middle
   process that lives and dies between two ticks, and whose programs all
   hide their environment or had it wiped, is unseen until a readable
   program carrying the stamp appears in it: nothing below the shell is
   followed (5.3), and the wake-up caught it 0 of 100 times (2.1 fact 25).
   Misses.
2. **Work handed to a service.** A container engine, a launchd or systemd
   job, or a build server a person started was never the command's
   descendant. Its memory is in "other". Misses.
3. **Other users' processes.** A root-owned process can be neither sized nor
   signalled. Misses.
4. **Commands a person types with `!`.** They leave no note (2.1 fact 16),
   so their shell is never learned: detached work they leave is found only
   by its stamp, shows no subagent and is never chosen by the guard. A
   person can stop it from the view. When such a command has the same text
   as one of the agent's noted calls at the same moment, a fork wake-up can
   give that call's note to the typed command's shell (5.3), and the noted
   call is then not reported as `unmatched`, even when the agent's own
   command ends within a tick unfound. Misses.
5. **A call that starts before any watcher runs.** The pre hook starts the
   watcher, so a session's first call can start its shell before a watcher
   follows its agent. If that command ends within a tick, its shell is never
   learned, and its detached work is found only by its stamp and never
   chosen (5.5). Misses.
6. **An opt-out removed from what a program runs.** A program that set
   `WORKBENCH_GUARD=off` and runs others with the variable removed (`env
   -u`, a build tool that filters variables) leaves them judged by their
   own stamps, which read as not set (4.4): a hidden root whose first
   readable programs read so may be chosen, and so may such a program that
   later detaches from a root that opted out. May stop what it must not.
   Only an opt-out that needs no environment would close it, and none is
   built.
7. **A session that never runs a readable program carrying its own agent
   stamp.** An agent session a command starts and leaves detached is known,
   and so never stopped, from its SessionStart hook, or, without the hooks
   and on macOS, protected from the first readable program that carries its
   own `CLAUDE_PID` (5.4). One without the hooks whose programs are all
   hidden, one without the hooks on Linux and WSL, and one still in its
   first moments are programs like any other (5.4 lists them and for how
   long): a unit of the outer command, which after ten seconds the guard may
   choose, with the work it left detached. May stop what it must not. Once
   such a session is known or protected, the work it left before is no
   longer the outer call's (5.3).
8. **A Codex session whose required MCP servers take longer than the ten
   seconds.** Codex runs its SessionStart hook in its first turn, after its
   required MCP servers are prepared (2.2 item 23); with one that answered
   after 15 s it registered at 17.1 s (2.1 fact 28). Left detached by a
   command, it is a unit the guard may choose between the tenth second and
   its registration (5.4). May stop what it must not.
9. **Claude Code before 2.1.214.** It sets no `CLAUDE_PID` (2.2 fact 1), so
   its commands carry no valid stamp. Their detached work is found only by
   the store (5.3), never by a stamp: misses. And a nested session of such a
   version started without the hooks is never protected by a stamp (5.4),
   as in item 7: may stop what it must not.
10. **A command that names one of its own processes as an agent.** Any
    command can keep the guard off its own tree by naming one of its own
    processes in `CLAUDE_PID` (`CLAUDE_PID=$$ …`): the stamp protects that
    process, in a detached tree everywhere and inside a tracked tree on
    macOS (5.4). The guard cannot stop that command. Misses.
11. **While the kernel does not answer where processes come from.** No new
    unit is made and every root is pending, so no detached work is chosen
    by the guard (5.2, section 6). Misses.
12. **Linux and WSL find less.** With no original-parent record, no tie by
    stamp and no fork events, detached work is found there only when the
    guard recorded it in a tracked command's tree at a tick before it
    detached; with ticks of one second, or 100 ms under memory pressure, a
    program that detaches at once, through a double fork, is unseen. Misses.
    Work whose environment was wiped, such as a program that sets a long
    title over its environment area as Perl does for `$0` (2.2 item 26), is
    seen and tracked when it was recorded before it detached, but held
    `unread` and never chosen by the guard itself (4.4); a person can stop
    it. Misses. No stamp is read inside a tracked tree there, so a session
    started without the hooks is never protected by one (item 7). Whether
    more can be found safely is decided with the checks of 2.3 and section
    11. None of this was run on Linux or WSL.
13. **A child that execs and exits while a tick runs.** The watcher handles
    wakes only between ticks, and the kernel folds a process's events that
    arrive meanwhile into one. A child of an agent still waiting for its
    exec, whose exec and exit come together that way, is dropped unmatched,
    so its shell is not learned and work it left is found only as for a call
    before any watcher ran (item 5). Misses.
14. **A note left to the tick.** At a fork wake-up a note is given only where
    the tick would give the same one (5.3). Where an agent forks a child and
    then execs it (Claude Code spawns its shells whole, 2.1 fact 24), a
    younger child with the same text that exec'd before an older one is left
    to the tick; if its command ends within a tick, its work is found only
    by its stamp and never chosen. Misses.
15. **A program the agent opened for a person.** An editor or a browser a
    command starts is that command's work wherever the system does not hand
    it to the init process to start. On macOS apps go through `open` and are
    left out (5.2); on Linux an app a command forked and left running is a
    unit like any other, which the guard may stop as that command's work.

No method open to a tool without root closes the first three. The ones that
would are in section 3, C and D.

Seen, though a tick alone would miss them: a subshell left by a command that
returned within a tick, under a shell whose environment is readable; detached
work of any noted call that ended within a tick, which gets its call and
subagent; a double fork made by the command's shell itself; all three
through the shell learned at its start (5.3), on macOS while a watcher runs.
Work a nested session left detached before it was known or protected is no
longer the outer call's once it is (5.3).

## 11. Waiting for a check: Claude Code's cgroup on Linux and WSL

Where Claude Code's memory cap is on, every tool command of a session
shares one cgroup and every descendant is born into it (2.2 facts 6
and 9). The guard could then read the cgroup of a tracked root and treat its
members outside the agent's tree as that session's detached work, whatever
their environment. This is designed only after 2.3 item 1 is checked on
Linux, and applies only with the cap on.

Not proposed: Workbench moving commands into cgroups itself. systemd can do
it for an unprivileged user, but it costs a bus call per command and changes
how a command is accounted.

## 12. Left to the builder

The names of the variable, the hidden command, the statuses and the log
attributes; the classification cache; a row's full ID in the socket
protocol; the Linux details once 2.3 is known; whether the protocol version
moves. The bounds this design rests on are stated where they are used: 4,096
call records and a stamp's 30-second window (5.5); versions kept 60 seconds
and at most 16,384, the oldest then dropped down to 14,336 (5.3); 250 wakes
a second for a call's shell, and 100 wakes a second then a 5-second pause
for an agent (5.3); the 5-second deadline of a handover's write (5.6); ten seconds before the guard's own
stop (section 6).

## 13. Tests and checks

Tests for two risks: unauthorized execution (the guard stopping a program no
agent command provably started, an agent, or work that opted out), and
credential exposure (the last). Each says its risk beside it.

- `TestUnitRootOnlyForWhatAnAgentCommandForked`: the unit rule yields no
  root for a process without a stamp and without a recorded original
  parent, for a stamp naming a dead process, an unknown one or one younger
  than the stamped process, for a process the init process started that
  carries a stamp, for a process that carries the agent stamp without the
  call stamp as a hook's child does, for one whose call stamp names another
  agent than its own `CLAUDE_PID` as a nested session's does, for stamps
  that name two agents, for a call stamp whose `CLAUDE_PID` is only among
  the arguments, for a stamp alone where the system keeps no
  original parent, for a known agent, for a Workbench process, for a
  protected process, for a child of a protected PID 1, and when the kernel
  does not answer; a tie by its own entry or by its original parent with
  nothing readable is marked as such.
- `TestGuardChoosesOnlyWhatACallProvablyStarted`: the guard's choice never
  falls on a unit whose call did not resolve, on a root tracked for less
  than ten seconds, on a root that is not settled, or on a unit tied by
  stamp alone to a call that started an agent.
- `TestWatcherKeepsWhatNoCallStartedOutOfItsOwnStop`, on a watcher with
  made-up processes: a final "no" at first sight drops what it started; a
  recorded process that becomes protected takes its tree; what judging a
  root again decides (settled, dry run) is kept; a root that runs another
  program is pending and young again; a root with nothing readable is
  pending; without lineage no unit is chosen; a stamp-named agent is
  protected from the first tick after a handover.
- `TestTargetsStayInsideTheCommandTree`: a stop of a unit signals only its
  live roots' trees, never a protected agent inside it, never a root that
  joined less than ten seconds ago or is not settled.
- `TestGlimpseRecordsNothingUnderAProtectedShell`: once a learned shell is
  protected, its forks and execs record nothing and it is no longer
  followed.
- `TestARetitledProgramKeepsItsOptOut`: a program that rewrote its title
  still reads `WORKBENCH_GUARD=off` and its `CLAUDE_PID`.
- `TestManualStopsOnlyWhatAPersonMayStop`: a stop naming what the guard does
  not track chooses nothing.
- `TestDropTakesWhatAProcessStarted`: forgetting a process takes what was
  recorded below it, through a middle process that exited and through a
  version recorded before an exec, and leaves a nested agent's own calls
  alone.
- `TestTickBuffersHoldOnlyThisTick`: the tick's snapshot and parent index,
  built in the last tick's, hold exactly what new ones would, so no process
  that left a command's tree is signalled with it.
- `TestWatcherReadsTheStampOfARootThatRunsAnotherProgram`: a root that runs
  another program has its stamp read again, so a `WORKBENCH_GUARD=off` set
  in its new program is honoured and the guard does not stop work that
  turned it off.
- `TestShownArgsNeverHoldTheEnvironmentOfARetitledProcess`: the shown
  command line of a retitled `/usr/bin/perl` holds no environment string
  (a credential-exposure risk).

Before release, on macOS in a disposable home, with throwaway sessions and
stand-ins for an agent, against the watcher's snapshot (read from its
socket, since `guard memory` has no JSON output) and its log. Memory pressure
is not forced, so the guard's own stop is shown by the facts it rests on
(`held`, `launched_by`) and by the tests above, not observed:

- A command that double-forks a sleeper gives a `detached` row with its
  agent, and with its subagent when a subagent made the call. A stop from the
  view ends it.
- A command that leaves a background job of a system program behind gives a
  `detached` row with its call, held as `unread`.
- A sub-second command that leaves a subshell, or double-forks from its
  shell, gives a row with its call and subagent, also in a real session; two
  subagents starting the same command at once each get their own call. A
  call's shell and an agent that fork in a loop are capped, and their calls
  are still tracked at the ticks; a refused follow is logged once and the
  ticks keep their pace, and an agent that exits as it is followed logs
  nothing. A retitled `/usr/bin/perl` shows no environment
  name in its row.
- An app started with `open` from a command gives no row. A daemon a hook
  starts gives no row.
- A program given `WORKBENCH_GUARD=off`: readable, no row; hidden, a row held
  as `unread`; one that execs into `off`, its unit ends with "its root turned
  the guard off"; a hidden root whose readable child dropped the variable
  stays choosable (section 10).
- A program that rewrites its title keeps its ten seconds; one that execs
  the same path and arguments starts them again.
- A nested session a command starts, in each shape (its early work hidden or
  readable, through a middle that lived or not, detached itself, exiting
  right after it registers, tied through a version from before an exec):
  once it is known, no row keeps the outer call. Without hooks, it is
  protected from its first readable program carrying its own `CLAUDE_PID`. A command
  that sets `CLAUDE_PID=$$` keeps the guard off its own tree.
- A session that exits but is not yet reaped stays out of the outer
  command's tree.
- A command that leaves a program detached and ends: the program's row is
  under the command's, which stays past its minute and goes a minute after
  the program ends.
- A handover with a unit tracked shows the unit once afterwards, with its
  call, held as `young` until its ten seconds have passed again.
- 1,400 processes under one command, and a churn of short processes under
  one command, for the watcher's CPU and resident memory.
- A Monitor command's note is gone once it ends, with no `abandoned` warning.
- `scripts/render-check.sh` for each role and mode the hook changes touch.

Nothing is run on Linux, WSL or Windows for this release (2.3).

## 14. Docs to revise

- The guard design's section 12 line on processes that leave the command's
  tree, and the view design's section 1 line on what is shown, its columns,
  the `held` line and its protocol section.
- ADR 0016, for recognizing detached work by stamps and the store, the
  settled root, the shell learned at its start, and what the view shows.
- `docs/usage.md`: the guard section (detached work, the opt-out, what is
  not seen), the view's rows, ID column and details, and the
  log's fields.
- The package comment of `internal/guard`, and the help texts of `workbench
  guard`, `guard memory` and `guard logs`.
- `AGENTS.md`: the guard row of the ownership table names the SessionStart
  hook, which both agents' hook files carry.
- `docs/acceptance.md`, once the checks in section 13 have run.

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
- Apple kernel at tag `xnu-12377.121.6`: a new version at every exec
  (`exec_handle_sugid`) and `P_EXEC`
  <https://github.com/apple-oss-distributions/xnu/blob/xnu-12377.121.6/bsd/kern/kern_exec.c>;
  `sysctl_procargsx`
  <https://github.com/apple-oss-distributions/xnu/blob/xnu-12377.121.6/bsd/kern/kern_sysctl.c>;
  `cs_restricted`
  <https://github.com/apple-oss-distributions/xnu/blob/xnu-12377.121.6/bsd/kern/kern_cs.c>;
  `filt_procattach` and `filt_procevent`
  <https://github.com/apple-oss-distributions/xnu/blob/xnu-12377.121.6/bsd/kern/kern_event.c>;
  the fork's `NOTE_FORK`, the flags a child keeps and `PID_MAX`
  <https://github.com/apple-oss-distributions/xnu/blob/xnu-12377.121.6/bsd/kern/kern_fork.c>;
  `PROC_FLAG_EXEC` and flavor 17's checks
  <https://github.com/apple-oss-distributions/xnu/blob/xnu-12377.121.6/bsd/kern/proc_info.c>;
  `p_orig_ppidversion`
  <https://github.com/apple-oss-distributions/xnu/blob/xnu-12377.121.6/bsd/sys/proc_info_private.h>;
  `PID_MAX`
  <https://github.com/apple-oss-distributions/xnu/blob/xnu-12377.121.6/bsd/sys/proc_internal.h>;
  `CS_OPS_MARKRESTRICT`
  <https://github.com/apple-oss-distributions/xnu/blob/xnu-12377.121.6/bsd/kern/kern_proc.c>
- Linux `v6.12`, `PF_FORKNOEXEC`:
  <https://elixir.bootlin.com/linux/v6.12/source/include/linux/sched.h>,
  set in `copy_process`
  <https://elixir.bootlin.com/linux/v6.12/source/kernel/fork.c> and cleared
  in `begin_new_exec` <https://elixir.bootlin.com/linux/v6.12/source/fs/exec.c>;
  `proc_pid_stat(5)` <https://man7.org/linux/man-pages/man5/proc_pid_stat.5.html>
- Perl, `$0` using the environment area (`perl_parse` in `perl.c`):
  <https://github.com/Perl/perl5/blob/blead/perl.c>
- `man 2 kqueue` and `man 2 execve` on macOS
- Codex at commit `048431dcbecc510f1879c9421a97220016ecac44`, SessionStart in
  the first turn:
  <https://github.com/openai/codex/blob/048431dcbecc510f1879c9421a97220016ecac44/codex-rs/core/src/session/turn.rs>;
  MCP servers at session start:
  <https://github.com/openai/codex/blob/048431dcbecc510f1879c9421a97220016ecac44/codex-rs/core/src/session/session.rs>;
  the MCP startup timeout:
  <https://github.com/openai/codex/blob/048431dcbecc510f1879c9421a97220016ecac44/codex-rs/codex-mcp/src/rmcp_client.rs>
- GitHub Actions runner at tag `v2.338.2`, a process killed only on a match:
  <https://github.com/actions/runner/blob/v2.338.2/src/Runner.Worker/JobExtension.cs>
  and its macOS environment read:
  <https://github.com/actions/runner/blob/v2.338.2/src/Runner.Common/ProcessExtensions.cs>
- Jenkins at tag `jenkins-2.590`, `ProcessTree`:
  <https://github.com/jenkinsci/jenkins/blob/jenkins-2.590/core/src/main/java/hudson/util/ProcessTree.java>
- Linux `v6.12`, `get_mm_cmdline`:
  <https://elixir.bootlin.com/linux/v6.12/source/fs/proc/base.c>
- Go's `time` package, a time's JSON form drops the monotonic clock reading:
  <https://pkg.go.dev/time>
- Go's GC guide, the heap target and GOGC under a memory limit:
  <https://go.dev/doc/gc-guide>; `runtime/debug.SetMemoryLimit`, what the
  limit counts: <https://pkg.go.dev/runtime/debug#SetMemoryLimit>
