# Workbench migrations design

Status: implemented, Oct 15, 2026. Numbered, idempotent migrations that bring files
Workbench writes into Workbench's own runtime folders. They run from `apply`,
as its one `migrations` step, or by hand with `workbench migrate`. The
first ones:
- move the costs files out of the `claude-costs/` folders
- remove the old shell-completion cache, which the startup files rebuild in
  Workbench's cache folder
- remove the old `claude-costs` script's cache.

After them every Workbench-owned file lives under the five runtime folders
`operation.Resolve` returns, and new runs use only those paths.

## 1. Decision this needs from AGENTS.md

AGENTS.md says "Greenfield … No migrations, compatibility readers/flags,
obsolete aliases or dual paths" and "Reject unsupported state without
deleting or converting it". Its one exception is the costs ledger's in-place
schema upgrade. This design adds a second exception, recorded in
`docs/adr/0008-migrations-move-workbench-files.md`, and AGENTS.md's
development contract states it in two sentences:

> A second exception: numbered migrations in `internal/migrate/` may move files
> Workbench itself wrote into its runtime folders, without losing data, and
> delete caches, logs, locks and empty files Workbench itself wrote, and an old
> copy whose new copy holds the same bytes; they are idempotent, never touch
> other tools' files, never merge, and leave a file where it is when old and
> new data both exist. The costs import worker runs the costs move itself,
> without asking, because usage left unread could be lost (ADR 0008).

Nothing else in the contract changes. A migration may read the old
locations, including old environment variables, only to find what to move.
No runtime code reads an old location.

## 2. What Workbench writes today

Every path below was traced to the code or template that writes it.

### 2.1 Already in Workbench's runtime folders

| Folder | macOS default | Linux and WSL default | Contents |
|---|---|---|---|
| Config | `~/Library/Application Support/workbench/config` | `~/.config/workbench` | `machine.toml` |
| Data | `~/Library/Application Support/workbench/data` | `~/.local/share/workbench` | `releases/`, `tools/`, `application-contexts/` |
| State | `~/Library/Application Support/workbench/state` | `~/.local/state/workbench` | `state.json`, `activation.json`, `checkpoints/`, `chezmoi/`, `locks/` |
| Cache | `~/Library/Caches/workbench` | `~/.cache/workbench` | `uv/` |
| Bin | `~/.local/bin` | `~/.local/bin` | `workbench` |

- **How each folder is chosen** (`internal/operation/context.go:169-240`), in
  order:
  1. `WORKBENCH_<NAME>_DIR`, which must be absolute.
  2. `$XDG_<NAME>_HOME/workbench`, when that variable is absolute.
  3. The default above.
- **Each folder that exists must be owned by the user,** and the config, data,
  state and cache folders private, with no group or other permissions
  (`context.go` `runtimePaths`). Workbench makes them `0700`.
- **What writes there:** every write through `c.Paths.*` in
  `internal/machine`, `internal/release` and `internal/operation`.
- **No history to migrate:** the defaults are unchanged since `3d4f6c4`, which
  introduced them (the repository starts at `d75cfaa`).

### 2.2 Outside them, and migrated

