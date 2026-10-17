# 9. Codex and Claude Code update through the installer that put them there

- Status: Accepted, October 16, 2026

## Context

Codex and Claude Code each ship through several channels: npm, a vendor
installer (Codex's standalone installer, Claude Code's native installer) and
Homebrew casks. The command-line tools step installed them through npm. On
macOS it did so only when they were missing, so an installed copy was never
updated; on Linux it reinstalled the pins through npm into nvm's default Node
whenever a pin changed, whatever had installed the copy the shell runs. A
vendor install earlier on PATH then stayed old, and npm added a second copy.

Each vendor documents one update path per channel, and running another one
installs a second copy rather than updating the first. Homebrew updates already
show the model to follow: one plan effect per outdated package, so approving
the plan approves the update, carried out by an apply script that runs exactly
the approved list. Both tools also update themselves, and Workbench's updates
must not fight those updaters.

## Options considered

- **A. One update effect per agent, run through the installer of the copy the
  shell runs, ending at the exact version the plan names, and leaving each
  tool's own updater on where the mode allows.** Chosen.
- **B. Always install through npm.** Simple, but it shadows or duplicates a
  vendor install, which both vendors warn about, and npm is not the
  recommended channel for either.
- **C. Leave updates to each tool's own updater.** Claude Code's native and
  npm installs update themselves in the background, but Codex updates only
  when its update prompt or `codex update` runs it, or, for a standalone
  install, in the background through its app-server daemon, and Homebrew casks
  update only through `brew upgrade`. Pinned mode would mean nothing, and the
  plan would not show what changes.

## Decision

