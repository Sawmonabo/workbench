# Changelog

Every Workbench release, newest first, generated from its commit messages when
the release is tagged.

## [v0.1.27](https://github.com/Sawmonabo/workbench/releases/tag/v0.1.27) - 2026-10-18

### Features

- Turn on the terminal's alternate scroll so the wheel scrolls the details
- Scroll the details and free the mouse there for text selection
- Label a worktree by its repository and drop the retry note
- Mark the selected row with a band, take the mouse, split long commands in details
- Turn the mouse on in the costs view
- Print measurements with two decimals and capacities whole
- Give the memory view the guard's colors, a four-line header and arrow keys
- Read the machine's used memory on macOS and Linux
- Color the log's level, agent and sizes in the pinned colors
- Scaffold the guard's pinned colors and a used-memory field
- Add workbench guard memory, a live view of tracked commands with a stop menu
- Serve snapshots and take a person's stop over a peer-checked Unix socket
- Print the guard log one event per line with an id, project and key=value fields
- Measure each tracked command every tick and record how long it ran and its peak
- Note each call's folder, project and subagent and prefer the command in the note's folder

### Fixes

- Match the saved alternate scroll state with a restore when it stays on
- Keep the mouse in the stop menu, ask the terminal before setting alternate scroll
- Cap a rate override at 1000 USD per million tokens
- Keep $' ' and case … esac whole, end a comment at its line and say what the log's cmd holds
- Keep the key line on screen when a long reason shortens the details
- Refuse rates out of range, scroll where the wheel selects nothing
- Color a size from where it prints as 1.00 GB and 4.00 GB
- Show here-documents, tests and groups whole in the details
- Keep the command, status and memory in details that do not fit
- Keep every color on the selected row's band at 3:1 or more
- Wrap the reason a page could not open
- Keep the marker on the name's line in a stacked table
- Print one figure for the same responses on every view
- Show the swap total whole when no swap is in use
- Keep the clock and the stop menu clear of the header, drop the unused release
- Show swap not in use as 0, not as a missing reading
- Keep the stop menu below a status line that wrapped
- Warn on unmatched notes, size a person's stop like the guard's, keep provider IDs apart
- Refuse a person's stop from under an agent running as PID 1
- Mark dry-run rows, keep the view inside 60 columns, make the socket owner-only
- Stop only the commands the menu opened with, and say what a dry run did
- Refuse --json and --non-interactive in guard memory, detect another protocol by error
- End a carried command whose root died during the handover, with who stopped it
- Carry the finished commands and who stopped each across a handover
- Log a repeated forced stop only when it kills, and name the protocol version from its constant
- Pass the listening socket without switching it to blocking mode
- Report a zombie's or exited process's folder as exited and read proc_pidinfo's int result
- Leave merge commits out of the changelog

### Documentation

- Describe the scrolling details and text selection there
- Describe the band's contrast, the details' cut and what splits
- Describe the selected row's band, the mouse and the details' command steps
- Say how dollar figures add up and round
- Describe the mouse in the costs view
- State the size rule and update the examples to it
- Wrap the view's lines and name what the reader needs
- Say what the hello checks and wrap the view's lines
- Record the header, color and arrow-key check
- Describe the memory view's header, colors and arrow keys
- State where the used memory comes from
- Point the guard design at the log's pinned colors
- Describe the log's pinned colors and why they are pinned
- Wrap the id sentence and state the stop test's risk as the docs do
- Match the log examples, the stop check, CI linting and tests to the code
- Describe dry-run marks, narrow-window lines and the socket's mode
- Name the macOS and Go versions the latest checks ran on
- Record the guard memory view and platform package checks
- Describe what a handover carries and the view's refusals and limits
- Describe shared files beside the per-system pairs
- Show the platform package's shipped layout and System's Go names
- Keep the guard's floor rule in the guard's memory comment
- Add the platform package to the owners table
- List the platform package and realign the layout listing
- Capture the Linux baseline from an image with CA certificates as a non-root user
- Plan the baseline apply from a home outside the worktree with the pinned tools installed
- Capture the apply plan in the baseline and drop the init flag that does not exist
- Keep the socket path helper out of the log reader's file
- Avoid shadowing make in the pairing test and name the assembly symbol rename
- Apply the second plan review: handover listener variable, manual-stop ordering, pure folder preference
- Apply the plan review: asynchronous manual stops, folder preference, handover descriptor and sources
- Plan the platform package, the log line format and the memory view
- Keep the stop reason out of the log line and name the unlisted keys
- Use the guard's real event names and verified counts in the spec
- Cite the short-ID and htop sources and use neutral examples
- Design one package for operating-system calls and platform facts
- Name the per-tick memory reader apart from the stop's
- State the memory view's platform code and the code it reuses
- Design the log line format and the live memory view