| Writer | Path today | Code |
|---|---|---|
| Ledger, with SQLite's `-wal` and `-shm` | `$CLAUDE_COSTS_LEDGER`, else `${XDG_DATA_HOME:-~/.local/share}/claude-costs/ledger.sqlite` | `internal/costs/ledger.go:38-70, 208-327` |
| Claude price cache | `rates-official.json` beside the ledger | `internal/costs/rates.go:334-342` |
| Codex price cache | `rates-official-codex.json` beside the ledger | same |
| A price write stopped part way | `rates-official*.json.tmp` beside the ledger | `rates.go:356-367` |
| Import log and lock | `$CLAUDE_COSTS_STATE`, else `${XDG_STATE_HOME:-~/.local/state}/claude-costs`, holding `ingest.log` and `ingest.lock` | `ledger.go:62,73-74`; `ingest.go:52, 725-762` |
| Manual price overrides (the owner's file) | `$CLAUDE_COSTS_RATES`, else `${XDG_CONFIG_HOME:-~/.config}/claude-costs/rates.json` | `ledger.go:63-69` |
| zsh completion cache (macOS) | `${XDG_CACHE_HOME:-~/.cache}/workbench-completion.zsh` | `home/dot_zshrc.tmpl:74-85` |
| bash completion cache (Linux, WSL) | `${XDG_CACHE_HOME:-~/.cache}/workbench-completion.bash` | `home/private_dot_bashrc.tmpl:85-96` |
| Old `claude-costs` script's parse cache | `${CLAUDE_COSTS_CACHE:-~/.cache/claude-costs}/<32 hex>.json.gz` and `.json.tmp` | Script at `d75cfaa` (lines 25, 103-108, 161-178); not read since `76c3f71` |

- **The old costs locations follow different rules from Workbench's own:**
  - They accept a relative `XDG_*_HOME`, which resolves against whatever folder
    the process started in.
  - They use the `~/.local/…` defaults on macOS too.
- **What a migration finds:** it resolves absolute values and the defaults
  exactly as the old code did. A relative `XDG_*_HOME` or `CLAUDE_COSTS_*`
  can't be found again, so the migration reports it. When it hides the old
  ledger, `0001` is a *Conflict* until a ledger is at the new path (5.4, 6.1);
  any other file it hides is skipped.
- **Which shell gets which file:** `home/.chezmoiignore` deploys `.zshrc`
  only on macOS and `.bashrc` only on Linux and WSL.

### 2.3 Outside them and staying

| Path | Why it stays |
|---|---|
| `~/.config/Bitwarden CLI/session` | Bitwarden CLI's own file; Workbench's `bw` wrapper writes it where Bitwarden reads it |
| `~/.local/share/go-versions`, `~/.local/bin/{go,gofmt,gh,bw,bat,fd}`, `~/.local/share/fonts`, `~/.local/share/man/man1` | The user's tools, installed by the Linux scripts |
| `~/.cache/oh-my-posh`, `~/.tmux/plugins`, `~/.config/oh-my-posh` | oh-my-posh and tmux |
| `~/.claude/**`, `~/.codex/**`, `~/.config/{ruff,ty,tmux}`, VS Code settings | The tools whose settings Workbench manages |
| `~/.config/chezmoi/chezmoi.toml` | The dotfiles-era chezmoi config; Workbench only reads it (`init --answers-from`, the `native-chezmoi` check) |
| `*.before-workbench.*` | Backups beside the Windows files they protect |
| `wb-askpass-*`, `workbench-{init,preview,version,project-resolution}-*`, script `mktemp` downloads | Temporary; removed by the code that makes them |
| `~/.local/bin/claude-costs` and its bash completion | Already removed by `home/.chezmoiremove` |

## 3. Target layout

| What | New path |
|---|---|
| Ledger (one database, both tools) | `<data>/costs/ledger.sqlite` (+ `-wal`, `-shm` while open) |
| Claude price cache | `<data>/costs/claude/rates-official.json` |
| Codex price cache | `<data>/costs/codex/rates-official.json` |
| Import log and lock | `<state>/costs/ingest.log`, `<state>/costs/ingest.lock` |
| Manual price overrides | `<config>/costs/rates.json` |
| Completion cache | `<cache>/completion.zsh` (macOS), `<cache>/completion.bash` (Linux, WSL) |
| Migration record | `<state>/migrations.json` |

**The ledger stays one database:**
- One import run writes both tools.
- Account matching reads both tools' rows in one query
  (`ledger.go:1520-1530`).
- Splitting it would rewrite the only lasting record of spend.

Only the per-tool price caches get their own folders. Reports already filter
by tool (`report.go:493`).

**Every new folder is private:** `0700`, created through the operation
layer's private-directory owner. Every new file is `0600`. This also fixes the
`0755` `claude-costs` folders and `0644` files the old script left.

### 3.1 Before and after, macOS

```
BEFORE
~/.local/share/claude-costs/                    0755
├── ledger.sqlite                               0600
├── ledger.sqlite-wal, ledger.sqlite-shm        (while open)
├── rates-official.json                         0644
└── rates-official-codex.json                   0644
~/.local/state/claude-costs/                    0755
├── ingest.log                                  0644
└── ingest.lock
~/.config/claude-costs/rates.json               (only if made)
~/.cache/workbench-completion.zsh               0644
~/.cache/claude-costs/*.json.gz                 (only where the old script ran)
~/Library/Application Support/workbench/        0700
├── config/machine.toml
├── data/{releases,tools,application-contexts}/
└── state/{state.json,activation.json,checkpoints/,chezmoi/,locks/}
~/Library/Caches/workbench/                     (only once uv's cache was made)

AFTER
~/Library/Application Support/workbench/        0700
├── config/
│   ├── machine.toml
│   └── costs/rates.json                        (only if made)
├── data/
│   ├── releases/  tools/  application-contexts/
│   └── costs/                                  0700
│       ├── ledger.sqlite                       0600
│       ├── claude/rates-official.json          0600
│       └── codex/rates-official.json           0600
└── state/
    ├── state.json  activation.json  migrations.json
    ├── checkpoints/  chezmoi/  locks/
    ├── costs/{ingest.log,ingest.lock}          0700 / 0600
    └── guard/                                  (agent command guard design)
~/Library/Caches/workbench/                     0700
└── completion.zsh                              0600
```

### 3.2 Before and after, Linux and WSL

```
BEFORE
~/.local/share/claude-costs/{ledger.sqlite*,rates-official.json,rates-official-codex.json}
~/.local/state/claude-costs/{ingest.log,ingest.lock}
~/.config/claude-costs/rates.json               (only if made)
~/.cache/workbench-completion.bash
~/.cache/claude-costs/*.json.gz                 (only where the old script ran)
~/.config/workbench/  ~/.local/share/workbench/  ~/.local/state/workbench/  ~/.cache/workbench/

AFTER
~/.config/workbench/
├── machine.toml
└── costs/rates.json                            (only if made)
~/.local/share/workbench/
├── releases/  tools/  application-contexts/
└── costs/
    ├── ledger.sqlite
    ├── claude/rates-official.json
    └── codex/rates-official.json
~/.local/state/workbench/
├── state.json  activation.json  migrations.json
├── checkpoints/  chezmoi/  locks/
├── costs/{ingest.log,ingest.lock}
└── guard/
~/.cache/workbench/
├── uv/
└── completion.bash
```

Under WSL every path is inside the Linux home. No migration touches the
Windows side.

## 4. The migrations folder

`internal/migrate/` holds the registry and one file per migration, run in
number order:

```
internal/migrate/
├── migrate.go                  registry, check/run loop, record, step list
├── 0001_costs_folders.go       calls internal/costs for the ledger
├── 0002_completion_cache.go
└── 0003_old_costs_cache.go
```

Each migration is a value:

```go
type Migration struct {
    ID       string // "0001-costs-folders"; names it in the plan and output
    Title    string // "Move the costs files into Workbench's folders"
    Doing    string // output text: "moving the costs files into Workbench's folders"
    What     string // one paragraph naming every path it touches
    Recovery string // how to undo it, for the plan
    Check    func(Locations) (Status, string)                    // what a run would do, or what Done keeps
    Run      func(context.Context, Locations) ([]string, error) // the files it kept because Workbench didn't write them
}

type Status int // Done, Pending, Unfinished, Conflict
```

`Check` returns no error: a path that can't be inspected counts as there, so
nothing is called done that isn't, and the run then says why.

Every path a migration's text names is shown through `operation.ShowPath`:
home-relative, and quoted when the name holds `; ` (written `;\x20`) or a
control character, so one path never splits a probe line (5.2).

`Locations` holds both sets of paths. The new ones come from
`operation.Resolve`, the old ones from the section 2.2 rules.

### Check

`Check` never locks or changes a file, and never opens the ledger. It looks
at which paths exist (`Lstat`), with these small reads:
- `0001` reads an old and a new overrides file, only when both exist, to tell
  a copy that finished from two different files (6.1). When nothing is left,
  it lists the old `claude-costs` folders, to name the files it keeps there.
- `0002` reads the first line of an old completion file that `Lstat` shows is
  a regular file, to tell Workbench's marker from someone else's file (6.2).
- `0003` reads the old cache folder's listing, only when `Lstat` shows a real
  folder (6.3).

| Status | Meaning |
|---|---|
| *Done* | Nothing the migration can do is left. Its text may still name a file it keeps because Workbench didn't write it. |
| *Pending* | An old file waits to move or be deleted, or an old path can't be read. |
| *Unfinished* | A stopped copy of the ledger left its marks (`ledger.sqlite.part` at the new path, or `ledger.sqlite.moved` at the old one); see 6.1. |
| *Conflict* | Only conflicts are left: a file whose old and new paths both hold different data, a link at an old file's path, or an old ledger a relative variable hides (6.1). |

A migration with both work and a conflict is *Pending*: its run does the
work, and reports the conflict. A held lock or an open ledger is not a
status: the run says so and fails as busy (5.3), moving nothing, and the next
apply tries again.

### Run

`Run` checks first and returns at once when the status is *Done*, so an apply
with nothing to migrate takes no lock. Otherwise it takes its locks, checks
again, and finishes whatever is left. Running it twice, or after a stopped run,
only completes the remaining moves.

- **On a conflict** it merges nothing and overwrites nothing. A file whose old
  and new paths both hold different data stays where it is, and is named
  until the owner removes one. Every other file still moves.
- **`migrations.json`** (`schema_version` 1) records each ID a
  `migrate run` or `migrate --apply` finished, and when. The import worker's
  own costs move (5.4) is not recorded. Only `workbench migrate` reads it, to
  say when each finished; correctness never depends on it. A record that
  can't be read, or isn't schema 1, is never replaced: the run says the
  record wasn't saved, and the migration still counts as finished.

### Ownership

`internal/migrate` holds the list and the loop. A move of another package's
files is done by that package: the ledger move lives in `internal/costs`,
which alone knows how to open, close and check the ledger.

### Adding a migration

Adding one means a numbered file and one line in the registry. A released
migration is never renumbered or removed. Once its old paths cannot exist on
any machine, it costs only a few `Lstat` calls.

## 5. Running migrations

### 5.1 The environment

The apply runs scripts with a fixed environment, not the owner's
(`internal/machine/dependencies.go:213-247`, `internal/operation/process.go:196`).
chezmoi gives every script one environment, so the apply adds to every
script's environment, for its probe and its run:

- **The resolved runtime folders**, `WORKBENCH_{CONFIG,DATA,STATE,CACHE}_DIR`,
  as `operation/handoff.go:62-66` and `release/lifecycle.go:695-699` already
  pass them. Only Workbench reads these.
- **The old locations' variables, under another name.** Each of
  `XDG_{DATA,STATE,CONFIG,CACHE}_HOME`, `CLAUDE_COSTS_LEDGER`,
  `CLAUDE_COSTS_STATE`, `CLAUDE_COSTS_RATES` and `CLAUDE_COSTS_CACHE` that is
  set in the apply's own environment is passed as `WORKBENCH_OLD_<NAME>`.
  Passed under its own name, an `XDG_*_HOME` would change where installers
  such as nvm or uv put things. The shared template
  `home/.chezmoitemplates/old-locations.sh` hands them back under their own
  names to the installed `workbench` only, in the migrations and
  `costs-ingest` scripts. `migrate.OldLocationVariables` and the template
  list the same names.
- **`WORKBENCH_BIN_DIR`**, the resolved bin folder, where the scripts find
  the installed `workbench`.
- **`WORKBENCH_MIGRATIONS`**, the registry's IDs.

The `costs-ingest` script gets the same variables. That fixes the gap that
today makes it read the default ledger path whatever the owner set.

### 5.2 During apply

**One step, `migrations`, for every migration.** It is on unless skipped, like
`costs-ingest`, and is not an optional step (`internal/machine/effects.go`
`gate`). `effectSources` maps it to `05-migrations`, and the script checks
`WORKBENCH_EFFECT_MIGRATIONS`.
- **Later migrations ask at most once.** The apply contract asks once for a
  step that is new with something to do. An approved apply that showed
  `migrations` with something to do saves it in `decided`, and a migration
  added by a later release then runs with the saved choice and never prompts.
  A step that sat on "Already set" is not saved in `decided`, so the first
  apply that finds `migrations` with something to do asks once.
- **With nothing to do,** the step folds into "Already set"
  (`operation/plan.go:98`).
- **It is not a fixed step.** Fixed steps never run a script in this codebase:
  `hasProvisioning` (`apply.go:357-361`), the step flags (`plan.go:947`) and
  the step runner (`plan.go:1035`) all skip them. None is needed: the import
  worker runs `0001-costs-folders` by itself (section 5.4), both from the next
  hook and from this apply's `costs-ingest` step, which runs after the
  migrations script. Its `What` says so: skipping `migrations` skips the cache
  clean-ups and only defers the costs move to the next import.

**What the probe prints.** `probeResult.merge` (`probe.go`) joins an
effect's lines of each kind with `; ` and counts the effect unchanged only
when every line is. So no migration's text holds `; ` (section 4 shows how a
path holding one is quoted), and:

