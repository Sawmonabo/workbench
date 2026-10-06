# 6. Revert checks the files an interrupted apply left unrecorded

- Status: Accepted, October 5, 2026
- Supersedes: in [the contracts](../superpowers/specs/workbench-contracts.md),
  "A checkpoint with unknown post-images after interruption requires reviewed
  reconciliation".

## Context

Before chezmoi writes anything, Workbench marks every file of the apply's
checkpoint as unknown, and checks each one once chezmoi stops. On a Mac, a file
chezmoi creates first gets the group of its temporary folder (`wheel` in a clean
macOS VM, where the home folder's is `staff`); Workbench gives it the approved
group back, and only then records it as written. That fix ran when the run
finished or a step failed, which includes ctrl+c at the terminal, since that
stops chezmoi. It did not run when Workbench itself was stopped: its terminal
window closed (SIGHUP) or it got SIGTERM. A crash, `kill -9` or power loss runs
nothing at all. Either way the files stayed unknown, and revert refused the whole
checkpoint ("could not confirm every file this checkpoint wrote"), so the person
had to restore the files by hand. ctrl+c at the terminal also ended the apply
with exit 5 and "Setup did not finish (chezmoi failed (signal: interrupt)) … fix
that, then run workbench apply again", though there was nothing to fix.

## How similar tools handle it

- **[SQLite](https://www.sqlite.org/atomiccommit.html)** writes its rollback
  journal before it changes the database. A journal left by "an earlier process
  [that] was in the middle of committing a transaction when it crashed or lost
  power" is a hot journal, and the next process that opens the database rolls it
  back (sections 4.2 to 4.6). Recovery happens later, from what the journal and
  the file hold, not at the moment of the crash.

## Options considered

- **A. Settle when Workbench is stopped.** It gets the same group fix and check
  as a failed step.
- **B. A, and revert checks any file still unknown.** Chosen.
- **C. Keep refusing.**

## Decision

- An apply whose Workbench was stopped is settled like a failed one, once
  chezmoi has stopped: the process runner kills its whole process group and waits
  for it.
- ctrl+c at the terminal, which stops chezmoi, ends the apply as interrupted
  (exit 130, "Interrupted; what already ran is kept, and running workbench apply
  again finishes the rest."), not as a failed step to fix.
- Revert decides each file still unknown from what it holds now: exactly the
  approved content means written, exactly the earlier content means untouched.
  A file chezmoi created that differs only by the group of its temporary folder
  counts as written; revert gives it the approved group, then restores it. A
  file holding anything else stops the revert, named like a later edit.
- The check runs at revert, not at the next apply. Every apply binds each
  file's current image, group included, into its approved plan and checks it
  again just before writing, so a group fix made by the next apply would refuse
  that apply.

## Consequences

- A closed terminal, SIGTERM, crash or power loss during an apply no longer
  leaves a checkpoint that revert cannot undo.
- Revert never undoes what the apply did not write: it acts only on exact
  matches, as it does for files whose outcome was recorded.
- The revert plan says when it checked files the apply left unrecorded.

## Sources

- [SQLite: Atomic Commit, section 4](https://www.sqlite.org/atomiccommit.html)