### Maintenance

- Lint the linux and the darwin target by name
- Open the handed-over descriptors one per line
- Keep the snapshot builders in view.go, apart from the message format
- Place nvm's folder in ToolFolders on both systems
- Share IsTerminal over each system's termios request
- Name the Python policy folder after its package so imports need no alias
- Name the documentation-only file doc.go
- Move the file and socket calls and read a process's folder and peer on both systems
- Declare the system name once, beside the other platform facts
- Move the guard's process, memory, signal and libc calls into one package
- Read every platform fact and WSL once from internal/platform
- Add the package that will own operating-system calls and platform facts
- Explain rules in comments instead of citing design documents

## [v0.1.26](https://github.com/Sawmonabo/workbench/releases/tag/v0.1.26) - 2026-10-17

### Features

- Log a watcher's exit on a signal
- Hand the watch over to the release an update installs
- Start a detached process from a command with descriptors to pass on, and take on a passed lock
- --since and --until take the forms guard logs --since takes
- Remove the guard log's old free-text lines
- Show the watcher's raw output and unmatched notes in guard logs
- Log events as JSON Lines and show them with workbench guard logs
- Update Codex and Claude Code through the installer that put them there
- Keep the costs files in Workbench's folders, and move them with workbench migrate
- Stop the largest agent command before memory runs out

### Fixes

- Tidy release-note wording and keep an installed row with no releases
- Show an installed pre-release or local build in version --list
- Replace a local build only when it runs as the active release
- Replace a local build with the latest published release
- Say why a release newer than the latest published one is kept
- Keep the watcher's own output out of its log, and follow any cut by an offset map
- Let a log cut that adds more than it drops grow the file
- Take the higher of the user and managed minimumVersion floors
- Ignore a relative CLAUDE_CONFIG_DIR in settings, and keep reading ~/.claude/projects after a move
- Cut logs in place without losing lines, and follow them by position
- Report a CLAUDE_CONFIG_DIR or CODEX_HOME that moves an agent away from Workbench's settings
- Plan no native Claude Code update below its settings' minimumVersion
- Verify Claude Code's binary before it runs and set a floor that never stops updates
- Keep Codex and Claude Code's own updaters on and check Claude Code's signed manifest
- Leave a Homebrew Codex or Claude Code on Linux without adding an npm copy
- Stage native's writes in the run's private folder and check that folder's group
- Prefix only the enforced hooks that run workbench with its path
- Match only by call ID where Codex sets them, and tick at 1 s at warning
- Refuse a native file whose folder group the user cannot set
- Bind Workbench's runtime folders into approved plans
- Read Linux extended attribute names into a buffer
- Match Codex commands by call ID, guard a PID 1 agent, drop abandoned notes
- Run the agent hooks from the apply's bin folder
- Match every agent shell form, read the cgroup, and hold soft signals
- Read ACLs through libSystem's getattrlist and fgetattrlist
- Open the ledger once per command and let empty old folders wait
- Log what the worker's move left once the ledger moved
- Gate every ledger open and finish the review's costs fixes

### Documentation