| Case | Probe lines | In the plan |
|---|---|---|
| Something to run | one `migrations: <ID>: <what it moves and deletes>` per *Pending* or *Unfinished* migration; nothing for a *Done* one | A change listing each |
| Nothing to run | exactly one `migrations: = none to run` | Folded into "Already set" |
| A *Conflict* | `migrations: ? <ID>: <what it leaves and why>` | A note on the row; the lines that could be checked stay in the step's detail |
| Installed binary lacks an ID | `migrations: ? <ID> needs a newer installed workbench (workbench update)`, for each ID in `WORKBENCH_MIGRATIONS` the installed binary doesn't know | The same |
| No installed binary | `migrations: = Workbench is not installed here yet, so there is nothing to move`, from the script | Folded into "Already set" |
| Installed binary predates migrations | `migrations: ? the installed workbench predates migrations, so run workbench update`, from the script | A note |
| `migrate --probe` failed | None: the script's probe exits 1, and the planning step says `migrations: couldn't check` | The note `Couldn't check what this would do here; checked again when applied` |

The script tells an older binary by its help: such a binary answers
`migrate --help` with its general help, which has no `workbench migrate [`
usage line, and exits 0.

**What was checked and what wasn't stay apart.** `probeResult` keeps the
text of the lines that answered and of the `?` lines in separate fields, and
`merge` joins each separately, so one script that prints both (the
migrations script with `0001` in conflict and `0002` pending) keeps both.
This holds for every step, including ones two scripts carry (`windows-files`,
`work-tools`):
- **The step's fields:** `Delta` holds the answered lines and `ProbeNote` the
  unanswered ones, and `--json` carries both.
