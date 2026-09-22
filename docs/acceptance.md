# Acceptance record

Date: 2026-09-22. Releases publish on `v*` tags for personal use; none is published yet.
Implemented handlers are not evidence that every native target is qualified.

## Observed checks

Native development environment: macOS 27.0 arm64, Go 1.26.4,
golangci-lint 2.12.2, chezmoi 2.70.3, uv 0.12.3, Python 3.13.7.

- Isolated native initialization used complete synthetic answers, built-in Git
  and no clone. Repeated initialization preserved configuration. A real terminal
  run also completed all five personal-role native questions and saved validated
  private answers; no simulated questionnaire substituted for that run.
- Editor-policy checks accepted the pinned ty/Ruff/basedpyright versions and
  extension setting schemas. Native user-config discovery worked. A repeated
  JSONC settings merge preserved unowned nested values/custom rules and bytes.
- Shared recovery checks refused later edits, corrupt images and escaping paths.
  Disposable apply/revert/undo restored exact files. At the 20-checkpoint
  limit, 22 further config-only applies each planned and removed the oldest
  settled checkpoint; 20 remained listed by `revert --list`.
- Native uv standalone/workspace resolution and repeated configuration/recovery
  passed in synthetic projects; no live project or global tool was changed.
- Native machine configuration applied 34 planned edits, including role-owned
  removals; repeat preview had zero edits and recovery restored original files,
  directory permissions and groups. SIGTERM after a durable running journal
  returned 130; the observed unchanged targets were recoverable. That timing
  does not qualify every possible mid-write interruption.
- All seven existing render entry points passed: personal/work/both in pinned
  and latest modes, plus work/pinned WSL simulation. These rendered synthetic
  macOS configurations, checked shell syntax/lint and malformed-input preservation,
  and ran existing leak/status-line checks. No provisioning scripts ran. Fixture
  freshness warnings were non-failing; WSL remains simulation only.
- Published-style local bundles passed stage-only, install-only, command-collision
  preservation and interrupted-activation resumption checks. Restricted-PATH
  bootstrap acquired private chezmoi/uv/CPython/TOML Kit, saved private native
  answers, then applied configuration only to a disposable destination. The
  separate setup/target approvals remained required. Remote download was not
  exercised against an actual published release.
- Final candidate checks passed: a changed-source candidate was previewed through
  the old entry point without changing the active runtime or configuration.
  Approved apply activated the matched candidate and changed the intended file.
  A CLI-only candidate activated without allocating a configuration checkpoint.
- Native apply runner: a streamed run lasted 62 seconds with no deadline and
  printed its redacted output; under a pseudo-terminal the child owned the
  foreground process group (stdin was a terminal), so sudo can prompt; a failed
  captured run reported its redacted stderr. Interactive and unattended
  config-only applies both completed into a disposable home.
- `install.sh`, with a local stand-in for the GitHub download, installed a
  darwin-arm64 bundle built by `scripts/package-release.py`: `--install-only`
  activated it, then an interactive `--config-only` install continued through
  the runtime handoff and applied configuration to a disposable home; the next
  plan had zero edits. With no release published, the real `gh` path reports
  "no release found".
- A one-run unoptimized development binary observation reported version startup
  at 0.00 seconds displayed precision and 13,041,664 bytes maximum RSS. Inspection
  of the checkout scanned 143 entries/four candidates in 0.20 seconds with
  13,844,480 bytes maximum RSS and zero swaps. These small-scope observations are
  not performance budgets, comparative benchmarks or universal guarantees.

Integrated local checks passed: `golangci-lint fmt`, configuration verification,
`golangci-lint run ./...` (zero issues), `go build ./...`, `go test ./...` (three
small safety safeguards), source-trust verification and whitespace checks.
Four macOS/Linux architecture cross-builds passed; they are compilation evidence.
A final affected personal/pinned render passed after source safeguard changes.

The final evaluation bundle passed checksum/manifest verification and offline
staging preview. Gitleaks found no leaks in the checkout or extracted payload
when run from their respective roots with the narrow canonical keyboard-shortcut
false-positive rule. Both consolidated code reviews closed their material
findings. Remote CI outcomes are recorded by the repository's GitHub Actions
runs; the local evidence here does not substitute for a successful remote run.
The initial remote run also passed Go quality/build/safety/cross-compilation and
all 13 existing Ubuntu/macOS role/mode/render jobs. These are native CI checks,
not full machine provisioning or Windows-host qualification.

## Unqualified release gates

| Area | Remaining evidence or decision |
| --- | --- |
| macOS | Complete disposable-user provisioning, minimum OS and Intel runs. Isolated arm64 checks do not qualify all native effects. |
| Ubuntu | Native 22.04/24.04/26.04 amd64/arm64 bundle/provisioning checks. Cross-builds and CI rendering are insufficient. |
| WSL/Windows | Real WSL2.6+/Windows11 24H2+ x64 path/ACL, Terminal/PowerShell preservation and individually approved external-effect checks. Full provisioning is enabled but unqualified: host adoption, font registry, PATH, default distribution and sysctl are selected `--effect`s, and no real host run is recorded. |
| Editor | Deliberately apply to an intended local profile, then confirm project-tool selection and only ty/native Ruff active. Linux/WSL editor hosts remain unchecked. |
| Release | First tagged release and a real `curl`/`gh` one-liner run; native capacity qualification. Releases stay unsigned with no redistribution license by decision. |

Windows ARM integration, native Windows, arbitrary Linux distributions and
additional project package managers/language configurators are not implemented
support commitments. No silent platform waiver is implied.

## Short qualification procedure

Use a disposable user/VM and synthetic credentials. Install a release bundle
with `install.sh` and without a checkout; inspect doctor/status; preview before approval; apply
configuration twice and compare; exercise unchanged recovery and a later-edit
conflict. Full provisioning additionally checks actual shell/Git/editor/theme,
runtime/tool outcomes, denied privilege and optional-effect denial. WSL requires
real host paths with spaces/non-default drives and Windows permissions.

Never execute restart helpers just to test their installation. Record OS/CPU,
tool versions, commands, outcome and known limits here. Reuse existing render,
lint and leak checks; do not add a new lifecycle or benchmark framework.