- Record the observed v0.1.26-rc.1 pre-release run
- Explain release-note ranges and the changelog tag choice
- Describe the full, pre-release and local channels
- State when update replaces a local build
- Describe the published stamp and local build naming
- Record the watcher handover observed on Linux
- Describe the watcher's handover to a newly installed release
- Let a migration drop a held-open log's old-format lines
- Describe how Workbench reports an agent config folder moved by its variable
- Describe verify-before-run, the rising floor and the launcher rule for agent updates
- Correct ADR 0009 and describe the agent updaters, channel and signed manifest
- Describe the Codex and Claude Code updates and record ADR 0009
- Restore the measured ingest timings and reflow reworded lines
- Name the container inits never taken for an agent
- State behaviour, not history, in the records, specs and comments
- Record the overlay home check with the landed attribute read
- Say a harness process with a note's exact shape is tracked
- Record the Linux agent, pressure and migration runs
- Bind the folders, gate at the open and follow the bin folder in the designs
- Describe the shapes, cgroup reading, holds and abandonment the guard has

### Maintenance

- Allow the Claude Code signing key's public fingerprint
- Write each release's notes from an explicit tag range
- Publish a hyphenated tag as a GitHub pre-release
- Drop the message explaining why update keeps a newer local build
- Pin Codex 0.163.0 and Claude Code 2.1.301, the versions the guard was observed with
- Drop writeTable's unparam exception
- Call libSystem from one place and sweep stale writes once
- Use wg.Go and errors.Is for missing files
- Name the old-location variables once, in Go
- Sweep stale write temporaries in one function for notes and killed
- Own error categories and removal of a missing path
- Write notes and price caches with ReplacePrivateFile
- Start detached children and cap logs in one place
- Own private folders and folder syncs in one place

## [v0.1.25](https://github.com/Sawmonabo/workbench/releases/tag/v0.1.25) - 2026-10-14

### Fixes

- Drop the Todo Tree cleanup, which AGENTS.md's no-migration rule forbids

## [v0.1.24](https://github.com/Sawmonabo/workbench/releases/tag/v0.1.24) - 2026-10-14

### Features

- Switch to Better Todo Tree and remove retired extensions and settings

## [v0.1.23](https://github.com/Sawmonabo/workbench/releases/tag/v0.1.23) - 2026-10-12

### Fixes

- Print the approved plan by what runs, and count steps like the header

## [v0.1.22](https://github.com/Sawmonabo/workbench/releases/tag/v0.1.22) - 2026-10-12

### Fixes

- Let tools update when they only move Homebrew's Python
- Stop warning about updates Workbench skips on purpose

## [v0.1.21](https://github.com/Sawmonabo/workbench/releases/tag/v0.1.21) - 2026-10-12

### Features

- Say to reload the shell after apply changes its startup file

### Fixes

- Tell Claude Code and Codex to give rg a folder and cat a file

## [v0.1.20](https://github.com/Sawmonabo/workbench/releases/tag/v0.1.20) - 2026-10-12

### Features

- Infer the account of usage Claude Code never recorded
- Complete commands, flags and their values with Tab

### Fixes

- Count inferred usage under its account without a label

## [v0.1.19](https://github.com/Sawmonabo/workbench/releases/tag/v0.1.19) - 2026-10-08

### Fixes

- Match Claude Code's read deny rules to the credential files they guard

## [v0.1.18](https://github.com/Sawmonabo/workbench/releases/tag/v0.1.18) - 2026-10-06

### Fixes

- Name each Claude Code row by the organization that answered

## [v0.1.17](https://github.com/Sawmonabo/workbench/releases/tag/v0.1.17) - 2026-10-06

### Features

- Name a plan no sign-in recorded by its organization

### Fixes

- --all help names this machine's project folders
- Wait for a running ingest before the first report
- Name only the project folders that exist on this machine

### Maintenance

- Sum a report in one pass instead of grouping in SQLite

## [v0.1.16](https://github.com/Sawmonabo/workbench/releases/tag/v0.1.16) - 2026-10-06

### Fixes

- Drop the retired claude-costs hook and keep the Codex status line's usage items

## [v0.1.15](https://github.com/Sawmonabo/workbench/releases/tag/v0.1.15) - 2026-10-06

### Fixes

- Say outright that nothing was applied

## [v0.1.14](https://github.com/Sawmonabo/workbench/releases/tag/v0.1.14) - 2026-10-06