- **The plan row** shows the note, in yellow, as today (`planlist.go`).
- **The step's detail** shows `On this PC` with what was checked and a second
  line, `Not checked`, with the note (`plandetail.go`).
- **After the apply**, a step whose plan carried a note reports its summary,
  not the partial text, which would read as the whole of what ran
  (`apply.go` `effectResults`).

**Shown while planning.** The `Planning` status line (`plan.go:25`) is the
spinner an apply shows. `probeEffects` (`probe.go`) runs the
`05-migrations` probe alone, before the other scripts' probes:
1. `c.Step("checking migrations")` while it runs (a few `Lstat` calls and
   the small reads section 4 lists, two binary starts: `migrate --help`, then
   `migrate --probe`).
2. Then one step saying what it found, counting its change lines and its `?`
   lines apart (`probeResult` keeps both counts):
   - `migrations: none to run`
   - `migrations: 2 to run`, `migrations: 1 needs attention`, or both:
     `migrations: 2 to run, 1 needs attention`
   - `migrations: couldn't check`, only when the probe itself failed, timed
     out or printed no `migrations` line.
3. Then the existing parallel probes under "checking what each step would do
   here".

At a terminal each step replaces the last (`internal/cli/progress.go`
`statusLine`); without one each is printed on its own line. The lasting
statement is the plan's `migrations` row and the apply-phase lines below.

**Shown while applying.** The script prints in the `==> costs:` style of
`run_after_90-costs-ingest.sh.tmpl`, through `workbench migrate run`:

```
==> migrations: checking
==> migrations: 0001-costs-folders: moving the costs files into Workbench's folders
==> migrations: 0001-costs-folders: done
```

When nothing is pending:

```
==> migrations: checking
==> migrations: nothing to migrate
```

Then the apply goes on to the next script. A migration that can't finish (a
*Conflict*, a held lock, an open ledger, an error) prints its reason, and the
script reports the step through `part_blocked` (the shared `effect-report.sh`
template) with "a migration couldn't finish (the output above says why and
what to do)". The other migrations still run, and the script ends 0, as a
`run_after_` script may: the next apply tries again, and a conflict waits
until the owner deals with each file it names. An ID in
`WORKBENCH_MIGRATIONS` that the installed binary doesn't know is named
(`==> migrations: <ID>: needs a newer installed workbench`); the run still
runs the migrations it knows, then fails with "<ID> needs a newer installed
workbench, so run workbench update and apply again", and the step is reported
the same way. An installed binary that predates
migrations is reported the same way, and moves nothing. With no installed
binary the script prints `==> migrations: no installed workbench at …, so
nothing was moved` and ends 0, with nothing reported blocked.

**The script** is `home/.chezmoiscripts/shared/run_after_05-migrations.sh.tmpl`.
chezmoi runs scripts in the order of their full source paths
(`.chezmoiscripts/darwin/…`, `linux/…`, `shared/…`, then `wsl/…`). So it
always runs after the files and before `shared/run_after_90-costs-ingest`. On
macOS and Linux that is after the platform's own `run_after_` scripts; on WSL
it is before the `wsl/` ones.
- The binary is `"${WORKBENCH_BIN_DIR:-$HOME/.local/bin}/workbench"`; the
  apply passes `WORKBENCH_BIN_DIR`, its resolved bin folder.
- With `WORKBENCH_PROBE=1` it runs `workbench migrate --probe`.
- Otherwise, when `WORKBENCH_EFFECT_MIGRATIONS=1`, it runs
  `workbench migrate run`, which runs every *Pending* and *Unfinished*
  migration that binary knows.

**It runs the installed `workbench`,** in Workbench's bin folder, as
`costs-ingest` does. The Claude Code and Codex hooks run
`~/.local/bin/workbench`, so with the default bin folder the binary that moves
the costs files is the one that writes them afterwards, and an older installed
binary never writes the old folders again. An apply from a newer `--local-build` whose
installed binary lacks an ID gets the "needs a newer installed workbench"
note, and that migration waits for `workbench update`.

