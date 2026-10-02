# Apply plan, effect selection and the update/apply split

Status: the update/apply split, the checklist, probes and saved skips were
implemented 2026-09-30 (observed checks in docs/acceptance.md). Decide-once
apply, host-relevant effects, structured no-change probes and the redesigned
plan view (sections 2, 4 and 5, from 2026-10-01) are a target until the same
checks are recorded there.

## 1. Problem

Three defects in how a machine is changed today, observed on a WSL machine
switched from dotfiles on 2026-09-30:

1. `update` installs a release and then runs `apply`. A user who wants only
   the newer release gets a full provisioning prompt, and `install.sh` applies
   the machine as a side effect of installing.
2. The plan prints the effect *catalogue* (name, privilege, recovery prose)
   rather than what will happen. A plan whose files already match showed nine
   paragraphs and did not say "Node 22 → 26, Go 1.27.0 → 1.27.1, everything
   else present".
3. The plan is approved as one Yes/No. The standard effects cannot be declined
   one at a time, and a decision is not remembered, so a user who never wants
   the Windows-side steps must re-read and re-decide every run.

## 2. Decision

- `update` only installs. `apply` is the only command that changes the machine.
- A choice is made once. The first approved apply shows a checklist and saves
  what was chosen and what was shown (`skip`, `select`, `decided`). Later
  applies reuse that and apply the release's latest changes without asking,
  unless an effect needs a decision it has not had (a new effect, or a
  Workbench-owned file edited outside Workbench) or the owner asks to choose
  again (`--choose`, `--reset`).
- The plan is a branded view built like `workbench costs`: one row per changed
  file and one row per effect that does something on this host, each a plain
  name over one faint line of description, with everything longer in a detail
  panel for the row under the cursor. Effects that can never apply here are not
  planned; effects with nothing to change collapse into one faint line; an
  internal probe state or effect name is never printed.
- One approval. Ticking an item in the plan, or the saved choice on a
  no-prompt run, is the approval. A script run by apply never stops to ask a
  second question; what it would have asked about is shown in the detail panel
  beforehand.
- Skips are saved per machine and honoured until `--reset`.
- The command surface: `apply` shows `--dry-run`, `--yes`, `--choose`,
  `--reset`; `--approve-plan` and `--local-build` are hidden;
  `--config-only`, `--install-only`, `--skip`, `--only`, `--machine-config`,
  `--effect` and `--source` are removed. `--ask` moves to `init`.

Greenfield applies: superseded flags, the update→apply handoff and the
every-run checklist are deleted, not aliased.

## 3. Commands

| Command | Does | Flags |
| --- | --- | --- |
| `workbench update [VERSION]` | Find, verify, install and activate the release and its pinned tools. Prints `[WorkBench] Installed vX; run workbench apply`. Never applies. | `--dry-run`, `--bundle` (hidden) |
| `workbench apply` | Ask the missing machine questions on a first run, plan files and effects, apply the saved choices, and show the checklist only when something needs deciding or the owner asks (section 5). | `--dry-run`, `--yes`, `--choose`, `--reset`; hidden: `--approve-plan DIGEST`, `--local-build` |
| `workbench init` | One-time adoption (`--answers-from`) and re-asking a saved answer (`--ask NAME`, moved from `apply`). | `--answers-from`, `--ask`, `--dry-run` |
| `workbench doctor` | As today, plus the saved skips and the last apply's release and date. | |
| `install.sh` | Installs through `update`, then prints the `apply` instruction. Extra arguments go to `update` only. | |

`--local-build` replaces `--source PATH`: it takes no value and resolves the
Workbench checkout containing the current directory (the nearest ancestor
holding `.chezmoiroot` and `go.mod`), exit 2 with the reason when there is
none. The release
source check (`scripts/package-release.py` digest) is unchanged; a checkout is
still bound by its actual content digest.

## 4. Plan rendering

One renderer draws the interactive plan, the no-prompt apply and `--dry-run`;
the three differ only in the cursor, the key line and whether the list is
live. It uses the styles `workbench costs` uses: the `[WorkBench]` brand in the
accent colour, faint secondary text, and one truncation rule at every width (a
line that does not fit clips with `…`). At about 100 columns and wider the
detail panel sits beside the list; below that it moves under it.