### Fixes

- Name the folder when one of Workbench's own folders is a link or a file
- Say why nothing was installed into ~/.local/bin
- An apply stopped mid-run or by a crash can be reverted, and ctrl+c says it was interrupted

### Documentation

- Say a file chezmoi creates takes its temporary folder's group
- Record interrupted and crashed applies and the ~/.local/bin reasons
- Revert checks the files an interrupted apply left unrecorded

## [v0.1.13](https://github.com/Sawmonabo/workbench/releases/tag/v0.1.13) - 2026-10-05

### Fixes

- Say plainly what a skipped step is missing, including the Command Line Tools for cargo
- The apps step says Homebrew is missing rather than that an install failed
- A failed setup step reports itself and the others still run
- A failed setup step no longer stops the steps after it
- Restore the rest and keep folders that now hold other files
- A failed setup step keeps correct files recorded as written and says plainly what to do
- A failed download or app install blocks its own step instead of stopping the apply
- Name the edited files and the folders that block a revert

### Documentation

- Record failed steps that leave the others running and the revert that keeps filled folders
- Describe the kept folders on revert and steps that never stop the others
- Record the v0.1.12 release checks and the failed-step fix
- Describe blocked apps and extensions and a failed setup step

## [v0.1.12](https://github.com/Sawmonabo/workbench/releases/tag/v0.1.12) - 2026-10-05

### Features

- Add an optional step that turns on Touch ID for sudo
- Leave the Mac password to sudo where Touch ID is on for it
- Install a checkout's pinned tools with update --local-build

### Fixes

- Keep the Touch ID step from prompting without a terminal
- Drop the sudo approval the password helper's answer leaves when the apply ends
- Give the terminal back as it was lent, so ctrl+c at the password helper's prompt leaves echo on
- Never leave a password read pending, so ctrl+c at the prompt cannot hang the apply
- Wait for the package lock that unattended-upgrades holds on a new Ubuntu machine
- Keep ~/.config private (0700)
- Ask for the Mac password once per apply and answer Homebrew's sudo requests
- Name the missing, invalid or unknown key when machine answers are refused
- Name the file and the chmod that fixes a private file open to other users
- Name the workbench path the shell can run in the unfinished-apply hint
- Write the GitHub sign-in helper on the first apply, before gh is installed
- Ask for the Mac password up front only to install Homebrew
- Say what the runtimes step will install before nvm, uv or rustup exist
- Keep ~/Library/Application Support private as macOS makes it
- Ask for the Mac password up front when an apply installs apps
- Name the full workbench path in the Mac password messages
- Name the full workbench path until a new terminal finds it
- Ask for the Mac password once before an apply installs Homebrew or apps
- Refuse an unreadable Windows settings file instead of reporting a change
- Keep comments and layout when merging VS Code settings
- Keep the comments of a settings file intact while planning
- Refresh Homebrew's index before an attended apply plans updates
- Name a qualified uv version once when the pin repeats an extra
- Refuse tool pins that name another download host or path
- Messages and comments no longer name the removed --source flag
- Missing-tool and missing-answer messages name a command that works
- Apply --local-build installs the checkout's pinned tools
- A saved choice this host does not offer warns instead of blocking
- A signal ends a prompt instead of waiting for a key
- Keep plan details when --approve-plan skips the prompt
- Version works when the saved state is unreadable
- Invocation errors say what was wrong
- List --approve-plan in --help
- Refuse an apply nobody approved before it downloads or saves anything
- Homebrew's update check uses the proxy and CA settings
- An empty VS Code settings.json counts as no settings
- A failed Windows download blocks only the steps that need it
- Windows setup is off until you tick it
- The pinned Rust toolchain becomes the default
- Windows setup no longer ticks the Terminal, PowerShell and font steps
- Stop listing toggles that cannot switch their files off
- The WSL plan renders the Windows scripts with the distribution name
- The Windows VS Code settings merge leaves unchanged files alone and keeps comments
- A broken or interrupted Windows oh-my-posh or font install is repaired
- An unreachable Windows side blocks its own steps, not the whole apply
- Homebrew cleanup keeps a formula when moving it to core fails
- Previewing an apply no longer refreshes Homebrew
- A missing docker CLI no longer fails the clean-Mac tools step
- Skip unticked sections in shared macOS and editor scripts
- Share one coding-agent check and carry npm, cargo and git CA settings
- Install private tools without leaving a truncated file behind
- Carry proxy, CA, color, locale and agent settings into the new runtime
- A date filter covers dated responses only
- Retry the WAL switch when two creators race for a new ledger
- Price Claude Code fast mode at the fast-mode rates
- Keep a forked Codex response when its parent was not read
- Price a model by its most specific rate, not its highest source
- Count and date the headline from the rows the report shows
- Report days and months in the machine's time zone
- Keep the ledger private and let two first-time creators share it
- Lay out new extensions.json entries in the file's own style
- --extensions indents the entries it adds to extensions.json
- --ci recognizes a test step written as a block scalar
- A ruff.toml below the project root blocks Python configure
- An existing [tool.pyright] table stops Python configure
- Stop the enclosing-project search at the repository root
- An unrelated lockfile no longer blocks Python configure, and a slow inspect says to narrow the scope
- Replace an older copy of the managed Claude hook instead of keeping both
- Fail when the oh-my-posh installer download fails
- Escape name and email for git and render vim without --wait
- Keep Codex-owned keys, inline hooks and user Claude hooks on apply
- Reuse download connections and say when GitHub limits requests
- Use gh only when it is logged in to github.com
- Repair a modified staged release, keep the previous release on reinstall, and time out stalled downloads
- Reject an empty or malformed VERSION instead of panicking
- Trust Workbench and home-level tool directories when $HOME holds a project marker
- Let any newer checkpoint supersede an incomplete one
- Do not flag files as edited outside when native wrote them last
- Do not overwrite another destination's unfinished-apply record
- Keep applies unblocked by retention, torn journals, stray files and incomplete checkpoints