**`migrate run` is hidden,** as `costs ingest --worker` is. Ticking the step is
the approval, as for every script the apply runs, so `migrate run` runs only as
that step: without `WORKBENCH_EFFECT_MIGRATIONS=1`, or with a coding agent's
variable set (the apply's script environment never carries one), it is
refused (exit 3) and points to `workbench migrate --apply`. That stops a run
by mistake, not a caller that sets the variable itself. A run only moves or
deletes Workbench's own files, and never loses data.

### 5.3 By hand

**`workbench migrate`** lists each migration with its status and the paths
involved, and, from the record, when a run finished it. It changes nothing.

**`workbench migrate --apply`** runs every *Pending* and *Unfinished*
migration. It shows `Checking migrations` while it checks, then prints the
same lines as the apply (on stderr with `--json`, which keeps stdout for the
result). Its plan (`migrate.Plan`) has one fixed, checked effect per
migration with something to do, named by its ID and described by the paths
its check names, so the digest changes when the paths do. A migration in
conflict is a plan warning; a relative old variable (2.2) is a warning of the
result. Outside `--dry-run` it uses the same approval rule as `apply`, the
shared `checkConsent` (`internal/cli/consent.go`), checked before anything
happens:
- At a terminal it shows the list and asks once.
- `--dry-run --json` prints the list with a `plan_digest`, and
  `--approve-plan DIGEST` runs exactly that list. A digest that doesn't
  match the current check exits 4, even when nothing is left to run.
- A coding agent must use `--approve-plan`.
- Without a terminal it needs `--approve-plan` or `--yes`. A coding agent is
  refused `--yes` (exit 3).
- `--yes` with `--approve-plan` is refused (exit 2): both approve. So are
  `--dry-run`, `--yes` and `--approve-plan` without `--apply`.
- When only conflicts are left, it names them as warnings and exits 4
  (`ExitConflict`) with "A migration needs you: deal with each file the
  warnings name as they say"; it never says "Nothing to migrate" then.
- **Exit codes of a run** (`migrate run`, `migrate --apply`): a conflict, found
  by the check or met during the run, exits 4 and says the migration "can't
  finish until you deal with each file named above as it says"; anything else
  that stops a migration (a held lock, a lock that can't be taken, an open
  ledger, an error) exits 3, as does an ID the binary doesn't know (5.2), and
  the next apply tries again. A runtime folder `operation.Resolve` refuses
  exits 2, as it does for every command.

### 5.4 Automatically, for the costs files

Every ledger open goes through one gate in `internal/costs` (section 6.1).
While anything of `0001-costs-folders` is left, the gate acts by caller:

- **The import worker** (`costs ingest --worker`, started by the hooks and by
  the `costs-ingest` step) runs `0001-costs-folders` itself, with the same code
  and locks as `migrate run`, moving every file it can. Then it imports, once
  the ledger itself has moved.
  - The first hook after `workbench update` moves the files.
  - No session expires unread while an apply is pending.
  - It moves only Workbench's own files, so it needs no further approval.
  - **Only a ledger conflict stops the import.** A conflict over the
    overrides file leaves that file for the owner; the ledger still moves and
    the import goes on. A ledger conflict (old and new ledgers both present,
    a set-aside original with a ledger back at the old path, or a link at the
    old ledger's path, 6.1) stops it, and the worker logs why and opens no
    ledger.
  - **A relative variable that hides the old ledger** (`XDG_DATA_HOME` or
    `CLAUDE_COSTS_LEDGER` set to a relative path) counts as a ledger conflict
    until a ledger is at the new path: the old ledger may still hold usage,
    and an empty new one beside it would split the record. The reason says
    which variable to make absolute or unset.
- **`workbench costs`, `costs status` and `costs rates`** stop with "the costs
  files are moving to Workbench's folders; run `workbench migrate --apply`"
  while anything waits to move, and with the conflict's text on a ledger
  conflict. A conflict over another file alone doesn't stop them;
  `workbench migrate` names it. They never open the old ledger, and never
  create an empty ledger beside it.
- **The hook itself is not gated.** It writes only the new import log, which
  no migration moves or compares, and then starts the worker, which runs the
  gate.

**When Workbench's folders can't be used.** `costs.Locations` resolves the
costs paths through `operation.Resolve`, which refuses a runtime folder that
is not private, for example. The costs folders are made through
`operation.PrivateDirectory`, which refuses an existing `costs/` folder that
is not private. The hook also needs to open its import log. When any of these
fails, nothing is recorded, and it shows:
- **the hook** says why in one line on stderr, `workbench costs: nothing
  recorded: <reason>`, which the agent keeps, and still exits 0;
- **`workbench costs` and `costs status`** fail with the reason.

### 5.5 One run at a time

**Only one migration run.** A run holds `<state>/locks/migrate.lock` through
`operation.TryLock`. A second run that finds it held says another migration
run is moving files, names any conflict, and fails (exit 3, or 4 with a
conflict) without moving anything; the import worker finding it held fails as
busy.

**Nothing writes to the ledger mid-move.** Before moving, the costs migration:
1. Takes both import locks: `<old state>/claude-costs/ingest.lock` and
   `<state>/costs/ingest.lock`.
2. Opens the old ledger alone and runs `PRAGMA wal_checkpoint(TRUNCATE)`.
3. Closes it, then requires that `-wal` and `-shm` are gone.

SQLite removes them only when the last connection closes. If either remains,
another process still has the ledger open, for example a report from the
release before the update. The run fails as busy and moves nothing, because
moving an open database or its log apart from it corrupts it (SQLite, "How To
Corrupt An SQLite Database File", 1.4 and 2.5). The next hook or apply tries
again.

## 6. The migrations

### 6.1 `0001-costs-folders`

**Old locations** follow section 2.2:
- **Ledger:** `$CLAUDE_COSTS_LEDGER` or `<old data>/claude-costs/ledger.sqlite`.
- **Price caches:** beside that ledger file, wherever it is.
- **Log and lock:** `<old state>/claude-costs/`.
- **Overrides:** `$CLAUDE_COSTS_RATES` or `<old config>/claude-costs/rates.json`.

| Old | New |
|---|---|
| ledger | `<data>/costs/ledger.sqlite` |
| `rates-official.json` | `<data>/costs/claude/rates-official.json`; deleted instead when a cache is already there |
| `rates-official-codex.json` | `<data>/costs/codex/rates-official.json`; the same |
| `rates-official*.json.tmp` | deleted (an unfinished price write) |
| `ingest.log` | deleted (a trimmed diagnostic log); not moved, so a log the new release already started is never a conflict |
| `ingest.lock` | deleted by every run, while it holds it |
| overrides | `<config>/costs/rates.json`, byte for byte |

**Which files can conflict.** A price cache never does: it is derived from
the public price pages and fetched again when stale, so an old cache whose new
path already holds one is deleted. An old file whose new path holds the same
bytes is a copy that finished before its original was deleted, and the
original is deleted. Only the ledger, an overrides file whose old and new
paths hold different bytes, and a link at any old file's path (below) can
conflict. Each conflict is per file: every
other file still moves (section 4, Run).

**No rename replaces a file.** Every rename that puts a ledger or another
costs file in place is a no-replace rename, `operation.RenameNoReplace`:
`renameat2` with `RENAME_NOREPLACE` on Linux and `renameatx_np` with
`RENAME_EXCL` on macOS (both in the pinned `golang.org/x/sys`). On a
filesystem that rejects the flag it links the new name and unlinks the old,
which fails the same way when the target exists. Where links are unsupported
too, it checks the target and renames, which is safe only under the
migration's locks. A target found there is a conflict.

**One file under both names is a move to finish.** A link-and-unlink
fallback that stopped between the two leaves the ledger, or another file,
under its old and new names, or the ledger under its old name and
`<old>.moved`. The check sees one file (`os.SameFile`), not two holding
different data: the ledger is *Unfinished*, a small file reads as the same
bytes, and the run removes the old name (for `<old>.moved`, that extra name,
and the copy starts again).

**An old path that is the new file is already moved.** When an old path names
the new file or folder itself (`CLAUDE_COSTS_LEDGER`, `CLAUDE_COSTS_STATE` or
`CLAUDE_COSTS_RATES` set to it, or an old `claude-costs` folder linked to the
new one), the check and the run drop that old path (`OldPaths.apart`): nothing
is moved, deleted or locked through it. Two paths are one when they are the
same path, resolve through their links to the same path, or name the same
folder or single-link file (a name differing only in case, say).

**A link is never moved or read through.** A link at an old file's path (the
ledger, the overrides or a price cache) that doesn't name the new file is a
conflict, "leaves X, a link (put the file it names in its place, or remove
it)", whether or not a ledger is at the new path. Moving it could leave a
relative link pointing nowhere, and the file it names isn't one Workbench
placed there. An old `claude-costs` folder that is a link is never removed:
it is named as a file Workbench didn't write.

**Moving the ledger, after section 5.5:**
1. **Open it the normal way.** Open the old ledger with the costs package's
   open (`OpenLedger`, `ledger.go:208-327`). That verifies it is a costs
   ledger and upgrades a version 1, 2 or 4 schema in place, the exception
   AGENTS.md already allows. A database it refuses is reported, and nothing
   moves. Then checkpoint and close it (section 5.5).
2. **Same filesystem:** `rename` `ledger.sqlite` to the new path, then `fsync`
   both folders. It is one atomic step, with nothing in between.
3. **Different filesystems** (`EXDEV`, for example an `XDG_DATA_HOME` on
   another volume):
   1. Delete any stale `<new>.part`.
   2. On a second connection, hold `BEGIN IMMEDIATE` so no other writer can
      start. Under it, run `VACUUM INTO '<new>.part'` on the first
      connection, check the copy against the original (step 3), and record
      the original's size, modification time and inode. Then release the
      transaction and close the ledger, and `fsync` the copy and its folder.
   3. The check: open the copy and attach the original without taking
      either over (no schema upgrade, no journal-mode change, never a new
      file) and run only queries: require `PRAGMA quick_check` to be `ok`,
      and require the same tables, each holding the same rows (`EXCEPT` both
      ways; every ledger table has a primary key). The same row count is not
      enough: another ledger can have as many rows, and an original is never
      deleted for it. A copy that fails `quick_check` or differs is a ledger
      error, and the original stays: a copy just written is discarded; a
      `.part` a stopped run left, or a fresh copy that can't be deleted,
      stays, and the error says to delete it, after which the next run
      starts again from the original. Every other file still moves. At step
      6 a new ledger with different content is another ledger, a
      *Conflict*; one that fails `quick_check` is a ledger error that says
      to delete it, after which the next run puts the original back and
      starts again. The error names only the first problem `quick_check`
      reports and how many more there are.
      Checking a ledger leaves its content as it was; a WAL ledger no other
      process has open may have its write-ahead log folded into the file
      when the check's connection closes, as SQLite does for any last
      connection.
   4. Require that `-wal` and `-shm` are gone and that the original still has
      the recorded size, time and inode. A changed original means something
      wrote it after the copy: delete the copy and copy again, at most three
      times, then fail as busy.
   5. Rename the old ledger to `<old>.moved` on its own filesystem, then
      `<new>.part` to the new path, both without replacing. When the copy
      can't be put in place (a ledger appeared at the new path), `<old>.moved`
      goes back to the old path without replacing and the `.part` is
      deleted, so the next run finds both ledgers, a *Conflict*.
   6. Check the new ledger against `<old>.moved` again (step 3), then delete
      `<old>.moved`.

   The write lock is not held across the renames: the original must be
   closed first, because renaming an open database apart from its `-wal` file
   is what corrupts it ("How To Corrupt", 2.5). The import locks (5.5) keep
   every writer out, and step 4 catches anything that wrote anyway.
4. **Picking up a stopped copy:**

   | Left behind | What the rerun does |
   |---|---|
   | `<new>.part` without `<old>.moved` | Deletes the `.part` and starts again. |
   | `<old>.moved` with `<new>.part` | Checks the `.part` against `.moved` (step 3), puts it in place as step 3.5 does, then finishes step 3.6. A `.part` that differs is a ledger error: both stay, and the error says to delete the `.part`. |
   | `<old>.moved` with the new ledger | Deletes `.moved` only when the new ledger holds the same content (step 3). A ledger with different content, even one with as many rows, is a *Conflict*: nothing is touched, and both are named. One that fails `quick_check` is a ledger error that says to delete it; the next run then puts the original back and starts again. |
   | `<old>.moved` alone | Renames it back to the old path without replacing, and starts again. |
   | `<old>.moved` with a ledger back at the old path | A *Conflict*: nothing is touched, and both are named. |
   | The old ledger and `<old>.moved` as one file | Removes the `.moved` name, and starts again. |

   Old and new ledgers both present are a *Conflict* whatever marks are left;
   the check looks for both before it looks for marks. After picking up, a
   ledger at the new path is never replaced: with the old one gone the move
   is done, and with it present it is a *Conflict*. The check never opens a
   ledger, so a set-aside original beside a different new ledger, or beside
   one that fails `quick_check`, checks as *Unfinished*; the run reports the
   *Conflict*, or, for the one that fails `quick_check`, the ledger error that
   says to delete it.
5. **Make it private:** the ledger ends at `0600` in a `0700` folder.

**The other files** move by `rename`, or by copy, `fsync` and delete on
`EXDEV`.

**Removing the old folders:** each old `claude-costs/` folder is removed only
when it is empty, and never when it is a link. A file the migration doesn't
know stays and is named, and once nothing is
left `0001`'s check is *Done* with the same list ("keeps X (Workbench didn't
write it)", or "them" for several). Files the
migration knows are never on that list: the old ledger, its marks and
journals, and every price cache, overrides file, log and lock it moves or
deletes. One left in conflict is reported as a conflict only.

