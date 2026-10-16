# 8. Migrations move Workbench's own files into its folders

- Status: Accepted, October 14, 2026
- Amends: [AGENTS.md](../../AGENTS.md), "Greenfield: … No migrations"

## Context

Workbench and the `claude-costs` script before it wrote some files outside
Workbench's runtime folders: the costs ledger, its price caches, the import
log and lock and the owner's price overrides under `claude-costs/` folders,
the shell completion cache in `~/.cache`, and the old script's parse cache.
The ledger is the only lasting record of spend: Claude Code deletes transcripts
after 30 days by default (`cleanupPeriodDays`). A release that only changed the
paths would start an empty ledger and leave the old one unread.

## Options considered

- **A. Numbered, idempotent migrations** in `internal/migrate/`, run by apply
  (one `migrations` step), by `workbench migrate`, and, for the costs files,
  by the import worker itself. Chosen.
- **B. Read the old paths forever.** A compatibility reader and a second path
  for every costs file, which AGENTS.md rules out.
- **C. Leave the files where they are.** Workbench's files stay split between
  its folders and ones named after a retired script, with `0755` folders.

## Decision

- A migration may move files Workbench itself wrote into its runtime folders,
  without losing data, and delete caches and logs Workbench itself wrote.
- Migrations are idempotent, never touch other tools' files, never merge, and
  leave a file where it is when old and new data both exist; every other file
  still moves. No rename ever replaces a file.
- The costs import worker runs the costs move itself, without asking, because
  usage left unread could be lost; it imports once the ledger has moved.
- A migration may read the old locations, including old environment
  variables, only to find what to move. No runtime code reads an old location.
- Released migrations are never renumbered or removed.
- The ledger moves by `rename`, or across filesystems by a checked copy, and
  only when no process has it open.

## Consequences

- One more exception in AGENTS.md's development contract, beside the ledger's
  in-place schema upgrade.
- `CLAUDE_COSTS_LEDGER`, `CLAUDE_COSTS_STATE` and `CLAUDE_COSTS_RATES` no
  longer relocate anything; `WORKBENCH_{DATA,STATE,CONFIG}_DIR` relocate the
  costs files with everything else.

## Sources

- [Workbench migrations design](../superpowers/specs/2026-10-14-workbench-migrations-design.md)
- [Costs ledger design, problem 5: `cleanupPeriodDays`](../superpowers/specs/2026-09-30-claude-costs-ledger-design.md)