### Documentation

- Say the Touch ID step can be the first to need the Mac password
- Record the Touch ID for sudo VM runs
- Describe Touch ID for sudo and the touch-id-sudo step
- Use Touch ID for sudo where it is on, and offer a step to turn it on
- Say a wrong password is asked again
- Describe dropping the helper's sudo approval and restoring the lent terminal
- Record the Mac password and ~/.config VM runs
- Point contributors at the decision records
- Count --approve-plan at a terminal as attended
- Note that the password check leaves no sudo approval behind
- Record the decisions to ask for the Mac password once per apply and keep ~/.config private
- Record the clean macOS 15.7.7 VM provisioning run
- Rewrap the Homebrew refresh paragraphs
- Describe the Homebrew refresh before an attended apply plans updates
- Describe cost checks and specs without one machine's figures
- Acceptance header, WSL themes, line widths, and --approve-plan keeping the plan
- Costs JSON span, update-time private tool files and release notes placement
- Acceptance record, ledger and Codex specs, plans and Python policy match the code
- Contracts, design and selection spec describe the checkpoint, consent and Windows rules as implemented
- Plan view comments no longer describe parent steps
- Usage, WSL and macOS notes describe the consent gate, opt-in Windows steps and recovery as they now work
- AGENTS.md states the consent rule, Windows setup rule, ledger exception and test scope

### Maintenance

- Pin the render and secret-scan runners to ubuntu-24.04
- Drop the plan view's locked parts
- The shared-script safeguard also reads the WSL scripts
- Lint the darwin target and shellcheck the repo scripts
- Verify git-cliff by checksum, guard the changelog commit, require pin SHAs
- Make the archive traversal safeguard fail when either path guard is off

## [v0.1.11](https://github.com/Sawmonabo/workbench/releases/tag/v0.1.11) - 2026-10-03

### Fixes

- Read the organization Claude Code records and fill a session's plan from its own transcript

### Documentation

- Source the credential_org version and note the backup rotation limit

## [v0.1.10](https://github.com/Sawmonabo/workbench/releases/tag/v0.1.10) - 2026-10-03

### Features