**A relative old variable.** When `CLAUDE_COSTS_LEDGER` or `XDG_DATA_HOME` is
relative, the old ledger can't be found again. `0001` is then a *Conflict*
naming the variable, until a ledger is at the new path (section 5.4); its
other files still move.

**Code changes in the same release:**
- **`costs.Locations` uses `operation.Resolve`.** It drops
  `CLAUDE_COSTS_LEDGER`, `CLAUDE_COSTS_STATE` and `CLAUDE_COSTS_RATES`;
  `WORKBENCH_{DATA,STATE,CONFIG}_DIR` relocate the costs files with everything
  else.
- **`officialPath`** becomes `<data>/costs/<tool>/rates-official.json`.
- **One gate before every ledger open** (section 5.4), placed before anything
  else the command writes:
  - the worker, in `Ingest`, before `bindSession` (`ingest.go:51, 150`;
    `ledger.go:1082`) and before its import lock
  - the reports and status (`internal/cli/costs.go` `ensureIngested`, which
    `workbench costs` and `costs status` call first) and `costs.Status`
    (`report.go:681`)
  - `costs rates` (`costs.Rates`).

  The hook (`ingest.go:758-762`) is not gated: it writes only the new import
  log (section 5.4).
- **`workbench costs status`** names the new paths.

