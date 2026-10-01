# Apply plan, effect selection and the update/apply split

Status: design approved 2026-09-30; not implemented.

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
- The plan is a branded checklist: one line per changed file, one line per
  effect with its concrete delta probed up front, everything checked by
  default, saved skips unchecked.
- Skips are saved per machine and honoured until `--reset`.
- The command surface shrinks: `apply` shows `--dry-run`, `--yes`, `--reset`;
  `--approve-plan` and `--local-build` are hidden; `--config-only`,
  `--install-only`, `--skip`, `--only`, `--machine-config`, `--effect` and
  `--source` are removed. `--ask` moves to `init`.

Greenfield applies: superseded flags and the update→apply handoff are deleted,
not aliased.

## 3. Commands

| Command | Does | Flags |
| --- | --- | --- |
| `workbench update [VERSION]` | Find, verify, install and activate the release and its pinned tools. Prints `[WorkBench] Installed vX; run workbench apply`. Never applies. | `--dry-run`, `--bundle` (hidden) |
| `workbench apply` | Ask the missing machine questions on a first run, plan files and effects, show the checklist, apply what is checked, save the skips. | `--dry-run`, `--yes`, `--reset`; hidden: `--approve-plan DIGEST`, `--local-build` |
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

```
[WorkBench] Plan for this machine (release v0.1.8)

Files (3 changed, 17 unchanged)
  ~/.bashrc                 +7 −19
  ~/.claude/settings.json   +37 −11, mode 0644 → 0600
  ~/.gitconfig              +4 −1

Effects
  [x] runtimes          Node 22.23 → 26.1 (default alias); Python 3.14 ok; Rust stable ok     user, network
  [x] runtime-managers  Go 1.27.0 → 1.27.1; nvm, uv, rustup, bun present                     user, network
  [x] global-tools      install just, cargo-audit, cargo-deny, gopls; 4 uv tools up to date   user, network
  [x] linux-packages    nothing to install                                                   sudo
  [ ] windows-files     .wslconfig networkingMode virtioproxy → mirrored (asks); 8 unchanged  Windows   skipped (saved)
Space toggles, enter applies, esc quits.
```

Rules:

- Every screen `apply`, `update` and `doctor` print starts with `[WorkBench]`,
  including the result line, for example
  `[WorkBench] Applied: 3 files, 4 effects; 1 skipped`.
- Files: changed ones listed with `+added −removed` lines and a mode change
  when there is one; unchanged ones counted.
- Width: every line fits the terminal. The privilege tag drops first, then the
  saved note, then the delta clips with `…`; below that each effect prints as
  a short stacked block. The rule and the shared table code, rendered through
  lipgloss, are in section 11 of the claude-costs ledger design. `--verbose`
  prints deltas in full, wrapped onto stacked lines rather than overflowing.
  The interactive checklist is a Bubble Tea list drawn by the same table code:
  it refits on every resize and scrolls when its rows are taller than the
  terminal.
- Effects: `[x]`/`[ ]`, name, delta, privilege tag (`user`, `sudo`, `Windows`,
  with `network` when the script fetches), and `skipped (saved)` when the skip
  came from `machine.toml`. Each effect's recovery text and the recovery
  limits print under `--verbose`.
- `--dry-run` prints the same checklist without the toggle line and exits 0.
  `--json` carries the same data: per file `path`, `action`, `summary` (the
  `+added −removed` and mode text); per effect `name`, `delta`, `privilege`
  (its text says `network` when the script fetches), `checked`, `saved_skip`,
  `probe` (`ok`, `failed`, `timeout`); plus `plan_digest`. The digest covers
  the files, the effect names and which are checked, never the probed text,
  so a probe that answers differently at recheck cannot void an approval.

### Deltas

Each provisioning script gains a probe mode: with `WORKBENCH_PROBE=1` it
prints one `<effect-name>: <delta>` line per effect it owns (most scripts own
one; `10-deploy-windows-configs` owns `windows-files` and `wsl-preferences`,
and the Windows scripts also describe the optional effects they carry) and
exits 0 without changing anything, including no network calls other than the
version lookups it already makes (`go.dev/VERSION`, release APIs), each bounded
by the existing timeouts. The planner runs every active script's probe in
parallel with a 5 s limit each, with Go telemetry and Node's compile cache
off and Go's version read from its `VERSION` file rather than by running
`go`, so a probe writes nothing under the home directory. A probe that fails, times out or prints
anything but `<effect-name>: <text>` lines shows `unprobed` for that effect;
the effect stays checked and its script runs normally. The plan never blocks on
a probe.

Probe lines follow the pattern `<item> <from> → <to>` for a change, `<item>
ok` or `present` for no change, `install <names>` for new items, and
`(asks)` for a step that prompts during the run. They are joined with `; `.

## 5. Selection and state

- The checklist saves to `~/.config/workbench/machine.toml`:

  ```toml
  [effects]
  skip = ["windows-files"]
  ```

  Only skips are stored, so an effect new to a later release is checked by
  default. The list is written after an approved apply, never by `--dry-run`.
- `--yes`: no prompt; applies the plan with the saved skips. The `-y` short
  form exists.
- `--reset`: ignore the saved skips for this run (everything checked), then
  save what is picked. Saved skips for effects this host does not list (for
  example a macOS effect on Linux) are kept in `machine.toml` and ignored;
  `--reset` re-checks this host's effects but keeps those foreign skips.
- `--approve-plan DIGEST`: applies exactly that plan without a prompt. The
  digest covers the files, the effects and which are checked, so a plan
  approved from a dry run cannot apply a different selection. A changed
  machine, release or selection exits 4 (conflict) with
  `Approval digest does not match the current plan; review a new plan`.
- A skipped effect is always listed, unchecked and tagged, so it never
  disappears silently. The files section is not selectable; "files only" is
  every effect unchecked.
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
  same list, unchecked by default, and are saved the same way. Checking one
  is remembered as `[effects] select = ["sysctl"]`.
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
  as `unprobed`.
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
  `<effect-name>: <text>`. The WSL probes run under the existing static
  simulation.
- One Go test: a digest approved for a selection must be refused when the
  saved skip list changes between dry run and apply. It guards the consent
  boundary, the one place a wrong answer runs unapproved scripts.
- Everything else is observed smoke checks, per `AGENTS.md`.

## 9. Out of scope

- Item-level selection inside an effect (Node yes, Go no). Effects can be
  split later (`runtimes` → `node`, `python`, `rust`) if one proves too
  coarse.
- Reverting packages, extensions or registry writes; recovery stays
  checkpointed files only.
- Changing which scripts exist or what they install.