- Draw the plan as one grouped view with a detail panel, full-screen diffs and mouse
- Ask once, then apply the saved choices unless something new needs deciding
- Give each planned file a title, merged or owned, line counts and a capped diff

### Fixes

- Never prompt or apply unasked for a coding agent, even in a pseudo-terminal
- Cap every Windows call at 60 s, let one failing call block only its step and clean up behind the calls
- Judge a setting by its own key name and mask more credential shapes in the diff and the settings list
- Keep the macOS package probe from fetching Homebrew data, and allow the masking test's made-up token
- A step whose check could not finish says so on its row
- Say "could not check" when a Windows call gives no answer instead of showing a change
- Mask the value after a credential flag in mixed lists and label hidden hashes in the settings list
- Keep the service tier Codex's /fast toggle chose instead of resetting it on apply
- List the settings a merge changes instead of a line diff of the re-ordered file
- Draw steps with nothing to change without a box, make --reset forget decided, and cap the font registry read at 3 s
- Never apply unasked without a terminal, record only what was shown to decide, and tidy the plan view
- Make the Terminal part idempotent, cap the PowerShell call, and keep the .wslconfig probe in line with its merge
- Count moved lines in file diffs and mask more credential shapes in them
- Hide new folders, skip prompts for undecided effects with nothing to change, and keep Already set text whole
- Keep the RestartWSL helper's own shutdown prompt
- Show every group in --dry-run and the final frame, compact only for the no-prompt run
- Cap the slow Windows probe calls and let only the parts write Terminal and PowerShell files

### Documentation

- Rewrap the apply paragraph
- Describe the decided list, terminal rule, per-key WSL preview and the changed Terminal copy
- Describe decide-once apply, the new plan view and observed checks
- Fold the approved plan view into the plan-selection spec
- Specify decide-once apply, host-relevant effects and one plan view

### Maintenance

- Wire the plan view to the consent flow and keep each Windows part's own choice
- No-change probe lines, one Windows call per script, no prompts, self-sufficient Windows parts
- Plain wording for every effect, plain probe notes and a 15 s probe limit
- Add the plain-words fields and the detail and diff renderers to the contract
- Add the decide-once contract: decided, no-change probes and one plan renderer

## [v0.1.9](https://github.com/Sawmonabo/workbench/releases/tag/v0.1.9) - 2026-10-01

### Features

- Give each row's email its own evidence, apart from its subscription's
- Bind sessions and observe sign-ins on every ingest run
- Report accounts by email and subscription, show sign-ins and evidence in status
- Read each Codex row's plan, account and root session from its rollout
- Read each tool's subscription from its sign-in
- Attribute rows from session bindings and observed sign-ins
- Say why each unpriced model has no rate and where to add one
- Status names each hook's state, and wrapped values stay in their column
- Any service tier word prices from its own table, and a snapshot without one is standard
- Apply ends by reading new Claude Code and Codex usage into the costs ledger
- Codex runs the costs ingest hooks on every role, trusted by the config merge
- The codex tab shows one cache-write column and names each service tier
- Record codex responses from rollout files, priced at the service tier they ran at
- Ingest reads transcripts in parallel, keeps per-file source state and spots rewritten files
- Ledger schema 3 keeps per-file source state and service tier changes

### Fixes

- Label a Claude organization type as Max, Team or Enterprise
- Resolve a nested Codex subagent's rows to its root thread
- Refuse a development ledger at schema 3 instead of reading half of it
- Mark the stored root of an upserted row for re-attribution
- Run the session hooks synchronously and pass the session id to the worker
- Attribute rows of a run cancelled before ResolveAccounts
- Keep a fork's copied response until its parent's rollout is read
- Price tiers again when a change arrives late, through rowless subagents, and as Codex served them
- Read Codex forks and versions as Codex wrote them
- Split a service tier only off model ids Codex composed
- Price each Codex response at the tier Codex sent it at
- A ledger no tier check of this build has seen gets one full check
- A fork's first copied token_count no longer counts its parent's response twice
- Apply trusts Workbench's hooks without re-enabling one turned off
- The last tier snapshot in a millisecond wins, and the tier check covers only what a run wrote
- Resolve CODEX_HOME to its real path as Codex does
- Cancelling an ingest stops its transcript reads at once
- A transcript caught mid-rewrite is read again from the start
- The report reads a tool's transcripts inline when the ledger has none of its rows yet
- The codex config merge names the type of a hook it cannot trust
- Tier changes stamped in the same millisecond resolve in file order, and a tool without cache-write columns keeps the report aligned
- A hook workbench no longer installs loses its codex trust
- Status labels line up when more than one tool is recorded
- A codex file with any usage record, copied or its own, never counts token_count events
- A pending service tier follows the copy whose attribution the ledger keeps
- Render checks run unattended on a WSL host