### 6.2 `0002-completion-cache`

**The old file:** `${XDG_CACHE_HOME:-$HOME/.cache}/workbench-completion.zsh` on
macOS, `.bash` on Linux and WSL. It is deleted, not moved: the new path is
rebuilt by the next shell.

**What counts as ours:** only a file whose first line is `# <absolute path
ending in /workbench>`, the marker the startup files write, is deleted. Both
names are checked on every platform, `Lstat` first: only a regular file is
read, and only its first line. Any other file of those names, a link
included, stays and is reported: `workbench migrate` lists it ("keeps …
(Workbench didn't write it)") even when nothing else is left, and a run names
it. One that can't be read keeps the migration *Pending* ("can't read …"), and
the run says why.

**The startup files** (`home/dot_zshrc.tmpl`, `home/private_dot_bashrc.tmpl`)
keep today's behaviour but use Workbench's cache folder, picked by the same
rules as `operation/context.go`. The template fills in each operating system's
default when it renders, so a new shell runs no extra program.

```sh
# zsh (macOS); bash on Linux and WSL is the same with $HOME/.cache/workbench
# and completion.bash
_workbench_cache=
if [[ $WORKBENCH_CACHE_DIR == /* ]]; then
    _workbench_cache=$WORKBENCH_CACHE_DIR
elif [[ -n $WORKBENCH_CACHE_DIR ]]; then
    _workbench_cache=      # relative: Workbench refuses it, so no cache
elif [[ $XDG_CACHE_HOME == /* ]]; then
    _workbench_cache=$XDG_CACHE_HOME/workbench
else
    _workbench_cache=$HOME/Library/Caches/workbench
fi
_workbench_completion=$_workbench_cache/completion.zsh
```

- **A relative `WORKBENCH_CACHE_DIR`** is refused by Workbench
  (`context.go:213-220`). The shell then makes no cache: it loads completion
  straight from `workbench completion` for that one shell.
- **The cache folder is made as Workbench makes it,** in a subshell, so the
  session's own umask is untouched: missing parents under `umask go-w`, never
  writable by group or others, and the folder itself `0700`
  (`umask go-w && mkdir -p "${_workbench_cache:h}" && mkdir -m 700 -p "$_workbench_cache"`;
  bash uses `$(dirname "$_workbench_cache")`). `-m` applies only to the last
  folder with both BSD and GNU `mkdir`, and only when `mkdir` creates it: an
  existing folder keeps its mode. The shell uses an existing folder as it is and
  doesn't change it; a `0755` runtime folder makes every Workbench command
  refuse to start (`context.go:236-252`), which reports it.
- **The file is written in the same subshell under `umask 077`,** so it's
  `0600`, to `completion.<shell>.$$`, and renamed into place only when the
  marker line and `workbench completion` both succeed; otherwise the
  temporary file is removed and an earlier cache stays.
- **The cache is read and sourced only when it is yours** (`-O`) and
  readable.
- **Rebuilding:** the first line still names the resolved `workbench` path. An
  update rebuilds the cache, and a cleared cache costs one `workbench
  completion` run (about 30–50 ms) in the next shell. Otherwise each shell
  reads one small file.

**Why a cache and not the shell's completion folder:** `workbench completion
zsh --help` suggests a folder on `fpath`, and bash-completion loads per-user
files from `${XDG_DATA_HOME:-~/.local/share}/bash-completion/completions/` on
demand. A file there would have to be rewritten by every update, and would
stop working silently if deleted. The cache rebuilds itself, for the cost of
one file read.

### 6.3 `0003-old-costs-cache`

**The old folder:** `${CLAUDE_COSTS_CACHE:-$HOME/.cache/claude-costs}`. The
old script built it from `HOME`, without `XDG_CACHE_HOME`.

**It holds only derived data** that nothing has read since `76c3f71`. The
migration:
1. Deletes the regular files named `^[0-9a-f]{32}\.json\.(gz|tmp)$`, the old
   script's naming (`cache_path` and its `.tmp` write).
2. Removes the folder if it's empty.

Any other file stays and is reported, as in 6.2. The folder is checked with
`Lstat` first and never read through: a link, or anything else that isn't a
real folder, is the one file kept and reported. A folder that can't be read
keeps the migration *Pending* ("can't read …"), and so does an empty folder,
until a run removes it.

## 7. The costs-ingest step's text

`internal/machine/effects.go` `costs-ingest` `Undo` becomes:

> Nothing to undo: rows already recorded stay, even after the session files
> they came from are deleted

**What the ledger ever deletes:**
- **Bookkeeping:**
  - where it stopped in a session file that has gone (`ingest.go:529-530`,
    `ledger.go:562-566`)
  - read positions, when a parser change rereads everything (`ForgetFiles`,
    `ledger.go:536-560`); the rows stay
  - tier, sign-in and metadata entries.
- **Duplicate Codex fork copies,** whose original row still counts
  (`DropHeldCopies`, `ledger.go:846-892`, called from `codex.go:690`).

Its `Touches` becomes the ledger in the data folder the apply resolved,
`<data>/costs/ledger.sqlite`, shown the way the plan shows home paths (with
`~`).

## 8. Files changed

| File | Change |
|---|---|
| `internal/migrate/*` | New: registry, loop, three migrations |
| `internal/operation/state.go`, `context.go`, `filesystem_{darwin,linux}.go` | `ReplacePrivateFile`, taken out of `WritePrivate`; `RenameNoReplace`; `Paths.MigrateLock`. Every private folder is made with `PrivateDirectory(root, name)`, which the agent command guard plan adds; this design adds no other folder helper |
| `internal/costs/move.go` | New: old and new locations, the check, the move, the ledger copy and its resume, the gate |
| `internal/costs/ledger.go`, `rates.go`, `ingest.go`, `report.go` | New paths from `operation.Resolve`; the gate at every open; the hook's stderr line |
| `internal/cli/costs.go` | Report and status go through the gate |
| `internal/cli/` | `workbench migrate` (`--apply`, `--dry-run`, `--json`, `--approve-plan`, `--yes`); hidden `migrate run` and `migrate --probe` |
| `internal/machine/effects.go` | The `migrations` step and its `effectSources` entry; `costs-ingest` `Undo` and `Touches` |
| `internal/machine/probe.go` | The `05-migrations` probe runs first, with the `checking migrations` steps; answered and unanswered probe text kept apart (5.2) |
| `internal/cli/plandetail.go`, `internal/machine/apply.go` | The step's detail adds a `Not checked` line; the after-apply message skips a partial text (5.2) |
| `internal/machine/dependencies.go` (or the probe and apply environment) | Section 5.1 variables for the migration and costs-ingest scripts |
| `home/.chezmoiscripts/shared/run_after_05-migrations.sh.tmpl` | New |
| `home/.chezmoitemplates/old-locations.sh` | New: hands `WORKBENCH_OLD_<NAME>` back to the installed `workbench` (5.1) |
| `home/.chezmoiscripts/shared/run_after_90-costs-ingest.sh.tmpl` | Runs the worker with the old locations |
| `home/dot_zshrc.tmpl`, `home/private_dot_bashrc.tmpl` | Completion cache path, modes, umask subshell |
| `docs/usage.md` | Costs paths (126-133), `workbench migrate`, completion cache |
| `docs/superpowers/specs/workbench-contracts.md` | Costs row (line 134): the new write paths; add `workbench migrate` |
| `docs/superpowers/specs/workbench-design.md` | Runtime layout and migrations |
| `docs/superpowers/specs/2026-09-30-claude-costs-ledger-design.md` | Paths table, the variables paragraph after it, the overrides path in the rate order, and "Ledger compatibility" point to this design |
| `AGENTS.md` | Section 1 sentence, including that the import worker runs the costs move without asking. New ownership row: "Migrations of Workbench's own files \| `internal/migrate/` (moves done by the package whose files move)". |
| `docs/adr/0008-migrations-move-workbench-files.md` | New |

`scripts/render-check.sh` covers the changed templates for every role and
mode.

## 9. Test

One test, for the catastrophic risk AGENTS.md names: silent, unrecoverable
loss or corruption of the costs ledger. It builds a ledger with rows for both
tools in a temporary home and runs `0001-costs-folders`:

1. **Same filesystem.**
2. **The copy path,** forced by the test.
3. **Stopped at each point a copy can stop** (a `.part` written; the
   original set aside beside its `.part`; the copy in place beside the
   set-aside original), then run again.
4. **A ledger back at the old path** beside a set-aside original and a copy
   in place.
5. **With a second connection holding the old ledger open.**
6. **With old and new ledgers both present.**
7. **With old and new overrides files holding different data.**
8. **One ledger under both names,** as a link-and-unlink fallback leaves it
   when it stops between the two.
9. **A different ledger at the new path** beside a set-aside original and
   its `.part`, holding as many rows.
10. **Old paths that name the new files:** `CLAUDE_COSTS_LEDGER`,
    `CLAUDE_COSTS_STATE` and `CLAUDE_COSTS_RATES` set to the new ledger,
    state folder and overrides.
11. **An old folder linked to the new one:** the old `claude-costs` data
    folder a link to `<data>/costs`.

**What it asserts:**
- **Cases 1–3:** the new ledger passes `quick_check` and holds exactly the
  original's rows, read through the test's own connection rather than the
  code under test. The old ledger is gone only after that, and a further run
  reports *Done* and changes nothing.
- **Cases 4 and 6:** *Conflict*, with every ledger file byte-identical to
  before.
- **Case 5:** refused as busy, with nothing moved.
- **Case 7:** a conflict over the overrides only: the ledger still moves with
  every row, the ledger itself checks as *Done*, and the overrides file is not
  named as a file Workbench didn't write.
- **Case 8:** *Unfinished*, never *Conflict*; the run finishes the move with
  every row.
- **Case 9:** *Conflict* on every run, with every file byte-identical to
  before: the same row count never lets a run delete the original.
- **Cases 10 and 11:** *Done*; a run succeeds and leaves every file
  byte-identical, with the ledger's rows, and in case 11 the link stays.

## 10. Order of work

1. ADR 0008 and the AGENTS.md sentence.
2. `internal/migrate`, the costs move and its gate, with the test.
3. The costs path change.
4. The `migrations` step and its planning steps, the section 5.1 environment, the `workbench migrate`
   command, and the startup files.
5. Checks:
   - `scripts/render-check.sh` for every role and mode.
   - A disposable home on macOS and on Linux, seeded with each "before"
     layout from section 3. The plan (`apply --dry-run`) must show `checking
     migrations` and `migrations: N to run` and list each pending migration;
     the migrations script, run as the apply runs it, must print the
     apply-phase lines and move everything; and the next plan must show
     `migrations: none to run` and fold the step into "Already set". A full
     apply is never run: its other scripts would provision the machine.
   - On Linux, the data folder on another filesystem (a tmpfs), so the
     ledger takes the copy path, with the row count compared before and
     after.
   - In the same homes, a hook firing right after `workbench update` must move
     the costs files by itself.
6. Docs (section 8).

## 11. Not in scope

- Files of other tools (section 2.3).
- Splitting the ledger by tool.
- Any change to Workbench's five runtime folders themselves.
- Undoing a migration. Its moves keep every ledger row and every other file's
  bytes, so moving the files back by hand restores the old layout. What they delete is
  rebuilt when needed (price caches, the completion cache), read by nothing
  (the old script's cache), the import log and lock, or an old copy whose new
  copy holds the same bytes.
