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
- The plan is a branded view built like `workbench costs`: one line per changed
  file, one line per effect that does something on this host, each saying in
  plain words what it changes. Effects that can never apply here are not
  planned; effects with nothing to change collapse into one faint line; an
  internal probe state is never printed.
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

One renderer draws the interactive checklist, the no-prompt apply and
`--dry-run`; the three differ only in the cursor, the key line and whether the
list is live. It uses the styles `workbench costs` uses: the `[WorkBench]`
brand header in the accent colour, faint secondary text, and one truncation
rule at every width (a line that does not fit clips with `…`, and below the
narrowest width a row stacks onto its own lines).

```
[WorkBench] Plan for this machine (release v0.1.8)

Files (3 changed, 17 unchanged)
  ~/.bashrc                 +7 −19, owned
  ~/.claude/settings.json   +37 −11, mode 0644 → 0600, merged
  ~/.gitconfig              +4 −1, owned, edited outside Workbench: the edit will be replaced

Will run
     ai-security-settings  managed AI policy files
  [x] runtimes              Node 22.23 → 26.1 (default alias)
  [x] global-tools          install just, cargo-audit, gopls  new
Off: you skipped
  [ ] windows-files         you skipped
Optional
  [ ] terminal-adoption     needs windows-files
Already set: sysctl, work-tools

runtimes
  Changes   Node 22.23 → 26.1 (default alias); Python 3.14 ok; Rust stable ok
  Touches   Native Node/Python/Rust installs and default Node alias
  Runs as   user; network
  Undo      external; no runtime rollback

space toggles · enter applies · esc quits
```

Rules:

- Every screen `apply`, `update` and `doctor` print starts with `[WorkBench]`,
  including the result line, for example
  `[WorkBench] Applied: 3 files, 4 effects; 1 skipped`.
- Files: one line per changed file with what changes (`+added −removed`, a mode
  change, `new`, `removed`), its owner (`owned`: a file Workbench writes whole;
  `merged`: a modify template that keeps the owner's own keys) and, for an
  owned file edited outside Workbench since Workbench last wrote it, that the
  edit will be replaced. Unchanged files are counted.
- Groups, each under a header: `Will run` (checked effects with a change; fixed
  effects first, unselectable), `Off: you skipped` (saved skips, always listed,
  unchecked), `Optional` (the optional host effects, unchecked unless
  selected), then the `Already set` line. An empty group is not printed.
  An effect new to this release since the last approved apply carries `new`.
- Each row: box, name, then what it changes here in plain words right after
  the name. No far-right columns. The text clips; the full delta is in the
  detail panel.
- The detail panel, under the list, is for the row under the cursor: the
  untruncated change on this machine, what it touches, the privilege it needs
  (with `network` when the script fetches), how to undo it, and why it is off
  or what it needs (`needs windows-files`). Non-interactive output prints the
  panel for every listed effect under `--verbose`, and the recovery limits.
- A probe that failed or timed out never appears as a status word. The row says
  what could not be checked and that apply checks again when it runs, for
  example `Windows didn't answer in time; checked again when applied`. The
  effect stays checked and its script runs normally.
- `--dry-run` prints the same view once, without the key line, and exits 0.
  `--json` carries the same data: per file `path`, `action`, `summary`,
  `merged`, `edited_outside`; per effect `name`, `delta`, `privilege`,
  `checked`, `saved_skip`, `no_change`, `new`, `optional`, `needs`, `probe`
  (`ok`, `failed`, `timeout`), `probe_note`; plus `plan_digest`. The digest
  covers the files, the effect names and which are checked, never the probed
  text, the no-change or new marks or the probe note, so a probe that answers
  differently at recheck cannot void an approval.
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
  once the effect is checked again. Unchecking `windows-files` also
  unchecks `terminal-adoption`, `powershell-adoption` and `font-registry`,
  which act on the files it writes; their rows say `needs windows-files`.
- The optional WSL effects (`terminal-adoption`, `powershell-adoption`,
  `font-registry`, `default-distro`, `windows-path`, `sysctl`) appear in the
  same view under `Optional`, unchecked by default, and are saved the same way.
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