### Documentation

- Specify per-field email evidence, subscription labels and the nested-subagent root walk
- Number the subscriptions section after verification
- Say what a mid-session login and a SessionEnd-only binding do
- Record the subscription verification and what still falls to unknown
- Describe per-row accounts and what a running session does after a login
- Specify per-row subscription attribution
- Codex first ingest observed on the host's disk
- Codex first ingest re-observed at 18 seconds, and the spec says what bounds it
- Codex costs observed on a WSL host
- Codex costs implementation plan
- Codex costs out-of-scope list matches tier pricing
- Price codex responses at the service tier they ran at
- Workbench trusts its own codex hooks
- Codex costs design

### Maintenance

- Fix the per-row subscription contract
- Check each Codex session event's ingest hook and its own trust entry
- A later copy with larger usage is never skipped as a no-op
- The ledger skips the disk sync at each commit, and read-ahead counts only the bytes left to read
- Codex lines are typed by their head and decoded once, and reads run further ahead
- The ledger prepares its statements once per file and skips copies that change nothing

## [v0.1.8](https://github.com/Sawmonabo/workbench/releases/tag/v0.1.8) - 2026-10-01

### Features

- Select a project or model and open its page by model, day and session
- A colored report with share bars, a headline total and a full key line
- Workbench costs replaces the claude-costs script behind a generic source interface
- One line per repository, plain column names, --detail and --tokens
- Update installs the release and its tools only; apply owns the machine
- Branded apply checklist with remembered effect skips, --yes, --reset and --local-build
- Probe effect deltas, check effects by saved selection and skip their scripts
- WORKBENCH_PROBE mode prints each effect's delta without changing anything
- Effect selection model saved in machine.toml [effects]

### Fixes

- The editor scripts probe the work editor extension, so work-tools is never unprobed
- Update without a version never installs a release older than the active one
- No local VS Code desktop is nothing to do, not a failed apply
- Setup leaves machine.toml untouched when its answers and skips are unchanged
- Status reports the hooks installed only when they call workbench costs ingest
- An isolated destination never records itself as the machine's applied configuration
- --approve-plan refuses a stale digest even when nothing would apply
- Left goes back from a project page instead of switching tools
- Share bars fill the terminal's whole width
- Apply removes the replaced claude-costs script and its completion
- The checklist leaves exactly its decided list on screen
- Align and clear the checklist, keep labels readable, wrap result lines
- Fit every table to the terminal; probes write nothing and stop on Ctrl-C
- Approve the selection the recheck computes under --reset
- Rerun partially skipped scripts, keep foreign skips and save all-unchecked selections

### Documentation

- Observed checklist, remembered skips and update/apply split on a WSL host
- Shift+tab switches the costs tabs; synthetic figures in the report example
- Costs tabs per tool, a resize-aware checklist and an ASCII fallback
- One color-profile test for tables and result marks; lint-clean table code
- Render terminal-width tables through lipgloss and charmbracelet/x/term
- Lock in workbench costs with a generic source interface and terminal-width tables
- Apply checklist, remembered skips, update/apply split and the unattended recipe
- Fix the apply plan selection spec and plan after review
- Revise the claude-costs report view and add its implementation task
- Add apply plan selection implementation plan
- Add apply plan selection and update/apply split design spec

### Maintenance

- Pin golangci-lint v2.14.0, which runs on Go 1.27

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
