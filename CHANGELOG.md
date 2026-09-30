# Changelog

Every Workbench release, newest first, generated from its commit messages when
the release is tagged.

## [v0.1.7](https://github.com/Sawmonabo/workbench/releases/tag/v0.1.7) - 2026-09-30

### Features

- Run claude-costs ingest from async session hooks
- Report and status from the ledger
- Layered rate card with best-effort official refresh
- Silent detached hook entry with single-instance lock
- Ingest transcripts into the ledger by offset
- Ledger schema and command skeleton

### Fixes

- Pass an int64 default to promptIntOnce for WSL processors
- Merge .wslconfig keys case-insensitively
- Keep the largest usage per request and resolve rates by source order
- Ledger advisor-tool usage as its own rows

### Documentation

- Record observed ledger checks
- Match the Task 4 override error text to the code
- Gate the Task 6 render check on an interactive route
- Apply review fixes to the ledger plan and spec
- Add claude-costs ledger implementation plan
- Add claude-costs ledger design spec

## [v0.1.6](https://github.com/Sawmonabo/workbench/releases/tag/v0.1.6) - 2026-09-29

### Fixes

- Rerun the runtime and tool installs only when their own pins change

## [v0.1.5](https://github.com/Sawmonabo/workbench/releases/tag/v0.1.5) - 2026-09-29

### Fixes

- Say the machine questions were cancelled instead of a missing-file error

### Maintenance

- Separate changelog releases with one blank line

## [v0.1.4](https://github.com/Sawmonabo/workbench/releases/tag/v0.1.4) - 2026-09-28

### Features

- Apply sets up and asks; update installs a release, then applies

### Fixes

- Refuse an unknown --effect before update installs anything
- First-time setup, role changes and gaps from the dotfiles review

### Documentation

- Describe apply's setup step everywhere it was still update's
- Stop naming the latest release, so a tag needs no follow-up edit
- Record the v0.1.3 release

### Maintenance

- Generate the changelog and release notes when a version is tagged

## [v0.1.3](https://github.com/Sawmonabo/workbench/releases/tag/v0.1.3) - 2026-09-28

### Fixes

- Show Homebrew's version for an app update unless the app moved past it
- Mark setup unchanged when its tools were already in place

### Documentation

- Say newerVersion is stricter than Homebrew for non-numeric parts
- Say which older releases ask twice on update

## [v0.1.2](https://github.com/Sawmonabo/workbench/releases/tag/v0.1.2) - 2026-09-28

### Features

- Ask update's one real question, and show a clear result

### Fixes

- Install Workbench's tools on every update; rewrite the acceptance record

### Documentation

- Record the v0.1.1 release and the update from v0.1.0

## [v0.1.1](https://github.com/Sawmonabo/workbench/releases/tag/v0.1.1) - 2026-09-28

### Fixes

- Plan only the steps that will run, and quiet repeat output
- Skip approval when a config-only apply has nothing to change; refresh docs

### Documentation

- Record v0.1.0 and drop the private-repository hints

## [v0.1.0](https://github.com/Sawmonabo/workbench/releases/tag/v0.1.0) - 2026-09-26

### Features

- Simplify the commands to apply, update, version, doctor and revert
- Show live progress while Workbench plans and sets up tools
- Name what each tool update brings along and stop Homebrew asking twice
- Offer command-line tool updates in the plan like app updates
- Start Claude Code in bypass-permissions mode
- Leave the Claude and Codex model and effort to each machine
- Deploy the owner's Claude instructions, zsh notes on macOS only
- Run Homebrew maintenance on every full macOS apply
- Add Discord to the macOS apps
- Offer app updates in the plan; stop adopting hand-installed apps
- Pin GitHub Actions once in versions.toml
- Preview and apply a developer checkout without rebuilding
- Summarize each planned edit and flag local changes
- Remove releases and setup leftovers that no kept runtime needs
- Adopt dotfiles answers and document the switchover
- Publish personal releases on tag with a one-line installer
- Allow full provisioning on WSL with selectable host effects
- Select optional provisioning effects with --effect
- Implement Workbench lifecycle and project tooling
- Add shared operation context and execution foundations

### Fixes

- Let revert restore the mode of folders above Workbench's data
- Allow a safe mode change on folders above Workbench's data
- Run with the user's umask plus 022, and name the tool setup rejects
- Ask for setup approval only when setup installs or needs answers
- Plain version line, readable project plans, exit 3 on a rerun refusal
- Point the missing-tools error past --config-only
- Let a rerun of update finish setup, and keep home parents ordinary
- Leave unchanged app settings alone and settle unfinished applies
- Show plans readably and stop an interactive apply from suspending
- Give the tmux step Homebrew's PATH and correct two step messages
- Tidy the Codex header wrap and the Claude settings trailing newline
- Keep private folders private and Codex-owned settings on apply
- Accept Apple's default folder ACLs on target parents
- Keep the preserved macOS attributes in the darwin file
- Keep app-replacement copies in Workbench state
- Keep private tools that any kept release pins
- Release only CI-checked commits and install through gh without curl
- Only --source selects a developer checkout
- Order last-applied images by their latest write
- Keep the replaced release when resuming an interrupted install
- Say which step a raw error came from
- Say which tool version was found and what setup will do
- Carry common macOS attributes instead of blocking on them
- Clear review nits in operation, machine and project
- Merge Claude Code settings instead of replacing them
- Keep the newest 20 checkpoints instead of blocking apply
- Let full apply finish with a terminal and visible output
- Scan complete history on initial repository pushes
- Reserve lock directories by filesystem identity
- Preserve trusted tools and operation lock boundaries

### Documentation

- Record the interrupted recheck after approval
- Rename CLAUDE.md to AGENTS.md
- Record the Homebrew dry-run plan and the unattended apply
- Note the unchanged-file VS Code merge in contributor instructions
- Record the real-Mac dry run
- Record the tool-retention and action-pin checks
- State that dropped enforced Claude settings stay live
- Record this round's observed checks
- State the Ubuntu-only Linux scope as a decision
- Note project checkpoints carry the policy ID
- Reserve checkpoint capacity for recovery
- Resolve Workbench implementation contracts and native proofs
- Prepare implementation plan for reviewed task execution

### Maintenance

- Update Go to 1.26.8
- Read tool version pins from the selected source at runtime
- Let the release build fingerprint its own source
- Define nameSeparators before requirementName
- Type exit codes, statuses, image kinds and journal states
- Declare what each command acts on instead of matching names
- Share digest, strict JSON and project-marker helpers
- Split project planning; record only the policy ID
- Split cli commands and share plan consent
- Split machine and release functions along their stages
- Document exports and split operation's long functions
- Enable golines, gofumpt and guardrail linters
- Establish Workbench source and CLI baseline
