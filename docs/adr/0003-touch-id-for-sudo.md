# 3. Use Touch ID for sudo, and offer to turn it on

- Status: Accepted, October 5, 2026
- Amends: [ADR 1](0001-mac-password-once-per-apply.md), for Macs where Touch ID
  for sudo is on.

## Context

ADR 1 has Workbench read the Mac password once per apply and hand it to
Homebrew's sudo through a `SUDO_ASKPASS` helper.

macOS can authenticate sudo with Touch ID through Apple's `pam_tid.so` module.
It is off by default. Apple ships `/etc/pam.d/sudo_local.template`, whose one
setting is a commented-out `auth sufficient pam_tid.so` line. `/etc/pam.d/sudo`
reads `/etc/pam.d/sudo_local` first, and a system update keeps that file, unlike
`/etc/pam.d/sudo` itself.

With the module on, sudo tries Touch ID before any password, including one a
`SUDO_ASKPASS` helper would give. If Touch ID cannot be used, sudo falls back to
the password: over SSH, inside tmux, with the lid closed, or on a Mac with no
sensor.

So on a Mac with Touch ID for sudo on, ADR 1 would ask the person to type the
password and then also show Touch ID prompts. And because Homebrew cancels sudo
approval before each command that needs root, every app that needs
administrator rights asks again, by Touch ID.

## How similar tools handle it

- **[A dotfiles setup](https://github.com/ryan953/dotFiles/pull/7)**, on
  September 28, 2026, turns Touch ID for sudo on before `brew bundle`, using
  `sudo_local`. It rejected `SUDO_ASKPASS` because "any process running as you
  can read the password during the install … and on macOS that is also the login
  and keychain password". It notes that with Touch ID, "each cask requiring root
  becomes a fingerprint prompt".
- **[mac9sb/config](https://www.mintlify.com/mac9sb/config/setup/touchid)**
  copies Apple's template to `sudo_local` and uncomments the line in its setup
  script. It skips the step when Touch ID is already on.
- **[macos-touchid-sudo](https://github.com/abd3lraouf-studios/macos-touchid-sudo)**
  and the dotfiles setup above add `pam_reattach` so Touch ID also works inside
  tmux.

## Options considered

- **A. Use Touch ID when it is on.** Chosen. Workbench leaves the password to
  sudo, so the person touches the sensor instead of typing.
- **B. Offer an optional step that turns Touch ID for sudo on.** Chosen, off until
  ticked.
- **C. Keep ADR 1 everywhere.** Rejected: a Mac with Touch ID for sudo on would
  get both a typed password and Touch ID prompts.
- **D. Add `pam_reattach` for tmux.** Rejected for now. Homebrew installs that
  module in a folder the user can write, and sudo would load it as root, so any
  program under the account could replace it and run code as root. Inside tmux,
  sudo asks for the password instead.

## Decision

1. **Touch ID for sudo counts as on** when `/etc/pam.d/sudo_local` or
   `/etc/pam.d/sudo` has an active (uncommented) `auth` line naming
   `pam_tid.so`. Workbench reads both files; they are readable by everyone.
2. **An apply at a terminal on a Mac with Touch ID for sudo on** never reads the
   password and creates no `SUDO_ASKPASS` helper.
   - When the plan installs Homebrew, Workbench runs `/usr/bin/sudo -v` at the
     terminal before any change. sudo asks by Touch ID, or with its own password
     prompt when Touch ID cannot be used. Homebrew's installer then uses that
     approval.
   - Later, Homebrew's own sudo asks by Touch ID each time an app needs
     administrator rights, or with its own password prompt when Touch ID cannot
     be used.
   - The plan line says so: Touch ID, or the password where Touch ID cannot be
     used.
3. **Every other attended Mac apply** follows ADR 1. Unattended runs and Linux do
   not change.
4. **The optional macOS step "Touch ID for sudo"** (`touch-id-sudo`) is off until
   ticked. When ticked:
   - With no `/etc/pam.d/sudo_local`, it creates the file from Apple's
     `/etc/pam.d/sudo_local.template` with the `pam_tid.so` line uncommented,
     owned by root and read-only (as the template is).
   - With a `sudo_local` already there, it uncomments or adds only that line, and
     every other line stays byte for byte.
   - It never edits `/etc/pam.d/sudo`. When Touch ID is already on, it does
     nothing.
   - Its one sudo command gets its password the way any other sudo in the apply
     does (ADR 1 or Touch ID).
   - Its probe reads only. It reports that Touch ID for sudo is on (nothing to
     do), or that the step will turn it on and which file it writes.
   - Revert does not undo it, because it is a system change. The step's undo text
     says how: delete `/etc/pam.d/sudo_local`, or comment out the line again.
5. **ADR 1's helper** is given to this step's one sudo command as well as to
   Homebrew.

## Consequences

- **On a Mac with Touch ID for sudo on,** the person touches the sensor instead
  of typing, and Workbench never sees the password. Each app that needs
  administrator rights asks for its own touch, because Homebrew cancels earlier
  approvals. The person cannot walk away from those steps, as they can after one
  typed password.
- **Where Touch ID cannot be used** (SSH, tmux, lid closed), sudo asks for the
  password itself each time.
- **A new Mac** does not have Touch ID for sudo on, so its first apply asks for
  the password once, as in ADR 1. If the step is ticked, it turns Touch ID on for
  that Mac's later applies, and for every `sudo` in the terminal, not only
  Workbench's.
- **Not testable in a VM.** Virtual Macs have no fingerprint sensor, so a test
  there covers the file the step writes, sudo still working with the password
  through the fallback, and the switch between the two modes. An actual touch
  can only be checked on a real Mac.
- **Open follow-up question:** Touch ID inside tmux, which would need
  `pam_reattach` from a location only root can write.

## Sources

- Apple's `/etc/pam.d/sudo_local.template` and `/etc/pam.d/sudo` (macOS 27)
- [Dotfiles PR: Touch ID for sudo during install](https://github.com/ryan953/dotFiles/pull/7)
- [mac9sb/config: Touch ID setup](https://www.mintlify.com/mac9sb/config/setup/touchid)
- [macos-touchid-sudo](https://github.com/abd3lraouf-studios/macos-touchid-sudo)
- [Getting sudo to use Touch ID on macOS](https://gordonbeeming.com/blog/2026-09-04/getting-sudo-to-use-touch-id-on-macos)
- [Enable Touch ID for sudo on macOS (`sudo_local`)](https://kapadiya.net/blog/macos-touch-id-sudo/)
- [9to5Mac: use Touch ID for sudo](https://9to5mac.com/2025/03/07/stop-typing-your-sudo-password-use-touch-id-instead/)
- [Ghostty discussion: `pam_tid` in some terminals](https://github.com/ghostty-org/ghostty/discussions/9259)