```
[WorkBench] Plan for this PC · v0.1.10    3 files   2 steps will run   1 new to decide

Files                                       select to see the diff
> Claude Code settings  +14 −16
    ~/.claude/settings.json · your own settings kept
  Codex hooks  +14 −0
    ~/.codex/hooks.json · owned by Workbench
Will run
      AI safety settings  always
        Claude Code and Codex stay on your approved rules
  [x] Record AI usage  NEW
        new usage shows up in workbench costs
Off          you turned these off before
  [ ] Windows setup
        theme, fonts and VS Code settings on the Windows side
        [ ] Replace Windows Terminal settings
              replaces yours; a dated copy is kept
        [ ] Replace PowerShell profile
        [ ] Install fonts for Windows apps
  [ ] WSL networking
        Windows and WSL share localhost (mirrored)
Optional     off unless you turn them on
  [ ] Make <distro> your default
Already set  Lower swapping (vm.swappiness 10), Work tools (Bitwarden CLI)

↑↓ move · space on/off · enter open · a apply · q quit
```

Rules:

- Every screen `apply`, `update` and `doctor` print starts with `[WorkBench]`,
  including the result line, for example
  `[WorkBench] Applied: 3 files, 4 effects; 1 skipped`.
- Header: `[WorkBench] Plan for this PC · vX`, or `Applying your saved choices ·
  vX` when nothing is asked, then the counts: files, steps that will run, new
  to decide.
- Rows. Each row is the item's plain name on its own line with one faint line
  of description under it; nothing else. Everything longer lives in the detail
  panel. The internal effect name appears nowhere in the default view; each
  effect carries a plain `Title` and one-line `Summary` (for example
  `windows-files` is "Windows setup", `wsl-preferences` "WSL networking",
  `costs-ingest` "Record AI usage", `ai-security-settings` "AI safety
  settings", `terminal-adoption` "Replace Windows Terminal settings",
  `powershell-adoption` "Replace PowerShell profile", `font-registry` "Install
  fonts for Windows apps", `default-distro` "Make <distro> your default",
  `windows-path` "Add Windows bin folders to PATH", `sysctl` "Lower swapping",
  `work-tools` "Work tools"). Fixed effects are tagged `always`, effects new to
  this release since the last approved apply `NEW`.
