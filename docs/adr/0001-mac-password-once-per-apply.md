# 1. Ask for the Mac password once per apply

- Status: Accepted, October 5, 2026
- Supersedes: the Mac password rule in [AGENTS.md](../../AGENTS.md) ("`/usr/bin/sudo`
  reads it, Workbench never does … an app that needs the password (such as Docker
  Desktop) is asked for it by Homebrew itself") and the matching sentence in
  [the contracts](../superpowers/specs/workbench-contracts.md) (the up-front
  `sudo -v` that "renews the ticket with `sudo -n -v` about every minute").

## Context

An apply on a new Mac installs Homebrew, which needs administrator rights.
Workbench asks for the Mac password once, after approval and before any change,
by running `sudo -v` at the terminal, and renews that sudo approval while the
apply runs.

Homebrew deliberately cancels sudo approval before its first sudo use in each
`brew` command ("Reset sudo timestamp to avoid running unauthorised sudo
commands", `Library/Homebrew/utils/sudo.sh`). The Homebrew issue asking to
document this says that removing it is "not a possibility". So after the first
`brew` command, nothing Workbench approved is left.

Some apps need administrator rights to install or update. Homebrew's Docker
Desktop package links `docker` and its helpers into `/usr/local/bin` and
`/usr/local/cli-plugins`, which only root can write. Docker Desktop itself can
put those commands in `~/.docker/bin` without a password, but its Homebrew
package always uses `/usr/local/bin`.

On a clean macOS 15.7.7 VM, the first apply asked for the password up front.
About 12 minutes later, at the apps step, it stopped at `Password:` again and
waited until someone typed it. A person who walked away after the first prompt
came back to a paused setup.

Homebrew supports a standard way to get the password without a terminal prompt,
`SUDO_ASKPASS`, the helper program sudo runs to ask for the password:

- `bin/brew` keeps `SUDO_ASKPASS` when it clears the environment;
- its `SystemCommand` adds `-A` to every sudo it runs when `SUDO_ASKPASS` is set;
- its installer, `install.sh`, does the same, even with `NONINTERACTIVE=1`.

## How similar tools handle it

- **[Strap](https://github.com/MikeMcQuaid/strap)**, the Mac setup script from
  Homebrew's project lead, reads the password once, writes a temporary
  `SUDO_ASKPASS` script that prints it, exports it for the whole run and deletes
  it when it exits.
- **[Ansible's `homebrew_cask` module](https://docs.ansible.com/ansible/latest/collections/community/general/homebrew_cask_module.html)**,
  which [mac-dev-playbook](https://github.com/geerlingguy/mac-dev-playbook) uses
  with `--ask-become-pass`, takes a `sudo_password` "to be passed to
  `SUDO_ASKPASS`" and writes a temporary script that prints it.
- **[A dotfiles setup that hit the same Homebrew behavior](https://github.com/henrilhos/dotfiles/pull/13)**
  dropped its keep-alive loop on September 30, 2026, and accepted up to three
  password prompts on a new machine.
- **[topgrade](https://github.com/topgrade-rs/topgrade/pull/2159)** proposes
  running `brew` in its own pseudo-terminal, so that Homebrew's reset clears
  only that terminal's approval. As of September 26, 2026, it is not merged.

## Options considered

- **A. Ask once, then answer each later sudo request of the apply through
  `SUDO_ASKPASS`.** Chosen.
- **B. Keep the current behavior:** ask up front to install Homebrew, and
  Homebrew asks again for apps that need it. Nothing holds the password, but the
  setup pauses partway through.
- **C. Replace apps that need administrator rights,** for example Docker Desktop
  with Colima, a Homebrew formula that needs none. This changes which tools the
  machine gets.
- **D. Make `/usr/local/bin` writable by the user.** Rejected: any program could
  then put commands on `PATH`, such as a fake `sudo`.

## Decision

This applies to an apply at a terminal on macOS.

1. **When Workbench asks.** Workbench reads the password itself, without echo,
   and checks it with `/usr/bin/sudo -k -S -v` before remembering it.
   - **Up front,** after approval and before any change, when the plan installs
     Homebrew. As today, an account that is not an administrator, a refusal or
     three wrong passwords end the apply as blocked (exit 3), and ctrl+c ends it
     as interrupted (exit 130). Nothing is changed either way.
   - **Otherwise,** the first time a Homebrew command asks during the apply.
2. **Where it is kept.** The password stays only in Workbench's memory for that
   apply. It is never written to disk, the environment, command arguments,
   logs, the plan or `--json`. It is added to the apply's secret redaction set,
   and Workbench lets go of it when the apply ends.
3. **How Homebrew gets it.** Workbench gives `SUDO_ASKPASS` to the Homebrew
   calls in the macOS scripts, and only to them. It points to a helper created
   for that apply in a new private (0700) folder.
   - The helper holds no password. It asks Workbench over a Unix socket in the
     same folder.
   - Workbench answers only while the apply runs, and only processes that the
     apply started, checked through the asking process's ID and its parents.
   - The folder is removed when the apply ends, however it ends.
4. **A password asked partway through.** When Workbench holds no password yet,
   the helper asks at the terminal itself, because it runs in the terminal's
   foreground process group during the native scripts. It checks the answer
   with sudo and passes only a correct one to Workbench to remember. Three wrong
   answers fail that one step, as sudo's own prompt would.
5. **Unattended runs do not change.** These are runs with no terminal, with
   `--approve-plan`, with `--yes` and no terminal, or from coding agents.
   Nothing is asked and no helper is created. A plan that must install Homebrew
   is refused (exit 3) before any change unless sudo approval is still valid.
6. **Linux and WSL do not change.**
7. **Cleanup.** The minute-by-minute renewal of sudo approval is removed. Sudo
   approval that Workbench's own check created is cancelled with `sudo -k` when
   the apply ends.

## Consequences

- **At most one password per apply.** A new Mac asks once, at the start, and
  then needs no one. A later apply asks only when Homebrew needs administrator
  rights, for example to update Docker Desktop, and then only once.
- **Workbench handles the password.** Before this, only sudo did.
- **Who could get the password.** While an apply runs, any process the apply
  started can get the password from the helper, and through it root. This
  includes everything the native scripts start, such as a package's install
  script. Processes under the same account that the apply did not start cannot.
  - Strap and Ansible expose the password to every process under the account,
    through a temporary file that account can read.
  - Giving `SUDO_ASKPASS` only to the Homebrew calls keeps other steps from
    using the helper by accident. It is not a security boundary.
- **This departs from Homebrew's intent,** which is that each command's sudo use
  is approved on its own. Workbench treats the approved plan as that approval,
  for as long as the apply runs.
- **What else changes.** AGENTS.md, the contracts, `docs/usage.md`,
  `docs/macos.md` and `apply --help`. The safeguard test changes to prove that
  the password never reaches disk, the environment, command arguments or
  output, and that the helper answers nothing once the apply has ended.

## Sources

- [Homebrew `utils/sudo.sh`](https://github.com/Homebrew/brew/blob/main/Library/Homebrew/utils/sudo.sh)
- [Homebrew `bin/brew`](https://github.com/Homebrew/brew/blob/main/bin/brew)
- [Homebrew `system_command.rb`](https://github.com/Homebrew/brew/blob/main/Library/Homebrew/system_command.rb)
- [Homebrew `install.sh`](https://github.com/Homebrew/install/blob/HEAD/install.sh)
- [Homebrew issue #20022, documenting the sudo reset](https://github.com/Homebrew/brew/issues/20022)
- [Homebrew PR #20037, moving the sudo reset](https://github.com/Homebrew/brew/pull/20037)
- [Homebrew's Docker Desktop package](https://github.com/Homebrew/homebrew-cask/blob/main/Casks/d/docker-desktop.rb)
- [Docker Desktop's Mac permission requirements](https://docs.docker.com/desktop/setup/install/mac-permission-requirements/)
- [Strap](https://github.com/MikeMcQuaid/strap)
- [Ansible `homebrew_cask` module](https://docs.ansible.com/ansible/latest/collections/community/general/homebrew_cask_module.html)
- [mac-dev-playbook](https://github.com/geerlingguy/mac-dev-playbook)
- [Dotfiles PR dropping the sudo keep-alive](https://github.com/henrilhos/dotfiles/pull/13)
- [topgrade PR #2159](https://github.com/topgrade-rs/topgrade/pull/2159)
