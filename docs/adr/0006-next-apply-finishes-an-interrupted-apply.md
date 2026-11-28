# 6. The next apply finishes an interrupted apply, and revert checks its files

- Status: Accepted, October 5, 2026
- Supersedes: in [the contracts](../superpowers/specs/workbench-contracts.md),
  "A checkpoint with unknown post-images after interruption requires reviewed
  reconciliation".

## Context

Before chezmoi writes anything, Workbench marks every file of the apply's
checkpoint as unknown, and checks each one once chezmoi stops. chezmoi's own
write leaves out what Workbench's pass after it gives a file: a file chezmoi
creates first gets the group of the temporary folder Workbench gives it
(`wheel` when that folder is under `/tmp` on a Mac, while a home folder's is
`staff`), and a file it writes in place of another lacks the ACL, extended
attributes, hidden flag and SELinux label the replaced one had. When a step
fails, or ctrl+c stops chezmoi, Workbench is still running and does that pass.
When Workbench itself is stopped between chezmoi's write and its pass (its
terminal window closed, SIGTERM, a crash, `kill -9` or power loss), nothing
does it then: the files stay unknown, without what the pass gives them, and
the state records the apply as unfinished. A plan, which compares content,
does not see what they lack, and someone may edit one of them before the next
run.

## How similar tools handle it

- **[SQLite](https://www.sqlite.org/atomiccommit.html)** writes its rollback
  journal before it changes the database. A journal left by "an earlier process
  [that] was in the middle of committing a transaction when it crashed or lost
  power" is a hot journal, and the next process that opens the database rolls it
  back (sections 4.2 to 4.6). Recovery happens later, from what the journal and
  the file hold, not at the moment of the crash.

## Options considered

- **A. Settle when Workbench is stopped.** It gets the same pass and check as
  a failed step.
- **B. A, and revert checks any file still unknown.**
- **C. B, and the next apply finishes the interrupted one, planned with it.**
  Chosen.
- **D. Refuse until the person restores the files.**

## Decision

- An apply whose Workbench was stopped is settled like a failed one, once
  chezmoi has stopped: the process runner kills its whole process group and
  waits for it.
- ctrl+c at the terminal, which stops chezmoi, ends the apply as interrupted
  (exit 130, "Interrupted", with "kept  what already ran" and the fix
  "workbench apply"), not as a failed step to fix.
- The next apply of the same destination finishes the interrupted one before
  it writes anything. Its plan reads the record first: for each file whose
  outcome the checkpoint does not know, what it lacks altogether of its
  approved image (its group, a folder's set-group-ID bit, its label, and the
  ACL, extended attributes and hidden flag carried from the file it replaced),
  and what it holds once given that. The plan takes that image as the file's
  image before it, shows the interrupted apply as a warning naming each file and
  what it lacks, and binds the record and both images of each file, so a change
  between the dry run and the apply stops the apply (exit 4). `doctor` says the
  same in the same words, with the command that applies to that destination.
- A destination whose folder is gone since the stop (not there, a file or a
  link in its place, or reached through a link) leaves nothing of the apply
  to finish, and no destination Workbench resolves is that one. Every command
  reads the record. The plan of any apply, and `doctor`, say how the
  destination is gone (`the destination is not there, so none of the files
  the apply wrote is left to finish`) and that the next apply records it as
  finished and keeps its checkpoint; the plan binds that, and the apply
  clears the record. `doctor` gives no command for it: any apply does it. A
  folder Workbench is kept from looking up, below a folder the user cannot
  search, cannot be told from one that is there: the record keeps blocking
  other destinations, and the apply command of its destination names that
  folder and the `chmod` or `sudo chown` that opens it.
- A file of the interrupted apply that is not there, in a destination that is
  (made again empty, or the file removed), lacks nothing: the warning names it
  (`files ~/dest/.zshrc and 14 more are not there`), and the next apply
  writes what its plan shows and records the apply as finished.
- Finishing gives each file what it lacks in place, a file or folder through
  a descriptor and a link by its path, and leaves its content alone, whether
  chezmoi wrote it or someone edited it afterwards. What it holds of that metadata with another value stays, as
  someone's. A file whose group its user may not give it is refused at the plan
  with `sudo chgrp`. The temporary files the stopped run staged beside its
  targets, named by the operation and the target's place in its checkpoint, go
  first.
- Revert decides each file still unknown from what it holds now: exactly the
  approved content means written, exactly the earlier content means untouched.
  A file chezmoi wrote that lacks only what the pass gives counts as written;
  revert gives it that, then restores it. A file holding anything else stops
  the revert, named like a later edit, with the fix that keeps it: `mv -n` to a
  name beside it that nothing has, then the revert again, which takes a file
  that is not there as moved aside and writes the checkpoint's image over
  nothing.
- A revert decides every changed file and folder by whether going on would
  lose anything of a person's. One that holds its recorded image but lacks an
  ACL, extended attributes or the hidden flag altogether, which someone took
  off, holds nothing that is not in the checkpoint: the revert takes it as it
  is, says so in its plan, which binds it, and writes its image over it. One
  that holds more than its image (an ACL, an attribute or the hidden flag), or
  any of it with another value (content, mode, group, an ACL's entries, an
  attribute's value), holds what the person made, which the revert would
  write over, so it stops the revert: a file or link with `mv -n`, a folder
  with the commands that take off what it holds beyond its image and give
  back its mode and group, after which it at most lacks something and the
  revert goes on. No line carries an attribute's value or an ACL entry.

The next apply finishes the interrupted one because revert alone (B) leaves
the files without what the pass gives them for as long as nobody reverts,
and a plan, which compares content, finds nothing to do for them; refusing
(D) sends the person to do by hand what Workbench can do itself. The plan
takes the interrupted apply into account because every apply binds each
file's current image, group included, into its approved plan and checks it
again just before writing: a plan made from the files as they are would
refuse a group the finishing gives, or find the file changed once it is
finished.

## Consequences

- A closed terminal, SIGTERM, crash or power loss during an apply leaves a
  record the next apply of that destination finishes, and a checkpoint revert
  can undo.
- Neither the next apply nor revert loses a later edit: the next apply keeps
  its content, and revert restores only exact matches and files that lack
  metadata of their image, and names a fix that keeps the rest.
- The plan of the next apply, and `doctor`, say which files an interrupted
  apply left and what each lacks; the revert plan says when it checked files
  the apply left unrecorded.

## Sources

- [SQLite: Atomic Commit, section 4](https://www.sqlite.org/atomiccommit.html)