- The plan finds the copy the shell runs, in the order the managed shell files
  put folders on PATH (on Linux and WSL `~/bin`, then bun's globals, nvm's
  default Node, Cargo's and Go's bin folders, `~/.local/bin`, then Homebrew's
  and the system's folders), and reads how it was installed and its version
  from the installer's own files, without running it:
  - npm in an nvm Node: `.nvm/versions/node/NODE/lib/node_modules/PACKAGE`,
    the version from that package's `package.json`; nvm's default Node is
    found by following `alias/default` through further aliases (such as
    `lts/*` to `lts/jod`) as nvm does;
  - Codex's standalone installer:
    `.codex/packages/standalone/releases/RELEASE`, the version from its
    `codex-package.json`, or else the release name;
  - Claude Code's native installer: `.local/share/claude/versions/VERSION`;
  - a Homebrew cask (`codex`, `claude-code`, `claude-code@latest`).

  A vendor installer updates only the launcher it makes, `~/.local/bin/claude`
  or `~/.local/bin/codex`; Anthropic's setup page says a launcher replaced by
  your own script or symlink is left in place. A release of either reached
  through any other launcher, such as a `~/bin/claude` symlink into
  `versions/`, is therefore named in a warning and left alone, rather than
  updated behind a launcher that keeps running the old one, which every plan
  would then propose again.
- The target is the `versions.toml` pin in pinned mode. In latest mode it is
  the newest release on the channel the tool's own updater follows: Codex's
  `releases.openai.com/codex/channels/latest` or the npm `latest` tag, and for
  Claude Code the channel `autoUpdatesChannel` names (`stable` or `latest`,
  unset meaning latest), read from the user settings (`settings.json` in
  `CLAUDE_CONFIG_DIR` when that is set, else in `~/.claude`) and the managed
  settings files (`managed-settings.json` and `managed-settings.d/*.json` in
  `/Library/Application Support/ClaudeCode` or `/etc/claude-code`), which
  override it: `downloads.claude.ai/claude-code-releases/stable` or `/latest`,
  or the npm `stable` or `latest` tag. A channel set only through an MDM
  profile or the claude.ai console cannot be read from a file and counts as
  latest; project and local settings depend on the folder Claude Code starts
  in and are not read. Planning looks both versions up side by side, each within 3 seconds,
  the limit the runtimes probe gives its Node lookup; the lookups answer in
  well under a second, a failed one only warns, and the next plan looks again.
- A copy as new as the target, or newer, is left alone, so nothing is
  downgraded. Versions compare as three numbers with an optional prerelease,
  which ranks below its own release (Semantic Versioning), so a Codex
  `0.163.0-alpha.3` is updated to `0.163.0`; anything else is named in a
  warning and not compared.
- An older copy gets one `update-codex` or `update-claude-code` effect, its
  description naming both versions, the installer and, in latest mode, the
  channel, so the plan digest binds the target. The `45-agent-updates`
  script, which runs on every apply on macOS, Linux and WSL, runs exactly the
  approved updates:
  - npm: `npm install -g PACKAGE@VERSION` with that Node's `npm`, the upgrade
    command both vendors document;
  - Codex standalone: Codex's own update command, its `install.sh` downloaded
    into the apply's private temporary folder and then run with
    `CODEX_NON_INTERACTIVE=1`, never piped into `sh`, so a download cut short
    runs nothing. Pinned mode passes `--release VERSION`. Latest mode passes
    `--release latest`, as `codex update` does, so the standalone package's
    `auto-update-version` marker, which its background updater follows, is
    written again; the script first checks that the latest channel still
    offers the planned version. In both modes the standalone `current` link,
    which the installer points at the release it selects, must then name
    `VERSION-TARGET`, which holds for both of the installer's layouts
    (`package` and the older `legacy-platform-npm`, which has no
    `codex-package.json`), since the installer can end without installing
    (for example while Codex's own updater runs); otherwise the step is
    blocked. A release published between the channel check and the install
    is installed instead and may remain installed, with the step blocked;
  - Claude Code native: `claude install VERSION`, the native installer's own
    command, run from VERSION's own binary (below). Installing a version
    leaves `autoUpdatesChannel` as it is; only `claude install stable` or
    `claude install latest` saves a channel.

  Small downloads use Codex's installer's limits (10 seconds to connect, 30
  for metadata). Claude Code's `install.sh` sets none; its binary of about
  250 MB gets the 10-second connect limit and is abandoned only when it
  stalls below 1 KB/s for 60 seconds, as a total limit would turn a slow
  connection into a failure.
  A failed update is blocked on its own effect; the others still run.
- Before a native Claude Code update, the apply checks Anthropic's release
  manifest for that version the way Anthropic's setup page documents: it
  fetches the release signing key from
  `downloads.claude.ai/keys/claude-code.asc` and uses it only when its
  fingerprint is `31DD DE24 DDFA B679 F42D 7BD2 BAA9 29FF 1A7E CACE`, verifies
  the detached signature `manifest.json.sig` over `manifest.json` with the
  maintained ProtonMail OpenPGP library (`golang.org/x/crypto/openpgp` is
  deprecated and unmaintained, and names that fork as its replacement), and
  passes the manifest's `platforms.<platform>.checksum` values to the script.
  A manifest that cannot be fetched or verified leaves Claude Code as it is
  and blocks the step. The key is fetched and pinned by its full fingerprint
  rather than kept in Workbench: that is the procedure Anthropic documents for
  the manifest and for its apt repository, a full fingerprint pin rejects any
  other key, and a rotated key fails the pin either way.
- The script then does what Anthropic's `install.sh` does, verifying before
  anything runs: it names the host platform as `install.sh` does (`darwin` or
  `linux`, `x64` or `arm64`, `arm64` for an x64 shell under Rosetta, `-musl`
  where musl's loader is present or `ldd` names it), accepts only that
  platform's checksum, downloads `VERSION/PLATFORM/claude` into the apply's
  private temporary folder and runs its `install VERSION` only when the file
  matches; a mismatch is removed without being run, and nothing is linked.
  `install.sh` instead runs the newest binary's `install VERSION`, which then
  downloads VERSION; running VERSION's own binary means only the version the
  plan names runs. `claude install VERSION` downloads VERSION again itself
  even when it runs from VERSION's binary (an install without network fails
  with `getaddrinfo EAI_AGAIN downloads.claude.ai`), and Claude Code offers
  no way to install a local file, so the script also checks the binary it
  installed in `~/.local/share/claude/versions`, whatever `claude install`
  exited with: one that does not match, or the empty file a failed download
  leaves, is removed, a launcher left pointing at nothing points at the
  previous version again, and the step is blocked. Between `claude install`
  linking the launcher and that check, a `claude` that starts runs the
  unchecked binary. Signatures exist from 2.1.89, below any target Workbench
  plans.
- Under a `minimumVersion` above the target, in the user or managed settings,
  `claude install VERSION` reports success and installs nothing, so such an
  update would be proposed and blocked on every apply. The plan names the
  floor in a warning and plans no native Claude Code update instead.
- In pinned mode the Claude Code settings merge raises `minimumVersion`, the
  floor Claude Code documents for this case (its background updates and
  `claude update` refuse anything below it, so a `stable` channel older than
  the pin cannot take back Workbench's update and the next plan does not put
  it forward again), to the pin, or to the version the shell's Claude Code is
  at while that is older. The plan passes it to the settings template as the
  `workbench_claude_floor` answer, from the installs it read before
  rendering, so the preview and the apply render the same floor and the plan
  digest binds it. The floor reaches the pin at the first apply after the
  update has run, and never stops Claude Code's own updates while the update
  is unticked or blocked. A higher floor already set stays; without a copy
  whose version can be read (none, Homebrew, another installer) or in latest
  mode no floor is set.
- On macOS a Homebrew copy joins the Homebrew updates and is upgraded with
  `brew upgrade --cask`, at Homebrew's current version, since a cask installs
  no other. Homebrew is not run on Linux, so a Homebrew copy there is named
  in a warning, like any copy installed some other way, and left alone.
- An isolated `--destination` provisions nothing, so its plan looks up no
  agent or Homebrew updates.
- The command-line tools step installs Codex and Claude Code through npm only
  when the command is missing, on every platform.

## Consequences

- Pinned mode installs the exact Codex release, which removes the standalone
  package's `auto-update-version` marker, so Codex stops updating itself in
  the background and stays at the pin. `codex update` turns background updates
  back on: it installs the newest release and writes the marker again, as does
  Workbench's next Codex update in latest mode.
- Latest mode updates Codex as `codex update` would, so its background updates
  stay on.
- Claude Code keeps updating itself in the background on its own channel
  unless its settings turn that off; in pinned mode it can run ahead of the
  pin, which the plan then leaves alone, but not below it once the update has
  run.
- A floor set in pinned mode stays after a switch to latest mode. The merge
  cannot tell Workbench's floor from one the user or Claude Code wrote (Claude
  Code writes the same key when `/config` switches to stable and keeps the
  current version, and clears it when it switches back to latest), so
  removing it could remove the user's own. It is at most a version that was
  installed, so it holds back no update on the latest channel.
- Both installers check a downloaded binary's SHA-256 against a manifest
  fetched over HTTPS from the same host: Codex's against
  `codex-package_SHA256SUMS` and its release metadata, Claude Code's against
  `manifest.json`. Neither checks a signature. Workbench adds the GPG check
  for Claude Code above. Codex publishes Sigstore bundles for its Linux
  binaries from its release workflow, but OpenAI documents no verification
  procedure or signing identity for them, so Workbench relies on the
  installer's SHA-256 check there. Codex's `install.sh` itself
  is fetched at apply time over HTTPS and is not bound to the plan digest; the
  plan binds the version, which the script checks afterwards.
- In latest mode, every plan looks the versions up again: a release published
  between `apply --dry-run` and `apply --approve-plan`, or between the plan an
  interactive apply shows and its recheck after approval, or a lookup that
  fails at the recheck, changes the plan, and the approval is refused with
  exit 4; running the apply again plans the new release.

## Sources

- [Claude Code setup: install, auto-updates, release channels, `minimumVersion`, npm, Homebrew, binary integrity](https://code.claude.com/docs/en/setup)
- [Claude Code settings reference: `autoUpdatesChannel`, `minimumVersion`](https://code.claude.com/docs/en/settings-reference)
- [Claude Code managed settings: file locations and `managed-settings.d`](https://code.claude.com/docs/en/managed-settings)
- [Claude Code native installer script: platform names, musl and Rosetta detection, verify before run](https://claude.ai/install.sh)
- [Claude Code environment variables: `CLAUDE_CONFIG_DIR`](https://code.claude.com/docs/en/env-vars)
- [curl: `--connect-timeout`, `--max-time`, `--speed-limit`, `--speed-time`](https://curl.se/docs/manpage.html)
- [Codex README: installation](https://github.com/openai/codex/blob/main/README.md)
- [Codex standalone installer: `--release`, `CODEX_NON_INTERACTIVE`, `auto-update-version`, SHA-256 check](https://github.com/openai/codex/blob/main/scripts/install/install.sh)
- [Codex update commands per install method (`codex update`, update prompt)](https://github.com/openai/codex/blob/main/codex-rs/tui/src/update_action.rs)
- [Codex app-server daemon: background updates follow `auto-update-version`](https://github.com/openai/codex/blob/main/codex-rs/app-server-daemon/src/managed_install.rs)
- [Codex Linux code signing with cosign](https://github.com/openai/codex/blob/main/.github/actions/linux-code-sign/action.yml)
- [Codex install-method detection](https://github.com/openai/codex/blob/main/codex-rs/install-context/src/lib.rs)
- [npm registry: dist-tags](https://docs.npmjs.com/cli/commands/npm-dist-tag)
- [nvm: aliases and `lts/*`](https://github.com/nvm-sh/nvm#long-term-support)
- [Semantic Versioning 2.0.0, precedence (item 11)](https://semver.org/#spec-item-11)
- [`golang.org/x/crypto/openpgp`: deprecated](https://pkg.go.dev/golang.org/x/crypto/openpgp)
- [ProtonMail go-crypto: maintained OpenPGP fork](https://github.com/ProtonMail/go-crypto)
- [Homebrew manual: `brew upgrade`, `brew outdated`](https://docs.brew.sh/Manpage)