- Files: one row per changed file with a friendly title (for example "Claude
  Code settings") and coloured `+N −N`, and a faint line with the path and
  `your own settings kept` (a merged file: a modify template that keeps the
  owner's own keys) or `owned by Workbench` (a file Workbench writes whole). An
  owned file edited outside Workbench since Workbench last wrote it says so and
  that the edit will be replaced. Selecting a file shows its unified diff in the
  detail panel; enter opens it full screen, and `q` or esc goes back. The plan
  carries each file's diff text, capped (and marked as shortened when it is).
  Unchanged files are counted in the header, not listed.
- Groups, each under a header: `Will run` (checked effects with a change; fixed
  effects first, unselectable), `Off` (saved skips: you turned these off
  before), `Optional` (the optional host effects, unchecked unless selected),
  then the faint `Already set` line. An empty group is not printed. The
  no-prompt view lists only what will run, then one faint `Off` line naming the
  skipped effects and `--choose`.
- Windows setup and its parts. `terminal-adoption`, `powershell-adoption` and
  `font-registry` are listed indented under Windows setup (indent only, no tree
  glyphs), wherever Windows setup is. Windows setup on: its parts show `[x]`,
  faint, and cannot be selected or toggled, because they are included. Windows
  setup off: each part is full brightness and can be ticked on its own. Each
  part installs what it needs itself and no longer depends on `windows-files`
  having run, so there is no partial state.
- The detail panel, for the row under the cursor: what it does, what it would
  do on this machine now (the probe's text), what it changes, who it runs as
  (with `network` when the script fetches), how to undo it, its status (always
  runs, will run, off because you turned it off, off and optional) and its
  relation to Windows setup. Non-interactive `--verbose` prints the panel for
  every listed row, and the recovery limits.
- Keys: ↑↓ move, space on/off, enter open (the full-screen diff for a file),
  `a` apply, `q` quit; mouse click selects a row, clicking the selected row
  toggles it.
- A probe that failed or timed out never appears as a status word. The row's
  panel says what could not be checked and that apply checks again when it
  runs, for example `Windows didn't answer in time; checked again when
  applied`. The effect stays checked and its script runs normally.
- `--dry-run` prints the same view once, without the key line, and exits 0.
  `--json` carries the same data: per file `path`, `title`, `action`, `summary`,
  `added`, `removed`, `diff`, `diff_truncated`, `merged`, `edited_outside`; per
  effect `name`, `title`, `summary`, `what`, `touches`, `runs_as`, `undo`,
  `delta`, `privilege`, `checked`, `saved_skip`, `no_change`, `new`, `optional`,
  `parent`, `probe` (`ok`, `failed`, `timeout`), `probe_note`; plus
  `plan_digest`. The digest covers the files, the effect names and which are
  checked, never the probed text, the no-change or new marks or the probe note,
  so a probe that answers differently at recheck cannot void an approval.
  File diff text is rendered, so it passes the same secret redaction as the
  scripts' output before it enters the plan.
- When the interactive list is dismissed, its final frame is printed once and
  nothing is left blank below it.

### Deltas

Each provisioning script gains a probe mode: with `WORKBENCH_PROBE=1` it
prints one line per effect it owns (most scripts own one;
`10-deploy-windows-configs` owns `windows-files` and `wsl-preferences`, and the
Windows scripts also describe the optional effects they carry) and exits 0
without changing anything, including no network calls other than the version
lookups it already makes (`go.dev/VERSION`, release APIs), each bounded by the
existing timeouts.

A line is `NAME: TEXT` when the effect would change something and
`NAME: = TEXT` when it has nothing to do (`=` and one space, then what is
already in place, for example `sysctl: = vm.swappiness 10`). The marker is
structural; nothing matches on the words of `TEXT`. An effect that several
lines or scripts describe is no-change only when every line is a `= ` line and
none of its scripts failed or timed out. For a change, `TEXT` follows
`<item> <from> → <to>`, `install <names>` for new items and `(asks)` for a step
that prompts during the run, joined with `; `.

The planner runs every active script's probe in parallel with a 15 s limit
each and shows a progress line while they run, with Go telemetry and Node's
compile cache off and Go's version read from its `VERSION` file rather than by
running `go`, so a probe writes nothing under the home directory. The two WSL
Windows scripts read every Windows variable they need in one `cmd.exe` call
instead of five each, which is what made a cold interop start overrun 5 s. A
probe that fails, times out or prints anything but effect lines leaves its
effects without a delta and with the plain-words note above; the effect stays
checked and its script runs normally. The plan never blocks on a probe.

## 5. Selection and state

- The selection saves to `~/.config/workbench/machine.toml`:

  ```toml
  [effects]
  skip = ["windows-files"]
  select = ["sysctl"]
  decided = ["linux-packages", "runtimes", "sysctl", "windows-files"]
  ```

  `skip` and `select` are as before: only skips of default effects and
  selected optional effects are stored, so a new default effect is checked
  unless declined. `decided` is every non-fixed effect the view listed at the
  last approved apply, including those on the `Already set` line, so it names
  what the owner has had the chance to decide. All three are written only
  after an approved apply, never by `--dry-run`.
- Merge-managed files (modify templates: Claude Code settings, Codex config,
  VS Code settings) keep the owner's own keys. Claude Code and Codex rewrite
  them constantly, so an edit outside Workbench on one never forces the
  checklist, and the view labels them `merged`. A file Workbench writes whole
  is `owned`.
- What `apply` does, after planning as before:
  - every non-fixed effect in the plan is in `decided`, and no owned file was
    edited outside Workbench since Workbench last wrote it: the plan prints
    and the saved selection applies without a prompt;
  - otherwise the checklist opens with the saved choices set, the undecided
    effects marked `new` and the edited owned files named, and an approval
    saves the result and `decided`.
  The first apply on a release that introduces `decided` has none saved, so it
  asks once.
- `--choose`: always show the checklist with the saved choices set. It needs a
  terminal and cannot be combined with `--yes`, which never asks (exit 2).
- `--reset`: forget `skip`, `select` and `decided` for this host's effects and
  show the checklist with defaults (everything default checked, optional
  effects unchecked). Saved entries for effects this host does not list (for
  example a macOS effect on Linux) are kept in `machine.toml` and ignored.
- `--yes` (`-y`): never prompt; undecided effects take their defaults (or their
  saved choice) and become decided.
- `--dry-run` and `--approve-plan DIGEST` are unchanged: a dry run saves
  nothing; an approved digest applies exactly that plan without a prompt, and a
  changed machine, release or selection exits 4 (conflict) with
  `Approval digest does not match the current plan; review a new plan`. The
  digest covers the files, the effects and which are checked, so a plan
  approved from a dry run cannot apply a different selection. An approved
  apply by any route records `decided`.
- A skipped effect is always listed, unchecked and tagged, so it never
  disappears silently. An effect that can never apply on this host (for
  example `linux-editor-extensions` on WSL, where the editor is the Windows
  desktop) is not planned at all, so it needs no decision. The files section is
  not selectable; "files only" is every effect unchecked. An `Already set`
  effect is not selectable either, and its saved decision is kept.
- Effects are gated one by one even when two share a script: apply exports
  `WORKBENCH_EFFECT_<NAME>=1` or `=0` for every effect, a script whose effects
  are all unchecked is not run at all, and a shared script wraps each
  effect's section in its own gate (`linux-packages`/`work-tools`,
  `windows-files`/`wsl-preferences`). A shared script with one effect
  unchecked still runs its other sections; its private copy gets a
  `# workbench: skipped <names>` line after the shebang, so chezmoi reruns it
  once the effect is checked again. Checking `windows-files` includes
  `terminal-adoption`, `powershell-adoption` and `font-registry` (they are its
  parts, shown checked and locked); with it unchecked each part is its own
  choice. A part installs what it needs itself. Saving records a part's
  `select` only while `windows-files` is unchecked, so the included state is
  never saved as a choice.
- The optional WSL effects (`terminal-adoption`, `powershell-adoption`,
  `font-registry`, `default-distro`, `windows-path`, `sysctl`) are unchecked by
  default and saved the same way; the three Windows parts are listed under
  Windows setup, the others under `Optional`.
  Checking one is remembered as `[effects] select = ["sysctl"]`.
- `doctor` prints `effects: skipped windows-files (saved)` and
  `applied: v0.1.8 on <date>`.

## 6. Unattended use

For agents and scripts, recorded in `AGENTS.md`:

```sh
workbench apply --dry-run --json      # read .plan_digest
workbench apply --approve-plan DIGEST # applies exactly that plan, exit 4 if it changed
workbench init --answers-from FILE --dry-run --json   # same pattern for init
```

`--yes` is for a person who trusts the saved selection; `--approve-plan` is
for a caller that read the plan first.

## 7. Failures

- A failing probe never blocks the plan (section 4). Ctrl-C during probing
  stops the plan at once with the interrupted exit (130); it is never shown
  as a failed probe.
- A script that fails mid-apply stops the run, as today. The result lists the
  checked effects, the failure's last lines and the checkpoint ID for the
  files; which effects ran is what chezmoi's script state says, and rerunning
  `apply` resumes from it.
- `--local-build` outside a Workbench checkout: exit 2.
- `update` with no newer release: `[WorkBench] v0.1.8 is already installed`,
  exit 0, no apply.

## 8. Testing

- CI render checks as today, plus one probe run per script per role/mode:
  `WORKBENCH_PROBE=1` must exit 0 and print only lines of the form
  `<effect-name>: <text>` or `<effect-name>: = <text>`. The WSL probes run under the existing static
  simulation.
- One Go test: a digest approved for a selection must be refused when the
  saved skip list changes between dry run and apply. It guards the consent
  boundary, the one place a wrong answer runs unapproved scripts. The
  no-prompt apply adds one more guarded risk, running an effect the owner
  skipped, and may add one safeguard for it.
- No script run by apply asks a second question (every `read -p`-style prompt
  in an apply-run script is gone; the `.wslconfig` merge shows its diff in the
  plan and still writes its recovery copy).
- Smoke checks of decide-once on an isolated destination: a first run asks;
  a second run with nothing new applies without asking; a new effect asks with
  it marked `new`; `--choose`, `--reset` and `--yes`; an owned file edited
  outside Workbench asks; a merged one does not. The view is rendered at 120,
  80 and 60 columns, interactive and dry-run identical in content.
  The cold-interop timeout is fixed structurally (one call, 15 s) and
  verified warm only; it cannot be made cold on demand without restarting WSL.
- Everything else is observed smoke checks, per `AGENTS.md`.

## 9. Out of scope

- Item-level selection inside an effect (Node yes, Go no). Effects can be
  split later (`runtimes` → `node`, `python`, `rust`) if one proves too
  coarse.
- Reverting packages, extensions or registry writes; recovery stays
  checkpointed files only.
- Changing which scripts exist or what they install.
