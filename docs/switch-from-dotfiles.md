# Switch a machine from dotfiles

Workbench manages the same files the dotfiles repository did. Switch each
machine once; keeping both active makes plain `chezmoi` and Workbench fight
over the same files.

1. **Install Workbench without configuring anything yet.**

   ```sh
   curl -fsSL https://github.com/Sawmonabo/workbench/releases/latest/download/install.sh | sh -s -- --install-only
   ```

   From a checkout instead, build with `go build -o bin/workbench ./cmd/workbench`
   and add `--source PATH_TO_CHECKOUT` to every `plan` and `apply` below.

2. **Adopt your existing answers.** This copies only the `[data]` table into
   Workbench's private `machine.toml`. `sourceDir`, hooks and any other keys
   are ignored and never run.

   ```sh
   workbench init --answers-from ~/.config/chezmoi/chezmoi.toml
   ```

   This is a one-time adoption. Plan and apply read only `machine.toml`, so
   `--machine-config` is no longer needed.

3. **Apply configuration only, and review it first.**

   ```sh
   workbench plan --config-only
   workbench apply --config-only
   ```

   Every changed file is checkpointed; `workbench revert --list` shows them.
   The first apply sets the managed VS Code keys in
   `home/.chezmoidata/vscode.json` (zoom, confirm prompts, ty/Ruff as the
   Python language tools). Remove any you don't want enforced before applying.

4. **Retire plain chezmoi's dotfiles config** so `chezmoi apply` can no longer
   reapply the dotfiles source:

   ```sh
   mv ~/.config/chezmoi/chezmoi.toml ~/.config/chezmoi/chezmoi.toml.dotfiles-retired
   ```

   Until you do, `workbench doctor` reports a `native-chezmoi` conflict.

5. **Provision when you're ready.**

   ```sh
   workbench plan --effect brew-maintenance
   workbench apply --effect brew-maintenance
   ```

   `brew-maintenance` keeps the Homebrew cleanup dotfiles ran on every apply;
   `workbench plan --help` lists the other optional effects. Workbench keeps
   its own chezmoi state, so the first full apply runs every `run_once_` and
   `run_onchange_` script again; they skip tools that are already installed.
   The apply uses your terminal, so sudo and installers can prompt.

6. **Archive the dotfiles repository** with a README pointer to Workbench, for
   example `gh repo archive Sawmonabo/dotfiles` after pushing the pointer.

Claude Code keeps the model and effort you choose in a session: Workbench fills
only missing defaults in `~/.claude/settings.json` and enforces its policy keys
(see [configuration ownership](chezmoi-local-overrides.md)).
